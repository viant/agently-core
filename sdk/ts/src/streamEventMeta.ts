import type { SSEEvent } from './types';
import { firstPositiveNumber, firstString } from './ordering';

export interface ConversationLifecyclePatch {
    running: boolean;
    stage: string;
    status: string;
}

export function eventSequenceValue(event?: SSEEvent, fallback = 1): number {
    return firstPositiveNumber(event?.pageIndex, event?.iteration, event?.eventSeq, fallback);
}

export function eventIterationValue(event?: SSEEvent, fallback = 0): number {
    return firstPositiveNumber(event?.iteration, event?.pageIndex, fallback);
}

export function terminalStatusForType(type = ''): string {
    const normalized = String(type || '').trim().toLowerCase();
    if (normalized === 'turn_failed') return 'failed';
    if (normalized === 'turn_canceled') return 'canceled';
    return 'completed';
}

export function conversationStatusForStreamEvent(type = '', status = '', fallback = 'running'): string {
    const explicit = String(status || '').trim().toLowerCase();
    if (explicit === 'succeeded' || explicit === 'success' || explicit === 'completed' || explicit === 'done') {
        return 'succeeded';
    }
    if (explicit === 'failed' || explicit === 'error') return 'failed';
    if (explicit === 'canceled' || explicit === 'cancelled') return 'canceled';
    if (explicit) return explicit;
    const normalizedType = String(type || '').trim().toLowerCase();
    if (normalizedType === 'turn_failed') return 'failed';
    if (normalizedType === 'turn_canceled') return 'canceled';
    if (normalizedType === 'turn_completed') return 'succeeded';
    return String(fallback || 'running').trim() || 'running';
}

export function conversationStageForStreamEvent(type = '', fallback = 'executing'): string {
    const normalized = String(type || '').trim().toLowerCase();
    if (normalized === 'turn_failed') return 'error';
    if (normalized === 'turn_canceled') return 'canceled';
    if (normalized === 'turn_completed') return 'done';
    return String(fallback || 'executing').trim() || 'executing';
}

export function conversationLifecyclePatchForStreamPhase(
    phase = '',
    payload: Partial<SSEEvent> | Record<string, unknown> = {}
): ConversationLifecyclePatch | null {
    const normalizedPhase = String(phase || '').trim().toLowerCase();
    if (normalizedPhase === 'thinking') {
        return {
            running: true,
            stage: 'thinking',
            status: conversationStatusForStreamEvent(String(payload?.type || ''), String(payload?.status || ''), 'running')
        };
    }
    if (normalizedPhase === 'executing') {
        return {
            running: true,
            stage: 'executing',
            status: conversationStatusForStreamEvent(String(payload?.type || ''), String(payload?.status || ''), 'running')
        };
    }
    if (normalizedPhase === 'eliciting') {
        return {
            running: true,
            stage: 'eliciting',
            status: conversationStatusForStreamEvent(String(payload?.type || ''), String(payload?.status || ''), 'pending')
        };
    }
    if (normalizedPhase === 'terminal') {
        return {
            running: false,
            stage: conversationStageForStreamEvent(String(payload?.type || ''), 'done'),
            status: conversationStatusForStreamEvent(String(payload?.type || ''), String(payload?.status || ''), 'succeeded')
        };
    }
    return null;
}

export function modelStepStatusForEvent(event: SSEEvent, existingStatus = '', fallbackStatus = 'running') {
    const explicitStatus = firstString(event?.status);
    if (explicitStatus) return explicitStatus;
    const type = firstString(event?.type).toLowerCase();
    if (type === 'model_completed') return 'completed';
    if (type === 'text_delta') return 'streaming';
    return firstString(fallbackStatus, existingStatus, 'running');
}

export function executionGroupStatusForEvent(event: SSEEvent, existingStatus = '', fallbackStatus = 'running') {
    const explicitStatus = firstString(event?.status);
    if (explicitStatus) return explicitStatus;
    const type = firstString(event?.type).toLowerCase();
    if (type === 'model_completed') return 'completed';
    if (type === 'text_delta') {
        const normalized = firstString(existingStatus).toLowerCase();
        if (['completed', 'done', 'success', 'succeeded', 'failed', 'error', 'canceled', 'cancelled', 'terminated'].includes(normalized)) {
            return existingStatus;
        }
        return 'streaming';
    }
    return firstString(fallbackStatus, existingStatus, 'running');
}
