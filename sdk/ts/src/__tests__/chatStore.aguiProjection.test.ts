import { describe, expect, it } from 'vitest';
import { applyEvent, applyTranscript, applyLocalSubmit, newConversationState } from '../chatStore/reducer';
import type { SSEEvent } from '../types';

describe('existing renderer store with reduced AG-UI content', () => {
    it('fails only the exact unadmitted request when submission is rejected', () => {
        const state = newConversationState('thread');
        for (const clientRequestId of ['one', 'two']) {
            applyLocalSubmit(state, { conversationId: 'thread', clientRequestId, content: 'Same prompt', createdAt: '2026-10-03T00:00:00Z', mode: 'submit' });
        }
        applyEvent(state, { type: 'turn_failed', conversationId: 'thread', clientRequestId: 'two', error: 'HTTP 403', status: 'failed' });
        expect(state.turns.map(turn => turn.lifecycle)).toEqual(['pending', 'failed']);
        expect(state.turns[1].pages[0].lifecycleEntries[0].errorMessage).toBe('HTTP 403');
        for (const clientRequestId of [undefined, 'unknown']) {
            applyEvent(state, { type: 'turn_failed', conversationId: 'thread', clientRequestId, error: 'Rejected' });
        }
        expect(state.turns[0].lifecycle).toBe('pending');
        applyEvent(state, { type: 'turn_failed', conversationId: 'another-thread', clientRequestId: 'one' });
        expect(state.turns[0].lifecycle).toBe('pending');
    });

    it('does not use a request-only rejection to fail an already admitted turn', () => {
        const state = newConversationState('thread');
        applyLocalSubmit(state, { conversationId: 'thread', clientRequestId: 'one', content: 'Prompt', createdAt: '2026-10-03T00:00:00Z', mode: 'submit' });
        applyEvent(state, { type: 'turn_started', conversationId: 'thread', turnId: 'native', clientRequestId: 'one' });
        applyEvent(state, { type: 'turn_failed', conversationId: 'thread', clientRequestId: 'one' });
        expect(state.turns[0].lifecycle).toBe('running');
    });

    it('refines protocol-to-native user identity by exact client request without duplicating the optimistic row', () => {
        const state = newConversationState('thread');
        applyLocalSubmit(state, { conversationId: 'thread', clientRequestId: 'ui-message', content: 'Friendly prompt', createdAt: '2026-10-03T00:00:00Z', mode: 'submit' });
        const identity = state.turns[0].users[0].renderKey;
        applyEvent(state, { type: 'turn_started', conversationId: 'thread', turnId: 'native-turn', userMessageId: 'ui-message', clientRequestId: 'ui-message' });
        applyEvent(state, { type: 'message_appended', conversationId: 'thread', turnId: 'native-turn', messageId: 'ui-message', clientRequestId: 'ui-message', content: 'Friendly prompt', patch: { role: 'user' } });
        applyEvent(state, { type: 'message_appended', conversationId: 'thread', turnId: 'native-turn', messageId: 'native-user', clientRequestId: 'ui-message', content: 'Friendly prompt', patch: { role: 'user' } });
        expect(state.turns).toHaveLength(1);
        expect(state.turns[0].users).toHaveLength(1);
        expect(state.turns[0].users[0]).toMatchObject({ renderKey: identity, messageId: 'native-user', clientRequestId: 'ui-message', content: 'Friendly prompt' });
    });
    it('replaces absolute text projections without duplicate accumulation or changing render identity', () => {
        const state = newConversationState('thread');
        const base = { conversationId: 'thread', turnId: 'native-turn', pageId: 'page', iteration: 1, messageId: 'assistant', createdAt: '2026-10-03T00:00:00Z' };
        applyEvent(state, { ...base, type: 'turn_started' });
        applyEvent(state, { ...base, type: 'model_started', modelCallId: 'model-call' });
        const project = (content: string) => applyEvent(state, { ...base, type: 'text_delta', contentMode: 'snapshot', content });
        project('Hello');
        const page = state.turns[0].pages.find(item => item.pageId === 'page')!;
        const identity = page.renderKey;
        project('Hello world');
        project('Hello world');
        expect(page.content).toBe('Hello world');
        expect(page.renderKey).toBe(identity);
        expect(page.modelSteps[0].modelCallId).toBe('model-call');
        project('Corrected');
        expect(page.content).toBe('Corrected');
        project('');
        expect(page.content).toBe('');
    });

    it('retains native delta semantics and replaces already-reduced reasoning snapshots', () => {
        const state = newConversationState('thread');
        const event = (partial: Partial<SSEEvent>): SSEEvent => ({ type: 'turn_started', conversationId: 'thread', turnId: 'turn', pageId: 'page', iteration: 1, ...partial });
        applyEvent(state, event({}));
        applyEvent(state, event({ type: 'text_delta', content: 'Hello' }));
        applyEvent(state, event({ type: 'text_delta', content: ' world' }));
        applyEvent(state, event({ type: 'reasoning_delta', contentMode: 'snapshot', content: 'A summary' }));
        applyEvent(state, event({ type: 'reasoning_delta', contentMode: 'snapshot', content: 'A summary' }));
        const page = state.turns[0].pages.find(item => item.pageId === 'page')!;
        expect(page.content).toBe('Hello world');
        expect(page.narration).toBe('A summary');
        applyEvent(state, event({ type: 'reasoning_delta', contentMode: 'snapshot', content: '' }));
        expect(page.narration).toBe('');
    });
});

describe('exact native steering correlation',()=>{
    it('keeps repeated identical steering prompts distinct but coalesces native echoes and receipts',()=>{
        const state=newConversationState('thread');
        applyEvent(state,{type:'turn_started',conversationId:'thread',turnId:'turn'});
        for(const id of ['one','two']){
            applyLocalSubmit(state,{conversationId:'thread',clientRequestId:id,content:'Same steering prompt',mode:'steer'});
            // Native echo may precede the command receipt.
            applyEvent(state,{type:'message_appended',conversationId:'thread',turnId:'turn',messageId:`native-${id}`,content:'Same steering prompt',patch:{role:'user'}});
            applyEvent(state,{type:'message_appended',conversationId:'thread',turnId:'turn',messageId:`native-${id}`,clientRequestId:id,content:'Same steering prompt',patch:{role:'user'}});
        }
        expect(state.turns[0].users.map(user=>[user.clientRequestId,user.messageId])).toEqual([['one','native-one'],['two','native-two']]);
        const snapshot={conversationId:'thread',turns:[{turnId:'turn',status:'completed' as const,messages:['one','two'].map(id=>({messageId:`native-${id}`,role:'user' as const,clientRequestId:id,content:'Same steering prompt'}))}]};
        applyTranscript(state,snapshot);expect(state.turns[0].users).toHaveLength(2);expect(state.turns[0].messages).toHaveLength(0);
        const cold=newConversationState('thread');applyTranscript(cold,snapshot);
        expect(cold.turns[0].users.map(user=>user.messageId)).toEqual(['native-one','native-two']);
    });
});
