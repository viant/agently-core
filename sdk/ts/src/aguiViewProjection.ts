import { isInternalMessageMode } from './messageVisibility';
import type { AgentSubscriber } from '@ag-ui/client';
import type { Message, State, Interrupt } from '@ag-ui/core';
import type { SSEEvent, JSONValue } from './types';
import type { CanonicalRenderedContent } from './chatStore/types';
import { readAgentlyPresentation, readAgentlyPresentationActivity, readAgentlyUsage, type AgentlyPresentation, type AgentlyPresentationActivity } from './aguiPresentation';

export interface AgUiViewEvent extends SSEEvent {
    connectionProfile: 'standard' | 'agently';
    /** The renderer must enforce this marker before mounting local resource/host controls. */
    hostEffectsAllowed: boolean;
    protocolRunId: string;
    protocolMessageId?: string;
    protocolToolCallId?: string;
    subagentRunId?: string;
    clientRequestId?: string;
    toolRequestOnly?: boolean;
    queueSequence?: string;
}
export interface AgUiViewOutcome {
    conversationId: string; runId: string; logicalTurnId: string;
    phase: 'started' | 'success' | 'interrupt' | 'cancelled' | 'error';
    interrupts?: readonly Interrupt[]; pendingToolCallIds?: readonly string[]; result?: unknown; error?: Error;
}
export type AgUiViewDescriptor =
    | { kind: 'protocol-snapshot'; messages: readonly Message[]; changedBaselineMessageIds: readonly string[]; removedMessageIds: readonly string[]; hostEffectsAllowed: false }
    | { kind: 'presentation'; messageId: string; activity: AgentlyPresentationActivity; hostEffectsAllowed: boolean }
    | { kind: 'protocol-message'; message: Message; hostEffectsAllowed: false }
    | { kind: 'host-activity'; message: Message; hostEffectsAllowed: true }
    | { kind: 'unsupported-activity'; messageId: string; activityType: string; hostEffectsAllowed: false }
    | { kind: 'state'; state: State; hostEffectsAllowed: boolean };
export interface AgUiViewRunInfo {
    conversationId: string; runId?: string; logicalTurnId?: string; nativeTurnId?: string;
    ownedMessageIds: readonly string[];
}
export interface AgUiViewProjectionOptions {
    profile: 'standard' | 'agently'; conversationId: string;
    runId?: string; logicalTurnId?: string;
    baselineMessages?: readonly Message[];
    displayQuery?: string;
    /** Opt in only for a trusted Agently connection with host capabilities. */
    allowHostEffects?: boolean;
    onViewEvent(event: AgUiViewEvent): void;
    onOutcome?(outcome: AgUiViewOutcome): void;
    onDescriptor?(descriptor: AgUiViewDescriptor): void;
}
interface Lane { presentation?: AgentlyPresentation; owner?: string; parent?: string; pageId?: string }
const object = (value: unknown): Record<string, unknown> | undefined => value && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : undefined;
const string = (value: unknown): string | undefined => typeof value === 'string' ? value : undefined;
const signature = (value: unknown): string => JSON.stringify(value,(_key,item)=>{
    const row=object(item);return row?Object.fromEntries(Object.keys(row).sort().map(key=>[key,row[key]])):item;
});

/**
 * Read-only adapter into existing renderer events. onEvent runs BEFORE upstream
 * reduction: it records identities only. Text/arguments/results are projected
 * from onMessagesChanged's complete official graph, never accumulated here.
 */
export class AgUiViewProjection {
    readonly subscriber: AgentSubscriber;
    private runId?: string;
    private nativeTurnId?: string;
    private inputUserId?: string;
    private messages: readonly Message[] = [];
    private readonly baseline = new Map<string, string>();
    private readonly baselineUserText = new Map<string,string>();
    private readonly owned = new Map<string, Lane>();
    private readonly calls = new Map<string, Lane>();
    private readonly steps = new Map<string, Lane>();
    private readonly emitted = new Map<string, string>();
    private readonly nativeStatus = new Map<string, string>();
    private readonly userIds = new Map<string, string>();
    private readonly toolEffects = new Map<string, Extract<AgentlyPresentationActivity,{kind:'agently.tool'}>>();
    private readonly hostEffects: boolean;
    private pendingStandardSnapshot = false;
    private priorSnapshotIds = new Set<string>();

