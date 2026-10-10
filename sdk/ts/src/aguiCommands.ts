import { AgUiClient, agentlyCommandProps, type AgentCapabilities, type HttpAgentConfig } from './agui';
import { parseSSEStream } from '@ag-ui/client';
import { boundedSSEFrames, preserveSSEFrames } from './aguiSSEFrames';
import { Subject, type Observable } from 'rxjs';
import type { Event, Message, State, RunAgentInput, ResumeEntry } from '@ag-ui/core';
import { MessageSchema } from '@ag-ui/core/schemas';
import type { CanonicalConversationState } from './chatStore/types';
import type { CreateGoalInput, UpdateGoalInput, Goal, GoalControllerSchedule, FeedSpec, FeedPresentation, FetchDatasourceInput, FetchDatasourceOutput, InvalidateDatasourceCacheInput, ListLookupRegistryInput, ListLookupRegistryOutput, ResourceRef } from './types';

export type AgUiJsonPatch =
    | { op: 'add' | 'replace' | 'test'; path: string; value: unknown }
    | { op: 'remove'; path: string }
    | { op: 'move' | 'copy'; path: string; from: string };
export interface AgUiStateResult { version: '1'; hash: string; state: unknown }
/** The AG-UI goal snapshot explicitly preserves absent budgets and schedules. */
export interface AgUiGoalResult {
    version: '1';
    goal: (Omit<Goal, 'tokenBudget' | 'controllerSchedule'> & { tokenBudget: number | null; controllerSchedule: GoalControllerSchedule | null }) | null;
    cleared?: boolean;
}
/** Resource content is base64 on the wire, not authoring text. */
export interface AgUiResource extends ResourceRef { data?: string; content?: string; contentType?: string; updatedAt?: string }
export interface AgUiFeedResult {
    feedId: string; title: string; developerOnly: boolean; presentation: FeedPresentation | null;
    data: unknown; dataSources: unknown; ui: unknown;
}
export interface AgUiRunResult {
    version: '1'; threadId: string; runId: string; parentRunId: string; status: string; revision: number; lastSequence: number;
    resumedByRunId?: string;
}

export interface AgUiConversationBootstrapInput {
    mode?: 'transcript' | 'live'; since?: string;
    includeModelCalls?: boolean; includeModelPayloads?: boolean; includeToolCalls?: boolean; includeFeeds?: boolean;
    selectors?: Record<string, { path?: string; limit?: number; offset?: number; orderBy?: string }>;
}
/** Exact canonical response, including future fields; no legacy turns-only normalization. */
export interface AgUiCanonicalTranscript {
    schemaVersion: string; conversation: CanonicalConversationState;
    feeds?: unknown[]; usage?: Record<string, unknown>; eventCursor?: string;
    [key: string]: unknown;
}
export interface AgUiConversationBootstrapResult {
    version: '1'; threadId: string; transcript: AgUiCanonicalTranscript;
    messages: Message[]; state: State | null;
    /** Authorized renderer-only activities; never seed a model session with these. */
    hostActivities?: Extract<Message, { role: 'activity' }>[];
    unavailableHostActivityIds?: string[];
    runs: Array<Omit<AgUiRunResult, 'version' | 'parentRunId'> & { parentRunId?: string; kind?: 'chat' | 'resource' | 'mcp-app' }>;
    /** False means canonical presentation history cannot prove lossless protocol reconstruction. */
    projection: { lossless: boolean; unavailableMessageIds: string[] };
}

