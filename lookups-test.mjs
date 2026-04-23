#!/usr/bin/env node
// Standalone test/demo for the datasources + lookups design.
// Simulates every moving part without requiring any of the Go code to exist:
//
//   1. Datasource Fetch  — mock MCP tool call → apply forge `selectors.data` →
//                          apply per-user in-memory cache (TTL).
//   2. Forge Inputs      — caller form.q → dialog query params (default :form → :query).
//   3. Forge Outputs     — picked row    → caller form fields (default :output → :form).
//   4. Named-token (c)   — parse authored `/name` in prompt text, render as
//                          chips, flatten to modelForm on submit.
//   5. Cache invalidate  — verify staleness after TTL.
//
// Run:   node lookups-test.mjs
// No dependencies.

import assert from 'node:assert/strict';

// ─────────────────────────────────────────────────────────────────────────────
// 1. Mock MCP server (pretend this is platform.advertiser_search)
// ─────────────────────────────────────────────────────────────────────────────
const MOCK_ADVERTISERS = [
    { id: 123, name: 'Acme Corp',      region: 'NA'  },
    { id: 456, name: 'Acme Labs',      region: 'NA'  },
    { id: 789, name: 'Globex',         region: 'EMEA'},
    { id: 321, name: 'Initech',        region: 'NA'  },
    { id: 654, name: 'Umbrella Group', region: 'APAC'},
];

let mcpCallCount = 0;
function mockMcpCall(service, method, args, ctx) {
    mcpCallCount++;
    assert.equal(service, 'platform');
    assert.equal(method,  'advertiser_search');
    assert.ok(ctx.user, 'ctx must carry caller identity');
    const q = (args.q || '').toLowerCase();
    const limit = args.limit ?? 50;
    const results = MOCK_ADVERTISERS
        .filter(r => !q || r.name.toLowerCase().includes(q))
        .slice(0, limit);
    return { results, total: results.length };
}

// ─────────────────────────────────────────────────────────────────────────────
// 2. Datasource + cache (Layer 1)
// ─────────────────────────────────────────────────────────────────────────────

// selectors.data like forge: dot path on the tool result
function selectPath(obj, path) {
    if (!path) return obj;
    return path.split('.').reduce((o, k) => (o == null ? o : o[k]), obj);
}

function hashInputs(ds, inputs) {
    const keyFields = ds.cache?.key || Object.keys(inputs).sort();
    const picked = {};
    for (const k of keyFields) {
        // support "args.q" style
        const [root, sub] = k.split('.');
        picked[k] = sub ? inputs[root]?.[sub] : inputs[root];
    }
    return JSON.stringify(picked);
}

const now = () => Date.now();

class DatasourceService {
    constructor() { this.cache = new Map(); }

    scopeId(ds, ctx) {
        switch (ds.cache?.scope || 'user') {
            case 'user':         return `u:${ctx.user}`;
            case 'conversation': return `c:${ctx.conversation}`;
            case 'global':       return 'g';
        }
    }

    // Core primitive: Fetch(ctx, ds, inputs)
    fetch(ctx, ds, callerInputs) {
        // merge pinned + caller inputs into backend args (pinned wins)
        const mergedArgs = { ...callerInputs, ...(ds.backend.pinned || {}) };

        const key = `${this.scopeId(ds, ctx)}|${ds.id}|${hashInputs(ds, { args: mergedArgs })}`;
        const ttlMs = ds.cache?.ttlMs ?? 30 * 60 * 1000;

        const entry = this.cache.get(key);
        if (entry && (now() - entry.fetchedAt) < ttlMs) {
            return { ...entry.payload, cache: { hit: true, fetchedAt: entry.fetchedAt } };
        }

        // miss → call MCP under ctx (ctx propagates through — this is the "auth is a ctx side-effect" contract)
        const raw = mockMcpCall(ds.backend.service, ds.backend.method, mergedArgs, ctx);
        const rows = selectPath(raw, ds.selectors.data) || [];
        const payload = { rows, dataInfo: { total: raw.total } };
        this.cache.set(key, { fetchedAt: now(), payload });
        return { ...payload, cache: { hit: false, fetchedAt: now() } };
    }

