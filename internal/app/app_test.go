// Copyright (C) 2026 yniverz
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func testApp(t *testing.T) (*App, *Event, *C) {
	t.Helper()
	db, err := openDB(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a := &App{db: db}
	a.seed()
	u, err := a.createUser("admin", "Admin", "passwort-12345", true, true, true)
	if err != nil {
		t.Fatal(err)
	}
	e := &Event{Name: "Test", Status: "idea", Modules: defaultModules(), Settings: map[string]jsonRaw{}}
	if err := a.saveEvent(e); err != nil {
		t.Fatal(err)
	}
	return a, e, &C{A: a, User: u, Event: e}
}

func near(a, b float64) bool { return math.Abs(a-b) < 0.005 }

func TestUnitCostIncludesWaste(t *testing.T) {
	r := &Rec{D: map[string]string{"content": "50", "price": "140", "waste": "10"}}
	if got := unitCostOf(r.N("price"), r); !near(got, 140.0/45.0) {
		t.Fatalf("unitCost = %v", got)
	}
}

func TestBarShoppingListRoundsUpAndBuffers(t *testing.T) {
	a, e, c := testApp(t)
	item := &Rec{EventID: e.ID, Module: "bar_items", D: map[string]string{"name": "Cola", "unit": "Stk", "content": "24", "price": "16"}}
	_ = a.saveRec(item)
	rec := `[{"i":` + itoa(item.ID) + `,"q":1}]`
	_ = a.saveRec(&Rec{EventID: e.ID, Module: "bar_products", D: map[string]string{"name": "Cola", "price": "3", "pp": "1", "recipe": rec}})
	e.setting("bar", BarSettings{Buffer: 0})
	b := c.bar(100) // 100 portions = 4.17 packs -> 5
	if b.Items[0].Buy != 5 {
		t.Fatalf("buy = %d, want 5", b.Items[0].Buy)
	}
	if !near(b.Purchase, 80) || !near(b.Revenue, 300/1.19) { // Verkaufspreis brutto, Auswertung netto
		t.Fatalf("purchase %v revenue %v", b.Purchase, b.Revenue)
	}
	e.setting("bar", BarSettings{Buffer: 10}) // 4.58 -> 5 still
	c.invalidate()
	if b = c.bar(100); b.Items[0].Buy != 5 {
		t.Fatalf("buffered buy = %d", b.Items[0].Buy)
	}
}

func TestReturnableItemsOnlyCostConsumption(t *testing.T) {
	a, e, c := testApp(t)
	item := &Rec{EventID: e.ID, Module: "bar_items", D: map[string]string{"name": "Fass", "unit": "l", "content": "50", "price": "100", "returnable": "1"}}
	_ = a.saveRec(item)
	_ = a.saveRec(&Rec{EventID: e.ID, Module: "bar_products", D: map[string]string{"name": "Bier", "price": "4", "fixqty": "100", "recipe": `[{"i":` + itoa(item.ID) + `,"q":0.5}]`}})
	e.setting("bar", BarSettings{Buffer: 0})
	if b := c.bar(10); !near(b.Purchase, 100) { // 50 l used = exactly one keg
		t.Fatalf("purchase = %v", b.Purchase)
	}
	c.invalidate()
	_ = a.saveRec(&Rec{EventID: e.ID, Module: "bar_products", D: map[string]string{"name": "Bier2", "price": "4", "fixqty": "20", "recipe": `[{"i":` + itoa(item.ID) + `,"q":0.5}]`}})
	c.invalidate()
	if b := c.bar(10); !near(b.Purchase, 120) { // 60 l -> 1.2 kegs paid by usage, not 2 full kegs
		t.Fatalf("purchase = %v", b.Purchase)
	}
}

func TestFinanceAndBreakEven(t *testing.T) {
	a, e, c := testApp(t)
	e.Modules = []string{"budget", "calc"}
	e.setting("calc", CalcSettings{Scenarios: []Scenario{{"A", 100}}, Tiers: []Tier{{"Eintritt", 10, 100}}})
	_ = a.saveRec(&Rec{EventID: e.ID, Module: "budget", D: map[string]string{"title": "Miete", "kind": "exp", "qty": "1", "unit_price": "1000", "scale": "fix"}})
	_ = a.saveRec(&Rec{EventID: e.ID, Module: "budget", D: map[string]string{"title": "Bändchen", "kind": "exp", "qty": "1", "unit_price": "1", "scale": "guest"}})
	f := c.finance(200)
	if !near(f.Income, 2000) || !near(f.Expense, 1200) || !near(f.Profit, 800) {
		t.Fatalf("income %v expense %v profit %v", f.Income, f.Expense, f.Profit)
	}
	// profit(g) = 10g - 1000 - g  => break-even at 1000/9 = 111.1 -> 112
	if be := c.breakEven(); be != 112 {
		t.Fatalf("break-even = %d, want 112", be)
	}
}

func TestVATReducesRevenue(t *testing.T) {
	_, e, c := testApp(t)
	e.Modules = []string{"calc"}
	e.setting("calc", CalcSettings{Scenarios: []Scenario{{"A", 100}}, Tiers: []Tier{{"Eintritt", 11.9, 100}}, VAT: 19})
	if f := c.finance(100); !near(f.Income, 1000) {
		t.Fatalf("net income = %v", f.Income)
	}
}

func TestSnapshotAndInstantiateRemapsRefs(t *testing.T) {
	a, e, _ := testApp(t)
	area := &Rec{EventID: e.ID, Module: "areas", D: map[string]string{"name": "Bar"}}
	_ = a.saveRec(area)
	_ = a.saveRec(&Rec{EventID: e.ID, Module: "tasks", D: map[string]string{"title": "Einkaufen", "area": itoa(area.ID), "status": "done", "due": "2026-01-01"}})
	p := a.snapshot(e, SnapOpts{Amounts: true})
	e2 := &Event{Name: "Neu", Status: "idea", Modules: p.Modules, Settings: map[string]jsonRaw{}}
	_ = a.saveEvent(e2)
	a.instantiate(e2, p, 1)
	tasks := a.recs(e2.ID, "tasks")
	areas := a.recs(e2.ID, "areas")
	if len(tasks) != 1 || len(areas) != 1 {
		t.Fatalf("tasks %d areas %d", len(tasks), len(areas))
	}
	if tasks[0].S("area") != itoa(areas[0].ID) || areas[0].ID == area.ID {
		t.Fatal("area ref not remapped to the new event's area")
	}
	if tasks[0].S("status") != "open" || tasks[0].S("due") != "" {
		t.Fatalf("status/date not reset: %v", tasks[0].D)
	}
}

func TestScopedRoleOnlySeesOwnAreas(t *testing.T) {
	a, e, _ := testApp(t)
	bar := &Rec{EventID: e.ID, Module: "areas", D: map[string]string{"name": "Bar"}}
	tech := &Rec{EventID: e.ID, Module: "areas", D: map[string]string{"name": "Technik"}}
	_ = a.saveRec(bar)
	_ = a.saveRec(tech)
	u, _ := a.createUser("baer", "Bär", "passwort-12345", false, false, false)
	var role *Role
	for _, r := range a.roles() {
		if r.Name == "Bar-Leitung" {
			role = r
		}
	}
	a.setMember(e.ID, u.ID, role.ID, []int64{bar.ID})
	c := &C{A: a, User: u, Event: e, Mem: a.member(e.ID, u.ID)}
	tasks := modByKey["tasks"]
	if !c.visible(tasks, &Rec{D: map[string]string{"area": itoa(bar.ID)}}) {
		t.Fatal("own area should be visible")
	}
	if c.visible(tasks, &Rec{D: map[string]string{"area": itoa(tech.ID)}}) {
		t.Fatal("foreign area must be hidden")
	}
	if c.Level("calc") != 0 || c.Level("bar") != 2 {
		t.Fatalf("levels calc=%d bar=%d", c.Level("calc"), c.Level("bar"))
	}
	if c.finVisible(&Rec{D: map[string]string{"area": itoa(tech.ID)}}, "equipment") {
		t.Fatal("costs of foreign areas must be hidden")
	}
}

func TestNetGrossHandling(t *testing.T) {
	a, e, c := testApp(t)
	e.Modules = []string{"budget"}
	// Standard: Auswertung netto, Eingabe netto, 19 %
	mk := func(d map[string]string) {
		d["title"], d["kind"], d["qty"], d["scale"] = "x", "exp", "1", "fix"
		_ = a.saveRec(&Rec{EventID: e.ID, Module: "budget", D: d})
	}
	mk(map[string]string{"unit_price": "100"})                               // netto -> 100
	mk(map[string]string{"unit_price": "119", "entry": "gross"})             // brutto 19 % -> 100
	mk(map[string]string{"unit_price": "50", "entry": "gross", "vat": "0"})  // Kleinlieferant -> 50
	mk(map[string]string{"unit_price": "107", "entry": "gross", "vat": "7"}) // 7 % -> 100
	if f := c.finance(0); !near(f.Expense, 350) {
		t.Fatalf("net expense = %v, want 350", f.Expense)
	}
	e.setting("tax", TaxSettings{Basis: "gross", Entry: "net", VAT: 19})
	c.invalidate()
	// brutto: 119 + 119 + 50 + 107
	if f := c.finance(0); !near(f.Expense, 395) {
		t.Fatalf("gross expense = %v, want 395", f.Expense)
	}
}

func TestSalePricesAreGrossByDefault(t *testing.T) {
	_, e, c := testApp(t)
	r := &Rec{D: map[string]string{"price": "4.76"}}
	if got := c.saleAmt(4.76, r); !near(got, 4) {
		t.Fatalf("net sale = %v", got)
	}
	r.D["vat"] = "7"
	if got := c.saleAmt(5.35, r); !near(got, 5) {
		t.Fatalf("7%% sale = %v", got)
	}
	e.setting("tax", TaxSettings{Basis: "gross", Entry: "net", VAT: 19})
	if got := c.saleAmt(5.35, r); !near(got, 5.35) {
		t.Fatalf("gross basis keeps gross, got %v", got)
	}
}

func TestCleanFilename(t *testing.T) {
	cases := map[string]string{
		"../../etc/passwd": "passwd",
		`C:\x\Vertrag.pdf`: "Vertrag.pdf",
		"a\"b.txt":         "ab.txt",
		"":                 "datei",
		"..":               "datei",
	}
	for in, want := range cases {
		if got := cleanFilename(in); got != want {
			t.Errorf("cleanFilename(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSessionTokensAreStoredHashed(t *testing.T) {
	a, _, c := testApp(t)
	tok, err := a.newSession(c.User.ID)
	if err != nil {
		t.Fatal(err)
	}
	var n int
	_ = a.db.QueryRow("SELECT COUNT(*) FROM sessions WHERE token=?", tok).Scan(&n)
	if n != 0 {
		t.Fatal("raw token must not be stored")
	}
	if a.session(tok) == nil {
		t.Fatal("session lookup by raw token failed")
	}
}

func TestRelativeDeadlines(t *testing.T) {
	a, e, _ := testApp(t)
	e.Start = "2026-12-12T20:00"
	_ = a.saveEvent(e)
	task := &Rec{EventID: e.ID, Module: "tasks", D: map[string]string{"title": "A", "rel": "42", "due": "2000-01-01"}}
	nd := &Rec{EventID: e.ID, Module: "tasks", D: map[string]string{"title": "B", "due": "2026-05-05"}}
	after := &Rec{EventID: e.ID, Module: "tasks", D: map[string]string{"title": "C", "rel": "-2"}}
	for _, r := range []*Rec{task, nd, after} {
		_ = a.saveRec(r)
	}
	a.applyRelative(e)
	if got := a.rec(task.ID).S("due"); got != "2026-10-31" {
		t.Fatalf("42 days before = %s", got)
	}
	if got := a.rec(after.ID).S("due"); got != "2026-12-14" {
		t.Fatalf("2 days after = %s", got)
	}
	if got := a.rec(nd.ID).S("due"); got != "2026-05-05" {
		t.Fatalf("record without rel must keep its date, got %s", got)
	}
	e.Start = "2027-01-09T20:00" // event moves by 28 days, deadline follows
	a.applyRelative(e)
	if got := a.rec(task.ID).S("due"); got != "2026-11-28" {
		t.Fatalf("after move: %s", got)
	}
}

func TestICSEscapingAndFolding(t *testing.T) {
	if got := icsEscape("a,b;c\nd\n"); got != "a\\,b\\;c\\nd\\n" {
		t.Fatalf("escape = %q", got)
	}
	long := "SUMMARY:" + strings.Repeat("ä", 80)
	for _, ln := range strings.Split(icsFold(long), "\r\n") {
		if len(ln) > 76 { // 75 octets plus the leading space of continuation lines
			t.Fatalf("line too long (%d)", len(ln))
		}
	}
}

func TestCalendarTokenFeed(t *testing.T) {
	a, e, c := testApp(t)
	e.Start = "2026-12-12T20:00"
	e.Modules = []string{"tasks", "timeplan"}
	_ = a.saveEvent(e)
	_ = a.saveRec(&Rec{EventID: e.ID, Module: "tasks", D: map[string]string{"title": "Flyer, drucken", "due": "2026-11-01", "status": "open"}})
	_ = a.saveRec(&Rec{EventID: e.ID, Module: "tasks", D: map[string]string{"title": "Erledigt", "due": "2026-11-02", "status": "done"}})
	tok, err := a.newCalToken(e.ID, c.User.ID)
	if err != nil {
		t.Fatal(err)
	}
	var n int
	_ = a.db.QueryRow("SELECT COUNT(*) FROM cal_tokens WHERE token_hash=?", tok).Scan(&n)
	if n != 0 {
		t.Fatal("calendar token must be stored hashed")
	}
	tok2, _ := a.newCalToken(e.ID, c.User.ID)
	var cnt int
	_ = a.db.QueryRow("SELECT COUNT(*) FROM cal_tokens WHERE token_hash=?", hashToken(tok)).Scan(&cnt)
	if cnt != 0 || tok2 == tok {
		t.Fatal("regenerating must invalidate the old token")
	}
	feed := c.icsFeed()
	if !strings.Contains(feed, "SUMMARY:Aufgabe: Flyer\\, drucken") || strings.Contains(feed, "Erledigt") {
		t.Fatalf("feed content wrong:\n%s", feed)
	}
	if !strings.Contains(feed, "DTSTART;VALUE=DATE:20261101") || !strings.Contains(feed, "END:VCALENDAR") {
		t.Fatalf("feed structure wrong:\n%s", feed)
	}
}

func TestGeoAndGeometryValidation(t *testing.T) {
	if lat, lng, ok := parseGeo("49.0069, 8.4037"); !ok || !near(lat, 49.0069) || !near(lng, 8.4037) {
		t.Fatalf("parseGeo = %v %v %v", lat, lng, ok)
	}
	for _, bad := range []string{"", "49", "91,0", "0,181", "a,b", "1,2,3"} {
		if _, _, ok := parseGeo(bad); ok {
			t.Errorf("parseGeo(%q) should fail", bad)
		}
	}
	if _, ok := validGeom([]byte(`{"t":"poly","p":[[0.1,0.1],[0.5,0.1],[0.5,0.5]]}`), "image"); !ok {
		t.Error("valid polygon rejected")
	}
	for _, bad := range []string{
		`{"t":"poly","p":[[0,0],[1,1]]}`,       // too few points
		`{"t":"point","p":[[0,0],[1,1]]}`,      // too many points
		`{"t":"poly","p":[[0,0],[1,1],[9,9]]}`, // outside image range
		`{"t":"circle","p":[[0,0]]}`,           // unknown type
		`not json`,
	} {
		if _, ok := validGeom([]byte(bad), "image"); ok {
			t.Errorf("validGeom accepted %s", bad)
		}
	}
	if _, ok := validGeom([]byte(`{"t":"point","p":[[49,8]]}`), "map"); !ok {
		t.Error("map point rejected")
	}
	if _, ok := validGeom([]byte(`{"t":"point","p":[[95,8]]}`), "map"); ok {
		t.Error("latitude 95 accepted")
	}
}

func TestImageTypeSniffing(t *testing.T) {
	if imageType([]byte("\x89PNG\r\n\x1a\nxxxx")) != "image/png" || imageType([]byte("\xff\xd8\xff\xe0")) != "image/jpeg" {
		t.Fatal("raster images not recognised")
	}
	for _, bad := range []string{"<svg xmlns=", "<html><script>", "%PDF-1.7", ""} {
		if imageType([]byte(bad)) != "" {
			t.Errorf("%q must not count as an image", bad)
		}
	}
}

func TestCSPAllowsOnlyConfiguredMapHosts(t *testing.T) {
	old := mapCfg
	defer func() { mapCfg = old }()
	mapCfg = MapConfig{Enabled: true, TileURL: "https://{s}.tiles.example.org/{z}/{x}/{y}.png", GeocoderURL: "https://geo.example.org/search"}
	csp := contentSecurityPolicy()
	if !strings.Contains(csp, "img-src 'self' data: blob: https://*.tiles.example.org") || !strings.Contains(csp, "connect-src 'self' https://geo.example.org") {
		t.Fatalf("csp = %s", csp)
	}
	mapCfg.Enabled = false
	if csp := contentSecurityPolicy(); strings.Contains(csp, "example.org") {
		t.Fatalf("maps off must not whitelist hosts: %s", csp)
	}
}

func TestPowerTreeLoadsAndOverload(t *testing.T) {
	a, e, c := testApp(t)
	e.Modules = []string{"power"}
	mk := func(d map[string]string) int64 {
		r := &Rec{EventID: e.ID, Module: "power", D: d}
		_ = a.saveRec(r)
		return r.ID
	}
	src := mk(map[string]string{"name": "Aggregat", "kind": "source", "watts": "10000", "volt": "400"})
	dist := mk(map[string]string{"name": "Verteiler", "kind": "dist", "parent": itoa(src), "volt": "400", "fuse": "16"})
	mk(map[string]string{"name": "PA", "kind": "load", "parent": itoa(dist), "watts": "4000", "qty": "1", "simult": "100"})
	mk(map[string]string{"name": "Licht", "kind": "load", "parent": itoa(dist), "watts": "150", "qty": "10", "simult": "80"}) // 1200 W
	mk(map[string]string{"name": "Frei", "kind": "load", "watts": "500"})
	v := c.powerTree()
	if !near(v.Total, 5700) {
		t.Fatalf("total = %v", v.Total)
	}
	var d, s *PowerRow
	for _, r := range v.Rows {
		switch r.R.S("name") {
		case "Verteiler":
			d = r
		case "Aggregat":
			s = r
		}
	}
	if !near(d.Load, 5200) || d.Tone != "bad" { // 16 A * 400 V * sqrt3 * 0.9 = 9977 W capacity -> 52 % ... must stay under
		if d.Tone == "bad" {
			t.Fatalf("16A/400V distributor should carry 5.2 kW, tone = %s", d.Tone)
		}
	}
	if !near(s.Load, 5200) || s.Pct < 50 || s.Pct > 53 {
		t.Fatalf("source load %v pct %v", s.Load, s.Pct)
	}
	// overload the distributor
	mk(map[string]string{"name": "Kochfeld", "kind": "load", "parent": itoa(dist), "watts": "9000", "qty": "1"})
	c.invalidate()
	if v = c.powerTree(); len(v.Warns) == 0 {
		t.Fatal("overload not reported")
	}
}

func TestPowerNestedLoadsCountForTheDistributor(t *testing.T) {
	a, e, c := testApp(t)
	e.Modules = []string{"power"}
	mk := func(d map[string]string) int64 {
		r := &Rec{EventID: e.ID, Module: "power", D: d}
		_ = a.saveRec(r)
		return r.ID
	}
	src := mk(map[string]string{"name": "Aggregat", "kind": "source", "watts": "5000"})
	dist := mk(map[string]string{"name": "Verteiler", "kind": "dist", "parent": itoa(src)})
	l1 := mk(map[string]string{"name": "Lampe 1", "kind": "load", "parent": itoa(dist), "watts": "100"})
	l2 := mk(map[string]string{"name": "Lampe 2", "kind": "load", "parent": itoa(l1), "watts": "50"})
	mk(map[string]string{"name": "Lampe 3", "kind": "load", "parent": itoa(l2), "watts": "25", "qty": "2", "simult": "50"}) // 25 W
	v := c.powerTree()
	byName := map[string]*PowerRow{}
	for _, r := range v.Rows {
		byName[r.R.S("name")] = r
	}
	if !near(byName["Verteiler"].Load, 175) || !near(byName["Aggregat"].Load, 175) {
		t.Fatalf("distributor %v source %v, want 175", byName["Verteiler"].Load, byName["Aggregat"].Load)
	}
	if !near(byName["Lampe 1"].Load, 175) || !near(byName["Lampe 2"].Load, 75) || !near(byName["Lampe 3"].Load, 25) {
		t.Fatalf("subtree loads: %v %v %v", byName["Lampe 1"].Load, byName["Lampe 2"].Load, byName["Lampe 3"].Load)
	}
	if !near(v.Total, 175) { // not 175+75+25: nothing is counted twice
		t.Fatalf("total = %v, want 175", v.Total)
	}
	if byName["Lampe 2"].Level != 3 || byName["Lampe 3"].Level != 4 {
		t.Fatal("indentation levels wrong")
	}
}

func TestPowerCycleDoesNotHang(t *testing.T) {
	a, e, c := testApp(t)
	a1 := &Rec{EventID: e.ID, Module: "power", D: map[string]string{"name": "A", "kind": "dist"}}
	_ = a.saveRec(a1)
	b1 := &Rec{EventID: e.ID, Module: "power", D: map[string]string{"name": "B", "kind": "dist", "parent": itoa(a1.ID)}}
	_ = a.saveRec(b1)
	a1.D["parent"] = itoa(b1.ID)
	_ = a.saveRec(a1)
	if v := c.powerTree(); len(v.Rows) != 2 {
		t.Fatalf("rows = %d", len(v.Rows))
	}
}

func TestTransportCost(t *testing.T) {
	_, e, c := testApp(t)
	r := &Rec{D: map[string]string{"km": "20", "trips": "2", "roundtrip": "1", "rate": "0.5", "flat": "10"}}
	if km := c.fahrtKm(r); !near(km, 80) {
		t.Fatalf("km = %v", km)
	}
	if got := c.fahrtCost(r); !near(got, 50) {
		t.Fatalf("cost = %v", got)
	}
	if d := haversineKm(49.0069, 8.4037, 48.7758, 9.1829); d < 55 || d > 70 { // Karlsruhe - Stuttgart ~ 62 km
		t.Fatalf("haversine = %v", d)
	}
	e.Modules = []string{"logistics"}
	if f := c.finance(0); f.Expense != 0 {
		t.Fatalf("no trips yet, expense = %v", f.Expense)
	}
}

func TestWeatherWarnings(t *testing.T) {
	if w := weatherWarn(WeatherDay{TMax: 20, TMin: 10, Rain: 0, RainPct: 10, Gust: 20}, defaultLimits()); len(w) != 0 {
		t.Fatalf("calm day warned: %v", w)
	}
	if w := weatherWarn(WeatherDay{TMax: 33, TMin: 20, Rain: 12, RainPct: 80, Gust: 70}, defaultLimits()); len(w) != 3 {
		t.Fatalf("expected rain, wind and heat warnings, got %v", w)
	}
}

func TestLimitsAreConfigurable(t *testing.T) {
	_, e, c := testApp(t)
	d := WeatherDay{TMax: 20, TMin: 10, Rain: 1, RainPct: 40, Gust: 35}
	if w := weatherWarn(d, c.limits()); len(w) != 0 {
		t.Fatalf("defaults must not warn: %v", w)
	}
	e.setting("limits", Limits{RainPct: 30, RainMM: 5, GustKMH: 30, HeatC: 30, ColdC: 3, Density: 3, EscapeW: 0.3, CosPhi: 0.8, PowerWarn: 70, PowerReserve: 30})
	lim := c.limits()
	if w := weatherWarn(d, lim); len(w) != 2 {
		t.Fatalf("stricter limits must warn about rain and wind: %v", w)
	}
	if lim.Density != 3 || lim.CosPhi != 0.8 {
		t.Fatalf("limits not applied: %+v", lim)
	}
	e.setting("limits", Limits{RainPct: -5, GustKMH: 9999, Density: 0})
	if got := c.limits(); got.RainPct != 60 || got.GustKMH != 50 || got.Density != 2 {
		t.Fatalf("invalid values must fall back to defaults: %+v", got)
	}
}

func TestPowerUsesConfiguredReserveAndThreshold(t *testing.T) {
	a, e, c := testApp(t)
	e.Modules = []string{"power"}
	src := &Rec{EventID: e.ID, Module: "power", D: map[string]string{"name": "A", "kind": "source", "watts": "10000"}}
	_ = a.saveRec(src)
	_ = a.saveRec(&Rec{EventID: e.ID, Module: "power", D: map[string]string{"name": "L", "kind": "load", "parent": itoa(src.ID), "watts": "7500"}})
	if v := c.powerTree(); v.Rows[0].Tone != "good" || !near(v.Needed, 7500/0.8) {
		t.Fatalf("defaults: tone %s needed %v", v.Rows[0].Tone, v.Needed)
	}
	e.setting("limits", Limits{RainPct: 60, RainMM: 5, GustKMH: 50, HeatC: 30, ColdC: 3, Density: 2, EscapeW: 0.2, CosPhi: 0.9, PowerWarn: 70, PowerReserve: 50})
	c.invalidate()
	v := c.powerTree()
	if v.Rows[0].Tone != "warn" || !near(v.Needed, 15000) {
		t.Fatalf("configured: tone %s needed %v", v.Rows[0].Tone, v.Needed)
	}
}

func TestNewModulePermissionFallback(t *testing.T) {
	old := map[string]int{"tasks": 2, "equipment": 2, "areas": 1, "permits": 2, "calc": 2}
	for key, want := range map[string]int{"timeplan": 1, "sitemap": 1, "power": 1, "logistics": 1, "checklists": 2, "neighbors": 1, "retro": 1} {
		if got := rolePerm(old, key); got != want {
			t.Errorf("rolePerm(%s) = %d, want %d", key, got, want)
		}
	}
	if rolePerm(map[string]int{"tasks": 2}, "power") != 0 {
		t.Error("no equipment access must mean no power access")
	}
}

func TestDefaultLetterUsesEventData(t *testing.T) {
	a, e, c := testApp(t)
	loc := &Rec{Module: "locations", D: map[string]string{"name": "Waldlichtung"}}
	_ = a.saveRec(loc)
	e.Name, e.Start, e.End, e.LocationID = "Sommerfest", "2026-12-12T20:00", "2026-12-13T02:00", loc.ID
	txt := c.defaultLetter(LetterSettings{Contact: "Mara", Phone: "0123", Until: "01:00"})
	for _, want := range []string{"Sommerfest", "Waldlichtung", "20:00", "Mara", "0123", "spätestens um 01:00 Uhr"} {
		if !strings.Contains(txt, want) {
			t.Errorf("letter misses %q:\n%s", want, txt)
		}
	}
}

func TestChecklistTemplateRoundTrip(t *testing.T) {
	a, e, _ := testApp(t)
	_ = a.saveRec(&Rec{EventID: e.ID, Module: "checklists", D: map[string]string{"list": "Abbau", "title": "Müll", "status": "done"}})
	p := a.snapshot(e, SnapOpts{})
	e2 := &Event{Name: "N", Modules: p.Modules, Settings: map[string]jsonRaw{}}
	_ = a.saveEvent(e2)
	a.instantiate(e2, p, 1)
	got := a.recs(e2.ID, "checklists")
	if len(got) != 1 || got[0].S("status") != "open" {
		t.Fatalf("checklist not reset in template copy: %+v", got)
	}
}

func TestSafeLocalBlocksOpenRedirects(t *testing.T) {
	for _, ok := range []string{"/", "/e/1/m/tasks", "/e/1/m/tasks?f_status=open&q=a"} {
		if !safeLocal(ok) {
			t.Errorf("%q should be allowed", ok)
		}
	}
	for _, bad := range []string{"", "//evil.example", "/\\evil.example", "https://evil.example", "evil", "/a\r\nSet-Cookie: x=1", "/a\tb"} {
		if safeLocal(bad) {
			t.Errorf("%q must be rejected", bad)
		}
	}
	if safeNext("/\\evil.example") != "/" {
		t.Error("safeNext must fall back to /")
	}
}

func TestHexColorValidation(t *testing.T) {
	for _, ok := range []string{"#000000", "#1a2B3c"} {
		if !validHexColor(ok) {
			t.Errorf("%q should be valid", ok)
		}
	}
	for _, bad := range []string{"", "red", "#12345", "#12345g", "red;position:fixed", "#1234567", "url(x)"} {
		if validHexColor(bad) {
			t.Errorf("%q must be invalid", bad)
		}
	}
}

func TestTextLengthLimits(t *testing.T) {
	if maxLen(TText) != 300 || maxLen(TTextarea) != 20000 || maxLen(TMoney) != 0 {
		t.Fatal("unexpected limits")
	}
}

func TestSameOriginBehindProxies(t *testing.T) {
	mk := func(host string, h map[string]string) *http.Request {
		r := httptest.NewRequest("POST", "http://"+host+"/x", nil)
		r.Host = host
		for k, v := range h {
			r.Header.Set(k, v)
		}
		return r
	}
	cases := []struct {
		name string
		r    *http.Request
		want bool
	}{
		{"no origin (non-browser)", mk("kollekt:8080", nil), true},
		{"same host", mk("a.example", map[string]string{"Origin": "https://a.example"}), true},
		{"proxy rewrites host but browser says same-origin", mk("kollekt:8080", map[string]string{"Origin": "https://a.example", "Sec-Fetch-Site": "same-origin"}), true},
		{"proxy sets forwarded host", mk("kollekt:8080", map[string]string{"Origin": "https://a.example", "X-Forwarded-Host": "a.example"}), true},
		{"other site", mk("a.example", map[string]string{"Origin": "https://evil.example"}), false},
		{"browser says cross-site", mk("a.example", map[string]string{"Origin": "https://a.example", "Sec-Fetch-Site": "cross-site"}), false},
		{"origin differs without hints", mk("kollekt:8080", map[string]string{"Origin": "https://a.example"}), false},
	}
	for _, c := range cases {
		if got := sameOrigin(c.r); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

func TestRollEndTreatsEarlierTimeAsNextDay(t *testing.T) {
	cases := []struct {
		start, end, want string
		changed          bool
	}{
		{"2026-12-11T20:00", "2026-12-11T01:00", "2026-12-12T01:00", true}, // 20 to 1 o'clock
		{"2026-12-11T20:00", "2026-12-12T01:00", "2026-12-12T01:00", false},
		{"2026-12-11T20:00", "2026-12-11T23:00", "2026-12-11T23:00", false},
		{"2026-12-11T20:00", "2026-12-10T23:00", "2026-12-10T23:00", false}, // day before stays an error
		{"2026-12-11T20:00", "2026-12-11", "2026-12-11", false},             // date only
	}
	for _, c := range cases {
		got, changed := rollEnd(c.start, c.end)
		if got != c.want || changed != c.changed {
			t.Errorf("rollEnd(%s, %s) = %s %v, want %s %v", c.start, c.end, got, changed, c.want, c.changed)
		}
	}
}

func TestEventDaysWithDifferentTimes(t *testing.T) {
	_, e, _ := testApp(t)
	days := []EventDay{{"2026-12-12", "14:00", "03:00"}, {"2026-12-11", "20:00", "01:00"}}
	e.applyDays([]EventDay{days[1], days[0]})
	if e.Start != "2026-12-11T20:00" || e.End != "2026-12-13T03:00" {
		t.Fatalf("start/end derived wrongly: %s – %s", e.Start, e.End)
	}
	sp := e.daySpans()
	if len(sp) != 2 || sp[0].Start.Day() != 11 || sp[0].End.Day() != 12 || sp[1].End.Hour() != 3 {
		t.Fatalf("spans = %+v", sp)
	}
	if txt := dayText(sp[0]); !strings.Contains(txt, "von 20:00 bis 01:00 Uhr (Folgetag)") {
		t.Fatalf("dayText = %q", txt)
	}
	if e.daysSummary() == "" || !strings.Contains(e.daysSummary(), "20:00–01:00") {
		t.Fatalf("summary = %q", e.daysSummary())
	}
	// without day rows the plain span is used
	e.applyDays(nil)
	e.Start, e.End = "2026-12-11T20:00", "2026-12-14T02:00"
	if sp := e.daySpans(); len(sp) != 1 || sp[0].End.Day() != 14 {
		t.Fatalf("fallback span = %+v", sp)
	}
	if !strings.Contains(dayText(e.daySpans()[0]), "bis Mo, 14.12.2026") {
		t.Fatalf("multi-day text = %q", dayText(e.daySpans()[0]))
	}
}

func TestInvalidDayRowsAreRejected(t *testing.T) {
	mk := func(date, from, to string) *http.Request {
		r := httptest.NewRequest("POST", "/", strings.NewReader("day_date="+date+"&day_from="+from+"&day_to="+to))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		return r
	}
	if d, msg := parseDayRows(mk("2026-12-11", "20:00", "01:00")); msg != "" || len(d) != 1 {
		t.Fatalf("valid row rejected: %v %s", d, msg)
	}
	for _, bad := range [][3]string{{"2026-13-40", "20:00", "01:00"}, {"2026-12-11", "25:00", "01:00"}, {"2026-12-11", "20:00", "abc"}, {"", "20:00", "01:00"}} {
		if _, msg := parseDayRows(mk(bad[0], bad[1], bad[2])); msg == "" {
			t.Errorf("%v must be rejected", bad)
		}
	}
}

func TestLetterListsEveryDay(t *testing.T) {
	_, e, c := testApp(t)
	e.Name = "Festival"
	e.applyDays([]EventDay{{"2026-12-11", "20:00", "01:00"}, {"2026-12-12", "14:00", "03:00"}})
	txt := c.defaultLetter(LetterSettings{})
	for _, want := range []string{"An mehreren Tagen", "11.12.2026 von 20:00 bis 01:00 Uhr (Folgetag)", "12.12.2026 von 14:00 bis 03:00 Uhr (Folgetag)"} {
		if !strings.Contains(txt, want) {
			t.Errorf("letter misses %q:\n%s", want, txt)
		}
	}
}

func TestICSHasOneEventPerDay(t *testing.T) {
	_, e, c := testApp(t)
	e.Name = "Festival"
	e.Modules = []string{"timeplan"}
	e.applyDays([]EventDay{{"2026-12-11", "20:00", "01:00"}, {"2026-12-12", "14:00", "03:00"}})
	feed := c.icsFeed()
	if n := strings.Count(feed, "SUMMARY:Festival (Tag"); n != 2 {
		t.Fatalf("expected 2 day events, got %d:\n%s", n, feed)
	}
	if !strings.Contains(feed, "DTSTART:20261211T200000") || !strings.Contains(feed, "DTEND:20261212T010000") || !strings.Contains(feed, "DTSTART:20261212T140000") {
		t.Fatalf("times wrong:\n%s", feed)
	}
}

func TestEmptyPlanDataIsValidJSONLists(t *testing.T) {
	a, e, c := testApp(t)
	plan := &Rec{EventID: e.ID, Module: "siteplans", D: map[string]string{"name": "Leer", "mode": "map"}}
	_ = a.saveRec(plan)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.SetPathValue("pid", itoa(plan.ID))
	c.W, c.R = rec, req
	c.handleSiteData(rec, req)
	var out struct {
		Items, Areas, Kinds []any
		Raw                 map[string]json.RawMessage
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out.Raw); err != nil {
		t.Fatalf("not json: %v\n%s", err, rec.Body.String())
	}
	for _, k := range []string{"items", "areas", "kinds"} {
		if string(out.Raw[k]) == "null" || len(out.Raw[k]) == 0 || out.Raw[k][0] != '[' {
			t.Errorf("%s must be a JSON array, got %s", k, out.Raw[k])
		}
	}
}

func TestNumberParsing(t *testing.T) {
	cases := map[string]float64{"1,5": 1.5, "1.234,56": 1234.56, "3.5": 3.5, "": 0, " 12 ": 12}
	for in, want := range cases {
		if got := parseNum(in); !near(got, want) {
			t.Errorf("parseNum(%q) = %v, want %v", in, got, want)
		}
	}
	if validNum("abc") || !validNum("1,5") {
		t.Fatal("validNum")
	}
}
