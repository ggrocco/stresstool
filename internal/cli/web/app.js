let configLoaded = false;

async function refreshNodes() {
  try {
    const res = await fetch('/api/nodes');
    const data = await res.json();
    const div = document.getElementById('nodes-list');
    while (div.firstChild) div.removeChild(div.firstChild);
    if (!data.nodes || data.nodes.length === 0) {
      const span = document.createElement('span');
      span.className = 'no-nodes';
      span.textContent = 'No nodes connected yet\u2026';
      div.appendChild(span);
    } else {
      for (const n of data.nodes) {
        const row = document.createElement('div');
        row.className = 'node';
        row.appendChild(document.createTextNode('\u2713 ' + (n.name || '')));
        if (n.addr) {
          const addr = document.createElement('span');
          addr.className = 'node-addr';
          addr.textContent = n.addr;
          row.appendChild(document.createTextNode(' '));
          row.appendChild(addr);
        }
        div.appendChild(row);
      }
    }
    await syncControlsForRunState();
  } catch(e) {
    document.getElementById('status').textContent = 'Error fetching nodes: ' + e.message;
  }
}

async function checkConfigLoaded() {
  try {
    const res = await fetch('/api/config');
    const text = await res.text();
    configLoaded = text.trim().length > 0;
  } catch(e) {
    configLoaded = false;
  }
}

async function syncControlsForRunState() {
  try {
    const res = await fetch('/api/run-status');
    const data = await res.json();
    const active = data.run_active === true;
    const startBtn = document.getElementById('start-btn');
    const stopBtn = document.getElementById('stop-btn');
    const checkBtn = document.getElementById('check-btn');
    const saveBtn = document.getElementById('save-config-btn');
    const nodesRes = await fetch('/api/nodes');
    const nodesData = await nodesRes.json();
    const hasNodes = nodesData.nodes && nodesData.nodes.length > 0;
    stopBtn.disabled = !active;
    checkBtn.disabled = active;
    startBtn.disabled = active || !hasNodes || !configLoaded;
    if (saveBtn) saveBtn.disabled = active;
    return active;
  } catch (e) {
    return false;
  }
}

async function startTests() {
  const btn = document.getElementById('start-btn');
  const stopBtn = document.getElementById('stop-btn');
  const checkBtn = document.getElementById('check-btn');
  const status = document.getElementById('status');
  btn.disabled = true;
  stopBtn.disabled = false;
  checkBtn.disabled = true;
  status.textContent = 'Sending start signal\u2026';
  try {
    const res = await fetch('/api/start', { method: 'POST' });
    const data = await res.json();
    if (data.ok) {
      status.textContent = 'Tests started!';
    } else {
      status.textContent = 'Error: ' + (data.error || 'unknown');
      await syncControlsForRunState();
    }
  } catch(e) {
    status.textContent = 'Error: ' + e.message;
    await syncControlsForRunState();
  }
}

async function stopTests() {
  const status = document.getElementById('status');
  status.textContent = 'Sending stop signal\u2026';
  try {
    const res = await fetch('/api/stop', { method: 'POST' });
    const data = await res.json();
    if (data.ok) {
      status.textContent = 'Stop sent to ' + (data.nodes || 0) + ' node(s).';
    } else {
      status.textContent = 'Error: ' + (data.error || 'unknown');
    }
  } catch(e) {
    status.textContent = 'Error: ' + e.message;
  }
  await syncControlsForRunState();
}

async function exitController() {
  if (!confirm('Shut down the controller? Nodes will disconnect (same as a non-web run finishing).')) {
    return;
  }
  const status = document.getElementById('status');
  status.textContent = 'Sending exit\u2026';
  try {
    const res = await fetch('/api/exit', { method: 'POST' });
    const data = await res.json();
    if (data.ok) {
      status.textContent = 'Exit sent. This page will stop responding shortly.';
    } else {
      status.textContent = 'Error: ' + (data.error || 'unknown');
    }
  } catch (e) {
    status.textContent = 'Error: ' + e.message;
  }
}

