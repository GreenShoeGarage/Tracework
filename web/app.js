// SPDX-License-Identifier: GPL-3.0-only
import { ExtensionsUI } from './extensions.js';
import { AccountsUI } from './auth.js';
import { ChecksUI } from './checks.js';
import { ActionsUI } from './actions.js';
import { InvestigationUI } from './investigation.js';
import { Workbench } from './workbench.js';
const $ = (id) => document.getElementById(id);
const esc = (s) => String(s ?? '').replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
const show = (id, visible) => $(id).hidden = !visible;
const text = (id, value) => { const v = String(value ?? ''); if ($(id).textContent !== v)
    $(id).textContent = v; };
const on = (id, fn) => $(id).addEventListener('click', () => { Promise.resolve().then(fn).catch(reportError); });
function setting(key, fallback) { try {
    return localStorage.getItem('tracework.' + key) ?? fallback;
}
catch {
    return fallback;
} }
function saveSetting(key, value) { try {
    localStorage.setItem('tracework.' + key, value);
    text('saveState', '● View saved');
}
catch {
    text('saveState', '! View could not be saved');
} }
let token = '';
try {
    token = sessionStorage.getItem('tracework.token') ?? '';
}
catch { }
let projects = [], project = null, sessions = [], selectedSession = null;
let events = [], sourceStates = {}, selected = new Set(), cursor = 0, follow = true, tab = 'plot', online = false, busy = false, generation = 0, next = 0, more = false, page = 1;
let modalSubmit = null, sourceSignature = '', recordSignature = '', lastStatus = 0;
let socket = null, streamRetry = 0, liveEvents = [], agentHealth = [];
let advanced = setting('mode', 'easy') === 'advanced';
const colors = ['#cef278', '#7fd9d1', '#ffbc83', '#c3b5f4', '#87c0ff', '#f4a8c3', '#ddda8a', '#aee0c0'];
async function api(path, method = 'GET', data) { const r = await fetch('/api/v1' + path, { method, headers: { 'Content-Type': 'application/json', ...(token ? { Authorization: 'Bearer ' + token } : {}) }, ...(data === undefined ? {} : { body: JSON.stringify(data) }) }); if (r.status === 401) {
    if (!$('dialog').open)
        login();
    throw Error('Sign in to this server to continue.');
} if (!r.ok) {
    const b = await r.json().catch(() => ({ error: r.statusText }));
    throw Error(b.error ?? r.statusText);
} return r.json(); }
function reportError(e) { if ($('dialog').open) {
    text('dialogError', e instanceof Error ? e.message : String(e));
    return;
} text('error', e instanceof Error ? e.message : String(e)); show('error', true); }
function notify(s) { text('notice', s); show('notice', true); setTimeout(() => show('notice', false), 6500); }
function clearError() { show('error', false); }
function bindings(p = selectedSession?.project ?? project) { if (!p)
    return []; return p.sources.flatMap(src => src.events.flatMap(sc => sc.fields.map(f => ({ src, sc, f, key: src.id + '/' + sc.id + '/' + sc.version + '/' + f.id })))); }
function picked() { return bindings().filter(b => selected.has(b.key)).slice(0, 8); }
function remember() { if (project)
    saveSetting('signals.' + project.id, JSON.stringify([...selected])); }
function dialog(title, html, submit, fn = null) { text('dialogTitle', title); $('dialogBody').innerHTML = html; text('dialogError', ''); show('submitDialog', !!submit); text('submitDialog', submit ?? ''); text('cancelDialog', submit ? 'Cancel' : 'Close'); modalSubmit = fn; const d = $('dialog'); if (!d.open)
    d.showModal(); setTimeout(() => $('dialogBody').querySelector('input,button,select,textarea')?.focus(), 30); }
function closeDialog() { $('dialog').close(); modalSubmit = null; }
function login() { void accounts.login().catch(reportError); }
function newProject() { dialog('Create a blank project', `<p>Start with your question or system. Add only the sources and signals you need.</p><label for="projectName">Project name</label><input id="projectName" placeholder="e.g. Kiln temperature study" maxlength="160" required><label for="projectDescription">Purpose / notes</label><textarea id="projectDescription" placeholder="What are you observing?" maxlength="4096"></textarea>`, 'Create project', async () => { const p = await api('/projects', 'POST', { name: $('projectName').value, description: $('projectDescription').value, sources: [] }); closeDialog(); projects.push(p); await selectProject(p.id); notify('Blank project created. Define your first source.'); sourceDialog(); }); }
function projectOptions() { const sel = $('projectSelect'); sel.innerHTML = '<option value="">Choose a project</option>' + projects.map(p => `<option value="${esc(p.id)}">${p.sample ? '[SYNTHETIC] ' : ''}${esc(p.name)}</option>`).join(''); sel.value = project?.id ?? ''; }
async function selectProject(pid) { extensions.reset(); checker.reset(); operator.reset(); investigator.reset(); pauseReplay(); selection = {}; rangeCount = 0; stopStream(); generation++; project = projects.find(p => p.id === pid) ?? null; selectedSession = null; sessions = []; events = []; sourceStates = {}; cursor = 0; follow = true; page = 1; sourceSignature = ''; recordSignature = ''; selected.clear(); if (project) {
    saveSetting('project', project.id);
    try {
        selected = new Set(JSON.parse(setting('signals.' + pid, '[]')).map(k => k.split('/').length === 3 ? k.split('/').slice(0, 2).join('/') + '/1/' + k.split('/')[2] : k));
    }
    catch { }
    if (!selected.size)
        bindings().slice(0, 4).forEach(b => selected.add(b.key));
} projectOptions(); show('welcome', !project); show('workbench', !!project); $('addSource').toggleAttribute('disabled', !project || !!project.read_only); render(); await workbench.load(project?.id ?? ''); if (project) {
    startStream();
    await refreshProject();
} }
function fieldRow(name = '', type = 'float64', unit = '') { const row = document.createElement('div'); row.className = 'fieldrow'; row.innerHTML = `<input class="fname" aria-label="Field name" placeholder="field_name" value="${esc(name)}" required pattern="[a-zA-Z0-9_-]{1,80}"><select class="ftype" aria-label="Field type">${['float64', 'int64', 'uint64', 'bool', 'string', 'enum'].map(t => `<option ${t === type ? 'selected' : ''}>${t}</option>`).join('')}</select><input class="funit" aria-label="Field unit" placeholder="unit" value="${esc(unit)}" maxlength="32"><button type="button" class="removefield icon" aria-label="Remove field">×</button><input class="enum" aria-label="Enumeration labels" placeholder="Enum labels, comma separated" ${type === 'enum' ? '' : 'hidden'}>`; row.querySelector('select').addEventListener('change', () => { row.querySelector('.enum').hidden = row.querySelector('select').value !== 'enum'; }); row.querySelector('button').onclick = () => row.remove(); $('fields').append(row); }
function sourceDialog() { if (project?.read_only)
    throw Error('Imported evidence is read-only. Export its project configuration to create a new capture project.'); if (!project) {
    newProject();
    return;
} dialog('Define a source', `<p>A source is something that produces observations: a device, fixture, process, or service. Its event schema describes one observation.</p><label for="sourceName">Source name</label><input id="sourceName" placeholder="e.g. thermocouple-reader" required maxlength="160"><label for="eventName">Event name</label><input id="eventName" value="measurement" required maxlength="160"><div class="fieldlabels">Fields · type · unit</div><div id="fields"></div><button type="button" id="moreField" class="textbutton">+ Add field</button><details ${advanced ? 'open' : ''}><summary>Timing and source identity</summary><label for="clockDomain">Clock domain</label><input id="clockDomain" value="producer-clock" required maxlength="160"><p>Use a different domain for independent clocks. Nanosecond precision does not imply synchronization.</p><label for="staleMS">Mark disconnected after no data (ms)</label><input id="staleMS" type="number" min="10" max="86400000" value="3000"></details>`, 'Save source', async () => { const fields = [...$('fields').querySelectorAll('.fieldrow')].map(r => ({ name: r.querySelector('.fname').value, type: r.querySelector('.ftype').value, unit: r.querySelector('.funit').value, enum: r.querySelector('.ftype').value === 'enum' ? r.querySelector('.enum').value.split(',').map(v => v.trim()).filter(Boolean) : undefined })); if (!fields.length)
    throw Error('Add at least one field.'); const p = await api('/projects/' + project.id + '/sources', 'POST', { name: $('sourceName').value, clock_domain: $('clockDomain').value, stale_ms: Number($('staleMS').value), synthetic: false, events: [{ name: $('eventName').value, version: 1, fields }] }); project = p; projects = projects.map(old => old.id === p.id ? p : old); const src = p.sources.at(-1); bindings().filter(b => b.src.id === src.id).slice(0, 4).forEach(b => selected.add(b.key)); remember(); sourceSignature = ''; render(); connection(src); }); fieldRow(); on('moreField', () => { if ($('fields').children.length >= 64)
    throw Error('Maximum 64 fields per schema'); fieldRow(); }); }
