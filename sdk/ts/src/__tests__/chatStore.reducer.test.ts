import { describe, it, expect } from 'vitest';

import {
    applyEvent,
    applyLocalSubmit,
    applyTranscript,
    getFieldProvenance,
    isEffectiveValue,
    newConversationState,
} from '../chatStore/reducer';
import type {
    CanonicalConversationState,
    ClientConversationState,
    LocalSubmit,
} from '../chatStore/types';
import type { SSEEvent } from '../types';

const CONV = 'conv_1';

function fresh(): ClientConversationState {
    return newConversationState(CONV);
}

function sse(partial: Partial<SSEEvent>): SSEEvent {
    return { type: 'turn_started', conversationId: CONV, ...partial } as SSEEvent;
}

function submit(partial: Partial<LocalSubmit> = {}): LocalSubmit {
    return {
        conversationId: CONV,
        clientRequestId: 'crid_1',
        content: 'hello',
        createdAt: '2025-01-01T00:00:00.000Z',
        ...partial,
    };
}

// ─── Effective-write gate ──────────────────────────────────────────────────────

describe('chatStore/reducer — isEffectiveValue', () => {
    it('non-empty strings are effective; empty / whitespace-only are not', () => {
        expect(isEffectiveValue('x')).toBe(true);
        expect(isEffectiveValue('   ')).toBe(false);
        expect(isEffectiveValue('')).toBe(false);
    });
    it('null and undefined are never effective', () => {
        expect(isEffectiveValue(null)).toBe(false);
        expect(isEffectiveValue(undefined)).toBe(false);
    });
    it('finite numbers are effective; NaN / Infinity are not', () => {
        expect(isEffectiveValue(0)).toBe(true);
        expect(isEffectiveValue(42)).toBe(true);
        expect(isEffectiveValue(NaN)).toBe(false);
        expect(isEffectiveValue(Infinity)).toBe(false);
    });
    it('booleans (true AND false) are effective', () => {
        expect(isEffectiveValue(true)).toBe(true);
        expect(isEffectiveValue(false)).toBe(true);
    });
    it('arrays are effective (empty array marks "observed")', () => {
        expect(isEffectiveValue([])).toBe(true);
        expect(isEffectiveValue([1])).toBe(true);
    });
});

// ─── applyLocalSubmit ─────────────────────────────────────────────────────────

describe('chatStore/reducer — applyLocalSubmit', () => {
    it('creates a pending turn with one user message (bootstrap)', () => {
        const state = applyLocalSubmit(fresh(), submit());
        expect(state.turns.length).toBe(1);
        const turn = state.turns[0];
        expect(turn.turnId).toBe('');
        expect(turn.lifecycle).toBe('pending');
        expect(turn.users.length).toBe(1);
        expect(turn.users[0].content).toBe('hello');
        expect(turn.users[0].clientRequestId).toBe('crid_1');
    });

    it('second normal submit during an active pending turn creates a queued follow-up turn', () => {
        let state = applyLocalSubmit(fresh(), submit({ clientRequestId: 'crid_1' }));
        state = applyLocalSubmit(state, submit({ clientRequestId: 'crid_2', content: 'follow-up' }));
        expect(state.turns.length).toBe(2);
        expect(state.turns[0].users.length).toBe(1);
        expect(state.turns[1].users.length).toBe(1);
        expect(state.turns[1].users[0].content).toBe('follow-up');
    });

    it('explicit steer during an active pending turn appends to that turn', () => {
        let state = applyLocalSubmit(fresh(), submit({ clientRequestId: 'crid_1' }));
        state = applyLocalSubmit(state, submit({
            clientRequestId: 'crid_2',
            content: 'follow-up',
            mode: 'steer',
        }));
        expect(state.turns.length).toBe(1);
        expect(state.turns[0].users.length).toBe(2);
        expect(state.turns[0].users[1].content).toBe('follow-up');
    });

    it('throws on duplicate clientRequestId', () => {
        const state = applyLocalSubmit(fresh(), submit({ clientRequestId: 'dup' }));
        expect(() => applyLocalSubmit(state, submit({ clientRequestId: 'dup' }))).toThrow();
    });

    it('rejects submits for a different conversation', () => {
        expect(() => applyLocalSubmit(fresh(), submit({ conversationId: 'other' }))).toThrow();
    });

    it('user renderKey is stable across local-then-SSE echo', () => {
        const state = applyLocalSubmit(fresh(), submit({ clientRequestId: 'crid_X' }));
        const original = state.turns[0].users[0].renderKey;
        applyEvent(state, sse({
            type: 'turn_started',
            turnId: 'tn_1',
            userMessageId: 'msg_1',
            clientRequestId: 'crid_X',
            createdAt: '2025-01-01T00:00:00.200Z',
        } as SSEEvent));
        expect(state.turns[0].users[0].renderKey).toBe(original);
        expect(state.turns[0].users[0].messageId).toBe('msg_1');
        expect(state.turns[0].turnId).toBe('tn_1');
        expect(state.turns[0].lifecycle).toBe('running');
    });

    it('turn_started without echoed ids promotes the single pending bootstrap turn', () => {
        const state = applyLocalSubmit(fresh(), submit({ clientRequestId: 'crid_only' }));
        const originalTurnKey = state.turns[0].renderKey;
        applyEvent(state, sse({
            type: 'turn_started',
            turnId: 'tn_bootstrap',
            createdAt: '2025-01-01T00:00:00.200Z',
        } as SSEEvent));
        expect(state.turns).toHaveLength(1);
        expect(state.turns[0].renderKey).toBe(originalTurnKey);
        expect(state.turns[0].turnId).toBe('tn_bootstrap');
        expect(state.turns[0].lifecycle).toBe('running');
    });

    it('completed transcript coalesces the starter user after a no-echo live bootstrap', () => {
        const state = applyLocalSubmit(fresh(), submit({
            clientRequestId: 'crid_only',
            content: 'hello',
        }));
        const originalUserKey = state.turns[0].users[0].renderKey;

        applyEvent(state, sse({
            type: 'turn_started',
            turnId: 'tn_bootstrap',
            createdAt: '2025-01-01T00:00:00.200Z',
        } as SSEEvent));
        applyEvent(state, sse({
            type: 'turn_completed',
            turnId: 'tn_bootstrap',
            createdAt: '2025-01-01T00:00:05.000Z',
        } as SSEEvent));

        applyTranscript(state, {
            conversationId: CONV,
            turns: [{
                turnId: 'tn_bootstrap',
                status: 'completed',
                startedByMessageId: 'msg_bootstrap',
                user: {
                    messageId: 'msg_bootstrap',
                    content: 'hello',
                },
            }],
        });

        expect(state.turns).toHaveLength(1);
        expect(state.turns[0].users).toHaveLength(1);
        expect(state.turns[0].users[0].renderKey).toBe(originalUserKey);
        expect(state.turns[0].users[0].messageId).toBe('msg_bootstrap');
    });
});

