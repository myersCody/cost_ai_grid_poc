package ingest

const reportsHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Cost Management — Reports</title>
<style>
  :root {
    --bg: #f0f4f8; --card: #fff; --border: #dde3ea; --text: #1a1a2e;
    --muted: #6b7280; --accent: #cc0000; --accent2: #a30000;
    --green: #16a34a; --amber: #d97706; --red: #dc2626; --blue: #1d4ed8;
    --purple: #7c3aed;
  }
  * { box-sizing: border-box; margin: 0; padding: 0; }
  body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
         background: var(--bg); color: var(--text); min-height: 100vh; }

  /* Header */
  .header { background: var(--accent); color: white; padding: 0 2rem;
            display: flex; align-items: center; justify-content: space-between; height: 56px; }
  .header-left { display: flex; align-items: center; gap: 1rem; }
  .header-logo { font-size: 1.15rem; font-weight: 700; letter-spacing: -0.02em; }
  .header-sub { font-size: 0.82rem; opacity: 0.8; padding-left: 1rem; border-left: 1px solid rgba(255,255,255,0.3); }
  .header-right { display: flex; align-items: center; gap: 0.75rem; }
  .token-btn { padding: 0.3rem 0.8rem; border-radius: 4px;
               border: 1px solid rgba(255,255,255,0.4); background: transparent;
               color: white; cursor: pointer; font-size: 0.82rem; }
  .token-btn.has-token { border-color: #86efac; color: #86efac; }

  /* Period bar */
  .period-bar { background: white; border-bottom: 1px solid var(--border);
                padding: 0 2rem; display: flex; align-items: center; gap: 1.5rem;
                height: 44px; font-size: 0.85rem; color: var(--muted); }
  .period-bar label { display: flex; align-items: center; gap: 0.4rem; }
  .period-bar input, .period-bar select {
    border: 1px solid var(--border); border-radius: 4px;
    padding: 0.25rem 0.5rem; font-size: 0.85rem; color: var(--text);
    background: white; }
  .period-bar .spacer { flex: 1; }
  .period-bar .last-updated { font-size: 0.78rem; color: var(--muted); }

  /* KPI tiles */
  .kpi-row { display: grid; grid-template-columns: repeat(4, 1fr); gap: 1rem;
             padding: 1.5rem 2rem 0; max-width: 1400px; margin: 0 auto; }
  .kpi { background: white; border-radius: 8px; padding: 1.25rem 1.5rem;
         border: 1px solid var(--border); box-shadow: 0 1px 3px rgba(0,0,0,0.05); }
  .kpi-value { font-size: 2rem; font-weight: 700; line-height: 1; }
  .kpi-label { font-size: 0.78rem; color: var(--muted); text-transform: uppercase;
               letter-spacing: 0.06em; margin-top: 0.4rem; }
  .kpi-delta { font-size: 0.8rem; margin-top: 0.5rem; }
  .kpi.total .kpi-value { color: var(--accent); }
  .kpi.infra .kpi-value { color: var(--blue); }
  .kpi.supp .kpi-value { color: var(--purple); }
  .kpi.events .kpi-value { color: var(--muted); font-size: 1.6rem; }

  /* Main content */
  .content { padding: 1.5rem 2rem; max-width: 1400px; margin: 0 auto; }

  /* Tabs */
  .tabs { display: flex; gap: 0; margin-bottom: 1.25rem; border-bottom: 2px solid var(--border); }
  .tab { padding: 0.6rem 1.25rem; cursor: pointer; font-size: 0.88rem;
         color: var(--muted); border-bottom: 2px solid transparent; margin-bottom: -2px; }
  .tab.active { color: var(--accent); border-bottom-color: var(--accent); font-weight: 600; }
  .tab:hover:not(.active) { color: var(--text); }

  /* Cards and tables */
  .card { background: white; border: 1px solid var(--border); border-radius: 8px;
          box-shadow: 0 1px 3px rgba(0,0,0,0.05); overflow: hidden; }
  .card-header { padding: 1rem 1.25rem; border-bottom: 1px solid var(--border);
                 font-weight: 600; font-size: 0.9rem; display: flex;
                 align-items: center; justify-content: space-between; }
  .card-header .subtitle { font-weight: 400; color: var(--muted); font-size: 0.82rem; }
  table { width: 100%; border-collapse: collapse; font-size: 0.87rem; }
  th { background: #fafbfc; padding: 0.65rem 1.25rem; text-align: left;
       font-size: 0.76rem; text-transform: uppercase; letter-spacing: 0.05em;
       color: var(--muted); border-bottom: 1px solid var(--border); font-weight: 600; }
  td { padding: 0.65rem 1.25rem; border-bottom: 1px solid #f3f4f6; }
  tr:last-child td { border-bottom: none; }
  tr:hover td { background: #fafbfc; }
  td.num, th.num { text-align: right; font-family: "SF Mono", "Fira Code", monospace; }
  .empty { text-align: center; padding: 3rem; color: var(--muted); font-size: 0.9rem; }

  /* Usage bar */
  .usage-bar-bg { background: #f0f4f8; border-radius: 3px; height: 6px;
                  overflow: hidden; min-width: 80px; }
  .usage-bar-fill { height: 100%; border-radius: 3px; transition: width 0.3s; }
  .bar-ok { background: var(--green); }
  .bar-warn { background: var(--amber); }
  .bar-crit { background: var(--red); }

  /* Quota badges */
  .badge { display: inline-block; padding: 0.15rem 0.5rem; border-radius: 9999px;
           font-size: 0.74rem; font-weight: 600; }
  .badge-ok { background: #dcfce7; color: var(--green); }
  .badge-warn { background: #fef3c7; color: var(--amber); }
  .badge-crit { background: #fee2e2; color: var(--red); }

  /* Two-column layout */
  .two-col { display: grid; grid-template-columns: 1fr 1fr; gap: 1rem; margin-top: 1rem; }

  /* Error and auth modal */
  .error-banner { background: #fee2e2; color: var(--red); padding: 0.75rem 1.25rem;
                  border-radius: 6px; margin-bottom: 1rem; font-size: 0.88rem; }
  .modal-overlay { position: fixed; inset: 0; background: rgba(0,0,0,0.45);
                   display: flex; align-items: center; justify-content: center; z-index: 100; }
  .modal { background: white; border-radius: 10px; padding: 1.75rem; width: 500px;
           max-width: 95vw; box-shadow: 0 20px 60px rgba(0,0,0,0.2); }
  .modal h2 { font-size: 1.1rem; margin-bottom: 0.5rem; color: var(--text); }
  .modal p { font-size: 0.85rem; color: var(--muted); margin-bottom: 0.75rem; line-height: 1.5; }
  .modal-hint { font-size: 0.78rem; color: var(--text); margin: 0.4rem 0 0.9rem;
                font-family: "SF Mono","Fira Code",monospace; background: #f0f4f8;
                padding: 0.45rem 0.7rem; border-radius: 4px; word-break: break-all; }
  .modal textarea { width: 100%; height: 90px; font-family: "SF Mono","Fira Code",monospace;
                    font-size: 0.78rem; border: 1px solid var(--border); border-radius: 4px;
                    padding: 0.5rem; resize: vertical; }
  .modal-btns { display: flex; gap: 0.5rem; justify-content: flex-end; margin-top: 1rem; }
  .modal-btns button { padding: 0.5rem 1.2rem; border-radius: 6px; border: none;
                       cursor: pointer; font-size: 0.88rem; font-weight: 500; }
  .btn-primary { background: var(--accent); color: white; }
  .btn-primary:hover { background: var(--accent2); }
  .btn-secondary { background: #f0f4f8; color: var(--text); }
</style>
</head>
<body>

<div class="header">
  <div class="header-left">
    <span class="header-logo">Cost Management</span>
    <span class="header-sub">Reports</span>
  </div>
  <div class="header-right">
    <span id="lastUpdated" style="font-size:0.78rem;opacity:0.7"></span>
    <button class="token-btn" id="tokenBtn" onclick="showTokenModal()">Token</button>
  </div>
</div>

<div class="period-bar">
  <label>Period <input type="month" id="period"></label>
  <label>Organization <select id="orgFilter"><option value="">All organizations</option></select></label>
  <div class="spacer"></div>
  <div class="last-updated" id="statusLine">Loading...</div>
</div>

<div class="kpi-row" id="kpiRow">
  <div class="kpi total"><div class="kpi-value" id="kTotal">—</div><div class="kpi-label">Total Cost</div></div>
  <div class="kpi infra"><div class="kpi-value" id="kInfra">—</div><div class="kpi-label">Infrastructure</div></div>
  <div class="kpi supp"><div class="kpi-value" id="kSupp">—</div><div class="kpi-label">AI Inference &amp; Usage</div></div>
  <div class="kpi events"><div class="kpi-value" id="kResources">—</div><div class="kpi-label">Active Resources</div></div>
</div>

<div class="content">
  <div id="errorBanner" class="error-banner" style="display:none"></div>

  <div class="tabs">
    <div class="tab active" data-tab="orgs" onclick="switchTab(this)">By Organization</div>
    <div class="tab" data-tab="resources" onclick="switchTab(this)">By Resource Type</div>
    <div class="tab" data-tab="quotas" onclick="switchTab(this)">Quota Status</div>
  </div>

  <div id="tabOrgs">
    <div class="card">
      <div class="card-header">
        Cost by Organization
        <span class="subtitle" id="orgPeriodLabel"></span>
      </div>
      <table>
        <thead><tr>
          <th>Organization</th>
          <th class="num">Total Cost</th>
          <th class="num">Infrastructure</th>
          <th class="num">AI Inference &amp; Usage</th>
          <th style="width:25%">Breakdown</th>
        </tr></thead>
        <tbody id="orgBody"><tr><td colspan="5" class="empty">Loading…</td></tr></tbody>
      </table>
    </div>
  </div>

  <div id="tabResources" style="display:none">
    <div class="two-col">
      <div class="card">
        <div class="card-header">Cost by Resource Type</div>
        <table>
          <thead><tr>
            <th>Resource Type</th>
            <th class="num">Cost</th>
            <th class="num">Share</th>
          </tr></thead>
          <tbody id="resourceBody"><tr><td colspan="3" class="empty">Loading…</td></tr></tbody>
        </table>
      </div>
      <div class="card">
        <div class="card-header">Cost by Meter</div>
        <table>
          <thead><tr>
            <th>Meter</th>
            <th class="num">Cost</th>
            <th class="num">Share</th>
          </tr></thead>
          <tbody id="meterBody"><tr><td colspan="3" class="empty">Loading…</td></tr></tbody>
        </table>
      </div>
    </div>
  </div>

  <div id="tabQuotas" style="display:none">
    <div class="card">
      <div class="card-header">
        Quota Status
        <span class="subtitle">Consumption against defined limits</span>
      </div>
      <table>
        <thead><tr>
          <th>Organization</th>
          <th>Meter</th>
          <th class="num">Consumed</th>
          <th class="num">Limit</th>
          <th style="width:200px">Usage</th>
          <th>Status</th>
        </tr></thead>
        <tbody id="quotaBody"><tr><td colspan="6" class="empty">Loading…</td></tr></tbody>
      </table>
    </div>
  </div>
</div>

<!-- Chart row: donut + bar chart -->
<div style="padding:1rem 2rem 0;max-width:1400px;margin:0 auto">
  <div style="display:grid;grid-template-columns:200px 1fr;gap:1rem">
    <div class="card" style="padding:1.25rem;display:flex;flex-direction:column;align-items:center;justify-content:center">
      <div style="font-size:0.75rem;text-transform:uppercase;letter-spacing:0.06em;color:var(--muted);margin-bottom:0.75rem">Cost Split</div>
      <svg width="110" height="110" viewBox="0 0 120 120">
        <circle cx="60" cy="60" r="46" fill="none" stroke="#f0f4f8" stroke-width="18"/>
        <circle id="donutInfra" cx="60" cy="60" r="46" fill="none" stroke="var(--blue)"
                stroke-width="18" stroke-dasharray="0 289" stroke-linecap="butt"
                transform="rotate(-90 60 60)" style="transition:stroke-dasharray 0.4s"/>
        <circle id="donutSupp" cx="60" cy="60" r="46" fill="none" stroke="var(--purple)"
                stroke-width="18" stroke-dasharray="0 289" stroke-linecap="butt"
                transform="rotate(-90 60 60)" style="transition:stroke-dasharray 0.4s"/>
        <text x="60" y="57" text-anchor="middle" font-size="10" fill="var(--muted)">infra</text>
        <text x="60" y="70" text-anchor="middle" font-size="12" id="donutPct" fill="var(--text)" font-weight="700">—%</text>
      </svg>
      <div style="display:flex;gap:0.75rem;margin-top:0.5rem;font-size:0.76rem;color:var(--muted)">
        <span><span style="display:inline-block;width:8px;height:8px;border-radius:2px;background:var(--blue);margin-right:3px;vertical-align:middle"></span>Infra</span>
        <span><span style="display:inline-block;width:8px;height:8px;border-radius:2px;background:var(--purple);margin-right:3px;vertical-align:middle"></span>Usage</span>
      </div>
    </div>
    <div class="card">
      <div class="card-header">Cost by Organization</div>
      <div id="barChart" style="padding:0.75rem 1.25rem 0.5rem"></div>
    </div>
  </div>
</div>

<!-- Token modal -->
<div class="modal-overlay" id="tokenModal" style="display:none">
  <div class="modal">
    <h2>Authentication Required</h2>
    <p>This report requires a Bearer token. Run <code>scripts/refresh-token.sh</code> then paste from:</p>
    <div class="modal-hint">cat /tmp/osac_token.txt</div>
    <p>Or retrieve directly:</p>
    <div class="modal-hint">oc get secret cost-consumer-secrets -n cost-mgmt -o jsonpath='{.data.osac-token}' | base64 -d</div>
    <textarea id="tokenInput" placeholder="eyJhbGci…"></textarea>
    <div class="modal-btns">
      <button class="btn-secondary" onclick="clearToken()">Clear</button>
      <button class="btn-secondary" onclick="hideTokenModal()">Cancel</button>
      <button class="btn-primary" onclick="saveToken()">Save &amp; load</button>
    </div>
  </div>
</div>

<script>
const $ = id => document.getElementById(id);
const TOKEN_KEY = 'cost_reports_token';
const RESOURCE_LABELS = {
  compute_instance: 'Virtual Machines',
  cluster: 'Clusters',
  model: 'AI Inference',
  bare_metal_instance: 'Bare Metal',
};
const METER_LABELS = {
  vm_cpu_core_seconds: 'CPU Core Seconds',
  vm_memory_gib_seconds: 'Memory',
  vm_uptime_seconds: 'VM Uptime',
  cluster_uptime_seconds: 'Cluster Uptime',
  cluster_worker_node_seconds: 'Worker Nodes',
  maas_tokens_in: 'Input Tokens',
  maas_tokens_out: 'Output Tokens',
  maas_requests: 'API Requests',
};
let currentTab = 'orgs';
let timer = null;

function getToken() { return localStorage.getItem(TOKEN_KEY) || ''; }
function updateTokenBtn() {
  const btn = $('tokenBtn');
  if (getToken()) { btn.textContent = 'Token ✓'; btn.classList.add('has-token'); }
  else { btn.textContent = 'Token'; btn.classList.remove('has-token'); }
}
function showTokenModal() {
  const open = $('tokenModal').style.display === 'flex';
  if (!open) $('tokenInput').value = getToken();
  $('tokenModal').style.display = 'flex';
  if (!open) $('tokenInput').focus();
}
function hideTokenModal() { $('tokenModal').style.display = 'none'; }
function saveToken() {
  localStorage.setItem(TOKEN_KEY, $('tokenInput').value.replace(/\s+/g, ''));
  updateTokenBtn(); hideTokenModal(); loadAll();
}
function clearToken() { localStorage.removeItem(TOKEN_KEY); updateTokenBtn(); hideTokenModal(); }
$('tokenModal').addEventListener('click', e => { if (e.target === $('tokenModal')) hideTokenModal(); });
document.addEventListener('keydown', e => { if (e.key === 'Escape') hideTokenModal(); });
updateTokenBtn();

async function api(path) {
  const headers = {};
  const tok = getToken();
  if (tok) headers['Authorization'] = 'Bearer ' + tok;
  const r = await fetch(window.location.origin + path, { headers });
  if (r.status === 401) { showTokenModal(); throw new Error('401'); }
  if (!r.ok) throw new Error(r.status);
  return r.json();
}

function fmt(raw) {
  const n = parseFloat(raw) || 0;
  if (n === 0) return '$0.00';
  if (n >= 1) return '$' + n.toFixed(2);
  if (n >= 0.01) return '$' + n.toFixed(4);
  return '$' + n.toFixed(6);
}
function fmtNum(n) { return n.toLocaleString(); }
function esc(s) { const d = document.createElement('div'); d.textContent = String(s||''); return d.innerHTML; }

function switchTab(el) {
  document.querySelectorAll('.tab').forEach(t => t.classList.remove('active'));
  el.classList.add('active');
  currentTab = el.dataset.tab;
  ['Orgs','Resources','Quotas'].forEach(t => $('tab'+t).style.display = 'none');
  $('tab' + currentTab.charAt(0).toUpperCase() + currentTab.slice(1)).style.display = '';
  loadAll();
}

function pct(a, b) { return parseFloat(b) > 0 ? (parseFloat(a) / parseFloat(b) * 100) : 0; }

function barClass(p) {
  if (p >= 100) return 'bar-crit';
  if (p >= 70) return 'bar-warn';
  return 'bar-ok';
}
function badgeClass(p) {
  if (p >= 100) return 'badge-crit';
  if (p >= 70) return 'badge-warn';
  return 'badge-ok';
}
function badgeText(p) {
  if (p >= 100) return 'Over limit';
  if (p >= 70) return 'Near limit';
  return 'OK';
}

function usageBar(consumed, limit) {
  if (!limit) return '<span style="color:var(--muted);font-size:0.8rem">No limit</span>';
  const p = Math.min(pct(consumed, limit), 100);
  return '<div class="usage-bar-bg"><div class="usage-bar-fill ' + barClass(p) + '" style="width:' + p.toFixed(1) + '%"></div></div>' +
         '<span style="font-size:0.78rem;color:var(--muted);margin-left:0.4rem">' + p.toFixed(0) + '%</span>';
}

function updateDonut(infra, supp) {
  const circ = 2 * Math.PI * 46; // 289
  const total = infra + supp || 1;
  const ip = infra / total * circ;
  const sp = supp / total * circ;
  $('donutInfra').setAttribute('stroke-dasharray', ip.toFixed(1) + ' ' + (circ - ip).toFixed(1));
  // supp arc starts after infra
  $('donutSupp').setAttribute('stroke-dasharray', sp.toFixed(1) + ' ' + (circ - sp).toFixed(1));
  $('donutSupp').setAttribute('transform', 'rotate(' + (-90 + ip / circ * 360).toFixed(1) + ' 60 60)');
  $('donutPct').textContent = (infra / total * 100).toFixed(0) + '%';
}

function updateBarChart(rows) {
  if (!rows || rows.length === 0) { $('barChart').innerHTML = '<p style="color:var(--muted);font-size:0.85rem;padding:0.5rem 0">No data</p>'; return; }
  const max = Math.max(...rows.map(r => parseFloat(r.cost) || 0), 0.001);
  const top = rows.slice(0, 8);
  $('barChart').innerHTML = top.map(row => {
    const w = ((parseFloat(row.cost)||0) / max * 100).toFixed(1);
    return '<div style="display:grid;grid-template-columns:140px 1fr 80px;align-items:center;gap:0.75rem;margin-bottom:0.5rem">' +
      '<span style="font-size:0.82rem;overflow:hidden;text-overflow:ellipsis;white-space:nowrap" title="' + esc(row.group) + '">' + esc(row.group || '—') + '</span>' +
      '<div style="background:#f0f4f8;border-radius:3px;height:8px;overflow:hidden"><div style="height:100%;width:' + w + '%;background:var(--accent);border-radius:3px;transition:width 0.4s"></div></div>' +
      '<span style="font-size:0.82rem;text-align:right;font-family:monospace">' + fmt(parseFloat(row.cost)||0) + '</span>' +
    '</div>';
  }).join('');
}

async function loadCostByOrg() {
  const period = $('period').value;
  const org = $('orgFilter').value;
  let url = '/api/v1/reports/costs?group_by=tenant';
  if (period) url += '&period=' + period;
  if (org) url += '&tenant_id=' + encodeURIComponent(org);

  const d = await api(url);
  const total = d.meta.total;
  $('kTotal').textContent = fmt(parseFloat(total.cost) || 0);
  $('kInfra').textContent = fmt(parseFloat(total.infrastructure_cost) || 0);
  $('kSupp').textContent = fmt(parseFloat(total.supplementary_cost) || 0);
  $('orgPeriodLabel').textContent = d.meta.period || '';
  updateDonut(parseFloat(total.infrastructure_cost) || 0, parseFloat(total.supplementary_cost) || 0);
  updateBarChart(d.data);

  // Populate org filter
  const sel = $('orgFilter');
  const existing = new Set(Array.from(sel.options).map(o => o.value));
  d.data.forEach(row => {
    if (!existing.has(row.group) && row.group) {
      const opt = document.createElement('option');
      opt.value = row.group; opt.textContent = row.group;
      sel.appendChild(opt);
    }
  });
  if (org) sel.value = org;

  const max = Math.max(...d.data.map(r => parseFloat(r.cost)||0), 0.001);
  $('orgBody').innerHTML = d.data.length === 0
    ? '<tr><td colspan="5" class="empty">No cost data for this period</td></tr>'
    : d.data.map(row => {
        const c = parseFloat(row.cost)||0, ic = parseFloat(row.infrastructure_cost)||0, sc = parseFloat(row.supplementary_cost)||0;
        const ip = c > 0 ? (ic / c * 100) : 0;
        const sp = c > 0 ? (sc / c * 100) : 0;
        const bw = (c / max * 100).toFixed(1);
        return '<tr>' +
          '<td><strong>' + esc(row.group || '—') + '</strong></td>' +
          '<td class="num"><strong>' + fmt(c) + '</strong></td>' +
          '<td class="num" style="color:var(--blue)">' + fmt(ic) + '</td>' +
          '<td class="num" style="color:var(--purple)">' + fmt(sc) + '</td>' +
          '<td><div style="display:flex;align-items:center;gap:6px">' +
            '<div class="usage-bar-bg" style="flex:1"><div class="usage-bar-fill bar-ok" style="width:' + (ip*bw/100).toFixed(1) + '%"></div></div>' +
            '<span style="font-size:0.75rem;color:var(--muted);white-space:nowrap">' + ip.toFixed(0) + '% infra</span>' +
          '</div></td>' +
        '</tr>';
      }).join('');
}

async function loadCostByResource() {
  const period = $('period').value;
  let url = '/api/v1/reports/costs?group_by=resource_type';
  if (period) url += '&period=' + period;
  const d = await api(url);
  const total = parseFloat(d.meta.total.cost) || 0.001;

  $('resourceBody').innerHTML = d.data.length === 0
    ? '<tr><td colspan="3" class="empty">No data</td></tr>'
    : d.data.map(row => '<tr>' +
        '<td>' + esc(RESOURCE_LABELS[row.group] || row.group) + '</td>' +
        '<td class="num">' + fmt(parseFloat(row.cost)||0) + '</td>' +
        '<td class="num">' + pct(row.cost, total).toFixed(1) + '%</td>' +
      '</tr>').join('');

  url = '/api/v1/reports/costs?group_by=meter';
  if (period) url += '&period=' + period;
  const m = await api(url);
  $('meterBody').innerHTML = m.data.length === 0
    ? '<tr><td colspan="3" class="empty">No data</td></tr>'
    : m.data.map(row => '<tr>' +
        '<td>' + esc(METER_LABELS[row.group] || row.group) + '</td>' +
        '<td class="num">' + fmt(parseFloat(row.cost)||0) + '</td>' +
        '<td class="num">' + pct(row.cost, total).toFixed(1) + '%</td>' +
      '</tr>').join('');
}

async function loadQuotas() {
  const summary = await api('/api/v1/reports/summary');
  $('kResources').textContent = fmtNum(
    (summary.live_vms||0) + (summary.live_clusters||0) + (summary.live_models||0));

  const period = $('period').value;
  let url = '/api/v1/reports/costs?group_by=tenant';
  if (period) url += '&period=' + period;
  const costData = await api(url);
  const orgs = costData.data.map(r => r.group).filter(Boolean);

  if (orgs.length === 0) {
    $('quotaBody').innerHTML = '<tr><td colspan="6" class="empty">No organizations with cost data</td></tr>';
    return;
  }

  const rows = [];
  await Promise.all(orgs.map(async org => {
    try {
      const q = await api('/api/v1/quotas/' + encodeURIComponent(org));
      (q.quotas || []).forEach(quota => {
        if (quota.limit > 0 || quota.consumed > 0) {
          rows.push({ org, quota });
        }
      });
    } catch (_) {}
  }));

  rows.sort((a, b) => pct(b.quota.consumed, b.quota.limit) - pct(a.quota.consumed, a.quota.limit));

  $('quotaBody').innerHTML = rows.length === 0
    ? '<tr><td colspan="6" class="empty">No quota data available</td></tr>'
    : rows.map(({ org, quota }) => {
        const p = quota.limit > 0 ? pct(quota.consumed, quota.limit) : 0;
        return '<tr>' +
          '<td><strong>' + esc(org) + '</strong></td>' +
          '<td>' + esc(METER_LABELS[quota.meter_name] || quota.meter_name) + '</td>' +
          '<td class="num">' + fmtNum(Math.round(quota.consumed)) + '</td>' +
          '<td class="num">' + (quota.limit > 0 ? fmtNum(quota.limit) : '—') + '</td>' +
          '<td style="vertical-align:middle">' + usageBar(quota.consumed, quota.limit) + '</td>' +
          '<td><span class="badge ' + badgeClass(p) + '">' + badgeText(p) + '</span></td>' +
        '</tr>';
      }).join('');
}

async function loadAll() {
  $('errorBanner').style.display = 'none';
  try {
    if (currentTab === 'orgs') await loadCostByOrg();
    else if (currentTab === 'resources') { await loadCostByOrg(); await loadCostByResource(); }
    else if (currentTab === 'quotas') { await loadCostByOrg(); await loadQuotas(); }
    $('statusLine').textContent = 'Updated ' + new Date().toLocaleTimeString();
    $('lastUpdated').textContent = new Date().toLocaleTimeString();
  } catch (err) {
    if (err.message !== '401') {
      $('errorBanner').textContent = 'Failed to load data: ' + err.message;
      $('errorBanner').style.display = 'block';
      $('statusLine').textContent = 'Error';
    }
  }
}

$('period').value = new Date().toISOString().slice(0, 7);
$('period').addEventListener('change', loadAll);
$('orgFilter').addEventListener('change', loadAll);

loadAll();
setInterval(loadAll, 60000);
</script>
</body>
</html>`
