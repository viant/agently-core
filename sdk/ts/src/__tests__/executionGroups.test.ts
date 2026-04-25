import { describe, expect, it } from 'vitest';

import {
    applyExecutionStreamEventToGroups,
    describeExecutionTimelineEvent,
    findExecutionStepById,
    findExecutionStepByPayloadId,
    isPresentableExecutionGroup,
    mergeLatestTranscriptAndLiveExecutionGroups,
    normalizeExecutionPageSize,
    plannedExecutionToolCalls,
    selectExecutionPages,
    selectExecutionSteps,
} from '../index';
import type { ExecutionPage, LiveExecutionGroupsById, PlannedToolCall, SSEEvent, Turn } from '../types';

function event(input: Partial<SSEEvent>): SSEEvent {
    return input as SSEEvent;
}

function turns(input: Turn[]): Turn[] {
    return input;
}

function groupMap(input: LiveExecutionGroupsById): LiveExecutionGroupsById {
    return input;
}

describe('executionGroups', () => {
    it('normalizes page size choices', () => {
        expect(normalizeExecutionPageSize('all')).toBe('all');
        expect(normalizeExecutionPageSize('5')).toBe('5');
        expect(normalizeExecutionPageSize('weird')).toBe('1');
    });

    it('reads planned tool calls and presentable groups from canonical pages', () => {
        const group: Partial<ExecutionPage> = {
            toolCallsPlanned: [{ toolCallId: 'tc1', toolName: 'llm/agents/run' } as PlannedToolCall],
            narration: '',
            content: '',
            finalResponse: false,
        };
        expect(plannedExecutionToolCalls(group)).toHaveLength(1);
        expect(isPresentableExecutionGroup(group)).toBe(true);
    });

    it('selects canonical execution pages and steps', () => {
        const transcriptTurns = turns([{
            turnId: 'turn-1',
            status: 'completed',
            execution: {
                pages: [{
                    pageId: 'page-1',
                    assistantMessageId: 'page-1',
                    iteration: 1,
                    status: 'completed',
                    finalResponse: true,
                    modelSteps: [{
                        modelCallId: 'mc-1',
                        assistantMessageId: 'page-1',
                        provider: 'openai',
                        model: 'gpt-5.4',
                        requestPayloadId: 'req-1',
                    }],
                    toolSteps: [{
                        toolCallId: 'tc-1',
                        toolMessageId: 'tm-1',
                        toolName: 'system/exec',
                        responsePayloadId: 'resp-1',
                    }],
                }],
            },
        } as Turn]);

        const pages = selectExecutionPages(transcriptTurns);
        const steps = selectExecutionSteps(pages);
        expect(pages).toHaveLength(1);
        expect(steps).toHaveLength(2);
        expect(findExecutionStepById(pages, 'tc-1')).toMatchObject({ toolName: 'system/exec' });
        expect(findExecutionStepByPayloadId(pages, 'req-1')).toMatchObject({ kind: 'model' });
        expect(findExecutionStepByPayloadId(pages, 'resp-1')).toMatchObject({ kind: 'tool' });
    });

    it('applies live stream events and merges latest visible groups', () => {
        const live1 = applyExecutionStreamEventToGroups({}, event({
            type: 'model_started',
            assistantMessageId: 'a1',
            turnId: 'turn-1',
            status: 'running',
            model: { provider: 'openai', model: 'gpt-5.4' },
        }));
        const live2 = applyExecutionStreamEventToGroups(live1, event({
            type: 'narration',
            assistantMessageId: 'a1',
            turnId: 'turn-1',
            content: 'Calling updatePlan.',
            status: 'running',
        }));
        const live3 = applyExecutionStreamEventToGroups(live2, event({
            type: 'tool_call_started',
            assistantMessageId: 'a1',
            turnId: 'turn-1',
            toolCallId: 'tc1',
            toolMessageId: 'tm1',
            toolName: 'llm/agents/run',
            status: 'running',
        }));
        const live4 = applyExecutionStreamEventToGroups(live3, event({
            type: 'tool_calls_planned',
            assistantMessageId: 'a1',
            turnId: 'turn-1',
            toolCallsPlanned: [{ toolCallId: 'tc2', toolName: 'resources/read' }],
            status: 'running',
        }));
        const merged = mergeLatestTranscriptAndLiveExecutionGroups([], live4, '1');

        expect(Object.keys(live4)).toContain('a1');
        expect(merged).toHaveLength(1);
        expect(merged[0]).toMatchObject({
            assistantMessageId: 'a1',
            turnId: 'turn-1',
            status: 'running',
            narration: 'Calling updatePlan.',
        });
        expect(merged[0].toolSteps[0]).toMatchObject({
            toolName: 'llm/agents/run',
            status: 'running',
        });
        expect(merged[0].toolCallsPlanned[0]).toMatchObject({
            toolName: 'resources/read',
        });
    });

    it('preserves leading-space text deltas in live execution group content', () => {
        const live1 = applyExecutionStreamEventToGroups({}, event({
            type: 'model_started',
            assistantMessageId: 'a1',
            turnId: 'turn-1',
            status: 'running',
            model: { provider: 'openai', model: 'gpt-5.4' },
        }));
        const live2 = applyExecutionStreamEventToGroups(live1, event({
            type: 'text_delta',
            assistantMessageId: 'a1',
            turnId: 'turn-1',
            content: 'Milo',
        }));
        const live3 = applyExecutionStreamEventToGroups(live2, event({
            type: 'text_delta',
            assistantMessageId: 'a1',
            turnId: 'turn-1',
            content: ' was',
        }));
        const live4 = applyExecutionStreamEventToGroups(live3, event({
            type: 'text_delta',
            assistantMessageId: 'a1',
            turnId: 'turn-1',
            content: ' curious',
        }));

        expect(live4.a1?.content).toBe('Milo was curious');
    });

    it('prefers live execution group over transcript group for the same assistant page', () => {
        const transcript: Array<Partial<ExecutionPage>> = [{
            assistantMessageId: 'a1',
            status: 'completed',
            content: 'persisted content',
            toolSteps: [],
        }];
        const live = groupMap({
            a1: {
                assistantMessageId: 'a1',
                status: 'running',
                content: 'live content',
                toolSteps: [{ toolName: 'system/exec', status: 'running' }],
            },
        });

        const merged = mergeLatestTranscriptAndLiveExecutionGroups(transcript, live, 'all');
        expect(merged).toHaveLength(1);
        expect(merged[0]).toMatchObject({
            assistantMessageId: 'a1',
            status: 'running',
            content: 'live content',
        });
        expect(merged[0].toolSteps[0]).toMatchObject({ toolName: 'system/exec' });
    });

    it('describes timeline events with planned tool names', () => {
        const text = describeExecutionTimelineEvent({
            type: 'model_completed',
            status: 'thinking',
            toolCallsPlanned: [
                { toolName: 'llm/agents/run' },
                { toolName: 'system/exec/run' },
            ],
        });
        expect(text).toContain('planned llm/agents/run, system/exec/run');
    });

    it('marks live execution groups terminal by turn id when the turn event has no assistant message id', () => {
        const live1 = applyExecutionStreamEventToGroups({}, event({
            type: 'model_started',
            assistantMessageId: 'a1',
            turnId: 'turn-1',
            status: 'running',
            model: { provider: 'openai', model: 'gpt-5.4' },
        }));
        const live2 = applyExecutionStreamEventToGroups(live1, event({
            type: 'tool_call_started',
            assistantMessageId: 'a1',
            turnId: 'turn-1',
            toolCallId: 'tc1',
            toolMessageId: 'tm1',
            toolName: 'resources/read',
            status: 'running',
        }));
        const live3 = applyExecutionStreamEventToGroups(live2, event({
            type: 'turn_failed',
            turnId: 'turn-1',
            status: 'failed',
            error: 'boom',
        }));

        expect(live3.a1).toMatchObject({
            status: 'failed',
            errorMessage: 'boom',
        });
        expect(live3.a1.modelSteps[0]).toMatchObject({
            status: 'failed',
            errorMessage: 'boom',
        });
        expect(live3.a1.toolSteps[0]).toMatchObject({
            status: 'failed',
            errorMessage: 'boom',
        });
    });

    it('tracks async waiting and failed tool lifecycle events', () => {
        const live1 = applyExecutionStreamEventToGroups({}, event({
            type: 'model_started',
            assistantMessageId: 'a1',
            turnId: 'turn-1',
            status: 'running',
            model: { provider: 'openai', model: 'gpt-5.4' },
        }));
        const live2 = applyExecutionStreamEventToGroups(live1, event({
            type: 'tool_call_waiting',
            assistantMessageId: 'a1',
            turnId: 'turn-1',
            toolCallId: 'tc1',
            toolMessageId: 'tm1',
            toolName: 'llm/agents:start',
            operationId: 'child-1',
            status: 'running',
        }));
        const live3 = applyExecutionStreamEventToGroups(live2, event({
            type: 'tool_call_failed',
            assistantMessageId: 'a1',
            turnId: 'turn-1',
            toolCallId: 'tc1',
            toolMessageId: 'tm1',
            toolName: 'llm/agents:start',
            operationId: 'child-1',
            status: 'failed',
            error: 'boom',
        }));

        expect(live3.a1).toMatchObject({
            status: 'failed',
            errorMessage: 'boom',
        });
        expect(live3.a1.toolSteps[0]).toMatchObject({
            toolName: 'llm/agents:start',
            operationId: 'child-1',
            status: 'failed',
            errorMessage: 'boom',
        });
        expect(live3.a1.toolSteps[0].asyncOperation).toMatchObject({
            operationId: 'child-1',
            status: 'failed',
            error: 'boom',
        });
    });

    it('propagates linked conversation metadata onto the tool step in live execution groups', () => {
        const live1 = applyExecutionStreamEventToGroups({}, event({
            type: 'model_started',
            assistantMessageId: 'a1',
            turnId: 'turn-1',
            iteration: 1,
            status: 'running',
            model: { provider: 'openai', model: 'gpt-5.4' },
        }));
        const live2 = applyExecutionStreamEventToGroups(live1, event({
            type: 'tool_call_started',
            assistantMessageId: 'a1',
            turnId: 'turn-1',
            toolCallId: 'call-agent-1',
            toolMessageId: 'tool-msg-1',
            toolName: 'llm/agents/run',
            status: 'running',
        }));
        const live3 = applyExecutionStreamEventToGroups(live2, event({
            type: 'linked_conversation_attached',
            assistantMessageId: 'a1',
            turnId: 'turn-1',
            toolCallId: 'call-agent-1',
            linkedConversationId: 'child-conv-1',
            linkedConversationAgentId: 'steward-forecasting',
            linkedConversationTitle: 'Forecasting Child',
        }));

        expect(live3.a1.toolSteps[0]).toMatchObject({
            toolCallId: 'call-agent-1',
            linkedConversationId: 'child-conv-1',
            linkedConversationAgentId: 'steward-forecasting',
            linkedConversationTitle: 'Forecasting Child',
        });
    });

    it('preserves execution page ordering when follow-up events omit iteration/page metadata', () => {
        const live1 = applyExecutionStreamEventToGroups({}, event({
            type: 'model_started',
            assistantMessageId: 'a7',
            turnId: 'turn-1',
            iteration: 7,
            pageIndex: 7,
            status: 'thinking',
            model: { provider: 'openai', model: 'gpt-5.4' },
        }));
        const live1b = applyExecutionStreamEventToGroups(live1, event({
            type: 'narration',
            assistantMessageId: 'a7',
            turnId: 'turn-1',
            iteration: 7,
            pageIndex: 7,
            content: 'First presentable narration.',
            status: 'completed',
        }));
        const live2 = applyExecutionStreamEventToGroups(live1b, event({
            type: 'model_started',
            assistantMessageId: 'a8',
            turnId: 'turn-1',
            iteration: 8,
            pageIndex: 8,
            status: 'thinking',
            model: { provider: 'openai', model: 'gpt-5.4' },
        }));
        const live3 = applyExecutionStreamEventToGroups(live2, event({
            type: 'tool_call_completed',
            assistantMessageId: 'a8',
            turnId: 'turn-1',
            iteration: 0,
            pageIndex: 0,
            toolCallId: 'tc8',
            toolMessageId: 'tm8',
            toolName: 'orchestration/updatePlan',
            status: 'completed',
        }));
        const live4 = applyExecutionStreamEventToGroups(live3, event({
            type: 'narration',
            assistantMessageId: 'a8',
            turnId: 'turn-1',
            iteration: 0,
            pageIndex: 0,
            content: 'Calling updatePlan.',
            status: 'completed',
        }));

        expect(live4.a8).toMatchObject({
            sequence: 8,
            iteration: 8,
            narration: 'Calling updatePlan.',
        });

        const merged = mergeLatestTranscriptAndLiveExecutionGroups([], live4, 'all');
        expect(merged.map((group) => group.assistantMessageId)).toEqual(['a7', 'a8']);
    });

    it('marks a live model page streaming when text deltas arrive without explicit status', () => {
        const live1 = applyExecutionStreamEventToGroups({}, event({
            type: 'model_started',
            assistantMessageId: 'a1',
            turnId: 'turn-1',
            iteration: 1,
            pageIndex: 1,
            status: 'thinking',
            model: { provider: 'openai', model: 'gpt-5.4' },
        }));
        const live2 = applyExecutionStreamEventToGroups(live1, event({
            type: 'text_delta',
            assistantMessageId: 'a1',
            turnId: 'turn-1',
            content: 'Hello',
        }));

        expect(live2.a1).toMatchObject({
            status: 'streaming',
            content: 'Hello',
        });
        expect(live2.a1.modelSteps[0]).toMatchObject({
            status: 'streaming',
        });
    });

    it('marks model_completed without explicit status as completed for the group and model step', () => {
        const live1 = applyExecutionStreamEventToGroups({}, event({
            type: 'model_started',
            assistantMessageId: 'a9',
            turnId: 'turn-1',
            iteration: 1,
            pageIndex: 1,
            status: 'thinking',
            model: { provider: 'openai', model: 'gpt-5.4' },
        }));
        const live2 = applyExecutionStreamEventToGroups(live1, event({
            type: 'model_completed',
            assistantMessageId: 'a9',
            turnId: 'turn-1',
            iteration: 1,
            pageIndex: 1,
            responsePayloadId: 'resp-9',
        }));

        expect(live2.a9).toMatchObject({
            status: 'completed',
            modelSteps: [{ status: 'completed', responsePayloadId: 'resp-9' }],
        });
    });
});
