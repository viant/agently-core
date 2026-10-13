import { expect, it, vi } from 'vitest';
import { browserMCPSetupMessage, BrowserMCPSetupError, BrowserMCPHost, ChromeExtensionMCPConnection, type BrowserMCPCatalog, type BrowserMCPConnection, type BrowserMCPDescriptor } from './browserMCP';
const descriptor: BrowserMCPDescriptor = { name: 'device', executionLocation: 'browser', transport: { type: 'chrome-extension', extensionId: 'a'.repeat(32), portName: 'fixture-v1' }, allowedTools: ['safe_*'] };
it('reports a closed setup refusal without dispatch or private error details', async () => {
    let receive!: (message: any) => void;
    const port = { postMessage: vi.fn(), disconnect: vi.fn(), onMessage: { addListener: (fn: (message: any) => void) => { receive = fn; } }, onDisconnect: { addListener: vi.fn() } };
    const connection = new ChromeExtensionMCPConnection(descriptor, undefined, { connect: () => port });
    const pending = connection.request('initialize');
    receive({ type: 'setupRefused', code: 'senderMetadataUnavailable', detail: 'PRIVATE_CANARY' });
    const error = await pending.catch(error => error);
    expect(browserMCPSetupMessage(error)).toContain('page identity');
    expect(browserMCPSetupMessage(error)).not.toContain('PRIVATE_CANARY');
    expect(port.postMessage).not.toHaveBeenCalled();
    expect(port.disconnect).toHaveBeenCalledOnce();
    expect(browserMCPSetupMessage(new Error('PRIVATE_CANARY'))).not.toContain('PRIVATE_CANARY');
    expect(browserMCPSetupMessage({ code: 'senderMetadataUnavailable', message: 'PRIVATE_CANARY' })).toContain('Check the local browser connection');
});
it('distinguishes an unavailable Chrome runtime before connection setup', () => {
    try { new ChromeExtensionMCPConnection(descriptor, undefined, {} as any); throw Error('expected refusal'); }
    catch (error) { expect(browserMCPSetupMessage(error)).toContain('Open the page in Chrome'); }
});
it('identifies workspace discovery failure without relaying server error contents', async () => {
    const host = new BrowserMCPHost({ descriptors: async () => { throw Error('PRIVATE_SERVER_CANARY'); }, register: vi.fn(), revoke: vi.fn(), current: vi.fn() });
    const error = await host.tools('conversation', 'thread').catch(error => error);
    expect(browserMCPSetupMessage(error)).toContain('workspace');
    expect(browserMCPSetupMessage(error)).not.toContain('PRIVATE_SERVER_CANARY');
});
it('identifies initialization failure and closes the original connection without retry', async () => {
    const request = vi.fn(async () => { throw Error('PRIVATE_NATIVE_CANARY'); }), close = vi.fn();
    const host = new BrowserMCPHost({ descriptors: async () => [descriptor], register: vi.fn(), revoke: vi.fn(), current: vi.fn() }, { transportFactory: () => ({ request, close }) });
    const error = await host.tools('conversation', 'thread').catch(error => error);
    expect(browserMCPSetupMessage(error)).toContain('MCP initialization');
    expect(browserMCPSetupMessage(error)).not.toContain('PRIVATE_NATIVE_CANARY');
    expect(request).toHaveBeenCalledOnce();
    expect(close).toHaveBeenCalledOnce();
});
it('bounds approval waiting without dispatching or replaying an MCP request', async () => {
    vi.useFakeTimers();
    try {
        const port = { postMessage: vi.fn(), disconnect: vi.fn(), onMessage: { addListener: vi.fn() }, onDisconnect: { addListener: vi.fn() } };
        const connection = new ChromeExtensionMCPConnection({ ...descriptor, transport: { ...descriptor.transport, requestTimeoutMs: 100 } }, undefined, { connect: () => port });
        const pending = expect(connection.request('initialize')).rejects.toThrow('did not respond to the connection request');
        await vi.advanceTimersByTimeAsync(100);
        await pending;
        expect(port.postMessage).not.toHaveBeenCalled();
        expect(port.disconnect).toHaveBeenCalledOnce();
        await expect(connection.request('initialize')).rejects.toThrow('unavailable');
    } finally { vi.useRealTimers(); }
});
it('keeps human approval visible beyond the action deadline without dispatching until approved', async () => {
    vi.useFakeTimers();
    try {
        let receive!: (message: any) => void;
        const dismiss = vi.fn();
        const port = { postMessage: vi.fn(), disconnect: vi.fn(), onMessage: { addListener: (fn: (message: any) => void) => { receive = fn; } }, onDisconnect: { addListener: vi.fn() } };
        const connection = new ChromeExtensionMCPConnection({ ...descriptor, transport: { ...descriptor.transport, requestTimeoutMs: 100 } }, () => dismiss, { connect: () => port });
        const pending = connection.request('initialize');
        receive({ type: 'approvalRequired' });
        await vi.advanceTimersByTimeAsync(5000);
        expect(port.postMessage).not.toHaveBeenCalled();
        expect(port.disconnect).not.toHaveBeenCalled();
        expect(dismiss).not.toHaveBeenCalled();
        receive({ type: 'approved' });
        await vi.advanceTimersByTimeAsync(0);
        expect(port.postMessage).toHaveBeenCalledOnce();
        expect(dismiss).toHaveBeenCalledOnce();
        const request = port.postMessage.mock.calls[0][0];
        receive({ type: 'result', requestId: request.requestId, result: { protocolVersion: '2025-11-25' } });
        await expect(pending).resolves.toEqual({ protocolVersion: '2025-11-25' });
        connection.close(); expect(dismiss).toHaveBeenCalledOnce();
    } finally { vi.useRealTimers(); }
});
it('repeated approval notifications cannot extend the bounded human wait', async () => {
    vi.useFakeTimers();
    try {
        let receive!: (message: any) => void;
        const dismiss = vi.fn(), show = vi.fn(() => dismiss);
        const port = { postMessage: vi.fn(), disconnect: vi.fn(), onMessage: { addListener: (fn: (message: any) => void) => { receive = fn; } }, onDisconnect: { addListener: vi.fn() } };
        const connection = new ChromeExtensionMCPConnection(descriptor, show, { connect: () => port });
        const pending = expect(connection.request('initialize')).rejects.toThrow('approval deadline exceeded');
        receive({ type: 'approvalRequired' });
        await vi.advanceTimersByTimeAsync(119000);
        receive({ type: 'approvalRequired' });
        await vi.advanceTimersByTimeAsync(1000); await pending;
        expect(show).toHaveBeenCalledOnce(); expect(dismiss).toHaveBeenCalledOnce();
        expect(port.postMessage).not.toHaveBeenCalled(); expect(port.disconnect).toHaveBeenCalledOnce();
    } finally { vi.useRealTimers(); }
});
it('ignores a delayed approval notification after connection approval', async () => {
    vi.useFakeTimers();
    try {
        let receive!: (message: any) => void;
        const show = vi.fn();
        const port = { postMessage: vi.fn(), disconnect: vi.fn(), onMessage: { addListener: (fn: (message: any) => void) => { receive = fn; } }, onDisconnect: { addListener: vi.fn() } };
        const connection = new ChromeExtensionMCPConnection(descriptor, show, { connect: () => port });
        receive({ type: 'approved' });
        receive({ type: 'approvalRequired' });
        await vi.advanceTimersByTimeAsync(120001);
        expect(show).not.toHaveBeenCalled();
        expect(port.disconnect).not.toHaveBeenCalled();
        const pending = connection.request('ping');
        await vi.advanceTimersByTimeAsync(0);
        const request = port.postMessage.mock.calls[0][0];
        receive({ type: 'result', requestId: request.requestId, result: {} });
        await expect(pending).resolves.toEqual({});
        connection.close();
    } finally { vi.useRealTimers(); }
});
it('distinguishes an unapproved page disconnect from an approved MCP response deadline without replay', async () => {
    vi.useFakeTimers();
    try {
        for (const approved of [false, true]) {
            let receive!: (message: any) => void, disconnect!: () => void;
            const port = { postMessage: vi.fn(), disconnect: vi.fn(), onMessage: { addListener: (fn: (message: any) => void) => { receive = fn; } }, onDisconnect: { addListener: (fn: () => void) => { disconnect = fn; } } };
            const connection = new ChromeExtensionMCPConnection({ ...descriptor, transport: { ...descriptor.transport, requestTimeoutMs: 100 } }, undefined, { connect: () => port, lastError: { message: 'PRIVATE_NATIVE_CANARY' } });
            const result = connection.request('initialize').catch(error => error);
            if (approved) { receive({ type: 'approved' }); await vi.advanceTimersByTimeAsync(100); }
            else { receive({ type: 'approvalRequired' }); disconnect(); }
            const message = browserMCPSetupMessage(await result);
            expect(message).toContain(approved ? 'did not answer the MCP request' : 'closed before page approval');
            expect(message).not.toContain('PRIVATE_NATIVE_CANARY');
            expect(port.postMessage).toHaveBeenCalledTimes(approved ? 1 : 0);
            connection.close();
        }
    } finally { vi.useRealTimers(); }
});

