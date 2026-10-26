import { describe, expect, it, vi } from 'vitest';
import { AgentlyClient } from '../client';
import { applyTranscript, applyTurnFeedback, newConversationState } from '../chatStore/reducer';
import { projectConversation } from '../chatStore/projector';
import { transcriptDTO } from '../wireDTO';

describe('turn feedback', () => {
    it('merges explicit clears, preserves render identity, and projects both final representations', () => {
        const state = newConversationState('c');
        const turn = {turnId:'t', status:'completed' as const, feedbackEditable:true,
            feedback:{rating:1 as const, comment:'Helpful 👍', updatedAt:'2026-10-24T12:00:00Z'},
            assistant:{final:{messageId:'m', content:'Hello'}}};
        applyTranscript(state, {conversationId:'c', turns:[turn]});
        const key = state.turns[0].renderKey;
        expect(projectConversation(state).find(row => row.kind === 'iteration')).toMatchObject({feedback:turn.feedback, feedbackEditable:true});
        applyTurnFeedback(state, 't', {rating:-1, comment:'Changed', updatedAt:'2026-10-24T12:00:01Z'});
        expect(state.turns[0].feedback?.rating).toBe(-1);
        const clear = {rating:0 as const, comment:null, updatedAt:null};
        applyTranscript(state, {conversationId:'c', turns:[{...turn, feedback:clear, feedbackEditable:false,
            messages:[{messageId:'standalone',role:'assistant',content:'Hello'}]}]});
        expect(state.turns[0].renderKey).toBe(key);
        expect(state.turns[0].feedback).toEqual(clear);
        expect(projectConversation(state).filter(row => row.kind === 'assistant' || row.kind === 'iteration'))
            .toEqual(expect.arrayContaining([expect.objectContaining({feedback:clear, feedbackEditable:false})]));
        const dto = transcriptDTO({conversation:{conversationId:'c',turns:[{...turn,feedback:clear}]}});
        expect(dto.turns[0].feedback).toEqual(clear);
    });

    it('sends escaped PUT/DELETE routes with credentials and returns explicit null clears', async () => {
        const saved = {rating:1,comment:'Helpful',updatedAt:'2026-10-24T12:00:00Z'};
        const cleared = {rating:0,comment:null,updatedAt:null};
        const fetchImpl = vi.fn(async (_url: RequestInfo | URL, init?: RequestInit) =>
            new Response(JSON.stringify(init?.method === 'DELETE' ? cleared : saved), {headers:{'Content-Type':'application/json'}}));
        const client = new AgentlyClient({baseURL:'/v1',useCookies:true,tokenProvider:()=> 'fixture',fetchImpl});
        expect(await client.setTurnFeedback('c/a','t b',{rating:1,comment:'Helpful'})).toEqual(saved);
        expect(await client.clearTurnFeedback('c/a','t b')).toEqual(cleared);
        expect(fetchImpl.mock.calls.map(([url, init]) => [url,init?.method,init?.body])).toEqual([
            ['/v1/conversations/c%2Fa/turns/t%20b/feedback','PUT','{"rating":1,"comment":"Helpful"}'],
            ['/v1/conversations/c%2Fa/turns/t%20b/feedback','DELETE',undefined],
        ]);
        for (const [,init] of fetchImpl.mock.calls) {
            expect(init?.credentials).toBe('include');
            expect(new Headers(init?.headers).get('Authorization')).toBe('Bearer fixture');
        }
    });
});
