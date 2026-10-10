import { BrowserMCPHost, type BrowserMCPHostOptions, type BrowserMCPCatalog, type BrowserMCPAppActivity } from './browserMCP';
import type { ReportRun, ReportContext, ReportRunResult, BeginReportRunInput, CompleteReportRunInput, AdoptReportRunInput, ReportExportJob, ReportArtifact, ReportAuditEvent } from "./types";
import type {ListSkillsInput, ListSkillsOutput, ActivateSkillInput, ActivateSkillOutput} from './types';
/**
 * AgentlyClient — TypeScript HTTP client for agently-core SDK.
 *
 * Mirrors the Go Client interface (sdk/client.go) and HTTPClient implementation
 * (sdk/http.go). All methods correspond 1:1 to the HTTP handler routes registered
 * in sdk/handler.go.
 *
 * Usage:
 *   const client = new AgentlyClient({ baseURL: 'http://localhost:8585/v1' });
 *   const page = await client.listConversations({ query: 'sales' });
 */

import type {
    Conversation, ConversationPage, CreateConversationInput, ListConversationsInput,
    UpdateConversationInput, Goal, GoalEnvelope, CreateGoalInput, UpdateGoalInput,
    ListAsyncOperationsOutput,
    Message, MessagePage, GetMessagesInput,
    Turn, TranscriptOutput, GetTranscriptInput, GetTranscriptOptions, QuerySelector,
    QueryInput, QueryOutput,
    SteerTurnInput, SteerTurnOutput, MoveQueuedTurnInput, EditQueuedTurnInput,
    SSEEvent, StreamEventsInput,
    PendingElicitation, ResolveElicitationInput,
    PendingToolApproval, PendingToolApprovalPage, DecideToolApprovalInput, DecideToolApprovalOutput,
    FileEntry, UploadFileOutput,
    Resource, ResourceRef, RunView,
    Schedule, ScheduleListOutput,
    WorkspaceMetadata, PayloadView, GetPayloadOptions, MetadataTargetContext, ApplyPermissionInput,
    ListLinkedConversationsInput, LinkedConversationPage,
    AuthProvider, AuthUser, LocalLoginInput, LocalLoginOutput,
    OAuthInitiateOutput, OAuthCallbackInput, OAuthCallbackOutput,
    OAuthConfigOutput, CreateSessionInput, CreateSessionOutput, OAuthInitiateInput,
    OOBLoginInput, IDPDelegateOutput,
    MCPAuthStatusOutput, MCPAuthConnectionsOutput, MCPAuthInitiateInput, MCPAuthInitiateOutput,
    FeedSpec, JSONObject, JSONValue, GetFeedDraftInput, GetFeedDraftOutput,
    UpdateFeedDraftInput, UpdateFeedDraftOutput,
    FetchDatasourceInput, FetchDatasourceOutput, InvalidateDatasourceCacheInput,
    ListLookupRegistryInput, ListLookupRegistryOutput,
    ListUIEventsInput, ListUIEventsOutput,
} from './types';
import { HttpError } from './errors';
import { FetchEventSource } from './fetchEventSource';
import { conversationDTO, messageDTO, transcriptDTO } from './wireDTO';
import { normalizeStreamEventIdentity } from './streamIdentity';
import { AgUiSession, type AgUiSessionOptions } from './aguiSession';
import type { HttpAgentConfig } from '@ag-ui/client';
import { AgUiConversationTransport } from './aguiConversationTransport';
import { AgUiViewProjection } from './aguiViewProjection';

// ─── Options ───────────────────────────────────────────────────────────────────

export type TokenProvider = () => Promise<string | null> | string | null;

export interface SessionDebugOptions {
    enabled?: boolean;
    level?: 'error' | 'warn' | 'warning' | 'info' | 'debug' | 'trace' | string;
    components?: string[];
}

export interface ClientOptions {
    /** Generic browser-local MCP support, selected only by authenticated MCP config. */
    browserMCP?: BrowserMCPHostOptions;
    /** Observe native mobile/CLI/scheduler work through the explicitly scoped application channel. */
    observeNativeWork?: boolean;
    /** Base URL including /v1 prefix, e.g. "http://localhost:8585/v1" */
    baseURL: string;
    /** Dynamic token provider (called before each request) */
    tokenProvider?: TokenProvider;
    /** Static headers merged into every request */
    headers?: Record<string, string>;
    /** Send cookies with requests (default false) */
    useCookies?: boolean;
    /** Number of retries for GET requests on transient errors (default 1) */
    retries?: number;
    /** Delay between retries in ms (default 200) */
    retryDelayMs?: number;
    /** HTTP status codes eligible for retry (default [429,502,503,504]) */
    retryStatuses?: number[];
    /** Request timeout in ms (0 = no timeout, default 30000) */
    timeoutMs?: number;
    /** Custom fetch implementation (default globalThis.fetch) */
    fetchImpl?: typeof fetch;
    /** Called on every non-retryable HTTP error (for UI toast notifications, logging, etc.) */
    onError?: (error: HttpError) => void;
    /** Called on 401 responses (for login redirects, token refresh, etc.) */
    onUnauthorized?: (error: HttpError) => void;
    /** Request-scoped debug logging sent to the server for this client/session. */
    sessionDebug?: SessionDebugOptions;
}

export interface AgUiBackendDescriptor {
    id: string;
    label: string;
    profile: 'agently' | 'standard';
    durableReplay: boolean;
    publicDemo?: boolean;
    ephemeral?: boolean;
    inputMode?: 'text-only';
}

export interface AgUiBackendThread {
    threadId: string;
    connectionId: string;
    ephemeral: boolean;
    durableReplay: boolean;
}

// DTO interfaces need not declare a string index signature to be JSON encoded.
type RequestBody = JSONValue | object | undefined;
type APIResponse = JSONValue | undefined;

// ─── Client ────────────────────────────────────────────────────────────────────

function isLegacyCompactPayloadSchemaRejection(error: unknown): boolean {
    if (!(error instanceof HttpError) || error.status !== 400) return false;
    const body = error.body;
    return body.includes('invalidEnvelope')
        && body.includes('conversation-v1.schema.json')
        && /additional\s*properties?/i.test(body)
        && body.includes('includeModelPayloads');
}

export class AgentlyClient {
    private baseURL: string;
    private tokenProvider?: TokenProvider;
    private staticHeaders: Record<string, string>;
    private useCookies: boolean;
    private retries: number;
    private retryDelayMs: number;
    private retryStatuses: Set<number>;
    private timeoutMs: number;
    private fetchImpl: typeof fetch;
    private onErrorHook?: (error: HttpError) => void;
    private onUnauthorizedHook?: (error: HttpError) => void;
    private sessionDebug?: SessionDebugOptions;
    private agUiInteractions: AgUiConversationTransport;
    private browserMCP?: BrowserMCPHost;
    private observeNativeWork = false;
    private nativeObservers = new Set<{ close(): void }>();
    private protocolSessionGeneration = 0;
    private observedApprovalOutcomes = new Map<string, string>();

    resetAgUiInteractions() {
        this.protocolSessionGeneration++;
        this.observedApprovalOutcomes.clear();
        this.agUiInteractions.reset();
        for (const observer of this.nativeObservers) observer.close();
        this.nativeObservers.clear();
    }

    refreshAgUiConversation(conversationId: string) { return this.agUiInteractions.refresh(conversationId); }
    /** Read after a committed application command, including when an older refresh is in flight. */
    reconcileAgUiConversation(conversationId: string) { return this.agUiInteractions.reconcile(conversationId); }
    observesNativeWork() { return this.observeNativeWork; }

    async listAgUiBackends(): Promise<AgUiBackendDescriptor[]> {
        const result = await this.get<{ backends: AgUiBackendDescriptor[] }>('/ag-ui/backends');
        return result.backends;
    }

    /** The BFF issues and owns remote demo thread identities; caller IDs are not accepted. */
    createAgUiBackendThread(connectionId: string): Promise<AgUiBackendThread> {
        if (!connectionId || connectionId === 'agently') throw new Error('A configured independent backend is required');
        return this.post(`/ag-ui/backends/${enc(connectionId)}/threads`, {});
    }

    /**
     * Reuses this client's BFF session and error policy for streaming AG-UI.
     * A connection ID selects a configured BFF route, never an arbitrary
     * downstream URL. Credentials are resolved for each request and remain on
     * the BFF hop. POSTs are not retried implicitly.
     */
    agUiTransport(connectionId = 'agently'): HttpAgentConfig {
        if (!connectionId.trim()) throw new Error('A backend connection identity is required');
        const url = connectionId === 'agently'
            ? `${this.baseURL}/ag-ui/run`
            : `${this.baseURL}/ag-ui/backends/${enc(connectionId)}/run`;
        return {
            url,
            fetch: async (target, init) => {
                if (target !== url) throw new Error('AG-UI transport target differs from the configured BFF route');
                try {
                    const headers = new Headers(await this.authHeaders());
                    new Headers(init.headers).forEach((value, name) => {
                        if (name.toLowerCase() !== 'authorization' && name.toLowerCase() !== 'cookie') headers.set(name, value);
                    });
                    const response = await this.fetchImpl(url, {
                        ...init, headers, credentials: this.useCookies ? 'include' : 'same-origin', redirect: 'error',
                    });
                    if (!response.ok) {
                        const error = await this.toHttpError(response);
                        if (error.status === 401) this.onUnauthorizedHook?.(error);
                        else this.onErrorHook?.(error);
                        throw error;
                    }
                    return response;
                } catch (error) {
                    if (!(error instanceof HttpError) && !(error instanceof Error && error.name === 'AbortError')) {
                        this.onErrorHook?.(new HttpError(0, 'NetworkError', error instanceof Error ? error.message : String(error)));
                    }
                    throw error;
                }
            },
        };
    }

