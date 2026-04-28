/**
 * chatStore/types.ts — client-side canonical state for the chat feed.
 *
 * These types mirror the backend canonical shape (`api.ConversationState`
 * in agently-core/sdk/api/canonical.go) one-for-one, adding exactly two
 * client-local concerns:
 *
 *   - a stable, opaque `renderKey` on every logical entity (identity that
 *     survives SSE echo, transcript hydration, and local→server id fill-in)
 *   - per-leaf provenance metadata consumed by the merge reducer
 *
 * Everything else in this file is the backend canonical shape in TypeScript.
 * No "segment protocol", no alternate wire model.
 *
 * See ui-improvement.md §3 (Contract 1 — Identity) and §4 (Contract 2 —
 * Canonical client state).
 */

import type { JSONObject, JSONValue } from '../types';

// ─── Provenance ────────────────────────────────────────────────────────────────

/**
 * Provenance of a field's current value.
 *
 * - `null` — field has never been written with a meaningful (effective) value.
 * - `local` — value was written by `applyLocalSubmit` (bootstrap only).
 * - `event` — value was written by a live SSE event via `applyEvent`.
 * - `transcript` — value was written by a canonical snapshot via `applyTranscript`.
 *
 * See ui-improvement.md §5.3. Precedence on write:
 *   local  < event                (event supersedes local)
 *   transcript < event             (live always wins)
 *   transcript may overwrite null | transcript
 *   transcript may NOT overwrite event (latest or historical; §5.4)
 */
export type Provenance = 'local' | 'event' | 'transcript' | null;

// ─── Identity ──────────────────────────────────────────────────────────────────

/**
 * Identity common to every logical client-side entity (turn, user message,
 * execution page, model step, tool call, lifecycle entry, linked conversation).
 *
 * `renderKey` is assigned once at entity creation by the `allocateRenderKey`
 * helper in identity.ts. It is opaque, has no substructure, has no encoded
 * meaning, and is the only field React components may use as `key`. Backend
 * ids (`messageId`, `pageId`, `toolCallId`, `turnId`, `clientRequestId`) are
 * data — they may be empty on entity creation and filled in later.
 */
export interface EntityIdentity {
    readonly renderKey: string;
    messageId?: string;
    pageId?: string;
    toolCallId?: string;
    turnId?: string;
    clientRequestId?: string;
}

// ─── Client lifecycle ──────────────────────────────────────────────────────────

/**
 * Turn lifecycle as seen by the client. Backend `api.TurnStatus` values map
 * into this narrower enum via `statusToLifecycle` in lifecycle.ts.
 *
 * Notably — `intake`, `sidecar`, `summary`, `main` are NOT lifecycle values
 * (those are per-page `phase` tags, not turn states). See ui-improvement.md
 * §4.3 and §2.6.
 */
export type ClientLifecycle =
    | 'pending'
    | 'running'
    | 'completed'
    | 'failed'
    | 'cancelled';

// ─── Execution page sub-entities ───────────────────────────────────────────────

/**
 * Execution-page phase tag. Mirrors backend `ExecutionPageState.Phase`.
 * The value `'main'` is the default when the backend emits nothing. `'intake'`
 * / `'sidecar'` / `'summary'` / `'bootstrap'` are phase-scoped annotations on a round, not
 * turn-level states.
 */
export type ClientExecutionPhase = 'intake' | 'sidecar' | 'summary' | 'bootstrap' | 'main';

export interface ClientModelStep extends EntityIdentity {
    modelCallId?: string;
    assistantMessageId?: string;
    executionRole?: string;
    phase?: string;
    provider?: string;
    model?: string;
    status?: string;
    errorMessage?: string;
    requestPayloadId?: string;
    responsePayloadId?: string;
    providerRequestPayloadId?: string;
    providerResponsePayloadId?: string;
    streamPayloadId?: string;
    startedAt?: string;
    completedAt?: string;
}

export interface ClientToolCall extends EntityIdentity {
    toolMessageId?: string;
    toolName?: string;
    executionRole?: string;
    operationId?: string;
    status?: string;
    errorMessage?: string;
    requestPayloadId?: string;
    responsePayloadId?: string;
    linkedConversationId?: string;
    linkedConversationAgentId?: string;
    linkedConversationTitle?: string;
    startedAt?: string;
    completedAt?: string;
    asyncOperation?: {
        operationId: string;
        status?: string;
        message?: string;
        error?: string;
        response?: JSONObject;
    };
}

/**
 * Lifecycle entry inside an execution page. Carries `turn_started` /
 * `turn_completed` / `turn_failed` / `turn_canceled` markers so they render
 * inline inside the execution-details card (ui-improvement.md §6.4).
 *
 * Identity composition (ui-improvement.md §3.2 "stable page-local identity"):
 *   the lifecycle entry is uniquely identified within its page by
 *   `(kind, createdAt)`. Two entries with the same `kind` but different
 *   `createdAt` are two entries.
 */
export type ClientLifecycleEntryKind =
    | 'turn_started'
    | 'turn_completed'
    | 'turn_failed'
    | 'turn_canceled';

