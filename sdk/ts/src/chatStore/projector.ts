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
 *   - if any round has content (model step / tool call / narration / content),
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
    ClientStandaloneMessage,
    ClientToolCall,
    ClientTurnState,
    ClientUserMessage,
} from './types';

import { compareTemporalEntries } from '../ordering';
import { getFieldProvenance } from './reducer';
import type { JSONObject } from '../types';

// ─── Public render-row types ──────────────────────────────────────────────────

export type RenderRowKind = 'user' | 'assistant' | 'iteration' | 'mcpui';

export interface UserRenderRow {
    kind: 'user';
    renderKey: string;
    turnId: string;
    messageId?: string;
    clientRequestId?: string;
    content: string;
    createdAt?: string;
    sequence?: number;
}

export interface AssistantRenderRow {
    kind: 'assistant';
    renderKey: string;
    turnId: string;
    messageId?: string;
    content: string;
    createdAt?: string;
    sequence?: number;
    mode?: string;
    status?: string;
}

export interface MCPUIRenderRow {
    kind: 'mcpui';
    renderKey: string;
    turnId: string;
    toolCallId?: string;
    toolName?: string;
    uri: string;
    toolInput?: JSONObject | null;
    createdAt?: string;
    sequence?: number;
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
    uiResourceUri?: string;
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
    narration?: string;
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
    requestedSchema?: ClientElicitation['requestedSchema'];
    callbackUrl?: ClientElicitation['callbackUrl'];
    responsePayload?: ClientElicitation['responsePayload'];
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
    turnStartedAt?: string;
    rounds: RoundRenderView[];
    elicitation?: ElicitationRenderView | null;
    elicitations?: ElicitationRenderView[];
    linkedConversations: LinkedConversationRenderView[];
    /** Fully-derived header for the card. */
    header: HeaderState;
    /** True while live updates are expected. */
    isStreaming: boolean;
    createdAt?: string;
    sequence?: number;
}

export type RenderRow = UserRenderRow | AssistantRenderRow | IterationRenderRow | MCPUIRenderRow;

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
    const narration = typeof round.narration === 'string' ? round.narration : '';
    const content = typeof round.content === 'string' ? round.content : '';
    if (narration.trim() !== '' || content.trim() !== '') return true;
    const modelSteps = Array.isArray(round.modelSteps) ? round.modelSteps : [];
    const toolCalls = Array.isArray(round.toolCalls) ? round.toolCalls : [];
    if (modelSteps.length > 0) return true;
    if (toolCalls.length > 0) return true;
    return false;
}

