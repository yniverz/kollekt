// Copyright (C) 2026 yniverz
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"sync"
	"time"
)

// Weather comes from Open-Meteo (no API key). Requests are made by the server, never by the browser,
// results are cached, and the whole feature can be switched off with KOLLEKT_WEATHER=off.

var weatherOff bool

type WeatherDay struct {
	Date    string   `json:"date"`
	Label   string   `json:"label"`
	TMax    float64  `json:"tmax"`
	TMin    float64  `json:"tmin"`
	Rain    float64  `json:"rain"`
	RainPct float64  `json:"rainPct"`
	Gust    float64  `json:"gust"`
	Desc    string   `json:"desc"`
	Warn    []string `json:"warn,omitempty"`
}

type WeatherOut struct {
	Mode   string       `json:"mode"` // forecast, history, climate, none
	Note   string       `json:"note"`
	Days   []WeatherDay `json:"days"`
	Source string       `json:"source"`
}

type wcache struct {
	at  time.Time
	out *WeatherOut
}

var (
	wmu    sync.Mutex
	wstore = map[string]wcache{}
	whttp  = &http.Client{Timeout: 8 * time.Second}
)

var wmoText = map[int]string{
	0: "klar", 1: "überwiegend klar", 2: "teils bewölkt", 3: "bedeckt", 45: "Nebel", 48: "Reifnebel",
	51: "leichter Nieselregen", 53: "Nieselregen", 55: "starker Nieselregen", 61: "leichter Regen", 63: "Regen", 65: "starker Regen",
	66: "gefrierender Regen", 67: "starker gefrierender Regen", 71: "leichter Schneefall", 73: "Schneefall", 75: "starker Schneefall",
	80: "leichte Schauer", 81: "Schauer", 82: "heftige Schauer", 95: "Gewitter", 96: "Gewitter mit Hagel", 99: "Gewitter mit starkem Hagel",
}

func wmoDesc(code float64) string {
	if d, ok := wmoText[int(code)]; ok {
		return d
	}
	return "wechselhaft"
}

func weatherWarn(d WeatherDay, l Limits) []string {
	var w []string
	if d.RainPct >= l.RainPct || d.Rain >= l.RainMM {
		w = append(w, "Regen wahrscheinlich: Wetterschutz für Technik, Kasse und Bar planen")
	}
	if d.Gust >= l.GustKMH {
		w = append(w, "Starke Böen: Bühne, Zelte und Traversen laut Prüfbuch auf zulässige Windlast prüfen")
	}
	if d.TMax >= l.HeatC {
		w = append(w, "Hitze: Wasserstellen, Schatten und Sanitäts-Info einplanen")
	}
	if d.TMin <= l.ColdC {
		w = append(w, "Kalt: Heizung, Garderobe und warme Getränke einplanen")
	}
	return w
}

