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
    UpdateConversationInput,
    Message, MessagePage, GetMessagesInput,
    Turn, TranscriptOutput, GetTranscriptInput, GetTranscriptOptions, QuerySelector,
    QueryInput, QueryOutput,
    SteerTurnInput, SteerTurnOutput, MoveQueuedTurnInput, EditQueuedTurnInput,
    SSEEvent, StreamEventsInput,
    PendingElicitation, ResolveElicitationInput,
    PendingToolApproval, DecideToolApprovalInput, DecideToolApprovalOutput,
    FileEntry, UploadFileOutput,
    Resource, ResourceRef, RunView,
    Schedule, ScheduleListOutput,
    WorkspaceMetadata, PayloadView, GetPayloadOptions,
    ListLinkedConversationsInput, LinkedConversationPage,
    AuthProvider, AuthUser, LocalLoginInput, LocalLoginOutput,
    OAuthInitiateOutput, OAuthCallbackInput, OAuthCallbackOutput,
    OAuthConfigOutput, CreateSessionInput, CreateSessionOutput,
    OOBLoginInput, IDPDelegateOutput,
    FeedSpec, JSONObject, JSONValue,
} from './types';
import { HttpError } from './errors';
import { normalizeStreamEventIdentity } from './streamIdentity';

// ─── Options ───────────────────────────────────────────────────────────────────

export type TokenProvider = () => Promise<string | null> | string | null;

export interface ClientOptions {
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
}

type RequestBody = JSONValue | undefined;
type APIResponse = JSONValue | undefined;

