import type { AgentSubscriber } from '@ag-ui/client';
import type { Message, State } from '@ag-ui/core';
import { AgUiCommands, type AgUiConversationBootstrapResult, type AgUiConversationBootstrapInput } from './aguiCommands';
import { readAgentlyPresentation } from './aguiPresentation';
import type { AgUiViewOutcome, AgUiViewDescriptor } from './aguiViewProjection';
import type { AgUiSession } from './aguiSession';
import type { AgentlyClient } from './client';
import type { QueryInput, QueryOutput, SSEEvent, TranscriptOutput } from './types';
import { transcriptDTO } from './wireDTO';

export interface AgUiConversationHandlers {
    onOpen?: () => void;
    onEvent?: (event: SSEEvent) => void;
    onTextDelta?: (content: string, event: SSEEvent) => void;
    onToolEvent?: (event: SSEEvent) => void;
    onTurnEnd?: (event: SSEEvent) => void;
    onFeedEvent?: (event: SSEEvent) => void;
    onError?: (error: string) => void;
    onSnapshot?: (snapshot: TranscriptOutput) => void;
    onOutcome?: (outcome: AgUiViewOutcome) => void;
    onHostActivities?: (activities: readonly Message[], unavailableIds: readonly string[]) => void;
}

export interface AgUiConversationProjectionOptions {
    profile: 'agently';
    conversationId: string;
    runId: string;
    baselineMessages?: readonly Message[];
    displayQuery?: string;
    allowHostEffects: boolean;
    onViewEvent(event: SSEEvent): void;
    onOutcome?(outcome: AgUiViewOutcome): void;
    onDescriptor?(descriptor: AgUiViewDescriptor): void;
}
export type AgUiConversationProjectionFactory = (options: AgUiConversationProjectionOptions) => { subscriber: AgentSubscriber };

type Host = Pick<AgentlyClient, 'agUiTransport' | 'createAgUiSession' | 'createConversation'> & Partial<Pick<AgentlyClient, 'getConversation'>>;
interface RunSlot {
    id: string;
    session: AgUiSession;
    nativeTurnId?: string;
    stopProjection(): void;
    completion: Promise<unknown>;
}
interface Entry {
    protocolThreadId?: string;
    reads: Set<{ abortTransport(): void }>;
    id: string;
    generation: number;
    listeners: Set<AgUiConversationHandlers>;
    runs: Map<string, RunSlot>;
    terminalTurns: Set<string>;
    state: State;
    hostActivities: Map<string, Message>;
    unavailableHostActivityIds: string[];
    bootstrap?: AgUiConversationBootstrapResult;
    loading?: Promise<AgUiConversationBootstrapResult>;
    bootstrapHandle?: { abortTransport(): void };
}

/**
 * Native conversation orchestration, not another protocol reducer. Each admitted
 * run has its own official AG-UI session, allowing the native queue to accept a
 * follow-up while another run is streaming. Existing application APIs remain on
 * the host client. No legacy query or event-stream request is made here.
 */
export class AgUiConversationTransport {
    private entries = new Map<string, Entry>();
    private generation = 0;

    constructor(private readonly host: Host, private readonly project: AgUiConversationProjectionFactory) {}

    subscribe(conversationId: string, handlers: AgUiConversationHandlers): { close(): void } {
        const entry = this.entry(conversationId);
        entry.listeners.add(handlers);
        if (entry.bootstrap) handlers.onSnapshot?.(transcriptDTO(entry.bootstrap.transcript));
        handlers.onHostActivities?.([...entry.hostActivities.values()], entry.unavailableHostActivityIds);
        void this.refresh(conversationId).catch(error => {
            // Shared/read-only conversations can still be rendered through the
            // existing authorized transcript API, without creating an owned run.
            if ((error as { status?: number })?.status !== 403) this.error(entry, error);
        });
        let closed = false;
        return { close: () => {
            if (closed) return;
            closed = true;
            entry.listeners.delete(handlers);
            // A view subscription does not own the submitted request. Navigation
            // remounts the composer during admission; aborting here can prevent
            // the POST from reaching the server at all. Keep coordinator-owned
            // work until completion; reset() owns the account/transport boundary.
        } };
    }

