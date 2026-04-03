async function refreshNodes() {
  try {
    const res = await fetch('/api/nodes');
    const data = await res.json();
    const div = document.getElementById('nodes-list');
    const btn = document.getElementById('start-btn');
    if (!data.nodes || data.nodes.length === 0) {
      div.innerHTML = '<span class="no-nodes">No nodes connected yet\u2026</span>';
      btn.disabled = true;
    } else {
      div.innerHTML = data.nodes.map(n => '<div class="node">&#10003; ' + n.name + (n.addr ? ' <span class="node-addr">' + n.addr + '</span>' : '') + '</div>').join('');
      btn.disabled = false;
    }
  } catch(e) {
    document.getElementById('status').textContent = 'Error fetching nodes: ' + e.message;
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
      btn.disabled = false;
      stopBtn.disabled = true;
      checkBtn.disabled = false;
    }
  } catch(e) {
    status.textContent = 'Error: ' + e.message;
    btn.disabled = false;
    stopBtn.disabled = true;
    checkBtn.disabled = false;
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
setInterval(refreshNodes, 2000);
setInterval(refreshLogs, 1000);
