package app

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const schemaSQL = `
CREATE TABLE IF NOT EXISTS users (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  username TEXT NOT NULL UNIQUE COLLATE NOCASE,
  name TEXT NOT NULL,
  pass_hash TEXT NOT NULL,
  is_admin INTEGER NOT NULL DEFAULT 0,
  can_create INTEGER NOT NULL DEFAULT 0,
  can_master INTEGER NOT NULL DEFAULT 1,
  disabled INTEGER NOT NULL DEFAULT 0,
  contact_id INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS sessions (
  token TEXT PRIMARY KEY,
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  csrf TEXT NOT NULL,
  expires_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS roles (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  name TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  perms TEXT NOT NULL DEFAULT '{}',
  area_scoped INTEGER NOT NULL DEFAULT 0,
  builtin INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS events (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  name TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'idea',
  kind TEXT NOT NULL DEFAULT '',
  start_at TEXT NOT NULL DEFAULT '',
  end_at TEXT NOT NULL DEFAULT '',
  location_id INTEGER NOT NULL DEFAULT 0,
  description TEXT NOT NULL DEFAULT '',
  modules TEXT NOT NULL DEFAULT '[]',
  settings TEXT NOT NULL DEFAULT '{}',
  created_by INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS members (
  event_id INTEGER NOT NULL REFERENCES events(id) ON DELETE CASCADE,
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  role_id INTEGER NOT NULL,
  areas TEXT NOT NULL DEFAULT '[]',
  PRIMARY KEY (event_id, user_id)
);
CREATE TABLE IF NOT EXISTS records (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  event_id INTEGER NOT NULL DEFAULT 0,
  module TEXT NOT NULL,
  data TEXT NOT NULL DEFAULT '{}',
  created_by INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_records_em ON records(event_id, module);
CREATE TABLE IF NOT EXISTS templates (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  name TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  payload TEXT NOT NULL,
  builtin INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS files (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  event_id INTEGER NOT NULL DEFAULT 0,
  module TEXT NOT NULL,
  record_id INTEGER NOT NULL,
  name TEXT NOT NULL,
  size INTEGER NOT NULL DEFAULT 0,
  stored TEXT NOT NULL,
  created_by INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_files_rec ON files(record_id);
CREATE INDEX IF NOT EXISTS idx_files_event ON files(event_id);
CREATE TABLE IF NOT EXISTS cal_tokens (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  event_id INTEGER NOT NULL REFERENCES events(id) ON DELETE CASCADE,
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  token_hash TEXT NOT NULL UNIQUE,
  created_at INTEGER NOT NULL,
  UNIQUE(event_id, user_id)
);
CREATE TABLE IF NOT EXISTS audit (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  ts INTEGER NOT NULL,
  user_id INTEGER NOT NULL DEFAULT 0,
  event_id INTEGER NOT NULL DEFAULT 0,
  module TEXT NOT NULL DEFAULT '',
  rec_id INTEGER NOT NULL DEFAULT 0,
  action TEXT NOT NULL,
  title TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_audit_event ON audit(event_id, id);
`

func openDB(dir string) (*sql.DB, error) {
	path := filepath.Join(dir, "kollekt.db")
	dsn := "file:" + path + "?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(8)
	if _, err := db.Exec(schemaSQL); err != nil {
		return nil, fmt.Errorf("schema: %w", err)
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		_ = os.Chmod(path+suffix, 0o600) // data is private: owner only
	}
	return db, nil
}

// ---------- Records ----------

type Rec struct {
	ID        int64
	EventID   int64
	Module    string
	D         map[string]string
	CreatedBy int64
	Created   time.Time
	Updated   time.Time
}

func (r *Rec) S(k string) string  { return r.D[k] }
func (r *Rec) N(k string) float64 { return parseNum(r.D[k]) }
func (r *Rec) B(k string) bool    { return r.D[k] == "1" }
func (r *Rec) I(k string) int64 {
	n := int64(parseNum(r.D[k]))
	return n
}

