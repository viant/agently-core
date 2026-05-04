/**
 * chatStore/reducer.ts — the one reducer.
 *
 * Three mutation entry points, one canonical state:
 *
 *   applyLocalSubmit(state, submit)       — bootstrap a local user message
 *   applyEvent(state, event)              — live SSE event
 *   applyTranscript(state, snapshot)      — persisted api.ConversationState
 *
 * All three operate over the same `ClientConversationState`. Per-field
 * provenance lives in a private WeakMap so the reducer is the sole writer
 * and consumers never see provenance metadata in the projected output.
 *
 * ─── Active-turn SSE contract (MUST READ before editing) ───────────────
 *
 * The server emits SEMANTIC events only. Raw DB column diffs
 * (`control:message_add`, `control:message_patch`) are NOT carried on
 * the live wire anymore — they were the transcript/SSE mixing regression
 * that kept breaking bubble rendering.
 *
 * The entire assistant-content surface is covered by exactly TWO events:
 *
 *   `narration`  — ephemeral interim commentary (pre-tool-call framing,
 *                  async narrator progress updates, reasoning accumulation).
 *                  Reuses the NEXT assistant message's bubble: the event
 *                  carries the upcoming assistant message id; the reducer
 *                  upserts a bubble at that id with `interim=1` and the
 *                  narration text in `content`. When the real assistant
 *                  message arrives with the SAME id, it replaces the
 *                  content and flips `interim=0`. Narration never gets
 *                  a bubble of its own — it inhabits the bubble of the
 *                  assistant message that's on its way.
 *
 *   `assistant`  — a real turn message (role=assistant OR role=user,
 *                  carried via `patch.role`). Idempotent by messageId.
 *                  Routing:
 *                    • page-tied (event carries `pageId` or non-zero
 *                      `iteration`)     → write into page.content /
 *                                          page.finalAssistantMessageId;
 *                                          rendered as an iteration block.
 *                    • standalone       → upsert into turn.messages (or
 *                                          turn.users when role=user);
 *                                          rendered as a bubble.
 *
 * ─── No "final message" ─────────────────────────────────────────────────
 *
 * There is NO notion of "final assistant message" at the event layer.
 * A turn may carry any number of assistant messages; none is "the final".
 * End-of-turn is signaled ONLY by `turn_completed` / `turn_failed` /
 * `turn_canceled`. This distinction has bitten us repeatedly
 * ("which is the final?", "why does this bubble disappear?", "why is
 * the final flag on an intermediate message?") — keep it dead.
 *
 * ─── Past turns ────────────────────────────────────────────────────────
 *
 * Past-turn content flows through `applyTranscript` from the canonical
 * snapshot, populating the same client-state fields. Active-turn SSE
 * events and transcript snapshots write the same shapes so the UI never
 * has to branch on source.
 *
 * Contract references:
 *   ui-improvement.md §4.1 (pending bootstrap), §4.2 (transcript-created
 *   turns), §4.3 (phase vs lifecycle), §4.4 (latest turn is monotonic),
 *   §5.0–§5.6 (merge contract).
 *   doc/async.md (end-of-turn signaling, narration pairing).
 */

import {
    allocateRenderKey,
    matchExecutionPage,
    matchLifecycleEntry,
    matchLinkedConversation,
    matchModelStep,
    matchToolCall,
    matchUserMessage,
    normalizeContent,
} from './identity';
import { isLiveLifecycle, isTerminalLifecycle, statusToLifecycle } from './lifecycle';
import type {
    CanonicalConversationState,
    CanonicalExecutionPageState,
    CanonicalLifecycleEntryState,
    CanonicalLinkedConversationState,
    CanonicalModelStepState,
    CanonicalToolStepState,
    CanonicalTurnMessageState,
    CanonicalTurnState,
    CanonicalUserMessageState,
    ClientAssistantFinal,
    ClientAssistantNarration,
    ClientConversationState,
    ClientElicitation,
    ClientExecutionPage,
    ClientExecutionPhase,
    ClientLifecycle,
    ClientLifecycleEntry,
    ClientLifecycleEntryKind,
    ClientLinkedConversation,
    ClientModelStep,
    ClientStandaloneMessage,
    ClientToolCall,
    ClientTurnState,
    ClientUserMessage,
    LocalSubmit,
    Provenance,
    ProvenanceMap,
} from './types';
import type { SSEEvent } from '../types';

// ─── Provenance tracking (private) ────────────────────────────────────────────

const provenanceByEntity: WeakMap<object, ProvenanceMap> = new WeakMap();

function provenanceFor(entity: object): ProvenanceMap {
    let map = provenanceByEntity.get(entity);
    if (!map) {
        map = {};
        provenanceByEntity.set(entity, map);
    }
    return map;
}

/** Read a field's current provenance; `null` if unset. */
export function getFieldProvenance(entity: object, field: string): Provenance {
    return provenanceByEntity.get(entity)?.[field] ?? null;
}

// ─── Effective-write gate (§5.2) ──────────────────────────────────────────────

/**
 * A write is effective iff the value carries meaningful data for its field.
 * Non-effective writes are no-ops and do not change provenance.
 *
 * Per-type rules:
 *   - scalar string: non-null, non-undefined, non-empty after trim
 *   - boolean:       any defined boolean (true/false both meaningful)
 *   - number:        any finite defined number
 *   - array:         non-null/non-undefined (empty array marks "observed")
 *   - object:        non-null/non-undefined
 *   - null / undefined: never effective
 */
export function isEffectiveValue(value: unknown): boolean {
    if (value === null || value === undefined) return false;
    if (typeof value === 'string') return value.trim() !== '';
    if (typeof value === 'number') return Number.isFinite(value);
    if (typeof value === 'boolean') return true;
    if (Array.isArray(value)) return true;    // empty array is "observed"
    if (typeof value === 'object') return true;
    return false;
}

// ─── The one write rule (§5.3) ────────────────────────────────────────────────

type WriteSource = 'local' | 'event' | 'transcript';

/**
 * Attempt to write `value` into `entity[field]` from `source`. Returns true
 * iff a write occurred (the field value may have changed and provenance
 * may have advanced); false if the write was rejected.
 *
 * Rules (ui-improvement.md §5.3, §5.4):
 *   1. Non-effective values are no-ops.
 *   2. Source precedence:
 *        - 'local' writes only when provenance is null (bootstrap only).
 *        - 'event' always writes (and supersedes local and transcript).
 *        - 'transcript' writes only when provenance ∈ {null, 'transcript'};
 *          it may not overwrite event-owned fields (latest or historical).
 *   3. Provenance is updated to the source that actually wrote.
 *
 * `setProvenance` is exported so structural inserts can mark fresh entities
 * with the source that introduced them.
 */
export function writeField<E extends object, K extends keyof E & string>(
    entity: E,
    field: K,
    value: E[K],
    source: WriteSource,
): boolean {
    if (!isEffectiveValue(value)) return false;
    const map = provenanceFor(entity);
    const current = map[field] ?? null;

    if (source === 'local') {
        if (current !== null) return false;       // never overwrite anything with local
        (entity as Record<string, unknown>)[field] = value as unknown;
        map[field] = 'local';
        return true;
    }
    if (source === 'event') {
        (entity as Record<string, unknown>)[field] = value as unknown;
        map[field] = 'event';
        return true;
    }
    // source === 'transcript'
    if (current === 'event') return false;        // live always wins
    (entity as Record<string, unknown>)[field] = value as unknown;
    map[field] = 'transcript';
    return true;
}

/** Record a provenance marker for a field without changing its value.
 *  Used when a field is populated at entity creation time and we need to
 *  remember which source introduced it. */
export function setFieldProvenance(entity: object, field: string, prov: Provenance): void {
    const map = provenanceFor(entity);
    if (prov === null) {
        delete map[field];
    } else {
        map[field] = prov;
    }
}

// ─── State construction helpers ────────────────────────────────────────────────

export function newConversationState(conversationId: string): ClientConversationState {
    return { conversationId, turns: [] };
}

function newPendingTurn(submit: LocalSubmit): ClientTurnState {
    const turn: ClientTurnState = {
        renderKey: allocateRenderKey(),
        turnId: '',
        lifecycle: 'pending',
        users: [],
        messages: [],
        pages: [],
        linkedConversations: [],
        createdAt: submit.createdAt,
    };
    // Provenance for the fields we explicitly set.
    setFieldProvenance(turn, 'lifecycle', 'local');
    if (submit.createdAt) setFieldProvenance(turn, 'createdAt', 'local');
    return turn;
}

function newServerTurn(source: WriteSource, turnId: string, lifecycle: ClientLifecycle, createdAt?: string): ClientTurnState {
    const turn: ClientTurnState = {
        renderKey: allocateRenderKey(),
        turnId,
        lifecycle,
        users: [],
        messages: [],
        pages: [],
        linkedConversations: [],
        createdAt,
    };
    if (turnId !== '') setFieldProvenance(turn, 'turnId', source);
    setFieldProvenance(turn, 'lifecycle', source);
    if (createdAt) setFieldProvenance(turn, 'createdAt', source);
    return turn;
}