export interface ClientLifecycleEntry extends EntityIdentity {
    kind: ClientLifecycleEntryKind;
    createdAt: string;
    status?: string;
    errorMessage?: string;
}

// ─── User message ──────────────────────────────────────────────────────────────

export interface ClientUserMessage extends EntityIdentity {
    role: 'user';
    content: string;
    createdAt?: string;
    sequence?: number;
    /**
     * Bootstrap-only timestamp set by `applyLocalSubmit` used to scope the
     * fuzzy-match window (§3.2). Unset once the entity has been echoed.
     */
    submittedAt?: string;
}

export interface ClientStandaloneMessage extends EntityIdentity {
    role: 'user' | 'assistant';
    content: string;
    createdAt?: string;
    sequence?: number;
    mode?: string;
    status?: string;
    interim?: number;
}

// ─── Assistant (optional aggregates) ──────────────────────────────────────────

export interface ClientAssistantFinal extends EntityIdentity {
    content?: string;
    createdAt?: string;
}

export interface ClientAssistantNarration extends EntityIdentity {
    content?: string;
    createdAt?: string;
}

// ─── Elicitation ───────────────────────────────────────────────────────────────

export type ClientElicitationStatus =
    | 'pending'
    | 'accepted'
    | 'declined'
    | 'canceled';

export interface ClientElicitation extends EntityIdentity {
    elicitationId?: string;
    status?: ClientElicitationStatus;
    message?: string;
    requestedSchema?: JSONObject | null;
    callbackUrl?: string;
    responsePayload?: JSONObject | null;
}

// ─── Linked conversation ───────────────────────────────────────────────────────

export interface ClientLinkedConversation extends EntityIdentity {
    conversationId: string;
    parentConversationId?: string;
    parentTurnId?: string;
    toolCallId?: string;
    agentId?: string;
    title?: string;
    status?: string;
    response?: string;
    createdAt?: string;
    updatedAt?: string;
}

// ─── Execution page ────────────────────────────────────────────────────────────

/**
 * One page = one model round within a turn. Mirrors backend
 * `ExecutionPageState` with client render identity.
 *
 * The data-model term is "page". The React component that renders a page is
 * called `IterationBlock`. One page becomes one visible entry / round inside
 * the execution-details card. (ui-improvement.md §6 terminology bridge.)
 */
export interface ClientExecutionPage extends EntityIdentity {
    iteration?: number;
    executionRole?: string;
    phase?: ClientExecutionPhase;
    mode?: string;
    status?: string;
    narration?: string;
    content?: string;
    finalResponse?: boolean;
    narrationMessageId?: string;
    finalAssistantMessageId?: string;
    modelSteps: ClientModelStep[];
    toolCalls: ClientToolCall[];
    lifecycleEntries: ClientLifecycleEntry[];
    createdAt?: string;
    startedAt?: string;
    completedAt?: string;
}

// ─── Turn ──────────────────────────────────────────────────────────────────────

export interface ClientTurnState extends EntityIdentity {
    /** Backend turnId; '' during pending-bootstrap (§4.1). */
    turnId: string;
    /** Derived/stored lifecycle (§4.3). Never set by assistant_* / model_* / tool_*. */
    lifecycle: ClientLifecycle;
    /** User messages in this turn. Plural to support steering (§6.8). */
    users: ClientUserMessage[];
    /** Standalone non-iteration messages persisted in this turn. */
    messages: ClientStandaloneMessage[];
    /** Execution pages, one per model round. */
    pages: ClientExecutionPage[];
    /** Optional turn-level assistant aggregate (final content). */
    assistantFinal?: ClientAssistantFinal | null;
    /** Optional turn-level assistant aggregate (narration). */
    assistantNarration?: ClientAssistantNarration | null;
    /** Pending turn-level elicitation (ui-improvement.md §6.3 renderable). */
    elicitation?: ClientElicitation | null;
    /** Linked conversations attached to this turn. */
    linkedConversations: ClientLinkedConversation[];
    /** Optional queue sequence for queued turns. */
    queueSeq?: number;
    /** Message id that started this turn (for run correlation). */
    startedByMessageId?: string;
    createdAt?: string;
    updatedAt?: string;
}

// ─── Conversation ─────────────────────────────────────────────────────────────

export interface ClientConversationState {
    conversationId: string;
    turns: ClientTurnState[];
}

// ─── Provenance map ────────────────────────────────────────────────────────────

/**
 * Per-leaf provenance tracked alongside an entity. Keyed by property name of
 * the owning entity (e.g. `content`, `status`, `messageId`). Entries whose key
 * is not present are implicitly `null` (unset).
 *
 * The reducer attaches one ProvenanceMap per entity instance via a WeakMap;
 * it is not part of the rendered shape and is not serialised.
 */
export type ProvenanceMap = Record<string, Provenance>;

// ─── LocalSubmit payload ───────────────────────────────────────────────────────

export interface LocalSubmit {
    conversationId: string;
    clientRequestId: string;
    content: string;
    createdAt?: string;
    attachments?: JSONValue[];
    mode?: 'submit' | 'steer';
}

