/**
 * reconcile.ts — SSE message buffering and transcript reconciliation.
 *
 * During streaming, chunks arrive before the full message is persisted.
 * The MessageBuffer accumulates streaming content optimistically, and
 * reconcileFromTranscript merges it with the authoritative server state.
 */

import type { JSONObject, SSEEvent, Message, Turn } from './types';
import { compareTemporalEntries } from './ordering';
import { resolveEventConversationId, resolveEventMessageId, resolveEventTurnId } from './streamIdentity';
import { terminalStatusForType } from './streamEventMeta';

// ─── Buffer ────────────────────────────────────────────────────────────────────

export interface MessageBuffer {
    /** Accumulated text keyed by the best available assistant/message identity. */
    byId: Map<string, Partial<Message>>;
    /** Currently active turn ID (null when idle). */
    activeTurnId: string | null;
}

export function newMessageBuffer(): MessageBuffer {
    return { byId: new Map(), activeTurnId: null };
}

function updateEntryIdentity(existing: Partial<Message>, event: SSEEvent, conversationId: string, turnId: string): Partial<Message> {
    if (turnId && !existing.turnId) existing.turnId = turnId;
    if (conversationId && !existing.conversationId) existing.conversationId = conversationId;
    if (event.createdAt && !existing.createdAt) existing.createdAt = event.createdAt;
    if (Number.isFinite(Number(event.eventSeq))) {
        const nextSeq = Number(event.eventSeq);
        const currentSeq = Number(existing.sequence || 0) || 0;
        if (nextSeq > currentSeq) {
            existing.sequence = nextSeq;
        }
    }
    return existing;
}

function ensureMessageEntry(
    buf: MessageBuffer,
    key: string,
    event: SSEEvent,
    conversationId: string,
    turnId: string,
): Partial<Message> {
    const existing = buf.byId.get(key);
    if (existing) {
        return updateEntryIdentity(existing, event, conversationId, turnId);
    }
    return {
        id: key,
        conversationId,
        turnId,
        role: 'assistant',
        type: 'text',
        content: '',
        interim: 1,
        createdAt: String(event.createdAt || '').trim(),
        sequence: Number.isFinite(Number(event.eventSeq)) ? Number(event.eventSeq) : undefined,
    } as Partial<Message>;
}

function markTurnTerminal(buf: MessageBuffer, turnId: string, terminalStatus: string): void {
    if (!turnId) return;
    for (const entry of buf.byId.values()) {
        if (String(entry?.turnId || '').trim() !== turnId) continue;
        entry.interim = 0;
        entry.status = terminalStatus;
    }
}

function setActiveTurn(buf: MessageBuffer, turnId: string): void {
    buf.activeTurnId = turnId || buf.activeTurnId;
}

function storeEntry(buf: MessageBuffer, key: string, entry: Partial<Message>): void {
    buf.byId.set(key, entry);
}

function applyMessagePatch(existing: Partial<Message>, patch: JSONObject): void {
    if (patch.linkedConversationId != null) {
        existing.linkedConversationId = String(patch.linkedConversationId);
    }
    if (patch.status != null) {
        existing.status = String(patch.status);
    }
    if (patch.toolName != null) {
        existing.toolName = String(patch.toolName);
    }
    if (patch.preamble != null) {
        existing.preamble = String(patch.preamble);
    }
    if (patch.interim != null) {
        const n = Number(patch.interim);
        if (Number.isFinite(n)) {
            existing.interim = n;
        }
    }
    if (patch.content != null) {
        existing.content = String(patch.content);
    }
}

// ─── Apply streaming event ─────────────────────────────────────────────────────

/**
 * Applies an SSE event to the buffer for optimistic display.
 *
 * Returns a partial message update if the event produced displayable content,
 * or null if no UI update is needed.
 */
