import { describe, expect, it } from 'vitest';
import { reduceLinkedConversationPreviewEvent, summarizeLinkedConversationTranscript } from '../linkedConversations';
import type { SSEEvent, TranscriptOutput } from '../types';

function transcript(input: TranscriptOutput): TranscriptOutput {
    return input;
}

function event(input: Partial<SSEEvent>): SSEEvent {
    return input as SSEEvent;
}

describe('summarizeLinkedConversationTranscript', () => {
    it('summarizes canonical child transcript pages into preview groups', () => {
        const summary = summarizeLinkedConversationTranscript(transcript({
            turns: [
                {
                    status: 'completed',
                    agentIdUsed: 'steward-forecasting',
                    execution: {
                        pages: [
                            {
                                assistantMessageId: 'child-1',
                                status: 'completed',
                                preamble: 'Calling roots.',
                                toolSteps: [
                                    { toolName: 'resources/roots', status: 'completed' },
                                ],
                            },
                            {
                                assistantMessageId: 'child-2',
                                status: 'completed',
                                preamble: 'Compiling final answer.',
                                content: 'Forecast returned zero reach.',
                            },
                        ],
                    },
                },
            ],
        }));

        expect(summary.agentId).toBe('steward-forecasting');
        expect(summary.status).toBe('completed');
        expect(summary.response).toBe('Forecast returned zero reach.');
        expect(summary.previewGroups).toHaveLength(2);
        expect(summary.previewGroups[0]).toMatchObject({
            title: 'Calling roots.',
            status: 'completed',
            stepKind: 'tool',
            stepLabel: 'resources/roots',
        });
        expect(summary.previewGroups[0].detailStep).toMatchObject({
            toolName: 'resources/roots',
            status: 'completed',
        });
    });
});

describe('reduceLinkedConversationPreviewEvent', () => {
    it('updates child preview incrementally from live SSE events', () => {
        const current = {
            status: 'running',
            response: '',
            previewGroups: [],
        };

        const afterModelStart = reduceLinkedConversationPreviewEvent(current, event({
            type: 'model_started',
            assistantMessageId: 'mc-1',
            modelName: 'gpt-5.4',
            provider: 'openai',
            status: 'thinking',
        }));
        expect(afterModelStart.previewGroups).toHaveLength(1);
        expect(afterModelStart.previewGroups[0]).toMatchObject({
            stepKind: 'model',
            stepLabel: 'gpt-5.4',
            status: 'thinking',
        });

        const afterTool = reduceLinkedConversationPreviewEvent(afterModelStart, event({
            type: 'tool_call_started',
            toolCallId: 'call-1',
            toolName: 'steward/AdHierarchy',
            status: 'running',
        }));
        expect(afterTool.previewGroups).toHaveLength(2);
        expect(afterTool.previewGroups[1]).toMatchObject({
            stepKind: 'tool',
            stepLabel: 'steward/AdHierarchy',
            status: 'running',
        });

        const afterFinal = reduceLinkedConversationPreviewEvent(afterTool, event({
            type: 'assistant_final',
            content: 'Child forecast complete.',
            status: 'completed',
        }));
        expect(afterFinal.response).toBe('Child forecast complete.');
        expect(afterFinal.status).toBe('completed');
    });

    it('maps failed and canceled tool lifecycle events into preview status', () => {
        const current = {
            status: 'running',
            response: '',
            previewGroups: [],
        };

        const afterFailed = reduceLinkedConversationPreviewEvent(current, event({
            type: 'tool_call_failed',
            toolCallId: 'call-1',
            toolName: 'system/exec:start',
            status: 'failed',
            error: 'boom',
        }));
        expect(afterFailed.previewGroups[0]).toMatchObject({
            stepKind: 'tool',
            stepLabel: 'system/exec:start',
            status: 'failed',
        });

        const afterCanceled = reduceLinkedConversationPreviewEvent(afterFailed, event({
            type: 'tool_call_canceled',
            toolCallId: 'call-2',
            toolName: 'llm/agents:run',
            status: 'canceled',
        }));
        expect(afterCanceled.previewGroups[1]).toMatchObject({
            stepKind: 'tool',
            stepLabel: 'llm/agents:run',
            status: 'canceled',
        });
    });
});
