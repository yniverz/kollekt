// Copyright (C) 2026 yniverz
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
)

// The web tool opens any Kollekt page or submits any form through the regular handlers, as the
// token's user. Permissions, validation and the change log are exactly those of the browser, and
// pages added later are reachable without a dedicated tool.

type mcpSessionKey struct{}

// webBlocked lists paths an assistant must not use: sign-in, own credentials, the MCP endpoint
// itself and raw downloads (read_file and upload_file handle files).
var webBlocked = []string{"/login", "/logout", "/setup", "/account", "/mcp", "/oauth", "/.well-known", "/cal/", "/static/", "/healthz", "/favicon.ico"}

var webFileRoute = regexp.MustCompile(`^/(e/\d+/files/|gf/|e/\d+/lageplan/\d+/image)|/files$`)

const webMaxText = 40000

func toolWeb(x *mcpCtx, args map[string]any) (any, error) {
	method := strings.ToUpper(argStr(args, "method"))
	if method == "" {
		method = http.MethodGet
	}
	if method != http.MethodGet && method != http.MethodPost {
		return nil, toolErrf("method muss GET oder POST sein.")
	}
	u, err := url.Parse(argStr(args, "path"))
	if err != nil || !safeLocal(u.Path) || u.Host != "" || u.Scheme != "" {
		return nil, toolErrf("path muss ein Pfad in Kollekt sein, z. B. /e/3/settings.")
	}
	for _, p := range webBlocked {
		if u.Path == strings.TrimSuffix(p, "/") || strings.HasPrefix(u.Path, strings.TrimSuffix(p, "/")+"/") {
			return nil, toolErrf("%s ist über MCP nicht erreichbar (Anmeldung, eigenes Konto und Zugangsdaten bleiben bei dir).", u.Path)
		}
	}
	if webFileRoute.MatchString(u.Path) {
		return nil, toolErrf("Dateien laufen über read_file, upload_file und delete_file, nicht über web.")
	}
	if method == http.MethodPost && !x.write {
		return nil, toolErrf("Dieser Zugang hat nur Lesezugriff. Absenden (POST) braucht Schreibzugriff.")
	}
	form := url.Values{}
	if fm, ok := args["form"].(map[string]any); ok {
		for k, v := range fm {
			switch t := v.(type) {
			case []any:
				for _, it := range t {
					form.Add(k, webStr(it))
				}
			default:
				form.Set(k, webStr(t))
			}
		}
	}
	sess := &Session{User: x.user, CSRF: randToken(16)}
	ctx := context.WithValue(x.r.Context(), mcpSessionKey{}, sess)
	if from := argStr(args, "defaults_from"); from != "" && method == http.MethodPost {
		page, code := x.webGet(ctx, from)
		if code != http.StatusOK {
			return nil, toolErrf("defaults_from %s ist nicht lesbar (HTTP %d).", from, code)
		}
		defs, ok := formDefaults(page, u.Path)
		if !ok {
			return nil, toolErrf("Auf %s gibt es kein Formular mit dem Ziel %s.", from, u.Path)
		}
		if fm, ok := args["form"].(map[string]any); ok {
			for k, v := range fm {
				if v == nil || v == false {
					defs.Del(k) // unticked checkbox or cleared choice
				} else {
					defs[k] = form[k]
				}
			}
		}
		form = defs
	}
	target := u.Path
	if method == http.MethodGet {
		q := u.Query()
		for k, v := range form {
			q[k] = v
		}
		if len(q) > 0 {
			target += "?" + q.Encode()
		}
	} else if u.RawQuery != "" {
		target += "?" + u.RawQuery
	}
	for hop := 0; ; hop++ {
		var body io.Reader
		if method == http.MethodPost {
			form.Set("_csrf", sess.CSRF)
			body = strings.NewReader(form.Encode())
		}
		req, _ := http.NewRequestWithContext(ctx, method, target, body)
		req.Host, req.RemoteAddr = x.r.Host, x.r.RemoteAddr
		for _, h := range []string{"X-Forwarded-Proto", "X-Forwarded-Host", "X-Forwarded-For"} {
			if v := x.r.Header.Get(h); v != "" {
				req.Header.Set(h, v)
			}
		}
		if method == http.MethodPost {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		rec := httptest.NewRecorder()
		x.a.mux.ServeHTTP(rec, req)
		res := rec.Result()
		flash := ""
		for _, ck := range res.Cookies() {
			if ck.Name == "kollekt_flash" && ck.MaxAge > 0 {
				flash, _ = url.QueryUnescape(ck.Value)
			}
		}
		if res.StatusCode >= 300 && res.StatusCode < 400 {
			loc := res.Header.Get("Location")
			if method == http.MethodPost {
				out := map[string]any{"status": "ok", "redirect": loc}
				if flash != "" {
					out["message"] = flash
				}
				return out, nil
			}
			if hop >= 3 || !safeLocal(loc) {
				return map[string]any{"status": res.StatusCode, "redirect": loc}, nil
			}
			target = loc
			continue
		}
		b, _ := io.ReadAll(io.LimitReader(res.Body, 4<<20))
		ct := res.Header.Get("Content-Type")
		if ct == "" && len(b) > 0 { // error pages set their status before the content type
			ct = http.DetectContentType(b)
		}
		var text string
		switch {
		case strings.HasPrefix(ct, "text/html"):
			text = htmlToText(string(b))
		case strings.HasPrefix(ct, "application/json"), strings.HasPrefix(ct, "text/"):
			text = string(b)
		case res.StatusCode == http.StatusNoContent:
			text = "Erledigt."
		default:
			return nil, toolErrf("Die Seite liefert %s (HTTP %d), das hier nicht lesbar ist.", ct, res.StatusCode)
		}
		if len(text) > webMaxText {
			text = text[:webMaxText] + "\n… (gekürzt)"
		}
		head := fmt.Sprintf("%s %s → HTTP %d", method, target, res.StatusCode)
		if method == http.MethodPost && res.StatusCode < 300 {
			head += " (gespeichert bzw. ausgeführt)"
		} else if res.StatusCode >= 400 {
			head += " (nicht ausgeführt)"
		}
		if flash != "" {
			head += "\nMeldung: " + flash
		}
		if res.StatusCode >= 400 {
			return nil, &toolError{head + "\n\n" + text}
		}
		return head + "\n\n" + text, nil
	}
}

// webGet fetches a page as the token's user and returns its HTML.
func (x *mcpCtx) webGet(ctx context.Context, path string) (string, int) {
	if !safeLocal(path) {
		return "", http.StatusBadRequest
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, path, nil)
	req.Host, req.RemoteAddr = x.r.Host, x.r.RemoteAddr
	rec := httptest.NewRecorder()
	x.a.mux.ServeHTTP(rec, req)
	return rec.Body.String(), rec.Code
}

var (
	formStartRe = regexp.MustCompile(`(?is)<form\b([^>]*)>`)
	templateRe  = regexp.MustCompile(`(?is)<template\b.*?</template>`)
	fieldTagRe  = regexp.MustCompile(`(?is)<(/?)(input|select|option|textarea)\b([^>]*)>`)
)

// formDefaults returns the current values of the form posting to action, as a browser would send
// them unchanged: text fields, ticked checkboxes, selected options and text areas.
func formDefaults(page, action string) (url.Values, bool) {
	for _, m := range formStartRe.FindAllStringSubmatchIndex(page, -1) {
		at := parseAttrs(page[m[2]:m[3]])
		a, _ := url.Parse(at["action"])
		if a == nil || a.Path != action {
			continue
		}
		body := page[m[1]:]
		if end := strings.Index(strings.ToLower(body), "</form>"); end >= 0 {
			body = body[:end]
		}
		body = templateRe.ReplaceAllString(body, "") // row templates for scripts are not submitted
		vals := url.Values{}
		var selName string
		var selFirst string
		selSet, inSel := false, false
		for _, t := range fieldTagRe.FindAllStringSubmatchIndex(body, -1) {
			closing := body[t[2]:t[3]] == "/"
			tag := strings.ToLower(body[t[4]:t[5]])
			fa := parseAttrs(body[t[6]:t[7]])
			_, disabled := fa["disabled"]
			switch {
			case tag == "input" && !closing && !disabled:
				n := fa["name"]
				typ := strings.ToLower(fa["type"])
				if n == "" || n == "_csrf" || typ == "submit" || typ == "button" || typ == "file" {
					continue
				}
				if typ == "checkbox" || typ == "radio" {
					if _, ok := fa["checked"]; ok {
						v := fa["value"]
						if v == "" {
							v = "on"
						}
						vals.Add(n, v)
					}
					continue
				}
				vals.Add(n, fa["value"])
			case tag == "select" && !closing:
				selName, selFirst, selSet, inSel = fa["name"], "\x00", false, !disabled
			case tag == "option" && !closing && inSel:
				v, has := fa["value"]
				if !has { // value is the option text
					rest := body[t[1]:]
					if e := strings.Index(rest, "<"); e >= 0 {
						v = strings.TrimSpace(html.UnescapeString(rest[:e]))
					}
				}
				if selFirst == "\x00" {
					selFirst = v
				}
				if _, ok := fa["selected"]; ok {
					vals.Add(selName, v)
					selSet = true
				}
			case tag == "select" && closing:
				if inSel && !selSet && selName != "" && selFirst != "\x00" {
					vals.Add(selName, selFirst)
				}
				inSel = false
			case tag == "textarea" && !closing && !disabled:
				rest := body[t[1]:]
				if e := strings.Index(strings.ToLower(rest), "</textarea>"); e >= 0 && fa["name"] != "" {
					vals.Add(fa["name"], html.UnescapeString(strings.TrimPrefix(rest[:e], "\n")))
				}
			}
		}
		return vals, true
	}
	return nil, false
}

func webStr(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case bool:
		if t {
			return "1"
		}
		return ""
	case float64:
		return numStr(t)
	}
	return fmt.Sprint(v)
}

