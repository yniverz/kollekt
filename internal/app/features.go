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

// registerFeatureModules adds the planning modules around power, checklists, transport and neighbours.
func registerFeatureModules() {
	reg(&Module{
		Key: "power", Name: "Strom", Singular: "Stromposten", Icon: "bolt", Default: true,
		Desc:  "Einspeisung, Verteiler und Verbraucher. Kollekt rechnet die Last je Verteiler und Einspeisung zusammen und zeigt Überlast. Grobe Planungshilfe, die Auslegung macht eine Elektrofachkraft.",
		Title: "name", Sort: "kind", AreaField: "area", NoList: true, ExtraTpl: "x_power", Extra: powerExtra, Empty: "Noch kein Stromplan.",
		Fields: []Field{
			F("name", "Bezeichnung", TText).Req().List(),
			F("kind", "Art", TSelect).Options(O("load", "Verbraucher", "gray"), O("dist", "Verteiler", "blue"), O("source", "Einspeisung (Aggregat / Hausanschluss)", "orange")).Def("load"),
			F("parent", "Hängt an", TRef).Of("power").Hint("Von welchem Verteiler oder welcher Einspeisung wird dieser Posten versorgt?"),
			F("area", "Bereich", TRef).Of("areas"),
			F("watts", "Leistung", TNumber).Unit("W").Hint("Verbraucher: Anschlusswert pro Stück. Einspeisung: verfügbare Leistung (alternativ nur die Absicherung angeben). Verteiler: leer lassen."),
			F("qty", "Stückzahl", TNumber).Def("1").Hint("Nur Verbraucher, z. B. 8 Scheinwerfer à 150 W."),
			F("simult", "Gleichzeitigkeit", TPercent).Unit("%").Def("100").Hint("Wie viel läuft gleichzeitig? Ton und Licht 100 %, Kühlung eher 70 %, Küchengeräte je nach Betrieb."),
			F("volt", "Netz", TSelect).Options(O("230", "230 V (1-phasig)", "gray"), O("400", "400 V (Drehstrom)", "gray")).Def("230").Hint("Für die Stromstärke bei Verteilern und Einspeisungen."),
			F("fuse", "Absicherung", TNumber).Unit("A").Hint("Verteiler und Einspeisung: Zuleitung bzw. Vorsicherung in Ampere."),
			F("site", "Standort im Lageplan", TRef).Of("site_items"),
			F("notes", "Notizen", TTextarea).Wide_(),
		},
	})
	reg(&Module{
		Key: "checklists", Name: "Checklisten", Singular: "Punkt", Icon: "list", Default: true,
		Desc:  "Abnahme vor Einlass, Aufbau, Abbau, Packlisten. Abhaken pro Event, als Vorlage wiederverwendbar.",
		Title: "title", Sort: "list", Group: "list", Filters: []string{"list", "area", "status"}, AreaField: "area", ExtraTpl: "x_checklists", Extra: checklistsExtra,
		Empty: "Noch keine Checklisten.",
		Fields: []Field{
			F("list", "Liste", TText).Req().List().Suggest("Vor Einlass", "Aufbau", "Abbau", "Packliste Bar", "Packliste Technik", "Notfall"),
			F("title", "Punkt", TText).Req().List(),
			F("status", "Status", TSelect).Options(O("open", "Offen", "gray"), O("done", "Erledigt", "green")).Def("open").Quick_(),
			F("area", "Bereich", TRef).Of("areas").List(),
			F("assignee", "Zuständig", TRef).Of("contacts").List(),
			F("notes", "Notizen", TTextarea).Wide_(),
		},
	})
	reg(&Module{
		Key: "logistics", Name: "Transport", Singular: "Fahrt", Icon: "truck", Default: true,
		Desc:  "Abholungen, Lieferungen und Anfahrt des Teams. Strecke aus den Pins von Location und Kontakten, Fahrtkosten laufen ins Budget, freie Plätze für Fahrgemeinschaften.",
		Title: "title", Sort: "date", Filters: []string{"kind"}, ExtraTpl: "x_logistics", Extra: logisticsExtra, Empty: "Noch keine Fahrten.",
		Fields: []Field{
			F("title", "Fahrt", TText).Req().List(),
			F("kind", "Art", TSelect).Options(O("pickup", "Abholung / Einkauf", "blue"), O("delivery", "Lieferung", "yellow"), O("team", "Team-Anfahrt", "green")).Def("pickup").List(),
			F("place", "Gegenstelle", TRef).Of("contacts").List().Hint("Kontakt mit Pin auf der Karte (Lieferant, Vermieter, Abholort). Daraus wird die Entfernung zur Location geschätzt."),
			F("km", "Strecke einfach", TNumber).Unit("km").Hint("Leer lassen = Luftlinie × 1,3 zwischen Gegenstelle und Location."),
			F("trips", "Anzahl Fahrten", TNumber).Def("1"),
			F("roundtrip", "Hin- und Rückfahrt", TBool).Def("1"),
			F("rate", "Satz", TMoney).Unit("€/km").Def("0.35").Fin_().Hint("Kostensatz je gefahrenem Kilometer (Sprit, Verschleiß). Mietfahrzeuge: unten die Pauschale eintragen."),
			F("flat", "Pauschale", TMoney).Unit("€").Fin_().Hint("z. B. Miete Transporter."),
			F("paid", "Bezahlt", TBool).Fin_(),
			F("date", "Datum", TDate).List(),
			F("driver", "Fahrer:in", TRef).Of("contacts").List(),
			F("seats", "Freie Plätze", TNumber).Hint("Für Fahrgemeinschaften."),
			F("riders", "Mitfahrende", TText).Hint("Namen, kommagetrennt."),
			F("notes", "Notizen", TTextarea).Wide_(),
		},
		Virt: []VField{
			{Key: "km_total", Label: "Gesamt-km", Type: TNumber, Fn: func(c *C, r *Rec) string { return numStr(math.Round(c.fahrtKm(r))) }},
			{Key: "cost", Label: "Kosten", Type: TMoney, Sum: true, Fn: func(c *C, r *Rec) string {
				if !c.finVisible(r, "logistics") {
					return ""
				}
				return numStr(c.fahrtCost(r))
			}},
		},
	})
	reg(&Module{
		Key: "neighbors", Name: "Nachbarschaft", Singular: "Anwohner", Icon: "home", Default: true,
		Desc:  "Wer wurde informiert? Wer hat sich gemeldet? Dazu ein Anwohner-Brief zum Ausdrucken.",
		Title: "who", Sort: "status", Group: "status", Filters: []string{"status"}, ExtraTpl: "x_neighbors", Extra: neighborsExtra, Empty: "Noch niemand erfasst.",
		Fields: []Field{
			F("who", "Adresse / Anwohner", TText).Req().List().Hint("z. B. „Musterstraße 1–9“ oder „Café am Eck“."),
			F("kind", "Art", TSelect).Options(O("resident", "Anwohner", "gray"), O("business", "Gewerbe", "blue"), O("official", "Stelle / Verein", "yellow")).Def("resident").List(),
			F("status", "Status", TSelect).Options(O("todo", "Noch nicht informiert", "gray"), O("informed", "Informiert", "blue"), O("ok", "Einverstanden", "green"), O("complaint", "Beschwerde / Rückfrage", "red")).Def("todo").Quick_(),
			F("date", "Informiert am", TDate).List(),
			F("by", "Informiert von", TRef).Of("contacts").List(),
			F("notes", "Notizen", TTextarea).Wide_().Hint("Was wurde gesagt, welche Wünsche gibt es?"),
		},
	})
	reg(&Module{
		Key: "retro", Name: "Nachbereitung", Icon: "flag", Default: true, Page: "retro",
		Desc: "Plan gegen Ist, Erkenntnisse für das nächste Mal und Vergleich mit früheren Events.",
	})
}