function fixture(app = false, mediaResult?: any) {
    const catalogs = new Map<string, BrowserMCPCatalog>(); const connections: BrowserMCPConnection[] = []; const requests: any[] = [];
    const api = { descriptors: vi.fn(async () => [descriptor]), register: vi.fn(async (input: any) => {
        const id = crypto.randomUUID();
        const catalog: BrowserMCPCatalog = { ...input, id, hash: id, expiresAt: new Date(Date.now() + 60000).toISOString(), tools: input.tools.map((tool: any) => ({ name: `device-${tool.name}`, description: tool.description, parameters: tool.inputSchema, metadata: { ...(tool._meta?.ui ? { ui: tool._meta.ui } : {}), browserMCP: { catalogId: id, connectionId: input.connectionId, server: input.server, tool: tool.name } } })) };
        catalogs.set(id, catalog); return catalog;
    }), revoke: vi.fn(async (id: string) => { catalogs.delete(id); }), current: vi.fn(async (id: string) => { const catalog = catalogs.get(id); if (!catalog) throw Error('revoked'); return catalog; }) };
    let fail = false;
    const host = new BrowserMCPHost(api, { transportFactory: (_config, binding) => {
        const connection: BrowserMCPConnection = { request: vi.fn(async (method, params) => {
            requests.push({ ...binding, method, params });
            if (method === 'initialize') {
                if (!params || Object.keys(params).sort().join() !== 'capabilities,clientInfo,protocolVersion' ||
                    typeof params.protocolVersion !== 'string' || !/^\d{4}-\d{2}-\d{2}$/.test(params.protocolVersion) ||
                    !params.capabilities || !params.clientInfo || typeof (params.clientInfo as any).name !== 'string' || typeof (params.clientInfo as any).version !== 'string') throw Error('Incomplete MCP initialization');
                return { protocolVersion: '2025-11-25', ...(app ? { capabilities: { resources: {} } } : {}) };
            }
            if (method === 'tools/list') return { tools: [{ name: 'safe_read', description: 'Read fixture', inputSchema: { type: 'object' }, ...(app ? { _meta: { ui: { resourceUri: 'ui://device/fixture' } } } : {}) }, { name: 'forbidden', inputSchema: { type: 'object' } }] };
            if (fail) throw Error('lost');
            if (method === 'resources/read') return { contents: [{ uri: params?.uri, mimeType: 'text/html;profile=mcp-app', text: '<p>Generic fixture</p>' }] };
            return mediaResult ?? { content: [{ type: 'text', text: 'ok' }], structuredContent: { ok: true }, _meta: { secret: 'not-model-content' } };
        }), close: vi.fn(() => connection.onclose?.()) };
        connections.push(connection); return connection;
    } });
    return { host, api, requests, connections, fail: () => { fail = true; } };
}
it('discovers and registers once, binds calls/results to original conversation without private MCP meta', async () => {
    const f = fixture(); const [tools, same] = await Promise.all([f.host.tools('conversation', 'wire'), f.host.tools('conversation', 'wire')]);
    expect(f.connections).toHaveLength(1); expect(same).toHaveLength(1); expect(f.api.register.mock.calls[0][0].tools).toHaveLength(1);
    const call = { id: 'call1', type: 'function' as const, function: { name: tools[0].tool.name, arguments: '{"id":7}' } };
    const result = await tools[0].execute({ id: 7 }, call); expect(JSON.stringify(result)).not.toContain('not-model-content');
    expect(await tools[0].execute({ id: 7 }, call)).toEqual(result); expect(f.requests.filter(r => r.method === 'tools/call')).toHaveLength(1);
    await expect(tools[0].execute({ id: 8 }, { ...call, function: { ...call.function, arguments: '{"id":8}' } })).rejects.toThrow(/conflicting/);
    await f.host.closeConversation('conversation'); await expect(tools[0].execute({ id: 7 }, call)).rejects.toThrow(/unavailable/);
    expect(f.api.revoke).toHaveBeenCalledOnce();
});
it('negotiates standard MCP initialization before requesting the browser catalog', async () => {
    const f = fixture();
    await f.host.tools('conversation', 'wire');
    expect(f.requests.slice(0, 2).map(request => request.method)).toEqual(['initialize', 'tools/list']);
    expect(f.requests[0].params).toEqual({
        protocolVersion: '2025-11-25',
        clientInfo: { name: 'agently-core-ui-sdk', version: '0.1.0' },
        capabilities: { extensions: { 'io.modelcontextprotocol/ui': { mimeTypes: ['text/html;profile=mcp-app'] } } },
    });
});
it('does not replay unknown calls and invalidates clients on account reset', async () => {
    const f = fixture(); const tools = await f.host.tools('conversation', 'wire'); f.fail();
    const call = { id: 'lost', type: 'function' as const, function: { name: tools[0].tool.name, arguments: '{}' } };
    const failure = await tools[0].execute({}, call);
    expect(JSON.parse(failure.content as string)).toEqual({ code: 'browserTransportUnavailable', dispatchState: 'unknown', outcome: 'unknown' });
    expect(await tools[0].execute({}, call)).toEqual(failure);
    expect(failure.metadata).toEqual(tools[0].tool.metadata);
    expect(f.requests.filter(r => r.method === 'tools/call')).toHaveLength(1);
    f.host.reset(); await expect(tools[0].execute({}, { ...call, id: 'new' })).rejects.toThrow(/unavailable/);
});

