// Copyright (C) 2026 yniverz
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type SiteKind struct {
	Key, Label, Color, Shape string // Shape: poly, point, line
}

var siteKinds = []SiteKind{
	{"zone", "Fläche / Bereich", "#6d6a62", "poly"},
	{"dance", "Tanzfläche / Publikum", "#a8322d", "poly"},
	{"stage", "Bühne / DJ", "#b5441f", "poly"},
	{"bar", "Bar / Verkaufsstand", "#9a6f12", "poly"},
	{"entry", "Einlass / Kasse", "#3f6b3a", "point"},
	{"wc", "Toiletten", "#2f5f8a", "point"},
	{"tech", "Technik / Strom", "#2b6b68", "point"},
	{"safety", "Sanitäts- / Notfallpunkt", "#a8322d", "point"},
	{"camp", "Chillout / Rückzug", "#55704a", "poly"},
	{"parking", "Parken / Anfahrt", "#57534a", "poly"},
	{"route", "Fluchtweg / Weg", "#3f6b3a", "line"},
	{"fence", "Zaun / Absperrung", "#1d1b18", "line"},
	{"other", "Sonstiges", "#8b867a", "point"},
}

func siteKindOpts() []Opt {
	var out []Opt
	for _, k := range siteKinds {
		out = append(out, Opt{k.Key, k.Label, "gray"})
	}
	return out
}

func siteKind(key string) SiteKind {
	for _, k := range siteKinds {
		if k.Key == key {
			return k
		}
	}
	return siteKinds[len(siteKinds)-1]
}

// ---------- map overviews for locations / contacts / candidates ----------

type MapPoint struct {
	Lat   float64 `json:"lat"`
	Lng   float64 `json:"lng"`
	Title string  `json:"title"`
	Sub   string  `json:"sub,omitempty"`
	URL   string  `json:"url,omitempty"`
	Color string  `json:"color,omitempty"`
}

type MapView struct {
	Points []MapPoint
	JSON   string
	Height int
	Hint   string
}

func newMapView(points []MapPoint, hint string) *MapView {
	b, _ := json.Marshal(points)
	return &MapView{Points: points, JSON: string(b), Height: 300, Hint: hint}
}

func locationsMapExtra(c *C) any {
	var pts []MapPoint
	colors := map[string]string{"indoor": "#2f5f8a", "outdoor": "#3f6b3a", "mixed": "#2b6b68"}
	for _, r := range c.Recs("locations") {
		if lat, lng, ok := parseGeo(r.S("geo")); ok {
			sub := r.S("city")
			if r.S("capacity") != "" {
				sub += " · bis " + fmtNum(r.N("capacity")) + " Personen"
			}
			pts = append(pts, MapPoint{lat, lng, r.S("name"), strings.Trim(sub, " ·"), fmt.Sprintf("/g/locations/%d?next=/g/locations", r.ID), colors[r.S("kind")]})
		}
	}
	return newMapView(pts, "Noch keine Position gesetzt. Öffne eine Location und setze den Pin.")
}

func contactsMapExtra(c *C) any {
	var pts []MapPoint
	for _, r := range c.Recs("contacts") {
		if lat, lng, ok := parseGeo(r.S("geo")); ok {
			pts = append(pts, MapPoint{lat, lng, r.S("name"), r.S("tags"), fmt.Sprintf("/g/contacts/%d?next=/g/contacts", r.ID), "#b5441f"})
		}
	}
	if len(pts) == 0 {
		return nil
	}
	return newMapView(pts, "")
}

