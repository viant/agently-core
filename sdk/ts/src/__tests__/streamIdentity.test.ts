import { describe, expect, it } from 'vitest';

import { normalizeStreamEventIdentity, resolveEventConversationId, resolveEventMessageId, resolveEventTurnId } from '../streamIdentity';
import type { SSEEvent } from '../types';

describe('streamIdentity', () => {
    it('prefers explicit conversation and turn identity', () => {
        const event = {
            conversationId: 'conv-1',
            streamId: 'stream-1',
            turnId: 'turn-1',
            messageId: 'msg-1-canonical',
            id: 'msg-1',
            assistantMessageId: 'assistant-1',
        } as SSEEvent;

        expect(resolveEventConversationId(event)).toBe('conv-1');
        expect(resolveEventTurnId(event)).toBe('turn-1');
        expect(resolveEventMessageId(event)).toBe('msg-1-canonical');
    });

    it('falls back through assistant/model/tool ids before stream id', () => {
        expect(resolveEventMessageId({ assistantMessageId: 'assistant-1', streamId: 'stream-1' } as SSEEvent)).toBe('assistant-1');
        expect(resolveEventMessageId({ modelCallId: 'model-1', streamId: 'stream-1' } as SSEEvent)).toBe('model-1');
        expect(resolveEventMessageId({ toolMessageId: 'tool-msg-1', streamId: 'stream-1' } as SSEEvent)).toBe('tool-msg-1');
        expect(resolveEventMessageId({ streamId: 'stream-1' } as SSEEvent)).toBe('stream-1');
    });

    it('does not derive turn identity from stream id', () => {
        expect(resolveEventTurnId({ streamId: 'stream-1' } as SSEEvent)).toBe('');
    });

    it('normalizes stream event identity and rejects mismatched subscribed conversations', () => {
        expect(normalizeStreamEventIdentity({
            type: 'text_delta',
            streamId: 'conv-1',
            content: 'hello',
        } as SSEEvent, 'conv-1')).toMatchObject({
            conversationId: 'conv-1',
            streamId: 'conv-1',
            messageId: 'conv-1',
        });

        expect(normalizeStreamEventIdentity({
            type: 'text_delta',
            conversationId: 'conv-2',
            content: 'skip',
        } as SSEEvent, 'conv-1')).toBeNull();
    });
});
