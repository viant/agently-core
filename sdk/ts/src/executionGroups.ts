import type { ExecutionPage, ModelStepState, PlannedToolCall, SSEEvent, ToolStepState, Turn } from './types';
import { compareExecutionGroups, firstNumber, firstPositiveNumber, firstString } from './ordering';
import { eventIterationValue, eventSequenceValue, executionGroupStatusForEvent, modelStepStatusForEvent, terminalStatusForType } from './streamEventMeta';

export function normalizeExecutionPageSize(value: string | number | null | undefined): '1' | '5' | '10' | 'all' {
    const text = String(value || '1').trim().toLowerCase();
    if (text === 'all') return 'all';
    if (text === '1' || text === '5' || text === '10') return text;
    return '1';
}

export function plannedToolCalls(group: Partial<ExecutionPage> = {}): PlannedToolCall[] {
    return Array.isArray(group?.toolCallsPlanned) ? group.toolCallsPlanned : [];
}

export function isPresentableExecutionGroup(group: Partial<ExecutionPage & { errorMessage?: string }> = {}): boolean {
    return Boolean(
        firstString(group?.preamble)
        || firstString((group as any)?.errorMessage)
        || (Array.isArray(group?.toolSteps) && group.toolSteps.length > 0)
        || (Array.isArray(group?.toolCallsPlanned) && group.toolCallsPlanned.length > 0)
        || (group?.finalResponse && firstString(group?.content))
    );
}

export function selectExecutionPages(turns: Turn[] = []): ExecutionPage[] {
    const groups: ExecutionPage[] = [];
    for (const turn of Array.isArray(turns) ? turns : []) {
        const turnId = firstString((turn as any)?.turnId, (turn as any)?.id);
        const turnStatus = firstString((turn as any)?.status);
        const pages = Array.isArray((turn as any)?.execution?.pages) ? (turn as any).execution.pages : [];
        for (const page of pages) {
            groups.push({
                ...page,
                turnId: firstString((page as any)?.turnId, turnId),
                assistantMessageId: firstString((page as any)?.assistantMessageId, (page as any)?.pageId),
                parentMessageId: firstString((page as any)?.parentMessageId),
                iteration: firstNumber((page as any)?.iteration),
                preamble: firstString((page as any)?.preamble),
                content: firstString((page as any)?.content),
                status: firstString((page as any)?.status, turnStatus),
                finalResponse: Boolean((page as any)?.finalResponse),
                modelSteps: Array.isArray((page as any)?.modelSteps) ? (page as any).modelSteps : [],
                toolSteps: Array.isArray((page as any)?.toolSteps) ? (page as any).toolSteps : [],
                toolCallsPlanned: Array.isArray((page as any)?.toolCallsPlanned) ? (page as any).toolCallsPlanned : [],
            } as ExecutionPage);
        }
    }
    return groups.sort((left: any, right: any) => compareExecutionGroups(left, right));
}

export type ExecutionStepLike = (ModelStepState | ToolStepState) & {
    id?: string;
    kind: 'model' | 'tool';
    toolName?: string;
    provider?: string;
    model?: string;
    errorMessage?: string;
};

export function selectExecutionSteps(groups: Partial<ExecutionPage>[] = []): ExecutionStepLike[] {
    const out: ExecutionStepLike[] = [];
    for (const group of Array.isArray(groups) ? groups : []) {
        for (const modelStep of Array.isArray(group?.modelSteps) ? group.modelSteps : []) {
            out.push({
                ...modelStep,
                id: firstString((modelStep as any)?.assistantMessageId, (modelStep as any)?.modelCallId),
                kind: 'model',
                toolName: firstString((modelStep as any)?.provider && (modelStep as any)?.model ? `${(modelStep as any).provider}/${(modelStep as any).model}` : '', (modelStep as any)?.model, (modelStep as any)?.provider, 'model'),
                errorMessage: firstString((modelStep as any)?.errorMessage),
            });
        }
        for (const toolStep of Array.isArray(group?.toolSteps) ? group.toolSteps : []) {
            out.push({
                ...toolStep,
                id: firstString((toolStep as any)?.toolMessageId, (toolStep as any)?.toolCallId, (toolStep as any)?.id),
                kind: 'tool',
                toolName: firstString((toolStep as any)?.toolName, 'tool'),
                errorMessage: firstString((toolStep as any)?.errorMessage),
            });
        }
    }
    return out;
}

