async function refreshNodes() {
  try {
    const res = await fetch('/api/nodes');
    const data = await res.json();
    const div = document.getElementById('nodes-list');
    if (!data.nodes || data.nodes.length === 0) {
      div.innerHTML = '<span class="no-nodes">No nodes connected yet\u2026</span>';
    } else {
      div.innerHTML = data.nodes.map(n => '<div class="node">&#10003; ' + n.name + (n.addr ? ' <span class="node-addr">' + n.addr + '</span>' : '') + '</div>').join('');
    }
    await syncControlsForRunState();
  } catch(e) {
    document.getElementById('status').textContent = 'Error fetching nodes: ' + e.message;
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
    const nodesRes = await fetch('/api/nodes');
    const nodesData = await nodesRes.json();
    const hasNodes = nodesData.nodes && nodesData.nodes.length > 0;
    stopBtn.disabled = !active;
    checkBtn.disabled = active;
    startBtn.disabled = active || !hasNodes;
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
    btn.textContent = 'Show Config';
    return;
  }
  try {
    const res = await fetch('/api/config');
    const text = await res.text();
    document.getElementById('config-content').textContent = text;
    panel.classList.remove('hidden');
    btn.textContent = 'Hide Config';
  } catch(e) {
    document.getElementById('config-content').textContent = 'Error loading config: ' + e.message;
    panel.classList.remove('hidden');
    btn.textContent = 'Hide Config';
  }
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

refreshNodes();
refreshLogs();
syncControlsForRunState();
setInterval(refreshNodes, 2000);
setInterval(refreshLogs, 1000);
setInterval(syncControlsForRunState, 1500);
