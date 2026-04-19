import { describe, it, expect } from 'vitest';

import {
    FUZZY_MATCH_WINDOW_MS,
    allocateRenderKey,
    matchExecutionPage,
    matchLifecycleEntry,
    matchLinkedConversation,
    matchModelStep,
    matchToolCall,
    matchUserMessage,
    normalizeContent,
    parseIsoMs,
} from '../chatStore/identity';
import type {
    ClientExecutionPage,
    ClientLifecycleEntry,
    ClientLinkedConversation,
    ClientModelStep,
    ClientToolCall,
    ClientUserMessage,
} from '../chatStore/types';

const emptyPage = (
    overrides: Partial<ClientExecutionPage> = {},
): ClientExecutionPage => ({
    renderKey: allocateRenderKey(),
    modelSteps: [],
    toolCalls: [],
    lifecycleEntries: [],
    ...overrides,
});

describe('chatStore/identity — allocateRenderKey', () => {
    it('produces opaque non-empty strings', () => {
        const a = allocateRenderKey();
        const b = allocateRenderKey();
        expect(a).toBeTypeOf('string');
        expect(a.length).toBeGreaterThan(0);
        expect(b).toBeTypeOf('string');
        expect(a).not.toBe(b);
    });

    it('prefixes keys with rk_ for devtools readability (opaque, not for parsing)', () => {
        const key = allocateRenderKey();
        // Prefix check is the only guarantee — consumers MUST NOT parse the suffix.
        expect(key.startsWith('rk_')).toBe(true);
    });
});

describe('chatStore/identity — normalizeContent', () => {
    it('returns empty string for non-string input', () => {
        expect(normalizeContent(null)).toBe('');
        expect(normalizeContent(undefined)).toBe('');
        expect(normalizeContent(123)).toBe('');
        expect(normalizeContent({})).toBe('');
    });

    it('trims leading and trailing whitespace', () => {
        expect(normalizeContent('  hello  ')).toBe('hello');
        expect(normalizeContent('\thello\n')).toBe('hello');
    });

    it('collapses internal whitespace runs to a single space', () => {
        expect(normalizeContent('hello   world')).toBe('hello world');
        expect(normalizeContent('a\t\tb\n\nc')).toBe('a b c');
    });

    it('preserves case (backend is case-preserving)', () => {
        expect(normalizeContent('Hello')).toBe('Hello');
        expect(normalizeContent('hello')).toBe('hello');
    });

    it('applies NFC unicode normalisation', () => {
        // "é" as NFD (e + combining acute) vs NFC (single codepoint)
        const nfd = 'e\u0301';
        const nfc = '\u00e9';
        expect(normalizeContent(nfd)).toBe(normalizeContent(nfc));
    });
});

describe('chatStore/identity — parseIsoMs', () => {
    it('parses valid ISO-8601 timestamps', () => {
        expect(parseIsoMs('2025-01-01T00:00:00.000Z')).toBe(Date.UTC(2025, 0, 1));
    });

    it('returns null for unparseable input', () => {
        expect(parseIsoMs('')).toBeNull();
        expect(parseIsoMs(null)).toBeNull();
        expect(parseIsoMs(undefined)).toBeNull();
        expect(parseIsoMs('not-a-date')).toBeNull();
    });
});

