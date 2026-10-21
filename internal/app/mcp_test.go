// Copyright (C) 2026 yniverz
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

// MCP must give an assistant exactly the rights of the token's owner, like the web interface.

type mcpClient struct {
	t     *testing.T
	base  string
	token string
	id    int
}

func (f *itFixture) mcp(username string, write bool) *mcpClient {
	f.t.Helper()
	var uid int64
	if err := f.a.db.QueryRow("SELECT id FROM users WHERE username=?", username).Scan(&uid); err != nil {
		f.t.Fatal(err)
	}
	tok, err := f.a.newAPIToken(uid, "test", write)
	if err != nil {
		f.t.Fatal(err)
	}
	return &mcpClient{t: f.t, base: f.srv.URL, token: tok}
}

func (m *mcpClient) raw(body string, hdr map[string]string) (int, string) {
	req, _ := http.NewRequest("POST", m.base+"/mcp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if m.token != "" {
		req.Header.Set("Authorization", "Bearer "+m.token)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		m.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func (m *mcpClient) rpc(method string, params any) map[string]any {
	m.t.Helper()
	m.id++
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": m.id, "method": method, "params": params})
	code, body := m.raw(string(b), nil)
	if code != 200 {
		m.t.Fatalf("%s: HTTP %d %s", method, code, body)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		m.t.Fatalf("%s: %v in %s", method, err, body)
	}
	return out
}

// call runs a tool and returns its text and whether it failed.
func (m *mcpClient) call(tool string, args map[string]any) (string, bool) {
	m.t.Helper()
	res := m.rpc("tools/call", map[string]any{"name": tool, "arguments": args})
	r, ok := res["result"].(map[string]any)
	if !ok {
		m.t.Fatalf("%s: no result: %v", tool, res)
	}
	var sb strings.Builder
	for _, c := range r["content"].([]any) {
		if t, ok := c.(map[string]any)["text"].(string); ok {
			sb.WriteString(t)
		}
	}
	isErr, _ := r["isError"].(bool)
	return sb.String(), isErr
}

func (m *mcpClient) ok(tool string, args map[string]any) map[string]any {
	m.t.Helper()
	txt, isErr := m.call(tool, args)
	if isErr {
		m.t.Fatalf("%s %v: %s", tool, args, txt)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(txt), &out); err != nil {
		m.t.Fatalf("%s: %v in %s", tool, err, txt)
	}
	return out
}

func TestMCPAuthAndTransport(t *testing.T) {
	f := newFixture(t)
	f.addUser("helfer", roleByName(f.a, "Helfer:in"))
	m := f.mcp("helfer", false)
	init := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`

	if code, _ := (&mcpClient{t: t, base: f.srv.URL}).raw(init, nil); code != 401 {
		t.Errorf("without token: %d, want 401", code)
	}
	if code, _ := (&mcpClient{t: t, base: f.srv.URL, token: "kx_" + strings.Repeat("0", 64)}).raw(init, nil); code != 401 {
		t.Errorf("unknown token: %d, want 401", code)
	}
	if code, _ := m.raw(init, map[string]string{"Origin": "https://evil.example"}); code != 403 {
		t.Errorf("foreign origin: %d, want 403", code)
	}
	if code, _ := m.raw(init, map[string]string{"MCP-Protocol-Version": "1999-01-01"}); code != 400 {
		t.Errorf("unknown protocol version: %d, want 400", code)
	}
	req, _ := http.NewRequest("GET", f.srv.URL+"/mcp", nil)
	req.Header.Set("Authorization", "Bearer "+m.token)
	if resp, err := http.DefaultClient.Do(req); err != nil || resp.StatusCode != 405 {
		t.Errorf("GET must be 405")
	}
	// a session cookie is no substitute for the token
	web := f.client("helfer", "rolepassword-123")
	if resp := web.do("POST", "/mcp", nil, map[string]string{"Content-Type": "application/json"}); resp.StatusCode != 401 {
		t.Errorf("cookie auth on /mcp: %d, want 401", resp.StatusCode)
	}

	code, body := m.raw(init, nil)
	if code != 200 || !strings.Contains(body, `"protocolVersion":"2025-06-18"`) || !strings.Contains(body, `"instructions"`) {
		t.Fatalf("initialize: %d %s", code, body)
	}
	if code, _ := m.raw(`{"jsonrpc":"2.0","method":"notifications/initialized"}`, nil); code != 202 {
		t.Errorf("notification: %d, want 202", code)
	}
	if res := m.rpc("nope", nil); res["error"] == nil {
		t.Errorf("unknown method must be a JSON-RPC error")
	}
	if code, body := m.raw(`[{"jsonrpc":"2.0","id":1,"method":"ping"},{"jsonrpc":"2.0","id":2,"method":"ping"}]`, nil); code != 200 || strings.Count(body, `"id"`) != 2 {
		t.Errorf("batch: %d %s", code, body)
	}

	// read-only tokens neither list nor run write tools
	var names []string
	for _, tl := range m.rpc("tools/list", map[string]any{})["result"].(map[string]any)["tools"].([]any) {
		names = append(names, tl.(map[string]any)["name"].(string))
	}
	if all := " " + strings.Join(names, " ") + " "; strings.Contains(all, " create_record ") || !strings.Contains(all, " list_records ") {
		t.Errorf("read-only token tool list: %s", names)
	}
	if txt, isErr := m.call("create_record", map[string]any{"event_id": f.ev.ID, "module": "tasks", "fields": map[string]any{"title": "x"}}); !isErr || !strings.Contains(txt, "Lesezugriff") {
		t.Errorf("read-only token could write: %s", txt)
	}

	// disabling the user or revoking the token ends access
	_, _ = f.a.db.Exec("UPDATE users SET disabled=1 WHERE username='helfer'")
	if code, _ := m.raw(init, nil); code != 401 {
		t.Errorf("disabled user: %d, want 401", code)
	}
	_, _ = f.a.db.Exec("UPDATE users SET disabled=0 WHERE username='helfer'")
	_, _ = f.a.db.Exec("DELETE FROM api_tokens")
	if code, _ := m.raw(init, nil); code != 401 {
		t.Errorf("revoked token: %d, want 401", code)
	}
}

func TestMCPRoleMatrix(t *testing.T) {
	f := newFixture(t)
	generic := []string{"areas", "tasks", "permits", "loc_candidates", "budget", "lineup", "staff", "timeline", "equipment", "notes", "power", "checklists", "logistics", "neighbors", "bar_items", "bar_products"}
	finChecked := 0
	for _, role := range f.a.roles() {
		name := "m_" + role.Name
		f.addUser(name, role, f.areaA.ID)
		m := f.mcp(name, true)
		for _, mod := range generic {
			lvl := rolePerm(role.Perms, modByKey[mod].PermKey())
			txt, isErr := m.call("list_records", map[string]any{"event_id": f.ev.ID, "module": mod, "include_done": true})
			if lvl < 1 {
				if !isErr {
					t.Errorf("%s: may not read %s but got %s", role.Name, mod, txt)
				}
				continue
			}
			if isErr {
				t.Errorf("%s: reading %s failed: %s", role.Name, mod, txt)
				continue
			}
			if strings.Contains(txt, "FREMD") {
				t.Errorf("%s: %s leaks the other event", role.Name, mod)
			}
			if role.AreaScoped && modByKey[mod].AreaField != "" && strings.Contains(txt, " B\"") {
				t.Errorf("%s: %s leaks area B: %s", role.Name, mod, txt)
			}
			own := f.recA[mod]
			if mod == "areas" {
				own = f.areaA
			}
			txt, isErr = m.call("update_record", map[string]any{"record_id": own.ID, "fields": map[string]any{}})
			if lvl >= 2 && isErr && !strings.HasPrefix(txt, "Nicht gespeichert") { // validation errors are fine here
				t.Errorf("%s: may edit %s but update failed: %s", role.Name, mod, txt)
			}
			if lvl < 2 && !isErr {
				t.Errorf("%s: may not edit %s but update worked", role.Name, mod)
			}
		}
		// fees stay hidden without the finance right, also for search
		lvlFin := rolePerm(role.Perms, "fin")
		if rolePerm(role.Perms, "lineup") >= 1 && lvlFin < 1 {
			finChecked++
			txt, _ := m.call("list_records", map[string]any{"event_id": f.ev.ID, "module": "lineup"})
			if strings.Contains(txt, "777") || strings.Contains(txt, `"fee"`) {
				t.Errorf("%s: lineup fee visible without finance right: %s", role.Name, txt)
			}
			if txt, _ := m.call("search", map[string]any{"query": "777"}); strings.Contains(txt, "Act A") {
				t.Errorf("%s: search finds records by hidden fee", role.Name)
			}
			if _, isErr := m.call("list_records", map[string]any{"event_id": f.ev.ID, "module": "lineup", "filters": map[string]any{"fee": 777}}); !isErr {
				if txt, _ := m.call("list_records", map[string]any{"event_id": f.ev.ID, "module": "lineup", "filters": map[string]any{"fee": 777}}); strings.Contains(txt, "Act A") {
					t.Errorf("%s: filter on hidden fee reveals records", role.Name)
				}
			}
		}
		// the other event does not exist for members of the first one
		for _, step := range []struct {
			tool string
			args map[string]any
		}{
			{"get_event", map[string]any{"event_id": f.ev2.ID}},
			{"get_record", map[string]any{"record_id": f.other["tasks"].ID}},
			{"list_records", map[string]any{"event_id": f.ev2.ID, "module": "tasks"}},
			{"update_record", map[string]any{"record_id": f.other["tasks"].ID, "fields": map[string]any{"title": "HACK"}}},
			{"delete_record", map[string]any{"record_id": f.other["tasks"].ID}},
		} {
			if txt, isErr := m.call(step.tool, step.args); !isErr {
				t.Errorf("%s: %s on the other event worked: %s", role.Name, step.tool, txt)
			}
		}
		if txt, _ := m.call("search", map[string]any{"query": "FREMD"}); strings.Contains(txt, "FREMD") {
			t.Errorf("%s: search leaks the other event: %s", role.Name, txt)
		}
	}
	if finChecked < 2 {
		t.Errorf("hidden-fee checks ran for %d roles only", finChecked)
	}
	if f.a.rec(f.other["tasks"].ID) == nil || f.a.rec(f.other["tasks"].ID).S("title") == "HACK" {
		t.Fatal("the other event was changed")
	}
}

func TestMCPScopedRoleWritesStayInOwnArea(t *testing.T) {
	f := newFixture(t)
	f.addUser("bar", roleByName(f.a, "Bar-Leitung"), f.areaA.ID)
	m := f.mcp("bar", true)
	if txt, isErr := m.call("get_record", map[string]any{"record_id": f.recB["tasks"].ID}); !isErr {
		t.Errorf("foreign area record readable: %s", txt)
	}
	out := m.ok("create_record", map[string]any{"event_id": f.ev.ID, "module": "tasks", "fields": map[string]any{"title": "Eis bestellen", "area": f.areaB.ID}})
	if got := out["fields"].(map[string]any)["area"]; got != float64(f.areaA.ID) {
		t.Errorf("task created in area %v, want own area %d", got, f.areaA.ID)
	}
	if txt, isErr := m.call("update_record", map[string]any{"record_id": f.recB["tasks"].ID, "fields": map[string]any{"title": "HACK"}}); !isErr {
		t.Errorf("foreign area record writable: %s", txt)
	}
}

func TestMCPWritesValidateLikeTheForm(t *testing.T) {
	f := newFixture(t)
	f.addUser("orga", roleByName(f.a, "Orga-Leitung"))
	m := f.mcp("orga", true)
	ev := f.ev.ID

	out := m.ok("create_record", map[string]any{"event_id": ev, "module": "tasks", "fields": map[string]any{"title": "Bauzaun mieten", "due": "2030-05-01", "status": "In Arbeit", "area": f.areaA.ID}})
	id := int64(out["id"].(float64))
	r := f.a.rec(id)
	if r.S("status") != "doing" || r.S("prio") != "normal" || r.S("due") != "2030-05-01" || r.EventID != ev {
		t.Errorf("created task: %v", r.D)
	}
	for name, fields := range map[string]map[string]any{
		"unknown field":         {"title": "x", "colour": "red"},
		"invalid select":        {"title": "x", "status": "vielleicht"},
		"missing required":      {"title": ""},
		"invalid date":          {"title": "x", "due": "morgen"},
		"area of another event": {"title": "x", "area": f.other["tasks"].I("area")},
	} {
		if txt, isErr := m.call("create_record", map[string]any{"event_id": ev, "module": "tasks", "fields": fields}); !isErr {
			t.Errorf("%s accepted: %s", name, txt)
		}
	}
	// partial update keeps the other fields
	m.ok("update_record", map[string]any{"record_id": id, "fields": map[string]any{"status": "done"}})
	if r := f.a.rec(id); r.S("status") != "done" || r.S("title") != "Bauzaun mieten" || r.S("due") != "2030-05-01" {
		t.Errorf("after update: %v", r.D)
	}
	// relative deadline follows the event date
	out = m.ok("create_record", map[string]any{"event_id": ev, "module": "permits", "fields": map[string]any{"title": "GEMA", "rel": 14}})
	want, _ := relDate(f.ev.Start, "14")
	if got := out["fields"].(map[string]any)["deadline"]; got != want {
		t.Errorf("relative deadline %v, want %s", got, want)
	}
	// bools, money and references
	out = m.ok("update_record", map[string]any{"record_id": f.recA["lineup"].ID, "fields": map[string]any{"paid": true, "fee": 450.5}})
	if fl := out["fields"].(map[string]any); fl["paid"] != true || fl["fee"] != 450.5 {
		t.Errorf("lineup after update: %v", fl)
	}
	// master data needs no event, but the right to edit master data
	if txt, isErr := m.call("create_record", map[string]any{"module": "contacts", "fields": map[string]any{"name": "X"}}); !isErr {
		t.Errorf("contact created without master data right: %s", txt)
	}
	_, _ = f.a.db.Exec("UPDATE users SET can_master=1 WHERE username='orga'")
	out = m.ok("create_record", map[string]any{"module": "contacts", "fields": map[string]any{"name": "Zeltverleih Muster", "kind": "company"}})
	cid := out["id"].(float64)
	m.ok("update_record", map[string]any{"record_id": f.recA["tasks"].ID, "fields": map[string]any{"assignee": cid}})
	if got := f.a.rec(f.recA["tasks"].ID).S("assignee"); got != itoa(int64(cid)) {
		t.Errorf("assignee %q", got)
	}
	// delete and audit trail
	m.ok("delete_record", map[string]any{"record_id": id})
	if f.a.rec(id) != nil {
		t.Error("record not deleted")
	}
	changes := m.ok("get_changes", map[string]any{"event_id": ev})
	if s := fmt.Sprint(changes["changes"]); !strings.Contains(s, "gelöscht (MCP)") || !strings.Contains(s, "angelegt (MCP)") {
		t.Errorf("audit: %s", s)
	}
}

func TestMCPFilesAndReads(t *testing.T) {
	f := newFixture(t)
	f.addUser("orga", roleByName(f.a, "Orga-Leitung"))
	m := f.mcp("orga", true)
	rid := f.recA["permits"].ID
	up := m.ok("upload_file", map[string]any{"record_id": rid, "name": "Bescheid.txt", "text": "Sperrstunde 23 Uhr, maximal 85 dB(A)."})
	fid := up["file_id"].(float64)
	if txt, isErr := m.call("read_file", map[string]any{"file_id": fid}); isErr || !strings.Contains(txt, "Sperrstunde 23 Uhr") {
		t.Errorf("read_file: %s", txt)
	}
	png := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")
	up = m.ok("upload_file", map[string]any{"record_id": rid, "name": "plan.png", "content_base64": base64.StdEncoding.EncodeToString(png)})
	res := m.rpc("tools/call", map[string]any{"name": "read_file", "arguments": map[string]any{"file_id": up["file_id"]}})
	if !strings.Contains(fmt.Sprint(res), "image/png") {
		t.Errorf("image not returned as image: %v", res)
	}
	rec := m.ok("get_record", map[string]any{"record_id": rid})
	if len(rec["files"].([]any)) != 2 {
		t.Errorf("files: %v", rec["files"])
	}
	if hits := fmt.Sprint(m.ok("search", map[string]any{"query": "bescheid"})["hits"]); !strings.Contains(hits, "Bescheid.txt") {
		t.Errorf("file name search: %s", hits)
	}
	// someone without permits access cannot read the file
	f.addUser("helfer", roleByName(f.a, "Helfer:in"))
	if rolePerm(roleByName(f.a, "Helfer:in").Perms, "permits") < 1 {
		h := f.mcp("helfer", false)
		if txt, isErr := h.call("read_file", map[string]any{"file_id": fid}); !isErr {
			t.Errorf("file readable without module access: %s", txt)
		}
	}
	m.ok("delete_file", map[string]any{"file_id": fid})
	if f.a.file(int64(fid)) != nil {
		t.Error("file not deleted")
	}

	ev := m.ok("get_event", map[string]any{"event_id": f.ev.ID})
	if ev["name"] != "Event Eins" || ev["modules"] == nil || ev["kpis"] == nil {
		t.Errorf("get_event: %v", ev)
	}
	if evs := fmt.Sprint(m.ok("list_events", nil)["events"]); strings.Contains(evs, "Event Zwei") || !strings.Contains(evs, "Event Eins") {
		t.Errorf("list_events: %s", evs)
	}
	m.ok("get_timeplan", map[string]any{"event_id": f.ev.ID})
	if fin := m.ok("get_finance", map[string]any{"event_id": f.ev.ID}); fin["expense_plan"] == nil {
		t.Errorf("get_finance: %v", fin)
	}
	if d := m.ok("describe_module", map[string]any{"module": "tasks"}); !strings.Contains(fmt.Sprint(d["fields"]), "In Arbeit") {
		t.Errorf("describe_module: %v", d)
	}
}

func TestAccountTokens(t *testing.T) {
	f := newFixture(t)
	c := f.client("admin", "testpass-12345")
	resp := c.do("POST", "/account/tokens", url.Values{"name": {"Laptop"}, "write": {"1"}}, nil)
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	tok := regexp.MustCompile(`kx_[0-9a-f]{64}`).FindString(string(b))
	if tok == "" || !strings.Contains(string(b), "claude mcp add") {
		t.Fatalf("token not shown: %d", resp.StatusCode)
	}
	if body := c.getBody("/account"); strings.Contains(body, tok) || !strings.Contains(body, "Laptop") {
		t.Error("token must be shown only once, but listed by name")
	}
	var stored string
	_ = f.a.db.QueryRow("SELECT token_hash FROM api_tokens").Scan(&stored)
	if stored == tok || stored != hashToken(tok) {
		t.Error("token must be stored hashed")
	}
	if u, w := f.a.apiTokenUser(tok); u == nil || !w {
		t.Error("token does not authenticate")
	}
	// another user cannot revoke it
	f.addUser("other", roleByName(f.a, "Helfer:in"))
	o := f.client("other", "rolepassword-123")
	var tid int64
	_ = f.a.db.QueryRow("SELECT id FROM api_tokens").Scan(&tid)
	o.status("POST", fmt.Sprintf("/account/tokens/%d/delete", tid), url.Values{})
	if u, _ := f.a.apiTokenUser(tok); u == nil {
		t.Error("foreign user revoked the token")
	}
	if st := c.status("POST", fmt.Sprintf("/account/tokens/%d/delete", tid), url.Values{"_csrf": {"wrong"}}); st != 403 {
		t.Errorf("revoke without CSRF: %d", st)
	}
	c.status("POST", fmt.Sprintf("/account/tokens/%d/delete", tid), url.Values{})
	if u, _ := f.a.apiTokenUser(tok); u != nil {
		t.Error("token still valid after revoke")
	}
}