function sourceTree() { if (!project) {
    $('sources').innerHTML = '<p class="muted">Your sources and signals will appear here.</p>';
    return;
} const query = $('signalSearch').value.toLowerCase(); const sig = JSON.stringify([project.sources, sourceStates, [...selected], query, !!selectedSession]); if (sig === sourceSignature)
    return; sourceSignature = sig; $('sources').innerHTML = project.sources.map(src => `<div class="source-title">${esc(src.name)}</div><div class="source-meta">${src.synthetic ? 'SYNTHETIC · ' : ''}${selectedSession ? 'RECORDED' : esc((sourceStates[src.id] ?? 'disconnected').toUpperCase())}</div>` + bindings().filter(b => b.src.id === src.id && `${b.src.name} ${b.sc.name} ${b.f.name}`.toLowerCase().includes(query)).map(b => `<label class="signal"><input type="checkbox" data-binding="${esc(b.key)}" ${selected.has(b.key) ? 'checked' : ''}><span>${esc(b.sc.name)} v${b.sc.version}<br><b>${esc(b.f.name)}</b></span><small>${esc(b.f.unit || b.f.type)}</small></label>`).join('')).join('') || '<p class="source-empty">No sources yet. Use + to define your first one.</p>'; $('sources').querySelectorAll('input').forEach(el => el.onchange = () => { if (el.checked && selected.size >= 8) {
    el.checked = false;
    notify('The signal browser shows up to eight signals at once.');
    return;
} el.checked ? selected.add(el.dataset.binding) : selected.delete(el.dataset.binding); remember(); sourceSignature = ''; renderData(); }); }
function sampleValue(f) { switch (f.type) {
    case 'int64':
    case 'uint64': return '0';
    case 'bool': return true;
    case 'string': return 'ready';
    case 'enum': return f.enum?.[0] ?? '';
    default: return 0;
} }
function connection(src, revision) {
    if (project?.read_only) {
        dialog('Imported source definitions', `<p>This project preserves imported evidence. Export its project configuration to start a separate capture project.</p><pre>${esc(JSON.stringify(project.sources, null, 2))}</pre>`, null);
        return;
    }
    if (!project?.sources.length) {
        sourceDialog();
        return;
    }
    src = src ?? project.sources[0];
    const sc = revision ?? src.events.at(-1);
    const vals = Object.fromEntries(sc.fields.map(f => [f.name, f.name === 'decoder_sha256' && src?.decoder ? src.decoder.sha256 : sampleValue(f)]));
    const py = `from tracework import Client\nimport time\nimport json\n\nclient = Client(${JSON.stringify(location.origin)})\nsource = client.source(${JSON.stringify(project.id)}, ${JSON.stringify(src.id)},\n                       ${JSON.stringify(sc.id)}, ${JSON.stringify(src.clock_domain)},\n                       schema_version=${sc.version}, require_recording=True)\n\n# Start recording, then send your own observations.\n# int64 and uint64 are decimal strings on the wire.\nsource.emit(json.loads(${JSON.stringify(JSON.stringify(vals))}),\n            timestamp_ns=time.time_ns())\n`;
    dialog('Connect your producer', `<p><b>${esc(src.name)} / ${esc(sc.name)} · v${sc.version}</b><br>Start a recording to receive buffered agent observations. After a server restart, start a new recording to drain the buffer.</p><label for="connectionSource">Source</label><select id="connectionSource">${project.sources.map(s => `<option value="${esc(s.id)}" ${s.id === src.id ? 'selected' : ''}>${esc(s.name)}</option>`).join('')}</select><label for="connectionSchema">Event schema revision</label><select id="connectionSchema">${src.events.map((v, i) => `<option value="${i}" ${v === sc ? 'selected' : ''}>${esc(v.name)} · v${v.version}</option>`).join('')}</select><button id="reviseSchema" type="button" class="textbutton">Create a schema revision</button><p><a href="/docs/INTEGRATIONS.md" target="_blank">CAN/DBC, read-only Modbus, native Go/Rust SDKs and local extensions</a></p>${src.decoder ? `<p>Decoder: ${esc(src.decoder.kind)} · SHA-256 ${esc(src.decoder.sha256)}. Use the matching native adapter configuration to retain raw evidence.</p>` : ''}<details open><summary>Go agent · Serial, MQTT, or stdin</summary><label for="connectorKind">Connector</label><select id="connectorKind"><option value="serial">Serial JSONL · 8N1</option><option value="mqtt">MQTT · QoS 1 subscription</option><option value="stdin">stdin JSONL</option></select><div id="serialSettings"><label for="serialDevice">Serial device path</label><input id="serialDevice" placeholder="/dev/ttyUSB0 or /dev/cu.usbserial…"><label for="serialBaud">Baud rate</label><input id="serialBaud" type="number" value="115200" min="1"></div><div id="mqttSettings" hidden><label for="mqttBroker">Broker URL</label><input id="mqttBroker" value="tcp://127.0.0.1:1883"><label for="mqttTopic">Topic filter</label><input id="mqttTopic" placeholder="lab/sensor/observations"><p>Credentials use TRACEWORK_MQTT_USERNAME and TRACEWORK_MQTT_PASSWORD. Use ssl:// for TLS. Retained messages are ignored by default.</p></div><label for="agentInput">Input format</label><select id="agentInput"><option value="values">Field values · agent timestamp and identity</option><option value="envelope">Native envelope · producer timestamp and identity</option></select><p>Use native envelopes for MQTT redelivery deduplication. Value-only MQTT messages may repeat after a disconnect. Serial device buffers can overflow under backpressure.</p><button id="downloadAgent" type="button">Download agent configuration</button><p>Run in the package folder: <code>TRACEWORK_URL=${esc(location.origin)} ./start.sh agent --config tracework-agent.json</code>. For stdin, append <code>&lt; my-values.jsonl</code>. Set TRACEWORK_TOKEN if your server requires it.</p><details><summary>Expected values object</summary><pre>${esc(JSON.stringify(vals, null, 2))}</pre></details></details><details><summary>Python · no pip install</summary><p>Save as my_source.py. Run <code>PYTHONPATH=sdk/python python3 my_source.py</code>.</p><textarea id="connectCode" readonly rows="12" aria-label="Python ingestion example">${esc(py)}</textarea><button id="copyCode" type="button">Copy Python example</button></details><p><a href="/docs/CONNECTORS.md" target="_blank">Buffering, remote agents, gRPC, and reconnect guide</a></p>`, null);
    $('connectionSource').onchange = () => connection(project.sources.find(s => s.id === $('connectionSource').value));
    $('connectionSchema').onchange = () => connection(src, src.events[Number($('connectionSchema').value)]);
    $('connectorKind').onchange = () => { const k = $('connectorKind').value; show('serialSettings', k === 'serial'); show('mqttSettings', k === 'mqtt'); };
    on('reviseSchema', () => revisionDialog(src, sc));
    on('downloadAgent', () => { const kind = $('connectorKind').value; const config = { project_id: project.id, source_id: src.id, schema_id: sc.id, schema_version: sc.version, clock_domain: src.clock_domain, name: src.name + ' agent', connector: kind, input: $('agentInput').value, spool: './spool-' + src.id + '-v' + sc.version, max_spool_mb: 64, recording: 'required', ...(kind === 'serial' ? { serial_device: $('serialDevice').value, serial_baud: Number($('serialBaud').value) } : {}), ...(kind === 'mqtt' ? { mqtt_broker: $('mqttBroker').value, mqtt_topic: $('mqttTopic').value, mqtt_retained: false } : {}) }; if (kind === 'serial' && !config.serial_device)
        throw Error('Enter the serial device path.'); if (kind === 'mqtt' && !config.mqtt_topic)
        throw Error('Enter a topic filter.'); download(new Blob([JSON.stringify(config, null, 2)], { type: 'application/json' }), 'tracework-agent.json'); notify('Agent configuration downloaded. Run it on the acquisition computer.'); });
    on('copyCode', async () => { try {
        await navigator.clipboard.writeText(py);
        notify('Python example copied.');
    }
    catch {
        $('connectCode').select();
        notify('Select and copy the example.');
    } });
}
function revisionDialog(src, chosen) {
    const sc = src.events.filter(v => v.id === chosen.id).at(-1);
    dialog('Create a schema revision', `<p>Revision ${sc.version} stays available for existing producers and recordings. Changed names, types, units, or enum labels receive a new field identity. Configure your producer to send the new version explicitly.</p><label for="eventName">Event name</label><input id="eventName" value="${esc(sc.name)}" required maxlength="160"><div id="fields"></div><button type="button" id="moreField">+ Add field</button>`, 'Save revision', async () => {
        const fields = [...$('fields').querySelectorAll('.fieldrow')].map(r => { const type = r.querySelector('.ftype').value; const f = { name: r.querySelector('.fname').value, type, unit: r.querySelector('.funit').value, ...(type === 'enum' ? { enum: r.querySelector('.enum').value.split(',').map(v => v.trim()).filter(Boolean) } : {}) }; const old = sc.fields.find(v => v.name === f.name && v.type === f.type && v.unit === f.unit && JSON.stringify(v.enum ?? []) === JSON.stringify(f.enum ?? [])); if (old)
            f.id = old.id; return f; });
        const p = await api('/projects/' + project.id + '/sources/' + src.id + '/schemas/' + sc.id + '/revisions', 'POST', { expected_version: sc.version, name: $('eventName').value, fields });
        project = p;
        projects = projects.map(v => v.id === p.id ? p : v);
        sourceSignature = '';
        render();
        notify('Schema revision saved. Existing data retains its original schema.');
        connection(p.sources.find(v => v.id === src.id));
    });
    sc.fields.forEach(f => { fieldRow(f.name, f.type, f.unit); $('fields').lastElementChild.querySelector('.enum').value = (f.enum ?? []).join(','); });
    on('moreField', () => fieldRow());
}
function stopStream() { text('streamState', 'Live stream idle'); clearTimeout(streamRetry); const old = socket; socket = null; old?.close(); liveEvents = []; agentHealth = []; }
function startStream() {
    if (!project || selectedSession || socket)
        return;
    const pid = project.id;
    const ws = new WebSocket((location.protocol === 'https:' ? 'wss://' : 'ws://') + location.host + '/api/v1/projects/' + pid + '/stream');
    socket = ws;
    ws.onopen = () => ws.send(JSON.stringify({ token }));
    ws.onmessage = event => { if (socket !== ws || project?.id !== pid || selectedSession)
        return; try {
        const f = JSON.parse(event.data);
        if (f.snapshot || f.lost_before)
            liveEvents = f.events;
        else
            liveEvents = liveEvents.concat(f.events).slice(-2000);
        liveEvents = liveEvents.filter(e => e.ordinal > (f.evicted_through ?? 0));
        sourceStates = f.source_states;
        agentHealth = f.agents;
        if (f.lost_before)
            notify('Live display fell behind its buffer. Open the recording for complete accepted observations.');
        if (follow) {
            events = liveEvents;
            cursor = Math.max(0, events.length - 1);
        }
        text('streamState', 'Live stream connected');
        render();
    }
    catch (e) {
        reportError(e);
        ws.close();
    } };
    ws.onclose = () => { if (socket !== ws)
        return; socket = null; text('streamState', 'Live stream reconnecting…'); streamRetry = window.setTimeout(startStream, 1000); };
    ws.onerror = () => ws.close();
}
function renderAgents() { text('agentCount', agentHealth.length); $('agentHealth').innerHTML = agentHealth.map(a => `<div class="agent-card"><b>${esc(a.name)}</b><span class="${a.online ? '' : 'muted'}">${a.online ? esc(a.state.replaceAll('_', ' ')) : 'offline'} · ${esc(a.connector)}</span><small>${a.queued} buffered · ${(a.spool_bytes / 1024).toFixed(1)} KiB</small><small>${esc(a.detail)}</small></div>`).join('') || '<p class="muted">Agents appear here after connecting. Direct Python and HTTP producers appear as sources.</p>'; }
async function refreshProject() { if (!project)
    return; const gen = generation, pid = project.id; const ss = await api('/projects/' + pid + '/sessions'); if (gen !== generation)
    return; sessions = ss; if (selectedSession) {
    const cur = ss.find(s => s.id === selectedSession.id);
    if (cur)
        selectedSession = cur;
}
else {
    startStream();
    if (follow) {
        events = liveEvents;
        cursor = Math.max(0, events.length - 1);
    }
} render(); }
async function openSession(sid, keepRange = false) { extensions.reset(); checker.reset(); operator.reset(); investigator.reset(); pauseReplay(); stopStream(); generation++; const gen = generation; if (!keepRange)
    selection = {}; const [s, data, stats] = await Promise.all([api('/sessions/' + sid), api('/sessions/' + sid + '/events?' + rangeQuery()), api('/sessions/' + sid + '/range?' + rangeQuery())]); if (gen !== generation)
    return; selectedSession = s; events = data.events; more = data.more; next = data.next; rangeCount = stats.count; cursor = 0; follow = false; page = 1; sourceSignature = ''; render(); notify('Recording opened. Inspect, select a range, or play its receive-time sequence.'); }
