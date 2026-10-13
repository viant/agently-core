import { describe, expect, it, vi } from 'vitest';
import { BrowserMCPHost, BrowserMCPSetupError, type BrowserMCPCatalog, type BrowserMCPConnection } from './browserMCP';
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
        const status = String(event.content.status);
        const type = status === 'queued' ? 'turn_queued' : status === 'completed' ? 'turn_completed'
            : status === 'failed' ? 'turn_failed' : status === 'canceled' ? 'turn_canceled' : 'turn_started';
        options.onViewEvent({ type,
            conversationId: options.conversationId, turnId: String(event.content.nativeTurnId) });
    },
} });

function fixture(active: Array<Record<string, unknown>> = [], beforeAdmission?: (init?: RequestInit) => Promise<void>, hostActivities: unknown[] = [], beforeBootstrap?: () => Promise<void>, bootstrapTurns: () => any[] = () => [], wireThreadId?: string, browserMCP?: BrowserMCPHost, initialNativeIdentity = true) {
    const posted: any[] = [];
    let metadataReads = 0;
    const bindingRequests: string[] = [];
    const controls = new Map<string, ReadableStreamDefaultController<Uint8Array>>();
    const fetchImpl = vi.fn(async (url: RequestInfo | URL, init?: RequestInit) => {
        if (String(url).includes('/conversations/')) {
            expect(init?.credentials).toBe('include');
            expect(init?.method).toBe('GET');
            metadataReads++;
            bindingRequests.push(String(url));
            const pathname = new URL(String(url), 'http://fixture.test').pathname;
            return new Response(JSON.stringify({ id: decodeURIComponent(pathname.split('/').at(-1)!), aguiThreadId: wireThreadId }), { headers: { 'Content-Type': 'application/json' } });
        }
        expect(String(url)).toBe('/v1/ag-ui/run');
        expect(init?.credentials).toBe('include');
        const input = JSON.parse(String(init?.body));
        posted.push(input);
        const start = { type: 'RUN_STARTED', threadId: input.threadId, runId: input.runId,
            ...(initialNativeIdentity ? { metadata: { agently: { identityVersion: '1', nativeTurnId: `native-${input.runId}` } } } : {}) };
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
    return { host, transport, posted, controls, finish, bindingRequests, metadataReads: () => metadataReads };
}

describe('native web conversation orchestration', () => {
    it.each(['completed', 'failed', 'canceled'])('keeps the streamed %s view without a completion bootstrap', async status => {
        const f = fixture();
        const onEvent = vi.fn();
        const onSnapshot = vi.fn();
        f.transport.subscribe('thread', { onEvent, onSnapshot });
        const admitted = await f.transport.query({ conversationId: 'thread', query: 'hello' });
        const runId = [...f.controls.keys()][0];
        const turnId = `native-${runId}`;
        const requestsBefore = f.posted.length;
        const snapshotsBefore = onSnapshot.mock.calls.length;
        expect(f.transport.hasStreamedTurnCompletion('thread', turnId)).toBe(false);
        expect(f.transport.observesTurn('thread', turnId)).toBe(true);
        expect(f.transport.observesTurn('thread', 'external-turn')).toBe(false);
        f.controls.get(runId)!.enqueue(frame({ type: 'ACTIVITY_SNAPSHOT', messageId: `activity-${runId}`,
            activityType: 'agently.turn', content: { version: '1', nativeTurnId: turnId, status } }));
        await vi.waitFor(() => expect(f.transport.hasStreamedTurnCompletion('thread', turnId)).toBe(true));
        f.finish(runId);
        // Let the run completion/finally callbacks settle before checking reads.
        await new Promise(resolve => setTimeout(resolve, 20));
        expect(admitted.turnId).toBe(turnId);
        expect(f.posted).toHaveLength(requestsBefore);
        expect(onSnapshot).toHaveBeenCalledTimes(snapshotsBefore);
        expect(f.transport.observesTurn('thread', turnId)).toBe(true);
        f.transport.reset(); f.host.resetAgUiInteractions();
        expect(f.transport.hasStreamedTurnCompletion('thread', turnId)).toBe(false);
        expect(f.transport.observesTurn('thread', turnId)).toBe(false);
    });

    it('still bootstraps protocol completion without an authoritative turn terminal event', async () => {
        const f = fixture();
        f.transport.subscribe('thread', {});
        await f.transport.query({ conversationId: 'thread', query: 'hello' });
        const runId = [...f.controls.keys()][0];
        const requestsBefore = f.posted.length;
        f.finish(runId);
        await vi.waitFor(() => expect(f.posted).toHaveLength(requestsBefore + 1));
        expect(f.posted.at(-1).forwardedProps.agently.operation).toBe('conversation.bootstrap');
        f.transport.reset(); f.host.resetAgUiInteractions();
    });

    it('resolves snapshot binding with an authenticated header-only read before compact bootstrap', async () => {
        const f = fixture([], undefined, [], undefined, () => [], 'opaque-wire');
        await f.transport.readSnapshot('thread', { mode: 'transcript', includeModelPayloads: false });
        expect(f.bindingRequests).toEqual(['/v1/conversations/thread?includeTranscript=false']);
        expect(f.posted).toHaveLength(1);
        expect(f.posted[0].threadId).toBe('opaque-wire');
        expect(f.posted[0].forwardedProps.agently.payload.includeModelPayloads).toBe(false);
        await f.transport.refresh('thread');
        expect(f.bindingRequests).toHaveLength(1);
        f.host.resetAgUiInteractions(); f.transport.reset();
    });
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

it('reports browser setup failure to the conversation before sending any model request', async () => {
    const browser = { tools: vi.fn(async () => { throw new Error('PRIVATE_TRANSPORT_DETAIL'); }), reset: vi.fn(), closeConversation: vi.fn(async () => {}) } as unknown as BrowserMCPHost;
    const f = fixture([], undefined, [], undefined, () => [], undefined, browser);
    const errors: string[] = [];
    f.transport.subscribe('thread', { onError: error => errors.push(error) });
    await expect(f.transport.query({ conversationId: 'thread', query: 'Use the configured browser tool' })).rejects.toThrow('Your message was not sent');
    expect(errors).toHaveLength(1);
    expect(errors[0]).toContain('Browser tools are not connected');
    expect(errors[0]).not.toContain('PRIVATE_TRANSPORT_DETAIL');
    expect(f.posted.filter(input => input.forwardedProps?.agently?.operation === 'chat')).toEqual([]);
    f.transport.reset(); f.host.resetAgUiInteractions();
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

it('keeps browser approval and active work across a composer remount, then closes idle ownership', async () => {
    let approve!: () => void;
    const ready = new Promise<[]>(resolve => { approve = () => resolve([]); });
    const tools = vi.fn(() => ready), closeConversation = vi.fn(async () => {});
    const browser = { tools, closeConversation, reset: vi.fn() } as unknown as BrowserMCPHost;
    const turns: any[] = [];
    const f = fixture([], undefined, [], undefined, () => turns, undefined, browser);
    const first = f.transport.subscribe('thread', {});
    const query = f.transport.query({conversationId:'thread', agentId:'coder', query:'Synthetic ordinary task'});
    await vi.waitFor(() => expect(tools).toHaveBeenCalledOnce());
    first.close();
    expect(closeConversation).not.toHaveBeenCalled();
    const replacement = f.transport.subscribe('thread', {});
    approve(); await query;
    const run = [...f.controls.keys()][0];
    replacement.close();
    expect(closeConversation).not.toHaveBeenCalled();
    turns.push({ turnId: `native-${run}`, status: 'completed' });
    f.finish(run);
    await vi.waitFor(() => expect(closeConversation).toHaveBeenCalledOnce());
    f.transport.reset(); f.host.resetAgUiInteractions();
});

it.each(['interrupted', 'completed', 'failed'])('retains the original browser catalog across a view replacement while protocol phase is %s and the native turn waits', async phase => {
    const tool = { name: 'device-safe_read', description: 'Fixture', parameters: { type: 'object' } };
    const execute = vi.fn(), closeConversation = vi.fn(async () => {}), reset = vi.fn();
    const browser = { tools: vi.fn(async () => [{ tool, execute }]), closeConversation, reset } as unknown as BrowserMCPHost;
    let turns: any[] = [];
    const f = fixture([], undefined, [], undefined, () => turns, undefined, browser, false);
    const first = f.transport.subscribe('thread', {});
    await f.transport.query({ conversationId: 'thread', query: 'Fixture approval task' });
    const chat = f.posted.find(input => input.forwardedProps?.agently?.operation === 'chat');
    const stream = f.controls.get(chat.runId)!;
    stream.enqueue(frame(phase === 'failed' ? { type: 'RUN_ERROR', message: 'Fixture protocol error' } : {
        type: 'RUN_FINISHED', threadId: 'thread', runId: chat.runId,
        outcome: phase === 'interrupted' ? { type: 'interrupt', interrupts: [{ id: 'approval', reason: 'agently.tool_approval',
            metadata: { agently: { version: '1', kind: 'tool-approval' } } }] } : { type: 'success' },
    }));
    stream.close(); f.controls.delete(chat.runId);
    await vi.waitFor(() => expect((f.transport as any).entries.get('thread').browserNativeTurns.size).toBe(1));
    first.close();
    expect(closeConversation).not.toHaveBeenCalled();
    const replacement = f.transport.subscribe('thread', {});
    replacement.close();
    expect(closeConversation).not.toHaveBeenCalled();
    expect(execute).not.toHaveBeenCalled();
    expect(f.posted.filter(input => input.forwardedProps?.agently?.operation === 'chat')).toHaveLength(1);
    turns = [{ turnId: `native-${chat.runId}`, status: 'completed' }];
    await f.transport.reconcile('thread');
    expect(closeConversation).toHaveBeenCalledOnce();
    f.transport.reset();
    expect(reset).toHaveBeenCalledOnce();
    f.host.resetAgUiInteractions();
});

it.each(['executor', 'displayAlias'])('retains the original catalog when success with pending calls fails before continuation (%s)', async failure => {
    const tool = { name: 'device-safe_read', description: 'Fixture', parameters: { type: 'object' } };
    const execute = vi.fn(async () => { throw new Error('Fixture execution unavailable'); });
    const closeConversation = vi.fn(async () => {});
    const browser = { tools: vi.fn(async () => [{ tool, execute }]), closeConversation, reset: vi.fn() } as unknown as BrowserMCPHost;
    let turns: any[] = [];
    const f = fixture([], undefined, [], undefined, () => turns, undefined, browser);
    const errors: string[] = [];
    const subscription = f.transport.subscribe('thread', { onError: error => errors.push(error) });
    await f.transport.query({ conversationId: 'thread', query: 'Fixture original input' });
    const chat = f.posted.find(input => input.forwardedProps?.agently?.operation === 'chat');
    const stream = f.controls.get(chat.runId)!;
    for (const event of [
        { type: 'TOOL_CALL_START', toolCallId: 'original-call', toolCallName: failure === 'displayAlias' ? 'device/safe_read' : tool.name, parentMessageId: 'assistant' },
        { type: 'TOOL_CALL_ARGS', toolCallId: 'original-call', delta: '{}' },
        { type: 'TOOL_CALL_END', toolCallId: 'original-call' },
        { type: 'RUN_FINISHED', threadId: 'thread', runId: chat.runId, outcome: { type: 'success', pendingToolCallIds: ['original-call'] } },
    ]) stream.enqueue(frame(event));
    stream.close(); f.controls.delete(chat.runId);
    await vi.waitFor(() => expect(errors).toEqual([failure === 'displayAlias' ? 'No client handler for tool: device/safe_read' : 'Fixture execution unavailable']));
    subscription.close();
    expect(closeConversation).not.toHaveBeenCalled();
    expect(execute).toHaveBeenCalledTimes(failure === 'displayAlias' ? 0 : 1);
    expect(f.posted.some(input => input.parentRunId === chat.runId)).toBe(false);
    expect((performance.getEntriesByName('agently.browserMCP.lifecycle.settled').at(-1) as PerformanceMark | undefined)?.detail).toMatchObject({ phase: 'completed', nativeIdentity: true, retained: true, pendingCount: 1 });
    expect((performance.getEntriesByName('agently.browserMCP.lifecycle.continuation').at(-1) as PerformanceMark | undefined)?.detail).toMatchObject({ stage: 'error', pendingCount: 1, failure: failure === 'displayAlias' ? 'missingClientHandler' : 'executionFailed' });
    expect((performance.getEntriesByName('agently.browserMCP.lifecycle.aliases').at(-1) as PerformanceMark | undefined)?.detail).toMatchObject({ pendingCount: 1, exactCount: failure === 'displayAlias' ? 0 : 1, canonicalCount: 1, slashCount: failure === 'displayAlias' ? 1 : 0, dashCount: failure === 'displayAlias' ? 0 : 1 });
    turns = [{ turnId: `native-${chat.runId}`, status: 'canceled' }];
    await f.transport.reconcile('thread');
    expect(closeConversation).toHaveBeenCalledOnce();
    f.transport.reset(); f.host.resetAgUiInteractions();
});

it('continues all original browser interrupts with terminal transport failures instead of stranding the turn', async () => {
    let catalog!: BrowserMCPCatalog;
    let connection!: BrowserMCPConnection;
    const request = vi.fn(async (method: string) => {
        if (method === 'initialize') return {};
        if (method === 'tools/list') return { tools: [{ name: 'safe_read', inputSchema: { type: 'object' } }] };
        connection.onclose?.();
        throw new BrowserMCPSetupError('connectionLost', 'unknown');
    });
    const revoke = vi.fn(async () => {});
    const browser = new BrowserMCPHost({
        descriptors: async () => [{ name: 'device', executionLocation: 'browser', transport: { type: 'chrome-extension', extensionId: 'a'.repeat(32), portName: 'fixture' }, allowedTools: ['safe_read'] }],
        register: async input => {
            catalog = { ...input, id: 'catalog', hash: 'hash', expiresAt: new Date(Date.now() + 60000).toISOString(), tools: [{ name: 'device-safe_read', description: 'Read fixture', parameters: { type: 'object' }, metadata: { browserMCP: { catalogId: 'catalog', connectionId: input.connectionId, server: input.server, tool: 'safe_read' } } }] };
            return catalog;
        }, current: async () => catalog, revoke,
    }, { transportFactory: () => connection = { request, close: vi.fn() } });
    const f = fixture([], undefined, [], undefined, () => [], undefined, browser);
    const errors: string[] = [];
    f.transport.subscribe('thread', { onError: error => errors.push(error) });
    await f.transport.query({ conversationId: 'thread', query: 'Fixture original input' });
    const chat = f.posted.find(input => input.forwardedProps?.agently?.operation === 'chat');
    const stream = f.controls.get(chat.runId)!;
    for (let i = 1; i <= 3; i++) {
        stream.enqueue(frame({ type: 'TOOL_CALL_START', toolCallId: `original-${i}`, toolCallName: 'device-safe_read', parentMessageId: 'assistant' }));
        stream.enqueue(frame({ type: 'TOOL_CALL_ARGS', toolCallId: `original-${i}`, delta: '{}' }));
        stream.enqueue(frame({ type: 'TOOL_CALL_END', toolCallId: `original-${i}` }));
    }
    stream.enqueue(frame({ type: 'RUN_FINISHED', threadId: 'thread', runId: chat.runId, outcome: { type: 'interrupt', interrupts: [1, 2, 3].map(i => ({ id: `interrupt-${i}`, toolCallId: `original-${i}`, reason: 'agently.client_tool', metadata: { agently: { version: '1', kind: 'client-tool' } } })) } }));
    stream.close(); f.controls.delete(chat.runId);
    await vi.waitFor(() => expect(f.posted.some(input => input.parentRunId === chat.runId)).toBe(true));
    const continuation = f.posted.find(input => input.parentRunId === chat.runId);
    expect(errors).toEqual([]);
    expect(continuation.resume.map((entry: any) => entry.interruptId)).toEqual(['interrupt-1', 'interrupt-2', 'interrupt-3']);
    expect(continuation.resume.map((entry: any) => JSON.parse(entry.payload.content).dispatchState)).toEqual(['unknown', 'notDispatched', 'notDispatched']);
    for (const entry of continuation.resume) {
        expect(entry.status).toBe('resolved'); expect(entry.payload.error).toBeTruthy();
        expect(entry.metadata).toEqual(catalog.tools[0].metadata);
    }
    expect(continuation.tools).toEqual(chat.tools);
    for (const phase of ['handoff.pendingToHandler', 'handoff.terminalToHandler', 'handoff.handlers']) {
        const entries = performance.getEntriesByName(`agently.browserMCP.${phase}`);
        expect(entries).toHaveLength(1);
        expect(entries[0].duration).toBeGreaterThanOrEqual(0);
        expect((entries[0] as PerformanceMeasure).detail).toBeNull();
    }
    expect(request.mock.calls.filter(([method]) => method === 'tools/call')).toHaveLength(1);
    expect(revoke).not.toHaveBeenCalled();
    f.finish(continuation.runId); f.transport.reset(); f.host.resetAgUiInteractions();
});

async function browserContinuationFixture(execute = vi.fn(async () => ({ content: 'fixture result' }))) {
    const tool = { name: 'device-safe_read', description: 'Fixture', parameters: { type: 'object' } };
    const browser = { tools: vi.fn(async () => [{ tool, execute }]), reset: vi.fn(), closeConversation: vi.fn(async () => {}) } as unknown as BrowserMCPHost;
    let turns: any[] = [];
    const f = fixture([], undefined, [], undefined, () => turns, undefined, browser);
    const errors: string[] = [];
    f.transport.subscribe('thread', { onError: error => errors.push(error) });
    await f.transport.query({ conversationId: 'thread', query: 'Generic continuation fixture' });
    const chat = f.posted.find(input => input.forwardedProps?.agently?.operation === 'chat');
    const completion: Promise<unknown> = (f.transport as any).entries.get('thread').runs.get(chat.runId).completion;
    const finishCall = (runId: string, id: string, mode: 'messages' | 'interrupts' = 'messages') => {
        const stream = f.controls.get(runId)!;
        for (const event of [
            { type: 'TOOL_CALL_START', toolCallId: id, toolCallName: tool.name, parentMessageId: `assistant-${id}` },
            { type: 'TOOL_CALL_ARGS', toolCallId: id, delta: '{}' },
            { type: 'TOOL_CALL_END', toolCallId: id },
            { type: 'RUN_FINISHED', threadId: 'thread', runId, outcome: mode === 'messages' ? { type: 'success', pendingToolCallIds: [id] }
                : { type: 'interrupt', interrupts: [{ id: `interrupt-${id}`, toolCallId: id, reason: 'agently.client_tool', metadata: { agently: { version: '1', kind: 'client-tool' } } }] } },
        ]) stream.enqueue(frame(event));
        stream.close(); f.controls.delete(runId);
    };
    const next = async (runId: string) => {
        await vi.waitFor(() => expect(f.posted.some(input => input.parentRunId === runId), JSON.stringify({ errors, calls: execute.mock.calls.length })).toBe(true));
        return f.posted.find(input => input.parentRunId === runId);
    };
    const dispose = () => { f.transport.reset(); f.host.resetAgUiInteractions(); };
    return { ...f, browser, tool, execute, errors, chat, completion, finishCall, next, dispose, setTurns: (value: any[]) => { turns = value; } };
}

it.each(['messages', 'interrupts'] as const)('continues forty distinct original browser calls via %s with identical arguments and one cached tool registry', async mode => {
    const f = await browserContinuationFixture();
    try {
        let run = f.chat;
        for (let i = 0; i < 40; i++) {
            const id = `original-call-${i}`;
            f.finishCall(run.runId, id, mode);
            run = await f.next(run.runId);
            if (mode === 'messages') expect(run.messages).toContainEqual(expect.objectContaining({ role: 'tool', toolCallId: id, content: 'fixture result' }));
            else expect(run.resume).toContainEqual(expect.objectContaining({ interruptId: `interrupt-${id}`, status: 'resolved', payload: { content: 'fixture result' } }));
            expect(run.tools).toEqual([f.tool]);
        }
        f.finish(run.runId);
        await f.completion;
        expect(f.execute).toHaveBeenCalledTimes(40);
        expect(new Set(f.execute.mock.calls.map(call => (call as any[])[1].id)).size).toBe(40);
        expect(f.browser.tools).toHaveBeenCalledOnce();
        expect(f.errors).toEqual([]);
    } finally { f.dispose(); }
});

it('leaves a human approval after forty browser continuations to the normal interrupt flow', async () => {
    const f = await browserContinuationFixture();
    try {
        let run = f.chat;
        for (let i = 0; i < 40; i++) {
            f.finishCall(run.runId, `original-call-${i}`, 'interrupts');
            run = await f.next(run.runId);
        }
        const before = f.posted.length, stream = f.controls.get(run.runId)!;
        stream.enqueue(frame({ type: 'RUN_FINISHED', threadId: 'thread', runId: run.runId, outcome: { type: 'interrupt', interrupts: [
            { id: 'human-approval', reason: 'agently.tool_approval', metadata: { agently: { version: '1', kind: 'tool-approval' } } },
        ] } }));
        stream.close(); f.controls.delete(run.runId);
        await f.completion;
        expect(f.posted).toHaveLength(before);
        expect(f.execute).toHaveBeenCalledTimes(40);
        expect(f.errors).toEqual([]);
    } finally { f.dispose(); }
});

it('does not resume after account disposal while a browser handler is finishing', async () => {
    let release!: () => void;
    const pending = new Promise<void>(resolve => { release = resolve; });
    const execute = vi.fn(async () => { await pending; return { content: 'fixture result' }; });
    const f = await browserContinuationFixture(execute);
    try {
        f.finishCall(f.chat.runId, 'original-call');
        await vi.waitFor(() => expect(execute).toHaveBeenCalledOnce());
        f.transport.reset();
        release();
        await f.completion;
        expect(f.posted.some(input => input.parentRunId === f.chat.runId)).toBe(false);
        expect(execute).toHaveBeenCalledOnce();
    } finally { release(); f.dispose(); }
});

it('does not resume a backend-canceled native turn while a browser handler is finishing', async () => {
    let release!: () => void;
    const pending = new Promise<void>(resolve => { release = resolve; });
    const execute = vi.fn(async () => { await pending; return { content: 'fixture result' }; });
    const f = await browserContinuationFixture(execute);
    try {
        f.finishCall(f.chat.runId, 'original-call');
        await vi.waitFor(() => expect(execute).toHaveBeenCalledOnce());
        f.setTurns([{ turnId: `native-${f.chat.runId}`, status: 'canceled' }]);
        await f.transport.reconcile('thread');
        release();
        await f.completion;
        expect(f.posted.some(input => input.parentRunId === f.chat.runId)).toBe(false);
        expect(execute).toHaveBeenCalledOnce();
        expect(f.errors).toEqual([]);
    } finally { release(); f.dispose(); }
});

it('refuses a repeated original pending call even when resume replaces the prior result messages', async () => {
    const f = await browserContinuationFixture();
    try {
        f.finishCall(f.chat.runId, 'original-call');
        const run = await f.next(f.chat.runId), stream = f.controls.get(run.runId)!;
        stream.enqueue(frame({ type: 'MESSAGES_SNAPSHOT', messages: [
            { id: 'repeated-assistant', role: 'assistant', content: '', toolCalls: [{ id: 'original-call', type: 'function', function: { name: f.tool.name, arguments: '{}' } }] },
        ] }));
        stream.enqueue(frame({ type: 'RUN_FINISHED', threadId: 'thread', runId: run.runId, outcome: { type: 'success', pendingToolCallIds: ['original-call'] } }));
        stream.close(); f.controls.delete(run.runId);
        await vi.waitFor(() => expect(f.errors).toContain('Browser MCP continuation repeated an original pending call'));
        await f.completion;
        expect(f.execute).toHaveBeenCalledOnce();
        expect(f.posted.some(input => input.parentRunId === run.runId)).toBe(false);
    } finally { f.dispose(); }
});
