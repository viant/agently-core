import {expect,it,vi} from 'vitest';
import {AgentlyClient} from './client';
import {AgUiConversationTransport} from './aguiConversationTransport';
import {AgUiViewProjection} from './aguiViewProjection';
import {applyEvent,newConversationState} from './chatStore/reducer';
import {projectConversation} from './chatStore/projector';

it('restores a prematurely closed native stream with full text and one completed callback',async()=>{
 const posts:any[]=[];const chats:any[]=[];let complete=false;
 const meta={agently:{presentation:{version:'1',conversationId:'thread',nativeTurnId:'native-turn',nativeMessageId:'answer',mode:'task',modelCallId:'answer-model',pageId:'answer'}}};
 const text='Order 8911 has complete synthetic findings.';
 const response=(events:any[])=>new Response(events.map(e=>'data: '+JSON.stringify(e)+'\n\n').join(''),{headers:{'Content-Type':'text/event-stream'}});
 const host=new AgentlyClient({baseURL:'/v1',useCookies:true,fetchImpl:async(url,init)=>{
  if(String(url).includes('/conversations/'))return new Response(JSON.stringify({id:'thread'}),{headers:{'Content-Type':'application/json'}});
  const input=JSON.parse(String(init?.body));posts.push(input);
  const start={type:'RUN_STARTED',threadId:'thread',runId:input.runId,metadata:{agently:{identityVersion:'1',nativeTurnId:'native-turn'}}};
  if(input.forwardedProps?.agently?.operation==='conversation.bootstrap')return response([start,{type:'RUN_FINISHED',threadId:'thread',runId:input.runId,result:{version:'1',threadId:'thread',transcript:{schemaVersion:'2',conversation:{conversationId:'thread',turns:complete?[{turnId:'native-turn',status:'completed',messages:[]}]:[]}},messages:[],state:{},runs:[],projection:{lossless:true,unavailableMessageIds:[]}}}]);
  expect(input.forwardedProps?.agently?.operation).toBe('chat');chats.push(input);
  const prefix=[start,{type:'ACTIVITY_SNAPSHOT',messageId:'turn-status',activityType:'agently.turn',content:{version:'1',nativeTurnId:'native-turn',status:'running',queueSequence:'0'},metadata:meta},
   {type:'TEXT_MESSAGE_START',messageId:'answer',role:'assistant',metadata:meta},{type:'TEXT_MESSAGE_CONTENT',messageId:'answer',delta:'Order 8911'}];
  if(chats.length===1)return response(prefix);
  complete=true;return response([...prefix,{type:'TEXT_MESSAGE_CONTENT',messageId:'answer',delta:text.slice('Order 8911'.length)},{type:'TEXT_MESSAGE_END',messageId:'answer'},{type:'RUN_FINISHED',threadId:'thread',runId:input.runId,outcome:{type:'success'}}]);
 }});
 const transport=new AgUiConversationTransport(host,options=>new AgUiViewProjection(options));
 let state=newConversationState('thread');const ended=vi.fn();const errors=vi.fn();
 // Require streamed completion; a bootstrap snapshot cannot make this pass.
 const subscription=transport.subscribe('thread',{onEvent:event=>{state=applyEvent(state,event)},onTurnEnd:ended,onError:errors});
 const admitted=await transport.query({conversationId:'thread',query:'Synthetic request'});
 expect(admitted.turnId).toBe('native-turn');
 await vi.waitFor(()=>expect(ended).toHaveBeenCalledTimes(1));
 await vi.waitFor(()=>expect(transport.observesTurn('thread','native-turn')).toBe(false));
 expect(chats).toHaveLength(2);expect(chats[1]).toEqual(chats[0]);expect(errors).not.toHaveBeenCalled();
 const rows=projectConversation(state);const current=rows.find(row=>row.kind==='iteration'&&row.turnId==='native-turn');
 expect(current?.kind).toBe('iteration');
 if(current?.kind==='iteration'){
  expect(current.lifecycle).toBe('completed');expect(current.rounds.filter(r=>r.content===text)).toHaveLength(1);
 }
 expect(posts.filter(p=>p.forwardedProps?.agently?.operation!=='chat').every(p=>p.forwardedProps.agently.operation==='conversation.bootstrap')).toBe(true);
 subscription.close();transport.reset();
});
