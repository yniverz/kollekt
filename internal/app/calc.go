// Copyright (C) 2026 yniverz
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"math"
	"net/http"
	"sort"
	"strings"
)

type Scenario struct {
	Name   string `json:"name"`
	Guests int    `json:"guests"`
}

type Tier struct {
	Name  string  `json:"name"`
	Price float64 `json:"price"`
	Share float64 `json:"share"`
}

type CalcSettings struct {
	Scenarios     []Scenario `json:"scenarios"`
	Tiers         []Tier     `json:"tiers"`
	Baseline      int        `json:"baseline"`
	VAT           float64    `json:"vat"`
	BarSpend      float64    `json:"bar_spend"`
	ActualGuests  float64    `json:"actual_guests"`
	ActualTickets string     `json:"actual_tickets"`
}

type TaxSettings struct {
	Basis string  `json:"basis"` // net | gross: what all results are shown in
	Entry string  `json:"entry"` // net | gross: how costs are entered by default
	VAT   float64 `json:"vat"`   // default VAT rate in percent
}

func (c *C) tax() TaxSettings {
	t := TaxSettings{Basis: "net", Entry: "net", VAT: 19}
	var s TaxSettings
	if c.Event.getSetting("tax", &s) {
		if s.Basis == "net" || s.Basis == "gross" {
			t.Basis = s.Basis
		}
		if s.Entry == "net" || s.Entry == "gross" {
			t.Entry = s.Entry
		}
		if s.VAT >= 0 && s.VAT <= 100 {
			t.VAT = s.VAT
		}
	}
	return t
}

func (c *C) basisLabel() string {
	if c.tax().Basis == "gross" {
		return "Alle Beträge brutto"
	}
	return "Alle Beträge netto"
}

func convertAmt(v float64, gross bool, rate float64, toGross bool) float64 {
	net := v
	if gross {
		net = v / (1 + rate/100)
	}
	if toGross {
		return net * (1 + rate/100)
	}
	return net
}

// amt converts a cost-style amount of record r into the event's calculation basis.
func (c *C) amt(v float64, r *Rec, sale bool) float64 {
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
	return convertAmt(v, entry == "gross", rate, t.Basis == "gross")
}

// saleAmt converts a price printed on the menu (always gross).
func (c *C) saleAmt(v float64, r *Rec) float64 {
	t := c.tax()
	rate := t.VAT
	if r.S("vat") != "" {
		rate = r.N("vat")
	}
	return convertAmt(v, true, rate, t.Basis == "gross")
}

// ticketFactor turns gross ticket/consumption revenue into the calculation basis.
func (c *C) ticketFactor(s CalcSettings) float64 {
	if c.tax().Basis == "net" && s.VAT > 0 {
		return 1 / (1 + s.VAT/100)
	}
	return 1
}

func (c *C) calcSettings() CalcSettings {
	var s CalcSettings
	if !c.Event.getSetting("calc", &s) || len(s.Scenarios) == 0 {
		s.Scenarios = []Scenario{{"Vorsichtig", 150}, {"Realistisch", 250}, {"Optimistisch", 350}}
		s.Baseline = 1
		s.VAT = 19
	}
	if len(s.Tiers) == 0 {
		s.Tiers = []Tier{{"Eintritt", 10, 100}}
	}
	if s.Baseline < 0 || s.Baseline >= len(s.Scenarios) {
		s.Baseline = 0
	}
	return s
}

func (c *C) Baseline() int {
	s := c.calcSettings()
	return s.Scenarios[s.Baseline].Guests
}

func (s CalcSettings) avgPrice() float64 {
	t := 0.0
	for _, ti := range s.Tiers {
		t += ti.Price * ti.Share / 100
	}
	return t
}

type FinLine struct {
	Source    string // tickets, bar, budget, lineup, staff, equipment, permits, location
	Kind      string // in, out
	Title     string
	Category  string
	AreaID    int64
	Plan      float64
	Actual    float64
	HasActual bool
	Status    string
	Paid      bool
	Auto      bool
	Due       string
	RecID     int64
}

func (l FinLine) Fore() float64 {
	if l.HasActual {
		return l.Actual
	}
	return l.Plan
}

