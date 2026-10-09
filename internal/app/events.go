// Copyright (C) 2026 yniverz
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"net/http"
	"sort"
	"strings"
)

func (a *App) locationOptions(selected int64) []RefOpt {
	var out []RefOpt
	for _, l := range a.recs(0, "locations") {
		out = append(out, RefOpt{itoa(l.ID), l.S("name"), l.ID == selected})
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Title) < strings.ToLower(out[j].Title) })
	return out
}

func (a *App) eventNew(c *C) {
	if !(c.User.IsAdmin || c.User.CanCreate) {
		c.Error(403, "Du darfst keine Events anlegen.")
		return
	}
	c.Page("event_new.html", map[string]any{
		"Title": "Neues Event", "Templates": a.templates(), "Locations": a.locationOptions(0), "Statuses": eventStatuses,
		"Form": map[string]string{"template": c.R.URL.Query().Get("template")},
	})
}

func (a *App) eventCreate(c *C) {
	if !(c.User.IsAdmin || c.User.CanCreate) {
		c.Error(403, "Du darfst keine Events anlegen.")
		return
	}
	r := c.R
	name := strings.TrimSpace(r.FormValue("name"))
	form := map[string]string{"name": name, "start": r.FormValue("start"), "end": r.FormValue("end"), "template": r.FormValue("template"), "location": r.FormValue("location")}
	if name == "" {
		c.W.WriteHeader(422)
		c.Page("event_new.html", map[string]any{"Title": "Neues Event", "Templates": a.templates(), "Locations": a.locationOptions(0), "Statuses": eventStatuses, "Form": form, "Error": "Bitte gib einen Namen an."})
		return
	}
	e := &Event{Name: name, Status: "idea", Settings: map[string]jsonRaw{}, CreatedBy: c.User.ID}
	if _, ok := parseDT(r.FormValue("start")); ok {
		e.Start = r.FormValue("start")
	}
	if _, ok := parseDT(r.FormValue("end")); ok {
		e.End = r.FormValue("end")
	}
	if lid := int64(parseNum(r.FormValue("location"))); lid != 0 {
		if l := a.rec(lid); l != nil && l.Module == "locations" {
			e.LocationID = lid
		}
	}
	var payload *TplPayload
	if tid := int64(parseNum(r.FormValue("template"))); tid != 0 {
		if t := a.template(tid); t != nil {
			e.Kind = t.Name
			payload = &t.Payload
			e.Modules = append([]string{}, t.Payload.Modules...)
			for k, v := range t.Payload.Settings {
				e.Settings[k] = v
			}
		}
	}
	if payload == nil {
		e.Modules = defaultModules()
	}
	if err := a.saveEvent(e); err != nil {
		c.Error(500, "Event konnte nicht angelegt werden.")
		return
	}
	if payload != nil {
		a.instantiate(e, *payload, c.User.ID)
		a.applyRelative(e)
	}
	for _, rl := range a.roles() {
		if rl.Name == "Orga-Leitung" {
			a.setMember(e.ID, c.User.ID, rl.ID, nil)
		}
	}
	a.logAudit(c.User.ID, e.ID, "event", e.ID, "angelegt", e.Name)
	c.setFlash("Event angelegt.")
	c.Redirect("/e/" + itoa(e.ID) + "/")
}

func (a *App) settingsPage(c *C) {
	if !c.CanEdit("settings") {
		c.Error(403, "Dafür fehlt dir die Berechtigung.")
		return
	}
	c.renderSettings(nil, "")
}

func (c *C) renderSettings(form map[string]string, errMsg string) {
	e := c.Event
	if form == nil {
		form = map[string]string{"name": e.Name, "status": e.Status, "start": e.Start, "end": e.End, "description": e.Description}
	}
	type ModView struct {
		M      *Module
		On     bool
		Locked bool
	}
	var mods []ModView
	for _, m := range eventModules() {
		if m.Key == "overview" {
			continue
		}
		mods = append(mods, ModView{m, m.Core || e.Has(m.Key), m.Core})
	}
	if errMsg != "" {
		c.W.WriteHeader(422)
	}
	c.Page("settings.html", map[string]any{
		"Title": "Event-Einstellungen", "Nav": c.eventNav(""), "Form": form, "Statuses": eventStatuses, "Mods": mods,
		"Locations": c.A.locationOptions(e.LocationID), "Error": errMsg, "Tax": c.tax(), "Lim": c.limits(),
	})
}

