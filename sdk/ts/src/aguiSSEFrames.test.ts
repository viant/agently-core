import { describe, expect, it, vi } from 'vitest';
import { boundedSSEFrames } from './aguiSSEFrames';
import { AgUiCommands } from './aguiCommands';
import { AgUiClient } from './agui';

function response(text: string, size = 4096) {
    const bytes = new TextEncoder().encode(text);
    let offset = 0;
    return new Response(new ReadableStream<Uint8Array>({
        pull(controller) {
            if (offset === bytes.length) { controller.close(); return; }
            controller.enqueue(bytes.subarray(offset, Math.min(bytes.length, offset + size)));
            offset = Math.min(bytes.length, offset + size);
        },
    }), { headers: { 'content-type': 'text/event-stream' } });
}

describe('bounded AG-UI SSE framing', () => {
    it('preserves field order, repeated data, opaque ids, comments and split UTF-8/CRLF', async () => {
        const source = ': keepalive\r\nid: opaque /é \r\nevent: custom\r\ndata: {"text":\r\ndata: "🌍"}\r\n\r\n\r\ndata: tail';
        const expected = ': keepalive\nid: opaque /é \nevent: custom\ndata: {"text":\ndata: "🌍"}\n\n\ndata: tail';
        for (const size of [1, 2, 3, 7, 4096]) expect(await boundedSSEFrames(response(source, size)).text()).toBe(expected);
    });

    it('caps each pending event, fails explicitly and propagates cancellation', async () => {
        await expect(boundedSSEFrames(response('data: ' + 'x'.repeat(64), 3), 32).text()).rejects.toThrow('SSE event exceeds 32 bytes');
        expect(await boundedSSEFrames(response('data: a\n\n'.repeat(100), 1024), 32).text()).toBe('data: a\n\n'.repeat(100));
        const cancel = vi.fn();
        const raw = new Response(new ReadableStream({ cancel }), { headers: { 'content-type': 'text/event-stream' } });
        await boundedSSEFrames(raw).body!.cancel('closed view');
        await Promise.resolve();
        expect(cancel).toHaveBeenCalledWith('closed view');
    });

    it('does not synthesize a delimiter for an unterminated frame; rejects bad UTF-8/read errors', async () => {
        expect(await boundedSSEFrames(response('data: unfinished')).text()).toBe('data: unfinished');
        const broken = new Response(new ReadableStream({ start(c) { c.error(new Error('reader failed')); } }), { headers: { 'content-type': 'text/event-stream' } });
        await expect(boundedSSEFrames(broken).text()).rejects.toThrow('reader failed');
        const invalid = new Response(new Uint8Array([0xff]), { headers: { 'content-type': 'text/event-stream' } });
        await expect(boundedSSEFrames(invalid).text()).rejects.toThrow();
    });

    it('enforces the default 64 MiB cap before releasing an oversized event', async () => {
        const chunk = new TextEncoder().encode('x'.repeat(256 * 1024));
        let count = 0;
        const source = new Response(new ReadableStream<Uint8Array>({
            pull(controller) {
                if (count++ < 257) controller.enqueue(chunk);
                else controller.close();
            },
        }), { headers: { 'content-type': 'text/event-stream' } });
        const reader = boundedSSEFrames(source).body!.getReader();
        await expect(reader.read()).rejects.toThrow('SSE event exceeds 67108864 bytes');
    }, 20000);

    it('reopens a canonical bootstrap larger than upstream 10 MiB through both result tap and HttpAgent', async () => {
        const padding = 'x'.repeat(12 * 1024 * 1024);
        const result = { version: '1', threadId: 'thread', transcript: { schemaVersion: '2', conversation: { conversationId: 'thread', turns: [] }, future: { padding } }, messages: [], state: null, runs: [], projection: { lossless: true, unavailableMessageIds: [] } };
        const fetch = vi.fn(async (_url: string, _init: RequestInit) => response([
            { type: 'RUN_STARTED', threadId: 'thread', runId: 'run' },
            { type: 'RUN_FINISHED', threadId: 'thread', runId: 'run', outcome: { type: 'success' }, result },
        ].map(event => `data: ${JSON.stringify(event)}\n\n`).join('')));
        const actual = await new AgUiCommands({ url: '/run', fetch }).execute('conversation.bootstrap', {}, { threadId: 'thread', runId: 'run', requestId: 'request' });
        expect(actual).toEqual(result);
        expect(fetch).toHaveBeenCalledTimes(1);
        expect(JSON.parse(String(fetch.mock.calls[0]?.[1]?.body)).forwardedProps.agently.operation).toBe('conversation.bootstrap');
    }, 20000);

    it('accepts a large standard state snapshot from an external agent without command extensions', async () => {
        const padding = 'y'.repeat(11 * 1024 * 1024);
        const client = new AgUiClient({ url: '/external', threadId: 'thread', fetch: async () => response([
            { type: 'RUN_STARTED', threadId: 'thread', runId: 'run' },
            { type: 'STATE_SNAPSHOT', snapshot: { padding } },
            { type: 'RUN_FINISHED', threadId: 'thread', runId: 'run', outcome: { type: 'success' } },
        ].map(event => `data: ${JSON.stringify(event)}\n\n`).join('')) });
        await client.run({ runId: 'run' });
        expect(client.agent.state).toEqual({ padding });
    }, 20000);
});
