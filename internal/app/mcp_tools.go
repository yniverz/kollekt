// Copyright (C) 2026 yniverz
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// The MCP tools reuse the permission checks of the web interface (moduleAccess, findRec, visible,
// finVisible, collect …), so an assistant never sees or changes more than its user could by hand.

type mcpTool struct {
	Name, Title, Desc string
	Props             map[string]any
	Required          []string
	Write             bool // needs a token with write access
	Destructive       bool
	Fn                func(x *mcpCtx, args map[string]any) (any, error)
}

func (t *mcpTool) describe() map[string]any {
	props := t.Props
	if props == nil {
		props = map[string]any{}
	}
	schema := map[string]any{"type": "object", "properties": props}
	if len(t.Required) > 0 {
		schema["required"] = t.Required
	}
	return map[string]any{
		"name": t.Name, "title": t.Title, "description": t.Desc, "inputSchema": schema,
		"annotations": map[string]any{
			"title": t.Title, "readOnlyHint": !t.Write, "destructiveHint": t.Destructive,
			"idempotentHint": !t.Write, "openWorldHint": false,
		},
	}
}

func pInt(desc string) map[string]any  { return map[string]any{"type": "integer", "description": desc} }
func pStr(desc string) map[string]any  { return map[string]any{"type": "string", "description": desc} }
func pBool(desc string) map[string]any { return map[string]any{"type": "boolean", "description": desc} }
func pObj(desc string) map[string]any {
	return map[string]any{"type": "object", "description": desc, "additionalProperties": true}
}

var (
	pEventID  = pInt("ID des Events (aus list_events).")
	pModule   = pStr("Modul-Schlüssel, z. B. tasks, permits, budget, lineup, staff, equipment, notes, contacts (siehe list_modules).")
	pRecordID = pInt("ID des Eintrags.")
)

var mcpToolList []*mcpTool

func mcpTools() []*mcpTool {
	if mcpToolList == nil {
		mcpToolList = []*mcpTool{
			{Name: "list_events", Title: "Events auflisten", Fn: toolListEvents,
				Desc: "Alle Events, auf die du Zugriff hast, mit Status, Termin, Location und deiner Rolle. Zeigt auch, wer du bist und was du global darfst."},
			{Name: "get_event", Title: "Event-Übersicht", Fn: toolGetEvent, Props: map[string]any{"event_id": pEventID}, Required: []string{"event_id"},
				Desc: "Übersicht eines Events wie auf der Startseite in Kollekt: Kennzahlen, Warnungen (überfällig, Fristen, Verlust, Überschneidungen, Überlast …), nächste Fristen, Bereiche, Team und die Module, die du lesen oder bearbeiten darfst."},
			{Name: "list_modules", Title: "Module auflisten", Fn: toolListModules, Props: map[string]any{"event_id": pInt("Optional. Ohne event_id: die globalen Stammdaten-Module.")},
				Desc: "Module eines Events (oder die Stammdaten) mit Beschreibung und deinem Zugriff. Module mit kind=records enthalten Einträge, die du mit list_records lesen kannst."},
			{Name: "describe_module", Title: "Felder eines Moduls", Fn: toolDescribeModule, Props: map[string]any{"module": pModule}, Required: []string{"module"},
				Desc: "Felder eines Moduls: Schlüssel, Typ, Pflichtfeld, erlaubte Werte bei Auswahlfeldern, Ziel-Modul bei Verweisen, Hilfetexte. Vor create_record und update_record aufrufen."},
			{Name: "list_records", Title: "Einträge auflisten", Fn: toolListRecords,
				Props: map[string]any{
					"event_id": pInt("ID des Events. Bei Stammdaten (contacts, locations, articles) weglassen."), "module": pModule,
					"query":        pStr("Optional. Volltextsuche in den Feldern."),
					"filters":      pObj("Optional. Exakte Feldwerte, z. B. {\"status\":\"open\",\"area\":12}."),
					"include_done": pBool("Auch Erledigtes zeigen (Standard: nein, wenn das Modul einen Erledigt-Status hat)."),
					"limit":        pInt("Höchstzahl (Standard 100, maximal 500)."), "offset": pInt("Überspringen, für weitere Seiten."),
				}, Required: []string{"module"},
				Desc: "Einträge eines Moduls mit allen Feldern, die du sehen darfst. Auswahlfelder und Verweise haben zusätzlich ihren Anzeigetext unter display. Lange Texte sind gekürzt; get_record liefert sie vollständig."},
			{Name: "get_record", Title: "Eintrag lesen", Fn: toolGetRecord, Props: map[string]any{"record_id": pRecordID}, Required: []string{"record_id"},
				Desc: "Ein Eintrag vollständig, mit Anhängen (Datei-IDs für read_file)."},
			{Name: "search", Title: "Suchen", Fn: toolSearch,
				Props:    map[string]any{"query": pStr("Suchbegriff (mindestens 2 Zeichen)."), "event_id": pInt("Optional. Nur in diesem Event suchen."), "limit": pInt("Höchstzahl der Treffer (Standard 30, maximal 200).")},
				Required: []string{"query"},
				Desc:     "Sucht in Events, Einträgen aller Module, Stammdaten und Dateinamen von Anhängen, soweit du sie sehen darfst."},
			{Name: "get_timeplan", Title: "Zeitplan", Fn: toolTimeplan, Props: map[string]any{"event_id": pEventID, "only_mine": pBool("Nur, wofür du zuständig bist.")}, Required: []string{"event_id"},
				Desc: "Alle offenen, datierten Fristen und Termine des Events (Aufgaben, Anträge, Zahlungen, Material abholen und zurückgeben), sortiert, mit Tagen bis heute und T-minus zum Event. Dazu offene Punkte ohne Datum."},
			{Name: "get_finance", Title: "Finanzen", Fn: toolFinance, Props: map[string]any{"event_id": pEventID}, Required: []string{"event_id"},
				Desc: "Budget-Auswertung: Einnahmen, Ausgaben, Ergebnis und Prognose, Kosten nach Kategorie und Bereich mit Limits, offene Zahlungen, automatisch eingerechnete Kosten aus anderen Modulen, Break-even."},
			{Name: "get_changes", Title: "Letzte Änderungen", Fn: toolChanges, Props: map[string]any{"event_id": pEventID, "limit": pInt("Anzahl (Standard 30, maximal 200).")}, Required: []string{"event_id"},
				Desc: "Wer hat zuletzt was im Event geändert (Änderungsprotokoll), soweit du die Module sehen darfst."},
			{Name: "read_file", Title: "Anhang lesen", Fn: toolReadFile, Props: map[string]any{"file_id": pInt("ID der Datei (aus get_record oder search)."), "max_chars": pInt("Bei Textdateien höchstens so viele Zeichen (Standard 100000).")}, Required: []string{"file_id"},
				Desc: "Inhalt eines Anhangs: Textdateien als Text, Bilder als Bild, PDFs als Dokument (bis 10 MB). Andere Formate (z. B. Word) nur mit Name und Typ."},

			{Name: "create_record", Title: "Eintrag anlegen", Write: true, Fn: toolCreate,
				Props:    map[string]any{"event_id": pInt("ID des Events. Bei Stammdaten weglassen."), "module": pModule, "fields": pObj("Feldwerte nach Schlüssel, siehe describe_module. Nicht genannte Felder bekommen ihren Standardwert.")},
				Required: []string{"module", "fields"},
				Desc:     "Legt einen Eintrag an, z. B. eine Aufgabe, Genehmigung, einen Budgetposten, Kontakt oder eine Notiz. Liefert den gespeicherten Eintrag zurück."},
			{Name: "update_record", Title: "Eintrag ändern", Write: true, Fn: toolUpdate,
				Props:    map[string]any{"record_id": pRecordID, "fields": pObj("Nur die Felder, die sich ändern sollen. null leert ein Feld.")},
				Required: []string{"record_id", "fields"},
				Desc:     "Ändert einzelne Felder eines Eintrags, z. B. Status auf erledigt, neue Frist, Zuständigkeit. Andere Felder bleiben unverändert."},
			{Name: "delete_record", Title: "Eintrag löschen", Write: true, Destructive: true, Fn: toolDelete, Props: map[string]any{"record_id": pRecordID}, Required: []string{"record_id"},
				Desc: "Löscht einen Eintrag samt Anhängen. Nicht rückgängig zu machen, vorher mit der Person abstimmen."},
			{Name: "run_action", Title: "Aktion ausführen", Write: true, Fn: toolAction, Props: map[string]any{"record_id": pRecordID, "action": pStr("Schlüssel der Aktion (describe_module → actions), z. B. choose.")}, Required: []string{"record_id", "action"},
				Desc: "Führt eine Aktion eines Moduls aus, z. B. in der Location-Suche „Als Location festlegen“ (choose)."},
			{Name: "upload_file", Title: "Datei anhängen", Write: true, Fn: toolUpload,
				Props: map[string]any{"record_id": pRecordID, "name": pStr("Dateiname mit Endung, z. B. Angebot.pdf."),
					"content_base64": pStr("Dateiinhalt base64-kodiert (bis 10 MB)."), "text": pStr("Alternativ: Textinhalt (UTF-8), z. B. für .txt, .md, .csv, .eml.")},
				Required: []string{"record_id", "name"},
				Desc:     "Hängt eine Datei an einen Eintrag (Vertrag, Rechnung, Bescheid, E-Mail …). Höchstens 20 Anhänge pro Eintrag."},
			{Name: "delete_file", Title: "Anhang entfernen", Write: true, Destructive: true, Fn: toolDeleteFile, Props: map[string]any{"file_id": pInt("ID der Datei.")}, Required: []string{"file_id"},
				Desc: "Entfernt einen Anhang endgültig."},
		}
	}
	return mcpToolList
}

