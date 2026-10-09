// Copyright (C) 2026 yniverz
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"fmt"
	"math"
	"net/http"
	"sort"
	"strings"
)

// netAmt converts an amount of record r to net regardless of the event's calculation basis.
func (c *C) netAmt(v float64, r *Rec, sale bool) float64 {
	t := c.tax()
	entry := r.S("entry")
	if entry == "" {
		entry = t.Entry
		if sale {
			entry = "gross"
		}
	}
	rate := t.VAT
	if r.S("vat") != "" {
		rate = r.N("vat")
	}
	return convertAmt(v, entry == "gross", rate, false)
}

// ---------- retro ----------

type RetroNotes struct {
	Well    string `json:"well"`
	Improve string `json:"improve"`
	Next    string `json:"next"`
	Rating  int    `json:"rating"`
}

type RetroRow struct {
	Label            string
	Plan, Fore, Diff float64
	Bad              bool
}

type RetroBar struct {
	Name           string
	Plan, Sold     float64
	Diff           float64
	RevPlan, RevIs float64
}

type OldRetro struct {
	Event, Date string
	Notes       RetroNotes
	SameKind    bool
	URL         string
}

func (c *C) handleRetro(w http.ResponseWriter, r *http.Request) {
	if !c.Event.Has("retro") || c.Level("retro") < 1 {
		c.Error(403, "Kein Zugriff auf die Nachbereitung.")
		return
	}
	var notes RetroNotes
	c.Event.getSetting("retro", &notes)
	cs := c.calcSettings()
	showMoney := c.Level("budget") >= 1 && c.Level("calc") >= 1 && !c.scoped()
	data := map[string]any{"Title": "Nachbereitung", "Nav": c.eventNav("retro"), "Notes": notes, "CanEdit": c.CanEdit("retro"), "ShowMoney": showMoney,
		"Basis": c.basisLabel(), "Ratings": []int{1, 2, 3, 4, 5}}
	guestsPlan := cs.Baseline
	g := cs.Scenarios[guestsPlan].Guests
	actualG := int(cs.ActualGuests)
	data["GuestsPlan"], data["GuestsActual"] = g, actualG
	useG := g
	if actualG > 0 {
		useG = actualG
	}
	if showMoney {
		f := c.finance(useG)
		data["Fin"] = f
		var rows []RetroRow
		add := func(label string, plan, fore float64, goodWhenHigher bool) {
			d := fore - plan
			bad := (goodWhenHigher && d < -0.005) || (!goodWhenHigher && d > 0.005)
			rows = append(rows, RetroRow{label, plan, fore, d, bad})
		}
		add("Einnahmen", f.Income, f.IncomeFore, true)
		add("Ausgaben", f.Expense, f.ExpenseFore, false)
		add("Ergebnis", f.Profit, f.ProfitFore, true)
		data["Rows"] = rows
		type CatDiff struct {
			Name             string
			Plan, Fore, Diff float64
		}
		cats := map[string]*CatDiff{}
		for _, l := range f.Lines {
			if l.Kind != "out" {
				continue
			}
			cd := cats[l.Category]
			if cd == nil {
				cd = &CatDiff{Name: l.Category}
				cats[l.Category] = cd
			}
			cd.Plan += l.Plan
			cd.Fore += l.Fore()
		}
		var cl []*CatDiff
		for _, cd := range cats {
			cd.Diff = cd.Fore - cd.Plan
			if math.Abs(cd.Diff) > 0.005 {
				cl = append(cl, cd)
			}
		}
		sort.Slice(cl, func(i, j int) bool { return math.Abs(cl[i].Diff) > math.Abs(cl[j].Diff) })
		if len(cl) > 8 {
			cl = cl[:8]
		}
		data["Cats"] = cl
		if actualG > 0 && f.Income > 0 {
			data["PerGuest"] = f.ProfitFore / float64(actualG)
		}
	}
	if c.can("bar") {
		b := c.bar(useG)
		var br []RetroBar
		for _, p := range b.Prods {
			if p.HasSold {
				br = append(br, RetroBar{p.R.S("name"), p.Qty, p.Sold, p.Sold - p.Qty, p.Rev, p.SoldRev})
			}
		}
		data["BarRows"] = br
	}
	if c.can("tasks") {
		done, total := 0, 0
		for _, t := range c.Recs("tasks") {
			total++
			if t.S("status") == "done" {
				done++
			}
		}
		data["TasksDone"], data["TasksTotal"] = done, total
	}
	// lessons from earlier events the user may see
	var old []OldRetro
	for _, e := range c.A.allEvents() {
		if e.ID == c.Event.ID {
			continue
		}
		if !c.User.IsAdmin && c.A.member(e.ID, c.User.ID) == nil {
			continue
		}
		var n RetroNotes
		if !e.getSetting("retro", &n) || strings.TrimSpace(n.Well+n.Improve+n.Next) == "" {
			continue
		}
		old = append(old, OldRetro{e.Name, fmtDate(e.Start), n, e.Kind != "" && e.Kind == c.Event.Kind, fmt.Sprintf("/e/%d/retro", e.ID)})
	}
	sort.SliceStable(old, func(i, j int) bool { return old[i].SameKind && !old[j].SameKind })
	if len(old) > 6 {
		old = old[:6]
	}
	data["Old"] = old
	c.Page("retro.html", data)
}

