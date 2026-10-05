import { HttpAgent, type HttpAgentConfig, type RunAgentParameters, type AgentSubscriber, buildResumeArray, isInterruptExpired } from '@ag-ui/client';
import { boundedSSEFrames } from './aguiSSEFrames';
import { ToolMessageSchema, AgentCapabilitiesSchema, RunAgentInputSchema } from '@ag-ui/core/schemas';
import type { AgentCapabilities, Message, Tool, ToolCall, ToolMessage, ResumeEntry, Interrupt, RunAgentInput } from '@ag-ui/core';

export const AGENTLY_EXTENSION_VERSION = '1' as const;
export const AGENTLY_CAPABILITIES_EVENT = 'agently.capabilities' as const;
export type ClientToolResult = Pick<ToolMessage, 'content' | 'error' | 'metadata'>;
export interface ClientTool {
    tool: Tool;
    /** Receives complete parsed JSON arguments. Throwing leaves the call unanswered. */
    validateArguments?: (args: unknown, schema: Tool['parameters'], call: ToolCall) => void;
    execute: (args: unknown, call: ToolCall) => ClientToolResult | Promise<ClientToolResult>;
}
export type InterruptResponse = Omit<ResumeEntry, 'interruptId'>;

export interface AgentlyAttachmentRef { name?: string; uri: string; mime?: string; stagingFolder?: string }
export interface AgentlyExecutionSelection {
    agentId?: string;
    model?: string;
    /** Native backend allow-list, distinct from AG-UI frontend tool definitions. */
    backendTools?: string[];
    toolBundles?: string[];
    autoSelectTools?: boolean;
    autoSummarize?: boolean;
    disableChains?: boolean;
    allowedChains?: string[];
    resourceURIs?: string[];
    toolCallExposure?: string;
    /** Native web submissions use the latest durable state, not HttpAgent's default empty object. */
    useServerState?: boolean;
    reasoningEffort?: string;
    displayQuery?: string;
    /** UI/workspace hints only; never an authenticated identity or resource grant. */
    context?: Record<string, unknown>;
    attachments?: AgentlyAttachmentRef[];
}
export interface AgentlyForwardedProps {
    version: '1';
    operation: 'chat' | 'capabilities';
    requestId?: string;
    payload?: AgentlyExecutionSelection;
}
export interface AgentlyCapabilityProfile {
    version: '1';
    operations: string[];
    execution?: { agentId?: boolean; model?: boolean };
    history?: 'server';
    disconnect?: 'detach';
    replay?: boolean;
    agents?: Array<{ id: string; name?: string }>;
    models?: Array<{ id: string; name?: string }>;
}
export interface AgentlyCapabilitiesEventValue { version: '1'; capabilities: AgentCapabilities }

export function agentlyForwardedProps(extension: AgentlyForwardedProps, forwardedProps: Record<string, unknown> = {}): Record<string, unknown> {
    return { ...forwardedProps, agently: extension };
}

/** Unknown custom events and unknown versions remain observable via subscribe(). */
export function parseAgentlyCapabilities(value: unknown): AgentCapabilities | undefined {
    if (!value || typeof value !== 'object') return undefined;
    const envelope = value as Partial<AgentlyCapabilitiesEventValue>;
    if (envelope.version !== '1' || !envelope.capabilities || typeof envelope.capabilities !== 'object' || Array.isArray(envelope.capabilities)) return undefined;
    return AgentCapabilitiesSchema.safeParse(envelope.capabilities).success ? envelope.capabilities : undefined;
}

export interface AgentlyCommand { version: '1'; operation: string; requestId: string; payload: Record<string, unknown> }
export function agentlyCommandProps(operation: string, requestId: string, payload: Record<string, unknown>, existing: Record<string, unknown> = {}): Record<string, unknown> {
    if (!operation || !requestId) throw new Error('Command requires operation and request identity');
    return { ...existing, agently: { version: '1', operation, requestId, payload } satisfies AgentlyCommand };
}

// Upstream1.0.1 rethrows non-AbortError from reader.cancel() in its detached
// teardown promise. Preserve read failures while making disposal idempotent.
function safeReaderDisposal(response: Response): Response {
    if (!response.body) return response;
    const body = new Proxy(response.body, { get(target, key) {
        if (key === 'getReader') return () => {
            const reader = target.getReader();
            return new Proxy(reader, { get(target, key) {
                if (key === 'cancel') return (reason?: unknown) => target.cancel(reason).catch(() => undefined);
                const member = Reflect.get(target, key, target);
                return typeof member === 'function' ? member.bind(target) : member;
            } });
        };
        const member = Reflect.get(target, key, target);
        return typeof member === 'function' ? member.bind(target) : member;
    } });
    return new Proxy(response, { get(target, key) {
        if (key === 'body') return body;
        const member = Reflect.get(target, key, target);
        return typeof member === 'function' ? member.bind(target) : member;
    } });
}