// ─── Turn lookup ───────────────────────────────────────────────────────────────

/**
 * Find an existing turn in state by turnId. Returns null when turnId is
 * empty (callers must then use other lookup paths or create a new turn).
 */
function findTurnByTurnId(state: ClientConversationState, turnId: string): ClientTurnState | null {
    if (!turnId) return null;
    for (const turn of state.turns) {
        if (turn.turnId === turnId) return turn;
    }
    return null;
}

/**
 * Find an existing turn that has a pending user message matching
 * `clientRequestId`. This is the bootstrap coalescence path for the
 * first server event carrying the original clientRequestId back.
 */
function findTurnByPendingClientRequestId(
    state: ClientConversationState,
    clientRequestId: string,
): ClientTurnState | null {
    if (!clientRequestId) return null;
    for (const turn of state.turns) {
        if (turn.turnId) continue;                 // already promoted to a real turnId
        for (const user of turn.users) {
            if ((user.clientRequestId ?? '') === clientRequestId) return turn;
        }
    }
    return null;
}

function findSinglePendingBootstrapTurn(
    state: ClientConversationState,
): ClientTurnState | null {
    const pending = state.turns.filter((turn) => isLiveLifecycle(turn.lifecycle) && (turn.turnId ?? '') === '');
    return pending.length === 1 ? pending[0] : null;
}

// ─── applyLocalSubmit (§4.1 pending bootstrap) ────────────────────────────────

/**
 * Bootstrap one local user message.
 *
 * `mode: "submit"` (default)
 *   Creates a fresh pending turn. A second normal submit while another turn
 *   is live becomes a queued follow-up turn, not a steering injection.
 *
 * `mode: "steer"`
 *   Appends the user message to the latest live turn (pending/running). If no
 *   live turn exists, falls back to creating a fresh pending turn.
 *
 * Rejects duplicate `clientRequestId` submissions with an Error — per
 * §3.2 "applyLocalSubmit duplicate-clientRequestId throw".
 */
export function applyLocalSubmit(
    state: ClientConversationState,
    submit: LocalSubmit,
): ClientConversationState {
    if (submit.conversationId !== state.conversationId) {
        throw new Error(
            `applyLocalSubmit: submit.conversationId ${submit.conversationId} ≠ state.conversationId ${state.conversationId}`,
        );
    }
    if (!submit.clientRequestId) {
        throw new Error('applyLocalSubmit: clientRequestId is required');
    }
    // Duplicate clientRequestId check — contract forbids silent merge here.
    for (const turn of state.turns) {
        for (const user of turn.users) {
            if ((user.clientRequestId ?? '') === submit.clientRequestId) {
                throw new Error(
                    `applyLocalSubmit: duplicate clientRequestId ${submit.clientRequestId}`,
                );
            }
        }
    }

    const mode = submit.mode === 'steer' ? 'steer' : 'submit';

    // Locate target turn only for explicit steering.
    let target: ClientTurnState | null = null;
    if (mode === 'steer') {
        for (let i = state.turns.length - 1; i >= 0; i -= 1) {
            const t = state.turns[i];
            if (isLiveLifecycle(t.lifecycle)) {
                target = t;
                break;
            }
        }
    }
    if (!target) {
        target = newPendingTurn(submit);
        state.turns.push(target);
    }

    const user: ClientUserMessage = {
        renderKey: allocateRenderKey(),
        role: 'user',
        content: submit.content,
        clientRequestId: submit.clientRequestId,
        submittedAt: submit.createdAt,
        createdAt: submit.createdAt,
    };
    if (submit.content) setFieldProvenance(user, 'content', 'local');
    if (submit.clientRequestId) setFieldProvenance(user, 'clientRequestId', 'local');
    if (submit.createdAt) {
        setFieldProvenance(user, 'submittedAt', 'local');
        setFieldProvenance(user, 'createdAt', 'local');
    }
    target.users.push(user);
    return state;
}

// ─── applyEvent (§5.0) ─────────────────────────────────────────────────────────

/**
 * Apply one live SSE event to state. The event shape is the backend
 * `streaming.Event`; no repackaging.
 *
 * Routing:
 *   - turn_started / turn_completed / turn_failed / turn_canceled
 *     → find or create turn, update lifecycle, append lifecycleEntries[]
 *   - model_started / model_completed → find/create page + model step
 *   - tool_call_* → find/create page + tool call
 *   - narration / assistant → set narration / append content
 *   - text_delta / reasoning_delta / tool_call_delta → append to last open
 *     compatible accumulator within the turn's current page (positional)
 *   - elicitation_* → set turn-level elicitation
 *   - linked_conversation_attached → append linked conversation
 *   - other event types are no-ops for now (best-effort parity)
 *
 * §4.3: assistant_* / model_* / tool_call_* never change turn lifecycle.
 */
export function applyEvent(
    state: ClientConversationState,
    event: SSEEvent,
): ClientConversationState {
    if ((event.conversationId ?? '') && event.conversationId !== state.conversationId) {
        // Different conversation — ignored.
        return state;
    }

    switch (event.type) {
        case 'turn_started':
            return onTurnStarted(state, event);
        case 'turn_completed':
        case 'turn_failed':
        case 'turn_canceled':
            return onTurnTerminal(state, event);
        case 'model_started':
            return onModelStarted(state, event);
        case 'model_completed':
            return onModelCompleted(state, event);
        case 'tool_call_started':
        case 'tool_call_waiting':
        case 'tool_call_completed':
        case 'tool_call_failed':
        case 'tool_call_canceled':
            return onToolCallEvent(state, event);
        case 'narration':
            return onAssistantNarration(state, event);
        case 'assistant':
            return onAssistantMessage(state, event);
        // `message_appended` is a transitional wire-name alias that
        // routes to the same handler — real turn message, upserted
        // by messageId.
        case 'message_appended':
            return onAssistantMessage(state, event);
        case 'text_delta':
            return onTextDelta(state, event);
        case 'reasoning_delta':
            return onReasoningDelta(state, event);
        case 'tool_call_delta':
            return onToolCallDelta(state, event);
        case 'elicitation_requested':
        case 'elicitation_resolved':
            return onElicitation(state, event);
        case 'linked_conversation_attached':
            return onLinkedConversationAttached(state, event);
        case 'control':
            // `control:message_patch` / `control:message_add` retired
            // (raw DB column diffs on the wire were the transcript/SSE
            // mixing regression). Non-message control ops (turn_started,
            // turn_completed, etc.) have dedicated semantic events; the
            // control type now receives nothing routed by the reducer.
            return state;
        default:
            return state;
    }
}

/** Resolve or create the target turn for a server event. */
function resolveEventTurn(
    state: ClientConversationState,
    event: SSEEvent,
): ClientTurnState | null {
    const turnId = (event.turnId ?? '').trim();

    // (1) Direct turnId match.
    if (turnId) {
        const hit = findTurnByTurnId(state, turnId);
        if (hit) return hit;
    }

    // (2) Bootstrap coalescence: an SSE event with userMessageId +
    // clientRequestId on turn_started may promote a pending local turn.
    // The userMessageId is carried as `event.userMessageId` on the
    // canonical streaming.Event shape.
    if (event.type === 'turn_started') {
        const crid = extractClientRequestId(event);
        if (crid) {
            const target = findTurnByPendingClientRequestId(state, crid);
            if (target) return target;
        }
        const bootstrapTurn = findSinglePendingBootstrapTurn(state);
        if (bootstrapTurn) return bootstrapTurn;
    }

    // (2b) Later SSE events may carry the real turnId even when the initial
    // turn_started control event did not. If there is exactly one pending
    // bootstrap turn, promote that same logical turn instead of creating a
    // second turn row.
    if (turnId) {
        const bootstrapTurn = findSinglePendingBootstrapTurn(state);
        if (bootstrapTurn) return bootstrapTurn;
    }

    // (3) No existing turn and no bootstrap match — for turn_started we
    // create a fresh server-origin turn. Other events without a turnId
    // have nowhere to attach; we fall through to null.
    if (event.type === 'turn_started' && turnId) {
        const created = newServerTurn('event', turnId, 'running', event.createdAt);
        state.turns.push(created);
        return created;
    }

    // Other events: if turnId is present but unknown, create a running turn
    // (server is ahead of us — conservative catch-up).
    if (turnId) {
        const created = newServerTurn('event', turnId, 'running', event.createdAt);
        state.turns.push(created);
        return created;
    }
    return null;
}

/** Extract clientRequestId from an SSE event. The canonical field isn't in
 *  the current TS SSEEvent shape; server emits it on turn_started. We read
 *  defensively so adding the field later is non-breaking. */
function extractClientRequestId(event: SSEEvent): string {
    const raw = (event as unknown as { clientRequestId?: string }).clientRequestId;
    return typeof raw === 'string' ? raw.trim() : '';
}

// ─── Event handlers ────────────────────────────────────────────────────────────