func (a *App) settingsSave(c *C) {
	if !c.CanEdit("settings") {
		c.Error(403, "Dafür fehlt dir die Berechtigung.")
		return
	}
	r := c.R
	form := map[string]string{"name": strings.TrimSpace(r.FormValue("name")), "status": r.FormValue("status"), "start": r.FormValue("start"), "end": r.FormValue("end"), "description": r.FormValue("description")}
	if form["name"] == "" {
		c.renderSettings(form, "Bitte gib einen Namen an.")
		return
	}
	e := c.Event
	e.Name = form["name"]
	for _, o := range eventStatuses {
		if o.V == form["status"] {
			e.Status = o.V
		}
	}
	e.Start, e.End = "", ""
	if _, ok := parseDT(form["start"]); ok {
		e.Start = form["start"]
	}
	if _, ok := parseDT(form["end"]); ok {
		e.End = form["end"]
	}
	e.Description = form["description"]
	e.LocationID = 0
	if lid := int64(parseNum(r.FormValue("location"))); lid != 0 {
		if l := a.rec(lid); l != nil && l.Module == "locations" {
			e.LocationID = lid
		}
	}
	tx := TaxSettings{Basis: r.FormValue("tax_basis"), Entry: r.FormValue("tax_entry"), VAT: parseNum(r.FormValue("tax_vat"))}
	if (tx.Basis == "net" || tx.Basis == "gross") && (tx.Entry == "net" || tx.Entry == "gross") && tx.VAT >= 0 && tx.VAT <= 100 {
		e.setting("tax", tx)
	}
	c.handleLimitsSave(r)
	var mods []string
	for _, m := range eventModules() {
		if m.Key == "overview" || m.Core {
			continue
		}
		if r.FormValue("mod_"+m.Key) != "" {
			mods = append(mods, m.Key)
		}
	}
	e.Modules = mods
	if err := a.saveEvent(e); err != nil {
		c.Error(500, "Speichern fehlgeschlagen.")
		return
	}
	a.applyRelative(e)
	a.logAudit(c.User.ID, e.ID, "event", e.ID, "Einstellungen geändert", e.Name)
	c.setFlash("Einstellungen gespeichert.")
	c.Redirect("/e/" + itoa(e.ID) + "/")
}

func (a *App) eventDelete(c *C) {
	if !c.CanEdit("settings") {
		c.Error(403, "Dafür fehlt dir die Berechtigung.")
		return
	}
	if strings.TrimSpace(c.R.FormValue("confirm")) != c.Event.Name {
		c.setFlash("Zum Löschen muss der Eventname exakt eingegeben werden.")
		c.Redirect("/e/" + itoa(c.Event.ID) + "/settings")
		return
	}
	a.deleteEvent(c.Event.ID)
	c.setFlash("Event gelöscht.")
	c.Redirect("/")
}

func (a *App) saveAsTemplate(c *C) {
	if !c.CanEdit("settings") || !(c.User.IsAdmin || c.User.CanCreate) {
		c.Error(403, "Dafür fehlt dir die Berechtigung.")
		return
	}
	name := strings.TrimSpace(c.R.FormValue("name"))
	if name == "" {
		name = c.Event.Name + " (Vorlage)"
	}
	p := a.snapshot(c.Event, SnapOpts{Amounts: c.R.FormValue("amounts") != "", People: c.R.FormValue("people") != ""})
	t := &Template{Name: name, Description: "Aus dem Event „" + c.Event.Name + "“ erstellt.", Payload: p}
	if err := a.saveTemplate(t); err != nil {
		c.Error(500, "Vorlage konnte nicht gespeichert werden.")
		return
	}
	c.setFlash("Vorlage „" + name + "“ gespeichert.")
	c.Redirect("/e/" + itoa(c.Event.ID) + "/settings")
}