    async query(input: QueryInput): Promise<QueryOutput> {
        if (input.elicitationMode && input.elicitationMode !== 'deferred') throw new Error('AG-UI uses deferred interrupts for elicitation');
        const conversationId = input.conversationId || (await this.host.createConversation({ agentId: input.agentId })).id;
        const entry = this.entry(conversationId);
        const bootstrap = await this.refresh(conversationId);
        if (!this.current(entry)) throw new Error('Conversation session was invalidated');
        const runId = crypto.randomUUID();
        const messageId = input.messageId || crypto.randomUUID();
        const session = this.host.createAgUiSession({ threadId: entry.protocolThreadId ?? conversationId, connectionId: 'agently', profile: 'agently', durableReplay: true });
        let accepted = false;
        let accept!: (value: QueryOutput) => void;
        let reject!: (error: unknown) => void;
        const admission = new Promise<QueryOutput>((resolve, fail) => { accept = resolve; reject = fail; });
        const slot = this.track(entry, runId, session, bootstrap.messages, event => {
            const nativeTurnId = event.turnId || entry.runs.get(runId)?.nativeTurnId;
            if (!accepted && nativeTurnId && ['turn_started', 'turn_queued', 'model_started'].includes(event.type)) {
                accepted = true;
                accept({ conversationId, content: '', turnId: nativeTurnId, messageId: nativeTurnId });
            }
        }, input.displayQuery);
        const selection = {
            agentId: input.agentId, model: input.model, backendTools: input.tools,
            toolBundles: input.toolBundles, autoSelectTools: input.autoSelectTools,
            autoSummarize: input.autoSummarize, disableChains: input.disableChains,
            allowedChains: input.allowedChains, resourceURIs: input.resourceURIs,
            toolCallExposure: input.toolCallExposure, reasoningEffort: input.reasoningEffort,
            displayQuery: input.displayQuery, context: input.context, useServerState: true,
            attachments: input.attachments?.map(({ name, uri, mime, stagingFolder }) => ({ name, uri, mime, stagingFolder })),
        };
        slot.completion = session.send({ id: messageId, role: 'user', content: input.query }, { runId }, selection).then(() => {
            if (!this.current(entry) || session.getSnapshot().phase === 'detached') {
                throw new Error('Conversation session was invalidated before submission completed');
            }
            if (!accepted) {
                accepted = true;
                const snapshot = session.getSnapshot();
                const last = [...snapshot.messages].reverse().find(message => message.role === 'assistant');
                accept({ conversationId, turnId: slot.nativeTurnId, messageId: slot.nativeTurnId,
                    content: snapshot.phase === 'completed' && typeof last?.content === 'string' ? last.content : '' });
            }
        }).catch(error => {
            if (!accepted) reject(error);
            else if (session.getSnapshot().phase !== 'detached') this.error(entry, error);
        }).finally(() => this.settled(entry, slot));
        return admission;
    }

    /** Explicit refresh on navigation, a domain action, or an availability notification. */
    reconcile(conversationId: string): Promise<AgUiConversationBootstrapResult> {
        const entry = this.entry(conversationId);
        if (!entry.loading) return this.refresh(conversationId);
        return entry.loading.then(() => {
            if (!this.current(entry)) throw new Error('Conversation session was invalidated');
            // The in-flight read may predate the committed change that caused
            // this notification. Read again once; concurrent hints coalesce.
            return this.refresh(conversationId);
        });
    }

