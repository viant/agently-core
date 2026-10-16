import { createServer, type Server } from 'node:http';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { AgentlyClient } from './client';
const frame = (event: unknown) => `data: ${JSON.stringify(event)}\n\n`;
const compact = { conversationId: 'native', includeModelCalls: true, includeToolCalls: true, includeModelPayloads: false, includeToolPayloads: false, includeFeeds: false };
function deferred() { let resolve!: () => void; const promise = new Promise<void>(done => { resolve = done; }); return { promise, resolve }; }
function bootstrap(input: any) {
    return frame({ type: 'RUN_STARTED', threadId: input.threadId, runId: input.runId }) + frame({ type: 'RUN_FINISHED', threadId: input.threadId, runId: input.runId, result: {
        version: '1', threadId: input.threadId, transcript: { schemaVersion: '2', aguiThreadId: input.threadId, conversation: { conversationId: 'native', turns: [] } },
        messages: [], state: {}, runs: [], projection: { lossless: true, unavailableMessageIds: [] },
    } });
}
const servers: Server[] = [];
afterEach(async () => { await Promise.all(servers.splice(0).map(server => new Promise<void>(resolve => { server.close(() => resolve()); server.closeAllConnections(); }))); });

describe('coordinator-owned authenticated header reads', () => {
    it('shares parallel UI metadata and binding resolution over HTTP, then refetches metadata and keeps full reads independent', async () => {
        const gate = deferred(); let headers = 0, full = 0;
        const commands: any[] = [];
        const server = createServer(async (req, res) => {
            expect(req.headers.authorization).toBe('Bearer fixture-token');
            if (req.method === 'GET') {
                if (req.url?.includes('includeTranscript=false')) { headers++; await gate.promise; } else full++;
                res.writeHead(200, { 'Content-Type': 'application/json' }); res.end(JSON.stringify({ id: 'native', aguiThreadId: 'opaque-wire', title: 'original title' })); return;
            }
            let body = ''; for await (const chunk of req) body += chunk;
            const input = JSON.parse(body); commands.push(input);
            res.writeHead(200, { 'Content-Type': 'text/event-stream' }); res.end(bootstrap(input));
        }); servers.push(server);
        await new Promise<void>(resolve => server.listen(0, '127.0.0.1', resolve));
        const address = server.address(); if (!address || typeof address === 'string') throw new Error('Missing fixture address');
        const host = new AgentlyClient({ baseURL: `http://127.0.0.1:${address.port}/v1`, headers: { Authorization: 'Bearer fixture-token' } });
        const metadata = host.getConversation('native', { includeTranscript: false });
        const read = host.getTranscript(compact);
        const subscription = host.streamEvents('native', {});
        await vi.waitFor(() => expect(headers).toBe(1));
        expect(commands).toHaveLength(0);
        await Promise.all([host.getConversation('native'), host.getConversation('native')]);
        expect(full).toBe(2); expect(headers).toBe(1);
        gate.resolve();
        expect((await metadata).title).toBe('original title'); await read;
        expect(commands).toHaveLength(1); expect(commands[0].threadId).toBe('opaque-wire');
        await host.getConversation('native', { includeTranscript: false }); expect(headers).toBe(2);
        await host.getTranscript(compact); expect(headers).toBe(2);
        subscription.close(); host.resetAgUiInteractions();
    });

    it('seeds only explicit matching authenticated DTO bindings and does not retain header data', async () => {
        let headers = 0, title = 'first'; const commands: any[] = [];
        const host = new AgentlyClient({ baseURL: '/v1', fetchImpl: async (url, init) => {
            if (String(url).includes('/conversations/')) { headers++; return new Response(JSON.stringify({ id: 'native', aguiThreadId: 'opaque', title })); }
            const input = JSON.parse(String(init?.body)); commands.push(input); return new Response(bootstrap(input), { headers: { 'Content-Type': 'text/event-stream' } });
        } });
        await host.getConversation('native', { includeTranscript: false });
        await host.getTranscript(compact); expect(headers).toBe(1); expect(commands[0].threadId).toBe('opaque');
        title = 'fresh'; expect((await host.getConversation('native', { includeTranscript: false })).title).toBe('fresh'); expect(headers).toBe(2);
        host.resetAgUiInteractions();
    });

    it.each(['wrong-native', 'changed-wire', 'empty-wire'])('rejects %s without poisoning the established binding', async kind => {
        let response: any = { id: 'native', aguiThreadId: 'opaque' }; const commands: any[] = [];
        const host = new AgentlyClient({ baseURL: '/v1', fetchImpl: async (url, init) => {
            if (String(url).includes('/conversations/')) return new Response(JSON.stringify(response));
            const input = JSON.parse(String(init?.body)); commands.push(input); return new Response(bootstrap(input), { headers: { 'Content-Type': 'text/event-stream' } });
        } });
        await host.getConversation('native', { includeTranscript: false });
        response = kind === 'wrong-native' ? { id: 'foreign', aguiThreadId: 'foreign-wire' } : { id: 'native', aguiThreadId: kind === 'empty-wire' ? '' : 'different-wire' };
        await expect(host.getConversation('native', { includeTranscript: false })).rejects.toThrow(/binding/);
        await host.getTranscript(compact); expect(commands[0].threadId).toBe('opaque'); host.resetAgUiInteractions();
    });

    it('discards a completed old-account header after reset and resolves the new account independently', async () => {
        const old = deferred(); let headers = 0; const commands: any[] = [];
        const host = new AgentlyClient({ baseURL: '/v1', fetchImpl: async (url, init) => {
            if (String(url).includes('/conversations/')) {
                const count = ++headers; if (count === 1) await old.promise;
                return new Response(JSON.stringify({ id: 'native', aguiThreadId: count === 1 ? 'old-wire' : 'new-wire' }));
            }
            const input = JSON.parse(String(init?.body)); commands.push(input); return new Response(bootstrap(input), { headers: { 'Content-Type': 'text/event-stream' } });
        } });
        const stale = host.getConversation('native', { includeTranscript: false });
        const rejected = expect(stale).rejects.toThrow(/invalidated/);
        await vi.waitFor(() => expect(headers).toBe(1)); host.resetAgUiInteractions();
        await host.getTranscript(compact); expect(commands[0].threadId).toBe('new-wire');
        old.resolve(); await rejected;
        await host.getTranscript(compact); expect(commands[1].threadId).toBe('new-wire'); expect(headers).toBe(2); host.resetAgUiInteractions();
    });

    it('preserves missing-binding fallback and leaves full DTOs outside binding seeding', async () => {
        let headers = 0; const commands: any[] = [];
        const host = new AgentlyClient({ baseURL: '/v1', fetchImpl: async (url, init) => {
            if (String(url).includes('/conversations/')) {
                if (String(url).includes('includeTranscript=false')) { headers++; return new Response(JSON.stringify({ id: 'native' })); }
                return new Response(JSON.stringify({ id: 'native', aguiThreadId: 'full-detail-wire' }));
            }
            const input = JSON.parse(String(init?.body)); commands.push(input); return new Response(bootstrap(input), { headers: { 'Content-Type': 'text/event-stream' } });
        } });
        await host.getConversation('native'); await host.getTranscript(compact);
        expect(headers).toBe(1); expect(commands[0].threadId).toBe('native');
        host.resetAgUiInteractions();
    });
});
