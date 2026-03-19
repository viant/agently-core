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

export interface PendingElicitation {
    elicitationId: string;
    conversationId: string;
    turnId?: string;
    message?: string;
    requestedSchema?: Record<string, any> | null;
    callbackURL?: string;
    /** OOB URL to open in a new browser window. */
    url?: string;
    /** Elicitation mode: 'oob', 'webonly', or empty for schema form. */
    mode?: string;
}

export type ElicitationListener = (pending: PendingElicitation | null) => void;

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
    applyEvent(event: { type?: string; elicitationId?: string; conversationId?: string; streamId?: string; turnId?: string; content?: string; callbackUrl?: string; elicitationData?: Record<string, any> | null }): void {
        if (event.type === 'elicitation_requested' && event.elicitationId) {
            const data = event.elicitationData;
            const requestedSchema = data?.requestedSchema ?? data?.schema ?? data ?? null;
            this.setPending({
                elicitationId: event.elicitationId,
                conversationId: event.conversationId || event.streamId || '',
                turnId: event.turnId || '',
                message: event.content || '',
                requestedSchema,
                callbackURL: event.callbackUrl || '',
                url: (data as any)?.url || '',
                mode: (data as any)?.mode || '',
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
