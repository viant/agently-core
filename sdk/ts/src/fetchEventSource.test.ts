import { expect, it, vi } from 'vitest';
import { FetchEventSource } from './fetchEventSource';
it('parses split UTF8/multiline SSE and closes the underlying request', async () => {
    const bytes = new TextEncoder().encode(': heartbeat\r\ndata: café\r\ndata: second\r\n\r\n');
    let controller!: ReadableStreamDefaultController<Uint8Array>;
    const response = new Response(new ReadableStream({ start(c) { controller = c; }, cancel: vi.fn() }));
    const fetcher = vi.fn(async (_url, init) => { expect(init.headers.Authorization).toBe('Bearer test'); return response; });
    const source = new FetchEventSource('https://example.test/stream', fetcher as typeof fetch, async () => ({ Authorization: 'Bearer test' }), 'same-origin');
    const events: string[] = []; source.onmessage = e => events.push(e.data);
    await Promise.resolve(); await Promise.resolve();
    controller.enqueue(bytes.slice(0, 22)); controller.enqueue(bytes.slice(22));
    await vi.waitFor(() => expect(events).toEqual(['café\nsecond']));
    source.close(); expect(fetcher.mock.calls[0][1].signal.aborted).toBe(true);
});
it('reconnects with last event ID and refreshed headers, then cancels retry on close', async () => {
    let calls = 0;
    const fetcher = vi.fn(async (_url, init) => {
        calls++;
        if (calls === 1) return new Response('id: event-7\nretry: 1\ndata: first\n\n');
        expect(init.headers['Last-Event-ID']).toBe('event-7');
        expect(init.headers.Authorization).toBe('Bearer refreshed');
        return new Response(new ReadableStream({ start() {} }));
    });
    const source = new FetchEventSource('https://example.test/stream', fetcher as typeof fetch, async () => ({ Authorization: calls ? 'Bearer refreshed' : 'Bearer initial' }), 'same-origin');
    const events: string[] = []; source.onmessage = e => events.push(e.data);
    await vi.waitFor(() => expect(calls).toBe(2));
    expect(events).toEqual(['first']); source.close();
    await new Promise(resolve => setTimeout(resolve, 10)); expect(calls).toBe(2);
});
