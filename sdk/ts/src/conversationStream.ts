import { ElicitationTracker, type PendingElicitation as TrackedElicitation } from './elicitation';
import { applyExecutionStreamEventToGroups } from './executionGroups';
import { FeedTracker } from './feedTracker';
import { compareExecutionGroups, compareTemporalEntries, firstString, temporalSequenceValue, temporalTimeValue } from './ordering';
import {
    applyEvent as applyMessageEvent,
    newMessageBuffer,
    reconcileFromTranscript,
    reconcileMessages,
    type MessageBuffer,
} from './reconcile';
import { resolveEventConversationId } from './streamIdentity';
import type { ActiveFeed, JSONObject, LiveExecutionGroup, LiveExecutionGroupsById, Message, SSEEvent, Turn } from './types';

export interface ProjectedConversationTurn extends Partial<Turn> {
    turnId: string;
    conversationId: string;
    status: string;
    createdAt: string;
    startedByMessageId?: string;
    user?: {
        messageId?: string;
        content?: string;
    };
    assistant?: {
        final?: {
            messageId?: string;
            content?: string;
        };
        preamble?: {
            messageId?: string;
            content?: string;
        };
    };
    execution?: {
        pages?: Partial<LiveExecutionGroup>[];
    };
    linkedConversations?: Array<{
        conversationId: string;
        agentId?: string;
        title?: string;
        status?: string;
        response?: string;
        createdAt?: string;
        updatedAt?: string;
    }>;
    elicitation?: {
        elicitationId?: string;
        status?: string;
        message?: string;
        requestedSchema?: JSONObject | null;
        callbackUrl?: string;
        callbackURL?: string;
    };
}

export interface ConversationStreamSnapshot {
    conversationId: string;
    activeTurnId: string | null;
    feeds: ActiveFeed[];
    pendingElicitation: TrackedElicitation | null;
    bufferedMessages: Partial<Message>[];
    liveExecutionGroupsById: LiveExecutionGroupsById;
}

export type CanonicalConversationSnapshot = ConversationStreamSnapshot;

export interface CanonicalLiveAssistantRow {
    id: string;
    conversationId: string;
    turnId: string;
    role: string;
    type: string;
    content: string;
    preamble: string;
    createdAt: string;
    interim: number;
    status: string;
    turnStatus: string;
    sequence: number | null;
    executionGroups: LiveExecutionGroup[];
    isStreaming?: boolean;
    _streamContent?: string;
    _streamFence?: JSONObject | null;
    rawContent?: string;
}

export interface LiveAssistantTransientOverlay {
    id?: string;
    turnId?: string;
    role?: string;
    _type?: string;
    createdAt?: string;
    updatedAt?: string;
    sequence?: number | null;
    eventSeq?: number | null;
    iteration?: number | null;
    messageId?: string;
    assistantMessageId?: string;
    executionGroups?: LiveExecutionGroup[];
    isStreaming?: boolean;
    _streamContent?: string;
    _streamFence?: JSONObject | null;
    rawContent?: string;
}

function selectExplicitLiveAssistantRowsForTurn(
    liveRows: LiveAssistantTransientOverlay[] = [],
    turnId = '',
): LiveAssistantTransientOverlay[] {
    const targetTurnId = String(turnId || '').trim();
    if (!targetTurnId) return [];
    return (Array.isArray(liveRows) ? liveRows : [])
        .filter((row) => (
            String(row?.turnId || '').trim() === targetTurnId
            && String(row?.role || '').trim().toLowerCase() === 'assistant'
        ))
        .sort(compareTemporalEntries);
}

function cloneExecutionGroups(groupsById: LiveExecutionGroupsById = {}): LiveExecutionGroupsById {
    return { ...groupsById };
}

