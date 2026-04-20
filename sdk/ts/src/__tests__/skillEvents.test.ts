import { describe, expect, it } from 'vitest';
import { applyEvent } from '../chatStore/reducer';
import { applyExecutionStreamEventToGroups } from '../executionGroups';

describe('skill stream events', () => {
    it('treats skill events as no-ops for chat reducer state', () => {
        const state: any = {
            conversationId: 'conv_1',
            turns: [],
            messageIndex: {},
            liveExecutionGroupsById: {},
        };
        const activated = applyEvent(state, {
            conversationId: 'conv_1',
            type: 'skill_started',
            skillName: 'playwright-cli',
        } as any);
        expect(activated).toBe(state);

        const updated = applyEvent(state, {
            conversationId: 'conv_1',
            type: 'skill_registry_updated',
            status: 'updated',
        } as any);
        expect(updated).toBe(state);
    });
});

describe('skill stream events in execution groups', () => {
    it('surfaces skill lifecycle as a tool-like step on the current execution group', () => {
        const groups = applyExecutionStreamEventToGroups({}, {
            type: 'skill_started',
            conversationId: 'conv_1',
            assistantMessageId: 'assistant_1',
            toolMessageId: 'tool_1',
            toolName: 'llm/skills:activate',
            skillName: 'playwright-cli',
            skillExecutionId: 'skill-exec-1',
            status: 'running',
            content: 'https://example.com',
        } as any);
        const groups2 = applyExecutionStreamEventToGroups(groups, {
            type: 'skill_completed',
            conversationId: 'conv_1',
            assistantMessageId: 'assistant_1',
            toolMessageId: 'tool_1',
            toolName: 'llm/skills:activate',
            skillName: 'playwright-cli',
            skillExecutionId: 'skill-exec-1',
            status: 'completed',
            content: 'https://example.com',
        } as any);
        const group = groups2['assistant_1'];
        expect(group).toBeTruthy();
        expect(group.toolSteps?.length).toBe(1);
        expect(group.toolSteps?.[0]?.toolName).toBe('llm/skills:activate');
        expect(group.toolSteps?.[0]?.content).toBe('https://example.com');
        expect(group.toolSteps?.[0]?.status).toBe('completed');
    });
});