export function findExecutionStepById(groups: Partial<ExecutionPage>[] = [], id = ''): ExecutionStepLike | null {
    const target = String(id || '').trim();
    if (!target) return null;
    return selectExecutionSteps(groups).find((step) => String((step as any)?.id || '').trim() === target) || null;
}

export function findExecutionStepByPayloadId(groups: Partial<ExecutionPage>[] = [], payloadId = ''): ExecutionStepLike | null {
    const target = String(payloadId || '').trim();
    if (!target) return null;
    return selectExecutionSteps(groups).find((step: any) => (
        String(step?.requestPayloadId || '').trim() === target
        || String(step?.responsePayloadId || '').trim() === target
        || String(step?.providerRequestPayloadId || '').trim() === target
        || String(step?.providerResponsePayloadId || '').trim() === target
        || String(step?.streamPayloadId || '').trim() === target
    )) || null;
}

function mergeExecutionGroup(existing: Record<string, any> = {}, incoming: Record<string, any> = {}) {
    const toolStepsByKey = new Map<string, Record<string, any>>();
    const mergedToolSteps: Record<string, any>[] = [];
    const existToolSteps = existing?.toolSteps || [];
    const incToolSteps = incoming?.toolSteps || [];
    for (const entry of [...existToolSteps, ...incToolSteps]) {
        const key = firstString(entry?.toolCallId, entry?.toolMessageId, entry?.id, entry?.toolName);
        if (!key) {
            mergedToolSteps.push(entry);
            continue;
        }
        const prior = toolStepsByKey.get(key) || {};
        toolStepsByKey.set(key, { ...prior, ...entry });
    }
    for (const entry of toolStepsByKey.values()) {
        mergedToolSteps.push(entry);
    }
    const existModelSteps = existing?.modelSteps || [];
    const incModelSteps = incoming?.modelSteps || [];
    const mergedModelSteps = incModelSteps.length > 0 ? incModelSteps.map((ms: any, i: number) => ({ ...(existModelSteps[i] || {}), ...ms })) : existModelSteps;
    return {
        ...existing,
        ...incoming,
        assistantMessageId: firstString(incoming?.assistantMessageId, existing?.assistantMessageId),
        parentMessageId: firstString(incoming?.parentMessageId, existing?.parentMessageId),
        sequence: firstNumber(incoming?.sequence, existing?.sequence),
        iteration: firstNumber(incoming?.iteration, existing?.iteration),
        preamble: firstString(incoming?.preamble, existing?.preamble),
        content: firstString(incoming?.content, existing?.content),
        errorMessage: firstString(incoming?.errorMessage, existing?.errorMessage),
        status: firstString(incoming?.status, existing?.status),
        finalResponse: Boolean(incoming?.finalResponse ?? existing?.finalResponse),
        modelSteps: mergedModelSteps,
        toolSteps: mergedToolSteps,
        toolCallsPlanned: Array.isArray(incoming?.toolCallsPlanned) && incoming.toolCallsPlanned.length > 0
            ? incoming.toolCallsPlanned
            : (Array.isArray(existing?.toolCallsPlanned) ? existing.toolCallsPlanned : []),
    };
}

