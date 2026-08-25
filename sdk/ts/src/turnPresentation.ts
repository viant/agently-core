import type { LiveExecutionGroup, PlannedToolCall, ToolStepState } from './types';

export type ActiveTurnState = 'sending' | 'running' | 'stopping' | 'waiting_for_user';
export type TurnOutcome = 'failed' | 'canceled' | 'partial';
export type TurnActivityKind = 'connecting' | 'planning' | 'thinking' | 'writing' | 'tool' | 'tools' | 'stopping' | 'waiting_for_user';
export type TokenUsageScope = 'turn' | 'conversation';

export interface ModelTokenUsage {
    provider?: string;
    model?: string;
    modelCallId?: string;
    totalTokens?: number;
    inputTokens?: number;
    outputTokens?: number;
    cachedInputTokens?: number;
    reasoningTokens?: number;
    embeddingTokens?: number;
}

export interface TokenUsageSummary {
    scope: TokenUsageScope;
    totalTokens: number;
    inputTokens?: number;
    outputTokens?: number;
    cachedInputTokens?: number;
    reasoningTokens?: number;
    embeddingTokens?: number;
    models?: ModelTokenUsage[];
}

export interface ToolProgressRow {
    toolCallId: string;
    toolName: string;
    status: string;
}

export interface ToolProgressSummary {
    completedToolCount: number;
    activeToolCount: number;
    queuedToolCount: number;
    failedToolCount: number;
    totalToolCount: number;
    identityComplete: boolean;
    rows: ToolProgressRow[];
}

export interface TurnActivity {
    kind: TurnActivityKind;
    label?: string;
}

export interface ActiveTurnProgress extends ToolProgressSummary {
    turnId: string;
    state: ActiveTurnState;
    activity: TurnActivity;
    tokenUsage?: TokenUsageSummary;
    startedAt?: string;
    canStop: boolean;
}

export interface TurnNarration {
    turnId: string;
    messageId: string;
    content: string;
    status: 'active' | 'final';
}

export interface TurnTerminalNotice {
    turnId: string;
    outcome: TurnOutcome;
    category: string;
    message: string;
}

export interface TurnPresentationInput {
    turnId?: string;
    status?: string;
    phase?: string;
    isSending?: boolean;
    isStopping?: boolean;
    startedAt?: string;
    canStop?: boolean;
    groups?: LiveExecutionGroup[];
    tokenUsage?: TokenUsageSummary | null;
    assistantHasContent?: boolean;
}

export interface TurnNarrationInput {
    turnId?: string;
    narrationMessageId?: string;
    finalMessageId?: string;
    status?: string;
    finalContent?: string;
    candidates?: Array<string | null | undefined>;
}

const ACTIVE_STATUSES = new Set(['', 'active', 'executing', 'in_progress', 'processing', 'running', 'started', 'streaming']);
const QUEUED_STATUSES = new Set(['open', 'pending', 'planned', 'queued', 'waiting']);
const COMPLETED_STATUSES = new Set(['completed', 'done', 'success', 'succeeded']);
const FAILED_STATUSES = new Set(['canceled', 'cancelled', 'declined', 'error', 'failed', 'terminated', 'timed_out', 'timeout']);
const TERMINAL_FAILURE_STATUSES = new Set(['error', 'failed', 'terminated', 'timed_out', 'timeout']);
const TERMINAL_CANCELED_STATUSES = new Set(['canceled', 'cancelled']);

function normalized(value: unknown): string {
    return String(value ?? '').trim().toLowerCase().replace(/\s+/g, '_');
}

function firstText(...values: unknown[]): string {
    for (const value of values) {
        const text = String(value ?? '').trim();
        if (text) return text;
    }
    return '';
}

function nonNegative(value: unknown): number | undefined {
    const number = Number(value);
    if (!Number.isFinite(number) || number < 0) return undefined;
    return number;
}

