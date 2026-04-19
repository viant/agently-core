import { describe, it, expect } from 'vitest';

import {
    isLiveLifecycle,
    isTerminalLifecycle,
    statusToLifecycle,
} from '../chatStore/lifecycle';

describe('chatStore/lifecycle — statusToLifecycle', () => {
    it('maps backend status to client lifecycle per the §8 table', () => {
        expect(statusToLifecycle('queued')).toBe('pending');
        expect(statusToLifecycle('running')).toBe('running');
        expect(statusToLifecycle('waiting_for_user')).toBe('running');
        expect(statusToLifecycle('completed')).toBe('completed');
        expect(statusToLifecycle('failed')).toBe('failed');
        expect(statusToLifecycle('canceled')).toBe('cancelled');
    });

    it('maps unknown / future status to running (safest default during rollout)', () => {
        expect(statusToLifecycle(undefined)).toBe('running');
        expect(statusToLifecycle('future_unknown_status' as unknown as 'running')).toBe('running');
    });
});

describe('chatStore/lifecycle — isTerminalLifecycle / isLiveLifecycle', () => {
    it('terminal set is exactly {completed, failed, cancelled}', () => {
        expect(isTerminalLifecycle('completed')).toBe(true);
        expect(isTerminalLifecycle('failed')).toBe(true);
        expect(isTerminalLifecycle('cancelled')).toBe(true);
        expect(isTerminalLifecycle('pending')).toBe(false);
        expect(isTerminalLifecycle('running')).toBe(false);
    });

    it('live set is exactly {pending, running} and is disjoint from terminal', () => {
        expect(isLiveLifecycle('pending')).toBe(true);
        expect(isLiveLifecycle('running')).toBe(true);
        expect(isLiveLifecycle('completed')).toBe(false);
        expect(isLiveLifecycle('failed')).toBe(false);
        expect(isLiveLifecycle('cancelled')).toBe(false);
    });
});
