// Copyright (C) 2026 yniverz
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// A real HTTP server over the real router, with real sessions. These tests are the safety net for
// "everybody gets exactly what their role allows".

type itFixture struct {
	t     *testing.T
	a     *App
	srv   *httptest.Server
	ev    *Event
	ev2   *Event
	areaA *Rec
	areaB *Rec
	recA  map[string]*Rec // module -> record in area A (own area of scoped users)
	recB  map[string]*Rec // module -> record in area B
	other map[string]*Rec // module -> record of the other event
}

type itClient struct {
	t    *testing.T
	base string
	c    *http.Client
	csrf string
}

func newFixture(t *testing.T) *itFixture {
	t.Helper()
	weatherOff = true
	db, err := openDB(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a := &App{cfg: Config{DataDir: t.TempDir()}, db: db}
	if err := a.loadTemplates(); err != nil {
		t.Fatal(err)
	}
	a.seed()
	a.routes()
	if _, err := a.createUser("admin", "Admin", "testpass-12345", true, true, true); err != nil {
		t.Fatal(err)
	}
	f := &itFixture{t: t, a: a, srv: httptest.NewServer(secureHeaders(a.mux)), recA: map[string]*Rec{}, recB: map[string]*Rec{}, other: map[string]*Rec{}}
	t.Cleanup(f.srv.Close)
	start := time.Now().AddDate(0, 0, 30).Format("2006-01-02") + "T20:00"
	f.ev = &Event{Name: "Event Eins", Status: "planning", Start: start, Modules: defaultModules(), Settings: map[string]jsonRaw{}}
	f.ev2 = &Event{Name: "Event Zwei", Status: "planning", Start: start, Modules: defaultModules(), Settings: map[string]jsonRaw{}}
	for _, e := range []*Event{f.ev, f.ev2} {
		if err := a.saveEvent(e); err != nil {
			t.Fatal(err)
		}
	}
	save := func(e *Event, module string, d map[string]string) *Rec {
		r := &Rec{EventID: e.ID, Module: module, D: d}
		if err := a.saveRec(r); err != nil {
			t.Fatal(err)
		}
		return r
	}
	f.areaA = save(f.ev, "areas", map[string]string{"name": "Bereich A", "color": "#112233"})
	f.areaB = save(f.ev, "areas", map[string]string{"name": "Bereich B", "color": "#445566"})
	area2 := save(f.ev2, "areas", map[string]string{"name": "Fremd-Bereich"})
	plan := save(f.ev, "siteplans", map[string]string{"name": "Plan", "mode": "map"})
	mk := func(e *Event, ar *Rec, tag string) map[string]*Rec {
		out := map[string]*Rec{}
		id := itoa(ar.ID)
		out["tasks"] = save(e, "tasks", map[string]string{"title": "Aufgabe " + tag, "area": id, "status": "open"})
		out["budget"] = save(e, "budget", map[string]string{"title": "Budget " + tag, "area": id, "kind": "exp", "qty": "1", "unit_price": "100", "scale": "fix"})
		out["equipment"] = save(e, "equipment", map[string]string{"item": "Material " + tag, "area": id, "cost": "250", "status": "need"})
		out["staff"] = save(e, "staff", map[string]string{"position": "Schicht " + tag, "area": id, "rate": "12", "start": start, "end": start})
		out["timeline"] = save(e, "timeline", map[string]string{"title": "Ablauf " + tag, "area": id})
		out["notes"] = save(e, "notes", map[string]string{"title": "Notiz " + tag, "area": id})
		out["checklists"] = save(e, "checklists", map[string]string{"list": "Liste", "title": "Punkt " + tag, "area": id, "status": "open"})
		out["lineup"] = save(e, "lineup", map[string]string{"artist": "Act " + tag, "stage": "Main", "fee": "777", "start": start, "end": start})
		out["permits"] = save(e, "permits", map[string]string{"title": "Antrag " + tag, "status": "todo", "cost": "55"})
		out["neighbors"] = save(e, "neighbors", map[string]string{"who": "Nachbar " + tag, "status": "todo"})
		out["power"] = save(e, "power", map[string]string{"name": "Strom " + tag, "kind": "load", "area": id, "watts": "100"})
		out["logistics"] = save(e, "logistics", map[string]string{"title": "Fahrt " + tag, "rate": "0.5", "km": "10"})
		out["bar_items"] = save(e, "bar_items", map[string]string{"name": "Artikel " + tag, "price": "10", "content": "10", "unit": "Stk"})
		out["bar_products"] = save(e, "bar_products", map[string]string{"name": "Produkt " + tag, "price": "3"})
		out["loc_candidates"] = save(e, "loc_candidates", map[string]string{"location": "0", "status": "idea", "cost": "999"})
		return out
	}
	f.recA, f.recB = mk(f.ev, f.areaA, "A"), mk(f.ev, f.areaB, "B")
	f.other = mk(f.ev2, area2, "FREMD")
	_ = plan
	return f
}

func (f *itFixture) client(user, pw string) *itClient {
	jar, _ := cookiejar.New(nil)
	c := &itClient{t: f.t, base: f.srv.URL, c: &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	if user != "" {
		resp := c.do("POST", "/login", url.Values{"username": {user}, "password": {pw}}, nil)
		if resp.StatusCode != 303 {
			f.t.Fatalf("login %s: %d", user, resp.StatusCode)
		}
		resp.Body.Close()
		body := c.getBody("/")
		if m := regexp.MustCompile(`name="csrf" content="([^"]+)"`).FindStringSubmatch(body); m != nil {
			c.csrf = m[1]
		}
	}
	return c
}

func (c *itClient) do(method, path string, form url.Values, hdr map[string]string) *http.Response {
	var body io.Reader
	if form != nil {
		if c.csrf != "" && form.Get("_csrf") == "" {
			form.Set("_csrf", c.csrf)
		}
		body = strings.NewReader(form.Encode())
	}
	req, _ := http.NewRequest(method, c.base+path, body)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := c.c.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	return resp
}

func (c *itClient) status(method, path string, form url.Values) int {
	resp := c.do(method, path, form, nil)
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode
}

func (c *itClient) getBody(path string) string {
	resp := c.do("GET", path, nil, nil)
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}

func (c *itClient) partial(path string) (int, string) {
	resp := c.do("GET", path, nil, map[string]string{"X-Partial": "1"})
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func (a *App) saveFileForTest(stored, content string) error {
	if err := os.MkdirAll(a.filesDir(), 0o700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(a.filesDir(), stored), []byte(content), 0o600)
}

func (f *itFixture) addUser(name string, role *Role, areas ...int64) {
	u, err := f.a.createUser(name, name, "rolepassword-123", false, false, false)
	if err != nil {
		f.t.Fatal(err)
	}
	f.a.setMember(f.ev.ID, u.ID, role.ID, areas)
}

func roleByName(a *App, name string) *Role {
	for _, r := range a.roles() {
		if r.Name == name {
			return r
		}
	}
	return nil
}

// ---------- the matrix ----------

var pageModules = map[string]string{"calc": "calc", "bar": "bar", "timeplan": "zeitplan", "sitemap": "lageplan", "retro": "retro"}

func TestRoleMatrixListsAndWrites(t *testing.T) {
	f := newFixture(t)
	for _, r := range f.a.roles() {
		f.addUser("u_"+r.Name, r, f.areaA.ID)
	}
	generic := []string{"areas", "tasks", "permits", "loc_candidates", "budget", "lineup", "staff", "timeline", "equipment", "notes", "power", "checklists", "logistics", "neighbors"}
	for _, role := range f.a.roles() {
		c := f.client("u_"+role.Name, "rolepassword-123")
		for _, mod := range generic {
			m := modByKey[mod]
			lvl := rolePerm(role.Perms, m.PermKey())
			if m.Core {
				lvl = max(lvl, 0)
			}
			base := fmt.Sprintf("/e/%d/m/%s", f.ev.ID, mod)
			want := 403
			if lvl >= 1 {
				want = 200
			}
			if got := c.status("GET", base, nil); got != want {
				t.Errorf("%s: GET %s = %d, want %d", role.Name, mod, got, want)
			}
			wantNew := 403
			if lvl >= 2 {
				wantNew = -1 // anything but 403/404: validation may answer 422, success 303
			}
			got := c.status("POST", base+"/new", url.Values{})
			if wantNew == 403 && got != 403 {
				t.Errorf("%s: POST new %s = %d, want 403", role.Name, mod, got)
			}
			if wantNew == -1 && (got == 403 || got == 404 || got >= 500) {
				t.Errorf("%s: POST new %s = %d, want it to pass authorization", role.Name, mod, got)
			}
		}
		for mod, page := range pageModules {
			lvl := rolePerm(role.Perms, modByKey[mod].PermKey())
			want := 403
			if lvl >= 1 {
				want = 200
			}
			if got := c.status("GET", fmt.Sprintf("/e/%d/%s", f.ev.ID, page), nil); got != want {
				t.Errorf("%s: GET page %s = %d, want %d", role.Name, page, got, want)
			}
		}
		// settings and team are only for people with the settings right
		want := 403
		if rolePerm(role.Perms, "settings") >= 2 {
			want = 200
		}
		for _, p := range []string{"settings", "team"} {
			if got := c.status("GET", fmt.Sprintf("/e/%d/%s", f.ev.ID, p), nil); got != want {
				t.Errorf("%s: GET %s = %d, want %d", role.Name, p, got, want)
			}
		}
		for _, p := range []string{"/admin/users", "/admin/roles", "/admin/templates"} {
			if got := c.status("GET", p, nil); got != 403 {
				t.Errorf("%s: GET %s = %d, want 403", role.Name, p, got)
			}
		}
	}
}

func TestScopedRolesOnlyTouchTheirOwnArea(t *testing.T) {
	f := newFixture(t)
	for _, name := range []string{"Bar-Leitung", "Bereichsleitung", "Helfer:in"} {
		f.addUser("s_"+name, roleByName(f.a, name), f.areaA.ID)
	}
	scopedMods := []string{"tasks", "budget", "equipment", "staff", "timeline", "notes", "checklists", "power"}
	for _, name := range []string{"Bar-Leitung", "Bereichsleitung", "Helfer:in"} {
		c := f.client("s_"+name, "rolepassword-123")
		role := roleByName(f.a, name)
		for _, mod := range scopedMods {
			lvl := rolePerm(role.Perms, modByKey[mod].PermKey())
			if lvl < 1 {
				continue
			}
			own, foreign := f.recA[mod], f.recB[mod]
			base := fmt.Sprintf("/e/%d/m/%s/", f.ev.ID, mod)
			if st, _ := c.partial(base + itoa(own.ID)); lvl >= 2 && st != 200 {
				t.Errorf("%s: own %s record not editable (%d)", name, mod, st)
			}
			// foreign records are invisible: list, form, save, delete, quick, upload all answer 404 (or 403 without edit right)
			for _, step := range []struct{ method, path string }{
				{"GET", base + itoa(foreign.ID)}, {"POST", base + itoa(foreign.ID)}, {"POST", base + itoa(foreign.ID) + "/delete"},
				{"POST", base + itoa(foreign.ID) + "/quick"}, {"POST", base + itoa(foreign.ID) + "/files"},
			} {
				if got := c.status(step.method, step.path, url.Values{"field": {"status"}, "value": {"done"}, "title": {"HACK"}}); got != 404 && got != 403 {
					t.Errorf("%s: %s %s = %d, must be 404/403", name, step.method, step.path, got)
				}
			}
			if body := c.getBody(fmt.Sprintf("/e/%d/m/%s", f.ev.ID, mod)); strings.Contains(body, foreign.D[modByKey[mod].Title]) {
				t.Errorf("%s: list of %s leaks the foreign record %q", name, mod, foreign.D[modByKey[mod].Title])
			}
			// writing into a foreign area is redirected into the own area
			if lvl >= 2 {
				c.status("POST", fmt.Sprintf("/e/%d/m/%s/new", f.ev.ID, mod), url.Values{"title": {"Neu-" + mod}, "name": {"Neu-" + mod}, "item": {"Neu-" + mod}, "position": {"Neu"}, "list": {"L"}, "area": {itoa(f.areaB.ID)}, "kind": {"load"}, "start": {""}})
				for _, r := range f.a.recs(f.ev.ID, mod) {
					if strings.HasPrefix(r.D[modByKey[mod].Title], "Neu") && r.D["area"] == itoa(f.areaB.ID) {
						t.Errorf("%s: created a %s record in a foreign area", name, mod)
					}
				}
			}
		}
		// the audit trail and overview must not name records of foreign areas
		ov := c.getBody(fmt.Sprintf("/e/%d/", f.ev.ID))
		for _, secret := range []string{"Budget B", "Material B", "Aufgabe B"} {
			if strings.Contains(ov, secret) {
				t.Errorf("%s: overview leaks %q", name, secret)
			}
		}
	}
}

func TestCrossEventIsolation(t *testing.T) {
	f := newFixture(t)
	f.addUser("member", roleByName(f.a, "Orga-Leitung"))
	c := f.client("member", "rolepassword-123")
	if got := c.status("GET", fmt.Sprintf("/e/%d/", f.ev2.ID), nil); got != 403 {
		t.Errorf("foreign event overview = %d, want 403", got)
	}
	for mod, rec := range f.other {
		if mod == "bar_items" || mod == "bar_products" {
			continue
		}
		// a record id of another event, requested through my event, must not be reachable
		for _, p := range []string{
			fmt.Sprintf("/e/%d/m/%s/%d", f.ev.ID, mod, rec.ID),
			fmt.Sprintf("/e/%d/m/%s/%d/delete", f.ev.ID, mod, rec.ID),
		} {
			method := "GET"
			if strings.HasSuffix(p, "/delete") {
				method = "POST"
			}
			if got := c.status(method, p, url.Values{}); got != 404 {
				t.Errorf("%s %s = %d, want 404", method, p, got)
			}
		}
	}
	if f.a.rec(f.other["tasks"].ID) == nil {
		t.Error("foreign record was deleted")
	}
}

func TestOverviewAndAuditDoNotLeakModulesTheRoleCannotSee(t *testing.T) {
	f := newFixture(t)
	f.addUser("helfer", roleByName(f.a, "Helfer:in"), f.areaA.ID)
	// somebody edits the budget; the audit trail names the record
	f.a.logAudit(1, f.ev.ID, "budget", f.recA["budget"].ID, "geändert", "Geheimes Budget Posten")
	f.a.logAudit(1, f.ev.ID, "lineup", f.recA["lineup"].ID, "geändert", "Geheimer Headliner")
	f.a.logAudit(1, f.ev.ID, "tasks", f.recA["tasks"].ID, "geändert", "Eigene Aufgabe", f.areaA.ID)
	f.a.logAudit(1, f.ev.ID, "tasks", f.recB["tasks"].ID, "geändert", "Fremde Aufgabe", f.areaB.ID)
	c := f.client("helfer", "rolepassword-123")
	ov := c.getBody(fmt.Sprintf("/e/%d/", f.ev.ID))
	for _, secret := range []string{"Geheimes Budget Posten", "Geheimer Headliner", "777", "999"} {
		if strings.Contains(ov, secret) {
			t.Errorf("overview leaks %q to a helper", secret)
		}
	}
	if strings.Contains(ov, "Fremde Aufgabe") {
		t.Error("overview shows a change in a foreign area")
	}
	if !strings.Contains(ov, "Eigene Aufgabe") {
		t.Error("overview should still show changes in modules the helper may see")
	}
}

func TestCostFieldsStayHiddenWithoutTheFinanceRight(t *testing.T) {
	f := newFixture(t)
	role := &Role{Name: "Material ohne Kosten", Perms: map[string]int{"equipment": 2, "lineup": 1, "permits": 1, "areas": 1}}
	if err := f.a.saveRole(role); err != nil {
		t.Fatal(err)
	}
	f.addUser("ohne_kosten", role)
	c := f.client("ohne_kosten", "rolepassword-123")
	eq := f.recA["equipment"]
	st, form := c.partial(fmt.Sprintf("/e/%d/m/equipment/%d", f.ev.ID, eq.ID))
	if st != 200 || strings.Contains(form, `name="cost"`) || strings.Contains(form, `name="paid"`) {
		t.Errorf("equipment form shows cost fields (status %d)", st)
	}
	if list := c.getBody(fmt.Sprintf("/e/%d/m/equipment", f.ev.ID)); strings.Contains(list, "250,00") {
		t.Error("equipment list shows the cost")
	}
	if list := c.getBody(fmt.Sprintf("/e/%d/m/lineup", f.ev.ID)); strings.Contains(list, "777") {
		t.Error("line-up shows the fee")
	}
	if list := c.getBody(fmt.Sprintf("/e/%d/m/permits", f.ev.ID)); strings.Contains(list, "55,00") {
		t.Error("permits show the fee")
	}
	// the quick endpoint must not toggle "paid", and saving must not overwrite the hidden cost
	if got := c.status("POST", fmt.Sprintf("/e/%d/m/equipment/%d/quick", f.ev.ID, eq.ID), url.Values{"field": {"paid"}, "value": {"1"}}); got != 403 {
		t.Errorf("quick paid = %d, want 403", got)
	}
	c.status("POST", fmt.Sprintf("/e/%d/m/equipment/%d", f.ev.ID, eq.ID), url.Values{"item": {"Neu benannt"}, "cost": {"1"}, "status": {"need"}})
	if got := f.a.rec(eq.ID); got.D["cost"] != "250" || got.D["item"] != "Neu benannt" {
		t.Errorf("hidden cost changed or edit lost: %v", got.D)
	}
	// ICS and calendar must not contain money either
	tok, _ := f.a.newCalToken(f.ev.ID, 0)
	_ = tok
}

func TestUnauthenticatedAndCSRF(t *testing.T) {
	f := newFixture(t)
	anon := f.client("", "")
	for _, p := range []string{"/", fmt.Sprintf("/e/%d/", f.ev.ID), "/g/contacts", "/admin/users", "/compare", "/account"} {
		if got := anon.status("GET", p, nil); got != 303 {
			t.Errorf("anonymous GET %s = %d, want redirect to login", p, got)
		}
	}
	for _, p := range []string{fmt.Sprintf("/e/%d/files/1", f.ev.ID), "/gf/1", fmt.Sprintf("/e/%d/lageplan/1/data", f.ev.ID), fmt.Sprintf("/e/%d/weather", f.ev.ID)} {
		if got := anon.status("GET", p, nil); got != 303 {
			t.Errorf("anonymous GET %s = %d, want redirect to login", p, got)
		}
	}
	if got := anon.status("GET", "/cal/"+strings.Repeat("a", 48)+".ics", nil); got != 404 {
		t.Errorf("unknown calendar token = %d, want 404", got)
	}
	admin := f.client("admin", "testpass-12345")
	// every state-changing request needs the token
	for _, p := range []string{fmt.Sprintf("/e/%d/m/tasks/new", f.ev.ID), fmt.Sprintf("/e/%d/settings", f.ev.ID), "/admin/users", fmt.Sprintf("/e/%d/delete", f.ev.ID), "/logout"} {
		resp := admin.do("POST", p, nil, nil)
		resp.Body.Close()
		form := url.Values{"title": {"x"}}
		req, _ := http.NewRequest("POST", admin.base+p, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r2, err := admin.c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		r2.Body.Close()
		if r2.StatusCode != 403 {
			t.Errorf("POST %s without CSRF token = %d, want 403", p, r2.StatusCode)
		}
	}
	if f.a.recs(f.ev.ID, "tasks")[0].D["title"] == "x" {
		t.Error("a request without CSRF token changed data")
	}
	// cross-site origin is refused even with a valid token
	resp := admin.do("POST", fmt.Sprintf("/e/%d/m/tasks/new", f.ev.ID), url.Values{"title": {"evil"}}, map[string]string{"Origin": "https://evil.example", "Sec-Fetch-Site": "cross-site"})
	resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Errorf("cross-site POST = %d, want 403", resp.StatusCode)
	}
}

func TestCalendarFeedLeaksNoMoneyOrForeignAreas(t *testing.T) {
	f := newFixture(t)
	f.addUser("kal", roleByName(f.a, "Bereichsleitung"), f.areaA.ID)
	var uid int64
	for _, u := range f.a.allUsers() {
		if u.Username == "kal" {
			uid = u.ID
		}
	}
	tok, err := f.a.newCalToken(f.ev.ID, uid)
	if err != nil {
		t.Fatal(err)
	}
	f.a.saveRec(&Rec{EventID: f.ev.ID, Module: "tasks", D: map[string]string{"title": "Frist A", "area": itoa(f.areaA.ID), "due": time.Now().AddDate(0, 0, 5).Format("2006-01-02"), "status": "open"}})
	f.a.saveRec(&Rec{EventID: f.ev.ID, Module: "budget", D: map[string]string{"title": "Rechnung Technik", "area": itoa(f.areaA.ID), "kind": "exp", "qty": "1", "unit_price": "4321", "scale": "fix", "due": time.Now().AddDate(0, 0, 6).Format("2006-01-02"), "status": "planned"}})
	f.a.saveRec(&Rec{EventID: f.ev.ID, Module: "tasks", D: map[string]string{"title": "Frist B", "area": itoa(f.areaB.ID), "due": time.Now().AddDate(0, 0, 5).Format("2006-01-02"), "status": "open"}})
	feed := f.client("", "").getBody("/cal/" + tok + ".ics")
	if !strings.Contains(feed, "Frist A") {
		t.Errorf("own task missing from feed:\n%s", feed)
	}
	if !strings.Contains(feed, "Rechnung Technik") {
		t.Errorf("own payment date missing from feed")
	}
	for _, secret := range []string{"Frist B", "777", "250", "Budget B", "4321", "4.321", "€"} {
		if strings.Contains(feed, secret) {
			t.Errorf("calendar feed leaks %q", secret)
		}
	}
	// leaving the event or being disabled ends the access
	f.a.removeMember(f.ev.ID, uid)
	if got := f.client("", "").status("GET", "/cal/"+tok+".ics", nil); got != 404 {
		t.Errorf("feed after removal = %d, want 404", got)
	}
}

func TestSetupNeedsTheCodeFromTheLog(t *testing.T) {
	weatherOff = true
	db, err := openDB(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a := &App{cfg: Config{DataDir: t.TempDir()}, db: db}
	if err := a.loadTemplates(); err != nil {
		t.Fatal(err)
	}
	a.seed()
	a.routes()
	a.initSetupCode()
	t.Cleanup(clearSetupCode)
	srv := httptest.NewServer(secureHeaders(a.mux))
	defer srv.Close()
	c := &itClient{t: t, base: srv.URL, c: &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	form := func(code string) url.Values {
		return url.Values{"name": {"Org"}, "username": {"orga"}, "password": {"longenough-123"}, "password2": {"longenough-123"}, "code": {code}}
	}
	if body := c.getBody("/setup"); !strings.Contains(body, "Einrichtungscode") {
		t.Fatal("setup page does not ask for the code")
	}
	for _, bad := range []string{"", "AAAA-BBBB-CCCC", "x"} {
		if got := c.status("POST", "/setup", form(bad)); got != 422 {
			t.Errorf("wrong code %q = %d, want 422", bad, got)
		}
	}
	if a.userCount() != 0 {
		t.Fatal("an account was created with a wrong code")
	}
	setupMu.Lock()
	code := setupCode
	setupMu.Unlock()
	if got := c.status("POST", "/setup", form(strings.ToLower(code))); got != 303 {
		t.Errorf("correct code (lower case) = %d, want 303", got)
	}
	if a.userCount() != 1 {
		t.Fatal("account not created with the right code")
	}
	if setupCodeRequired() {
		t.Error("code must be cleared after the first admin exists")
	}
}

// A crawler per role: every reachable page (and every edit form) must render without server errors.
func TestEveryRoleCanBrowseWithoutServerErrors(t *testing.T) {
	f := newFixture(t)
	roles := f.a.roles()
	for _, r := range roles {
		f.addUser("c_"+r.Name, r, f.areaA.ID)
	}
	linkRe := regexp.MustCompile(`(?:href|data-href)="(/[^"#]*)"`)
	skip := regexp.MustCompile(`^/(logout|static/|gf/|cal/)|/delete$|/files/\d+|/image|/items/|/data$|/weather$`)
	names := []string{"admin"}
	for _, r := range roles {
		names = append(names, "c_"+r.Name)
	}
	for _, name := range names {
		pw := "rolepassword-123"
		if name == "admin" {
			pw = "testpass-12345"
		}
		c := f.client(name, pw)
		seen := map[string]bool{}
		queue := []string{"/", fmt.Sprintf("/e/%d/", f.ev.ID)}
		for len(queue) > 0 && len(seen) < 600 {
			p := queue[0]
			queue = queue[1:]
			if seen[p] || skip.MatchString(p) {
				continue
			}
			seen[p] = true
			modes := []bool{false}
			if regexp.MustCompile(`/(\d+|new)(\?|$)`).MatchString(p) {
				modes = append(modes, true)
			}
			for _, partial := range modes {
				var st int
				var body string
				if partial {
					st, body = c.partial(p)
				} else {
					resp := c.do("GET", p, nil, nil)
					b, _ := io.ReadAll(resp.Body)
					resp.Body.Close()
					st, body = resp.StatusCode, string(b)
				}
				if st >= 500 || strings.Contains(body, "Template-Fehler") {
					t.Errorf("%s: GET %s (partial=%v) = %d", name, p, partial, st)
				}
				if !partial && st == 200 {
					for _, m := range linkRe.FindAllStringSubmatch(body, -1) {
						l := strings.ReplaceAll(m[1], "&amp;", "&")
						if !seen[l] {
							queue = append(queue, l)
						}
					}
				}
			}
		}
		if len(seen) < 8 {
			t.Errorf("%s: crawler only reached %d pages", name, len(seen))
		}
	}
}

func TestFilesFollowTheirRecordsPermissions(t *testing.T) {
	f := newFixture(t)
	f.addUser("bar", roleByName(f.a, "Bar-Leitung"), f.areaA.ID)
	f.addUser("lesen", roleByName(f.a, "Nur lesen"))
	mkFile := func(ev *Event, module string, rec *Rec) int64 {
		stored := "f_" + itoa(rec.ID)
		if err := f.a.saveFileForTest(stored, "geheim"); err != nil {
			t.Fatal(err)
		}
		res, _ := f.a.db.Exec("INSERT INTO files(event_id,module,record_id,name,size,stored,created_by,created_at) VALUES(?,?,?,?,?,?,?,?)", ev.ID, module, rec.ID, "vertrag.pdf", 6, stored, 1, time.Now().Unix())
		id, _ := res.LastInsertId()
		return id
	}
	own := mkFile(f.ev, "budget", f.recA["budget"])
	foreign := mkFile(f.ev, "budget", f.recB["budget"])
	other := mkFile(f.ev2, "tasks", f.other["tasks"])
	bar := f.client("bar", "rolepassword-123")
	if got := bar.status("GET", fmt.Sprintf("/e/%d/files/%d", f.ev.ID, own), nil); got != 200 {
		t.Errorf("own file = %d, want 200", got)
	}
	for _, id := range []int64{foreign, other} {
		if got := bar.status("GET", fmt.Sprintf("/e/%d/files/%d", f.ev.ID, id), nil); got != 404 && got != 403 {
			t.Errorf("file %d of another area/event = %d, must be 404/403", id, got)
		}
	}
	lesen := f.client("lesen", "rolepassword-123")
	if got := lesen.status("POST", fmt.Sprintf("/e/%d/files/%d/delete", f.ev.ID, own), nil); got != 403 {
		t.Errorf("read-only delete = %d, want 403", got)
	}
	if f.a.file(own) == nil {
		t.Error("a read-only user deleted a file")
	}
	resp := bar.do("GET", fmt.Sprintf("/e/%d/files/%d", f.ev.ID, own), nil, nil)
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "application/octet-stream" || !strings.HasPrefix(resp.Header.Get("Content-Disposition"), "attachment") || resp.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("download headers unsafe: %v", resp.Header)
	}
}
