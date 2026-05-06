import { describe, expect, it } from 'vitest';

import * as internalApi from '../internal';
import * as publicApi from '../index';
import type { ClientPlannerState } from '../index';

const INTERNAL_ONLY_EXPORTS = [
    'ConversationStreamTracker',
    'projectLiveAssistantRows',
    'overlayLiveAssistantTransientState',
    'filterExplicitLiveRowsAgainstTracker',
    'buildEffectiveLiveAssistantRows',
    'buildEffectiveLiveRows',
    'selectLiveAssistantRowsForTurn',
    'latestLiveAssistantRowForTurn',
    'latestLiveAssistantRowForTurnWithTransientState',
    'hasLiveAssistantRowForTurn',
    'latestEffectiveLiveAssistantRow',
    'eventSequenceValue',
    'eventIterationValue',
    'terminalStatusForType',
    'modelStepStatusForEvent',
    'executionGroupStatusForEvent',
] as const;

describe('api boundary', () => {
    it('exports planner client state types from the public root barrel', () => {
        const accept = (_value: ClientPlannerState | null | undefined) => true;
        expect(accept(undefined)).toBe(true);
    });

    it('keeps internal stream tracker and event helpers out of the public root barrel', () => {
        for (const key of INTERNAL_ONLY_EXPORTS) {
            expect(key in publicApi).toBe(false);
        }
    });

    it('keeps internal stream tracker and event helpers available from the internal barrel', () => {
        for (const key of INTERNAL_ONLY_EXPORTS) {
            expect(key in internalApi).toBe(true);
        }
    });
});