// extendModules enriches existing modules, sets navigation groups and ordering.
func extendModules() {
	if m := modByKey["equipment"]; m != nil {
		insertAfter(m, "status", F("place", "Standort im Lageplan", TRef).Of("site_items").List())
		insertAfter(m, "place", F("order", "Aufbau-Reihenfolge", TNumber).Hint("1 = zuerst aufbauen. Der Abbau läuft automatisch in umgekehrter Reihenfolge."))
		m.ExtraTpl, m.Extra, m.ExtraPos = "x_gear", gearExtra, "bottom"
	}
	if m := modByKey["siteplans"]; m != nil {
		m.Fields = append(m.Fields,
			F("density", "Personen pro m²", TNumber).Hint("Orientierung für die Besucherfläche (Standard 2). Verbindlich ist, was die Behörde vorgibt."),
			F("escape_w", "Rettungsweg-Breite je 100 Personen", TNumber).Unit("m").Hint("Orientierung (Standard 0,2 m, das entspricht 1,20 m je 600 Personen im Freien). Verbindlich ist der Bescheid."),
		)
	}
	if m := modByKey["site_items"]; m != nil {
		insertAfter(m, "area", F("width_m", "Breite", TNumber).Unit("m").Hint("Für Fluchtwege: nutzbare Breite."))
	}
	order := []struct{ key, group string }{
		{"overview", ""}, {"areas", ""}, {"timeplan", ""},
		{"tasks", "Planung"}, {"permits", "Planung"}, {"loc_candidates", "Planung"}, {"checklists", "Planung"}, {"notes", "Planung"},
		{"budget", "Geld"}, {"calc", "Geld"}, {"bar", "Geld"},
		{"lineup", "Programm & Team"}, {"staff", "Programm & Team"}, {"timeline", "Programm & Team"},
		{"sitemap", "Gelände & Technik"}, {"equipment", "Gelände & Technik"}, {"power", "Gelände & Technik"}, {"logistics", "Gelände & Technik"},
		{"neighbors", "Umfeld"}, {"retro", "Abschluss"},
	}
	for i, o := range order {
		if m := modByKey[o.key]; m != nil {
			m.Order, m.NavGroup = i*10, o.group
		}
	}
}

