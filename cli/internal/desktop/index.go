package desktop

const indexHTML = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>devopsellence solo desktop</title>
  <style>
    :root { color-scheme: dark; --bg:#070b12; --panel:#101827; --panel2:#0c1320; --text:#e5edf8; --muted:#8ea0b8; --accent:#7dd3fc; --ok:#86efac; --warn:#fde68a; --border:#253247; }
    * { box-sizing: border-box; }
    body { margin:0; font:14px/1.5 ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; background:radial-gradient(circle at top left,#16233a 0,#070b12 36rem); color:var(--text); }
    header { padding:32px; border-bottom:1px solid var(--border); }
    h1 { margin:0; font-size:28px; letter-spacing:-.03em; }
    h2 { margin:0 0 12px; font-size:15px; text-transform:uppercase; letter-spacing:.12em; color:var(--muted); }
    code { color:var(--accent); }
    .muted { color:var(--muted); }
    .grid { display:grid; grid-template-columns:repeat(auto-fit,minmax(260px,1fr)); gap:16px; padding:24px 32px 40px; }
    .card { background:linear-gradient(180deg,var(--panel),var(--panel2)); border:1px solid var(--border); border-radius:18px; padding:18px; box-shadow:0 18px 60px rgb(0 0 0 / .24); }
    .metric { font-size:30px; font-weight:700; letter-spacing:-.04em; }
    .pill { display:inline-flex; align-items:center; gap:6px; border:1px solid var(--border); border-radius:999px; padding:4px 9px; color:var(--muted); margin:3px 4px 3px 0; }
    .pill.ok { color:var(--ok); border-color:rgb(134 239 172 / .35); }
    .pill.warn { color:var(--warn); border-color:rgb(253 230 138 / .35); }
    table { width:100%; border-collapse:collapse; }
    th, td { text-align:left; padding:8px 0; border-top:1px solid var(--border); vertical-align:top; }
    th { color:var(--muted); font-weight:500; }
    pre { white-space:pre-wrap; overflow:auto; background:#050812; border:1px solid var(--border); border-radius:12px; padding:12px; color:#c7d2fe; }
    .wide { grid-column:1 / -1; }
    .error { color:#fca5a5; }
  </style>
</head>
<body>
  <header>
    <h1>devopsellence solo desktop</h1>
    <div class="muted">Local-first control surface for the solo filesystem state. Secrets are shown by name only, never by value.</div>
  </header>
  <main id="app" class="grid"><div class="card wide">Loading…</div></main>
  <script>
    const esc = (value) => String(value ?? '').replace(/[&<>"]/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;'}[c]));
    const list = (items, render, empty='None') => items && items.length ? items.map(render).join('') : '<span class="muted">' + empty + '</span>';
    function rowEmpty(cols, text) { return '<tr><td colspan="' + cols + '" class="muted">' + esc(text) + '</td></tr>'; }
    function render(data) {
      const project = data.project || {};
      const envPill = project.default_environment ? '<div class="pill">env: ' + esc(project.default_environment) + '</div>' : '';
      const nodePills = list(data.nodes, n => '<span class="pill ' + (n.attached ? 'ok' : 'warn') + '">' + esc(n.name) + (n.host ? ' · ' + esc(n.host) : '') + '</span>');
      const attachmentRows = list(data.attachments, a => '<tr><td>' + esc(a.environment) + '</td><td>' + esc((a.node_names || []).join(', ')) + '</td></tr>', rowEmpty(2, 'No attachments for this workspace.'));
      const releaseRows = list(data.releases.slice(0, 8), r => '<tr><td>' + (r.current ? '● ' : '') + esc(r.id) + '</td><td>' + esc(r.environment) + '</td><td><code>' + esc(r.revision) + '</code></td><td>' + esc((r.node_names || []).join(', ')) + '</td><td>' + esc(r.created_at) + '</td></tr>', rowEmpty(5, 'No releases yet.'));
      const nextSteps = list(data.next_steps, s => '<p><strong>' + esc(s.label) + '</strong><br><span class="muted">' + esc(s.reason) + '</span><pre>' + esc(s.command) + '</pre></p>', 'No suggested next steps.');
      document.getElementById('app').innerHTML =
        '<section class="card">' +
          '<h2>Workspace</h2>' +
          '<div><strong>' + esc(project.project || 'Uninitialized') + '</strong></div>' +
          '<div class="muted">' + esc(data.workspace.root) + '</div>' +
          '<div class="pill ok">mode: ' + esc(data.workspace.mode) + '</div>' + envPill +
        '</section>' +
        '<section class="card"><h2>Nodes</h2><div class="metric">' + data.state.node_count + '</div><div>' + nodePills + '</div></section>' +
        '<section class="card"><h2>Releases</h2><div class="metric">' + data.state.release_count + '</div><div class="muted">' + (data.state.current_revision ? 'current revision ' + esc(data.state.current_revision) : 'no current release') + '</div></section>' +
        '<section class="card"><h2>Secrets</h2><div class="metric">' + data.state.secret_ref_count + '</div><div class="muted">Names and stores only; values stay redacted.</div></section>' +
        '<section class="card wide"><h2>Attachments</h2><table><thead><tr><th>Environment</th><th>Nodes</th></tr></thead><tbody>' + attachmentRows + '</tbody></table></section>' +
        '<section class="card wide"><h2>Recent releases</h2><table><thead><tr><th>Release</th><th>Environment</th><th>Revision</th><th>Nodes</th><th>Created</th></tr></thead><tbody>' + releaseRows + '</tbody></table></section>' +
        '<section class="card wide"><h2>Next steps</h2>' + nextSteps + '</section>';
    }
    fetch('/api/summary').then(r => r.json()).then(data => {
      if (data.ok === false) throw new Error(data.error && data.error.message || 'summary failed');
      render(data);
    }).catch(err => {
      document.getElementById('app').innerHTML = '<div class="card wide error">' + esc(err.message) + '</div>';
    });
  </script>
</body>
</html>`
