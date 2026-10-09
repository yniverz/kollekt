// Copyright (C) 2026 yniverz
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"io/fs"
	"net/http"
	"strings"
	"time"
)

func (a *App) routes() {
	mux := http.NewServeMux()
	a.mux = mux

	static, _ := fs.Sub(webFS, "web/static")
	mux.Handle("GET /static/", http.StripPrefix("/static/", cacheStatic(http.FileServer(http.FS(static)))))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		if err := a.db.Ping(); err != nil {
			http.Error(w, "db", 500)
			return
		}
		_, _ = w.Write([]byte("ok"))
	})

	mux.HandleFunc("GET /login", a.public(a.loginPage))
	mux.HandleFunc("POST /login", a.public(a.loginPost))
	mux.HandleFunc("GET /setup", a.public(a.setupPage))
	mux.HandleFunc("POST /setup", a.public(a.setupPost))
	mux.HandleFunc("POST /logout", a.auth(func(c *C) {
		_, _ = a.db.Exec("DELETE FROM sessions WHERE token=?", hashToken(c.Sess.Token))
		a.setCookie(c.W, c.R, "", -1)
		c.Redirect("/login")
	}))

	mux.HandleFunc("GET /{$}", a.auth(func(c *C) { c.handleDashboard(c.W, c.R) }))
	mux.HandleFunc("GET /account", a.auth(func(c *C) { c.Page("account.html", map[string]any{"Title": "Mein Konto"}) }))
	mux.HandleFunc("POST /account", a.auth(a.accountPost))

	// events
	mux.HandleFunc("GET /events/new", a.auth(a.eventNew))
	mux.HandleFunc("POST /events/new", a.auth(a.eventCreate))
	mux.HandleFunc("GET /e/{eid}/{$}", a.evt(func(c *C) { c.handleOverview(c.W, c.R) }))
	mux.HandleFunc("GET /e/{eid}/settings", a.evt(a.settingsPage))
	mux.HandleFunc("POST /e/{eid}/settings", a.evt(a.settingsSave))
	mux.HandleFunc("POST /e/{eid}/delete", a.evt(a.eventDelete))
	mux.HandleFunc("POST /e/{eid}/save-template", a.evt(a.saveAsTemplate))
	mux.HandleFunc("POST /e/{eid}/duplicate", a.evt(a.eventDuplicate))
	mux.HandleFunc("GET /e/{eid}/team", a.evt(a.teamPage))
	mux.HandleFunc("POST /e/{eid}/team", a.evt(a.teamSave))
	mux.HandleFunc("POST /e/{eid}/team/{uid}/remove", a.evt(a.teamRemove))

	mux.HandleFunc("GET /e/{eid}/zeitplan", a.evt(func(c *C) { c.handleTimeplan(c.W, c.R) }))
	mux.HandleFunc("POST /e/{eid}/ical", a.evt(a.handleCalRegenerate))
	mux.HandleFunc("GET /cal/{token}", a.icsHandler)

	mux.HandleFunc("GET /e/{eid}/lageplan", a.evt(func(c *C) { c.handleLageplan(c.W, c.R) }))
	mux.HandleFunc("POST /e/{eid}/lageplan/new", a.evt(func(c *C) { c.handlePlanCreate(c.W, c.R) }))
	mux.HandleFunc("POST /e/{eid}/lageplan/{pid}", a.evt(func(c *C) { c.handlePlanUpdate(c.W, c.R) }))
	mux.HandleFunc("POST /e/{eid}/lageplan/{pid}/delete", a.evt(func(c *C) { c.handlePlanDelete(c.W, c.R) }))
	mux.HandleFunc("GET /e/{eid}/lageplan/{pid}/data", a.evt(func(c *C) { c.handleSiteData(c.W, c.R) }))
	mux.HandleFunc("GET /e/{eid}/lageplan/{pid}/image", a.evt(func(c *C) { c.handleSiteImage(c.W, c.R) }))
	mux.HandleFunc("POST /e/{eid}/lageplan/{pid}/items", a.evt(func(c *C) { c.handleSiteItemSave(c.W, c.R) }))
	mux.HandleFunc("POST /e/{eid}/lageplan/{pid}/view", a.evt(func(c *C) { c.handleSiteView(c.W, c.R) }))
	mux.HandleFunc("POST /e/{eid}/lageplan/items/{iid}/delete", a.evt(func(c *C) { c.handleSiteItemDelete(c.W, c.R) }))

	mux.HandleFunc("GET /e/{eid}/weather", a.evt(func(c *C) { c.handleWeather(c.W, c.R) }))
	mux.HandleFunc("POST /e/{eid}/m/checklists/reset", a.evt(func(c *C) { c.handleChecklistReset(c.W, c.R) }))
	mux.HandleFunc("GET /e/{eid}/retro", a.evt(func(c *C) { c.handleRetro(c.W, c.R) }))
	mux.HandleFunc("POST /e/{eid}/retro", a.evt(func(c *C) { c.handleRetroSave(c.W, c.R) }))
	mux.HandleFunc("GET /e/{eid}/neighbors/letter", a.evt(func(c *C) { c.handleLetter(c.W, c.R) }))
	mux.HandleFunc("POST /e/{eid}/neighbors/letter", a.evt(func(c *C) { c.handleLetterSave(c.W, c.R) }))
	mux.HandleFunc("GET /compare", a.auth(func(c *C) { c.handleCompare(c.W, c.R) }))

	// custom pages
	mux.HandleFunc("GET /e/{eid}/calc", a.evt(func(c *C) { c.handleCalc(c.W, c.R) }))
	mux.HandleFunc("POST /e/{eid}/calc", a.evt(func(c *C) { c.handleCalcSave(c.W, c.R) }))
	mux.HandleFunc("GET /e/{eid}/bar", a.evt(func(c *C) { c.handleBar(c.W, c.R) }))
	mux.HandleFunc("POST /e/{eid}/bar/settings", a.evt(func(c *C) { c.handleBarSettings(c.W, c.R) }))
	mux.HandleFunc("POST /e/{eid}/bar/import", a.evt(func(c *C) { c.handleBarImport(c.W, c.R) }))
	mux.HandleFunc("POST /e/{eid}/bar/tocatalog/{rid}", a.evt(func(c *C) { c.handleBarToCatalog(c.W, c.R) }))

	// generic module routes
	mux.HandleFunc("GET /e/{eid}/m/{mod}", a.evt(func(c *C) { c.handleList(c.W, c.R) }))
	mux.HandleFunc("GET /e/{eid}/m/{mod}/new", a.evt(func(c *C) { c.handleForm(c.W, c.R) }))
	mux.HandleFunc("POST /e/{eid}/m/{mod}/new", a.evt(func(c *C) { c.handleSave(c.W, c.R) }))
	mux.HandleFunc("GET /e/{eid}/m/{mod}/{rid}", a.evt(func(c *C) { c.handleForm(c.W, c.R) }))
	mux.HandleFunc("POST /e/{eid}/m/{mod}/{rid}", a.evt(func(c *C) { c.handleSave(c.W, c.R) }))
	mux.HandleFunc("POST /e/{eid}/m/{mod}/{rid}/delete", a.evt(func(c *C) { c.handleDelete(c.W, c.R) }))
	mux.HandleFunc("POST /e/{eid}/m/{mod}/{rid}/quick", a.evt(func(c *C) { c.handleQuick(c.W, c.R) }))
	mux.HandleFunc("POST /e/{eid}/m/{mod}/{rid}/files", a.evt(func(c *C) { c.handleUpload(c.W, c.R) }))
	mux.HandleFunc("GET /e/{eid}/files/{fid}", a.evt(func(c *C) { c.handleDownload(c.W, c.R) }))
	mux.HandleFunc("POST /e/{eid}/files/{fid}/delete", a.evt(func(c *C) { c.handleFileDelete(c.W, c.R) }))
	mux.HandleFunc("POST /e/{eid}/m/{mod}/{rid}/do/{act}", a.evt(func(c *C) { c.handleAction(c.W, c.R) }))

	// master data
	mux.HandleFunc("GET /g/{mod}", a.auth(func(c *C) { c.handleList(c.W, c.R) }))
	mux.HandleFunc("GET /g/{mod}/new", a.auth(func(c *C) { c.handleForm(c.W, c.R) }))
	mux.HandleFunc("POST /g/{mod}/new", a.auth(func(c *C) { c.handleSave(c.W, c.R) }))
	mux.HandleFunc("GET /g/{mod}/{rid}", a.auth(func(c *C) { c.handleForm(c.W, c.R) }))
	mux.HandleFunc("POST /g/{mod}/{rid}", a.auth(func(c *C) { c.handleSave(c.W, c.R) }))
	mux.HandleFunc("POST /g/{mod}/{rid}/delete", a.auth(func(c *C) { c.handleDelete(c.W, c.R) }))

	mux.HandleFunc("POST /g/{mod}/{rid}/files", a.auth(func(c *C) { c.handleUpload(c.W, c.R) }))
	mux.HandleFunc("GET /gf/{fid}", a.auth(func(c *C) { c.handleDownload(c.W, c.R) }))
	mux.HandleFunc("POST /gf/{fid}/delete", a.auth(func(c *C) { c.handleFileDelete(c.W, c.R) }))

	// admin
	mux.HandleFunc("GET /admin/users", a.admin(a.usersPage))
	mux.HandleFunc("POST /admin/users", a.admin(a.userCreate))
	mux.HandleFunc("GET /admin/users/{uid}", a.admin(a.userEditPage))
	mux.HandleFunc("POST /admin/users/{uid}", a.admin(a.userEditSave))
	mux.HandleFunc("GET /admin/roles", a.admin(a.rolesPage))
	mux.HandleFunc("GET /admin/roles/{rid}", a.admin(a.roleEditPage))
	mux.HandleFunc("POST /admin/roles/{rid}", a.admin(a.roleEditSave))
	mux.HandleFunc("POST /admin/roles/{rid}/delete", a.admin(a.roleDelete))
	mux.HandleFunc("GET /admin/templates", a.admin(a.templatesPage))
	mux.HandleFunc("POST /admin/templates/{tid}/delete", a.admin(a.templateDelete))
	mux.HandleFunc("POST /admin/templates/{tid}", a.admin(a.templateRename))
}

