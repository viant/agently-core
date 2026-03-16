/**
 * reconcile.ts — SSE message buffering and transcript reconciliation.
 *
 * During streaming, chunks arrive before the full message is persisted.
 * The MessageBuffer accumulates streaming content optimistically, and
 * reconcileFromTranscript merges it with the authoritative server state.
 */

import type { SSEEvent, Message, Turn } from './types';

// ─── Buffer ────────────────────────────────────────────────────────────────────

export interface MessageBuffer {
    /** Accumulated text keyed by message ID (or stream ID as fallback). */
    byId: Map<string, Partial<Message>>;
    /** Currently active turn/stream ID (null when idle). */
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
    const key = event.id || event.streamId || '';
    if (!key) return null;

    const ensureEntry = (): Partial<Message> => {
        return buf.byId.get(key) || {
            id: key,
            role: 'assistant',
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
            buf.activeTurnId = event.streamId || null;
            return { id: key, content: existing.content!, final: false };
        }

        case 'reasoning_delta': {
            const existing = ensureEntry();
            existing.preamble = (existing.preamble || '') + (event.content || '');
            buf.byId.set(key, existing);
            buf.activeTurnId = event.streamId || null;
            return null;
        }

        case 'tool_call_started':
        case 'tool_call_delta':
        case 'tool_call_completed': {
            buf.activeTurnId = event.streamId || null;
            return null;
        }

        case 'model_started':
        case 'model_completed':
        case 'assistant_preamble':
        case 'assistant_final':
        case 'elicitation_requested':
        case 'elicitation_resolved':
        case 'linked_conversation_attached': {
            buf.activeTurnId = event.streamId || null;
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
