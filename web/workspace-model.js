export const panelNames = { plot: 'Plot', value: 'Values', table: 'Signal table', raw: 'Raw events', state: 'State timeline', log: 'Logs' };
export const freshID = () => 'view_' + Array.from(crypto.getRandomValues(new Uint8Array(16)), b => b.toString(16).padStart(2, '0')).join('');
export const clone = (v) => JSON.parse(JSON.stringify(v));
export function newPanel(kind, id = freshID()) { return { id, kind, title: panelNames[kind], height: kind === 'value' ? 220 : 400, span: 2, binding_mode: 'selection', bindings: [], scope: kind === 'raw' || kind === 'log' ? 'all' : 'bindings', filter: '', level: '', sort: 'signal', descending: false, decimals: -1, palette: 'instrument', line: 'linear', y_min: '', y_max: '' }; }
export function defaultWorkspace() { return { format: 'tracework.workspace.v1', active_tab: 'plot', tabs: [{ id: 'plot', name: 'Signals & plot', columns: 2, panels: [newPanel('value', 'values_main'), newPanel('plot', 'plot_main')] }, { id: 'raw', name: 'Event ledger', columns: 1, panels: [newPanel('raw', 'raw_main')] }, { id: 'logs', name: 'Logs', columns: 1, panels: [newPanel('log', 'logs_main')] }] }; }
export function reference(b) { return { source_id: b.src.id, schema_id: b.sc.id, field_id: b.f.id, version: b.sc.version, source_name: b.src.name, schema_name: b.sc.name, field_name: b.f.name, type: b.f.type, unit: b.f.unit, enum: b.f.enum ?? [] }; }
export function resolvePanel(p, c) {
    if (p.binding_mode === 'selection')
        return { bindings: c.bindings.filter(b => c.selected.has(b.key)).slice(0, 8), issues: [] };
    const bindings = [], issues = [];
    for (const r of p.bindings) {
        const candidates = c.bindings.filter(b => p.binding_mode === 'pinned' ? b.src.id === r.source_id && b.sc.id === r.schema_id && b.sc.version === r.version && b.f.id === r.field_id : b.src.name === r.source_name && b.sc.name === r.schema_name && b.sc.version === r.version && b.f.name === r.field_name);
        const label = `${r.source_name} / ${r.schema_name} v${r.version} / ${r.field_name}`;
        if (candidates.length !== 1) {
            issues.push(`${label}: ${candidates.length ? 'ambiguous path' : 'not found'}`);
            continue;
        }
        const b = candidates[0];
        if (b.f.type !== r.type || b.f.unit !== r.unit || JSON.stringify(b.f.enum ?? []) !== JSON.stringify(r.enum ?? [])) {
            issues.push(`${label}: type, unit or enum changed`);
            continue;
        }
        bindings.push(b);
    }
    return { bindings, issues };
}
export function sameSeries(e, b) { return e.source_id === b.src.id && e.schema_id === b.sc.id && e.schema_version === b.sc.version; }
export function eventAt(c, b) { for (let i = Math.min(c.cursor, c.events.length - 1); i >= 0; i--)
    if (sameSeries(c.events[i], b))
        return c.events[i]; }
export function statusAt(c, b, e) { if (!e)
    return 'No observations on this page'; if (e.values[b.f.name] === null)
    return 'Unavailable (null)'; if (c.recorded)
    return 'Recorded observation'; if (!c.online || c.sourceStates[b.src.id] !== 'receiving' || Date.now() - Number(BigInt(e.received_ns) / 1000000n) > b.src.stale_ms)
    return 'Stale / disconnected'; return c.follow ? 'Receiving' : 'Paused view'; }
export function formatValue(v, decimals = -1) { if (v === null || v === undefined)
    return 'Unavailable'; if (typeof v === 'number')
    return decimals < 0 ? Number(v.toPrecision(7)).toString() : v.toFixed(decimals); return String(v); }
export function isGap(previous, next, b) { return !previous || next.gap_before || previous.boot_id !== next.boot_id || BigInt(next.received_ns) < BigInt(previous.received_ns) || BigInt(next.received_ns) - BigInt(previous.received_ns) > BigInt(b.src.stale_ms) * 1000000n; }
export function compareValues(a, b, typeA, typeB) {
    if (a === null || a === undefined)
        return b === null || b === undefined ? 0 : 1;
    if (b === null || b === undefined)
        return -1;
    if (['int64', 'uint64'].includes(typeA) && ['int64', 'uint64'].includes(typeB)) {
        const x = BigInt(String(a)), y = BigInt(String(b));
        return x < y ? -1 : x > y ? 1 : 0;
    }
    if (typeA === 'float64' && typeB === 'float64')
        return Number(a) - Number(b);
    return String(a).localeCompare(String(b), undefined, { numeric: false });
}
export function extent(events) { if (!events.length)
    return [0n, 1n]; let a = BigInt(events[0].received_ns), b = a; for (const e of events) {
    const t = BigInt(e.received_ns);
    if (t < a)
        a = t;
    if (t > b)
        b = t;
} return [a, b > a ? b : a + 1n]; }
export function boundedWindow(a, b, full) { let span = b - a; if (span < 1n)
    span = 1n; const total = full[1] - full[0]; if (span >= total)
    return [...full]; if (a < full[0])
    a = full[0]; if (a + span > full[1])
    a = full[1] - span; return [a, a + span]; }
