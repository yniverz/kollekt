package app

import (
	"bytes"
	"database/sql"
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
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
}

func ConfigFromEnv() Config {
	return Config{
		DataDir:       env("KOLLEKT_DATA", "./data"),
		Addr:          env("KOLLEKT_ADDR", ":8080"),
		SecureCookies: env("KOLLEKT_SECURE_COOKIES", "") == "1",
		AdminUser:     os.Getenv("KOLLEKT_ADMIN_USER"),
		AdminPass:     os.Getenv("KOLLEKT_ADMIN_PASSWORD"),
		TrustProxy:    env("KOLLEKT_TRUST_PROXY", "") == "1",
	}
}

func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

type App struct {
	cfg Config
	db  *sql.DB
	tpl map[string]*template.Template
	mux *http.ServeMux
}

// Run starts the server.
func Run(cfg Config) error {
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return err
	}
	trustProxy = cfg.TrustProxy
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
	if env("KOLLEKT_DEMO_DATA", "") == "1" {
		a.seedDemo()
	}
	a.routes()
	go a.gcSessions()
	log.Printf("Kollekt läuft auf %s (Daten: %s)", cfg.Addr, cfg.DataDir)
	srv := &http.Server{
		Addr: cfg.Addr, Handler: secureHeaders(a.mux),
		ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second,
		WriteTimeout: 60 * time.Second, IdleTimeout: 120 * time.Second,
	}
	return srv.ListenAndServe()
}

func (a *App) gcSessions() {
	for {
		_, _ = a.db.Exec("DELETE FROM sessions WHERE expires_at<?", time.Now().Unix())
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

func (a *App) loadTemplates() error {
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
		"neg":   func(f float64) bool { return f < 0 },
		"itoa":  itoa,
		"split": joinTags,
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
	A     *App
	W     http.ResponseWriter
	R     *http.Request
	Sess  *Session
	User  *User
	Event *Event
	Mem   *Member
	cache map[string][]*Rec
	refs  map[string]map[int64]string
	flash string
	fin   *bool
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
	return c.Mem.Role.Perms[perm]
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
}

func (c *C) eventNav(active string) []NavItem {
	var out []NavItem
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
		out = append(out, NavItem{m.Key, m.Name, m.Icon, u, m.Key == active})
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

func sameOrigin(r *http.Request) bool {
	o := r.Header.Get("Origin")
	if o == "" {
		return true
	}
	u, err := url.Parse(o)
	if err != nil {
		return false
	}
	host := r.Header.Get("X-Forwarded-Host")
	if host == "" {
		host = r.Host
	}
	return u.Host == host
}

const maxBody = 1 << 20

func limitBody(w http.ResponseWriter, r *http.Request) {
	if r.Body == nil {
		return
	}
	max := int64(maxBody)
	if strings.HasSuffix(r.URL.Path, "/files") {
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
			if !sameOrigin(r) || r.FormValue("_csrf") != c.Sess.CSRF {
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

func (c *C) back(def string) {
	to := c.R.FormValue("next")
	if to == "" || !strings.HasPrefix(to, "/") || strings.HasPrefix(to, "//") {
		to = def
	}
	c.Redirect(to)
}

func secureHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		if isHTTPS(r) {
			h.Set("Strict-Transport-Security", "max-age=31536000")
		}
		h.Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; script-src 'self'; frame-ancestors 'none'; form-action 'self'; base-uri 'none'")
		next.ServeHTTP(w, r)
	})
}