type EmptyPayload = Record<string, never>;
type Definition<I, R> = { input: I; result: R };
/** Only deterministic commands implemented by the current backend. Subscriptions have a separate lifecycle. */
export interface AgUiCommandDefinitions {
    'approval.decide': Definition<{ originalRunId: string; originalThreadId?: string; approvalId: string; answer: ResumeEntry }, import('./types').DecideToolApprovalOutput>;
    'conversation.bootstrap': Definition<AgUiConversationBootstrapInput, AgUiConversationBootstrapResult>;
    'state.get': Definition<EmptyPayload, AgUiStateResult>;
    'state.patch': Definition<{ patch: AgUiJsonPatch[]; ifMatch?: string }, AgUiStateResult>;
    'goal.get': Definition<EmptyPayload, AgUiGoalResult>;
    'goal.create': Definition<Omit<CreateGoalInput, 'controllerSpec'> & { controllerSpec?: string | Record<string, unknown> }, AgUiGoalResult>;
    'goal.update': Definition<UpdateGoalInput, AgUiGoalResult>;
    'goal.clear': Definition<EmptyPayload, AgUiGoalResult>;
    'goal.pause': Definition<{ reason?: string }, AgUiGoalResult>;
    'goal.resume': Definition<EmptyPayload, AgUiGoalResult>;
    // Preserve metadata's exact server DTO; normalization belongs to the shell adapter.
    'workspace.metadata.get': Definition<EmptyPayload, Record<string, unknown>>;
    'workspace.publicagents.list': Definition<EmptyPayload, { agentInfos: Record<string, unknown>[] }>;
    'workspace.layout.get': Definition<EmptyPayload, { schemaVersion: number; layoutRevision: string; workspaceId: string; catalogRevisions?: Record<string, string>; layout: Record<string, unknown> | null }>;
    'workspace.tools.list': Definition<{ pattern?: string }, { data: Record<string, unknown>[] }>;
    'workspace.models.list': Definition<EmptyPayload, { data: Record<string, unknown>[] }>;
    'workspace.model.get': Definition<{ id: string }, { data: Record<string, unknown> }>;
    'workspace.model.save': Definition<{ id: string; update: Record<string, unknown> }, { data: Record<string, unknown> }>;
    'workspace.resource.list': Definition<{ kind: string }, { names?: string[]; resources?: ResourceRef[] | null }>;
    'workspace.resource.get': Definition<ResourceRef, { kind?: string; name?: string; data?: string; resource?: AgUiResource }>;
    'workspace.resource.save': Definition<ResourceRef & { data: string }, { saved: boolean }>;
    'workspace.resource.delete': Definition<ResourceRef, { deleted: boolean }>;
    'workspace.resource.export': Definition<{ kinds?: string[] }, { data?: string; resources?: AgUiResource[] }>;
    'workspace.resource.import': Definition<{ resources: AgUiResource[]; replace?: boolean }, { imported: number; skipped: number }>;
    'datasource.fetch': Definition<Omit<FetchDatasourceInput, 'conversationId'>, Omit<FetchDatasourceOutput, 'rows'> & { rows: FetchDatasourceOutput['rows'] | null }>;
    'datasource.cache.invalidate': Definition<InvalidateDatasourceCacheInput, { invalidated: boolean }>;
    'lookup.registry': Definition<ListLookupRegistryInput, ListLookupRegistryOutput>;
    'feed.list': Definition<EmptyPayload, { feeds: FeedSpec[] }>;
    'feed.get': Definition<{ id: string }, AgUiFeedResult>;
    'run.get': Definition<{ runId: string }, AgUiRunResult>;
    'run.cancel': Definition<{ runId: string }, { version: '1'; runId: string; cancelled: boolean }>;
    'run.events.list': Definition<{ runId: string; after?: number; limit?: number }, { version: '1'; runId: string; events: { cursor: string; sequence: number; event: Event }[]; hasMore: boolean }>;
}
export type AgUiCommandOperation = keyof AgUiCommandDefinitions;
export type AgUiCommandInput<K extends AgUiCommandOperation> = AgUiCommandDefinitions[K]['input'];
export type AgUiCommandResult<K extends AgUiCommandOperation> = AgUiCommandDefinitions[K]['result'];

export interface AgUiCommandsConfig extends HttpAgentConfig {
    /** Same-origin BFF cookies by default; custom fetch remains the authentication boundary. */
    credentials?: RequestCredentials;
}
export interface AgUiCommandIdentity {
    threadId?: string;
    /** Keep both identities and the payload unchanged when replaying an accepted command. */
    runId: string;
    requestId: string;
}
export interface AgUiCommandHandle<R> {
    result: Promise<R | null | undefined>;
    /** Detaches this consumer only. Use an explicit run.cancel command to cancel execution. */
    abortTransport(): void;
    /** Defensive copy of the posted input; server admission requires backend evidence. */
    readonly lastPostedInput: RunAgentInput | undefined;
}
export class AgUiCommandError extends Error {
    constructor(message: string, readonly code?: string, readonly outcome?: string) { super(message); this.name = 'AgUiCommandError'; }
}