function buildSnapshot(
    conversationId: string,
    activeTurnId: string | null,
    feeds: ActiveFeed[],
    pendingElicitation: TrackedElicitation | null,
    bufferedMessages: Partial<Message>[],
    executionGroupsById: LiveExecutionGroupsById,
): ConversationStreamSnapshot {
    return {
        conversationId,
        activeTurnId,
        feeds,
        pendingElicitation,
        bufferedMessages,
        liveExecutionGroupsById: cloneExecutionGroups(executionGroupsById),
    };
}

function collectLiveAssistantMessageIds(
    snapshot: CanonicalConversationSnapshot | null | undefined,
    targetConversationId: string,
): string[] {
    const buffered = Array.isArray(snapshot?.bufferedMessages) ? snapshot.bufferedMessages : [];
    const groupsById: LiveExecutionGroupsById = snapshot?.liveExecutionGroupsById || {};
    const messageIds = new Set<string>();

    buffered.forEach((entry) => {
        const entryConversationId = String(entry?.conversationId || '').trim();
        if (entryConversationId && entryConversationId === targetConversationId) {
            const messageId = String(entry?.id || '').trim();
            if (messageId) messageIds.add(messageId);
        }
    });
    Object.entries(groupsById).forEach(([messageId, group]) => {
        const groupTurnId = String(group?.turnId || '').trim();
        if (messageId && groupTurnId) {
            messageIds.add(String(messageId).trim());
        }
    });

    return Array.from(messageIds);
}

function projectLiveAssistantRow(
    bufferedById: Map<string, Partial<Message>>,
    groupsById: LiveExecutionGroupsById,
    targetConversationId: string,
    messageId: string,
): CanonicalLiveAssistantRow | null {
    const entry = bufferedById.get(messageId) || {};
    const group = groupsById[messageId] || null;
    const entryConversationId = String(entry?.conversationId || '').trim();
    const rowTurnId = String(entry?.turnId || group?.turnId || '').trim();
    if (!rowTurnId) return null;
    if (entryConversationId && entryConversationId !== targetConversationId) return null;
    const content = String(
        entry?.content
        || group?.content
        || entry?.preamble
        || group?.preamble
        || '',
    ).trim();
    const preamble = String(entry?.preamble || group?.preamble || '').trim();
    const finalResponse = Boolean(group?.finalResponse) || Number(entry?.interim ?? 1) === 0;
    const createdAt = String(
        entry?.createdAt
        || group?.createdAt
        || group?.startedAt
        || group?.completedAt
        || '1970-01-01T00:00:00.000Z',
    ).trim();
    return {
        id: messageId,
        conversationId: entryConversationId || targetConversationId,
        turnId: rowTurnId,
        role: String(entry?.role || 'assistant').trim().toLowerCase(),
        type: String(entry?.type || 'text').trim().toLowerCase(),
        content,
        preamble,
        createdAt,
        interim: finalResponse ? 0 : (Number(entry?.interim ?? 1) || 1),
        status: String(entry?.status || group?.status || '').trim(),
        turnStatus: String(entry?.status || group?.status || '').trim(),
        sequence: Number(entry?.sequence || group?.sequence || 0) || null,
        executionGroups: group ? [group] : [],
    } satisfies CanonicalLiveAssistantRow;
}

function normalizeProjectedTurnStatus(
    turnId: string,
    activeTurnId: string | null | undefined,
    pendingElicitation: TrackedElicitation | null | undefined,
    pages: Partial<LiveExecutionGroup>[] = [],
    assistantEntries: Partial<Message>[] = [],
): string {
    const pendingForTurn = pendingElicitation && String(pendingElicitation?.turnId || '').trim() === turnId;
    if (pendingForTurn) return 'waiting_for_user';
    const pageStatuses = pages.map((page) => String(page?.status || '').trim().toLowerCase()).filter(Boolean);
    if (pageStatuses.some((status) => ['running', 'thinking', 'streaming', 'processing', 'in_progress', 'waiting_for_user', 'tool_calls'].includes(status))) {
        return pageStatuses[pageStatuses.length - 1] || 'running';
    }
    const entryStatuses = assistantEntries.map((entry) => String(entry?.status || '').trim().toLowerCase()).filter(Boolean);
    if (entryStatuses.some((status) => ['running', 'thinking', 'streaming', 'processing', 'in_progress', 'waiting_for_user'].includes(status))) {
        return entryStatuses[entryStatuses.length - 1] || 'running';
    }
    const hasFinalPage = pages.some((page) => Boolean(page?.finalResponse));
    const hasFinalAssistant = assistantEntries.some((entry) => Number(entry?.interim ?? 1) === 0 && String(entry?.content || '').trim() !== '');
    if (hasFinalPage || hasFinalAssistant) {
        const terminalStatus = pageStatuses[pageStatuses.length - 1] || entryStatuses[entryStatuses.length - 1];
        return terminalStatus || 'completed';
    }
    if (String(activeTurnId || '').trim() === turnId) return 'running';
    return pageStatuses[pageStatuses.length - 1] || entryStatuses[entryStatuses.length - 1] || 'running';
}

