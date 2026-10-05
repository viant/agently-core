import type { Interrupt, Message, RunAgentInput, State } from '@ag-ui/core';
import type { AgentSubscriber, HttpAgentConfig, RunAgentParameters } from '@ag-ui/client';
import { AgUiClient, type AgentlyExecutionSelection, type InterruptResponse } from './agui';

export type AgUiSessionPhase = 'idle' | 'running' | 'completed' | 'interrupted' | 'cancelled' | 'failed' | 'detached';

export interface AgUiSessionSnapshot {
    readonly connectionId: string;
    readonly threadId: string;
    readonly runId?: string;
    readonly phase: AgUiSessionPhase;
    readonly messages: readonly Message[];
    readonly state: State;
    readonly interrupts: readonly Interrupt[];
    readonly pendingToolCallIds: readonly string[];
    readonly error?: Error;
    readonly version: number;
}

export interface AgUiSessionOptions extends HttpAgentConfig {
    /** Stable BFF connection identity, separate from a conversation/thread ID. */
    connectionId: string;
    /** Generic backends must not receive an Agently command envelope. */
    profile?: 'standard' | 'agently';
    /** Explicitly advertised or trusted-configured durable replay capability. */
    durableReplay?: boolean;
}

export interface AgUiSessionCheckpoint {
    version: '1';
    connectionId: string;
    input: RunAgentInput;
}

export class AgUiRunError extends Error {
    constructor(message: string, readonly code?: string) {
        super(message);
        this.name = 'AgUiRunError';
    }
}

/**
 * One conversation's standard protocol state. Existing web/mobile renderers
 * consume snapshots as projections; they do not independently accumulate text.
 * Authentication belongs to the injected transport/BFF. No token, browser
 * storage, application endpoint or Agently capability probe is assumed here.
 */
export class AgUiSession {
    readonly client: AgUiClient;
    private readonly options: AgUiSessionOptions;
    private snapshot: AgUiSessionSnapshot;
    private listeners = new Set<() => void>();
    private active = false;
    private detached = false;
    private runError?: Error;

    constructor(options: AgUiSessionOptions) {
        if (!options.connectionId) throw new Error('A backend connection identity is required');
        this.options = { ...options };
        this.client = new AgUiClient(options);
        this.snapshot = {
            connectionId: this.options.connectionId, threadId: this.client.threadId,
            phase: 'idle', messages: structuredClone(this.client.messages),
            state: structuredClone(this.client.state), interrupts: [], pendingToolCallIds: [], version: 0,
        };
        this.client.subscribe({
            onRunInitialized: ({ input }) => {
                this.publish({ runId: input.runId, phase: 'running', error: undefined, pendingToolCallIds: [] });
            },
            onMessagesChanged: ({ messages }) => this.publish({ messages: structuredClone([...messages]) }),
            onStateChanged: ({ state }) => this.publish({ state: structuredClone(state) }),
            onRunFinishedEvent: params => {
                if (this.detached) return;
                this.publish({
                    phase: params.outcome === 'interrupt' ? 'interrupted' : params.outcome === 'cancelled' ? 'cancelled' : 'completed',
                    messages: structuredClone([...params.messages]), state: structuredClone(params.state),
                    interrupts: params.outcome === 'interrupt' ? structuredClone(params.interrupts) : [],
                    pendingToolCallIds: params.outcome === 'success' ? [...params.pendingToolCallIds] : [],
                });
            },
            onRunErrorEvent: ({ event }) => {
                this.runError = new AgUiRunError(event.message, event.code);
                if (!this.detached) this.publish({ phase: 'failed', error: this.runError, pendingToolCallIds: [] });
            },
        });
    }

    /** Stable reference until a protocol mutation; suitable for useSyncExternalStore. */
    getSnapshot = (): AgUiSessionSnapshot => this.snapshot;

    subscribe = (listener: () => void): (() => void) => {
        this.listeners.add(listener);
        return () => { this.listeners.delete(listener); };
    };

    /** Extension listeners observe original typed events without becoming another reducer. */
    subscribeProtocol(subscriber: AgentSubscriber) { return this.client.subscribe(subscriber); }

    async send(message: Message, parameters: RunAgentParameters = {}, selection?: AgentlyExecutionSelection) {
        if (message.role !== 'user') throw new Error('send requires a user message');
        this.assertIdle();
        if (selection && this.options.profile !== 'agently') throw new Error('Agent/model selection requires the Agently profile');
        this.client.addMessage(message);
        return this.execute(() => this.options.profile === 'agently'
            ? this.client.runAgently(selection, parameters)
            : this.client.run(parameters));
    }

    /** Standard follow-up after explicit frontend-tool execution. */
    continue(parameters: RunAgentParameters = {}) {
        this.assertIdle();
        return this.execute(() => this.client.run(parameters));
    }

    resume(responses: Record<string, InterruptResponse>, parameters: Omit<RunAgentParameters, 'resume'> = {}) {
        this.assertIdle();
        return this.execute(() => this.client.resume(responses, parameters));
    }

    checkpoint(): AgUiSessionCheckpoint | undefined {
        const input = this.client.lastPostedInput;
        return input ? { version: '1', connectionId: this.options.connectionId, input } : undefined;
    }

    reconnect(checkpoint = this.checkpoint()) {
        this.assertIdle();
        if (!this.options.durableReplay) throw new Error('Backend has not advertised durable replay');
        if (!checkpoint || checkpoint.version !== '1') throw new Error('A supported replay checkpoint is required');
        if (checkpoint.connectionId !== this.options.connectionId) throw new Error('Replay checkpoint belongs to another backend');
        if (checkpoint.input.threadId !== this.client.threadId) throw new Error('Replay checkpoint belongs to another thread');
        return this.execute(() => this.client.reconnectFromInput(checkpoint.input));
    }

    /** Bootstrap supplies an owned run ID; the BFF keeps its original input private. */
    attach(runId: string, requestId: string) {
        this.assertIdle();
        if (this.options.profile !== 'agently' || !this.options.durableReplay) throw new Error('Backend does not support Agently run attachment');
        return this.execute(() => this.client.attach(runId, requestId));
    }

    /** Navigation/unmount disconnects the transport. It does not cancel backend work. */
    detach() {
        if (!this.active) return;
        this.detached = true;
        this.publish({ phase: 'detached' });
        this.client.abortTransport();
    }

    private assertIdle() {
        if (this.active) throw new Error('A run is already active for this conversation');
    }

    private async execute(action: () => ReturnType<AgUiClient['run']>) {
        this.active = true;
        this.detached = false;
        this.runError = undefined;
        try {
            const result = await action();
            if (this.runError && !this.detached) throw this.runError;
            return result;
        } catch (error) {
            if (!this.detached) this.publish({ phase: 'failed', error: error instanceof Error ? error : new Error(String(error)) });
            throw error;
        } finally {
            this.active = false;
        }
    }

    private publish(update: Partial<AgUiSessionSnapshot>) {
        this.snapshot = { ...this.snapshot, ...update, version: this.snapshot.version + 1 };
        for (const listener of this.listeners) listener();
    }
}