export function normalizeTokenUsageSummary(usage: Partial<TokenUsageSummary> | null | undefined): TokenUsageSummary | undefined {
    if (!usage) return undefined;
    const inputTokens = nonNegative(usage.inputTokens);
    const outputTokens = nonNegative(usage.outputTokens);
    const cachedInputTokens = nonNegative(usage.cachedInputTokens);
    const reasoningTokens = nonNegative(usage.reasoningTokens);
    const embeddingTokens = nonNegative(usage.embeddingTokens);
    const explicitTotal = nonNegative(usage.totalTokens);
    const totalTokens = explicitTotal ?? ((inputTokens ?? 0) + (outputTokens ?? 0) + (embeddingTokens ?? 0));
    const models = (Array.isArray(usage.models) ? usage.models : [])
        .map((entry) => ({
            ...entry,
            provider: firstText(entry?.provider) || undefined,
            model: firstText(entry?.model) || undefined,
            modelCallId: firstText(entry?.modelCallId) || undefined,
            totalTokens: nonNegative(entry?.totalTokens),
            inputTokens: nonNegative(entry?.inputTokens),
            outputTokens: nonNegative(entry?.outputTokens),
            cachedInputTokens: nonNegative(entry?.cachedInputTokens),
            reasoningTokens: nonNegative(entry?.reasoningTokens),
            embeddingTokens: nonNegative(entry?.embeddingTokens),
        }))
        .sort((left, right) => {
            const tokenDelta = Number(right.totalTokens ?? 0) - Number(left.totalTokens ?? 0);
            if (tokenDelta !== 0) return tokenDelta;
            return `${left.provider ?? ''}/${left.model ?? ''}`.localeCompare(`${right.provider ?? ''}/${right.model ?? ''}`);
        });
    return {
        scope: usage.scope === 'turn' ? 'turn' : 'conversation',
        totalTokens,
        inputTokens,
        outputTokens,
        cachedInputTokens,
        reasoningTokens,
        embeddingTokens,
        models: models.length > 0 ? models : undefined,
    };
}

export function summarizeExecutionTokenUsage(groups: LiveExecutionGroup[] = []): TokenUsageSummary | undefined {
    const byModelCallId = new Map<string, ModelTokenUsage>();
    for (const group of Array.isArray(groups) ? groups : []) {
        for (const step of Array.isArray(group?.modelSteps) ? group.modelSteps : []) {
            const modelCallId = firstText(step?.modelCallId);
            if (!modelCallId || !step?.usage) continue;
            byModelCallId.set(modelCallId, {
                provider: firstText(step?.provider) || undefined,
                model: firstText(step?.model) || undefined,
                modelCallId,
                inputTokens: nonNegative(step.usage.inputTokens),
                outputTokens: nonNegative(step.usage.outputTokens),
                cachedInputTokens: nonNegative(step.usage.cachedInputTokens),
                reasoningTokens: nonNegative(step.usage.reasoningTokens),
                embeddingTokens: nonNegative(step.usage.embeddingTokens),
                totalTokens: nonNegative(step.usage.totalTokens),
            });
        }
    }
    const models = Array.from(byModelCallId.values());
    if (models.length === 0) return undefined;
    const sum = (field: keyof ModelTokenUsage) => models.reduce((total, row) => total + Number(row[field] ?? 0), 0);
    return normalizeTokenUsageSummary({
        scope: 'turn',
        totalTokens: sum('totalTokens'),
        inputTokens: sum('inputTokens'),
        outputTokens: sum('outputTokens'),
        cachedInputTokens: sum('cachedInputTokens'),
        reasoningTokens: sum('reasoningTokens'),
        embeddingTokens: sum('embeddingTokens'),
        models,
    });
}

function mergePlannedTool(rows: Map<string, ToolProgressRow>, planned: PlannedToolCall): boolean {
    const toolCallId = firstText(planned?.toolCallId);
    if (!toolCallId) return false;
    const prior = rows.get(toolCallId);
    rows.set(toolCallId, {
        toolCallId,
        toolName: firstText(prior?.toolName, planned?.toolName, 'Tool'),
        status: firstText(prior?.status, 'queued'),
    });
    return true;
}

function mergeObservedTool(rows: Map<string, ToolProgressRow>, step: ToolStepState): boolean {
    const toolCallId = firstText(step?.toolCallId);
    if (!toolCallId) return false;
    const prior = rows.get(toolCallId);
    rows.set(toolCallId, {
        toolCallId,
        toolName: firstText(step?.toolName, prior?.toolName, 'Tool'),
        status: firstText(step?.status, prior?.status, 'running'),
    });
    return true;
}