    createAgUiSession(options: Omit<AgUiSessionOptions, 'url' | 'fetch' | 'headers' | 'connectionId'> & { connectionId?: string }): AgUiSession {
        const connectionId = options.connectionId ?? 'agently';
        return new AgUiSession({
            ...options, ...this.agUiTransport(connectionId), connectionId,
            profile: options.profile ?? (connectionId === 'agently' ? 'agently' : 'standard'),
            durableReplay: options.durableReplay ?? (connectionId === 'agently'),
        });
    }

    constructor(opts: ClientOptions) {
        this.baseURL = opts.baseURL.replace(/\/+$/, '');
        this.tokenProvider = opts.tokenProvider;
        this.staticHeaders = opts.headers ?? {};
        this.useCookies = opts.useCookies ?? false;
        this.retries = opts.retries ?? 1;
        this.retryDelayMs = opts.retryDelayMs ?? 200;
        this.retryStatuses = new Set(opts.retryStatuses ?? [429, 502, 503, 504]);
        this.timeoutMs = opts.timeoutMs ?? 30_000;
        this.fetchImpl = opts.fetchImpl ?? fetch.bind(globalThis);
        this.onErrorHook = opts.onError;
        this.onUnauthorizedHook = opts.onUnauthorized;
        this.sessionDebug = opts.sessionDebug;
        this.observeNativeWork = opts.observeNativeWork === true;
        if (opts.browserMCP) this.browserMCP = new BrowserMCPHost({
            descriptors: async () => (await this.getWorkspaceMetadata()).browserMCP ?? [],
            register: input => this.post<BrowserMCPCatalog>('/mcp/browser/catalog', input),
            revoke: id => this.del(`/mcp/browser/catalog/${enc(id)}`),
            current: id => this.get<BrowserMCPCatalog>(`/mcp/browser/catalog/${enc(id)}`),
        }, opts.browserMCP);
        this.agUiInteractions = new AgUiConversationTransport(this, options => new AgUiViewProjection(options), this.browserMCP);
    }

    subscribeBrowserMCPApps(listener: () => void): () => void { return this.browserMCP?.subscribeApps(listener) ?? (() => {}); }
    browserMCPAppActivities(conversationId: string): BrowserMCPAppActivity[] { return this.browserMCP?.appActivities(conversationId) ?? []; }
    browserMCPAppProxy(activity: BrowserMCPAppActivity) { if (!this.browserMCP) throw new Error('Browser app host unavailable'); return this.browserMCP.appProxy(activity); }

    // ── Conversations ────────────────────────────────────────────────────────

    /** Create a new conversation. */
    async createConversation(input: CreateConversationInput): Promise<Conversation> {
        return this.post<Conversation>('/conversations', input);
    }

    /** List conversations with optional search, filter, and pagination. */
    /** Lists skills visible to the selected agent, including before a conversation exists. */
    async listSkills(input: ListSkillsInput = {}): Promise<ListSkillsOutput> {
        const q = new URLSearchParams();
        if (input.conversationId) q.set('conversationId', input.conversationId);
        if (input.agentId) q.set('agentId', input.agentId);
        return this.get('/skills', q);
    }

    async activateSkill(input: ActivateSkillInput): Promise<ActivateSkillOutput> {
        const q = new URLSearchParams({conversationId: input.conversationId});
        return this.post(`/skills/${enc(input.name)}/activate?${q}`, {args: input.args || ''});
    }

    async listConversations(input?: ListConversationsInput): Promise<ConversationPage> {
        const q = new URLSearchParams();
        if (input?.query) q.set('q', input.query);
        if (input?.agentId) q.set('agentId', input.agentId);
        if (input?.scheduleId) q.set('scheduleId', input.scheduleId);
        if (input?.excludeScheduled) q.set('excludeScheduled', 'true');
        if (input?.status) q.set('status', input.status);
        this.applyPage(q, input?.page);
        const out = await this.get<ConversationPage | { Rows?: Conversation[]; NextCursor?: string; PrevCursor?: string; HasMore?: boolean }>('/conversations', q);
        if (out && 'Rows' in out && Array.isArray(out.Rows)) {
            return {
                data: out.Rows.map(conversationDTO),
                page: {
                    cursor: out.NextCursor,
                    prevCursor: out.PrevCursor,
                    hasMore: out.HasMore,
                    hasOlder: (out as any).HasOlder,
                    hasNewer: (out as any).HasNewer,
                },
            };
        }
        const page = out as ConversationPage;
        return { ...page, data: page.data.map(conversationDTO) };
    }

    /** Get a single conversation by ID. */
    async getConversation(id: string, options?: {includeTranscript?: boolean}): Promise<Conversation> {
        const query = options?.includeTranscript === false ? '?includeTranscript=false' : '';
        return conversationDTO(await this.get<Conversation>(`/conversations/${enc(id)}${query}`));
    }

    /** Update mutable conversation fields such as visibility and shareability. */
    async updateConversation(
        id: string, input: UpdateConversationInput,
    ): Promise<Conversation> {
        return this.patch<Conversation>(`/conversations/${enc(id)}`, input);
    }

    /** Delete a conversation tree owned by the current user. */
    async deleteConversation(id: string): Promise<void> {
        await this.del(`/conversations/${enc(id)}`);
    }

    /** Get the current durable goal for a conversation. */
    async getGoal(conversationId: string): Promise<Goal | null> {
        const result = await this.get<GoalEnvelope>(`/conversations/${enc(conversationId)}/goal`);
        return result?.goal ?? null;
    }

    /** List non-terminal async operations for a conversation. */
    async listAsyncOperations(conversationId: string, input: { tool?: string; mode?: string } = {}): Promise<ListAsyncOperationsOutput> {
        const q = new URLSearchParams();
        if (input.tool) q.set('tool', input.tool);
        if (input.mode) q.set('mode', input.mode);
        const out = await this.get<ListAsyncOperationsOutput>(`/conversations/${enc(conversationId)}/async`, q);
        return {
            ops: Array.isArray(out?.ops) ? out.ops : [],
        };
    }

    /** Create the durable goal for a conversation. */
    async createGoal(conversationId: string, input: CreateGoalInput): Promise<Goal> {
        return this.post<Goal>(`/conversations/${enc(conversationId)}/goal`, input);
    }

    /** Update the durable goal for a conversation. */
    async updateGoal(conversationId: string, input: UpdateGoalInput): Promise<Goal> {
        return this.patch<Goal>(`/conversations/${enc(conversationId)}/goal`, input);
    }

    /** Clear the durable goal for a conversation. */
    async clearGoal(conversationId: string): Promise<void> {
        await this.del(`/conversations/${enc(conversationId)}/goal`);
    }

    // ── Messages ─────────────────────────────────────────────────────────────

    /** Get messages with filters and cursor pagination. */
    async getMessages(input: GetMessagesInput): Promise<MessagePage> {
        const q = new URLSearchParams();
        q.set('conversationId', input.conversationId);
        if (input.turnId) q.set('turnId', input.turnId);
        if (input.roles?.length) q.set('roles', input.roles.join(','));
        if (input.types?.length) q.set('types', input.types.join(','));
        this.applyPage(q, input.page);
        const out = await this.get<MessagePage | { Rows?: Message[]; NextCursor?: string; HasMore?: boolean }>('/messages', q);
        if (out && 'Rows' in out && Array.isArray(out.Rows)) {
            return {
                data: out.Rows.map(messageDTO),
                page: {
                    cursor: out.NextCursor,
                    hasMore: out.HasMore,
                },
            };
        }
        const page = out as MessagePage;
        return { ...page, data: page.data.map(messageDTO) };
    }

    // ── Transcript ───────────────────────────────────────────────────────────

    /** Get structured turn-based transcript with optional tool/model call details. */
    async readConversationHistory(input: GetTranscriptInput, options?: GetTranscriptOptions): Promise<TranscriptOutput> {
        const q = new URLSearchParams();
        if (input.since) q.set('since', input.since);
        if (input.includeModelCalls) q.set('includeModelCalls', 'true');
        if (input.includeModelPayloads !== undefined) q.set('includeModelPayloads', String(input.includeModelPayloads));
        if (input.includeToolCalls) q.set('includeToolCalls', 'true');
        if (input.includeFeeds) q.set('includeFeeds', 'true');
        const selectors = { ...(options?.selectors ?? {}) } as Record<string, QuerySelector>;
        if (options?.executionGroupSelector) {
            selectors.ExecutionGroup = {
                ...(selectors.ExecutionGroup ?? {}),
                ...options.executionGroupSelector,
            };
        }
        if (Number.isFinite(options?.executionGroupLimit)) {
            selectors.ExecutionGroup = {
                ...(selectors.ExecutionGroup ?? {}),
                limit: Number(options?.executionGroupLimit),
            };
        }
        if (Number.isFinite(options?.executionGroupOffset)) {
            selectors.ExecutionGroup = {
                ...(selectors.ExecutionGroup ?? {}),
                offset: Number(options?.executionGroupOffset),
            };
        }
        if (Object.keys(selectors).length > 0) {
            q.set('selectors', JSON.stringify(selectors));
        }
        return transcriptDTO(await this.get<TranscriptOutput>(`/conversations/${enc(input.conversationId)}/transcript`, q));
    }

