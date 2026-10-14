import {describe,it,expect} from 'vitest';
import {AgUiViewProjection} from '../aguiViewProjection';
import {newConversationState,applyEvent,applyLocalSubmit} from '../chatStore/reducer';
import {projectConversation} from '../chatStore/projector';

describe('native request lifecycle and recovered protocol failures',()=>{
 it('keeps four failed continuation runs diagnostic without four failed user requests',()=>{
  const state=newConversationState('thread'),outcomes:any[]=[];
  applyEvent(state,{type:'turn_started',conversationId:'thread',turnId:'native'});
  for(let n=0;n<4;n++){
   const view=new AgUiViewProjection({profile:'agently',conversationId:'thread',runId:`continuation-${n}`,allowHostEffects:true,onViewEvent:e=>applyEvent(state,e),onOutcome:o=>outcomes.push(o)});
   if (n%2) view.subscriber.onRunErrorEvent?.({event:{type:'RUN_ERROR',message:'Synthetic continuation unavailable'}} as any);
   else view.subscriber.onRunFailed?.({error:new Error('Synthetic continuation unavailable')} as any);
  }
  applyEvent(state,{type:'turn_completed',conversationId:'thread',turnId:'native'});
  expect(outcomes).toHaveLength(4);expect(outcomes.every(o=>o.phase==='error')).toBe(true);
  expect(projectConversation(state).filter(r=>r.kind==='iteration').map(r=>(r as any).lifecycle)).toEqual(['completed']);
 });
 it('preserves exact initial rejection but cannot fail an admitted native request',()=>{
  for(const admitted of [false,true]) {
   const state=newConversationState('thread');applyLocalSubmit(state,{conversationId:'thread',clientRequestId:'request',content:'Generic prompt',mode:'submit'});
   if(admitted)applyEvent(state,{type:'turn_started',conversationId:'thread',turnId:'native',clientRequestId:'request'});
   const view=new AgUiViewProjection({profile:'agently',conversationId:'thread',runId:'protocol',onViewEvent:e=>applyEvent(state,e)});
   view.subscriber.onRunInitialized?.({input:{runId:'protocol',messages:[{id:'request',role:'user',content:'Generic prompt'}]}} as any);
   view.subscriber.onRunFailed?.({error:new Error('Synthetic request error')} as any);
   expect(state.turns).toHaveLength(1);expect(state.turns[0].lifecycle).toBe(admitted?'running':'failed');
  }
 });
 it('preserves real native failure and standard protocol failure',()=>{
  const standard:any[]=[];const view=new AgUiViewProjection({profile:'standard',conversationId:'thread',runId:'standard',onViewEvent:e=>standard.push(e)});
  view.subscriber.onRunFailed?.({error:new Error('Synthetic actual request error')} as any);
  expect(standard.find(e=>e.type==='turn_failed')?.turnId).toBe('standard');
  const native:any[]=[];const owned=new AgUiViewProjection({profile:'agently',conversationId:'thread',runId:'transport',logicalTurnId:'native',onViewEvent:e=>native.push(e)});
  owned.consumeReduced([{id:'lifecycle',role:'activity',activityType:'agently.turn',content:{version:'1',nativeTurnId:'native',status:'failed',queueSequence:''},metadata:{agently:{identityVersion:'1',nativeTurnId:'native'}}} as any]);
  expect(native.find(e=>e.type==='turn_failed')?.turnId).toBe('native');
 });
});
