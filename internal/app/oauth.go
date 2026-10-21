// Copyright (C) 2026 yniverz
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"
)

// OAuth 2.1 authorization server for the MCP endpoint, as the MCP authorization spec and Claude's
// connector requirements expect: protected resource metadata (RFC 9728), server metadata (RFC 8414),
// dynamic client registration (RFC 7591), client ID metadata documents, authorization code with
// PKCE S256, rotating refresh tokens and revocation (RFC 7009). The person signs in to Kollekt as
// usual and decides on a consent page whether the app may only read or also write.

const (
	oauthAccessPrefix  = "ko_"
	oauthRefreshPrefix = "kr_"
	oauthAccessTTL     = time.Hour
	oauthRefreshTTL    = 30 * 24 * time.Hour
	oauthCodeTTL       = 2 * time.Minute
	maxOAuthGrants     = 30
)

// publicBase is the external base URL (KOLLEKT_PUBLIC_URL), or derived from the request.
func (a *App) publicBase(r *http.Request) string {
	if a.cfg.PublicURL != "" {
		return a.cfg.PublicURL
	}
	scheme := "http"
	if isHTTPS(r) {
		scheme = "https"
	}
	host := r.Host
	if trustProxy {
		if h := r.Header.Get("X-Forwarded-Host"); h != "" {
			host = h
		}
	}
	return scheme + "://" + host
}

func (a *App) mcpResource(r *http.Request) string { return a.publicBase(r) + "/mcp" }

func (a *App) resourceMetadataURL(r *http.Request) string {
	return a.publicBase(r) + "/.well-known/oauth-protected-resource/mcp"
}

// oauthJSON answers token, registration and metadata requests; none of them rely on cookies,
// so any origin may call them.
func oauthJSON(w http.ResponseWriter, code int, v any) {
	h := w.Header()
	h.Set("Content-Type", "application/json")
	h.Set("Cache-Control", "no-store")
	h.Set("Pragma", "no-cache")
	h.Set("Access-Control-Allow-Origin", "*")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func oauthErr(w http.ResponseWriter, code int, kind, desc string) {
	oauthJSON(w, code, map[string]string{"error": kind, "error_description": desc})
}

// cors wraps the public OAuth endpoints and answers preflight requests.
func cors(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if mcpOff {
			http.NotFound(w, r)
			return
		}
		if r.Method == http.MethodOptions {
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, MCP-Protocol-Version")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		h(w, r)
	}
}

// ---------- metadata ----------

func (a *App) oauthProtectedResource(w http.ResponseWriter, r *http.Request) {
	base := a.publicBase(r)
	oauthJSON(w, 200, map[string]any{
		"resource":                 base + "/mcp",
		"authorization_servers":    []string{base},
		"scopes_supported":         []string{"read", "write"},
		"bearer_methods_supported": []string{"header"},
		"resource_name":            "Kollekt",
	})
}

func (a *App) oauthServerMetadata(w http.ResponseWriter, r *http.Request) {
	base := a.publicBase(r)
	oauthJSON(w, 200, map[string]any{
		"issuer":                                         base,
		"authorization_endpoint":                         base + "/oauth/authorize",
		"token_endpoint":                                 base + "/oauth/token",
		"registration_endpoint":                          base + "/oauth/register",
		"revocation_endpoint":                            base + "/oauth/revoke",
		"response_types_supported":                       []string{"code"},
		"grant_types_supported":                          []string{"authorization_code", "refresh_token"},
		"code_challenge_methods_supported":               []string{"S256"},
		"token_endpoint_auth_methods_supported":          []string{"none", "client_secret_post", "client_secret_basic"},
		"revocation_endpoint_auth_methods_supported":     []string{"none", "client_secret_post", "client_secret_basic"},
		"scopes_supported":                               []string{"read", "write", "offline_access"},
		"client_id_metadata_document_supported":          true,
		"authorization_response_iss_parameter_supported": true,
	})
}

// ---------- clients ----------

type oauthClient struct {
	ID           string
	Name         string
	RedirectURIs []string
	SecretHash   string
	Metadata     bool // identified by a client ID metadata document
}

func (a *App) dbClient(id string) *oauthClient {
	cl := &oauthClient{ID: id}
	var uris string
	if a.db.QueryRow("SELECT name,redirect_uris,secret_hash FROM oauth_clients WHERE client_id=?", id).Scan(&cl.Name, &uris, &cl.SecretHash) != nil {
		return nil
	}
	_ = json.Unmarshal([]byte(uris), &cl.RedirectURIs)
	return cl
}

// lookupClient resolves a registered client or fetches the metadata document of a URL client ID.
func (a *App) lookupClient(id string) (*oauthClient, error) {
	if strings.HasPrefix(id, "https://") {
		return fetchClientMetadata(id)
	}
	if cl := a.dbClient(id); cl != nil {
		return cl, nil
	}
	return nil, errors.New("Unbekannte App (client_id). Bitte die Verbindung in der App neu einrichten.")
}

var (
	cimdMu    sync.Mutex
	cimdCache = map[string]cimdEntry{}
)

type cimdEntry struct {
	cl  *oauthClient
	exp time.Time
}

// cimdClient fetches metadata documents without following redirects and refuses internal addresses,
// so a client ID cannot make the server probe the local network.
var cimdClient = &http.Client{
	Timeout:       5 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	Transport: &http.Transport{
		Proxy:               http.ProxyFromEnvironment,
		DialContext:         guardedDial,
		TLSHandshakeTimeout: 5 * time.Second,
	},
}

// guardedDial refuses connections to internal addresses. An outbound proxy configured by the operator
// (HTTPS_PROXY) is trusted; the target behind it is checked by name in fetchClientMetadata.
func guardedDial(ctx context.Context, network, addr string) (net.Conn, error) {
	d := &net.Dialer{Timeout: 5 * time.Second}
	if !proxyAddrs()[addr] {
		d.Control = func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			if ip := net.ParseIP(host); ip == nil || !publicIP(ip) {
				return fmt.Errorf("address %s not allowed", host)
			}
			return nil
		}
	}
	return d.DialContext(ctx, network, addr)
}