func candidatesMapExtra(c *C) any {
	var pts []MapPoint
	colors := map[string]string{"idea": "#6d6a62", "asked": "#2f5f8a", "viewing": "#8a6a10", "offer": "#a8531a", "yes": "#3f6b3a", "no": "#a8322d"}
	f := modByKey["loc_candidates"].Field("status")
	for _, r := range c.Recs("loc_candidates") {
		loc := c.A.rec(r.I("location"))
		if loc == nil {
			continue
		}
		if lat, lng, ok := parseGeo(loc.S("geo")); ok {
			l, _ := optLabel(f.Opts, r.S("status"))
			pts = append(pts, MapPoint{lat, lng, loc.S("name"), l, fmt.Sprintf("/e/%d/m/loc_candidates/%d?next=/e/%d/m/loc_candidates", c.Event.ID, r.ID, c.Event.ID), colors[r.S("status")]})
		}
	}
	if len(pts) == 0 {
		return nil
	}
	return newMapView(pts, "")
}

// eventPin returns the chosen location of the event as map point, if it has a position.
func (c *C) eventPin() *MapView {
	if c.Event.LocationID == 0 {
		return nil
	}
	loc := c.A.rec(c.Event.LocationID)
	if loc == nil {
		return nil
	}
	lat, lng, ok := parseGeo(loc.S("geo"))
	if !ok {
		return nil
	}
	v := newMapView([]MapPoint{{lat, lng, loc.S("name"), loc.S("address"), "", "#b5441f"}}, "")
	v.Height = 200
	return v
}

// ---------- geometry ----------

type geomJSON struct {
	T string       `json:"t"`
	P [][2]float64 `json:"p"`
}

func validGeom(raw []byte, mode string) (string, bool) {
	var g geomJSON
	if json.Unmarshal(raw, &g) != nil {
		return "", false
	}
	min, max := 0, 0
	switch g.T {
	case "point":
		min, max = 1, 1
	case "poly":
		min, max = 3, 300
	case "line":
		min, max = 2, 300
	default:
		return "", false
	}
	if len(g.P) < min || len(g.P) > max {
		return "", false
	}
	for _, p := range g.P {
		for i, v := range p {
			if math.IsNaN(v) || math.IsInf(v, 0) {
				return "", false
			}
			if mode == "map" {
				lim := 90.0
				if i == 1 {
					lim = 180
				}
				if math.Abs(v) > lim {
					return "", false
				}
			} else if v < -1 || v > 2 {
				return "", false
			}
		}
	}
	b, _ := json.Marshal(g)
	return string(b), true
}

// ---------- API ----------

func (c *C) sitemapAllowed(needEdit bool) bool {
	need := 1
	if needEdit {
		need = 2
	}
	if !c.Event.Has("sitemap") || c.Level("sitemap") < need {
		c.Error(403, "Kein Zugriff auf den Lageplan.")
		return false
	}
	return true
}

func (c *C) canPlan() bool { return c.CanEdit("sitemap") && !c.scoped() }

// planAllowed guards plan management (create, rename, delete, view): editors without area restriction only.
func (c *C) planAllowed() bool {
	if !c.sitemapAllowed(true) {
		return false
	}
	if !c.canPlan() {
		c.Error(403, "Pläne anlegen und ändern dürfen nur Personen ohne Bereichsbeschränkung. Du kannst in deinem Bereich zeichnen.")
		return false
	}
	return true
}

func (c *C) loadPlan(id int64) *Rec {
	r := c.A.rec(id)
	if r == nil || r.Module != "siteplans" || r.EventID != c.Event.ID {
		return nil
	}
	return r
}

func (c *C) itemEditable(area int64) bool {
	if !c.CanEdit("sitemap") {
		return false
	}
	return !c.scoped() || c.Mem.InArea(area)
}

