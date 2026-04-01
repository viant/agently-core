import { describe, it, expect } from 'vitest';
import {
    newMessageBuffer, applyEvent, reconcileMessages, reconcileFromTranscript,
} from '../reconcile';
import type { SSEEvent, Message, Turn } from '../types';

describe('newMessageBuffer', () => {
    it('creates empty buffer', () => {
        const buf = newMessageBuffer();
        expect(buf.byId.size).toBe(0);
        expect(buf.activeTurnId).toBeNull();
    });
});

describe('applyEvent', () => {
    it('accumulates chunk content', () => {
        const buf = newMessageBuffer();

        const r1 = applyEvent(buf, {
            id: 'msg_1', streamId: 'conv_1', type: 'text_delta', content: 'Hello ',
        } as SSEEvent);
        expect(r1).toEqual({ id: 'msg_1', content: 'Hello ', final: false });

        const r2 = applyEvent(buf, {
            id: 'msg_1', streamId: 'conv_1', type: 'text_delta', content: 'world',
        } as SSEEvent);
        expect(r2).toEqual({ id: 'msg_1', content: 'Hello world', final: false });
    });

    it('sets activeTurnId on chunk', () => {
        const buf = newMessageBuffer();
        applyEvent(buf, {
            id: 'msg_1', streamId: 'conv_1', type: 'text_delta', content: 'hi',
        } as SSEEvent);
        expect(buf.activeTurnId).toBe('conv_1');
    });

    it('marks done as final', () => {
        const buf = newMessageBuffer();
        applyEvent(buf, {
            id: 'msg_1', streamId: 'conv_1', type: 'text_delta', content: 'text',
        } as SSEEvent);

        const r = applyEvent(buf, {
            id: 'msg_1', streamId: 'conv_1', type: 'turn_completed',
        } as SSEEvent);

        expect(r).toEqual({ id: 'msg_1', content: 'text', final: true });
        expect(buf.activeTurnId).toBeNull();
    });

    it('clears activeTurnId on error', () => {
        const buf = newMessageBuffer();
        applyEvent(buf, {
            id: 'msg_1', streamId: 'conv_1', type: 'text_delta', content: 'hi',
        } as SSEEvent);

        applyEvent(buf, {
            id: 'msg_1', streamId: 'conv_1', type: 'error', error: 'fail',
        } as SSEEvent);

        expect(buf.activeTurnId).toBeNull();
    });

    it('returns null for tool events', () => {
        const buf = newMessageBuffer();
        const r = applyEvent(buf, {
            id: 'msg_1', streamId: 'conv_1', type: 'tool_call_completed', toolName: 'exec',
        } as SSEEvent);
        expect(r).toBeNull();
    });

    it('returns null for events without id', () => {
        const buf = newMessageBuffer();
        const r = applyEvent(buf, { type: 'text_delta', content: 'hi' } as SSEEvent);
        expect(r).toBeNull();
    });

    it('accumulates text_delta content', () => {
        const buf = newMessageBuffer();
        const r1 = applyEvent(buf, {
            id: 'msg_1', streamId: 'conv_1', type: 'text_delta', content: 'Hello ',
        } as SSEEvent);
        expect(r1).toEqual({ id: 'msg_1', content: 'Hello ', final: false });
        const r2 = applyEvent(buf, {
            id: 'msg_1', streamId: 'conv_1', type: 'text_delta', content: 'world',
        } as SSEEvent);
        expect(r2).toEqual({ id: 'msg_1', content: 'Hello world', final: false });
    });

    it('accumulates reasoning_delta into preamble', () => {
        const buf = newMessageBuffer();
        applyEvent(buf, {
            id: 'msg_1', streamId: 'conv_1', type: 'reasoning_delta', content: 'Thinking...',
        } as SSEEvent);
        expect(buf.byId.get('msg_1')?.preamble).toBe('Thinking...');
    });

    it('applies assistant_preamble semantically', () => {
        const buf = newMessageBuffer();
        applyEvent(buf, {
            id: 'msg_1',
            conversationId: 'conv_1',
            turnId: 'turn_1',
            type: 'assistant_preamble',
            content: 'Calling run.',
            status: 'running',
        } as SSEEvent);
        expect(buf.byId.get('msg_1')).toMatchObject({
            id: 'msg_1',
            conversationId: 'conv_1',
            turnId: 'turn_1',
            preamble: 'Calling run.',
            status: 'running',
            interim: 1,
        });
        expect(buf.activeTurnId).toBe('turn_1');
    });

    it('applies assistant_final semantically', () => {
        const buf = newMessageBuffer();
        applyEvent(buf, {
            id: 'msg_1',
            conversationId: 'conv_1',
            turnId: 'turn_1',
            type: 'assistant_preamble',
            content: 'Thinking...',
        } as SSEEvent);
        const result = applyEvent(buf, {
            id: 'msg_1',
            conversationId: 'conv_1',
            turnId: 'turn_1',
            type: 'assistant_final',
            content: 'Final answer',
            status: 'completed',
            finalResponse: true,
        } as SSEEvent);
        expect(result).toEqual({ id: 'msg_1', content: 'Final answer', final: true });
        expect(buf.byId.get('msg_1')).toMatchObject({
            content: 'Final answer',
            status: 'completed',
            interim: 0,
            preamble: 'Thinking...',
        });
    });

    it('marks turn_completed as final', () => {
        const buf = newMessageBuffer();
        applyEvent(buf, {
            id: 'msg_1', streamId: 'conv_1', type: 'text_delta', content: 'done',
        } as SSEEvent);
        const r = applyEvent(buf, {
            id: 'msg_1', streamId: 'conv_1', type: 'turn_completed',
        } as SSEEvent);
        expect(r).toEqual({ id: 'msg_1', content: 'done', final: true });
    });

    it('marks turn_failed with failed status', () => {
        const buf = newMessageBuffer();
        applyEvent(buf, {
            id: 'msg_1', streamId: 'conv_1', type: 'text_delta', content: 'partial',
        } as SSEEvent);
        applyEvent(buf, {
            id: 'msg_1', streamId: 'conv_1', type: 'turn_failed',
        } as SSEEvent);
        expect(buf.byId.get('msg_1')?.status).toBe('failed');
    });

    it('returns null for usage and item_completed', () => {
        const buf = newMessageBuffer();
        expect(applyEvent(buf, { id: 'msg_1', type: 'usage' } as SSEEvent)).toBeNull();
        expect(applyEvent(buf, { id: 'msg_1', type: 'item_completed' } as SSEEvent)).toBeNull();
    });

    it('returns null for tool_call lifecycle events', () => {
        const buf = newMessageBuffer();
        expect(applyEvent(buf, { id: 'msg_1', streamId: 'c1', type: 'tool_call_started', toolName: 'exec' } as SSEEvent)).toBeNull();
        expect(applyEvent(buf, { id: 'msg_1', streamId: 'c1', type: 'tool_call_delta', toolName: 'exec' } as SSEEvent)).toBeNull();
        expect(applyEvent(buf, { id: 'msg_1', streamId: 'c1', type: 'tool_call_completed', toolName: 'exec' } as SSEEvent)).toBeNull();
    });
});

