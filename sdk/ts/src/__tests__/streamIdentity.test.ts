import { describe, expect, it } from 'vitest';

import { resolveEventConversationId, resolveEventMessageId, resolveEventTurnId } from '../streamIdentity';
import type { SSEEvent } from '../types';

describe('streamIdentity', () => {
    it('prefers explicit conversation and turn identity', () => {
        const event = {
            conversationId: 'conv-1',
            streamId: 'stream-1',
            turnId: 'turn-1',
            id: 'msg-1',
            assistantMessageId: 'assistant-1',
        } as SSEEvent;

        expect(resolveEventConversationId(event)).toBe('conv-1');
        expect(resolveEventTurnId(event)).toBe('turn-1');
        expect(resolveEventMessageId(event)).toBe('msg-1');
    });

    it('falls back through assistant/model/tool ids before stream id', () => {
        expect(resolveEventMessageId({ assistantMessageId: 'assistant-1', streamId: 'stream-1' } as SSEEvent)).toBe('assistant-1');
        expect(resolveEventMessageId({ modelCallId: 'model-1', streamId: 'stream-1' } as SSEEvent)).toBe('model-1');
        expect(resolveEventMessageId({ toolMessageId: 'tool-msg-1', streamId: 'stream-1' } as SSEEvent)).toBe('tool-msg-1');
        expect(resolveEventMessageId({ streamId: 'stream-1' } as SSEEvent)).toBe('stream-1');
    });
});
