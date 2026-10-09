package app

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const cookieName = "kollekt_session"

type User struct {
	ID        int64
	Username  string
	Name      string
	IsAdmin   bool
	CanCreate bool
	CanMaster bool
	Disabled  bool
	ContactID int64
}

type Role struct {
	ID          int64
	Name        string
	Description string
	Perms       map[string]int
	AreaScoped  bool
	Builtin     bool
}

type Member struct {
	EventID int64
	UserID  int64
	RoleID  int64
	Areas   []int64
	Role    *Role
	User    *User
}

func (m *Member) InArea(id int64) bool {
	for _, a := range m.Areas {
		if a == id {
			return true
		}
	}
	return false
}

func randToken(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// dummyHash keeps login timing constant for unknown usernames.
var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("kollekt-dummy"), 11)

// trustProxy makes clientIP honor X-Forwarded-For (only enable behind a reverse proxy).
var trustProxy bool

func hashToken(tok string) string {
	h := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(h[:])
}

// checkPassword returns a user-facing message if the password is unacceptable.
func checkPassword(pw string) string {
	if len(pw) < 10 {
		return "Das Passwort braucht mindestens 10 Zeichen."
	}
	if len(pw) > 72 {
		return "Das Passwort darf höchstens 72 Zeichen lang sein."
	}
	return ""
}

func hashPW(pw string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(pw), 11)
	return string(b), err
}

const userCols = "id,username,name,is_admin,can_create,can_master,disabled,contact_id"

func scanUser(row interface{ Scan(...any) error }) (*User, error) {
	u := &User{}
	var adm, cr, ma, di int
	if err := row.Scan(&u.ID, &u.Username, &u.Name, &adm, &cr, &ma, &di, &u.ContactID); err != nil {
		return nil, err
	}
	u.IsAdmin, u.CanCreate, u.CanMaster, u.Disabled = adm == 1, cr == 1, ma == 1, di == 1
	return u, nil
}

func (a *App) user(id int64) *User {
	u, err := scanUser(a.db.QueryRow("SELECT "+userCols+" FROM users WHERE id=?", id))
	if err != nil {
		return nil
	}
	return u
}

func (a *App) allUsers() []*User {
	rows, err := a.db.Query("SELECT " + userCols + " FROM users ORDER BY name COLLATE NOCASE")
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []*User
	for rows.Next() {
		if u, err := scanUser(rows); err == nil {
			out = append(out, u)
		}
	}
	return out
}

func (a *App) userCount() int {
	var n int
	_ = a.db.QueryRow("SELECT COUNT(*) FROM users").Scan(&n)
	return n
}

var errBadLogin = errors.New("Benutzername oder Passwort falsch.")

func (a *App) checkLogin(username, pw string) (*User, error) {
	var hash string
	var id int64
	err := a.db.QueryRow("SELECT id, pass_hash FROM users WHERE username=?", strings.TrimSpace(username)).Scan(&id, &hash)
	if err != nil {
		// constant-ish time to avoid user enumeration
		_ = bcrypt.CompareHashAndPassword(dummyHash, []byte(pw))
		return nil, errBadLogin
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(pw)) != nil {
		return nil, errBadLogin
	}
	u := a.user(id)
	if u == nil || u.Disabled {
		return nil, errors.New("Dieses Konto ist deaktiviert.")
	}
	return u, nil
}

func (a *App) createUser(username, name, pw string, admin, create, master bool) (*User, error) {
	h, err := hashPW(pw)
	if err != nil {
		return nil, err
	}
	res, err := a.db.Exec("INSERT INTO users(username,name,pass_hash,is_admin,can_create,can_master,created_at) VALUES(?,?,?,?,?,?,?)",
		strings.TrimSpace(username), strings.TrimSpace(name), h, b2i(admin), b2i(create), b2i(master), time.Now().Unix())
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	// linked contact so the person can be assigned to tasks etc.
	c := &Rec{Module: "contacts", D: map[string]string{"name": strings.TrimSpace(name), "kind": "person", "tags": "Team", "user": itoa(id)}}
	if a.saveRec(c) == nil {
		_, _ = a.db.Exec("UPDATE users SET contact_id=? WHERE id=?", c.ID, id)
	}
	return a.user(id), nil
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

type Session struct {
	Token string
	CSRF  string
	User  *User
}

func (a *App) newSession(userID int64) (string, error) {
	tok := randToken(32)
	_, err := a.db.Exec("INSERT INTO sessions(token,user_id,csrf,expires_at) VALUES(?,?,?,?)",
		hashToken(tok), userID, randToken(16), time.Now().Add(30*24*time.Hour).Unix())
	return tok, err
}

func (a *App) session(token string) *Session {
	if token == "" {
		return nil
	}
	var uid, exp int64
	var csrf string
	err := a.db.QueryRow("SELECT user_id,csrf,expires_at FROM sessions WHERE token=?", hashToken(token)).Scan(&uid, &csrf, &exp)
	if err != nil {
		return nil
	}
	if exp < time.Now().Unix() {
		_, _ = a.db.Exec("DELETE FROM sessions WHERE token=?", hashToken(token))
		return nil
	}
	u := a.user(uid)
	if u == nil || u.Disabled {
		return nil
	}
	return &Session{Token: token, CSRF: csrf, User: u}
}

func (a *App) setCookie(w http.ResponseWriter, r *http.Request, tok string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name: cookieName, Value: tok, Path: "/", MaxAge: maxAge, HttpOnly: true,
		SameSite: http.SameSiteLaxMode, Secure: isHTTPS(r) || a.cfg.SecureCookies,
	})
}

