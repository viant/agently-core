import { describe, expect, it, vi } from 'vitest';
import type { BrowserMCPHost } from './browserMCP';
import { AgentlyClient } from './client';
import { AgUiConversationTransport, type AgUiConversationProjectionFactory } from './aguiConversationTransport';

const frame = (event: unknown) => new TextEncoder().encode(`data: ${JSON.stringify(event)}\n\n`);
const projection: AgUiConversationProjectionFactory = options => ({ subscriber: {
    onActivitySnapshotEvent: ({ event }) => {
        if (event.activityType === 'mcp-apps') {
            options.onDescriptor?.({ kind: 'host-activity', hostEffectsAllowed: true,
                message: { id: event.messageId, role: 'activity', activityType: event.activityType, content: event.content } });
            return;
        }
        if (event.activityType !== 'agently.turn') return;
        options.onViewEvent({ type: event.content.status === 'queued' ? 'turn_queued' : 'turn_started',
            conversationId: options.conversationId, turnId: String(event.content.nativeTurnId) });
    },
} });

function fixture(active: Array<Record<string, unknown>> = [], beforeAdmission?: (init?: RequestInit) => Promise<void>, hostActivities: unknown[] = [], beforeBootstrap?: () => Promise<void>, bootstrapTurns: () => any[] = () => [], wireThreadId?: string, browserMCP?: BrowserMCPHost) {
    const posted: any[] = [];
    let metadataReads = 0;
    const controls = new Map<string, ReadableStreamDefaultController<Uint8Array>>();
    const fetchImpl = vi.fn(async (url: RequestInfo | URL, init?: RequestInit) => {
        if (String(url).includes('/conversations/')) {
            expect(init?.credentials).toBe('include');
            expect(init?.method).toBe('GET');
            metadataReads++;
            return new Response(JSON.stringify({ id: decodeURIComponent(String(url).split('/').at(-1)!), aguiThreadId: wireThreadId }), { headers: { 'Content-Type': 'application/json' } });
        }
        expect(String(url)).toBe('/v1/ag-ui/run');
        expect(init?.credentials).toBe('include');
        const input = JSON.parse(String(init?.body));
        posted.push(input);
        const start = { type: 'RUN_STARTED', threadId: input.threadId, runId: input.runId,
            metadata: { agently: { identityVersion: '1', nativeTurnId: `native-${input.runId}` } } };
        if (input.forwardedProps?.agently?.operation === 'conversation.bootstrap') {
            const committedTurns = structuredClone(bootstrapTurns());
            await beforeBootstrap?.();
            return new Response(new ReadableStream({ start(controller) {
                controller.enqueue(frame(start));
                controller.enqueue(frame({ type: 'RUN_FINISHED', threadId: input.threadId, runId: input.runId, result: {
                    version: '1', threadId: input.threadId, transcript: { schemaVersion: '1', aguiThreadId: input.threadId, conversation: { conversationId: 'thread', turns: committedTurns } },
                    messages: [], hostActivities, unavailableHostActivityIds: [], state: { keep: 'server-state' }, runs: active,
                    projection: { lossless: true, unavailableMessageIds: [] },
                } }));
                controller.close();
            } }), { headers: { 'Content-Type': 'text/event-stream' } });
        }
        await beforeAdmission?.(init);
        if (init?.signal?.aborted) throw new DOMException('Detached', 'AbortError');
        return new Response(new ReadableStream({ start(controller) {
            controls.set(input.runId, controller);
            controller.enqueue(frame(start));
            controller.enqueue(frame({ type: 'ACTIVITY_SNAPSHOT', messageId: `activity-${input.runId}`, activityType: 'agently.turn',
                content: { version: '1', nativeTurnId: `native-${input.runId}`, status: controls.size > 1 ? 'queued' : 'running', queueSequence: String(controls.size) } }));
            init?.signal?.addEventListener('abort', () => {
                controls.delete(input.runId);
                controller.error(new DOMException('Detached', 'AbortError'));
            }, { once: true });
        } }), { headers: { 'Content-Type': 'text/event-stream' } });
    });
    const host = new AgentlyClient({ baseURL: '/v1', useCookies: true, fetchImpl });
    const transport = new AgUiConversationTransport(host, projection, browserMCP);
    const finish = (runId: string) => {
        const controller = controls.get(runId)!;
        controls.delete(runId);
        controller.enqueue(frame({ type: 'RUN_FINISHED', threadId: wireThreadId ?? 'thread', runId }));
        controller.close();
    };
    return { host, transport, posted, controls, finish, metadataReads: () => metadataReads };
}

