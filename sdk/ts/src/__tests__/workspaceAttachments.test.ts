import {describe,it,expect} from 'vitest';
import {applyTranscript,newConversationState} from '../chatStore/reducer';
import {projectConversation} from '../chatStore/projector';
import type {WorkspaceAttachmentState,CanonicalConversationState} from '../chatStore/types';

describe('structured workspace attachments',()=>{
 it('survives canonical hydration and message refinement without tool history',()=>{
  const attachment:WorkspaceAttachmentState={kind:'workspaceObject',objectId:'workspace:resource-1',label:'Resource',workspaceObject:{version:1,objectId:'workspace:resource-1',origin:{turnId:'turn-1'},content:{windowId:'resource-1',windowKey:'resource'}}};
  let state=newConversationState('c');
  const snapshot:CanonicalConversationState={conversationId:'c',turns:[{turnId:'turn-1',status:'completed',messages:[{messageId:'message-1',role:'assistant',content:'The resource is open.',attachments:[attachment]}]}]};
  state=applyTranscript(state,snapshot);
  let row=projectConversation(state).find(row=>row.kind==='assistant');
  expect(row?.kind==='assistant'&&row.attachments).toEqual([attachment]);
  state=applyTranscript(state,{...snapshot,turns:[{...snapshot.turns![0],messages:[{messageId:'message-1',role:'assistant',content:'The resource is still open.'}]}]});
  row=projectConversation(state).find(row=>row.kind==='assistant');
  expect(row?.kind==='assistant'&&row.attachments).toEqual([attachment]);
 });
});