func jsonOut(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

type apiItem struct {
	ID       int64           `json:"id"`
	Title    string          `json:"title"`
	Kind     string          `json:"kind"`
	Area     int64           `json:"area"`
	AreaName string          `json:"areaName"`
	Color    string          `json:"color"`
	Geom     json.RawMessage `json:"geom"`
	Notes    string          `json:"notes"`
	Editable bool            `json:"editable"`
	Width    float64         `json:"width"`
	Gear     []string        `json:"gear,omitempty"`
}

// gearAt lists equipment placed at a plan object.
func (c *C) gearAt(itemID int64) []string {
	var out []string
	for _, e := range c.Recs("equipment") {
		if e.I("place") == itemID {
			t := e.S("item")
			if q := e.S("qty"); q != "" && q != "1" {
				t += " ×" + q
			}
			out = append(out, t)
		}
	}
	return out
}

func (c *C) apiItemOf(r *Rec, colors map[int64]string) apiItem {
	k := siteKind(r.S("kind"))
	col := k.Color
	if cc := colors[r.I("area")]; cc != "" {
		col = cc
	}
	g := json.RawMessage(r.S("geom"))
	if len(g) == 0 {
		g = json.RawMessage("null")
	}
	return apiItem{ID: r.ID, Title: r.S("title"), Kind: r.S("kind"), Area: r.I("area"), AreaName: c.RefTitle("areas", r.I("area")), Color: col,
		Geom: g, Notes: r.S("notes"), Editable: c.itemEditable(r.I("area")), Width: r.N("width_m"), Gear: c.gearAt(r.ID)}
}

func (c *C) handleSiteData(w http.ResponseWriter, r *http.Request) {
	if !c.sitemapAllowed(false) {
		return
	}
	plan := c.loadPlan(pathInt(r, "pid"))
	if plan == nil {
		c.Error(404, "Plan nicht gefunden.")
		return
	}
	colors := map[int64]string{}
	type apiArea struct {
		ID       int64  `json:"id"`
		Name     string `json:"name"`
		Color    string `json:"color"`
		Editable bool   `json:"editable"`
	}
	var areas []apiArea
	for _, a := range c.Recs("areas") {
		col := a.S("color")
		if col == "" {
			col = "#8a8f98"
		}
		colors[a.ID] = col
		areas = append(areas, apiArea{a.ID, a.S("name"), col, c.itemEditable(a.ID)})
	}
	var items []apiItem
	for _, it := range c.Recs("site_items") {
		if it.I("plan") == plan.ID {
			items = append(items, c.apiItemOf(it, colors))
		}
	}
	type kindOut struct {
		Key   string `json:"key"`
		Label string `json:"label"`
		Color string `json:"color"`
		Shape string `json:"shape"`
	}
	var kinds []kindOut
	for _, k := range siteKinds {
		kinds = append(kinds, kindOut{k.Key, k.Label, k.Color, k.Shape})
	}
	lat, lng, hasGeo := parseGeo(plan.S("geo"))
	if !hasGeo {
		lat, lng = 49.0069, 8.4037 // Karlsruhe
		if pin := c.eventPin(); pin != nil && len(pin.Points) > 0 {
			lat, lng = pin.Points[0].Lat, pin.Points[0].Lng
		}
	}
	zoom := plan.N("zoom")
	if zoom < 3 || zoom > 20 {
		zoom = 17
	}
	img := ""
	if plan.S("mode") != "map" && c.A.planImage(plan.ID) != nil {
		img = fmt.Sprintf("/e/%d/lageplan/%d/image?v=%d", c.Event.ID, plan.ID, plan.Updated.Unix())
	}
	density, escapeW := plan.N("density"), plan.N("escape_w")
	if density <= 0 {
		density = 2
	}
	if escapeW <= 0 {
		escapeW = 0.2
	}
	guests := 0
	if c.can("calc") && c.Level("calc") >= 1 && !c.scoped() {
		guests = c.Baseline()
	}
	jsonOut(w, 200, map[string]any{
		"plan": map[string]any{"id": plan.ID, "name": plan.S("name"), "mode": plan.S("mode"), "widthM": plan.N("width_m"), "lat": lat, "lng": lng, "zoom": zoom, "image": img,
			"density": density, "escapeW": escapeW, "guests": guests, "capacity": c.locationCapacity()},
		"items": items, "areas": areas, "kinds": kinds, "canEdit": c.CanEdit("sitemap"), "canPlan": c.canPlan(),
	})
}

func (c *C) handleSiteItemSave(w http.ResponseWriter, r *http.Request) {
	if !c.sitemapAllowed(true) {
		return
	}
	plan := c.loadPlan(pathInt(r, "pid"))
	if plan == nil {
		c.Error(404, "Plan nicht gefunden.")
		return
	}
	var in struct {
		ID    int64           `json:"id"`
		Title string          `json:"title"`
		Kind  string          `json:"kind"`
		Area  int64           `json:"area"`
		Geom  json.RawMessage `json:"geom"`
		Notes string          `json:"notes"`
		Width float64         `json:"width"`
	}
	if json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&in) != nil {
		jsonOut(w, 400, map[string]string{"error": "Ungültige Anfrage."})
		return
	}
	title := strings.TrimSpace(in.Title)
	if title == "" || len([]rune(title)) > 120 {
		jsonOut(w, 422, map[string]string{"error": "Bitte eine Bezeichnung (bis 120 Zeichen) angeben."})
		return
	}
	if len([]rune(in.Notes)) > 4000 {
		jsonOut(w, 422, map[string]string{"error": "Die Notiz ist zu lang."})
		return
	}
	kind := siteKind(in.Kind).Key
	if in.Kind != "" && in.Kind != kind {
		kind = "other"
	}
	if math.IsNaN(in.Width) || in.Width < 0 || in.Width > 200 {
		jsonOut(w, 422, map[string]string{"error": "Die Breite muss zwischen 0 und 200 Metern liegen."})
		return
	}
	var area int64
	if in.Area != 0 {
		if a := c.A.rec(in.Area); a != nil && a.Module == "areas" && a.EventID == c.Event.ID {
			area = a.ID
		}
	}
	if c.scoped() && !c.Mem.InArea(area) {
		jsonOut(w, 403, map[string]string{"error": "Du darfst nur in deinen eigenen Bereichen planen. Bitte einen Bereich wählen."})
		return
	}
	geom, ok := validGeom(in.Geom, plan.S("mode"))
	if !ok {
		jsonOut(w, 422, map[string]string{"error": "Ungültige Form."})
		return
	}
	var rec *Rec
	if in.ID != 0 {
		rec = c.A.rec(in.ID)
		if rec == nil || rec.Module != "site_items" || rec.EventID != c.Event.ID || rec.I("plan") != plan.ID {
			jsonOut(w, 404, map[string]string{"error": "Objekt nicht gefunden."})
			return
		}
		if !c.itemEditable(rec.I("area")) {
			jsonOut(w, 403, map[string]string{"error": "Dieses Objekt gehört zu einem anderen Bereich."})
			return
		}
	} else {
		rec = &Rec{EventID: c.Event.ID, Module: "site_items", D: map[string]string{}, CreatedBy: c.User.ID}
	}
	rec.D["plan"], rec.D["title"], rec.D["kind"], rec.D["area"], rec.D["geom"], rec.D["notes"] = itoa(plan.ID), title, kind, itoa(area), geom, strings.TrimSpace(in.Notes)
	if area == 0 {
		rec.D["area"] = ""
	}
	if in.Width > 0 {
		rec.D["width_m"] = numStr(math.Round(in.Width*100) / 100)
	} else {
		delete(rec.D, "width_m")
	}
	if err := c.A.saveRec(rec); err != nil {
		jsonOut(w, 500, map[string]string{"error": "Speichern fehlgeschlagen."})
		return
	}
	act := "geändert"
	if in.ID == 0 {
		act = "angelegt"
	}
	c.A.logAudit(c.User.ID, c.Event.ID, "site_items", rec.ID, "Lageplan "+act, title)
	colors := map[int64]string{}
	for _, a := range c.Recs("areas") {
		colors[a.ID] = a.S("color")
	}
	c.invalidate()
	jsonOut(w, 200, c.apiItemOf(rec, colors))
}