func (a *App) eventDuplicate(c *C) {
	if !c.CanEdit("settings") || !(c.User.IsAdmin || c.User.CanCreate) {
		c.Error(403, "Dafür fehlt dir die Berechtigung.")
		return
	}
	src := c.Event
	name := strings.TrimSpace(c.R.FormValue("name"))
	if name == "" {
		name = src.Name + " (Kopie)"
	}
	p := a.snapshot(src, SnapOpts{Amounts: true, People: true})
	e := &Event{Name: name, Status: "idea", Kind: src.Kind, LocationID: src.LocationID, Modules: append([]string{}, src.Modules...), Settings: map[string]jsonRaw{}, CreatedBy: c.User.ID, Description: src.Description}
	for k, v := range p.Settings {
		e.Settings[k] = v
	}
	if err := a.saveEvent(e); err != nil {
		c.Error(500, "Duplizieren fehlgeschlagen.")
		return
	}
	a.instantiate(e, p, c.User.ID)
	for _, m := range a.members(src.ID) {
		a.setMember(e.ID, m.UserID, m.RoleID, nil)
	}
	for _, rl := range a.roles() {
		if rl.Name == "Orga-Leitung" && a.member(e.ID, c.User.ID) == nil {
			a.setMember(e.ID, c.User.ID, rl.ID, nil)
		}
	}
	c.setFlash("Event dupliziert. Daten, Zuständigkeiten und Beträge sind übernommen, Termine und Stati zurückgesetzt.")
	c.Redirect("/e/" + itoa(e.ID) + "/")
}

// ---------- team ----------

func (a *App) teamPage(c *C) {
	if !c.CanEdit("settings") {
		c.Error(403, "Dafür fehlt dir die Berechtigung.")
		return
	}
	members := a.members(c.Event.ID)
	inTeam := map[int64]bool{}
	for _, m := range members {
		inTeam[m.UserID] = true
	}
	var avail []*User
	for _, u := range a.allUsers() {
		if !inTeam[u.ID] && !u.Disabled {
			avail = append(avail, u)
		}
	}
	var areas []RefOpt
	for _, ar := range c.Recs("areas") {
		areas = append(areas, RefOpt{itoa(ar.ID), ar.S("name"), false})
	}
	type MemView struct {
		M     *Member
		Areas string
		Sel   map[int64]bool
	}
	var mv []MemView
	for _, m := range members {
		var names []string
		sel := map[int64]bool{}
		for _, id := range m.Areas {
			sel[id] = true
			if t := c.RefTitle("areas", id); t != "" {
				names = append(names, t)
			}
		}
		mv = append(mv, MemView{m, strings.Join(names, ", "), sel})
	}
	sort.Slice(mv, func(i, j int) bool {
		return mv[i].M.User != nil && mv[j].M.User != nil && mv[i].M.User.Name < mv[j].M.User.Name
	})
	c.Page("team.html", map[string]any{
		"Title": "Team & Rechte", "Nav": c.eventNav(""), "Members": mv, "Avail": avail, "Roles": a.roles(), "Areas": areas,
		"IsAdmin": c.User.IsAdmin,
	})
}

func (a *App) teamSave(c *C) {
	if !c.CanEdit("settings") {
		c.Error(403, "Dafür fehlt dir die Berechtigung.")
		return
	}
	r := c.R
	_ = r.ParseForm()
	uid := int64(parseNum(r.FormValue("user")))
	rid := int64(parseNum(r.FormValue("role")))
	if a.user(uid) == nil || a.role(rid) == nil {
		c.setFlash("Benutzer oder Rolle nicht gefunden.")
		c.Redirect("/e/" + itoa(c.Event.ID) + "/team")
		return
	}
	var areas []int64
	valid := map[int64]bool{}
	for _, ar := range c.Recs("areas") {
		valid[ar.ID] = true
	}
	for _, s := range r.Form["area"] {
		if id := int64(parseNum(s)); valid[id] {
			areas = append(areas, id)
		}
	}
	a.setMember(c.Event.ID, uid, rid, areas)
	a.logAudit(c.User.ID, c.Event.ID, "team", uid, "Team geändert", a.user(uid).Name)
	c.setFlash("Team gespeichert.")
	c.Redirect("/e/" + itoa(c.Event.ID) + "/team")
}

func (a *App) teamRemove(c *C) {
	if !c.CanEdit("settings") {
		c.Error(403, "Dafür fehlt dir die Berechtigung.")
		return
	}
	uid := pathInt(c.R, "uid")
	if uid == c.User.ID && !c.User.IsAdmin {
		c.setFlash("Du kannst dich nicht selbst entfernen.")
	} else {
		a.removeMember(c.Event.ID, uid)
		c.setFlash("Mitglied entfernt.")
	}
	c.Redirect("/e/" + itoa(c.Event.ID) + "/team")
}

var _ = http.StatusOK
