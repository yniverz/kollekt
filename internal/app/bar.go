package app

import (
	"encoding/json"
	"math"
	"net/http"
	"sort"
	"strings"
)

type RecipeLine struct {
	I int64   `json:"i"`
	Q float64 `json:"q"`
}

type BarSettings struct {
	Buffer float64 `json:"buffer"`
}

func (c *C) barSettings() BarSettings {
	s := BarSettings{Buffer: 10}
	var tmp BarSettings
	if c.Event.getSetting("bar", &tmp) {
		s = tmp
	}
	return s
}

func parseRecipe(s string) []RecipeLine {
	var out []RecipeLine
	if strings.TrimSpace(s) == "" {
		return nil
	}
	_ = json.Unmarshal([]byte(s), &out)
	return out
}

func (c *C) cleanRecipe(raw string) string {
	lines := parseRecipe(raw)
	var out []RecipeLine
	ok := map[int64]bool{}
	for _, it := range c.Recs("bar_items") {
		ok[it.ID] = true
	}
	for _, l := range lines {
		if ok[l.I] && l.Q > 0 && !math.IsNaN(l.Q) && !math.IsInf(l.Q, 0) {
			out = append(out, l)
		}
	}
	if len(out) == 0 {
		return ""
	}
	b, _ := json.Marshal(out)
	return string(b)
}

func (c *C) recipeItemsJSON() string {
	type it struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
		Unit string `json:"unit"`
	}
	var out []it
	for _, r := range c.Recs("bar_items") {
		out = append(out, it{r.ID, r.S("name"), r.S("unit")})
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	b, _ := json.Marshal(out)
	return string(b)
}

// unitCost = purchase price per used unit (incl. waste).
func unitCost(r *Rec) float64 {
	eff := r.N("content") * (1 - r.N("waste")/100)
	if eff <= 0 {
		return 0
	}
	return r.N("price") / eff
}

type BarProd struct {
	R         *Rec
	Stand     string
	Qty       float64
	Rev       float64
	CostPer   float64
	Cost      float64
	Margin    float64
	MarginPct float64
	Sold      float64
	HasSold   bool
	SoldRev   float64
	Recipe    []RecipeLine
	Warn      string
}

type BarItem struct {
	R        *Rec
	Need     float64
	PacksRaw float64
	Buy      int
	Have     float64
	Cost     float64
	Deposit  float64
	Supplier string
}

type StandSum struct {
	Name                       string
	Revenue, SoldRevenue, Cost float64
	Prods                      int
}

type BarResult struct {
	G        int
	Prods    []*BarProd
	Items    []*BarItem
	Revenue  float64
	Ware     float64 // consumption-based cost of goods
	Purchase float64 // cash purchase
	Deposit  float64
	HasSold  bool
	SoldRev  float64
	Buffer   float64
	Stands   map[string]*StandSum
	PerGuest float64
	Drinks   float64
}

