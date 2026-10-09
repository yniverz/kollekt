package app

import (
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

// ---------- module resolution & permissions ----------

func (c *C) modBase(m *Module) string {
	if m.Global {
		return "/g/" + m.Key
	}
	return fmt.Sprintf("/e/%d/m/%s", c.Event.ID, m.Key)
}

func (c *C) modListURL(m *Module) string {
	if m.Key == "bar_items" || m.Key == "bar_products" {
		return fmt.Sprintf("/e/%d/bar", c.Event.ID)
	}
	if m.Page != "" {
		return fmt.Sprintf("/e/%d/%s", c.Event.ID, m.Page)
	}
	return c.modBase(m)
}

// resolveModule validates the {mod} path parameter against scope and permissions.
func (c *C) resolveModule(needEdit bool) *Module {
	m := modByKey[c.R.PathValue("mod")]
	if m == nil {
		c.Error(404, "Modul nicht gefunden.")
		return nil
	}
	if m.Global {
		if c.Event != nil {
			c.Error(404, "Modul nicht gefunden.")
			return nil
		}
		if needEdit && !(c.User.IsAdmin || c.User.CanMaster) {
			c.Error(403, "Keine Berechtigung, Stammdaten zu bearbeiten.")
			return nil
		}
		return m
	}
	if c.Event == nil || m.Key == "overview" || len(m.Fields) == 0 {
		c.Error(404, "Modul nicht gefunden.")
		return nil
	}
	parent := m.PermKey()
	if !m.Core && !c.Event.Has(parent) && !c.Event.Has(m.Key) {
		c.Error(404, "Dieses Modul ist für das Event nicht aktiviert.")
		return nil
	}
	need := 1
	if needEdit {
		need = 2
	}
	if c.Level(parent) < need {
		c.Error(403, "Dafür fehlt dir die Berechtigung.")
		return nil
	}
	return m
}

func (c *C) canEditModule(m *Module) bool {
	if m.Global {
		return c.User.IsAdmin || c.User.CanMaster
	}
	return c.Level(m.PermKey()) >= 2
}

// visible applies area scoping for restricted roles.
func (c *C) visible(m *Module, r *Rec) bool {
	if !c.scoped() || m.AreaField == "" {
		return true
	}
	if c.Mem.InArea(r.I(m.AreaField)) {
		return true
	}
	if m.Key == "tasks" && c.User.ContactID != 0 && r.I("assignee") == c.User.ContactID {
		return true
	}
	return false
}

func (c *C) finVisible(r *Rec, modKey string) bool {
	if c.User.IsAdmin {
		return true
	}
	if c.Level("fin") >= 1 {
		return true
	}
	if c.scoped() {
		if m := modByKey[modKey]; m != nil && m.AreaField != "" && c.Mem.InArea(r.I(m.AreaField)) {
			return true
		}
	}
	return false
}

// ---------- list ----------

type Cell struct {
	Kind  string // text, badge, money, number, bool, quick, link, empty, ref
	Text  string
	Color string
	Key   string
	Val   string
	Opts  []Opt
	URL   string
	Num   bool
	N     int
}

type Row struct {
	R     *Rec
	Title string
	Cells []Cell
	Edit  bool
	Muted bool
	Acts  []Action
	Warn  string
}

type Group struct {
	Label string
	Rows  []*Row
	Sums  []string
}

type FilterView struct {
	Key, Label string
	Opts       []FOpt
}
type FOpt struct {
	V, L string
	Sel  bool
}

type Col struct {
	Label string
	Num   bool
}

func (c *C) cellFor(m *Module, f *Field, r *Rec) Cell {
	v := r.S(f.Key)
	if f.Fin && !c.finVisible(r, m.PermKey()) && !m.Global {
		return Cell{Kind: "empty"}
	}
	switch f.Type {
	case TSelect:
		if f.Quick && c.canEditModule(m) {
			return Cell{Kind: "quick", Key: f.Key, Val: v, Opts: f.Opts}
		}
		if v == "" {
			return Cell{Kind: "empty"}
		}
		l, col := optLabel(f.Opts, v)
		return Cell{Kind: "badge", Text: l, Color: col}
	case TMoney:
		if v == "" {
			return Cell{Kind: "empty", Num: true}
		}
		return Cell{Kind: "money", Text: fmtEUR(parseNum(v)), Num: true}
	case TNumber, TPercent:
		if v == "" {
			return Cell{Kind: "empty", Num: true}
		}
		t := fmtNum(parseNum(v))
		if f.Type == TPercent {
			t += " %"
		}
		return Cell{Kind: "number", Text: t, Num: true}
	case TBool:
		return Cell{Kind: "bool", Val: v}
	case TDate:
		if v == "" {
			return Cell{Kind: "empty"}
		}
		col := ""
		if d, ok := daysUntil(v); ok && d < 0 && m.DoneField != "" && !contains(m.DoneVals, r.S(m.DoneField)) {
			col = "late"
		}
		return Cell{Kind: "text", Text: fmtDate(v), Color: col}
	case TDateTime:
		if v == "" {
			return Cell{Kind: "empty"}
		}
		return Cell{Kind: "text", Text: fmtDT(v)}
	case TRef:
		t := c.RefTitle(f.Ref, int64(parseNum(v)))
		if t == "" {
			return Cell{Kind: "empty"}
		}
		return Cell{Kind: "ref", Text: t}
	case TURL:
		if v == "" {
			return Cell{Kind: "empty"}
		}
		return Cell{Kind: "link", Text: strings.TrimPrefix(strings.TrimPrefix(v, "https://"), "http://"), URL: v}
	case TColor, TRecipe, TTextarea:
		return Cell{Kind: "empty"}
	}
	return Cell{Kind: "text", Text: v}
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func (c *C) sortKey(m *Module, f *Field, r *Rec) (string, float64) {
	v := r.S(f.Key)
	switch f.Type {
	case TMoney, TNumber, TPercent:
		return "", parseNum(v)
	case TSelect:
		for i, o := range f.Opts {
			if o.V == v {
				return "", float64(i)
			}
		}
		return "", 999
	case TRef:
		return strings.ToLower(c.RefTitle(f.Ref, int64(parseNum(v)))), 0
	}
	return strings.ToLower(v), 0
}

func (c *C) sortRecs(m *Module, recs []*Rec) {
	key := m.Sort
	desc := false
	if strings.HasPrefix(key, "-") {
		key, desc = key[1:], true
	}
	if key == "" {
		return
	}
	if key == "updated" {
		sort.SliceStable(recs, func(i, j int) bool {
			if desc {
				return recs[i].Updated.After(recs[j].Updated)
			}
			return recs[i].Updated.Before(recs[j].Updated)
		})
		return
	}
	f := m.Field(key)
	if f == nil {
		return
	}
	sort.SliceStable(recs, func(i, j int) bool {
		si, ni := c.sortKey(m, f, recs[i])
		sj, nj := c.sortKey(m, f, recs[j])
		ei, ej := recs[i].S(key) == "", recs[j].S(key) == ""
		if ei != ej {
			return ej // empty values last
		}
		var less bool
		if si != sj {
			less = si < sj
		} else {
			less = ni < nj
		}
		if desc {
			return !less && (si != sj || ni != nj)
		}
		return less
	})
}

func (c *C) buildRows(m *Module, recs []*Rec) []*Row {
	editable := c.canEditModule(m)
	var counts map[int64]int
	if c.attachable(m) {
		var evID int64
		if !m.Global {
			evID = c.Event.ID
		}
		counts = c.A.fileCounts(evID, m.Key)
	}
	var rows []*Row
	for _, r := range recs {
		row := &Row{R: r, Title: c.A.recTitle(c, m, r), Edit: editable}
		for i := range m.Fields {
			f := &m.Fields[i]
			if !f.InList {
				continue
			}
			cell := c.cellFor(m, f, r)
			if f.Key == m.Title {
				cell = Cell{Kind: "title", Text: row.Title, N: counts[r.ID]}
			}
			row.Cells = append(row.Cells, cell)
		}
		for _, v := range m.Virt {
			t := v.Fn(c, r)
			cell := Cell{Kind: "text", Text: t}
			switch v.Type {
			case TMoney:
				cell = Cell{Kind: "money", Text: fmtEUR(parseNum(t)), Num: true}
				if t == "" {
					cell = Cell{Kind: "empty", Num: true}
				}
			case TNumber:
				cell = Cell{Kind: "number", Text: fmtNum(parseNum(t)), Num: true}
				if t == "" || t == "0" {
					cell = Cell{Kind: "empty", Num: true}
				}
			default:
				if t == "" {
					cell = Cell{Kind: "empty"}
				}
			}
			row.Cells = append(row.Cells, cell)
		}
		if m.DoneField != "" && contains(m.DoneVals, r.S(m.DoneField)) {
			row.Muted = true
		}
		if editable {
			for _, a := range m.Actions {
				if c.Level(m.PermKey()) >= a.Level {
					row.Acts = append(row.Acts, a)
				}
			}
		}
		rows = append(rows, row)
	}
	return rows
}

func (c *C) matchQuery(m *Module, r *Rec, q string) bool {
	if q == "" {
		return true
	}
	q = strings.ToLower(q)
	if strings.Contains(strings.ToLower(c.A.recTitle(c, m, r)), q) {
		return true
	}
	for i := range m.Fields {
		f := &m.Fields[i]
		v := r.S(f.Key)
		if f.Type == TRef {
			v = c.RefTitle(f.Ref, int64(parseNum(v)))
		} else if f.Type == TSelect {
			v, _ = optLabel(f.Opts, v)
		} else if f.Type == TRecipe {
			continue
		}
		if strings.Contains(strings.ToLower(v), q) {
			return true
		}
	}
	return false
}

func (c *C) listData(m *Module) map[string]any {
	q := c.R.URL.Query()
	search := strings.TrimSpace(q.Get("q"))
	showDone := q.Get("done") == "1"
	var recs []*Rec
	for _, r := range c.Recs(m.Key) {
		if !c.visible(m, r) {
			continue
		}
		if !c.matchQuery(m, r, search) {
			continue
		}
		ok := true
		for _, fk := range m.Filters {
			if want := q.Get("f_" + fk); want != "" && r.S(fk) != want {
				ok = false
			}
		}
		if !ok {
			continue
		}
		if m.DoneField != "" && !showDone && contains(m.DoneVals, r.S(m.DoneField)) && q.Get("f_"+m.DoneField) == "" {
			continue
		}
		recs = append(recs, r)
	}
	c.sortRecs(m, recs)
	rows := c.buildRows(m, recs)

	// filters UI
	var filters []FilterView
	for _, fk := range m.Filters {
		f := m.Field(fk)
		if f == nil {
			continue
		}
		fv := FilterView{Key: "f_" + fk, Label: f.Label}
		cur := q.Get("f_" + fk)
		switch f.Type {
		case TSelect:
			for _, o := range f.Opts {
				fv.Opts = append(fv.Opts, FOpt{o.V, o.L, o.V == cur})
			}
		case TRef:
			for _, r := range c.Recs(f.Ref) {
				fv.Opts = append(fv.Opts, FOpt{itoa(r.ID), c.RefTitle(f.Ref, r.ID), itoa(r.ID) == cur})
			}
			sort.Slice(fv.Opts, func(i, j int) bool { return fv.Opts[i].L < fv.Opts[j].L })
		default:
			seen := map[string]bool{}
			for _, r := range c.Recs(m.Key) {
				if v := r.S(fk); v != "" && !seen[v] {
					seen[v] = true
					fv.Opts = append(fv.Opts, FOpt{v, v, v == cur})
				}
			}
			sort.Slice(fv.Opts, func(i, j int) bool { return fv.Opts[i].L < fv.Opts[j].L })
		}
		filters = append(filters, fv)
	}

	// headers
	var cols []Col
	for i := range m.Fields {
		if m.Fields[i].InList {
			cols = append(cols, Col{m.Fields[i].Label, m.Fields[i].Type == TMoney || m.Fields[i].Type == TNumber || m.Fields[i].Type == TPercent})
		}
	}
	for _, v := range m.Virt {
		cols = append(cols, Col{v.Label, v.Type == TMoney || v.Type == TNumber})
	}

	// sums per column
	sumOf := func(rs []*Row) []string {
		var sums []string
		any := false
		for i := range m.Fields {
			f := &m.Fields[i]
			if !f.InList {
				continue
			}
			if f.Sum {
				t := 0.0
				for _, r := range rs {
					if r.Cells[len(sums)].Kind != "empty" {
						t += r.R.N(f.Key)
					}
				}
				sums = append(sums, fmtEUR(t))
				any = true
			} else {
				sums = append(sums, "")
			}
		}
		for _, v := range m.Virt {
			if v.Sum {
				t := 0.0
				for _, r := range rs {
					t += parseNum(v.Fn(c, r.R))
				}
				sums = append(sums, fmtEUR(t))
				any = true
			} else {
				sums = append(sums, "")
			}
		}
		if !any {
			return nil
		}
		return sums
	}

	var groups []*Group
	if m.Group != "" {
		gf := m.Field(m.Group)
		idx := map[string]*Group{}
		var order []string
		label := func(r *Rec) (string, string) {
			v := r.S(m.Group)
			switch {
			case gf != nil && gf.Type == TSelect:
				l, _ := optLabel(gf.Opts, v)
				return v, l
			case gf != nil && gf.Type == TRef:
				t := c.RefTitle(gf.Ref, int64(parseNum(v)))
				if t == "" {
					t = "Ohne Zuordnung"
				}
				return v, t
			}
			if v == "" {
				return v, "Ohne Angabe"
			}
			return v, v
		}
		for _, r := range rows {
			k, l := label(r.R)
			g := idx[k]
			if g == nil {
				g = &Group{Label: l}
				idx[k] = g
				order = append(order, k)
			}
			g.Rows = append(g.Rows, r)
		}
		if gf != nil && gf.Type == TSelect {
			sort.SliceStable(order, func(i, j int) bool {
				oi, oj := 999, 999
				for n, o := range gf.Opts {
					if o.V == order[i] {
						oi = n
					}
					if o.V == order[j] {
						oj = n
					}
				}
				return oi < oj
			})
		}
		for _, k := range order {
			idx[k].Sums = sumOf(idx[k].Rows)
			groups = append(groups, idx[k])
		}
	} else {
		groups = []*Group{{Rows: rows}}
	}

	data := map[string]any{
		"M": m, "Groups": groups, "Filters": filters, "Cols": cols, "Q": search, "ShowDone": showDone,
		"Total": len(rows), "Sums": sumOf(rows), "CanEdit": c.canEditModule(m), "Base": c.modBase(m),
		"Title": m.Name, "ListURL": c.R.URL.RequestURI(), "HasDone": m.DoneField != "",
		"FilterActive": search != "" || anyFilter(q, m.Filters),
	}
	if m.Extra != nil {
		data["Extra"] = m.Extra(c)
	}
	return data
}

func anyFilter(q url.Values, keys []string) bool {
	for _, k := range keys {
		if q.Get("f_"+k) != "" {
			return true
		}
	}
	return false
}

func (c *C) handleList(w http.ResponseWriter, r *http.Request) {
	m := c.resolveModule(false)
	if m == nil {
		return
	}
	data := c.listData(m)
	if c.Event != nil {
		data["Nav"] = c.eventNav(m.PermKey())
	}
	if m.Global {
		data["Global"] = true
		data["GNav"] = m.Key
	}
	c.Page("list.html", data)
}

// ---------- form ----------

type RefOpt struct {
	ID    string
	Title string
	Sel   bool
}

type FieldView struct {
	F       *Field
	Value   string
	Opts    []RefOpt
	Err     string
	Suggest []string
	Items   string // recipe: JSON of available items
	Show    bool
	Display string
}

func (c *C) refOptions(f *Field, cur string) []RefOpt {
	var out []RefOpt
	rm := modByKey[f.Ref]
	for _, r := range c.Recs(f.Ref) {
		if rm != nil && !c.visible(rm, r) && itoa(r.ID) != cur {
			continue
		}
		out = append(out, RefOpt{itoa(r.ID), c.RefTitle(f.Ref, r.ID), itoa(r.ID) == cur})
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Title) < strings.ToLower(out[j].Title) })
	return out
}

