import { describe, expect, it, vi } from 'vitest';
import type { RunAgentInput } from '@ag-ui/core';
import { EventSchema } from '@ag-ui/core/schemas';
import { AgUiSession } from './aguiSession';

function stream(events: unknown[]) {
    return new Response(events.map(event => `data: ${JSON.stringify(EventSchema.parse(event))}\n\n`).join(''), {
        headers: { 'Content-Type': 'text/event-stream' },
    });
}

function resultEvents(input: RunAgentInput) {
    return [
        { type: 'RUN_STARTED', threadId: input.threadId, runId: input.runId },
        { type: 'STATE_SNAPSHOT', snapshot: { selected: 'report' } },
        { type: 'ACTIVITY_SNAPSHOT', messageId: 'report', activityType: 'external.report', content: { rows: [1, 2] } },
        { type: 'TEXT_MESSAGE_START', messageId: 'answer', role: 'assistant' },
        { type: 'TEXT_MESSAGE_CONTENT', messageId: 'answer', delta: 'Hello 🌍' },
        { type: 'TEXT_MESSAGE_END', messageId: 'answer' },
        { type: 'RUN_FINISHED', threadId: input.threadId, runId: input.runId },
    ];
}

describe('portable AG-UI conversation sessions', () => {
    it('keeps standard reduction authoritative without sending Agently commands to other backends', async () => {
        const posts: RunAgentInput[] = [];
        const session = new AgUiSession({
            connectionId: 'independent', threadId: 'thread', url: '/bff/independent/run',
            fetch: async (_url, init) => {
                const input = JSON.parse(String(init.body)); posts.push(input);
                return stream(resultEvents(input));
            },
        });
        const listener = vi.fn();
        const before = session.getSnapshot();
        expect(session.getSnapshot()).toBe(before);
        const unsubscribe = session.subscribe(listener);
        await session.send({ id: 'user', role: 'user', content: [{ type: 'text', text: 'Hello' }], metadata: { keep: true } }, { runId: 'run' });
        expect(posts).toHaveLength(1);
        expect(posts[0].forwardedProps).not.toHaveProperty('agently');
        expect(posts[0].messages[0]).toMatchObject({ content: [{ type: 'text', text: 'Hello' }], metadata: { keep: true } });
        const snapshot = session.getSnapshot();
        expect(snapshot.phase).toBe('completed');
        expect(snapshot.messages.find(message => message.id === 'answer')).toMatchObject({ content: 'Hello 🌍' });
        expect(snapshot.messages.find(message => message.id === 'report')).toMatchObject({ role: 'activity', content: { rows: [1, 2] } });
        expect(snapshot.state).toEqual({ selected: 'report' });
        expect(listener).toHaveBeenCalled();
        expect(session.getSnapshot()).toBe(snapshot);
        unsubscribe();
    });

    it('rejects protocol RUN_ERROR although upstream resolves the stream promise', async () => {
        const session = new AgUiSession({ connectionId: 'other', threadId: 'thread', url: '/run', fetch: async () => stream([
            { type: 'RUN_STARTED', threadId: 'thread', runId: 'run' },
            { type: 'RUN_ERROR', message: 'Tool failed', code: 'TOOL_FAILED' },
        ]) });
        await expect(session.send({ id: 'user', role: 'user', content: 'hi' }, { runId: 'run' }))
            .rejects.toMatchObject({ name: 'AgUiRunError', code: 'TOOL_FAILED' });
        expect(session.getSnapshot().phase).toBe('failed');
    });

    it('restores the original POST in a fresh session without creating another run or duplicating text', async () => {
        const posts: RunAgentInput[] = [];
        const options = { connectionId: 'agently', threadId: 'thread', url: '/run', durableReplay: true,
            fetch: async (_url: string, init: RequestInit) => {
                const input = JSON.parse(String(init.body)); posts.push(input);
                return stream(resultEvents(input));
            },
        };
        const first = new AgUiSession(options);
        await first.send({ id: 'user', role: 'user', content: 'hi' }, { runId: 'accepted-run' });
        const checkpoint = first.checkpoint()!;
        const restored = new AgUiSession(options);
        await restored.reconnect(checkpoint);
        expect(posts[1]).toEqual(posts[0]);
        expect(restored.getSnapshot().messages.filter(message => message.id === 'answer')).toHaveLength(1);
        expect(restored.getSnapshot().messages).toEqual(first.getSnapshot().messages);
        expect(restored.getSnapshot().runId).toBe('accepted-run');
        expect(checkpoint.input.runId).toBe('accepted-run');
    });

    it('rejects replay without advertised support and across backend/thread identities before any request', async () => {
        const fetch = vi.fn();
        const checkpoint = { version: '1' as const, connectionId: 'one', input: {
            threadId: 'thread', runId: 'run', messages: [], state: {}, tools: [], context: [], forwardedProps: {},
        } };
        const unsupported = new AgUiSession({ connectionId: 'one', threadId: 'thread', url: '/run', fetch });
        expect(() => unsupported.reconnect(checkpoint)).toThrow('advertised durable replay');
        const otherBackend = new AgUiSession({ connectionId: 'two', threadId: 'thread', url: '/run', fetch, durableReplay: true });
        expect(() => otherBackend.reconnect(checkpoint)).toThrow('another backend');
        const otherThread = new AgUiSession({ connectionId: 'one', threadId: 'different', url: '/run', fetch, durableReplay: true });
        expect(() => otherThread.reconnect(checkpoint)).toThrow('another thread');
        expect(fetch).not.toHaveBeenCalled();
    });

    it('only sends execution-selection extensions for an explicit Agently profile', async () => {
        const posts: RunAgentInput[] = [];
        const fetch = async (_url: string, init: RequestInit) => {
            const input = JSON.parse(String(init.body)); posts.push(input); return stream(resultEvents(input));
        };
        const generic = new AgUiSession({ connectionId: 'other', threadId: 'thread', url: '/run', fetch });
        await expect(generic.send({ id: 'u', role: 'user', content: 'hi' }, {}, { agentId: 'steward' })).rejects.toThrow('Agently profile');
        expect(posts).toHaveLength(0);
        const agently = new AgUiSession({ connectionId: 'agently', profile: 'agently', threadId: 'thread', url: '/run', fetch });
        await agently.send({ id: 'u', role: 'user', content: 'hi' }, { runId: 'r' }, { agentId: 'steward' });
        expect(posts[0].forwardedProps).toEqual({ agently: { version: '1', operation: 'chat', payload: { agentId: 'steward' } } });
    });

    it('retains interrupts and resumes with a new run instead of another user message', async () => {
        const posts: RunAgentInput[] = [];
        const session = new AgUiSession({ connectionId: 'other', threadId: 'thread', url: '/run', fetch: async (_url, init) => {
            const input = JSON.parse(String(init.body)); posts.push(input);
            return stream([
                { type: 'RUN_STARTED', threadId: 'thread', runId: input.runId },
                { type: 'RUN_FINISHED', threadId: 'thread', runId: input.runId,
                    outcome: posts.length === 1 ? { type: 'interrupt', interrupts: [{ id: 'approval', reason: 'approval', metadata: { question: 'Proceed?' } }] } : { type: 'success' } },
            ]);
        } });
        await session.send({ id: 'u', role: 'user', content: 'hi' }, { runId: 'r1' });
        expect(session.getSnapshot().phase).toBe('interrupted');
        expect(session.getSnapshot().interrupts[0].id).toBe('approval');
        await session.resume({ approval: { status: 'resolved', payload: true } }, { runId: 'r2' });
        expect(posts[1].resume).toEqual([{ interruptId: 'approval', status: 'resolved', payload: true }]);
        expect(posts[1].messages).toHaveLength(1);
        expect(session.getSnapshot().phase).toBe('completed');
        expect(session.getSnapshot().interrupts).toEqual([]);
    });

    it('detaches navigation without sending cancellation or accepting a concurrent turn', async () => {
        const posts: RunAgentInput[] = [];
        let connected = false;
        const session = new AgUiSession({ connectionId: 'agently', threadId: 'thread', url: '/run', fetch: async (_url, init) => {
            const input = JSON.parse(String(init.body)); posts.push(input);
            return new Response(new ReadableStream<Uint8Array>({
                start(controller) {
                    const event = { type: 'RUN_STARTED', threadId: input.threadId, runId: input.runId };
                    controller.enqueue(new TextEncoder().encode(`data: ${JSON.stringify(event)}\n\n`));
                    init.signal?.addEventListener('abort', () => controller.error(new DOMException('Detached', 'AbortError')), { once: true });
                    connected = true;
                },
            }), { headers: { 'Content-Type': 'text/event-stream' } });
        } });
        const pending = session.send({ id: 'u', role: 'user', content: 'hello' }, { runId: 'run' });
        const outcome = pending.catch(error => error);
        await vi.waitFor(() => expect(connected).toBe(true));
        await expect(session.send({ id: 'another', role: 'user', content: 'second' })).rejects.toThrow('already active');
        session.detach();
        await outcome;
        expect(session.getSnapshot().phase).toBe('detached');
        expect(posts).toHaveLength(1);
        expect(posts[0].forwardedProps).not.toHaveProperty('agently');
        expect(session.checkpoint()?.input.runId).toBe('run');
    });

    it('attaches after cold bootstrap without sending private original inputs or inherited view state', async () => {
        let posted: RunAgentInput | undefined;
        const session = new AgUiSession({ connectionId: 'agently', profile: 'agently', durableReplay: true,
            threadId: 'thread', url: '/run', initialState: { local: 'not-sent' },
            initialMessages: [{ id: 'stale', role: 'assistant', content: 'partial text' }],
            fetch: async (_url, init) => {
                posted = JSON.parse(String(init.body));
                return stream(resultEvents(posted!));
            },
        });
        await session.attach('existing-run', 'reload-request');
        expect(posted).toMatchObject({ threadId: 'thread', runId: 'existing-run', messages: [], state: {}, tools: [], context: [],
            forwardedProps: { agently: { version: '1', operation: 'run.attach', requestId: 'reload-request', payload: {} } } });
        expect(session.getSnapshot().phase).toBe('completed');
        expect(session.getSnapshot().messages.some(message => message.id === 'stale')).toBe(false);
        expect(session.getSnapshot().messages.find(message => message.id === 'answer')).toMatchObject({ content: 'Hello 🌍' });
        const generic = new AgUiSession({ connectionId: 'generic', threadId: 'thread', url: '/run' });
        expect(() => generic.attach('existing-run', 'reload-request')).toThrow('does not support');
    });
});
