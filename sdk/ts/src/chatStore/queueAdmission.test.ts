import { describe, expect, it } from 'vitest';
import { applyEvent, applyLocalSubmit, applyTranscript, newConversationState } from './reducer';
import { projectQueuedTurns } from './projector';
describe('native queued admission projection',()=>{
    it('uses committed queue state and exact integer ordering without treating admission as execution',()=>{
        const state=newConversationState('c');
        applyLocalSubmit(state,{conversationId:'c',clientRequestId:'optimistic',content:'Not admitted'});
        applyEvent(state,{type:'turn_queued',conversationId:'c',turnId:'second',clientRequestId:'two',content:'Second queued prompt',queueSequence:'9007199254740993'});
        applyEvent(state,{type:'turn_queued',conversationId:'c',turnId:'first',clientRequestId:'one',content:'First queued prompt',queueSequence:'9007199254740992'});
        expect(projectQueuedTurns(state).map(turn=>[turn.id,turn.queueSeq,turn.preview])).toEqual([['first','9007199254740992','First queued prompt'],['second','9007199254740993','Second queued prompt']]);
        expect(state.turns.find(turn=>turn.turnId==='first')?.lifecycle).toBe('pending');
        applyEvent(state,{type:'turn_started',conversationId:'c',turnId:'first'});
        expect(projectQueuedTurns(state).map(turn=>turn.id)).toEqual(['second']);
        applyEvent(state,{type:'turn_canceled',conversationId:'c',turnId:'second'});
        expect(projectQueuedTurns(state)).toEqual([]);
    });
    it('cold bootstrap preserves actual queued controls and ignores replayed queued admission after terminal state',()=>{
        const state=newConversationState('c');
        applyTranscript(state,{conversationId:'c',turns:[{turnId:'queued',status:'queued',queueSeq:0,user:{messageId:'u',content:'Cold queued prompt'}},{turnId:'done',status:'completed',user:{messageId:'done-user',content:'Done'}}]});
        expect(projectQueuedTurns(state).map(turn=>[turn.id,turn.queueSeq])).toEqual([['queued',0]]);
        applyEvent(state,{type:'turn_queued',conversationId:'c',turnId:'done',queueSequence:'2'});
        expect(state.turns.find(turn=>turn.turnId==='done')?.lifecycle).toBe('completed');
        expect(projectQueuedTurns(state).map(turn=>turn.id)).toEqual(['queued']);
    });
    it('uses authoritative post-move snapshots and fences stale admission replay with exact decimal order',()=>{
        const state=newConversationState('c');
        applyEvent(state,{type:'turn_queued',conversationId:'c',turnId:'a',queueSequence:'9007199254740992'});
        applyEvent(state,{type:'turn_queued',conversationId:'c',turnId:'b',queueSequence:'9007199254740993'});
        applyTranscript(state,{conversationId:'c',turns:[{turnId:'a',status:'queued',queueSequence:'9007199254740993'},{turnId:'b',status:'queued',queueSequence:'9007199254740992'}]});
        expect(projectQueuedTurns(state).map(turn=>turn.id)).toEqual(['b','a']);
        applyEvent(state,{type:'turn_queued',conversationId:'c',turnId:'a',queueSequence:'9007199254740992'});
        applyEvent(state,{type:'turn_queued',conversationId:'c',turnId:'b',queueSequence:'9007199254740993'});
        expect(projectQueuedTurns(state).map(turn=>[turn.id,turn.queueSeq])).toEqual([['b','9007199254740992'],['a','9007199254740993']]);
        applyTranscript(state,{conversationId:'c',turns:[{turnId:'a',status:'queued',queueSequence:'9007199254740991'},{turnId:'b',status:'queued',queueSequence:'9007199254740992'}]});
        expect(projectQueuedTurns(state).map(turn=>turn.id)).toEqual(['a','b']);
    });
    it('supports an explicit canonical native requeue after execution leaves queued',()=>{
        const state=newConversationState('c');
        applyEvent(state,{type:'turn_queued',conversationId:'c',turnId:'a',queueSequence:'10'});
        applyTranscript(state,{conversationId:'c',turns:[{turnId:'a',status:'queued',queueSequence:'10'}]});
        applyEvent(state,{type:'turn_started',conversationId:'c',turnId:'a'});
        expect(projectQueuedTurns(state)).toEqual([]);
        applyTranscript(state,{conversationId:'c',turns:[{turnId:'a',status:'running'}]});
        applyEvent(state,{type:'turn_queued',conversationId:'c',turnId:'a',queueSequence:'10'});
        expect(projectQueuedTurns(state)).toEqual([]);
        applyTranscript(state,{conversationId:'c',turns:[{turnId:'a',status:'queued',queueSequence:'20'}]});
        applyEvent(state,{type:'turn_queued',conversationId:'c',turnId:'a',queueSequence:'10'});
        expect(projectQueuedTurns(state).map(turn=>[turn.id,turn.queueSeq])).toEqual([['a','20']]);
    });

});
