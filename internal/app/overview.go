// Copyright (C) 2026 yniverz
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
)

type KPI struct {
	Label, Value, Sub, Link, Tone string
}

type Warn struct {
	Text, Link, Level string // Level: warn, bad
}

type Upcoming struct {
	Date, Title, What, Link string
	Days                    int
	Late                    bool
}

type OverviewView struct {
	KPIs     []KPI
	Warns    []Warn
	Upcoming []Upcoming
	Audit    []AuditRow
	Days     int
	HasDate  bool
	Loc      string
	LocCap   float64
	Areas    []*AreaCard
	Team     []*Member
}

func (c *C) modLink(key string) string {
	m := modByKey[key]
	if m == nil {
		return ""
	}
	return c.modListURL(m)
}

func (c *C) can(key string) bool {
	return c.Event.Has(key) && c.Level(modByKey[key].PermKey()) >= 1
}

func (c *C) handleOverview(w http.ResponseWriter, r *http.Request) {
	e := c.Event
	v := &OverviewView{}
	if d, ok := daysUntil(e.Start); ok {
		v.Days, v.HasDate = d, true
	}
	if e.LocationID != 0 {
		v.Loc = c.RefTitle("locations", e.LocationID)
		v.LocCap = c.locationCapacity()
	}
	g := c.Baseline()
	var fin *Finance
	if (c.can("budget") || c.can("calc")) && !c.scoped() {
		fin = c.finance(g)
	}
	cs := c.calcSettings()
	if c.can("calc") && !c.scoped() {
		sub := fmt.Sprintf("%s, %d Gäste", cs.Scenarios[cs.Baseline].Name, g)
		if v.LocCap > 0 {
			sub += fmt.Sprintf(" von %s Plätzen", fmtNum(v.LocCap))
		}
		v.KPIs = append(v.KPIs, KPI{"Gäste (Planung)", fmtNum(float64(g)), sub, c.modLink("calc"), ""})
	}
	if fin != nil {
		tone := "good"
		if fin.Profit < 0 {
			tone = "bad"
		}
		sub := "Einnahmen " + fmtEUR(fin.Income) + " − Ausgaben " + fmtEUR(fin.Expense)
		v.KPIs = append(v.KPIs, KPI{"Ergebnis (Plan)", fmtEURSigned(fin.Profit), sub, c.modLink("budget"), tone})
		if c.can("calc") {
			be := c.breakEven()
			val, sub := "–", "Mit der aktuellen Planung nicht erreichbar"
			if be == 0 {
				sub = "Noch keine Fixkosten erfasst"
			} else if be > 0 {
				val = fmtNum(float64(be))
				sub = "Gäste, ab denen es sich trägt"
				if g > 0 {
					sub = fmt.Sprintf("%s %% der Planung (%d Gäste)", fmtNum(float64(be)/float64(g)*100), g)
				}
			}
			v.KPIs = append(v.KPIs, KPI{"Break-even", val, sub, c.modLink("calc"), ""})
		}
	}
	if c.can("tasks") {
		open, over := 0, 0
		for _, t := range c.Recs("tasks") {
			if !c.visible(modByKey["tasks"], t) || t.S("status") == "done" {
				continue
			}
			open++
			if d, ok := daysUntil(t.S("due")); ok && d < 0 {
				over++
			}
		}
		k := KPI{"Offene Aufgaben", fmtNum(float64(open)), "", c.modLink("tasks"), ""}
		if over > 0 {
			k.Sub, k.Tone = fmt.Sprintf("%d überfällig", over), "bad"
			v.Warns = append(v.Warns, Warn{fmt.Sprintf("%s überfällig", plural(over, "Aufgabe", "Aufgaben")), c.modLink("tasks"), "bad"})
		}
		v.KPIs = append(v.KPIs, k)
	}
	if c.can("permits") {
		all, ok, soon := 0, 0, 0
		for _, p := range c.Recs("permits") {
			if p.S("status") == "na" {
				continue
			}
			all++
			if p.S("status") == "ok" {
				ok++
				continue
			}
			if p.S("status") == "denied" {
				v.Warns = append(v.Warns, Warn{"Abgelehnt: " + p.S("title"), c.modLink("permits"), "bad"})
				continue
			}
			if d, has := daysUntil(p.S("deadline")); has && d < 0 && (p.S("status") == "todo" || p.S("status") == "prep") {
				v.Warns = append(v.Warns, Warn{"Antragsfrist verstrichen: " + p.S("title"), c.modLink("permits"), "bad"})
			} else if has && d <= 14 && (p.S("status") == "todo" || p.S("status") == "prep") {
				soon++
			}
			if v.HasDate && v.Days <= 30 && v.Days >= 0 && p.S("status") == "todo" {
				v.Warns = append(v.Warns, Warn{fmt.Sprintf("Noch nicht begonnen, Event in %d Tagen: %s", v.Days, p.S("title")), c.modLink("permits"), "warn"})
			}
		}
		tone := ""
		sub := ""
		if soon > 0 {
			sub, tone = fmt.Sprintf("%d Fristen in 14 Tagen", soon), "warn"
		}
		if all > 0 {
			v.KPIs = append(v.KPIs, KPI{"Genehmigungen", fmt.Sprintf("%d / %d", ok, all), sub, c.modLink("permits"), tone})
		}
	}
	if c.can("staff") {
		slots, filled := 0, 0
		for _, s := range c.Recs("staff") {
			if !c.visible(modByKey["staff"], s) || s.S("status") == "no" {
				continue
			}
			slots++
			if s.I("person") != 0 {
				filled++
			}
		}
		if slots > 0 {
			tone := ""
			if filled < slots && v.HasDate && v.Days <= 14 && v.Days >= 0 {
				tone = "warn"
				v.Warns = append(v.Warns, Warn{fmt.Sprintf("%s noch offen, Event in %d Tagen", plural(slots-filled, "Schicht", "Schichten"), v.Days), c.modLink("staff"), "warn"})
			}
			v.KPIs = append(v.KPIs, KPI{"Personal besetzt", fmt.Sprintf("%d / %d", filled, slots), "", c.modLink("staff"), tone})
		}
	}
	if c.can("lineup") {
		total, yes := 0, 0
		for _, l := range c.Recs("lineup") {
			if l.S("status") == "no" {
				continue
			}
			total++
			if l.S("status") == "yes" || l.S("status") == "contract" {
				yes++
			}
		}
		if total > 0 {
			v.KPIs = append(v.KPIs, KPI{"Line-up bestätigt", fmt.Sprintf("%d / %d", yes, total), "", c.modLink("lineup"), ""})
		}
	}
	if c.can("equipment") {
		total, have := 0, 0
		for _, q := range c.Recs("equipment") {
			if !c.visible(modByKey["equipment"], q) {
				continue
			}
			total++
			if q.S("status") == "have" || q.S("status") == "back" {
				have++
			}
		}
		if total > 0 {
			v.KPIs = append(v.KPIs, KPI{"Material besorgt", fmt.Sprintf("%d / %d", have, total), "", c.modLink("equipment"), ""})
		}
	}

	// warnings
	if e.Has("loc_candidates") && e.LocationID == 0 && c.Level("loc_candidates") >= 1 {
		v.Warns = append(v.Warns, Warn{"Noch keine Location festgelegt", c.modLink("loc_candidates"), "warn"})
	}
	if fin != nil {
		if fin.Profit < 0 {
			v.Warns = append(v.Warns, Warn{"Die Planung ist bei " + fmtNum(float64(g)) + " Gästen im Minus (" + fmtEUR(fin.Profit) + ")", c.modLink("calc"), "warn"})
		}
		if v.LocCap > 0 && float64(g) > v.LocCap {
			v.Warns = append(v.Warns, Warn{"Geplante Gästezahl über der Kapazität der Location", c.modLink("calc"), "bad"})
		}
		sum := 0.0
		for _, t := range cs.Tiers {
			sum += t.Share
		}
		if c.can("calc") && sum > 100.01 {
			v.Warns = append(v.Warns, Warn{"Ticket-Anteile ergeben mehr als 100 %", c.modLink("calc"), "warn"})
		}
	}
	if c.can("bar") && !c.scoped() || c.can("bar") {
		b := c.bar(g)
		noRecipe, neg := 0, 0
		for _, p := range b.Prods {
			if len(p.Recipe) == 0 {
				noRecipe++
			} else if p.Margin < 0 {
				neg++
			}
		}
		if neg > 0 {
			v.Warns = append(v.Warns, Warn{plural(neg, "Artikel wird", "Artikel werden") + " unter Kosten verkauft", c.modLink("bar") + "?tab=sell", "warn"})
		}
		if noRecipe > 0 {
			v.Warns = append(v.Warns, Warn{plural(noRecipe, "Verkaufsartikel hat", "Verkaufsartikel haben") + " kein Rezept", c.modLink("bar") + "?tab=sell", "warn"})
		}
	}
	if c.can("lineup") {
		lv := lineupExtra(c).(*LineupView)
		n := 0
		for _, d := range lv.Days {
			for _, s := range d.Stages {
				for _, b := range s.Blocks {
					if b.Conflict {
						n++
					}
				}
			}
		}
		if n > 0 {
			v.Warns = append(v.Warns, Warn{plural(n, "Überschneidung", "Überschneidungen") + " im Line-up", c.modLink("lineup"), "warn"})
		}
	}
	if c.can("staff") {
		for _, p := range staffExtra(c).(*StaffView).People {
			if p.Conflict {
				v.Warns = append(v.Warns, Warn{p.Name + " ist in überschneidenden Schichten eingeteilt", c.modLink("staff"), "warn"})
			}
		}
	}
	sort.SliceStable(v.Warns, func(i, j int) bool { return v.Warns[i].Level == "bad" && v.Warns[j].Level != "bad" })

	// upcoming
	now := time.Now()
	horizon := 21
	add := func(date, title, what, link string) {
		d, ok := daysUntil(date)
		if !ok || d > horizon || d < -30 {
			return
		}
		v.Upcoming = append(v.Upcoming, Upcoming{Date: date, Title: title, What: what, Link: link, Days: d, Late: d < 0})
	}
	if c.can("tasks") {
		for _, t := range c.Recs("tasks") {
			if c.visible(modByKey["tasks"], t) && t.S("status") != "done" {
				add(t.S("due"), t.S("title"), "Aufgabe", c.modLink("tasks"))
			}
		}
	}
	if c.can("permits") {
		for _, p := range c.Recs("permits") {
			if p.S("status") == "todo" || p.S("status") == "prep" {
				add(p.S("deadline"), p.S("title"), "Antrag", c.modLink("permits"))
			}
		}
	}
	if c.can("budget") {
		for _, p := range c.Recs("budget") {
			if p.S("kind") == "exp" && p.S("status") != "paid" && c.visible(modByKey["budget"], p) {
				add(p.S("due"), p.S("title")+" ("+fmtEUR(c.budgetPlan(p))+")", "Zahlung", c.modLink("budget"))
			}
		}
	}
	sort.Slice(v.Upcoming, func(i, j int) bool { return v.Upcoming[i].Date < v.Upcoming[j].Date })
	if len(v.Upcoming) > 12 {
		v.Upcoming = v.Upcoming[:12]
	}
	_ = now

	if ar, ok := areasExtra(c).(*AreasView); ok {
		v.Areas = ar.Cards
	}
	v.Audit = c.A.recentAudit(e.ID, 10)
	v.Team = c.A.members(e.ID)
	c.Page("overview.html", map[string]any{"Title": e.Name, "Nav": c.eventNav("overview"), "V": v, "EStatus": e.Status})
}

