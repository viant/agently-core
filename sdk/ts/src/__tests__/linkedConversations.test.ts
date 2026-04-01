import { describe, expect, it } from 'vitest';
import { reduceLinkedConversationPreviewEvent, summarizeLinkedConversationTranscript } from '../linkedConversations';

describe('summarizeLinkedConversationTranscript', () => {
    it('summarizes canonical child transcript pages into preview groups', () => {
        const summary = summarizeLinkedConversationTranscript({
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
        } as any);

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

        const afterModelStart = reduceLinkedConversationPreviewEvent(current, {
            type: 'model_started',
            assistantMessageId: 'mc-1',
            modelName: 'gpt-5.4',
            provider: 'openai',
            status: 'thinking',
        } as any);
        expect(afterModelStart.previewGroups).toHaveLength(1);
        expect(afterModelStart.previewGroups[0]).toMatchObject({
            stepKind: 'model',
            stepLabel: 'gpt-5.4',
            status: 'thinking',
        });

        const afterTool = reduceLinkedConversationPreviewEvent(afterModelStart, {
            type: 'tool_call_started',
            toolCallId: 'call-1',
            toolName: 'steward/AdHierarchy',
            status: 'running',
        } as any);
        expect(afterTool.previewGroups).toHaveLength(2);
        expect(afterTool.previewGroups[1]).toMatchObject({
            stepKind: 'tool',
            stepLabel: 'steward/AdHierarchy',
            status: 'running',
        });

        const afterFinal = reduceLinkedConversationPreviewEvent(afterTool, {
            type: 'assistant_final',
            content: 'Child forecast complete.',
            status: 'completed',
        } as any);
        expect(afterFinal.response).toBe('Child forecast complete.');
        expect(afterFinal.status).toBe('completed');
    });
});