function onTurnStarted(state: ClientConversationState, event: SSEEvent): ClientConversationState {
    const turn = resolveEventTurn(state, event);
    if (!turn) return state;

    // Fill turnId on the turn (pending → running promotion).
    const eventTurnId = (event.turnId ?? '').trim();
    if (eventTurnId && turn.turnId === '') {
        writeField(turn, 'turnId', eventTurnId, 'event');
    }
    writeField(turn, 'lifecycle', 'running', 'event');
    if (event.createdAt) writeField(turn, 'createdAt', event.createdAt, 'event');

    // Coalesce a pending local user whose clientRequestId matches the event's.
    const crid = extractClientRequestId(event);
    const userMessageId = ((event as unknown as { userMessageId?: string }).userMessageId ?? '').trim();
    if (crid || userMessageId) {
        const userObservation = {
            messageId: userMessageId,
            clientRequestId: crid,
            content: (event as unknown as { content?: string }).content,
            createdAt: event.createdAt,
        };
        const res = matchUserMessage(turn.users, userObservation);
        if (res.matched) {
            if (userMessageId) writeField(res.matched, 'messageId', userMessageId, 'event');
            if (event.createdAt) writeField(res.matched, 'createdAt', event.createdAt, 'event');
        }
    }

    // Record the lifecycle entry on a synthetic round-0 page where turn-level
    // lifecycle anchors (§2.5, §6.4). If no page exists yet, create a
    // lightweight anchor page.
    const page = ensureAnchorPageForLifecycle(turn, event, 'event');
    appendLifecycleEntry(page, {
        kind: 'turn_started',
        createdAt: event.createdAt ?? new Date().toISOString(),
    }, 'event');
    return state;
}

function onTurnTerminal(state: ClientConversationState, event: SSEEvent): ClientConversationState {
    const turn = resolveEventTurn(state, event);
    if (!turn) return state;

    const kind: ClientLifecycleEntryKind =
        event.type === 'turn_completed' ? 'turn_completed' :
        event.type === 'turn_failed' ? 'turn_failed' :
        'turn_canceled';
    const lifecycle: ClientLifecycle =
        event.type === 'turn_completed' ? 'completed' :
        event.type === 'turn_failed' ? 'failed' :
        'cancelled';

    writeField(turn, 'lifecycle', lifecycle, 'event');
    if (event.createdAt) writeField(turn, 'updatedAt', event.createdAt, 'event');

    const page = ensureAnchorPageForLifecycle(turn, event, 'event');
    appendLifecycleEntry(page, {
        kind,
        createdAt: event.createdAt ?? new Date().toISOString(),
        status: event.status,
        errorMessage: event.error,
    }, 'event');
    return state;
}

function ensureAnchorPageForLifecycle(
    turn: ClientTurnState,
    event: SSEEvent,
    source: WriteSource,
): ClientExecutionPage {
    // Prefer the last existing page. If none exist, create an anchor page
    // with iteration=0 and phase='main' so lifecycle entries have a home.
    if (turn.pages.length > 0) return turn.pages[turn.pages.length - 1];
    const page: ClientExecutionPage = {
        renderKey: allocateRenderKey(),
        pageId: event.turnId ? `${event.turnId}:anchor` : undefined,
        iteration: 0,
        phase: 'main',
        modelSteps: [],
        toolCalls: [],
        lifecycleEntries: [],
    };
    setFieldProvenance(page, 'iteration', source);
    turn.pages.push(page);
    return page;
}

function appendLifecycleEntry(
    page: ClientExecutionPage,
    observation: { kind: ClientLifecycleEntryKind; createdAt: string; status?: string; errorMessage?: string },
    source: WriteSource,
): ClientLifecycleEntry {
    const matched = matchLifecycleEntry(page.lifecycleEntries, observation);
    if (matched) {
        if (observation.status) writeField(matched, 'status', observation.status, source);
        if (observation.errorMessage) writeField(matched, 'errorMessage', observation.errorMessage, source);
        return matched;
    }
    const entry: ClientLifecycleEntry = {
        renderKey: allocateRenderKey(),
        kind: observation.kind,
        createdAt: observation.createdAt,
        status: observation.status,
        errorMessage: observation.errorMessage,
    };
    setFieldProvenance(entry, 'kind', source);
    setFieldProvenance(entry, 'createdAt', source);
    if (observation.status) setFieldProvenance(entry, 'status', source);
    if (observation.errorMessage) setFieldProvenance(entry, 'errorMessage', source);
    page.lifecycleEntries.push(entry);
    return entry;
}

function findPageByIteration(
    turn: ClientTurnState,
    iteration: number | undefined,
): ClientExecutionPage | null {
    if (typeof iteration !== 'number') return null;
    for (const page of turn.pages) {
        if (page.iteration === iteration) return page;
    }
    return null;
}

function ensurePageForEvent(turn: ClientTurnState, event: SSEEvent, source: WriteSource): ClientExecutionPage {
    const pageId = ((event as unknown as { pageId?: string }).pageId ?? '').trim();
    const normalizedPhase = deriveExecutionPhase(event.phase, event.mode);
    const normalizedRole = executionRoleFromSignals((event as any).executionRole, event.phase, event.mode, event.toolName);
    const isBootstrapEvent = normalizedPhase === 'bootstrap' || normalizedRole === 'bootstrap';
    if (pageId) {
        const matched = matchExecutionPage(turn.pages, { pageId });
        if (matched) {
            // Fill late-arriving pageId provenance on an anchor if needed.
            writeField(matched, 'pageId', pageId, source);
            if (typeof event.iteration === 'number') {
                writeField(matched, 'iteration', event.iteration, source);
            }
            return matched;
        }
    }
    if (!pageId) {
        const iterationMatched = findPageByIteration(turn, event.iteration);
        if (iterationMatched) {
            return iterationMatched;
        }
    }
    if (isBootstrapEvent && typeof event.iteration !== 'number' && turn.pages.length > 0) {
        const last = turn.pages[turn.pages.length - 1];
        if (String(last?.phase || '').trim().toLowerCase() === 'bootstrap') {
            return last;
        }
    }
    if (!isBootstrapEvent && typeof event.iteration !== 'number' && turn.pages.length > 0) {
        const last = turn.pages[turn.pages.length - 1];
        if (String(last?.phase || '').trim().toLowerCase() !== 'bootstrap') {
            return last;
        }
    }
    // Reuse the last page if it's an anchor without real model/tool content
    // yet — the lifecycle-only anchor created by onTurnStarted merges into
    // the first real model round.
    if (!isBootstrapEvent && turn.pages.length > 0) {
        const last = turn.pages[turn.pages.length - 1];
        if (String(last?.phase || '').trim().toLowerCase() !== 'bootstrap') {
            const isAnchor = last.modelSteps.length === 0 && last.toolCalls.length === 0;
            if (isAnchor && pageId) {
                writeField(last, 'pageId', pageId, source);
                if (typeof event.iteration === 'number') {
                    writeField(last, 'iteration', event.iteration, source);
                }
                return last;
            }
            if (isAnchor && !pageId) {
                if (typeof event.iteration === 'number') {
                    writeField(last, 'iteration', event.iteration, source);
                }
                return last;
            }
        }
    }
    const created: ClientExecutionPage = {
        renderKey: allocateRenderKey(),
        pageId: pageId || undefined,
        iteration: typeof event.iteration === 'number'
            ? event.iteration
            : (isBootstrapEvent ? undefined : turn.pages.length),
        executionRole: normalizedRole,
        phase: normalizedPhase,
        modelSteps: [],
        toolCalls: [],
        lifecycleEntries: [],
        createdAt: event.createdAt,
    };
    if (pageId) setFieldProvenance(created, 'pageId', source);
    if (typeof created.iteration === 'number') setFieldProvenance(created, 'iteration', source);
    if (normalizedPhase !== 'main') setFieldProvenance(created, 'phase', source);
    if (created.executionRole) setFieldProvenance(created, 'executionRole', source);
    if (event.createdAt) setFieldProvenance(created, 'createdAt', source);
    turn.pages.push(created);
    return created;
}

function normalisePhase(raw: string | undefined): ClientExecutionPhase {
    const text = String(raw || '').trim().toLowerCase();
    if (text === 'intake' || text === 'sidecar' || text === 'summary' || text === 'bootstrap' || text === 'main') {
        return text;
    }
    return 'main';
}

function deriveExecutionPhase(raw: string | undefined, mode: string | undefined): ClientExecutionPhase {
    const normalized = normalisePhase(raw);
    if (normalized !== 'main') return normalized;
    const normalizedMode = String(mode || '').trim().toLowerCase();
    if (normalizedMode === 'systemcontext') return 'bootstrap';
    return normalized;
}

function normaliseExecutionRole(raw: string | undefined): string {
    const text = String(raw || '').trim().toLowerCase();
    if (text === 'react' || text === 'intake' || text === 'narrator' || text === 'router' || text === 'summary' || text === 'worker' || text === 'bootstrap') {
        return text;
    }
    return '';
}

