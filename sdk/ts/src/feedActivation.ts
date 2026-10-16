/** Shared snapshot/lifecycle semantics used by both the coordinator and shell. */
export interface FeedActivationState { feedId?: string; active?: boolean | null; activationKnown?: boolean; }
function hasActivation(feed: FeedActivationState | undefined) {
    return typeof feed?.activationKnown === 'boolean' || typeof feed?.active === 'boolean' || feed?.active === null;
}
export function feedActivationEventType(feed: FeedActivationState = {}): 'tool_feed_unknown' | 'tool_feed_inactive' | 'tool_feed_active' {
    if (feed.activationKnown === false || feed.active === null
        || (feed.activationKnown === true && typeof feed.active !== 'boolean')) return 'tool_feed_unknown';
    return feed.active === false ? 'tool_feed_inactive' : 'tool_feed_active';
}

// Legacy content is not a new lifecycle decision. Preserve prior markers,
// including omitted markers, without inventing active content.
export function mergeFeedSnapshotActivation<T extends FeedActivationState>(incoming: readonly T[] = [], previous: readonly T[] = []): T[] {
    const key = (feed: T) => String(feed?.feedId || '').trim();
    const known = new Map((Array.isArray(previous) ? previous : []).map(feed => [key(feed), feed]));
    const rows = Array.isArray(incoming) ? incoming : [];
    const present = new Set(rows.map(key));
    const result = rows.map(feed => {
        if (!feed) return feed;
        const prior = known.get(key(feed));
        if (hasActivation(feed) || !hasActivation(prior)) return feed;
        return { ...feed, active: prior!.active, activationKnown: prior!.activationKnown };
    });
    for (const [id, prior] of known) {
        if (id && !present.has(id) && hasActivation(prior)) {
            result.push({ feedId: id, active: prior.active, activationKnown: prior.activationKnown } as T);
        }
    }
    return result;
}
