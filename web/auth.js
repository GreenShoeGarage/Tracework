// SPDX-License-Identifier: GPL-3.0-only
import { escapeHTML as e } from './panel-renderers.js';
const $ = (id) => document.getElementById(id);
export class AccountsUI {
    d;
    me = { id: '', name: 'Not signed in', role: 'viewer' };
    constructor(d) {
        this.d = d;
        $('account').onclick = () => this.manage().catch(d.error);
    }
    async refresh() { this.me = await this.d.api('/auth/me'); document.body.dataset.role = this.me.role; $('account').textContent = this.me.id === 'local' ? 'Local access' : this.me.name + ' · ' + this.me.role; $('account').title = 'Accounts, sharing, audit and backup'; return this.me; }
    async login() { const info = await fetch('/api/v1/auth/info').then(r => r.json()); this.d.dialog('Sign in to TRACEWORK', `${info.accounts_enabled ? '<p>Use your local account. This server does not contact an external identity provider.</p><label for="accountName">Account name</label><input id="accountName" autocomplete="username"><label for="accountPassword">Password</label><input id="accountPassword" type="password" autocomplete="current-password">' : ''}<${info.accounts_enabled ? 'details' : 'div'}><${info.accounts_enabled ? 'summary' : 'p'}>Server administrator credential</${info.accounts_enabled ? 'summary' : 'p'}><label for="accessToken">Server access token</label><input type="password" id="accessToken" autocomplete="off"></${info.accounts_enabled ? 'details' : 'div'}><p class="muted">The session credential stays in this browser tab. Network deployments require HTTPS at the local reverse proxy.</p>`, 'Sign in', async () => { const name = $('accountName')?.value ?? ''; let token = $('accessToken').value; if (name) {
        const response = await fetch('/api/v1/auth/login', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ name, password: $('accountPassword').value }) });
        const out = await response.json();
        if (!response.ok)
            throw Error(out.error);
        token = out.token;
    } ; this.d.setToken(token); await this.refresh(); this.d.close(); await this.d.ready(); }); }
    act(id, f) { $(id).onclick = () => Promise.resolve().then(f).catch(this.d.error); }
    async manage() {
        await this.refresh();
        const admin = this.me.role === 'admin';
        const [accounts, shares] = admin ? await Promise.all([this.d.api('/admin/accounts'), this.d.api('/admin/shares')]) : [[], []];
        this.d.dialog('Access & recovery', `<p><b>${e(this.me.name)}</b> · ${e(this.me.role)}. Roles apply to all projects on this installation. Project workspaces organize views; they are not separate tenant boundaries.</p>${this.me.id === 'local' ? '<p>No accounts are enabled. This loopback installation is a standalone workbench. Creating the first administrator enables sign-in for other API clients.</p>' : '<button type="button" id="signOut">Sign out of this tab</button>'}${admin ? `<section><h3>Local accounts</h3><button type="button" id="createAccount">Create ${accounts.length ? 'account' : 'first administrator'}</button>${accounts.map((a) => `<div class="maintenance-item"><b>${e(a.name)}</b> · ${e(a.role)} ${a.disabled ? '· disabled' : ''}<button type="button" data-edit-account="${e(a.id)}">Edit / revoke sessions</button></div>`).join('')}</section><section><h3>Share retained evidence</h3><p>Scoped links allow anyone holding the credential to download one recording or check run until expiry. A downloaded copy cannot be revoked.</p><button type="button" id="createShare">Create evidence link</button>${shares.slice(0, 30).map((s) => `<div class="maintenance-item">${e(s.kind)} · ${e(s.resource_id)} · ${s.revoked ? 'revoked' : 'expires ' + e(new Date(Number(BigInt(s.expires_ns) / 1000000n)).toLocaleString())}${!s.revoked ? `<button type="button" data-revoke-share="${e(s.id)}">Revoke</button>` : ''}</div>`).join('')}</section><section><h3>Recovery & audit</h3><button type="button" id="downloadBackup">Download consistent backup</button><button type="button" id="reviewAudit">Review audit history</button><p class="muted">Backups include all projects, evidence and credential hashes. Keep them private. Capture pauses during snapshot creation; restored active recordings are marked interrupted.</p><p>Restore into a new directory: <code>tracework restore --backup backup.zip --data NEW_DIRECTORY</code></p></section>` : '<p>Viewers can read and export evidence. Operators can capture, edit investigations and run checks/actions. Administrators manage accounts, sharing, permanent deletion, retention and backups.</p>'}<p><a href="/docs/ACCESS-AND-RECOVERY.md" target="_blank">Access, credential recovery and backup guide</a></p>`, null, null);
        if (this.me.id !== 'local')
            this.act('signOut', async () => { await this.d.api('/auth/logout', 'POST', {}); this.d.setToken(''); for (const key of Object.keys(sessionStorage))
                if (key.startsWith('tracework.leases.'))
                    sessionStorage.removeItem(key); location.reload(); });
        if (!admin)
            return;
        this.act('createAccount', () => this.accountDialog(null, accounts));
        this.act('createShare', () => this.shareDialog());
        this.act('downloadBackup', async () => { const r = await fetch('/api/v1/admin/backup', { method: 'POST', headers: { Authorization: 'Bearer ' + this.d.token() } }); if (!r.ok)
            throw Error((await r.json()).error); this.d.download(await r.blob(), 'TRACEWORK-backup.zip'); this.d.notify('Verified snapshot downloaded. Keep the backup private and test restore before relying on it.'); });
        this.act('reviewAudit', async () => { const audit = await this.d.api('/admin/audit'); this.d.dialog('Audit history', `<p>${e(audit.integrity_scope)}</p><p>${audit.entries.length} latest entries. Status 0 records an intent; the matching request ID links its response outcome.</p><pre>${e(JSON.stringify(audit.entries, null, 2))}</pre><button type="button" id="exportAudit">Export audit JSON</button>`, null, null); this.act('exportAudit', () => this.d.download(new Blob([JSON.stringify(audit, null, 2)], { type: 'application/json' }), 'tracework-audit.json')); });
        $('dialogBody').querySelectorAll('[data-edit-account]').forEach(b => b.onclick = () => this.accountDialog(accounts.find((a) => a.id === b.dataset.editAccount), accounts));
        $('dialogBody').querySelectorAll('[data-revoke-share]').forEach(b => b.onclick = () => this.d.api('/admin/shares/' + b.dataset.revokeShare, 'DELETE').then(() => this.manage()).catch(this.d.error));
    }
    accountDialog(account, accounts) { this.d.dialog(account ? 'Edit local account' : 'Create local account', `<p>${accounts.length ? 'Changing an account revokes its browser/API sessions.' : 'The first account must be an administrator. Sign in with it after creation.'}</p><label for="editAccountName">Account name</label><input id="editAccountName" required pattern="[a-zA-Z0-9_-]{1,64}" value="${e(account?.name ?? '')}" ${account ? 'disabled' : ''}><label for="editAccountRole">Role</label><select id="editAccountRole">${(accounts.length ? ['viewer', 'operator', 'admin'] : ['admin']).map(r => `<option ${r === account?.role ? 'selected' : ''}>${r}</option>`).join('')}</select><label for="editAccountPassword">${account ? 'New password · blank keeps existing' : 'Password · at least 12 bytes'}</label><input id="editAccountPassword" type="password" autocomplete="new-password" ${account ? '' : 'required'} minlength="12">${account ? `<label><input id="editAccountDisabled" type="checkbox" ${account.disabled ? 'checked' : ''}> Disable account</label>` : ''}`, 'Save account', async () => { const password = $('editAccountPassword').value, role = $('editAccountRole').value; if (account)
        await this.d.api('/admin/accounts/' + account.id, 'PUT', { role, disabled: $('editAccountDisabled').checked, password });
    else
        await this.d.api('/admin/accounts', 'POST', { name: $('editAccountName').value, role, password }); this.d.close(); if (!accounts.length || account?.id === this.me.id) {
        this.d.setToken('');
        await this.login();
    }
    else
        await this.manage(); }); }
    async shareDialog() { const recordings = (await this.d.api('/recordings')).filter((s) => s.status !== 'recording' && !s.trashed); const project = this.d.context().project; const runs = project ? await this.d.api('/projects/' + project.id + '/check-runs') : []; const choices = [...recordings.map((s) => ({ key: 'recording/' + s.id, label: 'Recording · ' + s.name })), ...runs.map((r) => ({ key: 'check_run/' + r.id, label: 'Checks · ' + r.status + ' · ' + r.id }))]; if (!choices.length)
        throw Error('Stop a recording or retain a check run first.'); this.d.dialog('Create scoped evidence link', `<label for="shareResource">One retained resource</label><select id="shareResource">${choices.map(c => `<option value="${e(c.key)}">${e(c.label)}</option>`).join('')}</select><label for="shareHours">Expires after hours · 1–168</label><input id="shareHours" type="number" min="1" max="168" value="24" required><p>Anyone holding the generated link can download this resource. No link is sent automatically.</p>`, 'Create link', async () => { const [kind, resource_id] = $('shareResource').value.split('/'); const out = await this.d.api('/admin/shares', 'POST', { kind, resource_id, expires_hours: Number($('shareHours').value) }); const url = location.origin + '/share.html#' + out.link_fragment; this.d.dialog('Scoped evidence link', `<p>${e(out.notice)}</p><label for="shareLink">Copy this link; its secret is shown once</label><textarea id="shareLink" readonly rows="4">${e(url)}</textarea><button type="button" id="copyShare">Copy link</button>`, null, null); this.act('copyShare', async () => { await navigator.clipboard.writeText(url); this.d.notify('Evidence link copied.'); }); }); }
}
