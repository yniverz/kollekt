package app

import (
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
)

const (
	maxUpload       = 25 << 20
	maxFilesPerItem = 20
)

type FileRec struct {
	ID        int64
	EventID   int64
	Module    string
	RecordID  int64
	Name      string
	Size      int64
	Stored    string
	CreatedBy int64
	Created   time.Time
}

type FileView struct {
	ID       int64
	Name     string
	Size     string
	URL      string
	DelURL   string
	Uploaded string
}

func humanSize(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

func (a *App) filesDir() string { return filepath.Join(a.cfg.DataDir, "files") }

func scanFiles(a *App, query string, args ...any) []*FileRec {
	rows, err := a.db.Query("SELECT id,event_id,module,record_id,name,size,stored,created_by,created_at FROM files "+query, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []*FileRec
	for rows.Next() {
		f := &FileRec{}
		var ts int64
		if rows.Scan(&f.ID, &f.EventID, &f.Module, &f.RecordID, &f.Name, &f.Size, &f.Stored, &f.CreatedBy, &ts) == nil {
			f.Created = time.Unix(ts, 0)
			out = append(out, f)
		}
	}
	return out
}

func (a *App) filesFor(recordID int64) []*FileRec {
	return scanFiles(a, "WHERE record_id=? ORDER BY id", recordID)
}

func (a *App) file(id int64) *FileRec {
	if fs := scanFiles(a, "WHERE id=?", id); len(fs) > 0 {
		return fs[0]
	}
	return nil
}

func (a *App) fileCounts(eventID int64, module string) map[int64]int {
	out := map[int64]int{}
	rows, err := a.db.Query("SELECT record_id, COUNT(*) FROM files WHERE event_id=? AND module=? GROUP BY record_id", eventID, module)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var n int
		if rows.Scan(&id, &n) == nil {
			out[id] = n
		}
	}
	return out
}

func (a *App) removeFile(f *FileRec) {
	_ = os.Remove(filepath.Join(a.filesDir(), f.Stored))
	_, _ = a.db.Exec("DELETE FROM files WHERE id=?", f.ID)
}

func (a *App) removeFilesOfRecord(recordID int64) {
	for _, f := range a.filesFor(recordID) {
		a.removeFile(f)
	}
}

func (a *App) removeFilesOfEvent(eventID int64) {
	for _, f := range scanFiles(a, "WHERE event_id=?", eventID) {
		a.removeFile(f)
	}
}

func cleanFilename(n string) string {
	n = strings.ReplaceAll(n, "\\", "/")
	n = filepath.Base(n)
	var b strings.Builder
	for _, r := range n {
		if unicode.IsControl(r) || r == '"' || r == '/' || r == ':' {
			continue
		}
		b.WriteRune(r)
	}
	out := strings.TrimSpace(b.String())
	if r := []rune(out); len(r) > 120 {
		ext := filepath.Ext(out)
		if len([]rune(ext)) > 12 {
			ext = ""
		}
		out = string(r[:100]) + ext
	}
	if out == "" || out == "." || out == ".." {
		return "datei"
	}
	return out
}

func (c *C) attachable(m *Module) bool { return !m.NoAttach && !m.Hidden }

func (c *C) fileViews(m *Module, rec *Rec) []FileView {
	var out []FileView
	for _, f := range c.A.filesFor(rec.ID) {
		base := "/gf/"
		if !m.Global {
			base = fmt.Sprintf("/e/%d/files/", c.Event.ID)
		}
		out = append(out, FileView{ID: f.ID, Name: f.Name, Size: humanSize(f.Size), URL: base + itoa(f.ID), DelURL: base + itoa(f.ID) + "/delete", Uploaded: f.Created.Format("02.01.2006")})
	}
	return out
}

func (c *C) handleUpload(w http.ResponseWriter, r *http.Request) {
	m := c.resolveModule(true)
	if m == nil {
		return
	}
	if !c.attachable(m) {
		c.Error(400, "Für dieses Modul gibt es keine Anhänge.")
		return
	}
	rec := c.loadRec(m, pathInt(r, "rid"))
	if rec == nil {
		return
	}
	next := r.FormValue("next")
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		c.setFlash("Der Upload ist fehlgeschlagen oder zu groß (maximal 25 MB pro Datei).")
		c.back(c.modListURL(m))
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	existing := len(c.A.filesFor(rec.ID))
	var evID int64
	if !m.Global {
		evID = c.Event.ID
	}
	if err := os.MkdirAll(c.A.filesDir(), 0o700); err != nil {
		c.Error(500, "Dateispeicher nicht verfügbar.")
		return
	}
	added, skipped := 0, 0
	for _, fh := range r.MultipartForm.File["file"] {
		if existing+added >= maxFilesPerItem || fh.Size > maxUpload || fh.Size == 0 {
			skipped++
			continue
		}
		src, err := fh.Open()
		if err != nil {
			skipped++
			continue
		}
		stored := randToken(16)
		dst, err := os.OpenFile(filepath.Join(c.A.filesDir(), stored), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			src.Close()
			skipped++
			continue
		}
		n, err := io.Copy(dst, io.LimitReader(src, maxUpload+1))
		src.Close()
		dst.Close()
		if err != nil || n > maxUpload {
			_ = os.Remove(filepath.Join(c.A.filesDir(), stored))
			skipped++
			continue
		}
		_, err = c.A.db.Exec("INSERT INTO files(event_id,module,record_id,name,size,stored,created_by,created_at) VALUES(?,?,?,?,?,?,?,?)",
			evID, m.Key, rec.ID, cleanFilename(fh.Filename), n, stored, c.User.ID, time.Now().Unix())
		if err != nil {
			_ = os.Remove(filepath.Join(c.A.filesDir(), stored))
			skipped++
			continue
		}
		added++
	}
	if added > 0 {
		c.audit(m, rec, "Anhang hinzugefügt")
	}
	msg := fmt.Sprintf("%d Datei(en) angehängt.", added)
	if skipped > 0 {
		msg += fmt.Sprintf(" %d übersprungen (leer, über 25 MB oder mehr als %d Anhänge).", skipped, maxFilesPerItem)
	}
	c.setFlash(msg)
	if next == "" {
		next = c.modListURL(m)
	}
	c.back(next)
}

// fileAccess loads a file and checks the caller may see (or change) it.
func (c *C) fileAccess(needEdit bool) (*FileRec, *Module, *Rec) {
	f := c.A.file(pathInt(c.R, "fid"))
	if f == nil {
		c.Error(404, "Datei nicht gefunden.")
		return nil, nil, nil
	}
	m := modByKey[f.Module]
	var evID int64
	if c.Event != nil {
		evID = c.Event.ID
	}
	if m == nil || f.EventID != evID || m.Global != (c.Event == nil) {
		c.Error(404, "Datei nicht gefunden.")
		return nil, nil, nil
	}
	if m.Global {
		if needEdit && !(c.User.IsAdmin || c.User.CanMaster) {
			c.Error(403, "Keine Berechtigung.")
			return nil, nil, nil
		}
	} else {
		need := 1
		if needEdit {
			need = 2
		}
		if !c.Event.Has(m.PermKey()) && !m.Core || c.Level(m.PermKey()) < need {
			c.Error(403, "Keine Berechtigung.")
			return nil, nil, nil
		}
	}
	rec := c.A.rec(f.RecordID)
	if rec == nil || rec.Module != m.Key || !c.visible(m, rec) {
		c.Error(404, "Datei nicht gefunden.")
		return nil, nil, nil
	}
	return f, m, rec
}

func (c *C) handleDownload(w http.ResponseWriter, r *http.Request) {
	f, _, _ := c.fileAccess(false)
	if f == nil {
		return
	}
	fh, err := os.Open(filepath.Join(c.A.filesDir(), f.Stored))
	if err != nil {
		c.Error(404, "Die Datei fehlt im Speicher.")
		return
	}
	defer fh.Close()
	h := w.Header()
	h.Set("Content-Type", "application/octet-stream")
	h.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": f.Name}))
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	h.Set("Cache-Control", "private, no-store")
	http.ServeContent(w, r, f.Name, f.Created, fh)
}

func (c *C) handleFileDelete(w http.ResponseWriter, r *http.Request) {
	f, m, rec := c.fileAccess(true)
	if f == nil {
		return
	}
	c.A.removeFile(f)
	c.audit(m, rec, "Anhang entfernt")
	c.setFlash("Anhang „" + f.Name + "“ entfernt.")
	next := r.FormValue("next")
	if next == "" {
		next = c.modListURL(m)
	}
	c.back(next)
}