// ─── Client ────────────────────────────────────────────────────────────────────

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
    }

    // ── Conversations ────────────────────────────────────────────────────────

    /** Create a new conversation. */
    async createConversation(input: CreateConversationInput): Promise<Conversation> {
        return this.post<Conversation>('/conversations', input);
    }

    /** List conversations with optional search, filter, and pagination. */
    async listConversations(input?: ListConversationsInput): Promise<ConversationPage> {
        const q = new URLSearchParams();
        if (input?.query) q.set('q', input.query);
        if (input?.agentId) q.set('agentId', input.agentId);
        if (input?.excludeScheduled) q.set('excludeScheduled', 'true');
        if (input?.status) q.set('status', input.status);
        this.applyPage(q, input?.page);
        const out = await this.get<ConversationPage | { Rows?: Conversation[]; NextCursor?: string; PrevCursor?: string; HasMore?: boolean }>('/conversations', q);
        if (Array.isArray(out?.Rows)) {
            return {
                data: out.Rows,
                page: {
                    cursor: out.NextCursor,
                    prevCursor: out.PrevCursor,
                    hasMore: out.HasMore,
                },
            };
        }
        return out;
    }

    /** Get a single conversation by ID. */
    async getConversation(id: string): Promise<Conversation> {
        return this.get<Conversation>(`/conversations/${enc(id)}`);
    }

    /** Update mutable conversation fields such as visibility and shareability. */
    async updateConversation(
        id: string, input: UpdateConversationInput,
    ): Promise<Conversation> {
        return this.patch<Conversation>(`/conversations/${enc(id)}`, input);
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
        if (Array.isArray(out?.Rows)) {
            return {
                data: out.Rows,
                page: {
                    cursor: out.NextCursor,
                    hasMore: out.HasMore,
                },
            };
        }
        return out;
    }

    // ── Transcript ───────────────────────────────────────────────────────────

    /** Get structured turn-based transcript with optional tool/model call details. */
    async getTranscript(input: GetTranscriptInput, options?: GetTranscriptOptions): Promise<TranscriptOutput> {
        const q = new URLSearchParams();
        if (input.since) q.set('since', input.since);
        if (input.includeModelCalls) q.set('includeModelCalls', 'true');
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
        return this.get<TranscriptOutput>(`/conversations/${enc(input.conversationId)}/transcript`, q);
    }

    // ── Query ────────────────────────────────────────────────────────────────

    /**
     * Send a user message and run the agent's ReAct loop.
     * If a turn is already running, the new turn is automatically queued.
     */
    async query(input: QueryInput): Promise<QueryOutput> {
        return this.post<QueryOutput>('/agent/query', input);
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
            { content: input.content, role: input.role || 'user' },
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
        },
    ): { close: () => void } {
        const url = `${this.baseURL}/stream?conversationId=${enc(conversationId)}`;
        const es = new EventSource(url, { withCredentials: this.useCookies });
        let closed = false;

        es.onmessage = (ev) => {
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
                    case 'tool_call_delta':
                    case 'tool_call_completed':
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
        if (Array.isArray(out?.rows)) return out.rows;
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
    async listPendingToolApprovals(input?: {
        userId?: string;
        conversationId?: string;
        status?: string;
    }): Promise<PendingToolApproval[]> {
        const q = new URLSearchParams();
        if (input?.userId) q.set('userId', input.userId);
        if (input?.conversationId) q.set('conversationId', input.conversationId);
        if (input?.status) q.set('status', input.status);
        const out = await this.get<PendingToolApproval[] | { data?: PendingToolApproval[]; rows?: PendingToolApproval[] }>('/tool-approvals/pending', q);
        if (Array.isArray(out?.data)) return out.data;
        if (Array.isArray(out?.rows)) return out.rows;
        if (Array.isArray(out)) return out;
        return [];
    }

    /** Approve or reject a queued tool execution. */
    async decideToolApproval(
        id: string, input: DecideToolApprovalInput,
    ): Promise<DecideToolApprovalOutput> {
        return this.post<DecideToolApprovalOutput>(`/tool-approvals/${enc(id)}/decision`, input);
    }

    // ── Tools ────────────────────────────────────────────────────────────────

    /** Execute a registered tool by name. */
    async executeTool(name: string, args?: JSONObject): Promise<string> {
        const res = await this.post<JSONValue | undefined>(`/tools/${enc(name)}/execute`, args ?? {});
        return typeof res === 'string' ? res : (res?.result ?? JSON.stringify(res));
    }

    // ── Files ────────────────────────────────────────────────────────────────

    /** Upload a file associated with a conversation. */
    async uploadFile(
        conversationId: string, file: File | Blob, name?: string,
    ): Promise<UploadFileOutput> {
        void conversationId;
        void file;
        void name;
        throw new Error('File upload routes (/v1/files) are not exposed by sdk/handler.go HTTP routes');
    }

    /** List files for a conversation. */
    async listFiles(conversationId: string): Promise<FileEntry[]> {
        const q = new URLSearchParams({ conversationId });
        const out = await this.get<FileEntry[] | { files?: FileEntry[]; Files?: FileEntry[] }>('/files', q);
        if (Array.isArray(out?.files)) return out.files;
        if (Array.isArray(out?.Files)) return out.Files;
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
        if (Array.isArray(out?.agents)) return out.agents;
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

    /** Trigger an immediate run of a schedule. */
    async runScheduleNow(id: string): Promise<void> {
        await this.post(`/api/agently/scheduler/run-now/${enc(id)}`, {});
    }

    // ── Workspace Metadata ────────────────────────────────────────────────────

    /** Get workspace metadata (available agents, models, defaults, capabilities). */
    async getWorkspaceMetadata(): Promise<WorkspaceMetadata> {
        return this.get<WorkspaceMetadata>('/workspace/metadata');
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
        if (Array.isArray(out?.Rows)) {
            return {
                data: out.Rows,
                page: {
                    cursor: out.NextCursor,
                    prevCursor: out.PrevCursor,
                    hasMore: out.HasMore,
                },
            };
        }
        if (Array.isArray(out?.rows)) {
            return {
                data: out.rows,
                page: {
                    cursor: out.nextCursor ?? out.cursor,
                    prevCursor: out.prevCursor,
                    hasMore: out.hasMore,
                },
            };
        }
        return out;
    }

    // ── Auth ─────────────────────────────────────────────────────────────────

    /** List available auth providers (local, bff, oidc, jwt). */
    async getAuthProviders(): Promise<AuthProvider[]> {
        const out = await this.get<AuthProvider[] | { providers?: AuthProvider[] }>('/api/auth/providers');
        if (Array.isArray(out?.providers)) return out.providers;
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

    /** Login with a local username. */
    async localLogin(input: LocalLoginInput): Promise<LocalLoginOutput> {
        return this.post<LocalLoginOutput>('/api/auth/local/login', input);
    }

    /** Logout and destroy the current session. */
    async logout(): Promise<void> {
        await this.post('/api/auth/logout', {});
    }

    /** Initiate an OAuth BFF flow (returns authURL + state for redirect). */
    async oauthInitiate(): Promise<OAuthInitiateOutput> {
        return this.post<OAuthInitiateOutput>('/api/auth/oauth/initiate', {});
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

    private async request<T = APIResponse>(method: string, url: string, body?: RequestBody): Promise<T> {
        const maxAttempts = Math.max(1, this.retries);
        let lastErr: unknown = null;

        for (let attempt = 1; attempt <= maxAttempts; attempt++) {
            const headers = await this.authHeaders();
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
                    lastErr = await this.toHttpError(resp);
                    if (this.shouldRetry(method, resp.status) && attempt < maxAttempts) {
                        await sleep(this.retryDelayMs);
                        continue;
                    }
                    if (lastErr.status === 401) {
                        this.onUnauthorizedHook?.(lastErr);
                    } else {
                        this.onErrorHook?.(lastErr);
                    }
                    throw lastErr;
                }

                const text = await resp.text();
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

function sleep(ms: number): Promise<void> {
    return new Promise((r) => setTimeout(r, ms));
}