func mcpToolByName(n string) *mcpTool {
	for _, t := range mcpTools() {
		if t.Name == n {
			return t
		}
	}
	return nil
}

// ---------- arguments ----------

func argInt(args map[string]any, k string) (int64, bool) {
	switch v := args[k].(type) {
	case float64:
		if v == math.Trunc(v) {
			return int64(v), true
		}
	case string:
		if n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err == nil {
			return n, true
		}
	}
	return 0, false
}

func argStr(args map[string]any, k string) string {
	if s, ok := args[k].(string); ok {
		return strings.TrimSpace(s)
	}
	return ""
}

func argBool(args map[string]any, k string) bool {
	switch v := args[k].(type) {
	case bool:
		return v
	case string:
		return v == "true" || v == "1"
	}
	return false
}

func needInt(args map[string]any, k string) (int64, error) {
	n, ok := argInt(args, k)
	if !ok || n <= 0 {
		return 0, toolErrf("Parameter %s fehlt oder ist keine gültige ID.", k)
	}
	return n, nil
}

// ---------- contexts ----------

func (x *mcpCtx) base() *C { return &C{A: x.a, W: x.w, R: x.r, User: x.user} }

var errNoEvent = &toolError{"Event nicht gefunden oder kein Zugriff."}
var errNoRec = &toolError{"Eintrag nicht gefunden oder kein Zugriff."}

func (x *mcpCtx) eventC(id int64) (*C, error) {
	e := x.a.event(id)
	if e == nil {
		return nil, errNoEvent
	}
	c := x.base()
	c.Event = e
	if !x.user.IsAdmin {
		c.Mem = x.a.member(e.ID, x.user.ID)
		if c.Mem == nil || c.Mem.Role == nil {
			return nil, errNoEvent
		}
	}
	return c, nil
}

// scopeC returns the context for a module: the event for event modules, none for master data.
func (x *mcpCtx) scopeC(args map[string]any, module string) (*C, error) {
	m := modByKey[module]
	if m == nil {
		return nil, toolErrf("Unbekanntes Modul %q. list_modules zeigt die verfügbaren.", module)
	}
	if m.Global {
		return x.base(), nil
	}
	id, ok := argInt(args, "event_id")
	if !ok || id <= 0 {
		return nil, toolErrf("Für das Modul %s wird event_id gebraucht.", module)
	}
	return x.eventC(id)
}

func accessErr(code int, msg string) error {
	if code == 404 && msg == "Modul nicht gefunden." {
		return toolErrf("Dieses Modul enthält keine Einträge oder gibt es hier nicht. list_modules zeigt die verfügbaren.")
	}
	return &toolError{msg}
}

// recC loads a record with its context and checks the right to see (or edit) it.
func (x *mcpCtx) recC(id int64, needEdit bool) (*C, *Module, *Rec, error) {
	rec := x.a.rec(id)
	if rec == nil {
		return nil, nil, nil, errNoRec
	}
	m := modByKey[rec.Module]
	if m == nil {
		return nil, nil, nil, errNoRec
	}
	c := x.base()
	if !m.Global {
		var err error
		if c, err = x.eventC(rec.EventID); err != nil {
			return nil, nil, nil, errNoRec
		}
	}
	if _, code, msg := c.moduleAccess(m.Key, false); code != 0 {
		return nil, nil, nil, errNoRec
	} else if needEdit {
		if _, code, msg = c.moduleAccess(m.Key, true); code != 0 {
			return nil, nil, nil, &toolError{msg}
		}
	}
	if rec = c.findRec(m, id); rec == nil {
		return nil, nil, nil, errNoRec
	}
	return c, m, rec, nil
}