func cacheStatic(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=3600")
		h.ServeHTTP(w, r)
	})
}

// ---------- auth pages ----------

func safeNext(n string) string {
	if n == "" || !strings.HasPrefix(n, "/") || strings.HasPrefix(n, "//") || strings.Contains(n, "\\") {
		return "/"
	}
	return n
}

func (a *App) loginPage(c *C) {
	if c.User != nil {
		c.Redirect("/")
		return
	}
	if a.userCount() == 0 {
		c.Redirect("/setup")
		return
	}
	c.renderTpl("login.html", map[string]any{"Title": "Anmelden", "Next": c.R.URL.Query().Get("next")})
}

func (a *App) loginPost(c *C) {
	r := c.R
	user := strings.TrimSpace(r.FormValue("username"))
	key := clientIP(r) + "|" + strings.ToLower(user)
	if !loginThrottle.allow(key, 8, 10*time.Minute) || !loginThrottle.allow(clientIP(r), 40, 10*time.Minute) || !loginThrottle.allow("u|"+strings.ToLower(user), 30, 10*time.Minute) {
		c.W.WriteHeader(http.StatusTooManyRequests)
		c.renderTpl("login.html", map[string]any{"Title": "Anmelden", "Error": "Zu viele Versuche. Bitte in ein paar Minuten erneut probieren.", "Username": user, "Next": r.FormValue("next")})
		return
	}
	u, err := a.checkLogin(user, r.FormValue("password"))
	if err != nil {
		loginThrottle.hit(key)
		loginThrottle.hit(clientIP(r))
		loginThrottle.hit("u|" + strings.ToLower(user))
		c.W.WriteHeader(http.StatusUnauthorized)
		c.renderTpl("login.html", map[string]any{"Title": "Anmelden", "Error": err.Error(), "Username": user, "Next": r.FormValue("next")})
		return
	}
	tok, err := a.newSession(u.ID)
	if err != nil {
		c.Error(500, "Sitzung konnte nicht erstellt werden.")
		return
	}
	a.setCookie(c.W, r, tok, 30*24*3600)
	c.Redirect(safeNext(r.FormValue("next")))
}