    refresh(conversationId: string): Promise<AgUiConversationBootstrapResult> {
        const entry = this.entry(conversationId);
        if (entry.loading) return entry.loading;
        const runId = crypto.randomUUID();
        const commands = new AgUiCommands(this.host.agUiTransport());
        const pending = this.resolveProtocolThread(entry).then(threadId => {
        const command = commands.start('conversation.bootstrap', { mode: 'live', includeModelCalls: true, includeToolCalls: true, includeFeeds: true }, { threadId, runId, requestId: runId });
        entry.bootstrapHandle = command;
        return command.result;
        }).then(result => {
            if (!result || !this.current(entry) || result.transcript.conversation.conversationId !== entry.id || result.threadId !== entry.protocolThreadId) throw new Error('Conversation bootstrap was invalidated');
            entry.bootstrap = result;
            entry.state = result.state ?? {};
            this.replaceHostActivities(entry, result);
            entry.terminalTurns = new Set((result.transcript.conversation.turns ?? [])
                .filter(turn => ['completed', 'succeeded', 'failed', 'canceled', 'cancelled'].includes(turn.status)).map(turn => turn.turnId));
            for (const listener of entry.listeners) listener.onSnapshot?.(transcriptDTO(result.transcript));
            if (entry.listeners.size) {
                for (const run of result.runs) {
                    if (run.kind === 'resource' || run.kind === 'mcp-app' || entry.runs.has(run.runId)) continue;
                    this.attach(entry, run.runId, result.messages);
                }
            }
            return result;
        }).finally(() => {
            if (entry.loading === pending) { entry.loading = undefined; entry.bootstrapHandle = undefined; }
        });
        entry.loading = pending;
        return pending;
    }

    private async resolveProtocolThread(entry: Entry): Promise<string> {
        if (entry.protocolThreadId !== undefined) return entry.protocolThreadId;
        const conversation = this.host.getConversation ? await this.host.getConversation(entry.id) : undefined;
        if (!this.current(entry) || (conversation && conversation.id !== entry.id)) throw new Error('Native conversation binding mismatch');
        const wire = conversation?.aguiThreadId ?? entry.id;
        if (!wire) throw new Error('Empty protocol thread binding');
        entry.protocolThreadId = wire;
        return wire;
    }

    /** Selected history is a read result, never a replacement for full coordinator state. */
    async readSnapshot(conversationId: string, input: AgUiConversationBootstrapInput): Promise<TranscriptOutput> {
        const entry = this.entry(conversationId);
        const threadId = await this.resolveProtocolThread(entry);
        if (!this.current(entry)) throw new Error('Conversation session was invalidated');
        const runId = crypto.randomUUID();
        const command = new AgUiCommands(this.host.agUiTransport()).start('conversation.bootstrap', input, {threadId, runId, requestId:runId});
        entry.reads.add(command);
        try {
            const result = await command.result;
            if (!result || !this.current(entry) || result.threadId !== threadId || result.transcript.conversation.conversationId !== entry.id) {
                throw new Error('Conversation bootstrap was invalidated');
            }
            return transcriptDTO(result.transcript);
        } finally { entry.reads.delete(command); }
    }

    /** Account/logout boundary: detach transport, leave authorized backend work running. */
    reset() {
        this.generation++;
        for (const entry of this.entries.values()) {
            entry.listeners.clear();
            entry.bootstrapHandle?.abortTransport();
            for (const read of entry.reads) read.abortTransport();
            for (const run of entry.runs.values()) { run.stopProjection(); run.session.detach(); }
        }
        this.entries.clear();
    }

    private entry(id: string): Entry {
        if (!id) throw new Error('A conversation identity is required');
        let entry = this.entries.get(id);
        if (!entry) {
            entry = { reads: new Set(), id, generation: this.generation, listeners: new Set(), runs: new Map(), terminalTurns: new Set(), state: {}, hostActivities: new Map(), unavailableHostActivityIds: [] };
            this.entries.set(id, entry);
        }
        return entry;
    }

    private current(entry: Entry) { return entry.generation === this.generation && this.entries.get(entry.id) === entry; }

