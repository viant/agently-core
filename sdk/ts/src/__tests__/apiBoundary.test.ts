import { describe, expect, it } from 'vitest';

import * as internalApi from '../internal';
import * as publicApi from '../index';
import type { ClientPlannerState, FetchDatasourceInput, LookupRegistryEntry, MetadataTargetContext } from '../index';

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

    it('exports metadata target context from the public root barrel', () => {
        const accept = (_value: MetadataTargetContext) => true;
        expect(accept({ platform: 'ios', formFactor: 'tablet', surface: 'app', capabilities: ['chart'] })).toBe(true);
    });

    it('exports datasource and lookup wire types from the public root barrel', () => {
        const acceptFetch = (_value: FetchDatasourceInput) => true;
        const acceptLookup = (_value: LookupRegistryEntry) => true;
        expect(acceptFetch({ id: 'account_lookup', conversationId: 'conv-1', inputs: { q: 'acme' } })).toBe(true);
        expect(acceptLookup({ name: 'account', dataSource: 'account_lookup' })).toBe(true);
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