async function nextPage() { pauseReplay(); if (!selectedSession)
    return; const gen = generation; const data = await api('/sessions/' + selectedSession.id + '/events?' + rangeQuery({ after: String(next) })); if (gen !== generation)
    return; events = data.events; more = data.more; next = data.next; cursor = 0; page++; render(); }
function recordsList() { const signature = JSON.stringify([sessions.map(s => [s.id, s.name, s.status, s.count]), selectedSession?.id]); if (signature === recordSignature)
    return; recordSignature = signature; text('sessionCount', sessions.length); $('recordings').innerHTML = sessions.map(s => `<button class="recording-item ${selectedSession?.id === s.id ? 'selected' : ''}" data-session="${esc(s.id)}"><strong>${esc(s.name)}</strong><small>${esc(s.status.toUpperCase())} · ${s.count.toLocaleString()} events</small></button>`).join(''); $('recordings').querySelectorAll('button').forEach(el => el.onclick = () => openSession(el.dataset.session).catch(reportError)); }
function render() { sourceTree(); recordsList(); renderAgents(); if (!project)
    return; text('projectTitle', selectedSession?.name ?? project.name); text('projectSubtitle', selectedSession ? project.name : project.description || 'Define a source. Observe its signals. Keep the evidence.'); text('viewMode', selectedSession ? 'RECORDED / ' + selectedSession.status.toUpperCase() : (project.sample ? 'SYNTHETIC PROJECT / LIVE WORKBENCH' : 'LIVE WORKBENCH')); const active = sessions.find(s => s.status === 'recording'); const btn = $('record'); btn.disabled = !!project.read_only || !active && (!!selectedSession || !project.sources.length); text('connection', project.read_only ? 'Source definitions' : 'Connect source'); show('replayControls', !!selectedSession); text('rangeLabel', Object.keys(selection).length ? `Selected range · ${rangeCount.toLocaleString()} events` : 'Whole recording'); btn.classList.toggle('active', !!active); btn.classList.toggle('primary', !project.read_only); btn.textContent = project.read_only ? 'Read-only' : active ? '■ Stop recording' : '● Record'; document.querySelector('.agentsbox').hidden = !!selectedSession; let msg = active ? `Recording “${active.name}” on the server. You can close this browser; acquisition and capture continue while your producer runs.` : ''; if (project.read_only)
    msg = 'Imported evidence · read-only. Original event identities, timestamps, and values are preserved.';
else if (selectedSession?.status === 'interrupted')
    msg = 'This capture was interrupted by a server restart. A gap may follow its last persisted event. Start a new recording to continue.';
else if (project.sample)
    msg = (msg ? msg + ' ' : '') + 'Synthetic teaching data. No physical equipment is connected.'; text('recordBanner', msg); show('recordBanner', !!msg); renderData(); }