func (c *C) fieldViews(m *Module, rec *Rec, errs map[string]string, vals map[string]string) []*FieldView {
	var out []*FieldView
	for i := range m.Fields {
		f := &m.Fields[i]
		fv := &FieldView{F: f, Show: true}
		if f.Fin && !m.Global && rec != nil && rec.ID != 0 && !c.finVisible(rec, m.PermKey()) {
			fv.Show = false
		}
		if f.Fin && !m.Global && (rec == nil || rec.ID == 0) && c.Level("fin") < 1 && !c.User.IsAdmin && !c.scoped() {
			fv.Show = false
		}
		v := ""
		if vals != nil {
			v = vals[f.Key]
		} else if rec != nil {
			v = rec.S(f.Key)
		}
		fv.Value = v
		if errs != nil {
			fv.Err = errs[f.Key]
		}
		switch f.Type {
		case TRef:
			fv.Opts = c.refOptions(f, v)
		case TRecipe:
			fv.Items = c.recipeItemsJSON()
		case TText:
			fv.Suggest = f.Datalist
			if len(f.Datalist) == 0 && !m.Global {
				// suggest existing values of this field
				seen := map[string]bool{}
				for _, r := range c.Recs(m.Key) {
					if s := r.S(f.Key); s != "" && !seen[s] {
						seen[s] = true
						fv.Suggest = append(fv.Suggest, s)
					}
				}
			} else if f.Datalist != nil && !m.Global {
				seen := map[string]bool{}
				for _, s := range f.Datalist {
					seen[s] = true
				}
				for _, r := range c.Recs(m.Key) {
					if s := r.S(f.Key); s != "" && !seen[s] {
						seen[s] = true
						fv.Suggest = append(fv.Suggest, s)
					}
				}
			}
		}
		out = append(out, fv)
	}
	return out
}

