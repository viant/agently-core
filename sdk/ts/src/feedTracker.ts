/**
 * FeedTracker — manages active tool feed state for UI rendering.
 *
 * When a tool_feed_active SSE event arrives, the feed is added/updated.
 * When tool_feed_inactive arrives, the feed is removed.
 * UI components subscribe via onChange() and render the feed indicator bar.
 */

import type { ActiveFeed, SSEEvent } from './types';
import { resolveEventConversationId, resolveEventTurnId } from './streamIdentity';

export type FeedListener = (feeds: ActiveFeed[]) => void;

export class FeedTracker {
    private _feeds: Map<string, ActiveFeed> = new Map();
    private _listeners: FeedListener[] = [];

    /** Get all currently active feeds. */
    get feeds(): ActiveFeed[] {
        return Array.from(this._feeds.values());
    }

    /** Get a specific feed by ID. */
    get(feedId: string): ActiveFeed | undefined {
        return this._feeds.get(feedId);
    }

    /** Set or update an active feed. */
    setActive(feed: ActiveFeed): void {
        this._feeds.set(feed.feedId, { ...feed, updatedAt: Date.now() });
        this.notify();
    }

    /** Remove an active feed (mark inactive). */
    setInactive(feedId: string): void {
        if (this._feeds.delete(feedId)) {
            this.notify();
        }
    }

    /** Clear all active feeds. */
    clear(): void {
        if (this._feeds.size > 0) {
            this._feeds.clear();
            this.notify();
        }
    }

    /** Subscribe to changes. Returns an unsubscribe function. */
    onChange(fn: FeedListener): () => void {
        this._listeners.push(fn);
        return () => {
            this._listeners = this._listeners.filter((l) => l !== fn);
        };
    }

    /**
     * Apply an SSE event. Call this from your streamEvents onEvent handler.
     * Automatically sets/clears feeds based on event type.
     */
    applyEvent(event: SSEEvent): void {
        if (event.type === 'tool_feed_active' && event.feedId) {
            this.setActive({
                feedId: event.feedId,
                title: event.feedTitle || event.feedId,
                itemCount: event.feedItemCount || 0,
                conversationId: resolveEventConversationId(event),
                turnId: resolveEventTurnId(event),
                updatedAt: Date.now(),
            });
        } else if (event.type === 'tool_feed_inactive' && event.feedId) {
            this.setInactive(event.feedId);
        }
    }

    private notify(): void {
        const snapshot = this.feeds;
        for (const fn of this._listeners) {
            try { fn(snapshot); } catch { /* ignore */ }
        }
    }
}
