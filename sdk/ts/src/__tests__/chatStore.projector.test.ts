import { describe, it, expect } from 'vitest';

import {
    applyEvent,
    applyLocalSubmit,
    newConversationState,
} from '../chatStore/reducer';
import {
    describeHeader,
    projectConversation,
    toneForLifecycle,
    type IterationRenderRow,
    type UserRenderRow,
} from '../chatStore/projector';
import type { ClientConversationState } from '../chatStore/types';
import type { SSEEvent } from '../types';

const CONV = 'conv_p';

function fresh(): ClientConversationState {
    return newConversationState(CONV);
}

function sse(partial: Partial<SSEEvent>): SSEEvent {
    return { type: 'turn_started', conversationId: CONV, ...partial } as SSEEvent;
}

// ─── describeHeader total function ─────────────────────────────────────────────

describe('chatStore/projector — describeHeader', () => {
    it('N >= 1 produces "Execution details (N)" with lifecycle tone', () => {
        expect(describeHeader('running', 1)).toEqual({ label: 'Execution details (1)', tone: 'running', count: 1 });
        expect(describeHeader('running', 3)).toEqual({ label: 'Execution details (3)', tone: 'running', count: 3 });
        expect(describeHeader('completed', 2)).toEqual({ label: 'Execution details (2)', tone: 'success', count: 2 });
    });

    it('N = 0 returns descriptive label per lifecycle, never "(0)"', () => {
        expect(describeHeader('pending', 0)).toEqual({ label: 'Starting turn…', tone: 'running', count: 0 });
        expect(describeHeader('running', 0)).toEqual({ label: 'Starting turn…', tone: 'running', count: 0 });
        expect(describeHeader('completed', 0)).toEqual({ label: 'Completed', tone: 'success', count: 0 });
        expect(describeHeader('failed', 0)).toEqual({ label: 'Failed', tone: 'danger', count: 0 });
        expect(describeHeader('cancelled', 0)).toEqual({ label: 'Cancelled', tone: 'neutral', count: 0 });
    });

    it('never produces a "(0)" literal in the label', () => {
        for (const lc of ['pending', 'running', 'completed', 'failed', 'cancelled'] as const) {
            expect(describeHeader(lc, 0).label).not.toMatch(/\(0\)/);
        }
    });

    it('toneForLifecycle mapping', () => {
        expect(toneForLifecycle('pending')).toBe('running');
        expect(toneForLifecycle('running')).toBe('running');
        expect(toneForLifecycle('completed')).toBe('success');
        expect(toneForLifecycle('failed')).toBe('danger');
        expect(toneForLifecycle('cancelled')).toBe('neutral');
    });
});

// ─── Projection shape ──────────────────────────────────────────────────────────

