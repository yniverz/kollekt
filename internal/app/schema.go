// Copyright (C) 2026 yniverz
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"math"
	"strconv"
	"strings"
)

type FieldType string

const (
	TText     FieldType = "text"
	TTextarea FieldType = "textarea"
	TNumber   FieldType = "number"
	TMoney    FieldType = "money"
	TPercent  FieldType = "percent"
	TDate     FieldType = "date"
	TDateTime FieldType = "datetime"
	TSelect   FieldType = "select"
	TBool     FieldType = "bool"
	TRef      FieldType = "ref"
	TURL      FieldType = "url"
	TColor    FieldType = "color"
	TRecipe   FieldType = "recipe"
)

type Opt struct {
	V, L, Color string
}

func O(v, l, color string) Opt { return Opt{v, l, color} }

type Field struct {
	Key, Label string
	Type       FieldType
	Opts       []Opt
	Ref        string // module key for TRef
	Required   bool
	Help       string
	Suffix     string
	Default    string
	Wide       bool
	InList     bool
	Quick      bool     // select editable directly in list
	Fin        bool     // cost field, hidden without finance permission
	Datalist   []string // static suggestions for TText
	DatalistFn string   // dynamic suggestions: "field values of this module key"
	Sum        bool
	BlankLabel string
}

func F(key, label string, t FieldType) Field { return Field{Key: key, Label: label, Type: t} }

func (f Field) Req() Field             { f.Required = true; return f }
func (f Field) List() Field            { f.InList = true; return f }
func (f Field) Wide_() Field           { f.Wide = true; return f }
func (f Field) Fin_() Field            { f.Fin = true; return f }
func (f Field) Sum_() Field            { f.Sum = true; return f }
func (f Field) Quick_() Field          { f.Quick = true; f.InList = true; return f }
func (f Field) Blank(s string) Field   { f.BlankLabel = s; return f }
func (f Field) Hint(s string) Field    { f.Help = s; return f }
func (f Field) Unit(s string) Field    { f.Suffix = s; return f }
func (f Field) Def(s string) Field     { f.Default = s; return f }
func (f Field) Options(o ...Opt) Field { f.Opts = o; return f }
func (f Field) Of(mod string) Field    { f.Ref = mod; return f }
func (f Field) Suggest(s ...string) Field {
	f.Datalist = s
	return f
}

// Virtual (computed) column.
type VField struct {
	Key, Label string
	Type       FieldType // money, number, text, percent
	Fn         func(c *C, r *Rec) string
	Sum        bool
}

type Action struct {
	Key, Label string
	Fn         func(c *C, r *Rec) string // returns flash message
	Level      int
}

type Module struct {
	Key, Name, Singular, Icon, Desc string
	Global                          bool   // records with event_id = 0
	Core                            bool   // cannot be disabled in an event
	Default                         bool   // enabled for blank events
	Hidden                          bool   // sub-module, not in nav
	Perm                            string // permission key (defaults to Key)
	Title                           string
	Fields                          []Field
	Virt                            []VField
	Sort                            string // field key, "-key" for descending
	Group                           string // field key to group list by
	Filters                         []string
	AreaField                       string
	DoneField                       string
	DoneVals                        []string
	MultiCreate                     bool
	Actions                         []Action
	Page                            string // custom page route suffix instead of generic list (relative /e/{id}/<Page>)
	Extra                           func(c *C) any
	ExtraTpl                        string
	ExtraPos                        string // "top" (default) or "bottom"
	NoList                          bool   // list shows only Extra
	NoAttach                        bool   // no file attachments
	Empty                           string
	Order                           int
}

func (m *Module) PermKey() string {
	if m.Perm != "" {
		return m.Perm
	}
	return m.Key
}

func (m *Module) Field(key string) *Field {
	for i := range m.Fields {
		if m.Fields[i].Key == key {
			return &m.Fields[i]
		}
	}
	return nil
}

// ---------- number helpers ----------

func parseNum(s string) float64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	s = strings.ReplaceAll(s, " ", "")
	if strings.Contains(s, ",") {
		s = strings.ReplaceAll(s, ".", "")
		s = strings.ReplaceAll(s, ",", ".")
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		return 0
	}
	return f
}

func validNum(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return true
	}
	s = strings.ReplaceAll(s, " ", "")
	if strings.Contains(s, ",") {
		s = strings.ReplaceAll(strings.ReplaceAll(s, ".", ""), ",", ".")
	}
	f, err := strconv.ParseFloat(s, 64)
	return err == nil && !math.IsNaN(f) && !math.IsInf(f, 0)
}

func numStr(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}

func round2(f float64) float64 { return math.Round(f*100) / 100 }
