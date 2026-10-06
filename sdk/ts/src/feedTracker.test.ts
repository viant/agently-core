import { describe, expect, it } from 'vitest';

import { FeedTracker } from './feedTracker';

describe('FeedTracker presentation targets', () => {
    it('clears an activity claim without discarding content or activating unknown historical data', () => {
        const tracker = new FeedTracker();
        tracker.applyEvent({ type: 'tool_feed_unknown', feedId: 'historical' });
        expect(tracker.feeds).toEqual([]);
        tracker.applyEvent({ type: 'tool_feed_active', feedId: 'feed', feedTitle: 'Retained' });
        const data = { rows: [1] };
        tracker.setActive({ ...tracker.get('feed')!, data });
        tracker.applyEvent({ type: 'tool_feed_unknown', feedId: 'feed' });
        expect(tracker.get('feed')).toMatchObject({ active: null, activationKnown: false, title: 'Retained', data });
        expect(tracker.get('feed')?.data).toBe(data);
        tracker.applyEvent({ type: 'tool_feed_active', feedId: 'feed' });
        expect(tracker.get('feed')).toMatchObject({ active: true, activationKnown: true });
        tracker.applyEvent({ type: 'tool_feed_inactive', feedId: 'feed' });
        expect(tracker.feeds).toEqual([]);
    });
    it('preserves an explicit target from a live feed event', () => {
        const tracker = new FeedTracker();
        tracker.applyEvent({
            type: 'tool_feed_active',
            feedId: 'media-plan',
            feedTitle: 'Media plan',
            feedIcon: 'chart',
            feedAccent: 'red',
            feedTarget: 'workspace',
            conversationId: 'conv-1',
        });

        expect(tracker.feeds[0]?.presentation).toEqual({
            icon: 'chart',
            accent: 'red',
            target: 'workspace',
        });
    });
});