// fileC loads an attachment with the context of its event.
func (x *mcpCtx) fileC(id int64, needEdit bool) (*C, *FileRec, *Module, *Rec, error) {
	f := x.a.file(id)
	if f == nil {
		return nil, nil, nil, nil, toolErrf("Datei nicht gefunden.")
	}
	c := x.base()
	if f.EventID != 0 {
		var err error
		if c, err = x.eventC(f.EventID); err != nil {
			return nil, nil, nil, nil, toolErrf("Datei nicht gefunden.")
		}
	}
	f, m, rec, _, msg := c.fileCheck(id, needEdit)
	if f == nil {
		return nil, nil, nil, nil, &toolError{msg}
	}
	return c, f, m, rec, nil
}

// accessibleEvents returns contexts for all events the user is member of (admins: all).
func (x *mcpCtx) accessibleEvents() []*C {
	var out []*C
	members := map[int64]*Member{}
	if !x.user.IsAdmin {
		members = x.a.memberEvents(x.user.ID)
	}
	for _, e := range x.a.allEvents() {
		c := x.base()
		c.Event = e
		if !x.user.IsAdmin {
			c.Mem = members[e.ID]
			if c.Mem == nil || c.Mem.Role == nil {
				continue
			}
		}
		out = append(out, c)
	}
	return out
}

// ---------- output helpers ----------

func optOut(opts []Opt) []map[string]string {
	var out []map[string]string
	for _, o := range opts {
		out = append(out, map[string]string{"value": o.V, "label": o.L})
	}
	return out
}

func eventJSON(c *C) map[string]any {
	e := c.Event
	l, _ := optLabel(eventStatuses, e.Status)
	out := map[string]any{"id": e.ID, "name": e.Name, "status": e.Status, "status_label": l}
	if e.Kind != "" {
		out["kind"] = e.Kind
	}
	if e.Start != "" {
		out["start"] = e.Start
		if d, ok := daysUntil(e.Start); ok {
			out["days_until"] = d
		}
	}
	if e.End != "" {
		out["end"] = e.End
	}
	if e.LocationID != 0 {
		out["location"] = map[string]any{"id": e.LocationID, "name": c.RefTitle("locations", e.LocationID)}
	}
	if c.User.IsAdmin {
		out["role"] = "Admin"
	} else if c.Mem != nil && c.Mem.Role != nil {
		out["role"] = c.Mem.Role.Name
	}
	return out
}

// moduleList returns the modules of the event (or the master data) the user may open.
func (c *C) moduleList() []map[string]any {
	var out []map[string]any
	add := func(m *Module, level int, parent string) {
		kind := "records"
		if len(m.Fields) == 0 {
			kind = "page"
		}
		e := map[string]any{"key": m.Key, "name": m.Name, "access": levelName(level), "kind": kind}
		if m.Desc != "" {
			e["description"] = m.Desc
		}
		if parent != "" {
			e["part_of"] = parent
		}
		out = append(out, e)
	}
	if c.Event == nil {
		lvl := 1
		if c.User.IsAdmin || c.User.CanMaster {
			lvl = 2
		}
		for _, m := range modules {
			if m.Global {
				add(m, lvl, "")
			}
		}
		return out
	}
	for _, m := range modules {
		if m.Global || m.Key == "overview" {
			continue
		}
		pk := m.PermKey()
		if !m.Core && !c.Event.Has(pk) && !c.Event.Has(m.Key) {
			continue
		}
		lvl := c.Level(pk)
		if lvl < 1 {
			continue
		}
		parent := ""
		if m.Hidden {
			if p := modByKey[pk]; p != nil {
				parent = p.Name
			}
		}
		add(m, lvl, parent)
	}
	return out
}

func (c *C) fieldHidden(m *Module, f *Field, r *Rec) bool {
	return f.Fin && !m.Global && !c.finVisible(r, m.PermKey())
}

// recJSON renders a record with the fields the user may see. Short mode truncates long texts.
func (c *C) recJSON(m *Module, r *Rec, full bool) map[string]any {
	out := map[string]any{"id": r.ID, "module": m.Key, "title": c.A.recTitle(c, m, r)}
	if r.EventID != 0 {
		out["event_id"] = r.EventID
	}
	fields := map[string]any{}
	display := map[string]string{}
	for i := range m.Fields {
		f := &m.Fields[i]
		if c.fieldHidden(m, f, r) || (f.NoForm && !full) {
			continue
		}
		v := r.S(f.Key)
		if v == "" {
			continue
		}
		switch f.Type {
		case TMoney, TNumber, TPercent:
			fields[f.Key] = parseNum(v)
		case TBool:
			fields[f.Key] = v == "1"
		case TSelect:
			fields[f.Key] = v
			if l, _ := optLabel(f.Opts, v); l != v {
				display[f.Key] = l
			}
		case TRef:
			id := int64(parseNum(v))
			fields[f.Key] = id
			if t := c.RefTitle(f.Ref, id); t != "" {
				display[f.Key] = t
			}
		case TRecipe:
			var lines []RecipeLine
			if json.Unmarshal([]byte(v), &lines) == nil {
				var rl []map[string]any
				for _, l := range lines {
					rl = append(rl, map[string]any{"item": l.I, "item_name": c.RefTitle("bar_items", l.I), "qty": l.Q})
				}
				fields[f.Key] = rl
			}
		default:
			if !full && f.Type == TTextarea && utf8.RuneCountInString(v) > 400 {
				v = string([]rune(v)[:400]) + " … (gekürzt, get_record zeigt alles)"
			}
			fields[f.Key] = v
		}
	}
	out["fields"] = fields
	if len(display) > 0 {
		out["display"] = display
	}
	comp := map[string]any{}
	for _, v := range m.Virt {
		if t := v.Fn(c, r); t != "" {
			if v.Type == TMoney || v.Type == TNumber {
				comp[v.Key] = parseNum(t)
			} else {
				comp[v.Key] = t
			}
		}
	}
	if len(comp) > 0 {
		out["computed"] = comp
	}
	out["updated"] = r.Updated.Format(time.RFC3339)
	if full {
		out["created"] = r.Created.Format(time.RFC3339)
		if c.attachable(m) {
			files := []map[string]any{}
			for _, f := range c.A.filesFor(r.ID) {
				files = append(files, map[string]any{"id": f.ID, "name": f.Name, "size": humanSize(f.Size), "uploaded": f.Created.Format("2006-01-02")})
			}
			out["files"] = files
		}
		if m.DoneField != "" {
			out["done"] = contains(m.DoneVals, r.S(m.DoneField))
		}
	}
	return out
}

