// charts.js — uPlot-based chart rendering for Stresstool web UI

const CHART_COLORS = ['#4ec9b0', '#9cdcfe', '#ce9178', '#c586c0', '#dcdcaa', '#569cd6', '#f44747', '#6a9955'];
const GRID_COLOR = '#3c3c3c';
const AXIS_COLOR = '#808080';
const AXIS_FONT = '11px monospace';

function chartThemeAxes(label) {
  return [
    {
      stroke: AXIS_COLOR,
      grid: { stroke: GRID_COLOR, width: 1 },
      ticks: { stroke: GRID_COLOR, width: 1 },
      font: AXIS_FONT,
    },
    {
      stroke: AXIS_COLOR,
      grid: { stroke: GRID_COLOR, width: 1 },
      ticks: { stroke: GRID_COLOR, width: 1 },
      font: AXIS_FONT,
      label: label,
      labelFont: '12px monospace',
      labelSize: 20,
    }
  ];
}

// Destroy any existing chart inside a container before rendering a new one
function clearContainer(el) {
  while (el.firstChild) el.removeChild(el.firstChild);
}

// ─── 1. RPS over time (line chart) ────────────────────────────────────────────

function renderRPSChart(seriesData, container) {
  clearContainer(container);
  if (!seriesData || Object.keys(seriesData).length === 0) return;

  var title = document.createElement('h3');
  title.textContent = 'Requests per Second';
  title.className = 'chart-title';
  container.appendChild(title);

  // Build aligned data: x-axis = elapsed seconds, one series per node/test
  var allTimes = new Set();
  var seriesNames = [];
  var seriesMap = {};

  for (var node in seriesData) {
    for (var test in seriesData[node]) {
      var key = node + ' / ' + test;
      seriesNames.push(key);
      seriesMap[key] = {};
      var points = seriesData[node][test];
      for (var i = 0; i < points.length; i++) {
        var t = Math.round(points[i].elapsed_s);
        allTimes.add(t);
        seriesMap[key][t] = points[i].rps;
      }
    }
  }

  var times = Array.from(allTimes).sort(function(a, b) { return a - b; });
  if (times.length === 0) return;

  var data = [new Float64Array(times)];
  for (var si = 0; si < seriesNames.length; si++) {
    var vals = new Float64Array(times.length);
    for (var ti = 0; ti < times.length; ti++) {
      var v = seriesMap[seriesNames[si]][times[ti]];
      vals[ti] = v != null ? v : 0;
    }
    data.push(vals);
  }

  var series = [{ label: 'Elapsed (s)' }];
  for (var si = 0; si < seriesNames.length; si++) {
    series.push({
      label: seriesNames[si],
      stroke: CHART_COLORS[si % CHART_COLORS.length],
      width: 2,
    });
  }

  var opts = {
    width: container.clientWidth - 24,
    height: 250,
    series: series,
    axes: chartThemeAxes('RPS'),
    cursor: { show: true },
    scales: { x: { time: false } },
  };

  new uPlot(opts, data, container);
}

// ─── 2. Latency percentiles (bar chart) ──────────────────────────────────────

function renderLatencyChart(resultsData, container) {
  clearContainer(container);
  if (!resultsData || Object.keys(resultsData).length === 0) return;

  var title = document.createElement('h3');
  title.textContent = 'Latency Percentiles (ms)';
  title.className = 'chart-title';
  container.appendChild(title);

  // Gather labels and percentile data
  var labels = [];
  var p50 = [], p95 = [], p99 = [];
  for (var node in resultsData) {
    for (var test in resultsData[node]) {
      labels.push(node + ' / ' + test);
      var lat = resultsData[node][test].latency_ms;
      p50.push(lat.p50_ms);
      p95.push(lat.p95_ms);
      p99.push(lat.p99_ms);
    }
  }

  // Render as styled HTML bars (simpler and more readable for categorical data)
  var wrap = document.createElement('div');
  wrap.className = 'bar-chart';
  for (var i = 0; i < labels.length; i++) {
    var group = document.createElement('div');
    group.className = 'bar-group';
    var lbl = document.createElement('div');
    lbl.className = 'bar-label';
    lbl.textContent = labels[i];
    group.appendChild(lbl);

    var maxVal = Math.max(p99[i], 1);
    appendBar(group, 'P50', p50[i], maxVal, CHART_COLORS[0]);
    appendBar(group, 'P95', p95[i], maxVal, CHART_COLORS[1]);
    appendBar(group, 'P99', p99[i], maxVal, CHART_COLORS[2]);
    wrap.appendChild(group);
  }
  container.appendChild(wrap);
}