function deriveRoundPhase(page: ClientExecutionPage): ClientExecutionPhase {
    const explicit = page.phase;
    if (explicit === 'intake' || explicit === 'sidecar' || explicit === 'summary' || explicit === 'bootstrap') {
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
    const rows: Array<RenderRow & { sequence?: number; role?: string; _source?: string }> = [];
    const users = turn.users;
    const firstUser = users.length > 0 ? users[0] : null;
    const restUsers = users.length > 1 ? users.slice(1) : [];
    const standaloneMessages = projectStandaloneMessages(turn);
    const mcpuiRows = projectMCPUITurnRows(turn);

    if (firstUser) rows.push({ ...userToRow(firstUser, turn), _source: 'primary_user' });
    const trailing = [
        { ...iterationRow(turn), _source: 'iteration' },
        ...mcpuiRows.map((row) => ({ ...row, _source: 'mcpui' })),
        ...restUsers.map((u) => ({ ...userToRow(u, turn), _source: 'steer_user' })),
        ...standaloneMessages.map((message) => ({ ...messageToRow(message, turn), _source: 'turn_message' })),
    ];
    trailing.sort(compareProjectedRows);
    return [...rows, ...trailing];
}

function projectMCPUITurnRows(turn: ClientTurnState): MCPUIRenderRow[] {
    const rows: MCPUIRenderRow[] = [];
    const seenUris = new Set<string>();
    for (const page of Array.isArray(turn.pages) ? turn.pages : []) {
        for (const tool of Array.isArray(page.toolCalls) ? page.toolCalls : []) {
            const uri = String(tool?.uiResourceUri || '').trim();
            if (!uri) continue;
            seenUris.add(uri);
            rows.push({
                kind: 'mcpui',
                renderKey: `${tool.renderKey}:mcpui`,
                turnId: turn.turnId,
                toolCallId: tool.toolCallId,
                toolName: tool.toolName,
                uri,
                toolInput: tool.requestPayload ?? null,
                createdAt: String(tool.completedAt || tool.startedAt || page.completedAt || page.startedAt || page.createdAt || '').trim() || undefined,
                sequence: typeof page.sequence === 'number' ? page.sequence : undefined,
            });
        }
        const pageURI = String(page?.content || '').trim();
        if (!page.finalResponse || !pageURI.startsWith('ui://') || seenUris.has(pageURI)) {
            continue;
        }
        seenUris.add(pageURI);
        rows.push({
            kind: 'mcpui',
            renderKey: `${page.renderKey}:mcpui`,
            turnId: turn.turnId,
            toolCallId: undefined,
            toolName: 'interactive_app',
            uri: pageURI,
            toolInput: null,
            createdAt: String(page.completedAt || page.startedAt || page.createdAt || '').trim() || undefined,
            sequence: typeof page.sequence === 'number' ? page.sequence : undefined,
        });
    }
    for (const message of Array.isArray(turn.messages) ? turn.messages : []) {
        if (message.role !== 'assistant') continue;
        const uri = String(message.content || '').trim();
        if (!uri.startsWith('ui://') || seenUris.has(uri)) continue;
        seenUris.add(uri);
        rows.push({
            kind: 'mcpui',
            renderKey: `${message.renderKey}:mcpui`,
            turnId: turn.turnId,
            toolCallId: undefined,
            toolName: 'interactive_app',
            uri,
            toolInput: null,
            createdAt: message.createdAt,
            sequence: message.sequence,
        });
    }
    const assistantFinalUri = String(turn.assistantFinal?.content || '').trim();
    if (assistantFinalUri.startsWith('ui://') && !seenUris.has(assistantFinalUri)) {
        seenUris.add(assistantFinalUri);
        rows.push({
            kind: 'mcpui',
            renderKey: `${turn.assistantFinal?.renderKey || turn.turnId}:mcpui`,
            turnId: turn.turnId,
            toolCallId: undefined,
            toolName: 'interactive_app',
            uri: assistantFinalUri,
            toolInput: null,
            createdAt: turn.assistantFinal?.createdAt,
            sequence: undefined,
        });
    }
    return rows;
}

function pageOwnedAssistantMessageIds(turn: ClientTurnState): Set<string> {
    const ids = new Set<string>();
    for (const page of Array.isArray(turn.pages) ? turn.pages : []) {
        const narrationId = String(page?.narrationMessageId || '').trim();
        const finalId = String(page?.finalAssistantMessageId || '').trim();
        if (narrationId) ids.add(narrationId);
        if (finalId) ids.add(finalId);
    }
    const aggregateNarrationId = String(turn.assistantNarration?.messageId || '').trim();
    const aggregateFinalId = String(turn.assistantFinal?.messageId || '').trim();
    if (aggregateNarrationId) ids.add(aggregateNarrationId);
    if (aggregateFinalId) ids.add(aggregateFinalId);
    return ids;
}

function projectStandaloneMessages(turn: ClientTurnState): ClientStandaloneMessage[] {
    const pageOwnedIds = pageOwnedAssistantMessageIds(turn);
    return (Array.isArray(turn.messages) ? turn.messages : []).filter((message) => {
        if (message.role !== 'assistant') return true;
        if (Number(message.interim ?? 0) > 0) return false;
        if (String(message.content || '').trim().startsWith('ui://')) return false;
        const messageId = String(message.messageId || '').trim();
        if (messageId && pageOwnedIds.has(messageId)) return false;
        return true;
    });
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
        sequence: user.sequence,
    };
}

function messageToRow(message: ClientStandaloneMessage, turn: ClientTurnState): AssistantRenderRow | UserRenderRow {
    if (message.role === 'user') {
        return {
            kind: 'user',
            renderKey: message.renderKey,
            turnId: turn.turnId,
            messageId: message.messageId,
            content: message.content ?? '',
            createdAt: message.createdAt,
            sequence: message.sequence,
        };
    }
    return {
        kind: 'assistant',
        renderKey: message.renderKey,
        turnId: turn.turnId,
        messageId: message.messageId,
        content: message.content ?? '',
        createdAt: message.createdAt,
        sequence: message.sequence,
        mode: message.mode,
        status: message.status,
    };
}