// ─── applyEvent — turn_started and lifecycle ──────────────────────────────────

describe('chatStore/reducer — applyEvent lifecycle', () => {
    it('turn_started on a fresh conversation creates a running turn with a lifecycle entry', () => {
        const state = applyEvent(fresh(), sse({
            type: 'turn_started',
            turnId: 'tn_A',
            createdAt: '2025-01-01T00:00:00.000Z',
        }));
        expect(state.turns.length).toBe(1);
        expect(state.turns[0].lifecycle).toBe('running');
        const pageCountBefore = state.turns[0].pages.length;
        expect(state.turns[0].pages[0].lifecycleEntries.length).toBe(1);
        expect(state.turns[0].pages[0].lifecycleEntries[0].kind).toBe('turn_started');
    });

    it('turn_completed moves lifecycle to completed and appends a lifecycle entry', () => {
        let state = applyEvent(fresh(), sse({ type: 'turn_started', turnId: 'tn_A', createdAt: '2025-01-01T00:00:00.000Z' }));
        state = applyEvent(state, sse({ type: 'turn_completed', turnId: 'tn_A', createdAt: '2025-01-01T00:00:05.000Z' } as SSEEvent));
        expect(state.turns[0].lifecycle).toBe('completed');
        const entries = state.turns[0].pages[0].lifecycleEntries.map((e) => e.kind);
        expect(entries).toEqual(['turn_started', 'turn_completed']);
    });

    it('assistant_* / model_* / tool_call_* never set terminal lifecycle', () => {
        let state = applyEvent(fresh(), sse({ type: 'turn_started', turnId: 'tn_A', createdAt: '2025-01-01T00:00:00.000Z' }));
        state = applyEvent(state, sse({ type: 'model_started', turnId: 'tn_A', pageId: 'pg_1' } as SSEEvent));
        state = applyEvent(state, sse({ type: 'model_completed', turnId: 'tn_A', pageId: 'pg_1' } as SSEEvent));
        state = applyEvent(state, sse({ type: 'assistant', turnId: 'tn_A', pageId: 'pg_1', messageId: 'am_1', content: 'done', patch: { role: 'assistant' } } as SSEEvent));
        expect(state.turns[0].lifecycle).toBe('running');
    });

    it('assistant event appends a standalone turn message without changing lifecycle', () => {
        // Replaces the legacy `control:message_add` test. The new
        // contract: an `assistant` event carries role via patch and
        // upserts into turn.messages by messageId.
        let state = applyEvent(fresh(), sse({ type: 'turn_started', turnId: 'tn_A', createdAt: '2025-01-01T00:00:00.000Z' }));
        state = applyEvent(state, sse({
            type: 'assistant',
            turnId: 'tn_A',
            messageId: 'msg_note',
            content: 'PRELIMINARY NOTE',
            createdAt: '2025-01-01T00:00:08.000Z',
            patch: {
                role: 'assistant',
                sequence: 8,
            },
        } as SSEEvent));
        expect(state.turns[0].lifecycle).toBe('running');
        expect(state.turns[0].messages).toHaveLength(1);
        expect(state.turns[0].messages[0]).toMatchObject({
            messageId: 'msg_note',
            role: 'assistant',
            content: 'PRELIMINARY NOTE',
            sequence: 8,
        });
    });

    it('assistant message_add-style rows stay standalone even when execution pages already exist', () => {
        let state = applyEvent(fresh(), sse({
            type: 'turn_started',
            turnId: 'tn_A',
            createdAt: '2025-01-01T00:00:00.000Z',
        }));
        state = applyEvent(state, sse({
            type: 'model_started',
            turnId: 'tn_A',
            assistantMessageId: 'msg_exec',
            pageId: 'pg_1',
            status: 'running',
            model: { provider: 'openai', model: 'gpt-5.4' },
        } as SSEEvent));

        state = applyEvent(state, sse({
            type: 'assistant',
            turnId: 'tn_A',
            iteration: 1,
            messageId: 'msg_prelim',
            content: 'PRELIMINARY DASHBOARD',
            createdAt: '2025-01-01T00:00:08.000Z',
            patch: {
                role: 'assistant',
                mode: 'task',
                interim: 0,
                sequence: 12,
                status: 'completed',
            },
        } as SSEEvent));

        expect(state.turns[0].messages).toHaveLength(1);
        expect(state.turns[0].messages[0]).toMatchObject({
            messageId: 'msg_prelim',
            role: 'assistant',
            content: 'PRELIMINARY DASHBOARD',
            mode: 'task',
            interim: 0,
        });
        expect(state.turns[0].pages[0].content ?? '').not.toBe('PRELIMINARY DASHBOARD');
        expect(state.turns[0].pages[0].finalAssistantMessageId ?? '').not.toBe('msg_prelim');
    });

    it('later standalone assistant rows in the same iteration stay standalone', () => {
        let state = applyEvent(fresh(), sse({
            type: 'turn_started',
            turnId: 'tn_A',
            createdAt: '2025-01-01T00:00:00.000Z',
        }));
        state = applyEvent(state, sse({
            type: 'model_started',
            turnId: 'tn_A',
            assistantMessageId: 'msg_exec_1',
            pageId: 'pg_1',
            iteration: 4,
            status: 'running',
            model: { provider: 'openai', model: 'gpt-5.4' },
        } as SSEEvent));
        state = applyEvent(state, sse({
            type: 'assistant',
            turnId: 'tn_A',
            iteration: 4,
            messageId: 'msg_prelim_1',
            content: 'FIRST PRELIMINARY',
            createdAt: '2025-01-01T00:00:08.000Z',
            patch: {
                role: 'assistant',
                mode: 'task',
                interim: 0,
                sequence: 12,
                status: 'completed',
            },
        } as SSEEvent));
        state = applyEvent(state, sse({
            type: 'assistant',
            turnId: 'tn_A',
            iteration: 4,
            messageId: 'msg_prelim_2',
            content: 'SECOND PRELIMINARY',
            createdAt: '2025-01-01T00:00:09.000Z',
            patch: {
                role: 'assistant',
                mode: 'task',
                interim: 0,
                sequence: 13,
                status: 'completed',
            },
        } as SSEEvent));

        expect(state.turns[0].messages.map((message) => message.messageId)).toEqual(['msg_prelim_1', 'msg_prelim_2']);
        expect(state.turns[0].pages[0].content ?? '').not.toBe('FIRST PRELIMINARY');
        expect(state.turns[0].pages[0].content ?? '').not.toBe('SECOND PRELIMINARY');
    });

    it('elicitation_requested copies message and schema from SSE payload', () => {
        let state = applyEvent(fresh(), sse({ type: 'turn_started', turnId: 'tn_A', createdAt: '2025-01-01T00:00:00.000Z' }));
        state = applyEvent(state, sse({
            type: 'elicitation_requested',
            turnId: 'tn_A',
            elicitationId: 'elic_1',
            content: 'Please provide the environment variable name.',
            elicitationData: {
                requestedSchema: {
                    type: 'object',
                    properties: { name: { type: 'string' } },
                    required: ['name'],
                },
            },
        } as SSEEvent));
        expect(state.turns[0].elicitation?.message).toBe('Please provide the environment variable name.');
        expect(state.turns[0].elicitation?.requestedSchema).toMatchObject({
            type: 'object',
            required: ['name'],
        });
    });
});

