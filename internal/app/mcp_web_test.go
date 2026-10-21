// Copyright (C) 2026 yniverz
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"fmt"
	"strings"
	"testing"
)

// The web tool must allow exactly what the person may do in the browser, nothing more.

func TestMCPWebReadsAndWritesLikeTheBrowser(t *testing.T) {
	f := newFixture(t)
	f.addUser("orga", roleByName(f.a, "Orga-Leitung"))
	_, _ = f.a.db.Exec("UPDATE users SET can_create=1 WHERE username='orga'")
	m := f.mcp("orga", true)
	ev := f.ev.ID
	settings := fmt.Sprintf("/e/%d/settings", ev)

	txt, isErr := m.call("web", map[string]any{"path": settings})
	if isErr || !strings.Contains(txt, "[Formular POST "+settings+"]") || !strings.Contains(txt, `[Feld name (text) = "Event Eins"`) {
		t.Fatalf("settings page: %s", txt)
	}
	modsBefore := len(f.a.event(ev).Modules)
	txt, isErr = m.call("web", map[string]any{"method": "POST", "path": settings, "defaults_from": settings, "form": map[string]any{"name": "Umbenannt", "mod_neighbors": false}})
	if isErr {
		t.Fatalf("settings save: %s", txt)
	}
	e := f.a.event(ev)
	if e.Name != "Umbenannt" || e.Has("neighbors") || len(e.Modules) != modsBefore-1 || e.Start != f.ev.Start {
		t.Errorf("after save: name %q, modules %d (before %d), start %q", e.Name, len(e.Modules), modsBefore, e.Start)
	}
	// new event through the regular form
	txt, isErr = m.call("web", map[string]any{"method": "POST", "path": "/events/new", "defaults_from": "/events/new", "form": map[string]any{"name": "Herbstfest", "start": "2030-10-01T18:00"}})
	if isErr || !strings.Contains(txt, "Event angelegt") {
		t.Fatalf("create event: %s", txt)
	}
	found := false
	for _, e := range f.a.allEvents() {
		found = found || e.Name == "Herbstfest" && e.CreatedBy != 0
	}
	if !found {
		t.Error("event not created")
	}
	// pages with computed views
	for _, p := range []string{"/", fmt.Sprintf("/e/%d/calc", ev), fmt.Sprintf("/e/%d/bar", ev), fmt.Sprintf("/e/%d/retro", ev), fmt.Sprintf("/e/%d/team", ev), "/compare"} {
		if txt, isErr := m.call("web", map[string]any{"path": p}); isErr || len(txt) < 100 {
			t.Errorf("GET %s: %s", p, txt)
		}
	}
}

func TestMCPWebKeepsPermissions(t *testing.T) {
	f := newFixture(t)
	f.addUser("helfer", roleByName(f.a, "Helfer:in"), f.areaA.ID)
	h := f.mcp("helfer", true)
	ro := f.mcp("helfer", false)
	ev := f.ev.ID
	for _, p := range []string{fmt.Sprintf("/e/%d/settings", ev), fmt.Sprintf("/e/%d/", f.ev2.ID), "/admin/users", fmt.Sprintf("/e/%d/m/budget/%d", ev, f.recB["budget"].ID)} {
		if txt, isErr := h.call("web", map[string]any{"path": p}); !isErr {
			t.Errorf("helper reached %s: %.200s", p, txt)
		}
	}
	if _, isErr := h.call("web", map[string]any{"method": "POST", "path": fmt.Sprintf("/e/%d/delete", ev), "form": map[string]any{"confirm": "Event Eins"}}); !isErr || f.a.event(ev) == nil {
		t.Error("helper deleted the event")
	}
	// read-only access cannot submit anything
	if txt, isErr := ro.call("web", map[string]any{"method": "POST", "path": "/events/new", "form": map[string]any{"name": "x"}}); !isErr || !strings.Contains(txt, "Lesezugriff") {
		t.Errorf("read-only POST: %s", txt)
	}
	// own credentials, sign-in and raw files stay out of reach, also for admins
	a := f.mcp("admin", true)
	for _, p := range []string{"/account", "/account/tokens", "/logout", "/oauth/authorize", "/mcp", "/login", fmt.Sprintf("/e/%d/files/1", ev), "/gf/1", fmt.Sprintf("/e/%d/m/tasks/1/files", ev), "//evil.example/x", "https://evil.example/"} {
		if txt, isErr := a.call("web", map[string]any{"method": "POST", "path": p}); !isErr {
			t.Errorf("admin reached %s: %.200s", p, txt)
		}
	}
	if txt, isErr := a.call("web", map[string]any{"path": "/admin/users"}); isErr || !strings.Contains(txt, "Benutzer") {
		t.Errorf("admin page: %.300s", txt)
	}
}

func TestFormDefaults(t *testing.T) {
	page := `<form method="post" action="/x"><input type="hidden" name="_csrf" value="t"><input name="a" value="1 &amp; 2">
	<input type="checkbox" name="c" value="on" checked><input type="checkbox" name="d"><select name="s"><option value="p">P</option><option value="q" selected>Q</option></select>
	<select name="e"><option value="first">F</option></select><textarea name="t">
Zeile</textarea><input type="submit" name="go" value="x"><template><input name="tpl" value="1"></template></form><form action="/y"><input name="z" value="9"></form>`
	v, ok := formDefaults(page, "/x")
	if !ok || v.Get("a") != "1 & 2" || v.Get("c") != "on" || v.Has("d") || v.Get("s") != "q" || v.Get("e") != "first" || v.Get("t") != "Zeile" || v.Has("_csrf") || v.Has("go") || v.Has("z") || v.Has("tpl") {
		t.Errorf("defaults: %v", v)
	}
	if _, ok := formDefaults(page, "/nope"); ok {
		t.Error("found a form that does not exist")
	}
}
