package app

import (
	"encoding/json"
	"fmt"
	"log"
)

func allPerms(level int) map[string]int {
	p := map[string]int{}
	for _, d := range permDefs() {
		p[d.Key] = level
	}
	return p
}

func (a *App) seed() {
	var n int
	_ = a.db.QueryRow("SELECT COUNT(*) FROM roles").Scan(&n)
	if n == 0 {
		orga := allPerms(2)
		fin := allPerms(1)
		fin["budget"], fin["calc"], fin["fin"] = 2, 2, 2
		bereich := map[string]int{"areas": 1, "tasks": 2, "budget": 2, "equipment": 2, "staff": 2, "timeline": 1, "notes": 2, "permits": 1, "lineup": 1, "bar": 1}
		bar := map[string]int{"areas": 1, "tasks": 2, "budget": 2, "equipment": 2, "staff": 2, "timeline": 1, "notes": 2, "bar": 2, "lineup": 1}
		booking := map[string]int{"areas": 1, "tasks": 2, "lineup": 2, "timeline": 2, "notes": 2, "fin": 1, "budget": 1, "permits": 1}
		helfer := map[string]int{"areas": 1, "tasks": 1, "staff": 1, "timeline": 1, "notes": 1}
		read := allPerms(1)
		read["settings"] = 0
		for _, r := range []*Role{
			{Name: "Orga-Leitung", Description: "Voller Zugriff auf das Event, inklusive Einstellungen und Team.", Perms: orga, Builtin: true},
			{Name: "Finanzen", Description: "Budget und Kalkulation bearbeiten, alles andere lesen.", Perms: fin, Builtin: true},
			{Name: "Bar-Leitung", Description: "Bar & Verkauf komplett, Aufgaben/Budget/Personal/Material nur im eigenen Bereich.", Perms: bar, AreaScoped: true, Builtin: true},
			{Name: "Bereichsleitung", Description: "Aufgaben, Budget, Personal und Material im eigenen Bereich. Keine Gagen oder fremden Kosten.", Perms: bereich, AreaScoped: true, Builtin: true},
			{Name: "Booking", Description: "Line-up und Ablaufplan bearbeiten, Gagen sehen.", Perms: booking, Builtin: true},
			{Name: "Helfer:in", Description: "Sieht eigene Aufgaben, Schichten und den Ablaufplan.", Perms: helfer, AreaScoped: true, Builtin: true},
			{Name: "Nur lesen", Description: "Sieht alles, ändert nichts.", Perms: read, Builtin: true},
		} {
			if err := a.saveRole(r); err != nil {
				log.Printf("seed role: %v", err)
			}
		}
	}
	_ = a.db.QueryRow("SELECT COUNT(*) FROM templates").Scan(&n)
	if n == 0 {
		for _, t := range builtinTemplates() {
			t.Builtin = true
			if err := a.saveTemplate(t); err != nil {
				log.Printf("seed template: %v", err)
			}
		}
	}
}

// ---------- built-in templates ----------

type tb struct {
	recs  []TplRec
	areas map[string]int64
	next  int64
}

func newTB() *tb { return &tb{areas: map[string]int64{}, next: 1} }

func (b *tb) area(name, color string) {
	id := b.next
	b.next++
	b.areas[name] = id
	b.recs = append(b.recs, TplRec{Module: "areas", Orig: id, D: map[string]string{"name": name, "color": color}})
}

func (b *tb) add(mod string, kv string) int64 {
	d := parseKV(kv)
	if a, ok := d["area"]; ok {
		if id, ok := b.areas[a]; ok {
			d["area"] = itoa(id)
		} else {
			delete(d, "area")
		}
	}
	id := b.next
	b.next++
	b.recs = append(b.recs, TplRec{Module: mod, Orig: id, D: d})
	return id
}

func (b *tb) item(kv string) int64 { return b.add("bar_items", kv) }

func (b *tb) prod(kv string, recipe ...any) {
	var lines []RecipeLine
	for i := 0; i+1 < len(recipe); i += 2 {
		lines = append(lines, RecipeLine{recipe[i].(int64), recipe[i+1].(float64)})
	}
	js, _ := json.Marshal(lines)
	b.add("bar_products", kv+"|recipe="+string(js))
}