it('isolates browser app result/resources by original call, conversation and catalog without guest execution bypass', async () => {
    const f = fixture(true); const notify = vi.fn(); const stop = f.host.subscribeApps(notify);
    const tools = await f.host.tools('conversation', 'wire');
    const call = { id: 'app-call', type: 'function' as const, function: { name: tools[0].tool.name, arguments: '{}' } };
    const answer = await tools[0].execute({}, call);
    expect(JSON.stringify(answer)).not.toContain('not-model-content');
    const [activity] = f.host.appActivities('conversation');
    expect(activity.content.result._meta.secret).toBe('not-model-content');
    expect(f.host.appActivities('other')).toEqual([]);
    const proxy = f.host.appProxy(activity);
    expect(proxy.serverTools).toBe(false);
    expect((await proxy.call('resources/read', { uri: 'ui://device/fixture' })).contents[0].text).toContain('Generic fixture');
    await expect(proxy.call('resources/read', { uri: 'ui://other/fixture' })).rejects.toThrow('binding');
    await expect(proxy.call('tools/call', { name: 'safe_read' })).rejects.toThrow('binding');
    expect(() => f.host.appProxy({ ...activity, content: { ...activity.content, serverId: 'other' } })).toThrow('instance');
    await f.host.closeConversation('conversation');
    await expect(proxy.call('resources/read', { uri: 'ui://device/fixture' })).rejects.toThrow('unavailable');
    expect(f.host.appActivities('conversation')).toEqual([]); expect(notify).toHaveBeenCalled(); stop();
});

it('preserves screenshot parts alongside structured metadata without private MCP annotations', async () => {
    const f = fixture(false, { structuredContent: { captureRef: 'owned-frame', widthPixels: 800 }, content: [
        { type: 'text', text: 'duplicate metadata' },
        { type: 'image', mimeType: 'image/png', data: 'AQID', _meta: { secret: 'private-block-meta' }, annotations: { internal: 'private-annotation' } },
    ], _meta: { secret: 'private-result-meta' } });
    const tools = await f.host.tools('conversation', 'wire');
    const call = { id: 'screenshot', type: 'function' as const, function: { name: tools[0].tool.name, arguments: '{}' } };
    const answer = await tools[0].execute({}, call);
    expect(answer.content).toEqual([
        { type: 'text', text: JSON.stringify({ captureRef: 'owned-frame', widthPixels: 800 }) },
        { type: 'image', source: { type: 'data', value: 'AQID', mimeType: 'image/png' } },
    ]);
    expect(JSON.stringify(answer)).not.toContain('private-');
    expect(await tools[0].execute({}, call)).toEqual(answer);
    expect(f.requests.filter(r => r.method === 'tools/call')).toHaveLength(1);
});

