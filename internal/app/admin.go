package app

import (
	"encoding/json"
	"strings"
)

type jsonRaw = json.RawMessage

// ---------- users ----------

func (a *App) usersPage(c *C) {
	c.Page("admin_users.html", map[string]any{"Title": "Benutzer", "Users": a.allUsers(), "Section": "users"})
}

func (a *App) userCreate(c *C) {
	r := c.R
	name, user, pw := strings.TrimSpace(r.FormValue("name")), strings.TrimSpace(r.FormValue("username")), r.FormValue("password")
	fail := func(msg string) {
		c.W.WriteHeader(422)
		c.Page("admin_users.html", map[string]any{"Title": "Benutzer", "Users": a.allUsers(), "Section": "users", "Error": msg, "Form": map[string]string{"name": name, "username": user}})
	}
	if name == "" || user == "" {
		fail("Name und Benutzername sind Pflicht.")
		return
	}
	if len(pw) < 10 {
		fail("Das Passwort braucht mindestens 10 Zeichen.")
		return
	}
	if _, err := a.createUser(user, name, pw, r.FormValue("admin") != "", r.FormValue("create") != "", r.FormValue("master") != ""); err != nil {
		fail("Benutzername ist schon vergeben.")
		return
	}
	c.setFlash("Benutzer angelegt.")
	c.Redirect("/admin/users")
}

func (a *App) userEditPage(c *C) {
	u := a.user(pathInt(c.R, "uid"))
	if u == nil {
		c.Error(404, "Benutzer nicht gefunden.")
		return
	}
	c.Page("admin_user.html", map[string]any{"Title": u.Name, "U": u, "Section": "users"})
}

func (a *App) userEditSave(c *C) {
	u := a.user(pathInt(c.R, "uid"))
	if u == nil {
		c.Error(404, "Benutzer nicht gefunden.")
		return
	}
	r := c.R
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		name = u.Name
	}
	admin, disabled := r.FormValue("admin") != "", r.FormValue("disabled") != ""
	if u.ID == c.User.ID && (!admin || disabled) {
		c.setFlash("Du kannst dir selbst nicht den Admin-Zugang entziehen oder dich deaktivieren.")
		c.Redirect("/admin/users/" + itoa(u.ID))
		return
	}
	_, _ = a.db.Exec("UPDATE users SET name=?, is_admin=?, can_create=?, can_master=?, disabled=? WHERE id=?",
		name, b2i(admin), b2i(r.FormValue("create") != ""), b2i(r.FormValue("master") != ""), b2i(disabled), u.ID)
	if u.ContactID != 0 {
		if ct := a.rec(u.ContactID); ct != nil {
			ct.D["name"] = name
			_ = a.saveRec(ct)
		}
	}
	if pw := r.FormValue("password"); pw != "" {
		if len(pw) < 10 {
			c.setFlash("Das neue Passwort braucht mindestens 10 Zeichen. Übrige Änderungen wurden gespeichert.")
			c.Redirect("/admin/users/" + itoa(u.ID))
			return
		}
		h, _ := hashPW(pw)
		_, _ = a.db.Exec("UPDATE users SET pass_hash=? WHERE id=?", h, u.ID)
		_, _ = a.db.Exec("DELETE FROM sessions WHERE user_id=?", u.ID)
	}
	if disabled {
		_, _ = a.db.Exec("DELETE FROM sessions WHERE user_id=?", u.ID)
	}
	c.setFlash("Benutzer gespeichert.")
	c.Redirect("/admin/users")
}

// ---------- roles ----------

func (a *App) rolesPage(c *C) {
	c.Page("admin_roles.html", map[string]any{"Title": "Rollen", "Roles": a.roles(), "Section": "roles"})
}

type PermRow struct {
	Def   PermDef
	Level int
}

