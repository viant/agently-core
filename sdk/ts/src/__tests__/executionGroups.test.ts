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

describe('executionGroups', () => {
    it('normalizes page size choices', () => {
        expect(normalizeExecutionPageSize('all')).toBe('all');
        expect(normalizeExecutionPageSize('5')).toBe('5');
        expect(normalizeExecutionPageSize('weird')).toBe('1');
    });

    it('reads planned tool calls and presentable groups from canonical pages', () => {
        const group = {
            toolCallsPlanned: [{ toolCallId: 'tc1', toolName: 'llm/agents/run' }],
            preamble: '',
            content: '',
            finalResponse: false,
        };
        expect(plannedExecutionToolCalls(group)).toHaveLength(1);
        expect(isPresentableExecutionGroup(group)).toBe(true);
    });

    it('selects canonical execution pages and steps', () => {
        const turns = [{
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
        }] as any;

        const pages = selectExecutionPages(turns);
        const steps = selectExecutionSteps(pages);
        expect(pages).toHaveLength(1);
        expect(steps).toHaveLength(2);
        expect(findExecutionStepById(pages, 'tm-1')).toMatchObject({ toolName: 'system/exec' });
        expect(findExecutionStepByPayloadId(pages, 'req-1')).toMatchObject({ kind: 'model' });
        expect(findExecutionStepByPayloadId(pages, 'resp-1')).toMatchObject({ kind: 'tool' });
    });

    it('applies live stream events and merges latest visible groups', () => {
        const live1 = applyExecutionStreamEventToGroups({}, {
            type: 'model_started',
            assistantMessageId: 'a1',
            status: 'running',
            model: { provider: 'openai', model: 'gpt-5.4' },
        } as any);
        const live2 = applyExecutionStreamEventToGroups(live1, {
            type: 'tool_call_started',
            assistantMessageId: 'a1',
            toolCallId: 'tc1',
            toolMessageId: 'tm1',
            toolName: 'llm/agents/run',
            status: 'running',
        } as any);
        const merged = mergeLatestTranscriptAndLiveExecutionGroups([], live2, '1');

        expect(Object.keys(live2)).toContain('a1');
        expect(merged).toHaveLength(1);
        expect(merged[0]).toMatchObject({
            assistantMessageId: 'a1',
            status: 'running',
        });
        expect(merged[0].toolSteps[0]).toMatchObject({
            toolName: 'llm/agents/run',
            status: 'running',
        });
    });

    it('prefers live execution group over transcript group for the same assistant page', () => {
        const transcript = [{
            assistantMessageId: 'a1',
            status: 'completed',
            content: 'persisted content',
            toolSteps: [],
        }] as any;
        const live = {
            a1: {
                assistantMessageId: 'a1',
                status: 'running',
                content: 'live content',
                toolSteps: [{ toolName: 'system/exec', status: 'running' }],
            },
        } as any;

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
});
