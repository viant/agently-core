import { describe, it, expect } from 'vitest';

import {
    applyEvent,
    applyLocalSubmit,
    applyTranscript,
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
        expect(it.isStreaming).toBe(true);
    });

    it('transcript-owned running turn is not marked streaming for wall-clock UI updates', () => {
        const state = fresh();
        applyTranscript(state, {
            conversationId: CONV,
            turns: [{
                turnId: 'tn_tr_running',
                status: 'running',
                createdAt: '2026-04-18T19:00:00Z',
                user: {
                    messageId: 'msg_user_done',
                    content: 'check how many files are in my ~/Download folder',
                },
                execution: {
                    pages: [{
                        pageId: 'pg_tr_running',
                        assistantMessageId: 'msg_assistant_done',
                        status: 'running',
                        narration: 'Using prompt/get.',
                        modelSteps: [{
                            modelCallId: 'mc_done',
                            assistantMessageId: 'msg_assistant_done',
                            provider: 'openai',
                            model: 'gpt-5-mini',
                            status: 'running',
                        }]
                    }]
                }
            }],
        });
        const rows = projectConversation(state);
        const it = rows.find((row) => row.kind === 'iteration') as IterationRenderRow;
        expect(it.header).toEqual({ label: 'Execution details (1)', tone: 'running', count: 1 });
        expect(it.isStreaming).toBe(false);
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

    it('projects transcript standalone turn messages as separate chat rows ordered by sequence', () => {
        const state = fresh();
        applyTranscript(state, {
            conversationId: CONV,
            turns: [{
                turnId: 'tn_msg',
                status: 'completed',
                user: {
                    messageId: 'user-1',
                    content: 'Initial ask',
                    sequence: 1,
                    createdAt: '2026-04-21T00:00:00Z',
                },
                messages: [{
                    messageId: 'assistant-note-1',
                    role: 'assistant',
                    content: 'PRELIMINARY NOTE',
                    sequence: 8,
                    createdAt: '2026-04-21T00:00:08Z',
                }],
                execution: {
                    pages: [{
                        pageId: 'page-final',
                        assistantMessageId: 'page-final',
                        status: 'completed',
                        finalResponse: true,
                        content: 'Final answer',
                        sequence: 9,
                    }],
                },
            }],
        });

        const rows = projectConversation(state);
        expect(rows.map((row) => row.kind)).toEqual(['user', 'assistant', 'iteration']);
        expect(rows[1]).toMatchObject({
            kind: 'assistant',
            messageId: 'assistant-note-1',
            content: 'PRELIMINARY NOTE',
        });
    });

    it('does not project page-owned assistant narration or final messages as standalone rows', () => {
        const state = fresh();
        applyTranscript(state, {
            conversationId: CONV,
            turns: [{
                turnId: 'tn_owned',
                status: 'completed',
                user: {
                    messageId: 'user-1',
                    content: 'Initial ask',
                    sequence: 1,
                    createdAt: '2026-04-21T00:00:00Z',
                },
                messages: [{
                    messageId: 'msg-final',
                    role: 'assistant',
                    content: 'Final answer',
                    sequence: 9,
                    createdAt: '2026-04-21T00:00:09Z',
                    interim: 0,
                }, {
                    messageId: 'msg-narration',
                    role: 'assistant',
                    content: 'Thinking…',
                    sequence: 8,
                    createdAt: '2026-04-21T00:00:08Z',
                    interim: 1,
                }],
                assistant: {
                    narration: {
                        messageId: 'msg-narration',
                        content: 'Thinking…',
                    },
                    final: {
                        messageId: 'msg-final',
                        content: 'Final answer',
                    },
                },
                execution: {
                    pages: [{
                        pageId: 'page-final',
                        assistantMessageId: 'page-final',
                        finalAssistantMessageId: 'msg-final',
                        narrationMessageId: 'msg-narration',
                        status: 'completed',
                        finalResponse: true,
                        content: 'Final answer',
                        narration: 'Thinking…',
                        sequence: 9,
                    }],
                },
            }],
        });

        const rows = projectConversation(state);
        expect(rows.map((row) => row.kind)).toEqual(['user', 'iteration']);
    });

    it('keeps transcript extra user messages after the iteration when their sequence is later', () => {
        const state = fresh();
        applyTranscript(state, {
            conversationId: CONV,
            turns: [{
                turnId: 'tn_user_extra',
                status: 'completed',
                user: {
                    messageId: 'user-1',
                    content: 'Initial ask',
                    sequence: 1,
                    createdAt: '2026-04-21T00:00:00Z',
                },
                messages: [{
                    messageId: 'user-2',
                    role: 'user',
                    content: 'Steer: narrow the scope',
                    sequence: 10,
                    createdAt: '2026-04-21T00:00:10Z',
                }],
                execution: {
                    pages: [{
                        pageId: 'page-final',
                        assistantMessageId: 'page-final',
                        status: 'completed',
                        finalResponse: true,
                        content: 'Final answer',
                        sequence: 9,
                    }],
                },
            }],
        });

        const rows = projectConversation(state);
        expect(rows.map((row) => row.kind)).toEqual(['user', 'iteration', 'user']);
        expect(rows[2]).toMatchObject({
            kind: 'user',
            messageId: 'user-2',
            content: 'Steer: narrow the scope',
        });
    });

    it('anchors live iteration ordering to the latest page timestamp so a prior standalone note stays above a later final answer', () => {
        // Contract: an `assistant` event (explicit add, e.g. from the
        // message/add tool) produces a standalone bubble in
        // turn.messages. Page content arrives via `text_delta` stream
        // accumulation into page.content — simulated here with a
        // transcript merge so we don't have to play the full delta
        // sequence. The two coexist in the render: standalone
        // bubble first (by sequence / createdAt), iteration block
        // second.
        const state = fresh();
        applyEvent(state, sse({
            type: 'turn_started',
            turnId: 'tn_live_note',
            createdAt: '2026-04-21T00:00:00Z',
        }));
        applyEvent(state, sse({
            type: 'assistant',
            turnId: 'tn_live_note',
            messageId: 'msg_note_live',
            content: 'PRELIMINARY NOTE',
            createdAt: '2026-04-21T00:00:08Z',
            patch: {
                role: 'assistant',
                sequence: 8,
            },
        } as SSEEvent));
        // Page content lives on the execution page — feed it via a
        // transcript merge (simulating what text_delta + model_completed
        // would leave behind on the client).
        applyTranscript(state, {
            conversationId: CONV,
            turns: [{
                turnId: 'tn_live_note',
                status: 'running',
                execution: {
                    pages: [{
                        pageId: 'page_final_live',
                        iteration: 1,
                        content: 'Final answer',
                        createdAt: '2026-04-21T00:00:09Z',
                    }],
                },
            }],
        });

        const rows = projectConversation(state);
        expect(rows.map((row) => row.kind)).toEqual(['assistant', 'iteration']);
        expect(rows[0]).toMatchObject({
            kind: 'assistant',
            messageId: 'msg_note_live',
            content: 'PRELIMINARY NOTE',
        });
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
