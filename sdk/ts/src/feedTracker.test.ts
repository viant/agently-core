import { describe, expect, it } from 'vitest';

import { FeedTracker } from './feedTracker';

describe('FeedTracker presentation targets', () => {
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