it('rejects incomplete results rather than projecting them as completed artifacts', async () => {
    const f = fixture(false, { resultType: 'input_required', content: [] });
    const tools = await f.host.tools('conversation', 'wire');
    const call = { id: 'incomplete-result', type: 'function' as const, function: { name: tools[0].tool.name, arguments: '{}' } };
    const answer = await tools[0].execute({}, call);
    expect(JSON.parse(answer.content as string)).toEqual({ code: 'resultTransferInvalid', dispatchState: 'unknown', outcome: 'unknown' });
    expect(await tools[0].execute({}, call)).toEqual(answer);
    expect(f.requests.filter(r => r.method === 'tools/call')).toHaveLength(1);
});

it('returns one closed error for malformed screenshot data without replay', async () => {
    const f = fixture(false, { content: [{ type: 'image', mimeType: 'image/png', data: 'not base64' }] });
    const tools = await f.host.tools('conversation', 'wire');
    const call = { id: 'bad-image', type: 'function' as const, function: { name: tools[0].tool.name, arguments: '{}' } };
    const answer = await tools[0].execute({}, call);
    expect(JSON.parse(answer.content as string)).toEqual({ code: 'resultTransferInvalid', dispatchState: 'unknown', outcome: 'unknown' });
    expect(await tools[0].execute({}, call)).toEqual(answer);
    expect(f.requests.filter(r => r.method === 'tools/call')).toHaveLength(1);
});

it('relays a valid PNG above the old text limit as an intact image part', async () => {
    // Add a valid ancillary tEXt chunk to a real 1x1 PNG. This keeps the fixture
    // deterministic while exercising a binary picture above 192 KiB.
    const png = Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+aL1sAAAAASUVORK5CYII=', 'base64');
    const payload = Buffer.from('note\0' + 'x'.repeat(300000));
    const typeAndPayload = Buffer.concat([Buffer.from('tEXt'), payload]);
    let crc = 0xffffffff;
    for (const byte of typeAndPayload) {
        crc ^= byte;
        for (let bit = 0; bit < 8; bit++) crc = (crc >>> 1) ^ ((crc & 1) ? 0xedb88320 : 0);
    }
    const length = Buffer.alloc(4), checksum = Buffer.alloc(4);
    length.writeUInt32BE(payload.length); checksum.writeUInt32BE((crc ^ 0xffffffff) >>> 0);
    const data = Buffer.concat([png.subarray(0, -12), length, typeAndPayload, checksum, png.subarray(-12)]).toString('base64');
    const f = fixture(false, { content: [{ type: 'image', mimeType: 'image/png', data }] });
    const tools = await f.host.tools('conversation', 'wire');
    const call = { id: 'large-png', type: 'function' as const, function: { name: tools[0].tool.name, arguments: '{}' } };
    const answer = await tools[0].execute({}, call);
    expect(answer.content).toEqual([{ type: 'image', source: { type: 'data', value: data, mimeType: 'image/png' } }]);
    expect(await tools[0].execute({}, call)).toEqual(answer);
    expect(f.requests.filter(r => r.method === 'tools/call')).toHaveLength(1);
});

it('does not truncate large text before backend overflow policy', async () => {
    const text = 'public result '.repeat(24000);
    const f = fixture(false, { content: [{ type: 'text', text }] });
    const tools = await f.host.tools('conversation', 'wire');
    const call = { id: 'large-text', type: 'function' as const, function: { name: tools[0].tool.name, arguments: '{}' } };
    const answer = await tools[0].execute({}, call);
    expect(JSON.parse(answer.content as string)).toEqual([{ type: 'text', text }]);
});

it('keeps configured extraction in canonical MCP shape for backend artifact policy', async () => {
    const data='AQID';
    const f=fixture(false,{content:[{type:'image',mimeType:'image/png',data,_meta:{secret:'PRIVATE'}}],structuredContent:{files:[{bytes:data}]},_meta:{secret:'PRIVATE'}});
    f.api.descriptors.mockResolvedValue([{...descriptor,outputArtifacts:{safe_read:{bytesPath:'/structuredContent/files/0/bytes',encoding:'base64',name:'screen.png'}}}]);
    const tools=await f.host.tools('conversation','wire');
    const call={id:'artifact-output',type:'function' as const,function:{name:tools[0].tool.name,arguments:'{}'}};
    const result=await tools[0].execute({},call);
    expect(JSON.parse(result.content as string)).toEqual({content:[{type:'image',mimeType:'image/png',data}],structuredContent:{files:[{bytes:data}]}});
    expect(JSON.stringify(result)).not.toContain('PRIVATE');
    expect(await tools[0].execute({},call)).toEqual(result);
    expect(f.requests.filter(r=>r.method==='tools/call')).toHaveLength(1);
});