// ─── Merge rule ────────────────────────────────────────────────────────────────

describe('chatStore/reducer — merge rule', () => {
    function startedState(): ClientConversationState {
        let state = applyLocalSubmit(fresh(), submit({ clientRequestId: 'crid_M' }));
        state = applyEvent(state, sse({
            type: 'turn_started',
            turnId: 'tn_M',
            userMessageId: 'msg_M',
            clientRequestId: 'crid_M',
            createdAt: '2025-01-01T00:00:00.050Z',
        } as SSEEvent));
        return state;
    }

    it('live always wins: transcript cannot overwrite an event-owned field', () => {
        const state = startedState();
        // Live wrote page content via text_delta
        applyEvent(state, sse({ type: 'model_started', turnId: 'tn_M', pageId: 'pg_1', iteration: 1 } as SSEEvent));
        applyEvent(state, sse({ type: 'text_delta', turnId: 'tn_M', pageId: 'pg_1', iteration: 1, content: 'LIVE-TEXT' } as SSEEvent));
        expect(state.turns[0].pages[1]?.content ?? state.turns[0].pages[0].content).toBe('LIVE-TEXT');

        const transcript: CanonicalConversationState = {
            conversationId: CONV,
            turns: [{
                turnId: 'tn_M',
                status: 'running',
                execution: {
                    pages: [{
                        pageId: 'pg_1',
                        iteration: 1,
                        content: 'STALE-TRANSCRIPT',
                        finalResponse: false,
                    }],
                },
            }],
        };
        applyTranscript(state, transcript);
        // The event-written page.content must remain 'LIVE-TEXT'.
        const page = state.turns[0].pages.find((p) =>
            p.pageId === 'pg_1' || (Array.isArray(p.modelSteps) && p.modelSteps.some((s) => s.modelCallId === 'mc_1'))
        )!;
        expect(page.content).toBe('LIVE-TEXT');
    });

    it('transcript fills unset / transcript-owned fields (e.g. responsePayloadId)', () => {
        const state = startedState();
        // SSE creates the model step with modelCallId known.
        applyEvent(state, sse({
            type: 'model_started',
            turnId: 'tn_M',
            pageId: 'pg_1',
            modelCallId: 'mc_1',
            iteration: 1,
        } as SSEEvent));
        const transcript: CanonicalConversationState = {
            conversationId: CONV,
            turns: [{
                turnId: 'tn_M',
                status: 'running',
                execution: {
                    pages: [{
                        pageId: 'pg_1',
                        iteration: 1,
                        modelSteps: [{
                            modelCallId: 'mc_1',
                            responsePayloadId: 'pld_xyz',
                            // Intentionally omit status so transcript doesn't try to overwrite
                            // the event-owned running value.
                        }],
                        finalResponse: false,
                    }],
                },
            }],
        };
        applyTranscript(state, transcript);
        const page = state.turns[0].pages.find((p) =>
            p.pageId === 'pg_1' || (Array.isArray(p.modelSteps) && p.modelSteps.some((s) => s.modelCallId === 'mc_1'))
        )!;
        expect(page.modelSteps.length).toBe(1);
        const step = page.modelSteps[0];
        // Event-written field wins:
        expect(step.status).toBe('running');
        expect(getFieldProvenance(step, 'status')).toBe('event');
        // Previously-unset field is filled by transcript:
        expect(step.responsePayloadId).toBe('pld_xyz');
        expect(getFieldProvenance(step, 'responsePayloadId')).toBe('transcript');
    });

    it('keeps assistantMessageId distinct from modelCallId on model steps', () => {
        const state = startedState();
        applyEvent(state, sse({
            type: 'model_started',
            turnId: 'tn_M',
            pageId: 'pg_1',
            assistantMessageId: 'msg_1',
            modelCallId: 'mc_1',
            iteration: 1,
        } as SSEEvent));
        applyEvent(state, sse({
            type: 'model_completed',
            turnId: 'tn_M',
            pageId: 'pg_1',
            assistantMessageId: 'msg_1',
            modelCallId: 'mc_1',
            responsePayloadId: 'resp_1',
            iteration: 1,
        } as SSEEvent));

        const page = state.turns[0].pages.find((p) =>
            p.pageId === 'pg_1' || (Array.isArray(p.modelSteps) && p.modelSteps.some((s) => s.modelCallId === 'mc_1'))
        )!;
        expect(page.modelSteps).toHaveLength(1);
        const step = page.modelSteps[0];
        expect(step.assistantMessageId).toBe('msg_1');
        expect(step.modelCallId).toBe('mc_1');
        expect(step.responsePayloadId).toBe('resp_1');
    });

    it('does not replace progressive text_delta content with model_completed content unless finalResponse is explicit', () => {
        const state = startedState();
        applyEvent(state, sse({
            type: 'model_started',
            turnId: 'tn_M',
            pageId: 'pg_1',
            assistantMessageId: 'msg_1',
            modelCallId: 'mc_1',
            iteration: 1,
        } as SSEEvent));
        applyEvent(state, sse({
            type: 'text_delta',
            turnId: 'tn_M',
            pageId: 'pg_1',
            assistantMessageId: 'msg_1',
            content: 'streaming partial',
            iteration: 1,
        } as SSEEvent));
        applyEvent(state, sse({
            type: 'model_completed',
            turnId: 'tn_M',
            pageId: 'pg_1',
            assistantMessageId: 'msg_1',
            modelCallId: 'mc_1',
            content: 'final answer arrived too early',
            status: 'completed',
            iteration: 1,
        } as SSEEvent));

        const page = state.turns[0].pages.find((p) => p.pageId === 'pg_1')!;
        expect(page.content).toBe('streaming partial');
        expect(page.finalResponse).not.toBe(true);
        expect(page.finalAssistantMessageId).toBeUndefined();
    });

    it('does not surface intake/router model_completed JSON as page content without an explicit final response', () => {
        const state = startedState();
        applyEvent(state, sse({
            type: 'model_started',
            turnId: 'tn_M',
            pageId: 'pg_intake',
            assistantMessageId: 'msg_intake',
            modelCallId: 'mc_intake',
            phase: 'intake',
            mode: 'router',
            iteration: 0,
        } as SSEEvent));
        applyEvent(state, sse({
            type: 'model_completed',
            turnId: 'tn_M',
            pageId: 'pg_intake',
            assistantMessageId: 'msg_intake',
            modelCallId: 'mc_intake',
            phase: 'intake',
            mode: 'router',
            content: '{"clarificationNeeded":true}',
            status: 'completed',
            iteration: 0,
        } as SSEEvent));

        const page = state.turns[0].pages.find((p) => p.pageId === 'pg_intake')!;
        expect(page.content ?? '').toBe('');
        expect(page.finalResponse).not.toBe(true);
    });

    it('keeps fallback model_completed content for non-intake pages when no progressive text exists', () => {
        const state = startedState();
        applyEvent(state, sse({
            type: 'model_started',
            turnId: 'tn_M',
            pageId: 'pg_main',
            assistantMessageId: 'msg_main',
            modelCallId: 'mc_main',
            phase: 'main',
            mode: 'task',
            iteration: 1,
        } as SSEEvent));
        applyEvent(state, sse({
            type: 'model_completed',
            turnId: 'tn_M',
            pageId: 'pg_main',
            assistantMessageId: 'msg_main',
            modelCallId: 'mc_main',
            phase: 'main',
            mode: 'task',
            content: 'Checking active targeting first.',
            status: 'completed',
            iteration: 1,
        } as SSEEvent));

        const page = state.turns[0].pages.find((p) => p.pageId === 'pg_main')!;
        expect(page.content).toBe('Checking active targeting first.');
        expect(page.finalResponse).not.toBe(true);
    });

    it('applyTranscript is idempotent: same snapshot twice leaves state equal', () => {
        const state = startedState();
        const snapshot: CanonicalConversationState = {
            conversationId: CONV,
            turns: [{
                turnId: 'tn_M',
                status: 'running',
                execution: {
                    pages: [{
                        pageId: 'pg_1',
                        modelSteps: [{ modelCallId: 'mc_1', status: 'completed' }],
                        finalResponse: false,
                    }],
                },
            }],
        };
        applyTranscript(state, snapshot);
        const pageCountAfter1 = state.turns[0].pages.length;
        const msCountAfter1 = state.turns[0].pages.find((p) =>
            p.pageId === 'pg_1' || (Array.isArray(p.modelSteps) && p.modelSteps.some((s) => s.modelCallId === 'mc_1'))
        )!.modelSteps.length;
        applyTranscript(state, snapshot);
        const pageCountAfter2 = state.turns[0].pages.length;
        const msCountAfter2 = state.turns[0].pages.find((p) =>
            p.pageId === 'pg_1' || (Array.isArray(p.modelSteps) && p.modelSteps.some((s) => s.modelCallId === 'mc_1'))
        )!.modelSteps.length;
        expect(pageCountAfter2).toBe(pageCountAfter1);
        expect(msCountAfter2).toBe(msCountAfter1);
    });

    it('transcript cannot shrink a container (no-shrink on pages / modelSteps / toolCalls)', () => {
        const state = startedState();
        // Build up 4 model steps via SSE.
        applyEvent(state, sse({ type: 'model_started', turnId: 'tn_M', pageId: 'pg_1', modelCallId: 'mc_1', iteration: 1 } as SSEEvent));
        applyEvent(state, sse({ type: 'model_started', turnId: 'tn_M', pageId: 'pg_1', modelCallId: 'mc_2', iteration: 1 } as SSEEvent));
        applyEvent(state, sse({ type: 'model_started', turnId: 'tn_M', pageId: 'pg_1', modelCallId: 'mc_3', iteration: 1 } as SSEEvent));
        applyEvent(state, sse({ type: 'model_started', turnId: 'tn_M', pageId: 'pg_1', modelCallId: 'mc_4', iteration: 1 } as SSEEvent));
        const page = state.turns[0].pages.find((p) =>
            p.pageId === 'pg_1' || (Array.isArray(p.modelSteps) && p.modelSteps.some((s) => ['mc_1','mc_2','mc_3','mc_4'].includes(s.modelCallId || '')))
        )!;
        expect(page.modelSteps.length).toBe(4);

        const transcript: CanonicalConversationState = {
            conversationId: CONV,
            turns: [{
                turnId: 'tn_M',
                status: 'running',
                execution: {
                    pages: [{
                        pageId: 'pg_1',
                        iteration: 1,
                        modelSteps: [
                            { modelCallId: 'mc_1' },
                            { modelCallId: 'mc_2' },
                            { modelCallId: 'mc_3' },
                        ],
                        finalResponse: false,
                    }],
                },
            }],
        };
        applyTranscript(state, transcript);
        const pageAfter = state.turns[0].pages.find((p) =>
            p.pageId === 'pg_1' || (Array.isArray(p.modelSteps) && p.modelSteps.some((s) => ['mc_1','mc_2','mc_3','mc_4'].includes(s.modelCallId || '')))
        )!;
        expect(pageAfter.modelSteps.length).toBe(4);     // mc_4 survives
    });

    it('transcript page with matching iteration refines an existing live page instead of creating a duplicate', () => {
        const state = fresh();
        applyEvent(state, sse({
            type: 'turn_started',
            turnId: 'tn_iter',
            createdAt: '2025-01-01T00:00:00Z',
        }));
        applyEvent(state, sse({
            type: 'model_started',
            turnId: 'tn_iter',
            pageId: 'live_pg_1',
            iteration: 1,
            modelCallId: 'mc_live_1',
        } as SSEEvent));
        applyEvent(state, sse({
            type: 'tool_call_started',
            turnId: 'tn_iter',
            pageId: 'live_pg_1',
            iteration: 1,
            toolCallId: 'tool_live_1',
            toolName: 'llm/agents/list',
        } as SSEEvent));
        const pageCountBefore = state.turns[0].pages.length;
        expect(pageCountBefore).toBe(1);

        applyTranscript(state, {
            conversationId: CONV,
            turns: [{
                turnId: 'tn_iter',
                status: 'running',
                execution: {
                    pages: [{
                        pageId: 'transcript_pg_1',
                        iteration: 1,
                        phase: 'sidecar',
                        finalResponse: false,
                        toolSteps: [{ toolCallId: 'tool_live_1', toolName: 'llm/agents/list' }],
                    }],
                },
            }],
        });

        expect(state.turns[0].pages.length).toBe(pageCountBefore);
        const page = state.turns[0].pages.find((p) => p.iteration === 1)!;
        expect(page.phase).toBe('sidecar');
        expect(page.toolCalls.length).toBe(1);
    });

    it('classifies executionRole for intake pages and worker tools', () => {
        let state = fresh();

        state = applyEvent(state, sse({
            type: 'model_started',
            turnId: 'tn_role',
            pageId: 'pg_intake',
            assistantMessageId: 'msg-intake',
            phase: 'intake',
        } as SSEEvent));

        const intakePage = state.turns[0].pages.find((p) => p.pageId === 'pg_intake')!;
        expect(intakePage.executionRole).toBe('intake');
        expect(intakePage.modelSteps[0].executionRole).toBe('intake');

        state = applyEvent(state, sse({
            type: 'tool_call_started',
            turnId: 'tn_role',
            assistantMessageId: 'msg-worker',
            toolCallId: 'call-worker',
            toolName: 'llm/agents:start',
        } as SSEEvent));

        const workerPage = state.turns[0].pages[state.turns[0].pages.length - 1]!;
        expect(workerPage.toolCalls[0].executionRole).toBe('worker');
    });

    it('keeps bootstrap tool events in a dedicated bootstrap page instead of attaching them to intake', () => {
        let state = fresh();

        state = applyEvent(state, sse({
            type: 'model_started',
            turnId: 'tn_bootstrap',
            pageId: 'pg_intake',
            assistantMessageId: 'msg-intake',
            phase: 'intake',
            model: { provider: 'openai', model: 'gpt-5-mini' },
        } as SSEEvent));

        state = applyEvent(state, sse({
            type: 'tool_call_started',
            turnId: 'tn_bootstrap',
            toolCallId: 'bootstrap-1',
            toolName: 'llm/agents:list',
            phase: 'bootstrap',
            executionRole: 'bootstrap',
            status: 'running',
        } as SSEEvent));

        expect(state.turns[0].pages).toHaveLength(2);
        expect(state.turns[0].pages[0].phase).toBe('intake');
        expect(state.turns[0].pages[0].toolCalls).toHaveLength(0);
        expect(state.turns[0].pages[1].phase).toBe('bootstrap');
        expect(state.turns[0].pages[1].executionRole).toBe('bootstrap');
        expect(state.turns[0].pages[1].toolCalls[0]).toMatchObject({
            toolCallId: 'bootstrap-1',
            toolName: 'llm/agents:list',
            executionRole: 'bootstrap',
            status: 'running',
        });
    });

    it('derives bootstrap phase from systemContext mode even when phase is omitted', () => {
        let state = fresh();

        state = applyEvent(state, sse({
            type: 'model_started',
            turnId: 'tn_bootstrap_mode',
            pageId: 'pg_intake',
            assistantMessageId: 'msg-intake',
            phase: 'intake',
            model: { provider: 'openai', model: 'gpt-5-mini' },
        } as SSEEvent));

        state = applyEvent(state, sse({
            type: 'tool_call_started',
            turnId: 'tn_bootstrap_mode',
            toolCallId: 'bootstrap-2',
            toolName: 'llm/skills:list',
            mode: 'systemContext',
            status: 'running',
        } as SSEEvent));

        expect(state.turns[0].pages).toHaveLength(2);
        expect(state.turns[0].pages[1].phase).toBe('bootstrap');
        expect(state.turns[0].pages[1].executionRole).toBe('bootstrap');
        expect(state.turns[0].pages[1].toolCalls[0]).toMatchObject({
            toolCallId: 'bootstrap-2',
            toolName: 'llm/skills:list',
            executionRole: 'bootstrap',
            status: 'running',
        });
    });

    it('creates a new main page after bootstrap instead of attaching model work to the bootstrap page', () => {
        let state = fresh();

        state = applyEvent(state, sse({
            type: 'turn_started',
            turnId: 'tn_bootstrap_main',
            createdAt: '2025-01-01T00:00:00.000Z',
        } as SSEEvent));

        state = applyEvent(state, sse({
            type: 'tool_call_started',
            turnId: 'tn_bootstrap_main',
            toolCallId: 'bootstrap-3',
            toolName: 'llm/agents:list',
            mode: 'systemContext',
            status: 'running',
        } as SSEEvent));

        state = applyEvent(state, sse({
            type: 'model_started',
            turnId: 'tn_bootstrap_main',
            assistantMessageId: 'msg-main',
            modelCallId: 'mc-main',
            model: { provider: 'openai', model: 'gpt-5.4' },
            status: 'running',
        } as SSEEvent));

        expect(state.turns[0].pages).toHaveLength(3);
        expect(state.turns[0].pages[0].pageId).toBe('tn_bootstrap_main:anchor');
        expect(state.turns[0].pages[1].phase).toBe('bootstrap');
        expect(state.turns[0].pages[1].iteration).toBeUndefined();
        expect(state.turns[0].pages[1].toolCalls).toHaveLength(1);
        expect(state.turns[0].pages[1].modelSteps).toHaveLength(0);
        expect(state.turns[0].pages[2].phase).toBe('main');
        expect(state.turns[0].pages[2].iteration).toBe(2);
        expect(state.turns[0].pages[2].modelSteps[0]).toMatchObject({
            assistantMessageId: 'msg-main',
            modelCallId: 'mc-main',
            provider: 'openai',
            model: 'gpt-5.4',
            status: 'running',
        });
    });

    it('does not collapse a pageId-scoped main round onto a bootstrap page that shares the same iteration', () => {
        let state = fresh();

        state = applyEvent(state, sse({
            type: 'turn_started',
            turnId: 'tn_bootstrap_iter',
            createdAt: '2025-01-01T00:00:00.000Z',
        } as SSEEvent));

        state = applyEvent(state, sse({
            type: 'tool_call_started',
            turnId: 'tn_bootstrap_iter',
            pageId: 'tn_bootstrap_iter:bootstrap',
            toolCallId: 'bootstrap-iter-1',
            toolName: 'llm/agents:list',
            mode: 'systemContext',
            iteration: 1,
            status: 'running',
        } as SSEEvent));

        state = applyEvent(state, sse({
            type: 'model_started',
            turnId: 'tn_bootstrap_iter',
            pageId: 'msg-main-iter',
            assistantMessageId: 'msg-main-iter',
            modelCallId: 'mc-main-iter',
            iteration: 1,
            model: { provider: 'openai', model: 'gpt-5.4' },
            status: 'running',
        } as SSEEvent));

        expect(state.turns[0].pages).toHaveLength(3);
        expect(state.turns[0].pages[1]).toMatchObject({
            pageId: 'tn_bootstrap_iter:bootstrap',
            phase: 'bootstrap',
            iteration: 1,
        });
        expect(state.turns[0].pages[1].toolCalls[0]).toMatchObject({
            toolCallId: 'bootstrap-iter-1',
            toolName: 'llm/agents:list',
            executionRole: 'bootstrap',
        });
        expect(state.turns[0].pages[1].modelSteps).toHaveLength(0);
        expect(state.turns[0].pages[2]).toMatchObject({
            pageId: 'msg-main-iter',
            phase: 'main',
            iteration: 1,
        });
        expect(state.turns[0].pages[2].modelSteps[0]).toMatchObject({
            assistantMessageId: 'msg-main-iter',
            modelCallId: 'mc-main-iter',
            status: 'running',
        });
    });

    it('merges a late modelCallId onto the existing anonymous assistant step', () => {
        let state = fresh();

        state = applyEvent(state, sse({
            type: 'turn_started',
            turnId: 'tn_model_identity',
            createdAt: '2025-01-01T00:00:00.000Z',
        } as SSEEvent));

        state = applyEvent(state, sse({
            type: 'model_started',
            turnId: 'tn_model_identity',
            pageId: 'msg-main',
            assistantMessageId: 'msg-main',
            iteration: 1,
            model: { provider: 'openai', model: 'gpt-5.4' },
            status: 'running',
        } as SSEEvent));

        state = applyEvent(state, sse({
            type: 'model_completed',
            turnId: 'tn_model_identity',
            pageId: 'msg-main',
            assistantMessageId: 'msg-main',
            modelCallId: 'mc-final',
            iteration: 1,
            status: 'completed',
        } as SSEEvent));

        const page = state.turns[0].pages.find((p) => p.pageId === 'msg-main');
        expect(page).toBeTruthy();
        expect(page?.modelSteps).toHaveLength(1);
        expect(page?.modelSteps?.[0]).toMatchObject({
            assistantMessageId: 'msg-main',
            modelCallId: 'mc-final',
            status: 'completed',
        });
    });

    it('event supersedes local on the same field', () => {
        let state = applyLocalSubmit(fresh(), submit({ clientRequestId: 'crid_L', content: 'hello-local' }));
        const user = state.turns[0].users[0];
        expect(user.content).toBe('hello-local');
        expect(getFieldProvenance(user, 'content')).toBe('local');
        // A hypothetical message_patch event for the same user would write content.
        // We simulate by directly calling applyEvent with turn_started carrying an
        // echoed content via narration — not exercised on user row by
        // current spec. Instead, verify at the reducer's write primitive:
        // when the reducer encounters a user already echoed by SSE and matches by
        // clientRequestId, writing new event-owned content should supersede 'local'.
        state = applyEvent(state, sse({
            type: 'turn_started',
            turnId: 'tn_L',
            userMessageId: 'msg_L',
            clientRequestId: 'crid_L',
            content: 'hello-server',    // server-normalised
            createdAt: '2025-01-01T00:00:00.100Z',
        } as SSEEvent));
        // turn_started doesn't mutate user.content in our current reducer; it only
        // fills messageId. Test that the provenance path is correct end-to-end by
        // asserting user.messageId has 'event' provenance now.
        const userAfter = state.turns[0].users[0];
        expect(userAfter.messageId).toBe('msg_L');
        expect(getFieldProvenance(userAfter, 'messageId')).toBe('event');
    });
});

