import { describe, expect, it } from 'vitest';

import {
    executionGroupStatusForEvent,
    eventIterationValue,
    eventSequenceValue,
    modelStepStatusForEvent,
    terminalStatusForType,
} from '../streamEventMeta';

describe('streamEventMeta', () => {
    it('derives sequence and iteration from streaming page metadata', () => {
        expect(eventSequenceValue({ pageIndex: 4 } as any, 1)).toBe(4);
        expect(eventSequenceValue({ iteration: 5 } as any, 1)).toBe(5);
        expect(eventIterationValue({ iteration: 6 } as any, 0)).toBe(6);
        expect(eventIterationValue({ pageIndex: 7 } as any, 0)).toBe(7);
    });

    it('maps terminal stream types to canonical status values', () => {
        expect(terminalStatusForType('turn_completed')).toBe('completed');
        expect(terminalStatusForType('turn_failed')).toBe('failed');
        expect(terminalStatusForType('turn_canceled')).toBe('canceled');
    });

    it('marks text deltas as streaming when there is no explicit status', () => {
        expect(modelStepStatusForEvent({ type: 'text_delta' } as any, 'thinking', 'running')).toBe('streaming');
        expect(modelStepStatusForEvent({ type: 'model_started', status: 'thinking' } as any, '', 'running')).toBe('thinking');
        expect(modelStepStatusForEvent({ type: 'assistant_preamble' } as any, 'running', 'running')).toBe('running');
        expect(executionGroupStatusForEvent({ type: 'text_delta' } as any, 'thinking', 'running')).toBe('streaming');
        expect(executionGroupStatusForEvent({ type: 'text_delta' } as any, 'completed', 'running')).toBe('completed');
    });
});
