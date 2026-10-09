// Copyright (C) 2026 yniverz
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"fmt"
	"log"
	"time"
)

// seedDemo fills an empty installation with entirely fictional sample data.
// Enabled with KOLLEKT_DEMO_DATA=1. Does nothing if events already exist.
func (a *App) seedDemo() {
	if len(a.allEvents()) > 0 || a.userCount() == 0 {
		return
	}
	var admin *User
	for _, u := range a.allUsers() {
		if u.IsAdmin {
			admin = u
			break
		}
	}
	if admin == nil {
		return
	}
	contact := func(name, kind, tags, phone string) int64 {
		d := map[string]string{"name": name, "kind": kind, "tags": tags}
		if phone != "" {
			d["phone"] = phone
			d["email"] = "kontakt@beispiel.invalid"
		}
		r := &Rec{Module: "contacts", D: d, CreatedBy: admin.ID}
		_ = a.saveRec(r)
		return r.ID
	}
	loc := func(d map[string]string) int64 {
		r := &Rec{Module: "locations", D: d, CreatedBy: admin.ID}
		_ = a.saveRec(r)
		return r.ID
	}
	vermieter := contact("Beispiel-Vermieter GmbH (fiktiv)", "company", "Vermieter", "+49 000 0000001")
	mara := contact("Mara Muster (fiktiv)", "person", "Barkeeper, Helfer", "+49 000 0000002")
	jonas := contact("Jonas Probe (fiktiv)", "person", "Techniker", "+49 000 0000003")
	kessel := contact("DJ Kessel (fiktiv)", "person", "DJ", "+49 000 0000004")
	lund := contact("Lund (fiktiv)", "person", "DJ", "")
	pia := contact("Pia B. (fiktiv)", "person", "DJ", "")
	_ = contact("Getränkehandel Demo (fiktiv)", "company", "Lieferant", "+49 000 0000005")
	amt := contact("Ordnungsamt Beispielstadt (fiktiv)", "authority", "Behörde", "")
	hall := loc(map[string]string{"name": "Lagerhalle Süd (fiktiv)", "kind": "indoor", "city": "Beispielstadt", "capacity": "250", "rent": "1200", "owner": itoa(vermieter), "geo": "49.014200,8.389500", "curfew": "Open End bis 6 Uhr"})
	field := loc(map[string]string{"name": "Waldlichtung Beispielhain (fiktiv)", "kind": "outdoor", "city": "Beispielhain", "capacity": "400", "rent": "1800", "geo": "49.021500,8.412300", "curfew": "Musik bis 3 Uhr", "notes": "Strom und Wasser nicht vorhanden. Zufahrt über Feldweg."})
	_ = loc(map[string]string{"name": "Festplatz Demo-Aue (fiktiv)", "kind": "outdoor", "city": "Beispielstadt", "capacity": "1500", "rent": "900", "geo": "49.001000,8.430000"})

	var tpl *Template
	for _, t := range a.templates() {
		if t.Name == "Techno Open Air" {
			tpl = t
		}
	}
	if tpl == nil {
		return
	}
	start := time.Now().AddDate(0, 0, 60)
	for start.Weekday() != time.Saturday {
		start = start.AddDate(0, 0, 1)
	}
	st := time.Date(start.Year(), start.Month(), start.Day(), 20, 0, 0, 0, time.UTC)
	fmtT := func(t time.Time) string { return t.Format("2006-01-02T15:04") }
	e := &Event{Name: "Demo Open Air (fiktiv)", Status: "planning", Kind: tpl.Name, Start: fmtT(st), End: fmtT(st.Add(12 * time.Hour)),
		LocationID: field, Modules: tpl.Payload.Modules, Settings: map[string]jsonRaw{}, CreatedBy: admin.ID,
		Description: "Beispieldaten mit frei erfundenen Personen und Orten zum Ausprobieren. Alle Namen, Nummern und Beträge sind fiktiv."}
	for k, v := range tpl.Payload.Settings {
		e.Settings[k] = v
	}
	if err := a.saveEvent(e); err != nil {
		log.Printf("demo: %v", err)
		return
	}
	a.instantiate(e, tpl.Payload, admin.ID)
	area := map[string]int64{}
	for _, r := range a.recs(e.ID, "areas") {
		area[r.S("name")] = r.ID
	}
	add := func(mod string, d map[string]string) *Rec {
		r := &Rec{EventID: e.ID, Module: mod, D: d, CreatedBy: admin.ID}
		_ = a.saveRec(r)
		return r
	}
	// location requests
	c1 := add("loc_candidates", map[string]string{"location": itoa(field), "status": "yes", "cost": "1800", "asked_on": st.AddDate(0, 0, -50).Format("2006-01-02"), "conditions": "Musik bis 3 Uhr, Müll selbst entsorgen."})
	add("loc_candidates", map[string]string{"location": itoa(hall), "status": "offer", "cost": "1428", "entry": "gross", "vat": "19", "conditions": "Brutto-Angebot, Kaution 500 €."})
	e.setting("loc_cand", c1.ID)
	_ = a.saveEvent(e)
	// budget: mix of net, gross and small (VAT-free) suppliers
	bud := func(title, cat, kind, qty, price, scale, ar string, extra map[string]string) {
		d := map[string]string{"title": title, "category": cat, "kind": kind, "qty": qty, "unit_price": price, "scale": scale, "status": "planned"}
		if ar != "" {
			d["area"] = itoa(area[ar])
		}
		for k, v := range extra {
			d[k] = v
		}
		add("budget", d)
	}
	bud("Sicherheitsdienst", "Security", "exp", "4", "280", "fix", "Security & Sanitäts", nil)
	bud("Soundsystem inkl. Techniker", "Technik", "exp", "1", "1400", "fix", "Technik & Sound", map[string]string{"status": "booked"})
	bud("Flyer-Druck (Kleinbetrieb ohne USt)", "Werbung", "exp", "1", "180", "fix", "Marketing", map[string]string{"entry": "gross", "vat": "0"})
	bud("Toilettenwagen", "Toiletten & Müll", "exp", "2", "238", "fix", "Aufbau & Deko", map[string]string{"entry": "gross", "vat": "19"})
	bud("Armbänder", "Eintritt", "exp", "1", "0.2", "guest", "Einlass & Kasse", nil)
	bud("Sponsoring (Getränkehändler)", "Sponsoring", "inc", "1", "400", "fix", "", nil)
	// lineup
	add("lineup", map[string]string{"artist": "DJ Kessel (fiktiv)", "stage": "Main", "start": fmtT(st.Add(2 * time.Hour)), "end": fmtT(st.Add(4 * time.Hour)), "genre": "Techno", "status": "contract", "fee": "300", "contact": itoa(kessel)})
	add("lineup", map[string]string{"artist": "Lund (fiktiv)", "stage": "Main", "start": fmtT(st.Add(4 * time.Hour)), "end": fmtT(st.Add(6*time.Hour + 30*time.Minute)), "genre": "Acid", "status": "yes", "fee": "450", "contact": itoa(lund)})
	add("lineup", map[string]string{"artist": "Pia B. (fiktiv)", "stage": "Floor 2", "start": fmtT(st.Add(5 * time.Hour)), "end": fmtT(st.Add(9 * time.Hour)), "genre": "House", "status": "asked", "fee": "200", "contact": itoa(pia)})
	// staff
	for i := 0; i < 3; i++ {
		d := map[string]string{"position": "Barkeeper", "area": itoa(area["Bar"]), "start": fmtT(st.Add(time.Hour)), "end": fmtT(st.Add(8 * time.Hour)), "rate": "13", "status": "open"}
		if i == 0 {
			d["person"], d["status"] = itoa(mara), "yes"
		}
		add("staff", d)
	}
	add("staff", map[string]string{"position": "Technik", "area": itoa(area["Technik & Sound"]), "start": fmtT(st.Add(-4 * time.Hour)), "end": fmtT(st.Add(10 * time.Hour)), "flat": "150", "person": itoa(jonas), "status": "yes"})
	// a few concrete task assignments and permit progress
	for _, t := range a.recs(e.ID, "tasks") {
		if t.S("title") == "Soundsystem, Licht und Strom klären" {
			t.D["assignee"], t.D["status"], t.D["due"] = itoa(jonas), "doing", st.AddDate(0, 0, -30).Format("2006-01-02")
			_ = a.saveRec(t)
		}
	}
	for _, p := range a.recs(e.ID, "permits") {
		switch p.S("title") {
		case "Veranstaltung anmelden (Veranstaltungsleitfaden)":
			p.D["status"], p.D["contact"], p.D["deadline"] = "filed", itoa(amt), st.AddDate(0, 0, -42).Format("2006-01-02")
			_ = a.saveRec(p)
		case "Vorübergehender Gaststättenbetrieb (Alkoholausschank)":
			p.D["status"], p.D["deadline"] = "prep", st.AddDate(0, 0, -21).Format("2006-01-02")
			_ = a.saveRec(p)
		}
	}
	// demo site plan on the map around the (fictional) field
	plan := add("siteplans", map[string]string{"name": "Gelände (Karte)", "mode": "map", "geo": "49.021500,8.412300", "zoom": "18"})
	poly := func(lat, lng, dlat, dlng float64) string {
		return fmt.Sprintf(`{"t":"poly","p":[[%f,%f],[%f,%f],[%f,%f],[%f,%f]]}`, lat, lng, lat, lng+dlng, lat+dlat, lng+dlng, lat+dlat, lng)
	}
	pt := func(lat, lng float64) string { return fmt.Sprintf(`{"t":"point","p":[[%f,%f]]}`, lat, lng) }
	item := func(title, kind, ar, geom string) {
		add("site_items", map[string]string{"plan": itoa(plan.ID), "title": title, "kind": kind, "area": itoa(area[ar]), "geom": geom})
	}
	item("Main Floor", "dance", "Technik & Sound", poly(49.02145, 8.41215, 0.00025, 0.00040))
	item("Bühne", "stage", "Technik & Sound", poly(49.02170, 8.41225, 0.00010, 0.00020))
	item("Bar", "bar", "Bar", poly(49.02145, 8.41262, 0.00012, 0.00018))
	item("Einlass", "entry", "Einlass & Kasse", pt(49.02130, 8.41200))
	item("Toiletten", "wc", "Aufbau & Deko", pt(49.02175, 8.41275))
	item("Notausgang Ost", "safety", "Security & Sanitäts", pt(49.02160, 8.41290))
	add("site_items", map[string]string{"plan": itoa(plan.ID), "title": "Fluchtweg Ost", "kind": "route", "geom": `{"t":"line","p":[[49.02158,8.41255],[49.02160,8.41275],[49.02162,8.41292]]}`})
	for _, rl := range a.roles() {
		if rl.Name == "Orga-Leitung" {
			a.setMember(e.ID, admin.ID, rl.ID, nil)
		}
	}
	log.Printf("Demo-Daten (fiktiv) angelegt: Event %q", e.Name)
	fmt.Print()
}
