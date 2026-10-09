// Copyright (C) 2026 yniverz
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"embed"
	"encoding/hex"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"
)

//go:embed web
var webFS embed.FS

type Config struct {
	DataDir       string
	Addr          string
	SecureCookies bool
	AdminUser     string
	AdminPass     string
	TrustProxy    bool
	MapsOff       bool
	WeatherOff    bool
	TileURL       string
	TileAttrib    string
	GeocoderURL   string
	SatelliteURL  string
	SatelliteAttr string
	LabelsURL     string
}

func ConfigFromEnv() Config {
	cfg := Config{
		DataDir:       env("KOLLEKT_DATA", "./data"),
		Addr:          env("KOLLEKT_ADDR", ":8080"),
		SecureCookies: env("KOLLEKT_SECURE_COOKIES", "") == "1",
		AdminUser:     os.Getenv("KOLLEKT_ADMIN_USER"),
		AdminPass:     os.Getenv("KOLLEKT_ADMIN_PASSWORD"),
		TrustProxy:    env("KOLLEKT_TRUST_PROXY", "") == "1",
		MapsOff:       strings.EqualFold(env("KOLLEKT_MAPS", ""), "off"),
		WeatherOff:    strings.EqualFold(env("KOLLEKT_WEATHER", ""), "off"),
		TileURL:       env("KOLLEKT_TILE_URL", "https://tile.openstreetmap.org/{z}/{x}/{y}.png"),
		TileAttrib:    env("KOLLEKT_TILE_ATTRIBUTION", "© OpenStreetMap-Mitwirkende"),
		GeocoderURL:   env("KOLLEKT_GEOCODER_URL", "https://nominatim.openstreetmap.org/search"),
		SatelliteURL:  env("KOLLEKT_SATELLITE_URL", "https://server.arcgisonline.com/ArcGIS/rest/services/World_Imagery/MapServer/tile/{z}/{y}/{x}"),
		SatelliteAttr: env("KOLLEKT_SATELLITE_ATTRIBUTION", "Satellitenbilder © Esri, Maxar, Earthstar Geographics und die GIS-Nutzer-Community"),
		LabelsURL:     env("KOLLEKT_LABELS_URL", "https://server.arcgisonline.com/ArcGIS/rest/services/Reference/World_Boundaries_and_Places/MapServer/tile/{z}/{y}/{x}"),
	}
	if strings.EqualFold(cfg.SatelliteURL, "off") {
		cfg.SatelliteURL, cfg.LabelsURL = "", ""
	}
	if os.Getenv("KOLLEKT_SATELLITE_URL") != "" && os.Getenv("KOLLEKT_LABELS_URL") == "" {
		cfg.LabelsURL = "" // a custom imagery provider has no matching label layer unless configured
	}
	return cfg
}

func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

type App struct {
	cfg      Config
	assetVer string
	db       *sql.DB
	tpl      map[string]*template.Template
	mux      *http.ServeMux
}

// Run starts the server.
func Run(cfg Config) error {
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return err
	}
	trustProxy = cfg.TrustProxy
	weatherOff = cfg.WeatherOff
	mapCfg = MapConfig{Enabled: !cfg.MapsOff, TileURL: cfg.TileURL, Attribution: cfg.TileAttrib, GeocoderURL: cfg.GeocoderURL, SatelliteURL: cfg.SatelliteURL, SatelliteAttr: cfg.SatelliteAttr, LabelsURL: cfg.LabelsURL}
	db, err := openDB(cfg.DataDir)
	if err != nil {
		return err
	}
	a := &App{cfg: cfg, db: db}
	if err := a.loadTemplates(); err != nil {
		return err
	}
	a.seed()
	a.bootstrapAdmin()
	a.initSetupCode()
	if env("KOLLEKT_DEMO_DATA", "") == "1" {
		a.seedDemo()
	}
	a.routes()
	go a.gcSessions()
	ln, err := net.Listen("tcp", cfg.Addr)
	if err != nil {
		return err
	}
	log.Printf("Kollekt läuft auf %s (Daten: %s)", cfg.Addr, cfg.DataDir)
	srv := &http.Server{
		Handler:           secureHeaders(a.mux),
		ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 60 * time.Second,
		WriteTimeout: 90 * time.Second, IdleTimeout: 120 * time.Second,
	}
	// finish running requests and close the database cleanly on docker stop / Ctrl-C
	done := make(chan struct{})
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		<-sig
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
		close(done)
	}()
	if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
		return err
	}
	<-done
	_ = db.Close()
	log.Printf("Kollekt beendet.")
	return nil
}