function renderData() { workbench.render(); const at = events[cursor]; renderInspector(at); const range = $('cursor'); range.max = String(Math.max(0, events.length - 1)); range.value = String(cursor); range.disabled = !events.length; text('cursorLabel', at ? `Event ${cursor + 1} / ${events.length} · sequence ${at.sequence}` : 'Waiting for data'); text('eventCount', selectedSession ? `${rangeCount.toLocaleString()} in range / ${selectedSession.count.toLocaleString()} recorded · page ${page}` : `${events.length.toLocaleString()} events in live buffer`); text('follow', selectedSession ? 'Go to last event' : follow ? 'Pause view' : 'Follow live'); show('nextPage', !!selectedSession && more); show('firstPage', !!selectedSession && page > 1); text('pageStatus', selectedSession && more ? 'More recorded data available' : ''); }
function renderInspector(e) { if (!e) {
    $('eventInfo').innerHTML = '<p class="muted">No event selected.</p>';
    text('rawEvent', '');
    return;
} const src = project?.sources.find(s => s.id === e.source_id) ?? selectedSession?.project.sources.find(s => s.id === e.source_id); const schema = src?.events.find(s => s.id === e.schema_id && s.version === e.schema_version); $('eventInfo').innerHTML = `${Object.entries(e.values).map(([k, v]) => `<div class="payload"><span>${esc(k)}</span><b>${esc(v === null ? 'null' : String(v))}<small> ${esc(schema?.fields.find(f => f.name === k)?.unit ?? '')}</small></b></div>`).join('')}<dl><dt>Source / event</dt><dd>${esc(src?.name ?? e.source_id)} / ${esc(schema?.name ?? e.schema_id)} · v${e.schema_version}</dd><dt>Source timestamp · ns</dt><dd>${esc(e.timestamp_ns)}</dd><dt>Received timestamp · ns</dt><dd>${esc(e.received_ns)}</dd><dt>Clock domain</dt><dd>${esc(e.clock_domain)}</dd><dt>Synchronization</dt><dd>${esc(e.sync_quality)}<br>Uncertainty: ${esc(e.uncertainty_ns ?? 'unknown')}${e.uncertainty_ns ? ' ns' : ''}</dd><dt>Sequence / boot</dt><dd>${esc(e.sequence)}<br>${esc(e.boot_id)}</dd><dt>Event ID</dt><dd>${esc(e.id)}</dd></dl>${(e.flags ?? []).map(f => `<span class="flag">${esc(f.replaceAll('_', ' '))}</span>`).join('')}${e.gap_before ? '<span class="flag">Gap before event</span>' : ''}`; text('rawEvent', JSON.stringify(e, null, 2)); }
function seek(i) { pauseReplay(); follow = false; cursor = Math.max(0, Math.min(events.length - 1, i)); renderData(); }
function setTab(value) { workbench.selectTab(value); }
function record() { const active = sessions.find(s => s.status === 'recording'); if (active)
    return api('/sessions/' + active.id + '/stop', 'POST', {}).then(() => { notify('Recording sealed. Select it in Recordings to inspect or export.'); return refreshProject(); }); if (!project)
    return; dialog('Start a recording', `<p>The server records every accepted event in this project until you stop it. Keep your producer or agent running.</p><label for="recordName">Recording name</label><input id="recordName" value="${esc('Capture ' + new Date().toLocaleString())}" required maxlength="160">`, 'Start recording', async () => { await api('/projects/' + project.id + '/sessions', 'POST', { name: $('recordName').value }); closeDialog(); await refreshProject(); }); }