function normalizeProjectedTurnCreatedAt(
    pages: Partial<LiveExecutionGroup>[] = [],
    entries: Partial<Message>[] = [],
): string {
    const candidates = [
        ...entries.map((entry) => String(entry?.createdAt || '').trim()).filter(Boolean),
        ...pages.flatMap((page) => [
            String(page?.createdAt || '').trim(),
            String(page?.startedAt || '').trim(),
            String(page?.completedAt || '').trim(),
        ]).filter(Boolean),
    ];
    if (candidates.length === 0) return '';
    candidates.sort((left, right) => temporalTimeValue({ createdAt: left }) - temporalTimeValue({ createdAt: right }));
    return candidates[0] || '';
}

function normalizeProjectedTurnSequence(
    pages: Partial<LiveExecutionGroup>[] = [],
    entries: Partial<Message>[] = [],
): number {
    const values = [
        ...entries.map((entry) => temporalSequenceValue(entry || {})).filter((value) => Number.isFinite(value)),
        ...pages.map((page) => temporalSequenceValue(page || {})).filter((value) => Number.isFinite(value)),
    ];
    if (values.length === 0) return 0;
    return Math.min(...values);
}

function collectProjectedLinkedConversations(pages: Partial<LiveExecutionGroup>[] = []) {
    const out = new Map<string, {
        conversationId: string;
        agentId?: string;
        title?: string;
        status?: string;
        response?: string;
        createdAt?: string;
        updatedAt?: string;
    }>();
    for (const page of Array.isArray(pages) ? pages : []) {
        for (const step of Array.isArray(page?.toolSteps) ? page.toolSteps : []) {
            const conversationId = String(step?.linkedConversationId || '').trim();
            if (!conversationId) continue;
            const existing = out.get(conversationId) || { conversationId };
            out.set(conversationId, {
                ...existing,
                conversationId,
                agentId: firstString(step?.linkedConversationAgentId, existing.agentId),
                title: firstString(step?.linkedConversationTitle, existing.title),
                status: firstString(step?.status, existing.status),
                response: firstString(
                    typeof step?.asyncOperation?.response === 'string' ? step.asyncOperation.response : '',
                    existing.response,
                ),
                createdAt: firstString(page?.createdAt, existing.createdAt),
                updatedAt: firstString(page?.completedAt, page?.updatedAt, existing.updatedAt),
            });
        }
    }
    return Array.from(out.values());
}