function iterationRow(turn: ClientTurnState): IterationRenderRow {
    const rounds = turn.pages.map((p) => projectRound(p));
    const renderableCount = rounds.filter((r) => r.hasContent).length;
    const header = describeHeader(turn.lifecycle, renderableCount);
    const lifecycleProvenance = getFieldProvenance(turn, 'lifecycle');
    const isStreaming = (turn.lifecycle === 'pending' || turn.lifecycle === 'running')
        && lifecycleProvenance !== 'transcript';
    return {
        kind: 'iteration',
        renderKey: turn.renderKey,
        turnId: turn.turnId,
        lifecycle: turn.lifecycle,
        turnStartedAt: turn.createdAt,
        rounds,
        elicitation: projectElicitation(turn.elicitation),
        elicitations: projectElicitations(turn),
        linkedConversations: (turn.linkedConversations ?? []).map(projectLinkedConversation),
        header,
        isStreaming,
        createdAt: latestIterationCreatedAt(turn),
        sequence: latestIterationSequence(turn),
    };
}

function latestIterationSequence(turn: ClientTurnState): number | undefined {
    const pages = Array.isArray(turn.pages) ? turn.pages : [];
    const values = pages
        .map((page) => Number(page.sequence))
        .filter((value) => Number.isFinite(value) && value > 0);
    if (values.length === 0) return undefined;
    return Math.max(...values);
}

function latestIterationCreatedAt(turn: ClientTurnState): string | undefined {
    const candidates = (Array.isArray(turn.pages) ? turn.pages : [])
        .map((page) => String(page.createdAt || page.completedAt || page.startedAt || '').trim())
        .filter(Boolean)
        .sort();
    return candidates.at(-1) || turn.createdAt;
}

function compareProjectedRows(left: RenderRow & { sequence?: number; role?: string; _source?: string }, right: RenderRow & { sequence?: number; role?: string; _source?: string }): number {
    const leftTurnId = String(left.turnId || '').trim();
    const rightTurnId = String(right.turnId || '').trim();
    if (leftTurnId === rightTurnId) {
        const leftSource = String(left._source || '').trim();
        const rightSource = String(right._source || '').trim();
        if ((leftSource === 'primary_user' && rightSource !== 'primary_user') || (rightSource === 'primary_user' && leftSource !== 'primary_user')) {
            return leftSource === 'primary_user' ? -1 : 1;
        }
        if ((leftSource === 'iteration' && rightSource === 'steer_user') || (leftSource === 'steer_user' && rightSource === 'iteration')) {
            return leftSource === 'iteration' ? -1 : 1;
        }
        if ((leftSource === 'iteration' && rightSource === 'mcpui') || (leftSource === 'mcpui' && rightSource === 'iteration')) {
            return leftSource === 'iteration' ? -1 : 1;
        }
        const leftSeq = Number(left.sequence);
        const rightSeq = Number(right.sequence);
        const leftHasSeq = Number.isFinite(leftSeq) && leftSeq > 0;
        const rightHasSeq = Number.isFinite(rightSeq) && rightSeq > 0;
        if (leftHasSeq && rightHasSeq && leftSeq !== rightSeq) return leftSeq - rightSeq;
        if ((leftSource === 'turn_message' && rightSource === 'iteration') || (leftSource === 'iteration' && rightSource === 'turn_message')) {
            if (leftHasSeq !== rightHasSeq) return leftHasSeq ? -1 : 1;
        }
    }
    return compareTemporalEntries(
        {
            id: left.renderKey,
            messageId: left.kind === 'iteration' || left.kind === 'mcpui' ? undefined : left.messageId,
            turnId: leftTurnId,
            role: left.kind === 'iteration' || left.kind === 'mcpui' ? 'assistant' : left.kind,
            createdAt: left.createdAt,
            sequence: left.sequence,
        },
        {
            id: right.renderKey,
            messageId: right.kind === 'iteration' || right.kind === 'mcpui' ? undefined : right.messageId,
            turnId: rightTurnId,
            role: right.kind === 'iteration' || right.kind === 'mcpui' ? 'assistant' : right.kind,
            createdAt: right.createdAt,
            sequence: right.sequence,
        }
    );
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
        narration: page.narration,
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
        uiResourceUri: tool.uiResourceUri,
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

function projectElicitations(turn: ClientTurnState): ElicitationRenderView[] {
    const source = Array.isArray(turn.elicitations) && turn.elicitations.length > 0
        ? turn.elicitations
        : (turn.elicitation ? [turn.elicitation] : []);
    return source
        .map((elicitation) => projectElicitation(elicitation))
        .filter((elicitation): elicitation is ElicitationRenderView => !!elicitation);
}

function projectElicitation(elicitation: ClientElicitation | null | undefined): ElicitationRenderView | null {
    if (!elicitation) return null;
    return {
        renderKey: elicitation.renderKey,
        elicitationId: elicitation.elicitationId,
        status: elicitation.status,
        message: elicitation.message,
        requestedSchema: elicitation.requestedSchema,
        callbackUrl: elicitation.callbackUrl,
        responsePayload: elicitation.responsePayload,
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
