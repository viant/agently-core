import {describe,it,expect} from 'vitest';
import {applyEvent,applyLocalSubmit,applyTranscript,newConversationState} from './reducer';
import {projectConversation} from './projector';
describe('native user identity after protocol admission and concurrent bootstrap',()=>{
 it.each(['message_appended','turn_started'] as const)('joins an exact protocol-request alias on %s without losing the optimistic render key',type=>{
  const state=newConversationState('c');
  applyLocalSubmit(state,{conversationId:'c',clientRequestId:'client-user',content:'Repeated prompt'});
  const key=state.turns[0].users[0].renderKey;
  applyEvent(state,{type:'turn_started',conversationId:'c',turnId:'native-turn',userMessageId:'client-user',clientRequestId:'client-user'});
  applyTranscript(state,{conversationId:'c',turns:[{turnId:'native-turn',status:'running',user:{messageId:'native-user',content:'Repeated prompt'}}]});
  expect(state.turns[0].users).toHaveLength(2);
  applyEvent(state,{type,conversationId:'c',turnId:'native-turn',userMessageId:'native-user',messageId:'native-user',clientRequestId:'client-user',content:'Repeated prompt',patch:{role:'user'}});
  expect(state.turns[0].users).toHaveLength(1);
  expect(state.turns[0].users[0]).toMatchObject({renderKey:key,messageId:'native-user',clientRequestId:'client-user'});
  applyTranscript(state,{conversationId:'c',turns:[{turnId:'native-turn',status:'completed',user:{messageId:'native-user',content:'Repeated prompt'}}]});
  expect(projectConversation(state).filter(row=>row.kind==='user')).toHaveLength(1);
 });
 it('keeps equal text from separately identified submissions distinct',()=>{
  const state=newConversationState('c');
  applyLocalSubmit(state,{conversationId:'c',clientRequestId:'first',content:'Same text'});
  applyEvent(state,{type:'turn_started',conversationId:'c',turnId:'native-turn',userMessageId:'native-one',clientRequestId:'first'});
  applyEvent(state,{type:'message_appended',conversationId:'c',turnId:'native-turn',messageId:'native-two',clientRequestId:'second',content:'Same text',patch:{role:'user'}});
  expect(state.turns[0].users.map(user=>user.messageId)).toEqual(['native-one','native-two']);
 });
});
