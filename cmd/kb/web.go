package main

// WebUI is the embedded knowledge base web interface.
const WebUI = `<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>Knowledge Base</title>
<style>
*{box-sizing:border-box;margin:0;padding:0}
body{font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,Helvetica,Arial,sans-serif;background:#0f1117;color:#e6edf3;min-height:100vh}
.container{max-width:1200px;margin:0 auto;padding:20px}
header{display:flex;align-items:center;gap:16px;margin-bottom:24px;padding-bottom:16px;border-bottom:1px solid #30363d}
header h1{font-size:1.4rem;color:#58a6ff}
.tabs{display:flex;gap:4px;margin-bottom:20px}
.tab{padding:8px 16px;border-radius:6px 6px 0 0;border:1px solid #30363d;border-bottom:none;background:#161b22;color:#8b949e;cursor:pointer;font-size:0.9rem;transition:all 0.15s}
.tab:hover{color:#e6edf3}
.tab.active{background:#1c2128;color:#e6edf3;border-color:#8b949e}
.panel{display:none;border:1px solid #30363d;border-radius:0 6px 6px 6px;background:#161b22;padding:20px}
.panel.active{display:block}
.search-box{display:flex;gap:8px;margin-bottom:16px}
.search-box input{flex:1;padding:10px 14px;border-radius:6px;border:1px solid #30363d;background:#0d1117;color:#e6edf3;font-size:1rem;outline:none}
.search-box input:focus{border-color:#58a6ff}
.search-box button{padding:10px 20px;border-radius:6px;border:none;background:#238636;color:#fff;cursor:pointer;font-size:0.95rem}
.search-box button:hover{background:#2ea043}
.result{border:1px solid #30363d;border-radius:6px;padding:14px;margin-bottom:10px;background:#1c2128}
.result:hover{border-color:#58a6ff}
.result-title{font-size:0.95rem;margin-bottom:6px;display:flex;align-items:center;gap:8px}
.type{font-size:0.75rem;padding:2px 8px;border-radius:12px;background:#388bfd22;color:#58a6ff}
.result-score{font-size:0.8rem;color:#3fb950;margin-left:auto}
.result-chunk{margin-top:6px;font-size:0.85rem;color:#6e7681;line-height:1.6;padding:8px;background:#0d1117;border-radius:4px}
.stats-grid{display:grid;grid-template-columns:repeat(auto-fill,minmax(200px,1fr));gap:12px;margin-bottom:20px}
.stat-card{background:#1c2128;border:1px solid #30363d;border-radius:8px;padding:16px;text-align:center}
.stat-card .value{font-size:2rem;font-weight:bold;color:#58a6ff}
.stat-card .label{font-size:0.85rem;color:#8b949e;margin-top:4px}
.item-list{list-style:none}
.item-list li{display:flex;align-items:center;gap:10px;padding:10px;border-bottom:1px solid #21262d;font-size:0.9rem}
.item-list li:last-child{border-bottom:none}
.item-icon{width:8px;height:8px;border-radius:50%;background:#3fb950}
.item-icon.pending{background:#d29922}
.item-icon.failed{background:#f85149}
.item-type{color:#8b949e;min-width:70px;font-size:0.85rem}
.item-path{flex:1;color:#58a6ff;font-family:monospace;font-size:0.85rem;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
.loading,.empty{text-align:center;padding:40px;color:#8b949e}
.ingest-form{display:flex;gap:8px;margin-bottom:20px}
.ingest-form input{flex:1;padding:10px 14px;border-radius:6px;border:1px solid #30363d;background:#0d1117;color:#e6edf3;font-size:0.95rem;outline:none}
.ingest-form p{margin-top:8px;font-size:0.85rem;color:#8b949e}
.ingest-form code{color:#58a6ff;background:#1c2128;padding:2px 6px;border-radius:4px}
</style>
</head>
<body>
<div class="container">
<header>
<h1>Knowledge Base</h1>
</header>

<div class="tabs">
<button class="tab active" data-tab="search">Search</button>
<button class="tab" data-tab="browse">Browse</button>
<button class="tab" data-tab="stats">Stats</button>
</div>

<div class="panel active" id="panel-search">
<div class="search-box">
<input type="text" id="search-input" placeholder="Search (e.g., AI trends, Rust performance, project management)">
<button id="search-btn">Search</button>
</div>
<div id="search-results"></div>
</div>

<div class="panel" id="panel-browse">
<div id="browse-items" class="loading">Loading...</div>
</div>

<div class="panel" id="panel-stats">
<div class="stats-grid" id="stats-grid">
<div class="stat-card"><div class="value" id="stat-items">-</div><div class="label">Total Items</div></div>
<div class="stat-card"><div class="value" id="stat-chunks">-</div><div class="label">Text Chunks</div></div>
<div class="stat-card"><div class="value" id="stat-ready">-</div><div class="label">Ready</div></div>
</div>
<div id="stats-types"></div>
</div>
</div>

<script>
const API = location.origin;
let debounceTimer;

document.querySelectorAll(".tab").forEach(tab => {
  tab.addEventListener("click", () => {
    document.querySelectorAll(".tab").forEach(t => t.classList.remove("active"));
    document.querySelectorAll(".panel").forEach(p => p.classList.remove("active"));
    tab.classList.add("active");
    document.getElementById("panel-" + tab.dataset.tab).classList.add("active");
    if (tab.dataset.tab === "browse") loadBrowse();
    if (tab.dataset.tab === "stats") loadStats();
  });
});

document.getElementById("search-btn").addEventListener("click", doSearch);
document.getElementById("search-input").addEventListener("input", () => {
  clearTimeout(debounceTimer);
  debounceTimer = setTimeout(doSearch, 600);
});
document.getElementById("search-input").addEventListener("keydown", e => { if (e.key === "Enter") { clearTimeout(debounceTimer); doSearch(); }});

async function doSearch() {
  const q = document.getElementById("search-input").value.trim();
  const el = document.getElementById("search-results");
  if (!q) { el.innerHTML = ""; return; }
  el.innerHTML = '<div class="loading">Searching...</div>';
  try {
    const r = await fetch(API + "/api/search?q=" + encodeURIComponent(q) + "&topk=10");
    const data = await r.json();
    if (!data.results || data.results.length === 0) {
      el.innerHTML = '<div class="empty">No results for "<b>' + q + '</b>"</div>';
      return;
    }
    el.innerHTML = '<div style="margin-bottom:12px;color:#8b949e;font-size:0.9rem">' + data.count + ' results</div>';
    data.results.forEach(item => {
      const div = document.createElement("div");
      div.className = "result";
      const score = (item.score * 100).toFixed(1);
      const chunk = (item.chunk || "").replace(/\n/g, " ").substring(0, 300);
      div.innerHTML = '<div class="result-title"><span class="type">' + item.type + '</span><span class="item-path" title="' + item.path + '">' + item.path + '</span><span class="result-score">' + score + '%</span></div>';
      if (chunk) div.innerHTML += '<div class="result-chunk">' + chunk + (item.chunk && item.chunk.length > 300 ? "..." : "") + '</div>';
      el.appendChild(div);
    });
  } catch(e) { el.innerHTML = '<div class="empty">Error: ' + e.message + '</div>'; }
}

async function loadBrowse() {
  const el = document.getElementById("browse-items");
  el.innerHTML = '<div class="loading">Loading...</div>';
  try {
    const r = await fetch(API + "/api/media?limit=50");
    const data = await r.json();
    if (!data.items || data.items.length === 0) {
      el.innerHTML = '<div class="empty">No items yet. Run <code>kb ingest &lt;path&gt;</code> to add files.</div>';
      return;
    }
    el.innerHTML = '<ul class="item-list">' + data.items.map(item =>
      '<li><span class="item-icon ' + (item.status !== "ready" ? item.status : "") + '"></span><span class="item-type">' + item.type + '</span><span class="item-path" title="' + item.path + '">' + item.path + '</span></li>'
    ).join("") + '</ul>';
  } catch(e) { el.innerHTML = '<div class="empty">Error: ' + e.message + '</div>'; }
}

async function loadStats() {
  try {
    const r = await fetch(API + "/api/stats");
    const data = await r.json();
    document.getElementById("stat-items").textContent = data.items || 0;
    document.getElementById("stat-chunks").textContent = data.chunks || 0;
    document.getElementById("stat-ready").textContent = data.ready || 0;
  } catch(e) { document.getElementById("stat-items").textContent = "N/A"; }
}

// Init
fetch(API + "/api/stats").then(r => r.json()).then(d => {
  document.getElementById("stat-items").textContent = d.items || 0;
  document.getElementById("stat-chunks").textContent = d.chunks || 0;
  document.getElementById("stat-ready").textContent = d.ready || 0;
}).catch(() => {});
</script>
</body>
</html>`