    constructor(private readonly options: AgUiViewProjectionOptions) {
        if (!options.conversationId) throw new Error('View projection requires conversation identity');
        this.runId = options.runId;
        this.nativeTurnId = options.profile === 'agently' ? options.logicalTurnId : undefined;
        this.hostEffects = options.profile === 'agently' && options.allowHostEffects === true;
        for (const message of options.baselineMessages ?? []) {this.baseline.set(message.id, signature(message));if(message.role==='user'&&typeof message.content==='string')this.baselineUserText.set(message.id,message.content);}
        this.subscriber = {
            onRunInitialized: ({ input }) => {
                this.runId = this.options.runId ?? input.runId;
                for (const message of input.messages) if (!this.baseline.has(message.id)) this.baseline.set(message.id, signature(message));
                const last = input.messages.at(-1);
                if (last?.role === 'user') { this.inputUserId = last.id; this.owned.set(last.id, {}); }
            },
            onEvent: ({ event }) => this.observe(event as unknown as Record<string, unknown>),
            onMessagesChanged: ({ messages, state }) => this.consumeReduced(messages, state),
            onStateChanged: ({ state }) => this.options.onDescriptor?.({ kind:'state', state:structuredClone(state), hostEffectsAllowed:this.hostEffects }),
            onRunFinishedEvent: params => {
                this.consumeReduced(params.messages, params.state);
                this.options.onOutcome?.({ ...this.outcomeBase(), phase:params.outcome,
                    ...(params.outcome==='interrupt'?{interrupts:structuredClone(params.interrupts)}:{}),
                    ...(params.outcome==='success'?{pendingToolCallIds:[...params.pendingToolCallIds],result:params.result}:{}) });
                if (params.outcome==='cancelled'||params.outcome==='success'&&!params.pendingToolCallIds.length) this.emit({type:params.outcome==='cancelled'?'turn_canceled':'turn_completed',status:params.outcome==='cancelled'?'canceled':'completed'});
            },
            onRunErrorEvent: ({ event }) => {
                this.options.onOutcome?.({ ...this.outcomeBase(),phase:'error',error:new Error(event.message) });
                this.emit({type:'turn_failed',status:'failed',error:event.message});
            },
            onRunFailed: ({error})=>{this.options.onOutcome?.({...this.outcomeBase(),phase:'error',error});this.emit({type:'turn_failed',status:'failed',error:error.message});},
        };
    }

    getRunInfo = (): AgUiViewRunInfo => ({ conversationId:this.options.conversationId,runId:this.runId,logicalTurnId:this.turnId(),nativeTurnId:this.nativeTurnId,ownedMessageIds:[...this.owned.keys()] });

    consumeReduced(messages: readonly Message[], _state?: State): void {
        const previousMessages = this.messages;
        this.messages = messages;
        if (this.pendingStandardSnapshot) {
            this.pendingStandardSnapshot = false;
            const ids = new Set(messages.map(message => message.id));
            const removedMessageIds = [...new Set([...this.priorSnapshotIds,...previousMessages.map(message=>message.id)])].filter(id => !ids.has(id));
            const changedBaselineMessageIds = messages.filter(message => this.baseline.has(message.id) && this.baseline.get(message.id) !== signature(message) && !this.owned.has(message.id)).map(message => message.id);
            for (const id of removedMessageIds) this.owned.delete(id);
            this.priorSnapshotIds = ids;
            this.descriptor('protocol-snapshot', { kind: 'protocol-snapshot', messages: structuredClone(messages), changedBaselineMessageIds, removedMessageIds, hostEffectsAllowed: false });
        }
        // Descriptors establish actual native lifecycle/identity before text is
        // mapped into the existing store. The official message bodies stay intact.
        for (const message of messages) if (message.role==='activity' && this.isOwned(message)) this.projectActivity(message);
        for (const message of messages) if (message.role!=='activity' && this.isOwned(message)) this.projectMessage(message);
    }

