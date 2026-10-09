// Copyright (C) 2026 yniverz
// SPDX-License-Identifier: AGPL-3.0-only

package app

import "net/http"

// Limits are the planning thresholds of an event. All of them are orientation values that
// the organisers can adjust; none of them has legal effect.
type Limits struct {
	RainPct      float64 `json:"rain_pct"`      // warn from this rain probability (%)
	RainMM       float64 `json:"rain_mm"`       // ... or this daily amount (mm)
	GustKMH      float64 `json:"gust_kmh"`      // warn from these gusts (km/h)
	HeatC        float64 `json:"heat_c"`        // warn from this daily maximum
	ColdC        float64 `json:"cold_c"`        // warn up to this daily minimum
	Density      float64 `json:"density"`       // persons per m² of visitor area
	EscapeW      float64 `json:"escape_w"`      // metres of escape route width per 100 persons
	CosPhi       float64 `json:"cos_phi"`       // power factor for current estimates
	PowerWarn    float64 `json:"power_warn"`    // "tight" from this utilisation (%)
	PowerReserve float64 `json:"power_reserve"` // reserve on top of the total load (%)
}

func defaultLimits() Limits {
	return Limits{RainPct: 60, RainMM: 5, GustKMH: 50, HeatC: 30, ColdC: 3, Density: 2, EscapeW: 0.2, CosPhi: 0.9, PowerWarn: 80, PowerReserve: 20}
}

func (l Limits) sane() Limits {
	d := defaultLimits()
	in := func(v, lo, hi, def float64) float64 {
		if v < lo || v > hi || v != v {
			return def
		}
		return v
	}
	return Limits{
		RainPct: in(l.RainPct, 1, 100, d.RainPct), RainMM: in(l.RainMM, 0.1, 500, d.RainMM), GustKMH: in(l.GustKMH, 5, 250, d.GustKMH),
		HeatC: in(l.HeatC, 15, 55, d.HeatC), ColdC: in(l.ColdC, -40, 25, d.ColdC),
		Density: in(l.Density, 0.1, 10, d.Density), EscapeW: in(l.EscapeW, 0.01, 5, d.EscapeW),
		CosPhi: in(l.CosPhi, 0.5, 1, d.CosPhi), PowerWarn: in(l.PowerWarn, 10, 99, d.PowerWarn), PowerReserve: in(l.PowerReserve, 0, 80, d.PowerReserve),
	}
}

func (c *C) limits() Limits {
	var l Limits
	if c.Event != nil && c.Event.getSetting("limits", &l) {
		return l.sane()
	}
	return defaultLimits()
}

func (c *C) handleLimitsSave(r *http.Request) {
	l := Limits{
		RainPct: parseNum(r.FormValue("rain_pct")), RainMM: parseNum(r.FormValue("rain_mm")), GustKMH: parseNum(r.FormValue("gust_kmh")),
		HeatC: parseNum(r.FormValue("heat_c")), ColdC: parseNum(r.FormValue("cold_c")), Density: parseNum(r.FormValue("density")),
		EscapeW: parseNum(r.FormValue("escape_w")), CosPhi: parseNum(r.FormValue("cos_phi")), PowerWarn: parseNum(r.FormValue("power_warn")),
		PowerReserve: parseNum(r.FormValue("power_reserve")),
	}
	if r.FormValue("rain_pct") == "" { // form not submitted with these fields
		return
	}
	c.Event.setting("limits", l.sane())
}