describe('chatStore/projector — projectConversation', () => {
    it('empty state → empty rows', () => {
        expect(projectConversation(fresh())).toEqual([]);
    });

    it('one pending turn → [user, iteration(no-content)]', () => {
        const state = applyLocalSubmit(fresh(), {
            conversationId: CONV,
            clientRequestId: 'crid_1',
            content: 'hi',
            createdAt: '2025-01-01T00:00:00Z',
        });
        const rows = projectConversation(state);
        expect(rows.map((r) => r.kind)).toEqual(['user', 'iteration']);
        const it = rows[1] as IterationRenderRow;
        expect(it.lifecycle).toBe('pending');
        expect(it.header.label).toBe('Starting turn…');
        expect(it.header.count).toBe(0);
        expect(it.rounds.length).toBe(0);
    });

    it('lifecycle-only turn_started → header "Starting turn…"; lifecycle entry visible inside the round; no (0)', () => {
        const state = applyEvent(fresh(), sse({
            type: 'turn_started',
            turnId: 'tn_A',
            createdAt: '2025-01-01T00:00:00Z',
        }));
        const rows = projectConversation(state);
        expect(rows.length).toBe(1);
        const it = rows[0] as IterationRenderRow;
        expect(it.header.label).toBe('Starting turn…');
        expect(it.header.count).toBe(0);
        expect(it.rounds.length).toBe(1);
        expect(it.rounds[0].lifecycleEntries.length).toBe(1);
        expect(it.rounds[0].lifecycleEntries[0].kind).toBe('turn_started');
        expect(it.rounds[0].hasContent).toBe(false);       // lifecycle doesn't count
    });

    it('pure model-started turn → header "Execution details (1)" with running tone', () => {
        let state = applyEvent(fresh(), sse({
            type: 'turn_started',
            turnId: 'tn_B',
            createdAt: '2025-01-01T00:00:00Z',
        }));
        state = applyEvent(state, sse({
            type: 'model_started',
            turnId: 'tn_B',
            pageId: 'pg_1',
            modelCallId: 'mc_1',
        } as SSEEvent));
        const rows = projectConversation(state);
        const it = rows[0] as IterationRenderRow;
        expect(it.header).toEqual({ label: 'Execution details (1)', tone: 'running', count: 1 });
        expect(it.rounds.length).toBe(1);
        expect(it.rounds[0].hasContent).toBe(true);
    });

    it('terminal lifecycle-only cancelled turn → header "Cancelled" with neutral tone', () => {
        let state = applyEvent(fresh(), sse({
            type: 'turn_started',
            turnId: 'tn_C',
            createdAt: '2025-01-01T00:00:00Z',
        }));
        state = applyEvent(state, sse({
            type: 'turn_canceled',
            turnId: 'tn_C',
            createdAt: '2025-01-01T00:00:01Z',
        } as SSEEvent));
        const rows = projectConversation(state);
        const it = rows[0] as IterationRenderRow;
        expect(it.header).toEqual({ label: 'Cancelled', tone: 'neutral', count: 0 });
        expect(it.isStreaming).toBe(false);
    });

    it('terminal failed turn → header "Failed" with danger tone', () => {
        let state = applyEvent(fresh(), sse({
            type: 'turn_started',
            turnId: 'tn_F',
            createdAt: '2025-01-01T00:00:00Z',
        }));
        state = applyEvent(state, sse({
            type: 'turn_failed',
            turnId: 'tn_F',
            createdAt: '2025-01-01T00:00:01Z',
        } as SSEEvent));
        const rows = projectConversation(state);
        const it = rows[0] as IterationRenderRow;
        expect(it.header).toEqual({ label: 'Failed', tone: 'danger', count: 0 });
    });

    it('lifecycle entries render as inline entries without counting toward (N)', () => {
        let state = applyEvent(fresh(), sse({
            type: 'turn_started',
            turnId: 'tn_L',
            createdAt: '2025-01-01T00:00:00Z',
        }));
        // add a real model step → round has content
        state = applyEvent(state, sse({
            type: 'model_started',
            turnId: 'tn_L',
            pageId: 'pg_1',
            modelCallId: 'mc_1',
        } as SSEEvent));
        const rows = projectConversation(state);
        const it = rows[0] as IterationRenderRow;
        // One round, counted.
        expect(it.header.count).toBe(1);
        // Lifecycle entry from turn_started remains visible on round 0.
        const allLifecycleKinds = it.rounds.flatMap((r) => r.lifecycleEntries.map((e) => e.kind));
        expect(allLifecycleKinds).toContain('turn_started');
    });

    it('keeps non-final tool-bearing rounds as main when phase is missing', () => {
        let state = applyEvent(fresh(), sse({
            type: 'turn_started',
            turnId: 'tn_sidecar',
            createdAt: '2025-01-01T00:00:00Z',
        }));
        state = applyEvent(state, sse({
            type: 'model_started',
            turnId: 'tn_sidecar',
            pageId: 'pg_sidecar',
            modelCallId: 'mc_sidecar',
            iteration: 1,
        } as SSEEvent));
        state = applyEvent(state, sse({
            type: 'tool_call_started',
            turnId: 'tn_sidecar',
            pageId: 'pg_sidecar',
            toolCallId: 'tool_sidecar',
            toolName: 'llm/agents/list',
            iteration: 1,
        } as SSEEvent));
        const rows = projectConversation(state);
        const it = rows[0] as IterationRenderRow;
        const round = it.rounds.find((r) => r.pageId === 'pg_sidecar');
        expect(round?.phase).toBe('main');
    });
});

// ─── Steering placement rule (§2.4) ────────────────────────────────────────────

describe('chatStore/projector — steering placement', () => {
    it('[u_first, iteration, u_rest…] at every projection tick', () => {
        let state = applyLocalSubmit(fresh(), {
            conversationId: CONV,
            clientRequestId: 'crid_1',
            content: 'initial',
            createdAt: '2025-01-01T00:00:00Z',
        });
        // First projection: [user, iteration]
        const r1 = projectConversation(state);
        expect(r1.map((r) => r.kind)).toEqual(['user', 'iteration']);

        // Steering submit during pending turn.
        state = applyLocalSubmit(state, {
            conversationId: CONV,
            clientRequestId: 'crid_2',
            content: 'follow-up',
            createdAt: '2025-01-01T00:00:10Z',
            mode: 'steer',
        });
        const r2 = projectConversation(state);
        expect(r2.map((r) => r.kind)).toEqual(['user', 'iteration', 'user']);

        // Second steering.
        state = applyLocalSubmit(state, {
            conversationId: CONV,
            clientRequestId: 'crid_3',
            content: 'again',
            createdAt: '2025-01-01T00:00:20Z',
            mode: 'steer',
        });
        const r3 = projectConversation(state);
        expect(r3.map((r) => r.kind)).toEqual(['user', 'iteration', 'user', 'user']);

        // IterationRow renderKey is stable across all three projections.
        const iter1 = r1.find((r) => r.kind === 'iteration')!.renderKey;
        const iter2 = r2.find((r) => r.kind === 'iteration')!.renderKey;
        const iter3 = r3.find((r) => r.kind === 'iteration')!.renderKey;
        expect(iter2).toBe(iter1);
        expect(iter3).toBe(iter1);

        // u_first renderKey is stable across all three projections.
        const ufirst1 = (r1.find((r) => r.kind === 'user') as UserRenderRow).renderKey;
        const ufirst2 = (r2.find((r) => r.kind === 'user') as UserRenderRow).renderKey;
        expect(ufirst2).toBe(ufirst1);

        // The single card persists; u_rest segments render after it.
        const contents = r3
            .filter((r) => r.kind === 'user')
            .map((r) => (r as UserRenderRow).content);
        expect(contents).toEqual(['initial', 'follow-up', 'again']);
    });
});
