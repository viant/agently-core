import { describe, expect, it } from 'vitest';

import { shouldShowNarrationBubble } from '../narrationBubble';

describe('narrationBubble', () => {
    it('shows a narration bubble for active execution groups when live narration exists', () => {
        expect(shouldShowNarrationBubble(
            [{ finalResponse: false, narrationContent: 'Checking the latest child-agent blocker summary now.' }],
            'Checking the latest child-agent blocker summary now.',
            '',
        )).toBe(true);
    });

    it('does not show a narration bubble when no visible text exists', () => {
        expect(shouldShowNarrationBubble(
            [{ finalResponse: false, narrationContent: 'Checking the latest child-agent blocker summary now.' }],
            '',
            '',
        )).toBe(false);
    });
});