export function summarizeToolProgress(groups: LiveExecutionGroup[] = []): ToolProgressSummary {
    const rowsById = new Map<string, ToolProgressRow>();
    let identityComplete = true;
    for (const group of Array.isArray(groups) ? groups : []) {
        for (const planned of Array.isArray(group?.toolCallsPlanned) ? group.toolCallsPlanned : []) {
            if (!mergePlannedTool(rowsById, planned)) identityComplete = false;
        }
        for (const step of Array.isArray(group?.toolSteps) ? group.toolSteps : []) {
            if (!mergeObservedTool(rowsById, step)) identityComplete = false;
        }
    }
    const rows = Array.from(rowsById.values());
    let completedToolCount = 0;
    let activeToolCount = 0;
    let queuedToolCount = 0;
    let failedToolCount = 0;
    for (const row of rows) {
        const status = normalized(row.status);
        if (COMPLETED_STATUSES.has(status)) completedToolCount += 1;
        else if (FAILED_STATUSES.has(status)) failedToolCount += 1;
        else if (QUEUED_STATUSES.has(status)) queuedToolCount += 1;
        else activeToolCount += 1;
    }
    return {
        completedToolCount,
        activeToolCount,
        queuedToolCount,
        failedToolCount,
        totalToolCount: rows.length,
        identityComplete,
        rows,
    };
}

function resolveTurnState(input: TurnPresentationInput): ActiveTurnState | null {
    if (input.isStopping) return 'stopping';
    const status = normalized(input.status);
    if (status === 'waiting_for_user' || status === 'blocked' || status === 'eliciting') return 'waiting_for_user';
    if (input.isSending && !firstText(input.turnId)) return 'sending';
    if (ACTIVE_STATUSES.has(status) || input.isSending) return 'running';
    return null;
}

function resolveActivity(input: TurnPresentationInput, tools: ToolProgressSummary, state: ActiveTurnState): TurnActivity {
    if (state === 'stopping') return { kind: 'stopping' };
    if (state === 'waiting_for_user') return { kind: 'waiting_for_user' };
    if (state === 'sending') return { kind: 'connecting' };
    const activeTools = tools.rows.filter((row) => ACTIVE_STATUSES.has(normalized(row.status)));
    if (activeTools.length === 1) return { kind: 'tool', label: activeTools[0].toolName };
    if (activeTools.length > 1) return { kind: 'tools' };
    if (input.assistantHasContent) return { kind: 'writing' };
    const phase = normalized(input.phase || input.groups?.at(-1)?.phase);
    if (phase.includes('plan')) return { kind: 'planning' };
    return { kind: 'thinking' };
}

export function resolveActiveTurnProgress(input: TurnPresentationInput = {}): ActiveTurnProgress | null {
    const state = resolveTurnState(input);
    if (!state) return null;
    const groups = Array.isArray(input.groups) ? input.groups : [];
    const tools = summarizeToolProgress(groups);
    return {
        turnId: firstText(input.turnId, groups.at(-1)?.turnId),
        state,
        activity: resolveActivity(input, tools, state),
        ...tools,
        tokenUsage: normalizeTokenUsageSummary(input.tokenUsage) || summarizeExecutionTokenUsage(groups),
        startedAt: firstText(input.startedAt) || undefined,
        canStop: state !== 'sending' && state !== 'stopping' && state !== 'waiting_for_user' && input.canStop !== false,
    };
}

function isStructuredArtifact(value: string): boolean {
    return /```forge-(?:report|data)\b/i.test(value);
}

export function resolveTurnNarration(input: TurnNarrationInput = {}): TurnNarration | null {
    const turnId = firstText(input.turnId);
    if (!turnId) return null;
    const finalContent = firstText(input.finalContent);
    const candidate = finalContent || (Array.isArray(input.candidates)
        ? input.candidates.map((value) => firstText(value)).find((value) => value && !isStructuredArtifact(value))
        : '');
    if (!candidate) return null;
    const final = Boolean(finalContent) || COMPLETED_STATUSES.has(normalized(input.status));
    return {
        turnId,
        messageId: firstText(final ? input.finalMessageId : input.narrationMessageId, `${turnId}:narration`),
        content: candidate,
        status: final ? 'final' : 'active',
    };
}

export function resolveTurnTerminalNotice({ turnId = '', status = '', message = '', partial = false } = {}): TurnTerminalNotice | null {
    const normalizedStatus = normalized(status);
    let outcome: TurnOutcome | null = partial ? 'partial' : null;
    if (TERMINAL_FAILURE_STATUSES.has(normalizedStatus)) outcome = 'failed';
    else if (TERMINAL_CANCELED_STATUSES.has(normalizedStatus)) outcome = 'canceled';
    if (!outcome) return null;
    const category = outcome === 'canceled' ? 'canceled' : (normalizedStatus.includes('timeout') ? 'timed_out' : 'tool_failed');
    return {
        turnId: firstText(turnId),
        outcome,
        category,
        message: firstText(message, outcome === 'canceled' ? 'The request was canceled.' : 'The request could not be completed.'),
    };
}
