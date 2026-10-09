// Copyright (C) 2026 yniverz
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// ---------- areas ----------

type AreaCard struct {
	R                    *Rec
	Name, Color, Lead    string
	TasksOpen, TasksAll  int
	Overdue              int
	Cost, Limit          float64
	ShowCost, Over       bool
	Slots, Filled        int
	ShowStaff            bool
	EquipOpen, EquipAll  int
	ShowEquip, ShowTasks bool
}

type AreasView struct {
	Cards   []*AreaCard
	CanEdit bool
	EID     int64
}

func areasExtra(c *C) any {
	v := &AreasView{CanEdit: c.CanEdit("areas"), EID: c.Event.ID}
	var f *Finance
	showCost := c.Event.Has("budget") && c.Level("budget") >= 1
	if showCost {
		f = c.finance(c.Baseline())
	}
	for _, a := range c.Recs("areas") {
		if c.scoped() && !c.Mem.InArea(a.ID) {
			continue
		}
		card := &AreaCard{R: a, Name: a.S("name"), Color: a.S("color"), Lead: c.RefTitle("contacts", a.I("lead")), Limit: a.N("budget")}
		if card.Color == "" {
			card.Color = "#8a8f98"
		}
		if c.Event.Has("tasks") && c.Level("tasks") >= 1 {
			card.ShowTasks = true
			for _, t := range c.Recs("tasks") {
				if t.I("area") != a.ID {
					continue
				}
				card.TasksAll++
				if t.S("status") != "done" {
					card.TasksOpen++
					if d, ok := daysUntil(t.S("due")); ok && d < 0 {
						card.Overdue++
					}
				}
			}
		}
		if c.Event.Has("staff") && c.Level("staff") >= 1 {
			card.ShowStaff = true
			for _, s := range c.Recs("staff") {
				if s.I("area") != a.ID || s.S("status") == "no" {
					continue
				}
				card.Slots++
				if s.I("person") != 0 {
					card.Filled++
				}
			}
		}
		if c.Event.Has("equipment") && c.Level("equipment") >= 1 {
			card.ShowEquip = true
			for _, e := range c.Recs("equipment") {
				if e.I("area") != a.ID {
					continue
				}
				card.EquipAll++
				if e.S("status") != "have" && e.S("status") != "back" {
					card.EquipOpen++
				}
			}
		}
		if f != nil {
			card.ShowCost = true
			for _, l := range f.Lines {
				if l.Kind == "out" && l.AreaID == a.ID {
					card.Cost += l.Plan
				}
			}
			card.Over = card.Limit > 0 && card.Cost > card.Limit
		}
		v.Cards = append(v.Cards, card)
	}
	return v
}

// ---------- shared time helpers ----------