func (c *C) handleForm(w http.ResponseWriter, r *http.Request) {
	m := c.resolveModule(true)
	if m == nil {
		return
	}
	var rec *Rec
	if idStr := r.PathValue("rid"); idStr != "" {
		rec = c.loadRec(m, pathInt(r, "rid"))
		if rec == nil {
			return
		}
	}
	vals := map[string]string{}
	if rec == nil {
		for i := range m.Fields {
			f := &m.Fields[i]
			vals[f.Key] = f.Default
			if qv := r.URL.Query().Get(f.Key); qv != "" {
				vals[f.Key] = qv
			}
		}
		if c.scoped() && m.AreaField != "" && vals[m.AreaField] == "" && len(c.Mem.Areas) > 0 {
			vals[m.AreaField] = itoa(c.Mem.Areas[0])
		}
	} else {
		vals = nil
	}
	c.renderForm(m, rec, vals, nil)
}

func (c *C) renderForm(m *Module, rec *Rec, vals map[string]string, errs map[string]string) {
	next := c.R.URL.Query().Get("next")
	if next == "" {
		next = c.R.FormValue("next")
	}
	if next == "" {
		next = c.modListURL(m)
	}
	action := c.modBase(m)
	if rec != nil {
		action += "/" + itoa(rec.ID)
	} else {
		action += "/new"
	}
	title := m.Singular + " bearbeiten"
	if rec == nil {
		title = m.Singular + " anlegen"
	}
	data := map[string]any{
		"M": m, "Rec": rec, "Fields": c.fieldViews(m, rec, errs, vals), "Action": action, "Next": next,
		"Title": title, "IsNew": rec == nil, "MultiCreate": m.MultiCreate && rec == nil,
		"DeleteURL": action + "/delete", "CanDelete": rec != nil,
		"CanAttach": rec != nil && c.attachable(m),
	}
	if rec != nil && c.attachable(m) {
		data["Files"] = c.fileViews(m, rec)
		data["UploadURL"] = action + "/files"
	}
	if c.Event != nil {
		data["Nav"] = c.eventNav(m.PermKey())
	}
	if len(errs) > 0 {
		c.W.WriteHeader(http.StatusUnprocessableEntity)
	}
	c.Page("form.html", data)
}

