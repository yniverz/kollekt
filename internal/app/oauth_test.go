// Copyright (C) 2026 yniverz
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"
)

const claudeCallback = "https://claude.ai/api/mcp/auth_callback"

func pkcePair() (verifier, challenge string) {
	verifier = randToken(32) // 64 characters
	sum := sha256.Sum256([]byte(verifier))
	return verifier, base64.RawURLEncoding.EncodeToString(sum[:])
}

func postJSON(t *testing.T, u string, body any) (int, map[string]any) {
	t.Helper()
	b, _ := json.Marshal(body)
	resp, err := http.Post(u, "application/json", strings.NewReader(string(b)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func postForm(t *testing.T, u string, form url.Values, basic ...string) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest("POST", u, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if len(basic) == 2 {
		req.SetBasicAuth(basic[0], basic[1])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func register(t *testing.T, f *itFixture, body map[string]any) string {
	t.Helper()
	code, out := postJSON(t, f.srv.URL+"/oauth/register", body)
	if code != 201 {
		t.Fatalf("register: %d %v", code, out)
	}
	return out["client_id"].(string)
}

func authzQuery(clientID, redirect, challenge string) url.Values {
	return url.Values{"response_type": {"code"}, "client_id": {clientID}, "redirect_uri": {redirect}, "state": {"st-123"},
		"code_challenge": {challenge}, "code_challenge_method": {"S256"}, "scope": {"read write"}}
}

// consent opens the consent page and submits the decision; it returns the redirect location.
func consent(t *testing.T, c *itClient, q url.Values, decision string, write bool) *url.URL {
	t.Helper()
	resp := c.do("GET", "/oauth/authorize?"+q.Encode(), nil, nil)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("consent page: %d %s", resp.StatusCode, body)
	}
	form := url.Values{}
	for k, v := range q {
		form[k] = v
	}
	form.Set("decision", decision)
	if write {
		form.Set("write", "1")
	}
	resp = c.do("POST", "/oauth/authorize", form, nil)
	resp.Body.Close()
	if resp.StatusCode != 302 {
		t.Fatalf("decision: %d", resp.StatusCode)
	}
	loc, _ := url.Parse(resp.Header.Get("Location"))
	return loc
}

func TestOAuthDiscovery(t *testing.T) {
	f := newFixture(t)
	resp, _ := http.Post(f.srv.URL+"/mcp", "application/json", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`))
	resp.Body.Close()
	hdr := resp.Header.Get("WWW-Authenticate")
	if resp.StatusCode != 401 || !strings.Contains(hdr, `resource_metadata="`+f.srv.URL+`/.well-known/oauth-protected-resource/mcp"`) {
		t.Fatalf("401 without metadata pointer: %d %q", resp.StatusCode, hdr)
	}
	var prm, asm map[string]any
	for path, dst := range map[string]*map[string]any{"/.well-known/oauth-protected-resource/mcp": &prm, "/.well-known/oauth-authorization-server": &asm} {
		r, err := http.Get(f.srv.URL + path)
		if err != nil || r.StatusCode != 200 || r.Header.Get("Access-Control-Allow-Origin") != "*" {
			t.Fatalf("%s: %v", path, err)
		}
		_ = json.NewDecoder(r.Body).Decode(dst)
		r.Body.Close()
	}
	if prm["resource"] != f.srv.URL+"/mcp" || prm["authorization_servers"].([]any)[0] != f.srv.URL {
		t.Errorf("protected resource metadata: %v", prm)
	}
	if asm["issuer"] != f.srv.URL || asm["client_id_metadata_document_supported"] != true ||
		!strings.Contains(strings.Join(toStrings(asm["code_challenge_methods_supported"]), " "), "S256") ||
		!strings.Contains(strings.Join(toStrings(asm["token_endpoint_auth_methods_supported"]), " "), "none") {
		t.Errorf("server metadata: %v", asm)
	}
	// a configured public URL wins over the Host header
	f.a.cfg.PublicURL = "https://kollekt.example.org"
	r, _ := http.Get(f.srv.URL + "/.well-known/oauth-protected-resource")
	_ = json.NewDecoder(r.Body).Decode(&prm)
	r.Body.Close()
	if prm["resource"] != "https://kollekt.example.org/mcp" {
		t.Errorf("public URL ignored: %v", prm["resource"])
	}
}

func toStrings(v any) []string {
	var out []string
	for _, x := range v.([]any) {
		out = append(out, x.(string))
	}
	return out
}

func TestOAuthFullFlow(t *testing.T) {
	f := newFixture(t)
	f.addUser("orga", roleByName(f.a, "Orga-Leitung"))
	cid := register(t, f, map[string]any{"client_name": "Claude", "redirect_uris": []string{claudeCallback}})
	verifier, challenge := pkcePair()
	q := authzQuery(cid, claudeCallback, challenge)

	// signed out: off to the login page and back
	anon := f.client("", "")
	resp := anon.do("GET", "/oauth/authorize?"+q.Encode(), nil, nil)
	resp.Body.Close()
	if loc := resp.Header.Get("Location"); resp.StatusCode != 303 || !strings.HasPrefix(loc, "/login?next=") {
		t.Fatalf("signed out: %d %s", resp.StatusCode, loc)
	}

	c := f.client("orga", "rolepassword-123")
	loc := consent(t, c, q, "allow", false)
	if loc.Scheme+"://"+loc.Host+loc.Path != claudeCallback || loc.Query().Get("state") != "st-123" || loc.Query().Get("iss") != f.srv.URL {
		t.Fatalf("redirect: %s", loc)
	}
	code := loc.Query().Get("code")

	// wrong verifier burns the code
	if st, out := postForm(t, f.srv.URL+"/oauth/token", url.Values{"grant_type": {"authorization_code"}, "code": {code}, "client_id": {cid}, "redirect_uri": {claudeCallback}, "code_verifier": {strings.Repeat("x", 64)}}); st != 400 || out["error"] != "invalid_grant" {
		t.Fatalf("wrong verifier: %d %v", st, out)
	}
	if st, _ := postForm(t, f.srv.URL+"/oauth/token", url.Values{"grant_type": {"authorization_code"}, "code": {code}, "client_id": {cid}, "redirect_uri": {claudeCallback}, "code_verifier": {verifier}}); st != 400 {
		t.Fatal("a code must be usable only once")
	}

	loc = consent(t, c, q, "allow", false)
	st, tok := postForm(t, f.srv.URL+"/oauth/token", url.Values{"grant_type": {"authorization_code"}, "code": {loc.Query().Get("code")}, "client_id": {cid}, "redirect_uri": {claudeCallback}, "code_verifier": {verifier}})
	if st != 200 || tok["token_type"] != "Bearer" || tok["scope"] != "read" {
		t.Fatalf("token: %d %v", st, tok)
	}
	access, refresh := tok["access_token"].(string), tok["refresh_token"].(string)
	m := &mcpClient{t: t, base: f.srv.URL, token: access}
	if evs := m.ok("list_events", nil); evs["token_can_write"] != false {
		t.Errorf("read-only consent gave write access: %v", evs)
	}
	if txt, isErr := m.call("create_record", map[string]any{"event_id": f.ev.ID, "module": "tasks", "fields": map[string]any{"title": "x"}}); !isErr {
		t.Errorf("read-only grant could write: %s", txt)
	}

	// refresh rotates both tokens
	st, tok2 := postForm(t, f.srv.URL+"/oauth/token", url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}, "client_id": {cid}})
	if st != 200 || tok2["access_token"] == access || tok2["refresh_token"] == refresh {
		t.Fatalf("refresh: %d %v", st, tok2)
	}
	if st, out := postForm(t, f.srv.URL+"/oauth/token", url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}, "client_id": {cid}}); st != 400 || out["error"] != "invalid_grant" {
		t.Errorf("old refresh token still works: %d %v", st, out)
	}
	if code, _ := m.raw(`{"jsonrpc":"2.0","id":1,"method":"ping"}`, nil); code != 401 {
		t.Errorf("old access token still works: %d", code)
	}
	m.token = tok2["access_token"].(string)
	m.ok("list_events", nil)

	// expired access token: 401 with invalid_token so the client refreshes
	_, _ = f.a.db.Exec("UPDATE oauth_grants SET access_expires=?", time.Now().Add(-time.Minute).Unix())
	req, _ := http.NewRequest("POST", f.srv.URL+"/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
	req.Header.Set("Authorization", "Bearer "+m.token)
	resp, _ = http.DefaultClient.Do(req)
	resp.Body.Close()
	if resp.StatusCode != 401 || !strings.Contains(resp.Header.Get("WWW-Authenticate"), `error="invalid_token"`) {
		t.Errorf("expired token: %d %s", resp.StatusCode, resp.Header.Get("WWW-Authenticate"))
	}

	// the connection is listed on the account page and can be ended there
	body := c.getBody("/account")
	if !strings.Contains(body, "Claude") {
		t.Fatal("connection not listed")
	}
	gid := regexp.MustCompile(`/account/connections/(\d+)/delete`).FindStringSubmatch(body)[1]
	c.status("POST", "/account/connections/"+gid+"/delete", url.Values{})
	if st, _ := postForm(t, f.srv.URL+"/oauth/token", url.Values{"grant_type": {"refresh_token"}, "refresh_token": {tok2["refresh_token"].(string)}, "client_id": {cid}}); st != 400 {
		t.Error("refresh works after disconnecting")
	}
}

func TestOAuthWriteConsentAndDeny(t *testing.T) {
	f := newFixture(t)
	f.addUser("orga", roleByName(f.a, "Orga-Leitung"))
	c := f.client("orga", "rolepassword-123")
	cid := register(t, f, map[string]any{"client_name": "Claude", "redirect_uris": []string{claudeCallback}})
	verifier, challenge := pkcePair()
	q := authzQuery(cid, claudeCallback, challenge)

	if loc := consent(t, c, q, "deny", false); loc.Query().Get("error") != "access_denied" || loc.Query().Get("code") != "" {
		t.Errorf("deny: %s", loc)
	}
	loc := consent(t, c, q, "allow", true)
	_, tok := postForm(t, f.srv.URL+"/oauth/token", url.Values{"grant_type": {"authorization_code"}, "code": {loc.Query().Get("code")}, "client_id": {cid}, "redirect_uri": {claudeCallback}, "code_verifier": {verifier}})
	if tok["scope"] != "read write" {
		t.Fatalf("write consent: %v", tok)
	}
	m := &mcpClient{t: t, base: f.srv.URL, token: tok["access_token"].(string)}
	m.ok("create_record", map[string]any{"event_id": f.ev.ID, "module": "tasks", "fields": map[string]any{"title": "Über OAuth"}})
	// an app that asks only for read access cannot be given write access
	q.Set("scope", "read")
	loc = consent(t, c, q, "allow", true)
	_, tok = postForm(t, f.srv.URL+"/oauth/token", url.Values{"grant_type": {"authorization_code"}, "code": {loc.Query().Get("code")}, "client_id": {cid}, "redirect_uri": {claudeCallback}, "code_verifier": {verifier}})
	if tok["scope"] != "read" {
		t.Errorf("read request turned into write: %v", tok)
	}
	// a disabled account loses access immediately
	_, _ = f.a.db.Exec("UPDATE users SET disabled=1 WHERE username='orga'")
	if code, _ := m.raw(`{"jsonrpc":"2.0","id":1,"method":"ping"}`, nil); code != 401 {
		t.Errorf("disabled user: %d", code)
	}
}

func TestOAuthRejectsBadRequests(t *testing.T) {
	f := newFixture(t)
	c := f.client("admin", "testpass-12345")
	cid := register(t, f, map[string]any{"client_name": "Claude Code", "redirect_uris": []string{"http://localhost/callback", "http://127.0.0.1/callback"}})
	_, challenge := pkcePair()

	// loopback callbacks match on any port, everything else exactly
	if st, _ := c.partial("/oauth/authorize?" + authzQuery(cid, "http://localhost:53682/callback", challenge).Encode()); st != 200 {
		t.Errorf("loopback with port refused: %d", st)
	}
	for _, ru := range []string{"https://evil.example/callback", "http://localhost:53682/other", "http://evil.example/callback"} {
		if st, _ := c.partial("/oauth/authorize?" + authzQuery(cid, ru, challenge).Encode()); st != 400 {
			t.Errorf("redirect %s accepted: %d", ru, st)
		}
	}
	if st, _ := c.partial("/oauth/authorize?" + authzQuery("kc_unknown", claudeCallback, challenge).Encode()); st != 400 {
		t.Errorf("unknown client accepted: %d", st)
	}
	// PKCE is mandatory, reported to the app
	q := authzQuery(cid, "http://127.0.0.1:9999/callback", challenge)
	q.Del("code_challenge_method")
	resp := c.do("GET", "/oauth/authorize?"+q.Encode(), nil, nil)
	resp.Body.Close()
	if loc, _ := url.Parse(resp.Header.Get("Location")); resp.StatusCode != 302 || loc.Query().Get("error") != "invalid_request" {
		t.Errorf("missing PKCE: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	q = authzQuery(cid, "http://127.0.0.1:9999/callback", challenge)
	q.Set("resource", "https://other.example/mcp")
	resp = c.do("GET", "/oauth/authorize?"+q.Encode(), nil, nil)
	resp.Body.Close()
	if loc, _ := url.Parse(resp.Header.Get("Location")); loc == nil || loc.Query().Get("error") != "invalid_target" {
		t.Errorf("foreign resource: %s", resp.Header.Get("Location"))
	}
	// the consent decision needs the session's CSRF token
	form := url.Values{}
	for k, v := range authzQuery(cid, "http://127.0.0.1:9999/callback", challenge) {
		form[k] = v
	}
	form.Set("decision", "allow")
	form.Set("_csrf", "wrong")
	if st := c.status("POST", "/oauth/authorize", form); st != 403 {
		t.Errorf("consent without CSRF: %d", st)
	}

	// registration only takes https or loopback callbacks
	for _, ru := range []string{"http://evil.example/cb", "javascript:alert(1)", "https://x.example/cb#frag"} {
		if st, _ := postJSON(t, f.srv.URL+"/oauth/register", map[string]any{"redirect_uris": []string{ru}}); st != 400 {
			t.Errorf("registered %s: %d", ru, st)
		}
	}
	// confidential clients must authenticate
	st, reg := postJSON(t, f.srv.URL+"/oauth/register", map[string]any{"redirect_uris": []string{claudeCallback}, "token_endpoint_auth_method": "client_secret_basic"})
	if st != 201 || reg["client_secret"] == nil {
		t.Fatalf("confidential register: %d %v", st, reg)
	}
	ccid := reg["client_id"].(string)
	if st, out := postForm(t, f.srv.URL+"/oauth/token", url.Values{"grant_type": {"refresh_token"}, "refresh_token": {"kr_x"}}, ccid, "wrong"); st != 401 || out["error"] != "invalid_client" {
		t.Errorf("wrong client secret: %d %v", st, out)
	}
	if st, out := postForm(t, f.srv.URL+"/oauth/token", url.Values{"grant_type": {"refresh_token"}, "refresh_token": {"kr_x"}}, ccid, reg["client_secret"].(string)); st != 400 || out["error"] != "invalid_grant" {
		t.Errorf("right secret, bad grant: %d %v", st, out)
	}
	// with MCP switched off, OAuth is gone too
	mcpOff = true
	defer func() { mcpOff = false }()
	if r, _ := http.Get(f.srv.URL + "/.well-known/oauth-authorization-server"); r.StatusCode != 404 {
		t.Errorf("metadata with MCP off: %d", r.StatusCode)
	}
}

func TestClientMetadataDocuments(t *testing.T) {
	// internal addresses are refused, so a client ID cannot probe the local network
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"client_id":"x","redirect_uris":["http://localhost/callback"]}`))
	}))
	defer srv.Close()
	if _, err := fetchClientMetadata(srv.URL + "/meta"); err == nil {
		t.Error("metadata fetched from a loopback address")
	}
	for _, id := range []string{"http://example.org/meta", "https://example.org", "https://example.org/meta#x"} {
		if _, err := fetchClientMetadata(id); err == nil {
			t.Errorf("accepted client ID %s", id)
		}
	}
	cl := &oauthClient{RedirectURIs: []string{"http://localhost/callback", "http://127.0.0.1/callback", claudeCallback}}
	for ru, want := range map[string]bool{
		"http://localhost:3118/callback": true, "http://127.0.0.1:1/callback": true, claudeCallback: true,
		"http://localhost:3118/callback?x=1": false, "http://127.0.0.2:80/callback": false,
		"https://claude.ai/api/mcp/auth_callback/x": false, "http://localhost.evil.example:80/callback": false,
	} {
		if got := cl.redirectAllowed(ru); got != want {
			t.Errorf("redirectAllowed(%s) = %v, want %v", ru, got, want)
		}
	}
}