// matchFields returns the keys of the visible fields that contain q (lower case).
func (c *C) matchFields(m *Module, r *Rec, q string) []string {
	var hits []string
	if strings.Contains(strings.ToLower(c.A.recTitle(c, m, r)), q) {
		hits = append(hits, "title")
	}
	for i := range m.Fields {
		f := &m.Fields[i]
		if f.Type == TRecipe || f.NoForm || f.Type == TColor || c.fieldHidden(m, f, r) {
			continue
		}
		v := r.S(f.Key)
		switch f.Type {
		case TRef:
			v = c.RefTitle(f.Ref, int64(parseNum(v)))
		case TSelect:
			v, _ = optLabel(f.Opts, v)
		}
		if v != "" && strings.Contains(strings.ToLower(v), q) && f.Key != m.Title {
			hits = append(hits, f.Key)
		}
	}
	return hits
}

// ---------- read tools ----------

func toolListEvents(x *mcpCtx, args map[string]any) (any, error) {
	u := x.user
	var events []map[string]any
	for _, c := range x.accessibleEvents() {
		ev := eventJSON(c)
		end := c.Event.End
		if end == "" {
			end = c.Event.Start
		}
		if d, ok := daysUntil(end); ok && d < 0 || c.Event.Status == "settled" || c.Event.Status == "cancelled" {
			ev["past"] = true
		}
		events = append(events, ev)
	}
	return map[string]any{
		"user": map[string]any{"id": u.ID, "name": u.Name, "username": u.Username, "is_admin": u.IsAdmin,
			"can_create_events": u.IsAdmin || u.CanCreate, "can_edit_master_data": u.IsAdmin || u.CanMaster, "contact_id": u.ContactID},
		"token_can_write": x.write,
		"today":           time.Now().Format("2006-01-02"),
		"events":          events,
	}, nil
}

func toolGetEvent(x *mcpCtx, args map[string]any) (any, error) {
	id, err := needInt(args, "event_id")
	if err != nil {
		return nil, err
	}
	c, err := x.eventC(id)
	if err != nil {
		return nil, err
	}
	v := c.overviewView()
	out := eventJSON(c)
	if c.Event.Description != "" {
		out["description"] = c.Event.Description
	}
	if v.DaysText != "" {
		out["days"] = v.DaysText
	}
	if v.LocCap > 0 {
		out["location_capacity"] = v.LocCap
	}
	if c.scoped() {
		var names []string
		for _, a := range c.Mem.Areas {
			names = append(names, c.RefTitle("areas", a))
		}
		out["only_own_areas"] = names
	}
	var kpis, warns, next, areas, team []map[string]any
	for _, k := range v.KPIs {
		e := map[string]any{"label": k.Label, "value": k.Value}
		if k.Sub != "" {
			e["detail"] = k.Sub
		}
		kpis = append(kpis, e)
	}
	for _, w := range v.Warns {
		warns = append(warns, map[string]any{"text": w.Text, "level": w.Level})
	}
	for _, u := range v.Upcoming {
		next = append(next, map[string]any{"date": u.Date, "title": u.Title, "what": u.What, "days": u.Days, "late": u.Late})
	}
	for _, a := range v.Areas {
		e := map[string]any{"id": a.R.ID, "name": a.Name}
		if a.Lead != "" {
			e["lead"] = a.Lead
		}
		if a.ShowTasks {
			e["tasks_open"], e["tasks_total"], e["tasks_overdue"] = a.TasksOpen, a.TasksAll, a.Overdue
		}
		if a.ShowCost {
			e["cost_plan"], e["cost_limit"], e["over_limit"] = a.Cost, a.Limit, a.Over
		}
		if a.ShowStaff {
			e["shifts"], e["shifts_filled"] = a.Slots, a.Filled
		}
		if a.ShowEquip {
			e["equipment_open"], e["equipment_total"] = a.EquipOpen, a.EquipAll
		}
		areas = append(areas, e)
	}
	for _, m := range v.Team {
		if m.User == nil {
			continue
		}
		e := map[string]any{"name": m.User.Name}
		if m.Role != nil {
			e["role"] = m.Role.Name
		}
		team = append(team, e)
	}
	out["kpis"], out["warnings"], out["upcoming"], out["areas"], out["team"] = kpis, warns, next, areas, team
	out["modules"] = c.moduleList()
	return out, nil
}

func toolListModules(x *mcpCtx, args map[string]any) (any, error) {
	c := x.base()
	if id, ok := argInt(args, "event_id"); ok && id > 0 {
		var err error
		if c, err = x.eventC(id); err != nil {
			return nil, err
		}
	}
	return map[string]any{"modules": c.moduleList()}, nil
}

var typeHelp = map[FieldType]string{
	TText: "Text (bis 300 Zeichen)", TTextarea: "Langer Text", TNumber: "Zahl", TMoney: "Betrag in Euro", TPercent: "Prozent als Zahl",
	TDate: "Datum YYYY-MM-DD", TDateTime: "Zeitpunkt YYYY-MM-DDTHH:MM", TSelect: "Einer der Werte aus options (value)", TBool: "true/false",
	TRef: "ID eines Eintrags aus dem Modul ref", TURL: "Link", TColor: "Farbe #rrggbb", TGeo: "Position \"lat,lng\"",
	TRecipe: "Liste [{\"item\": ID aus bar_items, \"qty\": Menge pro Portion}]",
}