export function projectTrackerToTurns(
    snapshot: CanonicalConversationSnapshot | null | undefined,
    conversationId = '',
): ProjectedConversationTurn[] {
    const targetConversationId = String(conversationId || snapshot?.conversationId || '').trim();
    if (!targetConversationId) return [];

    const buffered = Array.isArray(snapshot?.bufferedMessages) ? snapshot.bufferedMessages : [];
    const groups = Object.values(snapshot?.liveExecutionGroupsById || {})
        .filter((group) => String(group?.turnId || '').trim() !== '')
        .sort(compareExecutionGroups);

    const turnIds = new Set<string>();
    buffered.forEach((entry) => {
        const entryConversationId = String(entry?.conversationId || '').trim();
        const turnId = String(entry?.turnId || '').trim();
        if (!turnId) return;
        if (entryConversationId && entryConversationId !== targetConversationId) return;
        turnIds.add(turnId);
    });
    groups.forEach((group) => {
        const turnId = String(group?.turnId || '').trim();
        if (turnId) turnIds.add(turnId);
    });
    const pendingTurnId = String(snapshot?.pendingElicitation?.turnId || '').trim();
    if (pendingTurnId) turnIds.add(pendingTurnId);

    const turns = Array.from(turnIds).map((turnId) => {
        const turnEntries = buffered
            .filter((entry) => {
                const entryConversationId = String(entry?.conversationId || '').trim();
                return String(entry?.turnId || '').trim() === turnId
                    && (!entryConversationId || entryConversationId === targetConversationId);
            })
            .sort(compareTemporalEntries);
        const turnPages = groups
            .filter((group) => String(group?.turnId || '').trim() === turnId)
            .map((group) => ({
                ...group,
                turnId,
                pageId: firstString(group?.pageId, group?.assistantMessageId, group?.modelMessageId),
                assistantMessageId: firstString(group?.assistantMessageId, group?.pageId, group?.modelMessageId),
                parentMessageId: firstString(group?.parentMessageId),
            }));
        const userEntry = turnEntries.find((entry) => String(entry?.role || '').trim().toLowerCase() === 'user') || null;
        const assistantEntries = turnEntries.filter((entry) => String(entry?.role || '').trim().toLowerCase() === 'assistant');
        const finalAssistant = [...assistantEntries].reverse().find((entry) => Number(entry?.interim ?? 1) === 0 && String(entry?.content || '').trim() !== '') || null;
        const preambleAssistant = [...assistantEntries].reverse().find((entry) => String(entry?.preamble || '').trim() !== '') || null;
        const turnCreatedAt = normalizeProjectedTurnCreatedAt(turnPages, turnEntries);
        const turnSequence = normalizeProjectedTurnSequence(turnPages, turnEntries);
        const linkedConversations = collectProjectedLinkedConversations(turnPages);
        const pendingElicitation = snapshot?.pendingElicitation && String(snapshot.pendingElicitation?.turnId || '').trim() === turnId
            ? snapshot.pendingElicitation
            : null;

        return {
            turnId,
            id: turnId,
            conversationId: targetConversationId,
            status: normalizeProjectedTurnStatus(turnId, snapshot?.activeTurnId, pendingElicitation, turnPages, assistantEntries),
            createdAt: turnCreatedAt,
            sequence: turnSequence,
            startedByMessageId: userEntry
                ? String(userEntry?.id || '').trim()
                : undefined,
            user: userEntry
                ? {
                    messageId: String(userEntry?.id || '').trim(),
                    content: String(userEntry?.content || '').trim(),
                }
                : undefined,
            assistant: {
                final: finalAssistant
                    ? {
                        messageId: String(finalAssistant?.id || '').trim(),
                        content: String(finalAssistant?.content || '').trim(),
                    }
                    : undefined,
                preamble: preambleAssistant
                    ? {
                        messageId: String(preambleAssistant?.id || '').trim(),
                        content: String(preambleAssistant?.preamble || '').trim(),
                    }
                    : undefined,
            },
            execution: {
                pages: turnPages,
            },
            linkedConversations,
            elicitation: pendingElicitation
                ? {
                    elicitationId: pendingElicitation.elicitationId,
                    status: 'pending',
                    message: pendingElicitation.message,
                    requestedSchema: pendingElicitation.requestedSchema || null,
                    callbackUrl: pendingElicitation.callbackURL || '',
                    callbackURL: pendingElicitation.callbackURL || '',
                }
                : undefined,
        } satisfies ProjectedConversationTurn;
    });

    return turns.sort((left, right) => {
        const leftTime = temporalTimeValue({ createdAt: left.createdAt });
        const rightTime = temporalTimeValue({ createdAt: right.createdAt });
        if (leftTime !== rightTime) return leftTime - rightTime;
        const leftSeq = temporalSequenceValue({ sequence: (left as any)?.sequence });
        const rightSeq = temporalSequenceValue({ sequence: (right as any)?.sequence });
        if (leftSeq !== rightSeq) return leftSeq - rightSeq;
        return String(left?.turnId || '').localeCompare(String(right?.turnId || ''));
    });
}

