import { describe, it, expect } from 'vitest';
import {
    isPreamble, isFinalResponse, isUserMessage, isToolMessage,
    isSystemMessage, isArchived, isSummary, isSummarized,
    toolName, toolStatus, toolElapsedMs, toolCallId,
    messageIteration, messagePreamble,
    groupByIteration, messageUIType,
} from '../interpret';
import type { Message } from '../types';

function msg(overrides: Partial<Message>): Message {
    return {
        id: 'msg_1',
        conversationId: 'c1',
        role: 'assistant',
        type: 'text',
        content: '',
        interim: 0,
        createdAt: '2026-01-01T00:00:00Z',
        ...overrides,
    };
}

describe('isPreamble', () => {
    it('true for assistant + interim=1', () => {
        expect(isPreamble(msg({ role: 'assistant', interim: 1 }))).toBe(true);
    });
    it('false for assistant + interim=0', () => {
        expect(isPreamble(msg({ role: 'assistant', interim: 0 }))).toBe(false);
    });
    it('false for user', () => {
        expect(isPreamble(msg({ role: 'user', interim: 1 }))).toBe(false);
    });
});

describe('isFinalResponse', () => {
    it('true for assistant + interim=0', () => {
        expect(isFinalResponse(msg({ role: 'assistant', interim: 0 }))).toBe(true);
    });
    it('false for interim=1', () => {
        expect(isFinalResponse(msg({ role: 'assistant', interim: 1 }))).toBe(false);
    });
});

describe('isUserMessage', () => {
    it('true for role=user', () => {
        expect(isUserMessage(msg({ role: 'user' }))).toBe(true);
    });
    it('false for role=assistant', () => {
        expect(isUserMessage(msg({ role: 'assistant' }))).toBe(false);
    });
});

describe('isToolMessage', () => {
    it('true for role=tool', () => {
        expect(isToolMessage(msg({ role: 'tool' }))).toBe(true);
    });
    it('true when toolMessage array is populated', () => {
        expect(isToolMessage(msg({
            role: 'assistant',
            toolMessage: [{ id: 'tm1', createdAt: '', type: 'text', toolCall: { id: 'tc1', toolName: 'exec', status: 'succeeded', createdAt: '' } }],
        }))).toBe(true);
    });
    it('false for regular assistant message', () => {
        expect(isToolMessage(msg({ role: 'assistant' }))).toBe(false);
    });
});

describe('isSystemMessage', () => {
    it('true for role=system', () => {
        expect(isSystemMessage(msg({ role: 'system' }))).toBe(true);
    });
    it('true for type=control', () => {
        expect(isSystemMessage(msg({ role: 'assistant', type: 'control' }))).toBe(true);
    });
});

describe('isArchived / isSummary / isSummarized', () => {
    it('isArchived true when archived=1', () => {
        expect(isArchived(msg({ archived: 1 }))).toBe(true);
    });
    it('isArchived false when archived=0', () => {
        expect(isArchived(msg({ archived: 0 }))).toBe(false);
    });
    it('isSummary true for status=summary', () => {
        expect(isSummary(msg({ status: 'summary' }))).toBe(true);
    });
    it('isSummary true for mode=summary', () => {
        expect(isSummary(msg({ mode: 'summary' }))).toBe(true);
    });
    it('isSummarized true for status=summarized', () => {
        expect(isSummarized(msg({ status: 'summarized' }))).toBe(true);
    });
});

describe('toolName / toolStatus / toolElapsedMs / toolCallId', () => {
    const toolMsg = msg({
        toolMessage: [{
            id: 'tm1',
            createdAt: '',
            type: 'text',
            toolName: 'sqlkit/query',
            toolCall: {
                id: 'tc1',
                toolName: 'sqlkit/query',
                status: 'succeeded',
                elapsedMs: 234,
                traceId: 'trace_1',
                createdAt: '',
            },
        }],
    });

    it('extracts tool name', () => {
        expect(toolName(toolMsg)).toBe('sqlkit/query');
    });
    it('extracts tool status', () => {
        expect(toolStatus(toolMsg)).toBe('succeeded');
    });
    it('extracts elapsed ms', () => {
        expect(toolElapsedMs(toolMsg)).toBe(234);
    });
    it('extracts tool call id', () => {
        expect(toolCallId(toolMsg)).toBe('trace_1');
    });
    it('returns null for messages without tool calls', () => {
        expect(toolName(msg({}))).toBeNull();
        expect(toolStatus(msg({}))).toBeNull();
        expect(toolElapsedMs(msg({}))).toBeNull();
        expect(toolCallId(msg({}))).toBeNull();
    });
});

describe('messageIteration / messagePreamble', () => {
    it('extracts iteration number', () => {
        expect(messageIteration(msg({ iteration: 3 }))).toBe(3);
    });
    it('returns null when no iteration', () => {
        expect(messageIteration(msg({}))).toBeNull();
    });
    it('extracts preamble text', () => {
        expect(messagePreamble(msg({ preamble: 'thinking...' }))).toBe('thinking...');
    });
    it('returns null when no preamble', () => {
        expect(messagePreamble(msg({}))).toBeNull();
    });
});

describe('groupByIteration', () => {
    it('groups messages by iteration field', () => {
        const msgs = [
            msg({ id: 'm1', iteration: 1 }),
            msg({ id: 'm2', iteration: 1 }),
            msg({ id: 'm3', iteration: 2 }),
            msg({ id: 'm4' }),  // no iteration → -1
        ];
        const groups = groupByIteration(msgs);
        expect(groups.get(1)).toHaveLength(2);
        expect(groups.get(2)).toHaveLength(1);
        expect(groups.get(-1)).toHaveLength(1);
    });

    it('handles empty array', () => {
        const groups = groupByIteration([]);
        expect(groups.size).toBe(0);
    });
});

describe('messageUIType', () => {
    it('classifies user message', () => {
        expect(messageUIType(msg({ role: 'user' }))).toBe('user');
    });
    it('classifies preamble', () => {
        expect(messageUIType(msg({ role: 'assistant', interim: 1 }))).toBe('preamble');
    });
    it('classifies final response', () => {
        expect(messageUIType(msg({ role: 'assistant', interim: 0 }))).toBe('response');
    });
    it('classifies tool message', () => {
        expect(messageUIType(msg({ role: 'tool' }))).toBe('tool');
    });
    it('classifies summary', () => {
        expect(messageUIType(msg({ status: 'summary' }))).toBe('summary');
    });
    it('classifies summary from mode=summary', () => {
        expect(messageUIType(msg({ mode: 'summary' }))).toBe('summary');
    });
    it('classifies summarized (hidden)', () => {
        expect(messageUIType(msg({ status: 'summarized' }))).toBe('summarized');
    });
    it('classifies elicitation', () => {
        expect(messageUIType(msg({ elicitationId: 'elic_1', status: 'pending' }))).toBe('elicitation');
    });
    it('accepted elicitation is not classified as elicitation', () => {
        expect(messageUIType(msg({ elicitationId: 'elic_1', status: 'accepted' }))).toBe('response');
    });
    it('classifies system message', () => {
        expect(messageUIType(msg({ role: 'system' }))).toBe('system');
    });
    it('classifies archived', () => {
        expect(messageUIType(msg({ archived: 1 }))).toBe('archived');
    });
});