it('classifies disconnect by observed handshake state without raw Chrome diagnostics', async () => {
    vi.useFakeTimers();
    try {
        for (const scenario of [
            { state: 'initial', message: 'PRIVATE_RUNTIME_CANARY', code: 'connectionClosedBeforeHandshake', pending: false, sent: 0 },
            { state: 'initial', message: 'Could not establish connection. Receiving end does not exist.', code: 'extensionReceiverUnavailable', pending: false, sent: 0 },
            { state: 'initial', message: 'Could not establish connection. Receiving end does not exist. PRIVATE_RUNTIME_CANARY', code: 'connectionClosedBeforeHandshake', pending: false, sent: 0 },
            { state: 'approvalRequired', message: 'PRIVATE_RUNTIME_CANARY', code: 'connectionClosedBeforeApproval', pending: true, sent: 0 },
            { state: 'approved', message: 'Could not establish connection. Receiving end does not exist.', code: 'connectionLost', pending: false, sent: 1 },
        ]) {
            let receive!: (message: any) => void, disconnect!: () => void;
            const dismiss = vi.fn(), approval = vi.fn(() => dismiss), close = vi.fn();
            const port = { postMessage: vi.fn(), disconnect: vi.fn(), onMessage: { addListener: (fn: (message: any) => void) => { receive = fn; } }, onDisconnect: { addListener: (fn: () => void) => { disconnect = fn; } } };
            let diagnosticReads = 0;
            const runtime = { connect: vi.fn(() => port), get lastError() { diagnosticReads++; return { message: scenario.message }; } };
            const connection = new ChromeExtensionMCPConnection(descriptor, approval, runtime);
            connection.onclose = close;
            const pending = connection.request('initialize').catch(error => error);
            if (scenario.state !== 'initial') receive({ type: scenario.state });
            await vi.advanceTimersByTimeAsync(0);
            disconnect();
            const error = await pending;
            expect(error.code).toBe(scenario.code);
            const message = browserMCPSetupMessage(error);
            expect(message.includes('pending controller request')).toBe(scenario.pending);
            expect(message).not.toContain('PRIVATE_RUNTIME_CANARY');
            expect(message).not.toContain('Receiving end does not exist');
            expect(diagnosticReads).toBe(1);
            expect(approval).toHaveBeenCalledTimes(scenario.pending ? 1 : 0);
            expect(dismiss).toHaveBeenCalledTimes(scenario.pending ? 1 : 0);
            expect(port.postMessage).toHaveBeenCalledTimes(scenario.sent);
            expect(runtime.connect).toHaveBeenCalledOnce();
            connection.close();
            expect(close).toHaveBeenCalledOnce();
            await vi.advanceTimersByTimeAsync(120001);
            expect(port.postMessage).toHaveBeenCalledTimes(scenario.sent);
        }
    } finally { vi.useRealTimers(); }
});

it.each(['initialize', 'tools/list'])('keeps the failed MCP setup stage for an approved request: %s', async (failedMethod) => {
    const request = vi.fn(async (method: string) => {
        if (method === failedMethod) throw new BrowserMCPSetupError('requestUnavailable');
        return { capabilities: {} };
    });
    const close = vi.fn();
    const host = new BrowserMCPHost({ descriptors: async () => [descriptor], register: vi.fn(), revoke: vi.fn(), current: vi.fn() }, { transportFactory: () => ({ request, close }) });
    const error = await host.tools('conversation', 'thread').catch(error => error);
    expect(error.code).toBe(failedMethod === 'initialize' ? 'initializeUnavailable' : 'catalogUnavailable');
    expect(request).toHaveBeenCalledTimes(failedMethod === 'initialize' ? 1 : 2);
    expect(close).toHaveBeenCalledOnce();
});

it.each([false, true])('bounds transient setup recovery, permanentFailure=%s', async (permanentFailure) => {
    const connections: any[] = [];
    const register = vi.fn(async (input: any) => ({ ...input, id: 'catalog', hash: 'hash', expiresAt: new Date(Date.now()+60000).toISOString(), tools: [] }));
    const host = new BrowserMCPHost({ descriptors: async () => [descriptor], register, revoke: vi.fn(), current: vi.fn() }, { transportFactory: () => {
        const index = connections.length;
        const connection = { request: vi.fn(async (method: string) => {
            if (index === 0 || permanentFailure) throw new BrowserMCPSetupError('connectionLost');
            return method === 'initialize' ? { capabilities: {} } : { tools: [] };
        }), close: vi.fn() };
        connections.push(connection); return connection;
    } });
    if (permanentFailure) await expect(host.tools('conversation','thread')).rejects.toThrow('connection closed');
    else expect(await host.tools('conversation','thread')).toEqual([]);
    expect(connections).toHaveLength(2);
    expect(connections[0].close).toHaveBeenCalledOnce();
    expect(register).toHaveBeenCalledTimes(permanentFailure ? 0 : 1);
});

it.each(['reset','close'])('does not reconnect after explicit %s during setup', async (action) => {
    let reject!: (error: Error) => void;
    const request = vi.fn(() => new Promise((_resolve, fail) => { reject = fail; }));
    const factory = vi.fn(() => ({ request, close: vi.fn() }));
    const host = new BrowserMCPHost({ descriptors: async () => [descriptor], register: vi.fn(), revoke: vi.fn(), current: vi.fn() }, { transportFactory: factory });
    const pending = host.tools('conversation','thread').catch(error => error);
    await vi.waitFor(() => expect(request).toHaveBeenCalledOnce());
    if (action === 'reset') host.reset(); else await host.closeConversation('conversation');
    reject(new BrowserMCPSetupError('connectionLost'));
    expect((await pending).code).toBe('connectionLost');
    expect(factory).toHaveBeenCalledOnce();
});

