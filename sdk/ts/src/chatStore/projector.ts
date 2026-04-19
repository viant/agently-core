/**
 * chatStore/projector.ts — pure projection from canonical client state to
 * render rows consumed by the chat feed.
 *
 * One turn produces:
 *   - one UserRow per user segment (steering → multiple rows)
 *   - exactly one IterationRow, carrying all non-user content (rounds,
 *     lifecycle entries, phase, status, header-derivation data)
 *
 * Placement rule (ui-improvement.md §2.4 / §6.8):
 *     [ u_first, IterationRow, u_rest₁, u_rest₂, ..., u_restₙ ]
 *
 * Header rule (§6.3, §6.5):
 *   - if any round has content (model step / tool call / preamble / content),
 *     label is "Execution details · (N)" where N is the count of such rounds.
 *     Lifecycle-only rounds do NOT contribute to N.
 *   - else, label is a descriptive state from the turn's lifecycle.
 *
 * This module is pure. It reads `ClientConversationState` and returns a new
 * array of `RenderRow`. No mutation, no side effects.
 */

import type {
    ClientConversationState,
    ClientElicitation,
    ClientExecutionPage,
    ClientExecutionPhase,
    ClientLifecycle,
    ClientLifecycleEntry,
    ClientLinkedConversation,
    ClientModelStep,
    ClientToolCall,
    ClientTurnState,
    ClientUserMessage,
} from './types';

// ─── Public render-row types ──────────────────────────────────────────────────

export type RenderRowKind = 'user' | 'iteration';

export interface UserRenderRow {
    kind: 'user';
    renderKey: string;
    turnId: string;
    messageId?: string;
    clientRequestId?: string;
    content: string;
    createdAt?: string;
}

export interface ModelStepRenderView {
    renderKey: string;
    modelCallId?: string;
    assistantMessageId?: string;
    phase?: string;
    provider?: string;
    model?: string;
    status?: string;
    errorMessage?: string;
    requestPayloadId?: string;
    responsePayloadId?: string;
    providerRequestPayloadId?: string;
    providerResponsePayloadId?: string;
    streamPayloadId?: string;
    startedAt?: string;
    completedAt?: string;
}

export interface ToolCallRenderView {
    renderKey: string;
    toolCallId?: string;
    toolName?: string;
    operationId?: string;
    status?: string;
    errorMessage?: string;
    requestPayloadId?: string;
    responsePayloadId?: string;
    linkedConversationId?: string;
    linkedConversationAgentId?: string;
    linkedConversationTitle?: string;
    startedAt?: string;
    completedAt?: string;
}

export interface LifecycleEntryRenderView {
    renderKey: string;
    kind: ClientLifecycleEntry['kind'];
    createdAt: string;
    status?: string;
    errorMessage?: string;
}

export interface RoundRenderView {
    renderKey: string;
    pageId?: string;
    iteration: number;
    phase: ClientExecutionPhase;
    preamble?: string;
    content?: string;
    status?: string;
    finalResponse: boolean;
    modelSteps: ModelStepRenderView[];
    toolCalls: ToolCallRenderView[];
    lifecycleEntries: LifecycleEntryRenderView[];
    /** True iff this round has any non-lifecycle renderable signal. */
    hasContent: boolean;
}

export interface ElicitationRenderView {
    renderKey: string;
    elicitationId?: string;
    status?: ClientElicitation['status'];
    message?: string;
}

export interface LinkedConversationRenderView {
    renderKey: string;
    conversationId: string;
    agentId?: string;
    title?: string;
    status?: string;
}

export interface IterationRenderRow {
    kind: 'iteration';
    renderKey: string;
    turnId: string;
    lifecycle: ClientLifecycle;
    rounds: RoundRenderView[];
    elicitation?: ElicitationRenderView | null;
    linkedConversations: LinkedConversationRenderView[];
    /** Fully-derived header for the card. */
    header: HeaderState;
    /** True while live updates are expected. */
    isStreaming: boolean;
    createdAt?: string;
}

