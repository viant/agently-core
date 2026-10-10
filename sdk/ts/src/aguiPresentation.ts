import type { Message } from '@ag-ui/core';

export interface AgentlyPresentation {
    version: '1'; messageKind?: 'standalone'; conversationId?: string; nativeTurnId?: string; nativeMessageId?: string;
    parentMessageId?: string; pageId?: string; modelCallId?: string; nativeToolCallId?: string; toolMessageId?: string;
    protocolRunId?:string; clientMessageId?:string; clientRequestId?:string; nativeUserMessageId?:string;
    executionRole?: string; phase?: string; mode?: string; status?: string; agentId?: string; agentName?: string;
    provider?: string; model?: string; requestPayloadId?: string; responsePayloadId?: string;
    providerRequestPayloadId?: string; providerResponsePayloadId?: string; streamPayloadId?: string;
    createdAt?: string; startedAt?: string; completedAt?: string; usageScope?: string;
    iteration?: number; pageIndex?: number; pageCount?: number; latestPage?: boolean;
    inputTokens?: number; outputTokens?: number; cachedInputTokens?: number; reasoningTokens?: number;
    embeddingTokens?: number; totalTokens?: number; cacheWriteInputTokens?: number;
}
const stringFields = ['conversationId','nativeTurnId','nativeMessageId','parentMessageId','pageId','modelCallId','nativeToolCallId','toolMessageId','executionRole','phase','mode','status','agentId','agentName','provider','model','requestPayloadId','responsePayloadId','providerRequestPayloadId','providerResponsePayloadId','streamPayloadId','createdAt','startedAt','completedAt','usageScope','protocolRunId','clientMessageId','clientRequestId','nativeUserMessageId'] as const;
const numberFields = ['iteration','pageIndex','pageCount','inputTokens','outputTokens','cachedInputTokens','reasoningTokens','embeddingTokens','totalTokens','cacheWriteInputTokens'] as const;
const record = (value: unknown): Record<string, unknown> | undefined => value && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : undefined;
const count = (value: unknown): value is number => typeof value === 'number' && Number.isSafeInteger(value) && value >= 0;

/** Read only the approved scalar namespace. Unknown metadata remains observable in the original protocol event. */
export function readAgentlyPresentation(metadata: unknown): AgentlyPresentation | undefined {
    const namespace = record(record(metadata)?.agently);
    const source = record(namespace?.presentation);
    if (!source && namespace?.identityVersion === '1' && typeof namespace.nativeTurnId === 'string') return { version:'1', nativeTurnId:namespace.nativeTurnId };
    if (source?.version !== '1') return undefined;
    const value: AgentlyPresentation = { version: '1' };
    if (source.messageKind !== undefined) { if (source.messageKind !== 'standalone') return undefined; value.messageKind = 'standalone'; }
    for (const key of stringFields) { if (source[key] !== undefined) { if (typeof source[key] !== 'string') return undefined; value[key] = source[key]; } }
    for (const key of numberFields) { if (source[key] !== undefined) { if (!count(source[key])) return undefined; value[key] = source[key]; } }
    if (source.latestPage !== undefined) { if (typeof source.latestPage !== 'boolean') return undefined; value.latestPage = source.latestPage; }
    return value;
}

export interface AgentlyFeedActivationSource { runId:string; sequence:number }
const feedActivationTime=(value:unknown):value is string=>typeof value==='string'&&/^\d{4}-\d{2}-\d{2}T/.test(value)&&Number.isFinite(Date.parse(value));
function feedActivationSource(value:unknown):AgentlyFeedActivationSource|undefined {
    const source=record(value);
    if(typeof source?.runId!=='string'||!source.runId||!count(source.sequence)||source.sequence<1||Object.keys(source).some(key=>key!=='runId'&&key!=='sequence'))return undefined;
    return {runId:source.runId,sequence:source.sequence};
}