    /** Get the compact authoritative live snapshot and event cursor before joining SSE. */
    async readApplicationState(input: GetTranscriptInput, options?: GetTranscriptOptions): Promise<TranscriptOutput> {
        const q = new URLSearchParams();
        if (input.includeModelCalls) q.set('includeModelCalls', 'true');
        if (input.includeModelPayloads !== undefined) q.set('includeModelPayloads', String(input.includeModelPayloads));
        if (input.includeToolCalls) q.set('includeToolCalls', 'true');
        if (input.includeFeeds) q.set('includeFeeds', 'true');
        if (options?.selectors && Object.keys(options.selectors).length > 0) {
            q.set('selectors', JSON.stringify(options.selectors));
        }
        return transcriptDTO(await this.get<TranscriptOutput>(`/conversations/${enc(input.conversationId)}/live-state`, q));
    }

    private transcriptSelectors(options?: GetTranscriptOptions): Record<string, QuerySelector> {
        const selectors = {...options?.selectors};
        if (options?.executionGroupSelector) selectors.ExecutionGroup = {...selectors.ExecutionGroup, ...options.executionGroupSelector};
        if (Number.isFinite(options?.executionGroupLimit)) selectors.ExecutionGroup = {...selectors.ExecutionGroup, limit:Number(options!.executionGroupLimit)};
        if (Number.isFinite(options?.executionGroupOffset)) selectors.ExecutionGroup = {...selectors.ExecutionGroup, offset:Number(options!.executionGroupOffset)};
        return selectors;
    }

    async getTranscript(input: GetTranscriptInput, options?: GetTranscriptOptions): Promise<TranscriptOutput> {
        try { return await this.agUiInteractions.readSnapshot(input.conversationId, {
            mode:'transcript', since:input.since, includeModelCalls:input.includeModelCalls === true,
            includeToolCalls:input.includeToolCalls === true, includeFeeds:input.includeFeeds === true,
            includeModelPayloads:input.includeModelPayloads,
            selectors:this.transcriptSelectors(options),
        }); }
        catch (error) { if ((error as {status?:number})?.status === 403 || input.includeModelPayloads === false && isLegacyCompactPayloadSchemaRejection(error)) return this.readConversationHistory(input, options); throw error; }
    }

    async getLiveState(input: GetTranscriptInput, options?: GetTranscriptOptions): Promise<TranscriptOutput> {
        try { return await this.agUiInteractions.readSnapshot(input.conversationId, {
            mode:'live', includeModelCalls:input.includeModelCalls === true,
            includeToolCalls:input.includeToolCalls === true, includeFeeds:input.includeFeeds === true,
            includeModelPayloads:input.includeModelPayloads,
            selectors:this.transcriptSelectors(options),
        }); }
        catch (error) { if ((error as {status?:number})?.status === 403 || input.includeModelPayloads === false && isLegacyCompactPayloadSchemaRejection(error)) return this.readApplicationState(input, options); throw error; }
    }

    // ── Query ────────────────────────────────────────────────────────────────

    /**
     * Send a user message and run the agent's ReAct loop.
     * If a turn is already running, the new turn is automatically queued.
     */
    async query(input: QueryInput): Promise<QueryOutput> {
        return this.agUiInteractions.query(input);
    }

    // ── Turns ────────────────────────────────────────────────────────────────

    /** Cancel a running turn. Returns true if a running turn was found. */
    async cancelTurn(turnId: string): Promise<boolean> {
        const res = await this.post<{ cancelled?: boolean; canceled?: boolean }>(`/turns/${enc(turnId)}/cancel`, {});
        return res?.cancelled ?? res?.canceled ?? true;
    }

    /** Get current state of a run. */
    async getRun(id: string): Promise<RunView> {
        throw new Error(
            `GET /v1/runs/${enc(id)} is not exposed by sdk/handler.go HTTP routes`,
        );
    }

    // ── Steer ────────────────────────────────────────────────────────────────

    /** Inject a user message into the currently running turn. */
    async steerTurn(
        conversationId: string, turnId: string, input: SteerTurnInput,
    ): Promise<SteerTurnOutput> {
        return this.post<SteerTurnOutput>(
            `/conversations/${enc(conversationId)}/turns/${enc(turnId)}/steer`,
            { content: input.content, role: input.role || 'user', ...(input.clientRequestId ? {clientRequestId: input.clientRequestId} : {}) },
        );
    }

    // ── Queue Management ─────────────────────────────────────────────────────

    /** Cancel a queued turn (status must be "queued"). */
    async cancelQueuedTurn(conversationId: string, turnId: string): Promise<void> {
        await this.del(`/conversations/${enc(conversationId)}/turns/${enc(turnId)}`);
    }

    /** Move a queued turn up or down in the queue. */
    async moveQueuedTurn(
        conversationId: string, turnId: string, input: MoveQueuedTurnInput,
    ): Promise<void> {
        await this.post(
            `/conversations/${enc(conversationId)}/turns/${enc(turnId)}/move`,
            input,
        );
    }

    /** Edit the content of a queued turn (preserves queue position). */
    async editQueuedTurn(
        conversationId: string, turnId: string, input: EditQueuedTurnInput,
    ): Promise<void> {
        await this.patch(
            `/conversations/${enc(conversationId)}/turns/${enc(turnId)}`,
            input,
        );
    }

    /** Promote a queued turn into the running turn via steer, removing it from queue. */
    async forceSteerQueuedTurn(
        conversationId: string, turnId: string,
    ): Promise<SteerTurnOutput> {
        return this.post<SteerTurnOutput>(
            `/conversations/${enc(conversationId)}/turns/${enc(turnId)}/force-steer`,
            {},
        );
    }

    // ── SSE Streaming ────────────────────────────────────────────────────────

    /**
     * Subscribe to real-time streaming events for a conversation.
     * Returns an object with a close() method to unsubscribe.
     *
     * All server events are delivered via the onEvent callback as parsed SSEEvent
     * objects whose `type` field matches the SSEEventType union (text_delta,
     * reasoning_delta, tool_call_started, turn_completed, etc.).
     *
     * Convenience callbacks map to the most common event types:
     *   onTextDelta  — text_delta events (streaming content chunks)
     *   onToolEvent  — tool_call_started / tool_call_delta / tool_call_completed
     *   onTurnEnd    — turn_completed / turn_failed / turn_canceled
     *   onError      — error events and SSE connection errors
     */
    streamEvents(
        conversationId: string,
        handlers: {
            /** Called for every SSE event (raw). */
            onEvent?: (event: SSEEvent) => void;
            /** Streaming text content chunks. */
            onTextDelta?: (content: string, event: SSEEvent) => void;
            /** Tool call lifecycle events. */
            onToolEvent?: (event: SSEEvent) => void;
            /** Turn finished (completed, failed, or canceled). */
            onTurnEnd?: (event: SSEEvent) => void;
            /** Error events or SSE connection failures. */
            onError?: (error: string) => void;
            /** Tool feed lifecycle events. */
            onFeedEvent?: (event: SSEEvent) => void;
            /** Authoritative canonical bootstrap/completion view, independent of streamed content. */
            onSnapshot?: (snapshot: TranscriptOutput) => void;
            /** Protocol completion/interrupt, separate from native turn completion. */
            onOutcome?: (outcome: import('./aguiViewProjection').AgUiViewOutcome) => void;
            /** Trusted native host data, separate from model-visible protocol messages. */
            onHostActivities?: import('./aguiConversationTransport').AgUiConversationHandlers['onHostActivities'];
        },
    ): { close: () => void } {
        const primary = this.agUiInteractions.subscribe(conversationId, handlers);
        if (!this.observeNativeWork) return primary;
        let active = true;
        const generation = this.protocolSessionGeneration;
        const current = () => active && generation === this.protocolSessionGeneration;
        const reconcile = () => {
            void this.agUiInteractions.reconcile(conversationId).catch(async error => {
                if (!current()) return;
                if (error?.status === 403) {
                    // Shared readers use authorized native history, without
                    // access to the owner's private protocol journal.
                    const snapshot = await this.readConversationHistory({ conversationId, includeModelCalls: true, includeToolCalls: true, includeFeeds: true });
                    if (current()) handlers.onSnapshot?.(snapshot);
                    return;
                }
                throw error;
            }).catch(error => { if (current()) handlers.onError?.(String(error?.message || error)); });
        };
        const application = this.observeNativeEvents(conversationId, {
            ...handlers,
            onEvent: event => {
                if (String(event.type) === 'compatibility_reconcile') {
                    reconcile();
                    return;
                }
                handlers.onEvent?.(event);
                // Native deltas need not carry the originating user's text.
                // Reconcile once on completion to recover its canonical row.
                if (['turn_completed', 'turn_failed', 'turn_canceled'].includes(event.type)) reconcile();
                if (event.type === 'conversation_meta_updated' && event.patch?.aguiUpdated === true) reconcile();
            },
        });
        return { close: () => { active = false; primary.close(); application.close(); } };
    }