class ReplayableHttpAgent extends HttpAgent {
    lastPostedInput?: RunAgentInput;
    replayInput?: RunAgentInput;
    protected override requestInit(input: RunAgentInput): RequestInit {
        const posted = this.replayInput ?? input;
        this.replayInput = undefined;
        this.lastPostedInput = structuredClone(posted);
        return super.requestInit(posted);
    }
}

/**
 * Initial AG-UI transport. HttpAgent owns validation, sequencing and lossless
 * protocol reduction. Activity messages stay in consumer history; upstream
 * omits them from subsequent run inputs. Use its messages/state directly;
 * rendered chat rows are a projection, never the authoritative transcript.
 */
export class AgUiClient {
    readonly agent: ReplayableHttpAgent;
    private readonly transportConfig: HttpAgentConfig;
    capabilities?: AgentCapabilities;
    private pendingClientToolIds: string[] = [];
    private dispatching = false;
 private interruptToolResults = new Map<string, InterruptResponse>();

    constructor(config: HttpAgentConfig) {
        const configuredFetch = config.fetch ?? globalThis.fetch.bind(globalThis);
        this.transportConfig = { ...config, fetch: async (url, init) => safeReaderDisposal(boundedSSEFrames(await configuredFetch(url, init))) };
        this.agent = new ReplayableHttpAgent(this.transportConfig);
        this.agent.subscribe({
            onRunStartedEvent: () => { this.pendingClientToolIds = []; },
            onRunErrorEvent: () => { this.pendingClientToolIds = []; },
            onRunFinishedEvent: params => { this.pendingClientToolIds = params.outcome === 'success' ? [...params.pendingToolCallIds] : []; if(params.outcome === 'success') this.interruptToolResults.clear(); },
            onCustomEvent: ({ event }) => {
            if (event.name === AGENTLY_CAPABILITIES_EVENT) {
                const capabilities = parseAgentlyCapabilities(event.value);
                if (capabilities) this.capabilities = capabilities;
            }
        } });
    }

