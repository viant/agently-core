import { describe, expect, it } from 'vitest';

import { ConversationStreamTracker } from '../conversationStream';
import type { Message, SSEEvent, Turn } from '../types';

describe('ConversationStreamTracker', () => {
    it('applies message, feed, and elicitation events through one facade', () => {
        const tracker = new ConversationStreamTracker();

        const delta = tracker.applyEvent({
            id: 'msg-1',
            conversationId: 'conv-1',
            turnId: 'turn-1',
            type: 'text_delta',
            content: 'Hello',
        } as SSEEvent);
        expect(delta).toEqual({ id: 'msg-1', content: 'Hello', final: false });
        expect(tracker.activeTurnId).toBe('turn-1');

        tracker.applyEvent({
            type: 'tool_feed_active',
            feedId: 'plan',
            feedTitle: 'Plan',
            feedItemCount: 2,
            conversationId: 'conv-1',
            turnId: 'turn-1',
        } as SSEEvent);
        expect(tracker.feeds).toHaveLength(1);
        expect(tracker.feeds[0]).toMatchObject({ feedId: 'plan', conversationId: 'conv-1', turnId: 'turn-1' });

        tracker.applyEvent({
            type: 'elicitation_requested',
            elicitationId: 'elic-1',
            conversationId: 'conv-1',
            turnId: 'turn-1',
            content: 'Need input',
            elicitationData: { requestedSchema: { type: 'object' } },
        } as SSEEvent);
        expect(tracker.pendingElicitation).toMatchObject({
            elicitationId: 'elic-1',
            conversationId: 'conv-1',
            turnId: 'turn-1',
            message: 'Need input',
        });
        expect(tracker.state.activeTurnId).toBe('turn-1');
    });

    it('reconciles transcript and server messages through one facade', () => {
        const tracker = new ConversationStreamTracker();
        tracker.applyEvent({
            id: 'msg-1',
            conversationId: 'conv-1',
            turnId: 'turn-1',
            type: 'text_delta',
            content: 'Hello',
        } as SSEEvent);

        tracker.applyTranscript([{
            id: 'turn-1',
            conversationId: 'conv-1',
            status: 'completed',
            createdAt: '2026-01-01T00:00:00Z',
            message: [{
                id: 'msg-1',
                conversationId: 'conv-1',
                role: 'assistant',
                type: 'text',
                content: 'Hello world',
                interim: 0,
                createdAt: '2026-01-01T00:00:01Z',
            }],
        } as Turn]);

        const merged = tracker.reconcile([{
            id: 'msg-1',
            conversationId: 'conv-1',
            role: 'assistant',
            type: 'text',
            content: 'Hello world',
            interim: 0,
            createdAt: '2026-01-01T00:00:01Z',
        } as Message]);

        expect(merged).toHaveLength(1);
        expect(merged[0].content).toBe('Hello world');
        expect(tracker.snapshot().bufferedMessages).toHaveLength(1);
        tracker.reset();
        expect(tracker.state.bufferedMessages).toHaveLength(0);
    });
});