export class ConversationStreamTracker {
    private readonly _messages: MessageBuffer;
    private _executionGroupsById: LiveExecutionGroupsById;
    private readonly _feeds: FeedTracker;
    private readonly _elicitation: ElicitationTracker;
    private _conversationId = '';

    constructor(conversationId = '') {
        this._messages = newMessageBuffer();
        this._executionGroupsById = {};
        this._feeds = new FeedTracker();
        this._elicitation = new ElicitationTracker();
        this._conversationId = String(conversationId || '').trim();
    }

    /** Backward-compatible composite view; prefer `canonicalState` for canonical access. */
    get state(): ConversationStreamSnapshot {
        return this.snapshot();
    }

    get compositeState(): ConversationStreamSnapshot {
        return this.snapshot();
    }

    get canonicalState(): CanonicalConversationSnapshot {
        return this.snapshot();
    }

    get conversationId(): string {
        return this._conversationId;
    }

    get activeTurnId(): string | null {
        return this._messages.activeTurnId;
    }

    get feeds(): ActiveFeed[] {
        return this._feeds.feeds;
    }

    get pendingElicitation(): TrackedElicitation | null {
        return this._elicitation.pending;
    }

    get bufferedMessages(): Partial<Message>[] {
        return Array.from(this._messages.byId.values());
    }

    snapshot(): ConversationStreamSnapshot {
        return buildSnapshot(
            this.conversationId,
            this.activeTurnId,
            this.feeds,
            this.pendingElicitation,
            this.bufferedMessages,
            this._executionGroupsById,
        );
    }

    snapshotComposite(): ConversationStreamSnapshot {
        return this.snapshot();
    }

    snapshotCanonical(): CanonicalConversationSnapshot {
        return this.snapshot();
    }

    clear(): void {
        this._messages.byId.clear();
        this._messages.activeTurnId = null;
        this._executionGroupsById = {};
        this._feeds.clear();
        this._elicitation.clear();
        this._conversationId = '';
    }

    reset(): void {
        this.clear();
    }

    applyEvent(event: SSEEvent): { id: string; content: string; final: boolean } | null {
        const conversationId = resolveEventConversationId(event);
        if (conversationId) {
            this._conversationId = conversationId;
        }
        this._executionGroupsById = applyExecutionStreamEventToGroups(this._executionGroupsById, event);
        this._feeds.applyEvent(event);
        this._elicitation.applyEvent(event);
        return applyMessageEvent(this._messages, event);
    }

    reconcileTranscript(turns: Turn[]): void {
        const firstTurn = Array.isArray(turns) && turns.length > 0 ? turns[0] : null;
        if (firstTurn?.conversationId) {
            this._conversationId = String(firstTurn.conversationId).trim();
        }
        reconcileFromTranscript(this._messages, turns);
        const transcriptGroups = Array.isArray(turns)
            ? turns.flatMap((turn: any) => Array.isArray(turn?.execution?.pages) ? turn.execution.pages : [])
            : [];
        if (transcriptGroups.length > 0) {
            const nextGroups: LiveExecutionGroupsById = { ...this._executionGroupsById };
            for (const page of transcriptGroups) {
                const key = String(page?.assistantMessageId || page?.pageId || '').trim();
                if (!key) continue;
                nextGroups[key] = {
                    ...(nextGroups[key] || {}),
                    ...page,
                    assistantMessageId: String(page?.assistantMessageId || page?.pageId || '').trim(),
                    turnId: String(page?.turnId || nextGroups[key]?.turnId || '').trim(),
                };
            }
            this._executionGroupsById = nextGroups;
        }
    }