function createLiveExecutionGroup(event: SSEEvent = {} as SSEEvent) {
    const assistantMessageId = firstString(event?.assistantMessageId, event?.id);
    if (!assistantMessageId) return null;
    return {
        pageId: assistantMessageId,
        assistantMessageId,
        turnId: firstString((event as any)?.turnId),
        parentMessageId: firstString(event?.parentMessageId),
        sequence: eventSequenceValue(event, 1),
        iteration: eventIterationValue(event, 1),
        preamble: firstString(event?.preamble),
        content: firstString(event?.content),
        errorMessage: firstString(event?.error),
        status: firstString(event?.status, 'running'),
        finalResponse: Boolean(event?.finalResponse),
        modelSteps: event?.model ? [{
            modelCallId: assistantMessageId,
            provider: firstString(event?.model?.provider),
            model: firstString(event?.model?.model),
            status: firstString(event?.status, 'running'),
            requestPayloadId: firstString(event?.requestPayloadId),
            responsePayloadId: firstString(event?.responsePayloadId),
            providerRequestPayloadId: firstString(event?.providerRequestPayloadId),
            providerResponsePayloadId: firstString(event?.providerResponsePayloadId),
            streamPayloadId: firstString(event?.streamPayloadId),
        }] : [],
        toolSteps: [],
        toolCallsPlanned: Array.isArray(event?.toolCallsPlanned) ? event.toolCallsPlanned : [],
    };
}

function ensureLiveExecutionGroup(groupsById: Record<string, any>, assistantMessageId: string, event: SSEEvent) {
    return groupsById[assistantMessageId] || createLiveExecutionGroup(event);
}

function applyLiveGroupIdentity(current: Record<string, any>, event: SSEEvent) {
    current.turnId = firstString((event as any)?.turnId, current.turnId);
    current.sequence = eventSequenceValue(event, current.sequence || 1);
    current.iteration = eventIterationValue(event, current.iteration || 1);
    return current;
}

function mergePrimaryModelStep(current: Record<string, any>, event: SSEEvent, fallbackStatus = 'running') {
    const assistantMessageId = firstString(event?.assistantMessageId, event?.id);
    const existMs = Array.isArray(current.modelSteps) && current.modelSteps.length > 0 ? current.modelSteps[0] : {};
    current.modelSteps = [{
        ...existMs,
        modelCallId: firstString(assistantMessageId, existMs?.modelCallId),
        provider: firstString(event?.model?.provider, existMs?.provider),
        model: firstString(event?.model?.model, existMs?.model),
        errorMessage: firstString(event?.error, existMs?.errorMessage),
        status: modelStepStatusForEvent(event, firstString(existMs?.status), fallbackStatus),
        requestPayloadId: firstString(event?.requestPayloadId, existMs?.requestPayloadId),
        responsePayloadId: firstString(event?.responsePayloadId, existMs?.responsePayloadId),
        providerRequestPayloadId: firstString(event?.providerRequestPayloadId, existMs?.providerRequestPayloadId),
        providerResponsePayloadId: firstString(event?.providerResponsePayloadId, existMs?.providerResponsePayloadId),
        streamPayloadId: firstString(event?.streamPayloadId, existMs?.streamPayloadId),
    }];
    return current;
}

function upsertToolStep(current: Record<string, any>, event: SSEEvent, extra: Record<string, any> = {}) {
    const toolKey = firstString(event?.toolCallId, event?.toolMessageId, event?.id, event?.toolName);
    const priorList = Array.isArray(current.toolSteps) ? current.toolSteps : [];
    const existingIndex = priorList.findIndex((entry: any) => firstString(entry?.toolCallId, entry?.toolMessageId, entry?.id, entry?.toolName) === toolKey);
    const toolEntry = {
        toolCallId: firstString(event?.toolCallId),
        toolMessageId: firstString(event?.toolMessageId, event?.id),
        toolName: firstString(event?.toolName),
        errorMessage: firstString(event?.error, existingIndex >= 0 ? priorList[existingIndex]?.errorMessage : ''),
        status: firstString(event?.status, existingIndex >= 0 ? priorList[existingIndex]?.status : ''),
        requestPayloadId: firstString(event?.requestPayloadId),
        responsePayloadId: firstString(event?.responsePayloadId),
        linkedConversationId: firstString(event?.linkedConversationId),
        linkedConversationAgentId: firstString((event as any)?.linkedConversationAgentId),
        linkedConversationTitle: firstString((event as any)?.linkedConversationTitle),
        ...extra,
    };
    const nextTools = [...priorList];
    if (existingIndex >= 0) nextTools[existingIndex] = { ...nextTools[existingIndex], ...toolEntry };
    else nextTools.push(toolEntry);
    current.toolSteps = nextTools;
    return current;
}

