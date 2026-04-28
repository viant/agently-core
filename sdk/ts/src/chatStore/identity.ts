/**
 * chatStore/identity.ts — render identity, content normalisation, and per-kind
 * matching helpers for the chat-store reducer.
 *
 * Contract references:
 *   - ui-improvement.md §3.1: renderKey is allocated once, opaque, immutable.
 *   - ui-improvement.md §3.2: per-entity-kind match order + fuzzy-match
 *     window of ± 500 ms for unresolved local-origin user entities.
 *   - ui-improvement.md §3.3: bootstrap + echo resolve to the same entity.
 */

import type {
    ClientExecutionPage,
    ClientLifecycleEntry,
    ClientLifecycleEntryKind,
    ClientLinkedConversation,
    ClientModelStep,
    ClientToolCall,
    ClientUserMessage,
} from './types';

// ─── renderKey allocator ──────────────────────────────────────────────────────

/**
 * Produces a fresh opaque renderKey string. The allocator has no visible
 * substructure: callers may assume only that two calls produce distinct
 * strings.
 *
 * Implementation uses crypto.randomUUID where available (browsers + Node 19+,
 * all supported runtimes) with a deterministic fallback for test shims. A
 * small prefix is added so the string is readable when it surfaces in React
 * devtools, but consumers MUST NOT parse it.
 */
export function allocateRenderKey(): string {
    // `crypto` is available in browsers and in Node (globalThis.crypto).
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const g = globalThis as any;
    if (g?.crypto?.randomUUID) return `rk_${g.crypto.randomUUID()}`;
    // Fallback: Math.random-based, only used if no crypto at all (e.g. a
    // custom minimal test runtime). Good enough for unique keys in-process.
    const rand = Math.random().toString(36).slice(2, 10);
    const now = Date.now().toString(36);
    return `rk_${now}_${rand}`;
}

// ─── Content normalisation (pinned for the fuzzy match) ───────────────────────

/**
 * Normalise a text value for the unresolved-local-user fuzzy match in §3.2.
 *
 * Normalisation steps, in order:
 *   1. Guard against non-string input (returns '' for null/undefined/object).
 *   2. Unicode NFC canonicalisation (so equivalent glyphs compare equal).
 *   3. Trim leading and trailing whitespace (Unicode-aware).
 *   4. Collapse internal runs of whitespace (spaces, tabs, newlines) to a
 *      single space.
 *
 * Case-sensitivity is preserved — a user submitting "Hello" and the backend
 * persisting "hello" are different texts. This matches backend storage,
 * which does not canonicalise case.
 */
export function normalizeContent(value: unknown): string {
    if (typeof value !== 'string') return '';
    const nfc = value.normalize('NFC');
    const trimmed = nfc.trim();
    // \s covers ASCII + unicode whitespace in modern JS engines.
    return trimmed.replace(/\s+/g, ' ');
}

// ─── Fuzzy match window ───────────────────────────────────────────────────────

/**
 * Half-width of the fuzzy-match time window for the unresolved-local-user
 * match rule. Pinned at 500 ms per ui-improvement.md §3.2. A backend echo
 * whose `createdAt` is within `± FUZZY_MATCH_WINDOW_MS` of the local submit's
 * `submittedAt` may match; anything outside is treated as distinct.
 */
export const FUZZY_MATCH_WINDOW_MS = 500;

// ─── Time parsing ─────────────────────────────────────────────────────────────

/**
 * Parse an ISO-8601 timestamp into milliseconds since epoch. Returns null on
 * unparseable input; callers use null to mean "no usable timestamp" and
 * therefore "no fuzzy match".
 */
export function parseIsoMs(value: unknown): number | null {
    if (typeof value !== 'string' || value === '') return null;
    const ms = Date.parse(value);
    return Number.isFinite(ms) ? ms : null;
}

// ─── Per-kind match inputs ────────────────────────────────────────────────────

