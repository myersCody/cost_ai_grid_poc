package ingest

import (
	"net/http"
)

const gorulesDemo = `<!DOCTYPE html>
<html>
<head>
<title>GoRules Rating Demo</title>
<style>
  * { box-sizing: border-box; margin: 0; padding: 0; }
  body { font-family: -apple-system, system-ui, sans-serif; background: #f5f7fa; color: #333; }
  .header { background: #1a3a5c; color: white; padding: 16px 24px; display: flex; justify-content: space-between; align-items: center; }
  .header h1 { font-size: 1.3rem; }
  .header .status { font-size: 0.9rem; opacity: 0.8; }
  .container { max-width: 1200px; margin: 0 auto; padding: 20px; }
  .grid { display: grid; grid-template-columns: 1fr 1fr; gap: 20px; margin-bottom: 20px; }
  .card { background: white; border-radius: 8px; padding: 20px; box-shadow: 0 1px 4px rgba(0,0,0,0.1); }
  .card h2 { font-size: 1.1rem; color: #1a3a5c; margin-bottom: 12px; border-bottom: 2px solid #e0e6ed; padding-bottom: 8px; }
  .full { grid-column: 1 / -1; }
  button { padding: 8px 16px; border: none; border-radius: 4px; cursor: pointer; font-size: 0.9rem; margin: 4px; }
  .btn-primary { background: #2c6fad; color: white; }
  .btn-primary:hover { background: #1a5a8e; }
  .btn-success { background: #16a34a; color: white; }
  .btn-success:hover { background: #15803d; }
  .btn-warning { background: #d97706; color: white; }
  .btn-warning:hover { background: #b45309; }
  .log { background: #1e293b; color: #e2e8f0; padding: 12px; border-radius: 4px; font-family: monospace; font-size: 0.85rem; max-height: 300px; overflow-y: auto; white-space: pre-wrap; }
  .stage { display: flex; align-items: center; gap: 12px; padding: 10px 0; border-bottom: 1px solid #f0f0f0; }
  .stage:last-child { border-bottom: none; }
  .stage-num { width: 28px; height: 28px; border-radius: 50%; background: #e0e6ed; display: flex; align-items: center; justify-content: center; font-weight: bold; font-size: 0.85rem; flex-shrink: 0; }
  .stage-num.done { background: #16a34a; color: white; }
  .stage-num.active { background: #2c6fad; color: white; }
  .stage-desc { flex: 1; }
  .stage-desc .title { font-weight: 600; }
  .stage-desc .detail { font-size: 0.85rem; color: #666; }
  table { width: 100%; border-collapse: collapse; font-size: 0.9rem; }
  th { background: #f0f4f8; text-align: left; padding: 8px; border-bottom: 2px solid #d0d7e0; }
  td { padding: 8px; border-bottom: 1px solid #e8ecf0; }
  .gold { background: #fef3c7; }
  .cost { font-family: monospace; font-weight: 600; }
  .decision-table { font-size: 0.85rem; }
  .decision-table th { background: #1a3a5c; color: white; }
  .config { margin-top: 12px; }
  .config label { font-size: 0.85rem; font-weight: 600; }
  .config input { padding: 4px 8px; border: 1px solid #d0d7e0; border-radius: 4px; font-size: 0.85rem; width: 200px; margin: 4px; }
</style>
</head>
<body>
<div class="header">
  <h1>GoRules Rating Demo — Instance-Type Pricing with Tenant Discounts</h1>
  <div class="status" id="status">Ready</div>
</div>
<div class="container">

<div class="config card">
  <label>OSAC REST Gateway:</label>
  <input type="text" id="osac-url" value="http://localhost:8011" />
  <label>Token:</label>
  <input type="text" id="osac-token" placeholder="paste OSAC token" style="width:300px" />
</div>

<div class="grid">
  <div class="card">
    <h2>Demo Stages</h2>
    <div id="stages">
      <div class="stage" id="stage-1">
        <div class="stage-num">1</div>
        <div class="stage-desc">
          <div class="title">Trigger Reconciliation</div>
          <div class="detail">Sync VMs and tenants from OSAC</div>
        </div>
        <button class="btn-primary" onclick="runStage(1)">Run</button>
      </div>
      <div class="stage" id="stage-2">
        <div class="stage-num">2</div>
        <div class="stage-desc">
          <div class="title">Set Tenant Tiers via OSAC</div>
          <div class="detail">PATCH tenant labels in OSAC, then reconcile</div>
        </div>
        <button class="btn-warning" onclick="runStage(2)">Run</button>
      </div>
      <div class="stage" id="stage-3">
        <div class="stage-num">3</div>
        <div class="stage-desc">
          <div class="title">Show Pipeline Summary</div>
          <div class="detail">Check current metering + cost entry counts</div>
        </div>
        <button class="btn-primary" onclick="runStage(3)">Run</button>
      </div>
      <div class="stage" id="stage-4">
        <div class="stage-num">4</div>
        <div class="stage-desc">
          <div class="title">Cost Report by Tenant</div>
          <div class="detail">Compare gold vs standard tenant costs</div>
        </div>
        <button class="btn-success" onclick="runStage(4)">Run</button>
      </div>
      <div class="stage" id="stage-5">
        <div class="stage-num">5</div>
        <div class="stage-desc">
          <div class="title">Cost Report by Resource</div>
          <div class="detail">Per-VM costs with instance type pricing</div>
        </div>
        <button class="btn-success" onclick="runStage(5)">Run</button>
      </div>
    </div>
  </div>

  <div class="card">
    <h2>Decision Table: compute-pricing.json</h2>
    <table class="decision-table">
      <thead>
        <tr><th>Instance Type</th><th>Tenant Tier</th><th>$/Hour</th><th>Discount</th></tr>
      </thead>
      <tbody>
        <tr><td>standard-2-8</td><td class="gold">gold</td><td>$0.10</td><td>20%</td></tr>
        <tr><td>standard-2-8</td><td>(any)</td><td>$0.10</td><td>0%</td></tr>
        <tr><td>standard-4-16</td><td class="gold">gold</td><td>$0.20</td><td>20%</td></tr>
        <tr><td>standard-4-16</td><td>(any)</td><td>$0.20</td><td>0%</td></tr>
        <tr><td>standard-8-32</td><td class="gold">gold</td><td>$0.40</td><td>20%</td></tr>
        <tr><td>standard-8-32</td><td>(any)</td><td>$0.40</td><td>0%</td></tr>
        <tr><td>(any)</td><td>(any)</td><td>$0.10</td><td>0%</td></tr>
      </tbody>
    </table>
    <p style="margin-top:12px; font-size:0.85rem; color:#666;">
      JSON file, not code. Edit, restart, pricing changes. No PR needed.
    </p>
  </div>
</div>

<div class="grid">
  <div class="card" id="results-card" style="display:none">
    <h2 id="results-title">Results</h2>
    <div id="results"></div>
  </div>
  <div class="card full">
    <h2>Log</h2>
    <div class="log" id="log">Ready. Click a stage to begin.
</div>
  </div>
</div>

</div>

<script>
const COST_API = window.location.origin;
const logEl = document.getElementById('log');

function osacUrl() { return document.getElementById('osac-url').value; }
function osacToken() { return document.getElementById('osac-token').value; }

function addLog(msg) {
  const ts = new Date().toLocaleTimeString();
  logEl.textContent += ts + '  ' + msg + '\n';
  logEl.scrollTop = logEl.scrollHeight;
}

function setStatus(msg) {
  document.getElementById('status').textContent = msg;
}

function markStage(n, state) {
  document.querySelector('#stage-' + n + ' .stage-num').className = 'stage-num ' + state;
}

async function costApi(method, path, body) {
  const opts = { method, headers: {} };
  if (body) { opts.headers['Content-Type'] = 'application/json'; opts.body = JSON.stringify(body); }
  const resp = await fetch(COST_API + path, opts);
  const text = await resp.text();
  try { return JSON.parse(text); } catch { return text; }
}

async function osacApi(method, path, body) {
  const token = osacToken();
  if (!token) throw new Error('Paste OSAC token first');
  const opts = { method, headers: { 'Authorization': 'Bearer ' + token } };
  if (body) { opts.headers['Content-Type'] = 'application/json'; opts.body = JSON.stringify(body); }
  const resp = await fetch(osacUrl() + path, opts);
  const text = await resp.text();
  try { return JSON.parse(text); } catch { return text; }
}

function showResults(title, html) {
  document.getElementById('results-card').style.display = 'block';
  document.getElementById('results-title').textContent = title;
  document.getElementById('results').innerHTML = html;
}

function renderTable(data, columns) {
  let html = '<table><thead><tr>';
  for (const col of columns) html += '<th>' + col.label + '</th>';
  html += '</tr></thead><tbody>';
  for (const row of data) {
    html += '<tr>';
    for (const col of columns) {
      let val = row[col.key];
      if (col.fmt) val = col.fmt(val);
      const cls = col.cls ? col.cls(row) : '';
      html += '<td class="' + cls + '">' + (val != null ? val : '') + '</td>';
    }
    html += '</tr>';
  }
  html += '</tbody></table>';
  return html;
}

async function runStage(n) {
  markStage(n, 'active');
  setStatus('Running stage ' + n + '...');

  try {
    switch(n) {
    case 1:
      addLog('Triggering reconciliation...');
      await costApi('POST', '/api/v1/reconcile');
      addLog('Reconciliation triggered. Waiting 5s for sync...');
      await new Promise(r => setTimeout(r, 5000));
      const summary = await costApi('GET', '/api/v1/reports/summary');
      addLog('Live VMs: ' + summary.live_vms + ', Clusters: ' + summary.live_clusters);
      showResults('Pipeline Status', renderTable([summary], [
        {key: 'raw_events', label: 'Raw Events'},
        {key: 'metering_entries', label: 'Metering'},
        {key: 'cost_entries', label: 'Costs'},
        {key: 'rates', label: 'Rates'},
        {key: 'live_vms', label: 'VMs'},
        {key: 'live_clusters', label: 'Clusters'},
      ]));
      break;

    case 2:
      addLog('Listing tenants from OSAC...');
      const tenantList = await osacApi('GET', '/api/fulfillment/v1/tenants');
      const tenants = tenantList.items || [];
      addLog('Found ' + tenants.length + ' tenants');

      if (tenants.length > 0) {
        const target = tenants[0];
        addLog('Setting tier=gold on tenant "' + target.metadata.name + '" (' + target.id + ') via OSAC PATCH...');
        const updated = Object.assign({}, target);
        if (!updated.metadata.labels) updated.metadata.labels = {};
        updated.metadata.labels['cost-mgmt/tier'] = 'gold';
        await osacApi('PATCH', '/api/fulfillment/v1/tenants/' + target.id, updated);
        addLog('OSAC tenant updated with label cost-mgmt/tier=gold');

        addLog('Triggering reconciliation to sync labels...');
        await costApi('POST', '/api/v1/reconcile');
        await new Promise(r => setTimeout(r, 3000));
        addLog('Reconciliation complete. Tenant tier synced to local inventory.');
      }

      showResults('Tenants', renderTable(tenants.map(t => ({
        id: t.id,
        name: t.metadata.name,
        tier: (t.metadata.labels && t.metadata.labels['cost-mgmt/tier']) || 'standard',
      })), [
        {key: 'name', label: 'Tenant'},
        {key: 'tier', label: 'Tier', cls: r => r.tier === 'gold' ? 'gold' : ''},
        {key: 'id', label: 'ID'},
      ]));
      break;

    case 3:
      addLog('Fetching pipeline summary...');
      const sum = await costApi('GET', '/api/v1/reports/summary');
      addLog('Metering: ' + sum.metering_entries + ', Cost: ' + sum.cost_entries);
      showResults('Pipeline Summary', renderTable([sum], [
        {key: 'raw_events', label: 'Raw Events'},
        {key: 'metering_entries', label: 'Metering'},
        {key: 'cost_entries', label: 'Costs'},
        {key: 'live_vms', label: 'VMs'},
      ]));
      break;

    case 4:
      addLog('Fetching cost report by tenant...');
      const byTenant = await costApi('GET', '/api/v1/reports/costs?group_by=tenant');
      if (byTenant.data) {
        showResults('Cost by Tenant', renderTable(byTenant.data, [
          {key: 'group', label: 'Tenant'},
          {key: 'entries', label: 'Entries'},
          {key: 'cost', label: 'Total Cost', fmt: v => '$' + v.toFixed(4), cls: () => 'cost'},
          {key: 'infrastructure_cost', label: 'Infra', fmt: v => '$' + v.toFixed(4), cls: () => 'cost'},
          {key: 'supplementary_cost', label: 'Suppl.', fmt: v => '$' + v.toFixed(4), cls: () => 'cost'},
        ]));
        addLog('Gold tenant should show lower per-unit cost (20% discount)');
      }
      break;

    case 5:
      addLog('Fetching cost report by resource...');
      const byResource = await costApi('GET', '/api/v1/reports/costs?group_by=resource');
      if (byResource.data) {
        showResults('Cost by Resource', renderTable(byResource.data, [
          {key: 'group', label: 'Resource ID'},
          {key: 'entries', label: 'Entries'},
          {key: 'cost', label: 'Total Cost', fmt: v => '$' + v.toFixed(6), cls: () => 'cost'},
          {key: 'currency', label: 'Currency'},
        ]));
        addLog('Different instance types = different costs per resource');
      }
      break;
    }

    markStage(n, 'done');
    setStatus('Stage ' + n + ' complete');
  } catch(e) {
    addLog('ERROR: ' + e.message);
    setStatus('Error in stage ' + n);
  }
}
</script>
</body>
</html>`

func (h *Handler) handleGoRulesDemo(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(gorulesDemo))
}
