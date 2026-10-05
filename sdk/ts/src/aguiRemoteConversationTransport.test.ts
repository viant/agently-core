import { describe, expect, it, vi } from 'vitest';
import type { RunAgentInput } from '@ag-ui/core';
import { AgUiRemoteConversationTransport } from './aguiRemoteConversationTransport';
import type { AgUiViewEvent } from './aguiViewProjection';
const sse = (events: unknown[]) => new Response(events.map(event => `data: ${JSON.stringify(event)}\n\n`).join(''), { headers: { 'Content-Type': 'text/event-stream' } });
function fixture(makeEvents?: (input: RunAgentInput) => unknown[]) {
    const posts: RunAgentInput[] = [];
    let count = 0;
    const host = {
        listAgUiBackends: vi.fn(async () => [{ id: 'dojo', label: 'Dojo', profile: 'standard' as const, durableReplay: false, ephemeral: true, inputMode: 'text-only' as const }]),
        createAgUiBackendThread: vi.fn(async (connectionId: string) => ({ connectionId, threadId: `remote-${++count}`, ephemeral: true, durableReplay: false })),
        agUiTransport: vi.fn(() => ({ url: '/bff/dojo/run', fetch: async (_url: string, init: RequestInit) => {
            const input = JSON.parse(String(init.body)) as RunAgentInput; posts.push(input);
            return sse(makeEvents?.(input) ?? [
                { type: 'RUN_STARTED', threadId: input.threadId, runId: input.runId },
                { type: 'TEXT_MESSAGE_START', messageId: `answer-${posts.length}`, role: 'assistant' },
                { type: 'TEXT_MESSAGE_CONTENT', messageId: `answer-${posts.length}`, delta: 'Hello' },
                { type: 'TEXT_MESSAGE_END', messageId: `answer-${posts.length}` },
                { type: 'RUN_FINISHED', threadId: input.threadId, runId: input.runId },
            ]);
        } })),
    };
    return { host, posts, coordinator: new AgUiRemoteConversationTransport(host) };
}
describe('remote standard orchestration', () => {
    it('uses isolated server-issued threads and fresh IDs without native context, then replays absolute views on remount', async () => {
        const { coordinator, posts } = fixture();
        const first = await coordinator.create('dojo'), second = await coordinator.create('dojo');
        const live: AgUiViewEvent[] = [];
        const subscription = coordinator.subscribe('dojo', first.threadId, { onEvent: event => live.push(event) });
        const submit = coordinator.send('dojo', first.threadId, 'hello');
        subscription.close();
        await submit.completion;
        const replay: AgUiViewEvent[] = [];
        coordinator.subscribe('dojo', first.threadId, { onEvent: event => replay.push(event) });
        expect(replay.find(event => event.type === 'text_delta')).toMatchObject({ content: 'Hello', contentMode: 'snapshot', connectionProfile: 'standard', hostEffectsAllowed: false });
        await coordinator.send('dojo', first.threadId, 'follow-up').completion;
        await coordinator.send('dojo', second.threadId, 'separate').completion;
        expect(posts[0]).toMatchObject({ tools: [], context: [], forwardedProps: {} });
        expect(posts[1].messages.some(message => message.role === 'assistant')).toBe(true);
        expect(posts[2].messages).toHaveLength(1);
        expect(new Set(posts.map(input => input.runId)).size).toBe(3);
        expect(posts[0].messages[0].id).toBe(submit.messageId);
        expect(posts[0].messages[0].metadata).toBeUndefined();
    });
    it('rejects unknown registry identities and reset clears local history', async () => {
        const { coordinator, host } = fixture();
        await expect(coordinator.create('foreign')).rejects.toThrow('configured standard');
        expect(host.createAgUiBackendThread).not.toHaveBeenCalled();
        const thread = await coordinator.create('dojo'); coordinator.reset();
        expect(() => coordinator.getSnapshot('dojo', thread.threadId)).toThrow('unavailable');
    });
    it('permits a new turn after explicit protocol error without retrying the old run', async () => {
        const { coordinator, posts } = fixture(input => [
            { type: 'RUN_STARTED', threadId: input.threadId, runId: input.runId },
            { type: 'RUN_ERROR', message: 'Explicit failure' },
        ]);
        const thread = await coordinator.create('dojo');
        await expect(coordinator.send('dojo', thread.threadId, 'one').completion).rejects.toThrow('Explicit failure');
        await expect(coordinator.send('dojo', thread.threadId, 'two').completion).rejects.toThrow('Explicit failure');
        expect(posts).toHaveLength(2); expect(posts[0].runId).not.toBe(posts[1].runId);
    });
    it('blocks uncertain transport failure without retry or fallback', async () => {
        const { coordinator, host } = fixture();
        host.agUiTransport.mockReturnValue({ url: '/run', fetch: async () => { throw new Error('Lost transport'); } });
        const thread = await coordinator.create('dojo');
        await expect(coordinator.send('dojo', thread.threadId, 'one').completion).rejects.toThrow('Lost transport');
        expect(() => coordinator.send('dojo', thread.threadId, 'again')).toThrow('uncertain');
    });
    it('fences creation across account reset and rejects nonterminal streams', async () => {
        const { coordinator, host } = fixture(input => [
            { type: 'RUN_STARTED', threadId: input.threadId, runId: input.runId },
        ]);
        const thread = await coordinator.create('dojo');
        await expect(coordinator.send('dojo', thread.threadId, 'one').completion).rejects.toThrow('terminal');
        expect(() => coordinator.send('dojo', thread.threadId, 'two')).toThrow('uncertain');
        let resolve!: (value: Awaited<ReturnType<typeof host.createAgUiBackendThread>>) => void;
        host.createAgUiBackendThread.mockImplementation(() => new Promise(done => { resolve = done; }));
        const pending = coordinator.create('dojo');
        await Promise.resolve();
        coordinator.reset();
        resolve({ connectionId: 'dojo', threadId: 'late', ephemeral: true, durableReplay: false });
        await expect(pending).rejects.toThrow('invalidated');
        expect(() => coordinator.getSnapshot('dojo', 'late')).toThrow('unavailable');
    });
    it('prunes removed snapshot messages from remount event cache', async () => {
        const { coordinator } = fixture(input => [
            { type: 'RUN_STARTED', threadId: input.threadId, runId: input.runId },
            { type: 'MESSAGES_SNAPSHOT', messages: [...input.messages, { id: 'removed', role: 'assistant', content: 'gone' }, { id: 'retained', role: 'assistant', content: 'here' }] },
            { type: 'MESSAGES_SNAPSHOT', messages: [...input.messages, { id: 'retained', role: 'assistant', content: 'updated' }] },
            { type: 'RUN_FINISHED', threadId: input.threadId, runId: input.runId },
        ]);
        const thread = await coordinator.create('dojo'); await coordinator.send('dojo', thread.threadId, 'hello').completion;
        const events: AgUiViewEvent[] = [], descriptors: unknown[] = [];
        coordinator.subscribe('dojo', thread.threadId, { onEvent: event => events.push(event), onDescriptor: descriptor => descriptors.push(descriptor) });
        expect(events.some(event => event.protocolMessageId === 'removed')).toBe(false);
        expect(events.find(event => event.protocolMessageId === 'retained')).toMatchObject({ content: 'updated', contentMode: 'snapshot' });
        expect(descriptors).toContainEqual(expect.objectContaining({ kind: 'protocol-snapshot', removedMessageIds: ['removed'] }));
    });
    it('keeps foreign host activities passive and caches one absolute tool row per identity', async () => {
        const { coordinator } = fixture(input => [
            { type: 'RUN_STARTED', threadId: input.threadId, runId: input.runId },
            { type: 'TOOL_CALL_START', toolCallId: 'call', toolCallName: 'foreign', parentMessageId: 'assistant' },
            { type: 'TOOL_CALL_ARGS', toolCallId: 'call', delta: '{"a":1}' },
            { type: 'TOOL_CALL_END', toolCallId: 'call' },
            { type: 'TOOL_CALL_RESULT', messageId: 'result', toolCallId: 'call', content: '{"_meta":{"ui":{"resourceUri":"ui://foreign"}}}', role: 'tool' },
            { type: 'ACTIVITY_SNAPSHOT', messageId: 'host', activityType: 'mcp-apps', content: { resourceUri: 'ui://foreign' } },
            { type: 'RUN_FINISHED', threadId: input.threadId, runId: input.runId },
        ]);
        const thread = await coordinator.create('dojo'); await coordinator.send('dojo', thread.threadId, 'hello').completion;
        const events: AgUiViewEvent[] = [], descriptors: unknown[] = [];
        coordinator.subscribe('dojo', thread.threadId, { onEvent: event => events.push(event), onDescriptor: descriptor => descriptors.push(descriptor) });
        expect(events.filter(event => event.type === 'tool_call_started')).toHaveLength(1);
        expect(events.filter(event => event.type === 'tool_call_completed')).toHaveLength(1);
        expect(events.every(event => !event.hostEffectsAllowed)).toBe(true);
        expect(descriptors).toContainEqual({ kind: 'unsupported-activity', messageId: 'host', activityType: 'mcp-apps', hostEffectsAllowed: false });
    });
});