export interface IncomingUserObservation {
    messageId?: string;
    clientRequestId?: string;
    content?: string;
    createdAt?: string;
}

export interface IncomingPageObservation {
    pageId?: string;
}

export interface IncomingToolCallObservation {
    toolCallId?: string;
}

export interface IncomingModelStepObservation {
    modelCallId?: string;
    assistantMessageId?: string;
    /**
     * Positional index within the parent page for the rare case where the
     * step arrives before any id is known and attaches to the last open
     * model step. Callers that don't have a position pass undefined.
     */
    positionHint?: number;
}

export interface IncomingLifecycleEntryObservation {
    kind: ClientLifecycleEntryKind;
    createdAt: string;
}

export interface IncomingLinkedConversationObservation {
    linkedConversationId?: string;
}

// ─── Per-kind match functions ─────────────────────────────────────────────────

/**
 * Match an incoming user-shaped observation to an existing user entity.
 *
 * Order (§3.2):
 *   1. messageId (exact, non-empty)
 *   2. clientRequestId (exact, non-empty)
 *   3. constrained fuzzy match: among unresolved local-origin user entities
 *      whose normalised content matches and whose submittedAt is within
 *      ± FUZZY_MATCH_WINDOW_MS of the incoming createdAt, allow a match only
 *      when EXACTLY ONE candidate qualifies. Zero and two-plus candidates
 *      both mean "no fuzzy match" (the caller must then create a new entity
 *      and emit telemetry).
 *
 * An "unresolved local-origin user entity" is one with a non-empty
 * clientRequestId, an empty messageId, and a submittedAt timestamp.
 *
 * Returns the matched user entity, or null (meaning "caller should create
 * a new entity"). The caller is responsible for distinguishing the
 * telemetry case (ambiguous fuzzy candidate set) from the no-match case.
 */
export function matchUserMessage(
    existing: ReadonlyArray<ClientUserMessage>,
    observation: IncomingUserObservation,
): { matched: ClientUserMessage | null; fuzzyAmbiguous: boolean } {
    // (1) messageId
    const obsMessageId = (observation.messageId ?? '').trim();
    if (obsMessageId) {
        for (const entity of existing) {
            if ((entity.messageId ?? '') === obsMessageId) {
                return { matched: entity, fuzzyAmbiguous: false };
            }
        }
    }

    // (2) clientRequestId
    const obsCrid = (observation.clientRequestId ?? '').trim();
    if (obsCrid) {
        for (const entity of existing) {
            if ((entity.clientRequestId ?? '') === obsCrid) {
                return { matched: entity, fuzzyAmbiguous: false };
            }
        }
    }

    // (3) constrained fuzzy match — only for unresolved local-origin user entities
    const obsContent = normalizeContent(observation.content);
    const obsCreatedMs = parseIsoMs(observation.createdAt);
    if (obsContent === '' || obsCreatedMs === null) {
        return { matched: null, fuzzyAmbiguous: false };
    }

    const candidates: ClientUserMessage[] = [];
    for (const entity of existing) {
        const hasMessageId = (entity.messageId ?? '').trim() !== '';
        const hasClientRequestId = (entity.clientRequestId ?? '').trim() !== '';
        if (hasMessageId) continue;                  // already echoed
        if (!hasClientRequestId) continue;           // not local-origin
        const entityContent = normalizeContent(entity.content);
        if (entityContent !== obsContent) continue;
        const entitySubmittedMs = parseIsoMs(entity.submittedAt);
        if (entitySubmittedMs === null) continue;
        if (Math.abs(entitySubmittedMs - obsCreatedMs) > FUZZY_MATCH_WINDOW_MS) continue;
        candidates.push(entity);
    }

    if (candidates.length === 1) {
        return { matched: candidates[0], fuzzyAmbiguous: false };
    }
    if (candidates.length >= 2) {
        return { matched: null, fuzzyAmbiguous: true };
    }
    return { matched: null, fuzzyAmbiguous: false };
}