    private metadata(value: unknown) { return this.options.profile==='agently' ? readAgentlyPresentation(value) : undefined; }
    private turnId(lane?: Lane): string { return lane?.presentation?.nativeTurnId ?? this.nativeTurnId ?? this.options.logicalTurnId ?? this.runId ?? ''; }
    private outcomeBase() { return {conversationId:this.options.conversationId,runId:this.runId ?? '',logicalTurnId:this.turnId()}; }
    private binding(message: Message): Lane {
        const owned=this.owned.get(message.id), metadata=this.metadata(message.metadata);
        return {...owned,presentation:metadata&&owned?.presentation?.messageKind==='standalone'&&!metadata.modelCallId
            ?{...metadata,messageKind:'standalone'}:metadata??owned?.presentation};
    }
    private isOwned(message: Message): boolean {
        if (this.owned.has(message.id)) return true;
        const metadata = this.metadata(message.metadata);
        if (metadata?.nativeTurnId && metadata.nativeTurnId===this.nativeTurnId) {
            // A baseline message without changes is history, even in a reused turn.
            if (this.baseline.get(message.id)===signature(message)) return false;
            this.owned.set(message.id,{presentation:metadata});return true;
        }
        return false;
    }
    private observe(event: Record<string,unknown>): void {
        const kind=string(event.type), metadata=this.metadata(event.metadata), owner=string(event.subagentRunId);
        if (kind==='RUN_STARTED') {
            this.runId=string(event.runId)??this.runId;
            if (!owner && metadata?.nativeTurnId) this.nativeTurnId=metadata.nativeTurnId;
            this.options.onOutcome?.({...this.outcomeBase(),phase:'started'});
            if(this.options.profile==='standard')this.emit({type:'turn_started',status:'running',userMessageId:this.inputUserId,clientRequestId:this.inputUserId});
            return;
        }
        const id=string(event.messageId),callId=string(event.toolCallId),parent=string(event.parentMessageId);
        const lane:Lane={presentation:metadata,owner,parent,pageId:metadata?.pageId};
        if(id&&kind!=='MESSAGES_SNAPSHOT'){
            const previous=this.owned.get(id)?.presentation;
            const presentation=metadata&&previous?.messageKind==='standalone'&&!metadata.modelCallId
                ?{...metadata,messageKind:previous.messageKind}:metadata??previous;
            this.owned.set(id,{...this.owned.get(id),...lane,presentation});
        }
        if(callId){this.calls.set(callId,{...this.calls.get(callId),...lane,presentation:metadata??this.calls.get(callId)?.presentation});if(parent)this.owned.set(parent,{...this.owned.get(parent),...lane});}
        if(kind==='MESSAGES_SNAPSHOT'&&this.options.profile==='standard'&&Array.isArray(event.messages)){
            this.pendingStandardSnapshot=true;
            // A snapshot is replacement state, not a new turn for prior history.
            // Preserve current-run ownership; only newly introduced IDs join it.
            for(const raw of event.messages){const message=object(raw),mid=string(message?.id);if(mid&&!this.baseline.has(mid))this.owned.set(mid,{});}
            if(!this.priorSnapshotIds.size)this.priorSnapshotIds=new Set([...this.baseline.keys(),...this.messages.map(message=>message.id)]);
        }
        if (kind === 'MESSAGES_SNAPSHOT' && Array.isArray(event.messages)) {
            for (const raw of event.messages) {
                const message = object(raw);
                if (!message) continue;
                const mid = string(message.id), presentation = this.metadata(message.metadata);
                if (!mid || presentation?.nativeTurnId === undefined || presentation.nativeTurnId !== this.nativeTurnId
                    || !this.owned.has(mid) && this.baseline.get(mid) === signature(message)) continue;
                const previous = this.owned.get(mid);
                // A terminal snapshot can refine text without carrying the live
                // message/add origin. Preserve that exact message classification;
                // explicit model ownership still supersedes it.
                const retained = previous?.presentation?.messageKind === 'standalone' && !presentation.modelCallId
                    ? {...presentation, messageKind: previous.presentation.messageKind} : presentation;
                this.owned.set(mid, {...previous, presentation: retained, owner: string(message.subagentRunId)});
            }
        }
        if(kind==='STEP_STARTED'||kind==='STEP_FINISHED'){
            const step=string(event.stepName)??'';if(kind==='STEP_STARTED')this.steps.set(step,lane);
            const found={...this.steps.get(step),...lane,presentation:metadata??this.steps.get(step)?.presentation};
            this.emit({type:kind==='STEP_STARTED'?'model_started':'model_completed',modelCallId:metadata?.modelCallId??step,status:kind==='STEP_STARTED'?'running':'completed'},found);
        }
        if(kind==='CUSTOM'&&this.options.profile==='agently'){
            if(event.name==='agently.usage'){const usage=readAgentlyUsage(event.value);if(usage)this.emit({type:'usage',modelCallId:usage.modelCallId,turnId:usage.nativeTurnId,usage:{...usage.usage,scope:usage.scope} as SSEEvent['usage']},lane);}
            if(event.name==='agently.linked-conversation'){const value=object(event.value);if(value?.version==='1')this.emit({type:'linked_conversation_attached',linkedConversationId:string(value.conversationId),linkedConversationAgentId:string(value.agentId),linkedConversationTitle:string(value.title),protocolToolCallId:string(value.parentToolCallId)},lane);}
        }
    }