// ---------- HTML to text ----------

var blockTags = map[string]bool{"div": true, "p": true, "section": true, "header": true, "nav": true, "ul": true, "ol": true, "table": true,
	"tr": true, "br": true, "details": true, "summary": true, "h1": true, "h2": true, "h3": true, "h4": true, "li": true, "main": true, "aside": true, "fieldset": true, "legend": true, "dl": true, "dt": true, "dd": true, "hr": true}

var attrRe = regexp.MustCompile(`([a-zA-Z_:][-a-zA-Z0-9_:.]*)(?:\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s"'>]+)))?`)

func parseAttrs(s string) map[string]string {
	out := map[string]string{}
	for _, m := range attrRe.FindAllStringSubmatch(s, -1) {
		v := m[2] + m[3] + m[4]
		out[strings.ToLower(m[1])] = html.UnescapeString(v)
	}
	return out
}

type webOpt struct {
	v, l string
	sel  bool
}

// htmlToText renders a Kollekt page as plain text and spells out forms, fields and links,
// so an assistant can read the page and submit its forms.
func htmlToText(src string) string {
	var b strings.Builder
	nl := func() {
		s := b.String()
		if !strings.HasSuffix(s, "\n") && s != "" {
			b.WriteString("\n")
		}
	}
	text := func(t string) {
		t = strings.Join(strings.Fields(html.UnescapeString(t)), " ")
		if t == "" {
			return
		}
		s := b.String()
		if s != "" && !strings.HasSuffix(s, "\n") && !strings.HasSuffix(s, " ") {
			b.WriteString(" ")
		}
		b.WriteString(t)
	}
	if i := strings.Index(src, "<body"); i >= 0 {
		src = src[i:]
	}
	skip := 0 // inside script, style, svg, footer, dialog
	var sel struct {
		on         bool
		name       string
		opts       []webOpt
		cur        *webOpt
		multiple   bool
		curLabelSB strings.Builder
	}
	var ta struct {
		on   bool
		name string
		sb   strings.Builder
	}
	var btn struct {
		on    bool
		attrs map[string]string
		sb    strings.Builder
	}
	var link struct {
		on   bool
		href string
		sb   strings.Builder
	}
	for len(src) > 0 {
		lt := strings.IndexByte(src, '<')
		if lt < 0 {
			lt = len(src)
		}
		chunk := src[:lt]
		src = src[lt:]
		if chunk != "" && skip == 0 {
			switch {
			case sel.on && sel.cur != nil:
				sel.curLabelSB.WriteString(chunk)
			case ta.on:
				ta.sb.WriteString(chunk)
			case btn.on:
				btn.sb.WriteString(chunk)
			case link.on:
				link.sb.WriteString(chunk)
			case sel.on:
			default:
				text(chunk)
			}
		}
		if src == "" {
			break
		}
		if strings.HasPrefix(src, "<!--") {
			end := strings.Index(src, "-->")
			if end < 0 {
				break
			}
			src = src[end+3:]
			continue
		}
		// find the end of the tag, respecting quoted attribute values
		end, q := -1, byte(0)
		for i := 1; i < len(src); i++ {
			c := src[i]
			if q != 0 {
				if c == q {
					q = 0
				}
				continue
			}
			if c == '"' || c == '\'' {
				q = c
			} else if c == '>' {
				end = i
				break
			}
		}
		if end < 0 {
			break
		}
		tag := src[1:end]
		src = src[end+1:]
		closing := strings.HasPrefix(tag, "/")
		tag = strings.TrimPrefix(tag, "/")
		words := strings.FieldsFunc(tag, func(r rune) bool { return r == ' ' || r == '\n' || r == '\t' || r == '/' })
		if len(words) == 0 {
			continue
		}
		name := strings.ToLower(words[0])
		rest := tag[strings.Index(tag, words[0])+len(words[0]):]
		at := parseAttrs(rest)
		switch name {
		case "script", "style", "svg", "dialog", "footer", "template":
			if closing {
				skip = max(skip-1, 0)
			} else if !strings.HasSuffix(tag, "/") {
				skip++
			}
			continue
		}
		if skip > 0 {
			continue
		}
		switch {
		case name == "select" && !closing:
			sel.on, sel.name, sel.opts, sel.cur, sel.multiple = true, at["name"], nil, nil, at["multiple"] != "" || strings.Contains(rest, "multiple")
		case name == "option" && !closing && sel.on:
			if sel.cur != nil {
				sel.cur.l = strings.TrimSpace(html.UnescapeString(sel.curLabelSB.String()))
				sel.opts = append(sel.opts, *sel.cur)
			}
			_, selected := at["selected"]
			sel.cur = &webOpt{v: at["value"], sel: selected}
			sel.curLabelSB.Reset()
		case name == "option" && closing && sel.cur != nil:
			sel.cur.l = strings.Join(strings.Fields(html.UnescapeString(sel.curLabelSB.String())), " ")
			if _, ok := at["value"]; !ok && sel.cur.v == "" {
				sel.cur.v = sel.cur.l
			}
			sel.opts = append(sel.opts, *sel.cur)
			sel.cur = nil
		case name == "select" && closing:
			if sel.cur != nil {
				sel.cur.l = strings.TrimSpace(sel.curLabelSB.String())
				sel.opts = append(sel.opts, *sel.cur)
			}
			var parts []string
			for i, o := range sel.opts {
				if i == 80 {
					parts = append(parts, fmt.Sprintf("… %d weitere", len(sel.opts)-80))
					break
				}
				mark := ""
				if o.sel {
					mark = "*"
				}
				l := o.l
				if l == "" {
					l = "(leer)"
				}
				parts = append(parts, fmt.Sprintf("%s%q=%s", mark, o.v, l))
			}
			kind := "Auswahl"
			if sel.multiple {
				kind = "Mehrfachauswahl"
			}
			text(fmt.Sprintf("[%s %s: %s]", kind, sel.name, strings.Join(parts, ", ")))
			sel.on, sel.cur = false, nil
		case name == "textarea" && !closing:
			ta.on, ta.name = true, at["name"]
			ta.sb.Reset()
		case name == "textarea" && closing:
			text(fmt.Sprintf("[Textfeld %s = %q]", ta.name, strings.TrimSpace(html.UnescapeString(ta.sb.String()))))
			ta.on = false
		case name == "button" && !closing:
			btn.on, btn.attrs = true, at
			btn.sb.Reset()
		case name == "button" && closing:
			l := strings.Join(strings.Fields(html.UnescapeString(btn.sb.String())), " ")
			if l == "" {
				l = btn.attrs["title"]
			}
			if btn.attrs["type"] != "button" {
				s := "[Knopf „" + l + "“"
				if btn.attrs["name"] != "" {
					s += fmt.Sprintf(" sendet %s=%q", btn.attrs["name"], btn.attrs["value"])
				}
				if f := btn.attrs["form"]; f != "" {
					s += " für Formular " + f
				}
				text(s + "]")
			}
			btn.on = false
		case name == "a" && !closing:
			link.on, link.href = true, at["href"]
			link.sb.Reset()
		case name == "a" && closing && link.on:
			l := strings.Join(strings.Fields(html.UnescapeString(link.sb.String())), " ")
			if l == "" {
				l = "Link"
			}
			if strings.HasPrefix(link.href, "/") {
				text(fmt.Sprintf("%s (→ %s)", l, link.href))
			} else {
				text(l)
			}
			link.on = false
		case name == "input" && !closing:
			n, typ, v := at["name"], strings.ToLower(at["type"]), at["value"]
			_, checked := at["checked"]
			_, req := at["required"]
			switch typ {
			case "hidden":
				if n != "_csrf" && n != "" {
					text(fmt.Sprintf("(versteckt %s=%q)", n, v))
				}
			case "checkbox", "radio":
				box := "☐"
				if checked {
					box = "☑"
				}
				if v == "" {
					v = "on"
				}
				text(fmt.Sprintf("[%s %s %s=%q]", typ, box, n, v))
			case "submit":
				text(fmt.Sprintf("[Knopf „%s“]", v))
			case "file":
				text("[Datei-Upload " + n + ": über MCP mit upload_file]")
			default:
				if typ == "" {
					typ = "text"
				}
				s := fmt.Sprintf("[Feld %s (%s) = %q", n, typ, v)
				if req {
					s += ", Pflicht"
				}
				text(s + "]")
			}
		case name == "form" && !closing:
			nl()
			m := strings.ToUpper(at["method"])
			if m == "" {
				m = "GET"
			}
			s := fmt.Sprintf("[Formular %s %s", m, at["action"])
			if at["id"] != "" {
				s += " id=" + at["id"]
			}
			if strings.Contains(at["enctype"], "multipart") {
				s += ", Datei-Upload: upload_file nutzen"
			}
			b.WriteString(s + "]\n")
		case name == "form" && closing:
			nl()
			b.WriteString("[Ende Formular]\n")
		case name == "h1" || name == "h2" || name == "h3" || name == "h4":
			nl()
			if !closing {
				b.WriteString(strings.Repeat("#", int(name[1]-'0')) + " ")
			}
		case name == "li" && !closing:
			nl()
			b.WriteString("- ")
		case name == "td" || name == "th":
			if !closing {
				b.WriteString(" | ")
			}
		case blockTags[name]:
			nl()
		}
	}
	// squeeze empty lines
	lines := strings.Split(b.String(), "\n")
	var out []string
	for _, l := range lines {
		l = strings.TrimRight(l, " ")
		if strings.TrimSpace(l) == "" || strings.TrimSpace(l) == "|" {
			continue
		}
		out = append(out, l)
	}
	return strings.Join(out, "\n")
}