export function applyEvent(
    buf: MessageBuffer,
    event: SSEEvent,
): { id: string; content: string; final: boolean } | null {
    const conversationId = resolveEventConversationId(event);
    const turnId = resolveEventTurnId(event);
    const type = String(event?.type || '').trim().toLowerCase();

    if (type === 'turn_started') {
        setActiveTurn(buf, turnId);
        return null;
    }
    if (type === 'turn_completed' || type === 'turn_failed' || type === 'turn_canceled') {
        markTurnTerminal(buf, turnId, terminalStatusForType(type));
        buf.activeTurnId = null;
    }
    if (type === 'error') {
        buf.activeTurnId = null;
    }

    const key = resolveEventMessageId(event);
    if (!key) return null;

    switch (event.type) {
        case 'text_delta': {
            const existing = ensureMessageEntry(buf, key, event, conversationId, turnId);
            existing.content = (existing.content || '') + (event.content || '');
            storeEntry(buf, key, existing);
            setActiveTurn(buf, turnId);
            return { id: key, content: existing.content!, final: false };
        }

        case 'reasoning_delta': {
            const existing = ensureMessageEntry(buf, key, event, conversationId, turnId);
            existing.preamble = (existing.preamble || '') + (event.content || '');
            storeEntry(buf, key, existing);
            setActiveTurn(buf, turnId);
            return null;
        }

        case 'tool_call_started':
        case 'tool_call_delta':
        case 'tool_call_waiting':
        case 'tool_call_completed': {
            setActiveTurn(buf, turnId);
            return null;
        }
        case 'tool_call_failed':
        case 'tool_call_canceled': {
            setActiveTurn(buf, turnId);
            return null;
        }

        case 'model_started': {
            const existing = ensureMessageEntry(buf, key, event, conversationId, turnId);
            existing.status = String(event.status || existing.status || 'running');
            storeEntry(buf, key, existing);
            setActiveTurn(buf, turnId);
            return null;
        }

        case 'model_completed': {
            const existing = ensureMessageEntry(buf, key, event, conversationId, turnId);
            existing.status = String(event.status || existing.status || 'completed');
            storeEntry(buf, key, existing);
            setActiveTurn(buf, turnId);
            return null;
        }

        case 'assistant_preamble': {
            const existing = ensureMessageEntry(buf, key, event, conversationId, turnId);
            existing.preamble = String(event.content || event.preamble || existing.preamble || '');
            existing.status = String(event.status || existing.status || 'running');
            storeEntry(buf, key, existing);
            setActiveTurn(buf, turnId);
            return null;
        }

        case 'assistant_final': {
            const existing = ensureMessageEntry(buf, key, event, conversationId, turnId);
            existing.content = String(event.content || existing.content || '');
            existing.preamble = String(event.preamble || existing.preamble || '');
            existing.status = String(event.status || existing.status || 'completed');
            existing.interim = 0;
            storeEntry(buf, key, existing);
            setActiveTurn(buf, turnId);
            return { id: key, content: String(existing.content || ''), final: true };
        }

        case 'elicitation_requested':
        case 'elicitation_resolved':
        case 'linked_conversation_attached': {
            setActiveTurn(buf, turnId);
            return null;
        }

        case 'usage':
        case 'item_completed': {
            return null;
        }

        case 'control': {
            if (event.op !== 'message_patch') return null;
            const existing = ensureMessageEntry(buf, key, event, conversationId, turnId);
            applyMessagePatch(existing, event.patch || {});
            storeEntry(buf, key, existing);
            return null;
        }

        case 'turn_completed':
        case 'turn_failed':
        case 'turn_canceled': {
            const existing = buf.byId.get(key);
            if (existing) {
                existing.interim = 0;
                existing.status = terminalStatusForType(event.type);
            }
            return existing
                ? { id: key, content: existing.content || '', final: true }
                : null;
        }

        case 'error': {
            return null;
        }

        default:
            return null;
    }
}

// ─── Reconcile with server transcript ──────────────────────────────────────────

/**
 * Merges buffered streaming content with authoritative messages from the server.
 *
 * Server messages take precedence. Buffered messages that don't yet exist on the
 * server are appended (optimistic display). Once the server catches up, the buffer
 * entries are replaced by the authoritative version.
 */
export function reconcileMessages(
    buf: MessageBuffer,
    serverMessages: Message[],
): Message[] {
    const merged = new Map<string, Message>();

    // Server messages are authoritative
    for (const msg of serverMessages) {
        merged.set(msg.id, msg);
    }

    // Append buffered messages not yet on server (optimistic)
    for (const [id, partial] of buf.byId) {
        if (!merged.has(id)) {
            merged.set(id, partial as Message);
        }
    }

    return Array.from(merged.values()).sort(compareTemporalEntries);
}

/**
 * Updates the buffer from a full transcript response.
 * Used to sync buffer IDs with actual server message IDs after polling.
 */
export function reconcileFromTranscript(
    buf: MessageBuffer,
    turns: Turn[],
): void {
    for (const turn of turns as any[]) {
        const conversationId = String(turn?.conversationId || '').trim();
        const turnId = String(turn?.turnId || turn?.id || '').trim();
        const canonicalUser = turn?.user;
        if (canonicalUser?.messageId) {
            buf.byId.set(String(canonicalUser.messageId).trim(), {
                id: String(canonicalUser.messageId).trim(),
                conversationId,
                turnId,
                role: 'user',
                type: 'text',
                content: String(canonicalUser?.content || '').trim(),
                interim: 0,
                createdAt: String(turn?.createdAt || '').trim(),
                status: String(turn?.status || '').trim(),
            } as Partial<Message>);
        }
        const canonicalAssistant = turn?.assistant?.final;
        if (canonicalAssistant?.messageId) {
            buf.byId.set(String(canonicalAssistant.messageId).trim(), {
                id: String(canonicalAssistant.messageId).trim(),
                conversationId,
                turnId,
                role: 'assistant',
                type: 'text',
                content: String(canonicalAssistant?.content || '').trim(),
                interim: 0,
                createdAt: String(turn?.createdAt || '').trim(),
                status: String(turn?.status || '').trim(),
            } as Partial<Message>);
        }
        const pages = Array.isArray(turn?.execution?.pages) ? turn.execution.pages : [];
        for (const page of pages) {
            const pageMessageId = String(page?.assistantMessageId || page?.pageId || '').trim();
            if (!pageMessageId) continue;
            buf.byId.set(pageMessageId, {
                id: pageMessageId,
                conversationId,
                turnId,
                role: 'assistant',
                type: 'text',
                content: String(page?.content || '').trim(),
                preamble: String(page?.preamble || '').trim(),
                interim: Boolean(page?.finalResponse) ? 0 : 1,
                createdAt: String(page?.createdAt || turn?.createdAt || '').trim(),
                status: String(page?.status || turn?.status || '').trim(),
            } as Partial<Message>);
        }
        for (const m of turn.message || []) {
            if (!m?.id) continue;
            const role = (m.role || '').toLowerCase();
            if ((role === 'assistant' && m.content) || role === 'user') {
                buf.byId.set(m.id, m);
            }
        }
    }
}
