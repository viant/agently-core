import type { ExecutionPage, LiveExecutionGroup, LiveExecutionGroupsById, ModelStepState, PlannedToolCall, SSEEvent, ToolStepState, Turn } from './types';
import { compareExecutionGroups, firstNumber, firstPositiveNumber, firstString } from './ordering';
import { eventIterationValue, eventSequenceValue, executionGroupStatusForEvent, modelStepStatusForEvent, terminalStatusForType } from './streamEventMeta';

function rawText(value: unknown): string {
    return value == null ? '' : String(value);
}

function appendText(existing: unknown, incoming: unknown): string {
    return `${rawText(existing)}${rawText(incoming)}`;
}

function normalizeEventPhase(event: SSEEvent = {} as SSEEvent): string {
    const explicit = firstString(event?.phase).trim().toLowerCase();
    if (explicit) return explicit;
    const mode = firstString(event?.mode).trim().toLowerCase();
    if (mode === 'systemcontext') return 'bootstrap';
    return '';
}

function normalizeEventExecutionRole(event: SSEEvent = {} as SSEEvent): string {
    const explicit = firstString((event as any)?.executionRole).trim().toLowerCase();
    if (explicit) return explicit;
    const phase = normalizeEventPhase(event);
    if (phase === 'bootstrap') return 'bootstrap';
    return '';
}

function liveGroupId(event: SSEEvent = {} as SSEEvent): string {
    return firstString((event as any)?.pageId, event?.assistantMessageId, event?.id);
}

export function normalizeExecutionPageSize(value: string | number | null | undefined): '1' | '5' | '10' | 'all' {
    const text = String(value || '1').trim().toLowerCase();
    if (text === 'all') return 'all';
    if (text === '1' || text === '5' || text === '10') return text;
    return '1';
}

export function plannedToolCalls(group: Partial<ExecutionPage> = {}): PlannedToolCall[] {
    return Array.isArray(group?.toolCallsPlanned) ? group.toolCallsPlanned : [];
}

export function isPresentableExecutionGroup(group: LiveExecutionGroup = {}): boolean {
    return Boolean(
        firstString(group?.narration)
        || firstString(group?.errorMessage)
        || (Array.isArray(group?.toolSteps) && group.toolSteps.length > 0)
        || (Array.isArray(group?.toolCallsPlanned) && group.toolCallsPlanned.length > 0)
        || (group?.finalResponse && firstString(group?.content))
    );
}

export function selectExecutionPages(turns: Turn[] = []): ExecutionPage[] {
    const groups: ExecutionPage[] = [];
    for (const turn of Array.isArray(turns) ? turns : []) {
        const turnId = firstString(turn?.turnId, turn?.id);
        const turnStatus = firstString(turn?.status);
        const pages = Array.isArray(turn?.execution?.pages) ? turn.execution.pages : [];
        for (const page of pages) {
            const normalizedPage = page || {};
            groups.push({
                ...normalizedPage,
                turnId: firstString(normalizedPage?.turnId, turnId),
                assistantMessageId: firstString(normalizedPage?.assistantMessageId, normalizedPage?.pageId),
                parentMessageId: firstString(normalizedPage?.parentMessageId),
                iteration: firstNumber(normalizedPage?.iteration),
                narration: firstString(normalizedPage?.narration),
                content: firstString(normalizedPage?.content),
                status: firstString(normalizedPage?.status, turnStatus),
                finalResponse: Boolean(normalizedPage?.finalResponse),
                modelSteps: Array.isArray(normalizedPage?.modelSteps) ? normalizedPage.modelSteps : [],
                toolSteps: Array.isArray(normalizedPage?.toolSteps) ? normalizedPage.toolSteps : [],
                toolCallsPlanned: Array.isArray(normalizedPage?.toolCallsPlanned) ? normalizedPage.toolCallsPlanned : [],
            } as ExecutionPage);
        }
    }
    return groups.sort((left, right) => compareExecutionGroups(left, right));
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
                id: firstString(modelStep?.modelCallId, modelStep?.assistantMessageId),
                kind: 'model',
                toolName: firstString(modelStep?.provider && modelStep?.model ? `${modelStep.provider}/${modelStep.model}` : '', modelStep?.model, modelStep?.provider, 'model'),
                errorMessage: firstString(modelStep?.errorMessage),
            });
        }
        for (const toolStep of Array.isArray(group?.toolSteps) ? group.toolSteps : []) {
            out.push({
                ...toolStep,
                id: firstString(toolStep?.toolCallId, toolStep?.toolMessageId),
                kind: 'tool',
                toolName: firstString(toolStep?.toolName, 'tool'),
                errorMessage: firstString(toolStep?.errorMessage),
            });
        }
    }
    return out;
}