func (c *C) loadRec(m *Module, id int64) *Rec {
	rec := c.A.rec(id)
	var evID int64
	if !m.Global && c.Event != nil {
		evID = c.Event.ID
	}
	if rec == nil || rec.Module != m.Key || rec.EventID != evID || !c.visible(m, rec) {
		c.Error(404, "Eintrag nicht gefunden.")
		return nil
	}
	return rec
}

func (c *C) collect(m *Module, rec *Rec) (map[string]string, map[string]string) {
	r := c.R
	vals := map[string]string{}
	errs := map[string]string{}
	for i := range m.Fields {
		f := &m.Fields[i]
		if f.Fin && !m.Global {
			probe := rec
			if probe == nil {
				probe = &Rec{D: map[string]string{}}
				if m.AreaField != "" {
					probe.D[m.AreaField] = r.FormValue(m.AreaField)
				}
			}
			if !c.finVisible(probe, m.PermKey()) {
				if rec != nil {
					vals[f.Key] = rec.S(f.Key)
				}
				continue
			}
		}
		v := strings.TrimSpace(r.FormValue(f.Key))
		switch f.Type {
		case TBool:
			if r.FormValue(f.Key) != "" {
				v = "1"
			} else {
				v = ""
			}
		case TMoney, TNumber, TPercent:
			if !validNum(v) {
				errs[f.Key] = "Bitte eine Zahl eingeben."
			} else if v != "" {
				v = numStr(parseNum(v))
			}
		case TSelect:
			if v != "" {
				okOpt := false
				for _, o := range f.Opts {
					if o.V == v {
						okOpt = true
					}
				}
				if !okOpt {
					v = f.Default
				}
			}
		case TDate:
			if v != "" {
				if _, ok := parseDT(v); !ok {
					errs[f.Key] = "Ungültiges Datum."
				}
			}
		case TDateTime:
			if v != "" {
				if _, ok := parseDT(v); !ok {
					errs[f.Key] = "Ungültiges Datum."
				}
			}
		case TRef:
			if v != "" {
				id := int64(parseNum(v))
				target := c.A.rec(id)
				rm := modByKey[f.Ref]
				if id == 0 || target == nil || rm == nil || target.Module != f.Ref || (!rm.Global && (c.Event == nil || target.EventID != c.Event.ID)) {
					v = ""
				} else {
					v = itoa(id)
				}
			}
		case TURL:
			if v != "" && !strings.HasPrefix(v, "http://") && !strings.HasPrefix(v, "https://") && !strings.HasPrefix(v, "mailto:") {
				v = "https://" + v
			}
		case TRecipe:
			v = c.cleanRecipe(r.FormValue(f.Key))
		}
		if f.Required && v == "" {
			errs[f.Key] = "Pflichtfeld."
		}
		vals[f.Key] = v
	}
	// scoped roles may only write into their own areas
	if c.scoped() && m.AreaField != "" {
		a := vals[m.AreaField]
		if a == "" || !c.Mem.InArea(int64(parseNum(a))) {
			if len(c.Mem.Areas) > 0 {
				vals[m.AreaField] = itoa(c.Mem.Areas[0])
			}
		}
	}
	if st, en := vals["start"], vals["end"]; st != "" && en != "" {
		a, ok1 := parseDT(st)
		b, ok2 := parseDT(en)
		if ok1 && ok2 && b.Before(a) {
			errs["end"] = "Das Ende liegt vor dem Beginn."
		}
	}
	return vals, errs
}