func builtinTemplates() []*Template {
	mods := defaultModules()
	var out []*Template

	// ---- Techno Open Air ----
	b := newTB()
	for _, a := range [][2]string{{"Orga", "#3d3d3d"}, {"Bar", "#b4572c"}, {"Technik & Sound", "#2f6f73"}, {"Einlass & Kasse", "#7a6a2e"}, {"Security & Sanitäts", "#8a2f2f"}, {"Aufbau & Deko", "#55704a"}, {"Marketing", "#6b4f7a"}} {
		b.area(a[0], a[1])
	}
	for _, p := range []string{
		"title=Veranstaltung anmelden (Veranstaltungsleitfaden)|authority=Ordnungs- und Bürgeramt Karlsruhe|notes=Erste Anlaufstelle. Das Amt zieht bei Bedarf die Fachämter hinzu und legt Auflagen fest. Frühzeitig melden. Leitfaden: https://web1.karlsruhe.de/service/Formulare/ordnungsamt/OA3_Veranstaltungsleitfaden_Karlsruhe.pdf",
		"title=Vorübergehender Gaststättenbetrieb (Alkoholausschank)|authority=Ordnungs- und Bürgeramt Karlsruhe – Gaststätten- und Gewerberecht|notes=In Baden-Württemberg Anzeige bzw. Gestattung nach dem Landesgaststättengesetz. Kommunale Formulare nennen meist mindestens zwei Wochen Vorlauf. Aktuelles Karlsruher Formular und Frist beim Amt bestätigen. Nur alkoholfreie Getränke oder Speisen laufen über ein anderes Verfahren.",
		"title=Sperrzeit und Lärmschutz klären|authority=Ordnungs- und Bürgeramt Karlsruhe|notes=Sperrzeit nach Landesgaststättengesetz (laut kommunalen Hinweisen regelmäßig ab 3 Uhr, Verkürzung beantragbar) und Immissionswerte für die Nachtruhe. Open-End-Zeiten und Boxenausrichtung vorab abstimmen. Ohne Gewähr, mit dem Amt klären.",
		"title=Fläche: Zustimmung Eigentümer oder Sondernutzung öffentlicher Grund|authority=Eigentümer / Stadt Karlsruhe|notes=Privatfläche: schriftliche Zustimmung mit Nutzungszeitraum, Haftung und Rückbau. Öffentlicher Grund: Antrag über die Stadt (im Veranstaltungsleitfaden beschrieben).",
		"title=Verkehrsrechtliche Anordnung (Sperrung, Parken, Zufahrt)|authority=Straßenverkehrsstelle Karlsruhe|notes=Nur nötig bei Straßensperrung, Halteverboten oder Nutzung von Verkehrsflächen.",
		"title=Sicherheitskonzept (Besucherzahl, Fluchtwege, Ordnungsdienst)|authority=Ordnungs- und Bürgeramt / Polizei / Feuerwehr|notes=Umfang legt die Stadt im Einzelfall fest. Bei großen Veranstaltungen (Leitfaden: ab etwa 5.000 gleichzeitig Anwesenden) frühzeitig die Straßenverkehrsstelle kontaktieren.",
		"title=Bühne und Zelte: Fliegende Bauten anzeigen / abnehmen|authority=Bauordnungsbehörde der Stadt|notes=Zelte und Bühnen sind in der Regel Fliegende Bauten. Kommunale Formulare nennen mindestens eine Woche Vorlauf vor dem Aufbau. Ausführungsgenehmigung und Prüfbuch vom Verleiher geben lassen.",
		"title=Versammlungsstättenrecht prüfen (VStättVO Baden-Württemberg)|authority=Bauordnungsbehörde der Stadt|notes=Gilt je nach Größe auch für Freigelände mit Szenenflächen. Mit der Bauordnungsbehörde klären, ob und was gilt.",
		"title=Brandschutz, Zufahrten, Rettungswege|authority=Feuerwehr Karlsruhe|notes=Zufahrt für Rettungsfahrzeuge, Löschmittel, Pyrotechnik, Heizgeräte, Aggregate.",
		"title=Sanitätsdienst buchen|authority=Hilfsorganisation|notes=Umfang richtet sich nach Besucherzahl und Sicherheitskonzept.",
		"title=Naturschutz / Landschaftsschutz prüfen|authority=Untere Naturschutzbehörde|notes=Nur bei Flächen in oder nahe Schutzgebieten (Wald, Auen, Gewässer).",
		"title=Jugendschutz: Aushang und Alterskontrolle|authority=Ordnungs- und Bürgeramt Karlsruhe|notes=Jugendschutzgesetz: Aushang, Alterskontrolle beim Einlass und Ausschank.",
		"title=Lebensmittelrecht bei Speisen anmelden|authority=Lebensmittelüberwachung Karlsruhe|notes=Wenn Speisen angeboten werden: Anmeldung, Hygienebelehrung der Helfer (Infektionsschutzgesetz).",
		"title=GEMA-Anmeldung|authority=GEMA|notes=Vor dem Event anmelden (Fläche, Eintritt, Veranstaltungsart). Tarif hängt von Größe und Eintritt ab.",
		"title=Künstlersozialabgabe prüfen|authority=Künstlersozialkasse|notes=Bei Honoraren an Künstler und DJs kann Abgabepflicht bestehen. Prüfen und ggf. melden.",
		"title=Vergnügungssteuer: Pflicht bei der Stadt erfragen|authority=Stadt Karlsruhe|notes=Klären, ob für die Veranstaltungsart eine kommunale Steuer anfällt.",
		"title=Veranstalterhaftpflicht abschließen|authority=Versicherung|notes=Deckungssumme, Bedingungen, Außenbereich, Aufbauzeit und Helfer prüfen.",
		"title=Müll- und Reinigungskonzept|authority=Amt für Abfallwirtschaft Karlsruhe|notes=Container, Endreinigung, Mehrweg und Pfand.",
	} {
		b.add("permits", p+"|status=todo")
	}
	for _, t := range []string{
		"title=Termin und Location festlegen|area=Orga|prio=high",
		"title=Genehmigungsliste durchgehen und Fristen eintragen|area=Orga|prio=high",
		"title=Line-up buchen und Verträge schicken|area=Orga",
		"title=Flyer, Artwork und Social-Media-Plan|area=Marketing",
		"title=Vorverkauf / Tickets organisieren|area=Einlass & Kasse",
		"title=Soundsystem, Licht und Strom klären|area=Technik & Sound|prio=high",
		"title=Toiletten, Wasser und Müllcontainer bestellen|area=Aufbau & Deko",
		"title=Barbedarf kalkulieren und bestellen|area=Bar",
		"title=Helfer:innen suchen und Schichtplan abstimmen|area=Orga",
		"title=Sicherheits- und Sanitätsdienst abstimmen|area=Security & Sanitäts|prio=high",
		"title=Abbau, Endreinigung und Müll|area=Aufbau & Deko",
		"title=Kassensturz und Abrechnung|area=Orga",
	} {
		b.add("tasks", t+"|status=open")
	}
	for _, e := range []string{
		"item=Soundsystem inkl. Techniker|area=Technik & Sound|source=rent|status=need",
		"item=DJ-Equipment (CDJs, Mixer)|area=Technik & Sound|source=rent|status=need",
		"item=Licht und Effekte|area=Technik & Sound|source=rent|status=need",
		"item=Stromverteilung / Aggregat|area=Technik & Sound|source=rent|status=need",
		"item=Bauzaun / Absperrung|area=Security & Sanitäts|source=rent|status=need",
		"item=Toilettenwagen / Dixis|area=Aufbau & Deko|source=rent|qty=1|status=need",
		"item=Theken, Kühlung und Zapfanlage|area=Bar|source=rent|status=need",
		"item=Biertischgarnituren|area=Aufbau & Deko|source=borrow|qty=10|status=need",
		"item=Funkgeräte|area=Security & Sanitäts|source=own|qty=6|status=need",
	} {
		b.add("equipment", e)
	}
	for _, s := range []string{
		"position=Barkeeper|area=Bar|status=open",
		"position=Einlass|area=Einlass & Kasse|status=open",
		"position=Security|area=Security & Sanitäts|status=open",
		"position=Springer|area=Orga|status=open",
	} {
		b.add("staff", s)
	}
	for _, t := range []string{"title=Aufbau", "title=Soundcheck", "title=Einlass", "title=Show-Start", "title=Abbau"} {
		b.add("timeline", t)
	}
	for _, l := range []string{"title=Location-Miete|category=Location|kind=exp", "title=Sicherheitsdienst|category=Security|kind=exp", "title=Versicherung und Gebühren|category=Versicherung & Gebühren|kind=exp", "title=Werbung (Druck, Ads)|category=Werbung|kind=exp", "title=Sponsoring|category=Sponsoring|kind=inc"} {
		b.add("budget", l+"|qty=1|scale=fix|status=planned")
	}
	pils := b.item("name=Pils (Fass 50 l)|category=Bier|pack=Fass 50 l|unit=l|content=50|price=140|deposit=30|waste=8|returnable=1|notes=Beispielwert, bitte anpassen")
	mate := b.item("name=Club-Mate (Kasten 20×0,5 l)|category=Softdrinks|pack=Kasten 20×0,5 l|unit=Stk|content=20|price=21|deposit=4.5|notes=Beispielwert, bitte anpassen")
	cola := b.item("name=Cola (Kasten 24×0,33 l)|category=Softdrinks|pack=Kasten 24×0,33 l|unit=Stk|content=24|price=16|deposit=5.1|notes=Beispielwert, bitte anpassen")
	wasser := b.item("name=Wasser (Kasten 12×0,75 l)|category=Softdrinks|pack=Kasten 12×0,75 l|unit=Stk|content=12|price=7|deposit=3.3|notes=Beispielwert, bitte anpassen")
	vodka := b.item("name=Vodka 0,7 l|category=Spirituosen|pack=Flasche 0,7 l|unit=cl|content=70|price=10|waste=3|notes=Beispielwert, bitte anpassen")
	gin := b.item("name=Gin 0,7 l|category=Spirituosen|pack=Flasche 0,7 l|unit=cl|content=70|price=15|waste=3|notes=Beispielwert, bitte anpassen")
	tonic := b.item("name=Tonic (Kasten 24×0,2 l)|category=Mixer & Säfte|pack=Kasten 24×0,2 l|unit=Stk|content=24|price=19|deposit=3.4|notes=Beispielwert, bitte anpassen")
	becher := b.item("name=Becher 0,4 l (100 Stk)|category=Verbrauchsmaterial|pack=Stange 100 Stk|unit=Stk|content=100|price=9|notes=Beispielwert, bitte anpassen")
	eis := b.item("name=Eiswürfel (10-kg-Sack)|category=Eis|pack=Sack 10 kg|unit=kg|content=10|price=6|notes=Beispielwert, bitte anpassen")
	b.prod("name=Bier 0,4 l|stand=Bar|category=Bier|price=4|pp=1.6", pils, 0.4, becher, 1.0)
	b.prod("name=Club-Mate|stand=Bar|category=Softdrinks|price=3.5|pp=0.8", mate, 1.0)
	b.prod("name=Cola|stand=Bar|category=Softdrinks|price=3|pp=0.4", cola, 1.0)
	b.prod("name=Wasser|stand=Bar|category=Softdrinks|price=2|pp=0.4", wasser, 1.0)
	b.prod("name=Vodka-Mate|stand=Bar|category=Longdrinks|price=6.5|pp=0.5", vodka, 4.0, mate, 1.0, becher, 1.0, eis, 0.1)
	b.prod("name=Gin Tonic|stand=Bar|category=Longdrinks|price=7|pp=0.4", gin, 4.0, tonic, 1.0, becher, 1.0, eis, 0.1)
	out = append(out, &Template{
		Name: "Techno Open Air", Description: "Outdoor-Rave mit Genehmigungsliste für Karlsruhe / Baden-Württemberg (Hinweise ohne Gewähr), Bereichen, Bar-Beispielsortiment und Aufgaben.",
		Payload: TplPayload{Modules: mods, Records: b.recs, Settings: calcSeed(300)},
	})

	// ---- Club Night ----
	b = newTB()
	for _, a := range [][2]string{{"Orga", "#3d3d3d"}, {"Bar", "#b4572c"}, {"Technik", "#2f6f73"}, {"Einlass", "#7a6a2e"}, {"Garderobe", "#6b4f7a"}} {
		b.area(a[0], a[1])
	}
	for _, p := range []string{
		"title=Mietvertrag mit der Location|authority=Betreiber|notes=Miete, Kaution, Technik, Bar-Regelung, Sperrzeit, Haftung, Endreinigung.",
		"title=Deckt die Konzession der Location die Veranstaltung?|authority=Betreiber / Ordnungs- und Bürgeramt Karlsruhe|notes=Wenn nicht: vorübergehender Gaststättenbetrieb anzeigen bzw. gestatten lassen (Landesgaststättengesetz, Karlsruher Formular und Frist prüfen).",
		"title=Veranstaltung anmelden bzw. mit dem Amt abstimmen|authority=Ordnungs- und Bürgeramt Karlsruhe|notes=Bei Unsicherheit zuerst beim Amt nachfragen. Leitfaden: https://web1.karlsruhe.de/service/Formulare/ordnungsamt/OA3_Veranstaltungsleitfaden_Karlsruhe.pdf",
		"title=Sperrzeit und Lärmschutz klären|authority=Ordnungs- und Bürgeramt Karlsruhe|notes=Sperrzeit nach Landesgaststättengesetz, Verkürzung beantragbar. Nachbarschaft und Auflagen der Location beachten. Ohne Gewähr, mit dem Amt klären.",
		"title=Jugendschutz: Aushang und Alterskontrolle|authority=Ordnungs- und Bürgeramt Karlsruhe",
		"title=GEMA-Anmeldung|authority=GEMA|notes=Oft über die Location geregelt: klären, wer anmeldet.",
		"title=Künstlersozialabgabe prüfen|authority=Künstlersozialkasse",
		"title=Veranstalterhaftpflicht prüfen|authority=Versicherung|notes=Manchmal in der Location-Versicherung enthalten, oft aber nicht.",
	} {
		b.add("permits", p+"|status=todo")
	}
	for _, t := range []string{
		"title=Termin und Location bestätigen|area=Orga|prio=high", "title=Line-up und Gagen klären|area=Orga",
		"title=Flyer und Social Media|area=Orga", "title=Barbedarf bestellen|area=Bar", "title=Schichtplan erstellen|area=Orga",
		"title=Soundcheck und Technik-Rider abstimmen|area=Technik", "title=Kassensturz und Abrechnung|area=Orga",
	} {
		b.add("tasks", t+"|status=open")
	}
	for _, s := range []string{"position=Barkeeper|area=Bar|status=open", "position=Einlass|area=Einlass|status=open", "position=Garderobe|area=Garderobe|status=open", "position=Technik|area=Technik|status=open"} {
		b.add("staff", s)
	}
	for _, t := range []string{"title=Aufbau und Soundcheck", "title=Einlass", "title=Show-Start", "title=Open End / Abbau"} {
		b.add("timeline", t)
	}
	for _, l := range []string{"title=Location-Miete|category=Location|kind=exp", "title=Technik-Miete|category=Technik|kind=exp", "title=Werbung|category=Werbung|kind=exp"} {
		b.add("budget", l+"|qty=1|scale=fix|status=planned")
	}
	out = append(out, &Template{
		Name: "Club Night (Indoor)", Description: "Abend im Club oder einer gemieteten Halle mit kleiner Genehmigungsliste für Karlsruhe / Baden-Württemberg.",
		Payload: TplPayload{Modules: mods, Records: b.recs, Settings: calcSeed(200)},
	})

	// ---- Oktoberfest ----
	b = newTB()
	for _, a := range [][2]string{{"Orga", "#3d3d3d"}, {"Bierstand", "#b4572c"}, {"Küche & Essen", "#7a6a2e"}, {"Zelt & Aufbau", "#55704a"}, {"Kasse & Pfand", "#2f6f73"}, {"Programm & Musik", "#6b4f7a"}, {"Sicherheit & Sanitäts", "#8a2f2f"}} {
		b.area(a[0], a[1])
	}
	for _, p := range []string{
		"title=Veranstaltung anmelden (Veranstaltungsleitfaden)|authority=Ordnungs- und Bürgeramt Karlsruhe|notes=Erste Anlaufstelle für Fest, Zelt und Ausschank. Leitfaden: https://web1.karlsruhe.de/service/Formulare/ordnungsamt/OA3_Veranstaltungsleitfaden_Karlsruhe.pdf",
		"title=Vorübergehender Gaststättenbetrieb (Bier, Speisen)|authority=Ordnungs- und Bürgeramt Karlsruhe – Gaststätten- und Gewerberecht|notes=In Baden-Württemberg Anzeige bzw. Gestattung nach dem Landesgaststättengesetz. Kommunale Formulare nennen meist mindestens zwei Wochen Vorlauf. Aktuelles Karlsruher Formular und Frist beim Amt bestätigen.",
		"title=Festzelt: Fliegender Bau anzeigen / Gebrauchsabnahme|authority=Bauordnungsbehörde der Stadt|notes=Kommunale Formulare nennen mindestens eine Woche Vorlauf vor dem Aufbau. Ausführungsgenehmigung und Prüfbuch vom Zeltverleiher geben lassen.",
		"title=Versammlungsstättenrecht prüfen (VStättVO Baden-Württemberg)|authority=Bauordnungsbehörde der Stadt|notes=Für Zelte mit größerer Besucherzahl relevant, mit der Behörde klären.",
		"title=Brandschutz, Fluchtwege, Zufahrten|authority=Feuerwehr Karlsruhe",
		"title=Sicherheitskonzept|authority=Ordnungs- und Bürgeramt / Polizei|notes=Umfang legt die Stadt im Einzelfall fest.",
		"title=Sanitätsdienst|authority=Hilfsorganisation",
		"title=Sperrzeit und Lärmschutz klären|authority=Ordnungs- und Bürgeramt Karlsruhe|notes=Sperrzeit nach Landesgaststättengesetz, Musik im Zelt und Nachtruhe. Ohne Gewähr, mit dem Amt klären.",
		"title=Lebensmittelhygiene und Belehrung der Helfer|authority=Lebensmittelüberwachung Karlsruhe|notes=Belehrung nach Infektionsschutzgesetz für alle, die mit Speisen arbeiten. Küche und Handwaschmöglichkeit planen.",
		"title=Jugendschutz: Aushang und Kontrollen|authority=Ordnungs- und Bürgeramt Karlsruhe|notes=Besonders bei Bier und Radler wichtig.",
		"title=GEMA-Anmeldung|authority=GEMA|notes=Für Livemusik und Musik vom Band.",
		"title=Müll-, Spül- und Mehrwegkonzept|authority=Amt für Abfallwirtschaft Karlsruhe",
		"title=Veranstalterhaftpflicht|authority=Versicherung",
	} {
		b.add("permits", p+"|status=todo")
	}
	for _, t := range []string{
		"title=Termin, Platz und Zelt klären|area=Orga|prio=high", "title=Brauerei: Fassbier, Kühlung und Zapfanlage|area=Bierstand|prio=high",
		"title=Speisekarte kalkulieren|area=Küche & Essen", "title=Band und Musik buchen|area=Programm & Musik",
		"title=Biertischgarnituren und Geschirr besorgen|area=Zelt & Aufbau", "title=Becher- und Krugpfand organisieren|area=Kasse & Pfand",
		"title=Helfer:innen und Schichten planen|area=Orga", "title=Dekoration (Blau-Weiß, Girlanden)|area=Zelt & Aufbau",
		"title=Abbau und Endreinigung|area=Zelt & Aufbau", "title=Kassensturz und Abrechnung|area=Orga",
	} {
		b.add("tasks", t+"|status=open")
	}
	for _, e := range []string{
		"item=Festzelt|area=Zelt & Aufbau|source=rent|status=need", "item=Biertischgarnituren|area=Zelt & Aufbau|source=borrow|qty=30|status=need",
		"item=Zapfanlage und Kühlung|area=Bierstand|source=rent|status=need", "item=Maßkrüge|area=Bierstand|source=rent|qty=300|status=need",
		"item=Grill / Kochstation|area=Küche & Essen|source=borrow|status=need", "item=Beschallung für Band|area=Programm & Musik|source=rent|status=need",
		"item=Stromverteilung|area=Zelt & Aufbau|source=rent|status=need", "item=Handwaschbecken (Hygiene)|area=Küche & Essen|source=rent|status=need",
	} {
		b.add("equipment", e)
	}
	for _, s := range []string{"position=Zapfen|area=Bierstand|status=open", "position=Service|area=Bierstand|status=open", "position=Küche|area=Küche & Essen|status=open", "position=Pfandrückgabe|area=Kasse & Pfand|status=open", "position=Einlass|area=Sicherheit & Sanitäts|status=open"} {
		b.add("staff", s)
	}
	for _, t := range []string{"title=Aufbau", "title=Fassanstich", "title=Live-Musik", "title=Prämierung / Wettbewerb", "title=Ausklang und Abbau"} {
		b.add("timeline", t)
	}
	for _, l := range []string{"title=Zeltmiete|category=Location|kind=exp", "title=Band|category=Programm|kind=exp", "title=Dekoration|category=Licht & Deko|kind=exp", "title=Sponsoring Brauerei|category=Sponsoring|kind=inc"} {
		b.add("budget", l+"|qty=1|scale=fix|status=planned")
	}
	fest := b.item("name=Festbier (Fass 50 l)|category=Bier|pack=Fass 50 l|unit=l|content=50|price=150|deposit=30|waste=6|returnable=1|notes=Beispielwert, bitte anpassen")
	weizen := b.item("name=Weißbier (Fass 30 l)|category=Bier|pack=Fass 30 l|unit=l|content=30|price=100|deposit=30|waste=6|returnable=1|notes=Beispielwert, bitte anpassen")
	limo := b.item("name=Zitronenlimonade (Kasten 12×1 l)|category=Softdrinks|pack=Kasten 12×1 l|unit=l|content=12|price=10|deposit=3.3|notes=Beispielwert, bitte anpassen")
	brezn := b.item("name=Brezn (Karton 40 Stk)|category=Essen|pack=Karton 40 Stk|unit=Stk|content=40|price=22|notes=Beispielwert, bitte anpassen")
	wurst := b.item("name=Weißwurst (Paket 20 Stk)|category=Essen|pack=Paket 20 Stk|unit=Stk|content=20|price=18|notes=Beispielwert, bitte anpassen")
	hendl := b.item("name=Hendl halb (Karton 20 Stk)|category=Essen|pack=Karton 20 Stk|unit=Stk|content=20|price=70|notes=Beispielwert, bitte anpassen")
	b.prod("name=Festbier Maß 1 l|stand=Bierstand|category=Bier|price=11|pp=2", fest, 1.0)
	b.prod("name=Weißbier 0,5 l|stand=Bierstand|category=Bier|price=5.5|pp=0.6", weizen, 0.5)
	b.prod("name=Radler Maß 1 l|stand=Bierstand|category=Bier|price=10.5|pp=0.5", fest, 0.5, limo, 0.5)
	b.prod("name=Brezn|stand=Foodstand|category=Essen|price=3|pp=0.5", brezn, 1.0)
	b.prod("name=Weißwurst-Paar|stand=Foodstand|category=Essen|price=5|pp=0.3", wurst, 2.0)
	b.prod("name=Hendl halb|stand=Foodstand|category=Essen|price=11|pp=0.3", hendl, 1.0)
	out = append(out, &Template{
		Name: "Kleines Oktoberfest", Description: "Festzelt mit Bierstand, Essen, Pfandsystem und Genehmigungen für Karlsruhe / Baden-Württemberg inkl. Hygiene und Zeltabnahme.",
		Payload: TplPayload{Modules: mods, Records: b.recs, Settings: calcSeed(250)},
	})

	// ---- blank ----
	out = append(out, &Template{
		Name: "Leeres Event", Description: "Alle Module aktiv, sonst nichts vorbelegt.",
		Payload: TplPayload{Modules: mods},
	})
	return out
}

func calcSeed(guests int) map[string]json.RawMessage {
	s := CalcSettings{
		Scenarios: []Scenario{{"Vorsichtig", guests * 6 / 10}, {"Realistisch", guests}, {"Optimistisch", guests * 14 / 10}},
		Tiers:     []Tier{{"Eintritt", 10, 100}}, Baseline: 1, VAT: 19,
	}
	b, _ := json.Marshal(s)
	return map[string]json.RawMessage{"calc": b}
}

var _ = fmt.Sprint