export type AgentlyPresentationActivity =
    | {kind:'agently.tool';presentation?:AgentlyPresentation;toolCallId:string;phase:string;status:string;toolMessageId?:string;startedAt?:string;completedAt?:string}
    | {kind:'agently.user-identity';presentation?:AgentlyPresentation;protocolRunId:string;nativeTurnId:string;clientMessageId:string;clientRequestId:string;nativeUserMessageId:string}
    | { kind: 'agently.turn'; presentation?: AgentlyPresentation; nativeTurnId: string; status: string; queueSequence: string; startedByMessageId?: string; origin?: string; goalId?: string; statusReason?: string }
    | { kind: 'agently.feed'; presentation?: AgentlyPresentation; feedId: string; active?: boolean; activationKnown: boolean; activationAt?:string; activationSource?:AgentlyFeedActivationSource; title: string; developerOnly?: boolean; itemCount?: number; feedPresentation?: Record<string, unknown> | null; data?: unknown; dataSources?: unknown; ui?: unknown }
    | { kind: 'agently.planner'; presentation?: AgentlyPresentation; status: string; attempt: number; trigger: string; staticProfile: string; strategyFamily: string; secondPolicy: string; outputPayloadId?: string; validated?: boolean }
    | { kind: 'agently.tools-planned'; presentation?: AgentlyPresentation; calls: { toolCallId: string; toolName: string }[] }
    | { kind: 'agently.narration'; presentation?: AgentlyPresentation; text: string; source: string; status: string; toolCallId: string };