    /** Native application observation is separate from primary AG-UI execution. */
    observeNativeEvents(conversationId: string, handlers: import('./aguiConversationTransport').AgUiConversationHandlers) {
        const inner = this.streamApplicationEvents(conversationId, handlers);
        const observer = { close: () => { inner.close(); this.nativeObservers.delete(observer); } };
        this.nativeObservers.add(observer);
        return observer;
    }

    private streamApplicationEvents(conversationId: string, handlers: import('./aguiConversationTransport').AgUiConversationHandlers): { close(): void } {
        const url = `${this.baseURL}/application-events?conversationId=${enc(conversationId)}`;
        const es = this.tokenProvider || Object.keys(this.staticHeaders).length > 0
            ? new FetchEventSource(url, this.fetchImpl, () => this.authHeaders(), this.useCookies ? 'include' : 'same-origin')
            : new EventSource(url, { withCredentials: this.useCookies });
        let closed = false;

        es.onopen = () => { if (!closed) handlers.onOpen?.(); };

        es.onmessage = (ev: { data: string }) => {
            if (closed) return;
            try {
                const parsed: SSEEvent = JSON.parse(ev.data);
                const event = normalizeStreamEventIdentity(parsed, conversationId);
                if (!event) return;
                handlers.onEvent?.(event);
                switch (event.type) {
                    case 'text_delta':
                        handlers.onTextDelta?.(event.content ?? '', event);
                        break;
                    case 'tool_call_started':
                    case 'tool_call_waiting':
                    case 'tool_call_delta':
                    case 'tool_call_completed':
                    case 'tool_call_failed':
                    case 'tool_call_canceled':
                    case 'skill_started':
                    case 'skill_completed':
                        handlers.onToolEvent?.(event);
                        break;
                    case 'turn_completed':
                    case 'turn_failed':
                    case 'turn_canceled':
                        handlers.onTurnEnd?.(event);
                        break;
                    case 'tool_feed_active':
                    case 'tool_feed_inactive':
                        handlers.onFeedEvent?.(event);
                        break;
                    case 'error':
                        handlers.onError?.(event.error ?? 'Unknown error');
                        break;
                }
            } catch { /* ignore malformed events */ }
        };

        es.onerror = () => {
            if (closed) return;
            // EventSource does not expose the HTTP status on error. Probe the
            // stream endpoint with a HEAD/GET to detect 401 vs transport failure
            // so the client-level onUnauthorized hook fires correctly.
            this.probeStreamAuth(url).then((status) => {
                if (closed) return;
                if (status === 401) {
                    const err = new HttpError(401, 'Unauthorized', 'SSE stream rejected (401)');
                    this.onUnauthorizedHook?.(err);
                    handlers.onError?.('SSE unauthorized (401)');
                    es.close();
                } else {
                    this.onErrorHook?.(new HttpError(status || 0, 'SSE Error', 'SSE connection error'));
                    handlers.onError?.('SSE connection error');
                }
            }).catch(() => {
                if (closed) return;
                this.onErrorHook?.(new HttpError(0, 'NetworkError', 'SSE connection error'));
                handlers.onError?.('SSE connection error');
            });
        };

        return {
            close: () => {
                closed = true;
                es.close();
            },
        };
    }

    /** Probe the stream endpoint to detect HTTP status (used for SSE error diagnosis). */
    private async probeStreamAuth(url: string): Promise<number> {
        try {
            const headers = await this.authHeaders();
            const resp = await this.fetchImpl(url, {
                method: 'GET',
                headers,
                credentials: this.useCookies ? 'include' : 'same-origin',
            });
            // Abort immediately — we only need the status code, not the stream body.
            try { resp.body?.cancel(); } catch { /* ignore */ }
            return resp.status;
        } catch {
            return 0;
        }
    }

    // ── Elicitations ─────────────────────────────────────────────────────────

    /** List pending elicitation prompts for a conversation. */
    async listPendingElicitations(conversationId: string): Promise<PendingElicitation[]> {
        const q = new URLSearchParams({ conversationId });
        const out = await this.get<PendingElicitation[] | { rows?: PendingElicitation[] }>('/elicitations', q);
        if (out && !Array.isArray(out) && 'rows' in out && Array.isArray(out.rows)) return out.rows;
        if (Array.isArray(out)) return out;
        return [];
    }

    /** Resolve a pending elicitation with user response. */
    async resolveElicitation(
        conversationId: string, elicitationId: string, input: ResolveElicitationInput,
    ): Promise<void> {
        await this.post(
            `/elicitations/${enc(conversationId)}/${enc(elicitationId)}/resolve`,
            input,
        );
    }

    // ── Tool Feeds ────────────────────────────────────────────────────────────

    /** List available feed specs from workspace. */
    async listFeeds(): Promise<FeedSpec[]> {
        const out = await this.get<{ feeds?: FeedSpec[] }>('/feeds');
        return Array.isArray(out?.feeds) ? out.feeds : [];
    }

    /** Get resolved feed data for a conversation. */
    async getFeedData(feedId: string, conversationId: string): Promise<JSONValue | undefined> {
        const q = new URLSearchParams({ conversationId });
        return this.get<JSONValue | undefined>(`/feeds/${enc(feedId)}/data`, q);
    }

    // ── Tool Approvals ───────────────────────────────────────────────────────

    /** List pending tool approvals with optional filters. */
    async listPendingToolApprovalsPage(input?: {
        userId?: string;
        conversationId?: string;
        status?: string;
        limit?: number;
        offset?: number;
        outcomeSince?: string;
    }): Promise<PendingToolApprovalPage> {
        const generation = this.protocolSessionGeneration;
        const q = new URLSearchParams();
        if (input?.userId) q.set('userId', input.userId);
        if (input?.conversationId) q.set('conversationId', input.conversationId);
        if (input?.status) q.set('status', input.status);
        if (Number.isFinite(input?.limit) && Number(input?.limit) > 0) q.set('limit', String(Math.floor(Number(input?.limit))));
        if (Number.isFinite(input?.offset) && Number(input?.offset) >= 0) q.set('offset', String(Math.floor(Number(input?.offset))));
        if (input?.outcomeSince) q.set('outcomeSince', input.outcomeSince);
        const out = await this.get<PendingToolApprovalPage | PendingToolApproval[] | { data?: PendingToolApproval[]; rows?: PendingToolApproval[]; total?: number; offset?: number; limit?: number; hasMore?: boolean; outcomes?: NonNullable<DecideToolApprovalOutput['outcome']>[]; outcomeCursor?: string }>('/tool-approvals/pending', q);
        if (Array.isArray(out)) {
            return {
                rows: out,
                total: out.length,
                offset: Number(input?.offset || 0) || 0,
                limit: Number(input?.limit || out.length) || out.length,
                hasMore: false,
                outcomeCursor: '',
            };
        }
        const rows = out && !Array.isArray(out) && 'rows' in out && Array.isArray(out.rows) ? out.rows : ('data' in out && Array.isArray(out.data) ? out.data : []);
        if (generation === this.protocolSessionGeneration) {
            for (const outcome of out?.outcomes ?? []) {
                const signature = JSON.stringify(outcome.protocol);
                if (!signature || this.observedApprovalOutcomes.get(outcome.approvalId) === signature) continue;
                this.observedApprovalOutcomes.set(outcome.approvalId, signature);
                if (this.observedApprovalOutcomes.size > 256) this.observedApprovalOutcomes.delete(this.observedApprovalOutcomes.keys().next().value!);
                this.reconcileApproval(outcome.protocol);
            }
        }
        return {
            rows,
            total: Number(out?.total || rows.length) || 0,
            offset: Number(out?.offset || 0) || 0,
            limit: Number(out?.limit || rows.length) || 0,
            hasMore: Boolean(out?.hasMore),
            outcomes: Array.isArray(out?.outcomes) ? out.outcomes : [],
            outcomeCursor: typeof out?.outcomeCursor === 'string' ? out.outcomeCursor : '',
        };
    }

    /** Backward-compatible array view of pending approvals. */
    async listPendingToolApprovals(input?: {
        userId?: string;
        conversationId?: string;
        status?: string;
        limit?: number;
        offset?: number;
    }): Promise<PendingToolApproval[]> {
        const page = await this.listPendingToolApprovalsPage(input);
        return Array.isArray(page?.rows) ? page.rows : [];
    }

    /** Approve or reject a queued tool execution. */
    async decideToolApproval(
        id: string, input: DecideToolApprovalInput,
    ): Promise<DecideToolApprovalOutput> {
        const generation = this.protocolSessionGeneration;
        const output = await this.post<DecideToolApprovalOutput>(`/tool-approvals/${enc(id)}/decision`, input);
        if (generation === this.protocolSessionGeneration) this.reconcileApproval(output.protocol ?? output.outcome?.protocol);
        return output;
    }