describe('native web conversation orchestration', () => {
    it('preserves selected history flags and pagination through AG-UI without replacing full coordinator state', async () => {
        const f = fixture();
        const result = await f.host.getTranscript({conversationId:'thread',since:'previous',includeModelCalls:false,includeToolCalls:true,includeFeeds:false}, {executionGroupLimit:5,executionGroupOffset:2});
        expect(result.turns).toEqual([]);
        expect(f.posted).toHaveLength(1);
        expect(f.posted[0].forwardedProps.agently.payload).toEqual({mode:'transcript',since:'previous',includeModelCalls:false,includeToolCalls:true,includeFeeds:false,selectors:{ExecutionGroup:{limit:5,offset:2}}});
        await f.transport.refresh('thread');
        expect(f.posted[1].forwardedProps.agently.payload).toMatchObject({mode:'live',includeModelCalls:true,includeToolCalls:true,includeFeeds:true});
        f.host.resetAgUiInteractions(); f.transport.reset();
    });
    it('rejects selected history arriving after account reset', async () => {
        let release!: () => void;
        const waiting = new Promise<void>(resolve=>{release=resolve;});
        const f=fixture([],undefined,[],()=>waiting);
        const reading=f.transport.readSnapshot('thread',{mode:'transcript',selectors:{ExecutionGroup:{limit:1}}});
        const rejection=expect(reading).rejects.toThrow(/invalidated|abort|cancel/i);
        await vi.waitFor(()=>expect(f.posted).toHaveLength(1));
        f.transport.reset(); release(); await rejection;
        f.host.resetAgUiInteractions();
    });

    it('reopens native history with the authenticated exact opaque wire binding', async () => {
        const wire = '  Wire-雪\t';
        const f = fixture([], undefined, [], undefined, () => [], wire);
        const bootstrap = await f.transport.refresh('thread');
        expect(bootstrap.transcript.conversation.conversationId).toBe('thread');
        expect(bootstrap.threadId).toBe(wire);
        const admitted = await f.transport.query({ conversationId: 'thread', query: 'same native UI' });
        expect(admitted.conversationId).toBe('thread');
        expect(f.posted.every(row => row.threadId === wire)).toBe(true);
        expect(f.metadataReads()).toBe(1);
        const chat = f.posted.find(row => row.forwardedProps.agently.operation === 'chat');
        f.finish(chat.runId);
        f.transport.reset();
    });
    it('does a fresh read for committed notifications arriving during an older bootstrap', async () => {
        let release!: () => void;
        let reads = 0;
        const waiting = new Promise<void>(resolve => { release = resolve; });
        const f = fixture([], undefined, [], async () => { if (++reads === 1) await waiting; });
        const original = f.transport.refresh('thread');
        await vi.waitFor(() => expect(reads).toBe(1));
        const changed = f.transport.reconcile('thread');
        const alsoChanged = f.transport.reconcile('thread');
        release();
        await Promise.all([original, changed, alsoChanged]);
        expect(reads).toBe(2);
        f.transport.reset();
    });
    it('post-move reconciliation returns committed ordering after a pre-command read completes', async () => {
        let release!: () => void;
        let reads = 0;
        let order = [{turnId:'a',status:'queued',queueSequence:'9007199254740992'},{turnId:'b',status:'queued',queueSequence:'9007199254740993'}];
        const waiting = new Promise<void>(resolve => { release = resolve; });
        const f = fixture([],undefined,[],async()=>{if(++reads===1)await waiting;},()=>order);
        const preCommand = f.transport.refresh('thread');
        await vi.waitFor(()=>expect(reads).toBe(1));
        // The native move has committed while the older read is in flight.
        order=[{turnId:'b',status:'queued',queueSequence:'9007199254740992'},{turnId:'a',status:'queued',queueSequence:'9007199254740993'}];
        const postCommand=f.transport.reconcile('thread');
        release();
        const [before,after]=await Promise.all([preCommand,postCommand]);
        expect(before.transcript.conversation.turns!.map(turn=>turn.turnId)).toEqual(['a','b']);
        expect(after.transcript.conversation.turns!.map(turn=>turn.turnId)).toEqual(['b','a']);
        expect(reads).toBe(2);
        f.transport.reset();
    });
    it('keeps bootstrap and live host activities separate from subsequent model inputs', async () => {
        const activity = { id: 'app-one', role: 'activity', activityType: 'mcp-apps', content: { result: { _meta: { private: 'HOST_PRIVATE' } } } };
        const f = fixture([], undefined, [activity]);
        const host = vi.fn();
        f.transport.subscribe('thread', { onHostActivities: host });
        await f.transport.query({ conversationId: 'thread', query: 'Continue' });
        expect(host.mock.calls.some(([activities]) => activities.some((item: { id: string }) => item.id === 'app-one'))).toBe(true);
        expect(JSON.stringify(f.posted)).not.toContain('HOST_PRIVATE');
        const controller = [...f.controls.values()][0];
        controller.enqueue(frame({ type: 'ACTIVITY_SNAPSHOT', messageId: 'app-two', activityType: 'mcp-apps', content: { result: { _meta: { private: 'OTHER_PRIVATE' } } } }));
        await vi.waitFor(() => expect(host.mock.calls.at(-1)?.[0]).toHaveLength(2));
        f.transport.reset();
    });
    it('accepts a queued follow-up while the first AG-UI run is still streaming', async () => {
        const f = fixture();
        const events: string[] = [];
        const subscription = f.transport.subscribe('thread', { onEvent: event => events.push(event.type) });
        const first = await f.transport.query({ conversationId: 'thread', messageId: 'first-user', query: 'one', userId: 'not-authority', toolBundles: ['reporting'] });
        expect(first.turnId).toMatch(/^native-/);
        expect(f.controls.size).toBe(1);
        const second = await f.transport.query({ conversationId: 'thread', messageId: 'second-user', query: 'two' });
        expect(second.turnId).not.toBe(first.turnId);
        expect(f.controls.size).toBe(2);
        expect(events).toEqual(['turn_started', 'turn_queued']);
        const queries = f.posted.filter(input => input.forwardedProps?.agently?.operation === 'chat');
        expect(queries).toHaveLength(2);
        expect(queries[0].messages).toEqual([{ id: 'first-user', role: 'user', content: 'one' }]);
        expect(queries[1].messages).toEqual([{ id: 'second-user', role: 'user', content: 'two' }]);
        expect(queries[0].forwardedProps.agently.payload).toMatchObject({ toolBundles: ['reporting'], useServerState: true });
        expect(queries[0].forwardedProps.agently.payload).not.toHaveProperty('userId');
        subscription.close();
        expect(f.controls.size).toBe(2);
        f.transport.reset();
        await vi.waitFor(() => expect(f.controls.size).toBe(0));
        expect(f.posted.some(input => input.forwardedProps?.agently?.operation === 'run.cancel')).toBe(false);
        f.transport.reset();
    });

    it('preserves a submitted run across composer unmount and remount', async () => {
        const f = fixture();
        const first = f.transport.subscribe('thread', {});
        const admitted = await f.transport.query({ conversationId: 'thread', query: 'one' });
        const runId = [...f.controls.keys()][0];
        first.close();
        expect(f.controls.has(runId)).toBe(true);
        const replacement = f.transport.subscribe('thread', {});
        await f.transport.refresh('thread');
        expect(f.posted.filter(input => input.forwardedProps?.agently?.operation === 'chat')).toHaveLength(1);
        expect(admitted.turnId).toBe(`native-${runId}`);
        f.finish(runId);
        await vi.waitFor(() => expect(f.controls.size).toBe(0));
        replacement.close();
        f.transport.reset();
    });

    it('does not abort admission when the submitting view unmounts before response headers', async () => {
        let release!: () => void;
        let admissionSignal: AbortSignal | null | undefined;
        const waiting = new Promise<void>(resolve => { release = resolve; });
        const f = fixture([], async init => { admissionSignal = init?.signal; await waiting; });
        const view = f.transport.subscribe('thread', {});
        const query = f.transport.query({ conversationId: 'thread', query: 'one' });
        await vi.waitFor(() => expect(admissionSignal).toBeDefined());
        view.close();
        expect(admissionSignal?.aborted).toBe(false);
        release();
        const result = await query;
        expect(result.turnId).toMatch(/^native-/);
        expect(f.controls.size).toBe(1);
        f.transport.reset();
    });

    it('does not report a detached pre-admission submission as successful after account reset', async () => {
        let release!: () => void;
        let admissionSignal: AbortSignal | null | undefined;
        const waiting = new Promise<void>(resolve => { release = resolve; });
        const f = fixture([], async init => { admissionSignal = init?.signal; await waiting; });
        f.transport.subscribe('thread', {});
        const outcome = f.transport.query({ conversationId: 'thread', query: 'one' })
            .then(result => ({ result, error: undefined }), error => ({ result: undefined, error }));
        await vi.waitFor(() => expect(admissionSignal).toBeDefined());
        f.transport.reset();
        expect(admissionSignal?.aborted).toBe(true);
        release();
        const settled = await outcome;
        expect(settled.result).toBeUndefined();
        expect(settled.error).toBeInstanceOf(Error);
        expect(f.controls.size).toBe(0);
    });

    it('cold-loads only execution runs and attaches without original input', async () => {
        const f = fixture([
            { threadId: 'thread', runId: 'existing-chat', kind: 'chat', status: 'running', revision: 1, lastSequence: 2 },
            { threadId: 'thread', runId: 'goal-observer', kind: 'resource', status: 'running', revision: 1, lastSequence: 2 },
        ]);
        const snapshots = vi.fn();
        const subscription = f.transport.subscribe('thread', { onSnapshot: snapshots });
        await vi.waitFor(() => expect(f.controls.has('existing-chat')).toBe(true));
        expect(snapshots).toHaveBeenCalled();
        const attachments = f.posted.filter(input => input.forwardedProps?.agently?.operation === 'run.attach');
        expect(attachments.map(input => input.runId)).toEqual(['existing-chat']);
        expect(attachments[0].messages).toEqual([]);
        subscription.close();
        f.transport.reset();
    });

    it('invalidates subscriptions on account reset and leaves native cancellation explicit', async () => {
        const f = fixture();
        const events = vi.fn();
        f.transport.subscribe('thread', { onEvent: events });
        await f.transport.query({ conversationId: 'thread', query: 'one' });
        const before = events.mock.calls.length;
        f.transport.reset();
        await vi.waitFor(() => expect(f.controls.size).toBe(0));
        expect(events).toHaveBeenCalledTimes(before);
        expect(f.posted.some(input => input.forwardedProps?.agently?.operation === 'run.cancel')).toBe(false);
    });
});