func (c *C) handleRetroSave(w http.ResponseWriter, r *http.Request) {
	if !c.Event.Has("retro") || c.Level("retro") < 2 {
		c.Error(403, "Dafür fehlt dir die Berechtigung.")
		return
	}
	clip := func(s string) string {
		s = strings.TrimSpace(s)
		if rs := []rune(s); len(rs) > 5000 {
			s = string(rs[:5000])
		}
		return s
	}
	n := RetroNotes{Well: clip(r.FormValue("well")), Improve: clip(r.FormValue("improve")), Next: clip(r.FormValue("next")), Rating: int(parseNum(r.FormValue("rating")))}
	if n.Rating < 0 || n.Rating > 5 {
		n.Rating = 0
	}
	c.Event.setting("retro", n)
	_ = c.A.saveEvent(c.Event)
	c.A.logAudit(c.User.ID, c.Event.ID, "retro", 0, "Nachbereitung gespeichert", c.Event.Name)
	c.setFlash("Nachbereitung gespeichert.")
	c.Redirect(fmt.Sprintf("/e/%d/retro", c.Event.ID))
}

// ---------- neighbour letter ----------

type LetterSettings struct {
	Contact string `json:"contact"`
	Phone   string `json:"phone"`
	Until   string `json:"until"`
	Body    string `json:"body"`
}

func (c *C) defaultLetter(s LetterSettings) string {
	e := c.Event
	when := "an einem noch offenen Termin"
	if e.Start != "" {
		when = fmtDate(e.Start)
		if t := fmtHM(e.Start); t != "" {
			when += " ab " + t + " Uhr"
		}
		if e.End != "" {
			if st, ok := parseDT(e.Start); ok {
				if en, ok2 := parseDT(e.End); ok2 && fmtHM(e.End) != "" {
					if en.YearDay() == st.YearDay() && en.Year() == st.Year() {
						when += " bis ca. " + fmtHM(e.End) + " Uhr"
					} else {
						when += " bis " + fmtDT(e.End) + " Uhr"
					}
				}
			}
		}
	}
	place := ""
	if e.LocationID != 0 {
		if l := c.A.rec(e.LocationID); l != nil {
			place = " bei „" + l.S("name") + "“"
		}
	}
	var b strings.Builder
	b.WriteString("Sehr geehrte Nachbarinnen und Nachbarn,\n\n")
	b.WriteString(fmt.Sprintf("am %s findet%s die Veranstaltung „%s“ statt. Der Auf- und Abbau erfolgt davor beziehungsweise danach.\n\n", when, place, e.Name))
	b.WriteString("Es wird Musik gespielt. Die Lautstärke wird laufend kontrolliert, und die Lautsprecher werden so ausgerichtet, dass die Belastung für die Nachbarschaft so gering wie möglich bleibt.")
	if s.Until != "" {
		b.WriteString(fmt.Sprintf(" Die Musik endet spätestens um %s Uhr.", s.Until))
	}
	b.WriteString("\n\n")
	contact := strOr(s.Contact, "[Ansprechperson]")
	phone := strOr(s.Phone, "[Telefonnummer]")
	b.WriteString(fmt.Sprintf("Sollte es zu Störungen kommen oder Sie Fragen haben, erreichen Sie uns während der gesamten Veranstaltung unter: %s, Telefon %s. Wir kümmern uns umgehend darum.\n\n", contact, phone))
	b.WriteString("Vielen Dank für Ihr Verständnis.\n\nIhr Team von „" + e.Name + "“")
	return b.String()
}

func (c *C) handleLetter(w http.ResponseWriter, r *http.Request) {
	if !c.Event.Has("neighbors") || c.Level("neighbors") < 1 {
		c.Error(403, "Kein Zugriff auf die Nachbarschaft.")
		return
	}
	var s LetterSettings
	c.Event.getSetting("letter", &s)
	if s.Body == "" {
		s.Body = c.defaultLetter(s)
	}
	c.Page("letter.html", map[string]any{"Title": "Anwohner-Brief", "Nav": c.eventNav("neighbors"), "S": s, "CanEdit": c.CanEdit("neighbors")})
}

