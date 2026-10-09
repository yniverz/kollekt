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

// TEntry is one dated item (deadline, appointment) of an event.
type TEntry struct {
	Raw     string
	When    time.Time
	AllDay  bool
	Kind    string // task, permit, pay, gear, event
	Label   string
	Color   string
	Title   string
	Area    string
	Who     string
	Status  string
	Module  string
	ID      int64
	Late    bool
	Days    int // relative to today
	TMinus  int // days before the event
	HasT    bool
	Mine    bool
	EditURL string
	ListURL string
	CanEdit bool
}

type UndatedItem struct {
	Title, Kind, URL string
}

func today() time.Time {
	n := time.Now()
	return time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, time.UTC)
}

func (c *C) eventStartDay() (time.Time, bool) {
	t, ok := parseDT(c.Event.Start)
	if !ok {
		return t, false
	}
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC), true
}

// timeEntries collects all open, dated items the current user may see.
func (c *C) timeEntries() ([]*TEntry, []UndatedItem) {
	var out []*TEntry
	var undated []UndatedItem
	e := c.Event
	td := today()
	evDay, hasEv := c.eventStartDay()
	ref := func(mod string, r *Rec) string { return c.RefTitle(modByKey[mod].Fields[0].Ref, 0) }
	_ = ref
	add := func(mod, kind, label, color, title, rawDate string, r *Rec, who, area int64, whoMod string, status string) {
		if rawDate == "" {
			return
		}
		t, ok := parseDT(rawDate)
		if !ok {
			return
		}
		m := modByKey[mod]
		en := &TEntry{Raw: rawDate, When: t, AllDay: len(rawDate) <= 10, Kind: kind, Label: label, Color: color, Title: title,
			Area: c.RefTitle("areas", area), Who: c.RefTitle("contacts", who), Status: status, Module: mod, ID: r.ID,
			CanEdit: c.canEditModule(m), ListURL: c.modListURL(m)}
		en.EditURL = fmt.Sprintf("%s/%d?next=%s", c.modBase(m), r.ID, "/e/"+itoa(e.ID)+"/zeitplan")
		d := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
		en.Days = int(d.Sub(td).Hours() / 24)
		en.Late = en.Days < 0
		if hasEv {
			en.TMinus, en.HasT = int(evDay.Sub(d).Hours()/24), true
		}
		en.Mine = c.User.ContactID != 0 && who == c.User.ContactID
		out = append(out, en)
	}
	statusLabel := func(mod, key, v string) string {
		if f := modByKey[mod].Field(key); f != nil {
			l, _ := optLabel(f.Opts, v)
			return l
		}
		return ""
	}
	if c.can("tasks") {
		m := modByKey["tasks"]
		for _, r := range c.Recs("tasks") {
			if !c.visible(m, r) || r.S("status") == "done" {
				continue
			}
			if r.S("due") == "" {
				undated = append(undated, UndatedItem{r.S("title"), "Aufgabe", c.modListURL(m)})
				continue
			}
			add("tasks", "task", "Aufgabe", "blue", r.S("title"), r.S("due"), r, r.I("assignee"), r.I("area"), "contacts", statusLabel("tasks", "status", r.S("status")))
		}
	}
	if c.can("permits") {
		m := modByKey["permits"]
		for _, r := range c.Recs("permits") {
			if st := r.S("status"); st == "ok" || st == "na" || st == "denied" {
				continue
			}
			if r.S("deadline") == "" {
				undated = append(undated, UndatedItem{r.S("title"), "Genehmigung", c.modListURL(m)})
				continue
			}
			add("permits", "permit", "Antrag", "yellow", r.S("title"), r.S("deadline"), r, r.I("responsible"), 0, "contacts", statusLabel("permits", "status", r.S("status")))
		}
	}
	if c.can("budget") {
		m := modByKey["budget"]
		for _, r := range c.Recs("budget") {
			if !c.visible(m, r) || r.S("kind") != "exp" || r.S("status") == "paid" {
				continue
			}
			t := r.S("title") + " · " + fmtEUR(c.budgetPlan(r))
			if r.S("due") == "" {
				continue // most planned lines have no payment date, do not nag
			}
			add("budget", "pay", "Zahlung", "orange", t, r.S("due"), r, r.I("vendor"), r.I("area"), "contacts", statusLabel("budget", "status", r.S("status")))
		}
	}
	if c.can("equipment") {
		m := modByKey["equipment"]
		for _, r := range c.Recs("equipment") {
			if !c.visible(m, r) || r.S("status") == "back" {
				continue
			}
			if r.S("pickup") != "" && r.S("status") != "have" {
				add("equipment", "gear", "Material", "teal", "Abholen: "+r.S("item"), r.S("pickup"), r, r.I("vendor"), r.I("area"), "contacts", statusLabel("equipment", "status", r.S("status")))
			}
			if r.S("ret") != "" {
				add("equipment", "gear", "Material", "teal", "Zurückgeben: "+r.S("item"), r.S("ret"), r, r.I("vendor"), r.I("area"), "contacts", statusLabel("equipment", "status", r.S("status")))
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].When.Before(out[j].When) })
	return out, undated
}

type TGroup struct {
	Label   string
	Sub     string
	Late    bool
	Current bool
	Entries []*TEntry
	Marker  bool // contains the event itself
}

func weekStart(t time.Time) time.Time {
	d := int(t.Weekday()+6) % 7 // Monday = 0
	return time.Date(t.Year(), t.Month(), t.Day()-d, 0, 0, 0, 0, time.UTC)
}

