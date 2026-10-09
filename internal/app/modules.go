// Copyright (C) 2026 yniverz
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

var (
	optPrio   = []Opt{O("low", "Niedrig", "gray"), O("normal", "Normal", "blue"), O("high", "Hoch", "red")}
	optSource = []Opt{O("own", "Eigenbestand", "gray"), O("borrow", "Geliehen", "teal"), O("rent", "Gemietet", "yellow"), O("buy", "Gekauft", "blue"), O("sponsor", "Gestellt/Sponsor", "green")}
)

var modules []*Module
var modByKey = map[string]*Module{}

func reg(m *Module) {
	if m.Singular == "" {
		m.Singular = m.Name
	}
	modules = append(modules, m)
	modByKey[m.Key] = m
}

func parseDT(s string) (time.Time, bool) {
	for _, l := range []string{"2006-01-02T15:04", "2006-01-02 15:04", "2006-01-02"} {
		if t, err := time.Parse(l, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func hoursBetween(a, b string) float64 {
	t1, ok1 := parseDT(a)
	t2, ok2 := parseDT(b)
	if !ok1 || !ok2 || !t2.After(t1) {
		return 0
	}
	return t2.Sub(t1).Hours()
}

func init() {
	reg(&Module{Key: "overview", Name: "Übersicht", Icon: "grid", Core: true, Default: true, Page: "", Order: 0})

	reg(&Module{
		Key: "areas", Name: "Bereiche", Singular: "Bereich", Icon: "layers", Core: true, Default: true, Order: 1,
		Desc:  "Zuständigkeiten wie Bar, Technik oder Einlass. Jeder Bereich hat eine Leitung; Aufgaben, Kosten und Personal lassen sich Bereichen zuordnen.",
		Title: "name", NoList: true, NoAttach: true, ExtraTpl: "x_areas", Extra: areasExtra,
		Empty: "Noch keine Bereiche. Lege z. B. Bar, Technik und Einlass an.",
		Fields: []Field{
			F("name", "Name", TText).Req().List(),
			F("color", "Farbe", TColor).Def("#8a8f98"),
			F("lead", "Leitung", TRef).Of("contacts").List(),
			F("budget", "Kostenlimit", TMoney).Unit("€").Hint("Optional. Warnt, wenn die geplanten Kosten des Bereichs darüber liegen."),
			F("notes", "Notizen", TTextarea).Wide_(),
		},
	})

	reg(&Module{
		Key: "timeplan", Name: "Zeitplan", Icon: "cal", Default: true, Order: 2, Page: "zeitplan",
		Desc: "Alle Fristen und Termine des Events an einem Ort, mit Countdown zum Eventtag und Kalender-Abo.",
	})

	reg(&Module{
		Key: "tasks", Name: "Aufgaben", Singular: "Aufgabe", Icon: "check", Default: true, Order: 2,
		Desc:  "Alles, was erledigt werden muss. Mit Bereich, Zuständigkeit und Frist.",
		Title: "title", Sort: "due", AreaField: "area", Filters: []string{"area", "assignee", "status"},
		DoneField: "status", DoneVals: []string{"done"}, Empty: "Keine Aufgaben.",
		Fields: []Field{
			F("title", "Aufgabe", TText).Req().List(),
			F("area", "Bereich", TRef).Of("areas").List(),
			F("assignee", "Zuständig", TRef).Of("contacts").List(),
			F("due", "Frist", TDate).List(),
			F("prio", "Priorität", TSelect).Options(optPrio...).Def("normal").List(),
			F("status", "Status", TSelect).Options(O("open", "Offen", "gray"), O("doing", "In Arbeit", "blue"), O("waiting", "Wartet", "yellow"), O("done", "Erledigt", "green")).Def("open").Quick_(),
			F("notes", "Notizen", TTextarea).Wide_(),
		},
	})

	reg(&Module{
		Key: "permits", Name: "Genehmigungen", Singular: "Genehmigung", Icon: "shield", Default: true, Order: 3,
		Desc:  "Behörden, Anträge, Auflagen und Fristen: Ordnungsamt, Gestattung, GEMA, Sicherheitskonzept und alles, was sonst noch genehmigt oder angemeldet werden muss.",
		Title: "title", Sort: "deadline", Filters: []string{"status"}, Empty: "Keine Genehmigungen erfasst.",
		Fields: []Field{
			F("title", "Was", TText).Req().List().Suggest("Veranstaltungsanzeige / Ordnungsamt", "Gaststättenerlaubnis (Gestattung)", "Sondernutzung öffentlicher Grund", "Lärmschutzausnahme", "GEMA-Anmeldung", "Sicherheitskonzept", "Sanitätsdienst", "Feuerwehr / Brandschutz", "Verkehrsrechtliche Anordnung", "Veranstalterhaftpflicht", "Jugendschutz", "Müll- und Reinigungskonzept"),
			F("authority", "Behörde / Stelle", TText).List().Suggest("Ordnungsamt", "Bauamt", "Feuerwehr", "Polizei", "GEMA", "Gesundheitsamt", "Straßenverkehrsamt", "Versicherung"),
			F("status", "Status", TSelect).Options(O("todo", "Offen", "gray"), O("prep", "In Vorbereitung", "blue"), O("filed", "Beantragt", "yellow"), O("query", "Rückfrage", "orange"), O("ok", "Genehmigt", "green"), O("denied", "Abgelehnt", "red"), O("na", "Nicht nötig", "ink")).Def("todo").Quick_(),
			F("deadline", "Antrag bis", TDate).List().Hint("Späteste Abgabe bzw. Frist der Behörde."),
			F("responsible", "Zuständig im Team", TRef).Of("contacts").List(),
			F("contact", "Ansprechperson Behörde", TRef).Of("contacts"),
			F("ref_no", "Aktenzeichen", TText),
			F("cost", "Gebühr", TMoney).Unit("€").Fin_().Sum_(),
			F("paid", "Gebühr bezahlt", TBool).Fin_(),
			F("conditions", "Auflagen", TTextarea).Wide_().Hint("Auflagen aus dem Bescheid, z. B. Sperrstunde, Lautstärke, Fluchtwege."),
			F("notes", "Notizen", TTextarea).Wide_(),
		},
	})

	reg(&Module{
		Key: "loc_candidates", Name: "Location-Suche", Singular: "Anfrage", Icon: "pin", Default: true, Order: 4,
		Desc:  "Locations anfragen und vergleichen. Eine Anfrage lässt sich als gewählte Location festlegen.",
		Title: "location", Filters: []string{"status"}, Empty: "Noch keine Location angefragt.",
		ExtraTpl: "x_map", Extra: candidatesMapExtra,
		Fields: []Field{
			F("location", "Location", TRef).Of("locations").Req().List(),
			F("status", "Status", TSelect).Options(O("idea", "Idee", "gray"), O("asked", "Angefragt", "blue"), O("viewing", "Besichtigung", "yellow"), O("offer", "Angebot", "orange"), O("yes", "Zusage", "green"), O("no", "Absage", "red")).Def("idea").Quick_(),
			F("cost", "Kosten", TMoney).Unit("€").Fin_().List().Sum_().Hint("Miete bzw. Pauschale für den Event."),
			F("deposit", "Kaution", TMoney).Unit("€").Fin_(),
			F("asked_on", "Angefragt am", TDate).List(),
			F("conditions", "Bedingungen", TTextarea).Wide_().Hint("Sperrzeit, Lautstärke, Auflagen, Haftung, Reinigung …"),
			F("notes", "Notizen", TTextarea).Wide_(),
		},
		Virt: []VField{{Key: "chosen", Label: "Gewählt", Type: TText, Fn: func(c *C, r *Rec) string {
			if c.chosenCandidate() == r.ID {
				return "Gewählt"
			}
			return ""
		}}},
		Actions: []Action{{Key: "choose", Label: "Als Location festlegen", Level: 2, Fn: func(c *C, r *Rec) string {
			c.Event.setting("loc_cand", r.ID)
			c.Event.LocationID = r.I("location")
			_ = c.A.saveEvent(c.Event)
			return "Location festgelegt."
		}}},
	})

	reg(&Module{
		Key: "budget", Name: "Budget", Singular: "Posten", Icon: "euro", Default: true, Order: 5,
		Desc:  "Alle Einnahmen und Ausgaben. Kosten aus Line-up, Personal, Material, Genehmigungen und Bar werden automatisch dazugerechnet.",
		Title: "title", Sort: "category", Group: "kind", Filters: []string{"kind", "category", "area", "status"}, AreaField: "area",
		ExtraTpl: "x_budget", Extra: budgetExtra, Empty: "Noch keine Posten.",
		Fields: []Field{
			F("title", "Bezeichnung", TText).Req().List(),
			F("kind", "Art", TSelect).Options(O("exp", "Ausgabe", "red"), O("inc", "Einnahme", "green")).Def("exp").List(),
			F("category", "Kategorie", TText).List().Suggest("Location", "Technik", "Licht & Deko", "Personal", "Security", "Getränke/Einkauf", "Versicherung & Gebühren", "Werbung", "Verpflegung", "Toiletten & Müll", "Sonstiges", "Sponsoring", "Förderung", "Verkauf"),
			F("area", "Bereich", TRef).Of("areas").List(),
			F("qty", "Menge", TNumber).Def("1"),
			F("unit_price", "Einzelpreis", TMoney).Unit("€"),
			F("scale", "Skalierung", TSelect).Options(O("fix", "Fix", "gray"), O("guest", "Pro Gast", "blue")).Def("fix").Hint("„Pro Gast“ rechnet Menge × Preis × Gästezahl (z. B. Armbänder, Müllbeutel)."),
			F("actual", "Ist-Betrag", TMoney).Unit("€").Hint("Tatsächlich bezahlt bzw. erhalten. Leer lassen, solange es nur eine Planung ist."),
			F("status", "Status", TSelect).Options(O("planned", "Geplant", "gray"), O("asked", "Angefragt", "blue"), O("offer", "Angebot", "yellow"), O("booked", "Bestätigt", "orange"), O("paid", "Bezahlt/Erhalten", "green")).Def("planned").Quick_(),
			F("due", "Fällig am", TDate).List(),
			F("vendor", "Lieferant / Gegenpartei", TRef).Of("contacts"),
			F("notes", "Notizen", TTextarea).Wide_(),
		},
		Virt: []VField{
			{Key: "plan", Label: "Plan", Type: TMoney, Sum: true, Fn: func(c *C, r *Rec) string { return numStr(c.budgetPlan(r)) }},
		},
	})

	reg(&Module{
		Key: "calc", Name: "Kalkulation", Icon: "calc", Default: true, Order: 6, Page: "calc",
		Desc: "Eintritt, Gästezahlen, Break-even und Szenarien.",
	})

	reg(&Module{
		Key: "bar", Name: "Bar & Verkauf", Icon: "glass", Default: true, Order: 7, Page: "bar",
		Desc: "Artikel, Rezepte, Preise, Einkaufsliste und Marge.",
	})
	reg(&Module{
		Key: "bar_items", Name: "Einkaufsartikel", Singular: "Einkaufsartikel", Perm: "bar", Hidden: true, Title: "name", Sort: "category",
		Fields: []Field{
			F("name", "Name", TText).Req().List(),
			F("category", "Kategorie", TText).List().Suggest("Bier", "Wein & Sekt", "Spirituosen", "Softdrinks", "Mixer & Säfte", "Obst & Zutaten", "Eis", "Essen", "Verbrauchsmaterial"),
			F("pack", "Gebinde", TText).Hint("z. B. „Kasten 20×0,5 l“ oder „Fass 50 l“").List(),
			F("unit", "Einheit", TSelect).Options(O("Stk", "Stück", "gray"), O("l", "Liter", "gray"), O("cl", "Zentiliter", "gray"), O("ml", "Milliliter", "gray"), O("kg", "Kilogramm", "gray"), O("g", "Gramm", "gray")).Def("Stk"),
			F("content", "Inhalt pro Gebinde", TNumber).Req().Hint("In der gewählten Einheit, z. B. 20 (Stück) oder 50 (Liter)."),
			F("price", "Preis pro Gebinde", TMoney).Unit("€").Req().List(),
			F("deposit", "Pfand pro Gebinde", TMoney).Unit("€").Hint("Wird nicht als Kosten gerechnet, aber in der Einkaufsliste als Vorlage ausgewiesen."),
			F("waste", "Schwund", TPercent).Unit("%").Hint("Schaum, Verschüttetes, Bruch. Erhöht die Menge, die du einkaufen musst."),
			F("returnable", "Ungeöffnet zurückgebbar", TBool).Hint("Kommissionsware: Es werden nur verbrauchte Gebinde als Kosten gerechnet."),
			F("have", "Schon vorhanden", TNumber).Unit("Gebinde").Hint("Eigenbestand oder gesponsert. Wird nicht eingekauft."),
			F("supplier", "Lieferant", TRef).Of("contacts"),
			F("notes", "Notizen", TTextarea).Wide_(),
		},
		Virt: []VField{{Key: "unit_cost", Label: "Kosten pro Einheit", Type: TMoney, Fn: func(c *C, r *Rec) string {
			return numStr(c.unitCost(r))
		}}},
	})
	reg(&Module{
		Key: "bar_products", Name: "Verkaufsartikel", Singular: "Verkaufsartikel", Perm: "bar", Hidden: true, Title: "name", Sort: "stand", Group: "stand",
		Fields: []Field{
			F("name", "Name", TText).Req().List(),
			F("stand", "Verkaufsstelle", TText).Def("Bar").List().Suggest("Bar", "Cocktailbar", "Bierstand", "Foodstand", "Kasse"),
			F("category", "Kategorie", TText).List().Suggest("Bier", "Cocktails", "Longdrinks", "Shots", "Softdrinks", "Wein & Sekt", "Essen"),
			F("price", "Verkaufspreis", TMoney).Unit("€").Req().List(),
			F("recipe", "Rezept", TRecipe).Wide_().Hint("Welche Einkaufsartikel in welcher Menge pro Portion verbraucht werden."),
			F("pp", "Portionen pro Gast", TNumber).Hint("Erwarteter Konsum pro Gast, z. B. 1,5. Bestimmt die geplante Menge.").List(),
			F("fixqty", "Feste Menge", TNumber).Hint("Optional. Überschreibt „Portionen pro Gast“, unabhängig von der Gästezahl."),
			F("sold", "Ist verkauft", TNumber).Hint("Nach dem Event: tatsächlich verkaufte Portionen."),
			F("notes", "Notizen", TTextarea).Wide_(),
		},
	})

	reg(&Module{
		Key: "lineup", Name: "Line-up", Singular: "Slot", Icon: "music", Default: true, Order: 8,
		Desc:  "Künstler, Bühnen, Zeiten und Gagen.",
		Title: "artist", Sort: "start", Filters: []string{"stage", "status"}, ExtraTpl: "x_lineup", Extra: lineupExtra, ExtraPos: "top", Empty: "Noch kein Line-up.",
		Fields: []Field{
			F("artist", "Künstler / Act", TText).Req().List(),
			F("stage", "Bühne / Floor", TText).List().Def("Main").Suggest("Main", "Floor 2", "Outdoor", "Chillout"),
			F("start", "Beginn", TDateTime).List(),
			F("end", "Ende", TDateTime).List(),
			F("genre", "Genre", TText).List(),
			F("status", "Status", TSelect).Options(O("idea", "Idee", "gray"), O("asked", "Angefragt", "blue"), O("yes", "Zugesagt", "green"), O("contract", "Vertrag", "teal"), O("no", "Abgesagt", "red")).Def("idea").Quick_(),
			F("contact", "Booking-Kontakt", TRef).Of("contacts"),
			F("fee", "Gage", TMoney).Unit("€").Fin_().List().Sum_(),
			F("extra", "Nebenkosten", TMoney).Unit("€").Fin_().Hint("Anreise, Hotel, Verpflegung, Rider."),
			F("paid", "Bezahlt", TBool).Fin_(),
			F("rider", "Technik-Rider / Bedarf", TTextarea).Wide_(),
			F("notes", "Notizen", TTextarea).Wide_(),
		},
	})

	reg(&Module{
		Key: "staff", Name: "Personal", Singular: "Schicht", Icon: "users", Default: true, Order: 9,
		Desc:  "Schichtplan: Wer macht wann was? Offene Plätze sind sofort sichtbar.",
		Title: "position", Sort: "start", Group: "area", Filters: []string{"area", "person", "status"}, AreaField: "area",
		MultiCreate: true, ExtraTpl: "x_staff", Extra: staffExtra, Empty: "Noch keine Schichten.",
		Fields: []Field{
			F("position", "Aufgabe / Position", TText).Req().List().Suggest("Barkeeper", "Theke", "Einlass", "Security", "Garderobe", "Springer", "Aufbau", "Abbau", "Kasse", "Runner", "Springer Bar", "Toilettenaufsicht"),
			F("area", "Bereich", TRef).Of("areas").List(),
			F("start", "Beginn", TDateTime).List(),
			F("end", "Ende", TDateTime).List(),
			F("person", "Person", TRef).Of("contacts").List().Hint("Leer lassen = offener Platz."),
			F("status", "Status", TSelect).Options(O("open", "Offen", "gray"), O("asked", "Angefragt", "blue"), O("yes", "Bestätigt", "green"), O("no", "Abgesagt", "red")).Def("open").Quick_(),
			F("rate", "Stundensatz", TMoney).Unit("€/h").Fin_(),
			F("flat", "Pauschale", TMoney).Unit("€").Fin_(),
			F("paid", "Bezahlt", TBool).Fin_(),
			F("notes", "Notizen", TTextarea).Wide_(),
		},
		Virt: []VField{
			{Key: "hours", Label: "Std.", Type: TNumber, Fn: func(c *C, r *Rec) string { return numStr(round2(hoursBetween(r.S("start"), r.S("end")))) }},
			{Key: "cost", Label: "Kosten", Type: TMoney, Sum: true, Fn: func(c *C, r *Rec) string {
				if !c.finVisible(r, "staff") {
					return ""
				}
				return numStr(staffCost(r))
			}},
		},
	})

	reg(&Module{
		Key: "timeline", Name: "Ablaufplan", Singular: "Programmpunkt", Icon: "clock", Default: true, Order: 10,
		Desc:  "Zeitlicher Ablauf von Aufbau bis Abbau. Line-up-Slots erscheinen automatisch mit.",
		Title: "title", Sort: "start", AreaField: "area", Filters: []string{"area"}, ExtraTpl: "x_timeline", Extra: timelineExtra, ExtraPos: "top", Empty: "Noch kein Ablauf.",
		Fields: []Field{
			F("title", "Was", TText).Req().List().Suggest("Aufbau", "Soundcheck", "Einlass", "Show-Start", "Pause", "Open End", "Abbau", "Kassensturz"),
			F("start", "Beginn", TDateTime).List(),
			F("end", "Ende", TDateTime).List(),
			F("area", "Bereich", TRef).Of("areas").List(),
			F("resp", "Verantwortlich", TRef).Of("contacts").List(),
			F("notes", "Notizen", TTextarea).Wide_(),
		},
	})

	reg(&Module{
		Key: "equipment", Name: "Material", Singular: "Material", Icon: "box", Default: true, Order: 11,
		Desc:  "Technik, Möbel, Zelte, Verbrauchsmaterial: was gebraucht wird, woher es kommt und was es kostet.",
		Title: "item", Sort: "area", Group: "area", Filters: []string{"area", "source", "status"}, AreaField: "area", Empty: "Noch kein Material erfasst.",
		Fields: []Field{
			F("item", "Material", TText).Req().List(),
			F("qty", "Menge", TNumber).Def("1").List(),
			F("area", "Bereich", TRef).Of("areas").List(),
			F("source", "Beschaffung", TSelect).Options(optSource...).Def("own").List(),
			F("vendor", "Verleiher / Händler", TRef).Of("contacts"),
			F("status", "Status", TSelect).Options(O("need", "Benötigt", "gray"), O("asked", "Angefragt", "blue"), O("reserved", "Reserviert", "yellow"), O("have", "Besorgt", "green"), O("back", "Zurückgegeben", "ink")).Def("need").Quick_(),
			F("cost", "Kosten gesamt", TMoney).Unit("€").Fin_().List().Sum_(),
			F("paid", "Bezahlt", TBool).Fin_(),
			F("pickup", "Abholung", TDate),
			F("ret", "Rückgabe", TDate),
			F("notes", "Notizen", TTextarea).Wide_(),
		},
	})

	reg(&Module{
		Key: "sitemap", Name: "Lageplan", Icon: "map", Default: true, Order: 11, Page: "lageplan",
		Desc: "Wo steht was? Plan hochladen oder direkt auf der Karte planen, Bereiche, Bühne, Bar und Fluchtwege einzeichnen.",
	})
	reg(&Module{
		Key: "siteplans", Name: "Pläne", Singular: "Plan", Perm: "sitemap", Hidden: true, Title: "name", Sort: "name",
		Fields: []Field{
			F("name", "Name", TText).Req().List(),
			F("mode", "Art", TSelect).Options(O("image", "Bild (eigener Plan)", "blue"), O("map", "Karte", "green")).Def("image"),
			F("width_m", "Breite des Plans", TNumber).Unit("Meter").Hint("Für Flächenangaben bei Bildplänen: wie breit ist das ganze Bild in der Wirklichkeit?"),
			F("geo", "Kartenmitte", TGeo).Wide_(),
			F("zoom", "Zoomstufe", TNumber),
		},
	})
	reg(&Module{
		Key: "site_items", Name: "Plan-Objekte", Singular: "Objekt", Perm: "sitemap", Hidden: true, Title: "title", Sort: "title", AreaField: "area",
		Fields: []Field{
			F("plan", "Plan", TRef).Of("siteplans").Req(),
			F("title", "Bezeichnung", TText).Req().List(),
			F("kind", "Art", TSelect).Options(siteKindOpts()...).Def("zone").List(),
			F("area", "Bereich", TRef).Of("areas").List(),
			F("geom", "Geometrie", TText).Hidden_(),
			F("notes", "Notizen", TTextarea).Wide_(),
		},
	})
	reg(&Module{
		Key: "notes", Name: "Notizen & Links", Singular: "Notiz", Icon: "note", Default: true, Order: 12,
		Desc:  "Konzepte, Verträge, Ideen und Links, die zum Event gehören.",
		Title: "title", Sort: "-updated", Filters: []string{"category"}, AreaField: "area", Empty: "Noch keine Notizen.",
		Fields: []Field{
			F("title", "Titel", TText).Req().List(),
			F("category", "Kategorie", TText).List().Suggest("Konzept", "Vertrag", "Idee", "Link", "Protokoll"),
			F("area", "Bereich", TRef).Of("areas").List(),
			F("url", "Link", TURL).List(),
			F("content", "Inhalt", TTextarea).Wide_(),
		},
	})

	// --- global master data ---
	reg(&Module{
		Key: "contacts", Name: "Kontakte", Singular: "Kontakt", Icon: "book", Global: true, Order: 100,
		Desc:  "Personen, Firmen und Behörden. Alle Events greifen auf dieselben Kontakte zu.",
		Title: "name", Sort: "name", Filters: []string{"kind"}, Empty: "Noch keine Kontakte.",
		Fields: []Field{
			F("name", "Name", TText).Req().List(),
			F("kind", "Art", TSelect).Options(O("person", "Person", "gray"), O("company", "Firma", "blue"), O("authority", "Behörde", "yellow")).Def("person").List(),
			F("org", "Organisation", TText).List(),
			F("tags", "Rollen / Tags", TText).List().Hint("Kommagetrennt, z. B. DJ, Barkeeper, Techniker, Vermieter").Suggest("DJ", "Booking", "Barkeeper", "Techniker", "Security", "Helfer", "Vermieter", "Lieferant", "Sponsor", "Fotograf", "Behörde"),
			F("phone", "Telefon", TText).List(),
			F("email", "E-Mail", TText).List(),
			F("address", "Adresse", TTextarea),
			F("geo", "Position auf der Karte", TGeo).Wide_().Hint("Optional, z. B. für Lieferanten und Vermieter."),
			F("notes", "Notizen", TTextarea).Wide_(),
		},
		ExtraTpl: "x_map", Extra: contactsMapExtra,
	})
	reg(&Module{
		Key: "locations", Name: "Locations", Singular: "Location", Icon: "pin", Global: true, Order: 101,
		Desc:  "Orte, die ihr nutzen könntet oder schon genutzt habt.",
		Title: "name", Sort: "name", Filters: []string{"kind"}, Empty: "Noch keine Locations.",
		ExtraTpl: "x_map", Extra: locationsMapExtra,
		Fields: []Field{
			F("name", "Name", TText).Req().List(),
			F("kind", "Art", TSelect).Options(O("indoor", "Indoor", "blue"), O("outdoor", "Outdoor", "green"), O("mixed", "Beides", "teal")).Def("indoor").List(),
			F("city", "Ort", TText).List(),
			F("address", "Adresse", TText),
			F("geo", "Position auf der Karte", TGeo).Wide_().List().Hint("Adresse suchen oder direkt auf die Karte klicken."),
			F("capacity", "Kapazität", TNumber).Unit("Personen").List().Hint("Maximal zulässige Personenzahl."),
			F("rent", "Typische Kosten", TMoney).Unit("€").List(),
			F("owner", "Ansprechperson", TRef).Of("contacts").List(),
			F("curfew", "Sperrzeit / Lautstärke", TText).Hint("z. B. „22 Uhr, 55 dB“ oder „Open End“"),
			F("power", "Strom / Wasser", TText),
			F("rating", "Eindruck", TSelect).Options(O("1", "Schwach", "red"), O("2", "Okay", "yellow"), O("3", "Gut", "green"), O("4", "Top", "teal")),
			F("url", "Link", TURL),
			F("notes", "Notizen", TTextarea).Wide_().Hint("Auflagen, Erfahrungen, Zufahrt, Anwohner, Parken …"),
		},
	})
	reg(&Module{
		Key: "articles", Name: "Artikelstamm", Singular: "Artikel", Icon: "tag", Global: true, Order: 102,
		Desc:  "Wiederkehrende Einkaufsartikel mit Richtpreisen. Beim Anlegen einer Bar kannst du daraus übernehmen.",
		Title: "name", Sort: "category", Group: "category", Empty: "Noch keine Artikel.",
		Fields: []Field{
			F("name", "Name", TText).Req().List(),
			F("category", "Kategorie", TText).List(),
			F("pack", "Gebinde", TText).List(),
			F("unit", "Einheit", TSelect).Options(O("Stk", "Stück", "gray"), O("l", "Liter", "gray"), O("cl", "Zentiliter", "gray"), O("ml", "Milliliter", "gray"), O("kg", "Kilogramm", "gray"), O("g", "Gramm", "gray")).Def("Stk"),
			F("content", "Inhalt pro Gebinde", TNumber).Req(),
			F("price", "Richtpreis", TMoney).Unit("€").List(),
			F("deposit", "Pfand", TMoney).Unit("€"),
			F("waste", "Schwund", TPercent).Unit("%"),
			F("returnable", "Zurückgebbar", TBool),
			F("supplier", "Lieferant", TRef).Of("contacts"),
			F("notes", "Notizen", TTextarea).Wide_(),
		},
	})

	// relative deadlines: derived from the event date and moved along with it
	for key, after := range map[string]string{"tasks": "due", "permits": "deadline", "budget": "due"} {
		insertAfter(modByKey[key], after, F("rel", "Frist relativ zum Event", TNumber).Unit("Tage vorher").Hint("Optional. Leitet die Frist aus dem Eventtermin ab und verschiebt sie mit. Negativ = nach dem Event."))
	}
	// net/gross handling: amounts can be entered net or gross with their own VAT rate.
	for _, k := range []string{"budget", "lineup", "equipment", "loc_candidates", "bar_items"} {
		insertBeforeNotes(modByKey[k], vatFields())
	}
	insertBeforeNotes(modByKey["bar_products"], []Field{
		F("vat", "USt-Satz", TSelect).Options(O("0", "0 %", "gray"), O("7", "7 %", "gray"), O("19", "19 %", "gray")).Blank("Standard des Events").Hint("Verkaufspreise gelten als Brutto-Preise (so steht es auf der Karte). 7 % z. B. für Speisen zum Mitnehmen."),
	})
	registerFeatureModules()
	extendModules()
	sort.SliceStable(modules, func(i, j int) bool { return modules[i].Order < modules[j].Order })
}

func vatFields() []Field {
	return []Field{
		F("entry", "Betrag ist", TSelect).Options(O("net", "Netto", "blue"), O("gross", "Brutto", "gray")).Blank("Standard des Events").Hint("Kleine Lieferanten ohne USt: „Brutto“ und 0 %."),
		F("vat", "USt-Satz", TSelect).Options(O("0", "0 % (steuerfrei / Kleinunternehmer)", "gray"), O("7", "7 %", "gray"), O("19", "19 %", "gray")).Blank("Standard des Events"),
	}
}

func insertBeforeNotes(m *Module, add []Field) {
	for i := range m.Fields {
		if m.Fields[i].Key == "notes" {
			out := append([]Field{}, m.Fields[:i]...)
			out = append(out, add...)
			m.Fields = append(out, m.Fields[i:]...)
			return
		}
	}
	m.Fields = append(m.Fields, add...)
}

// eventModules returns modules usable inside events, in nav order.
func eventModules() []*Module {
	var out []*Module
	for _, m := range modules {
		if !m.Global && !m.Hidden {
			out = append(out, m)
		}
	}
	return out
}

func globalModules() []*Module {
	var out []*Module
	for _, m := range modules {
		if m.Global {
			out = append(out, m)
		}
	}
	return out
}

type PermDef struct {
	Key, Label, Desc string
}

func permDefs() []PermDef {
	var out []PermDef
	for _, m := range eventModules() {
		if m.Key == "overview" {
			continue
		}
		out = append(out, PermDef{m.PermKey(), m.Name, m.Desc})
	}
	out = append(out,
		PermDef{"fin", "Kosten & Gagen sehen", "Kostenfelder in Line-up, Personal, Material, Genehmigungen und Location. Ohne dieses Recht bleiben sie verborgen."},
		PermDef{"settings", "Event-Einstellungen", "Event bearbeiten, Module aktivieren, Mitglieder einladen, als Vorlage speichern, löschen."},
	)
	return out
}

func levelName(l int) string {
	switch l {
	case 1:
		return "Lesen"
	case 2:
		return "Bearbeiten"
	}
	return "Kein Zugriff"
}

func itoa(i int64) string { return fmt.Sprintf("%d", i) }

func joinTags(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func insertAfter(m *Module, key string, f Field) {
	for i := range m.Fields {
		if m.Fields[i].Key == key {
			out := append([]Field{}, m.Fields[:i+1]...)
			out = append(out, f)
			m.Fields = append(out, m.Fields[i+1:]...)
			return
		}
	}
	m.Fields = append(m.Fields, f)
}

// relDateKey names the date field a module derives from its "rel" field.
var relDateKey = map[string]string{"tasks": "due", "permits": "deadline", "budget": "due"}

// relDate returns the absolute date for "rel days before the event start".
func relDate(eventStart, rel string) (string, bool) {
	if rel == "" {
		return "", false
	}
	t, ok := parseDT(eventStart)
	if !ok {
		return "", false
	}
	return t.AddDate(0, 0, -int(parseNum(rel))).Format("2006-01-02"), true
}

// applyRelative recomputes all relative deadlines of an event.
func (a *App) applyRelative(e *Event) {
	for mod, key := range relDateKey {
		for _, r := range a.recs(e.ID, mod) {
			if d, ok := relDate(e.Start, r.S("rel")); ok && r.S(key) != d {
				r.D[key] = d
				_ = a.saveRec(r)
			}
		}
	}
}