export type RenderRow = UserRenderRow | IterationRenderRow;

// ─── Header derivation ────────────────────────────────────────────────────────

export type HeaderTone = 'running' | 'success' | 'danger' | 'neutral';

export interface HeaderState {
    label: string;
    tone: HeaderTone;
    /** Count shown in the label as "(N)"; 0 means descriptive text, no N. */
    count: number;
}

/**
 * Derive the header label/tone/count from a turn's lifecycle and rounds.
 *
 * Total function over the 5 lifecycle states × 2 content buckets
 * ({has content, has no content}) per ui-improvement.md §6.3 / §6.6.
 */
export function describeHeader(
    lifecycle: ClientLifecycle,
    nRenderableRounds: number,
): HeaderState {
    const tone = toneForLifecycle(lifecycle);
    if (nRenderableRounds >= 1) {
        return {
            label: `Execution details (${nRenderableRounds})`,
            tone,
            count: nRenderableRounds,
        };
    }
    // N = 0: descriptive per-lifecycle label, never renders "(0)".
    switch (lifecycle) {
        case 'pending':
        case 'running':
            return { label: 'Starting turn…', tone: 'running', count: 0 };
        case 'completed':
            return { label: 'Completed', tone: 'success', count: 0 };
        case 'failed':
            return { label: 'Failed', tone: 'danger', count: 0 };
        case 'cancelled':
            return { label: 'Cancelled', tone: 'neutral', count: 0 };
    }
}

export function toneForLifecycle(lifecycle: ClientLifecycle): HeaderTone {
    switch (lifecycle) {
        case 'pending':
        case 'running':
            return 'running';
        case 'completed':
            return 'success';
        case 'failed':
            return 'danger';
        case 'cancelled':
            return 'neutral';
    }
}

// ─── Round-level predicate (§6.3 count rule) ──────────────────────────────────

/**
 * True iff a round carries any non-lifecycle renderable signal. Lifecycle
 * entries alone do NOT flip this to true (§6.3 count rule).
 */
export function roundHasContent(round: RoundRenderView | ClientExecutionPage): boolean {
    const preamble = typeof round.preamble === 'string' ? round.preamble : '';
    const content = typeof round.content === 'string' ? round.content : '';
    if (preamble.trim() !== '' || content.trim() !== '') return true;
    const modelSteps = Array.isArray(round.modelSteps) ? round.modelSteps : [];
    const toolCalls = Array.isArray(round.toolCalls) ? round.toolCalls : [];
    if (modelSteps.length > 0) return true;
    if (toolCalls.length > 0) return true;
    return false;
}

function deriveRoundPhase(page: ClientExecutionPage): ClientExecutionPhase {
    const explicit = page.phase;
    if (explicit === 'intake' || explicit === 'sidecar' || explicit === 'summary') {
        return explicit;
    }
    return 'main';
}

// ─── Projection ────────────────────────────────────────────────────────────────

/**
 * Project a ClientConversationState into render rows. Pure function.
 */
export function projectConversation(
    state: ClientConversationState,
): RenderRow[] {
    const rows: RenderRow[] = [];
    for (const turn of state.turns) {
        rows.push(...projectTurn(turn));
    }
    return rows;
}

/** Project a single turn. Exported for testing. */
export function projectTurn(turn: ClientTurnState): RenderRow[] {
    const out: RenderRow[] = [];

    // u_first = first user segment (originator); u_rest = steering injections.
    const users = turn.users;
    const firstUser = users.length > 0 ? users[0] : null;
    const restUsers = users.length > 1 ? users.slice(1) : [];

    if (firstUser) {
        out.push(userToRow(firstUser, turn));
    }

    out.push(iterationRow(turn));

    for (const u of restUsers) {
        out.push(userToRow(u, turn));
    }
    return out;
}

function userToRow(user: ClientUserMessage, turn: ClientTurnState): UserRenderRow {
    return {
        kind: 'user',
        renderKey: user.renderKey,
        turnId: turn.turnId,
        messageId: user.messageId,
        clientRequestId: user.clientRequestId,
        content: user.content ?? '',
        createdAt: user.createdAt,
    };
}

