// Copyright (C) 2026 yniverz
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"encoding/json"
	"strings"
)

type TplRec struct {
	Module string            `json:"m"`
	Orig   int64             `json:"o"`
	D      map[string]string `json:"d"`
}

type TplPayload struct {
	Modules    []string                   `json:"modules"`
	Settings   map[string]json.RawMessage `json:"settings,omitempty"`
	Records    []TplRec                   `json:"records"`
	KeepGlobal bool                       `json:"keep_global,omitempty"`
}

type Template struct {
	ID          int64
	Name        string
	Description string
	Payload     TplPayload
	Builtin     bool
}

func (a *App) templates() []*Template {
	rows, err := a.db.Query("SELECT id,name,description,payload,builtin FROM templates ORDER BY builtin DESC, id")
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []*Template
	for rows.Next() {
		t := &Template{}
		var p string
		var b int
		if rows.Scan(&t.ID, &t.Name, &t.Description, &p, &b) == nil {
			_ = json.Unmarshal([]byte(p), &t.Payload)
			t.Builtin = b == 1
			out = append(out, t)
		}
	}
	return out
}

func (a *App) template(id int64) *Template {
	for _, t := range a.templates() {
		if t.ID == id {
			return t
		}
	}
	return nil
}

func (a *App) saveTemplate(t *Template) error {
	p, _ := json.Marshal(t.Payload)
	if t.ID == 0 {
		res, err := a.db.Exec("INSERT INTO templates(name,description,payload,builtin) VALUES(?,?,?,?)", t.Name, t.Description, string(p), b2i(t.Builtin))
		if err != nil {
			return err
		}
		t.ID, _ = res.LastInsertId()
		return nil
	}
	_, err := a.db.Exec("UPDATE templates SET name=?,description=?,payload=? WHERE id=?", t.Name, t.Description, string(p), t.ID)
	return err
}

// instantiate fills an (already saved) event from a payload.
func (a *App) instantiate(e *Event, p TplPayload, userID int64) {
	idMap := map[string]map[string]int64{}
	order := []string{"areas", "bar_items", "siteplans"}
	byMod := map[string][]TplRec{}
	for _, r := range p.Records {
		byMod[r.Module] = append(byMod[r.Module], r)
	}
	for m := range byMod {
		if !contains(order, m) {
			order = append(order, m)
		}
	}
	for _, mk := range order {
		mod := modByKey[mk]
		if mod == nil {
			continue
		}
		idMap[mk] = map[string]int64{}
		for _, tr := range byMod[mk] {
			d := map[string]string{}
			for k, v := range tr.D {
				d[k] = v
			}
			for i := range mod.Fields {
				f := &mod.Fields[i]
				v := d[f.Key]
				if v == "" {
					continue
				}
				switch f.Type {
				case TRef:
					rm := modByKey[f.Ref]
					if rm != nil && rm.Global {
						if !p.KeepGlobal {
							delete(d, f.Key)
						}
					} else if nid, ok := idMap[f.Ref][v]; ok {
						d[f.Key] = itoa(nid)
					} else {
						delete(d, f.Key)
					}
				case TRecipe:
					lines := parseRecipe(v)
					var out []RecipeLine
					for _, l := range lines {
						if nid, ok := idMap["bar_items"][itoa(l.I)]; ok {
							out = append(out, RecipeLine{nid, l.Q})
						}
					}
					if len(out) > 0 {
						b, _ := json.Marshal(out)
						d[f.Key] = string(b)
					} else {
						delete(d, f.Key)
					}
				}
			}
			nr := &Rec{EventID: e.ID, Module: mk, D: d, CreatedBy: userID}
			if a.saveRec(nr) == nil && tr.Orig != 0 {
				idMap[mk][itoa(tr.Orig)] = nr.ID
			}
		}
	}
}

type SnapOpts struct {
	Amounts bool
	People  bool
}

// snapshot captures an event's structure as a reusable payload.
func (a *App) snapshot(e *Event, o SnapOpts) TplPayload {
	p := TplPayload{Modules: append([]string{}, e.Modules...), KeepGlobal: o.People, Settings: map[string]json.RawMessage{}}
	for _, k := range []string{"calc", "bar", "tax"} {
		if raw, ok := e.Settings[k]; ok {
			p.Settings[k] = raw
		}
	}
	for _, r := range a.allEventRecs(e.ID) {
		mod := modByKey[r.Module]
		if mod == nil || r.Module == "loc_candidates" || r.Module == "siteplans" || r.Module == "site_items" {
			continue
		}
		d := map[string]string{}
		for k, v := range r.D {
			d[k] = v
		}
		for i := range mod.Fields {
			f := &mod.Fields[i]
			switch {
			case f.Type == TDate || f.Type == TDateTime:
				delete(d, f.Key)
			case f.Type == TBool && (f.Key == "paid"):
				delete(d, f.Key)
			case f.Key == "actual" || f.Key == "sold" || f.Key == "have":
				delete(d, f.Key)
			case f.Key == "status" && f.Type == TSelect:
				if f.Default != "" {
					d[f.Key] = f.Default
				}
			case f.Type == TMoney && !o.Amounts && (f.Fin || r.Module == "budget"):
				delete(d, f.Key)
			case f.Type == TRef && !o.People:
				if rm := modByKey[f.Ref]; rm != nil && rm.Global {
					delete(d, f.Key)
				}
			}
		}
		if r.Module == "staff" && !o.People {
			delete(d, "person")
		}
		p.Records = append(p.Records, TplRec{Module: r.Module, Orig: r.ID, D: d})
	}
	return p
}

func defaultModules() []string {
	var out []string
	for _, m := range eventModules() {
		if m.Default && !m.Core && m.Key != "overview" {
			out = append(out, m.Key)
		}
	}
	return out
}

func parseKV(s string) map[string]string { // helper for seeds: "a=b|c=d"
	d := map[string]string{}
	for _, part := range strings.Split(s, "|") {
		if i := strings.Index(part, "="); i > 0 {
			d[strings.TrimSpace(part[:i])] = strings.TrimSpace(part[i+1:])
		}
	}
	return d
}