func (c *C) handleSiteItemDelete(w http.ResponseWriter, r *http.Request) {
	if !c.sitemapAllowed(true) {
		return
	}
	rec := c.A.rec(pathInt(r, "iid"))
	if rec == nil || rec.Module != "site_items" || rec.EventID != c.Event.ID {
		jsonOut(w, 404, map[string]string{"error": "Objekt nicht gefunden."})
		return
	}
	if !c.itemEditable(rec.I("area")) {
		jsonOut(w, 403, map[string]string{"error": "Dieses Objekt gehört zu einem anderen Bereich."})
		return
	}
	title := rec.S("title")
	c.A.delRec(rec.ID)
	c.A.logAudit(c.User.ID, c.Event.ID, "site_items", rec.ID, "Lageplan gelöscht", title)
	jsonOut(w, 200, map[string]bool{"ok": true})
}

func (c *C) handleSiteView(w http.ResponseWriter, r *http.Request) {
	if !c.planAllowed() {
		return
	}
	plan := c.loadPlan(pathInt(r, "pid"))
	if plan == nil || plan.S("mode") != "map" {
		jsonOut(w, 404, map[string]string{"error": "Plan nicht gefunden."})
		return
	}
	var in struct {
		Lat, Lng, Zoom float64
	}
	if json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&in) != nil || in.Lat < -90 || in.Lat > 90 || in.Lng < -180 || in.Lng > 180 || in.Zoom < 1 || in.Zoom > 22 {
		jsonOut(w, 422, map[string]string{"error": "Ungültiger Ausschnitt."})
		return
	}
	plan.D["geo"], plan.D["zoom"] = fmt.Sprintf("%.6f,%.6f", in.Lat, in.Lng), numStr(math.Round(in.Zoom))
	_ = c.A.saveRec(plan)
	jsonOut(w, 200, map[string]bool{"ok": true})
}