func getJSON(u string, v any) error {
	req, _ := http.NewRequest("GET", u, nil)
	req.Header.Set("User-Agent", "Kollekt (self-hosted event planner)")
	res, err := whttp.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return fmt.Errorf("status %d", res.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(v)
}

type omDaily struct {
	Daily struct {
		Time  []string   `json:"time"`
		Code  []*float64 `json:"weather_code"`
		TMax  []*float64 `json:"temperature_2m_max"`
		TMin  []*float64 `json:"temperature_2m_min"`
		Rain  []*float64 `json:"precipitation_sum"`
		RainP []*float64 `json:"precipitation_probability_max"`
		Gust  []*float64 `json:"wind_gusts_10m_max"`
	} `json:"daily"`
}

func val(p []*float64, i int) float64 {
	if i < len(p) && p[i] != nil {
		return *p[i]
	}
	return 0
}

func fetchDaily(base string, lat, lng float64, from, to string, withProb bool) (*omDaily, error) {
	q := url.Values{}
	q.Set("latitude", fmt.Sprintf("%.4f", lat))
	q.Set("longitude", fmt.Sprintf("%.4f", lng))
	daily := "weather_code,temperature_2m_max,temperature_2m_min,precipitation_sum,wind_gusts_10m_max"
	if withProb {
		daily += ",precipitation_probability_max"
	}
	q.Set("daily", daily)
	q.Set("timezone", "Europe/Berlin")
	q.Set("start_date", from)
	q.Set("end_date", to)
	var out omDaily
	if err := getJSON(base+"?"+q.Encode(), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func toDays(o *omDaily) []WeatherDay {
	var days []WeatherDay
	for i, t := range o.Daily.Time {
		if i >= len(o.Daily.TMax) || o.Daily.TMax[i] == nil { // no data (yet) for this day
			continue
		}
		d := WeatherDay{Date: t, Label: fmtDate(t), TMax: math.Round(val(o.Daily.TMax, i)), TMin: math.Round(val(o.Daily.TMin, i)), Rain: math.Round(val(o.Daily.Rain, i)*10) / 10,
			RainPct: val(o.Daily.RainP, i), Gust: math.Round(val(o.Daily.Gust, i)), Desc: wmoDesc(val(o.Daily.Code, i))}
		days = append(days, d)
	}
	return days
}

// eventWeather returns forecast, observed weather or a climate summary for the event's days.
func eventWeather(lat, lng float64, start, end string) *WeatherOut {
	key := fmt.Sprintf("%.2f,%.2f,%s,%s", lat, lng, start, end)
	wmu.Lock()
	if c, ok := wstore[key]; ok && time.Since(c.at) < 3*time.Hour {
		wmu.Unlock()
		return c.out
	}
	wmu.Unlock()
	out := computeWeather(lat, lng, start, end)
	wmu.Lock()
	wstore[key] = wcache{time.Now(), out}
	if len(wstore) > 200 {
		wstore = map[string]wcache{key: {time.Now(), out}}
	}
	wmu.Unlock()
	return out
}

func computeWeather(lat, lng float64, start, end string) *WeatherOut {
	s, ok := parseDT(start)
	if !ok {
		return &WeatherOut{Mode: "none", Note: "Kein Eventtermin gesetzt."}
	}
	e, ok := parseDT(end)
	if !ok || e.Before(s) {
		e = s
	}
	if e.Sub(s) > 6*24*time.Hour {
		e = s.AddDate(0, 0, 6)
	}
	from, to := s.Format("2006-01-02"), e.Format("2006-01-02")
	td := today()
	sd := time.Date(s.Year(), s.Month(), s.Day(), 0, 0, 0, 0, time.UTC)
	days := int(sd.Sub(td).Hours() / 24)
	switch {
	case days >= -7 && days <= 15 || (days < -7 && !e.Before(td)):
		o, err := fetchDaily("https://api.open-meteo.com/v1/forecast", lat, lng, from, to, true)
		if err != nil {
			return &WeatherOut{Mode: "none", Note: "Die Wetterdaten sind gerade nicht erreichbar."}
		}
		return &WeatherOut{Mode: "forecast", Days: toDays(o), Source: "Open-Meteo", Note: "Vorhersage, aktualisiert alle paar Stunden."}
	case days < 0:
		o, err := fetchDaily("https://archive-api.open-meteo.com/v1/archive", lat, lng, from, to, false)
		if err != nil {
			return &WeatherOut{Mode: "none", Note: "Die Wetterdaten sind gerade nicht erreichbar."}
		}
		return &WeatherOut{Mode: "history", Days: toDays(o), Source: "Open-Meteo", Note: "So war das Wetter an den Eventtagen."}
	}
	// too far ahead: climate of the same dates in earlier years
	var tmax, tmin, rain, gust, wet, n float64
	years := 0
	for y := 1; y <= 5; y++ {
		f, t := s.AddDate(-y, 0, 0).Format("2006-01-02"), e.AddDate(-y, 0, 0).Format("2006-01-02")
		o, err := fetchDaily("https://archive-api.open-meteo.com/v1/archive", lat, lng, f, t, false)
		if err != nil || len(o.Daily.Time) == 0 {
			continue
		}
		years++
		for i := range o.Daily.Time {
			tmax += val(o.Daily.TMax, i)
			tmin += val(o.Daily.TMin, i)
			r := val(o.Daily.Rain, i)
			rain += r
			if r >= 1 {
				wet++
			}
			g := val(o.Daily.Gust, i)
			if g > gust {
				gust = g
			}
			n++
		}
	}
	if n == 0 {
		return &WeatherOut{Mode: "none", Note: "Die Wetterdaten sind gerade nicht erreichbar."}
	}
	d := WeatherDay{Date: from, Label: "Ø der letzten " + fmt.Sprint(years) + " Jahre", TMax: math.Round(tmax / n), TMin: math.Round(tmin / n), Rain: math.Round(rain/n*10) / 10,
		RainPct: math.Round(wet / n * 100), Gust: math.Round(gust), Desc: "Klima um diesen Termin"}
	return &WeatherOut{Mode: "climate", Days: []WeatherDay{d}, Source: "Open-Meteo",
		Note: fmt.Sprintf("Eine Vorhersage gibt es erst 16 Tage vorher. Hier das typische Wetter an diesen Tagen (Regenanteil = Tage mit mindestens 1 mm, Böen = Höchstwert der letzten %d Jahre).", years)}
}

func (c *C) handleWeather(w http.ResponseWriter, r *http.Request) {
	if weatherOff {
		jsonOut(w, 200, &WeatherOut{Mode: "none", Note: "Wetterdaten sind in dieser Installation deaktiviert."})
		return
	}
	lat, lng, ok := c.eventLatLng()
	if !ok {
		for _, p := range c.Recs("siteplans") {
			if la, ln, ok2 := parseGeo(p.S("geo")); ok2 && p.S("mode") == "map" {
				lat, lng, ok = la, ln, true
				break
			}
		}
	}
	if !ok {
		jsonOut(w, 200, &WeatherOut{Mode: "none", Note: "Für die Wetterdaten fehlt die Position der Location. Setze bei der Location einen Pin."})
		return
	}
	if c.Event.Start == "" {
		jsonOut(w, 200, &WeatherOut{Mode: "none", Note: "Für die Wetterdaten fehlt der Eventtermin."})
		return
	}
	out := *eventWeather(lat, lng, c.Event.Start, c.Event.End) // copy: the cached value stays untouched
	lim := c.limits()
	out.Days = append([]WeatherDay(nil), out.Days...)
	for i := range out.Days {
		out.Days[i].Warn = weatherWarn(out.Days[i], lim)
	}
	jsonOut(w, 200, &out)
}
