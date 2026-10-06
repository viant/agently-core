import { describe, expect, it } from 'vitest';
import { applyEvent, applyTranscript, newConversationState } from './reducer';
import { projectConversation } from './projector';
describe('transport host authority', () => {
    it('preserves denial through projection, later native-looking events and transcript additions', () => {
        const state=newConversationState('c');
        applyEvent(state,{type:'turn_started',conversationId:'c',turnId:'t',connectionProfile:'standard',hostEffectsAllowed:false});
        applyEvent(state,{type:'text_delta',conversationId:'c',turnId:'t',pageId:'p',messageId:'m',content:'```forge-ui\n{}\n```',contentMode:'snapshot'});
        applyEvent(state,{type:'tool_call_completed',conversationId:'c',turnId:'t',pageId:'p',toolCallId:'tool',toolName:'mcp:foreign',connectionProfile:'agently',hostEffectsAllowed:true,responsePayload:{_meta:{ui:{resourceUri:'ui://foreign'}}}});
        applyTranscript(state,{conversationId:'c',turns:[{turnId:'t',status:'completed',execution:{pages:[{pageId:'new',iteration:1,content:'hydrated',toolSteps:[{toolCallId:'hydrate-tool',toolName:'mcp:hydrate'}]}]}}]});
        const row=projectConversation(state).find(row=>row.kind==='iteration');
        expect(row).toMatchObject({connectionProfile:'standard',hostEffectsAllowed:false});
        if(row?.kind!=='iteration')throw new Error('missing row');
        expect(row.rounds.every(round=>round.hostEffectsAllowed===false)).toBe(true);
        expect(row.rounds.flatMap(round=>round.toolCalls).every(tool=>tool.hostEffectsAllowed===false)).toBe(true);
    });
    it('leaves legacy undefined authority undefined and ignores unrelated events', () => {
        const state=newConversationState('c');
        applyEvent(state,{type:'turn_started',conversationId:'c',turnId:'t'});
        expect(projectConversation(state)[0].hostEffectsAllowed).toBeUndefined();
        applyEvent(state,{type:'control',conversationId:'c',turnId:'ignored',hostEffectsAllowed:false});
        expect(state.turns).toHaveLength(1);
        applyEvent(state,{type:'turn_started',conversationId:'foreign',turnId:'f',hostEffectsAllowed:false});
        expect(state.turns[0].hostEffectsAllowed).toBeUndefined();
    });
});