function applyTerminalState(current: Record<string, any>, terminalStatus: string, terminalError: string) {
    return {
        ...current,
        status: firstString(terminalStatus, current?.status),
        errorMessage: firstString(terminalError, current?.errorMessage),
        modelSteps: Array.isArray(current?.modelSteps)
            ? current.modelSteps.map((step: any) => ({
                ...step,
                status: firstString(terminalStatus, step?.status),
                errorMessage: firstString(terminalError, step?.errorMessage),
            }))
            : current?.modelSteps,
        toolSteps: Array.isArray(current?.toolSteps)
            ? current.toolSteps.map((step: any) => ({
                ...step,
                status: firstString(terminalStatus, step?.status),
                errorMessage: firstString(terminalError, step?.errorMessage),
            }))
            : current?.toolSteps,
    };
}

export function applyExecutionStreamEventToGroups(groupsById: Record<string, any> = {}, rawEvent: SSEEvent = {} as SSEEvent) {
    const event = rawEvent || ({} as SSEEvent);
    const type = firstString(event?.type).toLowerCase();
    const assistantMessageId = firstString(event?.assistantMessageId, event?.id);
    const next = { ...groupsById };

    if (type === 'model_started' && assistantMessageId) {
        next[assistantMessageId] = mergeExecutionGroup(next[assistantMessageId], createLiveExecutionGroup(event) || {});
        return next;
    }
    if ((type === 'assistant_preamble' || type === 'reasoning_delta') && assistantMessageId) {
        const current = ensureLiveExecutionGroup(next, assistantMessageId, event);
        if (!current) return next;
        applyLiveGroupIdentity(current, event);
        current.preamble = type === 'reasoning_delta'
            ? `${firstString(current.preamble)}${firstString(event?.content)}`
            : firstString(event?.content, event?.preamble, current.preamble);
        current.status = firstString(event?.status, current.status, 'running');
        mergePrimaryModelStep(current, event, current.status || 'running');
        next[assistantMessageId] = current;
        return next;
    }
    if (type === 'tool_calls_planned' && assistantMessageId) {
        const current = ensureLiveExecutionGroup(next, assistantMessageId, event);
        if (!current) return next;
        applyLiveGroupIdentity(current, event);
        current.preamble = firstString(event?.content, event?.preamble, current.preamble);
        current.status = firstString(event?.status, current.status, 'running');
        current.toolCallsPlanned = Array.isArray(event?.toolCallsPlanned) && event.toolCallsPlanned.length > 0
            ? event.toolCallsPlanned
            : (Array.isArray(current?.toolCallsPlanned) ? current.toolCallsPlanned : []);
        next[assistantMessageId] = current;
        return next;
    }
    if ((type === 'model_completed' || type === 'text_delta') && assistantMessageId) {
        const current = ensureLiveExecutionGroup(next, assistantMessageId, event);
        if (!current) return next;
        applyLiveGroupIdentity(current, event);
        current.content = type === 'text_delta'
            ? `${firstString(current.content)}${firstString(event?.content)}`
            : firstString(event?.content, current.content);
        current.preamble = firstString(event?.preamble, current.preamble);
        current.errorMessage = firstString(event?.error, current.errorMessage);
        current.status = executionGroupStatusForEvent(event, current.status, current.status || 'running');
        current.finalResponse = Boolean(event?.finalResponse ?? current.finalResponse);
        current.toolCallsPlanned = Array.isArray(event?.toolCallsPlanned) && event.toolCallsPlanned.length > 0
            ? event.toolCallsPlanned
            : (Array.isArray(current?.toolCallsPlanned) ? current.toolCallsPlanned : []);
        mergePrimaryModelStep(current, event, current.status || 'running');
        next[assistantMessageId] = current;
        return next;
    }
    if ((type === 'tool_call_started' || type === 'tool_call_completed') && assistantMessageId) {
        const current = ensureLiveExecutionGroup(next, assistantMessageId, event);
        if (!current) return next;
        applyLiveGroupIdentity(current, event);
        upsertToolStep(current, event);
        current.status = firstString(event?.status, current.status);
        next[assistantMessageId] = current;
        return next;
    }
    if (type === 'linked_conversation_attached' && assistantMessageId) {
        const current = ensureLiveExecutionGroup(next, assistantMessageId, event);
        if (!current) return next;
        current.turnId = firstString((event as any)?.turnId, current.turnId);
        const linkedConversationId = firstString(event?.linkedConversationId);
        if (!linkedConversationId) return next;
        upsertToolStep(current, event, { linkedConversationId });
        next[assistantMessageId] = current;
        return next;
    }
    if (type === 'assistant_final' && assistantMessageId) {
        const current = ensureLiveExecutionGroup(next, assistantMessageId, event);
        if (!current) return next;
        applyLiveGroupIdentity(current, event);
        current.content = firstString(event?.content, current.content);
        current.preamble = firstString(event?.preamble, current.preamble);
        current.errorMessage = firstString(event?.error, current.errorMessage);
        current.status = firstString(event?.status, current.status, 'completed');
        current.finalResponse = true;
        mergePrimaryModelStep(current, event, 'completed');
        next[assistantMessageId] = current;
        return next;
    }
    if (type === 'turn_completed' || type === 'turn_failed' || type === 'turn_canceled') {
        const targetTurnId = firstString((event as any)?.turnId);
        const terminalStatus = firstString(event?.status, terminalStatusForType(type));
        const terminalError = firstString(event?.error);
        if (assistantMessageId && next[assistantMessageId]) {
            next[assistantMessageId] = applyTerminalState(next[assistantMessageId], terminalStatus, terminalError);
            return next;
        }
        if (targetTurnId) {
            Object.entries(next).forEach(([id, value]) => {
                if (firstString((value as any)?.turnId) !== targetTurnId) return;
                next[id] = applyTerminalState(value, terminalStatus, terminalError);
            });
        }
    }
    return next;
}