// ---------- power ----------

type PowerRow struct {
	R         *Rec
	Level     int
	Kind      string
	KindLabel string
	Load      float64
	Cap       float64
	Amps      float64
	Pct       float64
	Tone      string
	Own       float64
	Note      string
	Site      string
	AddURL    string
	Pad       int
	Chained   bool // a load with further posts hanging below it
}

type PowerView struct {
	Rows      []*PowerRow
	Total     float64
	Needed    float64
	KVA       float64
	Warns     []string
	Base      string
	Next      string
	CanEdit   bool
	HasSource bool
	Reserve   float64
	WarnPct   float64
	Cos       float64
}

func phaseFactor(r *Rec) float64 {
	if r.S("volt") == "400" {
		return math.Sqrt(3)
	}
	return 1
}

func volts(r *Rec) float64 {
	if r.S("volt") == "400" {
		return 400
	}
	return 230
}

func (c *C) powerTree() *PowerView {
	lim := c.limits()
	cosPhi := lim.CosPhi
	recs := c.Recs("power")
	byID := map[int64]*Rec{}
	children := map[int64][]*Rec{}
	for _, r := range recs {
		byID[r.ID] = r
	}
	for _, r := range recs {
		p := r.I("parent")
		if p != 0 && p != r.ID && byID[p] != nil {
			children[p] = append(children[p], r)
		}
	}
	// own is what a post draws itself: only loads have a connected load
	own := func(r *Rec) float64 {
		if r.S("kind") != "load" && r.S("kind") != "" {
			return 0
		}
		q := r.N("qty")
		if r.S("qty") == "" {
			q = 1
		}
		s := r.N("simult")
		if r.S("simult") == "" {
			s = 100
		}
		return r.N("watts") * q * s / 100
	}
	// loadOf is the load of a post including everything hanging below it, at any depth
	// (a lamp chained to another lamp counts for the distributor above both).
	var loadOf func(r *Rec, depth int) float64
	loadOf = func(r *Rec, depth int) float64 {
		if depth > 12 {
			return own(r)
		}
		t := own(r)
		for _, ch := range children[r.ID] {
			t += loadOf(ch, depth+1)
		}
		return t
	}
	v := &PowerView{Base: c.modBase(modByKey["power"]), Next: fmt.Sprintf("/e/%d/m/power", c.Event.ID), CanEdit: c.canEditModule(modByKey["power"])}
	kindOpts := modByKey["power"].Field("kind").Opts
	seen := map[int64]bool{}
	var walk func(r *Rec, level int)
	walk = func(r *Rec, level int) {
		if seen[r.ID] || level > 12 {
			return
		}
		seen[r.ID] = true
		row := &PowerRow{R: r, Level: level, Pad: level * 22, Kind: r.S("kind"), Load: loadOf(r, 0), Own: r.N("watts")}
		row.KindLabel, _ = optLabel(kindOpts, r.S("kind"))
		row.Chained = (row.Kind == "load" || row.Kind == "") && row.Load > own(r)+0.001
		row.Site = c.RefTitle("site_items", r.I("site"))
		row.AddURL = fmt.Sprintf("%s/new?parent=%d&area=%d&kind=load&next=%s", v.Base, r.ID, r.I("area"), v.Next)
		if row.Kind == "source" || row.Kind == "dist" {
			fuse := r.N("fuse")
			if row.Kind == "source" && r.N("watts") > 0 {
				row.Cap = r.N("watts")
			} else if fuse > 0 {
				row.Cap = fuse * volts(r) * phaseFactor(r) * cosPhi
			}
			if row.Load > 0 {
				row.Amps = row.Load / (volts(r) * phaseFactor(r) * cosPhi)
			}
			if row.Cap > 0 {
				row.Pct = row.Load / row.Cap * 100
				switch {
				case row.Pct > 100:
					row.Tone, row.Note = "bad", "Überlast"
					v.Warns = append(v.Warns, fmt.Sprintf("%s: %s %% der Leistung, Überlast", r.S("name"), fmtNum(math.Round(row.Pct))))
				case row.Pct >= lim.PowerWarn:
					row.Tone, row.Note = "warn", "knapp"
				default:
					row.Tone = "good"
				}
			} else if row.Load > 0 {
				row.Note = "Leistung oder Absicherung fehlt"
			}
			if row.Kind == "source" {
				v.HasSource = true
			}
		}
		v.Rows = append(v.Rows, row)
		kids := children[r.ID]
		sort.SliceStable(kids, func(i, j int) bool {
			if kids[i].S("kind") != kids[j].S("kind") {
				return kids[i].S("kind") != "load" && kids[j].S("kind") == "load"
			}
			return strings.ToLower(kids[i].S("name")) < strings.ToLower(kids[j].S("name"))
		})
		for _, ch := range kids {
			walk(ch, level+1)
		}
	}
	var roots []*Rec
	for _, r := range recs {
		p := r.I("parent")
		if p == 0 || p == r.ID || byID[p] == nil {
			roots = append(roots, r)
		}
	}
	rank := map[string]int{"source": 0, "dist": 1, "load": 2}
	sort.SliceStable(roots, func(i, j int) bool {
		if rank[roots[i].S("kind")] != rank[roots[j].S("kind")] {
			return rank[roots[i].S("kind")] < rank[roots[j].S("kind")]
		}
		return strings.ToLower(roots[i].S("name")) < strings.ToLower(roots[j].S("name"))
	})
	for _, r := range roots {
		walk(r, 0)
	}
	for _, r := range recs { // members of a cycle are not reachable from a root
		if !seen[r.ID] {
			walk(r, 0)
		}
	}
	for _, r := range recs {
		v.Total += own(r) // every post counted once, wherever it hangs
	}
	v.Needed = v.Total / (1 - lim.PowerReserve/100)
	v.KVA = v.Needed / cosPhi / 1000
	v.Reserve, v.WarnPct, v.Cos = lim.PowerReserve, lim.PowerWarn, cosPhi
	return v
}