func (a *App) gcSessions() {
	for {
		_, _ = a.db.Exec("DELETE FROM sessions WHERE expires_at<?", time.Now().Unix())
		loginThrottle.gc(10 * time.Minute)
		time.Sleep(time.Hour)
	}
}

func (a *App) bootstrapAdmin() {
	if a.userCount() > 0 || a.cfg.AdminUser == "" || a.cfg.AdminPass == "" {
		return
	}
	if msg := checkPassword(a.cfg.AdminPass); msg != "" {
		log.Printf("KOLLEKT_ADMIN_PASSWORD abgelehnt: %s", msg)
		return
	}
	if _, err := a.createUser(a.cfg.AdminUser, a.cfg.AdminUser, a.cfg.AdminPass, true, true, true); err != nil {
		log.Printf("Admin-Bootstrap fehlgeschlagen: %v", err)
		return
	}
	log.Printf("Admin-Konto %q aus Umgebungsvariablen angelegt.", a.cfg.AdminUser)
}

// ---------- templates ----------

// computeAssetVersion hashes all embedded static files so browsers fetch new JS/CSS after an update.
func (a *App) computeAssetVersion() {
	h := sha256.New()
	_ = fs.WalkDir(webFS, "web/static", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		b, _ := webFS.ReadFile(p)
		h.Write([]byte(p))
		h.Write(b)
		return nil
	})
	a.assetVer = hex.EncodeToString(h.Sum(nil))[:10]
}

func (a *App) loadTemplates() error {
	a.computeAssetVersion()
	a.tpl = map[string]*template.Template{}
	entries, err := fs.ReadDir(webFS, "web/templates")
	if err != nil {
		return err
	}
	var partials []string
	var pages []string
	for _, e := range entries {
		n := e.Name()
		if strings.HasPrefix(n, "x_") || n == "base.html" {
			partials = append(partials, "web/templates/"+n)
		} else {
			pages = append(pages, n)
		}
	}
	for _, p := range pages {
		t := template.New(p).Funcs(a.funcs(nil))
		files := append([]string{}, partials...)
		files = append(files, "web/templates/"+p)
		t, err := t.ParseFS(webFS, files...)
		if err != nil {
			return fmt.Errorf("%s: %w", p, err)
		}
		// give "include" access to this set
		t = t.Funcs(a.funcs(t))
		a.tpl[p] = t
	}
	return nil
}

