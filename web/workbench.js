// SPDX-License-Identifier: GPL-3.0-only
import { defaultWorkspace, newPanel, panelNames, clone, freshID, reference, resolvePanel, extent, boundedWindow } from './workspace-model.js';
import { panelHTML, escapeHTML as esc } from './panel-renderers.js';
const $ = (id) => document.getElementById(id);
const setText = (id, v) => { if ($(id).textContent !== v)
    $(id).textContent = v; };
const draftOwner = freshID(), adoptedDrafts = new Set();
let previousDraftOwner = '';
try {
    previousDraftOwner = sessionStorage.getItem('tracework.draft-owner') ?? '';
    sessionStorage.setItem('tracework.draft-owner', draftOwner);
}
catch { }
const draftKey = (pid, owner = draftOwner) => 'tracework.workspace-draft.' + pid + '.' + owner;
const titleCopy = (name) => { let s = [...name]; while (new TextEncoder().encode(s.join('')).length > 140)
    s.pop(); return s.join('') + ' copy'; };
export class Workbench {
    d;
    state = null;
    epoch = 0;
    signature = '';
    tabSignature = '';
    undoStack = [];
    zooms = new Map();
    scope = '';
    mode = 'inspect';
    dragID = '';
    interacting = false;
    observer;
    resizeFrame = 0;
    constructor(d) {
        this.d = d;
        this.click('addPanel', () => this.panelDialog());
        this.click('layoutMenu', () => this.layoutDialog());
        this.click('undoLayout', () => this.undo());
        this.click('zoomIn', () => this.zoom(.5));
        this.click('zoomOut', () => this.zoom(2));
        this.click('panLeft', () => this.pan(-.25));
        this.click('panRight', () => this.pan(.25));
        this.click('fitPlot', () => this.fit());
        $('plotGesture').onchange = () => { this.mode = $('plotGesture').value; this.renderData(); };
        $('workspaceFile').onchange = () => { this.importFile().catch(this.d.error); };
        this.observer = new ResizeObserver(() => { cancelAnimationFrame(this.resizeFrame); this.resizeFrame = requestAnimationFrame(() => this.renderData()); });
        this.observer.observe($('panelGrid'));
    }
    click(id, fn) { $(id).onclick = () => { Promise.resolve().then(fn).catch(this.d.error); }; }
    action(fn) { return () => { Promise.resolve().then(fn).catch(this.d.error); }; }
    currentTab() { return this.state?.doc.tabs.find(t => t.id === this.state.doc.active_tab); }
    rememberDraft(s) { try {
        localStorage.setItem(draftKey(s.pid), JSON.stringify({ revision: s.revision, workspace: s.doc }));
    }
    catch { /* Server save remains available; failed saves stay visible and exportable. */ } }
    async load(pid) {
        if (this.state?.dirty && !this.state.blocked) {
            clearTimeout(this.state.timer);
            void this.save(this.state);
        }
        const epoch = ++this.epoch;
        this.state = null;
        this.signature = '';
        this.tabSignature = '';
        this.undoStack = [];
        this.zooms.clear();
        $('panelGrid').innerHTML = '<p class="muted">Loading workspace…</p>';
        $('layoutTabs').innerHTML = '';
        this.status();
        if (!pid) {
            $('panelGrid').innerHTML = '';
            return;
        }
        try {
            const saved = await this.d.api('/projects/' + pid + '/workspace');
            if (epoch !== this.epoch)
                return;
            const s = { pid, revision: saved.revision, doc: saved.workspace ?? defaultWorkspace(), serial: 0, dirty: !saved.workspace && !this.d.readOnly(), blocked: false, message: '', saving: null, timer: 0, recovery: null };
            this.state = s;
            try {
                const raw = this.d.readOnly() ? null : localStorage.getItem(draftKey(pid)) || (previousDraftOwner && !adoptedDrafts.has(pid) ? localStorage.getItem(draftKey(pid, previousDraftOwner)) : null);
                if (raw) {
                    localStorage.setItem(draftKey(pid), raw);
                    adoptedDrafts.add(pid);
                    const prior = JSON.parse(raw);
                    if (JSON.stringify(prior.workspace) !== JSON.stringify(s.doc)) {
                        s.recovery = prior.workspace;
                        s.blocked = true;
                        s.message = 'An unsaved browser recovery copy is available. Review it before editing this layout.';
                    }
                    else
                        localStorage.removeItem(draftKey(pid));
                }
            }
            catch {
                s.blocked = true;
                s.message = 'Browser recovery data could not be read. Export or clear the recovery copy from Workspace before editing.';
                s.recovery = { unreadable: true };
            }
            this.render();
            if (s.dirty && !s.blocked)
                void this.save(s);
        }
        catch (e) {
            if (epoch !== this.epoch)
                return;
            $('panelGrid').innerHTML = '<div class="panel-empty">Could not load this workspace. Recorded evidence is unchanged. <button id="retryWorkspace">Retry loading</button></div>';
            this.click('retryWorkspace', () => this.load(pid));
            setText('layoutSaveState', '! Layout unavailable');
            this.d.error(e);
        }
    }
    status() {
        const s = this.state;
        for (const id of ['addPanel', 'layoutMenu', 'undoLayout'])
            $(id).disabled = !s;
        if (!s) {
            setText('layoutSaveState', 'Loading layout');
            $('layoutNotice').hidden = true;
            return;
        }
        setText('layoutSaveState', this.d.readOnly() ? 'Viewer · temporary view, not saved' : s.blocked ? '! Layout unsaved' : s.saving ? '◐ Saving layout' : s.dirty ? '○ Layout unsaved' : '● Layout saved on server');
        $('undoLayout').disabled = !this.undoStack.length || s.blocked;
        $('layoutNotice').hidden = !s.blocked;
        if (s.blocked && $('layoutNotice').dataset.message !== s.message) {
            $('layoutNotice').dataset.message = s.message;
            $('layoutNotice').innerHTML = `<span>${esc(s.message)}</span><button id="reviewLayoutSave">Review local / saved layout</button>`;
            this.click('reviewLayoutSave', () => this.reviewSave());
        }
    }
    change(fn, history = true) { const s = this.state; if (!s)
        return; if (s.blocked) {
        this.reviewSave();
        return;
    } if (history) {
        this.undoStack.push(clone(s.doc));
        if (this.undoStack.length > 15)
            this.undoStack.shift();
    } ; fn(s.doc); s.serial++; if (this.d.readOnly()) {
        this.render();
        return;
    } ; s.dirty = true; this.rememberDraft(s); clearTimeout(s.timer); s.timer = window.setTimeout(() => void this.save(s), 350); this.render(); }
    async save(s) {
        if (this.d.readOnly()) {
            s.dirty = false;
            return;
        }
        ;
        if (s.saving)
            return s.saving;
        if (s.blocked || !s.dirty)
            return;
        s.saving = (async () => { try {
            while (s.dirty && !s.blocked) {
                const serial = s.serial, snapshot = clone(s.doc);
                const response = await this.d.api('/projects/' + s.pid + '/workspace', 'PUT', { expected_revision: s.revision, workspace: snapshot });
                s.revision = response.revision;
                s.dirty = serial !== s.serial;
                if (s.dirty)
                    this.rememberDraft(s);
                else
                    try {
                        localStorage.removeItem(draftKey(s.pid));
                    }
                    catch { }
            }
        }
        catch (e) {
            s.blocked = true;
            s.message = e.message;
            this.rememberDraft(s);
        }
        finally {
            s.saving = null;
            if (this.state === s)
                this.status();
        } })();
        this.status();
        return s.saving;
    }
    async flush() { const s = this.state; if (!s)
        throw Error('Choose a project first.'); clearTimeout(s.timer); await this.save(s); if (s.blocked)
        throw Error('Resolve the unsaved layout before importing another one.'); }
    selectTab(id) { if (!this.state?.doc.tabs.some(t => t.id === id) || this.state.doc.active_tab === id)
        return; this.change(w => w.active_tab = id, false); }
    undo() { if (!this.state || !this.undoStack.length)
        return; const previous = this.undoStack.pop(); this.change(() => { this.state.doc = previous; this.signature = ''; this.tabSignature = ''; }, false); this.d.notify('Previous layout restored. Recorded evidence is unchanged.'); }
    render() {
        const s = this.state, tab = this.currentTab();
        if (!s || !tab) {
            this.status();
            return;
        }
        const tabs = JSON.stringify([s.doc.active_tab, s.doc.tabs.map(t => [t.id, t.name])]);
        if (tabs !== this.tabSignature) {
            this.tabSignature = tabs;
            $('layoutTabs').innerHTML = s.doc.tabs.map(t => `<button id="tab_${t.id}" role="tab" data-tab="${t.id}" aria-controls="panelGrid" tabindex="${t.id === s.doc.active_tab ? '0' : '-1'}" aria-selected="${t.id === s.doc.active_tab}">${esc(t.name)}</button>`).join('');
            $('layoutTabs').querySelectorAll('button').forEach((b, i) => { b.onclick = () => this.selectTab(b.dataset.tab); b.onkeydown = ev => { let target = -1; if (ev.key === 'ArrowRight')
                target = (i + 1) % s.doc.tabs.length; if (ev.key === 'ArrowLeft')
                target = (i + s.doc.tabs.length - 1) % s.doc.tabs.length; if (ev.key === 'Home')
                target = 0; if (ev.key === 'End')
                target = s.doc.tabs.length - 1; if (target >= 0) {
                ev.preventDefault();
                ev.stopPropagation();
                this.selectTab(s.doc.tabs[target].id);
                $('tab_' + s.doc.tabs[target].id).focus();
            } }; });
        }
        const sig = JSON.stringify([tab.id, tab.columns, tab.panels.map(p => [p.id, p.kind, p.title, p.height, p.span])]);
        $('panelGrid').setAttribute('aria-labelledby', 'tab_' + tab.id);
        $('panelGrid').style.setProperty('--columns', String(tab.columns));
        if (sig !== this.signature) {
            this.signature = sig;
            $('panelGrid').innerHTML = tab.panels.length ? tab.panels.map(p => this.panelShell(p, tab.columns)).join('') : '<div class="panel-empty layout-empty"><h2>Build this view around your question.</h2><p>Add a plot, values, table, state timeline, raw events or logs. Panels share the same event cursor.</p><button id="emptyAddPanel" class="primary">Add first panel</button></div>';
            if (!tab.panels.length)
                this.click('emptyAddPanel', () => this.panelDialog());
            for (const p of tab.panels)
                this.wirePanel(p);
        }
        this.renderData();
        this.status();
    }
    panelShell(p, columns) {
        const legacy = p.id === 'plot_main' ? 'plots' : p.id === 'values_main' ? 'readouts' : p.id === 'logs_main' ? 'logRows' : '';
        return `<section class="instrument-panel" data-panel="${p.id}" style="--panel-span:${Math.min(p.span, columns)};height:${p.height}px"><header class="instrument-panel-head"><button class="panel-grip" data-drag="${p.id}" draggable="true" aria-label="Drag ${esc(p.title)} to reorder" title="Drag to reorder; arrow buttons provide a keyboard alternative">⠿</button><h2>${esc(p.title)}</h2><span class="panel-kind">${esc(panelNames[p.kind])}</span><div class="panel-actions"><button data-move="-1" aria-label="Move ${esc(p.title)} earlier">↑</button><button data-move="1" aria-label="Move ${esc(p.title)} later">↓</button><button data-configure aria-label="Configure ${esc(p.title)}">⋯</button></div></header><div class="binding-state"></div>${['raw', 'log', 'table'].includes(p.kind) ? `<div class="panel-filter"><label class="sr" for="filter_${p.id}">Filter ${esc(p.title)}</label><input id="${p.id === 'logs_main' ? 'logSearch' : 'filter_' + p.id}" data-filter type="search" placeholder="Filter ${esc(panelNames[p.kind].toLowerCase())}…" aria-label="Filter ${esc(p.title)}" value="${esc(p.filter)}">${p.kind === 'log' ? `<select ${p.id === 'logs_main' ? 'id="logLevel"' : ''} data-level aria-label="Log level"><option value="">All levels</option>${['debug', 'info', 'warn', 'error'].map(v => `<option ${v === p.level ? 'selected' : ''}>${v}</option>`).join('')}</select>` : ''}${p.kind === 'table' ? `<select data-sort aria-label="Sort signal table">${['signal', 'source', 'value', 'time'].map(v => `<option value="${v}" ${p.sort === v ? 'selected' : ''}>${v}</option>`).join('')}</select><button data-desc aria-label="Reverse table sort">${p.descending ? '↓' : '↑'}</button>` : ''}</div>` : ''}<div class="panel-render ${p.kind === 'value' ? 'readouts' : ''}" ${legacy ? `id="${legacy}"` : ''}></div><div class="panel-resize" role="separator" aria-label="Resize ${esc(p.title)} height" aria-orientation="horizontal" aria-valuemin="180" aria-valuemax="900" aria-valuenow="${p.height}" tabindex="0"><span aria-hidden="true">⋯</span></div></section>`;
    }
    wirePanel(p) {
        const card = document.querySelector(`[data-panel="${p.id}"]`);
        card.querySelector('[data-configure]').onclick = this.action(() => this.panelDialog(p.id));
        card.querySelectorAll('[data-move]').forEach(b => b.onclick = () => this.movePanel(p.id, Number(b.dataset.move)));
        const grip = card.querySelector('[data-drag]');
        grip.ondragstart = ev => { this.dragID = p.id; ev.dataTransfer.setData('text/plain', 'tracework-panel:' + p.id); ev.dataTransfer.effectAllowed = 'move'; };
        grip.ondragend = () => { this.dragID = ''; document.querySelectorAll('.drop-target').forEach(e => e.classList.remove('drop-target')); };
        card.ondragover = ev => { if (this.dragID && this.dragID !== p.id) {
            ev.preventDefault();
            card.classList.add('drop-target');
        } };
        card.ondragleave = () => card.classList.remove('drop-target');
        card.ondrop = ev => { ev.preventDefault(); card.classList.remove('drop-target'); if (this.dragID && this.dragID !== p.id && ev.dataTransfer?.getData('text/plain') === 'tracework-panel:' + this.dragID) {
            const moving = this.dragID;
            this.change(w => { const panels = w.tabs.find(t => t.id === w.active_tab).panels; const old = panels.findIndex(v => v.id === moving), target = panels.findIndex(v => v.id === p.id); if (old >= 0 && target >= 0) {
                const [item] = panels.splice(old, 1);
                panels.splice(target, 0, item);
            } });
        } this.dragID = ''; };
        const filter = card.querySelector('[data-filter]');
        if (filter)
            filter.oninput = () => this.updatePanel(p.id, v => v.filter = filter.value, false);
        const level = card.querySelector('[data-level]');
        if (level)
            level.onchange = () => this.updatePanel(p.id, v => v.level = level.value, false);
        const sort = card.querySelector('[data-sort]');
        if (sort)
            sort.onchange = () => this.updatePanel(p.id, v => v.sort = sort.value, false);
        const desc = card.querySelector('[data-desc]');
        if (desc)
            desc.onclick = () => { this.updatePanel(p.id, v => v.descending = !v.descending, false); desc.textContent = this.currentTab().panels.find(v => v.id === p.id).descending ? '↓' : '↑'; };
        const handle = card.querySelector('.panel-resize');
        handle.onkeydown = ev => { let h = p.height; if (ev.key === 'ArrowUp')
            h -= 20;
        else if (ev.key === 'ArrowDown')
            h += 20;
        else if (ev.key === 'Home')
            h = 180;
        else if (ev.key === 'End')
            h = 900;
        else
            return; ev.preventDefault(); ev.stopPropagation(); this.updatePanel(p.id, v => v.height = Math.max(180, Math.min(900, h))); document.querySelector(`[data-panel="${p.id}"] .panel-resize`)?.focus(); };
        handle.onpointerdown = ev => { if (ev.button !== 0)
            return; ev.preventDefault(); handle.setPointerCapture(ev.pointerId); const y = ev.clientY, height = p.height; this.interacting = true; let next = height; handle.onpointermove = event => { next = Math.max(180, Math.min(900, Math.round((height + event.clientY - y) / 20) * 20)); card.style.height = next + 'px'; handle.setAttribute('aria-valuenow', String(next)); }; const finish = () => { this.interacting = false; handle.onpointermove = null; handle.onpointerup = null; handle.onpointercancel = null; this.updatePanel(p.id, v => v.height = next); }; handle.onpointerup = finish; handle.onpointercancel = finish; };
        const host = card.querySelector('.panel-render');
        host.onclick = ev => { if (this.mode !== 'inspect' && ev.target.closest('svg'))
            return; const button = ev.target.closest('[data-observation]'); if (button) {
            const index = this.d.context().events.findIndex(e => e.ordinal === Number(button.dataset.observation));
            if (index >= 0)
                this.d.seek(index);
        } };
        host.onpointerdown = ev => this.chartGesture(host, ev);
    }
    movePanel(id, direction) { this.change(w => { const panels = w.tabs.find(t => t.id === w.active_tab).panels, i = panels.findIndex(p => p.id === id), j = i + direction; if (i >= 0 && j >= 0 && j < panels.length)
        [panels[i], panels[j]] = [panels[j], panels[i]]; }); document.querySelector(`[data-panel="${id}"] [data-move="${direction}"]`)?.focus(); }
    updatePanel(id, fn, history = true) { this.change(w => { const p = w.tabs.flatMap(t => t.panels).find(p => p.id === id); if (p)
        fn(p); }, history); }
    window() { const c = this.d.context(), full = extent(c.events), key = this.currentTab()?.id ?? ''; if (c.scopeKey !== this.scope) {
        this.scope = c.scopeKey;
        this.zooms.clear();
    } const saved = this.zooms.get(key); return saved ? boundedWindow(saved[0], saved[1], full) : full; }
    applyWindow(a, b) { const tab = this.currentTab(); if (!tab)
        return; this.zooms.set(tab.id, boundedWindow(a, b, extent(this.d.context().events))); this.renderData(); }
    zoom(factor) { const [a, b] = this.window(), span = b - a; let next = BigInt(Math.round(Number(span) * factor)); if (next < 1n)
        next = 1n; const mid = a + span / 2n; this.applyWindow(mid - next / 2n, mid + (next - next / 2n)); }
    pan(fraction) { const [a, b] = this.window(), delta = BigInt(Math.round(Number(b - a) * fraction)); this.applyWindow(a + delta, b + delta); }
    fit() { const tab = this.currentTab(); if (tab)
        this.zooms.delete(tab.id); this.renderData(); }
    renderData() {
        if (this.interacting)
            return;
        const tab = this.currentTab();
        if (!tab)
            return;
        const c = this.d.context(), window = this.window(), full = extent(c.events);
        $('plotTools').hidden = !tab.panels.some(p => p.kind === 'plot' || p.kind === 'state');
        setText('plotWindowLabel', window[0] === full[0] && window[1] === full[1] ? 'Whole loaded page · receive time' : `Zoomed · ${(Number(window[1] - window[0]) / 1e9).toPrecision(4)} s · loaded page only`);
        for (const p of tab.panels) {
            const card = document.querySelector(`[data-panel="${p.id}"]`);
            if (!card)
                continue;
            const resolved = resolvePanel(p, c), state = card.querySelector('.binding-state');
            state.classList.toggle('binding-error', !!resolved.issues.length);
            state.textContent = resolved.issues.length ? resolved.issues.join(' · ') : p.scope === 'all' && (p.kind === 'raw' || p.kind === 'log') ? 'All sources on this page' : p.binding_mode === 'selection' ? 'Follows signal browser selection' : `${p.binding_mode === 'pinned' ? 'Pinned source IDs' : 'Portable named paths'} · ${resolved.bindings.length} signals`;
            const host = card.querySelector('.panel-render');
            host.innerHTML = panelHTML(p, c, resolved.bindings, window, host.clientWidth - 12);
            host.dataset.gesture = this.mode;
        }
    }
    chartGesture(host, ev) {
        const svg = ev.target.closest('svg[data-chart]');
        if (!svg || ev.button !== 0)
            return;
        ev.preventDefault();
        const c = this.d.context(), [a, b] = this.window(), rect = svg.getBoundingClientRect(), viewW = Number(svg.dataset.width), left = Number(svg.dataset.left), right = Number(svg.dataset.right);
        const fraction = (x) => Math.max(0, Math.min(1, ((x - rect.left) / rect.width * viewW - left) / (right - left)));
        const start = fraction(ev.clientX), startX = ev.clientX;
        host.setPointerCapture(ev.pointerId);
        this.interacting = true;
        host.classList.add('chart-dragging');
        let last = start;
        host.onpointermove = event => { last = fraction(event.clientX); setText('plotWindowLabel', this.mode === 'pan' ? 'Panning receive-time window…' : this.mode === 'zoom' ? 'Drag to choose zoom window…' : 'Release to inspect nearest observation'); };
        const finish = (event) => {
            this.interacting = false;
            host.classList.remove('chart-dragging');
            host.onpointermove = null;
            host.onpointerup = null;
            host.onpointercancel = null;
            if (event.type === 'pointercancel') {
                this.renderData();
                return;
            }
            last = fraction(event.clientX);
            if (this.mode === 'zoom' && Math.abs(event.clientX - startX) > 5) {
                this.applyWindow(a + BigInt(Math.round(Number(b - a) * Math.min(start, last))), a + BigInt(Math.round(Number(b - a) * Math.max(start, last))));
            }
            else if (this.mode === 'pan') {
                const delta = BigInt(Math.round(Number(b - a) * (start - last)));
                this.applyWindow(a + delta, b + delta);
            }
            else if (this.mode === 'inspect') {
                const t = a + BigInt(Math.round(Number(b - a) * last));
                let best = -1, distance = 2n ** 100n;
                c.events.forEach((event, i) => { if (event.source_id !== svg.dataset.source || event.schema_id !== svg.dataset.schema || event.schema_version !== Number(svg.dataset.version))
                    return; let delta = BigInt(event.received_ns) - t; if (delta < 0)
                    delta = -delta; if (delta < distance) {
                    distance = delta;
                    best = i;
                } });
                if (best >= 0)
                    this.d.seek(best);
            }
            this.renderData();
        };
        host.onpointerup = finish;
        host.onpointercancel = finish;
    }
    panelDialog(id) {
        const s = this.state, tab = this.currentTab();
        if (!s || !tab)
            return;
        if (s.blocked) {
            this.reviewSave();
            return;
        }
        if (!id && tab.panels.length >= 12)
            throw Error('A tab supports up to 12 panels. Add another tab for a different view.');
        const existing = tab.panels.find(p => p.id === id), p = clone(existing ?? newPanel('plot')), c = this.d.context();
        const options = c.bindings.map(b => ({ key: b.key, label: `${b.src.name} / ${b.sc.name} v${b.sc.version} / ${b.f.name} · ${b.f.unit || b.f.type}`, ref: reference(b) }));
        for (const r of p.bindings) {
            const key = r.source_id + '/' + r.schema_id + '/' + r.version + '/' + r.field_id;
            if (!options.some(v => v.key === key))
                options.push({ key, label: `Missing: ${r.source_name} / ${r.schema_name} v${r.version} / ${r.field_name}`, ref: r });
        }
        const selected = new Set(existing && p.binding_mode !== 'selection' ? p.bindings.map(r => r.source_id + '/' + r.schema_id + '/' + r.version + '/' + r.field_id) : [...c.selected]);
        this.d.dialog(existing ? 'Configure panel' : 'Add a panel', `<div class="rangegrid"><div><label for="panelKind">Panel type</label><select id="panelKind">${Object.entries(panelNames).map(([key, name]) => `<option value="${key}" ${p.kind === key ? 'selected' : ''}>${esc(name)}</option>`).join('')}</select></div><div><label for="panelTitle">Panel title</label><input id="panelTitle" value="${esc(p.title)}" maxlength="160" required></div></div><label for="panelBindingMode">Signal binding</label><select id="panelBindingMode"><option value="selection" ${p.binding_mode === 'selection' ? 'selected' : ''}>Follow signal browser selection</option><option value="pinned" ${p.binding_mode === 'pinned' ? 'selected' : ''}>Pin exact source / schema / field IDs</option><option value="portable" ${p.binding_mode === 'portable' ? 'selected' : ''}>Portable named paths</option></select><p id="bindingHelp" class="muted">Pinned bindings preserve IDs. Portable paths require an unambiguous source/event/field name, exact version, type, unit and enum definition.</p><div id="panelBindingChoices" class="binding-choices" ${p.binding_mode === 'selection' ? 'hidden' : ''}>${options.map((v, i) => `<label><input type="checkbox" data-panel-binding="${i}" ${selected.has(v.key) ? 'checked' : ''}>${esc(v.label)}</label>`).join('') || '<p>No fields are defined yet. Follow the signal browser, or define a source first.</p>'}</div><div id="panelScopeWrap" ${['raw', 'log'].includes(p.kind) ? '' : 'hidden'}><label for="panelScope">Events included</label><select id="panelScope"><option value="all" ${p.scope === 'all' ? 'selected' : ''}>All sources on loaded page</option><option value="bindings" ${p.scope === 'bindings' ? 'selected' : ''}>Only sources and schema versions from resolved bindings</option></select></div><details open><summary>Panel size and appearance</summary><div class="rangegrid"><div><label for="panelSpan">Width · columns</label><select id="panelSpan">${[1, 2, 3].map(n => `<option ${n === p.span ? 'selected' : ''}>${n}</option>`).join('')}</select></div><div><label for="panelHeight">Height · px</label><input id="panelHeight" type="number" min="180" max="900" step="20" value="${p.height}"></div></div><p class="muted">Width is capped by the tab’s column count. Panels stack on phones. Drag the bottom edge or focus it and use ↑ / ↓ to resize.</p><div id="plotAppearance" ${p.kind === 'plot' ? '' : 'hidden'}><label for="panelPalette">Plot palette</label><select id="panelPalette">${['instrument', 'blue', 'warm'].map(v => `<option ${v === p.palette ? 'selected' : ''}>${v}</option>`).join('')}</select><label for="panelLine">Line style</label><select id="panelLine">${['linear', 'step', 'points'].map(v => `<option ${v === p.line ? 'selected' : ''}>${v}</option>`).join('')}</select><div class="rangegrid"><div><label for="panelYMin">Y minimum · blank = automatic</label><input id="panelYMin" value="${esc(p.y_min)}" inputmode="decimal"></div><div><label for="panelYMax">Y maximum</label><input id="panelYMax" value="${esc(p.y_max)}" inputmode="decimal"></div></div><p class="muted">Each signal keeps its own unit. Fixed limits apply to every lane in this panel. Integer plots use a labeled delta from their exact baseline.</p></div><div id="valueAppearance" ${p.kind === 'value' ? '' : 'hidden'}><label for="panelDecimals">Float display decimals · -1 = automatic</label><input id="panelDecimals" type="number" min="-1" max="12" value="${p.decimals}"><p class="muted">Exact integers and the original payload are never rounded.</p></div></details>${existing ? '<div class="panel-dialog-actions"><button type="button" id="duplicatePanel">Duplicate panel</button><button type="button" id="removePanel">Remove panel</button></div>' : ''}`, 'Save panel', async () => {
            p.kind = $('panelKind').value;
            p.title = $('panelTitle').value.trim();
            p.binding_mode = $('panelBindingMode').value;
            p.bindings = p.binding_mode === 'selection' ? [] : [...$('panelBindingChoices').querySelectorAll('input:checked')].map(v => options[Number(v.dataset.panelBinding)].ref);
            if (p.bindings.length > 8)
                throw Error('Choose at most eight signals per panel.');
            if (!p.title || new TextEncoder().encode(p.title).length > 160)
                throw Error('Give the panel a title of at most 160 UTF-8 bytes.');
            p.scope = ['raw', 'log'].includes(p.kind) ? $('panelScope').value : 'bindings';
            p.span = Number($('panelSpan').value);
            p.height = Number($('panelHeight').value);
            p.palette = $('panelPalette').value;
            p.line = $('panelLine').value;
            p.decimals = Number($('panelDecimals').value);
            p.y_min = $('panelYMin').value.trim();
            p.y_max = $('panelYMax').value.trim();
            if ((p.y_min === '') !== (p.y_max === '') || p.y_min !== '' && (!Number.isFinite(Number(p.y_min)) || !Number.isFinite(Number(p.y_max)) || Number(p.y_min) >= Number(p.y_max)))
                throw Error('Set two finite Y bounds with minimum below maximum, or leave both blank.');
            this.change(w => { const panels = w.tabs.find(t => t.id === w.active_tab).panels; const i = panels.findIndex(v => v.id === p.id); if (i >= 0)
                panels[i] = p;
            else
                panels.push(p); });
            this.d.close();
        });
        $('panelBindingMode').onchange = () => { $('panelBindingChoices').hidden = $('panelBindingMode').value === 'selection'; };
        $('panelKind').onchange = () => { const k = $('panelKind').value; $('plotAppearance').hidden = k !== 'plot'; $('valueAppearance').hidden = k !== 'value'; $('panelScopeWrap').hidden = !['raw', 'log'].includes(k); if (!existing || Object.values(panelNames).includes($('panelTitle').value))
            $('panelTitle').value = panelNames[k]; if (!existing && ['log', 'raw'].includes(k))
            $('panelScope').value = 'all'; };
        if (existing) {
            this.click('removePanel', () => { this.change(w => { const t = w.tabs.find(t => t.id === w.active_tab); t.panels = t.panels.filter(v => v.id !== p.id); }); this.d.close(); this.d.notify('Panel removed. Undo layout restores it.'); });
            this.click('duplicatePanel', () => { if (tab.panels.length >= 12)
                throw Error('This tab already has 12 panels.'); this.change(w => { const copy = clone(existing); copy.id = freshID(); copy.title = titleCopy(copy.title); w.tabs.find(t => t.id === w.active_tab).panels.push(copy); }); this.d.close(); });
        }
    }
    layoutDialog() {
        const s = this.state, tab = this.currentTab();
        if (!s || !tab)
            return;
        if (s.blocked) {
            this.reviewSave();
            return;
        }
        this.d.dialog('Workspace tabs & portability', `<p>${this.d.readOnly() ? 'Viewer changes apply to this temporary view only and reset on reload. Export a copy to retain them.' : 'Layouts are saved with this project on the server.'} They organize views; original observations and recording schemas remain unchanged.</p><div class="rangegrid"><div><label for="tabName">Current tab name</label><input id="tabName" value="${esc(tab.name)}" maxlength="160" required></div><div><label for="tabColumns">Columns on wide screens</label><select id="tabColumns">${[1, 2, 3].map(n => `<option ${n === tab.columns ? 'selected' : ''}>${n}</option>`).join('')}</select></div></div><div class="workspace-actions"><button id="newLayoutTab" type="button" ${s.doc.tabs.length >= 8 ? 'disabled' : ''}>New empty tab</button><button id="duplicateLayoutTab" type="button" ${s.doc.tabs.length >= 8 ? 'disabled' : ''}>Duplicate tab</button><button id="removeLayoutTab" type="button" ${s.doc.tabs.length === 1 ? 'disabled' : ''}>Remove tab</button><button id="moveTabLeft" type="button">Move tab earlier</button><button id="moveTabRight" type="button">Move tab later</button></div><h3>Move views between projects</h3><div class="exportchoices"><button id="exportWorkspace" type="button">Export exact workspace JSON</button><button id="exportPortable" type="button">Export portable workspace JSON</button><button id="importWorkspace" type="button">Import workspace JSON…</button></div><p>Portable export freezes the current signal selection into named bindings. Import shows unresolved or ambiguous paths until you rebind them. No signals or data are imported by a layout file.</p><p><a href="/docs/WORKSPACES.md" target="_blank">Panels, bindings, keyboard controls and recovery</a></p>`, 'Save tab', async () => { const name = $('tabName').value.trim(); if (!name || new TextEncoder().encode(name).length > 160)
            throw Error('Give the tab a name of at most 160 UTF-8 bytes.'); this.change(w => { const t = w.tabs.find(t => t.id === w.active_tab); t.name = name; t.columns = Number($('tabColumns').value); }); this.d.close(); });
        this.click('newLayoutTab', () => this.newTabDialog());
        this.click('duplicateLayoutTab', () => { this.change(w => { const t = clone(tab); t.id = freshID(); t.name = titleCopy(t.name); t.panels.forEach(p => p.id = freshID()); w.tabs.push(t); w.active_tab = t.id; }); this.d.close(); });
        this.click('removeLayoutTab', () => { this.change(w => { w.tabs = w.tabs.filter(t => t.id !== tab.id); w.active_tab = w.tabs[0].id; }); this.d.close(); this.d.notify('Tab removed. Undo layout restores it.'); });
        for (const [button, delta] of [['moveTabLeft', -1], ['moveTabRight', 1]])
            this.click(button, () => { this.change(w => { const i = w.tabs.findIndex(t => t.id === tab.id), j = i + delta; if (j >= 0 && j < w.tabs.length)
                [w.tabs[i], w.tabs[j]] = [w.tabs[j], w.tabs[i]]; }); this.layoutDialog(); });
        this.click('exportWorkspace', () => this.exportWorkspace(false));
        this.click('exportPortable', () => this.exportWorkspace(true));
        this.click('importWorkspace', () => { $('workspaceFile').click(); });
    }
    newTabDialog() { this.d.dialog('Create a workspace tab', '<label for="newTabName">Tab name</label><input id="newTabName" value="New view" maxlength="160" required><p>Start empty, then add panels for the question you are investigating.</p>', 'Create tab', async () => { const name = $('newTabName').value.trim(); if (!name || new TextEncoder().encode(name).length > 160)
        throw Error('Give the tab a name of at most 160 UTF-8 bytes.'); this.change(w => { if (w.tabs.length >= 8)
        throw Error('Maximum eight tabs.'); const t = { id: freshID(), name, columns: 2, panels: [] }; w.tabs.push(t); w.active_tab = t.id; }); this.d.close(); }); }
    exportWorkspace(portable) { if (!this.state)
        return; const doc = clone(this.state.doc), c = this.d.context(); if (portable)
        for (const tab of doc.tabs)
            for (const p of tab.panels) {
                if (p.binding_mode === 'selection')
                    p.bindings = resolvePanel(p, c).bindings.map(reference);
                p.binding_mode = 'portable';
            } this.d.download(new Blob([JSON.stringify(doc)], { type: 'application/json' }), portable ? 'TRACEWORK-workspace-portable.json' : 'TRACEWORK-workspace.json'); this.d.notify('Workspace exported. Recorded evidence is unchanged.'); }
    async importFile() {
        const input = $('workspaceFile'), file = input.files?.[0];
        input.value = '';
        if (!file)
            return;
        if (!this.state)
            throw Error('Choose a project before importing a workspace.');
        if (file.size > 256 * 1024)
            throw Error('Workspace JSON exceeds 256 KiB.');
        const candidate = JSON.parse(await file.text());
        if (candidate.format !== 'tracework.workspace.v1' || !Array.isArray(candidate.tabs) || candidate.tabs.length < 1 || candidate.tabs.length > 8)
            throw Error('Expected tracework.workspace.v1 with one to eight tabs.');
        this.d.dialog('Import workspace', `<p>Replace this project’s saved layout with <b>${esc(file.name)}</b>? Original observations and schemas remain unchanged. The complete layout is validated before it is saved.</p><ul>${candidate.tabs.map((t) => `<li>${esc(t.name)} · ${Array.isArray(t.panels) ? t.panels.length : 0} panels</li>`).join('')}</ul><p>Export your current layout first if you want a separate backup. Undo layout also remains available during this browser session.</p>`, 'Import layout', async () => { await this.flush(); const s = this.state; const result = await this.d.api('/projects/' + s.pid + '/workspace', 'PUT', { expected_revision: s.revision, workspace: candidate }); this.undoStack.push(clone(s.doc)); s.doc = result.workspace; s.revision = result.revision; s.dirty = false; s.serial++; this.signature = ''; this.tabSignature = ''; this.zooms.clear(); this.d.close(); this.render(); this.d.notify('Workspace imported. Review any unresolved bindings before using the panels.'); });
    }
    reviewSave() {
        const s = this.state;
        if (!s)
            return;
        const local = s.recovery ?? s.doc;
        this.d.dialog('Recover workspace changes', `<p>${esc(s.message || 'Review the local layout and the last saved server layout.')}</p><p>Export a local copy before replacing it if needed. Loading the saved layout discards the current unsaved view. Keeping the local layout explicitly replaces the latest server layout.</p><div class="exportchoices"><button id="exportLocalLayout" type="button">Export local recovery copy</button><button id="loadSavedLayout" type="button">Load saved layout</button><button id="keepLocalLayout" type="button">Keep local layout on server</button></div>`, null, null);
        this.click('exportLocalLayout', () => { this.d.download(new Blob([JSON.stringify(local, null, 2)], { type: 'application/json' }), 'TRACEWORK-workspace-recovery.json'); });
        this.click('loadSavedLayout', async () => { const latest = await this.d.api('/projects/' + s.pid + '/workspace'); if (this.state !== s)
            return; s.doc = latest.workspace ?? defaultWorkspace(); s.revision = latest.revision; s.recovery = null; s.blocked = false; s.dirty = !latest.workspace; this.signature = ''; this.tabSignature = ''; this.undoStack = []; try {
            localStorage.removeItem(draftKey(s.pid));
        }
        catch { } this.d.close(); this.render(); if (s.dirty)
            void this.save(s); });
        this.click('keepLocalLayout', async () => { const latest = await this.d.api('/projects/' + s.pid + '/workspace'); const saved = await this.d.api('/projects/' + s.pid + '/workspace', 'PUT', { expected_revision: latest.revision, workspace: local }); if (this.state !== s)
            return; s.doc = saved.workspace; s.revision = saved.revision; s.recovery = null; s.blocked = false; s.dirty = false; this.signature = ''; this.tabSignature = ''; try {
            localStorage.removeItem(draftKey(s.pid));
        }
        catch { } this.d.close(); this.render(); });
    }
}