describe('reconcileMessages', () => {
    it('server messages take precedence over buffer', () => {
        const buf = newMessageBuffer();
        buf.byId.set('msg_1', {
            id: 'msg_1', role: 'assistant', content: 'partial...', interim: 1,
        } as Partial<Message>);

        const serverMsgs: Message[] = [
            { id: 'msg_1', conversationId: 'c1', role: 'assistant', type: 'text', content: 'full response', interim: 0, createdAt: '2026-01-01T00:00:00Z' },
        ];

        const merged = reconcileMessages(buf, serverMsgs);
        expect(merged).toHaveLength(1);
        expect(merged[0].content).toBe('full response');
        expect(merged[0].interim).toBe(0);
    });

    it('appends buffered messages not yet on server', () => {
        const buf = newMessageBuffer();
        buf.byId.set('msg_2', {
            id: 'msg_2', role: 'assistant', content: 'streaming...', interim: 1,
            createdAt: '2026-01-01T00:00:02Z',
        } as Partial<Message>);

        const serverMsgs: Message[] = [
            { id: 'msg_1', conversationId: 'c1', role: 'user', type: 'text', content: 'hello', interim: 0, createdAt: '2026-01-01T00:00:01Z' },
        ];

        const merged = reconcileMessages(buf, serverMsgs);
        expect(merged).toHaveLength(2);
        expect(merged[0].id).toBe('msg_1');
        expect(merged[1].id).toBe('msg_2');
    });

    it('sorts by createdAt', () => {
        const buf = newMessageBuffer();
        const serverMsgs: Message[] = [
            { id: 'msg_2', conversationId: 'c1', role: 'assistant', type: 'text', content: 'b', interim: 0, createdAt: '2026-01-01T00:00:02Z' },
            { id: 'msg_1', conversationId: 'c1', role: 'user', type: 'text', content: 'a', interim: 0, createdAt: '2026-01-01T00:00:01Z' },
        ];

        const merged = reconcileMessages(buf, serverMsgs);
        expect(merged[0].id).toBe('msg_1');
        expect(merged[1].id).toBe('msg_2');
    });
});

describe('reconcileFromTranscript', () => {
    it('updates buffer from transcript turns', () => {
        const buf = newMessageBuffer();

        const turns: Turn[] = [{
            id: 'turn_1',
            conversationId: 'c1',
            status: 'completed',
            createdAt: '2026-01-01T00:00:00Z',
            message: [
                { id: 'msg_1', conversationId: 'c1', role: 'assistant', type: 'text', content: 'server content', interim: 0, createdAt: '2026-01-01T00:00:01Z' },
                { id: 'msg_2', conversationId: 'c1', role: 'user', type: 'text', content: 'user msg', interim: 0, createdAt: '2026-01-01T00:00:00Z' },
            ],
        }];

        reconcileFromTranscript(buf, turns);
        expect(buf.byId.get('msg_1')?.content).toBe('server content');
        // User messages should not be buffered
        expect(buf.byId.has('msg_2')).toBe(false);
    });
});
