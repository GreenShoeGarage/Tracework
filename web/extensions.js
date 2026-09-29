// SPDX-License-Identifier: GPL-3.0-only
import { escapeHTML as e } from './panel-renderers.js';
const $ = (id) => document.getElementById(id);
export class ExtensionsUI {
    d;
    epoch = 0;
    frame = null;
    payload = null;
    ready = false;
    constructor(d) {
        this.d = d;
        $('extensionTools').onclick = () => this.open().catch(d.error);
        $('closeExtensions').onclick = () => this.reset();
        window.addEventListener('message', event => { if (event.source !== this.frame?.contentWindow || event.origin !== 'null' || event.data?.type !== 'tracework.extension.ready')
            return; this.ready = true; if (this.payload) {
            this.frame.contentWindow.postMessage(this.payload, '*');
            $('extensionStatus').textContent = 'Snapshot delivered to the isolated renderer. No control capability or credential was provided.';
        } });
    }
    reset() { this.epoch++; this.frame?.remove(); this.frame = null; this.payload = null; this.ready = false; $('extensionView').hidden = true; $('workbench').hidden = !this.d.context().project; }
    async open() {
        const c = this.d.context();
        if (!c.project)
            throw Error('Choose a project and select signals before opening an extension.');
        this.d.leaveOthers();
        const epoch = ++this.epoch;
        const extensions = await this.d.api('/extensions');
        if (epoch !== this.epoch)
            return;
        $('workbench').hidden = true;
        $('extensionView').hidden = false;
        $('extensionBody').innerHTML = `<p class="muted">Installed renderers receive an explicitly approved snapshot of selected signal values. They run in an opaque iframe with network access and parent-page access blocked.</p>${extensions.length ? `<label for="extensionChoice">Locally installed renderer</label><select id="extensionChoice">${extensions.map((x) => `<option value="${e(x.id)}">${e(x.name)}</option>`).join('')}</select><div id="extensionDetails"></div><button id="renderExtension" class="primary">Send selected signals to renderer</button><p id="extensionStatus" role="status">No evidence has been sent.</p><div id="extensionFrame"></div>` : '<section class="investigation-section"><h2>No browser extension installed.</h2><p>Install a reviewed renderer on the server host, then start the server with its explicit extension directory.</p><pre>tracework extension-install --directory ./local-extensions \\\n  --manifest examples/extensions/summary.json \\\n  --script examples/extensions/summary.js\ntracework serve --extensions ./local-extensions</pre></section>'}<p><a href="/docs/INTEGRATIONS.md" target="_blank">SDKs, adapters, extension contracts and qualification limits</a></p>`;
        if (!extensions.length)
            return;
        const details = () => { const x = extensions.find((x) => x.id === $('extensionChoice').value); $('extensionDetails').innerHTML = `<p>${e(x.description)}</p><p class="muted">Renderer SHA-256 <code>${e(x.script_sha256)}</code></p>`; };
        details();
        $('extensionChoice').onchange = details;
        $('renderExtension').onclick = () => { try {
            this.render($('extensionChoice').value);
        }
        catch (err) {
            this.d.error(err);
        } };
    }
    render(id) { const c = this.d.context(); const bindings = c.bindings.filter((b) => c.selected.has(b.key)).slice(0, 8); if (!bindings.length)
        throw Error('Select one or more signals in the signal browser.'); const events = c.events.slice(Math.max(0, c.cursor - 199), c.cursor + 1); const observations = events.flatMap((ev) => { const selected = bindings.filter((b) => b.src.id === ev.source_id && b.sc.id === ev.schema_id && b.sc.version === ev.schema_version); if (!selected.length)
        return []; return [{ event_id: ev.id, ordinal: ev.ordinal, source_id: ev.source_id, schema_id: ev.schema_id, schema_version: ev.schema_version, timestamp_ns: ev.timestamp_ns, received_ns: ev.received_ns, clock_domain: ev.clock_domain, sync_quality: ev.sync_quality, uncertainty_ns: ev.uncertainty_ns, gap_before: ev.gap_before, values: Object.fromEntries(selected.map((b) => [b.f.name, ev.values[b.f.name]])) }]; }); const payload = { type: 'tracework.extension.snapshot', format: 'tracework.extension.snapshot.v1', theme: document.documentElement.dataset.theme, recorded: !!c.session, session_id: c.session?.id ?? '', signals: bindings.map((b) => ({ source_id: b.src.id, schema_id: b.sc.id, schema_version: b.sc.version, field_id: b.f.id, name: b.f.name, unit: b.f.unit, type: b.f.type })), observations, limits: 'At most 200 currently loaded observations and eight explicitly selected fields; no server credentials, mutation API or unselected values.' }; if (new TextEncoder().encode(JSON.stringify(payload)).length > 1 << 20)
        throw Error('Snapshot exceeds 1 MiB. Select fewer fields or a smaller loaded range.'); this.frame?.remove(); this.ready = false; this.payload = payload; this.frame = document.createElement('iframe'); this.frame.title = 'Isolated ' + id + ' renderer'; this.frame.sandbox.add('allow-scripts'); this.frame.src = '/extensions/' + id + '/frame.html'; $('extensionFrame').replaceChildren(this.frame); $('extensionStatus').textContent = 'Starting isolated renderer…'; const frame = this.frame; setTimeout(() => { if (this.frame === frame && !this.ready)
        $('extensionStatus').textContent = 'Renderer did not acknowledge the extension protocol. No snapshot was delivered.'; }, 5000); }
}