func (c *C) handleLetterSave(w http.ResponseWriter, r *http.Request) {
	if !c.Event.Has("neighbors") || c.Level("neighbors") < 2 {
		c.Error(403, "Dafür fehlt dir die Berechtigung.")
		return
	}
	clip := func(s string, n int) string {
		s = strings.TrimSpace(s)
		if rs := []rune(s); len(rs) > n {
			s = string(rs[:n])
		}
		return s
	}
	s := LetterSettings{Contact: clip(r.FormValue("contact"), 120), Phone: clip(r.FormValue("phone"), 60), Until: clip(r.FormValue("until"), 10), Body: clip(r.FormValue("body"), 6000)}
	if r.FormValue("regenerate") != "" || s.Body == "" {
		s.Body = c.defaultLetter(s)
	}
	c.Event.setting("letter", s)
	_ = c.A.saveEvent(c.Event)
	c.setFlash("Brief gespeichert.")
	c.Redirect(fmt.Sprintf("/e/%d/neighbors/letter", c.Event.ID))
}

// ---------- compare events ----------

type CompareRow struct {
	E                                         *Event
	Date                                      string
	GuestsPlan, GuestsActual                  int
	Income, Expense, Profit                   float64
	Margin, IncomePG, CostPG, BarPG, ProfitPG float64
	Done, HasGuests                           bool
}

type PriceRow struct {
	Name, Pack, Unit string
	Latest           float64
	Min, Max         float64
	Events           int
	LatestEvent      string
	Spread           float64
}

func (c *C) handleCompare(w http.ResponseWriter, r *http.Request) {
	var rows []*CompareRow
	type pr struct {
		name, pack, unit string
		points           []struct {
			at, event string
			price     float64
		}
	}
	prices := map[string]*pr{}
	for _, e := range c.A.allEvents() {
		s := c.sub(e)
		if !c.User.IsAdmin && s.Mem == nil {
			continue
		}
		if s.scoped() {
			continue
		}
		if e.Has("calc") && s.Level("calc") >= 1 && s.Level("budget") >= 1 {
			cs := s.calcSettings()
			g := cs.Scenarios[cs.Baseline].Guests
			use := g
			if cs.ActualGuests > 0 {
				use = int(cs.ActualGuests)
			}
			f := s.finance(use)
			row := &CompareRow{E: e, Date: fmtDate(e.Start), GuestsPlan: g, GuestsActual: int(cs.ActualGuests), Income: f.IncomeFore, Expense: f.ExpenseFore, Profit: f.ProfitFore,
				Done: e.Status == "done" || e.Status == "settled", HasGuests: use > 0}
			if f.IncomeFore > 0 {
				row.Margin = f.ProfitFore / f.IncomeFore * 100
			}
			if use > 0 {
				row.IncomePG, row.CostPG, row.ProfitPG = f.IncomeFore/float64(use), f.ExpenseFore/float64(use), f.ProfitFore/float64(use)
				if f.Bar != nil {
					row.BarPG = f.BarNet / float64(use)
				}
			}
			rows = append(rows, row)
		}
		if e.Has("bar") && s.Level("bar") >= 1 {
			for _, it := range s.Recs("bar_items") {
				key := strings.ToLower(strings.TrimSpace(it.S("name")))
				if key == "" || it.N("price") <= 0 {
					continue
				}
				p := prices[key]
				if p == nil {
					p = &pr{name: it.S("name"), pack: it.S("pack"), unit: it.S("unit")}
					prices[key] = p
				}
				at := e.Start
				if at == "" {
					at = e.Created.Format("2006-01-02")
				}
				p.points = append(p.points, struct {
					at, event string
					price     float64
				}{at, e.Name, s.netAmt(it.N("price"), it, false)})
			}
		}
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].E.Start > rows[j].E.Start })
	var avg CompareRow
	n := 0
	for _, r := range rows {
		if r.Done && r.HasGuests {
			avg.IncomePG += r.IncomePG
			avg.CostPG += r.CostPG
			avg.ProfitPG += r.ProfitPG
			avg.BarPG += r.BarPG
			avg.Margin += r.Margin
			n++
		}
	}
	if n > 0 {
		f := float64(n)
		avg.IncomePG, avg.CostPG, avg.ProfitPG, avg.BarPG, avg.Margin = avg.IncomePG/f, avg.CostPG/f, avg.ProfitPG/f, avg.BarPG/f, avg.Margin/f
	}
	var plist []PriceRow
	for _, p := range prices {
		if len(p.points) < 2 {
			continue
		}
		sort.Slice(p.points, func(i, j int) bool { return p.points[i].at < p.points[j].at })
		row := PriceRow{Name: p.name, Pack: p.pack, Unit: p.unit, Events: len(p.points), Min: math.MaxFloat64}
		for _, pt := range p.points {
			row.Min, row.Max = math.Min(row.Min, pt.price), math.Max(row.Max, pt.price)
		}
		last := p.points[len(p.points)-1]
		row.Latest, row.LatestEvent = last.price, last.event
		if row.Min > 0 {
			row.Spread = (row.Max - row.Min) / row.Min * 100
		}
		plist = append(plist, row)
	}
	sort.Slice(plist, func(i, j int) bool { return plist[i].Spread > plist[j].Spread })
	c.Page("compare.html", map[string]any{"Title": "Event-Vergleich", "Rows": rows, "Avg": avg, "AvgN": n, "Prices": plist})
}