type Finance struct {
	G                                         int
	Lines                                     []FinLine
	TicketNet, BarNet                         float64
	Income, Expense, Profit                   float64
	IncomeFore, ExpenseFore, ProfitFore, Paid float64
	Bar                                       *BarResult
}

func (c *C) budgetPlan(r *Rec) float64 {
	amt := r.N("qty") * r.N("unit_price")
	if r.S("qty") == "" {
		amt = r.N("unit_price")
	}
	if r.S("scale") == "guest" {
		amt *= float64(c.Baseline())
	}
	return c.amt(amt, r, false)
}

func (c *C) chosenCandidate() int64 {
	var id int64
	c.Event.getSetting("loc_cand", &id)
	return id
}

func staffCost(r *Rec) float64 {
	return r.N("flat") + r.N("rate")*hoursBetween(r.S("start"), r.S("end"))
}

func (c *C) finance(G int) *Finance {
	f := &Finance{G: G}
	s := c.calcSettings()
	vatF := c.ticketFactor(s)
	e := c.Event
	// tickets
	ticket := FinLine{Source: "tickets", Kind: "in", Title: "Eintritt", Category: "Eintritt", Auto: true}
	ticket.Plan = float64(G) * s.avgPrice() * vatF
	if strings.TrimSpace(s.ActualTickets) != "" {
		ticket.Actual, ticket.HasActual = parseNum(s.ActualTickets)*vatF, true
	}
	f.TicketNet = ticket.Plan
	f.Lines = append(f.Lines, ticket)

	if e.Has("bar") {
		b := c.bar(G)
		f.Bar = b
		for _, st := range b.stands() {
			l := FinLine{Source: "bar", Kind: "in", Title: "Verkauf " + st.Name, Category: "Verkauf", Auto: true, Plan: st.Revenue * vatF}
			if b.HasSold {
				l.Actual, l.HasActual = st.SoldRevenue*vatF, true
			}
			f.BarNet += l.Plan
			f.Lines = append(f.Lines, l)
		}
		if b.Purchase > 0 {
			f.Lines = append(f.Lines, FinLine{Source: "bar", Kind: "out", Title: "Einkauf Bar & Verkauf", Category: "Getränke/Einkauf", Plan: b.Purchase, Auto: true})
		}
	} else if s.BarSpend > 0 {
		pl := float64(G) * s.BarSpend * vatF
		f.BarNet = pl
		f.Lines = append(f.Lines, FinLine{Source: "bar", Kind: "in", Title: "Verzehr (pauschal)", Category: "Verkauf", Plan: pl, Auto: true})
	}

	if e.Has("budget") {
		for _, r := range c.Recs("budget") {
			kind := "out"
			if r.S("kind") == "inc" {
				kind = "in"
			}
			l := FinLine{Source: "budget", Kind: kind, Title: r.S("title"), Category: r.S("category"), AreaID: r.I("area"),
				Plan: c.amt(c.budgetPlanG(r, G), r, false), Status: r.S("status"), Due: r.S("due"), RecID: r.ID}
			if l.Category == "" {
				l.Category = "Ohne Kategorie"
			}
			if r.S("actual") != "" {
				l.Actual, l.HasActual = c.amt(r.N("actual"), r, false), true
			}
			l.Paid = r.S("status") == "paid"
			f.Lines = append(f.Lines, l)
		}
	}
	add := func(src, title, cat string, area int64, plan float64, paid bool, recID int64) {
		if plan <= 0 {
			return
		}
		l := FinLine{Source: src, Kind: "out", Title: title, Category: cat, AreaID: area, Plan: plan, Auto: true, Paid: paid, RecID: recID}
		if paid {
			l.Actual, l.HasActual = plan, true
		}
		f.Lines = append(f.Lines, l)
	}
	if e.Has("lineup") {
		for _, r := range c.Recs("lineup") {
			if r.S("status") == "no" {
				continue
			}
			add("lineup", r.S("artist"), "Line-up", 0, c.amt(r.N("fee")+r.N("extra"), r, false), r.B("paid"), r.ID)
		}
	}
	if e.Has("staff") {
		for _, r := range c.Recs("staff") {
			if r.S("status") == "no" {
				continue
			}
			t := r.S("position")
			if p := c.RefTitle("contacts", r.I("person")); p != "" {
				t += " – " + p
			}
			add("staff", t, "Personal", r.I("area"), staffCost(r), r.B("paid"), r.ID)
		}
	}
	if e.Has("equipment") {
		for _, r := range c.Recs("equipment") {
			add("equipment", r.S("item"), "Material", r.I("area"), c.amt(r.N("cost"), r, false), r.B("paid"), r.ID)
		}
	}
	if e.Has("logistics") {
		for _, r := range c.Recs("logistics") {
			add("logistics", r.S("title"), "Transport", 0, c.fahrtCost(r), r.B("paid"), r.ID)
		}
	}
	if e.Has("permits") {
		for _, r := range c.Recs("permits") {
			if r.S("status") == "na" || r.S("status") == "denied" {
				continue
			}
			add("permits", r.S("title"), "Genehmigungen & Gebühren", 0, r.N("cost"), r.B("paid"), r.ID)
		}
	}
	if e.Has("loc_candidates") {
		if id := c.chosenCandidate(); id != 0 {
			for _, r := range c.Recs("loc_candidates") {
				if r.ID == id {
					add("location", "Location: "+c.RefTitle("locations", r.I("location")), "Location", 0, c.amt(r.N("cost"), r, false), false, r.ID)
				}
			}
		}
	}
	for _, l := range f.Lines {
		if l.Kind == "in" {
			f.Income += l.Plan
			f.IncomeFore += l.Fore()
		} else {
			f.Expense += l.Plan
			f.ExpenseFore += l.Fore()
			if l.HasActual && l.Paid {
				f.Paid += l.Actual
			}
		}
	}
	f.Profit = f.Income - f.Expense
	f.ProfitFore = f.IncomeFore - f.ExpenseFore
	return f
}

