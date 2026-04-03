import { describe, expect, it } from 'vitest';

import { isLiveConversationState } from '../conversationStatus';

describe('conversationStatus', () => {
    it('treats live stages as live conversation state', () => {
        expect(isLiveConversationState({ stage: 'thinking' })).toBe(true);
        expect(isLiveConversationState({ stage: 'executing' })).toBe(true);
        expect(isLiveConversationState({ stage: 'eliciting' })).toBe(true);
    });

    it('treats live statuses as live conversation state', () => {
        expect(isLiveConversationState({ status: 'running' })).toBe(true);
        expect(isLiveConversationState({ status: 'waiting_for_user' })).toBe(true);
    });

    it('treats terminal/empty states as not live', () => {
        expect(isLiveConversationState({ stage: 'done', status: 'succeeded' })).toBe(false);
        expect(isLiveConversationState({})).toBe(false);
        expect(isLiveConversationState(null)).toBe(false);
    });
});