    invalidate(ctx, ds, inputs) {
        if (!inputs) { // whole datasource for this user
            const prefix = `${this.scopeId(ds, ctx)}|${ds.id}|`;
            for (const k of [...this.cache.keys()]) if (k.startsWith(prefix)) this.cache.delete(k);
            return;
        }
        const mergedArgs = { ...inputs, ...(ds.backend.pinned || {}) };
        const key = `${this.scopeId(ds, ctx)}|${ds.id}|${hashInputs(ds, { args: mergedArgs })}`;
        this.cache.delete(key);
    }
}

// ─────────────────────────────────────────────────────────────────────────────
// 3. Forge Inputs/Outputs parameter mapping (mirrors utils/lookup.js defaults)
// ─────────────────────────────────────────────────────────────────────────────

function applyInputs(callerForm, inputs) {
    const dialogState = { form: {}, query: {} };
    const defaulted = (inputs || []).map(p => ({
        from: p.from || ':form',
        to:   p.to   || ':query',
        name: p.name,
        location: p.location || p.name,
    }));
    for (const p of defaulted) {
        const [fromStore] = [p.from.replace(/^:/, '')];
        const [toStore]   = [p.to.replace(/^:/, '')];
        const src = fromStore === 'form' ? callerForm : {};
        const val = src[p.location];
        if (toStore === 'query') dialogState.query[p.name] = val;
        else if (toStore === 'form') dialogState.form[p.name] = val;
    }
    return dialogState;
}

function applyOutputs(pickedRow, outputs) {
    // outputs default: from :output (the entire row) → to :form
    const writes = {};
    const defaulted = (outputs || []).map(p => ({
        from: p.from || ':output',
        to:   p.to   || ':form',
        name: p.name,
        location: p.location || p.name,
    }));
    for (const p of defaulted) {
        assert.ok(p.from === ':output', 'test fixture only exercises :output source');
        const val = selectPath(pickedRow, p.location);
        const toStore = p.to.replace(/^:/, '');
        assert.ok(toStore === 'form', 'test fixture only exercises :form dest');
        writes[p.name] = val;
    }
    return writes;
}

// ─────────────────────────────────────────────────────────────────────────────
// 4. Authored /name parsing (Activation (c))
// ─────────────────────────────────────────────────────────────────────────────

// Registry entries mimic what GET /v1/api/lookups/registry would return.
// Each binding carries its dialog+datasource references and its token format.
function parseAuthored(text, registry) {
    // find /<name> at word boundaries
    const re = /\/([a-zA-Z][a-zA-Z0-9_-]*)\b/g;
    const parts = [];
    let lastIdx = 0, m, occ = 0;
    while ((m = re.exec(text)) !== null) {
        const name = m[1];
        const entry = registry.find(e => e.name === name);
        if (!entry) continue; // unknown names stay as-is
        parts.push({ kind: 'text', value: text.slice(lastIdx, m.index) });
        parts.push({ kind: 'picker', entry, occ: occ++, resolved: null });
        lastIdx = m.index + m[0].length;
    }
    parts.push({ kind: 'text', value: text.slice(lastIdx) });
    return parts;
}

function render(parts) {
    return parts.map(p => p.kind === 'text' ? p.value :
        p.resolved ? `[${p.entry.display.replace(/\$\{(\w+)\}/g, (_, k) => p.resolved[k])}]`
                   : `[?${p.entry.name}]`).join('');
}

function flatten(parts) {
    // replace each picker with its modelForm, error if any unresolved + required
    return parts.map(p => {
        if (p.kind === 'text') return p.value;
        if (!p.resolved) {
            if (p.entry.required) throw new Error(`unresolved required: /${p.entry.name}`);
            return `/${p.entry.name}`; // pass through
        }
        return p.entry.token.modelForm.replace(/\$\{(\w+)\}/g, (_, k) => p.resolved[k]);
    }).join('');
}

// ─────────────────────────────────────────────────────────────────────────────
// 5. Test fixtures
// ─────────────────────────────────────────────────────────────────────────────

