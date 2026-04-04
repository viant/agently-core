import { ElicitationTracker, type PendingElicitation as TrackedElicitation } from './elicitation';
import { applyExecutionStreamEventToGroups } from './executionGroups';
import { FeedTracker } from './feedTracker';
import { compareTemporalEntries } from './ordering';
import {
    applyEvent as applyMessageEvent,
    newMessageBuffer,
    reconcileFromTranscript,
    reconcileMessages,
    type MessageBuffer,
} from './reconcile';
import { resolveEventConversationId } from './streamIdentity';
import type { ActiveFeed, JSONObject, LiveExecutionGroup, LiveExecutionGroupsById, Message, SSEEvent, Turn } from './types';

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
    const trackerRow = latestLiveAssistantRowForTurn(snapshot, conversationId, targetTurnId);
    if (!trackerRow) return null;
    const trackerId = String(trackerRow?.id || '').trim();
    const sameTurnLiveRows = selectExplicitLiveAssistantRowsForTurn(liveRows, targetTurnId);
    const exactLiveRow = trackerId
        ? sameTurnLiveRows.find((row) => String(row?.id || '').trim() === trackerId)
        : null;
    const matchingLiveRow = exactLiveRow || sameTurnLiveRows.at(-1) || null;
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