func (a *App) funcs(self *template.Template) template.FuncMap {
	return template.FuncMap{
		"include": func(name string, data any) (template.HTML, error) {
			if self == nil {
				return "", nil
			}
			var b bytes.Buffer
			if err := self.ExecuteTemplate(&b, name, data); err != nil {
				return "", err
			}
			return template.HTML(b.String()), nil
		},
		"eur":       fmtEUR,
		"eurSigned": fmtEURSigned,
		"num":       fmtNum,
		"pct":       func(f float64) string { return fmtNum(f) + " %" },
		"date":      fmtDate,
		"dt":        fmtDT,
		"hm":        fmtHM,
		"dayLabel":  dayLabel,
		"icon":      icon,
		"add":       func(a, b int) int { return a + b },
		"join":      strings.Join,
		"lower":     strings.ToLower,
		"dict":      dict,
		"seq": func(n int) []int {
			out := make([]int, n)
			for i := range out {
				out[i] = i
			}
			return out
		},
		"optLabel": func(opts []Opt, v string) string { l, _ := optLabel(opts, v); return l },
		"badge": func(label, color string) template.HTML {
			return template.HTML(fmt.Sprintf(`<span class="badge b-%s">%s</span>`, template.HTMLEscapeString(color), template.HTMLEscapeString(label)))
		},
		"statusBadge": func(v string) template.HTML {
			l, c := optLabel(eventStatuses, v)
			return template.HTML(fmt.Sprintf(`<span class="badge b-%s">%s</span>`, c, template.HTMLEscapeString(l)))
		},
		"levelName": levelName,
		"hasPrefix": strings.HasPrefix,
		"hasSuffix": strings.HasSuffix,
		"optColor":  func(opts []Opt, v string) string { _, c := optLabel(opts, v); return c },
		"srcLabel":  srcLabel,
		"float": func(v any) float64 {
			switch n := v.(type) {
			case int:
				return float64(n)
			case int64:
				return float64(n)
			case float64:
				return n
			}
			return 0
		},
		"sub":   func(a, b int) int { return a - b },
		"sub64": func(a, b float64) float64 { return a - b },
		"int64": func(s string) int64 { return int64(parseNum(s)) },
		"urlq":  url.QueryEscape,
		"safeURL": func(s string) template.URL {
			if strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") || strings.HasPrefix(s, "mailto:") {
				return template.URL(s)
			}
			return template.URL("https://" + s)
		},
		"nl2br": func(s string) template.HTML {
			return template.HTML(strings.ReplaceAll(template.HTMLEscapeString(s), "\n", "<br>"))
		},
		"pctOf": func(a, b float64) float64 {
			if b == 0 {
				return 0
			}
			return a / b * 100
		},
		"abs": func(f float64) float64 {
			if f < 0 {
				return -f
			}
			return f
		},
		"neg":     func(f float64) bool { return f < 0 },
		"div1000": func(f float64) float64 { return f / 1000 },
		"minf":    func(a, b float64) float64 { return math.Min(a, b) },
		"itoa":    itoa,
		"split":   joinTags,
	}
}

func dict(kv ...any) map[string]any {
	m := map[string]any{}
	for i := 0; i+1 < len(kv); i += 2 {
		m[fmt.Sprint(kv[i])] = kv[i+1]
	}
	return m
}

// ---------- request context ----------

type C struct {
	A              *App
	W              http.ResponseWriter
	R              *http.Request
	Sess           *Session
	User           *User
	Event          *Event
	Mem            *Member
	cache          map[string][]*Rec
	geoDone, geoOK bool
	rolled         bool
	geoLat, geoLng float64
	refs           map[string]map[int64]string
}

func (c *C) Level(perm string) int {
	if c.User == nil {
		return 0
	}
	if c.User.IsAdmin {
		return 2
	}
	if c.Event == nil || c.Mem == nil || c.Mem.Role == nil {
		return 0
	}
	if perm == "overview" {
		return 1
	}
	return rolePerm(c.Mem.Role.Perms, perm)
}

// rolePerm returns a role's level for a permission key. Roles saved before a module existed
// get read access derived from a related permission.
func rolePerm(p map[string]int, key string) int {
	if l, ok := p[key]; ok {
		return l
	}
	switch key {
	case "timeplan":
		return min(p["tasks"], 1)
	case "sitemap":
		return min(p["areas"], 1)
	case "power", "logistics":
		return min(p["equipment"], 1)
	case "checklists":
		return p["tasks"]
	case "neighbors":
		return min(p["permits"], 1)
	case "retro":
		return min(p["calc"], 1)
	}
	return 0
}

func (c *C) CanEdit(perm string) bool { return c.Level(perm) >= 2 }
func (c *C) CanView(perm string) bool { return c.Level(perm) >= 1 }

func (c *C) scoped() bool {
	return c.Event != nil && !c.User.IsAdmin && c.Mem != nil && c.Mem.Role != nil && c.Mem.Role.AreaScoped
}

// Recs returns event records of a module (memoized per request).
func (c *C) Recs(module string) []*Rec {
	key := module
	if m := modByKey[module]; m != nil && m.Global {
		key = "g:" + module
	}
	if c.cache == nil {
		c.cache = map[string][]*Rec{}
	}
	if v, ok := c.cache[key]; ok {
		return v
	}
	var evID int64
	if m := modByKey[module]; m != nil && !m.Global && c.Event != nil {
		evID = c.Event.ID
	}
	v := c.A.recs(evID, module)
	c.cache[key] = v
	return v
}

func (c *C) invalidate() { c.cache = nil; c.refs = nil }