export function findExecutionStepById(groups: Partial<ExecutionPage>[] = [], id = ''): ExecutionStepLike | null {
    const target = String(id || '').trim();
    if (!target) return null;
    return selectExecutionSteps(groups).find((step) => String(step?.id || '').trim() === target) || null;
}

export function findExecutionStepByPayloadId(groups: Partial<ExecutionPage>[] = [], payloadId = ''): ExecutionStepLike | null {
    const target = String(payloadId || '').trim();
    if (!target) return null;
    return selectExecutionSteps(groups).find((step) => (
        String(step?.requestPayloadId || '').trim() === target
        || String(step?.responsePayloadId || '').trim() === target
        || String(step?.providerRequestPayloadId || '').trim() === target
        || String(step?.providerResponsePayloadId || '').trim() === target
        || String(step?.streamPayloadId || '').trim() === target
    )) || null;
}

function mergeExecutionGroup(existing: LiveExecutionGroup = {}, incoming: LiveExecutionGroup = {}): LiveExecutionGroup {
    const toolStepsByKey = new Map<string, ToolStepState>();
    const mergedToolSteps: ToolStepState[] = [];
    const existToolSteps = existing?.toolSteps || [];
    const incToolSteps = incoming?.toolSteps || [];
    for (const entry of [...existToolSteps, ...incToolSteps]) {
        const key = firstString(entry?.toolCallId, entry?.toolMessageId, entry?.id, entry?.toolName);
        if (!key) {
            mergedToolSteps.push(entry);
            continue;
        }
        const prior = toolStepsByKey.get(key) || {} as ToolStepState;
        toolStepsByKey.set(key, { ...prior, ...entry });
    }
    for (const entry of toolStepsByKey.values()) {
        mergedToolSteps.push(entry);
    }
    const existModelSteps = existing?.modelSteps || [];
    const incModelSteps = incoming?.modelSteps || [];
    const mergedModelSteps = incModelSteps.length > 0 ? incModelSteps.map((ms, i) => ({ ...(existModelSteps[i] || {}), ...ms })) : existModelSteps;
    return {
        ...existing,
        ...incoming,
        pageId: firstString(incoming?.pageId, existing?.pageId, incoming?.assistantMessageId, existing?.assistantMessageId),
        assistantMessageId: firstString(incoming?.assistantMessageId, existing?.assistantMessageId),
        parentMessageId: firstString(incoming?.parentMessageId, existing?.parentMessageId),
        executionRole: firstString(incoming?.executionRole, existing?.executionRole),
        phase: firstString(incoming?.phase, existing?.phase),
        sequence: firstNumber(incoming?.sequence, existing?.sequence),
        iteration: firstNumber(incoming?.iteration, existing?.iteration),
        narration: firstString(incoming?.narration, existing?.narration),
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
    const groupId = liveGroupId(event);
    if (!groupId) return null;
    const assistantMessageId = firstString(event?.assistantMessageId, groupId, event?.id);
    const phase = normalizeEventPhase(event);
    const executionRole = normalizeEventExecutionRole(event);
    const group: LiveExecutionGroup = {
        pageId: firstString((event as any)?.pageId, groupId, assistantMessageId),
        assistantMessageId,
        turnId: firstString(event?.turnId),
        parentMessageId: firstString(event?.parentMessageId),
        executionRole,
        sequence: eventSequenceValue(event, 1),
        iteration: eventIterationValue(event, 1),
        phase,
        narration: firstString(event?.narration),
        content: firstString(event?.content),
        errorMessage: firstString(event?.error),
        status: firstString(event?.status, 'running'),
        finalResponse: Boolean(event?.finalResponse),
        modelSteps: event?.model ? [{
            modelCallId: firstString(event?.modelCallId),
            executionRole: firstString(executionRole),
            phase,
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
    return group;
}

function ensureLiveExecutionGroup(groupsById: LiveExecutionGroupsById, groupId: string, event: SSEEvent): LiveExecutionGroup | null {
    return groupsById[groupId] || createLiveExecutionGroup(event);
}

function applyLiveGroupIdentity(current: LiveExecutionGroup, event: SSEEvent) {
    current.pageId = firstString((event as any)?.pageId, current.pageId, current.assistantMessageId);
    current.assistantMessageId = firstString(event?.assistantMessageId, current.assistantMessageId, current.pageId);
    current.turnId = firstString(event?.turnId, current.turnId);
    current.executionRole = firstString(normalizeEventExecutionRole(event), current.executionRole);
    current.sequence = eventSequenceValue(event, current.sequence || 1);
    current.iteration = eventIterationValue(event, current.iteration || 1);
    current.phase = firstString(normalizeEventPhase(event), current.phase);
    return current;
}

function mergePrimaryModelStep(current: LiveExecutionGroup, event: SSEEvent, fallbackStatus = 'running') {
    const phase = normalizeEventPhase(event);
    const executionRole = normalizeEventExecutionRole(event);
    const existMs = Array.isArray(current.modelSteps) && current.modelSteps.length > 0 ? current.modelSteps[0] : {};
    current.modelSteps = [{
        ...existMs,
        modelCallId: firstString(event?.modelCallId, existMs?.modelCallId),
        executionRole: firstString(executionRole, existMs?.executionRole),
        phase: firstString(phase, existMs?.phase),
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

function upsertToolStep(current: LiveExecutionGroup, event: SSEEvent, extra: Partial<ToolStepState> = {}) {
    const toolKey = firstString(
        extra?.toolCallId,
        extra?.toolMessageId,
        event?.toolCallId,
        event?.toolMessageId,
        event?.id,
        extra?.toolName,
        event?.toolName,
    );
    const priorList = Array.isArray(current.toolSteps) ? current.toolSteps : [];
    const existingIndex = priorList.findIndex((entry) => firstString(entry?.toolCallId, entry?.toolMessageId, entry?.toolName) === toolKey);
    const operationId = firstString(event?.operationId, existingIndex >= 0 ? priorList[existingIndex]?.operationId : '');
    const executionRole = normalizeEventExecutionRole(event);
    const toolEntry: ToolStepState = {
        toolCallId: firstString(extra?.toolCallId, event?.toolCallId),
        toolMessageId: firstString(extra?.toolMessageId, event?.toolMessageId, event?.id),
        toolName: firstString(extra?.toolName, event?.toolName),
        executionRole: firstString(extra?.executionRole, executionRole, existingIndex >= 0 ? priorList[existingIndex]?.executionRole : ''),
        content: firstString(extra?.content, event?.content),
        operationId,
        errorMessage: firstString(event?.error, existingIndex >= 0 ? priorList[existingIndex]?.errorMessage : ''),
        status: firstString(event?.status, existingIndex >= 0 ? priorList[existingIndex]?.status : ''),
        requestPayloadId: firstString(event?.requestPayloadId),
        responsePayloadId: firstString(event?.responsePayloadId),
        linkedConversationId: firstString(event?.linkedConversationId),
        linkedConversationAgentId: firstString(event?.linkedConversationAgentId),
        linkedConversationTitle: firstString(event?.linkedConversationTitle),
        asyncOperation: operationId ? {
            operationId,
            status: firstString(event?.status),
            message: firstString(event?.content),
            error: firstString(event?.error),
            response: event?.responsePayload,
        } : undefined,
        ...extra,
    } as ToolStepState;
    const nextTools = [...priorList];
    if (existingIndex >= 0) nextTools[existingIndex] = { ...nextTools[existingIndex], ...toolEntry };
    else nextTools.push(toolEntry);
    current.toolSteps = nextTools;
    return current;
}

function applyTerminalState(current: LiveExecutionGroup, terminalStatus: string, terminalError: string): LiveExecutionGroup {
    return {
        ...current,
        status: firstString(terminalStatus, current?.status),
        errorMessage: firstString(terminalError, current?.errorMessage),
        modelSteps: Array.isArray(current?.modelSteps)
            ? current.modelSteps.map((step) => ({
                ...step,
                status: firstString(terminalStatus, step?.status),
                errorMessage: firstString(terminalError, step?.errorMessage),
            }))
            : current?.modelSteps,
        toolSteps: Array.isArray(current?.toolSteps)
            ? current.toolSteps.map((step) => ({
                ...step,
                status: firstString(terminalStatus, step?.status),
                errorMessage: firstString(terminalError, step?.errorMessage),
            }))
            : current?.toolSteps,
    };
}

export function applyExecutionStreamEventToGroups(groupsById: LiveExecutionGroupsById = {}, rawEvent: SSEEvent = {} as SSEEvent) {
    const event = rawEvent || ({} as SSEEvent);
    const type = firstString(event?.type).toLowerCase();
    const groupId = liveGroupId(event);
    const next: LiveExecutionGroupsById = { ...groupsById };

    if (type === 'model_started' && groupId) {
        next[groupId] = mergeExecutionGroup(next[groupId], createLiveExecutionGroup(event) || {});
        return next;
    }
    if ((type === 'narration' || type === 'reasoning_delta') && groupId) {
        const current = ensureLiveExecutionGroup(next, groupId, event);
        if (!current) return next;
        applyLiveGroupIdentity(current, event);
        current.narration = type === 'reasoning_delta'
            ? appendText(current.narration, event?.content)
            : firstString(event?.content, event?.narration, current.narration);
        current.status = firstString(event?.status, current.status, 'running');
        mergePrimaryModelStep(current, event, current.status || 'running');
        next[groupId] = current;
        return next;
    }
    if (type === 'tool_calls_planned' && groupId) {
        const current = ensureLiveExecutionGroup(next, groupId, event);
        if (!current) return next;
        applyLiveGroupIdentity(current, event);
        current.narration = firstString(event?.content, event?.narration, current.narration);
        current.status = firstString(event?.status, current.status, 'running');
        current.toolCallsPlanned = Array.isArray(event?.toolCallsPlanned) && event.toolCallsPlanned.length > 0
            ? event.toolCallsPlanned
            : (Array.isArray(current?.toolCallsPlanned) ? current.toolCallsPlanned : []);
        next[groupId] = current;
        return next;
    }
    if ((type === 'model_completed' || type === 'text_delta') && groupId) {
        const current = ensureLiveExecutionGroup(next, groupId, event);
        if (!current) return next;
        applyLiveGroupIdentity(current, event);
        current.content = type === 'text_delta'
            ? appendText(current.content, event?.content)
            : firstString(event?.content, current.content);
        current.narration = firstString(event?.narration, current.narration);
        current.errorMessage = firstString(event?.error, current.errorMessage);
        current.status = executionGroupStatusForEvent(event, current.status, current.status || 'running');
        current.finalResponse = Boolean(event?.finalResponse ?? current.finalResponse);
        current.toolCallsPlanned = Array.isArray(event?.toolCallsPlanned) && event.toolCallsPlanned.length > 0
            ? event.toolCallsPlanned
            : (Array.isArray(current?.toolCallsPlanned) ? current.toolCallsPlanned : []);
        mergePrimaryModelStep(current, event, current.status || 'running');
        next[groupId] = current;
        return next;
    }
    if ((type === 'tool_call_started'
        || type === 'tool_call_waiting'
        || type === 'tool_call_completed'
        || type === 'tool_call_failed'
        || type === 'tool_call_canceled') && groupId) {
        const current = ensureLiveExecutionGroup(next, groupId, event);
        if (!current) return next;
        applyLiveGroupIdentity(current, event);
        upsertToolStep(current, event);
        current.errorMessage = firstString(event?.error, current.errorMessage);
        current.status = firstString(
            event?.status,
            current.status,
            type === 'tool_call_failed' ? 'failed' : (type === 'tool_call_canceled' ? 'canceled' : current.status),
        );
        next[groupId] = current;
        return next;
    }
    if ((type === 'skill_started' || type === 'skill_completed') && groupId) {
        const current = ensureLiveExecutionGroup(next, groupId, event);
        if (!current) return next;
        applyLiveGroupIdentity(current, event);
        upsertToolStep(current, event, {
            toolCallId: firstString(event?.skillExecutionId, event?.toolCallId, event?.toolMessageId, event?.skillName),
            toolName: firstString(event?.toolName, event?.skillName ? `skill:${event.skillName}` : 'skill'),
            status: firstString(event?.status, type === 'skill_started' ? 'running' : 'completed'),
            content: firstString(event?.content, event?.skillName),
        });
        current.status = firstString(event?.status, current.status, type === 'skill_started' ? 'running' : 'completed');
        next[groupId] = current;
        return next;
    }
    if (type === 'linked_conversation_attached' && groupId) {
        const current = ensureLiveExecutionGroup(next, groupId, event);
        if (!current) return next;
        current.turnId = firstString(event?.turnId, current.turnId);
        const linkedConversationId = firstString(event?.linkedConversationId);
        if (!linkedConversationId) return next;
        upsertToolStep(current, event, { linkedConversationId });
        next[groupId] = current;
        return next;
    }
    if (type === 'turn_completed' || type === 'turn_failed' || type === 'turn_canceled') {
        const targetTurnId = firstString(event?.turnId);
        const terminalStatus = firstString(event?.status, terminalStatusForType(type));
        const terminalError = firstString(event?.error);
        if (groupId && next[groupId]) {
            next[groupId] = applyTerminalState(next[groupId], terminalStatus, terminalError);
            return next;
        }
        if (targetTurnId) {
            Object.entries(next).forEach(([id, value]) => {
                if (firstString(value?.turnId) !== targetTurnId) return;
                next[id] = applyTerminalState(value, terminalStatus, terminalError);
            });
        }
    }
    return next;
}

export function mergeLatestTranscriptAndLiveExecutionGroups(transcriptGroups: LiveExecutionGroup[] = [], liveGroupsById: LiveExecutionGroupsById = {}, pageSize: string | number = '1') {
    const normalizedPageSize = normalizeExecutionPageSize(pageSize);
    const mergedById = new Map<string, LiveExecutionGroup>();
    for (const group of transcriptGroups) {
        const key = firstString(group?.assistantMessageId);
        if (!key) continue;
        mergedById.set(key, group);
    }
    Object.values(liveGroupsById || {}).forEach((liveGroup) => {
        if (!isPresentableExecutionGroup(liveGroup)) return;
        const key = firstString(liveGroup?.assistantMessageId);
        if (!key) return;
        // Keep the active/live execution group authoritative for its page.
        // Transcript groups remain for older pages, but we avoid blending the
        // active page object between transcript and SSE sources.
        mergedById.set(key, liveGroup);
    });
    const merged = Array.from(mergedById.values()).sort((left, right) => compareExecutionGroups(left, right));
    if (normalizedPageSize === 'all') return merged;
    const limit = Math.max(1, Number(normalizedPageSize || 1));
    if (limit === 1) {
        const latestPresentable = [...merged].reverse().find((group) => isPresentableExecutionGroup(group));
        return latestPresentable ? [latestPresentable] : transcriptGroups.slice(-1);
    }
    return merged.slice(Math.max(0, merged.length - limit));
}

export function describeExecutionTimelineEvent(event: LiveExecutionGroup & { type?: string; toolName?: string } = {}) {
    const type = firstString(event?.type, 'event');
    const status = firstString(event?.status);
    const toolName = firstString(event?.toolName);
    const skillName = firstString((event as any)?.skillName);
    const message = firstString(event?.content, event?.narration, event?.error);
    const parts = [type];
    if (status) parts.push(status);
    if (toolName) parts.push(toolName);
    if (skillName) parts.push(skillName);
    if (Array.isArray(event?.toolCallsPlanned) && event.toolCallsPlanned.length > 0) {
        parts.push(`planned ${event.toolCallsPlanned.map((item) => firstString(item?.toolName, 'tool')).join(', ')}`);
    }
    if (message) parts.push(message);
    return parts.join(' · ');
}
