import { describe, expect, it, vi } from 'vitest';
import { AgentlyClient } from './client';
import { AgUiConversationTransport } from './aguiConversationTransport';

const frame = (event: unknown) => new TextEncoder().encode(`data: ${JSON.stringify(event)}\n\n`);

describe('coordinator compact history', () => {
    it('admits one turn and refreshes completion without inlining history larger than the SSE cap', async () => {
        const flags: unknown[] = [], snapshots: string[] = [];
        let chats = 0;
        let finish!: () => void;
        // Two saved provider bodies exceed the frame cap in aggregate.
        const savedBody = 'x'.repeat(34 * 1024 * 1024);
        const host = new AgentlyClient({ baseURL: '/v1', useCookies: true, fetchImpl: async (url, init) => {
            if (String(url).includes('/conversations/')) return new Response(JSON.stringify({ id: 'thread' }));
            const input = JSON.parse(String(init?.body));
            const started = { type: 'RUN_STARTED', threadId: 'thread', runId: input.runId };
            if (input.forwardedProps.agently.operation === 'conversation.bootstrap') {
                const compact = input.forwardedProps.agently.payload.includeModelPayloads === false;
                flags.push(input.forwardedProps.agently.payload.includeModelPayloads);
                const modelSteps = [1, 2].map(id => ({ modelCallId: `model-${id}`, status: 'completed', requestPayloadId: `payload-${id}`,
                    ...(!compact ? { requestPayload: { inlineBody: savedBody } } : {}) }));
                const result = { version: '1', threadId: 'thread', transcript: { schemaVersion: '2', conversation: { conversationId: 'thread', turns: [{ turnId: 'old-turn', status: 'completed', execution: { pages: [{ modelSteps }] } }] } },
                    messages: [], state: {}, runs: [], hostActivities: [], unavailableHostActivityIds: [], projection: { lossless: true, unavailableMessageIds: [] } };
                return new Response(new ReadableStream({ start(c) {
                    c.enqueue(frame(started)); c.enqueue(frame({ type: 'RUN_FINISHED', threadId: 'thread', runId: input.runId, result })); c.close();
                } }), { headers: { 'content-type': 'text/event-stream' } });
            }
            expect(input.forwardedProps.agently.operation).toBe('chat'); chats++;
            return new Response(new ReadableStream({ start(c) {
                c.enqueue(frame(started));
                c.enqueue(frame({ type: 'ACTIVITY_SNAPSHOT', messageId: 'native-turn', activityType: 'agently.turn', content: { nativeTurnId: 'new-turn', status: 'running' } }));
                finish = () => { c.enqueue(frame({ type: 'RUN_FINISHED', threadId: 'thread', runId: input.runId, outcome: { type: 'success' } })); c.close(); };
            } }), { headers: { 'content-type': 'text/event-stream' } });
        } });
        const transport = new AgUiConversationTransport(host, options => ({ subscriber: { onActivitySnapshotEvent: ({ event }) => {
            options.onViewEvent({ type: 'turn_started', conversationId: 'thread', turnId: String(event.content.nativeTurnId) });
        } } }));
        const subscription = transport.subscribe('thread', { onSnapshot: snapshot => snapshots.push(JSON.stringify(snapshot)) });
        try {
            const admitted = await transport.query({ conversationId: 'thread', query: 'public synthetic request' });
            expect(admitted.turnId).toBe('new-turn'); expect(chats).toBe(1);
            finish();
            await vi.waitFor(() => expect(flags.length).toBeGreaterThanOrEqual(2));
            expect(flags.every(flag => flag === false)).toBe(true);
            expect(snapshots.at(-1)).toContain('payload-1'); expect(snapshots.at(-1)).not.toContain('inlineBody');
            expect(savedBody.length).toBe(34 * 1024 * 1024); // Saved detail source remains available.
        } finally { subscription.close(); transport.reset(); host.resetAgUiInteractions(); }
    }, 20000);

    it('preserves explicit full-payload detail reads', async () => {
        let payload: any;
        const host = new AgentlyClient({ baseURL: '/v1', useCookies: true, fetchImpl: async (url, init) => {
            if (String(url).includes('/conversations/')) return new Response(JSON.stringify({ id: 'thread' }));
            const input = JSON.parse(String(init?.body)); payload = input.forwardedProps.agently.payload;
            return new Response(new Blob([frame({ type: 'RUN_STARTED', threadId: 'thread', runId: input.runId }), frame({ type: 'RUN_FINISHED', threadId: 'thread', runId: input.runId, result: { version: '1', threadId: 'thread', transcript: { schemaVersion: '2', conversation: { conversationId: 'thread', turns: [] } }, messages: [], state: {}, runs: [], projection: { lossless: true, unavailableMessageIds: [] } } })]), { headers: { 'content-type': 'text/event-stream' } });
        } });
        const transport = new AgUiConversationTransport(host, () => ({ subscriber: {} }));
        try { await transport.readSnapshot('thread', { mode: 'transcript', includeModelPayloads: true }); expect(payload.includeModelPayloads).toBe(true); }
        finally { transport.reset(); host.resetAgUiInteractions(); }
    });
});
