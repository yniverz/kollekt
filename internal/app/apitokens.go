// Copyright (C) 2026 yniverz
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"strings"
	"time"
)

// Personal API tokens authenticate MCP clients (Claude Code, Claude Desktop …) as one user.
// Only the hash is stored; the token itself is shown once after creating it.

const (
	apiTokenPrefix = "kx_"
	maxAPITokens   = 20
)

type APIToken struct {
	ID       int64
	Name     string
	CanWrite bool
	Created  time.Time
	LastUsed time.Time
}

func (a *App) apiTokens(userID int64) []*APIToken {
	rows, err := a.db.Query("SELECT id,name,can_write,created_at,last_used FROM api_tokens WHERE user_id=? ORDER BY id", userID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []*APIToken
	for rows.Next() {
		t := &APIToken{}
		var w int
		var c, u int64
		if rows.Scan(&t.ID, &t.Name, &w, &c, &u) == nil {
			t.CanWrite, t.Created = w == 1, time.Unix(c, 0)
			if u > 0 {
				t.LastUsed = time.Unix(u, 0)
			}
			out = append(out, t)
		}
	}
	return out
}

func (a *App) newAPIToken(userID int64, name string, canWrite bool) (string, error) {
	tok := apiTokenPrefix + randToken(32)
	_, err := a.db.Exec("INSERT INTO api_tokens(user_id,name,token_hash,can_write,created_at) VALUES(?,?,?,?,?)",
		userID, name, hashToken(tok), b2i(canWrite), time.Now().Unix())
	return tok, err
}

// apiTokenUser resolves a bearer token to its (enabled) user and whether it may write.
func (a *App) apiTokenUser(tok string) (*User, bool) {
	if !strings.HasPrefix(tok, apiTokenPrefix) || len(tok) < 40 {
		return nil, false
	}
	var id, uid, last int64
	var w int
	if a.db.QueryRow("SELECT id,user_id,can_write,last_used FROM api_tokens WHERE token_hash=?", hashToken(tok)).Scan(&id, &uid, &w, &last) != nil {
		return nil, false
	}
	u := a.user(uid)
	if u == nil || u.Disabled {
		return nil, false
	}
	if now := time.Now().Unix(); now-last > 60 { // spare the database a write on every call
		_, _ = a.db.Exec("UPDATE api_tokens SET last_used=? WHERE id=?", now, id)
	}
	return u, w == 1
}

func (a *App) accountPage(c *C) {
	c.renderAccount(nil)
}

func (c *C) renderAccount(extra map[string]any) {
	data := map[string]any{"Title": "Mein Konto", "Tokens": c.A.apiTokens(c.User.ID), "Grants": c.A.oauthGrants(c.User.ID), "MCPOn": !mcpOff, "MCPURL": c.A.mcpResource(c.R)}
	for k, v := range extra {
		data[k] = v
	}
	c.Page("account.html", data)
}

func (a *App) tokenCreate(c *C) {
	if mcpOff {
		c.Error(404, "Die MCP-Schnittstelle ist in dieser Installation abgeschaltet.")
		return
	}
	name := strings.TrimSpace(c.R.FormValue("name"))
	if name == "" {
		name = "MCP"
	}
	if len([]rune(name)) > 60 {
		name = string([]rune(name)[:60])
	}
	if len(a.apiTokens(c.User.ID)) >= maxAPITokens {
		c.W.WriteHeader(422)
		c.renderAccount(map[string]any{"TokenError": "Du hast schon 20 Tokens. Widerrufe zuerst einen, den du nicht mehr brauchst."})
		return
	}
	canWrite := c.R.FormValue("write") != ""
	tok, err := a.newAPIToken(c.User.ID, name, canWrite)
	if err != nil {
		c.Error(500, "Token konnte nicht erzeugt werden.")
		return
	}
	c.renderAccount(map[string]any{"NewToken": tok, "NewTokenName": name, "NewTokenWrite": canWrite})
}

func (a *App) tokenDelete(c *C) {
	res, err := a.db.Exec("DELETE FROM api_tokens WHERE id=? AND user_id=?", pathInt(c.R, "tid"), c.User.ID)
	if err != nil {
		c.Error(500, "Token konnte nicht widerrufen werden.")
		return
	}
	if n, _ := res.RowsAffected(); n > 0 {
		c.setFlash("Token widerrufen. Programme, die ihn nutzen, haben keinen Zugriff mehr.")
	}
	c.Redirect("/account")
}