describe('chatStore/identity — matchUserMessage', () => {
    const baseLocal = (overrides: Partial<ClientUserMessage> = {}): ClientUserMessage => ({
        renderKey: allocateRenderKey(),
        role: 'user',
        content: 'hello',
        clientRequestId: 'crid_A',
        submittedAt: '2025-01-01T00:00:00.000Z',
        ...overrides,
    });

    it('matches by messageId first (highest priority)', () => {
        const entity = baseLocal({ messageId: 'msg_1', clientRequestId: 'crid_A' });
        const res = matchUserMessage([entity], {
            messageId: 'msg_1',
            clientRequestId: 'crid_OTHER',
        });
        expect(res.matched).toBe(entity);
        expect(res.fuzzyAmbiguous).toBe(false);
    });

    it('matches by clientRequestId when messageId not given', () => {
        const entity = baseLocal({ clientRequestId: 'crid_A' });
        const res = matchUserMessage([entity], {
            clientRequestId: 'crid_A',
        });
        expect(res.matched).toBe(entity);
    });

    it('fuzzy matches a single unresolved local-origin user by content + time window', () => {
        const entity = baseLocal({
            content: '  hello  ',            // normalises to 'hello'
            submittedAt: '2025-01-01T00:00:00.000Z',
        });
        const res = matchUserMessage([entity], {
            // no messageId, no clientRequestId — falls to fuzzy
            content: 'hello',
            createdAt: '2025-01-01T00:00:00.200Z',     // +200 ms within ±500
        });
        expect(res.matched).toBe(entity);
        expect(res.fuzzyAmbiguous).toBe(false);
    });

    it('does NOT fuzzy-match when createdAt falls outside the ±500 ms window', () => {
        const entity = baseLocal({ submittedAt: '2025-01-01T00:00:00.000Z' });
        const res = matchUserMessage([entity], {
            content: 'hello',
            createdAt: '2025-01-01T00:00:00.600Z',     // +600 ms outside window
        });
        expect(res.matched).toBeNull();
        expect(res.fuzzyAmbiguous).toBe(false);
    });

    it('treats the fuzzy window boundary as inclusive at exactly ±window ms', () => {
        const entity = baseLocal({ submittedAt: '2025-01-01T00:00:00.000Z' });
        const res = matchUserMessage([entity], {
            content: 'hello',
            createdAt: `2025-01-01T00:00:00.${FUZZY_MATCH_WINDOW_MS}Z`,
        });
        expect(res.matched).toBe(entity);
    });

    it('skips entities that already have a messageId (echoed) when fuzzy-matching', () => {
        const echoed = baseLocal({ messageId: 'msg_1', submittedAt: '2025-01-01T00:00:00.000Z' });
        const pending = baseLocal({ clientRequestId: 'crid_B', submittedAt: '2025-01-01T00:00:00.100Z' });
        const res = matchUserMessage([echoed, pending], {
            content: 'hello',
            createdAt: '2025-01-01T00:00:00.150Z',
        });
        expect(res.matched).toBe(pending);
    });

    it('does NOT collapse when two unresolved locals both qualify — returns fuzzyAmbiguous', () => {
        const l1 = baseLocal({
            clientRequestId: 'crid_1',
            submittedAt: '2025-01-01T00:00:00.000Z',
        });
        const l2 = baseLocal({
            clientRequestId: 'crid_2',
            submittedAt: '2025-01-01T00:00:00.200Z',
        });
        const res = matchUserMessage([l1, l2], {
            // Both locals have "hello" within ±500 ms of createdAt=.100
            content: 'hello',
            createdAt: '2025-01-01T00:00:00.100Z',
        });
        expect(res.matched).toBeNull();
        expect(res.fuzzyAmbiguous).toBe(true);
    });

    it('treats differing normalised content as non-matching even within the time window', () => {
        const entity = baseLocal({ content: 'hello', submittedAt: '2025-01-01T00:00:00.000Z' });
        const res = matchUserMessage([entity], {
            content: 'goodbye',
            createdAt: '2025-01-01T00:00:00.100Z',
        });
        expect(res.matched).toBeNull();
        expect(res.fuzzyAmbiguous).toBe(false);
    });

    it('returns null (not ambiguous) when there are zero candidates', () => {
        const res = matchUserMessage([], { content: 'x', createdAt: '2025-01-01T00:00:00Z' });
        expect(res.matched).toBeNull();
        expect(res.fuzzyAmbiguous).toBe(false);
    });
});