// ---------- plan images ----------

func imageType(head []byte) string {
	switch {
	case bytes.HasPrefix(head, []byte("\x89PNG\r\n\x1a\n")):
		return "image/png"
	case bytes.HasPrefix(head, []byte("\xff\xd8\xff")):
		return "image/jpeg"
	case bytes.HasPrefix(head, []byte("GIF87a")), bytes.HasPrefix(head, []byte("GIF89a")):
		return "image/gif"
	case len(head) >= 12 && bytes.Equal(head[0:4], []byte("RIFF")) && bytes.Equal(head[8:12], []byte("WEBP")):
		return "image/webp"
	}
	return ""
}

// planImage returns the first attached file that really is a raster image.
func (a *App) planImage(planID int64) *FileRec {
	for _, f := range a.filesFor(planID) {
		fh, err := os.Open(filepath.Join(a.filesDir(), f.Stored))
		if err != nil {
			continue
		}
		head := make([]byte, 16)
		n, _ := io.ReadFull(fh, head)
		fh.Close()
		if imageType(head[:n]) != "" {
			return f
		}
	}
	return nil
}

func (c *C) handleSiteImage(w http.ResponseWriter, r *http.Request) {
	if !c.sitemapAllowed(false) {
		return
	}
	plan := c.loadPlan(pathInt(r, "pid"))
	if plan == nil {
		c.Error(404, "Plan nicht gefunden.")
		return
	}
	f := c.A.planImage(plan.ID)
	if f == nil {
		c.Error(404, "Kein Bild vorhanden.")
		return
	}
	fh, err := os.Open(filepath.Join(c.A.filesDir(), f.Stored))
	if err != nil {
		c.Error(404, "Bild fehlt im Speicher.")
		return
	}
	defer fh.Close()
	head := make([]byte, 16)
	n, _ := io.ReadFull(fh, head)
	_, _ = fh.Seek(0, io.SeekStart)
	h := w.Header()
	h.Set("Content-Type", imageType(head[:n]))
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	h.Set("Cache-Control", "private, max-age=3600")
	http.ServeContent(w, r, "", f.Created, fh)
}

