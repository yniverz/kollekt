// Copyright (C) 2026 yniverz
// SPDX-License-Identifier: AGPL-3.0-only
(function () {
  'use strict';
  function meta(n) { var m = document.querySelector('meta[name="' + n + '"]'); return m ? m.content : ''; }
  var enabled = meta('kmaps') === '1';
  var tileURL = meta('ktile'), attrib = meta('kattrib'), geocoder = meta('kgeocoder');
  if (window.L) L.Icon.Default.prototype.options.imagePath = '/static/vendor/leaflet/images/';

  var satURL = meta('ksat'), satAttr = meta('ksatattr'), labelsURL = meta('klabels');
  function remembered() { try { return localStorage.getItem('kollekt.maplayer') || ''; } catch (e) { return ''; } }
  function remember(v) { try { localStorage.setItem('kollekt.maplayer', v); } catch (e) {} }
  // Base maps: street map, satellite, satellite with labels. The choice is remembered for all maps.
  function baseLayer(map) {
    if (!enabled || !tileURL) return;
    var street = L.tileLayer(tileURL, { maxNativeZoom: 19, maxZoom: 21, attribution: attrib || '' });
    if (!satURL) { street.addTo(map); return; }
    var sat = function () { return L.tileLayer(satURL, { maxNativeZoom: 18, maxZoom: 21, attribution: satAttr || '' }); };
    var layers = { 'Karte': street, 'Satellit': sat() };
    if (labelsURL) layers['Satellit mit Beschriftung'] = L.layerGroup([sat(), L.tileLayer(labelsURL, { maxNativeZoom: 17, maxZoom: 21 })]);
    (layers[remembered()] || street).addTo(map);
    L.control.layers(layers, null, { position: 'topright', collapsed: true }).addTo(map);
    map.on('baselayerchange', function (e) { remember(e.name); });
  }
  // Plain wheel scrolls the page, Cmd/Ctrl + wheel (and trackpad pinch) zooms the map.
  var isMac = /Mac|iPhone|iPad/.test(navigator.platform || navigator.userAgent || '');
  function wheelGuard(map) {
    var box = map.getContainer();
    map.options.wheelPxPerZoomLevel = 90;
    var hint = el('div', 'kmap-hint', (isMac ? '⌘' : 'Strg') + ' + Scrollen zum Zoomen');
    box.appendChild(hint);
    var timer = null;
    // capture phase on the parent: runs before Leaflet's own wheel handler on the map element
    box.parentNode.addEventListener('wheel', function (e) {
      if (!box.contains(e.target)) return;
      if (e.ctrlKey || e.metaKey) { hint.classList.remove('on'); return; }
      e.stopPropagation(); // Leaflet never sees it, the page keeps scrolling
      hint.classList.add('on');
      clearTimeout(timer);
      timer = setTimeout(function () { hint.classList.remove('on'); }, 1200);
    }, { capture: true, passive: true });
  }

  function el(tag, cls, text) {
    var e = document.createElement(tag);
    if (cls) e.className = cls;
    if (text != null) e.textContent = text;
    return e;
  }

  // ---- read-only map with pins ----
  function initPins(host) {
    host.dataset.ready = '1';
    var pts = [];
    try { pts = JSON.parse(host.getAttribute('data-points') || '[]') || []; } catch (e) {}
    host.style.height = (host.getAttribute('data-height') || 300) + 'px';
    if (!enabled) { host.classList.add('kmap-off'); host.textContent = 'Karten sind in dieser Installation deaktiviert.'; return; }
    var map = L.map(host);
    wheelGuard(map);
    baseLayer(map);
    var bounds = [];
    pts.forEach(function (p) {
      var m = L.circleMarker([p.lat, p.lng], { radius: 9, color: '#fff', weight: 2, fillColor: p.color || '#b5441f', fillOpacity: 1 }).addTo(map);
      var box = el('div');
      box.appendChild(el('b', null, p.title));
      if (p.sub) { box.appendChild(el('br')); box.appendChild(el('span', null, p.sub)); }
      if (p.url) { box.appendChild(el('br')); var a = el('a', null, 'Öffnen'); a.href = p.url; a.setAttribute('data-modal', ''); box.appendChild(a); }
      m.bindPopup(box);
      bounds.push([p.lat, p.lng]);
    });
    if (bounds.length === 1) map.setView(bounds[0], 15);
    else if (bounds.length) map.fitBounds(bounds, { padding: [30, 30], maxZoom: 16 });
    else map.setView([49.0069, 8.4037], 11);
    setTimeout(function () { map.invalidateSize(); }, 80);
  }

  // ---- position picker inside forms ----
  function initGeo(host) {
    host.dataset.ready = '1';
    var input = host.querySelector('input[type=hidden]');
    if (!enabled) {
      var t = el('input'); t.type = 'text'; t.placeholder = 'Breite, Länge (z. B. 49.0069, 8.4037)'; t.value = input.value;
      t.addEventListener('input', function () { input.value = t.value; });
      host.appendChild(t);
      return;
    }
    var bar = el('div', 'kgeo-bar');
    var q = el('input'); q.type = 'search'; q.placeholder = 'Adresse oder Ort suchen …';
    var go = el('button', 'btn sm', 'Suchen'); go.type = 'button';
    var clear = el('button', 'btn ghost sm', 'Pin entfernen'); clear.type = 'button';
    bar.append(q, go, clear);
    var res = el('div', 'kgeo-res');
    var mapEl = el('div', 'kgeo-map');
    var info = el('div', 'help');
    host.append(bar, res, mapEl, info);
    var form = host.closest('form');
    function fieldVal(n) { var f = form && form.querySelector('[name="' + n + '"]'); return f ? f.value.trim() : ''; }
    q.value = [fieldVal('address'), fieldVal('city')].filter(Boolean).join(', ');

    var map = L.map(mapEl).setView([49.0069, 8.4037], 12);
    wheelGuard(map);
    baseLayer(map);
    var marker = null;
    function show() {
      info.textContent = input.value ? 'Position: ' + input.value : 'Noch keine Position. Suchen oder auf die Karte klicken.';
    }
    function set(lat, lng, zoom) {
      lat = Math.round(lat * 1e6) / 1e6; lng = Math.round(lng * 1e6) / 1e6;
      input.value = lat + ',' + lng;
      if (!marker) {
        marker = L.marker([lat, lng], { draggable: true }).addTo(map);
        marker.on('dragend', function () { var p = marker.getLatLng(); set(p.lat, p.lng); });
      } else marker.setLatLng([lat, lng]);
      if (zoom) map.setView([lat, lng], zoom);
      show();
    }
    var m = /^\s*(-?[\d.]+)\s*,\s*(-?[\d.]+)\s*$/.exec(input.value || '');
    if (m) set(parseFloat(m[1]), parseFloat(m[2]), 16); else show();
    map.on('click', function (e) { set(e.latlng.lat, e.latlng.lng); });
    clear.onclick = function () { input.value = ''; if (marker) { map.removeLayer(marker); marker = null; } show(); };
    function search() {
      var term = q.value.trim();
      if (!term || !geocoder) return;
      res.textContent = 'Suche …';
      fetch(geocoder + '?format=jsonv2&limit=5&accept-language=de&q=' + encodeURIComponent(term))
        .then(function (r) { return r.json(); })
        .then(function (list) {
          res.textContent = '';
          if (!list.length) { res.textContent = 'Nichts gefunden. Anderen Suchbegriff probieren oder auf die Karte klicken.'; return; }
          list.forEach(function (it) {
            var b = el('button', 'btn ghost sm', it.display_name); b.type = 'button';
            b.onclick = function () { set(parseFloat(it.lat), parseFloat(it.lon), 17); res.textContent = ''; };
            res.appendChild(b);
          });
        })
        .catch(function () { res.textContent = 'Die Adresssuche ist gerade nicht erreichbar. Du kannst direkt auf die Karte klicken.'; });
    }
    go.onclick = search;
    q.addEventListener('keydown', function (e) { if (e.key === 'Enter') { e.preventDefault(); search(); } });
    setTimeout(function () { map.invalidateSize(); if (marker) map.setView(marker.getLatLng(), map.getZoom()); }, 120);
  }

  window.KMaps = {
    enabled: enabled, baseLayer: baseLayer, el: el, wheelGuard: wheelGuard,
    init: function (scope) {
      scope.querySelectorAll('.kmap:not([data-ready])').forEach(initPins);
      scope.querySelectorAll('.kgeo:not([data-ready])').forEach(initGeo);
    }
  };
})();