it('relays configured browser tools through original AG-UI call IDs and durable parent continuation', async () => {
    const execute = vi.fn(async () => ({ content: '{"ok":true}' }));
    const tool = { name: 'device-safe_read', description: 'Fixture', parameters: { type: 'object' } };
    const browser = { tools: vi.fn(async () => [{ tool, execute }]), reset: vi.fn(), closeConversation: vi.fn(async () => {}) } as unknown as BrowserMCPHost;
    const f = fixture([], undefined, [], undefined, () => [], undefined, browser);
    const errors: string[] = []; f.transport.subscribe('thread', { onError: error => errors.push(error) });
    await f.transport.query({ conversationId: 'thread', query: 'Use the configured browser tool' });
    const chat = f.posted.find(input => input.forwardedProps?.agently?.operation === 'chat');
    expect(chat.tools).toEqual([tool]);
    const stream = f.controls.get(chat.runId)!;
    for (const event of [
        { type: 'TOOL_CALL_START', toolCallId: 'original-call', toolCallName: tool.name, parentMessageId: 'assistant' },
        { type: 'TOOL_CALL_ARGS', toolCallId: 'original-call', delta: '{}' },
        { type: 'TOOL_CALL_END', toolCallId: 'original-call' },
        { type: 'RUN_FINISHED', threadId: 'thread', runId: chat.runId, outcome: { type: 'success', pendingToolCallIds: ['original-call'] } },
    ]) stream.enqueue(frame(event));
    stream.close(); f.controls.delete(chat.runId);
    await vi.waitFor(() => expect(f.posted.some(input => input.parentRunId === chat.runId), JSON.stringify({ errors, requests: f.posted.map(input => ({runId:input.runId,parentRunId:input.parentRunId,operation:input.forwardedProps?.agently?.operation})),calls:execute.mock.calls.length })).toBe(true));
    const continuation = f.posted.find(input => input.parentRunId === chat.runId);
    expect(continuation.messages).toContainEqual(expect.objectContaining({ role: 'tool', toolCallId: 'original-call', content: '{"ok":true}' }));
    expect(continuation.tools).toEqual([tool]); expect(execute).toHaveBeenCalledOnce();
    f.finish(continuation.runId); f.transport.reset(); f.host.resetAgUiInteractions();
});
