import type { SSEEvent } from './types';
import { firstPositiveNumber, firstString } from './ordering';

export function eventSequenceValue(event: SSEEvent = {} as SSEEvent, fallback = 1): number {
    return firstPositiveNumber((event as any)?.pageIndex, (event as any)?.iteration, (event as any)?.eventSeq, fallback);
}

export function eventIterationValue(event: SSEEvent = {} as SSEEvent, fallback = 0): number {
    return firstPositiveNumber((event as any)?.iteration, (event as any)?.pageIndex, fallback);
}

export function terminalStatusForType(type = ''): string {
    const normalized = String(type || '').trim().toLowerCase();
    if (normalized === 'turn_failed') return 'failed';
    if (normalized === 'turn_canceled') return 'canceled';
    return 'completed';
}

export function modelStepStatusForEvent(event: SSEEvent, existingStatus = '', fallbackStatus = 'running') {
    const explicitStatus = firstString(event?.status);
    if (explicitStatus) return explicitStatus;
    const type = firstString(event?.type).toLowerCase();
    if (type === 'text_delta') return 'streaming';
    return firstString(fallbackStatus, existingStatus, 'running');
}

export function executionGroupStatusForEvent(event: SSEEvent, existingStatus = '', fallbackStatus = 'running') {
    const explicitStatus = firstString(event?.status);
    if (explicitStatus) return explicitStatus;
    const type = firstString(event?.type).toLowerCase();
    if (type === 'text_delta') {
        const normalized = firstString(existingStatus).toLowerCase();
        if (['completed', 'done', 'success', 'succeeded', 'failed', 'error', 'canceled', 'cancelled', 'terminated'].includes(normalized)) {
            return existingStatus;
        }
        return 'streaming';
    }
    return firstString(fallbackStatus, existingStatus, 'running');
}
