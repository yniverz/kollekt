(function () {
  'use strict';
  var drawer = document.getElementById('drawer');
  var csrf = (document.querySelector('meta[name="csrf"]') || {}).content || '';

  function openDrawer(url) {
    return fetch(url, { headers: { 'X-Partial': '1' }, credentials: 'same-origin' }).then(function (res) {
      if (res.status === 401) { location.href = '/login'; return; }
      return res.text().then(function (html) { showDrawer(html); });
    });
  }
  function showDrawer(html) {
    drawer.innerHTML = html;
    if (!drawer.open) drawer.showModal();
    initWidgets(drawer);
    initMaps(drawer);
    var f = drawer.querySelector('input:not([type=hidden]), select, textarea');
    if (f && !f.value) f.focus();
  }
  function closeDrawer() { if (drawer.open) drawer.close(); }

  document.addEventListener('click', function (e) {
    var a = e.target.closest('[data-modal]');
    if (a && !e.metaKey && !e.ctrlKey && !e.shiftKey) {
      e.preventDefault();
      openDrawer(a.getAttribute('href') || a.getAttribute('data-modal'));
      return;
    }
    var nn = e.target.closest('[data-togglenew]');
    if (nn) { var dd = document.getElementById('neuer-plan'); if (dd) dd.open = true; }
    if (e.target.closest('[data-close]')) { e.preventDefault(); closeDrawer(); return; }
    var row = e.target.closest('tr[data-href]');
    if (row && !e.target.closest('a, button, select, input, label, form')) {
      if (row.hasAttribute('data-nav')) location.href = row.getAttribute('data-href');
      else openDrawer(row.getAttribute('data-href'));
      return;
    }
    if (e.target.closest('[data-print]')) { window.print(); return; }
    var c = e.target.closest('[data-confirm]');
    if (c && !confirm(c.getAttribute('data-confirm'))) { e.preventDefault(); }
  });
  drawer.addEventListener('click', function (e) { if (e.target === drawer) closeDrawer(); });

  // drawer forms submit via fetch so validation errors keep the drawer open
  document.addEventListener('submit', function (e) {
    var form = e.target;
    if (!form.closest('#drawer') || form.hasAttribute('data-plain')) return;
    e.preventDefault();
    var btn = e.submitter;
    var body = new FormData(form);
    if (btn && btn.name) body.append(btn.name, btn.value);
    fetch(form.action, { method: 'POST', body: new URLSearchParams(body), headers: { 'X-Partial': '1' }, credentials: 'same-origin' })
      .then(function (res) {
        if (res.redirected) { location.href = res.url; return; }
        return res.text().then(function (html) { showDrawer(html); });
      });
  });

  // quick selects & checkboxes in lists
  document.addEventListener('change', function (e) {
    var el = e.target;
    if (el.matches('select.quick')) {
      var opt = el.options[el.selectedIndex];
      el.setAttribute('data-color', opt.getAttribute('data-color') || 'gray');
      var body = new URLSearchParams({ field: el.getAttribute('data-field'), value: el.value, _csrf: csrf });
      el.disabled = true;
      fetch(el.getAttribute('data-url'), { method: 'POST', body: body, credentials: 'same-origin' })
        .then(function (r) { el.disabled = false; if (!r.ok) location.reload(); else if (el.hasAttribute('data-reload')) location.reload(); });
    }
    if (el.matches('[data-autosubmit]')) { el.form.submit(); }
  });

  document.addEventListener('focusin', function (e) { if (e.target.matches('[data-selectall]')) e.target.select(); });

  // dynamic rows (calculator)
  document.addEventListener('click', function (e) {
    var add = e.target.closest('[data-add-row]');
    if (add) {
      e.preventDefault();
      var tpl = document.getElementById(add.getAttribute('data-add-row'));
      var host = document.getElementById(add.getAttribute('data-into'));
      host.appendChild(tpl.content.cloneNode(true));
      var first = host.lastElementChild.querySelector('input'); if (first) first.focus();
      return;
    }
    var rm = e.target.closest('[data-rm-row]');
    if (rm) { e.preventDefault(); rm.closest('.rowline, .rline').remove(); }
  });

  // recipe editor
  function initRecipe(root) {
    var hidden = root.querySelector('input[type=hidden]');
    var items = JSON.parse(root.getAttribute('data-items') || '[]');
    var lines = root.querySelector('.rlines');
    var byId = {}; items.forEach(function (i) { byId[i.id] = i; });
    var current = [];
    try { current = JSON.parse(hidden.value || '[]') || []; } catch (x) {}
    function sync() {
      var out = [];
      lines.querySelectorAll('.rline').forEach(function (row) {
        var id = parseInt(row.querySelector('select').value, 10);
        var q = parseFloat(String(row.querySelector('input').value).replace(',', '.'));
        row.querySelector('.u').textContent = byId[id] ? byId[id].unit : '';
        if (id && q > 0) out.push({ i: id, q: q });
      });
      hidden.value = out.length ? JSON.stringify(out) : '';
    }
    function addLine(l) {
      var row = document.createElement('div'); row.className = 'rline';
      var sel = document.createElement('select');
      var o0 = document.createElement('option'); o0.value = ''; o0.textContent = 'Artikel wählen …'; sel.appendChild(o0);
      items.forEach(function (it) { var o = document.createElement('option'); o.value = it.id; o.textContent = it.name; if (l && l.i === it.id) o.selected = true; sel.appendChild(o); });
      var q = document.createElement('input'); q.type = 'number'; q.step = 'any'; q.min = '0'; q.placeholder = 'Menge'; if (l) q.value = l.q;
      var u = document.createElement('span'); u.className = 'u';
      var x = document.createElement('button'); x.type = 'button'; x.className = 'btn ghost sm'; x.textContent = '×'; x.title = 'Entfernen';
      x.onclick = function () { row.remove(); sync(); };
      sel.onchange = sync; q.oninput = sync;
      row.append(sel, q, u, x); lines.appendChild(row);
    }
    current.forEach(addLine);
    if (!current.length) addLine(null);
    root.querySelector('.radd').onclick = function () { addLine(null); };
    sync();
  }
  function initWidgets(scope) {
    scope.querySelectorAll('.recipe').forEach(function (r) { if (!r.dataset.ready) { r.dataset.ready = 1; initRecipe(r); } });
  }
  initWidgets(document);

  // maps are loaded on demand so pages without maps stay light
  var leafletReady = null;
  function loadScript(src) {
    return new Promise(function (res, rej) {
      var s = document.createElement('script'); s.src = src; s.onload = res; s.onerror = rej; document.head.appendChild(s);
    });
  }
  function loadLeaflet() {
    if (leafletReady) return leafletReady;
    var l = document.createElement('link'); l.rel = 'stylesheet'; l.href = '/static/vendor/leaflet/leaflet.css?v=1'; document.head.appendChild(l);
    leafletReady = loadScript('/static/vendor/leaflet/leaflet.js?v=1').then(function () { return loadScript('/static/maps.js?v=2'); });
    return leafletReady;
  }
  function initMaps(scope) {
    var wantsSite = scope === document && document.getElementById('siteplan');
    if (!scope.querySelector('.kmap:not([data-ready]), .kgeo:not([data-ready])') && !wantsSite) return;
    loadLeaflet().then(function () {
      window.KMaps.init(scope);
      if (wantsSite && !window.KSite) loadScript('/static/siteplan.js?v=2');
    });
  }
  initMaps(document);

})();