    private fields(lane:Lane={}): Partial<AgUiViewEvent> {
        const p=lane.presentation;
        return {conversationId:p?.conversationId??this.options.conversationId,turnId:this.turnId(lane),pageId:p?.pageId??lane.pageId,iteration:p?.iteration,phase:p?.phase,mode:p?.mode,executionRole:p?.executionRole,agentIdUsed:p?.agentId,agentName:p?.agentName,modelCallId:p?.modelCallId,provider:p?.provider,modelName:p?.model,createdAt:p?.createdAt,startedAt:p?.startedAt,completedAt:p?.completedAt,requestPayloadId:p?.requestPayloadId,responsePayloadId:p?.responsePayloadId,providerRequestPayloadId:p?.providerRequestPayloadId,providerResponsePayloadId:p?.providerResponsePayloadId,streamPayloadId:p?.streamPayloadId,subagentRunId:lane.owner};
    }
    private emit(event:Partial<AgUiViewEvent>&{type:SSEEvent['type']},lane:Lane={},key?:string):void {
        const value:AgUiViewEvent={...this.fields(lane),...event,connectionProfile:this.options.profile,hostEffectsAllowed:this.hostEffects,protocolRunId:this.runId??''};
        const identity=key??`${value.type}:${value.turnId}:${value.messageId??value.toolCallId??value.modelCallId??''}`;
        const signature=JSON.stringify(value);if(this.emitted.get(identity)===signature)return;this.emitted.set(identity,signature);this.options.onViewEvent(value);
    }
    private descriptor(key:string,value:AgUiViewDescriptor):void {const signature=JSON.stringify(value);if(this.emitted.get(key)===signature)return;this.emitted.set(key,signature);this.options.onDescriptor?.(value);}

