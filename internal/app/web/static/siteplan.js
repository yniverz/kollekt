// Copyright (C) 2026 yniverz
// SPDX-License-Identifier: AGPL-3.0-only
(function () {
  'use strict';
  var root = document.getElementById('siteplan');
  if (!root || !window.L) return;
  window.KSite = true;
  var API = root.getAttribute('data-api');
  var csrf = (document.querySelector('meta[name="csrf"]') || {}).content || '';
  var el = window.KMaps.el;
  var D = null, map = null, mode = 'image', W = 1000, H = 1000;
  var layers = {}, selected = null, tool = 'select', draft = null, handles = [], hidden = {};
  var side = document.getElementById('sp-side'), list = document.getElementById('sp-list'), form = document.getElementById('sp-form'),
      msg = document.getElementById('sp-msg'), legend = document.getElementById('sp-legend'), drawArea = document.getElementById('sp-area'),
      hint = document.getElementById('sp-hint');

  function api(method, url, body) {
    return fetch(url, { method: method, credentials: 'same-origin', headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrf }, body: body ? JSON.stringify(body) : undefined })
      .then(function (r) { return r.json().catch(function () { return {}; }).then(function (j) { if (!r.ok) throw new Error(j.error || 'Fehler ' + r.status); return j; }); });
  }
  function say(t, bad) { msg.textContent = t || ''; msg.className = 'small ' + (bad ? 'bad' : 'muted'); }

  // stored coordinates <-> leaflet coordinates
  function toLL(p) { return mode === 'map' ? L.latLng(p[0], p[1]) : L.latLng((1 - p[1]) * H, p[0] * W); }
  function fromLL(ll) { return mode === 'map' ? [ll.lat, ll.lng] : [ll.lng / W, 1 - ll.lat / H]; }
  function kindOf(key) { return D.kinds.filter(function (k) { return k.key === key; })[0] || D.kinds[D.kinds.length - 1]; }
  function itemById(id) { return D.items.filter(function (i) { return i.id === id; })[0]; }

  // ---- measurements ----
  function metersXY(pts) { // pts: stored coords -> local meters
    if (mode === 'map') {
      var lat0 = pts[0][0] * Math.PI / 180, R = 6371008.8;
      return pts.map(function (p) { return [R * Math.cos(lat0) * (p[1] - pts[0][1]) * Math.PI / 180, R * (p[0] - pts[0][0]) * Math.PI / 180]; });
    }
    if (!D.plan.widthM) return null;
    var s = D.plan.widthM / W;
    return pts.map(function (p) { return [p[0] * W * s, p[1] * H * s]; });
  }
  function areaM2(g) {
    if (!g || g.t !== 'poly') return null;
    var m = metersXY(g.p); if (!m) return null;
    var a = 0, i;
    for (i = 0; i < m.length; i++) { var j = (i + 1) % m.length; a += m[i][0] * m[j][1] - m[j][0] * m[i][1]; }
    return Math.abs(a) / 2;
  }
  function measure(g) {
    if (!g || g.t === 'point') return '';
    var m = metersXY(g.p);
    if (!m) return 'Maßstab fehlt: trage unter „Plan-Einstellungen“ die Breite des Plans in Metern ein, dann werden Flächen und Längen berechnet.';
    var len = 0, i;
    for (i = 1; i < m.length; i++) len += Math.hypot(m[i][0] - m[i - 1][0], m[i][1] - m[i - 1][1]);
    if (g.t === 'line') return 'Länge ≈ ' + Math.round(len) + ' m';
    len += Math.hypot(m[0][0] - m[m.length - 1][0], m[0][1] - m[m.length - 1][1]);
    var a = 0;
    for (i = 0; i < m.length; i++) { var j = (i + 1) % m.length; a += m[i][0] * m[j][1] - m[j][0] * m[i][1]; }
    return 'Fläche ≈ ' + Math.round(Math.abs(a) / 2).toLocaleString('de-DE') + ' m² · Umfang ≈ ' + Math.round(len) + ' m';
  }

  // ---- drawing layers ----
  function styleFor(it, sel) {
    var k = kindOf(it.kind);
    return { color: it.color, weight: sel ? 4 : 2, fillColor: it.color, fillOpacity: sel ? 0.45 : 0.28, dashArray: it.kind === 'fence' ? '2 6' : (it.kind === 'route' ? '10 6' : null) };
  }
  function labelEl(it) {
    var d = el('div', 'sp-pin'); d.style.background = it.color;
    d.appendChild(el('span', null, it.title)); return d;
  }
  function drawItem(it) {
    removeLayer(it.id);
    if (hidden[it.area || 0] || !it.geom) return;
    var g = it.geom, lay;
    if (g.t === 'point') {
      lay = L.marker(toLL(g.p[0]), { icon: L.divIcon({ className: 'sp-pin-wrap', html: labelEl(it), iconSize: null, iconAnchor: [8, 8] }), keyboard: false });
    } else if (g.t === 'poly') {
      lay = L.polygon(g.p.map(toLL), styleFor(it, false)); lay.bindTooltip(el('span', null, it.title), { permanent: true, direction: 'center', className: 'sp-label' });
    } else {
      lay = L.polyline(g.p.map(toLL), styleFor(it, false)); lay.bindTooltip(el('span', null, it.title), { permanent: true, direction: 'center', className: 'sp-label' });
    }
    lay.on('click', function (e) { if (tool === 'select') { L.DomEvent.stopPropagation(e); select(it.id); } });
    lay.addTo(map); layers[it.id] = lay;
  }
  function removeLayer(id) { if (layers[id]) { map.removeLayer(layers[id]); delete layers[id]; } }
  function clearHandles() { handles.forEach(function (h) { map.removeLayer(h); }); handles = []; }

  function persistGeom(it) {
    return save(it, true);
  }
  function showHandles(it) {
    clearHandles();
    var lay = layers[it.id];
    if (!lay || !it.editable) return;
    var g = it.geom;
    if (g.t === 'point') { // drag the pin itself
      lay.dragging && lay.dragging.enable();
      lay.off('dragend'); lay.on('dragend', function () { g.p[0] = fromLL(lay.getLatLng()); persistGeom(it); });
      return;
    }
    function relayer() { lay.setLatLngs(g.p.map(toLL)); }
    g.p.forEach(function (p, idx) {
      var h = L.marker(toLL(p), { draggable: true, icon: L.divIcon({ className: 'sp-handle', iconSize: [14, 14] }), zIndexOffset: 1000 }).addTo(map);
      h.on('drag', function () { g.p[idx] = fromLL(h.getLatLng()); relayer(); });
      h.on('dragend', function () { persistGeom(it); });
      h.on('contextmenu', function () {
        var min = g.t === 'poly' ? 3 : 2;
        if (g.p.length > min) { g.p.splice(idx, 1); relayer(); persistGeom(it).then(function () { showHandles(it); }); }
      });
      handles.push(h);
    });
    // midpoints add a vertex
    var n = g.p.length, segs = g.t === 'poly' ? n : n - 1;
    for (var i = 0; i < segs; i++) {
      (function (i) {
        var a = g.p[i], b = g.p[(i + 1) % n], mid = [(a[0] + b[0]) / 2, (a[1] + b[1]) / 2];
        var h = L.marker(toLL(mid), { icon: L.divIcon({ className: 'sp-handle sp-mid', iconSize: [10, 10] }), zIndexOffset: 900 }).addTo(map);
        h.on('click', function () { g.p.splice(i + 1, 0, mid); relayer(); persistGeom(it).then(function () { showHandles(it); }); });
        handles.push(h);
      })(i);
    }
    // move whole shape
    var c = g.p.reduce(function (s, p) { return [s[0] + p[0] / g.p.length, s[1] + p[1] / g.p.length]; }, [0, 0]);
    var mv = L.marker(toLL(c), { draggable: true, icon: L.divIcon({ className: 'sp-handle sp-move', html: '✥', iconSize: [22, 22] }), zIndexOffset: 1100, title: 'Verschieben' }).addTo(map);
    var last = null;
    mv.on('dragstart', function () { last = fromLL(mv.getLatLng()); });
    mv.on('drag', function () {
      var cur = fromLL(mv.getLatLng()), dx = cur[0] - last[0], dy = cur[1] - last[1]; last = cur;
      g.p = g.p.map(function (p) { return [p[0] + dx, p[1] + dy]; }); relayer();
      handles.forEach(function (x) { if (x !== mv && x._kidx != null) x.setLatLng(toLL(g.p[x._kidx])); });
    });
    mv.on('dragend', function () { persistGeom(it).then(function () { showHandles(it); }); });
    handles.push(mv);
    g.p.forEach(function (_, idx) { if (handles[idx]) handles[idx]._kidx = idx; });
  }

  function select(id) {
    if (selected && layers[selected] && layers[selected].setStyle) { var prev = itemById(selected); if (prev) layers[selected].setStyle(styleFor(prev, false)); }
    if (selected && layers[selected] && layers[selected].dragging) layers[selected].dragging.disable();
    clearHandles();
    selected = id;
    var it = id ? itemById(id) : null;
    if (it && layers[id]) {
      if (layers[id].setStyle) layers[id].setStyle(styleFor(it, true));
      showHandles(it);
    }
    renderForm(); renderList();
  }

  // ---- side panel ----
  function option(sel, v, t, cur) { var o = el('option', null, t); o.value = v; if (String(v) === String(cur)) o.selected = true; sel.appendChild(o); }
  function renderForm() {
    form.textContent = '';
    var it = selected ? itemById(selected) : null;
    if (!it) { form.appendChild(el('p', 'muted small', D.canEdit ? 'Wähle ein Objekt auf dem Plan oder in der Liste, oder zeichne ein neues mit den Werkzeugen oben.' : 'Wähle ein Objekt, um Details zu sehen.')); return; }
    var ro = !it.editable;
    function field(label, node) { var f = el('div', 'field'); var l = el('label', 'lbl', label); f.append(l, node); form.appendChild(f); return node; }
    var title = el('input'); title.value = it.title; title.disabled = ro; title.maxLength = 120; field('Bezeichnung', title);
    var kind = el('select'); D.kinds.forEach(function (k) { option(kind, k.key, k.label, it.kind); }); kind.disabled = ro; field('Art', kind);
    var area = el('select'); if (!D.areas.some(function (a) { return a.editable; }) || !ro) { /* keep */ }
    option(area, 0, 'Ohne Bereich', it.area);
    D.areas.forEach(function (a) { if (a.editable || a.id === it.area) option(area, a.id, a.name, it.area); });
    area.disabled = ro; field('Bereich', area);
    var notes = el('textarea'); notes.value = it.notes; notes.disabled = ro; notes.rows = 3; field('Notizen', notes);
    var width = null;
    if (it.kind === 'route' || it.kind === 'safety') {
      width = el('input'); width.type = 'number'; width.step = 'any'; width.min = '0'; width.max = '200'; width.value = it.width || ''; width.disabled = ro; width.placeholder = 'z. B. 3';
      field('Nutzbare Breite (m)', width);
    }
    var m = measure(it.geom); if (m) form.appendChild(el('p', 'small muted', m));
    if (it.gear && it.gear.length) form.appendChild(el('p', 'small', 'Material hier: ' + it.gear.join(', ')));
    if (ro) { form.appendChild(el('p', 'small muted', 'Dieses Objekt gehört zu einem anderen Bereich und ist für dich nur lesbar.')); return; }
    var row = el('div', 'actions');
    var ok = el('button', 'btn primary sm', 'Speichern'); ok.type = 'button';
    var del = el('button', 'btn danger sm', 'Löschen'); del.type = 'button';
    ok.onclick = function () {
      it.title = title.value; it.kind = kind.value; it.area = parseInt(area.value, 10) || 0; it.notes = notes.value;
      it.width = width ? (parseFloat(String(width.value).replace(',', '.')) || 0) : (it.width || 0);
      save(it, false).then(function () { say('Gespeichert.'); });
    };
    del.onclick = function () {
      if (!confirm('„' + it.title + '“ wirklich löschen?')) return;
      api('POST', API.replace(/\/\d+$/, '') + '/items/' + it.id + '/delete').then(function () {
        removeLayer(it.id); clearHandles(); D.items = D.items.filter(function (x) { return x.id !== it.id; }); selected = null; renderForm(); renderList(); renderCheck(); say('Gelöscht.');
      }).catch(function (e) { say(e.message, true); });
    };
    row.append(ok, del); form.appendChild(row);
  }
  function renderList() {
    list.textContent = '';
    var groups = {};
    D.items.slice().sort(function (a, b) { return a.title.localeCompare(b.title, 'de'); }).forEach(function (it) {
      var k = it.areaName || 'Ohne Bereich'; (groups[k] = groups[k] || []).push(it);
    });
    Object.keys(groups).sort().forEach(function (g) {
      list.appendChild(el('div', 'sp-grp', g));
      groups[g].forEach(function (it) {
        var b = el('button', 'sp-row' + (it.id === selected ? ' on' : ''));
        b.type = 'button';
        var dot = el('i', 'sp-dot'); dot.style.background = it.color;
        b.append(dot, el('span', null, it.title), el('small', 'muted', kindOf(it.kind).label));
        b.onclick = function () { select(it.id); var l = layers[it.id]; if (l) { if (l.getBounds) map.fitBounds(l.getBounds(), { maxZoom: map.getZoom(), padding: [60, 60] }); else map.panTo(l.getLatLng()); } };
        list.appendChild(b);
      });
    });
    if (!D.items.length) list.appendChild(el('p', 'muted small', 'Noch keine Objekte.'));
  }
  function renderLegend() {
    legend.textContent = '';
    var seen = {};
    D.areas.forEach(function (a) { seen[a.id] = a; });
    var all = D.areas.slice(); all.push({ id: 0, name: 'Ohne Bereich', color: '#8b867a' });
    all.forEach(function (a) {
      if (!D.items.some(function (i) { return (i.area || 0) === a.id; })) return;
      var l = el('label', 'sp-leg'); var c = el('input'); c.type = 'checkbox'; c.checked = !hidden[a.id];
      c.onchange = function () { hidden[a.id] = !c.checked; D.items.forEach(drawItem); if (selected) select(selected); };
      var dot = el('i', 'sp-dot'); dot.style.background = a.color;
      l.append(c, dot, el('span', null, a.name)); legend.appendChild(l);
    });
  }

  // ---- density & escape routes (orientation only) ----
  function renderCheck() {
    var box = document.getElementById('sp-check'); if (!box) return;
    box.textContent = '';
    box.appendChild(el('h2', null, 'Dichte und Fluchtwege'));
    var visitors = 0, known = true, routes = 0, routeN = 0, noWidth = 0;
    D.items.forEach(function (it) {
      if (it.kind === 'dance' || it.kind === 'camp') { var a = areaM2(it.geom); if (a == null) known = false; else visitors += a; }
      if (it.kind === 'route') { routeN++; if (it.width > 0) routes += it.width; else noWidth++; }
    });
    var p = D.plan, g = p.guests, rows = [];
    function line(text, cls) { rows.push(el('p', 'small ' + (cls || ''), text)); }
    if (!known) line('Für Flächen fehlt der Maßstab: trage in den Plan-Einstellungen die Breite des Plans in Metern ein.', 'muted');
    else if (!visitors) line('Zeichne Tanzfläche oder Chillout-Bereiche ein (Art „Tanzfläche / Publikum“ oder „Chillout / Rückzug“), dann wird die Besucherfläche gerechnet.', 'muted');
    else {
      var maxP = Math.floor(visitors * p.density);
      line('Besucherfläche ≈ ' + Math.round(visitors).toLocaleString('de-DE') + ' m² · bei ' + String(p.density).replace('.', ',') + ' Personen/m² rechnerisch ' + maxP.toLocaleString('de-DE') + ' Personen.');
      if (g) line('Geplante Gäste (Kalkulation): ' + g.toLocaleString('de-DE') + (g > maxP ? ' → mehr als die Fläche hergibt.' : ' → passt rechnerisch.'), g > maxP ? 'bad' : 'good');
      if (p.capacity && g > p.capacity) line('Geplante Gäste liegen über der Kapazität der Location (' + p.capacity.toLocaleString('de-DE') + ').', 'bad');
    }
    if (g && routeN) {
      var need = g / 100 * p.escapeW;
      line('Fluchtwege: ' + String(Math.round(routes * 10) / 10).replace('.', ',') + ' m nutzbare Breite eingezeichnet, bei ' + g.toLocaleString('de-DE') + ' Gästen rechnerisch ≈ ' + String(Math.round(need * 10) / 10).replace('.', ',') + ' m nötig.' + (noWidth ? ' ' + noWidth + ' Fluchtweg(e) ohne Breite.' : ''), (routes < need) ? 'bad' : 'good');
    } else if (g && !routeN) line('Noch kein Fluchtweg eingezeichnet (Art „Fluchtweg / Weg“, mit nutzbarer Breite).', 'warn-t');
    line('Orientierung ohne Rechtswirkung. Maßgeblich sind Bescheid und Vorgaben von Bauordnungsbehörde, Feuerwehr und Ordnungsamt.', 'muted');
    rows.forEach(function (r) { box.appendChild(r); });
  }

  // ---- saving ----
  function save(it, geomOnly) {
    return api('POST', API + '/items', { id: it.id || 0, title: it.title, kind: it.kind, area: it.area || 0, geom: it.geom, notes: it.notes || '', width: it.width || 0 })
      .then(function (r) {
        var cur = itemById(it.id);
        if (cur) { Object.assign(cur, r); } else { D.items.push(r); }
        drawItem(cur || r); renderList(); renderLegend(); renderCheck();
        if (!geomOnly) select(r.id); else { var s = itemById(r.id); if (s && layers[r.id] && layers[r.id].setStyle && r.id === selected) layers[r.id].setStyle(styleFor(s, true)); renderForm(); }
        return r;
      })
      .catch(function (e) { say(e.message, true); throw e; });
  }
  function create(kindKey, geom) {
    var k = kindOf(kindKey);
    var it = { id: 0, title: k.label.split(' / ')[0], kind: k.key, area: parseInt(drawArea.value, 10) || 0, geom: geom, notes: '' };
    return save(it, false).then(function (r) { var t = form.querySelector('input'); if (t) { t.focus(); t.select(); } return r; }).catch(function () {});
  }

  // ---- tools ----
  function setTool(t) {
    tool = t; cancelDraft();
    document.querySelectorAll('[data-sptool]').forEach(function (b) { b.classList.toggle('on', b.getAttribute('data-sptool') === t); });
    map.getContainer().style.cursor = t === 'select' ? '' : 'crosshair';
    hint.textContent = {
      select: 'Objekt anklicken zum Bearbeiten. Ziehen: Ecken verschieben, Mittelpunkte anklicken: Ecke hinzufügen, Rechtsklick auf eine Ecke: entfernen.',
      point: 'Klicke auf den Plan, um einen Punkt zu setzen.',
      rect: 'Klicke zwei gegenüberliegende Ecken.',
      poly: 'Klicke die Ecken nacheinander. Doppelklick oder Enter beendet, Esc bricht ab.',
      line: 'Klicke die Punkte nacheinander. Doppelklick oder Enter beendet, Esc bricht ab.'
    }[t] || '';
    if (t === 'poly' || t === 'line') map.doubleClickZoom.disable(); else map.doubleClickZoom.enable();
  }
  function cancelDraft() { if (draft) { map.removeLayer(draft.preview); draft = null; } }
  function kindForTool() { var k = document.getElementById('sp-kind').value; return k; }
  function finishDraft() {
    if (!draft) return;
    var pts = draft.pts, t = draft.t, min = t === 'poly' ? 3 : 2;
    cancelDraft();
    if (pts.length < min) return;
    create(kindForTool(), { t: t, p: pts }).then(function () { setTool('select'); });
  }
  function onMapClick(e) {
    if (!D.canEdit || tool === 'select') { if (tool === 'select') select(null); return; }
    var p = fromLL(e.latlng);
    if (tool === 'point') { create(kindForTool(), { t: 'point', p: [p] }).then(function () { setTool('select'); }); return; }
    if (tool === 'rect') {
      if (!draft) { draft = { t: 'poly', pts: [p], rect: true, preview: L.rectangle([e.latlng, e.latlng], { color: '#b5441f', weight: 2, dashArray: '4 4' }).addTo(map) }; return; }
      var a = draft.pts[0], pts = [a, [p[0], a[1]], p, [a[0], p[1]]];
      cancelDraft(); create(kindForTool(), { t: 'poly', p: pts }).then(function () { setTool('select'); });
      return;
    }
    if (!draft) draft = { t: tool === 'poly' ? 'poly' : 'line', pts: [], preview: L.polyline([], { color: '#b5441f', weight: 2, dashArray: '4 4' }).addTo(map) };
    draft.pts.push(p); draft.preview.setLatLngs(draft.pts.map(toLL));
  }
  function onMove(e) {
    if (!draft) return;
    if (draft.rect) draft.preview.setBounds([toLL(draft.pts[0]), e.latlng]);
    else draft.preview.setLatLngs(draft.pts.map(toLL).concat([e.latlng]));
  }

  // ---- start ----
  function start(data) {
    D = data; mode = D.plan.mode;
    D.items = D.items || []; D.areas = D.areas || []; D.kinds = D.kinds || [];
    var mapEl = document.getElementById('sp-map');
    if (mode === 'map') {
      if (!window.KMaps.enabled) { mapEl.textContent = 'Karten sind in dieser Installation deaktiviert.'; return; }
      map = L.map(mapEl, { doubleClickZoom: true }).setView([D.plan.lat, D.plan.lng], D.plan.zoom);
      window.KMaps.baseLayer(map);
      go();
    } else {
      var img = new Image();
      img.onload = function () {
        W = 1000; H = 1000 * img.naturalHeight / img.naturalWidth;
        map = L.map(mapEl, { crs: L.CRS.Simple, minZoom: -4, maxZoom: 3, zoomSnap: 0.25, attributionControl: false });
        var b = [[0, 0], [H, W]];
        L.imageOverlay(D.plan.image, b).addTo(map);
        map.fitBounds(b); map.setMaxBounds([[-H * 0.5, -W * 0.5], [H * 1.5, W * 1.5]]);
        go();
      };
      img.onerror = function () { mapEl.textContent = 'Das Plan-Bild konnte nicht geladen werden.'; };
      img.src = D.plan.image;
    }
  }
  function go() {
    window.KMaps.wheelGuard(map); // page scrolls normally, Cmd/Ctrl + wheel zooms
    D.items.forEach(function (it) { if (typeof it.geom === 'string') it.geom = JSON.parse(it.geom); });
    D.items.forEach(drawItem);
    map.on('click', onMapClick); map.on('mousemove', onMove); map.on('dblclick', function () { if (draft && (tool === 'poly' || tool === 'line')) finishDraft(); });
    document.addEventListener('keydown', function (e) {
      if (e.target.matches('input, textarea, select')) return;
      if (e.key === 'Escape') { cancelDraft(); setTool('select'); }
      if (e.key === 'Enter' && draft) finishDraft();
    });
    document.querySelectorAll('[data-sptool]').forEach(function (b) { b.onclick = function () { setTool(b.getAttribute('data-sptool')); }; });
    var kindSel = document.getElementById('sp-kind');
    if (kindSel && drawArea) { // the drawing toolbar only exists for people who may edit
      D.kinds.forEach(function (k) { option(kindSel, k.key, k.label, 'zone'); });
      kindSel.onchange = function () { var k = kindOf(kindSel.value); if (k.shape === 'point') setTool('point'); else if (k.shape === 'line') setTool('line'); else if (tool === 'point' || tool === 'line') setTool('poly'); };
      var editableAreas = D.areas.filter(function (a) { return a.editable; });
      option(drawArea, 0, 'Ohne Bereich', 0);
      editableAreas.forEach(function (a) { option(drawArea, a.id, a.name, editableAreas[0].id); });
      if (editableAreas.length) drawArea.value = editableAreas[0].id;

    }
    var saveView = document.getElementById('sp-saveview');
    if (saveView) saveView.onclick = function () {
      var c = map.getCenter();
      api('POST', API + '/view', { lat: c.lat, lng: c.lng, zoom: map.getZoom() }).then(function () { say('Ausschnitt als Standard gespeichert.'); }).catch(function (e) { say(e.message, true); });
    };
    var pr = document.getElementById('sp-print'); if (pr) pr.onclick = function () { window.print(); };
    setTool('select'); renderLegend(); renderList(); renderForm(); renderCheck();
    setTimeout(function () { map.invalidateSize(); }, 100);
  }
  api('GET', API + '/data').then(start).catch(function (e) { document.getElementById('sp-map').textContent = e.message; });
})();
