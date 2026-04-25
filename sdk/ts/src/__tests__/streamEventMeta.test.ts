import { describe, expect, it } from 'vitest';

import {
    executionGroupStatusForEvent,
    eventIterationValue,
    eventSequenceValue,
    modelStepStatusForEvent,
    terminalStatusForType,
} from '../streamEventMeta';
import type { SSEEvent } from '../types';

function event(input: Partial<SSEEvent>): SSEEvent {
    return input as SSEEvent;
}

describe('streamEventMeta', () => {
    it('derives sequence and iteration from streaming page metadata', () => {
        expect(eventSequenceValue(event({ pageIndex: 4 }), 1)).toBe(4);
        expect(eventSequenceValue(event({ iteration: 5 }), 1)).toBe(5);
        expect(eventIterationValue(event({ iteration: 6 }), 0)).toBe(6);
        expect(eventIterationValue(event({ pageIndex: 7 }), 0)).toBe(7);
    });

    it('maps terminal stream types to canonical status values', () => {
        expect(terminalStatusForType('turn_completed')).toBe('completed');
        expect(terminalStatusForType('turn_failed')).toBe('failed');
        expect(terminalStatusForType('turn_canceled')).toBe('canceled');
    });

    it('marks text deltas as streaming when there is no explicit status', () => {
        expect(modelStepStatusForEvent(event({ type: 'text_delta' }), 'thinking', 'running')).toBe('streaming');
        expect(modelStepStatusForEvent(event({ type: 'model_started', status: 'thinking' }), '', 'running')).toBe('thinking');
        expect(modelStepStatusForEvent(event({ type: 'narration' }), 'running', 'running')).toBe('running');
        expect(executionGroupStatusForEvent(event({ type: 'text_delta' }), 'thinking', 'running')).toBe('streaming');
        expect(executionGroupStatusForEvent(event({ type: 'text_delta' }), 'completed', 'running')).toBe('completed');
    });
});