func (c *C) handleSave(w http.ResponseWriter, r *http.Request) {
	m := c.resolveModule(true)
	if m == nil {
		return
	}
	var rec *Rec
	if r.PathValue("rid") != "" {
		rec = c.loadRec(m, pathInt(r, "rid"))
		if rec == nil {
			return
		}
	}
	vals, errs := c.collect(m, rec)
	if len(errs) > 0 {
		c.renderForm(m, rec, vals, errs)
		return
	}
	if key, ok := relDateKey[m.Key]; ok && c.Event != nil {
		if d, ok := relDate(c.Event.Start, vals["rel"]); ok {
			vals[key] = d
		}
	}
	next := r.FormValue("next")
	if next == "" {
		next = c.modListURL(m)
	}
	if rec != nil {
		for k, v := range vals {
			rec.D[k] = v
		}
		if err := c.A.saveRec(rec); err != nil {
			c.Error(500, "Speichern fehlgeschlagen.")
			return
		}
		c.audit(m, rec, "geändert")
		c.setFlash("Gespeichert.")
		c.back(next)
		return
	}
	count := 1
	if m.MultiCreate {
		if n := int(parseNum(r.FormValue("_count"))); n > 1 {
			count = n
			if count > 60 {
				count = 60
			}
		}
	}
	var evID int64
	if !m.Global {
		evID = c.Event.ID
	}
	var last *Rec
	for i := 0; i < count; i++ {
		d := map[string]string{}
		for k, v := range vals {
			d[k] = v
		}
		nr := &Rec{EventID: evID, Module: m.Key, D: d, CreatedBy: c.User.ID}
		if err := c.A.saveRec(nr); err != nil {
			c.Error(500, "Speichern fehlgeschlagen.")
			return
		}
		last = nr
	}
	c.audit(m, last, "angelegt")
	if count > 1 {
		c.setFlash(fmt.Sprintf("%d Einträge angelegt.", count))
	} else {
		c.setFlash("Angelegt.")
	}
	c.back(next)
}