/** Activities stay in the official reducer's graph; this reader adds no text/state accumulator. */
export function readAgentlyPresentationActivity(message: Message): AgentlyPresentationActivity | undefined {
    if (message.role !== 'activity') return undefined;
    const content = record(message.content);
    if (content?.version !== '1') return undefined;
    const presentation = readAgentlyPresentation(message.metadata);
    const strings = (keys: string[]) => keys.every(key => typeof content[key] === 'string');
    switch (message.activityType) {
        case 'agently.tool': {
            if(!strings(['toolCallId','phase','status']))return undefined;
            const output:Extract<AgentlyPresentationActivity,{kind:'agently.tool'}>={kind:'agently.tool',presentation,toolCallId:content.toolCallId as string,phase:content.phase as string,status:content.status as string};
            for(const key of ['toolMessageId','startedAt','completedAt'] as const){if(content[key]!==undefined){if(typeof content[key]!=='string')return undefined;output[key]=content[key];}}
            return output;
        }
        case 'agently.user-identity':
            if(!strings(['protocolRunId','nativeTurnId','clientMessageId','clientRequestId','nativeUserMessageId']))return undefined;
            return {kind:'agently.user-identity',presentation,protocolRunId:content.protocolRunId as string,nativeTurnId:content.nativeTurnId as string,clientMessageId:content.clientMessageId as string,clientRequestId:content.clientRequestId as string,nativeUserMessageId:content.nativeUserMessageId as string};
        case 'agently.turn': {
            if (!strings(['nativeTurnId','status','queueSequence'])) return undefined;
            const output: Extract<AgentlyPresentationActivity,{kind:'agently.turn'}> = { kind: 'agently.turn', presentation, nativeTurnId: content.nativeTurnId as string, status: content.status as string, queueSequence: content.queueSequence as string };
            for (const key of ['startedByMessageId','origin','goalId','statusReason'] as const) { if (content[key] !== undefined) { if (typeof content[key] !== 'string') return undefined; output[key] = content[key]; } }
            return output;
        }
        case 'agently.feed': {
            // The independent feed subscription retains its existing nested DTO.
            const source = record(content.feed) ?? content;
            if (typeof source.feedId !== 'string') return undefined;
            const active = content.activationKnown !== false && typeof content.active === 'boolean' ? content.active : undefined;
            const output: Extract<AgentlyPresentationActivity,{kind:'agently.feed'}> = { kind:'agently.feed', presentation, feedId:source.feedId, active, activationKnown:active !== undefined, title: typeof source.title === 'string' ? source.title : source.feedId };
            if(content.activationAt!==undefined){if(!feedActivationTime(content.activationAt))return undefined;output.activationAt=content.activationAt;}
            if(content.activationSource!==undefined){const origin=feedActivationSource(content.activationSource);if(!origin)return undefined;output.activationSource=origin;}
            if(content.activationFact!==undefined){const fact=record(content.activationFact);if(fact?.version!=='1'||fact.feedId!==source.feedId||typeof fact.active!=='boolean'||!feedActivationSource(fact.activationSource)||fact.activationAt!==undefined&&!feedActivationTime(fact.activationAt)||Object.keys(fact).some(key=>!['version','feedId','active','activationAt','activationSource'].includes(key)))return undefined;}
            if (typeof source.developerOnly === 'boolean') output.developerOnly=source.developerOnly;
            if (count(source.itemCount)) output.itemCount=source.itemCount;
            if (source.presentation === null || record(source.presentation)) output.feedPresentation=source.presentation as Record<string,unknown> | null;
            for (const key of ['data','dataSources','ui'] as const) { if (Object.hasOwn(source,key)) output[key]=source[key]; }
            return output;
        }
        case 'agently.planner': {
            if (!strings(['status','trigger','staticProfile','strategyFamily','secondPolicy']) || !count(content.attempt)) return undefined;
            if (content.validated !== undefined && typeof content.validated !== 'boolean' || content.outputPayloadId !== undefined && typeof content.outputPayloadId !== 'string') return undefined;
            return { kind:'agently.planner', presentation, status:content.status as string, attempt:content.attempt, trigger:content.trigger as string, staticProfile:content.staticProfile as string, strategyFamily:content.strategyFamily as string, secondPolicy:content.secondPolicy as string, ...(typeof content.outputPayloadId==='string'?{outputPayloadId:content.outputPayloadId}:{}), ...(typeof content.validated==='boolean'?{validated:content.validated}:{}) };
        }
        case 'agently.tools-planned': {
            if (!Array.isArray(content.calls)) return undefined;
            const calls: {toolCallId:string;toolName:string}[]=[];
            for (const raw of content.calls) { const call=record(raw);if(typeof call?.toolCallId!=='string'||typeof call.toolName!=='string')return undefined;calls.push({toolCallId:call.toolCallId,toolName:call.toolName}); }
            return {kind:'agently.tools-planned',presentation,calls};
        }
        case 'agently.narration':
            if (!strings(['text','source','status','toolCallId'])) return undefined;
            return {kind:'agently.narration',presentation,text:content.text as string,source:content.source as string,status:content.status as string,toolCallId:content.toolCallId as string};
    }
    return undefined;
}

export interface AgentlyUsagePresentation { version: '1'; scope:'model_call'|'turn'|'conversation'; modelCallId?:string; nativeTurnId?:string; usage:Record<string,number|string> }
export function readAgentlyUsage(value:unknown):AgentlyUsagePresentation|undefined {
    const source=record(value),usage=record(source?.usage);
    if(source?.version!=='1'||!usage)return undefined;
    const scope=source.scope??(source.modelCallId?'model_call':'conversation');
    if(scope!=='model_call'&&scope!=='turn'&&scope!=='conversation')return undefined;
    const output:AgentlyUsagePresentation={version:'1',scope,usage:{}};
    for(const key of ['modelCallId','nativeTurnId'] as const){if(source[key]!==undefined){if(typeof source[key]!=='string')return undefined;output[key]=source[key];}}
    for(const key of ['inputTokens','outputTokens','cachedInputTokens','reasoningTokens','embeddingTokens','totalTokens','cacheWriteInputTokens']){if(usage[key]!==undefined){if(!count(usage[key]))return undefined;output.usage[key]=usage[key];}}
    for(const key of ['provider','model']){if(usage[key]!==undefined){if(typeof usage[key]!=='string')return undefined;output.usage[key]=usage[key];}}
    return output;
}