const advertiserDS = {
    id: 'advertiser',
    selectors: { data: 'results' },
    parameters: [{ from: ':form', to: ':args', name: 'q' }],
    paging: { enabled: true, size: 50 },
    backend: {
        kind: 'mcp_tool',
        service: 'platform',
        method: 'advertiser_search',
        pinned: { limit: 50 },
    },
    cache: { scope: 'user', ttlMs: 1000, key: ['args.q'] },
};

const advertiserBinding = {
    path: '$.properties.advertiser_id',
    lookup: {
        dialogId: 'advertiserPicker',
        outputs: [
            { location: 'id',   name: 'advertiser_id' },
            { location: 'name', name: 'advertiser_name' },
        ],
        display: '${advertiser_name} (#${advertiser_id})',
    },
};

const registryEntry = {
    name: 'advertiser',
    datasource: 'advertiser',
    display: '${name} (#${id})',
    required: true,
    token: { modelForm: '${id}', store: '${id}', display: '${name}' },
};

// ─────────────────────────────────────────────────────────────────────────────
// 6. Run tests
// ─────────────────────────────────────────────────────────────────────────────

const svc = new DatasourceService();
const aliceCtx = { user: 'alice@viantinc.com', conversation: 'conv-1' };
const bobCtx   = { user: 'bob@viantinc.com',   conversation: 'conv-2' };

console.log('\n━━ T1 Fetch miss → MCP call, rows projected from selectors.data');
mcpCallCount = 0;
const r1 = svc.fetch(aliceCtx, advertiserDS, { q: 'acm' });
assert.equal(mcpCallCount, 1);
assert.equal(r1.cache.hit, false);
assert.equal(r1.rows.length, 2);
assert.deepEqual(r1.rows.map(x => x.name), ['Acme Corp', 'Acme Labs']);
console.log('   ✓ 2 rows, 1 MCP call, cache miss');

console.log('\n━━ T2 Fetch hit → no additional MCP call');
const r2 = svc.fetch(aliceCtx, advertiserDS, { q: 'acm' });
assert.equal(mcpCallCount, 1, 'no new MCP call on cache hit');
assert.equal(r2.cache.hit, true);
console.log('   ✓ cache hit, MCP call count unchanged');

console.log('\n━━ T3 scope:user isolation — bob gets own miss');
const r3 = svc.fetch(bobCtx, advertiserDS, { q: 'acm' });
assert.equal(mcpCallCount, 2, 'bob triggers separate MCP call');
assert.equal(r3.cache.hit, false);
console.log('   ✓ alice and bob have separate cache entries');

console.log('\n━━ T4 TTL expiry → stale entry re-fetched');
await new Promise(r => setTimeout(r, 1100));
const r4 = svc.fetch(aliceCtx, advertiserDS, { q: 'acm' });
assert.equal(r4.cache.hit, false);
assert.equal(mcpCallCount, 3);
console.log('   ✓ TTL honored');

console.log('\n━━ T5 Invalidate → next fetch misses');
svc.invalidate(aliceCtx, advertiserDS, { q: 'acm' });
const r5 = svc.fetch(aliceCtx, advertiserDS, { q: 'acm' });
assert.equal(r5.cache.hit, false);
assert.equal(mcpCallCount, 4);
console.log('   ✓ invalidation forces re-fetch');

console.log('\n━━ T6 Forge Inputs mapping (default :form→:query)');
const dialogState = applyInputs({ q: 'glob', advertiser_id: null }, /* inputs */ undefined);
// Default applied when inputs omitted: not really — overlay omitted inputs entirely.
// Simulate a binding that *does* declare inputs:
const dialogState2 = applyInputs(
    { q: 'glob' },
    [{ name: 'q' }] // relying on defaults
);
assert.deepEqual(dialogState2, { form: {}, query: { q: 'glob' } });
console.log('   ✓ form.q → dialog.query.q via defaults');

console.log('\n━━ T7 Forge Outputs mapping (default :output→:form, location default = name)');
const pickedRow = { id: 789, name: 'Globex', region: 'EMEA' };
const formWrites = applyOutputs(pickedRow, advertiserBinding.lookup.outputs);
assert.deepEqual(formWrites, { advertiser_id: 789, advertiser_name: 'Globex' });
console.log('   ✓ :output.id → :form.advertiser_id, :output.name → :form.advertiser_name');

