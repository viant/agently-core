import { describe, expect, it } from 'vitest';

import { compareExecutionGroups, compareTemporalEntries, temporalSequenceValue, temporalTimeValue } from '../ordering';

describe('ordering', () => {
    it('reads temporal sequence from sequence, eventSeq, or iteration', () => {
        expect(temporalSequenceValue({ sequence: 3 })).toBe(3);
        expect(temporalSequenceValue({ eventSeq: 4 })).toBe(4);
        expect(temporalSequenceValue({ iteration: 5 })).toBe(5);
    });

    it('sorts temporal entries by createdAt then sequence', () => {
        const rows = [
            { id: 'b', createdAt: '2026-01-01T00:00:00Z', sequence: 2 },
            { id: 'a', createdAt: '2026-01-01T00:00:00Z', sequence: 1 },
        ];
        rows.sort(compareTemporalEntries);
        expect(rows.map((row) => row.id)).toEqual(['a', 'b']);
    });

    it('falls back cleanly when createdAt is invalid or missing', () => {
        expect(temporalTimeValue({ createdAt: 'not-a-date' })).toBe(0);
        expect(temporalTimeValue({})).toBe(0);
        const rows = [
            { id: 'b', createdAt: 'not-a-date', sequence: 2 },
            { id: 'a', sequence: 1 },
        ];
        rows.sort(compareTemporalEntries);
        expect(rows.map((row) => row.id)).toEqual(['a', 'b']);
    });

    it('keeps user messages ahead of assistant rows within the same turn even if timestamps drift later', () => {
        const rows = [
            { id: 'assistant-1', role: 'assistant', turnId: 'turn-1', createdAt: '2026-01-01T00:00:05Z' },
            { id: 'user-1', role: 'user', turnId: 'turn-1', createdAt: '2026-01-01T00:00:06Z' },
        ];
        rows.sort(compareTemporalEntries);
        expect(rows.map((row) => row.id)).toEqual(['user-1', 'assistant-1']);
    });

    it('sorts execution groups by sequence then assistant id', () => {
        const groups = [
            { assistantMessageId: 'b', sequence: 2 },
            { assistantMessageId: 'a', sequence: 1 },
        ];
        groups.sort(compareExecutionGroups);
        expect(groups.map((group) => group.assistantMessageId)).toEqual(['a', 'b']);
    });
});
