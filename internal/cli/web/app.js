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
      div.innerHTML = data.nodes.map(n => '<div class="node">&#10003; ' + n + '</div>').join('');
      btn.disabled = false;
    }
  } catch(e) {
    document.getElementById('status').textContent = 'Error fetching nodes: ' + e.message;
  }
}

async function startTests() {
  const btn = document.getElementById('start-btn');
  const checkBtn = document.getElementById('check-btn');
  const status = document.getElementById('status');
  btn.disabled = true;
  checkBtn.disabled = true;
  status.textContent = 'Sending start signal\u2026';
  try {
    const res = await fetch('/api/start', { method: 'POST' });
    const data = await res.json();
    if (data.ok) {
      status.textContent = 'Tests started! Monitor progress in the controller terminal.';
    } else {
      status.textContent = 'Error: ' + (data.error || 'unknown');
      btn.disabled = false;
      checkBtn.disabled = false;
    }
  } catch(e) {
    status.textContent = 'Error: ' + e.message;
    btn.disabled = false;
    checkBtn.disabled = false;
  }
}

refreshNodes();
setInterval(refreshNodes, 2000);