function examples() { dialog('Explore with synthetic data', `<p>Each example creates its own project and a clearly labeled recording. Your existing projects remain available.</p><div class="examples-grid"><button type="button" data-example="motor"><b>Simulated motor fault</b><span>Watch a speed drop, inspect a discontinuity, and read the fault log.</span></button><button type="button" data-example="serial"><b>Serial sensor fixture</b><span>Explore a temperature schema ready for a JSONL producer.</span></button><button type="button" data-example="service"><b>Software service fixture</b><span>Inspect a latency spike and its structured observations.</span></button></div>`, null); $('dialogBody').querySelectorAll('[data-example]').forEach(el => el.onclick = async () => { el.disabled = true; try {
    const r = await api('/examples/' + el.dataset.example, 'POST', {});
    closeDialog();
    projects.push(r.project);
    await selectProject(r.project.id);
    await openSession(r.session_id);
}
catch (e) {
    text('dialogError', e.message);
    el.disabled = false;
} }); }
function download(blob, name) { const url = URL.createObjectURL(blob); const a = document.createElement('a'); a.href = url; a.download = name; a.click(); setTimeout(() => URL.revokeObjectURL(url), 1000); }
async function exportRecording(format) { if (!selectedSession)
    throw Error('Open a recording first.'); const r = await fetch('/api/v1/sessions/' + selectedSession.id + '/export/' + format + '?' + rangeQuery(), { headers: token ? { Authorization: 'Bearer ' + token } : {} }); if (!r.ok)
    throw Error((await r.json()).error); download(await r.blob(), 'TRACEWORK-' + selectedSession.id + '.' + format); notify('Recording exported.'); closeDialog(); }
