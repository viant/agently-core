/**
 * reconcile.ts — SSE message buffering and transcript reconciliation.
 *
 * During streaming, chunks arrive before the full message is persisted.
 * The MessageBuffer accumulates streaming content optimistically, and
 * reconcileFromTranscript merges it with the authoritative server state.
 */

import type { SSEEvent, Message, Turn } from './types';
import { resolveEventConversationId, resolveEventMessageId, resolveEventTurnId } from './streamIdentity';

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
    const key = resolveEventMessageId(event);
    const conversationId = resolveEventConversationId(event);
    const turnId = resolveEventTurnId(event);
    if (!key) return null;

    const ensureEntry = (): Partial<Message> => {
        const existing = buf.byId.get(key);
        if (existing) {
            if (turnId && !existing.turnId) existing.turnId = turnId;
            if (conversationId && !existing.conversationId) existing.conversationId = conversationId;
            if (event.createdAt && !existing.createdAt) existing.createdAt = event.createdAt;
            return existing;
        }
        return {
            id: key,
            conversationId,
            turnId,
            role: 'assistant',
            type: 'text',
            content: '',
            interim: 1,
            createdAt: event.createdAt || new Date().toISOString(),
        } as Partial<Message>;
    };

    switch (event.type) {
        case 'text_delta': {
            const existing = ensureEntry();
            existing.content = (existing.content || '') + (event.content || '');
            buf.byId.set(key, existing);
            buf.activeTurnId = turnId || buf.activeTurnId;
            return { id: key, content: existing.content!, final: false };
        }

        case 'reasoning_delta': {
            const existing = ensureEntry();
            existing.preamble = (existing.preamble || '') + (event.content || '');
            buf.byId.set(key, existing);
            buf.activeTurnId = turnId || buf.activeTurnId;
            return null;
        }

        case 'tool_call_started':
        case 'tool_call_delta':
        case 'tool_call_completed': {
            buf.activeTurnId = turnId || buf.activeTurnId;
            return null;
        }

        case 'model_started': {
            const existing = ensureEntry();
            existing.status = String(event.status || existing.status || 'running');
            buf.byId.set(key, existing);
            buf.activeTurnId = turnId || buf.activeTurnId;
            return null;
        }

        case 'model_completed': {
            const existing = ensureEntry();
            existing.status = String(event.status || existing.status || 'completed');
            buf.byId.set(key, existing);
            buf.activeTurnId = turnId || buf.activeTurnId;
            return null;
        }

        case 'assistant_preamble': {
            const existing = ensureEntry();
            existing.preamble = String(event.content || event.preamble || existing.preamble || '');
            existing.status = String(event.status || existing.status || 'running');
            buf.byId.set(key, existing);
            buf.activeTurnId = turnId || buf.activeTurnId;
            return null;
        }

        case 'assistant_final': {
            const existing = ensureEntry();
            existing.content = String(event.content || existing.content || '');
            existing.preamble = String(event.preamble || existing.preamble || '');
            existing.status = String(event.status || existing.status || 'completed');
            existing.interim = 0;
            buf.byId.set(key, existing);
            buf.activeTurnId = turnId || buf.activeTurnId;
            return { id: key, content: String(existing.content || ''), final: true };
        }

        case 'elicitation_requested':
        case 'elicitation_resolved':
        case 'linked_conversation_attached': {
            buf.activeTurnId = turnId || buf.activeTurnId;
            return null;
        }

        case 'usage':
        case 'item_completed': {
            return null;
        }

        case 'control': {
            if (event.op !== 'message_patch') return null;
            const existing = ensureEntry();
            const patch = (event.patch || {}) as Record<string, any>;
            if (patch.linkedConversationId != null) {
                (existing as any).linkedConversationId = String(patch.linkedConversationId);
            }
            if (patch.status != null) {
                (existing as any).status = String(patch.status);
            }
            if (patch.toolName != null) {
                (existing as any).toolName = String(patch.toolName);
            }
            if (patch.preamble != null) {
                (existing as any).preamble = String(patch.preamble);
            }
            if (patch.interim != null) {
                const n = Number(patch.interim);
                if (Number.isFinite(n)) {
                    (existing as any).interim = n;
                }
            }
            if (patch.content != null) {
                (existing as any).content = String(patch.content);
            }
            buf.byId.set(key, existing);
            return null;
        }

        case 'turn_completed':
        case 'turn_failed':
        case 'turn_canceled': {
            const existing = buf.byId.get(key);
            if (existing) {
                existing.interim = 0;
                existing.status = event.type === 'turn_failed' ? 'failed'
                    : event.type === 'turn_canceled' ? 'canceled'
                    : 'completed';
            }
            buf.activeTurnId = null;
            return existing
                ? { id: key, content: existing.content || '', final: true }
                : null;
        }

        case 'error': {
            buf.activeTurnId = null;
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

    // Sort by createdAt
    return Array.from(merged.values()).sort(
        (a, b) => new Date(a.createdAt).getTime() - new Date(b.createdAt).getTime(),
    );
}

/**
 * Updates the buffer from a full transcript response.
 * Used to sync buffer IDs with actual server message IDs after polling.
 */
export function reconcileFromTranscript(
    buf: MessageBuffer,
    turns: Turn[],
): void {
    for (const turn of turns) {
        for (const m of turn.message || []) {
            if (!m?.id) continue;
            const role = (m.role || '').toLowerCase();
            if (role === 'assistant' && m.content) {
                buf.byId.set(m.id, m);
            }
        }
    }
}