// RefTitle resolves the display title of a referenced record.
func (c *C) RefTitle(module string, id int64) string {
	if id == 0 {
		return ""
	}
	if c.refs == nil {
		c.refs = map[string]map[int64]string{}
	}
	m, ok := c.refs[module]
	if !ok {
		m = map[int64]string{}
		mod := modByKey[module]
		for _, r := range c.Recs(module) {
			m[r.ID] = c.A.recTitle(c, mod, r)
		}
		c.refs[module] = m
	}
	return m[id]
}

func (a *App) recTitle(c *C, m *Module, r *Rec) string {
	if m == nil {
		return ""
	}
	switch m.Key {
	case "loc_candidates":
		return c.RefTitle("locations", r.I("location"))
	}
	t := r.S(m.Title)
	if t == "" {
		t = "(ohne Titel)"
	}
	return t
}

func (c *C) setFlash(msg string) {
	http.SetCookie(c.W, &http.Cookie{Name: "kollekt_flash", Value: url.QueryEscape(msg), Path: "/", MaxAge: 30, HttpOnly: true, SameSite: http.SameSiteLaxMode})
}

func (c *C) takeFlash() string {
	ck, err := c.R.Cookie("kollekt_flash")
	if err != nil {
		return ""
	}
	http.SetCookie(c.W, &http.Cookie{Name: "kollekt_flash", Value: "", Path: "/", MaxAge: -1})
	v, _ := url.QueryUnescape(ck.Value)
	return v
}

func (c *C) Redirect(to string) {
	if c.isPartial() {
		// fetch() follows redirects; JS detects res.redirected.
	}
	http.Redirect(c.W, c.R, to, http.StatusSeeOther)
}

func (c *C) isPartial() bool { return c.R.Header.Get("X-Partial") == "1" }

func (c *C) Error(code int, msg string) {
	c.W.WriteHeader(code)
	c.renderTpl("error.html", map[string]any{"Code": code, "Msg": msg, "Title": "Fehler"})
}

type NavItem struct {
	Key, Label, Icon, URL string
	Active                bool
	Heading               string // section title shown above this item
}

func (c *C) eventNav(active string) []NavItem {
	var out []NavItem
	lastGroup := ""
	for _, m := range eventModules() {
		if m.Key != "overview" && !m.Core && !c.Event.Has(m.Key) {
			continue
		}
		if m.Key != "overview" && c.Level(m.PermKey()) < 1 {
			continue
		}
		u := fmt.Sprintf("/e/%d/m/%s", c.Event.ID, m.Key)
		if m.Key == "overview" {
			u = fmt.Sprintf("/e/%d/", c.Event.ID)
		} else if m.Page != "" {
			u = fmt.Sprintf("/e/%d/%s", c.Event.ID, m.Page)
		}
		item := NavItem{Key: m.Key, Label: m.Name, Icon: m.Icon, URL: u, Active: m.Key == active}
		if m.NavGroup != lastGroup {
			item.Heading = m.NavGroup
			lastGroup = m.NavGroup
		}
		out = append(out, item)
	}
	return out
}

func (c *C) renderTpl(name string, data map[string]any) {
	t := c.A.tpl[name]
	if t == nil {
		http.Error(c.W, "template missing: "+name, 500)
		return
	}
	if data == nil {
		data = map[string]any{}
	}
	data["User"] = c.User
	data["Maps"] = mapCfg
	data["AssetV"] = c.A.assetVer
	data["SourceURL"] = env("KOLLEKT_SOURCE_URL", "https://github.com/yniverz/kollekt")
	if c.Sess != nil {
		data["CSRF"] = c.Sess.CSRF
	}
	data["Event"] = c.Event
	data["Path"] = c.R.URL.Path
	if _, ok := data["Flash"]; !ok {
		data["Flash"] = c.takeFlash()
	}
	if c.Event != nil {
		if _, ok := data["Nav"]; !ok {
			data["Nav"] = c.eventNav("")
		}
		data["CanSettings"] = c.CanEdit("settings")
	}
	c.W.Header().Set("Content-Type", "text/html; charset=utf-8")
	c.W.Header().Set("Cache-Control", "no-store")
	var b bytes.Buffer
	if err := t.ExecuteTemplate(&b, "base", data); err != nil {
		log.Printf("template %s: %v", name, err)
		http.Error(c.W, "Template-Fehler", 500)
		return
	}
	_, _ = c.W.Write(b.Bytes())
}

