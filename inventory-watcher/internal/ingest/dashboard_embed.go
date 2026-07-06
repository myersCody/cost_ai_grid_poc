package ingest

const dashboardHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Cost Dashboard — Diagnostics</title>
<style>
  :root {
    --bg: #f5f5f5; --card: #fff; --border: #e0e0e0; --text: #212121;
    --muted: #757575; --accent: #1565C0; --green: #2E7D32; --orange: #E65100;
    --purple: #7B1FA2; --red: #C62828;
  }
  * { box-sizing: border-box; margin: 0; padding: 0; }
  body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif; background: var(--bg); color: var(--text); }
  .header { background: var(--accent); color: white; padding: 1rem 2rem; display: flex; align-items: center; justify-content: space-between; }
  .header h1 { font-size: 1.3rem; font-weight: 600; }
  .header-right { display: flex; align-items: center; gap: 1rem; font-size: 0.85rem; }
  .status-dot { width: 8px; height: 8px; border-radius: 50%; display: inline-block; }
  .status-dot.ok { background: #69F0AE; }
  .status-dot.err { background: #FF5252; }
  .config-bar { background: #E3F2FD; padding: 0.5rem 2rem; display: flex; align-items: center; gap: 1rem; font-size: 0.85rem; }
  .config-bar select { border: 1px solid var(--border); border-radius: 4px; padding: 0.3rem 0.5rem; font-size: 0.85rem; }
  .summary-strip { display: flex; gap: 0; background: white; border-bottom: 1px solid var(--border); }
  .summary-item { flex: 1; padding: 1rem; text-align: center; border-right: 1px solid var(--border); }
  .summary-item:last-child { border-right: none; }
  .summary-item .value { font-size: 1.6rem; font-weight: 700; color: var(--accent); }
  .summary-item .label { font-size: 0.75rem; color: var(--muted); text-transform: uppercase; letter-spacing: 0.05em; margin-top: 0.2rem; }
  .content { padding: 1.5rem 2rem; max-width: 1400px; margin: 0 auto; }
  .tabs { display: flex; gap: 0; margin-bottom: 1.5rem; align-items: center; }
  .tab { padding: 0.6rem 1.2rem; background: white; border: 1px solid var(--border); cursor: pointer; font-size: 0.9rem; color: var(--muted); }
  .tab:first-child { border-radius: 6px 0 0 6px; }
  .tab:last-child { border-radius: 0 6px 6px 0; }
  .tab.active { background: var(--accent); color: white; border-color: var(--accent); }
  .cost-totals { display: grid; grid-template-columns: 1fr 1fr 1fr; gap: 1rem; margin-bottom: 1.5rem; }
  .cost-box { background: white; border-radius: 8px; padding: 1.2rem; text-align: center; border: 1px solid var(--border); box-shadow: 0 1px 3px rgba(0,0,0,0.06); }
  .cost-box .amount { font-size: 2rem; font-weight: 700; }
  .cost-box .lbl { font-size: 0.8rem; color: var(--muted); margin-top: 0.2rem; }
  .cost-box.total .amount { color: var(--accent); }
  .cost-box.infra .amount { color: var(--green); }
  .cost-box.supp .amount { color: var(--purple); }
  .card { background: white; border: 1px solid var(--border); border-radius: 8px; box-shadow: 0 1px 3px rgba(0,0,0,0.06); overflow: hidden; }
  table { width: 100%; border-collapse: collapse; font-size: 0.88rem; }
  th { background: #f9f9f9; padding: 0.7rem 1rem; text-align: left; font-size: 0.78rem; text-transform: uppercase; letter-spacing: 0.04em; color: var(--muted); border-bottom: 2px solid var(--border); }
  td { padding: 0.6rem 1rem; border-bottom: 1px solid #f0f0f0; }
  tr:hover td { background: #fafafa; }
  td.num { text-align: right; font-family: "SF Mono", "Fira Code", monospace; }
  th.num { text-align: right; }
  .bar-cell { position: relative; }
  .bar-bg { position: absolute; left: 0; top: 0; bottom: 0; border-radius: 0 3px 3px 0; opacity: 0.15; }
  .bar-infra { background: var(--green); }
  .bar-supp { background: var(--purple); }
  .footer { padding: 1rem 2rem; text-align: center; color: var(--muted); font-size: 0.8rem; }
  .error-msg { background: #FFEBEE; color: var(--red); padding: 1rem; border-radius: 6px; margin-bottom: 1rem; }
  .config-grid { display: grid; grid-template-columns: 1fr 1fr; gap: 1rem; margin: 1rem 0; }
  .config-card { background: white; border: 1px solid var(--border); border-radius: 8px; padding: 1rem; }
  .config-card h3 { font-size: 0.9rem; color: var(--accent); margin-bottom: 0.8rem; }
  .config-row { display: flex; justify-content: space-between; padding: 0.4rem 0; border-bottom: 1px solid #f5f5f5; font-size: 0.85rem; }
  .config-row:last-child { border-bottom: none; }
  .config-key { color: var(--muted); }
  .config-val { font-family: "SF Mono", "Fira Code", monospace; font-size: 0.82rem; }
  .config-val.yes { color: var(--green); }
  .config-val.no { color: var(--red); }
  #configView { display: none; }
  #costView { display: block; }
</style>
</head>
<body>

<div class="header">
  <h1>Cost Management — Diagnostics</h1>
  <div class="header-right">
    <span class="status-dot" id="statusDot"></span>
    <span id="statusText">Connecting...</span>
    <span id="lastUpdate" style="opacity:0.7"></span>
  </div>
</div>

<div class="config-bar">
  <label title="Billing period">Period: <input type="month" id="period" style="border:1px solid #e0e0e0;border-radius:4px;padding:0.3rem 0.5rem;font-size:0.85rem"></label>
  <label title="Filter to a specific tenant">Tenant: <select id="tenantFilter"><option value="">All tenants</option></select></label>
  <label>Refresh:
    <select id="refreshInterval">
      <option value="1000" selected>1s</option>
      <option value="3000">3s</option>
      <option value="5000">5s</option>
      <option value="10000">10s</option>
      <option value="30000">30s</option>
      <option value="0">Manual</option>
    </select>
  </label>
  <button onclick="refresh()" style="padding:0.3rem 0.8rem;border-radius:4px;border:1px solid #1565C0;background:#1565C0;color:white;cursor:pointer">Refresh</button>
</div>

<div class="summary-strip" id="summaryStrip">
  <div class="summary-item" title="Immutable audit log of all events"><div class="value" id="sRawEvents">-</div><div class="label">Raw Events</div></div>
  <div class="summary-item" title="Usage records from metering sweep and CloudEvents ingest"><div class="value" id="sMeteringEntries">-</div><div class="label">Metering Entries</div></div>
  <div class="summary-item" title="Priced usage: metering entry + rate = cost"><div class="value" id="sCostEntries">-</div><div class="label">Cost Entries</div></div>
  <div class="summary-item" title="Price definitions (flat + tiered, per-tenant overrides)"><div class="value" id="sRates">-</div><div class="label">Rates</div></div>
  <div class="summary-item" title="Running compute instances from OSAC"><div class="value" id="sLiveVMs">-</div><div class="label">Live VMs</div></div>
  <div class="summary-item" title="Active clusters from OSAC"><div class="value" id="sLiveClusters">-</div><div class="label">Live Clusters</div></div>
  <div class="summary-item" title="MaaS model deployments"><div class="value" id="sLiveModels">-</div><div class="label">Live Models</div></div>
</div>

<div class="content">
  <div id="errorBox" class="error-msg" style="display:none"></div>

  <div class="cost-totals">
    <div class="cost-box total" title="Sum of all cost entries"><div class="amount" id="totalCost">$0.00</div><div class="lbl">Total Cost</div></div>
    <div class="cost-box infra" title="Base resource costs (uptime, nodes)"><div class="amount" id="infraCost">$0.00</div><div class="lbl">Infrastructure</div></div>
    <div class="cost-box supp" title="Usage-based costs (CPU, memory, tokens)"><div class="amount" id="suppCost">$0.00</div><div class="lbl">Supplementary</div></div>
  </div>

  <div class="tabs">
    <div class="tab active" data-group="tenant" data-view="cost" onclick="switchTab(this)">By Tenant</div>
    <div class="tab" data-group="resource_type" data-view="cost" onclick="switchTab(this)">By Resource Type</div>
    <div class="tab" data-group="meter" data-view="cost" onclick="switchTab(this)">By Meter</div>
    <div class="tab" data-group="resource" data-view="cost" onclick="switchTab(this)">By Resource</div>
    <div style="flex:1"></div>
    <div class="tab" data-view="config" onclick="switchTab(this)" style="border-radius:6px">Environment</div>
  </div>

  <div id="costView">
    <div class="card">
      <table>
        <thead>
          <tr>
            <th id="groupHeader">Tenant</th>
            <th class="num">Entries</th>
            <th class="num">Total Cost</th>
            <th class="num">Infrastructure</th>
            <th class="num">Supplementary</th>
            <th style="width:30%">Distribution</th>
          </tr>
        </thead>
        <tbody id="reportBody">
          <tr><td colspan="6" style="text-align:center;padding:2rem;color:var(--muted)">Loading...</td></tr>
        </tbody>
      </table>
    </div>
  </div>

  <div id="configView">
    <div class="config-grid">
      <div class="config-card">
        <h3>OSAC Connection</h3>
        <div id="cfgOsac">Loading...</div>
      </div>
      <div class="config-card">
        <h3>Database</h3>
        <div id="cfgDb">Loading...</div>
      </div>
      <div class="config-card">
        <h3>Processing Intervals</h3>
        <div id="cfgIntervals">Loading...</div>
      </div>
      <div class="config-card">
        <h3>Service Settings</h3>
        <div id="cfgService">Loading...</div>
      </div>
    </div>
  </div>
</div>

<div class="footer">
  Cost Management AI Grid PoC &mdash; Built-in Diagnostics
  &bull; <span id="footerVersion">Set DEBUG_DASHBOARD=false to disable</span>
</div>

<script>
const $ = id => document.getElementById(id);
let currentGroup = 'tenant';
let currentView = 'cost';
let timer = null;

const apiBase = window.location.origin;

function fmt(n) {
  if (n >= 1) return '$' + n.toFixed(2);
  if (n >= 0.01) return '$' + n.toFixed(4);
  return '$' + n.toFixed(6);
}
function fmtInt(n) { return n.toLocaleString(); }
function esc(s) { const d = document.createElement('div'); d.textContent = s; return d.innerHTML; }

function switchTab(el) {
  document.querySelectorAll('.tab').forEach(t => t.classList.remove('active'));
  el.classList.add('active');

  const view = el.dataset.view;
  currentView = view;

  if (view === 'config') {
    $('costView').style.display = 'none';
    $('configView').style.display = 'block';
    fetchConfig();
  } else {
    $('costView').style.display = 'block';
    $('configView').style.display = 'none';
    currentGroup = el.dataset.group;
    const headers = { tenant: 'Tenant', resource_type: 'Resource Type', meter: 'Meter', resource: 'Resource ID' };
    $('groupHeader').textContent = headers[currentGroup] || 'Group';
    refresh();
  }
}

function cfgRow(key, val) {
  let cls = '';
  if (val === true) { val = 'Yes'; cls = 'yes'; }
  else if (val === false) { val = 'No'; cls = 'no'; }
  return '<div class="config-row"><span class="config-key">' + esc(key) + '</span><span class="config-val ' + cls + '">' + esc(String(val)) + '</span></div>';
}

async function fetchConfig() {
  try {
    const cfg = await fetchJSON(apiBase + '/api/v1/debug/config');
    $('cfgOsac').innerHTML =
      cfgRow('Base URL', cfg.osac_base_url) +
      cfgRow('Token Set', cfg.osac_token_set) +
      cfgRow('CA Cert Set', cfg.osac_ca_cert_set);
    $('cfgDb').innerHTML =
      cfgRow('Connection', cfg.inventory_db_host);
    $('cfgIntervals').innerHTML =
      cfgRow('Reconcile', cfg.reconcile_interval) +
      cfgRow('Summarize', cfg.summarize_interval) +
      cfgRow('Metering Sweep', cfg.metering_interval) +
      cfgRow('Rating Sweep', cfg.rating_interval);
    $('cfgService').innerHTML =
      cfgRow('Ingest Address', cfg.ingest_listen_addr || '(not set)') +
      cfgRow('Auth Issuer URL', cfg.auth_issuer_url || '(not set)') +
      cfgRow('Log Level', cfg.log_level) +
      cfgRow('Debug Dashboard', cfg.debug_dashboard);
  } catch (err) {
    $('cfgOsac').innerHTML = '<span style="color:var(--red)">Failed to load config</span>';
  }
}

async function fetchJSON(url) {
  const resp = await fetch(url);
  if (!resp.ok) throw new Error(resp.status + ' ' + resp.statusText);
  return resp.json();
}

async function refresh() {
  const period = $('period').value || '';
  const tenant = $('tenantFilter').value;

  try {
    const summary = await fetchJSON(apiBase + '/api/v1/reports/summary');
    $('sRawEvents').textContent = fmtInt(summary.raw_events);
    $('sMeteringEntries').textContent = fmtInt(summary.metering_entries);
    $('sCostEntries').textContent = fmtInt(summary.cost_entries);
    $('sRates').textContent = fmtInt(summary.rates);
    $('sLiveVMs').textContent = fmtInt(summary.live_vms);
    $('sLiveClusters').textContent = fmtInt(summary.live_clusters);
    $('sLiveModels').textContent = fmtInt(summary.live_models);

    if (currentView === 'config') {
      $('statusDot').className = 'status-dot ok';
      $('statusText').textContent = 'Connected';
      $('lastUpdate').textContent = new Date().toLocaleTimeString();
      $('errorBox').style.display = 'none';
      return;
    }

    let url = apiBase + '/api/v1/reports/costs?group_by=' + currentGroup;
    if (period) url += '&period=' + period;
    if (tenant) url += '&tenant_id=' + encodeURIComponent(tenant);

    const report = await fetchJSON(url);

    $('totalCost').textContent = fmt(report.meta.total.cost);
    $('infraCost').textContent = fmt(report.meta.total.infrastructure_cost);
    $('suppCost').textContent = fmt(report.meta.total.supplementary_cost);

    const maxCost = Math.max(...report.data.map(r => r.cost), 0.001);
    const tbody = $('reportBody');
    if (report.data.length === 0) {
      tbody.innerHTML = '<tr><td colspan="6" style="text-align:center;padding:2rem;color:var(--muted)">No cost data for this period</td></tr>';
    } else {
      tbody.innerHTML = report.data.map(row => {
        const infraPct = row.cost > 0 ? (row.infrastructure_cost / row.cost * 100) : 0;
        const suppPct = row.cost > 0 ? (row.supplementary_cost / row.cost * 100) : 0;
        const barWidth = (row.cost / maxCost * 100);
        return '<tr>' +
          '<td>' + esc(row.group) + '</td>' +
          '<td class="num">' + fmtInt(row.entries) + '</td>' +
          '<td class="num"><strong>' + fmt(row.cost) + '</strong></td>' +
          '<td class="num" style="color:var(--green)">' + fmt(row.infrastructure_cost) + '</td>' +
          '<td class="num" style="color:var(--purple)">' + fmt(row.supplementary_cost) + '</td>' +
          '<td class="bar-cell">' +
            '<div class="bar-bg bar-infra" style="width:' + (infraPct * barWidth / 100) + '%"></div>' +
            '<div class="bar-bg bar-supp" style="width:' + barWidth + '%;left:' + (infraPct * barWidth / 100) + '%"></div>' +
            '<span style="position:relative;font-size:0.78rem;color:var(--muted)">' + infraPct.toFixed(0) + '% / ' + suppPct.toFixed(0) + '%</span>' +
          '</td></tr>';
      }).join('');
    }

    if (currentGroup === 'tenant') {
      const current = $('tenantFilter').value;
      const existing = new Set(Array.from($('tenantFilter').options).map(o => o.value));
      report.data.forEach(row => {
        if (!existing.has(row.group)) {
          const opt = document.createElement('option');
          opt.value = row.group;
          opt.textContent = row.group;
          $('tenantFilter').appendChild(opt);
        }
      });
      $('tenantFilter').value = current;
    }

    $('statusDot').className = 'status-dot ok';
    $('statusText').textContent = 'Connected';
    $('lastUpdate').textContent = new Date().toLocaleTimeString();
    $('errorBox').style.display = 'none';

  } catch (err) {
    $('statusDot').className = 'status-dot err';
    $('statusText').textContent = 'Error';
    $('errorBox').textContent = 'Failed to fetch: ' + err.message;
    $('errorBox').style.display = 'block';
  }
}

function startTimer() {
  if (timer) clearInterval(timer);
  const ms = parseInt($('refreshInterval').value);
  if (ms > 0) timer = setInterval(refresh, ms);
}

$('refreshInterval').addEventListener('change', startTimer);
$('period').addEventListener('change', refresh);
$('tenantFilter').addEventListener('change', refresh);

$('period').value = new Date().toISOString().slice(0, 7);

refresh();
fetchConfig();
startTimer();
</script>
</body>
</html>`
