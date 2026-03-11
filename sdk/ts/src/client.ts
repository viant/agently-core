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
    Turn, TranscriptOutput, GetTranscriptInput,
    QueryInput, QueryOutput,
    SteerTurnInput, SteerTurnOutput, MoveQueuedTurnInput, EditQueuedTurnInput,
    SSEEvent, StreamEventsInput,
    PendingElicitation, ResolveElicitationInput,
    PendingToolApproval, DecideToolApprovalInput, DecideToolApprovalOutput,
    FileEntry, UploadFileOutput,
    Resource, ResourceRef, RunView,
    Schedule, ScheduleListOutput,
} from './types';
import { HttpError } from './errors';

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
}

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
    }

    // ── Conversations ────────────────────────────────────────────────────────

    /** Create a new conversation. */
    async createConversation(input: CreateConversationInput): Promise<Conversation> {
        return this.post('/conversations', input);
    }

    /** List conversations with optional search, filter, and pagination. */
    async listConversations(input?: ListConversationsInput): Promise<ConversationPage> {
        const q = new URLSearchParams();
        if (input?.query) q.set('q', input.query);
        if (input?.agentId) q.set('agentId', input.agentId);
        if (input?.status) q.set('status', input.status);
        this.applyPage(q, input?.page);
        const out = await this.get('/conversations', q);
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

    /** Get a single conversation by ID. */
    async getConversation(id: string): Promise<Conversation> {
        return this.get(`/conversations/${enc(id)}`);
    }

    /** Update mutable conversation fields such as visibility and shareability. */
    async updateConversation(
        id: string, input: UpdateConversationInput,
    ): Promise<Conversation> {
        return this.patch(`/conversations/${enc(id)}`, input);
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
        const out = await this.get('/messages', q);
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
    async getTranscript(input: GetTranscriptInput): Promise<TranscriptOutput> {
        const q = new URLSearchParams();
        if (input.since) q.set('since', input.since);
        if (input.includeModelCalls) q.set('includeModelCalls', 'true');
        if (input.includeToolCalls) q.set('includeToolCalls', 'true');
        return this.get(`/conversations/${enc(input.conversationId)}/transcript`, q);
    }

    // ── Query ────────────────────────────────────────────────────────────────

    /**
     * Send a user message and run the agent's ReAct loop.
     * If a turn is already running, the new turn is automatically queued.
     */
    async query(input: QueryInput): Promise<QueryOutput> {
        return this.post('/agent/query', input);
    }

    // ── Turns ────────────────────────────────────────────────────────────────

    /** Cancel a running turn. Returns true if a running turn was found. */
    async cancelTurn(turnId: string): Promise<boolean> {
        const res = await this.post(`/turns/${enc(turnId)}/cancel`, {});
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
        return this.post(
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
        return this.post(
            `/conversations/${enc(conversationId)}/turns/${enc(turnId)}/force-steer`,
            {},
        );
    }

    // ── SSE Streaming ────────────────────────────────────────────────────────

    /**
     * Subscribe to real-time streaming events for a conversation.
     * Returns an object with a close() method to unsubscribe.
     *
     * Events are delivered via the onEvent callback as parsed SSEEvent objects.
     * Convenience callbacks (onChunk, onTool, onDone, onError) are also available.
     */
    streamEvents(
        conversationId: string,
        handlers: {
            onChunk?: (content: string) => void;
            onTool?: (toolName: string, args?: Record<string, any>) => void;
            onDone?: () => void;
            onError?: (error: string) => void;
            onEvent?: (event: SSEEvent) => void;
        },
    ): { close: () => void } {
        const url = `${this.baseURL}/stream?conversationId=${enc(conversationId)}`;
        const es = new EventSource(url, { withCredentials: this.useCookies });

        es.onmessage = (ev) => {
            try {
                const event: SSEEvent = JSON.parse(ev.data);
                handlers.onEvent?.(event);
                switch (event.type) {
                    case 'chunk':
                        handlers.onChunk?.(event.content ?? '');
                        break;
                    case 'tool':
                        handlers.onTool?.(event.toolName ?? '', event.arguments);
                        break;
                    case 'done':
                        handlers.onDone?.();
                        break;
                    case 'error':
                        handlers.onError?.(event.error ?? 'Unknown error');
                        break;
                }
            } catch { /* ignore malformed events */ }
        };

        es.onerror = () => {
            handlers.onError?.('SSE connection error');
        };

        return { close: () => es.close() };
    }

    // ── Elicitations ─────────────────────────────────────────────────────────

    /** List pending elicitation prompts for a conversation. */
    async listPendingElicitations(conversationId: string): Promise<PendingElicitation[]> {
        const q = new URLSearchParams({ conversationId });
        const out = await this.get('/elicitations', q);
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
        const out = await this.get('/tool-approvals/pending', q);
        if (Array.isArray(out?.data)) return out.data;
        if (Array.isArray(out?.rows)) return out.rows;
        if (Array.isArray(out)) return out;
        return [];
    }

    /** Approve or reject a queued tool execution. */
    async decideToolApproval(
        id: string, input: DecideToolApprovalInput,
    ): Promise<DecideToolApprovalOutput> {
        return this.post(`/tool-approvals/${enc(id)}/decision`, input);
    }

    // ── Tools ────────────────────────────────────────────────────────────────

    /** Execute a registered tool by name. */
    async executeTool(name: string, args?: Record<string, any>): Promise<string> {
        const res = await this.post(`/tools/${enc(name)}/execute`, args ?? {});
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
        void conversationId;
        throw new Error('File list routes (/v1/files) are not exposed by sdk/handler.go HTTP routes');
    }

    // ── Workspace Resources ──────────────────────────────────────────────────

    /** List resource names for a workspace kind (agent, model, embedder, etc). */
    async listResources(kind?: string): Promise<{ names: string[] }> {
        const q = new URLSearchParams();
        if (kind) q.set('kind', kind);
        return this.get('/workspace/resources', q);
    }

    /** Get a single workspace resource content. */
    async getResource(kind: string, name: string): Promise<{ kind: string; name: string; data: string }> {
        return this.get(`/workspace/resources/${enc(kind)}/${enc(name)}`);
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
        return this.post('/workspace/resources/export', { kinds });
    }

    /** Import resources in bulk. */
    async importResources(resources: Resource[], replace?: boolean): Promise<{ imported: number; skipped: number }> {
        return this.post('/workspace/resources/import', { resources, replace });
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
        void conversationId;
        void fileId;
        throw new Error('File download routes (/v1/files/{id}) are not exposed by sdk/handler.go HTTP routes');
    }

    // ── A2A (Agent-to-Agent) ─────────────────────────────────────────────────

    /** Get the A2A agent card for a given agent. */
    async getA2AAgentCard(agentId: string): Promise<any> {
        return this.get(`/api/a2a/agents/${enc(agentId)}/card`);
    }

    /** Send a message to an A2A agent. */
    async sendA2AMessage(agentId: string, request: any): Promise<any> {
        return this.post(`/api/a2a/agents/${enc(agentId)}/message`, request);
    }

    /** List agent IDs that have A2A serving enabled. */
    async listA2AAgents(agentIds?: string[]): Promise<string[]> {
        const q = new URLSearchParams();
        if (agentIds?.length) q.set('ids', agentIds.join(','));
        const out = await this.get('/api/a2a/agents', q);
        if (Array.isArray(out?.agents)) return out.agents;
        if (Array.isArray(out)) return out;
        return [];
    }

    // ── Scheduler ─────────────────────────────────────────────────────────────

    /** Get a single schedule by ID. */
    async getSchedule(id: string): Promise<Schedule> {
        return this.get(`/api/agently/scheduler/schedule/${enc(id)}`);
    }

    /** List all schedules. */
    async listSchedules(): Promise<ScheduleListOutput> {
        return this.get('/api/agently/scheduler/');
    }

    /** Batch create or update schedules. */
    async upsertSchedules(schedules: Schedule[]): Promise<void> {
        await this.patch('/api/agently/scheduler/', { schedules });
    }

    /** Trigger an immediate run of a schedule. */
    async runScheduleNow(id: string): Promise<void> {
        await this.post(`/api/agently/scheduler/run-now/${enc(id)}`, {});
    }

    // ── Internal HTTP ────────────────────────────────────────────────────────

    private async get(path: string, params?: URLSearchParams): Promise<any> {
        const qs = params?.toString();
        const url = qs ? `${this.baseURL}${path}?${qs}` : `${this.baseURL}${path}`;
        return this.request('GET', url);
    }

    private async post(path: string, body: any): Promise<any> {
        return this.request('POST', `${this.baseURL}${path}`, body);
    }

    private async put(path: string, body: any): Promise<any> {
        return this.request('PUT', `${this.baseURL}${path}`, body);
    }

    private async patch(path: string, body: any): Promise<any> {
        return this.request('PATCH', `${this.baseURL}${path}`, body);
    }

    private async del(path: string): Promise<void> {
        await this.request('DELETE', `${this.baseURL}${path}`);
    }

    private async request(method: string, url: string, body?: any): Promise<any> {
        const maxAttempts = Math.max(1, this.retries);
        let lastErr: any = null;

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
                    throw lastErr;
                }

                const text = await resp.text();
                return text ? JSON.parse(text) : undefined;
            } catch (err) {
                if (err instanceof HttpError) throw err;
                lastErr = err;
                if (this.shouldRetry(method, 0) && attempt < maxAttempts) {
                    await sleep(this.retryDelayMs);
                    continue;
                }
                throw err;
            } finally {
                if (timer) clearTimeout(timer);
            }
        }

        throw lastErr ?? new Error('request failed');
    }

    private async authHeaders(): Promise<Record<string, string>> {
        const headers: Record<string, string> = { ...this.staticHeaders };
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