    private projectMessage(message:Message):void {
        const lane=this.binding(message),p=lane.presentation;
        if(message.role==='user'){
            const turn=this.turnId(lane),nativeId=this.userIds.get(turn)??p?.nativeUserMessageId??message.id;
            const display=this.baselineUserText.get(nativeId)??this.baselineUserText.get(message.id)??(message.id===this.inputUserId?this.options.displayQuery:undefined)??(typeof message.content==='string'?message.content:undefined);
            if(typeof message.content!=='string')this.descriptor(`message:${message.id}`,{kind:'protocol-message',message:structuredClone(message),hostEffectsAllowed:false});
            if(display!==undefined&&(this.options.profile==='standard'||this.nativeStatus.get(turn)==='running'))this.emit({type:'message_appended',messageId:nativeId,userMessageId:nativeId,clientRequestId:p?.clientRequestId??message.id,content:display,contentMode:'snapshot',patch:{role:'user'},protocolMessageId:message.id},lane);
            return;
        }
        if(message.role==='assistant'||message.role==='reasoning'){
            if(isInternalMessageMode(p?.mode)){
                // Keep tool-call containers, never their internal message body.
            }else if(typeof message.content!=='string'){
                if(Array.isArray(message.content)||'encryptedValue' in message)this.descriptor(`message:${message.id}`,{kind:'protocol-message',message:structuredClone(message),hostEffectsAllowed:false});
            }else{
                const renderedContent = message.role === 'assistant' && this.options.profile === 'agently'
                    ? this.renderedFor(message.id) : undefined;
                const standalone = message.role === 'assistant' && p?.messageKind === 'standalone' && !p?.modelCallId;
                const typedReport = message.role === 'assistant' && !!renderedContent?.reports?.length;
                const snapshotAssistant = standalone || typedReport;
                this.emit({
                    type: message.role === 'reasoning' ? 'reasoning_delta' : snapshotAssistant ? 'assistant' : 'text_delta',
                    messageId: p?.nativeMessageId ?? message.id,
                    assistantMessageId: p?.nativeMessageId ?? message.id,
                    content: message.content,
                    contentMode: 'snapshot',
                    ...(message.role === 'assistant' && this.options.profile === 'agently' ? {renderedContent} : {}),
                    ...(snapshotAssistant ? {patch: {role: 'assistant'}} : {}),
                    protocolMessageId: message.id,
                    pageId: standalone ? undefined : p?.pageId ?? lane.pageId ?? p?.nativeMessageId ?? `${this.runId}/message/${message.id}`,
                }, lane);
            }
            if(message.role==='assistant')for(const call of message.toolCalls??[]){
                const toolLane={...lane,...this.calls.get(call.id),parent:message.id};
                const native=toolLane.presentation?.nativeToolCallId??call.id;
                let args:JSONValue|undefined;try{args=JSON.parse(call.function.arguments) as JSONValue;}catch{/* partial arguments stay in the official graph */}
                const effect=this.toolEffects.get(call.id);
                this.emit({type:'tool_call_started',toolCallId:native,toolName:call.function.name,assistantMessageId:p?.nativeMessageId??message.id,arguments:args,status:effect?.status??'requested',startedAt:effect?.startedAt,completedAt:effect?.completedAt,toolRequestOnly:true,protocolToolCallId:call.id,pageId:toolLane.presentation?.pageId??`${this.runId}/message/${message.id}`},toolLane,`tool-request:${call.id}`);
            }
        }else if(message.role==='tool'){
            const toolLane={...this.calls.get(message.toolCallId),...lane,presentation:p??this.calls.get(message.toolCallId)?.presentation};
            const content=typeof message.content==='string'?message.content:undefined;
            if(content===undefined){this.descriptor(`message:${message.id}`,{kind:'protocol-message',message:structuredClone(message),hostEffectsAllowed:false});return;}
            let responsePayload:JSONValue|undefined;
            if(this.options.profile==='agently'){try{responsePayload=JSON.parse(content) as JSONValue;}catch{responsePayload={text:content};}}
            else responsePayload={text:content}; // passive wrapper; never expose _meta/ui binding shape to native derivation
            this.emit({type:message.error?'tool_call_failed':'tool_call_completed',toolCallId:toolLane.presentation?.nativeToolCallId??message.toolCallId,toolMessageId:toolLane.presentation?.toolMessageId??message.id,content,responsePayload,error:message.error,status:message.error?'failed':'completed',protocolToolCallId:message.toolCallId,protocolMessageId:message.id,pageId:toolLane.presentation?.pageId??`${this.runId}/message/${toolLane.parent??message.id}`},toolLane,`tool-result:${message.id}`);
        }
    }

