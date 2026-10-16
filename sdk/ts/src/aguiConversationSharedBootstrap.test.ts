import { describe, expect, it, vi } from 'vitest';
import { AgentlyClient } from './client';

const frame = (event: unknown) => `data: ${JSON.stringify(event)}\n\n`;
const compact = { conversationId: 'native', includeModelCalls: true, includeToolCalls: true, includeModelPayloads: false, includeToolPayloads: false, includeFeeds: false };
function deferred() { let resolve!: () => void; const promise = new Promise<void>(done => { resolve = done; }); return { promise, resolve }; }
function fixture(toolSteps = true) {
    const initial = deferred(), feeds = deferred();
    const posted: any[] = [], feedSignals: Array<AbortSignal | null | undefined> = [];
    let text = 'persisted report and highlight', feedError = false;
    let primary: ReadableStreamDefaultController<Uint8Array> | undefined;
    const host = new AgentlyClient({ baseURL: '/v1', fetchImpl: async (url, init) => {
        if (String(url).includes('/conversations/')) return new Response(JSON.stringify({ id: 'native', aguiThreadId: 'opaque' }));
        const input = JSON.parse(String(init?.body));
        if (input.forwardedProps.agently.operation === 'chat') {
            return new Response(new ReadableStream({ start(controller) {
                primary = controller;
                controller.enqueue(new TextEncoder().encode(frame({ type: 'RUN_STARTED', threadId: 'opaque', runId: input.runId, metadata: { agently: { identityVersion: '1', nativeTurnId: 'live-turn' } } })));
                controller.enqueue(new TextEncoder().encode(frame({ type: 'ACTIVITY_SNAPSHOT', messageId: 'live-turn', activityType: 'agently.turn', content: { version: '1', nativeTurnId: 'live-turn', status: 'running', queueSequence: '0' }, metadata: { agently: { presentation: { version: '1', conversationId: 'native', nativeTurnId: 'live-turn', pageId: 'page', iteration: 0 } } } })));
                controller.enqueue(new TextEncoder().encode(frame({ type: 'TEXT_MESSAGE_START', messageId: 'live-answer', role: 'assistant' })));
                controller.enqueue(new TextEncoder().encode(frame({ type: 'TEXT_MESSAGE_CONTENT', messageId: 'live-answer', delta: ' STREAMED NEW TEXT' })));
                init?.signal?.addEventListener('abort', () => controller.error(new DOMException('Detached', 'AbortError')), { once: true });
            } }), { headers: { 'Content-Type': 'text/event-stream' } });
        }
        const payload = input.forwardedProps.agently.payload;
        posted.push(payload);
        const isFeed = payload.includeFeeds === true;
        const capturedText = isFeed ? 'STALE HYDRATION TEXT' : text;
        if (isFeed) { feedSignals.push(init?.signal); await feeds.promise; }
        else await initial.promise;
        if (isFeed && feedError) throw new Error('feed unavailable');
        const result = { version: '1', threadId: 'opaque', transcript: { schemaVersion: '2', aguiThreadId: 'opaque', eventCursor: 'authorized-live-cursor',
            conversation: { conversationId: 'native', turns: [{ turnId: 'turn', status: 'completed', assistant: { final: { messageId: 'answer', content: capturedText } },
                execution: { pages: [{ toolSteps: toolSteps ? [{ toolName: 'arbitrary/producer', status: 'completed', toolCallId: 'call', responsePayloadId: 'payload', responsePayload: { inlineBody: '{"reportSpec":{},"reportFill":{},"reportPrint":{}}' } }] : [] }] } }] },
            ...(isFeed ? { feeds: [{ feedId: 'external-wildcard', data: { items: ['authorized highlight'] } }] } : {}) },
            messages: [], hostActivities: [{ id: 'app', role: 'activity', activityType: 'mcp-apps', content: { preserved: true } }], unavailableHostActivityIds: [], state: { original: true }, runs: [], projection: { lossless: true, unavailableMessageIds: [] } };
        return new Response(frame({ type: 'RUN_STARTED', threadId: 'opaque', runId: input.runId }) + frame({ type: 'RUN_FINISHED', threadId: 'opaque', runId: input.runId, result }), { headers: { 'Content-Type': 'text/event-stream' } });
    } });
    return { host, initial, feeds, posted, feedSignals, text: (value: string) => { text = value; }, failFeeds: () => { feedError = true; }, emit: (event: unknown) => primary!.enqueue(new TextEncoder().encode(frame(event))) };
}

