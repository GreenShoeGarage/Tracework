// SPDX-License-Identifier: GPL-3.0-only
import { escapeHTML as e } from './panel-renderers.js';
const $ = (id) => document.getElementById(id);
export class ActionsUI {
    d;
    pid = '';
    data = { providers: [], runs: [] };
    leases = new Map();
    signature = '';
    epoch = 0;
    refreshing = false;
    lastRenew = 0;
    constructor(d) {
        this.d = d;
        $('operate').onclick = () => this.open().catch(d.error);
        $('closeOperations').onclick = () => this.reset();
        setInterval(() => { if (this.pid && !this.refreshing)
            this.refresh().catch(d.error); }, 1500);
    }
    reset() { this.epoch++; this.pid = ''; $('operationView').hidden = true; $('workbench').hidden = !this.d.context().project; }
    act(id, fn) { $(id).onclick = () => Promise.resolve().then(fn).catch(this.d.error); }
    async open() { const c = this.d.context(); if (!c.project)
        throw Error('Choose a project first.'); this.d.leaveOthers(); this.pid = c.project.id; this.epoch++; this.signature = ''; $('workbench').hidden = true; $('operationView').hidden = false; try {
        this.leases = new Map(JSON.parse(sessionStorage.getItem('tracework.leases.' + this.pid) ?? '[]'));
    }
    catch {
        this.leases = new Map();
    } ; await this.refresh(); }
    remember() { try {
        sessionStorage.setItem('tracework.leases.' + this.pid, JSON.stringify([...this.leases]));
    }
    catch { } }
    async take(id, release = false) { const out = await this.d.api('/projects/' + this.pid + '/providers/' + id + '/lease', 'POST', { lease_token: this.leases.get(id) ?? '', label: 'Browser operator', release }); if (release)
        this.leases.delete(id);
    else
        this.leases.set(id, out.lease_token); this.remember(); this.signature = ''; await this.refresh(); this.d.notify(release ? 'Control released.' : 'Exclusive control acquired for 90 seconds; renews while this view is open.'); }
    async refresh() { const pid = this.pid, epoch = this.epoch; if (!pid)
        return; this.refreshing = true; try {
        const data = await this.d.api('/projects/' + pid + '/actions');
        if (this.pid !== pid || this.epoch !== epoch)
            return;
        this.data = data;
        if (Date.now() - this.lastRenew > 30000) {
            this.lastRenew = Date.now();
            for (const [id, secret] of this.leases) {
                try {
                    await this.d.api('/projects/' + pid + '/providers/' + id + '/lease', 'POST', { lease_token: secret, label: 'Browser operator', release: false });
                }
                catch {
                    this.leases.delete(id);
                    this.remember();
                }
            }
        }
        ;
        const sig = JSON.stringify([data.providers.map((p) => [p.id, p.name, p.online, p.actions]), data.runs, [...this.leases.keys()]]);
        if (sig !== this.signature) {
            this.signature = sig;
            this.render();
        }
    }
    finally {
        this.refreshing = false;
    } }
    render() {
        const c = this.d.context();
        $('operationBody').innerHTML = `<p class="muted">Take exclusive control, review parameters, then arm one action. The provider validates again before touching its target. A timeout after dispatch means the outcome is unknown.</p><button id="providerSetup">Connect an action provider</button><div class="provider-grid">${this.data.providers.map((p) => { const busy = this.data.runs.some((r) => r.provider_id === p.id && ['queued', 'running', 'cancel_requested', 'unknown'].includes(r.state)); return `<section class="investigation-section"><div class="sectiontitle"><h2>${e(p.name)}</h2><span class="pill ${p.online ? 'ok' : 'bad'}">${p.online ? '● Online' : '○ Offline'}</span></div><p class="muted">Source ${e(c.project?.sources.find((s) => s.id === p.source_id)?.name ?? p.source_id)} · ${this.leases.has(p.id) ? 'You hold control' : 'Control required'}</p><div class="row wrap"><button data-own="${e(p.id)}" ${!p.online ? 'disabled' : ''}>${this.leases.has(p.id) ? 'Renew control' : 'Take control'}</button>${this.leases.has(p.id) ? `<button data-release="${e(p.id)}">Release control</button>` : ''}</div>${busy ? '<p class="action-warning">An action is outstanding. Resolve its outcome before starting another.</p>' : ''}<div class="action-list">${p.actions.map((a) => `<button data-action="${e(p.id + '/' + a.id)}" ${!p.online || !this.leases.has(p.id) || busy ? 'disabled' : ''}><b>${e(a.name)}</b><span>${e(a.description)}</span><small>${e(a.effect)} · target timeout ${a.timeout_ms} ms</small></button>`).join('')}</div></section>`; }).join('') || '<div class="panel-empty"><h2>No action provider connected.</h2><p>Install a provider program on the target host. Recording and observation work independently.</p></div>'}</div><section class="investigation-section"><h2>Action outcome history</h2><p class="muted">Success, failure, cancellation and unknown outcomes remain distinct. Cancellation is only complete when the target confirms it.</p><div class="tablewrap"><table><thead><tr><th>Action / target</th><th>Outcome</th><th>Detail</th><th>Review</th></tr></thead><tbody>${this.data.runs.map((r) => `<tr><td>${e(r.action.name)}<br><small>${e(this.data.providers.find((p) => p.id === r.provider_id)?.name ?? r.provider_id)}</small></td><td><b>${e(r.state)}</b>${r.late_result ? ' · late target report' : ''}</td><td>${e(r.detail)}</td><td><button data-run="${e(r.id)}">Inspect</button>${['queued', 'running'].includes(r.state) && this.leases.has(r.provider_id) ? `<button data-cancel="${e(r.id)}">Request cancellation</button>` : ''}${r.state === 'unknown' && this.leases.has(r.provider_id) ? `<button data-reconcile="${e(r.id)}">Reconcile target state…</button>` : ''}</td></tr>`).join('')}</tbody></table></div></section>`;
        this.act('providerSetup', () => this.setup());
        for (const [attr, fn] of [['own', (id) => this.take(id)], ['release', (id) => this.take(id, true)], ['action', (key) => this.parameterDialog(key)], ['cancel', (id) => this.cancel(id)], ['run', (id) => this.inspect(id)], ['reconcile', (id) => this.reconcile(id)]])
            $('operationBody').querySelectorAll('[data-' + attr + ']').forEach(b => b.onclick = () => Promise.resolve().then(() => fn(b.dataset[attr])).catch(this.d.error));
    }
    setup() { const c = this.d.context(); this.d.dialog('Connect an action provider', `<p>Run a provider on the acquisition/target host. It advertises named actions and typed parameters; the server never executes code from a project file.</p><p>The supplied software-counter example changes only a local text file and demonstrates validation, ownership and cancellation.</p>${c.project.sources.length ? `<pre>PYTHONPATH=sdk/python python3 examples/action_provider.py \\\n  --project ${e(this.pid)} \\\n  --source ${e(c.project.sources[0].id)}</pre>` : '<p>Define a source first, then return here for its IDs.</p>'}<p>Keep the registration token on that host. Its credential is restricted to its own provider transport. Adapt locally installed handlers for your target, including hardware limits and interlocks.</p><p><a href="/docs/ACTIONS.md" target="_blank">Provider lifecycle, recovery and SDK guide</a></p>`, null, null); }
    parameterDialog(key) { const [pid, aid] = key.split('/'), provider = this.data.providers.find((p) => p.id === pid), action = provider.actions.find((a) => a.id === aid); this.d.dialog(action.name, `<p><b>Target:</b> ${e(provider.name)} · ${e(action.effect)} action</p><p>${e(action.description)}</p>${action.parameters.map((p, i) => { const f = p.field, id = 'actionParam' + i; const input = f.type === 'bool' ? `<select id="${id}"><option value="false">false</option><option value="true">true</option></select>` : f.type === 'enum' ? `<select id="${id}">${f.enum.map((v) => `<option>${e(v)}</option>`).join('')}</select>` : `<input id="${id}" value="${e(f.type === 'string' ? '' : p.min || '0')}" ${f.type === 'float64' ? 'type="number" step="any"' : 'type="text"'} required>`; return `<label for="${id}">${e(f.name)} · ${e(f.unit || f.type)}</label>${input}${p.min !== '' ? `<p class="muted">Allowed ${e(p.min)} through ${e(p.max)}</p>` : ''}`; }).join('')}`, 'Review action', async () => { const params = {}; action.parameters.forEach((p, i) => { const v = $('actionParam' + i).value; params[p.field.name] = p.field.type === 'bool' ? v === 'true' : p.field.type === 'float64' ? Number(v) : v; }); const prepared = await this.d.api('/projects/' + this.pid + '/actions/prepare', 'POST', { provider_id: pid, action_id: aid, lease_token: this.leases.get(pid), parameters: params }); this.arm(prepared, pid); }); }
    arm(prepared, provider) { const run = prepared.review; this.d.dialog('Arm one action', `<p><b>${e(run.action.name)}</b> on ${e(this.data.providers.find((p) => p.id === provider)?.name)}</p><pre>${e(JSON.stringify(run.parameters, null, 2))}</pre><p>Review expires in 30 seconds. This action is dispatched once. A lost response or timeout must be investigated before another attempt.</p><label><input id="armAction" type="checkbox" required> I reviewed the target and parameters and intend to run this action.</label>`, 'Arm & run', async () => { if (!$('armAction').checked)
        throw Error('Review and arm this action first.'); const result = await this.d.api('/projects/' + this.pid + '/actions/execute', 'POST', { preparation_id: prepared.preparation_id, lease_token: this.leases.get(provider), arming_text: prepared.arming_text }); this.d.close(); await this.refresh(); this.d.notify('Action queued: ' + result.action.name + '. Watch its target outcome below.'); }); }
    inspect(id) { const run = this.data.runs.find((r) => r.id === id); this.d.dialog('Action outcome', `<p>${e(run.state)} · ${e(run.detail)}</p><pre>${e(JSON.stringify(run, null, 2))}</pre>`, null, null); }
    async cancel(id) { const run = this.data.runs.find((r) => r.id === id); await this.d.api('/projects/' + this.pid + '/actions/' + id + '/cancel', 'POST', { lease_token: this.leases.get(run.provider_id) }); await this.refresh(); this.d.notify('Cancellation requested. The target outcome determines whether it stopped.'); }
    reconcile(id) { const run = this.data.runs.find((r) => r.id === id); this.d.dialog('Reconcile unknown target outcome', `<p>The server cannot determine whether this action completed. Check the actual target through an independent observation before releasing this block. This does not turn an unknown result into success.</p><label for="reconcileNote">What did you check, and what is the target state?</label><textarea id="reconcileNote" required minlength="10" maxlength="4096"></textarea><label><input id="reconcileChecked" type="checkbox" required> I checked the target and am ready to allow future actions.</label>`, 'Record reconciliation', async () => { if (!$('reconcileChecked').checked)
        throw Error('Confirm the independent target check.'); await this.d.api('/projects/' + this.pid + '/actions/' + id + '/reconcile', 'POST', { lease_token: this.leases.get(run.provider_id), confirmation: 'CHECKED ' + id, detail: $('reconcileNote').value }); this.d.close(); await this.refresh(); }); }
}