func scanRecs(rows *sql.Rows) ([]*Rec, error) {
	defer rows.Close()
	var out []*Rec
	for rows.Next() {
		r := &Rec{}
		var data string
		var c, u int64
		if err := rows.Scan(&r.ID, &r.EventID, &r.Module, &data, &r.CreatedBy, &c, &u); err != nil {
			return nil, err
		}
		r.D = map[string]string{}
		_ = json.Unmarshal([]byte(data), &r.D)
		if r.D == nil {
			r.D = map[string]string{}
		}
		r.Created, r.Updated = time.Unix(c, 0), time.Unix(u, 0)
		out = append(out, r)
	}
	return out, rows.Err()
}

const recCols = "id,event_id,module,data,created_by,created_at,updated_at"

func (a *App) recs(eventID int64, module string) []*Rec {
	rows, err := a.db.Query("SELECT "+recCols+" FROM records WHERE event_id=? AND module=? ORDER BY id", eventID, module)
	if err != nil {
		return nil
	}
	out, _ := scanRecs(rows)
	return out
}

func (a *App) allEventRecs(eventID int64) []*Rec {
	rows, err := a.db.Query("SELECT "+recCols+" FROM records WHERE event_id=? ORDER BY id", eventID)
	if err != nil {
		return nil
	}
	out, _ := scanRecs(rows)
	return out
}

func (a *App) rec(id int64) *Rec {
	rows, err := a.db.Query("SELECT "+recCols+" FROM records WHERE id=?", id)
	if err != nil {
		return nil
	}
	out, _ := scanRecs(rows)
	if len(out) == 0 {
		return nil
	}
	return out[0]
}

func (a *App) saveRec(r *Rec) error {
	b, _ := json.Marshal(r.D)
	now := time.Now().Unix()
	if r.ID == 0 {
		res, err := a.db.Exec("INSERT INTO records(event_id,module,data,created_by,created_at,updated_at) VALUES(?,?,?,?,?,?)",
			r.EventID, r.Module, string(b), r.CreatedBy, now, now)
		if err != nil {
			return err
		}
		r.ID, _ = res.LastInsertId()
		return nil
	}
	_, err := a.db.Exec("UPDATE records SET data=?, updated_at=? WHERE id=?", string(b), now, r.ID)
	return err
}

func (a *App) delRec(id int64) {
	a.removeFilesOfRecord(id)
	_, _ = a.db.Exec("DELETE FROM records WHERE id=?", id)
}

func (a *App) logAudit(userID, eventID int64, module string, recID int64, action, title string) {
	_, _ = a.db.Exec("INSERT INTO audit(ts,user_id,event_id,module,rec_id,action,title) VALUES(?,?,?,?,?,?,?)",
		time.Now().Unix(), userID, eventID, module, recID, action, title)
}

type AuditRow struct {
	TS     time.Time
	User   string
	Module string
	Action string
	Title  string
	RecID  int64
}

func (a *App) recentAudit(eventID int64, limit int) []AuditRow {
	rows, err := a.db.Query(`SELECT a.ts, COALESCE(u.name,'?'), a.module, a.action, a.title, a.rec_id
		FROM audit a LEFT JOIN users u ON u.id=a.user_id WHERE a.event_id=? ORDER BY a.id DESC LIMIT ?`, eventID, limit)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []AuditRow
	for rows.Next() {
		var r AuditRow
		var ts int64
		if rows.Scan(&ts, &r.User, &r.Module, &r.Action, &r.Title, &r.RecID) == nil {
			r.TS = time.Unix(ts, 0)
			out = append(out, r)
		}
	}
	return out
}

// ---------- Events ----------

type Event struct {
	ID          int64
	Name        string
	Status      string
	Kind        string
	Start, End  string // "2006-01-02T15:04" or date
	LocationID  int64
	Description string
	Modules     []string
	Settings    map[string]json.RawMessage
	CreatedBy   int64
	Created     time.Time
}