func powerExtra(c *C) any { return c.powerTree() }

// ---------- checklists ----------

type CheckList struct {
	Name        string
	Done, Total int
	Pct         float64
}

func checklistsExtra(c *C) any {
	m := modByKey["checklists"]
	idx := map[string]*CheckList{}
	var out []*CheckList
	for _, r := range c.Recs("checklists") {
		if !c.visible(m, r) {
			continue
		}
		l := idx[r.S("list")]
		if l == nil {
			l = &CheckList{Name: r.S("list")}
			idx[l.Name] = l
			out = append(out, l)
		}
		l.Total++
		if r.S("status") == "done" {
			l.Done++
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	for _, l := range out {
		if l.Total > 0 {
			l.Pct = float64(l.Done) / float64(l.Total) * 100
		}
	}
	return map[string]any{"Lists": out, "CanEdit": c.canEditModule(m), "EID": c.Event.ID}
}

func (c *C) handleChecklistReset(w http.ResponseWriter, r *http.Request) {
	m := modByKey["checklists"]
	if !c.Event.Has("checklists") || c.Level("checklists") < 2 {
		c.Error(403, "Dafür fehlt dir die Berechtigung.")
		return
	}
	list := r.FormValue("list")
	n := 0
	for _, rec := range c.Recs("checklists") {
		if !c.visible(m, rec) || (list != "" && rec.S("list") != list) || rec.S("status") != "done" {
			continue
		}
		rec.D["status"] = "open"
		if c.A.saveRec(rec) == nil {
			n++
		}
	}
	c.A.logAudit(c.User.ID, c.Event.ID, "checklists", 0, "zurückgesetzt", strOr(list, "alle Listen"))
	c.setFlash(fmt.Sprintf("%d Punkte zurückgesetzt.", n))
	c.Redirect(fmt.Sprintf("/e/%d/m/checklists", c.Event.ID))
}

// ---------- logistics ----------

func haversineKm(lat1, lng1, lat2, lng2 float64) float64 {
	const R = 6371.0088
	rad := math.Pi / 180
	dLat, dLng := (lat2-lat1)*rad, (lng2-lng1)*rad
	a := math.Sin(dLat/2)*math.Sin(dLat/2) + math.Cos(lat1*rad)*math.Cos(lat2*rad)*math.Sin(dLng/2)*math.Sin(dLng/2)
	return 2 * R * math.Asin(math.Min(1, math.Sqrt(a)))
}

// eventLatLng is the position of the event's chosen location.
func (c *C) eventLatLng() (float64, float64, bool) {
	if c.geoDone {
		return c.geoLat, c.geoLng, c.geoOK
	}
	c.geoDone = true
	if c.Event.LocationID == 0 {
		return 0, 0, false
	}
	if loc := c.A.rec(c.Event.LocationID); loc != nil {
		c.geoLat, c.geoLng, c.geoOK = parseGeo(loc.S("geo"))
	}
	return c.geoLat, c.geoLng, c.geoOK
}

// fahrtOneWay is the one-way distance in km: manual value or beeline × 1.3.
func (c *C) fahrtOneWay(r *Rec) (float64, bool) {
	if r.S("km") != "" {
		return r.N("km"), true
	}
	lat, lng, ok := c.eventLatLng()
	if !ok {
		return 0, false
	}
	for _, ct := range c.Recs("contacts") {
		if ct.ID == r.I("place") {
			if la, ln, ok := parseGeo(ct.S("geo")); ok {
				return haversineKm(lat, lng, la, ln) * 1.3, true
			}
		}
	}
	return 0, false
}

func (c *C) fahrtKm(r *Rec) float64 {
	one, _ := c.fahrtOneWay(r)
	trips := r.N("trips")
	if r.S("trips") == "" {
		trips = 1
	}
	f := 1.0
	if r.B("roundtrip") {
		f = 2
	}
	return one * f * trips
}

func (c *C) fahrtCost(r *Rec) float64 { return c.fahrtKm(r)*r.N("rate") + r.N("flat") }

func logisticsExtra(c *C) any {
	m := modByKey["logistics"]
	var km, cost float64
	var seats int
	unknown := 0
	for _, r := range c.Recs("logistics") {
		if !c.visible(m, r) {
			continue
		}
		km += c.fahrtKm(r)
		if c.finVisible(r, "logistics") {
			cost += c.fahrtCost(r)
		}
		if _, ok := c.fahrtOneWay(r); !ok {
			unknown++
		}
		if r.S("kind") == "team" {
			seats += int(r.N("seats"))
		}
	}
	_, _, hasLoc := c.eventLatLng()
	return map[string]any{"KM": km, "Cost": cost, "Seats": seats, "Unknown": unknown, "ShowCost": c.Level("fin") >= 1 || c.User.IsAdmin, "HasLoc": hasLoc}
}

// ---------- neighbours ----------

func neighborsExtra(c *C) any {
	m := modByKey["neighbors"]
	counts := map[string]int{}
	total := 0
	for _, r := range c.Recs("neighbors") {
		if !c.visible(m, r) {
			continue
		}
		counts[r.S("status")]++
		total++
	}
	return map[string]any{"Total": total, "Todo": counts["todo"], "Informed": counts["informed"] + counts["ok"], "Complaints": counts["complaint"], "EID": c.Event.ID, "CanEdit": c.canEditModule(m)}
}

// ---------- gear: set-up order ----------

type GearRow struct {
	Item   string
	Qty    string
	Area   string
	Place  string
	Order  float64
	Status string
	Color  string
}

func gearExtra(c *C) any {
	m := modByKey["equipment"]
	st := m.Field("status")
	var rows []*GearRow
	unplaced, unordered := 0, 0
	for _, r := range c.Recs("equipment") {
		if !c.visible(m, r) {
			continue
		}
		l, col := optLabel(st.Opts, r.S("status"))
		g := &GearRow{Item: r.S("item"), Qty: r.S("qty"), Area: c.RefTitle("areas", r.I("area")), Place: c.RefTitle("site_items", r.I("place")), Order: r.N("order"), Status: l, Color: col}
		if r.S("order") == "" {
			g.Order = 1e9
			unordered++
		}
		if g.Place == "" {
			unplaced++
		}
		rows = append(rows, g)
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].Order != rows[j].Order {
			return rows[i].Order < rows[j].Order
		}
		return rows[i].Area < rows[j].Area
	})
	rev := make([]*GearRow, len(rows))
	for i, g := range rows {
		rev[len(rows)-1-i] = g
	}
	return map[string]any{"Setup": rows, "Teardown": rev, "Unplaced": unplaced, "Unordered": unordered, "Total": len(rows)}
}

// ---------- helpers used by overview ----------

func (c *C) powerOverloads() []string {
	if !c.can("power") {
		return nil
	}
	return c.powerTree().Warns
}