    private reconcileApproval(protocol?: import('./types').ApprovalProtocolReferences) {
        if (protocol?.version !== '1' || !protocol.threadId) return;
        const conversationId = protocol.kind === 'mcp-app' ? protocol.nativeConversationId : protocol.threadId;
        if (!conversationId) return;
        // A replayed decision receipt may predate successor admission. Discover
        // current runs instead of assuming the returned continuation is latest.
        const generation = this.protocolSessionGeneration;
        void this.agUiInteractions.reconcile(conversationId).catch(error => {
            if (generation === this.protocolSessionGeneration) this.onErrorHook?.(error);
        });
    }

    // Browser report lifecycle and reporting tools.
    async beginReportRun(input: BeginReportRunInput): Promise<ReportRunResult> {
        return this.post('/api/report-runs/begin', input);
    }
    async getReportRun(id: string, conversationId?: string): Promise<ReportRun> {
        const query = new URLSearchParams(); if (conversationId) query.set('conversationId', conversationId);
        return this.get(`/api/report-runs/${enc(id)}`, query);
    }
    async getReportContext(conversationId: string): Promise<ReportContext> {
        return this.get(`/api/report-runs/context/${enc(conversationId)}`);
    }
    async completeReportRun(id: string, input: CompleteReportRunInput): Promise<ReportRun> {
        return this.post(`/api/report-runs/${enc(id)}/complete`, input);
    }
    async adoptReportRun(id: string, input: AdoptReportRunInput): Promise<ReportRunResult> {
        return this.post(`/api/report-runs/${enc(id)}/adopt`, input);
    }
    async submitReportRunExport(reportRunId: string, options: { conversationId: string; exportRequestId: string }): Promise<ReportExportJob> {
        const query = new URLSearchParams({ conversationId: options.conversationId });
        const result = await this.request<{ result: string }>('POST', `${this.baseURL}/tools/reporting:submit_export/execute?${query}`, { reportRunId, format: 'pdf' }, { 'X-Agently-Export-Request-ID': options.exportRequestId });
        return JSON.parse(result.result) as ReportExportJob;
    }
    async getReportExportStatus(jobId: string, conversationId?: string): Promise<ReportExportJob> {
        return JSON.parse(await this.executeTool('reporting:get_export_status', { jobId }, { conversationId })) as ReportExportJob;
    }
    async getReportArtifact(artifactId: string, conversationId?: string): Promise<ReportArtifact> {
        return JSON.parse(await this.executeTool('reporting:get_artifact', { artifactId }, { conversationId })) as ReportArtifact;
    }
    async recordReportAuditEvent(event: ReportAuditEvent, conversationId?: string): Promise<ReportAuditEvent> {
        return JSON.parse(await this.executeTool('reporting:record_audit_event', { event: event as unknown as JSONObject }, { conversationId })) as ReportAuditEvent;
    }

    // ── Tools ────────────────────────────────────────────────────────────────

    /** Execute a registered tool by name. */
    async executeTool(name: string, args?: JSONObject, options?: { conversationId?: string }): Promise<string> {
        const q = new URLSearchParams();
        if (options?.conversationId) q.set('conversationId', options.conversationId);
        const url = q.toString() ? `/tools/${enc(name)}/execute?${q.toString()}` : `/tools/${enc(name)}/execute`;
        const res = await this.post<JSONValue | undefined>(url, args ?? {});
        if (typeof res === 'string') return res;
        const result = res && isJSONObject(res) ? res.result : undefined;
        return typeof result === 'string' ? result : JSON.stringify(result ?? res);
    }

    /** List recent structured UI events for a conversation/client/window scope. */
    async listUIEvents(input: ListUIEventsInput): Promise<ListUIEventsOutput> {
        const { conversationId, ...filters } = input;
        if (!conversationId || !conversationId.trim()) {
            throw new Error('conversationId is required');
        }
        const raw = await this.executeTool('ui/events:list', compactObject(filters), { conversationId });
        const parsed = safeParseJSON(raw);
        const output = isJSONObject(parsed) ? parsed : {};
        return {
            conversationId: String(output.conversationId || conversationId),
            clientId: typeof output.clientId === 'string' ? output.clientId : filters.clientId,
            events: Array.isArray(output.events) ? output.events.filter(isJSONObject) as unknown as ListUIEventsOutput['events'] : [],
        };
    }

    /** Read unified live state for selected datasources in an active Tool Feed. */
    async getFeedDraft(input: GetFeedDraftInput): Promise<GetFeedDraftOutput> {
        const { conversationId, ...args } = input;
        if (!conversationId || !conversationId.trim()) throw new Error('conversationId is required');
        if (!args.feedId || !args.feedId.trim()) throw new Error('feedId is required');
        if (!Array.isArray(args.dataSourceRefs) || args.dataSourceRefs.length === 0) {
            throw new Error('dataSourceRefs are required');
        }
        const raw = await this.executeTool('ui/feed:get', compactObject(args), { conversationId });
        const parsed = safeParseJSON(raw);
        const output = isJSONObject(parsed) ? parsed : {};
        return {
            clientId: typeof output.clientId === 'string' ? output.clientId : args.clientId,
            data: output.data as JSONValue | undefined,
        };
    }

    /** Apply preview-only JSON Pointer operations to an active Tool Feed draft. */
    async updateFeedDraft(input: UpdateFeedDraftInput): Promise<UpdateFeedDraftOutput> {
        const { conversationId, ...args } = input;
        if (!conversationId || !conversationId.trim()) throw new Error('conversationId is required');
        if (!args.feedId || !args.feedId.trim()) throw new Error('feedId is required');
        if (!Array.isArray(args.operations) || args.operations.length === 0) {
            throw new Error('operations are required');
        }
        const raw = await this.executeTool('ui/feed:update', compactObject(args), { conversationId });
        const parsed = safeParseJSON(raw);
        const output = isJSONObject(parsed) ? parsed : {};
        return {
            clientId: typeof output.clientId === 'string' ? output.clientId : args.clientId,
            ok: output.ok === true,
            error: typeof output.error === 'string' && output.error ? output.error : undefined,
        };
    }

    // ── Files ────────────────────────────────────────────────────────────────

    /** Upload a file associated with a conversation. */
    async uploadFile(
        conversationId: string, file: File | Blob, name?: string,
    ): Promise<UploadFileOutput> {
        const headers = await this.authHeaders();
        delete headers['Content-Type'];

        const fileName = name || (typeof File !== 'undefined' && file instanceof File ? file.name : 'upload.bin');
        const contentType = ('type' in file && typeof file.type === 'string' && file.type) ? file.type : 'application/octet-stream';
        const form = new FormData();
        form.set('conversationId', conversationId);
        form.set('name', fileName);
        form.set('contentType', contentType);
        form.set('file', file, fileName);

        const resp = await this.fetchImpl(`${this.baseURL}/files`, {
            method: 'POST',
            headers,
            body: form,
            credentials: this.useCookies ? 'include' : 'same-origin',
        });
        if (!resp.ok) throw await this.toHttpError(resp);
        return (await resp.json()) as UploadFileOutput;
    }

    /** Associate an existing user-scoped scratchpad artifact with a conversation. */
    async attachArtifact(conversationId: string, resourceURI: string): Promise<UploadFileOutput> {
        if (!conversationId.trim()) throw new Error('conversationId is required');
        if (!resourceURI.trim()) throw new Error('resourceURI is required');

        const headers = await this.authHeaders();
        delete headers['Content-Type'];

        const form = new FormData();
        form.set('conversationId', conversationId);
        form.set('resourceURI', resourceURI);

        const resp = await this.fetchImpl(`${this.baseURL}/files`, {
            method: 'POST',
            headers,
            body: form,
            credentials: this.useCookies ? 'include' : 'same-origin',
        });
        if (!resp.ok) throw await this.toHttpError(resp);
        return (await resp.json()) as UploadFileOutput;
    }

    /** List files for a conversation. */
    async listFiles(conversationId: string): Promise<FileEntry[]> {
        const q = new URLSearchParams({ conversationId });
        const out = await this.get<FileEntry[] | { files?: FileEntry[]; Files?: FileEntry[] }>('/files', q);
        if (out && !Array.isArray(out) && 'files' in out && Array.isArray(out.files)) return out.files;
        if (out && !Array.isArray(out) && 'Files' in out && Array.isArray(out.Files)) return out.Files;
        if (Array.isArray(out)) return out;
        return [];
    }

    // ── Workspace Resources ──────────────────────────────────────────────────

    /** List resource names for a workspace kind (agent, model, embedder, etc). */
    async listResources(kind?: string): Promise<{ names: string[] }> {
        const q = new URLSearchParams();
        if (kind) q.set('kind', kind);
        return this.get<{ names: string[] }>('/workspace/resources', q);
    }

    /** Get a single workspace resource content. */
    async getResource(kind: string, name: string): Promise<{ kind: string; name: string; data: string }> {
        return this.get<{ kind: string; name: string; data: string }>(`/workspace/resources/${enc(kind)}/${enc(name)}`);
    }

    /** Create or update a workspace resource. */
    async saveResource(kind: string, name: string, data: string): Promise<void> {
        const headers = await this.authHeaders();
        const resp = await this.fetchImpl(`${this.baseURL}/workspace/resources/${enc(kind)}/${enc(name)}`, {
            method: 'PUT',
            headers,
            body: data,
            credentials: this.useCookies ? 'include' : 'same-origin',
        });
        if (!resp.ok) throw await this.toHttpError(resp);
    }

