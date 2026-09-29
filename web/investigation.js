// SPDX-License-Identifier: GPL-3.0-only
import { escapeHTML as e } from './panel-renderers.js';
import { freshID, clone } from './workspace-model.js';
const $ = (id) => document.getElementById(id);
export class InvestigationUI {
    d;
    pid = '';
    revision = 0;
    doc = {};
    serial = 0;
    dirty = false;
    blocked = false;
    timer = 0;
    saving = null;
    epoch = 0;
    recordings = [];
    result = null;
    measurement = null;
    undo = [];
    constructor(d) {
        this.d = d;
        $('investigate').onclick = () => this.open().catch(d.error);
        $('closeInvestigation').onclick = () => this.reset();
    }
    reset() { if (this.dirty && !this.blocked)
        void this.save(); this.epoch++; $('investigationView').hidden = true; $('workbench').hidden = !this.d.context().project; this.pid = ''; }
    action(id, fn) { $(id).onclick = () => Promise.resolve().then(fn).catch(this.d.error); }
    draft() { try {
        sessionStorage.setItem('tracework.investigation.' + this.pid, JSON.stringify({ revision: this.revision, document: this.doc }));
    }
    catch { } }
    status(message) { if ($('investigationSave'))
        $('investigationSave').textContent = message ?? (this.d.readOnly() ? 'Viewer · saved investigation; comparisons are temporary' : this.blocked ? '! Unsaved — export a recovery copy or reload saved' : this.saving ? '◐ Saving investigation' : this.dirty ? '○ Unsaved' : '● Saved on server'); if (this.blocked && $('investigationUtilities'))
        $('investigationUtilities').open = true; if ($('investigationUndo'))
        $('investigationUndo').disabled = !this.undo.length; }
    async open() {
        const context = this.d.context();
        if (!context.project)
            throw Error('Choose a project first.');
        const epoch = ++this.epoch;
        this.pid = context.project.id;
        const pid = this.pid;
        const state = await this.d.api('/projects/' + pid + '/investigation');
        const recordings = await this.d.api('/recordings');
        if (epoch !== this.epoch)
            return;
        this.revision = state.revision;
        this.doc = state.document;
        this.recordings = recordings.filter((s) => s.status !== 'recording' && !s.trashed);
        this.serial = 0;
        this.dirty = false;
        this.blocked = false;
        this.undo = [];
        this.result = null;
        this.measurement = null;
        this.render();
        $('workbench').hidden = true;
        $('investigationView').hidden = false;
        let raw = '';
        try {
            if (!this.d.readOnly())
                raw = sessionStorage.getItem('tracework.investigation.' + pid) ?? '';
        }
        catch { }
        if (raw) {
            try {
                const draft = JSON.parse(raw);
                if (JSON.stringify(draft.document) !== JSON.stringify(this.doc)) {
                    this.doc = draft.document;
                    this.revision = draft.revision;
                    this.dirty = true;
                    this.blocked = true;
                    this.render();
                    this.status('! Unsaved recovery copy — export it, retry save, or reload saved');
                }
            }
            catch {
                this.d.error(Error('A recovery copy is unreadable. Export browser data before clearing it.'));
            }
        }
    }
    change(fn, history = true) { if (this.blocked)
        throw Error('Resolve the unsaved investigation before making further changes.'); if (history) {
        this.undo.push(clone(this.doc));
        if (this.undo.length > 15)
            this.undo.shift();
    } ; fn(); if (this.d.readOnly()) {
        this.status();
        return;
    } ; this.serial++; this.dirty = true; this.draft(); clearTimeout(this.timer); this.timer = window.setTimeout(() => void this.save(), 450); this.status(); }
    async save() { if (this.d.readOnly())
        return; if (this.saving)
        return this.saving; if (!this.pid || !this.dirty || this.blocked)
        return; const pid = this.pid, epoch = this.epoch; this.saving = (async () => { try {
        while (this.dirty && this.pid === pid && this.epoch === epoch) {
            const serial = this.serial;
            const result = await this.d.api('/projects/' + pid + '/investigation', 'PUT', { expected_revision: this.revision, document: this.doc });
            if (this.pid !== pid || this.epoch !== epoch)
                return;
            this.revision = result.revision;
            this.dirty = serial !== this.serial;
            if (!this.dirty) {
                this.doc = result.document;
                this.renderNotes();
                try {
                    sessionStorage.removeItem('tracework.investigation.' + pid);
                }
                catch { }
            }
            else
                this.draft();
        }
    }
    catch (err) {
        if (this.pid === pid) {
            this.blocked = true;
            this.draft();
            this.d.error(err);
        }
    }
    finally {
        this.saving = null;
        if (this.pid === pid)
            this.status();
    } })(); this.status(); return this.saving; }
    async flush() { if (this.d.readOnly())
        return; clearTimeout(this.timer); await this.save(); if (this.dirty || this.blocked)
        throw Error('Save or resolve the investigation before this operation.'); }
    render() {
        const doc = this.doc;
        $('investigationBody').innerHTML = `<div class="investigation-heading"><div><label for="investigationTitle">Investigation title</label><input id="investigationTitle" ${this.d.readOnly() ? 'readonly' : ''} value="${e(doc.title)}" maxlength="160"><label for="investigationQuestion">Question / purpose</label><textarea id="investigationQuestion" ${this.d.readOnly() ? 'readonly' : ''} rows="2" maxlength="4096">${e(doc.question)}</textarea></div><div class="investigation-actions"><span id="investigationSave" role="status"></span><button id="investigationReport">Print / download report</button><details id="investigationUtilities"><summary>Save, recovery & files</summary><button id="investigationRetry">Retry save</button><button id="investigationReload">Reload saved</button><button id="investigationUndo">Undo edit</button><button id="investigationExport">Export investigation JSON</button><button id="investigationImport">Import investigation JSON</button><input type="file" id="investigationFile" accept=".json" hidden></details></div></div>
 <section class="investigation-section"><div class="sectiontitle"><div><h2>Compare recordings</h2><p class="muted">Choose explicit signal aliases. Alignment changes the view; original clocks and identities stay intact.</p></div><button id="addCompareTrace">+ Trace</button></div><div id="compareTraces"></div><div class="row wrap"><label for="comparisonAlignment">Alignment</label><select id="comparisonAlignment"><option value="relative">Relative to first matching sample</option><option value="absolute">Absolute time</option><option value="event">Selected event at zero</option></select><label for="comparisonBasis">Time basis</label><select id="comparisonBasis"><option value="receive">Server receive time</option><option value="source">Original source time</option></select><button id="runComparison" class="primary">Compare selected signals</button></div><p class="muted">Maximum four traces, 10,000 events per selected range and 32 MiB combined. Unsynchronized clocks do not establish causality.</p><div id="comparisonResult"></div></section>
 <section class="investigation-section"><div class="sectiontitle"><h2>Assumptions, evidence & findings</h2><button id="addInvestigationNote" class="primary">+ Note / evidence</button></div><input id="noteFilter" type="search" aria-label="Filter investigation notes" placeholder="Find notes, findings or evidence…"><div id="investigationNotes"></div></section>
 <section class="investigation-section"><div class="sectiontitle"><h2>Baselines</h2><button id="freezeBaseline">Freeze saved investigation</button></div><p class="muted">A baseline is an immutable snapshot. Compare it with your current saved work.</p><div id="baselineList"></div></section>`;
        const input = (id, key) => { $(id).oninput = () => { try {
            this.change(() => this.doc[key] = $(id).value, false);
        }
        catch (err) {
            this.d.error(err);
        } }; };
        input('investigationTitle', 'title');
        input('investigationQuestion', 'question');
        this.action('investigationRetry', async () => { this.blocked = false; await this.save(); });
        this.action('investigationReload', async () => { if (this.dirty) {
            this.d.dialog('Reload saved investigation?', `<p>The unsaved local edits will be replaced. Export a recovery copy first if needed.</p>`, 'Reload saved', async () => { try {
                sessionStorage.removeItem('tracework.investigation.' + this.pid);
            }
            catch { } ; this.d.close(); await this.open(); });
        }
        else
            await this.open(); });
        this.action('investigationUndo', () => { if (!this.undo.length)
            return; const old = this.undo.pop(); this.change(() => this.doc = old, false); this.renderNotes(); $('investigationTitle').value = this.doc.title; $('investigationQuestion').value = this.doc.question; this.status(); });
        this.action('investigationExport', () => this.d.download(new Blob([JSON.stringify(this.doc, null, 2)], { type: 'application/json' }), 'TRACEWORK-investigation.json'));
        this.action('investigationImport', () => $('investigationFile').click());
        $('investigationFile').onchange = () => this.importFile().catch(this.d.error);
        this.action('investigationReport', () => this.report());
        this.action('addCompareTrace', () => this.traceRow());
        this.action('runComparison', () => this.compare());
        this.action('addInvestigationNote', () => this.noteDialog());
        $('noteFilter').oninput = () => this.renderNotes();
        this.action('freezeBaseline', () => this.freeze());
        this.traceRow(this.doc.comparison?.traces?.[0]);
        this.traceRow(this.doc.comparison?.traces?.[1]);
        if (this.doc.comparison) {
            $('comparisonAlignment').value = this.doc.comparison.alignment;
            $('comparisonBasis').value = this.doc.comparison.time_basis;
            for (const t of this.doc.comparison.traces.slice(2))
                this.traceRow(t);
        }
        this.renderNotes();
        void this.baselines().catch(this.d.error);
        this.status();
    }
    traceRow(saved) {
        const host = $('compareTraces');
        if (host.children.length >= 4)
            throw Error('At most four trace aliases.');
        const id = freshID(), n = host.children.length;
        const box = document.createElement('div');
        box.className = 'compare-trace';
        box.dataset.trace = id;
        box.innerHTML = `<label>Alias<input data-alias value="${e(saved?.alias ?? String.fromCharCode(65 + n))}" maxlength="160"></label><label>Recording<select data-session>${this.recordings.map(s => `<option value="${e(s.id)}">${e(s.project.name)} / ${e(s.name)}</option>`).join('')}</select></label><label>Signal<select data-field></select></label><label>Anchor ordinal<input data-anchor type="number" min="1" value="${saved?.anchor_ordinal || 1}"></label><details><summary>Ordinal range</summary><label>From<input data-from type="number" min="1" value="${saved?.range?.from || ''}"></label><label>To<input data-to type="number" min="1" value="${saved?.range?.to || ''}"></label></details><button data-remove aria-label="Remove trace alias">×</button>`;
        host.append(box);
        const select = box.querySelector('[data-session]');
        if (saved)
            select.value = saved.session_id;
        else if (n === 0 && this.d.context().session)
            select.value = this.d.context().session.id;
        else if (n < this.recordings.length)
            select.selectedIndex = n;
        const fields = () => { const session = this.recordings.find(s => s.id === select.value); box.querySelector('[data-field]').innerHTML = (session?.project.sources ?? []).flatMap((src) => src.events.flatMap((sc) => sc.fields.map((f) => `<option value="${e([src.id, sc.id, sc.version, f.id].join('/'))}">${e(src.name)} / ${e(sc.name)} v${sc.version} / ${e(f.name)} · ${e(f.unit || f.type)}</option>`))).join(''); };
        select.onchange = fields;
        fields();
        if (saved)
            box.querySelector('[data-field]').value = [saved.source_id, saved.schema_id, saved.schema_version, saved.field_id].join('/');
        box.querySelector('[data-remove]').onclick = () => box.remove();
    }
    request() { return { alignment: $('comparisonAlignment').value, time_basis: $('comparisonBasis').value, traces: [...$('compareTraces').children].map(box => { const val = (key) => box.querySelector('[data-' + key + ']').value; const [source, schema, version, field] = val('field').split('/'); return { alias: val('alias'), session_id: val('session'), source_id: source, schema_id: schema, schema_version: Number(version), field_id: field, anchor_ordinal: Number(val('anchor')), range: { from: Number(val('from')), to: Number(val('to')) } }; }) }; }
    async compare() { const req = this.request(); $('comparisonResult').textContent = 'Reading selected original observations…'; try {
        const result = await this.d.api('/compare', 'POST', req);
        this.result = result;
        this.change(() => this.doc.comparison = req);
        this.renderComparison();
    }
    catch (err) {
        $('comparisonResult').textContent = 'Comparison could not be completed. Adjust the selection and try again.';
        throw err;
    } }
    renderComparison() {
        if (!this.result)
            return;
        const r = this.result, traces = r.traces;
        $('comparisonResult').innerHTML = `<p class="muted">${e(r.alignment)} alignment · ${e(r.time_basis)} time · ${traces.map(t => `${e(t.request.alias)}: ${t.points.length} samples`).join(' · ')}</p>${this.graph(traces)}<div class="measurement-controls"><h3>Two-observation measurement · B − A</h3><div class="row wrap">${['A', 'B'].map((side, i) => `<label>Trace ${side}<select id="measureTrace${side}">${traces.map((t, j) => `<option value="${j}" ${j === Math.min(i, traces.length - 1) ? 'selected' : ''}>${e(t.request.alias)}</option>`).join('')}</select></label><label>Original ordinal ${side}<input id="measureOrdinal${side}" type="number" min="1" value="${traces[Math.min(i, traces.length - 1)]?.points[0]?.ordinal ?? 1}"></label>`).join('')}<button id="measureCompare">Measure</button></div><div id="measurementResult"></div></div><details><summary>Exact observations and alignment offsets</summary>${traces.map(t => `<h3>${e(t.request.alias)} · ${e(t.field.name)} ${e(t.field.unit)}</h3><p>Recording ${e(t.request.session_id)} · subtract ${e(t.offset_ns)} ns · ${e(t.coverage)}</p><div class="tablewrap"><table><thead><tr><th>Ordinal</th><th>Aligned ns</th><th>Original source ns</th><th>Exact value</th><th>Discontinuity</th></tr></thead><tbody>${t.points.slice(0, 500).map((p) => `<tr><td>${p.ordinal}</td><td>${e(p.aligned_ns)}</td><td>${e(p.timestamp_ns)}</td><td>${e(JSON.stringify(p.value))}</td><td>${p.gap_before ? 'Yes' : 'No'}</td></tr>`).join('')}</tbody></table></div><p class="muted">Table shows first ${Math.min(500, t.points.length)} samples; export comparison JSON for every selected point.</p>`).join('')}</details><button id="exportComparison">Export comparison JSON</button>`;
        this.action('measureCompare', () => this.measure());
        this.action('exportComparison', () => this.d.download(new Blob([JSON.stringify(this.result, null, 2)], { type: 'application/json' }), 'TRACEWORK-comparison.json'));
    }
    graph(traces) { const numeric = traces.filter(t => ['float64', 'int64', 'uint64'].includes(t.field.type) && t.points.some((p) => p.value !== null)); if (!numeric.length)
        return '<p>No numeric signals selected; use the exact observation tables.</p>'; const groups = new Map(); for (const t of numeric) {
        const key = t.field.unit + '/' + (t.field.type === 'float64' ? 'float' : 'integer');
        groups.set(key, [...(groups.get(key) ?? []), t]);
    } const colors = ['var(--trace-a)', 'var(--trace-b)', 'var(--trace-c)', 'var(--trace-d)']; let html = ''; for (const group of groups.values()) {
        const all = group.flatMap(t => t.points), first = all.find(p => p.value !== null), base = group[0].field.type === 'float64' ? null : BigInt(first.value), value = (p) => base === null ? Number(p.value) : Number(BigInt(p.value) - base);
        const times = all.map(p => BigInt(p.aligned_ns));
        let loT = times[0], hiT = loT;
        for (const t of times) {
            if (t < loT)
                loT = t;
            if (t > hiT)
                hiT = t;
        }
        if (hiT === loT)
            hiT++;
        const nums = all.filter(p => p.value !== null).map(value);
        let lo = Math.min(...nums), hi = Math.max(...nums);
        const pad = hi * .05 - lo * .05 || Math.max(1, Math.abs(hi) * .05);
        lo = Math.max(-Number.MAX_VALUE, lo - pad);
        hi = Math.min(Number.MAX_VALUE, hi + pad);
        const scale = Math.max(Math.abs(lo), Math.abs(hi), 1), denom = hi / scale - lo / scale;
        const x = (p) => 60 + Number(BigInt(p.aligned_ns) - loT) / Number(hiT - loT) * 810;
        const y = (p) => 190 - (value(p) / scale - lo / scale) / denom * 165;
        html += `<div class="comparison-plot"><p>${e(group[0].field.unit || 'Unitless')} ${base === null ? '' : '· Δ from ' + e(base.toString())} · ${group.map((t, i) => `<span style="color:${colors[i % 4]}">${e(t.request.alias)} — ${e(t.field.name)}</span>`).join(' · ')}</p><svg viewBox="0 0 920 225" role="img" aria-label="Aligned recording comparison"><path d="M60 20V190H870" stroke="currentColor" fill="none"/>${group.map((t, i) => { let prev = false; let path = ''; for (const p of t.points) {
            if (p.value === null) {
                prev = false;
                continue;
            }
            const yy = y(p);
            if (!Number.isFinite(yy)) {
                prev = false;
                continue;
            }
            path += (prev && !p.gap_before ? 'L' : 'M') + x(p) + ',' + yy + ' ';
            prev = true;
        } return `<path d="${path}" fill="none" stroke="${colors[i % 4]}" stroke-width="2" stroke-dasharray="${i % 2 ? '6 3' : 'none'}"/>`; }).join('')}<text x="5" y="28">${e(hi.toPrecision(4))}</text><text x="5" y="190">${e(lo.toPrecision(4))}</text><text x="60" y="217">${e(loT.toString())} ns</text><text x="630" y="217">${e(hiT.toString())} ns</text></svg></div>`;
    } return html; }
    async measure() { if (!this.result)
        return; const r = this.result; const trace = (side) => r.traces[Number($('measureTrace' + side).value)]; const a = trace('A'), b = trace('B'); this.measurement = await this.d.api('/measure', 'POST', { a: { session_id: a.request.session_id, ordinal: Number($('measureOrdinalA').value) }, b: { session_id: b.request.session_id, ordinal: Number($('measureOrdinalB').value) }, field_a: a.request.field_id, field_b: b.request.field_id, time_basis: r.time_basis }); const m = this.measurement; $('measurementResult').innerHTML = `<p><b>Δt ${e(m.delta_time_ns)} ns</b> · <b>Δvalue ${e(m.delta_value_exact)} ${e(m.unit)}</b></p><p class="muted">${e(m.interpretation_limits)}</p><button id="retainMeasurement">Add measurement & endpoint evidence to notes</button>`; this.action('retainMeasurement', () => { this.change(() => { for (const [label, ref] of [['A', m.a], ['B', m.b]])
        this.doc.notes.push({ id: freshID(), kind: 'evidence', text: 'Measurement endpoint ' + label, severity: 'info', confidence: 'high', ref }); this.doc.notes.push({ id: freshID(), kind: 'annotation', text: `Measured B − A: Δt ${m.delta_time_ns} ns (${m.time_basis}); Δvalue ${m.delta_value_exact} ${m.unit}. A ${m.a.session_id} ordinal ${m.a.ordinal}; B ${m.b.session_id} ordinal ${m.b.ordinal}. Endpoint difference only.`, severity: 'info', confidence: 'high' }); }); this.renderNotes(); this.d.notify('Measurement and both original endpoints added to this investigation.'); }); }
    renderNotes() { const q = $('noteFilter').value.toLowerCase(); $('investigationNotes').innerHTML = (this.doc.notes ?? []).filter((n) => (n.kind + ' ' + n.text).toLowerCase().includes(q)).map((n) => `<article class="investigation-note"><div class="sectiontitle"><b>${e(n.kind)} · ${e(n.severity)} severity · ${e(n.confidence)} confidence</b><div><button data-edit-note="${e(n.id)}">Edit</button><button data-remove-note="${e(n.id)}">Remove</button></div></div><p>${e(n.text)}</p>${n.ref ? `<small>Recording ${e(n.ref.session_id)} · ordinal ${n.ref.ordinal} · ${n.event_sha256 ? 'SHA-256 ' + e(n.event_sha256) : 'Snapshot will be verified on save'}</small>` : ''}</article>`).join('') || '<p class="muted">Add an assumption, cite an observation, or record a finding with severity and confidence.</p>'; $('investigationNotes').querySelectorAll('[data-edit-note]').forEach(b => b.onclick = () => this.noteDialog(b.dataset.editNote)); $('investigationNotes').querySelectorAll('[data-remove-note]').forEach(b => b.onclick = () => { try {
        this.change(() => this.doc.notes = this.doc.notes.filter((n) => n.id !== b.dataset.removeNote));
        this.renderNotes();
    }
    catch (err) {
        this.d.error(err);
    } }); }
    noteDialog(id) { const old = this.doc.notes.find((n) => n.id === id), c = this.d.context(); this.d.dialog(old ? 'Edit investigation note' : 'Add note or cite evidence', `<label for="noteKind">Register</label><select id="noteKind">${['annotation', 'assumption', 'evidence', 'finding'].map(k => `<option ${old?.kind === k ? 'selected' : ''}>${k}</option>`).join('')}</select><label for="noteText">Observation / interpretation</label><textarea id="noteText" required maxlength="8192" rows="5">${e(old?.text)}</textarea><div class="rangegrid"><label>Severity<select id="noteSeverity">${['info', 'low', 'medium', 'high', 'critical'].map(v => `<option ${old?.severity === v ? 'selected' : ''}>${v}</option>`).join('')}</select></label><label>Confidence<select id="noteConfidence">${['low', 'medium', 'high'].map(v => `<option ${(old?.confidence ?? 'medium') === v ? 'selected' : ''}>${v}</option>`).join('')}</select></label></div>${old?.ref ? `<p>Retains recording ${e(old.ref.session_id)} ordinal ${old.ref.ordinal}.</p>` : `<label><input id="noteCite" type="checkbox" ${c.session && c.event ? '' : 'disabled'}> Cite current recorded observation ${c.event?.ordinal ?? ''}</label>`}<p class="muted">Evidence requires a recorded reference. Assumptions and findings remain authored interpretation.</p>`, 'Save note', async () => { const note = { ...(old ?? { id: freshID() }), kind: $('noteKind').value, text: $('noteText').value, severity: $('noteSeverity').value, confidence: $('noteConfidence').value }; if (!old?.ref && $('noteCite').checked)
        note.ref = { session_id: c.session.id, ordinal: c.event.ordinal }; if (!note.text.trim())
        throw Error('Write a note before saving.'); if (note.kind === 'evidence' && !note.ref)
        throw Error('Choose a recorded observation and enable its citation.'); this.change(() => { const i = this.doc.notes.findIndex((n) => n.id === id); if (i >= 0)
        this.doc.notes[i] = note;
    else
        this.doc.notes.push(note); }); this.renderNotes(); this.d.close(); }); }
    async baselines() { const pid = this.pid, rows = await this.d.api('/projects/' + pid + '/baselines'); if (pid !== this.pid)
        return; $('baselineList').innerHTML = rows.map((b) => `<button data-baseline="${e(b.id)}">${e(b.name)} · revision ${b.snapshot.revision}</button>`).join(' ') || '<p class="muted">No baseline frozen yet.</p>'; $('baselineList').querySelectorAll('[data-baseline]').forEach(button => button.onclick = () => { const b = rows.find((x) => x.id === button.dataset.baseline), old = b.snapshot.document; const oldNotes = new Map(old.notes.map((n) => [n.id, n])); const added = this.doc.notes.filter((n) => !oldNotes.has(n.id)); const changed = this.doc.notes.filter((n) => oldNotes.has(n.id) && JSON.stringify(oldNotes.get(n.id)) !== JSON.stringify(n)); const removed = old.notes.filter((n) => !this.doc.notes.some((x) => x.id === n.id)); this.d.dialog('Baseline comparison', `<p><b>${e(b.name)}</b> · baseline revision ${b.snapshot.revision}, current revision ${this.revision}</p><p>Title ${old.title === this.doc.title ? 'unchanged' : 'changed'} · question ${old.question === this.doc.question ? 'unchanged' : 'changed'} · comparison ${JSON.stringify(old.comparison) === JSON.stringify(this.doc.comparison) ? 'unchanged' : 'changed'}</p><p>${added.length} added · ${changed.length} changed · ${removed.length} removed notes</p><pre>${e(JSON.stringify({ added, changed, removed }, null, 2))}</pre>`, null, null); }); }
    async freeze() { await this.flush(); this.d.dialog('Freeze investigation baseline', '<label for="baselineName">Baseline name</label><input id="baselineName" value="Reviewed ' + new Date().toISOString().slice(0, 10) + '" required maxlength="160">', 'Freeze baseline', async () => { await this.d.api('/projects/' + this.pid + '/baselines', 'POST', { name: $('baselineName').value, expected_revision: this.revision }); this.d.close(); await this.baselines(); this.d.notify('Immutable investigation baseline saved.'); }); }
    async importFile() { const input = $('investigationFile'), file = input.files?.[0]; input.value = ''; if (!file)
        return; if (file.size > 2 * 1024 * 1024)
        throw Error('Investigation file exceeds 2 MiB.'); const candidate = JSON.parse(await file.text()); this.d.dialog('Import investigation?', `<p>Replace this project's investigation with ${e(file.name)}? Recorded data stays intact; original evidence references must resolve on this server.</p>`, 'Import investigation', async () => { await this.flush(); const result = await this.d.api('/projects/' + this.pid + '/investigation', 'PUT', { expected_revision: this.revision, document: candidate }); this.undo.push(clone(this.doc)); this.doc = result.document; this.revision = result.revision; this.d.close(); this.render(); }); }
    async report() { await this.flush(); const html = await this.d.report('/projects/' + this.pid + '/investigation/report'); this.d.download(new Blob([html], { type: 'text/html' }), 'TRACEWORK-investigation-report.html'); const win = window.open('', '_blank'); if (win) {
        win.document.write(html);
        win.document.close();
        const button = win.document.createElement('button');
        button.textContent = 'Print / Save as PDF';
        button.onclick = () => win.print();
        win.document.body.prepend(button);
    }
    else
        this.d.notify('Report downloaded. Open it and print to PDF.'); }
}