func isHTTPS(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

// ---------- login throttling ----------

type throttle struct {
	mu sync.Mutex
	m  map[string][]time.Time
}

var loginThrottle = &throttle{m: map[string][]time.Time{}}

func (t *throttle) allow(key string, max int, window time.Duration) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := time.Now()
	var keep []time.Time
	for _, ts := range t.m[key] {
		if now.Sub(ts) < window {
			keep = append(keep, ts)
		}
	}
	t.m[key] = keep
	return len(keep) < max
}

func (t *throttle) hit(key string) {
	t.mu.Lock()
	t.m[key] = append(t.m[key], time.Now())
	t.mu.Unlock()
}

func clientIP(r *http.Request) string {
	if f := r.Header.Get("X-Forwarded-For"); f != "" && trustProxy {
		return strings.TrimSpace(strings.Split(f, ",")[0])
	}
	h, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return h
}

// ---------- roles ----------

func (a *App) roles() []*Role {
	rows, err := a.db.Query("SELECT id,name,description,perms,area_scoped,builtin FROM roles ORDER BY builtin DESC, id")
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []*Role
	for rows.Next() {
		r := &Role{}
		var p string
		var sc, bi int
		if rows.Scan(&r.ID, &r.Name, &r.Description, &p, &sc, &bi) == nil {
			r.Perms = map[string]int{}
			_ = json.Unmarshal([]byte(p), &r.Perms)
			r.AreaScoped, r.Builtin = sc == 1, bi == 1
			out = append(out, r)
		}
	}
	return out
}

func (a *App) role(id int64) *Role {
	for _, r := range a.roles() {
		if r.ID == id {
			return r
		}
	}
	return nil
}

func (a *App) saveRole(r *Role) error {
	p, _ := json.Marshal(r.Perms)
	if r.ID == 0 {
		res, err := a.db.Exec("INSERT INTO roles(name,description,perms,area_scoped,builtin) VALUES(?,?,?,?,?)",
			r.Name, r.Description, string(p), b2i(r.AreaScoped), b2i(r.Builtin))
		if err != nil {
			return err
		}
		r.ID, _ = res.LastInsertId()
		return nil
	}
	_, err := a.db.Exec("UPDATE roles SET name=?,description=?,perms=?,area_scoped=? WHERE id=?",
		r.Name, r.Description, string(p), b2i(r.AreaScoped), r.ID)
	return err
}

func (a *App) members(eventID int64) []*Member {
	rows, err := a.db.Query("SELECT user_id,role_id,areas FROM members WHERE event_id=?", eventID)
	if err != nil {
		return nil
	}
	var out []*Member
	roles := map[int64]*Role{}
	for _, r := range a.roles() {
		roles[r.ID] = r
	}
	for rows.Next() {
		m := &Member{EventID: eventID}
		var ar string
		if rows.Scan(&m.UserID, &m.RoleID, &ar) == nil {
			_ = json.Unmarshal([]byte(ar), &m.Areas)
			m.Role = roles[m.RoleID]
			out = append(out, m)
		}
	}
	rows.Close()
	for _, m := range out {
		m.User = a.user(m.UserID)
	}
	return out
}

func (a *App) member(eventID, userID int64) *Member {
	var m = &Member{EventID: eventID, UserID: userID}
	var ar string
	err := a.db.QueryRow("SELECT role_id,areas FROM members WHERE event_id=? AND user_id=?", eventID, userID).Scan(&m.RoleID, &ar)
	if err == sql.ErrNoRows || err != nil {
		return nil
	}
	_ = json.Unmarshal([]byte(ar), &m.Areas)
	m.Role = a.role(m.RoleID)
	return m
}

func (a *App) setMember(eventID, userID, roleID int64, areas []int64) {
	b, _ := json.Marshal(areas)
	if areas == nil {
		b = []byte("[]")
	}
	_, _ = a.db.Exec(`INSERT INTO members(event_id,user_id,role_id,areas) VALUES(?,?,?,?)
		ON CONFLICT(event_id,user_id) DO UPDATE SET role_id=excluded.role_id, areas=excluded.areas`, eventID, userID, roleID, string(b))
}

func (a *App) removeMember(eventID, userID int64) {
	_, _ = a.db.Exec("DELETE FROM members WHERE event_id=? AND user_id=?", eventID, userID)
}

func (a *App) memberEvents(userID int64) map[int64]*Member {
	rows, err := a.db.Query("SELECT event_id FROM members WHERE user_id=?", userID)
	out := map[int64]*Member{}
	if err != nil {
		return out
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	for _, id := range ids {
		if m := a.member(id, userID); m != nil {
			out[id] = m
		}
	}
	return out
}