func (c *C) audit(m *Module, r *Rec, action string) {
	if m.Global || c.Event == nil || r == nil {
		return
	}
	c.A.logAudit(c.User.ID, c.Event.ID, m.Key, r.ID, action, c.A.recTitle(c, m, r))
}

func (c *C) handleDelete(w http.ResponseWriter, r *http.Request) {
	m := c.resolveModule(true)
	if m == nil {
		return
	}
	rec := c.loadRec(m, pathInt(r, "rid"))
	if rec == nil {
		return
	}
	if m.Key == "contacts" && rec.I("user") != 0 {
		c.setFlash("Dieser Kontakt gehört zu einem Benutzerkonto und kann nicht gelöscht werden.")
		c.back("/g/contacts")
		return
	}
	title := c.A.recTitle(c, m, rec)
	c.A.delRec(rec.ID)
	if !m.Global {
		c.A.logAudit(c.User.ID, c.Event.ID, m.Key, rec.ID, "gelöscht", title)
	}
	c.setFlash("„" + title + "“ gelöscht.")
	next := r.FormValue("next")
	if next == "" {
		next = c.modListURL(m)
	}
	c.back(next)
}

func (c *C) handleQuick(w http.ResponseWriter, r *http.Request) {
	m := c.resolveModule(true)
	if m == nil {
		return
	}
	rec := c.loadRec(m, pathInt(r, "rid"))
	if rec == nil {
		return
	}
	f := m.Field(r.FormValue("field"))
	if f == nil || (!f.Quick && f.Type != TBool) {
		c.Error(400, "Feld nicht änderbar.")
		return
	}
	v := r.FormValue("value")
	if f.Type == TSelect {
		ok := false
		for _, o := range f.Opts {
			if o.V == v {
				ok = true
			}
		}
		if !ok {
			c.Error(400, "Ungültiger Wert.")
			return
		}
	}
	rec.D[f.Key] = v
	_ = c.A.saveRec(rec)
	c.audit(m, rec, "Status geändert")
	w.WriteHeader(http.StatusNoContent)
}

func (c *C) handleAction(w http.ResponseWriter, r *http.Request) {
	m := c.resolveModule(true)
	if m == nil {
		return
	}
	rec := c.loadRec(m, pathInt(r, "rid"))
	if rec == nil {
		return
	}
	for _, a := range m.Actions {
		if a.Key == r.PathValue("act") && c.Level(m.PermKey()) >= a.Level {
			msg := a.Fn(c, rec)
			c.audit(m, rec, a.Label)
			c.setFlash(msg)
			c.back(c.modListURL(m))
			return
		}
	}
	c.Error(404, "Aktion nicht gefunden.")
}