func (c *C) budgetPlanG(r *Rec, G int) float64 {
	amt := r.N("qty") * r.N("unit_price")
	if r.S("qty") == "" {
		amt = r.N("unit_price")
	}
	if r.S("scale") == "guest" {
		amt *= float64(G)
	}
	return amt
}

// breakEven returns the smallest guest count with profit >= 0 (or -1).
func (c *C) breakEven() int {
	profit := func(g int) float64 { return c.finance(g).Profit }
	if profit(0) >= 0 {
		return 0
	}
	const limit = 20000
	step := 50
	prev := 0
	for g := step; g <= limit; g += step {
		if profit(g) >= 0 {
			for x := prev + 1; x <= g; x++ {
				if profit(x) >= 0 {
					return x
				}
			}
		}
		prev = g
	}
	return -1
}

type ScenarioRow struct {
	Name         string
	Guests       int
	Fin          *Finance
	TicketNet    float64
	BarNet       float64
	OtherIn      float64
	Cost         float64
	Profit       float64
	PerGuest     float64
	Margin       float64
	OverCapacity bool
}

type GridCell struct {
	Profit float64
}

type GridRow struct {
	Price float64
	Cells []float64
	Cur   bool
}

func (c *C) locationCapacity() float64 {
	if c.Event.LocationID == 0 {
		return 0
	}
	for _, r := range c.Recs("locations") {
		if r.ID == c.Event.LocationID {
			return r.N("capacity")
		}
	}
	return 0
}