function exportDialog() { if (!project) {
    notify('Create or open a project first.');
    return;
} dialog('Export your work', `<div class="exportchoices"><button type="button" id="exportConfig">Project configuration · JSON</button>${selectedSession ? `<button type="button" id="exportBundle">${Object.keys(selection).length ? 'Selected range' : 'Complete recording'} · trace ZIP with manifest</button><button type="button" id="exportJSONL">${Object.keys(selection).length ? 'Selected range' : 'Complete recording'} · JSONL</button><button type="button" id="exportCSV">${Object.keys(selection).length ? 'Selected range' : 'Complete recording'} · CSV</button>` : '<p>Open a recording to export its observations.</p>'}</div><p>Configuration includes schemas and source identities. Trace bundles include original event values, timestamps, provenance, and integrity hashes. This is TRACEWORK native data, not a Zelos .trz file.</p>`, null); on('exportConfig', () => { download(new Blob([JSON.stringify(project, null, 2)], { type: 'application/json' }), 'TRACEWORK-project.json'); closeDialog(); }); if (selectedSession) {
    on('exportBundle', () => exportRecording('zip'));
    on('exportJSONL', () => exportRecording('jsonl'));
    on('exportCSV', () => exportRecording('csv'));
} }
function evidenceSummary() { if (!project) {
    notify('Open a project first.');
    return;
} const e = events[cursor]; const win = window.open('', '_blank'); if (!win) {
    reportError(Error('Allow the print window to open.'));
    return;
} win.document.write(`<!doctype html><html><head><title>TRACEWORK evidence summary</title><style>body{font:14px/1.6 system-ui;max-width:800px;margin:40px auto;color:#17251b}h1{font-size:28px}h2{font-size:17px;margin-top:28px}pre{font:11px/1.5 monospace;white-space:pre-wrap;overflow-wrap:anywhere;border:1px solid #ccc;padding:16px}small{color:#526052}@media print{button{display:none}body{margin:15mm}}</style></head><body><small>GREEN SHOE GARAGE · TRACEWORK v1.0.0</small><h1>${esc(project.name)}</h1><p>${esc(project.description)}</p><h2>Recording / evidence scope</h2><p>${esc(selectedSession ? `${selectedSession.name} · ${selectedSession.status} · ${selectedSession.count} total recorded events` : 'Live buffer only; this is not a complete recording report.')}</p><p>Session ID: ${esc(selectedSession?.id ?? 'none')}<br>Generated: ${esc(new Date().toISOString())}<br>Synthetic project: ${project.sample ? 'Yes' : 'No'}</p><p>Selected range: ${esc(Object.keys(selection).length ? JSON.stringify(selection) : 'Whole recording')} · ${selectedSession ? rangeCount : 'live'} events in view scope.</p>${selectedSession?.origin ? `<h2>Import provenance</h2><pre>${esc(JSON.stringify(selectedSession.origin, null, 2))}</pre>` : ''}<h2>Selected observation</h2><pre>${esc(e ? JSON.stringify(e, null, 2) : 'No selected event.')}</pre><h2>Sources and schemas</h2><pre>${esc(JSON.stringify((selectedSession?.project ?? project).sources, null, 2))}</pre><h2>Interpretation limits</h2><p>Values are observations. No diagnostic conclusion is inferred. Source timestamps retain their original precision; clock accuracy is governed by synchronization quality and uncertainty. Receive order cannot establish causality between unsynchronized sources. Review gap flags and recording status. This summary includes only the selected event; the exported trace bundle contains all events in its explicitly selected range.</p><button onclick="window.print()">Print / Save as PDF</button></body></html>`); win.document.close(); }
function help() { dialog('TRACEWORK v0.9 · Bench guide', `<p><b>1 · Define.</b> Create a blank project and add a source with your own field names, types, and units.</p><p><b>2 · Connect.</b> Run the generated Python snippet or pipe JSONL values into the Go agent. Select signals in the browser.</p><p><b>3 · Record.</b> Press Record before sending the observations you want to keep. The server owns capture. Closing this browser is safe.</p><p><b>4 · Investigate.</b> Stop, select a recording, and move the shared cursor. The inspector shows exact timestamps and values. Export the full recording for independent use.</p><p><b>Keyboard.</b> Ctrl/Cmd K opens commands. ← / → step through events; Space plays/pauses a recording or pauses/follows live data. Use Tab to reach every control.</p><p>v0.4 adds configurable plot, table, raw, state, log and value panels. Add panel chooses signals and appearance; Workspace manages saved tabs and portable layout files. Plot zoom/pan applies to the loaded page and does not change the export range. Resize a focused panel edge with ↑ / ↓; use panel arrows to reorder. Native trace import, replay and protected recordings remain available. Use Storage to preview retention; no automatic deletion runs. Investigate compares recordings, retains exact evidence and exports reports. Actions discovers connected providers, takes exclusive control and requires an explicit review and arming step. Checks evaluates saved rules with explicit coverage policies and retains exact evidence with JUnit/HTML reports. Access manages optional local accounts, scoped evidence links, audit history and full backups. Roles apply to this installation; viewer display changes remain temporary.</p><p><a href="/docs/QUICKSTART.md" target="_blank">Local quick start</a> · <a href="/docs/DATA-CONTRACT.md" target="_blank">Data contract</a> · <a href="/docs/CAPABILITY-MATRIX.md" target="_blank">Capability matrix</a></p>`, null); }
function palette() { dialog('Commands', `<div class="exportchoices"><button type="button" id="cmdNew">New project</button><button type="button" id="cmdSource">Define source</button><button type="button" id="cmdLive">Live workbench</button><button type="button" id="cmdExport">Export</button><button type="button" id="cmdPanel">Add panel</button><button type="button" id="cmdWorkspace">Workspace tabs</button><button type="button" id="cmdHelp">Help</button></div>`, null); on('cmdNew', newProject); on('cmdSource', sourceDialog); on('cmdLive', async () => { closeDialog(); await goLive(); }); on('cmdExport', exportDialog); on('cmdPanel', () => workbench.panelDialog()); on('cmdWorkspace', () => workbench.layoutDialog()); on('cmdHelp', help); }
async function goLive() { extensions.reset(); checker.reset(); operator.reset(); investigator.reset(); pauseReplay(); selection = {}; stopStream(); generation++; selectedSession = null; follow = true; page = 1; events = []; sourceSignature = ''; await refreshProject(); }
async function poll() { if (busy)
    return; busy = true; try {
    if (Date.now() - lastStatus > 2000) {
        const status = await api('/status');
        if (!online)
            clearError();
        online = true;
        text('serverState', '● Server connected');
        $('serverState').className = 'pill ok';
        text('counterInfo', `Received ${status.received} · Persisted ${status.persisted} · Duplicates ${status.duplicates} · Rejected ${status.rejected} · Dropped ${status.dropped} | Trace storage ${(status.trace_bytes / 1048576).toFixed(2)} / ${(status.quota_bytes / 1048576).toFixed(0)} MiB | Disk free ${(status.disk_free_bytes / 1073741824).toFixed(1)} GiB`);
        lastStatus = Date.now();
        const freshProjects = await api('/projects');
        if (JSON.stringify(freshProjects) !== JSON.stringify(projects)) {
            projects = freshProjects;
            if (project) {
                const fresh = projects.find(p => p.id === project.id);
                if (fresh) {
                    project = fresh;
                    if (!selected.size)
                        bindings().slice(0, 4).forEach(b => selected.add(b.key));
                    sourceSignature = '';
                }
                else {
                    await selectProject('');
                }
            }
            projectOptions();
        }
        if (status.recovery.length && !setting('recovery.' + status.recovery.join(''), '')) {
            notify(status.recovery.join(' '));
            saveSetting('recovery.' + status.recovery.join(''), 'seen');
        }
    }
    if (project)
        await refreshProject();
}
catch (e) {
    online = false;
    text('serverState', '! Server disconnected');
    $('serverState').className = 'pill bad';
    if (project)
        renderData();
    if (!$('dialog').open)
        reportError(e);
}
finally {
    busy = false;
} }
async function initialize() { await accounts.refresh(); projects = await api('/projects'); online = true; text('serverState', '● Server connected'); $('serverState').className = 'pill ok'; projectOptions(); const previous = setting('project', ''); await selectProject(projects.some(p => p.id === previous) ? previous : ''); clearError(); text('saveState', '● Project saved on server'); }
let selection = {}, rangeCount = 0, playing = false, replayEpoch = 0, replayTimer = 0;
function rangeQuery(extra = {}) { return new URLSearchParams({ ...selection, ...extra }).toString(); }
function pauseReplay() { playing = false; replayEpoch++; clearTimeout(replayTimer); text('playReplay', '▶ Play'); text('replayStatus', 'Receive-time replay'); }
async function toggleReplay() {
    if (playing) {
        pauseReplay();
        return;
    }
    if (!selectedSession || !events.length)
        return;
    if (cursor === events.length - 1 && !more) {
        await openSession(selectedSession.id, true);
        if (!events.length)
            return;
    }
    playing = true;
    const epoch = ++replayEpoch, gen = generation;
    const speed = Number($('replaySpeed').value);
    text('playReplay', 'Ⅱ Pause');
    let due = performance.now(), previous = events[cursor], pending, pendingPage;
    const tick = async () => {
        try {
            if (!playing || epoch !== replayEpoch || gen !== generation)
                return;
            let advancedEvents = 0;
            while (advancedEvents < 100) {
                if (!pending) {
                    if (cursor + 1 < events.length)
                        pending = events[cursor + 1];
                    else if (more) {
                        pendingPage = await api('/sessions/' + selectedSession.id + '/events?' + rangeQuery({ after: String(next) }));
                        if (epoch !== replayEpoch || gen !== generation)
                            return;
                        pending = pendingPage.events[0];
                    }
                    if (!pending) {
                        renderData();
                        pauseReplay();
                        text('replayStatus', 'End of selected range');
                        return;
                    }
                    due += Math.max(0, Number(BigInt(pending.received_ns) - BigInt(previous.received_ns)) / 1e6) / speed;
                }
                const wait = due - performance.now();
                if (wait > 0) {
                    text('replayStatus', wait > 1000 ? `Waiting ${Math.ceil(wait / 1000)} s · original receive interval` : 'Playing · receive intervals');
                    break;
                }
                if (pendingPage) {
                    events = pendingPage.events;
                    more = pendingPage.more;
                    next = pendingPage.next;
                    page++;
                    cursor = 0;
                    pendingPage = undefined;
                }
                else
                    cursor++;
                previous = pending;
                pending = undefined;
                advancedEvents++;
            }
            if (advancedEvents)
                renderData();
            replayTimer = window.setTimeout(tick, 16);
        }
        catch (e) {
            pauseReplay();
            reportError(e);
        }
    };
    await tick();
}
async function rangeDialog() {
    if (!selectedSession)
        return;
    pauseReplay();
    const sid = selectedSession.id, stats = await api('/sessions/' + sid + '/range');
    dialog('Select a recording range', `<p>Inclusive bounds in original acceptance order. Blank fields include the full extent. ${stats.count.toLocaleString()} events, ordinals ${stats.first_ordinal}–${stats.last_ordinal}.</p><div class="rangegrid"><div><label for="rangeFrom">First ordinal</label><input id="rangeFrom" type="number" min="1" max="9007199254740991" step="1" value="${esc(selection.from)}"><button type="button" id="cursorStart" class="textbutton">Use cursor as start</button></div><div><label for="rangeTo">Last ordinal</label><input id="rangeTo" type="number" min="1" max="9007199254740991" step="1" value="${esc(selection.to)}"><button type="button" id="cursorEnd" class="textbutton">Use cursor as end</button></div></div><label for="rangeSource">Source</label><select id="rangeSource"><option value="">All sources</option>${selectedSession.project.sources.map(s => `<option value="${esc(s.id)}" ${s.id === selection.source ? 'selected' : ''}>${esc(s.name)}</option>`).join('')}</select><details><summary>Exact receive-time bounds</summary><p>Signed integer nanoseconds, preserved as text. Receive time is not a synchronized source clock.</p><label for="rangeFromNS">From receive time · ns</label><input id="rangeFromNS" value="${esc(selection.from_ns)}" inputmode="numeric"><label for="rangeToNS">To receive time · ns</label><input id="rangeToNS" value="${esc(selection.to_ns)}" inputmode="numeric"></details><p>This selection applies to replay, pagination, and ZIP/JSONL/CSV export.</p><button type="button" id="resetRange">Use whole recording</button>`, 'Apply range', async () => {
        const proposed = {};
        for (const [key, id] of Object.entries({ from: 'rangeFrom', to: 'rangeTo', source: 'rangeSource', from_ns: 'rangeFromNS', to_ns: 'rangeToNS' })) {
            const value = $(id).value.trim();
            if (value)
                proposed[key] = value;
        }
        await api('/sessions/' + sid + '/range?' + new URLSearchParams(proposed));
        selection = proposed;
        closeDialog();
        await openSession(sid, true);
    });
    on('cursorStart', () => { $('rangeFrom').value = String(events[cursor]?.ordinal ?? stats.first_ordinal); });
    on('cursorEnd', () => { $('rangeTo').value = String(events[cursor]?.ordinal ?? stats.last_ordinal); });
    on('resetRange', async () => { selection = {}; closeDialog(); await openSession(sid, true); });
}
async function importTrace() {
    const input = $('traceFile'), file = input.files?.[0];
    if (!file)
        return;
    const button = $('traceImport');
    button.disabled = true;
    try {
        if (file.size > 256 * 1048576)
            throw Error('Trace ZIP exceeds the 256 MiB import limit. Export a smaller range.');
        notify('Validating and importing trace…');
        const response = await fetch('/api/v1/import/trace', { method: 'POST', headers: { 'Content-Type': 'application/zip', ...(token ? { Authorization: 'Bearer ' + token } : {}) }, body: file });
        const result = await response.json();
        if (!response.ok)
            throw Error(result.error ?? response.statusText);
        projects = await api('/projects');
        await selectProject(result.project_id);
        await openSession(result.session_id);
        notify(result.already_imported ? 'This exact archive is already imported. Opened its existing recording.' : 'Trace imported. Original event envelopes preserved; project is read-only.');
    }
    catch (e) {
        reportError(e);
    }
    finally {
        input.value = '';
        button.disabled = false;
    }
}
async function manageRecording() {
    if (!selectedSession)
        return;
    pauseReplay();
    const sid = selectedSession.id;
    const [s, stats] = await Promise.all([api('/sessions/' + sid), api('/sessions/' + sid + '/range')]);
    dialog('Manage recording', `<p><b>${esc(s.name)}</b> · ${esc(s.status)}<br>${s.count.toLocaleString()} events · ${stats.chunks} chunks · ${(stats.recording_bytes / 1048576).toFixed(2)} MiB</p><div class="exportchoices"><button type="button" id="protectRecording">${s.protected ? 'Unprotect' : 'Protect'} recording</button><button type="button" id="optimizeRecording" ${s.status === 'recording' ? 'disabled' : ''}>Optimize storage</button><button type="button" id="trashRecording" ${s.status === 'recording' || s.protected ? 'disabled' : ''}>Move to recoverable trash</button></div><p>Optimization combines small chunks of a stopped recording while preserving every original event. Protection prevents retention, trash, and permanent deletion.</p>${s.origin ? `<details><summary>Import provenance</summary><pre>${esc(JSON.stringify(s.origin, null, 2))}</pre></details>` : ''}<p><a href="/docs/STORAGE-REPLAY.md" target="_blank">Storage, import, and recovery guide</a></p>`, null);
    on('protectRecording', async () => { await api('/sessions/' + sid + '/protect', 'POST', { protected: !s.protected }); await refreshProject(); await manageRecording(); });
    on('optimizeRecording', async () => { const b = $('optimizeRecording'); b.disabled = true; try {
        const result = await api('/sessions/' + sid + '/compact', 'POST', {});
        await manageRecording();
        notify(`${result.chunks_before} → ${result.chunks_after} chunks. Original events preserved.${result.warning ? ' ' + result.warning : ''}`);
    }
    finally {
        b.disabled = false;
    } });
    on('trashRecording', async () => { await api('/sessions/' + sid + '/trash', 'POST', {}); closeDialog(); await goLive(); notify('Recording moved to trash. Restore it from Storage & retention.'); });
}
async function storageDialog() {
    pauseReplay();
    const info = await api('/storage'), pid = project?.id;
    const [policy, trash] = pid && accounts.me.role === 'admin' ? await Promise.all([api('/projects/' + pid + '/retention'), api('/projects/' + pid + '/trash')]) : [null, []];
    dialog('Storage & retention', `<p>${(info.trace_bytes / 1048576).toFixed(2)} / ${(info.quota_bytes / 1048576).toFixed(0)} MiB trace quota · ${info.chunks} chunks<br>${(info.disk_free_bytes / 1073741824).toFixed(1)} GiB free on disk · ${(info.trash_bytes / 1048576).toFixed(2)} MiB in recoverable trash</p><p>No automatic deletion. Trash still uses disk space. Permanent deletion removes event payloads and retains identity tombstones.</p>${pid && policy ? `<h3>${esc(project.name)} · retention policy</h3><div class="rangegrid"><div><label for="keepLast">Keep newest closed recordings</label><input id="keepLast" type="number" min="1" max="100000" value="${policy.keep_last}"></div><div><label for="olderDays">Also require age in days · 0 means any age</label><input id="olderDays" type="number" min="0" max="36500" value="${policy.older_than_days}"></div></div><button type="button" id="previewRetention">Save policy & preview</button><div id="retentionPreview"></div><h3>Recoverable trash</h3>${trash.map(s => `<div class="maintenance-item"><b>${esc(s.name)}</b><p>${s.count} events${s.protected ? ' · protected' : ''}</p><div class="row"><button type="button" data-restore="${esc(s.id)}">Restore</button><button type="button" data-purge="${esc(s.id)}" ${s.protected ? 'disabled' : ''}>Delete permanently…</button></div></div>`).join('') || '<p>Trash is empty for this project.</p>'}` : '<p>Choose a project with administrator access to manage retention and trash.</p>'}<details><summary>Recovery inventory · ${info.quarantined_files.length} quarantined files</summary><p>Quarantined files are excluded from recorded evidence. Stop the server and follow the recovery guide before investigating them.</p><pre>${esc(JSON.stringify(info.quarantined_files, null, 2))}</pre></details>`, null);
    if (!pid || !policy)
        return;
    on('previewRetention', async () => { await api('/projects/' + pid + '/retention', 'PUT', { keep_last: Number($('keepLast').value), older_than_days: Number($('olderDays').value) }); const preview = await api('/projects/' + pid + '/retention/preview'); $('retentionPreview').innerHTML = `<p>${preview.candidates.length} recordings eligible (${(preview.bytes / 1048576).toFixed(2)} MiB). Active and protected recordings are excluded.</p><ul>${preview.candidates.map((s) => `<li>${esc(s.name)} · ${s.count} events</li>`).join('')}</ul>${preview.candidates.length ? '<button type="button" id="applyRetention">Move these recordings to trash</button>' : ''}`; if (preview.candidates.length)
        on('applyRetention', async () => { await api('/projects/' + pid + '/retention/apply', 'POST', { plan_token: preview.plan_token }); await goLive(); await storageDialog(); notify('Reviewed recordings moved to recoverable trash.'); }); });
    $('dialogBody').querySelectorAll('[data-restore]').forEach(b => b.onclick = () => api('/sessions/' + b.dataset.restore + '/restore', 'POST', {}).then(async () => { await refreshProject(); await storageDialog(); }).catch(reportError));
    $('dialogBody').querySelectorAll('[data-purge]').forEach(b => b.onclick = () => { const sid = b.dataset.purge, s = trash.find(s => s.id === sid); dialog('Permanently delete recording?', `<p><b>${esc(s.name)}</b> · ${s.count} event payloads will be removed. This cannot be undone here; export a backup before deleting evidence.</p><label for="purgeName">Type DELETE to confirm</label><input id="purgeName" autocomplete="off" required pattern="DELETE">`, 'Delete permanently', async () => { if ($('purgeName').value !== 'DELETE')
        throw Error('Type DELETE to confirm.'); const result = await api('/sessions/' + sid, 'DELETE', { confirmation: sid }); await storageDialog(); notify(`Recording deleted. ${result.warning || 'Identity tombstones retained.'}`); }); });
}
const extensions = new ExtensionsUI({ api, context: () => ({ project, session: selectedSession, bindings: bindings(), selected, events, cursor }), leaveOthers: () => { checker.reset(); operator.reset(); investigator.reset(); }, error: reportError });
const accounts = new AccountsUI({ api, context: () => ({ project, session: selectedSession }), token: () => token, setToken: value => { token = value; try {
        sessionStorage.setItem('tracework.token', token);
    }
    catch { } }, ready: initialize, dialog, close: closeDialog, error: reportError, notify, download });