func (a *App) setupPage(c *C) {
	if a.userCount() > 0 {
		c.Redirect("/login")
		return
	}
	c.renderTpl("setup.html", map[string]any{"Title": "Einrichtung"})
}

func (a *App) setupPost(c *C) {
	if a.userCount() > 0 {
		c.Redirect("/login")
		return
	}
	r := c.R
	name, user, pw := strings.TrimSpace(r.FormValue("name")), strings.TrimSpace(r.FormValue("username")), r.FormValue("password")
	fail := func(msg string) {
		c.W.WriteHeader(422)
		c.renderTpl("setup.html", map[string]any{"Title": "Einrichtung", "Error": msg, "Name": name, "Username": user})
	}
	if name == "" || user == "" {
		fail("Name und Benutzername sind Pflicht.")
		return
	}
	if msg := checkPassword(pw); msg != "" {
		fail(msg)
		return
	}
	if pw != r.FormValue("password2") {
		fail("Die Passwörter stimmen nicht überein.")
		return
	}
	u, err := a.createUser(user, name, pw, true, true, true)
	if err != nil {
		fail("Konto konnte nicht angelegt werden.")
		return
	}
	tok, _ := a.newSession(u.ID)
	a.setCookie(c.W, r, tok, 30*24*3600)
	c.Redirect("/")
}

func (a *App) accountPost(c *C) {
	r := c.R
	if _, err := a.checkLogin(c.User.Username, r.FormValue("old")); err != nil {
		c.W.WriteHeader(422)
		c.Page("account.html", map[string]any{"Title": "Mein Konto", "Error": "Das aktuelle Passwort stimmt nicht."})
		return
	}
	pw := r.FormValue("new")
	if msg := checkPassword(pw); msg != "" || pw != r.FormValue("new2") {
		if msg == "" {
			msg = "Die beiden Passwörter stimmen nicht überein."
		}
		c.W.WriteHeader(422)
		c.Page("account.html", map[string]any{"Title": "Mein Konto", "Error": msg})
		return
	}
	h, _ := hashPW(pw)
	_, _ = a.db.Exec("UPDATE users SET pass_hash=? WHERE id=?", h, c.User.ID)
	_, _ = a.db.Exec("DELETE FROM sessions WHERE user_id=? AND token<>?", c.User.ID, hashToken(c.Sess.Token))
	c.setFlash("Passwort geändert.")
	c.Redirect("/account")
}
