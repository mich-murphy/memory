'use strict';
// All report and transcript content is untrusted. Construct nodes, never markup.
const $ = (s) => document.querySelector(s);
const node = (tag, cls, text) => {
  const n = document.createElement(tag);
  if (cls) n.className = cls;
  if (text !== undefined) n.textContent = String(text);
  return n;
};
const arr = (v) => Array.isArray(v) ? v : [];
const text = (v) => v == null ? 'Not reported' : typeof v === 'object' ? JSON.stringify(v, null, 2) : String(v);
const number = (v) => typeof v === 'number' && Number.isFinite(v) ? v.toLocaleString() : '—';
const timestamp = (v) => { const d = v ? new Date(v) : null; return d && Number.isFinite(d.getTime()) ? new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short' }).format(d) : text(v); };
const button = (label, action, cls = '') => { const b = node('button', `button ${cls}`, label); b.type = 'button'; b.addEventListener('click', action); return b; };
const badge = (label, cls = '') => node('span', `badge ${cls}`, label);
const append = (parent, ...children) => { children.filter(Boolean).forEach(c => parent.append(c)); return parent; };
const state = { report: null, tab: 'overview', agent: '', project: '', sessionsOffset: 0, sessionQuery: '', trace: null, line: null, traceOffset: 0, role: '', query: '', generation: 0, loading: false };
const sections = {
  overview: ['Overview', 'From raw conversations to evidence you can inspect.'],
  findings: ['Findings', 'Reviewed insights, traceable evidence, and proposals that stay yours to approve.'],
  corrections: ['Corrections', 'Candidate signals, not confirmed corrections. Inspect the question and context.'],
  discovery: ['Discovery & code map', 'Recurring discoveries and paths grounded in recorded activity.'],
  trace: ['Trace explorer', 'Read the source. Inspect original events without losing the surrounding context.'],
  health: ['Capture health', 'What was captured, what is missing, and what the data cannot tell you.'],
  pipeline: ['Pipeline & prompts', 'Inspect the process behind the report: inputs, questions, models and limitations.']
};
let reviews = {};
let storageWarning = '';
try { reviews = JSON.parse(localStorage.getItem('session-observatory-reviews-v1') || '{}'); if (!reviews || typeof reviews !== 'object' || Array.isArray(reviews)) reviews = {}; } catch { storageWarning = 'Local review storage is unavailable. Download feedback before leaving this page.'; }
async function api(path, params = {}) {
  const url = new URL(path, location.origin);
  Object.entries(params).forEach(([k, v]) => { if (v !== '' && v != null) url.searchParams.set(k, v); });
  const res = await fetch(url, { cache: 'no-store', headers: { Accept: 'application/json' } });
  if (!res.ok) throw new Error(`Local API returned HTTP ${res.status}.`);
  return res.json();
}
function empty(title, detail = 'No matching records were returned by the local report.') { return append(node('div', 'empty'), node('h3', '', title), node('p', '', detail)); }
function heading(title, note) { return append(node('div', 'section-heading'), node('h2', '', title), note ? node('p', '', note) : null); }
function panel(title, note) { const p = node('section', 'panel'); p.append(append(node('div', 'panel-head'), node('h3', '', title), note ? badge(note, 'neutral') : null)); return p; }
function details(label, value, open = false) { const d = node('details'); d.open = open; append(d, node('summary', '', label), node('pre', '', text(value))); return d; }
function kv(parent, key, value) { append(parent, node('dt', '', key), node('dd', '', text(value))); }
function scoped(item) { return (!state.agent || item.agent === state.agent) && (!state.project || item.project === state.project); }
function findingScoped(f) { if (!state.agent && !state.project) return true; return arr(f.evidence).some(scoped) || (f.scope && typeof f.scope === 'object' && scoped(f.scope)); }
function limitedText(value, mode = 'middle', size = 1300) {
  const s = text(value); const box = node('div');
  if (mode === 'full' || s.length <= size) { box.append(document.createTextNode(s)); return box; }
  let start = 0, end = size;
  if (mode === 'tail') { start = s.length - size; end = s.length; }
  if (mode === 'middle') { start = Math.floor((s.length - size) / 2); end = start + size; }
  if (start) box.append(node('span', 'omission', `[… ${number(start)} preceding characters omitted · choose Full for original text]`));
  box.append(document.createTextNode(s.slice(start, end)));
  if (end < s.length) box.append(node('span', 'omission', `[… ${number(s.length - end)} following characters omitted · choose Full for original text]`));
  return box;
}
function traceLink(e, label) {
  if (!e.trace_id || e.line == null) return node('span', 'muted small', 'Source location not reported');
  const a = node('a', '', label || `Original · line ${e.line}`);
  a.href = `#${new URLSearchParams({ trace: e.trace_id, line: e.line })}`;
  return a;
}
function evidence(e) {
  const box = node('div', 'evidence');
  append(box, append(node('div', 'evidence-meta'), node('span', '', [e.agent, e.project, e.session_key].filter(Boolean).join(' / ') || 'Source metadata not reported'), traceLink(e)));
  if (e.char_start != null || e.char_end != null) box.append(node('div', 'small muted', `Source character offsets: ${text(e.char_start)}–${text(e.char_end)} · exact quoted range as reported`));
  const quote = node('blockquote'); quote.append(limitedText(e.quote ?? e.text ?? e.note, 'middle', 650)); box.append(quote);
  if (text(e.quote ?? e.text ?? e.note).length > 650) box.append(details('Full evidence quote', e.quote ?? e.text ?? e.note));
  if (e.note && (e.quote || e.text)) box.append(node('div', 'small muted prose', e.note));
  return box;
}
function table(headers, rows) {
  const wrap = node('div', 'table-wrap'), t = node('table'), head = node('thead'), tr = node('tr');
  headers.forEach(h => tr.append(node('th', '', h))); head.append(tr); t.append(head); const body = node('tbody');
  rows.forEach(row => { const r = node('tr'); row.forEach(v => { const td = node('td'); td.append(v instanceof Node ? v : document.createTextNode(text(v))); r.append(td); }); body.append(r); });
  t.append(body); wrap.append(t); return wrap;
}
function render() {
  state.generation++;
  const [title, desc] = sections[state.tab]; $('#page-title').textContent = title; $('#page-description').textContent = desc;
  document.querySelectorAll('[data-tab]').forEach(b => { if (b.dataset.tab === state.tab) b.setAttribute('aria-current', 'page'); else b.removeAttribute('aria-current'); });
  const root = $('#content'); root.replaceChildren();
  if (!state.report) { root.append(state.loading ? node('div', 'loading', 'Reading the local report…') : empty('Report unavailable', 'Start the local analytics server, then select Refresh to retry.')); return; }
  if (state.report.source === 'publications') {
    publicationView(root);
    return;
  }
  const views = { overview, findings, corrections, discovery, trace, health, pipeline };
  views[state.tab](root);
}
// Publication coordinates and nanoseconds remain exact strings. Never pass NS to Date.
function publicationTime(value) {
  if (value == null || String(value) === '0') return 'Unknown';
  try { return `${timestamp(Number(BigInt(value) / 1000000n))} · ${value} ns`; }
  catch { return 'Unknown'; }
}
function publicationView(root) {
  const r = state.report;
  root.append(node('p', 'panel prose', `PUBLICATIONS CANDIDATE · Archive ${text(r.archive_id)}. Read-only imported evidence; Refresh does not capture, import or classify. Legacy findings and browser reviews remain separate and unavailable in this mode.`));
  if (state.tab === 'trace') { trace(root); return; }
  if (['findings', 'corrections', 'discovery', 'pipeline'].includes(state.tab)) {
    root.append(empty('Findings / classification / review unavailable', 'No classifier or research migration has run for this archive. This is not an empty successful analysis. Existing legacy findings, history and browser review storage are untouched.'));
    return;
  }
  root.append(heading('Publication coverage & freshness', 'Coverage is producer-declared, not a completeness guarantee'));
  const health = r.refresh_health;
  if (!health?.healthy) root.append(node('p', 'warning', `Publication refresh incomplete / unknown (${health?.mode || 'partial'}). Imported sessions are a subset, not archive coverage. Endpoint errors: ${text(health?.endpoint_errors)}; discovery errors: ${text(health?.discovery_errors)}; unimported: ${text(health?.unimported)}; target errors: ${text(health?.target_errors)}; stale: ${text(health?.stale)}.`));
  root.append(details('Independent publication refresh health', health || {mode: 'partial', healthy: false}, true));
  root.append(details('Selected / withheld source counts', r.source_counts, true));
  const freshness = node('dl', 'kv');
  ['captured_at_ns', 'published_at_ns', 'imported_at_ns'].forEach(k => kv(freshness, k, publicationTime(r.freshness?.[k])));
  kv(freshness, 'Served', publicationTime(r.served_at_ns)); root.append(freshness);
  root.append(details('Unknown freshness counts', { capture: r.freshness?.unknown_capture_sessions, publication: r.freshness?.unknown_publication_sessions }, true));
  root.append(table(['Machine / agent / session', 'Publication / sequence', 'Discovery / enrichment', 'Normalization', 'Captured', 'Published'], arr(r.sessions).map(s => [
    [s.machine_id, s.agent, s.session_id].join(' / '), `${s.publication_id} / ${s.sequence}`,
    `${s.coverage?.discovery} / ${s.coverage?.enrichment} · missing: ${arr(s.coverage?.missing).join(', ') || 'none declared'}`,
    s.normalization, publicationTime(s.captured_at_ns), publicationTime(s.published_at_ns)])));
  root.append(details('Import failures (empty does not prove complete capture)', r.failures, true));
  root.append(details('Normalization observations', r.issue_counts, true));
  root.append(node('p', 'muted', 'Project is unknown unless explicitly projected; it is never guessed from a filename. One trace is one effective source hash and epoch. Overlapping epochs are not merged. Opaque artifacts are not transcript line views.'));
  root.append(button('Explore sources / epochs →', () => switchTab('trace')));
}
function overview(root) {
  const r = state.report, m = r.metrics || {};
  root.append(heading('The collection at a glance', 'GLOBAL TOTALS · not changed by scope filters'));
  const cards = node('div', 'grid metrics');
  const metrics = [['Sessions with bodies', 'sessions', 'Imported main/subagent session identities'], ['Source files', 'files', 'Files included in this report'], ['Event components', 'events', 'Normalized components, not model turns'], ['Human source lines', 'human_messages', 'Distinct source lines · heuristic human attribution'], ['Tool calls', 'tool_calls', 'Recorded tool invocations'], ['Tool results', 'tool_results', 'Normalized result components'], ['Explicit errors', 'explicit_errors', 'Error-flagged result components'], ['Parser observations', 'parse_issues', 'Unsupported records + malformed lines']];
  metrics.forEach(([label, key, note]) => cards.append(append(node('div', 'metric'), node('div', 'metric-label', label), node('div', 'metric-value', number(m[key])), node('div', 'metric-note', note)))); root.append(cards);
  root.append(node('p', 'small muted', `Coverage: ${number(r.scope?.manifest_sessions)} manifest identities; ${number(m.sessions)} have imported main/subagent bodies. The remainder may contain launches/auxiliary artifacts only. See Capture health for missing-main references and source-format limits.`));
  root.append(node('p', 'small muted', `Duplicate events reported: ${number(m.duplicate_events)}. Missing values are shown as —, never treated as zero. Counts describe captured data, not completeness. Event components are not conversation turns; human attribution is heuristic.`));
  const cols = node('div', 'grid columns'); const agents = panel('Activity by agent', 'global');
  agents.append(node('p', 'small muted', 'Human-text source lines and tool calls, stacked against the largest combined count. Activity is not a quality ranking; fork/copy context is not globally deduplicated.'));
  const list = arr(r.agents); const max = Math.max(1, ...list.map(a => (Number(a.humans) || 0) + (Number(a.tools) || 0)));
  list.forEach(a => {
    const row = node('div', 'bar-row'); append(row, append(node('div', 'bar-label'), node('strong', '', a.agent), node('span', 'muted small', `${number(a.sessions)} sessions · ${number(a.events)} events`)));
    const track = node('div', 'bar-track'); [[a.humans, 'var(--teal)'], [a.tools, 'var(--coral)']].forEach(([v, color]) => { const b = node('span', 'bar-segment'); b.style.width = `${Math.max(0, (Number(v) || 0) / max * 100)}%`; b.style.background = color; track.append(b); });
    track.setAttribute('role', 'img'); track.setAttribute('aria-label', `${text(a.agent)}: ${number(a.humans)} human messages, ${number(a.tools)} tool calls`); append(row, track); agents.append(row);
  });
  if (!list.length) agents.append(empty('No agent totals reported'));
  const legend = node('div', 'legend'); [['Human source lines', 'var(--teal)'], ['Tool calls', 'var(--coral)']].forEach(([s, c]) => { const l = node('span'); const i = node('i'); i.style.background = c; append(l, i, document.createTextNode(s)); legend.append(l); }); agents.append(legend);
  const timeline = panel('Activity over time', 'global'); renderTimeline(timeline, arr(r.timeline)); append(cols, agents, timeline); root.append(cols);
  root.append(heading('Patterns & process', 'Follow the evidence, not just the frequency'));
  const lower = node('div', 'grid columns'), themes = panel('Top themes', 'global');
  arr(r.themes).slice(0, 12).forEach(t => themes.append(append(node('div', 'theme'), node('strong', '', t.name), node('span', '', number(t.count)), node('small', '', `${number(t.sessions)} distinct sessions · count as reported by pipeline`))));
  if (!arr(r.themes).length) themes.append(empty('No themes reported'));
  const process = panel('From sources to review'); process.append(node('p', 'muted small', 'A report is an interpretation of captured evidence. Candidate signals require human review.'));
  const flow = node('div', 'flow'); arr(r.pipeline?.steps).forEach((s, i) => flow.append(append(node('div', 'flow-step'), node('div', 'number', String(i + 1).padStart(2, '0')), node('h3', '', s.name), node('p', '', `${text(s.input)} → ${text(s.output)}`))));
  if (flow.childNodes.length) process.append(flow); else process.append(empty('Pipeline steps not reported'));
  process.append(button('Inspect pipeline & prompts →', () => switchTab('pipeline'))); append(lower, themes, process); root.append(lower);
}
function renderTimeline(parent, points) {
  if (!points.length || !points.some(p => [p.events, p.humans].some(v => typeof v === 'number' && Number.isFinite(v)))) { parent.append(empty('No numeric timeline reported')); return; }
  const ns = 'http://www.w3.org/2000/svg'; const svg = document.createElementNS(ns, 'svg'); svg.classList.add('chart'); svg.setAttribute('viewBox', '0 0 550 170'); svg.setAttribute('role', 'img');
  const title = document.createElementNS(ns, 'title'); title.textContent = 'Global recorded events and human messages by date. Exact counts in the table below.'; svg.append(title);
  const max = Math.max(1, ...points.flatMap(p => [Number(p.events) || 0, Number(p.humans) || 0]));
  [0, .5, 1].forEach(v => { const l = document.createElementNS(ns, 'line'); l.setAttribute('x1', '40'); l.setAttribute('x2', '535'); l.setAttribute('y1', String(140 - v * 120)); l.setAttribute('y2', String(140 - v * 120)); svg.append(l); const t = document.createElementNS(ns, 'text'); t.setAttribute('x', '0'); t.setAttribute('y', String(144 - v * 120)); t.textContent = number(Math.round(max * v)); svg.append(t); });
  [['events', '#087f78'], ['humans', '#d66e54']].forEach(([key, color]) => {
    const line = document.createElementNS(ns, 'path'); let connected = false; const segments = [];
    points.forEach((p, i) => { if (typeof p[key] !== 'number' || !Number.isFinite(p[key])) { connected = false; return; } segments.push(`${connected ? 'L' : 'M'}${40 + i / Math.max(1, points.length - 1) * 495},${140 - p[key] / max * 120}`); connected = true; });
    line.setAttribute('d', segments.join(' ')); line.setAttribute('fill', 'none'); line.setAttribute('stroke', color); line.setAttribute('stroke-width', '2.5'); svg.append(line);
    if (points.length === 1 && typeof points[0][key] === 'number' && Number.isFinite(points[0][key])) { const dot = document.createElementNS(ns, 'circle'); dot.setAttribute('cx', '40'); dot.setAttribute('cy', String(140 - (Number(points[0][key]) || 0) / max * 120)); dot.setAttribute('r', '4'); dot.setAttribute('fill', color); svg.append(dot); }
  }); parent.append(svg);
  parent.append(node('div', 'small muted', `${text(points[0].date)} — ${text(points[points.length - 1].date)} · Teal: events / Coral: human messages`));
  const d = node('details'); append(d, node('summary', '', 'Exact daily counts'), table(['Date', 'Events', 'Human messages'], points.map(p => [p.date, number(p.events), number(p.humans)]))); parent.append(d);
}
function findings(root) {
  const fs = arr(state.report.findings).filter(findingScoped);
  root.append(heading('Evidence-backed proposals', `${fs.length} in selected scope · reviews stored only in this browser`));
  root.append(node('p', 'small muted', 'Analyst-reviewed means the evidence and interpretation were reviewed during report preparation. It is not your approval. Your separate local decision appears below each proposal.'));
  root.append(button('Download review feedback JSON', downloadFeedback));
  if (storageWarning) root.append(node('p', 'muted', storageWarning));
  if (!fs.length) root.append(empty('No findings in this scope', 'Try another agent or project. The dashboard does not invent findings from activity counts.'));
  fs.forEach(f => {
    const card = node('article', 'finding'); const review = reviews[f.id] || {};
    append(card, append(node('div', 'finding-top'), append(node('div'), badge(f.priority || 'Priority not reported', 'coral'), badge(f.category || 'Uncategorized', 'neutral')), badge(f.status === 'reviewed' ? 'Analyst-reviewed' : 'Candidate', f.status === 'reviewed' ? '' : 'gold')));
    append(card, node('h2', '', f.title || 'Untitled finding'), node('p', 'prose finding-summary', text(f.summary)));
    const ev = arr(f.evidence); const sessions = new Set(ev.filter(e => e.session_key || e.trace_id).map(e => JSON.stringify([e.agent, e.project, e.session_key || e.trace_id]))).size;
    const projects = new Set(ev.map(e => e.project).filter(Boolean)).size;
    card.append(node('p', 'small muted', `${ev.length} evidence references · ${sessions} distinct referenced sessions · ${projects} named projects. Counts reflect supplied evidence, not all supporting records.`));
    if (f.scope) card.append(details('Finding scope', f.scope));
    if (f.reasoning) card.append(details('Reasoning & interpretation', f.reasoning));
    if (arr(f.actions).length) { card.append(node('h4', '', 'PROPOSED ACTIONS')); arr(f.actions).forEach(a => card.append(append(node('p', 'prose'), badge(a.kind || 'Action'), document.createTextNode(text(a.text))))); }
    if (f.memory) append(card, node('h4', '', 'CONDITIONAL MEMORY PROPOSAL · NOT INSTALLED'), node('p', 'small muted', 'Adopt only when the conditions and evidence apply. Approval here does not modify any agent memory.'), node('div', 'memory', text(f.memory)));
    const ed = node('details'); ed.open = true; ed.append(node('summary', '', 'Inspect source evidence')); ev.forEach(e => ed.append(evidence(e))); if (!ev.length) ed.append(empty('No source evidence supplied')); card.append(ed);
    const reviewBox = node('div', 'review'); const notes = node('textarea'); notes.placeholder = 'Review notes: conditions, counterexamples, or why this should not persist…'; notes.setAttribute('aria-label', `Review notes for ${f.title || f.id}`); notes.value = review.notes || '';
    const status = node('span', 'review-status', review.state || 'Not reviewed locally');
    const save = (decision) => { if (!f.id) return; reviews[f.id] = { state: decision, notes: notes.value, updated_at: new Date().toISOString() }; status.textContent = decision; try { localStorage.setItem('session-observatory-reviews-v1', JSON.stringify(reviews)); } catch { status.textContent += ' · storage unavailable; download feedback'; } };
    notes.addEventListener('change', () => save(reviews[f.id]?.state || 'not-reviewed'));
    const controls = append(node('div', 'actions'), button('Approve proposal', () => save('approved'), 'primary'), button('Reject', () => save('rejected'), 'danger'), button('Reset review', () => save('not-reviewed')), status);
    if (!f.id) { controls.querySelectorAll('button').forEach(b => b.disabled = true); status.textContent = 'Missing finding ID; cannot store review'; }
    append(reviewBox, node('h4', '', 'YOUR LOCAL REVIEW · SEPARATE FROM ANALYST STATUS'), notes, controls); card.append(reviewBox); root.append(card);
  });
}
function downloadFeedback() {
  const blob = new Blob([JSON.stringify({ schema_version: 1, exported_at: new Date().toISOString(), report_built_at: state.report?.built_at, scope: { agent: state.agent, project: state.project }, reviews }, null, 2)], { type: 'application/json' });
  const url = URL.createObjectURL(blob), a = node('a'); a.href = url; a.download = 'session-review-feedback.json'; a.click(); setTimeout(() => URL.revokeObjectURL(url), 1000);
}
function corrections(root) {
  root.append(node('p', 'muted', 'These are sampled correction candidates from a stratified classifier selection, not a population census, verified corrections, or instructions to remember. Counts and probabilities describe the supplied sample and classifier output, not correction prevalence or ground truth.'));
  const items = arr(state.report.corrections).filter(scoped);
  root.append(heading('Candidate correction signals', `${items.length} in selected scope`));
  items.forEach(c => {
    const p = panel(c.id || 'Candidate signal', 'unconfirmed'); p.append(evidence({ ...c, quote: c.text }));
    p.append(details('Exact preceding input context', c.previous, true));
    const cl = c.classification || {};
    // Keep the entire classifier result: backend-specific questions, Jev output,
    // probabilities and usage must remain inspectable without lossy guessing.
    p.append(details('Exact candidate input text', c.text, true));
    if (c.questions || cl.questions || cl.question || cl.prompt) p.append(details('Jev / classifier questions', c.questions ?? cl.questions ?? cl.question ?? cl.prompt, true));
    else p.append(node('p', 'small muted', 'Per-candidate question not reported. Inspect pipeline questions for shared prompts.'));
    if (c.state !== undefined) p.append(details('Candidate state', c.state));
    p.append(details('Classifier output · answers, probabilities, model & usage', cl, true));
    if (c.review_note) p.append(node('p', 'prose', c.review_note)); root.append(p);
  });
  if (!items.length) root.append(empty('No correction candidates in this scope'));
}
function discovery(root) {
  root.append(heading('Questions worth carrying forward', 'Analyst-reviewed groups · cards filtered by referenced evidence'));
  const groups = arr(state.report.discovery_groups).filter(findingScoped);
  groups.forEach(g => {
    const card = node('article', 'finding discovery-group');
    append(card, badge('Analyst-reviewed question group'), node('h2', '', g.title || 'Discovery question'), node('p', 'discovery-question prose', text(g.question)), node('p', 'prose', text(g.summary)));
    const stats = node('div', 'group-stats');
    [['Sessions', g.session_count], ['Source traces', g.trace_count], ['Tool calls', g.tool_calls]].forEach(([label, value]) => stats.append(append(node('div'), node('strong', '', number(value)), node('span', '', label))));
    append(card, stats, node('p', 'small muted', 'Group-wide supporting counts; scope filters select cards by evidence but do not recompute these totals.'));
    if (g.recommendation) append(card, node('h4', '', 'RECOMMENDATION · NOT AN INSTALLED INSTRUCTION'), node('p', 'prose', text(g.recommendation)));
    if (arr(g.read_references).length) { const refs = node('details'); append(refs, node('summary', '', 'Referenced reads'), table(['Path', 'Recorded calls'], arr(g.read_references).map(r => [r.path, number(r.calls)]))); card.append(refs); }
    const sources = node('details'); sources.open = true; sources.append(node('summary', '', 'Inspect supporting evidence')); arr(g.evidence).forEach(e => sources.append(evidence(e))); if (!arr(g.evidence).length) sources.append(empty('No supporting quotes supplied')); card.append(sources); root.append(card);
  });
  if (!groups.length) root.append(empty('No reviewed question groups in this scope'));
  root.append(heading('Individual discoveries', 'Selected agent & project scope'));
  const items = arr(state.report.discoveries).filter(d => arr(d.evidence).length ? d.evidence.some(scoped) : scoped(d));
  items.forEach(d => { const p = panel(d.title || d.name || 'Recorded discovery'); if (d.summary || d.text) p.append(node('p', 'prose', d.summary || d.text)); if (arr(d.evidence).length) arr(d.evidence).forEach(e => p.append(evidence(e))); else p.append(evidence(d)); p.append(details('Complete discovery record', d)); root.append(p); });
  if (!items.length) root.append(empty('No discoveries in this scope'));
  root.append(heading('Code map · recorded path activity', 'GLOBAL · scope filters do not change path aggregates'));
  const paths = arr(state.report.paths);
  if (paths.length) {
    const maxCalls = Math.max(1, ...paths.map(p => Number(p.calls) || 0));
    const heatmap = node('div', 'path-map');
    paths.slice(0, 48).forEach(p => { const tile = node('div', 'path-tile'); tile.style.setProperty('--heat', String(.04 + .18 * Math.max(0, (Number(p.calls) || 0) / maxCalls))); append(tile, node('div', 'path-name', text(p.path)), node('div', 'small muted', `${number(p.calls)} calls · ${number(p.sessions)} sessions · ${p.project || 'Project not reported'}`)); heatmap.append(tile); });
    root.append(node('p', 'small muted', 'Darker teal indicates more recorded path references relative to the largest count. A reference in tool input is not proof of a successful read or wasted discovery. Up to 48 paths shown; exact counts for all reported paths below.'), heatmap);
    const full = node('details'); append(full, node('summary', '', `All ${number(paths.length)} reported paths · exact counts`), table(['Path', 'Project', 'Calls', 'Sessions'], paths.map(p => [p.path, p.project, number(p.calls), number(p.sessions)]))); root.append(full);
  } else root.append(empty('No paths reported'));
  root.append(heading('Tool activity', 'GLOBAL · filters do not change these totals'));
  const tools = arr(state.report.tools); root.append(tools.length ? table(['Tool', 'Calls', 'Explicit errors'], tools.map(t => [t.name, number(t.calls), number(t.errors)])) : empty('No tool totals reported'));
  root.append(heading('Project activity', 'GLOBAL · filters do not change these totals'));
  const projects = arr(state.report.projects); root.append(projects.length ? table(['Project', 'Sessions', 'Events', 'Tool calls'], projects.map(p => [p.project, number(p.sessions), number(p.events), number(p.tools)])) : empty('No project totals reported'));
}
function health(root) {
  const m = state.report.metrics || {};
  root.append(heading('Capture is not completeness', 'GLOBAL · source coverage, not a success rate'));
  root.append(node('p', 'prose muted', `Reported parse issues: ${number(m.parse_issues)}. Duplicate events: ${number(m.duplicate_events)}. Missing-main-file and gap counts below are shown only when the pipeline reports them. An empty issue list is not proof of complete capture.`));
  const hs = arr(state.report.health);
  hs.forEach(h => { const p = node('section', `panel health-card ${h.severity === 'error' || h.severity === 'critical' ? 'error' : ''}`); append(p, node('span', 'count', number(h.count)), badge(h.severity || 'Unspecified', 'gold'), node('h3', '', h.title || 'Capture issue'), node('p', 'prose', text(h.detail))); arr(h.evidence).forEach(e => p.append(typeof e === 'object' && e !== null ? evidence(e) : node('p', 'prose', text(e)))); if (arr(h.records).length) p.append(details(`Affected identities · ${h.records.length} source records`, h.records)); root.append(p); });
  if (!hs.length) root.append(empty('No health issues supplied', 'The report has not supplied a gap list. Do not interpret this as a clean bill of health.'));
  root.append(heading('Known limitations'));
  const limitations = [...arr(state.report.limitations), ...arr(state.report.pipeline?.limitations)];
  limitations.forEach(l => root.append(node('p', 'panel prose', text(l)))); if (!limitations.length) root.append(empty('No limitations documented', 'Capture boundaries and inference quality remain unknown.'));
  root.append(details('Report scope & provenance', state.report.scope || {}));
}
function pipeline(root) {
  const p = state.report.pipeline || {};
  root.append(heading('A transparent processing chain', 'GLOBAL · as reported by the local pipeline'));
  arr(p.steps).forEach((s, i) => {
    const box = node('section', 'panel pipeline-step'), body = node('div'); append(body, node('h3', '', s.name || `Step ${i + 1}`), node('p', 'prose muted', text(s.description)));
    const dl = node('dl', 'kv'); kv(dl, 'Input', s.input); kv(dl, 'Output', s.output); body.append(dl); body.append(details('Full step metadata', s)); append(box, node('span', 'step-index', i + 1), body); root.append(box);
  });
  if (!arr(p.steps).length) root.append(empty('Pipeline steps not reported'));
  const meta = panel('Model, run & accounting'); const dl = node('dl', 'kv'); kv(dl, 'Model', p.model);
  const run = p.run;
  if (run && typeof run === 'object') {
    // Large selection lists belong in the expandable original, not the summary.
    const summary = Object.fromEntries(Object.entries(run).filter(([, v]) => !Array.isArray(v) && (v === null || typeof v !== 'object')));
    kv(dl, 'Run summary', summary);
    kv(dl, 'Reported cost (USD)', run.reported_cost_usd == null ? 'Unknown · SDK catalog zero is not evidence of a free run' : run.reported_cost_usd);
  } else kv(dl, 'Run', run);
  meta.append(dl); if (run && typeof run === 'object') meta.append(details('Full run metadata & selection lists', run));
  ['cache', 'usage', 'cost', 'errors'].forEach(k => { if (p[k] !== undefined) meta.append(details(k, p[k], true)); });
  meta.append(node('p', 'small muted', 'Unreported cache, token usage, cost or errors are unknown, not zero. Expanded run metadata may contain additional accounting.')); meta.append(details('Complete pipeline metadata', p)); root.append(meta);
  root.append(heading('Question & prompt registry', 'Full text · copyable · never rendered as HTML'));
  const questions = p.questions;
  const entries = questions && typeof questions === 'object' ? Object.entries(questions) : questions ? [['Prompt', questions]] : [];
  entries.forEach(([key, value]) => { const box = panel(key); const full = text(value); box.append(node('pre', '', full)); const status = node('span', 'small muted'); box.append(append(node('div', 'actions'), button('Copy full prompt', async () => { try { await navigator.clipboard.writeText(full); status.textContent = 'Copied'; } catch { status.textContent = 'Clipboard unavailable. Select and copy the full text above.'; } }), status)); root.append(box); });
  if (!entries.length) root.append(empty('No prompt text reported', 'Model output cannot be audited fully without the question and prompt text.'));
  root.append(heading('Iteration history', 'Reported revisions · not an inferred improvement claim'));
  arr(p.iterations).forEach((iteration, i) => root.append(details(`Iteration ${i + 1}${iteration && typeof iteration === 'object' && (iteration.title || iteration.name) ? ` · ${iteration.title || iteration.name}` : ''}`, iteration, true)));
  if (!arr(p.iterations).length) root.append(node('p', 'muted small', 'No iteration history supplied.'));
  root.append(heading('Evaluation & limitations')); root.append(details('Reported evaluation', p.evaluation || {}, true));
  arr(p.limitations).forEach(l => root.append(node('p', 'panel prose', text(l))));
}
function searchForm(placeholder, value, onSearch, extra) {
  const f = node('form', 'search-form'), input = node('input'); input.type = 'search'; input.placeholder = placeholder; input.value = value; input.setAttribute('aria-label', placeholder);
  append(f, input, extra, button('Search', () => onSearch(input.value)));
  f.addEventListener('submit', e => { e.preventDefault(); onSearch(input.value); }); return f;
}
function pagination(offset, count, total, limit, action) {
  const prev = button('← Previous', () => action(Math.max(0, offset - limit))); prev.disabled = offset <= 0;
  const next = button('Next →', () => action(offset + limit)); next.disabled = typeof total !== 'number' || offset + count >= total || count === 0;
  return append(node('div', 'pagination'), prev, node('span', '', count ? `${number(offset + 1)}–${number(offset + count)} / ${number(total)}` : `0 / ${number(total)}`), next);
}
async function trace(root) {
  const generation = state.generation;
  root.append(node('p', 'small muted', 'Session list follows agent / project filters. An opened deep-linked trace remains visible even when outside that scope. Role and text search apply only to its messages.'));
  const layout = node('div', 'trace-layout'), sidebar = node('section'), viewer = node('section');
  sidebar.append(node('h3', '', 'Source sessions'), searchForm('Search sessions', state.sessionQuery, q => { state.sessionQuery = q; state.sessionsOffset = 0; render(); }));
  const list = node('div', 'session-list'); list.append(node('div', 'loading', 'Loading sessions…')); sidebar.append(list); append(layout, sidebar, viewer); root.append(layout);
  api('/api/sessions', { agent: state.agent, project: state.project, q: state.sessionQuery, offset: state.sessionsOffset, limit: 50 }).then(data => {
    if (generation !== state.generation) return; list.replaceChildren();
    arr(data.items).forEach(s => { const b = node('button', `session-button ${state.trace === s.trace_id ? 'active' : ''}`); append(b, node('strong', '', `${s.agent || 'Unknown agent'} / ${s.project || 'Unknown project'}`), node('span', '', s.session_key || s.file_key || s.trace_id), node('span', '', `${number(s.events)} events · ${number(s.humans)} human · ${number(s.tools)} tools · ${number(s.errors)} errors`), node('span', '', state.report.source === 'publications' ? `${s.source_role} · epoch ${s.epoch_id} · revision ${s.revision} · ${s.withheld_reason ? `withheld: ${s.withheld_reason}` : 'selected'}` : [s.source_role, s.started_at].filter(Boolean).join(' · '))); b.addEventListener('click', () => openTrace(s.trace_id)); list.append(b); });
    if (!arr(data.items).length) list.append(empty('No matching sessions'));
    sidebar.append(pagination(state.sessionsOffset, arr(data.items).length, data.total, 50, o => { state.sessionsOffset = o; render(); }));
  }).catch(err => { if (generation === state.generation) list.replaceChildren(empty('Could not load sessions', err.message)); });
  if (!state.trace) { viewer.append(empty('Choose a source session', 'Every evidence link opens its trace and original source line here.')); return; }
  viewer.append(node('h3', '', 'Trace messages'), node('p', 'small muted prose', state.trace));
  const citation = new URLSearchParams(location.hash.slice(1));
  const citedLine = citation.get('trace') === state.trace && /^\d+$/.test(citation.get('line') || '') ? Number(citation.get('line')) : state.line;
  if (citedLine != null && state.report.source !== 'publications') viewer.append(append(node('div', 'actions'), button(`Raw cited source · line ${citedLine}`, () => showRaw(state.trace, citedLine)), node('span', 'small muted', 'Available independently of normalized events, including malformed source lines.')));
  const role = node('select'); role.setAttribute('aria-label', 'Message role');
  [['', 'All roles'], ['human', 'Human (is_human)'], ['assistant', 'Assistant'], ['tool', 'Tool'], ['system', 'System']].forEach(([v, label]) => { const o = node('option', '', label); o.value = v; role.append(o); }); role.value = state.role;
  role.addEventListener('change', () => { state.role = role.value; state.traceOffset = 0; state.line = null; render(); });
  viewer.append(searchForm('Search full message text', state.query, q => { state.query = q; state.traceOffset = 0; state.line = null; render(); }, role));
  if (state.role || state.query) viewer.append(button('Clear filters · show surrounding context', () => { state.role = ''; state.query = ''; state.traceOffset = 0; render(); }));
  const messages = node('div'); messages.append(node('div', 'loading', 'Loading source events…')); viewer.append(messages);
  try {
    const data = await api('/api/trace', { id: state.trace, offset: state.traceOffset, limit: 60, role: state.role, q: state.query, line: state.line });
    if (generation !== state.generation) return; messages.replaceChildren();
    const meta = node('details'); append(meta, node('summary', '', 'Session identity & source metadata'), node('pre', '', text(data.file))); messages.append(meta);
    if (state.report.source === 'publications' && citedLine != null && !data.file?.withheld_reason && data.file?.source_role !== 'artifact') messages.append(button(`Raw selected source · line ${citedLine}`, () => showRaw(state.trace, citedLine)));
    const offset = typeof data.offset === 'number' ? data.offset : state.traceOffset;
    const paging = () => pagination(offset, arr(data.items).length, data.total, 60, o => { state.traceOffset = o; state.line = null; render(); });
    messages.append(paging());
    arr(data.items).forEach(e => messages.append(eventCard(e, data.file || {})));
    if (!arr(data.items).length) messages.append(empty('No events match', data.file?.withheld_reason ? `Source withheld: ${data.file.withheld_reason}. Raw access unavailable.` : data.file?.source_role === 'artifact' ? 'Opaque artifact: normalization and transcript raw-line view unavailable.' : 'Clear search or role filters to inspect surrounding context.'));
    if (state.report.source === 'publications') {
      const issues = await api('/api/issues', { trace: state.trace, limit: 100 });
      if (generation !== state.generation) return;
      const observations = panel('Source observations', `${issues.total} total · first 100`);
      arr(issues.items).forEach(i => observations.append(append(node('p'), node('span', '', `${i.kind} · line ${i.line} `), i.line > 0 && !data.file?.withheld_reason && data.file?.source_role !== 'artifact' ? button('Inspect raw line', () => showRaw(state.trace, i.line)) : null)));
      messages.append(observations);
    }
    messages.append(paging());
    const focus = messages.querySelector('.event.focus'); if (focus) focus.scrollIntoView({ block: 'center', behavior: 'instant' });
  } catch (err) { if (generation === state.generation) messages.replaceChildren(empty('Could not read trace', err.message)); }
}
function eventCard(e, file) {
  const focused = state.line != null && String(state.line) === String(e.line);
  const card = node('article', `event ${focused ? 'focus' : ''}`); const head = node('div', 'event-head');
  append(head, badge(e.is_human ? 'human' : e.role || 'unknown', e.is_human ? '' : 'neutral'), node('strong', '', `Line ${text(e.line)}`), node('span', '', `Event ${text(e.event_id)} · ordinal ${text(e.ordinal)}`));
  if (e.event_type) head.append(badge(e.event_type, 'neutral'));
  if (e.is_error) head.append(badge('Explicit error', 'coral')); if (e.duplicate) head.append(badge('Duplicate', 'gold')); card.append(head);
  card.append(node('p', 'small muted prose', [file.agent, file.project, file.session_key || file.file_key, file.source_role, e.model && `Model: ${text(e.model)}`, e.timestamp, e.raw_type && `Type: ${e.raw_type}`].filter(Boolean).join(' · ') || 'Source identity not reported'));
  const body = node('div', 'event-text'), controls = node('div', 'event-controls');
  const show = mode => { body.replaceChildren(limitedText(e.text ?? '', mode)); controls.querySelectorAll('[data-mode]').forEach(b => b.setAttribute('aria-pressed', String(b.dataset.mode === mode))); };
  ['middle', 'head', 'tail', 'full'].forEach(mode => { const b = button(mode === 'full' ? 'Full original text' : mode[0].toUpperCase() + mode.slice(1), () => show(mode)); b.dataset.mode = mode; controls.append(b); });
  append(card, controls, body); show('middle');
  if (e.tool_name) card.append(node('h4', '', `TOOL · ${e.tool_name}`));
  if (e.tool_args != null) card.append(details('Tool input / arguments · full', e.tool_args, true));
  if (e.role === 'tool' || e.event_type === 'tool_result' || e.tool_output != null || e.tool_result != null) card.append(details('Tool output · full', e.tool_output ?? e.tool_result ?? e.text, true));
  const actions = node('div', 'actions'); append(actions, traceLink({ trace_id: state.trace, line: e.line }, 'Permalink to source line'), button('Surrounding context', () => { state.role = ''; state.query = ''; state.traceOffset = 0; state.line = e.line; render(); }), button('Original JSON event', () => showRaw(state.trace, e.line))); card.append(actions); return card;
}
let rawRequest = 0;
async function showRaw(traceId, line) {
  const request = ++rawRequest; const dialog = $('#raw-dialog'); $('#raw-description').textContent = 'Original source returned by the local server. Rendered as text, never executed.'; $('#raw-content').textContent = 'Loading original source event…'; if (!dialog.open) dialog.showModal();
  try {
    const data = await api('/api/event', { trace: traceId, line });
    if (request === rawRequest) {
      $('#raw-description').textContent = state.report?.source === 'publications' ? `Selected source line ${line} · exact UTF-8 text (invalid bytes replaced for display); never executed. JSON valid: ${data.valid_json}.` : data.valid_json === false ? `Source line ${line} is not valid JSON. Exact raw source text is shown below; it was not parsed or executed.` : `Source line ${line} · parsed original JSON returned by the local server. Rendered as text, never executed.`;
      $('#raw-content').textContent = data.valid_json === false || state.report?.source === 'publications' ? text(data.raw_text ?? data.raw) : text(data.raw);
    }
  }
  catch (err) { if (request === rawRequest) $('#raw-content').textContent = `Could not load original event: ${err.message}`; }
}
function switchTab(tab) { state.tab = tab; render(); }
function openTrace(id, line = null) { if (!id) return; const p = new URLSearchParams({ trace: id }); if (line != null) p.set('line', line); const hash = `#${p}`; if (location.hash === hash) readHash(); else location.hash = hash; }
function readHash() {
  const p = new URLSearchParams(location.hash.slice(1)); if (!p.get('trace')) return;
  state.trace = p.get('trace'); state.line = /^\d+$/.test(p.get('line') || '') ? Number(p.get('line')) : null; state.traceOffset = 0; state.query = ''; state.role = ''; switchTab('trace');
}
function populateFilters() {
  [['#agent-filter', 'agent', state.report.source === 'publications' ? arr(state.report.sessions).map(s => s.agent) : arr(state.report.agents).map(a => a.agent), 'All agents'], ['#project-filter', 'project', arr(state.report.projects).map(p => p.project), 'All projects']].forEach(([selector, key, values, label]) => {
    const select = $(selector); select.replaceChildren(); const all = node('option', '', label); all.value = ''; select.append(all);
    const unique = [...new Set([...values, state[key]].filter(Boolean))].sort(); unique.forEach(v => { const o = node('option', '', v); o.value = v; select.append(o); }); select.value = state[key];
  });
}
async function load() {
  if (state.loading) return; state.loading = true; $('#refresh').disabled = true; $('#notice').textContent = ''; if (!state.report) render();
  try {
    state.report = await api('/api/report'); populateFilters();
    $('#timestamps').textContent = state.report.source === 'publications' ? `Publications candidate · served ${publicationTime(state.report.served_at_ns)}` : `Built: ${timestamp(state.report.built_at)}\nObserved: ${timestamp(state.report.observed_at)} · local time`;
    $('#timestamps').title = `Original build timestamp: ${text(state.report.built_at)}\nOriginal observation timestamp: ${text(state.report.observed_at)}`;
    $('#findings-count').textContent = arr(state.report.findings).length || '';
    state.loading = false; render();
  } catch (err) { $('#notice').textContent = `Unable to refresh report. ${err.message} ${state.report ? 'Showing the last successfully loaded report.' : 'Check the local server, then retry.'}`; state.loading = false; if (!state.report) render(); }
  finally { state.loading = false; $('#refresh').disabled = false; }
}
$('#refresh').addEventListener('click', load);
document.querySelectorAll('[data-tab]').forEach(b => b.addEventListener('click', () => switchTab(b.dataset.tab)));
[['#agent-filter', 'agent'], ['#project-filter', 'project']].forEach(([s, key]) => $(s).addEventListener('change', e => { state[key] = e.target.value; state.sessionsOffset = 0; render(); }));
$('#close-dialog').addEventListener('click', () => $('#raw-dialog').close());
window.addEventListener('hashchange', readHash);
readHash(); load();