/** Independent protocol runs; never reuse chat messages, state, tools, or pending interrupts. */
export class AgUiCommands {
    private readonly config: HttpAgentConfig;
    constructor(config: AgUiCommandsConfig) {
        const { credentials = 'include', ...transport } = config;
        const fetchImpl = transport.fetch ?? globalThis.fetch.bind(globalThis);
        this.config = {
            ...transport, headers: transport.headers ? { ...transport.headers } : undefined,
            initialMessages: [], initialState: {},
            fetch: (url, init) => fetchImpl(url, { credentials, ...init }),
        };
    }

    start<K extends AgUiCommandOperation>(operation: K, payload: AgUiCommandInput<K>, identity: AgUiCommandIdentity): AgUiCommandHandle<AgUiCommandResult<K>> {
        const threadId = identity.threadId ?? this.config.threadId;
        const { runId, requestId } = identity;
        if (!threadId?.trim() || !runId?.trim() || !requestId?.trim()) throw new Error('Command requires threadId, runId and requestId');
        let rawResultSeen = false;
        let rawResult: unknown;
        let disposeTap = () => {};
        const fetchImpl = this.config.fetch!;
        const client = new AgUiClient({ ...this.config, threadId, initialMessages: [], initialState: {}, fetch: async (url, init) => {
            const response = boundedSSEFrames(await fetchImpl(url, init));
            if (!response.ok || !response.body || !response.headers.get('content-type')?.includes('text/event-stream')) return response;
            // Upstream's compatibility shim erases result:null. Observe only the raw
            // command result through its official parser; validated reduction stays
            // in HttpAgent; the tap passes those same framed bytes through.
            const tap = tapCommandResult(response, init.signal, threadId, runId, value => { rawResultSeen = true; rawResult = value; });
            disposeTap = tap.dispose;
            return tap.response;
        } });
        // Clone at dispatch, so edits to caller-owned form state cannot change an accepted command.
        const forwardedProps = agentlyCommandProps(operation, requestId, structuredClone(payload) as Record<string, unknown>);
        let terminal = false;
        let terminalError: AgUiCommandError | undefined;
        let result: unknown;
        let customResultSeen = false;
        const execution = client.run({ runId, tools: [], context: [], forwardedProps }, {
            onCustomEvent: ({ event }) => {
                if (operation.startsWith('goal.') && event.name === 'agently.goal.result') {
                    const value = asObject(event.value);
                    const goalResult = asObject(value?.result);
                    if (value?.version === '1' && value.operation === operation && value.requestId === requestId && goalResult?.version === '1' && Object.hasOwn(goalResult, 'goal') && (goalResult.goal === null || asObject(goalResult.goal))) {
                        result = value.result;
                        customResultSeen = true;
                    }
                } else if (operation.startsWith('state.') && event.name === 'agently.state.result') {
                    const value = asObject(event.value);
                    if (value?.version === '1' && typeof value.hash === 'string' && Object.hasOwn(value, 'state')) {
                        result = value;
                        customResultSeen = true;
                    }
                }
            },
            onRunErrorEvent: ({ event }) => { terminal = true; terminalError = new AgUiCommandError(event.message, event.code); },
            onRunFinishedEvent: ({ event, outcome }) => {
                terminal = true;
                if (outcome !== 'success') terminalError = new AgUiCommandError(`Command finished with ${outcome}`, undefined, outcome);
                // Goal/state results use their versioned CUSTOM event; RUN_FINISHED is their success boundary.
                else if (!operation.startsWith('goal.') && !operation.startsWith('state.')) result = rawResultSeen ? rawResult : event.result;
            },
        }).then(() => {
            if (terminalError) throw terminalError;
            if (!terminal) throw new AgUiCommandError('Command stream ended without a terminal event', 'INCOMPLETE_STREAM');
            if ((operation.startsWith('goal.') || operation.startsWith('state.')) && !customResultSeen) throw new AgUiCommandError('Command finished without its supported result event', 'MISSING_COMMAND_RESULT');
            if (operation === 'conversation.bootstrap') validateBootstrapResult(result, threadId);
            return result as AgUiCommandResult<K> | null | undefined;
        }).finally(() => disposeTap());
        return { result: execution, abortTransport: () => client.abortTransport(), get lastPostedInput() { return client.lastPostedInput; } };
    }