    private track(entry: Entry, runId: string, session: AgUiSession, baselineMessages: readonly Message[], onViewEvent?: (event: SSEEvent) => void, displayQuery?: string): RunSlot {
        const slot: RunSlot = { id: runId, session, stopProjection: () => {}, completion: Promise.resolve() };
        const identity = session.subscribeProtocol({ onRunStartedEvent: ({ event }) => {
            slot.nativeTurnId = readAgentlyPresentation(event.metadata)?.nativeTurnId;
        } });
        const projection = this.project({ profile: 'agently', conversationId: entry.id, runId,
            baselineMessages, displayQuery, allowHostEffects: true,
            onDescriptor: descriptor => {
                if (!this.current(entry) || descriptor.kind !== 'host-activity' || !descriptor.hostEffectsAllowed) return;
                entry.hostActivities.set(descriptor.message.id, structuredClone(descriptor.message));
                for (const listener of entry.listeners) listener.onHostActivities?.([...entry.hostActivities.values()], entry.unavailableHostActivityIds);
            },
            onOutcome: outcome => {
                if (!this.current(entry)) return;
                for (const listener of entry.listeners) listener.onOutcome?.(outcome);
            }, onViewEvent: event => {
                if (!this.current(entry)) return;
                onViewEvent?.(event);
                if (event.turnId && entry.terminalTurns.has(event.turnId) && /^(turn_started|turn_queued|model_|tool_call_|text_delta|reasoning_delta)/.test(event.type)) return;
                for (const listener of entry.listeners) {
                    listener.onEvent?.(event);
                    if (event.type === 'text_delta') listener.onTextDelta?.(event.content ?? '', event);
                    if (event.type.startsWith('tool_call_')) listener.onToolEvent?.(event);
                    if (['turn_completed', 'turn_failed', 'turn_canceled'].includes(event.type)) listener.onTurnEnd?.(event);
                    if (event.type.startsWith('tool_feed_')) listener.onFeedEvent?.(event);
                }
            } });
        const subscription = session.subscribeProtocol(projection.subscriber);
        slot.stopProjection = () => { subscription.unsubscribe(); identity.unsubscribe(); };
        entry.runs.set(runId, slot);
        return slot;
    }

    private attach(entry: Entry, runId: string, messages: readonly Message[]) {
        const session = this.host.createAgUiSession({ threadId: entry.protocolThreadId ?? entry.id, connectionId: 'agently', profile: 'agently', durableReplay: true });
        const slot = this.track(entry, runId, session, messages);
        slot.completion = session.attach(runId, crypto.randomUUID()).catch(error => {
            if (session.getSnapshot().phase !== 'detached') this.error(entry, error);
        }).finally(() => this.settled(entry, slot));
    }

    private settled(entry: Entry, slot: RunSlot) {
        slot.stopProjection();
        if (entry.runs.get(slot.id) === slot) entry.runs.delete(slot.id);
        if (this.current(entry) && entry.listeners.size && slot.session.getSnapshot().phase !== 'detached') {
            // One authoritative refresh after completion; no permanent polling
            // loop or implicit resubmission of an interrupted protocol run.
            void this.refreshAfterCompletion(entry).catch(error => this.error(entry, error));
        }
    }

    private async refreshAfterCompletion(entry: Entry) {
        const commands = new AgUiCommands(this.host.agUiTransport());
        const runId = crypto.randomUUID();
        const result = await commands.execute('conversation.bootstrap', { mode: 'live' }, { threadId: entry.protocolThreadId ?? entry.id, runId, requestId: runId });
        if (!result || !this.current(entry)) return;
        entry.bootstrap = result;
        this.replaceHostActivities(entry, result);
        for (const listener of entry.listeners) listener.onSnapshot?.(transcriptDTO(result.transcript));
    }

    private replaceHostActivities(entry: Entry, result: AgUiConversationBootstrapResult) {
        entry.hostActivities = new Map((result.hostActivities ?? []).map(message => [message.id, structuredClone(message)]));
        entry.unavailableHostActivityIds = [...(result.unavailableHostActivityIds ?? [])];
        for (const listener of entry.listeners) listener.onHostActivities?.([...entry.hostActivities.values()], entry.unavailableHostActivityIds);
    }

    private error(entry: Entry, error: unknown) {
        if (!this.current(entry)) return;
        const message = error instanceof Error ? error.message : String(error);
        for (const listener of entry.listeners) listener.onError?.(message);
    }
}