it('records bounded setup timings without connection metadata or repeated discovery on reuse', async () => {
    const f = fixture();
    await f.host.tools('private-conversation','private-thread');
    for (const phase of ['setup','metadata','initialize','catalog','register']) {
        const entries = performance.getEntriesByName(`agently.browserMCP.${phase}`);
        expect(entries).toHaveLength(1);
        expect(entries[0].duration).toBeGreaterThanOrEqual(0);
        expect(JSON.stringify(entries)).not.toContain('private-');
    }
    await f.host.tools('private-conversation','private-thread');
    expect(performance.getEntriesByName('agently.browserMCP.setup')).toHaveLength(1);
    expect(f.requests.filter(item => item.method === 'initialize')).toHaveLength(1);
    expect(f.requests.filter(item => item.method === 'tools/list')).toHaveLength(1);
});

it('does not connect or register tools without a workspace browser MCP descriptor', async () => {
    const factory = vi.fn(), register = vi.fn();
    const host = new BrowserMCPHost({ descriptors: async () => [], register, revoke: vi.fn(), current: vi.fn() }, { transportFactory: factory });
    expect(await host.tools('conversation', 'thread')).toEqual([]);
    expect(factory).not.toHaveBeenCalled();
    expect(register).not.toHaveBeenCalled();
});

it('settles three original calls after one disconnect and retains only original terminal metadata', async () => {
    let receive!: (message: any) => void, disconnect!: () => void;
    let catalog!: BrowserMCPCatalog;
    const calls: any[] = [];
    const port = { postMessage: vi.fn((request: any) => {
        calls.push(request);
        if (request.operation === 'tools/call') queueMicrotask(() => disconnect());
        else queueMicrotask(() => receive({ type: 'result', requestId: request.requestId, result: request.operation === 'tools/list' ? { tools: [{ name: 'safe_read', inputSchema: { type: 'object' } }] } : {} }));
    }), disconnect: vi.fn(), onMessage: { addListener: (fn: any) => { receive = fn; } }, onDisconnect: { addListener: (fn: any) => { disconnect = fn; } } };
    const api = { descriptors: async () => [descriptor], register: vi.fn(async (input: any) => {
        catalog = { ...input, id: 'catalog', hash: 'hash', expiresAt: new Date(Date.now() + 60000).toISOString(), tools: [{ name: 'device-safe_read', description: 'Read', parameters: { type: 'object' }, metadata: { browserMCP: { catalogId: 'catalog', connectionId: input.connectionId, server: input.server, tool: 'safe_read' } } }] };
        return catalog;
    }), current: vi.fn(async () => catalog), revoke: vi.fn(async () => {}) };
    const host = new BrowserMCPHost(api, { transportFactory: () => {
        const connection = new ChromeExtensionMCPConnection(descriptor, undefined, { connect: () => port, lastError: { message: 'PRIVATE_CANARY' } });
        queueMicrotask(() => receive({ type: 'approved' })); return connection;
    } });
    const [tool] = await host.tools('conversation', 'thread');
    const original = [1, 2, 3].map(n => ({ id: 'call-' + n, type: 'function' as const, function: { name: tool.tool.name, arguments: '{}' } }));
    const results = await Promise.all(original.map(call => tool.execute({}, call)));
    expect(results.map(result => JSON.parse(result.content as string).dispatchState)).toEqual(['unknown', 'notDispatched', 'notDispatched']);
    expect(calls.filter(call => call.operation === 'tools/call')).toHaveLength(1);
    expect(api.revoke).not.toHaveBeenCalled();
    for (let index = 0; index < 3; index++) {
        expect(results[index].metadata).toEqual(tool.tool.metadata);
        expect(await tool.execute({}, original[index])).toEqual(results[index]);
        expect(JSON.stringify(results[index])).not.toContain('PRIVATE_CANARY');
    }
    const sent = calls.find(call => call.operation === 'tools/call');
    receive({ type: 'result', requestId: sent.requestId, result: { content: [{ type: 'text', text: 'late' }] } });
    expect(await tool.execute({}, original[0])).toEqual(results[0]);
    await expect(tool.execute({ changed: true }, { ...original[0], function: { name: tool.tool.name, arguments: '{"changed":true}' } })).rejects.toThrow('conflicting');
    await host.closeConversation('conversation');
    expect(api.revoke).toHaveBeenCalledOnce();
    await expect(tool.execute({}, original[0])).rejects.toThrow('unavailable');
});

it('caches bounded timeout failure without bypassing catalog revocation', async () => {
    const f = fixture(); const [tool] = await f.host.tools('conversation', 'thread');
    const request = vi.mocked(f.connections[0].request);
    request.mockRejectedValueOnce(new BrowserMCPSetupError('requestDeadlineExceeded', 'unknown'));
    const call = { id: 'timeout-call', type: 'function' as const, function: { name: tool.tool.name, arguments: '{}' } };
    const failure = await tool.execute({}, call);
    expect(JSON.parse(failure.content as string)).toEqual({ code: 'browserTransportTimeout', dispatchState: 'unknown', outcome: 'unknown' });
    expect(await tool.execute({}, call)).toEqual(failure);
    expect(request.mock.calls.filter(([method]) => method === 'tools/call')).toHaveLength(1);
    f.api.current.mockRejectedValueOnce(Error('PRIVATE_REVOKED_CANARY'));
    await expect(tool.execute({}, call)).rejects.toThrow('PRIVATE_REVOKED_CANARY');
    f.host.reset();
});