func toolDescribeModule(x *mcpCtx, args map[string]any) (any, error) {
	m := modByKey[argStr(args, "module")]
	if m == nil {
		return nil, toolErrf("Unbekanntes Modul. list_modules zeigt die verfügbaren.")
	}
	var fields []map[string]any
	for i := range m.Fields {
		f := &m.Fields[i]
		e := map[string]any{"key": f.Key, "label": f.Label, "type": string(f.Type), "format": typeHelp[f.Type]}
		if f.Required {
			e["required"] = true
		}
		if len(f.Opts) > 0 {
			e["options"] = optOut(f.Opts)
		}
		if f.Ref != "" {
			e["ref"] = f.Ref
		}
		if f.Help != "" {
			e["help"] = f.Help
		}
		if f.Suffix != "" {
			e["unit"] = f.Suffix
		}
		if f.Default != "" {
			e["default"] = f.Default
		}
		if len(f.Datalist) > 0 {
			e["suggestions"] = f.Datalist
		}
		if f.NoForm {
			e["read_only"] = true
		}
		if f.Fin && !m.Global {
			e["cost_field"] = true
		}
		fields = append(fields, e)
	}
	out := map[string]any{"key": m.Key, "name": m.Name, "singular": m.Singular, "description": m.Desc, "global": m.Global, "title_field": m.Title, "fields": fields}
	var notes []string
	if len(m.Fields) == 0 {
		notes = append(notes, "Dieses Modul ist eine Auswertung ohne eigene Einträge. Nutze get_event, get_timeplan oder get_finance.")
	}
	if m.Global {
		notes = append(notes, "Stammdaten gelten für alle Events. Ohne event_id ansprechen; ändern dürfen nur Personen mit Recht auf Stammdaten.")
	}
	if m.AreaField != "" {
		out["area_field"] = m.AreaField
		notes = append(notes, "Wer nur eigene Bereiche sehen darf, legt Einträge immer in einem eigenen Bereich an.")
	}
	if m.DoneField != "" {
		out["done_field"], out["done_values"] = m.DoneField, m.DoneVals
	}
	if _, ok := relDateKey[m.Key]; ok {
		notes = append(notes, "Das Feld rel (Tage vor dem Event) setzt die Frist automatisch relativ zum Eventtermin.")
	}
	for i := range m.Fields {
		if m.Fields[i].Fin && !m.Global {
			notes = append(notes, "cost_field: nur sichtbar und änderbar mit dem Recht „Kosten & Gagen sehen“ (oder im eigenen Bereich).")
			break
		}
	}
	if notes != nil {
		out["notes"] = notes
	}
	var acts, virt []map[string]any
	for _, a := range m.Actions {
		acts = append(acts, map[string]any{"key": a.Key, "label": a.Label})
	}
	for _, v := range m.Virt {
		virt = append(virt, map[string]any{"key": v.Key, "label": v.Label})
	}
	if acts != nil {
		out["actions"] = acts
	}
	if virt != nil {
		out["computed"] = virt
	}
	return out, nil
}

func toolListRecords(x *mcpCtx, args map[string]any) (any, error) {
	key := argStr(args, "module")
	c, err := x.scopeC(args, key)
	if err != nil {
		return nil, err
	}
	m, code, msg := c.moduleAccess(key, false)
	if m == nil {
		return nil, accessErr(code, msg)
	}
	q := strings.ToLower(argStr(args, "query"))
	filters := map[string]string{}
	if fm, ok := args["filters"].(map[string]any); ok {
		for k, v := range fm {
			f := m.Field(k)
			if f == nil {
				return nil, toolErrf("Filter: das Modul %s hat kein Feld %q.", m.Key, k)
			}
			s, err := mcpValue(c, f, v)
			if err != nil {
				return nil, err
			}
			filters[k] = s
		}
	}
	showDone := argBool(args, "include_done")
	if _, ok := filters[m.DoneField]; ok {
		showDone = true
	}
	var recs []*Rec
	for _, r := range c.Recs(m.Key) {
		if !c.visible(m, r) {
			continue
		}
		if m.DoneField != "" && !showDone && contains(m.DoneVals, r.S(m.DoneField)) {
			continue
		}
		ok := true
		for k, want := range filters {
			if f := m.Field(k); c.fieldHidden(m, f, r) || r.S(k) != want {
				ok = false
				break
			}
		}
		if ok && q != "" && len(c.matchFields(m, r, q)) == 0 {
			ok = false
		}
		if ok {
			recs = append(recs, r)
		}
	}
	c.sortRecs(m, recs)
	limit, _ := argInt(args, "limit")
	if limit <= 0 {
		limit = 100
	}
	limit = min(limit, 500)
	offset, _ := argInt(args, "offset")
	offset = max(offset, 0)
	total := len(recs)
	out := []map[string]any{}
	for i := offset; i < int64(total) && i < offset+limit; i++ {
		out = append(out, c.recJSON(m, recs[i], false))
	}
	res := map[string]any{"module": m.Key, "total": total, "offset": offset, "records": out}
	if offset+limit < int64(total) {
		res["has_more"] = true
	}
	if m.DoneField != "" && !showDone {
		res["note"] = "Erledigte Einträge sind ausgeblendet (include_done: true zeigt sie)."
	}
	return res, nil
}

func toolGetRecord(x *mcpCtx, args map[string]any) (any, error) {
	id, err := needInt(args, "record_id")
	if err != nil {
		return nil, err
	}
	c, m, rec, err := x.recC(id, false)
	if err != nil {
		return nil, err
	}
	out := c.recJSON(m, rec, true)
	out["module_name"] = m.Name
	if c.Event != nil {
		out["event_name"] = c.Event.Name
	}
	out["can_edit"] = c.canEditModule(m)
	return out, nil
}

func toolSearch(x *mcpCtx, args map[string]any) (any, error) {
	q := strings.ToLower(argStr(args, "query"))
	if utf8.RuneCountInString(q) < 2 {
		return nil, toolErrf("Der Suchbegriff braucht mindestens 2 Zeichen.")
	}
	limit, _ := argInt(args, "limit")
	if limit <= 0 {
		limit = 30
	}
	limit = min(limit, 200)
	only, _ := argInt(args, "event_id")
	hits := []map[string]any{}
	full := false
	addHit := func(h map[string]any) {
		if int64(len(hits)) >= limit {
			full = true
			return
		}
		hits = append(hits, h)
	}
	scan := func(c *C) {
		for _, m := range modules {
			if len(m.Fields) == 0 || m.Global != (c.Event == nil) {
				continue
			}
			if mm, _, _ := c.moduleAccess(m.Key, false); mm == nil {
				continue
			}
			for _, r := range c.Recs(m.Key) {
				if !c.visible(m, r) {
					continue
				}
				if keys := c.matchFields(m, r, q); len(keys) > 0 {
					h := map[string]any{"record_id": r.ID, "module": m.Key, "module_name": m.Name, "title": c.A.recTitle(c, m, r), "matched": keys}
					if c.Event != nil {
						h["event_id"], h["event"] = c.Event.ID, c.Event.Name
					}
					addHit(h)
				}
			}
		}
	}
	var evs []*C
	if only > 0 {
		c, err := x.eventC(only)
		if err != nil {
			return nil, err
		}
		evs = []*C{c}
	} else {
		evs = x.accessibleEvents()
		scan(x.base())
	}
	for _, c := range evs {
		if strings.Contains(strings.ToLower(c.Event.Name+" "+c.Event.Description+" "+c.Event.Kind), q) {
			addHit(map[string]any{"event_id": c.Event.ID, "event": c.Event.Name, "module": "event"})
		}
		scan(c)
	}
	// attachments by file name
	evIDs := map[int64]*C{0: x.base()}
	if only > 0 {
		delete(evIDs, 0)
	}
	for _, c := range evs {
		evIDs[c.Event.ID] = c
	}
	for _, f := range scanFiles(x.a, "WHERE name LIKE ? ESCAPE '\\' ORDER BY id DESC LIMIT 500", "%"+likeEscape(q)+"%") {
		c := evIDs[f.EventID]
		if c == nil {
			continue
		}
		if ff, m, rec, _, _ := c.fileCheck(f.ID, false); ff != nil {
			h := map[string]any{"file_id": f.ID, "file": f.Name, "record_id": rec.ID, "module": m.Key, "title": c.A.recTitle(c, m, rec)}
			if c.Event != nil {
				h["event_id"], h["event"] = c.Event.ID, c.Event.Name
			}
			addHit(h)
		}
	}
	res := map[string]any{"query": q, "hits": hits}
	if full {
		res["note"] = "Es gibt weitere Treffer. Suche genauer oder erhöhe limit."
	}
	return res, nil
}

func likeEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

func toolTimeplan(x *mcpCtx, args map[string]any) (any, error) {
	id, err := needInt(args, "event_id")
	if err != nil {
		return nil, err
	}
	c, err := x.eventC(id)
	if err != nil {
		return nil, err
	}
	if !c.can("timeplan") {
		return nil, toolErrf("Der Zeitplan ist für dieses Event nicht aktiviert oder du hast keinen Zugriff.")
	}
	mine := argBool(args, "only_mine")
	entries, undated := c.timeEntries()
	var out []map[string]any
	for _, en := range entries {
		if mine && !en.Mine {
			continue
		}
		e := map[string]any{"date": en.Raw, "what": en.Label, "title": en.Title, "module": en.Module, "record_id": en.ID, "days": en.Days, "late": en.Late}
		if en.HasT {
			e["t_minus"] = en.TMinus
		}
		if en.Area != "" {
			e["area"] = en.Area
		}
		if en.Who != "" {
			e["who"] = en.Who
		}
		if en.Status != "" {
			e["status"] = en.Status
		}
		if en.Mine {
			e["mine"] = true
		}
		out = append(out, e)
	}
	var und []map[string]string
	if !mine {
		for _, u := range undated {
			und = append(und, map[string]string{"title": u.Title, "what": u.Kind})
		}
	}
	return map[string]any{"event": c.Event.Name, "event_start": c.Event.Start, "today": time.Now().Format("2006-01-02"), "entries": out, "undated": und}, nil
}

func toolFinance(x *mcpCtx, args map[string]any) (any, error) {
	id, err := needInt(args, "event_id")
	if err != nil {
		return nil, err
	}
	c, err := x.eventC(id)
	if err != nil {
		return nil, err
	}
	budget, calc := c.can("budget"), c.can("calc")
	if !budget && !(calc && !c.scoped()) {
		return nil, toolErrf("Kein Zugriff auf Budget oder Kalkulation in diesem Event.")
	}
	g := c.Baseline()
	out := map[string]any{"event": c.Event.Name, "guests_planned": g, "basis": c.basisLabel()}
	if !c.scoped() {
		f := c.finance(g)
		out["income_plan"], out["expense_plan"], out["result_plan"] = round2(f.Income), round2(f.Expense), round2(f.Profit)
		out["result_forecast"], out["paid"] = round2(f.ProfitFore), round2(f.Paid)
		if calc {
			out["break_even_guests"] = c.breakEven()
		}
	} else {
		out["note"] = "Nur Kosten deiner eigenen Bereiche."
	}
	if budget {
		bv := budgetExtra(c).(*BudgetView)
		cat := func(rows []CatRow) []map[string]any {
			var o []map[string]any
			for _, r := range rows {
				o = append(o, map[string]any{"name": r.Name, "plan": round2(r.Plan), "forecast": round2(r.Fore), "auto": r.Auto})
			}
			return o
		}
		out["expenses_by_category"], out["income_by_category"] = cat(bv.Cats), cat(bv.Incomes)
		var areas, open, auto []map[string]any
		for _, a := range bv.Areas {
			areas = append(areas, map[string]any{"name": a.Name, "plan": round2(a.Plan), "limit": a.Limit, "over_limit": a.Over})
		}
		for _, o := range bv.Open {
			open = append(open, map[string]any{"title": o.Title, "due": o.Due, "amount": round2(o.Amt), "late": o.Late})
		}
		for _, l := range bv.AutoRows {
			auto = append(auto, map[string]any{"source": srcLabel(l.Source), "title": l.Title, "plan": round2(l.Plan), "record_id": l.RecID})
		}
		out["areas"], out["open_payments"], out["from_other_modules"], out["warnings"] = areas, open, auto, bv.Warn
	}
	return out, nil
}

func toolChanges(x *mcpCtx, args map[string]any) (any, error) {
	id, err := needInt(args, "event_id")
	if err != nil {
		return nil, err
	}
	c, err := x.eventC(id)
	if err != nil {
		return nil, err
	}
	limit, _ := argInt(args, "limit")
	if limit <= 0 {
		limit = 30
	}
	limit = min(limit, 200)
	var out []map[string]any
	for _, r := range c.visibleAudit(c.A.recentAudit(c.Event.ID, int(limit)*5), int(limit)) {
		e := map[string]any{"when": r.TS.Format(time.RFC3339), "who": r.User, "action": r.Action, "title": r.Title, "module": r.Module}
		if r.RecID != 0 && r.Module != "event" && r.Module != "team" {
			e["record_id"] = r.RecID
		}
		out = append(out, e)
	}
	return map[string]any{"event": c.Event.Name, "changes": out}, nil
}

var textExt = map[string]bool{".txt": true, ".md": true, ".csv": true, ".tsv": true, ".json": true, ".xml": true, ".ics": true, ".eml": true, ".html": true, ".htm": true, ".log": true, ".yaml": true, ".yml": true, ".vcf": true}

