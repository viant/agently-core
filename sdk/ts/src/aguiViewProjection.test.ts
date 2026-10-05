import { describe, expect, it } from 'vitest';
import type { Message } from '@ag-ui/core';
import { AgUiClient } from './agui';
import { AgUiViewProjection, type AgUiViewEvent, type AgUiViewDescriptor, type AgUiViewOutcome } from './aguiViewProjection';
import { applyEvent, applyLocalSubmit, newConversationState } from './chatStore/reducer';
import { projectConversation, type IterationRenderRow } from './chatStore/projector';

const start={type:'RUN_STARTED',threadId:'conversation',runId:'run'};
const finish={type:'RUN_FINISHED',threadId:'conversation',runId:'run'};
const metadata=(extra:Record<string,unknown>={})=>({agently:{presentation:{version:'1',conversationId:'conversation',nativeTurnId:'native-turn',pageId:'page',iteration:0,...extra}}});
function harness(profile:'standard'|'agently',events:object[],options:{baseline?:Message[];allowHostEffects?:boolean;optimistic?:boolean;displayQuery?:string}={}){
    const views:AgUiViewEvent[]=[],descriptors:AgUiViewDescriptor[]=[],outcomes:AgUiViewOutcome[]=[];
    let store=newConversationState('conversation');
    if(options.optimistic)store=applyLocalSubmit(store,{conversationId:'conversation',clientRequestId:'client-user',content:'question'});
    const projection=new AgUiViewProjection({profile,conversationId:'conversation',baselineMessages:options.baseline,displayQuery:options.displayQuery,allowHostEffects:options.allowHostEffects,onViewEvent:event=>{views.push(event);store=applyEvent(store,event);},onDescriptor:descriptor=>descriptors.push(descriptor),onOutcome:outcome=>outcomes.push(outcome)});
    const client=new AgUiClient({url:'/run',threadId:'conversation',initialMessages:[...(options.baseline??[]),{id:'client-user',role:'user',content:'question'}],fetch:async()=>new Response(events.map(event=>`data: ${JSON.stringify(event)}\n\n`).join(''),{headers:{'Content-Type':'text/event-stream'}})});
    return{views,descriptors,outcomes,projection,client,run:()=>client.run({runId:'run'},projection.subscriber),rows:()=>projectConversation(store),store:()=>store};
}

