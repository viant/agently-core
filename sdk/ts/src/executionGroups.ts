import type { ExecutionPage, ModelStepState, PlannedToolCall, SSEEvent, ToolStepState, Turn } from './types';

function firstString(...values: unknown[]): string {
    for (const value of values) {
        const text = String(value || '').trim();
        if (text) return text;
    }
    return '';
}

function firstNumber(...values: unknown[]): number {
    for (const value of values) {
        const num = Number(value);
        if (Number.isFinite(num)) return num;
    }
    return 0;
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
    return groups.sort((left: any, right: any) => {
        const leftSeq = firstNumber((left as any)?.sequence, left?.iteration);
        const rightSeq = firstNumber((right as any)?.sequence, right?.iteration);
        if (leftSeq !== rightSeq) return leftSeq - rightSeq;
        return String(left?.assistantMessageId || '').localeCompare(String(right?.assistantMessageId || ''));
    });
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
        parentMessageId: firstString(event?.parentMessageId),
        sequence: firstNumber(event?.pageIndex, event?.iteration, 1),
        iteration: firstNumber(event?.iteration, event?.pageIndex, 1),
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

export function applyExecutionStreamEventToGroups(groupsById: Record<string, any> = {}, rawEvent: SSEEvent = {} as SSEEvent) {
    const event = rawEvent || ({} as SSEEvent);
    const type = firstString(event?.type).toLowerCase();
    const assistantMessageId = firstString(event?.assistantMessageId, event?.id);
    const next = { ...groupsById };

    if (type === 'model_started' && assistantMessageId) {
        next[assistantMessageId] = mergeExecutionGroup(next[assistantMessageId], createLiveExecutionGroup(event) || {});
        return next;
    }
    if ((type === 'model_completed' || type === 'text_delta') && assistantMessageId) {
        const current = next[assistantMessageId] || createLiveExecutionGroup(event);
        if (!current) return next;
        current.content = type === 'text_delta'
            ? `${firstString(current.content)}${firstString(event?.content)}`
            : firstString(event?.content, current.content);
        current.preamble = firstString(event?.preamble, current.preamble);
        current.errorMessage = firstString(event?.error, current.errorMessage);
        current.status = firstString(event?.status, current.status);
        current.finalResponse = Boolean(event?.finalResponse ?? current.finalResponse);
        current.toolCallsPlanned = Array.isArray(event?.toolCallsPlanned) && event.toolCallsPlanned.length > 0
            ? event.toolCallsPlanned
            : (Array.isArray(current?.toolCallsPlanned) ? current.toolCallsPlanned : []);
        const existMs = Array.isArray(current.modelSteps) && current.modelSteps.length > 0 ? current.modelSteps[0] : {};
        current.modelSteps = [{
            ...existMs,
            modelCallId: firstString(assistantMessageId, existMs?.modelCallId),
            provider: firstString(event?.model?.provider, existMs?.provider),
            model: firstString(event?.model?.model, existMs?.model),
            errorMessage: firstString(event?.error, existMs?.errorMessage),
            status: firstString(event?.status, existMs?.status),
            requestPayloadId: firstString(event?.requestPayloadId, existMs?.requestPayloadId),
            responsePayloadId: firstString(event?.responsePayloadId, existMs?.responsePayloadId),
            providerRequestPayloadId: firstString(event?.providerRequestPayloadId, existMs?.providerRequestPayloadId),
            providerResponsePayloadId: firstString(event?.providerResponsePayloadId, existMs?.providerResponsePayloadId),
            streamPayloadId: firstString(event?.streamPayloadId, existMs?.streamPayloadId),
        }];
        next[assistantMessageId] = current;
        return next;
    }
    if (type === 'reasoning_delta' && assistantMessageId) {
        const current = next[assistantMessageId] || createLiveExecutionGroup(event);
        if (!current) return next;
        current.preamble = `${firstString(current.preamble)}${firstString(event?.content)}`;
        next[assistantMessageId] = current;
        return next;
    }
    if ((type === 'tool_call_started' || type === 'tool_call_completed') && assistantMessageId) {
        const current = next[assistantMessageId] || createLiveExecutionGroup(event);
        if (!current) return next;
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
        };
        const nextTools = [...priorList];
        if (existingIndex >= 0) nextTools[existingIndex] = { ...nextTools[existingIndex], ...toolEntry };
        else nextTools.push(toolEntry);
        current.toolSteps = nextTools;
        current.status = firstString(event?.status, current.status);
        next[assistantMessageId] = current;
        return next;
    }
    if (type === 'turn_completed' && assistantMessageId && next[assistantMessageId]) {
        next[assistantMessageId] = { ...next[assistantMessageId], status: firstString(event?.status, next[assistantMessageId]?.status) };
    }
    if (type === 'turn_failed' && assistantMessageId && next[assistantMessageId]) {
        next[assistantMessageId] = {
            ...next[assistantMessageId],
            status: firstString(event?.status, next[assistantMessageId]?.status, 'failed'),
            errorMessage: firstString(event?.error, next[assistantMessageId]?.errorMessage),
        };
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
    const merged = Array.from(mergedById.values()).sort((left, right) => {
        if ((left as any).sequence !== (right as any).sequence) return firstNumber((left as any).sequence) - firstNumber((right as any).sequence);
        return String((left as any).assistantMessageId || '').localeCompare(String((right as any).assistantMessageId || ''));
    });
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