func toolReadFile(x *mcpCtx, args map[string]any) (any, error) {
	id, err := needInt(args, "file_id")
	if err != nil {
		return nil, err
	}
	c, f, m, rec, err := x.fileC(id, false)
	if err != nil {
		return nil, err
	}
	meta := fmt.Sprintf("Datei %q (%s) an %s „%s“", f.Name, humanSize(f.Size), m.Singular, c.A.recTitle(c, m, rec))
	path := filepath.Join(x.a.filesDir(), f.Stored)
	ext := strings.ToLower(filepath.Ext(f.Name))
	if f.Size > mcpMaxFile && !textExt[ext] {
		return toolContent{{"type": "text", "text": meta + ": zu groß, um sie hier zu übertragen (über 10 MB)."}}, nil
	}
	fh, err := os.Open(path)
	if err != nil {
		return nil, toolErrf("Die Datei fehlt im Speicher.")
	}
	b, err := io.ReadAll(io.LimitReader(fh, mcpMaxFile))
	fh.Close()
	if err != nil {
		return nil, toolErrf("Die Datei konnte nicht gelesen werden.")
	}
	mt := http.DetectContentType(b)
	if i := strings.Index(mt, ";"); i > 0 {
		mt = mt[:i]
	}
	switch {
	case mt == "image/png" || mt == "image/jpeg" || mt == "image/gif" || mt == "image/webp":
		return toolContent{{"type": "text", "text": meta}, {"type": "image", "data": base64.StdEncoding.EncodeToString(b), "mimeType": mt}}, nil
	case textExt[ext] || strings.HasPrefix(mt, "text/"):
		maxc, _ := argInt(args, "max_chars")
		if maxc <= 0 {
			maxc = 100000
		}
		s := strings.ToValidUTF8(string(b), "")
		if r := []rune(s); int64(len(r)) > maxc {
			s = string(r[:maxc]) + "\n… (gekürzt)"
		} else if f.Size > mcpMaxFile {
			s += "\n… (gekürzt, nur die ersten 10 MB)"
		}
		return toolContent{{"type": "text", "text": meta + ":\n\n" + s}}, nil
	}
	if mt != "application/pdf" {
		return toolContent{{"type": "text", "text": meta + ", Typ " + mt + ". Dieses Format kann hier nicht gelesen werden (nur Text, Bilder und PDF)."}}, nil
	}
	return toolContent{
		{"type": "text", "text": meta + ", Typ " + mt + "."},
		{"type": "resource", "resource": map[string]any{"uri": "kollekt://files/" + itoa(f.ID) + "/" + url.PathEscape(f.Name), "mimeType": mt, "blob": base64.StdEncoding.EncodeToString(b)}},
	}, nil
}

// ---------- write tools ----------

// mcpValue converts a JSON argument into the string form the field stores.
func mcpValue(c *C, f *Field, v any) (string, error) {
	switch t := v.(type) {
	case nil:
		return "", nil
	case bool:
		if f.Type == TBool {
			if t {
				return "1", nil
			}
			return "", nil
		}
		return "", toolErrf("Feld %s: true/false passt nicht zum Typ %s.", f.Key, f.Type)
	case float64:
		if f.Type == TBool {
			if t != 0 {
				return "1", nil
			}
			return "", nil
		}
		if f.Type == TSelect {
			return mcpSelect(f, numStr(t))
		}
		return numStr(t), nil
	case string:
		s := strings.TrimSpace(t)
		switch f.Type {
		case TBool:
			switch strings.ToLower(s) {
			case "", "0", "false", "nein", "no":
				return "", nil
			}
			return "1", nil
		case TSelect:
			if s == "" {
				return "", nil
			}
			return mcpSelect(f, s)
		}
		return s, nil
	case []any, map[string]any:
		if f.Type != TRecipe {
			return "", toolErrf("Feld %s erwartet einen einzelnen Wert.", f.Key)
		}
		var lines []RecipeLine
		list, _ := t.([]any)
		for _, it := range list {
			o, _ := it.(map[string]any)
			id, _ := argInt(o, "item")
			q, _ := o["qty"].(float64)
			lines = append(lines, RecipeLine{I: id, Q: q})
		}
		b, _ := json.Marshal(lines)
		return string(b), nil
	}
	return "", toolErrf("Feld %s: Wert nicht verständlich.", f.Key)
}

func mcpSelect(f *Field, s string) (string, error) {
	var vals []string
	for _, o := range f.Opts {
		if o.V == s || strings.EqualFold(o.L, s) {
			return o.V, nil
		}
		vals = append(vals, o.V+" ("+o.L+")")
	}
	return "", toolErrf("Feld %s: %q ist kein erlaubter Wert. Erlaubt: %s.", f.Key, s, strings.Join(vals, ", "))
}

// mcpCollect validates fields like a submitted form: it fills a form from the record (or the
// defaults), overlays the given values and runs the regular collect().
func (c *C) mcpCollect(m *Module, rec *Rec, fields map[string]any) (map[string]string, error) {
	form := url.Values{}
	for i := range m.Fields {
		f := &m.Fields[i]
		if rec != nil {
			form.Set(f.Key, rec.S(f.Key))
		} else {
			form.Set(f.Key, f.Default)
		}
	}
	var keys []string
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	given := map[string]string{}
	for _, k := range keys {
		f := m.Field(k)
		if f == nil {
			var valid []string
			for i := range m.Fields {
				if !m.Fields[i].NoForm {
					valid = append(valid, m.Fields[i].Key)
				}
			}
			return nil, toolErrf("Das Modul %s hat kein Feld %q. Felder: %s.", m.Key, k, strings.Join(valid, ", "))
		}
		if f.NoForm {
			return nil, toolErrf("Das Feld %s wird in Kollekt an anderer Stelle gepflegt und ist hier nicht änderbar.", k)
		}
		s, err := mcpValue(c, f, fields[k])
		if err != nil {
			return nil, err
		}
		form.Set(k, s)
		given[k] = s
	}
	// cost fields without the right stay untouched, as in the form; say so instead of ignoring silently
	probe := rec
	if probe == nil {
		probe = &Rec{D: map[string]string{}}
		if m.AreaField != "" {
			probe.D[m.AreaField] = form.Get(m.AreaField)
		}
	}
	for k := range given {
		if c.fieldHidden(m, m.Field(k), probe) {
			return nil, toolErrf("Das Kostenfeld %s darfst du nicht sehen oder ändern (Recht „Kosten & Gagen sehen“ fehlt).", k)
		}
	}
	orig := c.R
	req := orig.Clone(orig.Context())
	req.Form, req.PostForm = form, form
	c.R = req
	vals, errs := c.collect(m, rec)
	c.R = orig
	if len(errs) > 0 {
		var msgs []string
		for k, e := range errs {
			msgs = append(msgs, k+": "+e)
		}
		sort.Strings(msgs)
		return nil, toolErrf("Nicht gespeichert. %s", strings.Join(msgs, " "))
	}
	for k, s := range given {
		if f := m.Field(k); f.Type == TRef && s != "" && vals[k] == "" {
			return nil, toolErrf("Feld %s: %s ist keine ID eines Eintrags im Modul %s (bei Event-Modulen: aus demselben Event).", k, s, f.Ref)
		}
		if f := m.Field(k); f.Type == TRecipe && s != "" && s != "[]" && vals[k] == "" {
			return nil, toolErrf("Feld %s: keine gültigen Einkaufsartikel (IDs aus bar_items) mit Menge über 0.", k)
		}
	}
	if key, ok := relDateKey[m.Key]; ok && c.Event != nil {
		if d, ok := relDate(c.Event.Start, vals["rel"]); ok {
			vals[key] = d
		}
	}
	return vals, nil
}