// Page renders a page with optional bare-partial mode (for modal dialogs).
func (c *C) Page(name string, data map[string]any) {
	if c.isPartial() {
		t := c.A.tpl[name]
		if t != nil && t.Lookup("partial") != nil {
			if data == nil {
				data = map[string]any{}
			}
			if c.Sess != nil {
				data["CSRF"] = c.Sess.CSRF
			}
			data["Event"] = c.Event
			data["User"] = c.User
			data["Maps"] = mapCfg
			var b bytes.Buffer
			if err := t.ExecuteTemplate(&b, "partial", data); err != nil {
				log.Printf("partial %s: %v", name, err)
				http.Error(c.W, "Template-Fehler", 500)
				return
			}
			c.W.Header().Set("Content-Type", "text/html; charset=utf-8")
			c.W.Header().Set("Cache-Control", "no-store")
			_, _ = c.W.Write(b.Bytes())
			return
		}
	}
	c.renderTpl(name, data)
}

// ---------- routing helpers ----------

type handler func(c *C)

func (a *App) newC(w http.ResponseWriter, r *http.Request) *C {
	c := &C{A: a, W: w, R: r}
	if ck, err := r.Cookie(cookieName); err == nil {
		if s := a.session(ck.Value); s != nil {
			c.Sess, c.User = s, s.User
		}
	}
	return c
}

// sameOrigin is a second line of defence next to the CSRF token. Browsers announce the relationship
// themselves (Sec-Fetch-Site), which keeps working behind proxies that rewrite the Host header.
func sameOrigin(r *http.Request) bool {
	switch r.Header.Get("Sec-Fetch-Site") {
	case "same-origin":
		return true
	case "cross-site", "same-site":
		return false
	}
	o := r.Header.Get("Origin")
	if o == "" {
		return true
	}
	u, err := url.Parse(o)
	if err != nil {
		return false
	}
	if u.Host == r.Host {
		return true
	}
	if fh := r.Header.Get("X-Forwarded-Host"); fh != "" && u.Host == fh {
		return true
	}
	return false
}

const maxBody = 1 << 20

func limitBody(w http.ResponseWriter, r *http.Request) {
	if r.Body == nil {
		return
	}
	max := int64(maxBody)
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") && (strings.HasSuffix(r.URL.Path, "/files") || strings.Contains(r.URL.Path, "/lageplan")) {
		max = maxUpload + 1<<20
	}
	r.Body = http.MaxBytesReader(w, r.Body, max)
}

func (a *App) public(h handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		limitBody(w, r)
		c := a.newC(w, r)
		if r.Method == http.MethodPost && !sameOrigin(r) {
			c.Error(403, "Ungültige Herkunft der Anfrage.")
			return
		}
		h(c)
	}
}

func (a *App) auth(h handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		limitBody(w, r)
		c := a.newC(w, r)
		if c.User == nil {
			if a.userCount() == 0 {
				http.Redirect(w, r, "/setup", http.StatusSeeOther)
				return
			}
			if r.Header.Get("X-Partial") == "1" {
				w.WriteHeader(401)
				return
			}
			http.Redirect(w, r, "/login?next="+url.QueryEscape(r.URL.RequestURI()), http.StatusSeeOther)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if r.ContentLength > maxUpload+(1<<20) {
				c.Error(413, "Die Datei ist zu groß (maximal 25 MB pro Datei).")
				return
			}
			if !sameOrigin(r) || (r.FormValue("_csrf") != c.Sess.CSRF && r.Header.Get("X-CSRF-Token") != c.Sess.CSRF) {
				c.Error(403, "Die Sitzung ist abgelaufen oder die Anfrage ungültig. Bitte Seite neu laden.")
				return
			}
		}
		h(c)
	}
}

func (a *App) evt(h handler) http.HandlerFunc {
	return a.auth(func(c *C) {
		id := pathInt(c.R, "eid")
		e := a.event(id)
		if e == nil {
			c.Error(404, "Event nicht gefunden.")
			return
		}
		c.Event = e
		if !c.User.IsAdmin {
			c.Mem = a.member(e.ID, c.User.ID)
			if c.Mem == nil || c.Mem.Role == nil {
				c.Error(403, "Du bist nicht Mitglied dieses Events.")
				return
			}
		}
		h(c)
	})
}

