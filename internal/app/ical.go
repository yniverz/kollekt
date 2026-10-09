// Copyright (C) 2026 yniverz
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

func (a *App) hasCalToken(eventID, userID int64) bool {
	var n int
	_ = a.db.QueryRow("SELECT COUNT(*) FROM cal_tokens WHERE event_id=? AND user_id=?", eventID, userID).Scan(&n)
	return n > 0
}

// newCalToken replaces any existing token of this user for the event and returns the new one.
func (a *App) newCalToken(eventID, userID int64) (string, error) {
	tok := randToken(24)
	_, err := a.db.Exec(`INSERT INTO cal_tokens(event_id,user_id,token_hash,created_at) VALUES(?,?,?,?)
		ON CONFLICT(event_id,user_id) DO UPDATE SET token_hash=excluded.token_hash, created_at=excluded.created_at`,
		eventID, userID, hashToken(tok), time.Now().Unix())
	return tok, err
}

func (a *App) handleCalRegenerate(c *C) {
	if !c.Event.Has("timeplan") || c.Level("timeplan") < 1 {
		c.Error(403, "Kein Zugriff.")
		return
	}
	if c.R.FormValue("revoke") != "" {
		_, _ = a.db.Exec("DELETE FROM cal_tokens WHERE event_id=? AND user_id=?", c.Event.ID, c.User.ID)
		c.setFlash("Kalender-Link widerrufen. Bestehende Abos funktionieren nicht mehr.")
		c.Redirect("/e/" + itoa(c.Event.ID) + "/zeitplan")
		return
	}
	tok, err := a.newCalToken(c.Event.ID, c.User.ID)
	if err != nil {
		c.Error(500, "Link konnte nicht erzeugt werden.")
		return
	}
	link := c.calBase() + "/cal/" + tok + ".ics"
	c.R = c.R.WithContext(withNewLink(c.R.Context(), link))
	c.R.Method = http.MethodGet
	c.handleTimeplan(c.W, c.R)
}

// icsHandler serves the read-only calendar feed, authenticated by an unguessable per-user token.
func (a *App) icsHandler(w http.ResponseWriter, r *http.Request) {
	tok := strings.TrimSuffix(r.PathValue("token"), ".ics")
	var eid, uid int64
	if len(tok) < 32 || a.db.QueryRow("SELECT event_id,user_id FROM cal_tokens WHERE token_hash=?", hashToken(tok)).Scan(&eid, &uid) != nil {
		http.NotFound(w, r)
		return
	}
	u, e := a.user(uid), a.event(eid)
	if u == nil || u.Disabled || e == nil {
		http.NotFound(w, r)
		return
	}
	c := &C{A: a, W: w, R: r, User: u, Event: e}
	if !u.IsAdmin {
		c.Mem = a.member(e.ID, u.ID)
		if c.Mem == nil || c.Mem.Role == nil {
			http.NotFound(w, r)
			return
		}
	}
	if !e.Has("timeplan") || c.Level("timeplan") < 1 {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	w.Header().Set("Cache-Control", "private, max-age=900")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write([]byte(c.icsFeed()))
}

func icsEscape(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, ";", "\\;")
	s = strings.ReplaceAll(s, ",", "\\,")
	s = strings.ReplaceAll(s, "\r", "")
	s = strings.ReplaceAll(s, "\n", "\\n")
	return s
}

// icsFold wraps a content line at 75 octets without splitting a UTF-8 character.
func icsFold(line string) string {
	if len(line) <= 75 {
		return line
	}
	var b strings.Builder
	cur := 0
	for _, r := range line {
		n := utf8.RuneLen(r)
		if cur+n > 75 {
			b.WriteString("\r\n ")
			cur = 1
		}
		b.WriteRune(r)
		cur += n
	}
	return b.String()
}

type icsOut struct{ b strings.Builder }

func (o *icsOut) line(s string) { o.b.WriteString(icsFold(s)); o.b.WriteString("\r\n") }