function payloadMetadataValue(payload: unknown): Record<string, unknown> | null {
    if (!payload || typeof payload !== 'object' || Array.isArray(payload)) return null;
    const options = (payload as Record<string, unknown>).options;
    if (options && typeof options === 'object' && !Array.isArray(options)) {
        const metadata = (options as Record<string, unknown>).metadata;
        if (metadata && typeof metadata === 'object' && !Array.isArray(metadata)) {
            return metadata as Record<string, unknown>;
        }
    }
    const metadata = (payload as Record<string, unknown>).metadata;
    if (metadata && typeof metadata === 'object' && !Array.isArray(metadata)) {
        return metadata as Record<string, unknown>;
    }
    return null;
}

function metadataHasAsyncNarrator(...payloads: unknown[]): boolean {
    return payloads.some((payload) => payloadMetadataValue(payload)?.asyncNarrator === true);
}

function executionRoleFromSignals(explicit: string | undefined, phase: string | undefined, mode: string | undefined, toolName = '', ...payloads: unknown[]): string {
    const normalized = normaliseExecutionRole(explicit);
    if (normalized) return normalized;
    if (metadataHasAsyncNarrator(...payloads)) return 'narrator';
    const normalizedPhase = String(phase || '').trim().toLowerCase();
    if (normalizedPhase === 'intake') return 'intake';
    if (normalizedPhase === 'summary') return 'summary';
    if (normalizedPhase === 'bootstrap') return 'bootstrap';
    const normalizedMode = String(mode || '').trim().toLowerCase();
    if (normalizedMode === 'systemcontext') return 'bootstrap';
    if (normalizedMode === 'router') return 'router';
    if (normalizedMode === 'summary') return 'summary';
    const normalizedTool = String(toolName || '').trim().toLowerCase();
    if (normalizedTool.startsWith('llm/agents:') || normalizedTool.startsWith('llm/agents/')) return 'worker';
    return 'react';
}

function onModelStarted(state: ClientConversationState, event: SSEEvent): ClientConversationState {
    const turn = resolveEventTurn(state, event);
    if (!turn) return state;
    const eventTurnId = (event.turnId ?? '').trim();
    if (eventTurnId && turn.turnId === '') {
        writeField(turn, 'turnId', eventTurnId, 'event');
    }
    const page = ensurePageForEvent(turn, event, 'event');

    const modelCallId = ((event as unknown as { modelCallId?: string }).modelCallId ?? '').trim();
    // Assistant-content events (model_started, model_completed, narration,
    // assistant, text_delta, reasoning_delta, tool_calls_planned)
    // carry the assistant-bubble id in `messageId`. The legacy
    // `assistantMessageId` field is preserved as a fallback during the
    // transition — once all emitters drop it from assistant-content
    // emissions the fallback can be removed. Tool events still populate
    // `assistantMessageId` distinctly (parent bubble vs tool row); that
    // path is unaffected.
    const assistantMessageId = ((event.messageId ?? event.assistantMessageId) ?? '').trim();
    const matched = matchModelStep(page.modelSteps, {
        modelCallId,
        assistantMessageId,
    });
    const step = matched ?? appendModelStep(page, 'event');
    if (modelCallId) writeField(step, 'modelCallId', modelCallId, 'event');
    if (assistantMessageId) writeField(step, 'assistantMessageId', assistantMessageId, 'event');
    writeField(step, 'executionRole', executionRoleFromSignals((event as any).executionRole, event.phase, event.mode), 'event');
    const derivedPhase = deriveExecutionPhase(event.phase, event.mode);
    if (derivedPhase && derivedPhase !== 'main') writeField(step, 'phase', derivedPhase, 'event');
    if (event.provider ?? event.model?.provider) {
        writeField(step, 'provider', event.provider ?? event.model?.provider ?? '', 'event');
    }
    if (event.modelName ?? event.model?.model) {
        writeField(step, 'model', event.modelName ?? event.model?.model ?? '', 'event');
    }
    writeField(step, 'status', 'running', 'event');
    if (event.startedAt) writeField(step, 'startedAt', event.startedAt, 'event');
    // If an explicit phase is on the event, carry it on the page too.
    if (derivedPhase !== 'main') writeField(page, 'phase', derivedPhase, 'event');
    return state;
}

function onModelCompleted(state: ClientConversationState, event: SSEEvent): ClientConversationState {
    const turn = resolveEventTurn(state, event);
    if (!turn) return state;
    const eventTurnId = (event.turnId ?? '').trim();
    if (eventTurnId && turn.turnId === '') {
        writeField(turn, 'turnId', eventTurnId, 'event');
    }
    const page = ensurePageForEvent(turn, event, 'event');
    const modelCallId = ((event as unknown as { modelCallId?: string }).modelCallId ?? '').trim();
    // Prefer messageId (canonical); fall back to assistantMessageId.
    const assistantMessageId = ((event.messageId ?? event.assistantMessageId) ?? '').trim();
    const matched = matchModelStep(page.modelSteps, { modelCallId, assistantMessageId, positionHint: page.modelSteps.length - 1 });
    const step = matched ?? appendModelStep(page, 'event');
    const explicitFinalResponse = Boolean(event.finalResponse);
    const executionRole = executionRoleFromSignals((event as any).executionRole, event.phase, event.mode);
    if (modelCallId) writeField(step, 'modelCallId', modelCallId, 'event');
    if (assistantMessageId) writeField(step, 'assistantMessageId', assistantMessageId, 'event');
    const derivedPhase = deriveExecutionPhase(event.phase, event.mode);
    writeField(step, 'executionRole', executionRole, 'event');
    if (derivedPhase && derivedPhase !== 'main') writeField(step, 'phase', derivedPhase, 'event');
    writeField(step, 'status', event.status ?? 'completed', 'event');
    // Do not infer "final" from model_completed alone. Some providers emit a
    // completion record with content before the turn itself is terminal or
    // before progressive text_delta streaming has fully played out. Only an
    // explicit finalResponse marker should flip the page into final-answer
    // mode. When there is no progressive page content yet, we still accept
    // model_completed content as a fallback detail payload.
    const hidesInterimModelContent = !explicitFinalResponse
        && (derivedPhase === 'intake' || executionRole === 'router');
    if (event.content && !hidesInterimModelContent && (explicitFinalResponse || !String(page.content ?? '').trim())) {
        writeField(page, 'content', event.content, 'event');
    }
    if (explicitFinalResponse && assistantMessageId) writeField(page, 'finalAssistantMessageId', assistantMessageId, 'event');
    if (explicitFinalResponse) writeField(page, 'finalResponse', true, 'event');
    if (event.requestPayloadId) writeField(step, 'requestPayloadId', event.requestPayloadId, 'event');
    if (event.responsePayloadId) writeField(step, 'responsePayloadId', event.responsePayloadId, 'event');
    if (event.providerRequestPayloadId) writeField(step, 'providerRequestPayloadId', event.providerRequestPayloadId, 'event');
    if (event.providerResponsePayloadId) writeField(step, 'providerResponsePayloadId', event.providerResponsePayloadId, 'event');
    if (event.streamPayloadId) writeField(step, 'streamPayloadId', event.streamPayloadId, 'event');
    if (event.completedAt) writeField(step, 'completedAt', event.completedAt, 'event');
    if (derivedPhase !== 'main') writeField(page, 'phase', derivedPhase, 'event');
    // NOTE: model_completed does NOT change turn lifecycle per §4.3.
    return state;
}

function appendModelStep(page: ClientExecutionPage, source: WriteSource): ClientModelStep {
    const step: ClientModelStep = { renderKey: allocateRenderKey() };
    page.modelSteps.push(step);
    // Created-by-source marker is implicit; individual fields pick up
    // provenance as they are written. No leaf writes here.
    void source;
    return step;
}

function onToolCallEvent(state: ClientConversationState, event: SSEEvent): ClientConversationState {
    const turn = resolveEventTurn(state, event);
    if (!turn) return state;
    const eventTurnId = (event.turnId ?? '').trim();
    if (eventTurnId && turn.turnId === '') {
        writeField(turn, 'turnId', eventTurnId, 'event');
    }
    const page = ensurePageForEvent(turn, event, 'event');
    const toolCallId = (event.toolCallId ?? '').trim();
    const matched = matchToolCall(page.toolCalls, { toolCallId });
    const step = matched ?? appendToolCall(page);
    if (toolCallId) writeField(step, 'toolCallId', toolCallId, 'event');
    if (event.toolName) writeField(step, 'toolName', event.toolName, 'event');
    writeField(step, 'executionRole', executionRoleFromSignals((event as any).executionRole, event.phase, event.mode, event.toolName), 'event');
    const derivedPhase = deriveExecutionPhase(event.phase, event.mode);
    if (derivedPhase !== 'main') writeField(page, 'phase', derivedPhase, 'event');
    if (event.operationId) writeField(step, 'operationId', event.operationId, 'event');
    if (event.status) writeField(step, 'status', event.status, 'event');
    if (event.error) writeField(step, 'errorMessage', event.error, 'event');
    if (event.responsePayloadId) writeField(step, 'responsePayloadId', event.responsePayloadId, 'event');
    if (event.requestPayloadId) writeField(step, 'requestPayloadId', event.requestPayloadId, 'event');
    if (event.linkedConversationId) writeField(step, 'linkedConversationId', event.linkedConversationId, 'event');
    if (event.startedAt) writeField(step, 'startedAt', event.startedAt, 'event');
    if (event.completedAt) writeField(step, 'completedAt', event.completedAt, 'event');
    return state;
}