// storePlanImage replaces the plan's image with an uploaded raster image.
func (c *C) storePlanImage(plan *Rec, r *http.Request) string {
	files := r.MultipartForm.File["file"]
	if len(files) == 0 {
		return ""
	}
	fh := files[0]
	src, err := fh.Open()
	if err != nil {
		return "Die Datei konnte nicht gelesen werden."
	}
	head := make([]byte, 16)
	n, _ := io.ReadFull(src, head)
	src.Close()
	if imageType(head[:n]) == "" {
		return "Bitte PNG, JPG, GIF oder WebP hochladen. PDF-Pläne vorher als Bild exportieren (Screenshot oder „Als Bild speichern“)."
	}
	old := c.A.filesFor(plan.ID)
	if err := c.saveUpload(fh, c.Event.ID, "siteplans", plan.ID); err != nil {
		return err.Error()
	}
	for _, f := range old {
		c.A.removeFile(f)
	}
	plan.D["rev"] = itoa(int64(len(old)) + 1)
	_ = c.A.saveRec(plan)
	return ""
}

// ---------- page + plan management ----------

func (c *C) handleLageplan(w http.ResponseWriter, r *http.Request) {
	if !c.sitemapAllowed(false) {
		return
	}
	var plans []*Rec
	for _, p := range c.Recs("siteplans") {
		plans = append(plans, p)
	}
	sort.Slice(plans, func(i, j int) bool { return strings.ToLower(plans[i].S("name")) < strings.ToLower(plans[j].S("name")) })
	var active *Rec
	want := int64(parseNum(r.URL.Query().Get("plan")))
	for _, p := range plans {
		if p.ID == want {
			active = p
		}
	}
	if active == nil && len(plans) > 0 {
		active = plans[0]
	}
	type PlanTab struct {
		ID     int64
		Name   string
		Active bool
	}
	var tabs []PlanTab
	for _, p := range plans {
		tabs = append(tabs, PlanTab{p.ID, p.S("name"), active != nil && p.ID == active.ID})
	}
	var areas []RefOpt
	for _, a := range c.Recs("areas") {
		if !c.scoped() || c.Mem.InArea(a.ID) {
			areas = append(areas, RefOpt{itoa(a.ID), a.S("name"), false})
		}
	}
	data := map[string]any{
		"Title": "Lageplan", "Nav": c.eventNav("sitemap"), "Tabs": tabs, "Active": active, "CanEdit": c.CanEdit("sitemap"), "CanPlan": c.canPlan(),
		"Areas": areas, "Kinds": siteKinds, "MapsOn": mapCfg.Enabled, "ScopedAreas": c.scoped(),
	}
	if active != nil {
		data["API"] = fmt.Sprintf("/e/%d/lageplan/%d", c.Event.ID, active.ID)
		data["HasImage"] = active.S("mode") == "map" || c.A.planImage(active.ID) != nil
	}
	c.Page("lageplan.html", data)
}