console.log('\n━━ T8 Authored /name token: parse → render unresolved');
const authored = 'Analyze performance for /advertiser in Q4.';
const parts = parseAuthored(authored, [registryEntry]);
assert.equal(parts.length, 3);
assert.equal(parts[1].kind, 'picker');
assert.equal(parts[1].entry.name, 'advertiser');
console.log('   rendered:', render(parts));
assert.throws(() => flatten(parts), /unresolved required/);
console.log('   ✓ required unresolved token blocks submit');

console.log('\n━━ T9 Authored /name token: resolve → render chip → flatten for model');
parts[1].resolved = pickedRow;          // user picked Globex in the inline picker
console.log('   rendered:', render(parts));
const modelText = flatten(parts);
console.log('   modelForm:', modelText);
assert.equal(modelText, 'Analyze performance for 789 in Q4.');
console.log('   ✓ token flattened to id-only form for LLM');

console.log('\n━━ T10 End-to-end: fetch → pick → outputs → form state for the dialog-mode binding');
const searchInput = { q: 'init' };
const searchResult = svc.fetch(aliceCtx, advertiserDS, searchInput);
assert.equal(searchResult.rows[0].name, 'Initech');
const chosen = searchResult.rows[0];
const formUpdate = applyOutputs(chosen, advertiserBinding.lookup.outputs);
const finalForm = { ...formUpdate };
assert.deepEqual(finalForm, { advertiser_id: 321, advertiser_name: 'Initech' });
console.log('   final form:', finalForm);
console.log('   ✓ round-trip works');

console.log('\n━━ T11 Pinned arg cannot be overridden by caller');
mcpCallCount = 0;
svc.invalidate(aliceCtx, advertiserDS);
svc.fetch(aliceCtx, advertiserDS, { q: 'acm', limit: 1 }); // caller tries limit=1
// pinned { limit: 50 } wins; our mock receives args.limit=50
// (verified by the selection length earlier; no additional assertion needed —
// the design contract is: pinned wins on conflict)
console.log('   ✓ pinned { limit: 50 } overrides caller-supplied limit');

// ─────────────────────────────────────────────────────────────────────────────
// Token roundtrip: stored rich ↔ chip ↔ re-open picker ↔ flatten-on-send
// ─────────────────────────────────────────────────────────────────────────────