    private projectActivity(message:Message):void {
        if(message.role!=='activity')return;
        if(this.options.profile!=='agently'){
            if(message.activityType==='mcp-apps'||message.activityType.startsWith('agently.'))this.descriptor(`activity:${message.id}`,{kind:'unsupported-activity',messageId:message.id,activityType:message.activityType,hostEffectsAllowed:false});
            else this.descriptor(`activity:${message.id}`,{kind:'protocol-message',message:structuredClone(message),hostEffectsAllowed:false});
            return;
        }
        const activity=readAgentlyPresentationActivity(message),lane=this.binding(message);
        if(activity){
            this.descriptor(`activity:${message.id}`,{kind:'presentation',messageId:message.id,activity,hostEffectsAllowed:this.hostEffects});
            switch(activity.kind){
                case 'agently.user-identity':this.userIds.set(activity.nativeTurnId,activity.nativeUserMessageId);break;
                case 'agently.turn':{
                    // Root lifecycle activity establishes the current native turn
                    // when attach only knows the protocol run ID. Message history
                    // and child activities must never select the run's destination.
                    if (!this.nativeTurnId && !lane.owner
                        && (activity.status === 'queued' || activity.status === 'running')
                        && (!lane.presentation?.conversationId || lane.presentation.conversationId === this.options.conversationId)) {
                        this.nativeTurnId = activity.nativeTurnId;
                    }
                    this.nativeStatus.set(activity.nativeTurnId,activity.status);
                    if(activity.status==='queued')this.emit({type:'turn_queued',turnId:activity.nativeTurnId,status:'queued',queueSequence:activity.queueSequence,clientRequestId:this.inputUserId,content:this.options.displayQuery},lane);
                    else if(activity.status==='running')this.emit({type:'turn_started',turnId:activity.nativeTurnId,status:'running',userMessageId:activity.startedByMessageId??this.userIds.get(activity.nativeTurnId)??this.inputUserId,clientRequestId:this.inputUserId},lane);
                    else if(activity.status==='completed'||activity.status==='failed'||activity.status==='canceled')this.emit({type:activity.status==='completed'?'turn_completed':activity.status==='failed'?'turn_failed':'turn_canceled',turnId:activity.nativeTurnId,status:activity.status},lane);
                    break;
                }
                case 'agently.tool':this.toolEffects.set(activity.toolCallId,activity);this.emit({type:activity.phase==='waiting'?'tool_call_waiting':activity.status==='completed'?'tool_call_completed':activity.status==='failed'?'tool_call_failed':activity.status==='canceled'?'tool_call_canceled':'tool_call_started',toolCallId:activity.presentation?.nativeToolCallId??this.calls.get(activity.toolCallId)?.presentation?.nativeToolCallId??activity.toolCallId,protocolToolCallId:activity.toolCallId,toolMessageId:activity.toolMessageId,startedAt:activity.startedAt,completedAt:activity.completedAt,status:activity.status},lane,`tool-effect:${activity.toolCallId}`);break;
                case 'agently.feed':this.emit({type:!activity.activationKnown?'tool_feed_unknown':activity.active?'tool_feed_active':'tool_feed_inactive',feedId:activity.feedId,feedTitle:activity.title,feedDeveloperOnly:activity.developerOnly,feedItemCount:activity.itemCount,feedData:activity.data as JSONValue,feedIcon:string(activity.feedPresentation?.icon),feedAccent:string(activity.feedPresentation?.accent),feedTarget:activity.feedPresentation?.target as SSEEvent['feedTarget']},lane,`feed:${activity.feedId}`);break;
                case 'agently.planner':this.emit({type:`planner.${activity.status}` as SSEEvent['type'],plannerTrigger:activity.trigger,plannerStaticProfile:activity.staticProfile,plannerStrategyFamily:activity.strategyFamily,plannerAttempt:activity.attempt,plannerSecondPolicy:activity.secondPolicy,plannerValidated:activity.validated,plannerOutputPayloadId:activity.outputPayloadId},lane,`planner:${message.id}`);break;
                case 'agently.tools-planned':this.emit({type:'tool_calls_planned',toolCallsPlanned:activity.calls.map(call=>({toolCallId:this.calls.get(call.toolCallId)?.presentation?.nativeToolCallId??call.toolCallId,toolName:call.toolName}))},lane,`planned:${message.id}`);break;
                case 'agently.narration':this.emit({type:'narration',messageId:activity.presentation?.nativeMessageId??message.id,narration:activity.text,status:activity.status,protocolMessageId:message.id},lane,`narration:${message.id}`);break;
            }
            return;
        }
        const content=object(message.content);
        if(message.activityType==='agently.rendered-content'&&content?.version==='1'&&object(content.renderedContent)){
            const id=lane.presentation?.nativeMessageId??message.id.replace(/\/activity$/,'');
            const text=this.messages.find(m=>m.id===id&&m.role==='assistant');
            this.emit({type:'assistant',patch:{role:'assistant'},messageId:id,assistantMessageId:id,content:typeof text?.content==='string'?text.content:'',contentMode:'snapshot',renderedContent:content.renderedContent as unknown as CanonicalRenderedContent,protocolMessageId:message.id,pageId:lane.presentation?.pageId??`${this.runId}/message/${id}`},lane,`rich:${message.id}`);
        }else if(message.activityType==='mcp-apps'&&this.hostEffects)this.descriptor(`activity:${message.id}`,{kind:'host-activity',message:structuredClone(message),hostEffectsAllowed:true});
        else this.descriptor(`activity:${message.id}`,{kind:'unsupported-activity',messageId:message.id,activityType:message.activityType,hostEffectsAllowed:false});
    }

    private renderedFor(id:string):CanonicalRenderedContent|null {
        for(const message of this.messages){
            if(message.role!=='activity'||message.activityType!=='agently.rendered-content'||!this.isOwned(message))continue;
            const content=object(message.content),owner=this.metadata(message.metadata)?.nativeMessageId??message.id.replace(/\/activity$/,'');
            if(owner===id&&content?.version==='1'&&object(content.renderedContent))return content.renderedContent as unknown as CanonicalRenderedContent;
        }
        return null;
    }
}
