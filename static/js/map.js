/* Eagle Lake Rocks — map.
 *
 * Ported from the original Django app's rock-list-map.js. Behaviour kept
 * deliberately: three base layers, small dot markers with a separate always-on
 * label, popups, drag-to-reposition for signed-in editors, save-view-as-PNG,
 * and a table filter wired to the markers.
 */
(function () {
  'use strict';

  var el = document.getElementById('map');
  if (!el || typeof L === 'undefined') return;

  var rocks = window.ROCKS || [];
  var editable = el.dataset.editable === '1';
  var slug = el.dataset.lakeSlug;

  var bounds = L.latLngBounds(
    L.latLng(parseFloat(el.dataset.minLat), parseFloat(el.dataset.minLng)),
    L.latLng(parseFloat(el.dataset.maxLat), parseFloat(el.dataset.maxLng))
  );

  var map = L.map('map', {
    maxZoom: 19,
    // Hard-clamp panning to the lake. Viscosity 1.0 means the edge does not
    // rubber-band at all — you simply cannot drag off Eagle Lake.
    maxBounds: bounds.pad(0.05),
    maxBoundsViscosity: 1.0,
    // No wheel zoom. Scrolling the PAGE with the pointer over the map was
    // zooming the map instead, which is hostile on a trackpad. Zoom is the
    // +/- control, double-click, or pinch on touch.
    scrollWheelZoom: false
  }).fitBounds(bounds, { padding: [30, 30] });

  el._leafletMap = map;   // handle for debugging; harmless

  var osm = L.tileLayer('https://{s}.tile.openstreetmap.org/{z}/{x}/{y}.png', {
    attribution: '&copy; OpenStreetMap contributors', maxZoom: 19
  }).addTo(map);
  var sat = L.tileLayer(
    'https://server.arcgisonline.com/ArcGIS/rest/services/World_Imagery/MapServer/tile/{z}/{y}/{x}',
    { attribution: '&copy; Esri', maxZoom: 19 });
  /* Candidates live on their own overlay. Leaflet's layer control is the right
     home for "what am I looking at" — it is where users already reach, it gives
     the state a visible checkbox, and it keeps the data on the map rather than
     stripping it out of the DOM. Marked hazards are on by default; candidates
     (unmarked rocks) are not. */
  var markedLayer = L.layerGroup().addTo(map);
  var candidateLayer = L.layerGroup();          // deliberately NOT added

  L.control.layers(
    { 'Map': osm, 'Satellite': sat },
    { 'Marked hazards': markedLayer, 'Unmarked candidates': candidateLayer },
    { collapsed: false }
  ).addTo(map);
  L.control.scale({ imperial: true, metric: true }).addTo(map);

  // A key. Distinct markers without one is half a feature.
  var legend = L.control({ position: 'bottomright' });
  legend.onAdd = function () {
    var d = L.DomUtil.create('div', 'map-legend');
    d.innerHTML =
      '<b>Shape</b>' +
      '<span><i class="lg sh-marked vf-yes"></i>Marked \u2014 buoy on the water</span>' +
      '<span><i class="lg sh-candidate vf-yes"></i>Candidate \u2014 unmarked rock</span>' +
      '<b>Colour</b>' +
      '<span><i class="lg sh-marked vf-yes"></i>Verified</span>' +
      '<span><i class="lg sh-marked vf-no">?</i>Unverified</span>';
    L.DomEvent.disableClickPropagation(d);
    return d;
  };
  legend.addTo(map);

  var markers = {};

  function popupHTML(r) {
    var lat = r.latitude, lng = r.longitude;
    var bits = ['<div class="popup"><h3>' + esc(r.marker_id) + '</h3>'];
    if (r.local_name && r.local_name !== r.marker_id) {
      bits.push('<p class="pname">' + esc(r.local_name) + '</p>');
    }
    bits.push('<p>' + lat.toFixed(5) + ', ' + lng.toFixed(5) + '</p>');
    if (r.size != null) bits.push('<p>Radius ' + r.size + ' m</p>');
    if (r.depth_ft != null) bits.push('<p>Depth ' + r.depth_ft + ' ft</p>');
    if (r.dedication) bits.push('<p class="ded">' + esc(r.dedication) + '</p>');
    if (r.url) bits.push('<p><a href="' + r.url + '">Details &rarr;</a></p>');
    return bits.join('') + '</div>';
  }

  function esc(s) {
    return String(s == null ? '' : s).replace(/[&<>"']/g, function (c) {
      return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c];
    });
  }

  rocks.forEach(function (r) {
    if (r.latitude == null || r.longitude == null) return;   // not a mappable hazard

    /* Two independent axes, two independent visual channels.
       SHAPE  = is a buoy on the water?   circle = marked, diamond = candidate.
       COLOUR = has anyone confirmed it?  red = verified, amber = unverified.
       A marked rock can be unverified and a candidate can be verified, so
       neither channel may be derived from the other. Unverified also carries a
       "?" so the distinction survives greyscale and colour-blindness. */
    var marked = (r.status || 'candidate').toLowerCase() === 'marked';
    var glyph = r.verified ? '' : '?';
    var size = marked ? (glyph ? 17 : 13) : 15;

    var m = L.marker([r.latitude, r.longitude], {
      icon: L.divIcon({
        className: 'rock-marker'
          + (marked ? ' sh-marked' : ' sh-candidate')
          + (r.verified ? ' vf-yes' : ' vf-no')
          + (r.hidden ? ' is-hidden' : ''),
        iconSize: [size, size], iconAnchor: [size / 2, size / 2],
        html: '<span><i>' + glyph + '</i></span>'
      }),
      draggable: editable,
      keyboard: false
    });
    var layer = marked ? markedLayer : candidateLayer;
    m.addTo(layer);

    var label = L.marker([r.latitude, r.longitude], {
      icon: L.divIcon({ className: 'marker-label', iconSize: null, iconAnchor: [-8, 8],
                        html: '<span>' + esc(r.marker_id) + '</span>' }),
      interactive: false
    }).addTo(layer);          // the label belongs to the marker's layer, not the map

    m.bindPopup(popupHTML(r));
    markers[r.marker_id] = { marker: m, label: label, rock: r };

    if (editable) {
      m.on('dragend', function (e) {
        var p = e.target.getLatLng();
        label.setLatLng(p);
        fetch('/' + slug + '/rock/' + r.id + '/move', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ latitude: p.lat, longitude: p.lng })
        }).then(function (res) {
          if (!res.ok) throw new Error('save failed');
          r.latitude = p.lat; r.longitude = p.lng;
          m.setPopupContent(popupHTML(r));
          syncRow(r);
          flash(r.marker_id + ' moved to ' + p.lat.toFixed(5) + ', ' + p.lng.toFixed(5));
        }).catch(function () {
          m.setLatLng([r.latitude, r.longitude]);       // snap back; nothing was saved
          label.setLatLng([r.latitude, r.longitude]);
          flash('Could not save the new position for ' + r.marker_id + '.', true);
        });
      });
    }
  });

  /* Re-fit whenever the container is resized.
     Leaflet measures the container once, at construction. If the layout has not
     settled yet the map can be a couple of pixels wide, and fitBounds then
     legitimately clamps to maxZoom — a fully zoomed-in map showing nothing.
     This also covers the real-world cases: phone rotation, a responsive
     breakpoint, a pane being dragged wider. We stop re-fitting as soon as the
     user takes the view, so we never yank the map out from under them. */
  var userMoved = false;
  ['mousedown', 'wheel', 'touchstart', 'dblclick'].forEach(function (ev) {
    el.addEventListener(ev, function () { userMoved = true; }, { passive: true });
  });

  /* Floor the zoom at whatever currently fits the lake, so you can never zoom
     out past the boundary into empty Ontario. It has to be recomputed on every
     resize: the zoom that fits the lake in a phone-width container is not the
     zoom that fits it on a desktop. */
  function clampMinZoom() {
    map.setMinZoom(0);                                   // lift, or the probe is floored by the old value
    var fit = map.getBoundsZoom(bounds, false, L.point(30, 30));
    map.setMinZoom(fit);
    return fit;
  }

  function refit() {
    map.invalidateSize();
    var fit = clampMinZoom();
    if (userMoved) return;
    /* Keep the table in step with the overlay, so the list always describes
     exactly what is drawn. */
  function syncCandidateRows() {
    var shown = map.hasLayer(candidateLayer);
    document.querySelectorAll('#rocks tbody tr[data-candidate="1"]').forEach(function (row) {
      row.hidden = !shown;
    });
  }
  map.on('overlayadd overlayremove', syncCandidateRows);
  syncCandidateRows();

  if (el.dataset.focus === '1' && rocks.length === 1 && rocks[0].latitude != null) {
      map.setView([rocks[0].latitude, rocks[0].longitude], Math.max(fit, 16));
    } else {
      map.fitBounds(bounds, { padding: [30, 30] });
    }
  }
  clampMinZoom();

  if (window.ResizeObserver) {
    new ResizeObserver(refit).observe(el);
  } else {
    window.addEventListener('load', refit);
    window.addEventListener('resize', refit);
  }

  if (el.dataset.focus === '1' && rocks.length === 1 && rocks[0].latitude != null) {
    map.setView([rocks[0].latitude, rocks[0].longitude], 16);
    markers[rocks[0].marker_id].marker.openPopup();
  }

  /* --- table <-> map --------------------------------------------------- */

  function syncRow(r) {
    var row = document.querySelector('#rocks tbody tr[data-marker="' + CSS.escape(r.marker_id) + '"]');
    if (!row) return;
    row.children[2].textContent = r.latitude.toFixed(5);
    row.children[3].textContent = r.longitude.toFixed(5);
  }

  document.querySelectorAll('#rocks tbody tr').forEach(function (row) {
    row.addEventListener('mouseenter', function () {
      var e = markers[row.dataset.marker];
      if (e) e.marker.getElement() && e.marker.getElement().classList.add('hot');
    });
    row.addEventListener('mouseleave', function () {
      var e = markers[row.dataset.marker];
      if (e) e.marker.getElement() && e.marker.getElement().classList.remove('hot');
    });
    row.addEventListener('click', function (ev) {
      if (ev.target.tagName === 'A') return;
      var e = markers[row.dataset.marker];
      if (e) { map.panTo(e.marker.getLatLng()); e.marker.openPopup(); }
    });
  });

  var filter = document.getElementById('filter');
  if (filter) {
    filter.addEventListener('input', function () {
      var q = filter.value.trim().toLowerCase();
      var showCand = map.hasLayer(candidateLayer);
      document.querySelectorAll('#rocks tbody tr').forEach(function (row) {
        if (row.dataset.candidate === '1' && !showCand) { row.hidden = true; return; }
        row.hidden = q !== '' && row.textContent.toLowerCase().indexOf(q) === -1;
      });
    });
  }

  /* --- click-to-place for the add form --------------------------------- */

  var fLat = document.getElementById('f-lat'), fLng = document.getElementById('f-lng');
  if (editable && fLat && fLng) {
    var ghost = null;
    map.on('click', function (e) {
      fLat.value = e.latlng.lat.toFixed(5);
      fLng.value = e.latlng.lng.toFixed(5);
      if (ghost) map.removeLayer(ghost);
      ghost = L.marker(e.latlng, {
        icon: L.divIcon({ className: 'rock-marker ghost', iconSize: [12, 12], iconAnchor: [6, 6], html: '<span></span>' })
      }).addTo(map);
      flash('Position set — fill in the marker ID and save.');
    });
  }

  /* --- flash ------------------------------------------------------------ */

  var flashEl = null;
  function flash(msg, bad) {
    if (!flashEl) {
      flashEl = document.createElement('div');
      flashEl.className = 'flash';
      document.body.appendChild(flashEl);
    }
    flashEl.textContent = msg;
    flashEl.classList.toggle('bad', !!bad);
    flashEl.classList.add('show');
    clearTimeout(flashEl._t);
    flashEl._t = setTimeout(function () { flashEl.classList.remove('show'); }, 4000);
  }
})();