it('bounds real port timeout and ignores late duplicate replies without repeating a request', async () => {
    vi.useFakeTimers();
    try {
        let receive!: (message: any) => void;
        const port = { postMessage: vi.fn(), disconnect: vi.fn(), onMessage: { addListener: (fn: any) => { receive = fn; } }, onDisconnect: { addListener: vi.fn() } };
        const connection = new ChromeExtensionMCPConnection({ ...descriptor, transport: { ...descriptor.transport, requestTimeoutMs: 100 } }, undefined, { connect: () => port });
        receive({ type: 'approved' });
        const result = connection.request('tools/call', { name: 'safe_read', arguments: {} }).catch(error => error);
        await vi.advanceTimersByTimeAsync(0);
        const sent = port.postMessage.mock.calls[0][0];
        await vi.advanceTimersByTimeAsync(100);
        const failure = await result;
        expect(failure.code).toBe('requestDeadlineExceeded'); expect(failure.dispatchState).toBe('unknown');
        for (let i = 0; i < 2; i++) receive({ type: 'result', requestId: sent.requestId, result: { content: [{ type: 'text', text: 'late' }] } });
        expect(await result).toBe(failure); expect(port.postMessage).toHaveBeenCalledOnce();
        connection.close();
    } finally { vi.useRealTimers(); }
});

it('joins an in-flight duplicate and invalidates failure cache on expiry or explicit identity reset', async () => {
    const f = fixture(); const [tool] = await f.host.tools('conversation', 'thread');
    let fail!: (error: Error) => void;
    const request = vi.mocked(f.connections[0].request);
    request.mockImplementationOnce(async () => new Promise((_resolve, reject) => { fail = reject; }));
    const call = { id: 'same-call', type: 'function' as const, function: { name: tool.tool.name, arguments: '{}' } };
    const first = tool.execute({}, call), duplicate = tool.execute({}, structuredClone(call));
    await vi.waitFor(() => expect(fail).toBeTypeOf('function'));
    fail(new BrowserMCPSetupError('requestDeadlineExceeded', 'unknown'));
    const [one, two] = await Promise.all([first, duplicate]);
    expect(one).toEqual(two); expect(request.mock.calls.filter(([method]) => method === 'tools/call')).toHaveLength(1);
    (one as any).content = 'mutated';
    expect(await tool.execute({}, call)).toEqual(two);
    const catalog = await f.api.current((tool.tool.metadata as any).browserMCP.catalogId);
    catalog.expiresAt = new Date(Date.now() - 1).toISOString();
    await expect(tool.execute({}, call)).rejects.toThrow('unavailable');
    f.host.reset();
    await expect(tool.execute({}, call)).rejects.toThrow('unavailable');
});

it('transport loss exposes no app resources or new authority while retaining terminal metadata', async () => {
    const f = fixture(true); const [tool] = await f.host.tools('conversation', 'wire');
    await tool.execute({}, { id: 'app-result', type: 'function', function: { name: tool.tool.name, arguments: '{}' } });
    const [activity] = f.host.appActivities('conversation'); const proxy = f.host.appProxy(activity);
    f.connections[0].onclose?.();
    expect(f.host.appActivities('conversation')).toEqual([]);
    await expect(proxy.call('resources/read', { uri: activity.content.resourceUri })).rejects.toThrow('unavailable');
    expect(f.requests.filter(request => request.method === 'resources/read')).toHaveLength(0);
    expect(f.api.revoke).not.toHaveBeenCalled();
    await f.host.closeConversation('conversation'); expect(f.api.revoke).toHaveBeenCalledOnce();
});

it('explicit account reset while a call is pending never publishes a terminal result under the old identity', async () => {
    const f = fixture(); const [tool] = await f.host.tools('conversation', 'thread');
    let fail!: (error: Error) => void;
    vi.mocked(f.connections[0].request).mockImplementationOnce(async () => new Promise((_resolve, reject) => { fail = reject; }));
    const pending = tool.execute({}, { id: 'old-owner', type: 'function', function: { name: tool.tool.name, arguments: '{}' } });
    const rejected = expect(pending).rejects.toThrow('unavailable');
    await vi.waitFor(() => expect(fail).toBeTypeOf('function'));
    f.host.reset(); fail(new BrowserMCPSetupError('connectionLost', 'unknown'));
    await rejected;
});

it('expiry during a pending request is not converted into a valid original-catalog result', async () => {
    const f = fixture(); const [tool] = await f.host.tools('conversation', 'thread');
    let fail!: (error: Error) => void;
    vi.mocked(f.connections[0].request).mockImplementationOnce(async () => new Promise((_resolve, reject) => { fail = reject; }));
    const pending = tool.execute({}, { id: 'expired-pending', type: 'function', function: { name: tool.tool.name, arguments: '{}' } });
    const rejected = expect(pending).rejects.toThrow('unavailable');
    await vi.waitFor(() => expect(fail).toBeTypeOf('function'));
    const catalog = await f.api.current((tool.tool.metadata as any).browserMCP.catalogId);
    catalog.expiresAt = new Date(Date.now() - 1).toISOString();
    fail(new BrowserMCPSetupError('requestDeadlineExceeded', 'unknown'));
    await rejected; f.host.reset();
});