function iterationRow(turn: ClientTurnState): IterationRenderRow {
    const rounds = turn.pages.map((p) => projectRound(p));
    const renderableCount = rounds.filter((r) => r.hasContent).length;
    const header = describeHeader(turn.lifecycle, renderableCount);
    const isStreaming = turn.lifecycle === 'pending' || turn.lifecycle === 'running';
    return {
        kind: 'iteration',
        renderKey: turn.renderKey,
        turnId: turn.turnId,
        lifecycle: turn.lifecycle,
        rounds,
        elicitation: projectElicitation(turn.elicitation),
        linkedConversations: (turn.linkedConversations ?? []).map(projectLinkedConversation),
        header,
        isStreaming,
        createdAt: turn.createdAt,
    };
}

function projectRound(page: ClientExecutionPage): RoundRenderView {
    const modelSteps = (page.modelSteps ?? []).map(projectModelStep);
    const toolCalls = (page.toolCalls ?? []).map(projectToolCall);
    const lifecycleEntries = (page.lifecycleEntries ?? []).map(projectLifecycleEntry);
    const round: RoundRenderView = {
        renderKey: page.renderKey,
        pageId: page.pageId,
        iteration: typeof page.iteration === 'number' ? page.iteration : 0,
        phase: deriveRoundPhase(page),
        preamble: page.preamble,
        content: page.content,
        status: page.status,
        finalResponse: !!page.finalResponse,
        modelSteps,
        toolCalls,
        lifecycleEntries,
        hasContent: false,
    };
    round.hasContent = roundHasContent(round);
    return round;
}

function projectModelStep(step: ClientModelStep): ModelStepRenderView {
    return {
        renderKey: step.renderKey,
        modelCallId: step.modelCallId,
        assistantMessageId: step.assistantMessageId,
        phase: step.phase,
        provider: step.provider,
        model: step.model,
        status: step.status,
        errorMessage: step.errorMessage,
        requestPayloadId: step.requestPayloadId,
        responsePayloadId: step.responsePayloadId,
        providerRequestPayloadId: step.providerRequestPayloadId,
        providerResponsePayloadId: step.providerResponsePayloadId,
        streamPayloadId: step.streamPayloadId,
        startedAt: step.startedAt,
        completedAt: step.completedAt,
    };
}

function projectToolCall(tool: ClientToolCall): ToolCallRenderView {
    return {
        renderKey: tool.renderKey,
        toolCallId: tool.toolCallId,
        toolName: tool.toolName,
        operationId: tool.operationId,
        status: tool.status,
        errorMessage: tool.errorMessage,
        requestPayloadId: tool.requestPayloadId,
        responsePayloadId: tool.responsePayloadId,
        linkedConversationId: tool.linkedConversationId,
        linkedConversationAgentId: tool.linkedConversationAgentId,
        linkedConversationTitle: tool.linkedConversationTitle,
        startedAt: tool.startedAt,
        completedAt: tool.completedAt,
    };
}

function projectLifecycleEntry(entry: ClientLifecycleEntry): LifecycleEntryRenderView {
    return {
        renderKey: entry.renderKey,
        kind: entry.kind,
        createdAt: entry.createdAt,
        status: entry.status,
        errorMessage: entry.errorMessage,
    };
}

function projectElicitation(elicitation: ClientElicitation | null | undefined): ElicitationRenderView | null {
    if (!elicitation) return null;
    return {
        renderKey: elicitation.renderKey,
        elicitationId: elicitation.elicitationId,
        status: elicitation.status,
        message: elicitation.message,
    };
}

function projectLinkedConversation(lc: ClientLinkedConversation): LinkedConversationRenderView {
    return {
        renderKey: lc.renderKey,
        conversationId: lc.conversationId,
        agentId: lc.agentId,
        title: lc.title,
        status: lc.status,
    };
}