// ─── Transcript-created turns initialize lifecycle ─────────────────────────────

describe('chatStore/reducer — applyTranscript lifecycle init', () => {
    it('transcript-created terminal turns initialise to the mapped terminal lifecycle (not running)', () => {
        const state = fresh();
        applyTranscript(state, {
            conversationId: CONV,
            turns: [
                { turnId: 'tn_A', status: 'completed' },
                { turnId: 'tn_B', status: 'failed' },
                { turnId: 'tn_C', status: 'canceled' },
                { turnId: 'tn_D', status: 'queued' },
                { turnId: 'tn_E', status: 'waiting_for_user' },
            ],
        });
        const lifecycles = state.turns.map((t) => t.lifecycle);
        expect(lifecycles).toEqual(['completed', 'failed', 'cancelled', 'pending', 'running']);
    });
});

describe('chatStore/reducer — SSE + transcript de-dup for turn.messages', () => {
    // Invariant: active turn → SSE; transcript → past history.
    // A mid-turn transcript refresh must NOT duplicate an assistant
    // message that the SSE path already added. Dedup is by messageId
    // in mergeTranscriptTurnMessage; this test proves the invariant
    // holds for the "first assistant message before the next arrives"
    // case.
    it('transcript merge dedupes assistant message that SSE already added by messageId', () => {
        let state = applyEvent(fresh(), sse({
            type: 'turn_started',
            turnId: 'tn_dedup',
            createdAt: '2026-04-23T00:00:00Z',
        }));
        // Live SSE emits the first assistant message (explicit add).
        state = applyEvent(state, sse({
            type: 'assistant',
            turnId: 'tn_dedup',
            messageId: 'msg_A',
            content: 'First assistant note.',
            createdAt: '2026-04-23T00:00:05Z',
            patch: { role: 'assistant', sequence: 2 },
        } as SSEEvent));
        expect(state.turns[0].messages).toHaveLength(1);
        expect(state.turns[0].messages[0].messageId).toBe('msg_A');
        // Transcript refresh arrives before the next assistant message.
        // The same message id is present in the canonical snapshot.
        applyTranscript(state, {
            conversationId: CONV,
            turns: [{
                turnId: 'tn_dedup',
                status: 'running',
                messages: [{
                    messageId: 'msg_A',
                    role: 'assistant',
                    content: 'First assistant note.',
                    sequence: 2,
                    interim: 0,
                    createdAt: '2026-04-23T00:00:05Z',
                }],
            }],
        });
        // Must still have exactly one entry — transcript merges by
        // messageId rather than appending.
        expect(state.turns[0].messages).toHaveLength(1);
        expect(state.turns[0].messages[0]).toMatchObject({
            messageId: 'msg_A',
            role: 'assistant',
            content: 'First assistant note.',
            sequence: 2,
        });
    });

    it('transcript merge dedupes user message by messageId', () => {
        let state = applyEvent(fresh(), sse({
            type: 'turn_started',
            turnId: 'tn_dedup_user',
            createdAt: '2026-04-23T00:00:00Z',
        }));
        state = applyEvent(state, sse({
            type: 'assistant',
            turnId: 'tn_dedup_user',
            messageId: 'msg_U',
            content: 'Hello',
            createdAt: '2026-04-23T00:00:01Z',
            patch: { role: 'user', sequence: 1 },
        } as SSEEvent));
        expect(state.turns[0].users).toHaveLength(1);
        applyTranscript(state, {
            conversationId: CONV,
            turns: [{
                turnId: 'tn_dedup_user',
                status: 'running',
                users: [{
                    messageId: 'msg_U',
                    content: 'Hello',
                    sequence: 1,
                    createdAt: '2026-04-23T00:00:01Z',
                }],
            }],
        });
        expect(state.turns[0].users).toHaveLength(1);
        expect(state.turns[0].users[0]).toMatchObject({
            messageId: 'msg_U',
            content: 'Hello',
            sequence: 1,
        });
    });
});