function appendToolCall(page: ClientExecutionPage): ClientToolCall {
    const tc: ClientToolCall = { renderKey: allocateRenderKey() };
    page.toolCalls.push(tc);
    return tc;
}

function onAssistantNarration(state: ClientConversationState, event: SSEEvent): ClientConversationState {
    const turn = resolveEventTurn(state, event);
    if (!turn) return state;
    const eventTurnId = (event.turnId ?? '').trim();
    if (eventTurnId && turn.turnId === '') {
        writeField(turn, 'turnId', eventTurnId, 'event');
    }
    const page = ensurePageForEvent(turn, event, 'event');
    // Prefer messageId (canonical); fall back to assistantMessageId for
    // emitters that haven't yet dropped the redundant field.
    const messageId = ((event.messageId ?? event.assistantMessageId) ?? '').trim();
    const narrationText = typeof event.narration === 'string' ? event.narration : '';

    // Page-level mirror (for execution-panel UI that reads page state).
    if (event.createdAt) writeField(page, 'createdAt', event.createdAt, 'event');
    if (narrationText) writeField(page, 'narration', narrationText, 'event');
    if (messageId) writeField(page, 'narrationMessageId', messageId, 'event');
    return state;
}

function onAssistantFinal(state: ClientConversationState, event: SSEEvent): ClientConversationState {
    const turn = resolveEventTurn(state, event);
    if (!turn) return state;
    const eventTurnId = (event.turnId ?? '').trim();
    if (eventTurnId && turn.turnId === '') {
        writeField(turn, 'turnId', eventTurnId, 'event');
    }
    const page = ensurePageForEvent(turn, event, 'event');
    // Prefer messageId (canonical); fall back to assistantMessageId.
    const messageId = ((event.messageId ?? event.assistantMessageId) ?? '').trim();
    if (event.createdAt) writeField(page, 'createdAt', event.createdAt, 'event');
    if (event.content) writeField(page, 'content', event.content, 'event');
    if (messageId) writeField(page, 'finalAssistantMessageId', messageId, 'event');
    writeField(page, 'finalResponse', true, 'event');
    // Turn-level assistantFinal aggregate (optional mirror).
    if (event.content || messageId) {
        turn.assistantFinal = turn.assistantFinal ?? { renderKey: allocateRenderKey() };
        const af = turn.assistantFinal as ClientAssistantFinal;
        if (messageId) writeField(af, 'messageId', messageId, 'event');
        if (event.content) writeField(af, 'content', event.content, 'event');
        if (event.createdAt) writeField(af, 'createdAt', event.createdAt, 'event');
    }
    return state;
}

function onTextDelta(state: ClientConversationState, event: SSEEvent): ClientConversationState {
    const turn = resolveEventTurn(state, event);
    if (!turn) return state;
    const eventTurnId = (event.turnId ?? '').trim();
    if (eventTurnId && turn.turnId === '') {
        writeField(turn, 'turnId', eventTurnId, 'event');
    }
    const page = ensurePageForEvent(turn, event, 'event');
    const chunk = typeof event.content === 'string' ? event.content : '';
    if (chunk === '') return state;          // empty chunk → no-op per §5.2
    const prior = typeof page.content === 'string' ? page.content : '';
    writeField(page, 'content', prior + chunk, 'event');
    return state;
}

function onReasoningDelta(state: ClientConversationState, event: SSEEvent): ClientConversationState {
    // Reasoning deltas accumulate into the page narration accumulator.
    const turn = resolveEventTurn(state, event);
    if (!turn) return state;
    const eventTurnId = (event.turnId ?? '').trim();
    if (eventTurnId && turn.turnId === '') {
        writeField(turn, 'turnId', eventTurnId, 'event');
    }
    const page = ensurePageForEvent(turn, event, 'event');
    const chunk = typeof event.content === 'string' ? event.content : '';
    if (chunk === '') return state;
    const prior = typeof page.narration === 'string' ? page.narration : '';
    writeField(page, 'narration', prior + chunk, 'event');
    return state;
}

function onToolCallDelta(state: ClientConversationState, event: SSEEvent): ClientConversationState {
    const turn = resolveEventTurn(state, event);
    if (!turn) return state;
    const page = ensurePageForEvent(turn, event, 'event');
    const toolCallId = (event.toolCallId ?? '').trim();
    const matched = toolCallId
        ? matchToolCall(page.toolCalls, { toolCallId })
        : (page.toolCalls.length > 0 ? page.toolCalls[page.toolCalls.length - 1] : null);
    const step = matched ?? appendToolCall(page);
    if (toolCallId) writeField(step, 'toolCallId', toolCallId, 'event');
    if (event.toolName) writeField(step, 'toolName', event.toolName, 'event');
    return state;
}

function onElicitation(state: ClientConversationState, event: SSEEvent): ClientConversationState {
    const turn = resolveEventTurn(state, event);
    if (!turn) return state;
    turn.elicitation = turn.elicitation ?? { renderKey: allocateRenderKey() };
    const e = turn.elicitation as ClientElicitation;
    if (event.elicitationId) writeField(e, 'elicitationId', event.elicitationId, 'event');
    if (event.status) writeField(e, 'status', event.status as ClientElicitation['status'], 'event');
    if (event.callbackUrl) writeField(e, 'callbackUrl', event.callbackUrl, 'event');
    if (typeof event.content === 'string' && event.content.trim() !== '') {
        writeField(e, 'message', event.content, 'event');
    }
    if (event.elicitationData && typeof event.elicitationData === 'object') {
        const requestedSchema = (event.elicitationData?.requestedSchema
            ?? event.elicitationData?.schema
            ?? event.elicitationData) as ClientElicitation['requestedSchema'];
        if (requestedSchema !== undefined) {
            writeField(e, 'requestedSchema', requestedSchema, 'event');
        }
    }
    if (event.type === 'elicitation_resolved') {
        writeField(e, 'status', 'accepted', 'event');
    }
    return state;
}

function onLinkedConversationAttached(state: ClientConversationState, event: SSEEvent): ClientConversationState {
    const turn = resolveEventTurn(state, event);
    if (!turn) return state;
    const linkedConversationId = (event.linkedConversationId ?? '').trim();
    if (!linkedConversationId) return state;
    const matched = matchLinkedConversation(turn.linkedConversations, { linkedConversationId });
    const lc = matched ?? (() => {
        const fresh: ClientLinkedConversation = {
            renderKey: allocateRenderKey(),
            conversationId: linkedConversationId,
        };
        setFieldProvenance(fresh, 'conversationId', 'event');
        turn.linkedConversations.push(fresh);
        return fresh;
    })();
    if (event.linkedConversationAgentId) writeField(lc, 'agentId', event.linkedConversationAgentId, 'event');
    if (event.linkedConversationTitle) writeField(lc, 'title', event.linkedConversationTitle, 'event');
    if (event.toolCallId) writeField(lc, 'toolCallId', event.toolCallId, 'event');
    return state;
}

function assistantEventTargetsPage(turn: ClientTurnState, event: SSEEvent): boolean {
    const messageId = String((event.messageId ?? event.id ?? '')).trim();
    if (messageId) {
        for (const page of turn.pages) {
            const assistantMessageId = String(page?.finalAssistantMessageId || '').trim()
                || String(page?.narrationMessageId || '').trim()
                || String(page?.modelSteps?.[0]?.assistantMessageId || '').trim()
                || String(page?.pageId || '').trim();
            if (assistantMessageId && assistantMessageId === messageId) {
                return true;
            }
        }
    }
    const pageId = String((event as unknown as { pageId?: string }).pageId ?? '').trim();
    if (pageId) return true;
    return false;
}

