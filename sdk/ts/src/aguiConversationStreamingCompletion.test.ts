import { describe, expect, it, vi } from 'vitest';
import type { BrowserMCPHost } from './browserMCP';
import { AgentlyClient } from './client';
import { AgUiConversationTransport } from './aguiConversationTransport';
import { AgUiViewProjection } from './aguiViewProjection';
import { applyEvent, newConversationState } from './chatStore/reducer';

const frame = (event: unknown) => new TextEncoder().encode(`data: ${JSON.stringify(event)}\n\n`);
const metadata = { agently: { presentation: { version: '1', conversationId: 'thread', nativeTurnId: 'native-turn', pageId: 'page', nativeMessageId: 'answer' } } };
const lifecycle = (status: string, extra: object = {}) => ({ type: 'ACTIVITY_SNAPSHOT', messageId: 'native-turn/status', activityType: 'agently.turn', content: { version: '1', nativeTurnId: 'native-turn', status, queueSequence: '0' }, metadata, ...extra });

function fixture(browser?: BrowserMCPHost) {
    const posted: any[] = [], views: any[] = [], snapshots: any[] = [], hostActivities: any[] = [], outcomes: any[] = [], errors: any[] = [];
    let controller!: ReadableStreamDefaultController<Uint8Array>;
    let state = newConversationState('thread');
    const saved = { conversationId: 'thread', turns: [{ turnId: 'native-turn', status: 'completed', messages: [{ messageId: 'answer', role: 'assistant', content: 'public answer' }] }] };
    const host = new AgentlyClient({ baseURL: '/v1', useCookies: true, fetchImpl: async (url, init) => {
        if (String(url).includes('/conversations/')) return new Response(JSON.stringify({ id: 'thread' }));
        const input = JSON.parse(String(init?.body)); posted.push(input);
        const started = { type: 'RUN_STARTED', threadId: 'thread', runId: input.runId, metadata: { agently: { identityVersion: '1', nativeTurnId: 'native-turn' } } };
        if (input.forwardedProps?.agently?.operation === 'conversation.bootstrap') {
            const result = { version: '1', threadId: 'thread', transcript: { schemaVersion: '2', conversation: posted.some(p => p.forwardedProps?.agently?.operation !== 'conversation.bootstrap') ? saved : { conversationId: 'thread', turns: [] } }, messages: [], state: {}, runs: [], hostActivities: [], unavailableHostActivityIds: [], projection: { lossless: true, unavailableMessageIds: [] } };
            return new Response(new Blob([frame(started), frame({ type: 'RUN_FINISHED', threadId: 'thread', runId: input.runId, result })]), { headers: { 'content-type': 'text/event-stream' } });
        }
        return new Response(new ReadableStream({ start(c) { controller = c; c.enqueue(frame(started)); c.enqueue(frame(lifecycle('running'))); } }), { headers: { 'content-type': 'text/event-stream' } });
    } });
    const transport = new AgUiConversationTransport(host, options => new AgUiViewProjection(options), browser);
    const subscription = transport.subscribe('thread', {
        onEvent: event => { views.push(event); state = applyEvent(state, event); },
        onSnapshot: snapshot => snapshots.push(snapshot), onHostActivities: items => hostActivities.push(items),
        onOutcome: value => outcomes.push(value), onError: value => errors.push(value),
    });
    const bootstraps = () => posted.filter(p => p.forwardedProps?.agently?.operation === 'conversation.bootstrap');
    const send = (event: object) => controller.enqueue(frame(event));
    const terminal = (status: string, outcome?: object) => {
        const runId = posted.filter(p => p.forwardedProps?.agently?.operation !== 'conversation.bootstrap').at(-1).runId;
        if (status === 'failed') send({ type: 'RUN_ERROR', message: 'synthetic failure', code: 'AGENT_ERROR' });
        else send({ type: 'RUN_FINISHED', threadId: 'thread', runId, outcome: outcome ?? { type: status === 'canceled' ? 'cancelled' : 'success' } });
        controller.close();
    };
    const settled = async () => { await vi.waitFor(() => expect(!!outcomes.length && ['success', 'error', 'cancelled'].includes(outcomes.at(-1).phase) && !outcomes.at(-1).pendingToolCallIds?.length).toBe(true)); await new Promise(resolve => setTimeout(resolve, 0)); };
    return { transport, posted, views, snapshots, hostActivities, errors, send, terminal, settled, bootstraps, state: () => state,
        close: () => { subscription.close(); transport.reset(); host.resetAgUiInteractions(); }, disconnect: () => controller.error(new Error('synthetic disconnected stream')) };
}