func (a *App) roleEditPage(c *C) {
	idStr := c.R.PathValue("rid")
	role := &Role{Perms: map[string]int{}}
	if idStr != "new" {
		role = a.role(pathInt(c.R, "rid"))
		if role == nil {
			c.Error(404, "Rolle nicht gefunden.")
			return
		}
	}
	a.renderRole(c, role, "")
}

func (a *App) renderRole(c *C, role *Role, errMsg string) {
	var rows []PermRow
	for _, d := range permDefs() {
		rows = append(rows, PermRow{d, role.Perms[d.Key]})
	}
	if errMsg != "" {
		c.W.WriteHeader(422)
	}
	c.Page("admin_role.html", map[string]any{"Title": strOr(role.Name, "Neue Rolle"), "R": role, "Rows": rows, "Section": "roles", "Error": errMsg})
}

func (a *App) roleEditSave(c *C) {
	r := c.R
	role := &Role{Perms: map[string]int{}}
	if r.PathValue("rid") != "new" {
		role = a.role(pathInt(r, "rid"))
		if role == nil {
			c.Error(404, "Rolle nicht gefunden.")
			return
		}
	}
	role.Name = strings.TrimSpace(r.FormValue("name"))
	role.Description = strings.TrimSpace(r.FormValue("description"))
	role.AreaScoped = r.FormValue("area_scoped") != ""
	role.Perms = map[string]int{}
	for _, d := range permDefs() {
		l := int(parseNum(r.FormValue("perm_" + d.Key)))
		if l < 0 || l > 2 {
			l = 0
		}
		role.Perms[d.Key] = l
	}
	if role.Name == "" {
		a.renderRole(c, role, "Bitte gib der Rolle einen Namen.")
		return
	}
	if err := a.saveRole(role); err != nil {
		c.Error(500, "Speichern fehlgeschlagen.")
		return
	}
	c.setFlash("Rolle gespeichert.")
	c.Redirect("/admin/roles")
}

func (a *App) roleDelete(c *C) {
	role := a.role(pathInt(c.R, "rid"))
	if role == nil {
		c.Error(404, "Rolle nicht gefunden.")
		return
	}
	var n int
	_ = a.db.QueryRow("SELECT COUNT(*) FROM members WHERE role_id=?", role.ID).Scan(&n)
	if n > 0 {
		c.setFlash("Die Rolle wird noch verwendet und kann nicht gelöscht werden.")
		c.Redirect("/admin/roles")
		return
	}
	if role.Builtin && role.Name == "Orga-Leitung" {
		c.setFlash("Orga-Leitung wird für neue Events benötigt.")
		c.Redirect("/admin/roles")
		return
	}
	_, _ = a.db.Exec("DELETE FROM roles WHERE id=?", role.ID)
	c.setFlash("Rolle gelöscht.")
	c.Redirect("/admin/roles")
}

// ---------- templates ----------

func (a *App) templatesPage(c *C) {
	type TV struct {
		T     *Template
		Count int
	}
	var out []TV
	for _, t := range a.templates() {
		out = append(out, TV{t, len(t.Payload.Records)})
	}
	c.Page("admin_templates.html", map[string]any{"Title": "Vorlagen", "Templates": out, "Section": "templates"})
}

func (a *App) templateDelete(c *C) {
	_, _ = a.db.Exec("DELETE FROM templates WHERE id=?", pathInt(c.R, "tid"))
	c.setFlash("Vorlage gelöscht.")
	c.Redirect("/admin/templates")
}

func (a *App) templateRename(c *C) {
	t := a.template(pathInt(c.R, "tid"))
	if t == nil {
		c.Error(404, "Vorlage nicht gefunden.")
		return
	}
	if n := strings.TrimSpace(c.R.FormValue("name")); n != "" {
		t.Name = n
	}
	t.Description = strings.TrimSpace(c.R.FormValue("description"))
	_ = a.saveTemplate(t)
	c.setFlash("Vorlage gespeichert.")
	c.Redirect("/admin/templates")
}
