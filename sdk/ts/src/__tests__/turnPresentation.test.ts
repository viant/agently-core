import { describe, expect, it } from 'vitest';

import {
    normalizeTokenUsageSummary,
    resolveActiveTurnProgress,
    resolveTurnNarration,
    resolveTurnTerminalNotice,
    summarizeExecutionTokenUsage,
    summarizeToolProgress,
} from '../turnPresentation';

describe('turn presentation', () => {
    it('summarizes stable planned and observed tool identities', () => {
        expect(summarizeToolProgress([{
            toolCallsPlanned: [
                { toolCallId: 'a', toolName: 'Read orders' },
                { toolCallId: 'b', toolName: 'Read pacing' },
                { toolCallId: 'c', toolName: 'Apply change' },
            ],
            toolSteps: [
                { toolCallId: 'a', toolName: 'Read orders', status: 'completed' },
                { toolCallId: 'b', toolName: 'Read pacing', status: 'running' },
            ],
        }])).toMatchObject({
            completedToolCount: 1,
            activeToolCount: 1,
            queuedToolCount: 1,
            failedToolCount: 0,
            totalToolCount: 3,
            identityComplete: true,
        });
    });

    it('does not guess numeric identity for calls without toolCallId', () => {
        expect(summarizeToolProgress([{
            toolCallsPlanned: [{ toolName: 'Read orders' }],
            toolSteps: [{ toolCallId: '', toolName: 'Read orders', status: 'running' }],
        }])).toMatchObject({ totalToolCount: 0, identityComplete: false });
    });

    it('uses the single active tool display name', () => {
        expect(resolveActiveTurnProgress({
            turnId: 'turn-1',
            status: 'running',
            groups: [{ toolSteps: [{ toolCallId: 'a', toolName: 'Delivery diagnostics', status: 'running' }] }],
        })).toMatchObject({
            state: 'running',
            activity: { kind: 'tool', label: 'Delivery diagnostics' },
            activeToolCount: 1,
            canStop: true,
        });
    });

    it('uses aggregate tool activity for concurrent calls', () => {
        expect(resolveActiveTurnProgress({
            turnId: 'turn-1',
            status: 'running',
            groups: [{ toolSteps: [
                { toolCallId: 'a', toolName: 'Read orders', status: 'running' },
                { toolCallId: 'b', toolName: 'Read pacing', status: 'streaming' },
            ] }],
        })?.activity).toEqual({ kind: 'tools' });
    });

    it('keeps waiting-for-user non-stoppable', () => {
        expect(resolveActiveTurnProgress({ turnId: 'turn-1', status: 'waiting_for_user' })).toMatchObject({
            state: 'waiting_for_user',
            activity: { kind: 'waiting_for_user' },
            canStop: false,
        });
    });

    it('normalizes token scope and stable model ordering', () => {
        expect(normalizeTokenUsageSummary({
            scope: 'turn',
            totalTokens: 120,
            inputTokens: 80,
            outputTokens: 40,
            cachedInputTokens: 20,
            models: [
                { provider: 'openai', model: 'small', totalTokens: 20 },
                { provider: 'openai', model: 'large', totalTokens: 100 },
            ],
        })).toEqual({
            scope: 'turn',
            totalTokens: 120,
            inputTokens: 80,
            outputTokens: 40,
            cachedInputTokens: 20,
            reasoningTokens: undefined,
            embeddingTokens: undefined,
            models: [
                { provider: 'openai', model: 'large', modelCallId: undefined, totalTokens: 100, inputTokens: undefined, outputTokens: undefined, cachedInputTokens: undefined, reasoningTokens: undefined, embeddingTokens: undefined },
                { provider: 'openai', model: 'small', modelCallId: undefined, totalTokens: 20, inputTokens: undefined, outputTokens: undefined, cachedInputTokens: undefined, reasoningTokens: undefined, embeddingTokens: undefined },
            ],
        });
    });

    it('aggregates active-turn usage from canonical model steps', () => {
        expect(summarizeExecutionTokenUsage([{
            modelSteps: [
                { modelCallId: 'm1', provider: 'openai', model: 'gpt-a', usage: { inputTokens: 80, outputTokens: 20, cachedInputTokens: 10, totalTokens: 100 } },
                { modelCallId: 'm2', provider: 'openai', model: 'gpt-b', usage: { inputTokens: 40, outputTokens: 10, reasoningTokens: 4, totalTokens: 50 } },
            ],
        }])).toMatchObject({
            scope: 'turn',
            totalTokens: 150,
            inputTokens: 120,
            outputTokens: 30,
            cachedInputTokens: 10,
            reasoningTokens: 4,
            models: [{ modelCallId: 'm1' }, { modelCallId: 'm2' }],
        });
    });

    it('updates active narration by stable identity and promotes final content', () => {
        expect(resolveTurnNarration({
            turnId: 'turn-1',
            narrationMessageId: 'narr-1',
            candidates: ['Checking pacing.'],
        })).toEqual({ turnId: 'turn-1', messageId: 'narr-1', content: 'Checking pacing.', status: 'active' });
        expect(resolveTurnNarration({
            turnId: 'turn-1',
            narrationMessageId: 'narr-1',
            finalMessageId: 'answer-1',
            finalContent: 'Pacing is healthy.',
            status: 'completed',
        })).toEqual({ turnId: 'turn-1', messageId: 'answer-1', content: 'Pacing is healthy.', status: 'final' });
    });

    it('normalizes terminal failures without exposing raw details', () => {
        expect(resolveTurnTerminalNotice({ turnId: 'turn-1', status: 'failed' })).toEqual({
            turnId: 'turn-1',
            outcome: 'failed',
            category: 'tool_failed',
            message: 'The request could not be completed.',
        });
    });
});