func (e *Event) Has(mod string) bool {
	for _, m := range e.Modules {
		if m == mod {
			return true
		}
	}
	return false
}

func (e *Event) setting(key string, v any) {
	if e.Settings == nil {
		e.Settings = map[string]json.RawMessage{}
	}
	b, _ := json.Marshal(v)
	e.Settings[key] = b
}

func (e *Event) getSetting(key string, v any) bool {
	raw, ok := e.Settings[key]
	if !ok {
		return false
	}
	return json.Unmarshal(raw, v) == nil
}

func scanEvents(rows *sql.Rows) []*Event {
	defer rows.Close()
	var out []*Event
	for rows.Next() {
		e := &Event{}
		var mods, set string
		var c int64
		if err := rows.Scan(&e.ID, &e.Name, &e.Status, &e.Kind, &e.Start, &e.End, &e.LocationID, &e.Description, &mods, &set, &e.CreatedBy, &c); err != nil {
			continue
		}
		_ = json.Unmarshal([]byte(mods), &e.Modules)
		e.Settings = map[string]json.RawMessage{}
		_ = json.Unmarshal([]byte(set), &e.Settings)
		e.Created = time.Unix(c, 0)
		out = append(out, e)
	}
	return out
}

const eventCols = "id,name,status,kind,start_at,end_at,location_id,description,modules,settings,created_by,created_at"

func (a *App) event(id int64) *Event {
	rows, err := a.db.Query("SELECT "+eventCols+" FROM events WHERE id=?", id)
	if err != nil {
		return nil
	}
	out := scanEvents(rows)
	if len(out) == 0 {
		return nil
	}
	return out[0]
}

func (a *App) allEvents() []*Event {
	rows, err := a.db.Query("SELECT " + eventCols + " FROM events ORDER BY CASE WHEN start_at='' THEN 1 ELSE 0 END, start_at")
	if err != nil {
		return nil
	}
	return scanEvents(rows)
}

func (a *App) saveEvent(e *Event) error {
	mods, _ := json.Marshal(e.Modules)
	set, _ := json.Marshal(e.Settings)
	if e.ID == 0 {
		res, err := a.db.Exec("INSERT INTO events(name,status,kind,start_at,end_at,location_id,description,modules,settings,created_by,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)",
			e.Name, e.Status, e.Kind, e.Start, e.End, e.LocationID, e.Description, string(mods), string(set), e.CreatedBy, time.Now().Unix())
		if err != nil {
			return err
		}
		e.ID, _ = res.LastInsertId()
		return nil
	}
	_, err := a.db.Exec("UPDATE events SET name=?,status=?,kind=?,start_at=?,end_at=?,location_id=?,description=?,modules=?,settings=? WHERE id=?",
		e.Name, e.Status, e.Kind, e.Start, e.End, e.LocationID, e.Description, string(mods), string(set), e.ID)
	return err
}

func (a *App) deleteEvent(id int64) {
	a.removeFilesOfEvent(id)
	_, _ = a.db.Exec("DELETE FROM records WHERE event_id=?", id)
	_, _ = a.db.Exec("DELETE FROM audit WHERE event_id=?", id)
	_, _ = a.db.Exec("DELETE FROM cal_tokens WHERE event_id=?", id)
	_, _ = a.db.Exec("DELETE FROM events WHERE id=?", id)
}

var eventStatuses = []Opt{
	{"idea", "Idee", "gray"},
	{"planning", "In Planung", "blue"},
	{"permits", "Genehmigung läuft", "yellow"},
	{"confirmed", "Bestätigt", "green"},
	{"done", "Durchgeführt", "ink"},
	{"settled", "Abgerechnet", "ink"},
	{"cancelled", "Abgesagt", "red"},
}

func optLabel(opts []Opt, v string) (string, string) {
	for _, o := range opts {
		if o.V == v {
			return o.L, o.Color
		}
	}
	return v, "gray"
}

func idList(s string) []int64 {
	var out []int64
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		out = append(out, int64(parseNum(p)))
	}
	return out
}