describe('chatStore/identity — matchExecutionPage', () => {
    it('matches by pageId', () => {
        const p = emptyPage({ pageId: 'pg_1' });
        expect(matchExecutionPage([p], { pageId: 'pg_1' })).toBe(p);
    });

    it('returns null when pageId is absent', () => {
        const p = emptyPage({ pageId: 'pg_1' });
        expect(matchExecutionPage([p], {})).toBeNull();
    });

    it('returns null on no match', () => {
        const p = emptyPage({ pageId: 'pg_1' });
        expect(matchExecutionPage([p], { pageId: 'pg_2' })).toBeNull();
    });
});

describe('chatStore/identity — matchToolCall', () => {
    const tool = (id: string): ClientToolCall => ({
        renderKey: allocateRenderKey(),
        toolCallId: id,
    });
    it('matches by toolCallId', () => {
        const t = tool('tc_1');
        expect(matchToolCall([t], { toolCallId: 'tc_1' })).toBe(t);
    });
    it('returns null when toolCallId missing or unmatched', () => {
        const t = tool('tc_1');
        expect(matchToolCall([t], {})).toBeNull();
        expect(matchToolCall([t], { toolCallId: 'tc_2' })).toBeNull();
    });
});

describe('chatStore/identity — matchModelStep', () => {
    const step = (overrides: Partial<ClientModelStep> = {}): ClientModelStep => ({
        renderKey: allocateRenderKey(),
        ...overrides,
    });

    it('matches by modelCallId first', () => {
        const s1 = step({ modelCallId: 'mc_1' });
        const s2 = step({ modelCallId: 'mc_2' });
        expect(matchModelStep([s1, s2], { modelCallId: 'mc_2' })).toBe(s2);
    });

    it('explicit modelCallId with no match returns null (does not fall through)', () => {
        const s1 = step({ modelCallId: 'mc_1', assistantMessageId: 'am_1' });
        expect(matchModelStep([s1], { modelCallId: 'mc_2', assistantMessageId: 'am_1' })).toBeNull();
    });

    it('matches by assistantMessageId when modelCallId absent', () => {
        const s1 = step({ assistantMessageId: 'am_1' });
        expect(matchModelStep([s1], { assistantMessageId: 'am_1' })).toBe(s1);
    });

    it('matches by positionHint as a last resort', () => {
        const s1 = step();
        const s2 = step();
        expect(matchModelStep([s1, s2], { positionHint: 1 })).toBe(s2);
        expect(matchModelStep([s1, s2], { positionHint: 99 })).toBeNull();
        expect(matchModelStep([], { positionHint: 0 })).toBeNull();
    });
});

describe('chatStore/identity — matchLifecycleEntry', () => {
    const entry = (
        kind: ClientLifecycleEntry['kind'],
        createdAt: string,
    ): ClientLifecycleEntry => ({
        renderKey: allocateRenderKey(),
        kind,
        createdAt,
    });

    it('matches by (kind, createdAt) tuple', () => {
        const e1 = entry('turn_started', '2025-01-01T00:00:00.000Z');
        const e2 = entry('turn_completed', '2025-01-01T00:00:05.000Z');
        expect(matchLifecycleEntry([e1, e2], { kind: 'turn_completed', createdAt: '2025-01-01T00:00:05.000Z' })).toBe(e2);
    });

    it('differing createdAt with the same kind is a different entry', () => {
        const e = entry('turn_started', '2025-01-01T00:00:00.000Z');
        expect(matchLifecycleEntry([e], { kind: 'turn_started', createdAt: '2025-01-01T00:00:01.000Z' })).toBeNull();
    });
});

describe('chatStore/identity — matchLinkedConversation', () => {
    const linked = (id: string): ClientLinkedConversation => ({
        renderKey: allocateRenderKey(),
        conversationId: id,
    });

    it('matches by conversationId', () => {
        const lc = linked('conv_1');
        expect(matchLinkedConversation([lc], { linkedConversationId: 'conv_1' })).toBe(lc);
    });

    it('returns null when id missing or unmatched', () => {
        const lc = linked('conv_1');
        expect(matchLinkedConversation([lc], {})).toBeNull();
        expect(matchLinkedConversation([lc], { linkedConversationId: 'conv_2' })).toBeNull();
    });
});