/**
 * Match an incoming execution-page observation to an existing page. Pages
 * are keyed solely by `pageId` per §3.2; an observation without `pageId`
 * cannot match and callers fall back to positional rules in the reducer.
 */
export function matchExecutionPage(
    existing: ReadonlyArray<ClientExecutionPage>,
    observation: IncomingPageObservation,
): ClientExecutionPage | null {
    const obsId = (observation.pageId ?? '').trim();
    if (!obsId) return null;
    for (const entity of existing) {
        if ((entity.pageId ?? '') === obsId) return entity;
    }
    return null;
}

/**
 * Match an incoming tool-call observation. Tool calls are keyed by
 * `toolCallId` per §3.2.
 */
export function matchToolCall(
    existing: ReadonlyArray<ClientToolCall>,
    observation: IncomingToolCallObservation,
): ClientToolCall | null {
    const obsId = (observation.toolCallId ?? '').trim();
    if (!obsId) return null;
    for (const entity of existing) {
        if ((entity.toolCallId ?? '') === obsId) return entity;
    }
    return null;
}

/**
 * Match an incoming model-step observation. Model steps are keyed by
 * page-local identity — the tuple (modelCallId) when non-empty, else
 * (assistantMessageId) when non-empty, else by `positionHint` when provided.
 *
 * Two entries with the same non-empty modelCallId are the same entity;
 * two entries with different non-empty modelCallIds are distinct entities.
 *
 * The `positionHint` path is a last-resort attachment for `text_delta`-like
 * events that carry no ids. It is equivalent to "append to the last open
 * model step"; callers pass `positionHint = existing.length - 1` to mean
 * "the last one".
 */
export function matchModelStep(
    existing: ReadonlyArray<ClientModelStep>,
    observation: IncomingModelStepObservation,
): ClientModelStep | null {
    const mcid = (observation.modelCallId ?? '').trim();
    const amid = (observation.assistantMessageId ?? '').trim();
    if (mcid) {
        for (const entity of existing) {
            if ((entity.modelCallId ?? '') === mcid) return entity;
        }
        if (amid) {
            for (const entity of existing) {
                if ((entity.modelCallId ?? '').trim() !== '') continue;
                if ((entity.assistantMessageId ?? '') === amid) return entity;
            }
        }
        // Explicit mcid with no match -> new entity (caller creates).
        return null;
    }
    if (amid) {
        for (const entity of existing) {
            if ((entity.assistantMessageId ?? '') === amid) return entity;
        }
        return null;
    }
    if (typeof observation.positionHint === 'number'
        && observation.positionHint >= 0
        && observation.positionHint < existing.length) {
        return existing[observation.positionHint];
    }
    return null;
}

/**
 * Match an incoming lifecycle-entry observation. Identity is the
 * (kind, createdAt) tuple per §3.2 "stable page-local identity". Two
 * entries with the same kind but different createdAt are two entries.
 */
export function matchLifecycleEntry(
    existing: ReadonlyArray<ClientLifecycleEntry>,
    observation: IncomingLifecycleEntryObservation,
): ClientLifecycleEntry | null {
    for (const entity of existing) {
        if (entity.kind === observation.kind && entity.createdAt === observation.createdAt) {
            return entity;
        }
    }
    return null;
}

/**
 * Match an incoming linked-conversation observation. Keyed by
 * `linkedConversationId` per §3.2.
 */
export function matchLinkedConversation(
    existing: ReadonlyArray<ClientLinkedConversation>,
    observation: IncomingLinkedConversationObservation,
): ClientLinkedConversation | null {
    const obsId = (observation.linkedConversationId ?? '').trim();
    if (!obsId) return null;
    for (const entity of existing) {
        if ((entity.conversationId ?? '') === obsId) return entity;
    }
    return null;
}