func (c *C) handleCalc(w http.ResponseWriter, r *http.Request) {
	if c.Level("calc") < 1 || !c.Event.Has("calc") {
		c.Error(403, "Kein Zugriff auf die Kalkulation.")
		return
	}
	s := c.calcSettings()
	capacity := c.locationCapacity()
	var rows []ScenarioRow
	for _, sc := range s.Scenarios {
		f := c.finance(sc.Guests)
		row := ScenarioRow{Name: sc.Name, Guests: sc.Guests, Fin: f, TicketNet: f.TicketNet, BarNet: f.BarNet, Cost: f.Expense, Profit: f.Profit}
		row.OtherIn = f.Income - f.TicketNet - f.BarNet
		if sc.Guests > 0 {
			row.PerGuest = f.Profit / float64(sc.Guests)
		}
		if f.Income > 0 {
			row.Margin = f.Profit / f.Income * 100
		}
		row.OverCapacity = capacity > 0 && float64(sc.Guests) > capacity
		rows = append(rows, row)
	}
	be := c.breakEven()
	// price × guests grid
	prices := []float64{0, 5, 8, 10, 12, 15, 18, 20, 25, 30}
	cur := math.Round(s.avgPrice()*100) / 100
	has := false
	for _, p := range prices {
		if p == cur {
			has = true
		}
	}
	if !has {
		prices = append(prices, cur)
		sort.Float64s(prices)
	}
	vatF := c.ticketFactor(s)
	var grid []GridRow
	finCache := map[int]*Finance{}
	for _, p := range prices {
		gr := GridRow{Price: p, Cur: p == cur}
		for _, sc := range s.Scenarios {
			f := finCache[sc.Guests]
			if f == nil {
				f = c.finance(sc.Guests)
				finCache[sc.Guests] = f
			}
			gr.Cells = append(gr.Cells, f.Profit-f.TicketNet+float64(sc.Guests)*p*vatF)
		}
		grid = append(grid, gr)
	}
	shareSum := 0.0
	for _, t := range s.Tiers {
		shareSum += t.Share
	}
	base := rows[s.Baseline]
	// contribution per additional guest around baseline
	g1 := c.finance(base.Guests + 100)
	contrib := (g1.Profit - base.Fin.Profit) / 100
	data := map[string]any{
		"Title": "Kalkulation", "Nav": c.eventNav("calc"), "S": s, "Rows": rows, "BE": be, "Grid": grid,
		"ShareSum": shareSum, "Capacity": capacity, "CanEdit": c.CanEdit("calc"), "Base": base, "Contrib": contrib,
		"AvgPrice": s.avgPrice(), "Basis": c.basisLabel(), "NetBasis": c.tax().Basis == "net", "HasBar": c.Event.Has("bar"), "HasBudget": c.Event.Has("budget"),
		"Actual": strings.TrimSpace(s.ActualTickets) != "" || s.ActualGuests > 0,
	}
	c.Page("calc.html", data)
}

func (c *C) handleCalcSave(w http.ResponseWriter, r *http.Request) {
	if !c.CanEdit("calc") || !c.Event.Has("calc") {
		c.Error(403, "Keine Berechtigung.")
		return
	}
	_ = r.ParseForm()
	var s CalcSettings
	names, guests := r.Form["sc_name"], r.Form["sc_guests"]
	for i := range names {
		if i >= len(guests) {
			break
		}
		n := strings.TrimSpace(names[i])
		g := int(parseNum(guests[i]))
		if n == "" && g == 0 {
			continue
		}
		if n == "" {
			n = "Szenario"
		}
		if g < 0 {
			g = 0
		}
		s.Scenarios = append(s.Scenarios, Scenario{n, g})
	}
	tn, tp, ts := r.Form["t_name"], r.Form["t_price"], r.Form["t_share"]
	for i := range tn {
		if i >= len(tp) || i >= len(ts) {
			break
		}
		n := strings.TrimSpace(tn[i])
		if n == "" && strings.TrimSpace(tp[i]) == "" {
			continue
		}
		if n == "" {
			n = "Ticket"
		}
		s.Tiers = append(s.Tiers, Tier{n, math.Max(0, parseNum(tp[i])), math.Max(0, parseNum(ts[i]))})
	}
	s.Baseline = int(parseNum(r.FormValue("baseline")))
	s.VAT = math.Max(0, parseNum(r.FormValue("vat")))
	s.BarSpend = math.Max(0, parseNum(r.FormValue("bar_spend")))
	s.ActualGuests = math.Max(0, parseNum(r.FormValue("actual_guests")))
	if v := strings.TrimSpace(r.FormValue("actual_tickets")); v != "" && validNum(v) {
		s.ActualTickets = numStr(parseNum(v))
	}
	if len(s.Scenarios) == 0 {
		s.Scenarios = []Scenario{{"Realistisch", 200}}
	}
	if len(s.Tiers) == 0 {
		s.Tiers = []Tier{{"Eintritt", 0, 100}}
	}
	if s.Baseline < 0 || s.Baseline >= len(s.Scenarios) {
		s.Baseline = 0
	}
	c.Event.setting("calc", s)
	_ = c.A.saveEvent(c.Event)
	c.A.logAudit(c.User.ID, c.Event.ID, "calc", 0, "geändert", "Kalkulation")
	c.setFlash("Kalkulation gespeichert.")
	c.Redirect("/e/" + itoa(c.Event.ID) + "/calc")
}