func weekLabel(ws time.Time) string {
	we := ws.AddDate(0, 0, 6)
	_, kw := ws.ISOWeek()
	return fmt.Sprintf("KW %d · %d. %s – %d. %s", kw, ws.Day(), monthsShort[int(ws.Month())], we.Day(), monthsShort[int(we.Month())])
}

func (c *C) groupEntries(entries []*TEntry) []*TGroup {
	td := today()
	thisWeek := weekStart(td)
	var groups []*TGroup
	var late *TGroup
	idx := map[time.Time]*TGroup{}
	evWeek := time.Time{}
	if ev, ok := c.eventStartDay(); ok {
		evWeek = weekStart(ev)
	}
	for _, en := range entries {
		if en.Late {
			if late == nil {
				late = &TGroup{Label: "Überfällig", Late: true}
			}
			late.Entries = append(late.Entries, en)
			continue
		}
		ws := weekStart(en.When)
		g := idx[ws]
		if g == nil {
			g = &TGroup{Label: weekLabel(ws), Current: ws.Equal(thisWeek), Marker: ws.Equal(evWeek)}
			switch d := int(ws.Sub(thisWeek).Hours() / 24); {
			case d == 0:
				g.Sub = "diese Woche"
			case d == 7:
				g.Sub = "nächste Woche"
			}
			idx[ws] = g
			groups = append(groups, g)
		}
		g.Entries = append(g.Entries, en)
	}
	sort.SliceStable(groups, func(i, j int) bool { return groups[i].Entries[0].When.Before(groups[j].Entries[0].When) })
	if late != nil {
		groups = append([]*TGroup{late}, groups...)
	}
	return groups
}

func (c *C) handleTimeplan(w http.ResponseWriter, r *http.Request) {
	if !c.Event.Has("timeplan") || c.Level("timeplan") < 1 {
		c.Error(403, "Kein Zugriff auf den Zeitplan.")
		return
	}
	entries, undated := c.timeEntries()
	q := r.URL.Query()
	kind, mine := q.Get("type"), q.Get("mine") == "1"
	var lateN, weekN int
	td := today()
	for _, en := range entries {
		if en.Late {
			lateN++
		} else if weekStart(en.When).Equal(weekStart(td)) {
			weekN++
		}
	}
	var shown []*TEntry
	for _, en := range entries {
		if kind != "" && en.Kind != kind {
			continue
		}
		if mine && !en.Mine {
			continue
		}
		shown = append(shown, en)
	}
	groups := c.groupEntries(shown)
	// event marker: insert the event itself into the right group
	evDay, hasEv := c.eventStartDay()
	daysToEvent := 0
	if hasEv {
		daysToEvent = int(evDay.Sub(td).Hours() / 24)
		if kind == "" && !mine {
			marker := &TEntry{Raw: c.Event.Start, When: evDay, AllDay: true, Kind: "event", Label: "Event", Color: "red", Title: c.Event.Name, Days: daysToEvent, HasT: true, ID: 0, CanEdit: false}
			placed := false
			for _, g := range groups {
				if g.Late {
					continue
				}
				if weekStart(g.Entries[0].When).Equal(weekStart(evDay)) {
					g.Entries = append(g.Entries, marker)
					sort.SliceStable(g.Entries, func(i, j int) bool { return g.Entries[i].When.Before(g.Entries[j].When) })
					placed = true
				}
			}
			if !placed && daysToEvent >= 0 {
				g := &TGroup{Label: weekLabel(weekStart(evDay)), Entries: []*TEntry{marker}, Marker: true}
				groups = append(groups, g)
				sort.SliceStable(groups, func(i, j int) bool {
					if groups[i].Late != groups[j].Late {
						return groups[i].Late
					}
					return groups[i].Entries[0].When.Before(groups[j].Entries[0].When)
				})
			}
		}
	}
	data := map[string]any{
		"Title": "Zeitplan", "Nav": c.eventNav("timeplan"), "Groups": groups, "Undated": undated, "Total": len(entries),
		"Late": lateN, "ThisWeek": weekN, "HasEvent": hasEv, "DaysToEvent": daysToEvent, "Kind": kind, "Mine": mine,
		"Kinds":    []Opt{{"", "Alles", ""}, {"task", "Aufgaben", ""}, {"permit", "Anträge", ""}, {"pay", "Zahlungen", ""}, {"gear", "Material", ""}},
		"ICalLink": "", "HasLink": c.A.hasCalToken(c.Event.ID, c.User.ID),
	}
	if v, ok := r.Context().Value(newLinkKey{}).(string); ok {
		data["ICalLink"] = v
	}
	c.Page("zeitplan.html", data)
}

type newLinkKey struct{}

func (c *C) calBase() string {
	scheme := "http"
	if isHTTPS(c.R) {
		scheme = "https"
	}
	host := c.R.Host
	if trustProxy {
		if h := c.R.Header.Get("X-Forwarded-Host"); h != "" {
			host = h
		}
	}
	return scheme + "://" + host
}

// migrateModules switches newly introduced default modules on for events created before them.
func (a *App) migrateModules() {
	for _, mod := range []string{"timeplan", "sitemap"} {
		for _, e := range a.allEvents() {
			flag := "mig_" + mod
			var done bool
			if e.getSetting(flag, &done) && done {
				continue
			}
			if !e.Has(mod) {
				e.Modules = append(e.Modules, mod)
			}
			e.setting(flag, true)
			_ = a.saveEvent(e)
		}
	}
}

var _ = strings.TrimSpace
