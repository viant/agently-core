/**
 * ElicitationTracker — manages pending elicitation state for UI rendering.
 *
 * When an elicitation_requested SSE event arrives, call `setPending()`.
 * UI components subscribe via `onChange()` and render a form/dialog.
 * When the user resolves, call `client.resolveElicitation()` then `clear()`.
 *
 * This is deliberately independent of the message/row pipeline so
 * elicitation forms can't be overwritten by transcript fetches.
 */

import type { JSONObject, SSEEvent } from './types';
import { resolveEventConversationId, resolveEventTurnId } from './streamIdentity';

export interface PendingElicitation {
    elicitationId: string;
    conversationId: string;
    turnId?: string;
    message?: string;
    requestedSchema?: JSONObject | null;
    callbackURL?: string;
    /** OOB URL to open in a new browser window. */
    url?: string;
    /** Elicitation mode: 'oob', 'webonly', or empty for schema form. */
    mode?: string;
}

export type ElicitationListener = (pending: PendingElicitation | null) => void;

interface ElicitationEnvelope {
    requestedSchema?: JSONObject;
    schema?: JSONObject;
    url?: string;
    mode?: string;
}

function isJSONObject(value: unknown): value is JSONObject {
    return !!value && typeof value === 'object' && !Array.isArray(value);
}

function resolveElicitationEnvelope(data: JSONObject | null | undefined): ElicitationEnvelope {
    return {
        requestedSchema: isJSONObject(data?.requestedSchema) ? data.requestedSchema : (isJSONObject(data?.schema) ? data.schema : undefined),
        schema: isJSONObject(data?.schema) ? data.schema : undefined,
        url: String(data?.url || '').trim(),
        mode: String(data?.mode || '').trim(),
    };
}

export class ElicitationTracker {
    private _pending: PendingElicitation | null = null;
    private _listeners: ElicitationListener[] = [];

    /** Set the currently pending elicitation (from an SSE event). */
    setPending(elicitation: PendingElicitation | null): void {
        this._pending = elicitation ?? null;
        this.notify();
    }

    /** Get the currently pending elicitation. */
    get pending(): PendingElicitation | null {
        return this._pending;
    }

    /** Clear the pending elicitation (after resolve/cancel). */
    clear(): void {
        this._pending = null;
        this.notify();
    }

    /** Subscribe to changes. Returns an unsubscribe function. */
    onChange(fn: ElicitationListener): () => void {
        this._listeners.push(fn);
        return () => {
            this._listeners = this._listeners.filter((l) => l !== fn);
        };
    }

    /**
     * Apply an SSE event. Call this from your streamEvents onEvent handler.
     * Automatically sets/clears pending state based on event type.
     */
    applyEvent(event: Pick<SSEEvent, 'type' | 'elicitationId' | 'conversationId' | 'streamId' | 'turnId' | 'content' | 'callbackUrl' | 'elicitationData'>): void {
        if (event.type === 'elicitation_requested' && event.elicitationId) {
            const data = event.elicitationData;
            const envelope = resolveElicitationEnvelope(data);
            const requestedSchema = envelope.requestedSchema || envelope.schema || data || null;
            this.setPending({
                elicitationId: event.elicitationId,
                conversationId: resolveEventConversationId(event),
                turnId: resolveEventTurnId(event),
                message: event.content || '',
                requestedSchema,
                callbackURL: event.callbackUrl || '',
                url: envelope.url,
                mode: envelope.mode,
            });
        } else if (event.type === 'elicitation_resolved') {
            this.clear();
        }
    }

    private notify(): void {
        const snapshot = this._pending;
        for (const fn of this._listeners) {
            try { fn(snapshot); } catch { /* ignore */ }
        }
    }
}