// eventDay maps a timestamp to its "party day": hours before 06:00 belong to the previous day.
func eventDay(t time.Time) time.Time {
	t = t.Add(-6 * time.Hour)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

func slotEnd(r *Rec, startKey, endKey string, def time.Duration) (time.Time, time.Time, bool) {
	s, ok := parseDT(r.S(startKey))
	if !ok {
		return s, s, false
	}
	e, ok2 := parseDT(r.S(endKey))
	if !ok2 || !e.After(s) {
		e = s.Add(def)
	}
	return s, e, true
}

// ---------- line-up timetable ----------

type TTBlock struct {
	ID          int64
	Name, Time  string
	Left, Width float64
	Color       string
	Conflict    bool
	StatusLabel string
}

type TTStage struct {
	Name   string
	Blocks []*TTBlock
}

type TTick struct {
	Left  float64
	Label string
}

type TTDay struct {
	Label  string
	Step   float64
	Ticks  []TTick
	Stages []*TTStage
}

type LineupView struct {
	Days        []*TTDay
	Counts      []CountRow
	Unscheduled int
	EID         int64
	Fees        float64
	ShowFees    bool
}

type CountRow struct {
	Label, Color string
	N            int
}

func lineupExtra(c *C) any {
	f := modByKey["lineup"].Field("status")
	v := &LineupView{EID: c.Event.ID}
	counts := map[string]int{}
	type item struct {
		r    *Rec
		s, e time.Time
	}
	days := map[time.Time][]item{}
	for _, r := range c.Recs("lineup") {
		counts[r.S("status")]++
		if r.S("status") == "no" {
			continue
		}
		s, e, ok := slotEnd(r, "start", "end", time.Hour)
		if !ok {
			v.Unscheduled++
			continue
		}
		d := eventDay(s)
		days[d] = append(days[d], item{r, s, e})
		if c.finVisible(r, "lineup") {
			v.ShowFees = true
			v.Fees += r.N("fee") + r.N("extra")
		}
	}
	for _, o := range f.Opts {
		if counts[o.V] > 0 {
			v.Counts = append(v.Counts, CountRow{o.L, o.Color, counts[o.V]})
		}
	}
	var keys []time.Time
	for d := range days {
		keys = append(keys, d)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].Before(keys[j]) })
	for _, d := range keys {
		items := days[d]
		min, max := items[0].s, items[0].e
		for _, it := range items {
			if it.s.Before(min) {
				min = it.s
			}
			if it.e.After(max) {
				max = it.e
			}
		}
		min = min.Truncate(time.Hour)
		max = max.Truncate(time.Hour).Add(time.Hour)
		span := max.Sub(min).Minutes()
		stepH := 1
		if span/60 > 14 {
			stepH = 2
		}
		day := &TTDay{Label: dayLabel(d.Format("2006-01-02")), Step: float64(stepH) * 60 / span * 100}
		for t := min; !t.After(max); t = t.Add(time.Duration(stepH) * time.Hour) {
			day.Ticks = append(day.Ticks, TTick{t.Sub(min).Minutes() / span * 100, t.Format("15")})
		}
		stages := map[string]*TTStage{}
		var order []string
		sort.Slice(items, func(i, j int) bool { return items[i].s.Before(items[j].s) })
		lastEnd := map[string]time.Time{}
		for _, it := range items {
			name := it.r.S("stage")
			if name == "" {
				name = "Main"
			}
			st := stages[name]
			if st == nil {
				st = &TTStage{Name: name}
				stages[name] = st
				order = append(order, name)
			}
			_, col := optLabel(f.Opts, it.r.S("status"))
			lbl, _ := optLabel(f.Opts, it.r.S("status"))
			b := &TTBlock{
				ID: it.r.ID, Name: it.r.S("artist"), Color: col, StatusLabel: lbl,
				Time:  it.s.Format("15:04") + "–" + it.e.Format("15:04"),
				Left:  it.s.Sub(min).Minutes() / span * 100,
				Width: it.e.Sub(it.s).Minutes() / span * 100,
			}
			if le, ok := lastEnd[name]; ok && it.s.Before(le) {
				b.Conflict = true
			}
			lastEnd[name] = it.e
			st.Blocks = append(st.Blocks, b)
		}
		sort.Strings(order)
		for _, n := range order {
			day.Stages = append(day.Stages, stages[n])
		}
		v.Days = append(v.Days, day)
	}
	return v
}

// ---------- staff overview ----------

type StaffArea struct {
	Name, Color            string
	Slots, Filled, Confirm int
	Hours, Cost            float64
}

type StaffPerson struct {
	Name     string
	Shifts   int
	Hours    float64
	Conflict bool
}

type StaffView struct {
	Areas    []*StaffArea
	People   []*StaffPerson
	Open     int
	Slots    int
	Filled   int
	Cost     float64
	ShowCost bool
}

