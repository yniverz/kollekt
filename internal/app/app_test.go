package app

import (
	"math"
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
	if got := unitCost(r); !near(got, 140.0/45.0) {
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
	if !near(b.Purchase, 80) || !near(b.Revenue, 300) {
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