// ─── Backend canonical transcript shape (TS mirror) ────────────────────────────

/**
 * Structural TS mirror of `agently-core/sdk/api/canonical.go`'s
 * ConversationState. This is the exact shape `applyTranscript` consumes —
 * no diff, no reshape, no row envelope.
 *
 * These types are intentionally limited to what the reducer reads; fields
 * that exist on the backend but are not consumed by the reducer (e.g.
 * `feeds`) are typed loosely.
 *
 * Mirroring rules:
 *  - field names are camelCase (Go json tags).
 *  - all fields are read-only from the reducer's perspective.
 */
export interface CanonicalConversationState {
    conversationId: string;
    turns?: CanonicalTurnState[];
    feeds?: unknown[];
}

/** Mirrors `api.TurnStatus`. */
export type CanonicalTurnStatus =
    | 'queued'
    | 'running'
    | 'waiting_for_user'
    | 'completed'
    | 'failed'
    | 'canceled';

export interface CanonicalTurnState {
    turnId: string;
    status: CanonicalTurnStatus;
    user?: CanonicalUserMessageState | null;
    users?: CanonicalUserMessageState[];
    messages?: CanonicalTurnMessageState[];
    execution?: CanonicalExecutionState | null;
    assistant?: CanonicalAssistantState | null;
    elicitation?: CanonicalElicitationState | null;
    linkedConversations?: CanonicalLinkedConversationState[];
    createdAt?: string;
    queueSeq?: number;
    startedByMessageId?: string;
    clientRequestId?: string;
}

export interface CanonicalUserMessageState {
    messageId: string;
    content?: string;
    clientRequestId?: string;
    createdAt?: string;
    sequence?: number;
}

export interface CanonicalTurnMessageState {
    messageId: string;
    role: 'user' | 'assistant';
    content?: string;
    createdAt?: string;
    sequence?: number;
    mode?: string;
    status?: string;
    interim?: number;
}

export interface CanonicalAssistantState {
    narration?: CanonicalAssistantMessageState | null;
    final?: CanonicalAssistantMessageState | null;
}

export interface CanonicalAssistantMessageState {
    messageId: string;
    content?: string;
    createdAt?: string;
}

export interface CanonicalExecutionState {
    pages?: CanonicalExecutionPageState[];
    activePageIndex?: number;
    totalElapsedMs?: number;
}

export interface CanonicalExecutionPageState {
    pageId: string;
    assistantMessageId?: string;
    parentMessageId?: string;
    turnId?: string;
    iteration?: number;
    executionRole?: string;
    phase?: string;
    mode?: string;
    status?: string;
    modelSteps?: CanonicalModelStepState[];
    toolSteps?: CanonicalToolStepState[];
    narrationMessageId?: string;
    finalAssistantMessageId?: string;
    narration?: string;
    content?: string;
    finalResponse?: boolean;
    sequence?: number;
    createdAt?: string;
    startedAt?: string;
    completedAt?: string;
    lifecycleEntries?: CanonicalLifecycleEntryState[];
}

export interface CanonicalModelStepState {
    modelCallId: string;
    assistantMessageId?: string;
    executionRole?: string;
    phase?: string;
    provider?: string;
    model?: string;
    status?: string;
    errorMessage?: string;
    requestPayloadId?: string;
    responsePayloadId?: string;
    providerRequestPayloadId?: string;
    providerResponsePayloadId?: string;
    streamPayloadId?: string;
    startedAt?: string;
    completedAt?: string;
}

export interface CanonicalToolStepState {
    toolCallId: string;
    toolMessageId?: string;
    toolName?: string;
    executionRole?: string;
    operationId?: string;
    status?: string;
    errorMessage?: string;
    requestPayloadId?: string;
    responsePayloadId?: string;
    linkedConversationId?: string;
    linkedConversationAgentId?: string;
    linkedConversationTitle?: string;
    startedAt?: string;
    completedAt?: string;
    asyncOperation?: {
        operationId: string;
        status?: string;
        message?: string;
        error?: string;
        response?: JSONObject | null;
    } | null;
}

export interface CanonicalElicitationState {
    elicitationId?: string;
    status?: ClientElicitationStatus;
    message?: string;
    requestedSchema?: JSONObject | null;
    callbackUrl?: string;
    responsePayload?: JSONObject | null;
}

export interface CanonicalLinkedConversationState {
    conversationId: string;
    parentConversationId?: string;
    parentTurnId?: string;
    toolCallId?: string;
    agentId?: string;
    title?: string;
    status?: string;
    response?: string;
    createdAt?: string;
    updatedAt?: string;
}

/**
 * Lifecycle entries may be carried on transcript pages once the backend emits
 * them; until then they arrive only via SSE and are materialised on the
 * client. Field is optional here to keep the mirror tolerant.
 */
export interface CanonicalLifecycleEntryState {
    kind: ClientLifecycleEntryKind;
    createdAt: string;
    status?: string;
    errorMessage?: string;
}