    execute<K extends AgUiCommandOperation>(operation: K, payload: AgUiCommandInput<K>, identity: AgUiCommandIdentity): Promise<AgUiCommandResult<K> | null | undefined> {
        return this.start(operation, payload, identity).result;
    }

    /** Explicit opt-in discovery on its own empty run; command dispatch never requires it. */
    discoverCapabilities(identity: Omit<AgUiCommandIdentity, 'requestId'>): Promise<AgentCapabilities> {
        const threadId = identity.threadId ?? this.config.threadId;
        if (!threadId?.trim() || !identity.runId?.trim()) throw new Error('Discovery requires threadId and runId');
        return new AgUiClient({ ...this.config, threadId, initialMessages: [], initialState: {} }).discoverCapabilities({ runId: identity.runId, tools: [], context: [] });
    }
}

function asObject(value: unknown): Record<string, unknown> | undefined {
    return value && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : undefined;
}

function validateBootstrapResult(value: unknown, threadId: string): void {
    const result = asObject(value);
    const transcript = asObject(result?.transcript);
    const conversation = asObject(transcript?.conversation);
    const projection = asObject(result?.projection);
    if (result?.version !== '1' || result.threadId !== threadId || typeof transcript?.schemaVersion !== 'string' || (typeof conversation?.conversationId !== 'string' || !conversation.conversationId || (conversation.conversationId !== threadId && transcript?.aguiThreadId !== threadId)) || !Object.hasOwn(result, 'state') || !Array.isArray(result.messages) || !Array.isArray(result.runs) || typeof projection?.lossless !== 'boolean' || !Array.isArray(projection.unavailableMessageIds) || !projection.unavailableMessageIds.every(id => typeof id === 'string')) {
        throw new AgUiCommandError('Unsupported or malformed conversation bootstrap result', 'INVALID_BOOTSTRAP_RESULT');
    }
    for (const message of result.messages) MessageSchema.parse(message);
}

type ObservableValue<T> = T extends Observable<infer V> ? V : never;
type HttpChunk = Extract<ObservableValue<Parameters<typeof parseSSEStream>[0]>, { data?: Uint8Array }>;
function tapCommandResult(response: Response, signal: AbortSignal | null | undefined, threadId: string, runId: string, receive: (value: unknown) => void) {
    const chunks = new Subject<HttpChunk>();
    let parserError: unknown;
    let disposed = false;
    const parsed = parseSSEStream(chunks).subscribe({
        next: value => {
            const event = asObject(value);
            if (event?.type === 'RUN_FINISHED' && event.threadId === threadId && event.runId === runId && !event.subagentRunId && Object.hasOwn(event, 'result')) receive(event.result);
        },
        error: error => { parserError = error; },
    });
    const dispose = () => {
        if (disposed) return;
        disposed = true;
        chunks.complete();
        parsed.unsubscribe();
        signal?.removeEventListener('abort', dispose);
    };
    signal?.addEventListener('abort', dispose, { once: true });
    if (signal?.aborted) dispose();
    const bytes = response.body!.pipeThrough(new TransformStream<Uint8Array, Uint8Array>({
        transform(chunk, controller) {
            chunks.next({ type: 'data' as HttpChunk['type'], data: chunk });
            if (parserError) { dispose(); controller.error(parserError); }
            else controller.enqueue(chunk);
        },
        flush() { dispose(); },
    }));
    return { response: preserveSSEFrames(new Response(bytes, { status: response.status, statusText: response.statusText, headers: response.headers }), response), dispose };
}