const checker = new ChecksUI({ api, context: () => ({ project, session: selectedSession }), leaveOthers: () => { operator.reset(); investigator.reset(); }, dialog, close: closeDialog, error: reportError, notify, download, report: async (path) => { const r = await fetch('/api/v1' + path, { headers: token ? { Authorization: 'Bearer ' + token } : {} }); if (!r.ok)
        throw Error((await r.json()).error); return r.text(); } });
const operator = new ActionsUI({ api, context: () => ({ project }), leaveOthers: () => investigator.reset(), dialog, close: closeDialog, error: reportError, notify });
const investigator = new InvestigationUI({ readOnly: () => accounts.me.role === 'viewer', api, context: () => ({ project, session: selectedSession, event: events[cursor] }), dialog, close: closeDialog, error: reportError, notify, download, report: async (path) => { const r = await fetch('/api/v1' + path, { headers: token ? { Authorization: 'Bearer ' + token } : {} }); if (!r.ok)
        throw Error((await r.json()).error); return r.text(); } });
const workbench = new Workbench({ readOnly: () => accounts.me.role === 'viewer', api, context: () => ({ bindings: bindings(), selected, events, cursor, recorded: !!selectedSession, online, follow, sourceStates, scopeKey: (selectedSession?.id ?? project?.id ?? 'none') + '/' + page }), seek, error: reportError, notify, dialog, close: closeDialog, download });
// Wire every visible control. Project edits are committed through the API; view settings stay local.
$('theme').value = setting('theme', 'dark');
document.documentElement.dataset.theme = $('theme').value;
$('theme').onchange = () => { document.documentElement.dataset.theme = $('theme').value; saveSetting('theme', $('theme').value); renderData(); };
function updateMode() { document.body.classList.toggle('advanced-mode', advanced); text('mode', advanced ? 'Advanced' : 'Easy'); $('mode').setAttribute('aria-pressed', String(advanced)); saveSetting('mode', advanced ? 'advanced' : 'easy'); }
;
updateMode();
on('mode', () => { advanced = !advanced; updateMode(); });
if (setting('rail', matchMedia('(max-width:700px)').matches ? 'closed' : 'open') === 'closed')
    show('rail', false);