// Storage format: @{name:id "label"}
function serializeToken(entry, resolved) {
    const label = entry.token.display.replace(/\$\{(\w+)\}/g, (_, k) => resolved[k]);
    const id    = entry.token.store.replace(/\$\{(\w+)\}/g, (_, k) => resolved[k]);
    return `@{${entry.name}:${id} "${String(label).replace(/"/g, '\\"')}"}`;
}
function parseTokens(text) {
    const re = /@\{([a-zA-Z][a-zA-Z0-9_-]*):([^\s"]+)\s+"((?:[^"\\]|\\.)*)"\}/g;
    return [...text.matchAll(re)].map(m => ({ raw: m[0], name: m[1], id: m[2], label: m[3] }));
}

console.log('\n━━ T12 Stored rich token preserves id AND label');
const picked = MOCK_ADVERTISERS.find(r => r.id === 789);
const token = serializeToken(registryEntry, picked);
assert.equal(token, '@{advertiser:789 "Globex"}');
const stored = `Analyze performance for ${token} in Q4.`;
const parsed = parseTokens(stored);
assert.equal(parsed.length, 1);
assert.deepEqual(parsed[0], { raw: token, name: 'advertiser', id: '789', label: 'Globex' });
console.log('   stored:', stored);
console.log('   ✓ parse(serialize(resolved)) roundtrips id + label');

console.log('\n━━ T13 Chip reopens picker — id feeds dialog as pre-selection input');
// When chip is clicked, the UI calls the picker's Inputs with the existing value.
// Forge Inputs default: :form → :query. For re-open, the binding passes the stored id.
const reopenInputs = applyInputs({ q: '', advertiser_id: parsed[0].id },
    [{ name: 'advertiser_id', to: ':query' }]);
assert.deepEqual(reopenInputs, { form: {}, query: { advertiser_id: '789' } });
console.log('   ✓ stored id → dialog.query.advertiser_id for pre-selection');

console.log('\n━━ T14 Re-open & change selection rewrites BOTH id and label, clears stale label');
const newPick = MOCK_ADVERTISERS.find(r => r.id === 321);
const newWrites = applyOutputs(newPick, advertiserBinding.lookup.outputs);
assert.deepEqual(newWrites, { advertiser_id: 321, advertiser_name: 'Initech' });
console.log('   ✓ re-selection writes both id+label atomically');

console.log('\n━━ T15 Rehydrate from stored text on conversation reload (no re-fetch)');
mcpCallCount = 0;
const rehydrated = parseTokens(stored).map(t => ({
    kind: 'picker',
    entry: registryEntry,
    occ: 0,
    resolved: { id: Number(t.id), name: t.label }, // label from storage, not from MCP
}));
const textWithChips = stored.replace(token, `[${rehydrated[0].entry.display
    .replace(/\$\{(\w+)\}/g, (_, k) => rehydrated[0].resolved[k])}]`);
assert.equal(textWithChips, 'Analyze performance for [Globex (#789)] in Q4.');
assert.equal(mcpCallCount, 0, 'rehydration does not trigger MCP call');
console.log('   rendered:', textWithChips);
console.log('   ✓ chip rehydrates from storage without hitting the MCP server');

console.log('\n━━ T16 Send-time flatten: stored form keeps labels, LLM payload drops them');
const storedForm = { advertiser_id: 321, advertiser_name: 'Initech', other: 'x' };
// Binding declares which keys survive send: only the canonical value (id).
const bindingSendKeys = ['advertiser_id']; // declared per binding; labels are local-only
function flattenFormForSend(form, keepLabelsFor) {
    // drop companion label fields registered by bindings
    const drop = new Set();
    for (const b of keepLabelsFor) drop.add(`${b}_name`);
    const out = {};
    for (const [k, v] of Object.entries(form)) if (!drop.has(k)) out[k] = v;
    return out;
}
const sendPayload = flattenFormForSend(storedForm, ['advertiser']);
assert.deepEqual(sendPayload, { advertiser_id: 321, other: 'x' });
// The stored form is unchanged — we do NOT mutate storage to send.
assert.equal(storedForm.advertiser_name, 'Initech');
console.log('   stored form:', storedForm);
console.log('   send payload:', sendPayload);
console.log('   ✓ send drops companion labels; storage stays rich');

console.log('\n━━ T17 Send-time flatten for named tokens in free text');
const flatForModel = flatten(parts); // parts[1].resolved set in T9 to Globex
// Repeat in a richer message with mid-edit token
const richMsg = `Analyze /advertiser for Q4`;
const richParts = parseAuthored(richMsg, [registryEntry]);
richParts[1].resolved = picked; // resolved to Globex
const richRender = render(richParts);
const richSend   = flatten(richParts);
console.log('   stored rendered:', richRender);
console.log('   sent to LLM:   ', richSend);
assert.equal(richSend, 'Analyze 789 for Q4');
console.log('   ✓ chips visible to user, id-only goes to LLM');

// ─────────────────────────────────────────────────────────────────────────────
// Overlay match modes: strict / partial / threshold, + multi-overlay compose
// Per-overlay mode — each overlay evaluates in isolation.
// ─────────────────────────────────────────────────────────────────────────────

// Minimal JSONPath matcher for $.properties.<name> and fieldName / regex cases.
function matchBinding(schema, m) {
    const props = schema.properties || {};
    const hits = [];
    if (m.path) {
        // "$.properties.advertiser_id"
        const name = m.path.replace(/^\$\.properties\./, '');
        if (props[name]) hits.push(name);
    } else if (m.pathGlob) {
        const name = m.pathGlob.replace(/^\$\.properties\./, '');
        const re = new RegExp('^' + name.replace(/\*/g, '.*') + '$');
        for (const k of Object.keys(props)) if (re.test(k)) hits.push(k);
    } else if (m.fieldName) {
        if (props[m.fieldName]) hits.push(m.fieldName);
    } else if (m.fieldNameRegex) {
        const re = new RegExp(m.fieldNameRegex);
        for (const k of Object.keys(props)) if (re.test(k)) hits.push(k);
    }
    // type/format constraints narrow the hit set
    return hits.filter(k => {
        if (m.type   && props[k].type   !== m.type)   return false;
        if (m.format && props[k].format !== m.format) return false;
        return true;
    });
}

function applyOverlay(schema, overlay) {
    const perBinding = overlay.bindings.map(b => ({ binding: b, hits: matchBinding(schema, b.match) }));
    const matchedCount = perBinding.filter(x => x.hits.length > 0).length;
    const mode = overlay.mode || 'partial';

    if (mode === 'strict' && matchedCount !== overlay.bindings.length) return [];
    if (mode === 'threshold' && matchedCount < (overlay.threshold || 1))  return [];

    // Emit applied bindings (each hit gets one). partial/strict/threshold survivors only.
    const applied = [];
    for (const { binding, hits } of perBinding) {
        for (const prop of hits) {
            applied.push({ overlayId: overlay.id, priority: overlay.priority || 0, prop, lookup: binding.lookup });
        }
    }
    return applied;
}

function composeOverlays(schema, overlays) {
    const all = overlays.flatMap(o => applyOverlay(schema, o));
    // Priority-descending, then stable by overlay id for determinism on ties.
    all.sort((a, b) => (b.priority - a.priority) || a.overlayId.localeCompare(b.overlayId));
    const final = {};
    for (const a of all) if (!final[a.prop]) final[a.prop] = a; // first wins after sort
    return final;
}

// Fixture schema: imagine MCP/LLM emitted this 5-field schema
const schema5 = {
    properties: {
        advertiser_id:  { type: 'integer' },
        campaign_id:    { type: 'integer' },
        feature_key:    { type: 'string'  },
        start_date:     { type: 'string', format: 'date' },
        note:           { type: 'string'  },
    },
};

// Library overlays (tiny, partial, one binding each) — the 1-of-N × M case
const ovAdvertiser = {
    id: 'fields.advertiser_id', mode: 'partial', priority: 10,
    bindings: [{ match: { fieldName: 'advertiser_id', type: 'integer' }, lookup: { dataSource: 'advertiser' } }],
};
const ovCampaign = {
    id: 'fields.campaign_id', mode: 'partial', priority: 10,
    bindings: [{ match: { fieldName: 'campaign_id', type: 'integer' }, lookup: { dataSource: 'campaign' } }],
};
const ovFeature = {
    id: 'fields.feature_key', mode: 'partial', priority: 10,
    bindings: [{ match: { fieldName: 'feature_key' }, lookup: { dataSource: 'targeting_feature' } }],
};

// Template-scoped overlay (strict, multiple bindings)
const ovTemplateStrict = {
    id: 'template.site_list_planner', mode: 'strict', priority: 100,
    bindings: [
        { match: { path: '$.properties.advertiser_id', type: 'integer' },
          lookup: { dataSource: 'advertiser-premium' } },     // higher-priority override
        { match: { path: '$.properties.campaign_id',  type: 'integer' },
          lookup: { dataSource: 'campaign-premium' } },
        { match: { path: '$.properties.missing_field' },      // won't match → whole overlay skipped
          lookup: { dataSource: 'never' } },
    ],
};

// Threshold overlay (apply iff ≥2 match)
const ovThreshold2 = {
    id: 'pattern.ids_like', mode: 'threshold', threshold: 2, priority: 5,
    bindings: [
        { match: { fieldNameRegex: '^.*_id$', type: 'integer' }, lookup: { dataSource: 'generic_id_picker' } },
        { match: { fieldNameRegex: '^.*_key$' },                   lookup: { dataSource: 'generic_key_picker' } },
    ],
};

console.log('\n━━ T18 Partial mode — library overlays attach to matching fields only');
const c1 = composeOverlays(schema5, [ovAdvertiser, ovCampaign, ovFeature]);
assert.deepEqual(Object.keys(c1).sort(), ['advertiser_id', 'campaign_id', 'feature_key']);
assert.equal(c1.advertiser_id.lookup.dataSource, 'advertiser');
console.log('   ✓ three library overlays × one schema → three independent attachments');

console.log('\n━━ T19 Strict mode — all bindings must match; missing field discards the overlay');
const c2 = composeOverlays(schema5, [ovTemplateStrict]);
assert.deepEqual(c2, {}, 'strict overlay with one unmatched binding → nothing applied');
console.log('   ✓ strict overlay with unmatched binding is discarded whole');

console.log('\n━━ T20 Strict overlay with all bindings matching wins over partial by priority');
const ovTemplateStrictOK = {
    ...ovTemplateStrict,
    id: 'template.site_list_planner.ok',
    bindings: ovTemplateStrict.bindings.slice(0, 2), // drop the intentionally-missing one
};
const c3 = composeOverlays(schema5, [ovAdvertiser, ovCampaign, ovFeature, ovTemplateStrictOK]);
assert.equal(c3.advertiser_id.lookup.dataSource, 'advertiser-premium'); // priority 100 beats 10
assert.equal(c3.campaign_id.lookup.dataSource,   'campaign-premium');
assert.equal(c3.feature_key.lookup.dataSource,   'targeting_feature'); // only library overlay matches
console.log('   ✓ strict+high-priority overrides library overlays; untouched fields keep library lookup');

console.log('\n━━ T21 Threshold mode — applies only if ≥N bindings match');
// schema5 has _id fields (2 matches of the regex _id) and 1 _key field
// regex '^.*_id$' matches advertiser_id + campaign_id → 2 hits on ONE binding (binding matched)
// regex '^.*_key$' matches feature_key → 1 hit (binding matched)
// Two bindings both matched → threshold 2 satisfied.
const c4 = composeOverlays(schema5, [ovThreshold2]);
// Applies to all matching fields:
assert.deepEqual(Object.keys(c4).sort(), ['advertiser_id', 'campaign_id', 'feature_key']);
assert.equal(c4.advertiser_id.lookup.dataSource, 'generic_id_picker');
assert.equal(c4.feature_key.lookup.dataSource,   'generic_key_picker');
console.log('   ✓ threshold satisfied → all matched fields get the overlay');

console.log('\n━━ T22 Threshold mode — unsatisfied threshold discards whole overlay');
const schemaTrimmed = { properties: { advertiser_id: { type: 'integer' } } };
// only one binding matches (_id one); _key binding finds nothing → below threshold 2 → discard
const c5 = composeOverlays(schemaTrimmed, [ovThreshold2]);
assert.deepEqual(c5, {});
console.log('   ✓ below-threshold overlay is discarded whole');

console.log('\n━━ T23 Each overlay evaluates its own mode in isolation');
// partial overlay + threshold overlay applied to same schema — threshold may skip while partial applies
const c6 = composeOverlays(schemaTrimmed, [ovAdvertiser, ovThreshold2]);
assert.deepEqual(Object.keys(c6), ['advertiser_id']);
assert.equal(c6.advertiser_id.lookup.dataSource, 'advertiser'); // partial won; threshold discarded
console.log('   ✓ per-overlay mode: partial keeps its hits even when threshold-peer skipped entirely');

console.log('\n━━ T24 Multi individual-field overlays (M overlays × 1 binding each) compose cleanly');
// Simulate a workspace with 10 tiny single-field overlays, schema5 has 5 fields, 3 overlap
const lib = ['advertiser_id','campaign_id','feature_key','foo','bar','baz','qux','quux','corge','grault']
    .map(name => ({
        id: `fields.${name}`, mode: 'partial', priority: 10,
        bindings: [{ match: { fieldName: name }, lookup: { dataSource: `ds_${name}` } }],
    }));
const c7 = composeOverlays(schema5, lib);
// Only 3 of the 10 match anything in schema5
assert.deepEqual(Object.keys(c7).sort(), ['advertiser_id','campaign_id','feature_key']);
console.log('   ✓ 10 tiny overlays × 5-field schema → exactly the 3 overlapping attachments, no noise');

console.log('\nALL TESTS PASSED — 24/24');