func (o *icsOut) event(uid, summary, desc string, start time.Time, allDay bool, end time.Time, alarms bool) {
	o.line("BEGIN:VEVENT")
	o.line("UID:" + uid + "@kollekt")
	o.line("DTSTAMP:" + time.Now().UTC().Format("20060102T150405Z"))
	if allDay {
		o.line("DTSTART;VALUE=DATE:" + start.Format("20060102"))
		if end.IsZero() || !end.After(start) {
			end = start.AddDate(0, 0, 1)
		}
		o.line("DTEND;VALUE=DATE:" + end.Format("20060102"))
	} else {
		o.line("DTSTART:" + start.Format("20060102T150405"))
		if end.IsZero() || !end.After(start) {
			end = start.Add(time.Hour)
		}
		o.line("DTEND:" + end.Format("20060102T150405"))
	}
	o.line("SUMMARY:" + icsEscape(summary))
	if desc != "" {
		o.line("DESCRIPTION:" + icsEscape(desc))
	}
	if alarms {
		o.line("BEGIN:VALARM")
		o.line("ACTION:DISPLAY")
		o.line("DESCRIPTION:" + icsEscape(summary))
		o.line("TRIGGER:-PT15H")
		o.line("END:VALARM")
	}
	o.line("END:VEVENT")
}

func (c *C) icsFeed() string {
	var o icsOut
	o.line("BEGIN:VCALENDAR")
	o.line("VERSION:2.0")
	o.line("PRODID:-//Kollekt//Zeitplan//DE")
	o.line("CALSCALE:GREGORIAN")
	o.line("METHOD:PUBLISH")
	o.line("X-WR-CALNAME:" + icsEscape("Kollekt: "+c.Event.Name))
	o.line("REFRESH-INTERVAL;VALUE=DURATION:PT1H")
	spans := c.Event.daySpans()
	for i, sp := range spans {
		name := c.Event.Name
		if len(c.Event.configuredDays()) > 1 {
			name = fmt.Sprintf("%s (Tag %d/%d)", c.Event.Name, i+1, len(spans))
		}
		o.event(fmt.Sprintf("event-%d-%d", c.Event.ID, i), name, "", sp.Start, len(c.Event.Start) <= 10, sp.End, false)
	}
	entries, _ := c.timeEntries()
	for _, en := range entries {
		desc := en.Label
		if en.Area != "" {
			desc += " · " + en.Area
		}
		if en.Who != "" {
			desc += " · " + en.Who
		}
		if en.Status != "" {
			desc += " · " + en.Status
		}
		desc += "\n" + c.Event.Name
		o.event(en.Module+"-"+itoa(en.ID)+"-"+en.Raw, en.Label+": "+strings.TrimPrefix(strings.TrimPrefix(en.Name, "Abholen: "), "Zurückgeben: "), desc, en.When, en.AllDay, time.Time{}, en.Kind != "gear")
	}
	if c.can("lineup") {
		for _, r := range c.Recs("lineup") {
			if r.S("status") == "no" {
				continue
			}
			if s, e, ok := slotEnd(r, "start", "end", time.Hour); ok {
				o.event("lineup-"+itoa(r.ID), "Set: "+r.S("artist"), r.S("stage")+"\n"+c.Event.Name, s, false, e, false)
			}
		}
	}
	if c.can("timeline") {
		for _, r := range c.Recs("timeline") {
			if !c.visible(modByKey["timeline"], r) {
				continue
			}
			if s, e, ok := slotEnd(r, "start", "end", 30*time.Minute); ok {
				o.event("timeline-"+itoa(r.ID), r.S("title"), c.Event.Name, s, false, e, false)
			}
		}
	}
	if c.can("staff") && c.User.ContactID != 0 {
		for _, r := range c.Recs("staff") {
			if r.I("person") != c.User.ContactID || r.S("status") == "no" {
				continue
			}
			if s, e, ok := slotEnd(r, "start", "end", 4*time.Hour); ok {
				o.event("staff-"+itoa(r.ID), "Schicht: "+r.S("position"), c.RefTitle("areas", r.I("area"))+"\n"+c.Event.Name, s, false, e, false)
			}
		}
	}
	o.line("END:VCALENDAR")
	return o.b.String()
}

func withNewLink(ctx context.Context, link string) context.Context {
	return context.WithValue(ctx, newLinkKey{}, link)
}