function appendBar(parent, label, value, max, color) {
  var row = document.createElement('div');
  row.className = 'bar-row';
  var tag = document.createElement('span');
  tag.className = 'bar-tag';
  tag.textContent = label;
  row.appendChild(tag);
  var track = document.createElement('div');
  track.className = 'bar-track';
  var fill = document.createElement('div');
  fill.className = 'bar-fill';
  fill.style.width = Math.max((value / max) * 100, 2) + '%';
  fill.style.background = color;
  track.appendChild(fill);
  row.appendChild(track);
  var val = document.createElement('span');
  val.className = 'bar-value';
  val.textContent = value.toFixed(1) + ' ms';
  row.appendChild(val);
  parent.appendChild(row);
}

// ─── 3. Status code distribution ─────────────────────────────────────────────

function statusCodeColor(code) {
  var n = parseInt(code, 10);
  if (n >= 200 && n < 300) return '#4ec9b0';
  if (n >= 300 && n < 400) return '#569cd6';
  if (n >= 400 && n < 500) return '#ce9178';
  return '#f44747';
}

function renderStatusCodeChart(resultsData, container) {
  clearContainer(container);
  if (!resultsData || Object.keys(resultsData).length === 0) return;

  var title = document.createElement('h3');
  title.textContent = 'Status Codes';
  title.className = 'chart-title';
  container.appendChild(title);

  // Aggregate status codes across all nodes/tests
  var totals = {};
  for (var node in resultsData) {
    for (var test in resultsData[node]) {
      var sc = resultsData[node][test].status_codes;
      for (var code in sc) {
        totals[code] = (totals[code] || 0) + sc[code];
      }
    }
  }

  var codes = Object.keys(totals).sort();
  if (codes.length === 0) return;

  var maxVal = 0;
  for (var i = 0; i < codes.length; i++) {
    if (totals[codes[i]] > maxVal) maxVal = totals[codes[i]];
  }

  var wrap = document.createElement('div');
  wrap.className = 'bar-chart';
  for (var i = 0; i < codes.length; i++) {
    var row = document.createElement('div');
    row.className = 'bar-row';
    var tag = document.createElement('span');
    tag.className = 'bar-tag';
    tag.textContent = codes[i];
    row.appendChild(tag);
    var track = document.createElement('div');
    track.className = 'bar-track';
    var fill = document.createElement('div');
    fill.className = 'bar-fill';
    fill.style.width = Math.max((totals[codes[i]] / maxVal) * 100, 2) + '%';
    fill.style.background = statusCodeColor(codes[i]);
    track.appendChild(fill);
    row.appendChild(track);
    var val = document.createElement('span');
    val.className = 'bar-value';
    val.textContent = totals[codes[i]].toLocaleString();
    row.appendChild(val);
    wrap.appendChild(row);
  }
  container.appendChild(wrap);
}

// ─── 4. Error breakdown ──────────────────────────────────────────────────────

function renderErrorChart(resultsData, container) {
  clearContainer(container);
  if (!resultsData || Object.keys(resultsData).length === 0) return;

  var title = document.createElement('h3');
  title.textContent = 'Errors';
  title.className = 'chart-title';
  container.appendChild(title);

  // Aggregate errors across all nodes/tests
  var totals = {};
  for (var node in resultsData) {
    for (var test in resultsData[node]) {
      var errs = resultsData[node][test].errors;
      if (!errs) continue;
      for (var msg in errs) {
        totals[msg] = (totals[msg] || 0) + errs[msg];
      }
    }
  }

  var keys = Object.keys(totals);
  if (keys.length === 0) {
    var noErr = document.createElement('div');
    noErr.className = 'chart-empty';
    noErr.textContent = 'No errors recorded.';
    container.appendChild(noErr);
    return;
  }

  // Sort descending by count
  keys.sort(function(a, b) { return totals[b] - totals[a]; });

  var maxVal = totals[keys[0]];
  var wrap = document.createElement('div');
  wrap.className = 'bar-chart';
  for (var i = 0; i < keys.length; i++) {
    var row = document.createElement('div');
    row.className = 'bar-row';
    var tag = document.createElement('span');
    tag.className = 'bar-tag bar-tag-wide';
    tag.textContent = keys[i].length > 40 ? keys[i].substring(0, 37) + '...' : keys[i];
    tag.title = keys[i];
    row.appendChild(tag);
    var track = document.createElement('div');
    track.className = 'bar-track';
    var fill = document.createElement('div');
    fill.className = 'bar-fill';
    fill.style.width = Math.max((totals[keys[i]] / maxVal) * 100, 2) + '%';
    fill.style.background = '#f44747';
    track.appendChild(fill);
    row.appendChild(track);
    var val = document.createElement('span');
    val.className = 'bar-value';
    val.textContent = totals[keys[i]].toLocaleString();
    row.appendChild(val);
    wrap.appendChild(row);
  }
  container.appendChild(wrap);
}