func (c *C) handlePlanCreate(w http.ResponseWriter, r *http.Request) {
	if !c.planAllowed() {
		return
	}
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		c.setFlash("Der Upload ist fehlgeschlagen oder zu groß (maximal 25 MB).")
		c.Redirect(fmt.Sprintf("/e/%d/lageplan", c.Event.ID))
		return
	}
	defer r.MultipartForm.RemoveAll()
	name := strings.TrimSpace(r.FormValue("name"))
	mode := r.FormValue("mode")
	if mode != "map" {
		mode = "image"
	}
	if mode == "map" && !mapCfg.Enabled {
		c.setFlash("Kartenpläne sind in dieser Installation deaktiviert.")
		c.Redirect(fmt.Sprintf("/e/%d/lageplan", c.Event.ID))
		return
	}
	if name == "" || len([]rune(name)) > 80 {
		c.setFlash("Bitte einen Namen für den Plan angeben (bis 80 Zeichen).")
		c.Redirect(fmt.Sprintf("/e/%d/lageplan", c.Event.ID))
		return
	}
	plan := &Rec{EventID: c.Event.ID, Module: "siteplans", D: map[string]string{"name": name, "mode": mode}, CreatedBy: c.User.ID}
	if w := r.FormValue("width_m"); w != "" && validNum(w) && parseNum(w) > 0 && parseNum(w) < 100000 {
		plan.D["width_m"] = numStr(parseNum(w))
	}
	if pin := c.eventPin(); pin != nil && len(pin.Points) > 0 && mode == "map" {
		plan.D["geo"] = fmt.Sprintf("%.6f,%.6f", pin.Points[0].Lat, pin.Points[0].Lng)
	}
	if mode == "image" {
		if len(r.MultipartForm.File["file"]) == 0 {
			c.setFlash("Bitte ein Bild des Plans hochladen.")
			c.Redirect(fmt.Sprintf("/e/%d/lageplan", c.Event.ID))
			return
		}
	}
	if err := c.A.saveRec(plan); err != nil {
		c.Error(500, "Plan konnte nicht angelegt werden.")
		return
	}
	if mode == "image" {
		if msg := c.storePlanImage(plan, r); msg != "" {
			c.A.delRec(plan.ID)
			c.setFlash(msg)
			c.Redirect(fmt.Sprintf("/e/%d/lageplan", c.Event.ID))
			return
		}
	}
	c.A.logAudit(c.User.ID, c.Event.ID, "siteplans", plan.ID, "angelegt", name)
	c.Redirect(fmt.Sprintf("/e/%d/lageplan?plan=%d", c.Event.ID, plan.ID))
}

func (c *C) handlePlanUpdate(w http.ResponseWriter, r *http.Request) {
	if !c.planAllowed() {
		return
	}
	plan := c.loadPlan(pathInt(r, "pid"))
	if plan == nil {
		c.Error(404, "Plan nicht gefunden.")
		return
	}
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		c.setFlash("Der Upload ist fehlgeschlagen oder zu groß (maximal 25 MB).")
		c.Redirect(fmt.Sprintf("/e/%d/lageplan?plan=%d", c.Event.ID, plan.ID))
		return
	}
	defer r.MultipartForm.RemoveAll()
	if n := strings.TrimSpace(r.FormValue("name")); n != "" && len([]rune(n)) <= 80 {
		plan.D["name"] = n
	}
	if v := r.FormValue("width_m"); v == "" {
		delete(plan.D, "width_m")
	} else if validNum(v) && parseNum(v) > 0 && parseNum(v) < 100000 {
		plan.D["width_m"] = numStr(parseNum(v))
	}
	for _, k := range []string{"density", "escape_w"} {
		v := strings.TrimSpace(r.FormValue(k))
		if v == "" {
			delete(plan.D, k)
		} else if validNum(v) && parseNum(v) > 0 && parseNum(v) < 100 {
			plan.D[k] = numStr(parseNum(v))
		}
	}
	_ = c.A.saveRec(plan)
	if plan.S("mode") != "map" {
		if msg := c.storePlanImage(plan, r); msg != "" {
			c.setFlash(msg)
		} else {
			c.setFlash("Plan gespeichert.")
		}
	} else {
		c.setFlash("Plan gespeichert.")
	}
	c.Redirect(fmt.Sprintf("/e/%d/lageplan?plan=%d", c.Event.ID, plan.ID))
}

func (c *C) handlePlanDelete(w http.ResponseWriter, r *http.Request) {
	if !c.planAllowed() {
		return
	}
	plan := c.loadPlan(pathInt(r, "pid"))
	if plan == nil {
		c.Error(404, "Plan nicht gefunden.")
		return
	}
	for _, it := range c.Recs("site_items") {
		if it.I("plan") == plan.ID {
			c.A.delRec(it.ID)
		}
	}
	name := plan.S("name")
	c.A.delRec(plan.ID)
	c.A.logAudit(c.User.ID, c.Event.ID, "siteplans", plan.ID, "gelöscht", name)
	c.setFlash("Plan „" + name + "“ gelöscht.")
	c.Redirect(fmt.Sprintf("/e/%d/lageplan", c.Event.ID))
}
