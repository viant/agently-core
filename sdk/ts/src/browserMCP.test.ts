import { expect, it, vi } from 'vitest';
import { BrowserMCPHost, type BrowserMCPCatalog, type BrowserMCPConnection, type BrowserMCPDescriptor } from './browserMCP';
const descriptor: BrowserMCPDescriptor = { name: 'device', executionLocation: 'browser', transport: { type: 'chrome-extension', extensionId: 'a'.repeat(32), portName: 'fixture-v1' }, allowedTools: ['safe_*'] };
function fixture(app = false) {
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
            if (method === 'initialize') return { protocolVersion: '2025-11-25', ...(app ? { capabilities: { resources: {} } } : {}) };
            if (method === 'tools/list') return { tools: [{ name: 'safe_read', description: 'Read fixture', inputSchema: { type: 'object' }, ...(app ? { _meta: { ui: { resourceUri: 'ui://device/fixture' } } } : {}) }, { name: 'forbidden', inputSchema: { type: 'object' } }] };
            if (fail) throw Error('lost');
            if (method === 'resources/read') return { contents: [{ uri: params?.uri, mimeType: 'text/html;profile=mcp-app', text: '<p>Generic fixture</p>' }] };
            return { content: [{ type: 'text', text: 'ok' }], structuredContent: { ok: true }, _meta: { secret: 'not-model-content' } };
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
it('does not replay unknown calls and invalidates clients on account reset', async () => {
    const f = fixture(); const tools = await f.host.tools('conversation', 'wire'); f.fail();
    const call = { id: 'lost', type: 'function' as const, function: { name: tools[0].tool.name, arguments: '{}' } };
    await expect(tools[0].execute({}, call)).rejects.toThrow('lost'); await expect(tools[0].execute({}, call)).rejects.toThrow(/uncertain/);
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