    /** Delete a workspace resource. */
    async deleteResource(kind: string, name: string): Promise<void> {
        await this.del(`/workspace/resources/${enc(kind)}/${enc(name)}`);
    }

    /** Export resources of given kinds (or all). */
    async exportResources(kinds?: string[]): Promise<{ resources: Resource[] }> {
        return this.post<{ resources: Resource[] }>('/workspace/resources/export', { kinds });
    }

    /** Import resources in bulk. */
    async importResources(resources: Resource[], replace?: boolean): Promise<{ imported: number; skipped: number }> {
        return this.post<{ imported: number; skipped: number }>('/workspace/resources/import', { resources, replace });
    }

    // ── Conversation Maintenance ─────────────────────────────────────────────

    /** Cancel all active turns and mark conversation as done. */
    async terminateConversation(conversationId: string): Promise<void> {
        await this.post(`/conversations/${enc(conversationId)}/terminate`, {});
    }

    /** LLM-summarize old messages, archiving them. */
    async compactConversation(conversationId: string): Promise<void> {
        await this.post(`/conversations/${enc(conversationId)}/compact`, {});
    }

    /** LLM-select and remove low-value messages. */
    async pruneConversation(conversationId: string): Promise<void> {
        await this.post(`/conversations/${enc(conversationId)}/prune`, {});
    }

    // ── File Download ─────────────────────────────────────────────────────────