function onAssistantMessage(state: ClientConversationState, event: SSEEvent): ClientConversationState {
    const patch = (event.patch && typeof event.patch === 'object')
        ? (event.patch as Record<string, unknown>)
        : {};
    const turn = resolveEventTurn(state, {
        ...event,
        turnId: String(event.turnId || '').trim(),
    });
    if (!turn) return state;
    const messageId = String(event.messageId || event.id || '').trim();
    const role = String(patch.role || '').trim().toLowerCase();
    const content = typeof event.content === 'string' ? event.content : '';
    if (!messageId || !content.trim() || (role !== 'assistant' && role !== 'user')) {
        return state;
    }

    // User messages → turn.users.
    if (role === 'user') {
        let entry = turn.users.find((u) => (u.messageId ?? '').trim() === messageId) || null;
        if (entry) {
            writeField(entry, 'content', content, 'event');
            if (event.createdAt) writeField(entry, 'createdAt', event.createdAt, 'event');
            if (typeof patch.sequence === 'number') writeField(entry, 'sequence', patch.sequence, 'event');
            return state;
        }
        entry = {
            renderKey: allocateRenderKey(),
            messageId,
            role: 'user',
            content,
            createdAt: event.createdAt,
            sequence: typeof patch.sequence === 'number' ? patch.sequence : undefined,
        };
        setFieldProvenance(entry, 'messageId', 'event');
        setFieldProvenance(entry, 'role', 'event');
        setFieldProvenance(entry, 'content', 'event');
        if (entry.createdAt) setFieldProvenance(entry, 'createdAt', 'event');
        if (typeof entry.sequence === 'number') setFieldProvenance(entry, 'sequence', 'event');
        turn.users.push(entry);
        return state;
    }

    if (assistantEventTargetsPage(turn, event)) {
        const page = ensurePageForEvent(turn, event, 'event');
        if (event.createdAt) writeField(page, 'createdAt', event.createdAt, 'event');
        writeField(page, 'content', content, 'event');
        if (messageId) writeField(page, 'finalAssistantMessageId', messageId, 'event');
        writeField(page, 'finalResponse', true, 'event');
        turn.assistantFinal = turn.assistantFinal ?? { renderKey: allocateRenderKey() };
        const af = turn.assistantFinal as ClientAssistantFinal;
        if (messageId) writeField(af, 'messageId', messageId, 'event');
        writeField(af, 'content', content, 'event');
        if (event.createdAt) writeField(af, 'createdAt', event.createdAt, 'event');
        return state;
    }

    // Standalone assistant add outside the execution timeline.
    let entry = turn.messages.find((m) => (m.messageId ?? '').trim() === messageId) || null;
    if (entry) {
        writeField(entry, 'role', 'assistant', 'event');
        writeField(entry, 'content', content, 'event');
        if (event.createdAt) writeField(entry, 'createdAt', event.createdAt, 'event');
        if (typeof patch.sequence === 'number') writeField(entry, 'sequence', patch.sequence, 'event');
        if (typeof patch.mode === 'string' && patch.mode) writeField(entry, 'mode', String(patch.mode), 'event');
        if (typeof patch.status === 'string' && patch.status) writeField(entry, 'status', String(patch.status), 'event');
        // Real assistant message: flip interim off. A narration that
        // pre-filled this same bubble (interim=1) gets replaced here.
        writeField(entry, 'interim', 0, 'event');
        return state;
    }
    entry = {
        renderKey: allocateRenderKey(),
        messageId,
        role: 'assistant',
        content,
        createdAt: event.createdAt,
        sequence: typeof patch.sequence === 'number' ? patch.sequence : undefined,
        mode: typeof patch.mode === 'string' ? String(patch.mode) : undefined,
        status: typeof patch.status === 'string' ? String(patch.status) : undefined,
        interim: 0,
    };
    setFieldProvenance(entry, 'messageId', 'event');
    setFieldProvenance(entry, 'role', 'event');
    setFieldProvenance(entry, 'content', 'event');
    if (entry.createdAt) setFieldProvenance(entry, 'createdAt', 'event');
    if (typeof entry.sequence === 'number') setFieldProvenance(entry, 'sequence', 'event');
    if (entry.mode) setFieldProvenance(entry, 'mode', 'event');
    if (entry.status) setFieldProvenance(entry, 'status', 'event');
    setFieldProvenance(entry, 'interim', 'event');
    turn.messages.push(entry);
    return state;
}

function onControl(state: ClientConversationState, event: SSEEvent): ClientConversationState {
    const op = String(event.op || '').trim().toLowerCase();
    if (op !== 'message_add') return state;
    const patch = event.patch && typeof event.patch === 'object' ? event.patch as Record<string, unknown> : {};
    const turn = resolveEventTurn(state, {
        ...event,
        turnId: String(event.turnId || patch.turnId || '').trim(),
    });
    if (!turn) return state;
    const messageId = String(event.messageId || event.id || patch.id || '').trim();
    const role = String(patch.role || '').trim().toLowerCase();
    const content = typeof patch.rawContent === 'string'
        ? patch.rawContent
        : typeof patch.content === 'string'
            ? patch.content
            : typeof event.content === 'string'
                ? event.content
                : '';
    if (!messageId || !content.trim() || (role !== 'assistant' && role !== 'user')) return state;

    let message = turn.messages.find((entry) => (entry.messageId ?? '').trim() === messageId) || null;
    if (message) {
        writeField(message, 'role', role, 'event');
        writeField(message, 'content', content, 'event');
        if (typeof patch.createdAt === 'string' && patch.createdAt) writeField(message, 'createdAt', String(patch.createdAt), 'event');
        if (typeof patch.sequence === 'number') writeField(message, 'sequence', patch.sequence, 'event');
        if (typeof patch.mode === 'string') writeField(message, 'mode', patch.mode, 'event');
        if (typeof patch.status === 'string') writeField(message, 'status', patch.status, 'event');
        if (typeof patch.interim === 'number') writeField(message, 'interim', patch.interim, 'event');
        return state;
    }
    message = {
        renderKey: allocateRenderKey(),
        messageId,
        role: role as 'assistant' | 'user',
        content,
        createdAt: typeof patch.createdAt === 'string' ? String(patch.createdAt) : event.createdAt,
        sequence: typeof patch.sequence === 'number' ? patch.sequence : undefined,
        mode: typeof patch.mode === 'string' ? patch.mode : undefined,
        status: typeof patch.status === 'string' ? patch.status : undefined,
        interim: typeof patch.interim === 'number' ? patch.interim : 0,
    };
    setFieldProvenance(message, 'messageId', 'event');
    setFieldProvenance(message, 'role', 'event');
    setFieldProvenance(message, 'content', 'event');
    if (message.createdAt) setFieldProvenance(message, 'createdAt', 'event');
    if (typeof message.sequence === 'number') setFieldProvenance(message, 'sequence', 'event');
    if (message.mode) setFieldProvenance(message, 'mode', 'event');
    if (message.status) setFieldProvenance(message, 'status', 'event');
    setFieldProvenance(message, 'interim', 'event');
    turn.messages.push(message);
    return state;
}

// ─── applyTranscript (§5.0, §5.3, §5.4, §5.6) ─────────────────────────────────

/**
 * Merge a canonical api.ConversationState snapshot into client state.
 * Structural, idempotent, strictly additive. Never produces synthetic
 * events. Per-field merge rule in writeField handles precedence.
 */
export function applyTranscript(
    state: ClientConversationState,
    snapshot: CanonicalConversationState,
): ClientConversationState {
    if (snapshot.conversationId !== state.conversationId) return state;

    for (const snapshotTurn of snapshot.turns ?? []) {
        mergeTranscriptTurn(state, snapshotTurn);
    }
    return state;
}

function mergeTranscriptTurn(
    state: ClientConversationState,
    snapshotTurn: CanonicalTurnState,
): void {
    const turnId = (snapshotTurn.turnId ?? '').trim();
    let turn: ClientTurnState | null = null;

    // (1) Match by turnId.
    if (turnId) turn = findTurnByTurnId(state, turnId);

    // (2) Bootstrap coalescence via clientRequestId on the user side.
    if (!turn) {
        const crid = (snapshotTurn.clientRequestId ?? '').trim()
            || (snapshotTurn.users ?? [])
                .map((u) => (u.clientRequestId ?? '').trim())
                .find((s) => s !== '')
            || (snapshotTurn.user?.clientRequestId ?? '').trim();
        if (crid) turn = findTurnByPendingClientRequestId(state, crid);
    }

    // (3) Create a fresh transcript-origin turn.
    if (!turn) {
        const lifecycle = statusToLifecycle(snapshotTurn.status);
        turn = newServerTurn('transcript', turnId, lifecycle, snapshotTurn.createdAt);
        state.turns.push(turn);
    } else {
        // Existing turn — refine.
        if (turnId && turn.turnId === '') writeField(turn, 'turnId', turnId, 'transcript');
        // Lifecycle: transcript refines only if transcript-owned/null (§5.4).
        const trProvenance = getFieldProvenance(turn, 'lifecycle');
        if (trProvenance !== 'event') {
            writeField(turn, 'lifecycle', statusToLifecycle(snapshotTurn.status), 'transcript');
        }
        if (snapshotTurn.createdAt) writeField(turn, 'createdAt', snapshotTurn.createdAt, 'transcript');
    }
    if (snapshotTurn.queueSeq) writeField(turn, 'queueSeq', snapshotTurn.queueSeq, 'transcript');
    if (snapshotTurn.startedByMessageId) writeField(turn, 'startedByMessageId', snapshotTurn.startedByMessageId, 'transcript');

    // Users — transcript may add missing entities; can't shrink.
    const users = snapshotTurn.users ?? (snapshotTurn.user ? [snapshotTurn.user] : []);
    for (const u of users) mergeTranscriptUser(turn, u);
    for (const m of snapshotTurn.messages ?? []) mergeTranscriptTurnMessage(turn, m);

    // Execution pages.
    for (const p of snapshotTurn.execution?.pages ?? []) mergeTranscriptPage(turn, p);

    // Assistant aggregates.
    if (snapshotTurn.assistant?.narration) mergeTranscriptAssistantNarration(turn, snapshotTurn.assistant.narration);
    if (snapshotTurn.assistant?.final) mergeTranscriptAssistantFinal(turn, snapshotTurn.assistant.final);

    // Elicitation.
    if (snapshotTurn.elicitation) mergeTranscriptElicitation(turn, snapshotTurn.elicitation);

    // Linked conversations.
    for (const lc of snapshotTurn.linkedConversations ?? []) mergeTranscriptLinkedConversation(turn, lc);
}