export function mergeLatestTranscriptAndLiveExecutionGroups(transcriptGroups: Record<string, any>[] = [], liveGroupsById: Record<string, any> = {}, pageSize: string | number = '1') {
    const normalizedPageSize = normalizeExecutionPageSize(pageSize);
    const mergedById = new Map<string, Record<string, any>>();
    for (const group of transcriptGroups) {
        const key = firstString(group?.assistantMessageId);
        if (!key) continue;
        mergedById.set(key, group);
    }
    Object.values(liveGroupsById || {}).forEach((liveGroup) => {
        if (!isPresentableExecutionGroup(liveGroup)) return;
        const key = firstString((liveGroup as any)?.assistantMessageId);
        if (!key) return;
        // Keep the active/live execution group authoritative for its page.
        // Transcript groups remain for older pages, but we avoid blending the
        // active page object between transcript and SSE sources.
        mergedById.set(key, liveGroup as Record<string, any>);
    });
    const merged = Array.from(mergedById.values()).sort((left, right) => compareExecutionGroups(left as any, right as any));
    if (normalizedPageSize === 'all') return merged;
    const limit = Math.max(1, Number(normalizedPageSize || 1));
    if (limit === 1) {
        const latestPresentable = [...merged].reverse().find((group) => isPresentableExecutionGroup(group));
        return latestPresentable ? [latestPresentable] : transcriptGroups.slice(-1);
    }
    return merged.slice(Math.max(0, merged.length - limit));
}

export function describeExecutionTimelineEvent(event: Record<string, any> = {}) {
    const type = firstString(event?.type, 'event');
    const status = firstString(event?.status);
    const toolName = firstString(event?.toolName);
    const message = firstString(event?.content, event?.preamble, event?.error);
    const parts = [type];
    if (status) parts.push(status);
    if (toolName) parts.push(toolName);
    if (Array.isArray(event?.toolCallsPlanned) && event.toolCallsPlanned.length > 0) {
        parts.push(`planned ${event.toolCallsPlanned.map((item: any) => firstString(item?.toolName, 'tool')).join(', ')}`);
    }
    if (message) parts.push(message);
    return parts.join(' · ');
}