describe('official reduced AG-UI view projection',()=>{
    it('keeps native rounds stable when task text metadata omits pageId, including late old-message metadata changes',async()=>{
        const nativeMeta=(id:string,extra:Record<string,unknown>={})=>({agently:{presentation:{version:'1',conversationId:'conversation',nativeTurnId:'native-turn',nativeMessageId:id,modelCallId:id,iteration:0,mode:'task',...extra}}});
        const turn={type:'ACTIVITY_SNAPSHOT',messageId:'turn-status',activityType:'agently.turn',content:{version:'1',nativeTurnId:'native-turn',status:'running',queueSequence:'0'},metadata:metadata()};
        const h=harness('agently',[start,turn,
            {type:'STEP_STARTED',stepName:'interim',metadata:nativeMeta('interim',{pageId:'interim'})},
            {type:'TEXT_MESSAGE_START',messageId:'interim',role:'assistant',metadata:nativeMeta('interim')},
            {type:'TEXT_MESSAGE_CONTENT',messageId:'interim',delta:'I will create the draft.'},
            {type:'STEP_FINISHED',stepName:'interim',metadata:nativeMeta('interim',{pageId:'interim'})},
            {type:'STEP_STARTED',stepName:'final',metadata:nativeMeta('final',{pageId:'final'})},
            {type:'TEXT_MESSAGE_START',messageId:'final',role:'assistant',metadata:nativeMeta('final')},
            {type:'TEXT_MESSAGE_CONTENT',messageId:'final',delta:'Draft created: synthetic-plan-123.'},
            {type:'STEP_FINISHED',stepName:'final',metadata:nativeMeta('final',{pageId:'final'})},
            // A late old narration carries fuller page metadata while final text stays unchanged.
            {type:'ACTIVITY_SNAPSHOT',messageId:'interim-narration',activityType:'agently.narration',content:{version:'1',text:'I will create the draft.',status:'completed'},metadata:nativeMeta('interim',{pageId:'interim'})},
            {type:'TEXT_MESSAGE_END',messageId:'interim'}, {type:'TEXT_MESSAGE_END',messageId:'final'},finish],{optimistic:true});
        await h.run();
        const row=h.rows().find(row=>row.kind==='iteration') as IterationRenderRow;
        expect(row.rounds.find(round=>round.pageId==='final')?.content).toBe('Draft created: synthetic-plan-123.');
        expect(row.rounds.find(round=>round.pageId==='interim')?.content).toBe('I will create the draft.');
        expect(row.rounds.some(round=>round.pageId?.includes('/message/'))).toBe(false);
    });
    it('reads post-reduction absolute text and preserves existing renderer rows without a second accumulator',async()=>{
        const h=harness('standard',[start,{type:'TEXT_MESSAGE_START',messageId:'assistant',role:'assistant'},{type:'TEXT_MESSAGE_CONTENT',messageId:'assistant',delta:'hello '},{type:'TEXT_MESSAGE_CONTENT',messageId:'assistant',delta:'world'},{type:'TEXT_MESSAGE_END',messageId:'assistant'},finish],{optimistic:true});
        await h.run();
        const text=h.views.filter(event=>event.type==='text_delta');
        expect(text.map(event=>event.content)).toEqual(['','hello ','hello world']);
        expect(text.every(event=>event.contentMode==='snapshot')).toBe(true);
        const row=h.rows().find(row=>row.kind==='iteration') as IterationRenderRow;
        expect(row.rounds.some(round=>round.content==='hello world')).toBe(true);
        expect(h.rows().filter(row=>row.kind==='user')).toHaveLength(1);
        expect(h.client.messages.find(message=>message.id==='assistant')).toMatchObject({content:'hello world'});
        expect(h.outcomes.at(-1)).toMatchObject({phase:'success',pendingToolCallIds:[]});
    });

    it('projects reasoning snapshots while leaving encrypted and multipart data intact and unflattened',async()=>{
        const h=harness('standard',[start,{type:'REASONING_START',messageId:'span'},{type:'REASONING_MESSAGE_START',messageId:'reason',role:'reasoning'},{type:'REASONING_MESSAGE_CONTENT',messageId:'reason',delta:'think '},{type:'REASONING_MESSAGE_CONTENT',messageId:'reason',delta:'carefully'},{type:'REASONING_ENCRYPTED_VALUE',subtype:'message',entityId:'reason',encryptedValue:'opaque-private'},{type:'REASONING_MESSAGE_END',messageId:'reason'},{type:'REASONING_END',messageId:'span'},finish]);
        await h.run();
        expect(h.views.filter(event=>event.type==='reasoning_delta').map(event=>event.content)).toEqual(['','think ','think carefully']);
        expect(JSON.stringify(h.views)).not.toContain('opaque-private');
        expect(h.client.messages.find(message=>message.id==='reason')).toMatchObject({encryptedValue:'opaque-private'});
        const row=h.rows().find(row=>row.kind==='iteration') as IterationRenderRow;
        expect(row.rounds.some(round=>round.narration==='think carefully')).toBe(true);
    });

    it('does not replay other native turn history or unchanged same-turn baseline as live events',async()=>{
        const baseline:Message[]=[{id:'old',role:'assistant',content:'saved',metadata:metadata({nativeTurnId:'old-turn'})},{id:'same',role:'assistant',content:'already saved',metadata:metadata()}];
        const h=harness('agently',[{...start,metadata:{agently:{identityVersion:'1',nativeTurnId:'native-turn'}}},{type:'MESSAGES_SNAPSHOT',messages:baseline},{type:'TEXT_MESSAGE_START',messageId:'new',role:'assistant',metadata:metadata()},{type:'TEXT_MESSAGE_CONTENT',messageId:'new',delta:'live',metadata:metadata()},{type:'TEXT_MESSAGE_END',messageId:'new'},finish],{baseline});
        await h.run();
        expect(h.views.filter(event=>event.type==='text_delta').map(event=>event.messageId)).toEqual(['new','new']);
        expect(h.projection.getRunInfo()).toMatchObject({runId:'run',logicalTurnId:'native-turn'});
        expect(h.projection.getRunInfo().ownedMessageIds).not.toContain('old');
    });

    it('native queued descriptor never becomes actual started and explicit user correlation preserves optimistic identity',async()=>{
        const user={id:'client-user',role:'user',content:'question',metadata:metadata({clientMessageId:'client-user',clientRequestId:'client-user'})};
        const h=harness('agently',[{...start,metadata:{agently:{identityVersion:'1',nativeTurnId:'native-turn'}}},{type:'MESSAGES_SNAPSHOT',messages:[user]},{type:'ACTIVITY_SNAPSHOT',messageId:'turn-status',activityType:'agently.turn',content:{version:'1',nativeTurnId:'native-turn',status:'queued',queueSequence:'0'},metadata:metadata()},{type:'ACTIVITY_SNAPSHOT',messageId:'user-identity',activityType:'agently.user-identity',content:{version:'1',protocolRunId:'run',nativeTurnId:'native-turn',clientMessageId:'client-user',clientRequestId:'client-user',nativeUserMessageId:'native-user'},metadata:metadata()},{type:'ACTIVITY_SNAPSHOT',messageId:'turn-status',activityType:'agently.turn',content:{version:'1',nativeTurnId:'native-turn',status:'running',queueSequence:'0',startedByMessageId:'native-user'},metadata:metadata()},{type:'TEXT_MESSAGE_START',messageId:'assistant',role:'assistant',metadata:metadata()},{type:'TEXT_MESSAGE_CONTENT',messageId:'assistant',delta:'answer',metadata:metadata()},{type:'TEXT_MESSAGE_END',messageId:'assistant'},finish],{optimistic:true});
        await h.run();
        expect(h.views.filter(event=>event.type==='turn_started')).toHaveLength(1);
        expect(h.views.find(event=>event.type==='turn_queued')).toMatchObject({turnId:'native-turn',status:'queued',queueSequence:'0'});
        expect(h.views.find(event=>event.type==='turn_started')).toMatchObject({turnId:'native-turn',userMessageId:'native-user',clientRequestId:'client-user'});
        expect(h.descriptors.some(descriptor=>descriptor.kind==='presentation'&&descriptor.activity.kind==='agently.turn'&&descriptor.activity.status==='queued')).toBe(true);
        expect(h.rows().filter(row=>row.kind==='user')).toHaveLength(1);
    });

    it('tool argument end is request completion, not executor completion or frontend handoff resolution',async()=>{
        const h=harness('standard',[start,{type:'TOOL_CALL_START',toolCallId:'call',toolCallName:'lookup',parentMessageId:'assistant'},{type:'TOOL_CALL_ARGS',toolCallId:'call',delta:'{"q":'},{type:'TOOL_CALL_ARGS',toolCallId:'call',delta:'"question"}'},{type:'TOOL_CALL_END',toolCallId:'call'},finish]);
        await h.run();
        expect(h.views.filter(event=>event.type==='tool_call_completed')).toHaveLength(0);
        expect(h.views.filter(event=>event.toolRequestOnly).at(-1)).toMatchObject({arguments:{q:'question'},status:'requested'});
        expect(h.outcomes.at(-1)).toMatchObject({phase:'success',pendingToolCallIds:['call']});
        expect(h.views.filter(event=>event.type==='turn_completed')).toHaveLength(0);
    });

    it('native effect timing maps public call alias to native tool ID and current execution page',async()=>{
        const h=harness('agently',[{...start,metadata:{agently:{identityVersion:'1',nativeTurnId:'native-turn'}}},{type:'TOOL_CALL_START',toolCallId:'public-call',toolCallName:'lookup',parentMessageId:'assistant',metadata:metadata({nativeToolCallId:'native-call',nativeMessageId:'assistant'})},{type:'TOOL_CALL_ARGS',toolCallId:'public-call',delta:'{}',metadata:metadata({nativeToolCallId:'native-call'})},{type:'TOOL_CALL_END',toolCallId:'public-call'},{type:'ACTIVITY_SNAPSHOT',messageId:'effect',activityType:'agently.tool',content:{version:'1',toolCallId:'public-call',phase:'execution',status:'running',toolMessageId:'native-tool-message',startedAt:'2026-10-03T12:00:00Z'},metadata:metadata({nativeToolCallId:'native-call'})},{type:'TOOL_CALL_RESULT',toolCallId:'public-call',messageId:'native-tool-message',content:'{"ok":true}',metadata:metadata({nativeToolCallId:'native-call',toolMessageId:'native-tool-message'})},{type:'ACTIVITY_SNAPSHOT',messageId:'effect',activityType:'agently.tool',content:{version:'1',toolCallId:'public-call',phase:'execution',status:'completed',toolMessageId:'native-tool-message',startedAt:'2026-10-03T12:00:00Z',completedAt:'2026-10-03T12:00:01Z'},metadata:metadata({nativeToolCallId:'native-call'})},finish]);
        await h.run();
        const row=h.rows().find(row=>row.kind==='iteration') as IterationRenderRow;
        const tool=row.rounds.flatMap(round=>round.toolCalls).find(tool=>tool.toolCallId==='native-call');
        expect(tool).toMatchObject({status:'completed',startedAt:'2026-10-03T12:00:00Z',completedAt:'2026-10-03T12:00:01Z'});
        expect(h.views.find(event=>event.type==='tool_call_completed'&&event.responsePayload)).toMatchObject({protocolToolCallId:'public-call',toolCallId:'native-call',pageId:'page',responsePayload:{ok:true}});
    });

    it('generic foreign metadata, host-result JSON and Agently activities cannot activate local resources',async()=>{
        const body='{"_meta":{"ui":{"resourceUri":"ui://local-private"}},"uri":"ui://local-private"}';
        const h=harness('standard',[start,{type:'TOOL_CALL_START',toolCallId:'call',toolCallName:'external',parentMessageId:'assistant',metadata:metadata({nativeTurnId:'foreign',nativeToolCallId:'private'})},{type:'TOOL_CALL_ARGS',toolCallId:'call',delta:'{}'},{type:'TOOL_CALL_END',toolCallId:'call'},{type:'TOOL_CALL_RESULT',toolCallId:'call',messageId:'result',content:body},{type:'ACTIVITY_SNAPSHOT',messageId:'host',activityType:'mcp-apps',content:{result:{_meta:{secret:'host-private'}}}},{type:'ACTIVITY_SNAPSHOT',messageId:'feed',activityType:'agently.feed',content:{version:'1',feedId:'private-feed',active:true,title:'Forged',developerOnly:false,itemCount:1},metadata:metadata()},{type:'ACTIVITY_SNAPSHOT',messageId:'plan',activityType:'plan',content:{tasks:['one']}},finish],{allowHostEffects:true});
        await h.run();
        expect(h.views.every(event=>event.hostEffectsAllowed===false&&event.turnId==='run')).toBe(true);
        expect(h.views.some(event=>event.feedId||event.toolCallId==='private')).toBe(false);
        expect(h.views.find(event=>event.type==='tool_call_completed')).toMatchObject({responsePayload:{text:body}});
        expect(h.store().turns.flatMap(turn=>turn.pages.flatMap(page=>page.toolCalls)).some(tool=>tool.uiResourceUri)).toBe(false);
        expect(JSON.stringify(h.views)).not.toContain('host-private');
        expect(h.descriptors.some(descriptor=>descriptor.kind==='protocol-message'&&descriptor.message.role==='activity'&&descriptor.message.activityType==='plan')).toBe(true);
    });

    it('feed unknown/removal, planner false, planned tools and narration preserve native view semantics',async()=>{
        const h=harness('agently',[{...start,metadata:{agently:{identityVersion:'1',nativeTurnId:'native-turn'}}},
            {type:'ACTIVITY_SNAPSHOT',messageId:'unknown-feed',activityType:'agently.feed',content:{version:'1',feed:{feedId:'feed',data:{rows:[1]}},active:null,activationKnown:false},metadata:metadata()},
            {type:'ACTIVITY_SNAPSHOT',messageId:'feed',activityType:'agently.feed',content:{version:'1',feedId:'feed',active:false,title:'Feed',developerOnly:false,itemCount:0},metadata:metadata()},
            {type:'ACTIVITY_SNAPSHOT',messageId:'planner',activityType:'agently.planner',content:{version:'1',status:'validated',attempt:0,trigger:'',staticProfile:'',strategyFamily:'',secondPolicy:'',validated:false},metadata:metadata()},
            {type:'ACTIVITY_SNAPSHOT',messageId:'planned',activityType:'agently.tools-planned',content:{version:'1',calls:[{toolCallId:'planned-call',toolName:'tool'}]},metadata:metadata()},
            {type:'ACTIVITY_SNAPSHOT',messageId:'narration',activityType:'agently.narration',content:{version:'1',text:'Working',source:'narrator',status:'running',toolCallId:''},metadata:metadata()},finish]);
        await h.run();
        expect(h.views.filter(event=>event.type==='tool_feed_active')).toHaveLength(0);
        expect(h.views.find(event=>event.type==='tool_feed_unknown')).toMatchObject({feedId:'feed'});
        expect(h.views.find(event=>event.type==='tool_feed_inactive')).toMatchObject({feedId:'feed',feedItemCount:0});
        expect(h.views.find(event=>event.type==='planner.validated')).toMatchObject({plannerValidated:false,plannerAttempt:0});
        expect(h.views.find(event=>event.type==='narration')).toMatchObject({narration:'Working'});
        expect(h.views.some(event=>event.toolCallId==='planned-call')).toBe(false);
        const row=h.rows().find(row=>row.kind==='iteration') as IterationRenderRow;
        expect(row.rounds.some(round=>round.narration==='Working')).toBe(true);
    });

    it('returns standard interrupts separately without fabricated approval states',async()=>{
        const h=harness('standard',[start,{...finish,outcome:{type:'interrupt',interrupts:[{id:'ask',reason:'approval',responseSchema:{type:'boolean'}}]}}]);
        await h.run();
        expect(h.outcomes.at(-1)).toMatchObject({phase:'interrupt',interrupts:[{id:'ask',reason:'approval'}]});
        expect(h.views.some(event=>event.type==='elicitation_resolved'||event.type==='turn_completed')).toBe(false);
    });

    it('prefers friendly display text while retaining exact model query in official messages',async()=>{
        const h=harness('agently',[{...start,metadata:{agently:{identityVersion:'1',nativeTurnId:'native-turn'}}},{type:'MESSAGES_SNAPSHOT',messages:[{id:'client-user',role:'user',content:'hidden model instructions',metadata:metadata({clientRequestId:'client-user'})}]},{type:'ACTIVITY_SNAPSHOT',messageId:'turn',activityType:'agently.turn',content:{version:'1',nativeTurnId:'native-turn',status:'running',queueSequence:'0'},metadata:metadata()},finish],{displayQuery:'Friendly user question',optimistic:true});
        await h.run();
        expect(h.views.find(event=>event.type==='message_appended')).toMatchObject({content:'Friendly user question'});
        expect(h.client.messages.find(message=>message.id==='client-user')).toMatchObject({content:'hidden model instructions'});
    });

    it('preserves reduced native rich content and clears removed activity presentation',async()=>{
        const rendered={parts:[{kind:'data',data:{id:'rows',payload:{rows:[{n:1}]}}}]};
        const assistant={id:'assistant',role:'assistant',content:'plain caption',metadata:metadata({nativeMessageId:'assistant'})};
        const h=harness('agently',[{...start,metadata:{agently:{identityVersion:'1',nativeTurnId:'native-turn'}}},{type:'TEXT_MESSAGE_START',messageId:'assistant',role:'assistant',metadata:metadata({nativeMessageId:'assistant'})},{type:'TEXT_MESSAGE_CONTENT',messageId:'assistant',delta:'plain caption',metadata:metadata({nativeMessageId:'assistant'})},{type:'TEXT_MESSAGE_END',messageId:'assistant'},{type:'ACTIVITY_SNAPSHOT',messageId:'assistant/activity',activityType:'agently.rendered-content',content:{version:'1',renderedContent:rendered},metadata:metadata({nativeMessageId:'assistant'})},{type:'MESSAGES_SNAPSHOT',messages:[assistant],metadata:{'@ag-ui/client':{authoritativeActivityTypes:['agently.rendered-content']}}},finish],{allowHostEffects:true});
        await h.run();
        expect(h.views.some(event=>event.renderedContent?.parts?.[0]?.kind==='data')).toBe(true);
        expect(h.views.filter(event=>event.type==='text_delta').at(-1)).toMatchObject({content:'plain caption',renderedContent:null});
        expect(JSON.stringify(h.views.filter(event=>event.type==='text_delta').map(event=>event.content))).not.toContain('"rows"');
    });

    it('preserves nested source attribution and page grouping without replaying unrelated messages',async()=>{
        const childMetadata={agently:{presentation:{version:'1',conversationId:'child-conversation',nativeTurnId:'child-turn',pageId:'child-page',nativeMessageId:'child-answer'}}};
        const h=harness('agently',[{...start,metadata:{agently:{identityVersion:'1',nativeTurnId:'native-turn'}}},{type:'SUBAGENT_STARTED',subagentRunId:'child-run',name:'worker'},{type:'TEXT_MESSAGE_START',messageId:'child-answer',role:'assistant',subagentRunId:'child-run',metadata:childMetadata},{type:'TEXT_MESSAGE_CONTENT',messageId:'child-answer',delta:'child answer',subagentRunId:'child-run',metadata:childMetadata},{type:'TEXT_MESSAGE_END',messageId:'child-answer',subagentRunId:'child-run'},{type:'SUBAGENT_FINISHED',subagentRunId:'child-run'},finish]);
        await h.run();
        expect(h.views.find(event=>event.type==='text_delta'&&event.content==='child answer')).toMatchObject({conversationId:'child-conversation',turnId:'child-turn',pageId:'child-page',subagentRunId:'child-run'});
        expect(h.projection.getRunInfo().ownedMessageIds).toContain('child-answer');
    });

    it('preserves nullable/falsy official state and multipart parts as descriptors without mutating them',async()=>{
        const parts=[{type:'text',text:'caption'},{type:'image',source:{type:'url',value:'https://example.com/image.png'}}];
        const h=harness('standard',[start,{type:'STATE_SNAPSHOT',snapshot:null},{type:'MESSAGES_SNAPSHOT',messages:[{id:'client-user',role:'user',content:parts}]},finish]);
        await h.run();
        expect(h.descriptors.some(descriptor=>descriptor.kind==='state'&&descriptor.state===null)).toBe(true);
        expect(h.descriptors.some(descriptor=>descriptor.kind==='protocol-message'&&descriptor.message.id==='client-user'&&Array.isArray(descriptor.message.content))).toBe(true);
        expect(h.client.messages.find(message=>message.id==='client-user')?.content).toEqual(parts);
        expect(JSON.stringify(h.views.map(event=>event.content))).not.toContain('image.png');
    });
});

