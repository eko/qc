/* qc report: progressive enhancement of a static page.
   Charts are re-rendered from their embedded data at the page's pixel size,
   with a tooltip reading the measured values, legend toggles and a linked
   zoom on time. Tables sort, commands copy. Nothing here fetches anything. */
(function () {
  'use strict';

  var doc = document;
  var root = doc.documentElement;
  var SVGNS = 'http://www.w3.org/2000/svg';
  root.classList.add('js');

  var reduceMotion = window.matchMedia && window.matchMedia('(prefers-reduced-motion: reduce)').matches;

  /* ---------- formatting (mirrors svg.go) ---------- */

  function pad(n, w) {
    n = String(n);
    while (n.length < w) n = '0' + n;
    return n;
  }

  // clockMs is hh:mm:ss.mmm, the exact position of a frame.
  function clockMs(s) {
    var ms = Math.round(Math.max(s, 0) * 1000);
    return pad(Math.floor(ms / 3600000), 2) + ':' + pad(Math.floor(ms / 60000) % 60, 2) + ':' +
      pad(Math.floor(ms / 1000) % 60, 2) + '.' + pad(ms % 1000, 3);
  }

  function timeLabel(s, digits) {
    return clockLabel(s, digits, false);
  }

  function clockLabel(s, digits, hours) {
    var scale = Math.pow(10, digits);
    var total = Math.round(Math.max(s, 0) * scale);
    var whole = Math.floor(total / scale);
    var h = Math.floor(whole / 3600), m = Math.floor(whole / 60) % 60;
    var sec = pad(whole % 60, 2);
    if (digits > 0) sec += '.' + pad(total - whole * scale, digits);
    return h > 0 || hours ? h + ':' + pad(m, 2) + ':' + sec : m + ':' + sec;
  }

  function trim(v) {
    return String(Math.round(v * 100) / 100);
  }

  function bitrateLabel(v) {
    if (v >= 1e6) return (v / 1e6).toFixed(2) + ' Mb/s';
    if (v >= 1e3) return (v / 1e3).toFixed(0) + ' kb/s';
    return v.toFixed(0) + ' b/s';
  }

  function bitrateTick(v) {
    if (v >= 1e6) return trim(v / 1e6) + ' Mb/s';
    if (v >= 1e3) return trim(v / 1e3) + ' kb/s';
    return trim(v) + ' b/s';
  }

  function bytesLabel(v) {
    if (v >= 1048576) return (v / 1048576).toFixed(1) + ' MiB';
    if (v < 1024) return v.toFixed(0) + ' B';
    return (v / 1024).toFixed(1) + ' KiB';
  }

  function bytesTick(v) {
    if (v >= 1048576) return trim(v / 1048576) + ' MiB';
    if (v >= 1024) return trim(v / 1024) + ' KiB';
    return trim(v) + ' B';
  }

  function stepDigits(step) {
    if (step <= 0 || step >= 1) return 0;
    return Math.min(3, Math.ceil(-Math.log10(step) - 1e-9));
  }

  function tickLabel(unit, v, step) {
    switch (unit) {
      case 'time': return timeLabel(v, stepDigits(step));
      case 'bitrate': return bitrateTick(v);
      case 'bytes': return bytesTick(v);
    }
    return String(Math.round(v * 1000) / 1000);
  }

  function valueLabel(unit, v, digits) {
    switch (unit) {
      case 'time': return clockMs(v);
      case 'bitrate': return bitrateLabel(v);
      case 'bytes': return bytesLabel(v);
    }
    return v.toFixed(digits);
  }

  /* ---------- ticks (mirrors svg.go) ---------- */

  var TIME_STEPS = [0.001, 0.002, 0.005, 0.01, 0.02, 0.05, 0.1, 0.2, 0.5,
    1, 2, 5, 10, 15, 30, 60, 120, 300, 600, 900, 1800, 3600, 7200, 10800, 21600];

  function niceStep(span, n) {
    var raw = span / n;
    var mag = Math.pow(10, Math.floor(Math.log10(raw)));
    var ms = [1, 2, 2.5, 5];
    for (var i = 0; i < ms.length; i++) if (raw <= ms[i] * mag) return ms[i] * mag;
    return 10 * mag;
  }

  function unitStep(unit, span, n) {
    if (unit === 'bytes') {
      var units = [1048576, 1024];
      for (var i = 0; i < units.length; i++) {
        if (span >= units[i] * n) return niceStep(span / units[i], n) * units[i];
      }
    }
    return niceStep(span, n);
  }

  function timeStep(span, n) {
    var raw = span / n;
    for (var i = 0; i < TIME_STEPS.length; i++) if (raw <= TIME_STEPS[i]) return TIME_STEPS[i];
    return TIME_STEPS[TIME_STEPS.length - 1];
  }

  function linearTicks(lo, hi, step) {
    var out = [];
    for (var k = Math.ceil(lo / step - 1e-9); k * step <= hi + 1e-9 * step; k++) out.push(k * step);
    return out;
  }

  function logTicks(lo, hi) {
    var all = [], wide = [];
    for (var d = Math.floor(Math.log10(lo)); d <= Math.ceil(Math.log10(hi)); d++) {
      [1, 2, 5].forEach(function (m) {
        var v = m * Math.pow(10, d);
        if (v >= lo * (1 - 1e-9) && v <= hi * (1 + 1e-9)) {
          all.push(v);
          if (m !== 2) wide.push(v);
        }
      });
    }
    if (all.length < 2) return [lo, hi];
    return all.length > 9 ? wide : all;
  }

  /* ---------- data ---------- */

  // column decodes svg.go's compact columns: a progression or quantized
  // integer deltas.
  function column(c) {
    var n = c.n || 0, out = new Float64Array(n), i;
    if (c.d) {
      var acc = 0, q = c.q || 1;
      for (i = 0; i < n; i++) {
        acc += c.d[i];
        out[i] = acc / q;
      }
    } else {
      var a = c.a || 0, s = c.s || 0;
      for (i = 0; i < n; i++) out[i] = a + i * s;
    }
    return out;
  }

  // lowerBound is the first index whose value is >= v.
  function lowerBound(xs, v) {
    var lo = 0, hi = xs.length;
    while (lo < hi) {
      var mid = (lo + hi) >> 1;
      if (xs[mid] < v) lo = mid + 1; else hi = mid;
    }
    return lo;
  }

  function nearestIndex(s, v) {
    var xs = s.x, n = xs.length;
    if (!n) return -1;
    var i = lowerBound(xs, v);
    if (s.style === 'step') {
      if (i < n && xs[i] === v) return i;
      return i - 1;
    }
    if (i >= n) return n - 1;
    if (i > 0 && v - xs[i - 1] <= xs[i] - v) return i - 1;
    return i;
  }

  /* ---------- DOM helpers ---------- */

  function svgEl(tag, attrs, parent) {
    var e = doc.createElementNS(SVGNS, tag);
    for (var k in attrs) if (Object.prototype.hasOwnProperty.call(attrs, k)) e.setAttribute(k, attrs[k]);
    if (parent) parent.appendChild(e);
    return e;
  }

  function htmlEl(tag, cls, text, parent) {
    var e = doc.createElement(tag);
    if (cls) e.className = cls;
    if (text != null) e.textContent = text;
    if (parent) parent.appendChild(e);
    return e;
  }

  function r1(v) {
    return Math.round(v * 10) / 10;
  }

  var toastTimer;
  function toast(msg) {
    var t = doc.querySelector('.toast');
    if (!t) return;
    t.textContent = msg;
    t.classList.add('show');
    clearTimeout(toastTimer);
    toastTimer = setTimeout(function () { t.classList.remove('show'); }, 1600);
  }

  /* ---------- charts ---------- */

  var PAD_TOP = 10, PAD_RIGHT = 16, PAD_BOTTOM = 28, Y_TICKS = 4, EDGE = 28;
  var charts = [];
  var clipCount = 0;

  // readData parses a chart's data, inflating it when the report gzipped
  // it (long titles).
  function readData(fig) {
    var el = fig.querySelector('.chart-data');
    if (!el) return Promise.reject(new Error('no chart data'));
    if (el.getAttribute('data-encoding') !== 'gzip') {
      return Promise.resolve().then(function () { return JSON.parse(el.textContent); });
    }
    if (!('DecompressionStream' in window)) return Promise.reject(new Error('DecompressionStream unsupported'));
    var bin = atob(el.textContent.trim()), bytes = new Uint8Array(bin.length);
    for (var i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i);
    var stream = new Blob([bytes]).stream().pipeThrough(new DecompressionStream('gzip'));
    return new Response(stream).text().then(JSON.parse);
  }

  function Chart(fig, d) {
    this.fig = fig;
    this.d = d;
    this.point = fig.getAttribute('data-mode') === 'point';
    this.plotEl = fig.querySelector('.plot');
    this.svg = fig.querySelector('svg');
    this.log = !!d.log;
    this.full = [d.xr[0], d.xr[1]];
    this.view = this.full.slice();
    this.hidden = {};
    this.cursor = null;
    this.mark = null;
    this.clip = 'qc-clip-' + (++clipCount);
    this.series = (d.series || []).map(function (s) {
      var out = { i: s.i, name: s.n, key: s.k, color: s.c, style: s.s, bold: !!s.b, notip: !!s.notip, tiponly: !!s.tiponly, digits: s.d || 0 };
      out.x = column(s.x);
      out.y = column(s.y);
      out.f = s.f ? column(s.f) : null;
      out.info = s.info || null;
      return out;
    });
    this.tipSeries = this.series.filter(function (s) { return !s.notip && s.x.length; });
    this.primary = this.tipSeries.filter(function (s) { return !s.tiponly && s.style !== 'step'; })[0] || this.tipSeries[0] || null;
    this.build();
    this.render();
  }

  Chart.prototype.sx = function (v) {
    return this.log ? Math.log(Math.max(v, 1e-9)) : v;
  };

  Chart.prototype.ux = function (v) {
    return this.log ? Math.exp(v) : v;
  };

  Chart.prototype.isTime = function () {
    return !this.point && this.d.x === 'time';
  };

  Chart.prototype.zoomed = function () {
    return this.view[0] > this.full[0] + 1e-9 || this.view[1] < this.full[1] - 1e-9;
  };

  Chart.prototype.build = function () {
    var self = this, fig = this.fig;
    var head = htmlEl('div', 'chart-head');
    var legend = fig.querySelector('.legend');
    fig.insertBefore(head, this.plotEl);
    if (legend) head.appendChild(legend);

    fig.querySelectorAll('.key').forEach(function (key) {
      key.addEventListener('click', function () {
        var k = key.getAttribute('data-key');
        self.hidden[k] = !self.hidden[k];
        key.setAttribute('aria-pressed', self.hidden[k] ? 'false' : 'true');
        self.render();
        self.refreshTip();
      });
    });

    if (!this.point) {
      var tools = htmlEl('div', 'chart-tools', null, head);
      this.zoomLabel = htmlEl('span', 'zoom-label', '', tools);
      var mk = function (text, label, fn) {
        var b = htmlEl('button', null, text, tools);
        b.type = 'button';
        b.title = label;
        b.setAttribute('aria-label', label);
        b.addEventListener('click', fn);
        return b;
      };
      mk('−', 'Zoom out', function () { self.zoomBy(2); });
      mk('+', 'Zoom in', function () { self.zoomBy(0.5); });
      this.resetBtn = mk('Reset', 'Reset zoom', function () { self.reset(); });
    }

    this.tip = htmlEl('div', 'tip', null, this.plotEl);
    this.tip.hidden = true;
    this.tip.id = this.clip + '-tip';
    this.plotEl.setAttribute('tabindex', '0');
    this.plotEl.setAttribute('data-interactive', '');
    this.plotEl.setAttribute('aria-describedby', this.tip.id);
    this.plotEl.setAttribute('aria-label', (this.svg.getAttribute('aria-label') || 'Chart') +
      (this.point ? '. Arrow keys step through the points.' : '. Arrow keys read values, plus and minus zoom, Escape resets.'));

    this.plotEl.addEventListener('pointermove', function (e) { self.onMove(e); });
    this.plotEl.addEventListener('pointerdown', function (e) { self.onDown(e); });
    this.plotEl.addEventListener('pointerup', function (e) { self.onUp(e); });
    this.plotEl.addEventListener('pointercancel', function () { self.brush = null; self.drawBrush(); });
    this.plotEl.addEventListener('pointerleave', function (e) {
      if (e.pointerType === 'mouse' && !self.brush) self.hideTip();
    });
    this.plotEl.addEventListener('dblclick', function (e) {
      e.preventDefault();
      self.reset();
    });
    this.plotEl.addEventListener('keydown', function (e) { self.onKey(e); });
    this.plotEl.addEventListener('blur', function () { self.hideTip(); });
  };

  // layout sizes the chart to its container: one SVG unit per CSS pixel,
  // so labels keep their size on phones.
  Chart.prototype.layout = function () {
    var w = Math.max(280, Math.round(this.plotEl.clientWidth || this.d.w));
    var h = this.d.h;
    if (w < 560) h = Math.round(h * 0.85);
    var yLo = this.d.yr[0], yHi = this.d.yr[1], step = this.d.ys || (yHi - yLo) / Y_TICKS;
    var logy = !!this.d.logy;
    var ticks = logy ? logTicks(yLo, yHi) : linearTicks(yLo, yHi, step);
    var unit = this.d.y, longest = 0;
    var labels = ticks.map(function (v) {
      var l = tickLabel(unit, v, logy ? v : step);
      longest = Math.max(longest, l.length);
      return l;
    });
    var left = Math.max(36, Math.round(longest * 6.4 + 14));
    this.box = { l: left, t: PAD_TOP, r: w - PAD_RIGHT, b: h - PAD_BOTTOM, w: w, h: h };
    this.yTicks = ticks;
    this.yLabels = labels;
  };

  Chart.prototype.X = function (v) {
    var b = this.box;
    return b.l + (this.sx(v) - this.view[0]) / (this.view[1] - this.view[0]) * (b.r - b.l);
  };

  Chart.prototype.Y = function (v) {
    var b = this.box, lo = this.d.yr[0], hi = this.d.yr[1];
    v = Math.max(Math.min(v, hi), lo);
    if (this.d.logy) return b.t + Math.log(hi / v) / Math.log(hi / lo) * (b.b - b.t);
    return b.t + (hi - v) / (hi - lo) * (b.b - b.t);
  };

  // dataX is the unscaled x under a plot x.
  Chart.prototype.dataX = function (px) {
    var b = this.box;
    return this.ux(this.view[0] + (px - b.l) / (b.r - b.l) * (this.view[1] - this.view[0]));
  };

  Chart.prototype.render = function () {
    this.layout();
    var self = this, b = this.box, svg = this.svg;
    while (svg.firstChild) svg.removeChild(svg.firstChild);
    svg.setAttribute('viewBox', '0 0 ' + b.w + ' ' + b.h);

    var defs = svgEl('defs', {}, svg);
    var cp = svgEl('clipPath', { id: this.clip }, defs);
    svgEl('rect', { x: b.l, y: b.t - 6, width: b.r - b.l, height: b.b - b.t + 12 }, cp);

    var gy = svgEl('g', { 'class': 'yaxis' }, svg);
    this.yTicks.forEach(function (v, i) {
      var y = r1(self.Y(v));
      svgEl('line', { x1: b.l, x2: b.r, y1: y, y2: y, 'class': 'grid' }, gy);
      svgEl('text', { x: b.l - 8, y: y + 4, 'class': 'axis', 'text-anchor': 'end' }, gy).textContent = self.yLabels[i];
    });
    svgEl('line', { x1: b.l, x2: b.r, y1: b.b, y2: b.b, 'class': 'baseline' }, gy);

    var gb = svgEl('g', { 'class': 'bands', 'clip-path': 'url(#' + this.clip + ')' }, svg);
    (this.d.bands || []).forEach(function (band) {
      if (band.b < self.ux(self.view[0]) || band.a > self.ux(self.view[1])) return;
      var x0 = self.X(band.a), x1 = self.X(band.b);
      svgEl('rect', { x: r1(x0), y: b.t, width: r1(Math.max(x1 - x0, 1)), height: b.b - b.t, 'class': 'band', style: 'fill:' + band.c }, gb);
    });

    this.drawXAxis(svg);

    var gs = svgEl('g', { 'clip-path': 'url(#' + this.clip + ')' }, svg);
    this.series.forEach(function (s) {
      if (s.tiponly) return;
      var g = svgEl('g', { 'class': 'series' + (self.hidden[s.key] ? ' off' : '') }, gs);
      self.drawSeries(g, s);
    });

    this.overlay = svgEl('g', { 'class': 'overlay' }, svg);
    this.drawMark();

    if (this.resetBtn) {
      this.resetBtn.disabled = !this.zoomed();
      this.zoomLabel.textContent = this.zoomed() && this.isTime()
        ? timeLabel(this.view[0], 1) + ' – ' + timeLabel(this.view[1], 1) : '';
    }
  };

  Chart.prototype.drawXAxis = function (svg) {
    var self = this, b = this.box, g = svgEl('g', { 'class': 'xaxis' }, svg);
    var lo = this.view[0], hi = this.view[1], ticks, step;
    var target = Math.max(2, Math.min(6, Math.floor((b.r - b.l) / 80)));

    if (this.log) {
      ticks = logTicks(Math.exp(lo), Math.exp(hi));
      step = 0;
    } else {
      step = this.d.x === 'time' ? timeStep(hi - lo, target) : niceStep(hi - lo, target);
      ticks = linearTicks(lo, hi, step);
    }

    ticks.forEach(function (v) {
      var x = r1(self.X(v));
      var anchor = x - b.l < EDGE ? 'start' : b.r - x < EDGE ? 'end' : 'middle';
      svgEl('line', { x1: x, x2: x, y1: b.b, y2: b.b + 4, 'class': 'tick' }, g);
      var label = tickLabel(self.d.x, v, step || v);
      // Every label of an axis reaching an hour shows hours.
      if (self.d.x === 'time' && hi >= 3600) label = clockLabel(v, stepDigits(step), true);
      svgEl('text', { x: x, y: b.h - 8, 'class': 'axis', 'text-anchor': anchor }, g).textContent = label;
    });
  };

  // visible is the index range of a series' samples in view, with one
  // neighbour on each side so lines reach the plot edges.
  Chart.prototype.visible = function (s) {
    var lo = this.ux(this.view[0]), hi = this.ux(this.view[1]);
    var i0 = Math.max(0, lowerBound(s.x, lo) - 1);
    var i1 = Math.min(s.x.length, lowerBound(s.x, hi) + 2);
    if (i1 < s.x.length && s.x[i1 - 1] <= hi) i1++;
    return [i0, Math.min(i1, s.x.length)];
  };

  Chart.prototype.drawSeries = function (g, s) {
    var self = this, b = this.box, range = this.visible(s), i0 = range[0], i1 = range[1], n = i1 - i0;
    if (n <= 0) return;
    var budget = Math.max(50, Math.round(b.r - b.l));
    var i, j;

    if (s.style === 'markers') {
      var count = Math.min(n, Math.round(budget / 2));
      for (j = 0; j < count; j++) {
        i = i0 + Math.floor(j * n / count);
        svgEl('circle', { cx: r1(self.X(s.x[i])), cy: r1(self.Y(s.y[i])), r: 4, 'class': 'dot', style: 'fill:' + s.color }, g);
      }
      return;
    }

    var mean = [], lows = [], highs = [];
    if (n > budget && s.style !== 'step') {
      for (j = 0; j < budget; j++) {
        var from = i0 + Math.floor(j * n / budget), to = i0 + Math.floor((j + 1) * n / budget);
        var sx = 0, sy = 0, mn = Infinity, mx = -Infinity;
        for (i = from; i < to; i++) {
          sx += s.x[i];
          sy += s.y[i];
          if (s.y[i] < mn) mn = s.y[i];
          if (s.y[i] > mx) mx = s.y[i];
        }
        var cx = self.X(sx / (to - from));
        mean.push([cx, self.Y(sy / (to - from))]);
        lows.push([cx, self.Y(mn)]);
        highs.push([cx, self.Y(mx)]);
      }
    } else {
      for (i = i0; i < i1; i++) mean.push([self.X(s.x[i]), self.Y(s.y[i])]);
    }

    var path = function (pts) {
      return pts.map(function (p, k) { return (k ? 'L' : 'M') + r1(p[0]) + ',' + r1(p[1]); }).join(' ');
    };

    if (s.style === 'area') {
      svgEl('path', {
        d: path(mean) + ' L' + r1(mean[mean.length - 1][0]) + ',' + b.b + ' L' + r1(mean[0][0]) + ',' + b.b + ' Z',
        'class': 'area', style: 'fill:' + s.color
      }, g);
    } else if (highs.length) {
      svgEl('path', { d: path(highs) + ' ' + path(lows.slice().reverse()).replace('M', 'L') + ' Z', 'class': 'envelope', style: 'fill:' + s.color }, g);
    }

    svgEl('path', { d: path(mean), 'class': s.bold ? 'line bold' : 'line', style: 'stroke:' + s.color }, g);
  };

  Chart.prototype.drawMark = function () {
    if (!this.mark || !this.overlay) return;
    var b = this.box, x0 = this.X(this.mark[0]), x1 = this.X(this.mark[1]);
    if (x1 < b.l || x0 > b.r) return;
    x0 = Math.max(x0, b.l);
    x1 = Math.min(x1, b.r);
    var g = svgEl('g', {}, this.overlay);
    svgEl('rect', { x: r1(x0), y: b.t, width: r1(Math.max(x1 - x0, 2)), height: b.b - b.t, 'class': 'mark' }, g);
    svgEl('line', { x1: r1(x0), x2: r1(x0), y1: b.t, y2: b.b, 'class': 'mark-edge' }, g);
    if (x1 - x0 > 2) svgEl('line', { x1: r1(x1), x2: r1(x1), y1: b.t, y2: b.b, 'class': 'mark-edge' }, g);
  };

  /* ----- pointer & keyboard ----- */

  Chart.prototype.local = function (e) {
    var rect = this.svg.getBoundingClientRect();
    return { x: (e.clientX - rect.left) * this.box.w / rect.width, y: (e.clientY - rect.top) * this.box.h / rect.height, rect: rect };
  };

  Chart.prototype.onDown = function (e) {
    // A tap reads the values under the finger.
    if (e.pointerType !== 'mouse') this.onMove(e);
    if (this.point || e.button !== 0 || e.pointerType === 'touch') return;
    var p = this.local(e);
    if (p.x < this.box.l || p.x > this.box.r) return;
    this.brush = { from: p.x, to: p.x };
    try { this.plotEl.setPointerCapture(e.pointerId); } catch (err) { /* capture is optional */ }
  };

  Chart.prototype.onUp = function (e) {
    var br = this.brush;
    this.brush = null;
    this.drawBrush();
    if (!br) return;
    try { this.plotEl.releasePointerCapture(e.pointerId); } catch (err) { /* capture is optional */ }
    if (Math.abs(br.to - br.from) < 6) return;
    var a = this.sx(this.dataX(Math.max(Math.min(br.from, br.to), this.box.l)));
    var b = this.sx(this.dataX(Math.min(Math.max(br.from, br.to), this.box.r)));
    this.setView(a, b, true);
  };

  Chart.prototype.onMove = function (e) {
    var p = this.local(e);
    if (this.brush) {
      this.brush.to = Math.max(this.box.l, Math.min(this.box.r, p.x));
      this.drawBrush();
    }
    if (this.point) this.pointAt(p.x, p.y);
    else this.cursorAt(this.dataX(Math.max(this.box.l, Math.min(this.box.r, p.x))), p);
  };

  Chart.prototype.drawBrush = function () {
    var old = this.svg.querySelector('.brush');
    if (old) old.parentNode.removeChild(old);
    if (!this.brush || !this.overlay) return;
    var x0 = Math.min(this.brush.from, this.brush.to), x1 = Math.max(this.brush.from, this.brush.to);
    svgEl('rect', { x: r1(x0), y: this.box.t, width: r1(x1 - x0), height: this.box.b - this.box.t, 'class': 'brush' }, this.overlay);
  };

  Chart.prototype.onKey = function (e) {
    var k = e.key;
    if (this.point) {
      if (k === 'ArrowRight' || k === 'ArrowLeft') {
        this.stepPoint(k === 'ArrowRight' ? 1 : -1);
        e.preventDefault();
      } else if (k === 'Escape') this.hideTip();
      return;
    }
    var s = this.primary;
    if ((k === 'ArrowRight' || k === 'ArrowLeft' || k === 'Home' || k === 'End') && s) {
      var i = this.cursor == null ? lowerBound(s.x, this.ux(this.view[0])) : nearestIndex(s, this.cursor);
      var n = e.shiftKey ? 10 : 1;
      if (k === 'ArrowRight') i += this.cursor == null ? 0 : n;
      if (k === 'ArrowLeft') i -= this.cursor == null ? 0 : n;
      if (k === 'Home') i = lowerBound(s.x, this.ux(this.view[0]));
      if (k === 'End') i = lowerBound(s.x, this.ux(this.view[1])) - 1;
      i = Math.max(0, Math.min(s.x.length - 1, i));
      var t = this.sx(s.x[i]);
      if (t < this.view[0] || t > this.view[1]) {
        var span = this.view[1] - this.view[0];
        this.setView(t - span / 2, t + span / 2, true);
      }
      this.cursorAt(s.x[i], null);
      e.preventDefault();
    } else if (k === '+' || k === '=') {
      this.zoomBy(0.5);
      e.preventDefault();
    } else if (k === '-' || k === '_') {
      this.zoomBy(2);
      e.preventDefault();
    } else if (k === 'Escape' || k === '0') {
      this.hideTip();
      this.reset();
    }
  };

  /* ----- zoom, linked across time charts ----- */

  Chart.prototype.minSpan = function () {
    var s = this.primary;
    if (s && s.x.length > 1) return Math.max(1e-3, 4 * (s.x[s.x.length - 1] - s.x[0]) / (s.x.length - 1));
    return (this.full[1] - this.full[0]) / 1000;
  };

  Chart.prototype.clampView = function (a, b) {
    var lo = this.full[0], hi = this.full[1], min = Math.min(this.minSpan(), hi - lo);
    if (b - a < min) {
      var c = (a + b) / 2;
      a = c - min / 2;
      b = c + min / 2;
    }
    if (a < lo) { b += lo - a; a = lo; }
    if (b > hi) { a -= b - hi; b = hi; }
    return [Math.max(a, lo), Math.min(b, hi)];
  };

  Chart.prototype.setView = function (a, b, linked) {
    if (linked && this.isTime()) {
      timeCharts().forEach(function (c) { c.applyView(a, b); });
      return;
    }
    this.applyView(a, b);
  };

  Chart.prototype.applyView = function (a, b) {
    this.view = this.clampView(a, b);
    this.render();
    this.refreshTip();
  };

  Chart.prototype.zoomBy = function (factor) {
    var c = this.cursor != null ? this.sx(this.cursor) : (this.view[0] + this.view[1]) / 2;
    var a = c - (c - this.view[0]) * factor, b = c + (this.view[1] - c) * factor;
    this.setView(a, b, true);
  };

  Chart.prototype.reset = function () {
    if (this.isTime()) {
      timeCharts().forEach(function (c) { c.mark = null; c.applyView(c.full[0], c.full[1]); });
      return;
    }
    this.applyView(this.full[0], this.full[1]);
  };

  function timeCharts() {
    return charts.filter(function (c) { return c.isTime(); });
  }

  /* ----- tooltips ----- */

  Chart.prototype.hideTip = function () {
    this.tip.hidden = true;
    this.cursor = null;
    this.lastPoint = null;
    this.clearHover();
  };

  Chart.prototype.clearHover = function () {
    if (!this.overlay) return;
    this.overlay.querySelectorAll('.hover').forEach(function (n) { n.parentNode.removeChild(n); });
  };

  Chart.prototype.refreshTip = function () {
    if (this.tip.hidden) return;
    if (this.point && this.lastPoint) this.showPoint(this.lastPoint.s, this.lastPoint.i);
    else if (this.cursor != null) this.cursorAt(this.cursor, this.lastPos);
  };

  Chart.prototype.placeTip = function (px, py) {
    var tip = this.tip, rect = this.svg.getBoundingClientRect();
    var scale = rect.width / this.box.w;
    var x = px * scale, y = py * scale;
    tip.hidden = false;
    var tw = tip.offsetWidth, th = tip.offsetHeight, w = this.plotEl.clientWidth;
    var left = x + 16;
    if (left + tw > w) left = x - 16 - tw;
    if (left < 0) left = Math.max(0, Math.min(w - tw, x - tw / 2));
    var top = Math.max(0, Math.min(y - th / 2, rect.height - th));
    tip.style.left = Math.round(left) + 'px';
    tip.style.top = Math.round(top) + 'px';
  };

  // cursorAt shows every series at time t: the crosshair snaps to the
  // nearest sample of the primary series.
  Chart.prototype.cursorAt = function (t, pos) {
    var self = this, s0 = this.primary;
    if (!s0) return;
    var visibleSeries = this.tipSeries.filter(function (s) { return !self.hidden[s.key]; });
    var i0 = nearestIndex(s0, t);
    if (i0 < 0) return;
    var at = self.hidden[s0.key] ? t : s0.x[i0];
    this.cursor = at;
    this.lastPos = pos;
    this.clearHover();

    var b = this.box, cx = this.X(at);
    if (cx < b.l - 1 || cx > b.r + 1) {
      this.tip.hidden = true;
      return;
    }
    svgEl('line', { x1: r1(cx), x2: r1(cx), y1: b.t, y2: b.b, 'class': 'crosshair hover' }, this.overlay);

    var tip = this.tip;
    tip.textContent = '';
    var head = htmlEl('div', 'tip-head', null, tip);
    htmlEl('b', null, valueLabel(this.d.x, at, 0), head);
    if (s0.f && !self.hidden[s0.key]) htmlEl('span', null, 'frame ' + Math.round(s0.f[i0]), head);
    // Series whose samples carry details (per-shot rungs) name the sample
    // once in the head and list their own fields on their row.
    var described = visibleSeries.filter(function (s) { return s.info; })[0];
    tip.classList.toggle('wide', !!described);
    if (described) {
      var di = nearestIndex(described, t);
      if (di >= 0 && described.info[di]) htmlEl('span', null, described.info[di].t, head);
    }

    var anchorY = null, separated = false;
    visibleSeries.forEach(function (s) {
      var i = nearestIndex(s, t);
      if (i < 0 || i >= s.x.length) return;
      if (s.tiponly && !separated) {
        separated = true;
        htmlEl('div', 'tip-sep', 'same frame', tip);
      }
      var row = htmlEl('div', 'tip-row', null, tip);
      var key = htmlEl('i', null, null, row);
      key.style.setProperty('--c', s.tiponly ? 'var(--muted)' : s.color);
      htmlEl('b', null, valueLabel(s.tiponly ? 'number' : self.d.y, s.y[i], s.digits), row);
      htmlEl('span', null, s.name, row);
      if (s.style !== 'step' && Math.abs(s.x[i] - at) > 5e-4) {
        var own = s.f ? 'frame ' + Math.round(s.f[i]) + ' at ' + clockMs(s.x[i]) : 'at ' + clockMs(s.x[i]);
        htmlEl('small', null, own, row);
      }
      if (s.info && s.info[i]) {
        htmlEl('small', null, (s.info[i].r || []).map(function (f) { return f[0] + ' ' + f[1]; }).join(' · '), row);
      }
      if (!s.tiponly) {
        var y = self.Y(s.y[i]);
        if (anchorY == null) anchorY = y;
        svgEl('circle', { cx: r1(self.X(s.x[i])), cy: r1(y), r: 4.5, 'class': 'hl hover', style: 'fill:' + s.color }, self.overlay);
      }
    });

    (this.d.bands || []).forEach(function (band) {
      if (at >= band.a && at <= band.b) {
        htmlEl('div', 'tip-band', band.l + ' · ' + clockMs(band.a) + ' – ' + clockMs(band.b), tip);
      }
    });

    this.placeTip(cx, pos ? pos.y : (anchorY == null ? (b.t + b.b) / 2 : anchorY));
  };

  // pointAt picks the drawn point nearest to the pointer (scatter charts).
  Chart.prototype.pointAt = function (px, py) {
    var self = this, best = null, bestD = 34 * 34;
    this.tipSeries.forEach(function (s) {
      if (self.hidden[s.key] || s.tiponly) return;
      for (var i = 0; i < s.x.length; i++) {
        var dx = self.X(s.x[i]) - px, dy = self.Y(s.y[i]) - py, d = dx * dx + dy * dy;
        if (d < bestD) {
          bestD = d;
          best = { s: s, i: i };
        }
      }
    });
    if (!best) {
      this.hideTip();
      return;
    }
    this.showPoint(best.s, best.i);
  };

  Chart.prototype.stepPoint = function (dir) {
    var self = this, pts = [];
    this.tipSeries.forEach(function (s) {
      if (self.hidden[s.key] || s.tiponly) return;
      for (var i = 0; i < s.x.length; i++) pts.push({ s: s, i: i, x: s.x[i], y: s.y[i] });
    });
    if (!pts.length) return;
    pts.sort(function (a, b) { return a.x - b.x || a.y - b.y; });
    var cur = -1;
    if (this.lastPoint) {
      for (var k = 0; k < pts.length; k++) if (pts[k].s === this.lastPoint.s && pts[k].i === this.lastPoint.i) cur = k;
    }
    var next = pts[(cur + dir + pts.length) % pts.length];
    this.showPoint(next.s, next.i);
  };

  Chart.prototype.showPoint = function (s, i) {
    var self = this, b = this.box;
    this.lastPoint = { s: s, i: i };
    this.clearHover();
    var cx = this.X(s.x[i]), cy = this.Y(s.y[i]);
    svgEl('line', { x1: r1(cx), x2: r1(cx), y1: r1(cy), y2: b.b, 'class': 'crosshair hover' }, this.overlay);
    svgEl('line', { x1: b.l, x2: r1(cx), y1: r1(cy), y2: r1(cy), 'class': 'crosshair hover' }, this.overlay);
    svgEl('circle', { cx: r1(cx), cy: r1(cy), r: 7, 'class': 'ring hover' }, this.overlay);

    var tip = this.tip;
    tip.textContent = '';
    var info = s.info && s.info[i];
    var head = htmlEl('div', 'tip-head', null, tip);
    var title = htmlEl('b', null, info ? info.t : s.name, head);
    title.style.fontFamily = 'inherit';
    if (info) {
      (info.r || []).forEach(function (f) {
        var row = htmlEl('div', 'tip-field', null, tip);
        htmlEl('span', null, f[0], row);
        htmlEl('b', null, f[1], row);
      });
    } else {
      [[self.d.x, s.x[i]], [self.d.y, s.y[i]]].forEach(function (pair, k) {
        var row = htmlEl('div', 'tip-field', null, tip);
        htmlEl('span', null, k ? 'y' : 'x', row);
        htmlEl('b', null, valueLabel(pair[0], pair[1], s.digits), row);
      });
    }
    this.placeTip(cx, cy);
  };

  /* ---------- timestamps: zoom every time chart on a range ---------- */

  function focusRange(t0, t1, target) {
    var list = timeCharts();
    if (!list.length) return;
    var span = Math.max(t1 - t0, 0);
    var padding = Math.max(span * 0.35, 2);
    list.forEach(function (c) {
      c.mark = [t0, t1];
      c.applyView(t0 - padding, t1 + padding);
    });

    var section = target && doc.getElementById(target);
    if (section && section.tagName === 'DETAILS') section.open = true;
    var fig = section ? section.querySelector('.chart') : list[0].fig;
    if (!fig) return;
    fig.scrollIntoView({ behavior: reduceMotion ? 'auto' : 'smooth', block: 'center' });
    // An instant (the worst frame) opens the tooltip on it.
    if (t1 - t0 < 1e-3) {
      list.forEach(function (c) { if (c.fig === fig) c.cursorAt(t0, null); });
    }
  }

  function bindTimestamp(a) {
    a.addEventListener('click', function (e) {
      e.preventDefault();
      var href = a.getAttribute('href') || '';
      focusRange(parseFloat(a.getAttribute('data-t0')), parseFloat(a.getAttribute('data-t1')), href.charAt(0) === '#' ? href.slice(1) : '');
      doc.querySelectorAll('tr.picked').forEach(function (tr) { tr.classList.remove('picked'); });
      var tr = a.closest('tr');
      if (tr) tr.classList.add('picked');
    });
  }

  /* ---------- tables ---------- */

  var UNITS = { 'Mb/s': 1e6, 'kb/s': 1e3, 'b/s': 1, 'MiB': 1048576, 'KiB': 1024 };

  // sortValue reads the number a cell shows: clocks, resolutions, rates,
  // sizes, percentages and plain numbers; NaN for text.
  function sortValue(text) {
    text = text.trim();
    if (text === '' || text === '–' || text === '-') return null;
    var m = /^(?:(\d+):)?(\d+):(\d+(?:\.\d+)?)$/.exec(text);
    if (m) return (+(m[1] || 0)) * 3600 + (+m[2]) * 60 + (+m[3]);
    m = /^(\d+)\s*[×x]\s*(\d+)/.exec(text);
    if (m) return (+m[1]) * (+m[2]);
    m = /^([-+]?\d*\.?\d+)\s*(Mb\/s|kb\/s|b\/s|MiB|KiB)/.exec(text);
    if (m) return (+m[1]) * UNITS[m[2]];
    m = /^[-+]?\d*\.?\d+/.exec(text);
    if (m) return +m[0];
    return NaN;
  }

  function enhanceTable(table) {
    var head = table.tHead && table.tHead.rows[0], body = table.tBodies[0];
    if (!head || !body) return;
    var rows = Array.prototype.slice.call(body.rows);
    rows.forEach(function (tr, i) { tr.dataset.order = i; });

    Array.prototype.forEach.call(head.cells, function (th, col) {
      // A column is numeric when every non-empty cell reads as a number.
      var values = rows.map(function (tr) { return tr.cells[col] ? sortValue(tr.cells[col].textContent) : null; });
      var numeric = values.some(function (v) { return v !== null && !isNaN(v); }) &&
        values.every(function (v) { return v === null || !isNaN(v); });
      if (!numeric && col > 0) {
        th.classList.add('txt');
        rows.forEach(function (tr) { if (tr.cells[col]) tr.cells[col].classList.add('txt'); });
      }
      if (rows.length < 2) return;

      var button = htmlEl('button', 'sort');
      button.type = 'button';
      while (th.firstChild) button.appendChild(th.firstChild);
      th.appendChild(button);
      button.addEventListener('click', function () {
        var dir = th.getAttribute('aria-sort') === 'ascending' ? -1 : 1;
        Array.prototype.forEach.call(head.cells, function (other) { other.removeAttribute('aria-sort'); });
        th.setAttribute('aria-sort', dir > 0 ? 'ascending' : 'descending');
        var keyed = Array.prototype.slice.call(body.rows).map(function (tr) {
          var t = tr.cells[col] ? tr.cells[col].textContent : '';
          return { tr: tr, v: numeric ? sortValue(t) : t.trim().toLowerCase(), o: +tr.dataset.order };
        });
        keyed.sort(function (a, b) {
          if (a.v === null && b.v === null) return a.o - b.o;
          if (a.v === null) return 1;
          if (b.v === null) return -1;
          var c = numeric ? a.v - b.v : a.v.localeCompare(b.v);
          return c ? c * dir : a.o - b.o;
        });
        keyed.forEach(function (k) { body.appendChild(k.tr); });
      });
    });

    rows.forEach(function (tr) {
      if (!tr.hasAttribute('data-t0')) return;
      var cell = tr.cells[+tr.getAttribute('data-link') || 0];
      var section = tr.closest('details');
      if (!cell || !section || !section.querySelector('.chart')) return;
      var a = htmlEl('a', 'ts');
      a.href = '#' + section.id;
      a.setAttribute('data-t0', tr.getAttribute('data-t0'));
      a.setAttribute('data-t1', tr.getAttribute('data-t1'));
      a.title = 'Show on the charts';
      while (cell.firstChild) a.appendChild(cell.firstChild);
      cell.appendChild(a);
      bindTimestamp(a);
    });
  }

  /* ---------- copy ---------- */

  function copyText(text, button) {
    var done = function () {
      if (button) {
        var label = button.textContent;
        button.textContent = 'Copied';
        button.classList.add('done');
        setTimeout(function () { button.textContent = label; button.classList.remove('done'); }, 1400);
      }
      toast('Copied to the clipboard');
    };
    var fallback = function () {
      var area = htmlEl('textarea', null, null, doc.body);
      area.value = text;
      area.setAttribute('readonly', '');
      area.style.position = 'fixed';
      area.style.opacity = '0';
      area.select();
      try {
        doc.execCommand('copy');
        done();
      } catch (err) {
        toast('Copy failed: select the text instead');
      }
      doc.body.removeChild(area);
    };
    if (navigator.clipboard && window.isSecureContext) {
      navigator.clipboard.writeText(text).then(done, fallback);
    } else {
      fallback();
    }
  }

  function enhanceCommands(box) {
    var cmds = box.querySelectorAll('.cmd');
    cmds.forEach(function (cmd) {
      var button = cmd.querySelector('.copy');
      button.hidden = false;
      button.setAttribute('aria-label', 'Copy this command');
      button.addEventListener('click', function () { copyText(cmd.querySelector('code').textContent, button); });
    });
    if (cmds.length > 1) {
      var head = htmlEl('div', 'commands-head');
      var all = htmlEl('button', 'copy-all', 'Copy all ' + cmds.length + ' commands', head);
      all.type = 'button';
      all.addEventListener('click', function () {
        copyText(Array.prototype.map.call(cmds, function (c) { return c.querySelector('code').textContent; }).join('\n'), all);
      });
      box.insertBefore(head, box.firstChild);
    }
  }

  /* ---------- theme, sections, navigation ---------- */

  function effectiveTheme() {
    var t = root.getAttribute('data-theme');
    if (t) return t;
    return window.matchMedia && window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';
  }

  function setupTools() {
    var theme = doc.querySelector('[data-action="theme"]');
    if (theme) {
      theme.hidden = false;
      theme.addEventListener('click', function () {
        var next = effectiveTheme() === 'dark' ? 'light' : 'dark';
        root.setAttribute('data-theme', next);
        try { localStorage.setItem('qc-theme', next); } catch (err) { /* storage may be disabled */ }
      });
    }

    var sections = doc.querySelectorAll('details.section');
    var collapse = doc.querySelector('[data-action="collapse"]');
    if (collapse && sections.length) {
      collapse.hidden = false;
      collapse.addEventListener('click', function () {
        var anyOpen = Array.prototype.some.call(sections, function (s) { return s.open; });
        sections.forEach(function (s) { s.open = !anyOpen; });
        root.classList.toggle('all-closed', anyOpen);
      });
    }

    doc.querySelectorAll('.navlinks a, a.card').forEach(function (a) {
      a.addEventListener('click', function () {
        var target = doc.getElementById((a.getAttribute('href') || '').slice(1));
        if (target && target.tagName === 'DETAILS') target.open = true;
      });
    });

    // A link straight to a collapsed section (report.html#s-encoding-commands)
    // opens it, on load and when the anchor changes.
    function openTarget() {
      var target = location.hash && doc.getElementById(location.hash.slice(1));
      if (target && target.tagName === 'DETAILS') target.open = true;
    }

    openTarget();
    window.addEventListener('hashchange', openTarget);

    if ('IntersectionObserver' in window) {
      var links = {};
      doc.querySelectorAll('.navlinks a').forEach(function (a) { links[a.getAttribute('href').slice(1)] = a; });
      var io = new IntersectionObserver(function (entries) {
        entries.forEach(function (entry) {
          var a = links[entry.target.id];
          if (!a || !entry.isIntersecting) return;
          Object.keys(links).forEach(function (k) { links[k].classList.remove('active'); });
          a.classList.add('active');
          var nav = a.parentNode;
          if (a.offsetLeft < nav.scrollLeft || a.offsetLeft + a.offsetWidth > nav.scrollLeft + nav.clientWidth) {
            nav.scrollLeft = a.offsetLeft - 16;
          }
        });
      }, { rootMargin: '-60px 0px -65% 0px' });
      Object.keys(links).forEach(function (id) {
        var el = doc.getElementById(id);
        if (el) io.observe(el);
      });
    }

    // Printing opens every section and uses the light theme.
    var printState = null;
    window.addEventListener('beforeprint', function () {
      printState = { theme: root.getAttribute('data-theme'), open: Array.prototype.map.call(sections, function (s) { return s.open; }) };
      root.setAttribute('data-theme', 'light');
      sections.forEach(function (s) { s.open = true; });
    });
    window.addEventListener('afterprint', function () {
      if (!printState) return;
      if (printState.theme) root.setAttribute('data-theme', printState.theme); else root.removeAttribute('data-theme');
      sections.forEach(function (s, i) { s.open = printState.open[i]; });
      printState = null;
    });
  }

  /* ---------- start ---------- */

  function start() {
    // Touching outside a chart closes its tooltip.
    doc.addEventListener('pointerdown', function (e) {
      charts.forEach(function (c) { if (!c.plotEl.contains(e.target)) c.hideTip(); });
    });

    doc.querySelectorAll('.table-wrap table').forEach(enhanceTable);
    doc.querySelectorAll('.commands').forEach(enhanceCommands);
    doc.querySelectorAll('.findings a.ts').forEach(bindTimestamp);
    setupTools();

    var figs = Array.prototype.slice.call(doc.querySelectorAll('figure.chart'));
    Promise.all(figs.map(function (fig) {
      return readData(fig).then(function (d) {
        charts.push(new Chart(fig, d));
      }).catch(function (err) {
        // A chart that cannot be enhanced keeps its static rendering.
        if (window.console) console.warn('qc: chart left static', err);
      });
    })).then(function () {
      // Keep page order: linked zoom and timestamps walk the list.
      charts.sort(function (a, b) { return figs.indexOf(a.fig) - figs.indexOf(b.fig); });
      watchCharts();
    });
  }

  function watchCharts() {
    var hint = doc.querySelector('.js-hint');
    if (hint && charts.length) hint.hidden = false;

    // Re-render charts at their new size; collapsed sections have no width
    // until opened.
    var pending = false;
    var rerender = function () {
      if (pending) return;
      pending = true;
      requestAnimationFrame(function () {
        pending = false;
        charts.forEach(function (c) {
          var w = Math.round(c.plotEl.clientWidth);
          if (w && w !== c.box.w) {
            c.render();
            c.hideTip();
          }
        });
      });
    };
    if ('ResizeObserver' in window) {
      var ro = new ResizeObserver(rerender);
      charts.forEach(function (c) { ro.observe(c.plotEl); });
    } else {
      window.addEventListener('resize', rerender);
    }
    doc.querySelectorAll('details.section').forEach(function (d) { d.addEventListener('toggle', rerender); });
  }

  if (doc.readyState === 'loading') doc.addEventListener('DOMContentLoaded', start);
  else start();
})();