describe('stream-owned conversation completion', () => {
    it.each(['completed', 'failed', 'canceled'])('uses streamed native %s and protocol terminal without a completion bootstrap', async status => {
        const f = fixture();
        try {
            await f.transport.query({ conversationId: 'thread', query: 'public synthetic question' });
            const initial = f.bootstraps().length;
            f.send({ type: 'TEXT_MESSAGE_START', messageId: 'answer', role: 'assistant', metadata });
            f.send({ type: 'TEXT_MESSAGE_CONTENT', messageId: 'answer', delta: 'public answer', metadata });
            f.send({ type: 'TEXT_MESSAGE_END', messageId: 'answer' });
            f.send({ type: 'TOOL_CALL_START', toolCallId: 'capture', toolCallName: 'public.capture', parentMessageId: 'answer', metadata });
            f.send({ type: 'TOOL_CALL_ARGS', toolCallId: 'capture', delta: '{}' }); f.send({ type: 'TOOL_CALL_END', toolCallId: 'capture' });
            f.send({ type: 'TOOL_CALL_RESULT', toolCallId: 'capture', messageId: 'capture-result', content: JSON.stringify({ resources: [{ uri: 'scratchpad://public/screenshot.png', mimeType: 'image/png' }] }), metadata });
            f.send({ type: 'ACTIVITY_SNAPSHOT', messageId: 'answer/activity', activityType: 'agently.rendered-content', content: { version: '1', renderedContent: { parts: [{ kind: 'data', data: { id: 'image-reference', payload: { uri: 'scratchpad://public/screenshot.png' } } }] } }, metadata });
            f.send({ type: 'ACTIVITY_SNAPSHOT', messageId: 'host-artifact', activityType: 'mcp-apps', content: { resourceUri: 'ui://public/artifact' } });
            f.send(lifecycle(status)); f.terminal(status); await f.settled();
            expect(f.bootstraps()).toHaveLength(initial);
            expect(f.views.some(e => e.type === 'text_delta' && e.content === 'public answer')).toBe(true);
            expect(f.views.some(e => e.type === 'tool_call_completed' && e.responsePayload?.resources?.[0]?.uri === 'scratchpad://public/screenshot.png')).toBe(true);
            expect(f.views.some(e => e.renderedContent?.parts?.[0]?.data?.payload?.uri === 'scratchpad://public/screenshot.png')).toBe(true);
            expect(f.hostActivities.at(-1)).toEqual(expect.arrayContaining([expect.objectContaining({ id: 'host-artifact' })]));
            expect(JSON.stringify(f.state())).toContain('public answer'); expect(JSON.stringify(f.state())).toContain('scratchpad://public/screenshot.png');
            await f.transport.refresh('thread');
            expect(f.bootstraps()).toHaveLength(initial + 1); expect(f.bootstraps().at(-1).forwardedProps.agently.payload.includeModelPayloads).toBe(false);
            expect(f.snapshots.at(-1).turns[0].turnId).toBe('native-turn');
        } finally { f.close(); }
    });

    it.each(['missing', 'foreign', 'child', 'invalid'])('reconciles one compact snapshot when native terminal is %s', async kind => {
        const f = fixture();
        try {
            await f.transport.query({ conversationId: 'thread', query: 'public synthetic question' }); const initial = f.bootstraps().length;
            if (kind === 'foreign') f.send({ ...lifecycle('completed'), content: { version: '1', nativeTurnId: 'different-turn', status: 'completed', queueSequence: '0' } });
            if (kind === 'child') f.send(lifecycle('completed', { subagentRunId: 'child-run' }));
            if (kind === 'invalid') f.send({ ...lifecycle('completed'), content: { nativeTurnId: 'native-turn', status: 'completed' } });
            f.terminal('completed'); await f.settled();
            await vi.waitFor(() => expect(f.bootstraps()).toHaveLength(initial + 1));
            expect(f.bootstraps().at(-1).forwardedProps.agently.payload.includeModelPayloads).toBe(false);
            expect(f.posted.filter(p => p.forwardedProps?.agently?.operation !== 'conversation.bootstrap')).toHaveLength(1);
        } finally { f.close(); }
    });

    it('keeps native waiting and original browser calls through a handoff, then uses the final streamed terminal', async () => {
        let release!: () => void;
        const waiting = new Promise<void>(resolve => { release = resolve; });
        const execute = vi.fn(async () => { await waiting; return { content: '{"resources":[{"uri":"scratchpad://public/result.png"}]}' }; });
        const tool = { name: 'public-capture', description: 'Public synthetic capture', parameters: { type: 'object' } };
        const closeConversation = vi.fn(async () => {});
        const browser = { tools: vi.fn(async () => [{ tool, execute }]), closeConversation, reset: vi.fn() } as unknown as BrowserMCPHost;
        const f = fixture(browser);
        try {
            await f.transport.query({ conversationId: 'thread', query: 'public synthetic capture' }); const initial = f.bootstraps().length;
            const original = f.posted.find(p => p.forwardedProps?.agently?.operation !== 'conversation.bootstrap');
            f.send({ type: 'TOOL_CALL_START', toolCallId: 'original-call', toolCallName: tool.name, parentMessageId: 'answer' });
            f.send({ type: 'TOOL_CALL_ARGS', toolCallId: 'original-call', delta: '{}' }); f.send({ type: 'TOOL_CALL_END', toolCallId: 'original-call' });
            f.terminal('completed', { type: 'success', pendingToolCallIds: ['original-call'] });
            await vi.waitFor(() => expect(execute).toHaveBeenCalledOnce());
            expect(f.bootstraps()).toHaveLength(initial); expect(closeConversation).not.toHaveBeenCalled();
            release(); await vi.waitFor(() => expect(f.posted.filter(p => p.forwardedProps?.agently?.operation !== 'conversation.bootstrap')).toHaveLength(2));
            const continued = f.posted.filter(p => p.forwardedProps?.agently?.operation !== 'conversation.bootstrap').at(-1);
            expect(continued.parentRunId).toBe(original.runId);
            expect(continued.messages).toContainEqual(expect.objectContaining({ role: 'tool', toolCallId: 'original-call', content: '{"resources":[{"uri":"scratchpad://public/result.png"}]}' }));
            f.send(lifecycle('completed')); f.terminal('completed'); await f.settled();
            expect(f.bootstraps()).toHaveLength(initial); expect(execute).toHaveBeenCalledOnce();
        } finally { release(); f.close(); }
    });

    it('retains one compact reconciliation for an interrupted native turn', async () => {
        const f = fixture();
        try {
            await f.transport.query({ conversationId: 'thread', query: 'public synthetic question' }); const initial = f.bootstraps().length;
            f.terminal('completed', { type: 'interrupt', interrupts: [{ id: 'external-interrupt', reason: 'approval' }] });
            await vi.waitFor(() => expect(f.bootstraps()).toHaveLength(initial + 1));
            expect(f.bootstraps().at(-1).forwardedProps.agently.payload.includeModelPayloads).toBe(false);
            expect(f.posted.filter(p => p.forwardedProps?.agently?.operation !== 'conversation.bootstrap')).toHaveLength(1);
        } finally { f.close(); }
    });

    it.each([false, true])('reconciles a disconnected stream without protocol completion (native terminal: %s)', async nativeEnded => {
        const f = fixture();
        try {
            await f.transport.query({ conversationId: 'thread', query: 'public synthetic question' }); const initial = f.bootstraps().length;
            if (nativeEnded) f.send(lifecycle('completed'));
            f.disconnect(); await vi.waitFor(() => expect(f.bootstraps()).toHaveLength(initial + 1));
            expect(f.bootstraps().at(-1).forwardedProps.agently.payload.includeModelPayloads).toBe(false);
            expect(f.posted.filter(p => p.forwardedProps?.agently?.operation !== 'conversation.bootstrap')).toHaveLength(1);
        } finally { f.close(); }
    });
});