    applyTranscript(turns: Turn[]): void {
        this.reconcileTranscript(turns);
    }

    reconcile(serverMessages: Message[]): Message[] {
        return reconcileMessages(this._messages, serverMessages);
    }
}

export function projectLiveAssistantRows(
    snapshot: CanonicalConversationSnapshot | null | undefined,
    conversationId = '',
): CanonicalLiveAssistantRow[] {
    const targetConversationId = String(conversationId || snapshot?.conversationId || '').trim();
    if (!targetConversationId) return [];
    const buffered = Array.isArray(snapshot?.bufferedMessages) ? snapshot.bufferedMessages : [];
    const bufferedById = new Map(
        buffered
            .map((entry) => [String(entry?.id || '').trim(), entry] as const)
            .filter(([id]) => id),
    );
    const groupsById = snapshot?.liveExecutionGroupsById || {};
    return collectLiveAssistantMessageIds(snapshot, targetConversationId)
        .map((messageId) => projectLiveAssistantRow(bufferedById, groupsById, targetConversationId, messageId))
        .filter(Boolean)
        .sort(compareTemporalEntries) as CanonicalLiveAssistantRow[];
}

export function overlayLiveAssistantTransientState(
    rows: CanonicalLiveAssistantRow[] = [],
    liveRows: LiveAssistantTransientOverlay[] = [],
): CanonicalLiveAssistantRow[] {
    const explicitRows = Array.isArray(liveRows) ? liveRows : [];
    return (Array.isArray(rows) ? rows : []).map((row) => {
        const rowId = String(row?.id || '').trim();
        if (!rowId) return row;
        const matchingLiveRow = explicitRows.find((entry) => (
            String(entry?.role || '').trim().toLowerCase() === 'assistant'
            && String(entry?.id || '').trim() === rowId
        ));
        if (!matchingLiveRow) return row;
        return {
            ...row,
            isStreaming: matchingLiveRow?.isStreaming,
            _streamContent: matchingLiveRow?._streamContent,
            _streamFence: matchingLiveRow?._streamFence,
            rawContent: matchingLiveRow?.rawContent ?? row?.rawContent,
        } as CanonicalLiveAssistantRow;
    });
}

export function filterExplicitLiveRowsAgainstTracker(
    trackerRows: CanonicalLiveAssistantRow[] = [],
    liveRows: LiveAssistantTransientOverlay[] = [],
): LiveAssistantTransientOverlay[] {
    const explicitRows = Array.isArray(liveRows) ? liveRows : [];
    const trackerOwnsAssistantRows = trackerRows.length > 0;
    const trackerTurnIds = new Set(trackerRows.map((row) => String(row?.turnId || '').trim()).filter(Boolean));
    const trackerRowIds = new Set(trackerRows.map((row) => String(row?.id || '').trim()).filter(Boolean));
    return explicitRows.filter((row) => {
        if (String(row?._type || '').trim().toLowerCase() === 'stream') return false;
        if (String(row?.role || '').trim().toLowerCase() !== 'assistant') return true;
        if (!trackerOwnsAssistantRows) return true;
        const rowId = String(row?.id || '').trim();
        if (rowId && trackerRowIds.has(rowId)) return false;
        const turnId = String(row?.turnId || '').trim();
        return !turnId || !trackerTurnIds.has(turnId);
    });
}

export function buildEffectiveLiveAssistantRows(
    snapshot: CanonicalConversationSnapshot | null | undefined,
    liveRows: LiveAssistantTransientOverlay[] = [],
    conversationId = '',
): Array<CanonicalLiveAssistantRow | LiveAssistantTransientOverlay> {
    const trackerRows = projectLiveAssistantRows(snapshot, conversationId);
    const trackerRowsWithTransientState = overlayLiveAssistantTransientState(
        trackerRows,
        liveRows as LiveAssistantTransientOverlay[],
    );
    return [
        ...trackerRowsWithTransientState,
        ...filterExplicitLiveRowsAgainstTracker(trackerRows, liveRows),
    ].sort(compareTemporalEntries);
}