func fieldsArg(args map[string]any) (map[string]any, error) {
	f, ok := args["fields"].(map[string]any)
	if !ok {
		return nil, toolErrf("Parameter fields fehlt (Objekt mit Feldwerten).")
	}
	return f, nil
}

func toolCreate(x *mcpCtx, args map[string]any) (any, error) {
	key := argStr(args, "module")
	c, err := x.scopeC(args, key)
	if err != nil {
		return nil, err
	}
	m, code, msg := c.moduleAccess(key, true)
	if m == nil {
		return nil, accessErr(code, msg)
	}
	fields, err := fieldsArg(args)
	if err != nil {
		return nil, err
	}
	vals, err := c.mcpCollect(m, nil, fields)
	if err != nil {
		return nil, err
	}
	var evID int64
	if !m.Global {
		evID = c.Event.ID
	}
	r := &Rec{EventID: evID, Module: m.Key, D: vals, CreatedBy: c.User.ID}
	if err := c.A.saveRec(r); err != nil {
		return nil, err
	}
	c.audit(m, r, "angelegt (MCP)")
	c.invalidate()
	out := c.recJSON(m, c.A.rec(r.ID), true)
	if c.rolled {
		out["note"] = "Das Ende lag vor dem Beginn und wurde auf den Folgetag gelegt."
	}
	return out, nil
}

func toolUpdate(x *mcpCtx, args map[string]any) (any, error) {
	id, err := needInt(args, "record_id")
	if err != nil {
		return nil, err
	}
	c, m, rec, err := x.recC(id, true)
	if err != nil {
		return nil, err
	}
	fields, err := fieldsArg(args)
	if err != nil {
		return nil, err
	}
	vals, err := c.mcpCollect(m, rec, fields)
	if err != nil {
		return nil, err
	}
	for k, v := range vals {
		rec.D[k] = v
	}
	if err := c.A.saveRec(rec); err != nil {
		return nil, err
	}
	c.audit(m, rec, "geändert (MCP)")
	c.invalidate()
	out := c.recJSON(m, c.A.rec(rec.ID), true)
	if c.rolled {
		out["note"] = "Das Ende lag vor dem Beginn und wurde auf den Folgetag gelegt."
	}
	return out, nil
}

func toolDelete(x *mcpCtx, args map[string]any) (any, error) {
	id, err := needInt(args, "record_id")
	if err != nil {
		return nil, err
	}
	c, m, rec, err := x.recC(id, true)
	if err != nil {
		return nil, err
	}
	if m.Key == "contacts" && rec.I("user") != 0 {
		return nil, toolErrf("Dieser Kontakt gehört zu einem Benutzerkonto und kann nicht gelöscht werden.")
	}
	title := c.A.recTitle(c, m, rec)
	c.A.delRec(rec.ID)
	if !m.Global {
		var area int64
		if m.AreaField != "" {
			area = rec.I(m.AreaField)
		}
		c.A.logAudit(c.User.ID, c.Event.ID, m.Key, rec.ID, "gelöscht (MCP)", title, area)
	}
	return map[string]any{"deleted": rec.ID, "module": m.Key, "title": title}, nil
}

func toolAction(x *mcpCtx, args map[string]any) (any, error) {
	id, err := needInt(args, "record_id")
	if err != nil {
		return nil, err
	}
	c, m, rec, err := x.recC(id, true)
	if err != nil {
		return nil, err
	}
	key := argStr(args, "action")
	for _, a := range m.Actions {
		if a.Key == key && c.Level(m.PermKey()) >= a.Level {
			msg := a.Fn(c, rec)
			c.audit(m, rec, a.Label+" (MCP)")
			return map[string]any{"record_id": rec.ID, "action": a.Key, "result": msg}, nil
		}
	}
	var keys []string
	for _, a := range m.Actions {
		keys = append(keys, a.Key)
	}
	if len(keys) == 0 {
		return nil, toolErrf("Das Modul %s hat keine Aktionen.", m.Key)
	}
	return nil, toolErrf("Unbekannte Aktion %q. Möglich: %s.", key, strings.Join(keys, ", "))
}

func toolUpload(x *mcpCtx, args map[string]any) (any, error) {
	id, err := needInt(args, "record_id")
	if err != nil {
		return nil, err
	}
	c, m, rec, err := x.recC(id, true)
	if err != nil {
		return nil, err
	}
	if !c.attachable(m) {
		return nil, toolErrf("Für dieses Modul gibt es keine Anhänge.")
	}
	name := argStr(args, "name")
	if name == "" {
		return nil, toolErrf("Parameter name fehlt.")
	}
	var data []byte
	if s, ok := args["content_base64"].(string); ok && s != "" {
		if data, err = base64.StdEncoding.DecodeString(strings.TrimSpace(s)); err != nil {
			return nil, toolErrf("content_base64 ist kein gültiges Base64.")
		}
	} else if s, ok := args["text"].(string); ok {
		data = []byte(s)
	} else {
		return nil, toolErrf("content_base64 oder text angeben.")
	}
	if len(data) > mcpMaxFile {
		return nil, toolErrf("Über MCP gehen höchstens 10 MB pro Datei. Größere Dateien bitte in Kollekt selbst hochladen.")
	}
	if len(c.A.filesFor(rec.ID)) >= maxFilesPerItem {
		return nil, toolErrf("Dieser Eintrag hat schon %d Anhänge (Höchstzahl).", maxFilesPerItem)
	}
	var evID int64
	if !m.Global {
		evID = c.Event.ID
	}
	if err := c.storeFile(bytes.NewReader(data), int64(len(data)), name, evID, m.Key, rec.ID); err != nil {
		return nil, &toolError{err.Error()}
	}
	c.audit(m, rec, "Anhang hinzugefügt (MCP)")
	files := c.A.filesFor(rec.ID)
	f := files[len(files)-1]
	return map[string]any{"file_id": f.ID, "name": f.Name, "size": humanSize(f.Size), "record_id": rec.ID}, nil
}

func toolDeleteFile(x *mcpCtx, args map[string]any) (any, error) {
	id, err := needInt(args, "file_id")
	if err != nil {
		return nil, err
	}
	c, f, m, rec, err := x.fileC(id, true)
	if err != nil {
		return nil, err
	}
	c.A.removeFile(f)
	c.audit(m, rec, "Anhang entfernt (MCP)")
	return map[string]any{"deleted_file": f.ID, "name": f.Name}, nil
}