// ---------- budget page extras ----------

type CatRow struct {
	Name             string
	Plan, Fore, Paid float64
	Auto             bool
}

type AreaRow struct {
	Name        string
	Color       string
	Plan, Limit float64
	Over        bool
}

type OpenItem struct {
	Title string
	Due   string
	Amt   float64
	Late  bool
	Auto  bool
}

type BudgetView struct {
	Fin      *Finance
	Guests   int
	Cats     []CatRow
	Incomes  []CatRow
	Areas    []AreaRow
	Open     []OpenItem
	AutoRows []FinLine
	Scoped   bool
	Warn     []string
	Basis    string
}

func budgetExtra(c *C) any {
	g := c.Baseline()
	f := c.finance(g)
	bv := &BudgetView{Fin: f, Guests: g, Scoped: c.scoped(), Basis: c.basisLabel()}
	cats := map[string]*CatRow{}
	incs := map[string]*CatRow{}
	areaPlan := map[int64]float64{}
	for _, l := range f.Lines {
		if bv.Scoped && !(l.AreaID != 0 && c.Mem.InArea(l.AreaID)) {
			continue
		}
		target := cats
		if l.Kind == "in" {
			target = incs
		}
		cr := target[l.Category]
		if cr == nil {
			cr = &CatRow{Name: l.Category}
			target[l.Category] = cr
		}
		cr.Plan += l.Plan
		cr.Fore += l.Fore()
		if l.HasActual && (l.Paid || l.Source == "budget") {
			cr.Paid += l.Actual
		}
		if l.Auto {
			cr.Auto = true
		}
		if l.Kind == "out" {
			areaPlan[l.AreaID] += l.Plan
		}
		if l.Auto && l.Source != "tickets" {
			bv.AutoRows = append(bv.AutoRows, l)
		}
		if l.Kind == "out" && !l.Paid && l.Source == "budget" && l.Status != "paid" && l.Due != "" {
			d, _ := daysUntil(l.Due)
			bv.Open = append(bv.Open, OpenItem{l.Title, l.Due, l.Fore(), d < 0, false})
		}
	}
	for _, v := range cats {
		bv.Cats = append(bv.Cats, *v)
	}
	for _, v := range incs {
		bv.Incomes = append(bv.Incomes, *v)
	}
	sort.Slice(bv.Cats, func(i, j int) bool { return bv.Cats[i].Plan > bv.Cats[j].Plan })
	sort.Slice(bv.Incomes, func(i, j int) bool { return bv.Incomes[i].Plan > bv.Incomes[j].Plan })
	sort.Slice(bv.Open, func(i, j int) bool { return bv.Open[i].Due < bv.Open[j].Due })
	for _, a := range c.Recs("areas") {
		if bv.Scoped && !c.Mem.InArea(a.ID) {
			continue
		}
		ar := AreaRow{Name: a.S("name"), Color: a.S("color"), Plan: areaPlan[a.ID], Limit: a.N("budget")}
		ar.Over = ar.Limit > 0 && ar.Plan > ar.Limit
		if ar.Plan > 0 || ar.Limit > 0 {
			bv.Areas = append(bv.Areas, ar)
		}
		if ar.Over {
			bv.Warn = append(bv.Warn, ar.Name+": geplante Kosten über dem Limit")
		}
	}
	return bv
}

func srcLabel(s string) string {
	switch s {
	case "lineup":
		return "Line-up"
	case "staff":
		return "Personal"
	case "equipment":
		return "Material"
	case "permits":
		return "Genehmigungen"
	case "location":
		return "Location"
	case "logistics":
		return "Transport"
	case "bar":
		return "Bar & Verkauf"
	case "tickets":
		return "Eintritt"
	}
	return s
}