describe('shared compact bootstrap and existing feed hydration', () => {
    it('shares exact full reads with subscription, publishes first, then merges only eventual authorized feeds', async () => {
        const f = fixture(), snapshots: any[] = [], activities = vi.fn(), feedEvents: any[] = [];
        const subscription = f.host.streamEvents('native', { onSnapshot: value => snapshots.push(value), onHostActivities: activities, onFeedEvent: event => feedEvents.push(event) });
        const reads = Array.from({ length: 16 }, (_, i) => i % 2 ? f.host.getLiveState(compact) : f.host.getTranscript(compact));
        await vi.waitFor(() => expect(f.posted).toHaveLength(1));
        expect(f.posted[0]).toMatchObject({ mode: 'live', includeFeeds: false, includeToolPayloads: false });
        f.initial.resolve();
        const results = await Promise.all(reads);
        expect(results.every(value => (value as any).eventCursor === 'authorized-live-cursor')).toBe(true);
        expect(snapshots).toHaveLength(1);
        expect(JSON.stringify(snapshots[0])).toContain('persisted report and highlight');
        expect(JSON.stringify(snapshots[0])).toContain('reportSpec');
        expect(activities.mock.calls.at(-1)?.[0][0].id).toBe('app');
        await vi.waitFor(() => expect(f.posted).toHaveLength(2));
        expect(f.posted[1]).toMatchObject({ includeFeeds: true, includeToolPayloads: false, includeModelPayloads: false });
        // A newer compact read remains authoritative while feed discovery waits.
        f.text('newer canonical text');
        await f.host.getTranscript(compact);
        expect(f.posted).toHaveLength(3);
        f.feeds.resolve();
        await vi.waitFor(() => expect(feedEvents[0]?.feedId).toBe('external-wildcard'));
        expect(feedEvents[0].feedData).toEqual({ items: ['authorized highlight'] });
        expect(JSON.stringify(snapshots.at(-1))).toContain('newer canonical text');
        expect(JSON.stringify(snapshots.at(-1))).not.toContain('STALE HYDRATION TEXT');
        const after = await f.host.getTranscript(compact);
        expect((after as any).feeds).toBeUndefined(); expect((after as any).conversation.feeds).toBeUndefined();
        expect(f.posted.filter(p => p.includeFeeds)).toHaveLength(1);
        subscription.close(); f.host.resetAgUiInteractions();
    });

    it('delivers late hydration as feed events without republishing a stale transcript over primary text', async () => {
        const f = fixture(), feedEvents: any[] = [], snapshots: any[] = [];
        let visible = '';
        const subscription = f.host.streamEvents('native', {
            onSnapshot: value => { snapshots.push(value); visible = JSON.stringify(value); },
            onTextDelta: delta => { visible += delta; },
            onFeedEvent: event => feedEvents.push(event),
        });
        f.initial.resolve(); await vi.waitFor(() => expect(f.posted).toHaveLength(2));
        await f.host.query({ conversationId: 'native', query: 'continue' });
        await vi.waitFor(() => expect(visible).toContain('STREAMED NEW TEXT'));
        const before = snapshots.length;
        f.feeds.resolve();
        await vi.waitFor(() => expect(feedEvents).toHaveLength(1));
        expect(snapshots).toHaveLength(before);
        expect(visible).toContain('STREAMED NEW TEXT');
        expect(visible).not.toContain('STALE HYDRATION TEXT');
        subscription.close(); f.host.resetAgUiInteractions();
    });

    it.each([true, false])('keeps newer primary feed active=%s decisions and data when old hydration finishes', async active => {
        const f = fixture(), events: any[] = [];
        const subscription = f.host.streamEvents('native', { onFeedEvent: event => events.push(event) });
        f.initial.resolve(); await vi.waitFor(() => expect(f.posted).toHaveLength(2));
        await f.host.query({ conversationId: 'native', query: 'continue' });
        f.emit({ type: 'ACTIVITY_SNAPSHOT', messageId: 'feed-update', activityType: 'agently.feed',
            content: { version: '1', feedId: 'external-wildcard', active, activationKnown: true, title: 'Fresh', developerOnly: false, itemCount: 1, data: { items: ['LIVE DATA'] } },
            metadata: { agently: { presentation: { version: '1', conversationId: 'native', nativeTurnId: 'live-turn', pageId: 'page', iteration: 0 } } } });
        await vi.waitFor(() => expect(events).toHaveLength(1));
        expect(events[0].type).toBe(active ? 'tool_feed_active' : 'tool_feed_inactive');
        f.feeds.resolve(); await new Promise(resolve => setTimeout(resolve, 30));
        expect(events).toHaveLength(1);
        const snapshots: any[] = [];
        const second = f.host.streamEvents('native', { onSnapshot: value => snapshots.push(value) });
        expect(snapshots[0].feeds[0]).toMatchObject({ active, data: { items: ['LIVE DATA'] } });
        if (!active) {
            // A prior explicit inactive decision survives a later legacy feed
            // snapshot even after explicit reconciliation starts a fresh read.
            await f.host.reconcileAgUiConversation('native');
            await vi.waitFor(() => expect(events).toHaveLength(2));
            expect(events[1].type).toBe('tool_feed_inactive');
        }
        second.close(); subscription.close(); f.host.resetAgUiInteractions();
    });

    it('keeps selected, since, full-payload and undefined-payload reads independent', async () => {
        const f = fixture(false);
        const subscription = f.host.streamEvents('native', {});
        const reads = [
            f.host.getTranscript({ ...compact, since: 'prior' }),
            f.host.getTranscript(compact, { executionGroupLimit: 1 }),
            f.host.getTranscript({ ...compact, includeToolPayloads: true }),
            f.host.getTranscript({ ...compact, includeToolPayloads: undefined }),
        ];
        await vi.waitFor(() => expect(f.posted).toHaveLength(5));
        expect(f.posted.some(p => p.since === 'prior')).toBe(true);
        expect(f.posted.some(p => p.selectors)).toBe(true);
        expect(f.posted.some(p => p.includeToolPayloads === true)).toBe(true);
        expect(f.posted.some(p => p.includeToolPayloads === undefined)).toBe(true);
        f.initial.resolve(); await Promise.all(reads);
        expect(f.posted.some(p => p.includeFeeds)).toBe(false);
        subscription.close(); f.host.resetAgUiInteractions();
    });

    it.each(['reset', 'close'])('aborts and discards feed hydration at %s boundary', async boundary => {
        const f = fixture(), snapshots: any[] = [], errors = vi.fn();
        const subscription = f.host.streamEvents('native', { onSnapshot: value => snapshots.push(value), onError: errors });
        f.initial.resolve();
        await vi.waitFor(() => expect(f.feedSignals).toHaveLength(1));
        const before = snapshots.length;
        if (boundary === 'reset') f.host.resetAgUiInteractions(); else subscription.close();
        expect(f.feedSignals[0]?.aborted).toBe(true);
        f.feeds.resolve(); await new Promise(resolve => setTimeout(resolve, 20));
        expect(snapshots).toHaveLength(before); expect(errors).not.toHaveBeenCalled();
        subscription.close(); f.host.resetAgUiInteractions();
    });

    it('surfaces feed failures without clearing compact text and reconciles through the existing lifecycle', async () => {
        const f = fixture(), snapshots: any[] = [], errors = vi.fn();
        const subscription = f.host.streamEvents('native', { onSnapshot: value => snapshots.push(value), onError: errors });
        f.initial.resolve(); await vi.waitFor(() => expect(f.posted).toHaveLength(2));
        f.failFeeds(); f.feeds.resolve();
        await vi.waitFor(() => expect(errors).toHaveBeenCalled());
        expect(JSON.stringify(snapshots.at(-1))).toContain('persisted report and highlight');
        await f.host.reconcileAgUiConversation('native');
        await vi.waitFor(() => expect(f.posted.filter(p => p.includeFeeds)).toHaveLength(2));
        subscription.close(); f.host.resetAgUiInteractions();
    });
});