function mergeTranscriptUser(
    turn: ClientTurnState,
    snapshotUser: CanonicalUserMessageState,
): void {
    const observation = {
        messageId: snapshotUser.messageId,
        clientRequestId: snapshotUser.clientRequestId,
        content: snapshotUser.content,
        createdAt: snapshotUser.createdAt,
        sequence: snapshotUser.sequence,
    };
    let matched = matchUserMessage(turn.users, observation).matched;

    // Bootstrap coalescence for the "no echoed ids during live turn" path:
    // a completed transcript can arrive with the authoritative startedBy/
    // user message id even though turn_started never carried it over SSE.
    // If this transcript user is the turn starter and there is exactly one
    // unresolved local-origin user with identical content, treat it as the
    // same entity instead of appending a duplicate row.
    if (!matched) {
        const starterMessageId = (turn.startedByMessageId ?? '').trim();
        const snapshotMessageId = (snapshotUser.messageId ?? '').trim();
        const snapshotContent = normalizeContent(snapshotUser.content);
        if (starterMessageId && snapshotMessageId && starterMessageId === snapshotMessageId && snapshotContent) {
            const candidates = turn.users.filter((user) =>
                (user.messageId ?? '').trim() === ''
                && (user.clientRequestId ?? '').trim() !== ''
                && normalizeContent(user.content) === snapshotContent
            );
            if (candidates.length === 1) {
                matched = candidates[0];
            }
        }
    }

    if (matched) {
        const user = matched;
        if (snapshotUser.messageId) writeField(user, 'messageId', snapshotUser.messageId, 'transcript');
        if (snapshotUser.clientRequestId) writeField(user, 'clientRequestId', snapshotUser.clientRequestId, 'transcript');
        if (snapshotUser.content !== undefined) writeField(user, 'content', snapshotUser.content, 'transcript');
        if (snapshotUser.createdAt) writeField(user, 'createdAt', snapshotUser.createdAt, 'transcript');
        if (typeof snapshotUser.sequence === 'number') writeField(user, 'sequence', snapshotUser.sequence, 'transcript');
        return;
    }
    const user: ClientUserMessage = {
        renderKey: allocateRenderKey(),
        role: 'user',
        content: snapshotUser.content ?? '',
        messageId: snapshotUser.messageId,
        clientRequestId: snapshotUser.clientRequestId,
        createdAt: snapshotUser.createdAt,
        sequence: snapshotUser.sequence,
    };
    setFieldProvenance(user, 'content', 'transcript');
    if (snapshotUser.messageId) setFieldProvenance(user, 'messageId', 'transcript');
    if (snapshotUser.clientRequestId) setFieldProvenance(user, 'clientRequestId', 'transcript');
    if (snapshotUser.createdAt) setFieldProvenance(user, 'createdAt', 'transcript');
    if (typeof snapshotUser.sequence === 'number') setFieldProvenance(user, 'sequence', 'transcript');
    turn.users.push(user);
}

function mergeTranscriptTurnMessage(
    turn: ClientTurnState,
    snapshotMessage: CanonicalTurnMessageState,
): void {
    const messageId = (snapshotMessage.messageId ?? '').trim();
    if (!messageId) return;
    let message = turn.messages.find((entry) => (entry.messageId ?? '').trim() === messageId) || null;
    if (message) {
        writeField(message, 'role', snapshotMessage.role, 'transcript');
        if (snapshotMessage.content !== undefined) writeField(message, 'content', snapshotMessage.content, 'transcript');
        if (snapshotMessage.createdAt) writeField(message, 'createdAt', snapshotMessage.createdAt, 'transcript');
        if (typeof snapshotMessage.sequence === 'number') writeField(message, 'sequence', snapshotMessage.sequence, 'transcript');
        if (snapshotMessage.mode !== undefined) writeField(message, 'mode', snapshotMessage.mode, 'transcript');
        if (snapshotMessage.status !== undefined) writeField(message, 'status', snapshotMessage.status, 'transcript');
        if (typeof snapshotMessage.interim === 'number') writeField(message, 'interim', snapshotMessage.interim, 'transcript');
        return;
    }
    message = {
        renderKey: allocateRenderKey(),
        messageId,
        role: snapshotMessage.role,
        content: snapshotMessage.content ?? '',
        createdAt: snapshotMessage.createdAt,
        sequence: snapshotMessage.sequence,
        mode: snapshotMessage.mode,
        status: snapshotMessage.status,
        interim: snapshotMessage.interim,
    };
    setFieldProvenance(message, 'messageId', 'transcript');
    setFieldProvenance(message, 'role', 'transcript');
    setFieldProvenance(message, 'content', 'transcript');
    if (snapshotMessage.createdAt) setFieldProvenance(message, 'createdAt', 'transcript');
    if (typeof snapshotMessage.sequence === 'number') setFieldProvenance(message, 'sequence', 'transcript');
    if (snapshotMessage.mode !== undefined) setFieldProvenance(message, 'mode', 'transcript');
    if (snapshotMessage.status !== undefined) setFieldProvenance(message, 'status', 'transcript');
    if (typeof snapshotMessage.interim === 'number') setFieldProvenance(message, 'interim', 'transcript');
    turn.messages.push(message);
}

function mergeTranscriptPage(
    turn: ClientTurnState,
    snapshotPage: CanonicalExecutionPageState,
): void {
    let page = matchExecutionPage(turn.pages, { pageId: snapshotPage.pageId });
    if (!page) {
        page = findPageByIteration(turn, snapshotPage.iteration);
        if (page && snapshotPage.pageId) {
            writeField(page, 'pageId', snapshotPage.pageId, 'transcript');
        }
    }
    if (!page) {
        page = {
            renderKey: allocateRenderKey(),
            pageId: snapshotPage.pageId,
            iteration: snapshotPage.iteration,
            executionRole: snapshotPage.executionRole,
            phase: normalisePhase(snapshotPage.phase),
            mode: snapshotPage.mode,
            status: snapshotPage.status,
            narration: snapshotPage.narration,
            content: snapshotPage.content,
            finalResponse: snapshotPage.finalResponse,
            sequence: snapshotPage.sequence,
            narrationMessageId: snapshotPage.narrationMessageId,
            finalAssistantMessageId: snapshotPage.finalAssistantMessageId,
            modelSteps: [],
            toolCalls: [],
            lifecycleEntries: [],
            createdAt: snapshotPage.createdAt,
            startedAt: snapshotPage.startedAt,
            completedAt: snapshotPage.completedAt,
        };
        setFieldProvenance(page, 'pageId', 'transcript');
        if (snapshotPage.iteration !== undefined) setFieldProvenance(page, 'iteration', 'transcript');
        if (snapshotPage.executionRole) setFieldProvenance(page, 'executionRole', 'transcript');
        if (snapshotPage.phase) setFieldProvenance(page, 'phase', 'transcript');
        if (snapshotPage.content !== undefined) setFieldProvenance(page, 'content', 'transcript');
        if (snapshotPage.narration !== undefined) setFieldProvenance(page, 'narration', 'transcript');
        if (typeof snapshotPage.sequence === 'number') setFieldProvenance(page, 'sequence', 'transcript');
        turn.pages.push(page);
    } else {
        // Refine existing fields per §5.4.
        if (snapshotPage.iteration !== undefined) writeField(page, 'iteration', snapshotPage.iteration, 'transcript');
        if (snapshotPage.executionRole) writeField(page, 'executionRole', snapshotPage.executionRole, 'transcript');
        if (snapshotPage.phase) writeField(page, 'phase', normalisePhase(snapshotPage.phase), 'transcript');
        if (snapshotPage.mode) writeField(page, 'mode', snapshotPage.mode, 'transcript');
        if (snapshotPage.status) writeField(page, 'status', snapshotPage.status, 'transcript');
        if (snapshotPage.narration !== undefined) writeField(page, 'narration', snapshotPage.narration, 'transcript');
        if (snapshotPage.content !== undefined) writeField(page, 'content', snapshotPage.content, 'transcript');
        if (snapshotPage.finalResponse !== undefined) writeField(page, 'finalResponse', snapshotPage.finalResponse, 'transcript');
        if (typeof snapshotPage.sequence === 'number') writeField(page, 'sequence', snapshotPage.sequence, 'transcript');
        if (snapshotPage.narrationMessageId) writeField(page, 'narrationMessageId', snapshotPage.narrationMessageId, 'transcript');
        if (snapshotPage.finalAssistantMessageId) writeField(page, 'finalAssistantMessageId', snapshotPage.finalAssistantMessageId, 'transcript');
        if (snapshotPage.createdAt) writeField(page, 'createdAt', snapshotPage.createdAt, 'transcript');
        if (snapshotPage.startedAt) writeField(page, 'startedAt', snapshotPage.startedAt, 'transcript');
        if (snapshotPage.completedAt) writeField(page, 'completedAt', snapshotPage.completedAt, 'transcript');
    }

    for (const ms of snapshotPage.modelSteps ?? []) mergeTranscriptModelStep(page, ms);
    for (const ts of snapshotPage.toolSteps ?? []) mergeTranscriptToolCall(page, ts);
    for (const le of snapshotPage.lifecycleEntries ?? []) mergeTranscriptLifecycleEntry(page, le);
}