func staffExtra(c *C) any {
	v := &StaffView{}
	byArea := map[int64]*StaffArea{}
	byPerson := map[int64]*StaffPerson{}
	type iv struct{ s, e time.Time }
	ivs := map[int64][]iv{}
	areaOrder := []int64{}
	for _, r := range c.Recs("staff") {
		if !c.visible(modByKey["staff"], r) {
			continue
		}
		if r.S("status") == "no" {
			continue
		}
		aid := r.I("area")
		a := byArea[aid]
		if a == nil {
			a = &StaffArea{Name: c.RefTitle("areas", aid), Color: "#8a8f98"}
			if a.Name == "" {
				a.Name = "Ohne Bereich"
			}
			for _, ar := range c.Recs("areas") {
				if ar.ID == aid && ar.S("color") != "" {
					a.Color = ar.S("color")
				}
			}
			byArea[aid] = a
			areaOrder = append(areaOrder, aid)
		}
		a.Slots++
		v.Slots++
		h := hoursBetween(r.S("start"), r.S("end"))
		a.Hours += h
		if c.finVisible(r, "staff") {
			a.Cost += staffCost(r)
			v.Cost += staffCost(r)
			v.ShowCost = true
		}
		if pid := r.I("person"); pid != 0 {
			a.Filled++
			v.Filled++
			if r.S("status") == "yes" {
				a.Confirm++
			}
			p := byPerson[pid]
			if p == nil {
				p = &StaffPerson{Name: c.RefTitle("contacts", pid)}
				byPerson[pid] = p
			}
			p.Shifts++
			p.Hours += h
			if s, e, ok := slotEnd(r, "start", "end", 0); ok && e.After(s) {
				for _, o := range ivs[pid] {
					if s.Before(o.e) && o.s.Before(e) {
						p.Conflict = true
					}
				}
				ivs[pid] = append(ivs[pid], iv{s, e})
			}
		} else {
			v.Open++
		}
	}
	for _, id := range areaOrder {
		v.Areas = append(v.Areas, byArea[id])
	}
	sort.Slice(v.Areas, func(i, j int) bool { return v.Areas[i].Name < v.Areas[j].Name })
	for _, p := range byPerson {
		v.People = append(v.People, p)
	}
	sort.Slice(v.People, func(i, j int) bool { return v.People[i].Name < v.People[j].Name })
	return v
}

// ---------- combined schedule ----------

type SchedEntry struct {
	Start, End, Title, Where, Resp, Kind, KindColor string
	When                                            time.Time
	Conflict                                        bool
}

type SchedDay struct {
	Label   string
	Entries []*SchedEntry
}

type TimelineView struct {
	Days []*SchedDay
}

func timelineExtra(c *C) any {
	var all []*SchedEntry
	for _, r := range c.Recs("timeline") {
		if !c.visible(modByKey["timeline"], r) {
			continue
		}
		s, e, ok := slotEnd(r, "start", "end", 0)
		if !ok {
			continue
		}
		en := &SchedEntry{Title: r.S("title"), Where: c.RefTitle("areas", r.I("area")), Resp: c.RefTitle("contacts", r.I("resp")), Kind: "Ablauf", KindColor: "ink", When: s,
			Start: s.Format("15:04")}
		if e.After(s) {
			en.End = e.Format("15:04")
		}
		all = append(all, en)
	}
	if c.Event.Has("lineup") && c.Level("lineup") >= 1 {
		for _, r := range c.Recs("lineup") {
			if r.S("status") == "no" {
				continue
			}
			s, e, ok := slotEnd(r, "start", "end", time.Hour)
			if !ok {
				continue
			}
			all = append(all, &SchedEntry{Title: r.S("artist"), Where: r.S("stage"), Kind: "Line-up", KindColor: "blue", When: s, Start: s.Format("15:04"), End: e.Format("15:04")})
		}
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].When.Before(all[j].When) })
	v := &TimelineView{}
	var cur *SchedDay
	var curKey time.Time
	for _, en := range all {
		k := eventDay(en.When)
		if cur == nil || !k.Equal(curKey) {
			cur = &SchedDay{Label: dayLabel(k.Format("2006-01-02"))}
			curKey = k
			v.Days = append(v.Days, cur)
		}
		cur.Entries = append(cur.Entries, en)
	}
	return v
}

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

func initials(s string) string {
	f := strings.Fields(s)
	if len(f) == 0 {
		return "?"
	}
	r := []rune(f[0])
	out := string(r[0])
	if len(f) > 1 {
		out += string([]rune(f[len(f)-1])[0])
	}
	return strings.ToUpper(out)
}