func (b *BarResult) stands() []*StandSum {
	var out []*StandSum
	for _, s := range b.Stands {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (c *C) bar(G int) *BarResult {
	set := c.barSettings()
	b := &BarResult{G: G, Buffer: set.Buffer, Stands: map[string]*StandSum{}}
	items := map[int64]*Rec{}
	for _, it := range c.Recs("bar_items") {
		items[it.ID] = it
	}
	need := map[int64]float64{}
	for _, p := range c.Recs("bar_products") {
		bp := &BarProd{R: p, Stand: p.S("stand"), Recipe: parseRecipe(p.S("recipe"))}
		if bp.Stand == "" {
			bp.Stand = "Bar"
		}
		if p.S("fixqty") != "" {
			bp.Qty = p.N("fixqty")
		} else {
			bp.Qty = math.Round(p.N("pp") * float64(G))
		}
		for _, l := range bp.Recipe {
			if it := items[l.I]; it != nil {
				bp.CostPer += l.Q * unitCost(it)
				need[l.I] += l.Q * bp.Qty
			}
		}
		price := p.N("price")
		bp.Rev = bp.Qty * price
		bp.Cost = bp.Qty * bp.CostPer
		bp.Margin = price - bp.CostPer
		if price > 0 {
			bp.MarginPct = bp.Margin / price * 100
		}
		if p.S("sold") != "" {
			bp.HasSold = true
			bp.Sold = p.N("sold")
			bp.SoldRev = bp.Sold * price
			b.HasSold = true
		}
		if len(bp.Recipe) == 0 {
			bp.Warn = "Kein Rezept – Kosten unbekannt"
		} else if bp.Margin < 0 {
			bp.Warn = "Verkaufspreis unter Kosten"
		}
		st := b.Stands[bp.Stand]
		if st == nil {
			st = &StandSum{Name: bp.Stand}
			b.Stands[bp.Stand] = st
		}
		st.Revenue += bp.Rev
		st.Cost += bp.Cost
		st.SoldRevenue += bp.SoldRev
		st.Prods++
		b.Revenue += bp.Rev
		b.Ware += bp.Cost
		b.SoldRev += bp.SoldRev
		b.Drinks += p.N("pp")
		b.Prods = append(b.Prods, bp)
	}
	for _, it := range c.Recs("bar_items") {
		n := need[it.ID]
		bi := &BarItem{R: it, Need: n, Have: it.N("have")}
		eff := it.N("content") * (1 - it.N("waste")/100)
		if eff > 0 {
			bi.PacksRaw = n / eff
		}
		buf := bi.PacksRaw * (1 + set.Buffer/100)
		buy := math.Ceil(buf - bi.Have - 1e-9)
		if buy < 0 {
			buy = 0
		}
		bi.Buy = int(buy)
		if it.B("returnable") {
			bi.Cost = math.Max(0, bi.PacksRaw-bi.Have) * it.N("price")
		} else {
			bi.Cost = float64(bi.Buy) * it.N("price")
		}
		bi.Deposit = float64(bi.Buy) * it.N("deposit")
		bi.Supplier = c.RefTitle("contacts", it.I("supplier"))
		b.Purchase += bi.Cost
		b.Deposit += bi.Deposit
		b.Items = append(b.Items, bi)
	}
	if G > 0 {
		b.PerGuest = b.Revenue / float64(G)
	}
	sort.SliceStable(b.Prods, func(i, j int) bool {
		if b.Prods[i].Stand != b.Prods[j].Stand {
			return b.Prods[i].Stand < b.Prods[j].Stand
		}
		return strings.ToLower(b.Prods[i].R.S("name")) < strings.ToLower(b.Prods[j].R.S("name"))
	})
	sort.SliceStable(b.Items, func(i, j int) bool {
		a, bb := b.Items[i].R.S("category"), b.Items[j].R.S("category")
		if a != bb {
			return a < bb
		}
		return strings.ToLower(b.Items[i].R.S("name")) < strings.ToLower(b.Items[j].R.S("name"))
	})
	return b
}

func (c *C) handleBar(w http.ResponseWriter, r *http.Request) {
	if c.Level("bar") < 1 || !c.Event.Has("bar") {
		c.Error(403, "Kein Zugriff auf Bar & Verkauf.")
		return
	}
	tab := r.URL.Query().Get("tab")
	if tab != "buy" && tab != "list" && tab != "eval" {
		tab = "sell"
	}
	G := c.Baseline()
	b := c.bar(G)
	var articles []*Rec
	if tab == "buy" && c.CanEdit("bar") {
		articles = c.A.recs(0, "articles")
		sort.Slice(articles, func(i, j int) bool {
			return strings.ToLower(articles[i].S("name")) < strings.ToLower(articles[j].S("name"))
		})
	}
	// group shopping list by supplier
	type SupGroup struct {
		Name  string
		Items []*BarItem
		Cost  float64
	}
	var sups []*SupGroup
	if tab == "list" {
		idx := map[string]*SupGroup{}
		for _, it := range b.Items {
			if it.PacksRaw <= 0 && it.Buy == 0 {
				continue
			}
			n := it.Supplier
			if n == "" {
				n = "Ohne Lieferant"
			}
			g := idx[n]
			if g == nil {
				g = &SupGroup{Name: n}
				idx[n] = g
				sups = append(sups, g)
			}
			g.Items = append(g.Items, it)
			g.Cost += it.Cost
		}
		sort.Slice(sups, func(i, j int) bool { return sups[i].Name < sups[j].Name })
	}
	unused := 0
	for _, it := range b.Items {
		if it.Need == 0 {
			unused++
		}
	}
	cs := c.calcSettings()
	data := map[string]any{
		"Title": "Bar & Verkauf", "Nav": c.eventNav("bar"), "Tab": tab, "B": b, "G": G, "Articles": articles, "Sups": sups,
		"CanEdit": c.CanEdit("bar"), "Unused": unused, "Scenario": cs.Scenarios[cs.Baseline].Name, "Base": "/e/" + itoa(c.Event.ID) + "/bar",
		"HasCalc": c.Event.Has("calc"),
	}
	c.Page("bar.html", data)
}

func (c *C) handleBarSettings(w http.ResponseWriter, r *http.Request) {
	if !c.CanEdit("bar") {
		c.Error(403, "Keine Berechtigung.")
		return
	}
	v := math.Min(100, math.Max(0, parseNum(r.FormValue("buffer"))))
	c.Event.setting("bar", BarSettings{Buffer: v})
	_ = c.A.saveEvent(c.Event)
	c.setFlash("Sicherheitspuffer gespeichert.")
	c.Redirect("/e/" + itoa(c.Event.ID) + "/bar?tab=list")
}

// importArticles copies catalog articles into the event's purchase items.
func (c *C) handleBarImport(w http.ResponseWriter, r *http.Request) {
	if !c.CanEdit("bar") {
		c.Error(403, "Keine Berechtigung.")
		return
	}
	_ = r.ParseForm()
	n := 0
	for _, idStr := range r.Form["article"] {
		a := c.A.rec(int64(parseNum(idStr)))
		if a == nil || a.Module != "articles" {
			continue
		}
		d := map[string]string{}
		for k, v := range a.D {
			d[k] = v
		}
		d["catalog"] = itoa(a.ID)
		if c.A.saveRec(&Rec{EventID: c.Event.ID, Module: "bar_items", D: d, CreatedBy: c.User.ID}) == nil {
			n++
		}
	}
	c.setFlash(itoa(int64(n)) + " Artikel übernommen.")
	c.Redirect("/e/" + itoa(c.Event.ID) + "/bar?tab=buy")
}

// toCatalog saves an event item into the global article catalog.
func (c *C) handleBarToCatalog(w http.ResponseWriter, r *http.Request) {
	if !c.CanEdit("bar") || !(c.User.IsAdmin || c.User.CanMaster) {
		c.Error(403, "Keine Berechtigung.")
		return
	}
	it := c.A.rec(pathInt(r, "rid"))
	if it == nil || it.Module != "bar_items" || it.EventID != c.Event.ID {
		c.Error(404, "Artikel nicht gefunden.")
		return
	}
	d := map[string]string{}
	for k, v := range it.D {
		if k == "have" || k == "catalog" || k == "supplier" {
			continue
		}
		d[k] = v
	}
	if id := it.I("catalog"); id != 0 {
		if ex := c.A.rec(id); ex != nil && ex.Module == "articles" {
			for k, v := range d {
				ex.D[k] = v
			}
			_ = c.A.saveRec(ex)
			c.setFlash("Artikelstamm aktualisiert.")
			c.Redirect("/e/" + itoa(c.Event.ID) + "/bar?tab=buy")
			return
		}
	}
	nr := &Rec{Module: "articles", D: d, CreatedBy: c.User.ID}
	_ = c.A.saveRec(nr)
	it.D["catalog"] = itoa(nr.ID)
	_ = c.A.saveRec(it)
	c.setFlash("In den Artikelstamm übernommen.")
	c.Redirect("/e/" + itoa(c.Event.ID) + "/bar?tab=buy")
}