$('toggleRail').setAttribute('aria-expanded', String(!$('rail').hidden));
on('toggleRail', () => { show('rail', $('rail').hidden); $('toggleRail').setAttribute('aria-expanded', String(!$('rail').hidden)); saveSetting('rail', $('rail').hidden ? 'closed' : 'open'); });
if (setting('inspector', 'open') === 'closed')
    show('inspector', false);
$('toggleInspector').setAttribute('aria-expanded', String(!$('inspector').hidden));
on('toggleInspector', () => { show('inspector', $('inspector').hidden); $('toggleInspector').setAttribute('aria-expanded', String(!$('inspector').hidden)); saveSetting('inspector', $('inspector').hidden ? 'closed' : 'open'); });
on('newProject', newProject);
on('welcomeNew', newProject);
on('fresh', newProject);
on('welcomeExample', examples);
on('examples', examples);
on('addSource', sourceDialog);
on('connection', () => connection());
on('record', record);
on('liveButton', goLive);
on('help', help);
on('export', exportDialog);
on('print', evidenceSummary);
on('closeDialog', closeDialog);
on('cancelDialog', closeDialog);
on('nextPage', nextPage);
on('firstPage', () => openSession(selectedSession.id, true));
on('stepBack', () => seek(cursor - 1));
on('stepNext', () => seek(cursor + 1));
on('follow', async () => { if (selectedSession) {
    seek(events.length - 1);
    return;
} follow = !follow; if (follow)
    await refreshProject(); renderData(); });
$('cursor').oninput = () => seek(Number($('cursor').value));
$('projectSelect').onchange = () => selectProject($('projectSelect').value).catch(reportError);
$('signalSearch').oninput = sourceTree;
$('dialogForm').onsubmit = async (event) => { event.preventDefault(); const btn = $('submitDialog'); btn.disabled = true; try {
    clearError();
    if (modalSubmit)
        await modalSubmit();
}
catch (e) {
    text('dialogError', e.message);
}
finally {
    btn.disabled = false;
} };
on('clearSamples', () => dialog('Clear synthetic examples?', `<p>This removes sample projects and their recordings. Your own projects remain. Export any sample evidence you want to keep first.</p>`, 'Clear sample data', async () => { await api('/samples', 'DELETE'); closeDialog(); await initialize(); notify('Sample projects cleared.'); }));
on('importProject', () => { $('importFile').click(); });
$('importFile').onchange = async () => { const file = $('importFile').files?.[0]; if (!file)
    return; try {
    if (file.size > 4 * 1024 * 1024)
        throw Error('Project file exceeds 4 MiB.');
    const p = JSON.parse(await file.text());
    delete p.id;
    delete p.created_ns;
    p.sample = false;
    const saved = await api('/projects', 'POST', p);
    projects.push(saved);
    await selectProject(saved.id);
    notify('Configuration imported as a new project. No producer or code was started.');
}
catch (e) {
    reportError(e);
}
finally {
    $('importFile').value = '';
} };
document.addEventListener('keydown', e => { if (e.defaultPrevented)
    return; if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 'k') {
    e.preventDefault();
    palette();
    return;
} if ($('dialog').open || ['INPUT', 'TEXTAREA', 'SELECT', 'BUTTON'].includes(e.target.tagName))
    return; if (e.key === 'ArrowLeft') {
    e.preventDefault();
    seek(cursor - 1);
} if (e.key === 'ArrowRight') {
    e.preventDefault();
    seek(cursor + 1);
} if (e.code === 'Space') {
    e.preventDefault();
    $(selectedSession ? 'playReplay' : 'follow').click();
} });
on('traceImport', () => { $('traceFile').click(); });
$('traceFile').onchange = importTrace;
on('storage', storageDialog);
on('selectRange', rangeDialog);
on('manageRecording', manageRecording);
on('playReplay', toggleReplay);
on('skipReplay', async () => { pauseReplay(); if (cursor + 1 < events.length)
    seek(cursor + 1);
else if (more)
    await nextPage(); });
$('replaySpeed').onchange = () => { const running = playing; pauseReplay(); if (running)
    toggleReplay(); };
window.addEventListener('resize', () => workbench.renderData());
initialize().catch(reportError);
setInterval(poll, 2000);