export function buildEffectiveLiveRows(
    snapshot: CanonicalConversationSnapshot | null | undefined,
    liveRows: LiveAssistantTransientOverlay[] = [],
    conversationId = '',
): Array<CanonicalLiveAssistantRow | LiveAssistantTransientOverlay> {
    const explicitRows = Array.isArray(liveRows) ? liveRows : [];
    const streamRows = explicitRows.filter((row) => (
        String(row?._type || '').trim().toLowerCase() === 'stream'
    ));
    return [
        ...buildEffectiveLiveAssistantRows(snapshot, explicitRows, conversationId),
        ...streamRows,
    ];
}

export function hasLiveAssistantRowForTurn(
    snapshot: CanonicalConversationSnapshot | null | undefined,
    conversationId = '',
    turnId = '',
): boolean {
    return selectLiveAssistantRowsForTurn(snapshot, conversationId, turnId).length > 0;
}

export function selectLiveAssistantRowsForTurn(
    snapshot: CanonicalConversationSnapshot | null | undefined,
    conversationId = '',
    turnId = '',
): CanonicalLiveAssistantRow[] {
    const targetTurnId = String(turnId || '').trim();
    if (!targetTurnId) return [];
    return projectLiveAssistantRows(snapshot, conversationId)
        .filter((row) => String(row?.turnId || '').trim() === targetTurnId);
}

export function latestLiveAssistantRowForTurn(
    snapshot: CanonicalConversationSnapshot | null | undefined,
    conversationId = '',
    turnId = '',
): CanonicalLiveAssistantRow | null {
    const rows = selectLiveAssistantRowsForTurn(snapshot, conversationId, turnId);
    return rows.length > 0 ? rows[rows.length - 1] : null;
}

export function latestLiveAssistantRowForTurnWithTransientState(
    snapshot: CanonicalConversationSnapshot | null | undefined,
    liveRows: LiveAssistantTransientOverlay[] = [],
    conversationId = '',
    turnId = '',
): CanonicalLiveAssistantRow | null {
    const targetTurnId = String(turnId || '').trim();
    if (!targetTurnId) return null;
    const trackerRows = selectLiveAssistantRowsForTurn(snapshot, conversationId, targetTurnId);
    const trackerRow = trackerRows.length > 0 ? trackerRows[trackerRows.length - 1] : null;
    if (!trackerRow) return null;
    const trackerId = String(trackerRow?.id || '').trim();
    const sameTurnLiveRows = selectExplicitLiveAssistantRowsForTurn(liveRows, targetTurnId);
    const exactLiveRow = trackerId
        ? sameTurnLiveRows.find((row) => String(row?.id || '').trim() === trackerId)
        : null;
    const matchingLiveRow = exactLiveRow
        || ((trackerRows.length === 1 && sameTurnLiveRows.length === 1) ? sameTurnLiveRows[0] : null);
    return matchingLiveRow
        ? ({
            ...trackerRow,
            ...matchingLiveRow,
            executionGroups: trackerRow.executionGroups || matchingLiveRow.executionGroups || [],
        } as CanonicalLiveAssistantRow)
        : trackerRow;
}

export function latestEffectiveLiveAssistantRow(
    snapshot: CanonicalConversationSnapshot | null | undefined,
    liveRows: LiveAssistantTransientOverlay[] = [],
    conversationId = '',
    turnId = '',
): CanonicalLiveAssistantRow | LiveAssistantTransientOverlay | null {
    const targetTurnId = String(turnId || '').trim();
    if (!targetTurnId) return null;
    const trackerBacked = latestLiveAssistantRowForTurnWithTransientState(
        snapshot,
        liveRows,
        conversationId,
        targetTurnId,
    );
    if (trackerBacked) return trackerBacked;
    return selectExplicitLiveAssistantRowsForTurn(liveRows, targetTurnId).at(-1) || null;
}