func (c *C) sub(e *Event) *C {
	n := &C{A: c.A, W: c.W, R: c.R, Sess: c.Sess, User: c.User, Event: e}
	if !c.User.IsAdmin {
		n.Mem = c.A.member(e.ID, c.User.ID)
	}
	return n
}

// ---------- dashboard ----------

type EventRow struct {
	E          *Event
	Loc        string
	Days       int
	HasDate    bool
	Role       string
	OpenTasks  int
	Overdue    int
	Profit     float64
	ShowProfit bool
	Past       bool
}

type MyTask struct {
	Event, Title, Due, Link string
	Late                    bool
}

type DashDeadline struct {
	Event, Title, Label, Color, Date, Link string
	Days                                   int
	Late                                   bool
}

func (c *C) handleDashboard(w http.ResponseWriter, r *http.Request) {
	var upcoming, past []*EventRow
	var mine []MyTask
	var deadlines []DashDeadline
	members := map[int64]*Member{}
	if !c.User.IsAdmin {
		members = c.A.memberEvents(c.User.ID)
	}
	for _, e := range c.A.allEvents() {
		if !c.User.IsAdmin && members[e.ID] == nil {
			continue
		}
		s := c.sub(e)
		row := &EventRow{E: e}
		if d, ok := daysUntil(e.Start); ok {
			row.Days, row.HasDate = d, true
		}
		if e.LocationID != 0 {
			s.refs = nil
			if l := c.A.rec(e.LocationID); l != nil {
				row.Loc = l.S("name")
			}
		}
		if c.User.IsAdmin {
			row.Role = "Admin"
		} else if m := members[e.ID]; m != nil && m.Role != nil {
			row.Role = m.Role.Name
		}
		if e.Has("tasks") && s.Level("tasks") >= 1 {
			for _, t := range s.Recs("tasks") {
				if t.S("status") == "done" || !s.visible(modByKey["tasks"], t) {
					continue
				}
				row.OpenTasks++
				d, has := daysUntil(t.S("due"))
				if has && d < 0 {
					row.Overdue++
				}
				if c.User.ContactID != 0 && t.I("assignee") == c.User.ContactID {
					mine = append(mine, MyTask{e.Name, t.S("title"), t.S("due"), fmt.Sprintf("/e/%d/m/tasks", e.ID), has && d < 0})
				}
			}
		}
		if e.Has("timeplan") && s.Level("timeplan") >= 1 {
			entries, _ := s.timeEntries()
			for _, en := range entries {
				if en.Days <= 14 && (en.Kind == "permit" || en.Kind == "pay" || en.Kind == "gear" || (en.Kind == "task" && !en.Mine)) {
					deadlines = append(deadlines, DashDeadline{e.Name, en.Title, en.Label, en.Color, en.Raw, fmt.Sprintf("/e/%d/zeitplan", e.ID), en.Days, en.Late})
				}
			}
		}
		if e.Has("calc") && s.Level("calc") >= 1 && !s.scoped() {
			row.ShowProfit = true
			row.Profit = s.finance(s.Baseline()).Profit
		}
		end := e.End
		if end == "" {
			end = e.Start
		}
		if d, ok := daysUntil(end); ok && d < 0 || e.Status == "settled" || e.Status == "cancelled" {
			row.Past = true
			past = append(past, row)
		} else {
			upcoming = append(upcoming, row)
		}
	}
	sort.SliceStable(mine, func(i, j int) bool {
		if mine[i].Due == "" || mine[j].Due == "" {
			return mine[i].Due != ""
		}
		return mine[i].Due < mine[j].Due
	})
	if len(mine) > 12 {
		mine = mine[:12]
	}
	sort.SliceStable(deadlines, func(i, j int) bool { return deadlines[i].Date < deadlines[j].Date })
	if len(deadlines) > 12 {
		deadlines = deadlines[:12]
	}
	sort.SliceStable(past, func(i, j int) bool { return past[i].E.Start > past[j].E.Start })
	c.Page("dashboard.html", map[string]any{
		"Title": "Events", "Upcoming": upcoming, "Past": past, "Mine": mine, "Deadlines": deadlines, "CanCreate": c.User.IsAdmin || c.User.CanCreate,
	})
}

func strOr(s, d string) string {
	if strings.TrimSpace(s) == "" {
		return d
	}
	return s
}