async function toggleConfig() {
  const panel = document.getElementById('config-panel');
  const btn = document.getElementById('config-toggle');
  if (!panel.classList.contains('hidden')) {
    panel.classList.add('hidden');
    btn.textContent = 'Edit Config';
    return;
  }
  try {
    const res = await fetch('/api/config');
    const text = await res.text();
    document.getElementById('config-editor').value = text;
    panel.classList.remove('hidden');
    btn.textContent = 'Hide Config';
  } catch(e) {
    document.getElementById('config-status').textContent = 'Error loading config: ' + e.message;
    panel.classList.remove('hidden');
    btn.textContent = 'Hide Config';
  }
}

async function saveConfig() {
  const editor = document.getElementById('config-editor');
  const configStatus = document.getElementById('config-status');
  const yaml = editor.value;
  if (!yaml.trim()) {
    configStatus.textContent = 'Config cannot be empty';
    configStatus.className = 'config-status-error';
    return;
  }
  configStatus.textContent = 'Saving\u2026';
  configStatus.className = '';
  try {
    const res = await fetch('/api/config', {
      method: 'POST',
      headers: { 'Content-Type': 'text/plain' },
      body: yaml
    });
    const data = await res.json();
    if (data.ok) {
      configStatus.textContent = 'Saved (' + data.tests + ' test(s))';
      configStatus.className = 'config-status-ok';
      configLoaded = true;
      await syncControlsForRunState();
    } else {
      configStatus.textContent = 'Error: ' + (data.error || 'unknown');
      configStatus.className = 'config-status-error';
    }
  } catch(e) {
    configStatus.textContent = 'Error: ' + e.message;
    configStatus.className = 'config-status-error';
  }
}

let wasRunActive = false;

async function refreshCharts() {
  try {
    const statusRes = await fetch('/api/run-status');
    const statusData = await statusRes.json();
    const active = statusData.run_active === true;
    const panel = document.getElementById('charts-panel');

    if (active) {
      // While running, show real-time RPS chart from progress series
      wasRunActive = true;
      panel.classList.remove('hidden');
      const psRes = await fetch('/api/progress-series');
      const psData = await psRes.json();
      if (psData.series && Object.keys(psData.series).length > 0) {
        renderRPSChart(psData.series, document.getElementById('chart-rps'));
      }
    } else if (wasRunActive) {
      // Run just finished — fetch final results and full progress series
      wasRunActive = false;
      panel.classList.remove('hidden');
      const [psRes, resRes] = await Promise.all([
        fetch('/api/progress-series'),
        fetch('/api/results')
      ]);
      const psData = await psRes.json();
      const resData = await resRes.json();

      if (psData.series && Object.keys(psData.series).length > 0) {
        renderRPSChart(psData.series, document.getElementById('chart-rps'));
      }
      if (resData.results && Object.keys(resData.results).length > 0) {
        renderLatencyChart(resData.results, document.getElementById('chart-latency'));
        renderStatusCodeChart(resData.results, document.getElementById('chart-status-codes'));
        renderErrorChart(resData.results, document.getElementById('chart-errors'));
      }
    }
  } catch(e) { /* ignore */ }
}

let logOffset = 0;

async function refreshLogs() {
  try {
    const res = await fetch('/api/logs?offset=' + logOffset);
    const data = await res.json();
    if (data.lines && data.lines.length > 0) {
      const pre = document.getElementById('logs-content');
      pre.textContent += data.lines.join('\n') + '\n';
      logOffset = data.total;
      const panel = document.getElementById('logs-panel');
      panel.scrollTop = panel.scrollHeight;
    }
  } catch(e) { /* ignore */ }
}

checkConfigLoaded();
refreshNodes();
refreshLogs();
syncControlsForRunState();
setInterval(refreshNodes, 2000);
setInterval(refreshLogs, 1000);
setInterval(syncControlsForRunState, 1500);
setInterval(checkConfigLoaded, 3000);
setInterval(refreshCharts, 2000);
