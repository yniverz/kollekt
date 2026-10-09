// Copyright (C) 2026 yniverz
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"math"
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