func (a *App) admin(h handler) http.HandlerFunc {
	return a.auth(func(c *C) {
		if !c.User.IsAdmin {
			c.Error(403, "Nur für Administratoren.")
			return
		}
		h(c)
	})
}

func pathInt(r *http.Request, name string) int64 {
	n := int64(parseNum(r.PathValue(name)))
	return n
}

// safeLocal reports whether s is a same-site path (no scheme, no protocol-relative URL, no backslash tricks).
func safeLocal(s string) bool {
	return s != "" && strings.HasPrefix(s, "/") && !strings.HasPrefix(s, "//") && !strings.ContainsAny(s, "\\\r\n\t")
}

// back redirects to the "next" form value, or to def, but never to anything that is not a local path.
func (c *C) back(def string) {
	to := c.R.FormValue("next")
	if !strings.HasPrefix(to, "/") && strings.HasPrefix(to, "%2") { // tolerate a once more encoded path
		if dec, err := url.QueryUnescape(to); err == nil {
			to = dec
		}
	}
	if !safeLocal(to) {
		to = def
	}
	if !safeLocal(to) {
		to = "/"
	}
	c.Redirect(to)
}

// MapConfig controls the optional map features (tiles and address search come from external services).
type MapConfig struct {
	Enabled       bool
	TileURL       string
	Attribution   string
	GeocoderURL   string
	SatelliteURL  string
	SatelliteAttr string
	LabelsURL     string
}

var mapCfg = MapConfig{}

func originOf(raw string) string {
	wild := strings.Contains(raw, "{s}.")
	raw = strings.NewReplacer("{s}", "a", "{z}", "0", "{x}", "0", "{y}", "0").Replace(raw)
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return ""
	}
	host := u.Host
	if wild { // tile subdomains a/b/c
		host = "*." + strings.TrimPrefix(host, "a.")
	}
	return u.Scheme + "://" + host
}

func contentSecurityPolicy() string {
	img, connect := "'self' data: blob:", "'self'"
	if mapCfg.Enabled {
		for _, u := range []string{mapCfg.TileURL, mapCfg.SatelliteURL, mapCfg.LabelsURL} {
			if o := originOf(u); o != "" && !strings.Contains(img, o) {
				img += " " + o
			}
		}
		if o := originOf(mapCfg.GeocoderURL); o != "" {
			connect += " " + o
		}
	}
	return "default-src 'self'; style-src 'self' 'unsafe-inline'; img-src " + img + "; connect-src " + connect + "; script-src 'self'; frame-ancestors 'none'; form-action 'self'; base-uri 'none'"
}

func secureHeaders(next http.Handler) http.Handler {
	csp := contentSecurityPolicy()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin") // map tile servers require a Referer
		if isHTTPS(r) {
			h.Set("Strict-Transport-Security", "max-age=31536000")
		}
		h.Set("Content-Security-Policy", csp)
		next.ServeHTTP(w, r)
	})
}

var (
	setupMu   sync.Mutex
	setupCode string
)

// initSetupCode protects a fresh installation: whoever reaches the setup page first would otherwise
// become administrator. The code is printed to the container log, which only the operator can read.
func (a *App) initSetupCode() {
	if a.userCount() > 0 {
		return
	}
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	var sb strings.Builder
	for i, x := range b {
		if i > 0 && i%4 == 0 {
			sb.WriteByte('-')
		}
		sb.WriteByte(alphabet[int(x)%len(alphabet)])
	}
	setupMu.Lock()
	setupCode = sb.String()
	setupMu.Unlock()
	log.Printf("Einrichtung: Öffne die Seite und gib diesen Einrichtungscode ein: %s", sb.String())
}

func setupCodeRequired() bool {
	setupMu.Lock()
	defer setupMu.Unlock()
	return setupCode != ""
}

func checkSetupCode(in string) bool {
	setupMu.Lock()
	defer setupMu.Unlock()
	norm := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(in), " ", ""))
	return setupCode != "" && subtle.ConstantTimeCompare([]byte(norm), []byte(setupCode)) == 1
}

func clearSetupCode() {
	setupMu.Lock()
	setupCode = ""
	setupMu.Unlock()
}
