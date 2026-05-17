import { describe, expect, it } from 'vitest';

import {
    conversationLifecyclePatchForStreamPhase,
    conversationStageForStreamEvent,
    conversationStatusForStreamEvent,
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
        expect(conversationStatusForStreamEvent('turn_completed', 'succeeded')).toBe('succeeded');
        expect(conversationStatusForStreamEvent('turn_failed', '')).toBe('failed');
        expect(conversationStatusForStreamEvent('turn_canceled', '')).toBe('canceled');
        expect(conversationStageForStreamEvent('turn_completed')).toBe('done');
        expect(conversationStageForStreamEvent('turn_failed')).toBe('error');
        expect(conversationStageForStreamEvent('turn_canceled')).toBe('canceled');
    });

    it('builds normalized conversation lifecycle patches for stream phases', () => {
        expect(conversationLifecyclePatchForStreamPhase('thinking', event({ status: 'running' }))).toEqual({
            running: true,
            stage: 'thinking',
            status: 'running'
        });
        expect(conversationLifecyclePatchForStreamPhase('eliciting', event({}))).toEqual({
            running: true,
            stage: 'eliciting',
            status: 'pending'
        });
        expect(conversationLifecyclePatchForStreamPhase('terminal', event({ type: 'turn_completed', status: 'succeeded' }))).toEqual({
            running: false,
            stage: 'done',
            status: 'succeeded'
        });
    });

    it('marks text deltas as streaming when there is no explicit status', () => {
        expect(modelStepStatusForEvent(event({ type: 'text_delta' }), 'thinking', 'running')).toBe('streaming');
        expect(modelStepStatusForEvent(event({ type: 'model_started', status: 'thinking' }), '', 'running')).toBe('thinking');
        expect(modelStepStatusForEvent(event({ type: 'narration' }), 'running', 'running')).toBe('running');
        expect(executionGroupStatusForEvent(event({ type: 'text_delta' }), 'thinking', 'running')).toBe('streaming');
        expect(executionGroupStatusForEvent(event({ type: 'text_delta' }), 'completed', 'running')).toBe('completed');
    });
});
