// Copyright (C) 2026 yniverz
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"
)

// EventDay is one day of a (multi-day) event with its own opening hours.
// To may be earlier than From: the day then ends after midnight.
type EventDay struct {
	Date string `json:"date"`
	From string `json:"from"`
	To   string `json:"to"`
}

// DaySpan is a resolved day with absolute start and end.
type DaySpan struct {
	Start, End time.Time
}

const dtLayout = "2006-01-02T15:04"

var hhmm = regexp.MustCompile(`^([01]\d|2[0-3]):[0-5]\d$`)

func spanOf(d EventDay) (DaySpan, bool) {
	date, err := time.Parse("2006-01-02", d.Date)
	if err != nil || !hhmm.MatchString(d.From) || !hhmm.MatchString(d.To) {
		return DaySpan{}, false
	}
	st, _ := time.Parse(dtLayout, d.Date+"T"+d.From)
	en, _ := time.Parse(dtLayout, d.Date+"T"+d.To)
	_ = date
	if !en.After(st) {
		en = en.Add(24 * time.Hour) // ends after midnight
	}
	return DaySpan{st, en}, true
}

// rollEnd treats an end that lies before the start on the same date as "the next day"
// (typing 20:00 to 01:00 means one o'clock at night). It reports whether it changed the end.
func rollEnd(start, end string) (string, bool) {
	st, ok1 := parseDT(start)
	en, ok2 := parseDT(end)
	if !ok1 || !ok2 || len(end) <= 10 || !en.Before(st) {
		return end, false
	}
	if en.Year() == st.Year() && en.YearDay() == st.YearDay() {
		return en.Add(24 * time.Hour).Format(dtLayout), true
	}
	return end, false
}

// daySpans returns the days of the event: the configured ones, or a single span from start to end.
func (e *Event) daySpans() []DaySpan {
	var days []EventDay
	var out []DaySpan
	if e.getSetting("days", &days) {
		for _, d := range days {
			if sp, ok := spanOf(d); ok {
				out = append(out, sp)
			}
		}
	}
	if len(out) > 0 {
		sort.Slice(out, func(i, j int) bool { return out[i].Start.Before(out[j].Start) })
		return out
	}
	st, ok := parseDT(e.Start)
	if !ok {
		return nil
	}
	en, ok2 := parseDT(e.End)
	if !ok2 || en.Before(st) {
		en = st
	}
	return []DaySpan{{st, en}}
}

func (e *Event) configuredDays() []EventDay {
	var days []EventDay
	_ = e.getSetting("days", &days)
	return days
}

// parseDayRows reads the day rows of the settings form.
func parseDayRows(r *http.Request) ([]EventDay, string) {
	_ = r.ParseForm()
	dates, from, to := r.Form["day_date"], r.Form["day_from"], r.Form["day_to"]
	var out []EventDay
	seen := map[string]bool{}
	for i := range dates {
		if i >= len(from) || i >= len(to) {
			break
		}
		d := EventDay{strings.TrimSpace(dates[i]), strings.TrimSpace(from[i]), strings.TrimSpace(to[i])}
		if d.Date == "" && d.From == "" && d.To == "" {
			continue
		}
		if _, ok := spanOf(d); !ok {
			return nil, "Bitte bei jedem Veranstaltungstag Datum, Beginn und Ende (HH:MM) angeben."
		}
		if seen[d.Date] {
			return nil, "Jedes Datum darf nur einmal vorkommen."
		}
		seen[d.Date] = true
		out = append(out, d)
	}
	if len(out) > 21 {
		return nil, "Es sind höchstens 21 Veranstaltungstage möglich."
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Date < out[j].Date })
	return out, ""
}

// applyDays stores the day rows and derives the event's overall start and end from them.
func (e *Event) applyDays(days []EventDay) {
	if len(days) == 0 {
		delete(e.Settings, "days")
		return
	}
	e.setting("days", days)
	first, _ := spanOf(days[0])
	last, _ := spanOf(days[len(days)-1])
	e.Start, e.End = first.Start.Format(dtLayout), last.End.Format(dtLayout)
}

// dayText formats one span for people, e.g. "Fr, 11.12.2026 von 20:00 bis 01:00 Uhr (Folgetag)".
func dayText(sp DaySpan) string {
	sd := time.Date(sp.Start.Year(), sp.Start.Month(), sp.Start.Day(), 0, 0, 0, 0, time.UTC)
	ed := time.Date(sp.End.Year(), sp.End.Month(), sp.End.Day(), 0, 0, 0, 0, time.UTC)
	diff := int(ed.Sub(sd).Hours() / 24)
	day := fmtDate(sp.Start.Format("2006-01-02"))
	switch {
	case diff == 0:
		return day + " von " + sp.Start.Format("15:04") + " bis " + sp.End.Format("15:04") + " Uhr"
	case diff == 1:
		return day + " von " + sp.Start.Format("15:04") + " bis " + sp.End.Format("15:04") + " Uhr (Folgetag)"
	}
	return "von " + day + " " + sp.Start.Format("15:04") + " Uhr bis " + fmtDate(sp.End.Format("2006-01-02")) + " " + sp.End.Format("15:04") + " Uhr"
}

// daysSummary is a short one-liner for the event header.
func (e *Event) daysSummary() string {
	sp := e.daySpans()
	if len(sp) < 2 || len(e.configuredDays()) < 2 {
		return ""
	}
	var parts []string
	for _, s := range sp {
		parts = append(parts, fmt.Sprintf("%s %s–%s", weekdays[s.Start.Weekday()]+" "+s.Start.Format("02.01."), s.Start.Format("15:04"), s.End.Format("15:04")))
	}
	return strings.Join(parts, " · ")
}

func mustDT(s string) time.Time {
	t, _ := parseDT(s)
	return t
}