function mergeTranscriptModelStep(
    page: ClientExecutionPage,
    snapshotStep: CanonicalModelStepState,
): void {
    let step = matchModelStep(page.modelSteps, {
        modelCallId: snapshotStep.modelCallId,
        assistantMessageId: snapshotStep.assistantMessageId,
    });
    if (!step) {
        step = { renderKey: allocateRenderKey() };
        page.modelSteps.push(step);
    }
    if (snapshotStep.modelCallId) writeField(step, 'modelCallId', snapshotStep.modelCallId, 'transcript');
    if (snapshotStep.assistantMessageId) writeField(step, 'assistantMessageId', snapshotStep.assistantMessageId, 'transcript');
    if (snapshotStep.executionRole) writeField(step, 'executionRole', snapshotStep.executionRole, 'transcript');
    if (snapshotStep.phase) writeField(step, 'phase', snapshotStep.phase, 'transcript');
    if (snapshotStep.provider) writeField(step, 'provider', snapshotStep.provider, 'transcript');
    if (snapshotStep.model) writeField(step, 'model', snapshotStep.model, 'transcript');
    if (snapshotStep.status) writeField(step, 'status', snapshotStep.status, 'transcript');
    if (snapshotStep.errorMessage) writeField(step, 'errorMessage', snapshotStep.errorMessage, 'transcript');
    if (snapshotStep.requestPayloadId) writeField(step, 'requestPayloadId', snapshotStep.requestPayloadId, 'transcript');
    if (snapshotStep.responsePayloadId) writeField(step, 'responsePayloadId', snapshotStep.responsePayloadId, 'transcript');
    if (snapshotStep.providerRequestPayloadId) writeField(step, 'providerRequestPayloadId', snapshotStep.providerRequestPayloadId, 'transcript');
    if (snapshotStep.providerResponsePayloadId) writeField(step, 'providerResponsePayloadId', snapshotStep.providerResponsePayloadId, 'transcript');
    if (snapshotStep.streamPayloadId) writeField(step, 'streamPayloadId', snapshotStep.streamPayloadId, 'transcript');
    if (snapshotStep.startedAt) writeField(step, 'startedAt', snapshotStep.startedAt, 'transcript');
    if (snapshotStep.completedAt) writeField(step, 'completedAt', snapshotStep.completedAt, 'transcript');
}

function mergeTranscriptToolCall(
    page: ClientExecutionPage,
    snapshotStep: CanonicalToolStepState,
): void {
    let step = matchToolCall(page.toolCalls, { toolCallId: snapshotStep.toolCallId });
    if (!step) {
        step = { renderKey: allocateRenderKey(), toolCallId: snapshotStep.toolCallId };
        setFieldProvenance(step, 'toolCallId', 'transcript');
        page.toolCalls.push(step);
    }
    if (snapshotStep.toolCallId) writeField(step, 'toolCallId', snapshotStep.toolCallId, 'transcript');
    if (snapshotStep.toolMessageId) writeField(step, 'toolMessageId', snapshotStep.toolMessageId, 'transcript');
    if (snapshotStep.executionRole) writeField(step, 'executionRole', snapshotStep.executionRole, 'transcript');
    if (snapshotStep.toolName) writeField(step, 'toolName', snapshotStep.toolName, 'transcript');
    if (snapshotStep.operationId) writeField(step, 'operationId', snapshotStep.operationId, 'transcript');
    if (snapshotStep.status) writeField(step, 'status', snapshotStep.status, 'transcript');
    if (snapshotStep.errorMessage) writeField(step, 'errorMessage', snapshotStep.errorMessage, 'transcript');
    if (snapshotStep.requestPayloadId) writeField(step, 'requestPayloadId', snapshotStep.requestPayloadId, 'transcript');
    if (snapshotStep.responsePayloadId) writeField(step, 'responsePayloadId', snapshotStep.responsePayloadId, 'transcript');
    if (snapshotStep.linkedConversationId) writeField(step, 'linkedConversationId', snapshotStep.linkedConversationId, 'transcript');
    if (snapshotStep.linkedConversationAgentId) writeField(step, 'linkedConversationAgentId', snapshotStep.linkedConversationAgentId, 'transcript');
    if (snapshotStep.linkedConversationTitle) writeField(step, 'linkedConversationTitle', snapshotStep.linkedConversationTitle, 'transcript');
    if (snapshotStep.startedAt) writeField(step, 'startedAt', snapshotStep.startedAt, 'transcript');
    if (snapshotStep.completedAt) writeField(step, 'completedAt', snapshotStep.completedAt, 'transcript');
}

function mergeTranscriptLifecycleEntry(
    page: ClientExecutionPage,
    snapshotEntry: CanonicalLifecycleEntryState,
): void {
    appendLifecycleEntry(page, snapshotEntry, 'transcript');
}

function mergeTranscriptAssistantNarration(
    turn: ClientTurnState,
    snapshot: NonNullable<CanonicalTurnState['assistant']>['narration'],
): void {
    if (!snapshot) return;
    turn.assistantNarration = turn.assistantNarration ?? { renderKey: allocateRenderKey() };
    const p = turn.assistantNarration as ClientAssistantNarration;
    if (snapshot.messageId) writeField(p, 'messageId', snapshot.messageId, 'transcript');
    if (snapshot.content !== undefined) writeField(p, 'content', snapshot.content, 'transcript');
    if (snapshot.createdAt) writeField(p, 'createdAt', snapshot.createdAt, 'transcript');
}

function mergeTranscriptAssistantFinal(
    turn: ClientTurnState,
    snapshot: NonNullable<CanonicalTurnState['assistant']>['final'],
): void {
    if (!snapshot) return;
    turn.assistantFinal = turn.assistantFinal ?? { renderKey: allocateRenderKey() };
    const f = turn.assistantFinal as ClientAssistantFinal;
    if (snapshot.messageId) writeField(f, 'messageId', snapshot.messageId, 'transcript');
    if (snapshot.content !== undefined) writeField(f, 'content', snapshot.content, 'transcript');
    if (snapshot.createdAt) writeField(f, 'createdAt', snapshot.createdAt, 'transcript');
}

function mergeTranscriptElicitation(
    turn: ClientTurnState,
    snapshot: NonNullable<CanonicalTurnState['elicitation']>,
): void {
    turn.elicitation = turn.elicitation ?? { renderKey: allocateRenderKey() };
    const e = turn.elicitation as ClientElicitation;
    if (snapshot.elicitationId) writeField(e, 'elicitationId', snapshot.elicitationId, 'transcript');
    if (snapshot.status) writeField(e, 'status', snapshot.status, 'transcript');
    if (snapshot.message !== undefined) writeField(e, 'message', snapshot.message, 'transcript');
    if (snapshot.requestedSchema !== undefined) writeField(e, 'requestedSchema', snapshot.requestedSchema as ClientElicitation['requestedSchema'], 'transcript');
    if (snapshot.callbackUrl) writeField(e, 'callbackUrl', snapshot.callbackUrl, 'transcript');
    if (snapshot.responsePayload !== undefined) writeField(e, 'responsePayload', snapshot.responsePayload as ClientElicitation['responsePayload'], 'transcript');
}

function mergeTranscriptLinkedConversation(
    turn: ClientTurnState,
    snapshot: CanonicalLinkedConversationState,
): void {
    let lc = matchLinkedConversation(turn.linkedConversations, { linkedConversationId: snapshot.conversationId });
    if (!lc) {
        lc = {
            renderKey: allocateRenderKey(),
            conversationId: snapshot.conversationId,
        };
        setFieldProvenance(lc, 'conversationId', 'transcript');
        turn.linkedConversations.push(lc);
    }
    if (snapshot.parentConversationId) writeField(lc, 'parentConversationId', snapshot.parentConversationId, 'transcript');
    if (snapshot.parentTurnId) writeField(lc, 'parentTurnId', snapshot.parentTurnId, 'transcript');
    if (snapshot.toolCallId) writeField(lc, 'toolCallId', snapshot.toolCallId, 'transcript');
    if (snapshot.agentId) writeField(lc, 'agentId', snapshot.agentId, 'transcript');
    if (snapshot.title) writeField(lc, 'title', snapshot.title, 'transcript');
    if (snapshot.status) writeField(lc, 'status', snapshot.status, 'transcript');
    if (snapshot.response) writeField(lc, 'response', snapshot.response, 'transcript');
    if (snapshot.createdAt) writeField(lc, 'createdAt', snapshot.createdAt, 'transcript');
    if (snapshot.updatedAt) writeField(lc, 'updatedAt', snapshot.updatedAt, 'transcript');
}

// Re-export a few helpers used by tests / adjacent modules.
export { isLiveLifecycle, isTerminalLifecycle, statusToLifecycle };