it('distinguishes every original catalog guard with a closed condition and zero dispatch', async () => {
    for (const code of ['bindingClosed', 'originChanged', 'catalogMissing', 'catalogExpired', 'toolNameMismatch', 'catalogIdentityMismatch', 'connectionIdentityMismatch']) {
        const f = fixture(); const [tool] = await f.host.tools('conversation', 'thread');
        const binding = [...(f.host as any).bindings.values()][0] as any;
        const call = { id: 'guard-call', type: 'function' as const, function: { name: tool.tool.name, arguments: '{}' } };
        const savedCatalog = binding.catalog;
        switch (code) {
            case 'bindingClosed': await f.host.closeConversation('conversation'); break;
            case 'originChanged': binding.origin = 'https://PRIVATE_ORIGIN_CANARY.invalid'; break;
            case 'catalogMissing': binding.catalog = undefined; break;
            case 'catalogExpired': binding.catalog.expiresAt = new Date(Date.now() - 1).toISOString(); break;
            case 'toolNameMismatch': call.function.name = 'PRIVATE_TOOL_CANARY'; break;
            case 'catalogIdentityMismatch': (tool.tool.metadata as any).browserMCP.catalogId = 'PRIVATE_ID_CANARY'; break;
            case 'connectionIdentityMismatch': (tool.tool.metadata as any).browserMCP.connectionId = 'PRIVATE_ID_CANARY'; break;
        }
        const error = await Promise.resolve(tool.execute({}, call)).catch(error => error);
        expect(error.code).toBe(code); expect(error.message).toBe(`Original browser MCP catalog is unavailable (${code}).`);
        expect(error.message).not.toContain('PRIVATE_');
        expect(f.requests.filter(request => request.method === 'tools/call')).toHaveLength(0);
        expect(f.api.current).not.toHaveBeenCalled();
        binding.catalog = savedCatalog; f.host.reset();
    }
});

it('executes compact catalog validation through the extension transport without a fixed wait or duplicate request', async () => {
    let receive!: (message: any) => void;
    let catalog!: BrowserMCPCatalog;
    const posted: any[] = [];
    const port = { postMessage: (request: any) => {
        posted.push(request);
        queueMicrotask(() => receive({ type: 'result', requestId: request.requestId,
            result: request.operation === 'tools/list' ? { tools: [{ name: 'safe_read', inputSchema: { type: 'object' } }] }
                : request.operation === 'tools/call' ? { content: [{ type: 'text', text: 'PUBLIC_FIXTURE_RESULT' }] } : {} }));
    }, disconnect: vi.fn(), onMessage: { addListener: (fn: any) => { receive = fn; } }, onDisconnect: { addListener: vi.fn() } };
    const api = { descriptors: async () => [descriptor], register: async (input: any) => {
        catalog = { ...input, id: 'catalog', hash: 'hash', expiresAt: new Date(Date.now() + 60000).toISOString(), tools: [{ name: 'device-safe_read', parameters: { type: 'object' }, metadata: { browserMCP: { catalogId: 'catalog', connectionId: input.connectionId, server: input.server, tool: 'safe_read' } } }] };
        return catalog;
    }, current: vi.fn(async () => { const { tools, resources, ...status } = catalog; return status; }), revoke: vi.fn(async () => {}) };
    const host = new BrowserMCPHost(api, { transportFactory: () => {
        const connection = new ChromeExtensionMCPConnection(descriptor, undefined, { connect: () => port });
        queueMicrotask(() => receive({ type: 'approved' })); return connection;
    } });
    const [tool] = await host.tools('conversation', 'thread');
    const original = { id: 'original', type: 'function' as const, function: { name: tool.tool.name, arguments: '{}' } };
    const started = performance.now();
    const result = await tool.execute({}, original);
    expect(result.content).toContain('PUBLIC_FIXTURE_RESULT');
    expect(performance.now() - started).toBeLessThan(1000);
    expect(await tool.execute({}, original)).toEqual(result);
    expect(posted.filter(item => item.operation === 'tools/call')).toHaveLength(1);
    for (const phase of ['tool.total', 'tool.catalogCurrent', 'tool.nativeRequest', 'tool.resultProjection', 'transport.queue', 'transport.roundTrip']) {
        const entries = performance.getEntriesByName(`agently.browserMCP.${phase}`);
        expect(entries).toHaveLength(1);
        expect(entries[0].duration).toBeGreaterThanOrEqual(0);
        expect((entries[0] as PerformanceMeasure).detail).toBeNull();
    }
    await expect(tool.execute({ different: true }, { ...original, function: { ...original.function, arguments: '{"different":true}' } })).rejects.toThrow('conflicting');
    await host.closeConversation('conversation');
});

it('copies authenticated catalog renewal across a long active binding without replay or identity replacement', async () => {
    vi.useFakeTimers();
    try {
        const f = fixture(); const [tool] = await f.host.tools('conversation', 'thread');
        const binding = [...(f.host as any).bindings.values()][0];
        const original = { id: binding.catalog.id, connectionId: binding.connectionId };
        const registered = { ...binding.catalog };
        f.api.current.mockImplementation(async id => ({ ...registered, expiresAt: new Date(Date.now() + 24 * 60 * 60 * 1000).toISOString() }));
        const call = (id: string) => ({ id, type: 'function' as const, function: { name: tool.tool.name, arguments: '{}' } });
        await tool.execute({}, call('first'));
        await vi.advanceTimersByTimeAsync(16 * 60 * 1000);
        await tool.execute({}, call('second'));
        expect(binding.catalog.id).toBe(original.id); expect(binding.connectionId).toBe(original.connectionId);
        expect(f.requests.filter(r => r.method === 'tools/call')).toHaveLength(2);
        await tool.execute({}, call('second')); expect(f.requests.filter(r => r.method === 'tools/call')).toHaveLength(2);
        f.api.current.mockRejectedValueOnce(new Error('revoked'));
        await expect(tool.execute({}, call('revoked'))).rejects.toThrow('revoked');
        expect(f.requests.filter(r => r.method === 'tools/call')).toHaveLength(2);
        await vi.advanceTimersByTimeAsync(24 * 60 * 60 * 1000 + 1);
        await expect(tool.execute({}, call('idle-expired'))).rejects.toThrow('catalogExpired');
        expect(f.requests.filter(r => r.method === 'tools/call')).toHaveLength(2);
        await f.host.closeConversation('conversation');
    } finally { vi.useRealTimers(); }
});