func proxyAddrs() map[string]bool {
	out := map[string]bool{}
	for _, k := range []string{"HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy"} {
		u, err := url.Parse(os.Getenv(k))
		if err != nil || u.Host == "" {
			continue
		}
		port := u.Port()
		if port == "" {
			port = map[bool]string{true: "443", false: "80"}[u.Scheme == "https"]
		}
		out[net.JoinHostPort(u.Hostname(), port)] = true
	}
	return out
}

func publicIP(ip net.IP) bool {
	return !(ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsUnspecified() || ip.IsMulticast() || ip.IsInterfaceLocalMulticast())
}

func fetchClientMetadata(id string) (*oauthClient, error) {
	u, err := url.Parse(id)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.Path == "" || u.Path == "/" || u.Fragment != "" || u.User != nil {
		return nil, errors.New("Ungültige client_id.")
	}
	cimdMu.Lock()
	if e, ok := cimdCache[id]; ok && time.Now().Before(e.exp) {
		cimdMu.Unlock()
		return e.cl, nil
	}
	cimdMu.Unlock()
	fail := errors.New("Die Beschreibung der App (Client ID Metadata Document) ist nicht abrufbar oder ungültig.")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// checked here as well, because behind a proxy the dial goes to the proxy, not to the target
	if ip := net.ParseIP(u.Hostname()); ip != nil && !publicIP(ip) {
		return nil, fail
	}
	if ips, err := net.DefaultResolver.LookupIPAddr(ctx, u.Hostname()); err == nil {
		for _, ip := range ips {
			if !publicIP(ip.IP) {
				return nil, fail
			}
		}
	}
	req, _ := http.NewRequestWithContext(ctx, "GET", id, nil)
	req.Header.Set("Accept", "application/json")
	resp, err := cimdClient.Do(req)
	if err != nil {
		return nil, fail
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fail
	}
	var doc struct {
		ClientID     string   `json:"client_id"`
		ClientName   string   `json:"client_name"`
		RedirectURIs []string `json:"redirect_uris"`
		AuthMethod   string   `json:"token_endpoint_auth_method"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&doc) != nil || doc.ClientID != id || len(doc.RedirectURIs) == 0 {
		return nil, fail
	}
	if doc.AuthMethod != "" && doc.AuthMethod != "none" {
		return nil, errors.New("Diese App verlangt eine Client-Authentifizierung, die Kollekt für Metadaten-Dokumente nicht unterstützt.")
	}
	for _, ru := range doc.RedirectURIs {
		if !validRedirect(ru) {
			return nil, fail
		}
	}
	name := strings.TrimSpace(doc.ClientName)
	if name == "" {
		name = u.Host
	}
	cl := &oauthClient{ID: id, Name: clip(name, 100), RedirectURIs: doc.RedirectURIs, Metadata: true}
	cimdMu.Lock()
	if len(cimdCache) > 200 {
		cimdCache = map[string]cimdEntry{}
	}
	cimdCache[id] = cimdEntry{cl, time.Now().Add(10 * time.Minute)}
	cimdMu.Unlock()
	return cl, nil
}

func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

func isLoopbackHost(h string) bool {
	return h == "localhost" || h == "127.0.0.1" || h == "::1"
}

// validRedirect accepts https callbacks and http callbacks on this computer (native apps, RFC 8252).
func validRedirect(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Fragment != "" || u.User != nil || u.Host == "" {
		return false
	}
	return u.Scheme == "https" || (u.Scheme == "http" && isLoopbackHost(u.Hostname()))
}

// redirectAllowed matches exactly, or for loopback callbacks with any port (RFC 8252 section 7.3).
func (cl *oauthClient) redirectAllowed(raw string) bool {
	got, err := url.Parse(raw)
	if err != nil || !validRedirect(raw) {
		return false
	}
	for _, reg := range cl.RedirectURIs {
		if reg == raw {
			return true
		}
		want, err := url.Parse(reg)
		if err != nil || got.Scheme != "http" || want.Scheme != "http" {
			continue
		}
		if isLoopbackHost(got.Hostname()) && got.Hostname() == want.Hostname() && got.Path == want.Path && got.RawQuery == want.RawQuery {
			return true
		}
	}
	return false
}

// ---------- dynamic client registration ----------

func (a *App) oauthRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		oauthErr(w, 405, "invalid_request", "POST erwartet.")
		return
	}
	key := "oauthreg|" + clientIP(r)
	if !loginThrottle.allow(key, 20, time.Hour) {
		oauthErr(w, 429, "invalid_request", "Zu viele Registrierungen. Bitte später erneut versuchen.")
		return
	}
	loginThrottle.hit(key)
	var req struct {
		RedirectURIs []string `json:"redirect_uris"`
		ClientName   string   `json:"client_name"`
		AuthMethod   string   `json:"token_endpoint_auth_method"`
		GrantTypes   []string `json:"grant_types"`
	}
	if json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&req) != nil {
		oauthErr(w, 400, "invalid_client_metadata", "Ungültiges JSON.")
		return
	}
	if len(req.RedirectURIs) == 0 || len(req.RedirectURIs) > 10 {
		oauthErr(w, 400, "invalid_redirect_uri", "redirect_uris fehlt.")
		return
	}
	for _, ru := range req.RedirectURIs {
		if len(ru) > 500 || !validRedirect(ru) {
			oauthErr(w, 400, "invalid_redirect_uri", "Nur https-Adressen oder http auf localhost sind erlaubt: "+clip(ru, 100))
			return
		}
	}
	for _, g := range req.GrantTypes {
		if g != "authorization_code" && g != "refresh_token" {
			oauthErr(w, 400, "invalid_client_metadata", "Nicht unterstützter grant_type: "+clip(g, 50))
			return
		}
	}
	method := req.AuthMethod
	switch method {
	case "":
		method = "none"
	case "none", "client_secret_post", "client_secret_basic":
	default:
		oauthErr(w, 400, "invalid_client_metadata", "Nicht unterstützte token_endpoint_auth_method.")
		return
	}
	name := clip(strings.TrimSpace(req.ClientName), 100)
	if name == "" {
		name = "MCP-Client"
	}
	id := "kc_" + randToken(16)
	secret, secretHash := "", ""
	if method != "none" {
		secret = "ks_" + randToken(32)
		secretHash = hashToken(secret)
	}
	uris, _ := json.Marshal(req.RedirectURIs)
	now := time.Now().Unix()
	if _, err := a.db.Exec("INSERT INTO oauth_clients(client_id,name,redirect_uris,secret_hash,created_at) VALUES(?,?,?,?,?)", id, name, string(uris), secretHash, now); err != nil {
		oauthErr(w, 500, "server_error", "Registrierung fehlgeschlagen.")
		return
	}
	out := map[string]any{
		"client_id": id, "client_id_issued_at": now, "client_name": name, "redirect_uris": req.RedirectURIs,
		"token_endpoint_auth_method": method, "grant_types": []string{"authorization_code", "refresh_token"}, "response_types": []string{"code"},
	}
	if secret != "" {
		out["client_secret"], out["client_secret_expires_at"] = secret, 0
	}
	oauthJSON(w, http.StatusCreated, out)
}

// ---------- authorization ----------

type authzReq struct {
	Client      *oauthClient
	RedirectURI string
	State       string
	Challenge   string
	Resource    string
	WantWrite   bool // the app asks for write access
	Scope       string
}

// parseAuthz validates an authorization request. Problems with client or redirect URI are shown to
// the person (never redirected); everything else is reported to the app via the redirect.
func (a *App) parseAuthz(r *http.Request, v url.Values) (*authzReq, string, string) {
	cl, err := a.lookupClient(v.Get("client_id"))
	if err != nil {
		return nil, err.Error(), ""
	}
	ru := v.Get("redirect_uri")
	if ru == "" || !cl.redirectAllowed(ru) {
		return nil, "Die Rücksprungadresse (redirect_uri) ist für diese App nicht registriert.", ""
	}
	q := &authzReq{Client: cl, RedirectURI: ru, State: v.Get("state"), Challenge: v.Get("code_challenge"), Resource: v.Get("resource"), Scope: v.Get("scope")}
	if v.Get("response_type") != "code" {
		return q, "", "unsupported_response_type"
	}
	if v.Get("code_challenge_method") != "S256" || len(q.Challenge) < 43 || len(q.Challenge) > 128 {
		return q, "", "invalid_request"
	}
	if q.Resource != "" && strings.TrimSuffix(q.Resource, "/") != a.mcpResource(r) && strings.TrimSuffix(q.Resource, "/") != a.publicBase(r) {
		return q, "", "invalid_target"
	}
	scopes := strings.Fields(q.Scope)
	q.WantWrite = len(scopes) == 0
	for _, s := range scopes {
		switch s {
		case "write":
			q.WantWrite = true
		case "read", "offline_access":
		default:
			return q, "", "invalid_scope"
		}
	}
	return q, "", ""
}

func (a *App) authzRedirect(w http.ResponseWriter, r *http.Request, q *authzReq, params url.Values) {
	params.Set("iss", a.publicBase(r))
	if q.State != "" {
		params.Set("state", q.State)
	}
	sep := "?"
	if strings.Contains(q.RedirectURI, "?") {
		sep = "&"
	}
	http.Redirect(w, r, q.RedirectURI+sep+params.Encode(), http.StatusFound)
}

func (a *App) oauthAuthorizePage(c *C) {
	if mcpOff {
		c.Error(404, "Die MCP-Schnittstelle ist in dieser Installation abgeschaltet.")
		return
	}
	q, fatal, kind := a.parseAuthz(c.R, c.R.URL.Query())
	if fatal != "" {
		c.Error(400, fatal)
		return
	}
	if kind != "" {
		a.authzRedirect(c.W, c.R, q, url.Values{"error": {kind}})
		return
	}
	ru, _ := url.Parse(q.RedirectURI)
	// the form posts here and is then redirected to the app: allow that target for form-action
	target := ru.Scheme + "://" + ru.Host
	if ru.Port() != "" && isLoopbackHost(ru.Hostname()) {
		target = ru.Scheme + "://" + ru.Hostname() + ":*"
	}
	c.W.Header().Set("Content-Security-Policy", strings.Replace(contentSecurityPolicy(), "form-action 'self'", "form-action 'self' "+target, 1))
	c.renderTpl("oauth_consent.html", map[string]any{
		"Title": "Zugriff erlauben", "Q": q, "Params": c.R.URL.Query(), "RedirectHost": ru.Host,
		"Loopback": isLoopbackHost(ru.Hostname()),
	})
}

func (a *App) oauthAuthorizeDecide(c *C) {
	if mcpOff {
		c.Error(404, "Die MCP-Schnittstelle ist in dieser Installation abgeschaltet.")
		return
	}
	q, fatal, kind := a.parseAuthz(c.R, c.R.PostForm)
	if fatal != "" {
		c.Error(400, fatal)
		return
	}
	if kind != "" {
		a.authzRedirect(c.W, c.R, q, url.Values{"error": {kind}})
		return
	}
	if c.R.FormValue("decision") != "allow" {
		a.authzRedirect(c.W, c.R, q, url.Values{"error": {"access_denied"}})
		return
	}
	write := q.WantWrite && c.R.FormValue("write") != ""
	code := randToken(32)
	if _, err := a.db.Exec("INSERT INTO oauth_codes(code_hash,client_id,client_name,user_id,redirect_uri,challenge,can_write,expires_at) VALUES(?,?,?,?,?,?,?,?)",
		hashToken(code), q.Client.ID, q.Client.Name, c.User.ID, q.RedirectURI, q.Challenge, b2i(write), time.Now().Add(oauthCodeTTL).Unix()); err != nil {
		c.Error(500, "Anmeldung fehlgeschlagen.")
		return
	}
	a.authzRedirect(c.W, c.R, q, url.Values{"code": {code}})
}

// ---------- token endpoint ----------

// clientAuth checks the client's credentials at the token endpoint. Public clients send only their ID.
func (a *App) clientAuth(r *http.Request) (string, bool) {
	id, secret, basic := r.BasicAuth()
	if basic {
		if u, err := url.QueryUnescape(id); err == nil {
			id = u
		}
		if s, err := url.QueryUnescape(secret); err == nil {
			secret = s
		}
	} else {
		id, secret = r.PostFormValue("client_id"), r.PostFormValue("client_secret")
	}
	if id == "" {
		return "", false
	}
	if strings.HasPrefix(id, "https://") {
		return id, secret == ""
	}
	cl := a.dbClient(id)
	if cl == nil {
		return "", false
	}
	if cl.SecretHash == "" {
		return id, secret == ""
	}
	return id, subtle.ConstantTimeCompare([]byte(hashToken(secret)), []byte(cl.SecretHash)) == 1
}

func pkceOK(verifier, challenge string) bool {
	if len(verifier) < 43 || len(verifier) > 128 {
		return false
	}
	sum := sha256.Sum256([]byte(verifier))
	return subtle.ConstantTimeCompare([]byte(base64.RawURLEncoding.EncodeToString(sum[:])), []byte(challenge)) == 1
}

func (a *App) oauthToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		oauthErr(w, 405, "invalid_request", "POST erwartet.")
		return
	}
	ip := "oauthtok|" + clientIP(r)
	if !loginThrottle.allow(ip, 30, 10*time.Minute) {
		oauthErr(w, 429, "invalid_request", "Zu viele fehlerhafte Anfragen. Bitte später erneut versuchen.")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := r.ParseForm(); err != nil {
		oauthErr(w, 400, "invalid_request", "Formular erwartet (application/x-www-form-urlencoded).")
		return
	}
	fail := func(code int, kind, desc string) {
		loginThrottle.hit(ip)
		oauthErr(w, code, kind, desc)
	}
	clientID, ok := a.clientAuth(r)
	if !ok {
		fail(401, "invalid_client", "Client-Authentifizierung fehlgeschlagen.")
		return
	}
	now := time.Now()
	switch r.PostFormValue("grant_type") {
	case "authorization_code":
		var cid, cname, ru, challenge string
		var uid, exp int64
		var write int
		h := hashToken(r.PostFormValue("code"))
		err := a.db.QueryRow("SELECT client_id,client_name,user_id,redirect_uri,challenge,can_write,expires_at FROM oauth_codes WHERE code_hash=?", h).
			Scan(&cid, &cname, &uid, &ru, &challenge, &write, &exp)
		_, _ = a.db.Exec("DELETE FROM oauth_codes WHERE code_hash=?", h) // single use
		if err != nil || exp < now.Unix() || cid != clientID || ru != r.PostFormValue("redirect_uri") || !pkceOK(r.PostFormValue("code_verifier"), challenge) {
			fail(400, "invalid_grant", "Der Code ist ungültig, abgelaufen oder gehört zu einer anderen Anfrage.")
			return
		}
		if u := a.user(uid); u == nil || u.Disabled {
			fail(400, "invalid_grant", "Das Konto ist deaktiviert.")
			return
		}
		// keep the list of connections bounded: the oldest ones of this person go first
		_, _ = a.db.Exec(`DELETE FROM oauth_grants WHERE user_id=? AND id NOT IN (SELECT id FROM oauth_grants WHERE user_id=? ORDER BY id DESC LIMIT ?)`, uid, uid, maxOAuthGrants-1)
		access, refresh := oauthAccessPrefix+randToken(32), oauthRefreshPrefix+randToken(32)
		if _, err := a.db.Exec(`INSERT INTO oauth_grants(user_id,client_id,client_name,can_write,access_hash,access_expires,refresh_hash,refresh_expires,created_at)
			VALUES(?,?,?,?,?,?,?,?,?)`, uid, cid, cname, write, hashToken(access), now.Add(oauthAccessTTL).Unix(), hashToken(refresh), now.Add(oauthRefreshTTL).Unix(), now.Unix()); err != nil {
			oauthErr(w, 500, "server_error", "Token konnte nicht erzeugt werden.")
			return
		}
		tokenResponse(w, access, refresh, write == 1)
	case "refresh_token":
		var id, uid, exp int64
		var cid string
		var write int
		if a.db.QueryRow("SELECT id,user_id,client_id,can_write,refresh_expires FROM oauth_grants WHERE refresh_hash=?", hashToken(r.PostFormValue("refresh_token"))).
			Scan(&id, &uid, &cid, &write, &exp) != nil || exp < now.Unix() || cid != clientID {
			fail(400, "invalid_grant", "Der Refresh-Token ist ungültig oder abgelaufen. Bitte neu verbinden.")
			return
		}
		if u := a.user(uid); u == nil || u.Disabled {
			fail(400, "invalid_grant", "Das Konto ist deaktiviert.")
			return
		}
		access, refresh := oauthAccessPrefix+randToken(32), oauthRefreshPrefix+randToken(32)
		if _, err := a.db.Exec("UPDATE oauth_grants SET access_hash=?,access_expires=?,refresh_hash=?,refresh_expires=? WHERE id=?",
			hashToken(access), now.Add(oauthAccessTTL).Unix(), hashToken(refresh), now.Add(oauthRefreshTTL).Unix(), id); err != nil {
			oauthErr(w, 500, "server_error", "Token konnte nicht erneuert werden.")
			return
		}
		tokenResponse(w, access, refresh, write == 1)
	default:
		fail(400, "unsupported_grant_type", "Unterstützt: authorization_code, refresh_token.")
	}
}

func tokenResponse(w http.ResponseWriter, access, refresh string, write bool) {
	scope := "read"
	if write {
		scope = "read write"
	}
	oauthJSON(w, 200, map[string]any{"access_token": access, "token_type": "Bearer", "expires_in": int(oauthAccessTTL.Seconds()), "refresh_token": refresh, "scope": scope})
}

// oauthRevoke ends a connection by access or refresh token (RFC 7009 always answers 200).
func (a *App) oauthRevoke(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		oauthErr(w, 405, "invalid_request", "POST erwartet.")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if r.ParseForm() != nil {
		oauthErr(w, 400, "invalid_request", "Formular erwartet.")
		return
	}
	clientID, ok := a.clientAuth(r)
	if !ok {
		oauthErr(w, 401, "invalid_client", "Client-Authentifizierung fehlgeschlagen.")
		return
	}
	h := hashToken(r.PostFormValue("token"))
	_, _ = a.db.Exec("DELETE FROM oauth_grants WHERE (access_hash=? OR refresh_hash=?) AND client_id=?", h, h, clientID)
	oauthJSON(w, 200, map[string]any{})
}

// oauthUser resolves an access token to its (enabled) user and whether it may write.
func (a *App) oauthUser(tok string) (*User, bool) {
	if !strings.HasPrefix(tok, oauthAccessPrefix) || len(tok) < 40 {
		return nil, false
	}
	var id, uid, exp, last int64
	var w int
	if a.db.QueryRow("SELECT id,user_id,can_write,access_expires,last_used FROM oauth_grants WHERE access_hash=?", hashToken(tok)).Scan(&id, &uid, &w, &exp, &last) != nil {
		return nil, false
	}
	now := time.Now().Unix()
	if exp < now {
		return nil, false
	}
	u := a.user(uid)
	if u == nil || u.Disabled {
		return nil, false
	}
	if now-last > 60 {
		_, _ = a.db.Exec("UPDATE oauth_grants SET last_used=? WHERE id=?", now, id)
	}
	return u, w == 1
}

// gcOAuth drops expired codes and connections and registrations that were never used.
func (a *App) gcOAuth() {
	now := time.Now()
	_, _ = a.db.Exec("DELETE FROM oauth_codes WHERE expires_at<?", now.Unix())
	_, _ = a.db.Exec("DELETE FROM oauth_grants WHERE refresh_expires<?", now.Unix())
	_, _ = a.db.Exec("DELETE FROM oauth_clients WHERE created_at<? AND client_id NOT IN (SELECT client_id FROM oauth_grants)", now.Add(-7*24*time.Hour).Unix())
}

// ---------- connections on the account page ----------

type OAuthGrant struct {
	ID       int64
	Client   string
	CanWrite bool
	Created  time.Time
	LastUsed time.Time
}

func (a *App) oauthGrants(userID int64) []*OAuthGrant {
	rows, err := a.db.Query("SELECT id,client_name,can_write,created_at,last_used FROM oauth_grants WHERE user_id=? ORDER BY id", userID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []*OAuthGrant
	for rows.Next() {
		g := &OAuthGrant{}
		var w int
		var c, u int64
		if rows.Scan(&g.ID, &g.Client, &w, &c, &u) == nil {
			g.CanWrite, g.Created = w == 1, time.Unix(c, 0)
			if u > 0 {
				g.LastUsed = time.Unix(u, 0)
			}
			out = append(out, g)
		}
	}
	return out
}

func (a *App) grantDelete(c *C) {
	res, err := a.db.Exec("DELETE FROM oauth_grants WHERE id=? AND user_id=?", pathInt(c.R, "gid"), c.User.ID)
	if err != nil {
		c.Error(500, "Verbindung konnte nicht getrennt werden.")
		return
	}
	if n, _ := res.RowsAffected(); n > 0 {
		c.setFlash("Verbindung getrennt. Die App muss sich neu anmelden.")
	}
	c.Redirect("/account")
}
