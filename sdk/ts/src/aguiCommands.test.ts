import { describe, expect, it, vi } from 'vitest';
import { AgUiCommands, AgUiCommandError, type AgUiCommandIdentity } from './aguiCommands';

const identity: AgUiCommandIdentity = { threadId: 'resource-thread', runId: 'command-run', requestId: 'request' };
function transport(events: (input: any) => object[]) {
    const requests: { input: any; init: RequestInit; url: string }[] = [];
    const fetch = vi.fn(async (url: string, init: RequestInit) => {
        const input = JSON.parse(String(init.body));
        requests.push({ url, init, input });
        const frames = events(input).map(event => `data: ${JSON.stringify(event)}\n\n`).join('');
        return new Response(frames, { headers: { 'Content-Type': 'text/event-stream' } });
    });
    return { fetch, requests };
}
const start = (input: any) => ({ type: 'RUN_STARTED', threadId: input.threadId, runId: input.runId });
const finish = (input: any, extra: object = {}) => ({ type: 'RUN_FINISHED', threadId: input.threadId, runId: input.runId, outcome: { type: 'success' }, ...extra });

describe('independent AG-UI command transport', () => {
    it('preserves exact canonical bootstrap DTO, protocol media and nullable state', async () => {
        const result = {
            version: '1', threadId: identity.threadId, state: null,
            transcript: { schemaVersion: '2', conversation: { conversationId: identity.threadId, turns: [{ turnId: 'native-turn', status: 'completed', execution: { pages: [{ pageId: 'native-page', toolSteps: [{ toolCallId: 'native-tool', responsePayload: { original: true } }] }] } }] }, feeds: [{ feedId: 'feed', data: { rows: [{ count: 0 }] } }], usage: { totalTokens: 0 }, future: { preserved: true } },
            messages: [{ id: 'media', role: 'user', content: [{ type: 'image', source: { type: 'url', value: 'https://example.com/image.png' } }], metadata: { keep: false } }],
            runs: [{ threadId: identity.threadId, runId: 'active', status: 'running', revision: 1, lastSequence: 2 }],
            projection: { lossless: false, unavailableMessageIds: ['native-only'] },
        };
        const fixture = transport(input => [start(input), { type: 'STATE_SNAPSHOT', snapshot: null }, { type: 'MESSAGES_SNAPSHOT', messages: result.messages }, finish(input, { result })]);
        const actual = await new AgUiCommands({ url: '/run', fetch: fixture.fetch }).execute('conversation.bootstrap', { mode: 'live', includeFeeds: true, selectors: { ExecutionGroup: { limit: 20, offset: 0 } } }, identity);
        expect(actual).toEqual(result);
        expect(actual?.projection.lossless).toBe(false);
        expect(fixture.requests[0].input.messages).toEqual([]);
    });

    it('validates bootstrap protocol messages even inside opaque terminal result', async () => {
        const result = { version: '1', threadId: identity.threadId, transcript: { schemaVersion: '2', conversation: { conversationId: identity.threadId, turns: [] } }, messages: [{ role: 'user', content: 'missing identity' }], state: null, runs: [], projection: { lossless: false, unavailableMessageIds: [] } };
        const fixture = transport(input => [start(input), finish(input, { result })]);
        await expect(new AgUiCommands({ url: '/run', fetch: fixture.fetch }).execute('conversation.bootstrap', {}, identity)).rejects.toBeInstanceOf(Error);
    });
    it('posts isolated commands with stable identities, BFF cookies, headers and custom fetch', async () => {
        const fixture = transport(input => [start(input), finish(input, { result: { invalidated: true } })]);
        const client = new AgUiCommands({ url: '/v1/ag-ui/run', fetch: fixture.fetch, headers: { 'X-Test-Header': 'trusted' }, threadId: 'chat-thread', initialMessages: [{ id: 'secret-chat', role: 'user', content: 'do not inherit' }], initialState: { secret: true } });
        const payload = { id: 'lookup', inputsHash: 'hash' };
        expect(await client.execute('datasource.cache.invalidate', payload, identity)).toEqual({ invalidated: true });
        await client.execute('datasource.cache.invalidate', payload, identity);
        expect(fixture.requests).toHaveLength(2);
        expect(fixture.requests[1].input).toEqual(fixture.requests[0].input);
        const { input, init, url } = fixture.requests[0];
        expect(url).toBe('/v1/ag-ui/run');
        expect(init.credentials).toBe('include');
        expect(new Headers(init.headers).get('X-Test-Header')).toBe('trusted');
        expect(input).toMatchObject({ threadId: 'resource-thread', runId: 'command-run', messages: [], state: {}, tools: [], context: [], forwardedProps: { agently: { version: '1', operation: 'datasource.cache.invalidate', requestId: 'request', payload } } });
        expect(input).not.toHaveProperty('resume');
    });

    it.each([0, false, '', null, undefined])('preserves the terminal result %s', async value => {
        const fixture = transport(input => [start(input), finish(input, { result: value })]);
        const commands = new AgUiCommands({ url: '/run', fetch: fixture.fetch });
        expect(await commands.execute('workspace.metadata.get', {}, identity)).toBe(value);
    });

    it('returns defensive accepted input and does not mutate caller payload', async () => {
        const fixture = transport(input => [start(input), finish(input, { result: { saved: true } })]);
        const commands = new AgUiCommands({ url: '/run', fetch: fixture.fetch });
        const payload = { kind: 'agent', name: 'a', data: 'YmFzZTY0' };
        const mutableIdentity = { ...identity };
        const handle = commands.start('workspace.resource.save', payload, mutableIdentity);
        payload.data = 'changed';
        mutableIdentity.runId = 'changed';
        mutableIdentity.requestId = 'changed';
        await handle.result;
        expect(fixture.requests[0].input.forwardedProps.agently.payload.data).toBe('YmFzZTY0');
        expect(fixture.requests[0].input.runId).toBe('command-run');
        const posted = handle.lastPostedInput!;
        posted.messages.push({ id: 'mutated', role: 'user', content: 'local' });
        expect(handle.lastPostedInput!.messages).toEqual([]);
    });

    it('preserves split UTF-8 SSE results without buffering the response', async () => {
        const value = { title: 'héllo 🌍', count: 0 };
        const commands = new AgUiCommands({ url: '/run', fetch: async (_url, init) => {
            const input = JSON.parse(String(init.body));
            const bytes = new TextEncoder().encode([start(input), finish(input, { result: value })].map(event => `data: ${JSON.stringify(event)}\n\n`).join(''));
            let offset = 0;
            return new Response(new ReadableStream({ pull(controller) {
                if (offset >= bytes.length) { controller.close(); return; }
                controller.enqueue(bytes.slice(offset, offset + 7)); offset += 7;
            } }), { headers: { 'Content-Type': 'text/event-stream' } });
        } });
        expect(await commands.execute('workspace.metadata.get', {}, identity)).toEqual(value);
    });

    it('keeps concurrent resource runs isolated from each other', async () => {
        const fixture = transport(input => [start(input), { type: 'STATE_SNAPSHOT', snapshot: { dirty: input.runId } }, finish(input, { result: { feedId: input.forwardedProps.agently.payload.id } })]);
        const commands = new AgUiCommands({ url: '/run', fetch: fixture.fetch });
        const results = await Promise.all([
            commands.execute('feed.get', { id: 'left' }, identity),
            commands.execute('feed.get', { id: 'right' }, { ...identity, runId: 'another-run', requestId: 'another-request' }),
        ]);
        expect(results).toEqual([{ feedId: 'left' }, { feedId: 'right' }]);
        expect(fixture.requests.every(request => Object.keys(request.input.state).length === 0 && request.input.messages.length === 0)).toBe(true);
    });

    it('normalizes the current goal custom result only after successful terminal completion', async () => {
        const result = { version: '1', goal: null, cleared: true };
        const fixture = transport(input => [start(input), { type: 'CUSTOM', name: 'agently.goal.result', value: { version: '1', operation: 'goal.clear', requestId: 'request', result } }, finish(input)]);
        expect(await new AgUiCommands({ url: '/run', fetch: fixture.fetch }).execute('goal.clear', {}, identity)).toEqual(result);
    });

    it('normalizes state result while keeping command reducer state independent', async () => {
        const result = { version: '1', hash: 's1:hash', state: { count: 2 } };
        const fixture = transport(input => [start(input), { type: 'STATE_SNAPSHOT', snapshot: { count: 1 } }, { type: 'STATE_DELTA', delta: [{ op: 'replace', path: '/count', value: 2 }] }, { type: 'CUSTOM', name: 'agently.state.result', value: result }, finish(input)]);
        expect(await new AgUiCommands({ url: '/run', fetch: fixture.fetch }).execute('state.patch', { patch: [{ op: 'replace', path: '/count', value: 2 }], ifMatch: 's1:prior' }, identity)).toEqual(result);
        expect(fixture.requests[0].input.state).toEqual({});
    });

    it('rejects protocol errors even though the upstream agent resolves RUN_ERROR', async () => {
        const fixture = transport(input => [start(input), { type: 'CUSTOM', name: 'agently.goal.result', value: { version: '1', operation: 'goal.get', requestId: 'request', result: null } }, { type: 'RUN_ERROR', message: 'permission denied', code: 'FORBIDDEN' }]);
        await expect(new AgUiCommands({ url: '/run', fetch: fixture.fetch }).execute('goal.get', {}, identity)).rejects.toMatchObject({ name: 'AgUiCommandError', message: 'permission denied', code: 'FORBIDDEN' });
    });

    it.each(['interrupt', 'cancelled'])('rejects non-success command outcome %s', async outcome => {
        const fixture = transport(input => [start(input), finish(input, { outcome: outcome === 'interrupt' ? { type: outcome, interrupts: [{ id: 'approval', reason: 'approval' }] } : { type: outcome } })]);
        await expect(new AgUiCommands({ url: '/run', fetch: fixture.fetch }).execute('workspace.metadata.get', {}, identity)).rejects.toMatchObject({ outcome });
    });

    it('rejects unmatched custom result identities and unsupported versions', async () => {
        const fixture = transport(input => [start(input), { type: 'CUSTOM', name: 'agently.goal.result', value: { version: '1', operation: 'goal.get', requestId: 'someone-else', result: null } }, { type: 'CUSTOM', name: 'agently.goal.result', value: { version: '2', operation: 'goal.get', requestId: 'request', result: null } }, finish(input)]);
        await expect(new AgUiCommands({ url: '/run', fetch: fixture.fetch }).execute('goal.get', {}, identity)).rejects.toMatchObject({ code: 'MISSING_COMMAND_RESULT' });
    });

    it('rejects truncated command streams and HTTP/transport failures', async () => {
        const fixture = transport(input => [start(input)]);
        await expect(new AgUiCommands({ url: '/run', fetch: fixture.fetch }).execute('workspace.metadata.get', {}, identity)).rejects.toBeInstanceOf(Error);
        await expect(new AgUiCommands({ url: '/run', fetch: async () => new Response('unauthorized', { status: 401 }) }).execute('workspace.metadata.get', {}, identity)).rejects.toBeInstanceOf(Error);
        await expect(new AgUiCommands({ url: '/run', fetch: async () => { throw new Error('offline'); } }).execute('workspace.metadata.get', {}, identity)).rejects.toThrow('offline');
    });

    it('does not bypass official schema and sequence validation when a raw result exists', async () => {
        const fixture = transport(input => [start(input), { type: 'TEXT_MESSAGE_CONTENT', messageId: 'never-started', delta: 'invalid' }, finish(input, { result: null })]);
        await expect(new AgUiCommands({ url: '/run', fetch: fixture.fetch }).execute('workspace.metadata.get', {}, identity)).rejects.toBeInstanceOf(Error);
    });

    it('runs capabilities discovery only when explicitly requested', async () => {
        const capabilities = { custom: { agently: { version: '1', operations: ['workspace.metadata.get'] } } };
        const fixture = transport(input => [start(input), { type: 'CUSTOM', name: 'agently.capabilities', value: { version: '1', capabilities } }, finish(input)]);
        const commands = new AgUiCommands({ url: '/run', fetch: fixture.fetch, credentials: 'same-origin', threadId: 'thread' });
        expect(fixture.fetch).not.toHaveBeenCalled();
        expect(await commands.discoverCapabilities({ runId: 'discovery' })).toEqual(capabilities);
        expect(fixture.requests[0].input.forwardedProps.agently.operation).toBe('capabilities');
        expect(fixture.requests[0].init.credentials).toBe('same-origin');
        expect(fixture.requests[0].input.messages).toEqual([]);
    });

    it('aborts only the consumer transport; explicit cancellation is a separate command', async () => {
        let begin!: () => void;
        const begun = new Promise<void>(resolve => { begin = resolve; });
        const requests: any[] = [];
        let removeListener: ReturnType<typeof vi.spyOn>;
        const commands = new AgUiCommands({ url: '/run', fetch: async (_url, init) => {
            const input = JSON.parse(String(init.body)); requests.push(input);
            removeListener = vi.spyOn(init.signal!, 'removeEventListener');
            return new Response(new ReadableStream({ start(controller) {
                controller.enqueue(new TextEncoder().encode(`data: ${JSON.stringify(start(input))}\n\n`));
                init.signal!.addEventListener('abort', () => controller.error(new DOMException('Detached', 'AbortError')), { once: true });
                begin();
            } }), { headers: { 'Content-Type': 'text/event-stream' } });
        } });
        const handle = commands.start('workspace.metadata.get', {}, identity);
        const result = handle.result.catch(error => error);
        await begun;
        handle.abortTransport();
        expect(await result).toBeInstanceOf(Error);
        expect(removeListener!).toHaveBeenCalledWith('abort', expect.any(Function));
        expect(requests.map(input => input.forwardedProps.agently.operation)).toEqual(['workspace.metadata.get']);
        const fixture = transport(input => [start(input), finish(input, { result: { version: '1', runId: 'target', cancelled: false } })]);
        expect(await new AgUiCommands({ url: '/run', fetch: fixture.fetch }).execute('run.cancel', { runId: 'target' }, identity)).toMatchObject({ cancelled: false });
    });

    it('requires caller-provided stable command identity', () => {
        const commands = new AgUiCommands({ url: '/run' });
        expect(() => commands.start('feed.get', { id: 'feed' }, { ...identity, runId: '' })).toThrow(/runId/);
        expect(AgUiCommandError).toBeDefined();
    });
});