describe('standard authoritative message snapshots',()=>{
    it('projects snapshot-only new messages without turning baseline history into current rows',async()=>{
        const baseline:Message[]=[{id:'old',role:'assistant',content:'historic'}];
        const h=harness('standard',[start,{type:'MESSAGES_SNAPSHOT',messages:[...baseline,{id:'client-user',role:'user',content:'question'},{id:'new',role:'assistant',content:'snapshot answer'}]},finish],{baseline});
        await h.run();
        expect(h.views.filter(event=>event.type==='text_delta').map(event=>event.protocolMessageId)).toEqual(['new']);
        expect(h.views.find(event=>event.protocolMessageId==='new')).toMatchObject({content:'snapshot answer',contentMode:'snapshot',hostEffectsAllowed:false});
        expect(h.client.messages.map(message=>message.id)).toEqual(['old','client-user','new']);
    });
    it('explicitly describes history revisions and deletions, and updates current messages absolutely',async()=>{
        const baseline:Message[]=[{id:'old',role:'assistant',content:'historic'},{id:'removed-history',role:'assistant',content:'remove me'}];
        const user={id:'client-user',role:'user',content:'question'};
        const h=harness('standard',[start,
            {type:'MESSAGES_SNAPSHOT',messages:[...baseline,user,{id:'new',role:'assistant',content:'first'}]},
            {type:'MESSAGES_SNAPSHOT',messages:[{id:'old',role:'assistant',content:'corrected history'},user,{id:'new',role:'assistant',content:'replacement'}]},
            {type:'MESSAGES_SNAPSHOT',messages:[{id:'old',role:'assistant',content:'corrected history'},user]},finish],{baseline});
        await h.run();
        expect(h.views.filter(event=>event.type==='text_delta').map(event=>event.content)).toEqual(['first','replacement']);
        const snapshots=h.descriptors.filter(descriptor=>descriptor.kind==='protocol-snapshot');
        expect(snapshots[1]).toMatchObject({changedBaselineMessageIds:['old'],removedMessageIds:['removed-history'],hostEffectsAllowed:false});
        expect(snapshots[2]).toMatchObject({removedMessageIds:['new']});
        expect(h.client.messages.map(message=>message.id)).toEqual(['old','client-user']);
        expect(h.views.some(event=>event.protocolMessageId==='old')).toBe(false);
    });
});