    get messages() { return this.agent.messages; }
    get state() { return this.agent.state; }
    get pendingInterrupts(): readonly Interrupt[] { return structuredClone(this.agent.pendingInterrupts); }
    get threadId() { return this.agent.threadId; }
    subscribe(subscriber: AgentSubscriber) { return this.agent.subscribe(subscriber); }
    /** Full standard AG-UI run, including tools/context and upstream resume semantics. */
    run(parameters?: RunAgentParameters, subscriber?: AgentSubscriber) {
        if (this.dispatching) throw new Error('Cannot start a run during client tool execution');
        return this.agent.runAgent(parameters, subscriber);
    }
    /** Answers every open interrupt in a new run; the server owns durable continuation. */
    resume(responses: Record<string, InterruptResponse>, parameters: Omit<RunAgentParameters, 'resume'> = {}, subscriber?: AgentSubscriber, validateResponse?: (payload: unknown, schema: NonNullable<Interrupt['responseSchema']>, interrupt: Interrupt) => void) {
        const interrupts = this.agent.pendingInterrupts;
        if (!interrupts.length) throw new Error('No pending interrupts to resume');
        if (interrupts.some(interrupt => isInterruptExpired(interrupt) && responses[interrupt.id]?.status === 'resolved')) throw new Error('Cannot answer an expired interrupt; cancel it instead');
        const resume = buildResumeArray(interrupts, responses);
        const expected = new Set(interrupts.map(interrupt => interrupt.id));
        if (Object.keys(responses).some(id => !expected.has(id))) throw new Error('Unknown interrupt response identity');
        for (const interrupt of interrupts) {
            const answer = responses[interrupt.id];
            if (answer.status === 'resolved' && interrupt.responseSchema) {
                if (answer.payload === undefined) throw new Error('Interrupt requires response payload');
                validateResponse?.(answer.payload, interrupt.responseSchema, structuredClone(interrupt));
            }
        }
        return this.run({ ...parameters, resume }, subscriber);
    }
    /**
     * Executes the latest successful run's pending frontend calls, explicitly.
     * Results are appended immediately so retries do not repeat completed handlers.
     * Call run() afterwards to send results to any AG-UI agent. Interrupts require
     * explicit responses through resume(); tool results do not resolve them.
     */
    async executeClientTools(tools: readonly ClientTool[]): Promise<ToolMessage[]> {
        if (this.agent.isRunning || this.dispatching) throw new Error('Cannot execute client tools during a run or another dispatch');
        const registry = new Map<string, ClientTool>();
        for (const entry of tools) {
            if (registry.has(entry.tool.name)) throw new Error(`Duplicate client tool: ${entry.tool.name}`);
            registry.set(entry.tool.name, entry);
        }
        const answered = new Set(this.messages.filter(message => message.role === 'tool').map(message => message.toolCallId));
        const calls = this.pendingClientToolIds.filter(id => !answered.has(id)).map(id => {
            const call = this.messages.flatMap(message => message.role === 'assistant' ? message.toolCalls ?? [] : []).find(call => call.id === id);
            if (!call) throw new Error(`Missing pending tool call: ${id}`);
            const handler = registry.get(call.function.name);
            if (!handler) throw new Error(`No client handler for tool: ${call.function.name}`);
            return { call, handler, args: JSON.parse(call.function.arguments) as unknown };
        });
        const results: ToolMessage[] = [];
        this.dispatching = true;
        try {
            for (const { call, handler, args } of calls) {
                handler.validateArguments?.(args, handler.tool.parameters, structuredClone(call));
                const result = await handler.execute(args, structuredClone(call));
                if (!result || (typeof result.content !== 'string' && !Array.isArray(result.content))) throw new Error(`Client tool ${call.function.name} returned no valid content`);
                const message: ToolMessage = { ...structuredClone(result), id: crypto.randomUUID(), role: 'tool', toolCallId: call.id };
                ToolMessageSchema.parse(message);
                this.addMessage(message);
                results.push(message);
            }
        } finally { this.dispatching = false; }
        return results;
    }
    /**
     * Explicit profile opt-in: execute only version-1 agently.client_tool
     * interrupts. Approvals/unknown profiles remain for the caller. Returns
     * resume payloads; no internal delegation call gets a fabricated result.
     * Retrying after transport failure reuses completed handler outputs.
     */
    async executeClientToolInterrupts(tools: readonly ClientTool[]): Promise<Record<string, InterruptResponse>> {
        if (this.agent.isRunning || this.dispatching) throw new Error('Cannot execute interrupt tools during a run or another dispatch');
        const registry = new Map<string, ClientTool>();
        for (const entry of tools) {
            if (registry.has(entry.tool.name)) throw new Error(`Duplicate client tool: ${entry.tool.name}`);
            registry.set(entry.tool.name, entry);
        }
        const interrupts = this.agent.pendingInterrupts.filter(interrupt => {
            const metadata = interrupt.metadata?.agently;
            return interrupt.reason === 'agently.client_tool' && metadata && typeof metadata === 'object'
                && !Array.isArray(metadata) && metadata.version === '1' && metadata.kind === 'client-tool';
        });
        const pending = interrupts.map(interrupt => {
            if (!interrupt.toolCallId) throw new Error(`Client tool interrupt ${interrupt.id} has no tool call ID`);
            if (isInterruptExpired(interrupt)) throw new Error(`Client tool interrupt ${interrupt.id} has expired`);
            const owner = this.messages.find(message => message.role === 'assistant' && message.toolCalls?.some(call => call.id === interrupt.toolCallId));
            const call = owner?.role === 'assistant' ? owner.toolCalls?.find(call => call.id === interrupt.toolCallId) : undefined;
            if (!call) throw new Error(`Missing interrupted tool call: ${interrupt.toolCallId}`);
            const handler = registry.get(call.function.name);
            if (!handler) throw new Error(`No client handler for tool: ${call.function.name}`);
            const args: unknown = JSON.parse(call.function.arguments);
            const key = JSON.stringify([this.threadId, interrupt.id, call.id, call.function.name, call.function.arguments]);
            return { interrupt, call, handler, args, key };
        });
        const responses: Record<string, InterruptResponse> = {};
        this.dispatching = true;
        try {
            for (const { interrupt, call, handler, args, key } of pending) {
                const cached = this.interruptToolResults.get(key);
                if (cached) { responses[interrupt.id] = structuredClone(cached); continue; }
                handler.validateArguments?.(args, handler.tool.parameters, structuredClone(call));
                const result = await handler.execute(args, structuredClone(call));
                ToolMessageSchema.parse({ ...result, id: 'validation', role: 'tool', toolCallId: call.id });
                const payload = { content: structuredClone(result.content), ...(result.error !== undefined ? { error: result.error } : {}) };
                const response: InterruptResponse = { status: 'resolved', payload, ...(result.metadata ? { metadata: structuredClone(result.metadata) } : {}) };
                this.interruptToolResults.set(key, structuredClone(response));
                responses[interrupt.id] = response;
            }
        } finally { this.dispatching = false; }
        return responses;
    }
    /** Appends a typed message without flattening multipart content or metadata. */
    addMessage(message: Message) {
        if (this.agent.isRunning) throw new Error('Cannot append messages during a run');
        if (this.agent.messages.some(existing => existing.id === message.id)) throw new Error(`Duplicate message ID: ${message.id}`);
        this.agent.messages = [...this.agent.messages, message];
    }
    runAgently(selection: AgentlyExecutionSelection = {}, parameters: RunAgentParameters = {}, subscriber?: AgentSubscriber) {
        return this.run({ ...parameters, forwardedProps: agentlyForwardedProps({ version: '1', operation: 'chat', payload: selection }, parameters.forwardedProps) }, subscriber);
    }
    /** Explicit opt-in: external agents need not implement Agently discovery. */
    async discoverCapabilities(parameters: RunAgentParameters = {}): Promise<AgentCapabilities> {
        this.capabilities = undefined;
        await this.run({ ...parameters, forwardedProps: agentlyForwardedProps({ version: '1', operation: 'capabilities' }, parameters.forwardedProps) });
        if (!this.capabilities) throw new Error('Agent did not return supported agently.capabilities');
        return this.capabilities;
    }
    /** Defensive original POST input; server acceptance/durable replay remains a server capability. */
    get lastPostedInput(): RunAgentInput | undefined { return this.agent.lastPostedInput ? structuredClone(this.agent.lastPostedInput) : undefined; }
    /** Observe an owned server run after reload without retrieving its private input. */
    attach(runId: string, requestId: string, subscriber?: AgentSubscriber) {
        if (this.agent.isRunning || this.dispatching) throw new Error('Cannot attach during a run or dispatch');
        if (!runId || !requestId) throw new Error('Run attachment requires run and request identities');
        this.agent.messages = [];
        this.agent.state = {};
        return this.run({ runId, tools: [], context: [], forwardedProps: agentlyCommandProps('run.attach', requestId, {}) }, subscriber);
    }
    /** Restore an exact accepted input after reload. Requires server replay support. */
    reconnectFromInput(input: RunAgentInput, subscriber?: AgentSubscriber) {
        if (this.agent.isRunning || this.dispatching) throw new Error('Cannot restore during a run or dispatch');
        RunAgentInputSchema.parse(input);
        if (input.threadId !== this.threadId) throw new Error('Replay input belongs to another thread');
        this.agent.lastPostedInput = structuredClone(input);
        return this.reconnectFull(subscriber);
    }
    /** Rebuild from the original input and consume full journal replay, never a middle-of-run cursor. */
    reconnectFull(subscriber?: AgentSubscriber) {
        if (this.agent.isRunning || this.dispatching) throw new Error('Cannot reconnect during a run or dispatch');
        const original = this.lastPostedInput;
        if (!original) throw new Error('No original POST input to replay');
        this.agent.messages = structuredClone(original.messages);
        this.agent.state = structuredClone(original.state ?? {});
        this.agent.threadId = original.threadId;
        const { messages: _messages, state: _state, threadId: _threadId, ...parameters } = original;
        this.agent.replayInput = original;
        return this.run(parameters, subscriber);
    }
    /** Explicit durable backend command; abortTransport only disconnects the consumer. */
    cancelRun(targetRunId: string, parameters: RunAgentParameters & { runId: string }, subscriber?: AgentSubscriber) {
        if (!targetRunId) throw new Error('Cancellation requires target run identity');
        const command = new AgUiClient({ ...this.transportConfig, threadId: this.threadId, initialMessages: [], initialState: {} });
        return command.run({ ...parameters, forwardedProps: agentlyCommandProps('run.cancel', parameters.runId, { runId: targetRunId }, parameters.forwardedProps) }, subscriber);
    }
    /** Aborts the HTTP stream; backend execution cancellation is not guaranteed. */
    abortTransport() { this.agent.abortRun(); }
}

export type { AgentCapabilities, Message, State, RunAgentInput, Event as AgUiEvent } from '@ag-ui/core';
export type { AgentSubscriber, RunAgentParameters, HttpAgentConfig } from '@ag-ui/client';