    /** Download a previously uploaded file. Returns raw bytes + content type. */
    async downloadFile(
        conversationId: string, fileId: string,
    ): Promise<{ name: string; contentType: string; data: ArrayBuffer }> {
        const q = new URLSearchParams({ conversationId, raw: '1' });
        const url = `${this.baseURL}/files/${enc(fileId)}?${q}`;
        const headers = await this.authHeaders();
        const resp = await this.fetchImpl(url, {
            method: 'GET',
            headers,
            credentials: this.useCookies ? 'include' : 'same-origin',
        });
        if (!resp.ok) throw await this.toHttpError(resp);
        const contentType = resp.headers.get('content-type') || 'application/octet-stream';
        const disposition = resp.headers.get('content-disposition') || '';
        const nameMatch = disposition.match(/filename=\"?([^\";]+)\"?/i);
        const data = await resp.arrayBuffer();
        return {
            name: nameMatch?.[1] || fileId,
            contentType,
            data,
        };
    }

    // ── A2A (Agent-to-Agent) ─────────────────────────────────────────────────

    /** Get the A2A agent card for a given agent. */
    async getA2AAgentCard(agentId: string): Promise<JSONObject | undefined> {
        return this.get<JSONObject | undefined>(`/api/a2a/agents/${enc(agentId)}/card`);
    }

    /** Send a message to an A2A agent. */
    async sendA2AMessage<TResponse extends JSONValue | undefined = JSONObject | undefined>(
        agentId: string,
        request: JSONValue,
    ): Promise<TResponse> {
        return this.post<TResponse>(`/api/a2a/agents/${enc(agentId)}/message`, request);
    }

    /** List agent IDs that have A2A serving enabled. */
    async listA2AAgents(agentIds?: string[]): Promise<string[]> {
        const q = new URLSearchParams();
        if (agentIds?.length) q.set('ids', agentIds.join(','));
        const out = await this.get<string[] | { agents?: string[] }>('/api/a2a/agents', q);
        if (out && !Array.isArray(out) && 'agents' in out && Array.isArray(out.agents)) return out.agents;
        if (Array.isArray(out)) return out;
        return [];
    }

    // ── Scheduler ─────────────────────────────────────────────────────────────

    /** Get a single schedule by ID. */
    async getSchedule(id: string): Promise<Schedule> {
        return this.get<Schedule>(`/api/agently/scheduler/schedule/${enc(id)}`);
    }

    /** List all schedules. */
    async listSchedules(): Promise<ScheduleListOutput> {
        return this.get<ScheduleListOutput>('/api/agently/scheduler/');
    }

    /** Batch create or update schedules. */
    async upsertSchedules(schedules: Schedule[]): Promise<void> {
        await this.patch('/api/agently/scheduler/', { schedules });
    }

    /** Delete a schedule by ID. */
    async deleteSchedule(id: string): Promise<void> {
        await this.del(`/api/agently/scheduler/schedule/${enc(id)}`);
    }

    /** Trigger an immediate run of a schedule. */
    async runScheduleNow(id: string): Promise<void> {
        await this.post(`/api/agently/scheduler/run-now/${enc(id)}`, {});
    }

    // ── Workspace Metadata ────────────────────────────────────────────────────

    /** Get workspace metadata (available agents, models, defaults, capabilities). */
    async getWorkspaceMetadata(targetContext?: MetadataTargetContext): Promise<WorkspaceMetadata> {
        const decoded = await this.get<JSONValue>('/workspace/metadata', targetContextQuery(targetContext));
        return normalizeWorkspaceMetadata(decoded);
    }

    /** Fetch a validated workspace CSS/catalog URL using this client's auth policy. */
    async getWorkspaceStyleAsset(href: string): Promise<string> {
        if (!/^\/v1\/workspace\/ui\/(styles\/[a-f0-9]{64}\.css|themes\/[a-f0-9]{64}\.json)$/.test(href)) {
            throw new Error('Invalid workspace style asset URL');
        }
        return this.request<string>('GET', `${this.baseURL}${href.slice(3)}`, undefined,
            {Accept: href.endsWith('.css') ? 'text/css' : 'application/json'}, 'text');
    }

    /** Get Forge window metadata with optional platform/form-factor targeting. */
    async getForgeWindowMetadata(windowKey: string, targetContext?: MetadataTargetContext): Promise<JSONValue> {
        const key = String(windowKey || '').trim();
        if (!key) throw new Error('window key is required');
        const decoded = await this.get<JSONValue>(`/api/agently/forge/window/${enc(key)}`, targetContextQuery(targetContext));
        if (isJSONObject(decoded) && Object.prototype.hasOwnProperty.call(decoded, 'data')) {
            return decoded.data as JSONValue;
        }
        return decoded;
    }

    /** Apply authorization to one authored window before rendering or datasource initialization. */
    async applyPermission(windowKey: string, input: ApplyPermissionInput): Promise<JSONValue> {
        const key = String(windowKey || '').trim();
        if (!key) throw new Error('window key is required');
        const query = targetContextQuery(input.targetContext);
        query.set('applyPermission', 'true');
        const conversationId = String(input?.conversationId || '').trim();
        if (conversationId) query.set('conversationId', conversationId);
        if (input?.resource && Object.keys(input.resource).length > 0) query.set('resource', JSON.stringify(input.resource));
        if (input.windowParams) query.set('windowParams', JSON.stringify(input.windowParams));
        const decoded = await this.get<JSONValue>(`/api/agently/forge/window/${enc(key)}`, query);
        if (isJSONObject(decoded) && Object.prototype.hasOwnProperty.call(decoded, 'data')) {
            return decoded.data as JSONValue;
        }
        return decoded;
    }

    // ── Datasources + Lookups ───────────────────────────────────────────────

    /** Fetch a datasource by id. */
    async fetchDatasource(input: FetchDatasourceInput): Promise<FetchDatasourceOutput> {
        const id = String(input?.id || '').trim();
        if (!id) throw new Error('datasource id is required');
        const body: JSONObject = {};
        if (input.inputs) body.inputs = input.inputs;
        if (input.cache) body.cache = input.cache as JSONObject;
        const conversationId = input.conversationId?.trim();
        if (conversationId) body.conversationId = conversationId;
        return this.post<FetchDatasourceOutput>(
            `/api/datasources/${enc(id)}/fetch`,
            body
        );
    }

    /** Invalidate datasource cache entries by id and optional inputs hash. */
    async invalidateDatasourceCache(input: InvalidateDatasourceCacheInput): Promise<void> {
        const id = String(input?.id || '').trim();
        if (!id) throw new Error('datasource id is required');
        const q = new URLSearchParams();
        const inputsHash = String(input?.inputsHash || '').trim();
        if (inputsHash) q.set('inputsHash', inputsHash);
        const qs = q.toString();
        await this.del(`/api/datasources/${enc(id)}/cache${qs ? `?${qs}` : ''}`);
    }

    /** List lookup registry entries for a composer/window/template context. */
    async listLookupRegistry(input: ListLookupRegistryInput): Promise<ListLookupRegistryOutput> {
        const context = String(input?.context || '').trim();
        if (!context) throw new Error('context is required');
        const q = new URLSearchParams();
        q.set('context', context);
        return this.get<ListLookupRegistryOutput>('/api/lookups/registry', q);
    }

    // ── Payload ─────────────────────────────────────────────────────────────

    /**
     * Fetch a stored payload by ID.
     *
     * With `raw: true`, returns the raw binary content as an ArrayBuffer with
     * the original content type. Otherwise returns the structured PayloadView.
     */
    async getPayload(id: string, opts?: GetPayloadOptions): Promise<PayloadView>;
    async getPayload(id: string, opts: GetPayloadOptions & { raw: true }): Promise<{ contentType: string; data: ArrayBuffer }>;
    async getPayload(id: string, opts?: GetPayloadOptions): Promise<PayloadView | { contentType: string; data: ArrayBuffer }> {
        const q = new URLSearchParams();
        if (opts?.raw) q.set('raw', '1');
        if (opts?.meta) q.set('meta', '1');
        if (opts?.inline === false) q.set('inline', '0');
        const qs = q.toString();
        const url = qs
            ? `${this.baseURL}/api/payload/${enc(id)}?${qs}`
            : `${this.baseURL}/api/payload/${enc(id)}`;

        if (opts?.raw) {
            const headers = await this.authHeaders();
            const resp = await this.fetchImpl(url, {
                method: 'GET',
                headers,
                credentials: this.useCookies ? 'include' : 'same-origin',
            });
            if (!resp.ok) throw await this.toHttpError(resp);
            const contentType = resp.headers.get('content-type') || 'application/octet-stream';
            const data = await resp.arrayBuffer();
            return { contentType, data };
        }

        return this.request<PayloadView>('GET', url);
    }

    // ── File Browser ────────────────────────────────────────────────────────

    /** Download a workspace file by URI. Returns the raw text content. */
    async downloadWorkspaceFile(uri: string): Promise<string> {
        const q = new URLSearchParams({ uri });
        const url = `${this.baseURL}/workspace/file-browser/download?${q}`;
        const headers = await this.authHeaders();
        const resp = await this.fetchImpl(url, {
            method: 'GET',
            headers,
            credentials: this.useCookies ? 'include' : 'same-origin',
        });
        if (!resp.ok) throw await this.toHttpError(resp);
        return resp.text();
    }

    /** List workspace files/directories at the given path. */
    async listWorkspaceFiles(path?: string): Promise<JSONObject | undefined> {
        const q = new URLSearchParams();
        if (path) q.set('path', path);
        return this.get<JSONObject | undefined>('/workspace/file-browser/list', q);
    }

    // ── Linked Conversations ────────────────────────────────────────────────

    /** List child conversations linked to a parent conversation/turn. */
    async listLinkedConversations(input: ListLinkedConversationsInput): Promise<LinkedConversationPage> {
        const q = new URLSearchParams();
        q.set('parentConversationId', input.parentConversationId);
        if (input.parentTurnId) q.set('parentTurnId', input.parentTurnId);
        this.applyPage(q, input.page);
        const out = await this.get<LinkedConversationPage | { Rows?: LinkedConversationPage['data']; NextCursor?: string; PrevCursor?: string; HasMore?: boolean } | { rows?: LinkedConversationPage['data']; nextCursor?: string; prevCursor?: string; cursor?: string; hasMore?: boolean }>('/conversations/linked', q);
        if (out && 'Rows' in out && Array.isArray(out.Rows)) {
            return {
                data: out.Rows,
                page: {
                    cursor: out.NextCursor,
                    prevCursor: out.PrevCursor,
                    hasMore: out.HasMore,
                },
            };
        }
        if (out && !Array.isArray(out) && 'rows' in out && Array.isArray(out.rows)) {
            return {
                data: out.rows,
                page: {
                    cursor: out.nextCursor ?? out.cursor,
                    prevCursor: out.prevCursor,
                    hasMore: out.hasMore,
                },
            };
        }
        return out as LinkedConversationPage;
    }

    // ── Auth ─────────────────────────────────────────────────────────────────

    /** List available auth providers (local, bff, oidc, jwt). */
    async getAuthProviders(): Promise<AuthProvider[]> {
        const out = await this.get<AuthProvider[] | { providers?: AuthProvider[] }>('/api/auth/providers');
        if (out && !Array.isArray(out) && 'providers' in out && Array.isArray(out.providers)) return out.providers;
        if (Array.isArray(out)) return out;
        return [];
    }

    /** Get the currently authenticated user. Returns null if not authenticated. */
    async getAuthMe(): Promise<AuthUser | null> {
        try {
            return await this.get<AuthUser>('/api/auth/me');
        } catch (err) {
            if (err instanceof HttpError && err.status === 401) return null;
            throw err;
        }
    }

    /** Login with a workspace username. */
    async localLogin(input: LocalLoginInput): Promise<LocalLoginOutput> {
        return this.post<LocalLoginOutput>('/api/auth/local/login', input);
    }

    /** Logout and destroy the current session. */
    async logout(): Promise<void> {
        await this.post('/api/auth/logout', {});
    }

    /** Initiate an OAuth BFF flow (returns authURL + state for redirect). */
    async oauthInitiate(input: OAuthInitiateInput = {}): Promise<OAuthInitiateOutput> {
        return this.post<OAuthInitiateOutput>('/api/auth/oauth/initiate', input);
    }

    /** Complete an OAuth callback with authorization code + state. */
    async oauthCallback(input: OAuthCallbackInput): Promise<OAuthCallbackOutput> {
        return this.post<OAuthCallbackOutput>('/api/auth/oauth/callback', input);
    }

    /** Get OAuth client config metadata. */
    async getOAuthConfig(): Promise<OAuthConfigOutput> {
        return this.get<OAuthConfigOutput>('/api/auth/oauth/config');
    }

    /** Create a session from tokens (bearer, OOB, or anonymous). */
    async createAuthSession(input: CreateSessionInput): Promise<CreateSessionOutput> {
        return this.post<CreateSessionOutput>('/api/auth/session', input);
    }

    /** Out-of-band login with pre-obtained tokens. */
    async oobLogin(input: OOBLoginInput): Promise<CreateSessionOutput> {
        return this.post<CreateSessionOutput>('/api/auth/oob', input);
    }

    /**
     * Get a delegated IDP login URL (v1 extension).
     * Returns the auth URL + encrypted state for BFF PKCE flow.
     */
    async idpDelegate(): Promise<IDPDelegateOutput> {
        return this.post<IDPDelegateOutput>('/api/auth/idp/delegate', {});
    }

    /** Read delegated OAuth status for a configured MCP server. */
    async listMCPAuthConnections(): Promise<MCPAuthConnectionsOutput> {
        return this.get<MCPAuthConnectionsOutput>('/api/auth/mcp/status');
    }

    /** Read delegated OAuth status for a configured MCP server. */
    async getMCPAuthStatus(server: string): Promise<MCPAuthStatusOutput> {
        const normalized = String(server || '').trim();
        if (!normalized) throw new Error('MCP server is required');
        return this.get<MCPAuthStatusOutput>(`/api/auth/mcp/${enc(normalized)}/status`);
    }

    /** Initiate browser-delegated OAuth using the current cookie-backed session. */
    async initiateMCPAuth(
        server: string,
        csrfToken: string,
        input: MCPAuthInitiateInput = {},
    ): Promise<MCPAuthInitiateOutput> {
        const normalized = String(server || '').trim();
        const csrf = String(csrfToken || '').trim();
        if (!normalized) throw new Error('MCP server is required');
        if (!csrf) throw new Error('MCP auth CSRF token is required');
        const query = new URLSearchParams();
        if (input.returnURL) query.set('returnURL', input.returnURL);
        if (input.restart === true) query.set('restart', 'true');
        if (input.forceRestart === true) query.set('forceRestart', 'true');
        const suffix = query.toString() ? `?${query.toString()}` : '';
        return this.request<MCPAuthInitiateOutput>(
            'POST',
            `${this.baseURL}/api/auth/mcp/${enc(normalized)}/initiate${suffix}`,
            undefined,
            { 'X-Agently-Csrf': csrf },
        );
    }

    /**
     * Build the full IDP login redirect URL (v1 extension).
     * The backend responds with a 307 redirect to the IDP — callers
     * should use `window.location.assign()` rather than fetch.
     */
    idpLoginURL(returnURL?: string): string {
        const base = `${this.baseURL}/api/auth/idp/login`;
        if (returnURL) {
            return `${base}?returnURL=${encodeURIComponent(returnURL)}`;
        }
        return base;
    }

    /**
     * Full-page redirect to the IDP login.
     *
     * Saves the current URL in sessionStorage so that the OAuth callback
     * handler can redirect back to it after authentication completes.
     * The callback SPA route should call `getLoginReturnURL()` to retrieve it.
     */
    loginWithRedirect(): void {
        if (typeof window === 'undefined') return;
        const returnURL = `${window.location.pathname}${window.location.search}${window.location.hash}`;
        try {
            sessionStorage.setItem('agently.oauth.returnURL', returnURL);
        } catch { /* storage unavailable */ }
        window.location.assign(this.idpLoginURL(returnURL));
    }

    /**
     * Retrieve the saved returnURL after an OAuth callback completes.
     * Clears the stored value. Returns '/' if nothing was saved.
     */
    static getLoginReturnURL(): string {
        if (typeof sessionStorage === 'undefined') return '/';
        try {
            const saved = sessionStorage.getItem('agently.oauth.returnURL');
            sessionStorage.removeItem('agently.oauth.returnURL');
            return saved || '/';
        } catch {
            return '/';
        }
    }

    /**
     * Open the IDP login flow in a popup window.
     *
     * The backend's OAuth callback handler posts `{type:'oauth',status:'ok'}`
     * to the opener window and closes the popup. This method listens for that
     * message and resolves when authentication completes.
     *
     * If popups are blocked, falls back to a full-page redirect to the IDP
     * login URL with the current page as the returnURL.
     *
     * @returns Promise that resolves to `true` on successful auth, `false` if
     *          the popup was closed without completing auth.
     */
    loginWithPopup(opts?: {
        /** Window features string (default: centered 520x660). */
        windowFeatures?: string;
        /** Called when auth succeeds (before promise resolves). */
        onSuccess?: () => void;
        /** Called when popup is blocked and falling back to redirect. */
        onPopupBlocked?: () => void;
    }): Promise<boolean> {
        if (typeof window === 'undefined') {
            return Promise.resolve(false);
        }

        const url = this.idpLoginURL();
        const width = 520;
        const height = 660;
        const left = Math.round(window.screenX + (window.outerWidth - width) / 2);
        const top = Math.round(window.screenY + (window.outerHeight - height) / 2);
        const features = opts?.windowFeatures
            ?? `width=${width},height=${height},left=${left},top=${top},toolbar=no,menubar=no,scrollbars=yes`;

        let popup: Window | null = null;
        try {
            popup = window.open(url, 'agently_login', features);
        } catch { /* popup blocked */ }

        if (!popup || popup.closed) {
            opts?.onPopupBlocked?.();
            const returnURL = `${window.location.pathname}${window.location.search}${window.location.hash}`;
            window.location.assign(this.idpLoginURL(returnURL));
            return Promise.resolve(false);
        }

        return new Promise<boolean>((resolve) => {
            let settled = false;

            const onMessage = (event: MessageEvent) => {
                const data = event?.data;
                if (!data || typeof data !== 'object' || data.type !== 'oauth') return;
                cleanup();
                settled = true;
                if (data.status === 'ok') {
                    opts?.onSuccess?.();
                    resolve(true);
                } else {
                    resolve(false);
                }
            };

            const pollTimer = window.setInterval(() => {
                if (!popup || popup.closed) {
                    cleanup();
                    if (!settled) resolve(false);
                }
            }, 500);

            const cleanup = () => {
                window.removeEventListener('message', onMessage);
                window.clearInterval(pollTimer);
            };

            window.addEventListener('message', onMessage);
        });
    }

    // ── Internal HTTP ────────────────────────────────────────────────────────

    private async get<T = APIResponse>(path: string, params?: URLSearchParams): Promise<T> {
        const qs = params?.toString();
        const url = qs ? `${this.baseURL}${path}?${qs}` : `${this.baseURL}${path}`;
        return this.request<T>('GET', url);
    }

    private async post<T = APIResponse>(path: string, body: RequestBody): Promise<T> {
        return this.request<T>('POST', `${this.baseURL}${path}`, body);
    }

    private async put<T = APIResponse>(path: string, body: RequestBody): Promise<T> {
        return this.request<T>('PUT', `${this.baseURL}${path}`, body);
    }

    private async patch<T = APIResponse>(path: string, body: RequestBody): Promise<T> {
        return this.request<T>('PATCH', `${this.baseURL}${path}`, body);
    }

    private async del(path: string): Promise<void> {
        await this.request('DELETE', `${this.baseURL}${path}`);
    }

    private async request<T = APIResponse>(method: string, url: string, body?: RequestBody, extraHeaders: Record<string, string> = {}, format: 'json' | 'text' = 'json'): Promise<T> {
        const maxAttempts = Math.max(1, this.retries);
        let lastErr: unknown = null;

        for (let attempt = 1; attempt <= maxAttempts; attempt++) {
            const headers = await this.authHeaders();
            Object.assign(headers, extraHeaders);
            if (body !== undefined) {
                headers['Content-Type'] = 'application/json';
            }

            const controller = this.timeoutMs > 0 ? new AbortController() : null;
            const timer = controller ? setTimeout(() => controller.abort(), this.timeoutMs) : null;

            try {
                const resp = await this.fetchImpl(url, {
                    method,
                    headers,
                    body: body !== undefined ? JSON.stringify(body) : undefined,
                    credentials: this.useCookies ? 'include' : 'same-origin',
                    signal: controller?.signal,
                });

                if (!resp.ok) {
                    const httpError = await this.toHttpError(resp);
                    lastErr = httpError;
                    if (this.shouldRetry(method, resp.status) && attempt < maxAttempts) {
                        await sleep(this.retryDelayMs);
                        continue;
                    }
                    if (httpError.status === 401) {
                        this.onUnauthorizedHook?.(httpError);
                    } else {
                        this.onErrorHook?.(httpError);
                    }
                    throw lastErr;
                }

                const text = await resp.text();
                if (format === 'text') {
                    const expectedType = url.endsWith('.css') ? 'text/css' : 'application/json';
                    if (!String(resp.headers.get('content-type') || '').startsWith(expectedType)) throw new Error('Unexpected workspace asset content type');
                    return text as T;
                }
                return (text ? JSON.parse(text) : undefined) as T;
            } catch (err) {
                if (err instanceof HttpError) throw err;
                lastErr = err;
                if (this.shouldRetry(method, 0) && attempt < maxAttempts) {
                    await sleep(this.retryDelayMs);
                    continue;
                }
                this.onErrorHook?.(new HttpError(0, 'NetworkError', String((err as Error)?.message || err || 'network error')));
                throw err;
            } finally {
                if (timer) clearTimeout(timer);
            }
        }

        throw lastErr ?? new Error('request failed');
    }

    private async authHeaders(): Promise<Record<string, string>> {
        const headers: Record<string, string> = { 'Accept': 'application/json', ...this.staticHeaders };
        const token = this.tokenProvider ? await this.tokenProvider() : null;
        if (token) {
            headers['Authorization'] = `Bearer ${token}`;
        }
        if (this.sessionDebug?.enabled !== false) {
            const hasDebug = !!this.sessionDebug && (
                !!String(this.sessionDebug.level || '').trim()
                || (Array.isArray(this.sessionDebug.components) && this.sessionDebug.components.length > 0)
                || this.sessionDebug.enabled === true
            );
            if (hasDebug) {
                headers['X-Agently-Debug'] = 'true';
                const level = String(this.sessionDebug?.level || '').trim();
                if (level) headers['X-Agently-Debug-Level'] = level;
                const components = (Array.isArray(this.sessionDebug?.components) ? this.sessionDebug.components : [])
                    .map((value) => String(value || '').trim())
                    .filter(Boolean);
                if (components.length > 0) {
                    headers['X-Agently-Debug-Components'] = components.join(',');
                }
            }
        }
        return headers;
    }

    private async toHttpError(resp: Response): Promise<HttpError> {
        const body = await resp.text().catch(() => '');
        return new HttpError(resp.status, resp.statusText, body);
    }

    private shouldRetry(method: string, status: number): boolean {
        const m = method.toUpperCase();
        if (m !== 'GET' && m !== 'HEAD') return false;
        if (status <= 0) return true;
        return this.retryStatuses.has(status);
    }

    private applyPage(q: URLSearchParams, page?: { limit?: number; cursor?: string; direction?: string }) {
        if (!page) return;
        if (page.limit) q.set('limit', String(page.limit));
        if (page.cursor) q.set('cursor', page.cursor);
        if (page.direction) q.set('direction', page.direction);
    }
}

// ─── Helpers ───────────────────────────────────────────────────────────────────

function enc(s: string): string {
    return encodeURIComponent(s);
}

function targetContextQuery(targetContext?: MetadataTargetContext): URLSearchParams {
    const q = new URLSearchParams();
    const platform = targetContext?.platform?.trim();
    const formFactor = targetContext?.formFactor?.trim();
    const surface = targetContext?.surface?.trim();
    if (platform) q.set('platform', platform);
    if (formFactor) q.set('formFactor', formFactor);
    if (surface) q.set('surface', surface);
    for (const capability of Array.isArray(targetContext?.capabilities) ? targetContext.capabilities : []) {
        const trimmed = capability.trim();
        if (trimmed) q.append('capabilities', trimmed);
    }
    return q;
}

function isJSONObject(value: JSONValue): value is JSONObject {
    return value !== null && typeof value === 'object' && !Array.isArray(value);
}

function safeParseJSON(raw: string): JSONValue {
    try {
        return JSON.parse(raw) as JSONValue;
    } catch {
        return {};
    }
}

function compactObject(input: Record<string, unknown>): JSONObject {
    const out: JSONObject = {};
    for (const [key, value] of Object.entries(input)) {
        if (value === undefined || value === null) {
            continue;
        }
        if (Array.isArray(value) && value.length === 0) {
            continue;
        }
        out[key] = value as JSONValue;
    }
    return out;
}

function normalizeWorkspaceMetadata(value: JSONValue): WorkspaceMetadata {
    const payload = isJSONObject(value) && Object.prototype.hasOwnProperty.call(value, 'data')
        ? value.data
        : value;
    const metadata = (isJSONObject(payload) ? payload : {}) as WorkspaceMetadata;
    return {
        ...metadata,
        defaultAgent: metadata.defaultAgent ?? metadata.defaults?.agent,
        defaultModel: metadata.defaultModel ?? metadata.defaults?.model,
        defaultEmbedder: metadata.defaultEmbedder ?? metadata.defaults?.embedder,
    };
}

function sleep(ms: number): Promise<void> {
    return new Promise((r) => setTimeout(r, ms));
}
