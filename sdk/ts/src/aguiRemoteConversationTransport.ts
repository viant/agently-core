import type { AgentlyClient, AgUiBackendThread } from './client';
import { AgUiSession, AgUiRunError, type AgUiSessionSnapshot } from './aguiSession';
import { AgUiViewProjection, type AgUiViewEvent, type AgUiViewOutcome, type AgUiViewDescriptor } from './aguiViewProjection';
type Host = Pick<AgentlyClient, 'listAgUiBackends' | 'createAgUiBackendThread' | 'agUiTransport'>;
export interface AgUiRemoteConversationHandlers {
    onEvent?(event: AgUiViewEvent): void;
    onOutcome?(outcome: AgUiViewOutcome): void;
    onDescriptor?(descriptor: AgUiViewDescriptor): void;
    onSession?(snapshot: AgUiSessionSnapshot): void;
}
export interface AgUiRemoteSubmission { runId: string; messageId: string; completion: Promise<unknown> }
interface Entry {
    thread: AgUiBackendThread; session: AgUiSession; listeners: Set<AgUiRemoteConversationHandlers>;
    events: Map<string, AgUiViewEvent>; descriptors: Map<string, AgUiViewDescriptor>; outcome?: AgUiViewOutcome;
    active: boolean; uncertain: boolean; dispose(): void;
}
/** Memory-only, account-scoped standard conversations. Reset on logout/account
 * change. The pinned official client owns reduction; foreign metadata cannot
 * grant local workspace/MCP authority. No native bootstrap or implicit retry. */
export class AgUiRemoteConversationTransport {
    private entries = new Map<string, Entry>();
    private generation = 0;
    constructor(private readonly host: Host) {}
    async create(connectionId: string): Promise<AgUiBackendThread> {
        const generation = this.generation;
        const backend = (await this.host.listAgUiBackends()).find(item => item.id === connectionId);
        if (!backend || backend.profile !== 'standard' || connectionId === 'agently') throw new Error('A configured standard backend is required');
        if (generation !== this.generation) throw new Error('Remote account session was invalidated');
        const thread = await this.host.createAgUiBackendThread(connectionId);
        if (generation !== this.generation) throw new Error('Remote account session was invalidated');
        if (thread.connectionId !== connectionId || !thread.threadId) throw new Error('Invalid remote thread ownership');
        const key = this.key(connectionId, thread.threadId);
        if (this.entries.has(key)) throw new Error('Remote thread already exists');
        const session = new AgUiSession({ ...this.host.agUiTransport(connectionId), connectionId, threadId: thread.threadId, profile: 'standard', durableReplay: false });
        const entry: Entry = { thread: { ...thread }, session, listeners: new Set(), events: new Map(), descriptors: new Map(), active: false, uncertain: false, dispose: () => {} };
        entry.dispose = session.subscribe(() => { if (this.current(entry)) for (const listener of entry.listeners) listener.onSession?.(session.getSnapshot()); });
        this.entries.set(key, entry);
        return { ...thread };
    }
    getSnapshot(connectionId: string, threadId: string) { return this.entry(connectionId, threadId).session.getSnapshot(); }
    subscribe(connectionId: string, threadId: string, handlers: AgUiRemoteConversationHandlers): { close(): void } {
        const entry = this.entry(connectionId, threadId);
        entry.listeners.add(handlers);
        for (const event of entry.events.values()) handlers.onEvent?.(structuredClone(event));
        for (const descriptor of entry.descriptors.values()) handlers.onDescriptor?.(structuredClone(descriptor));
        if (entry.outcome) handlers.onOutcome?.(entry.outcome);
        handlers.onSession?.(entry.session.getSnapshot());
        return { close: () => { entry.listeners.delete(handlers); } };
    }
    send(connectionId: string, threadId: string, text: string): AgUiRemoteSubmission {
        const entry = this.entry(connectionId, threadId);
        if (typeof text !== 'string' || !text.trim()) throw new Error('A text message is required');
        if (entry.active) throw new Error('Remote backend does not support concurrent submissions');
        if (entry.uncertain) throw new Error('Previous remote execution is uncertain; create a fresh conversation');
        if (entry.session.getSnapshot().phase === 'interrupted' || entry.session.getSnapshot().pendingToolCallIds.length) throw new Error('Remote continuation requires an explicitly supported capability');
        const runId = crypto.randomUUID(), messageId = crypto.randomUUID();
        let terminal = false;
        const projection = new AgUiViewProjection({ profile: 'standard', conversationId: threadId, runId, logicalTurnId: runId,
            baselineMessages: entry.session.getSnapshot().messages, displayQuery: text, allowHostEffects: false,
            onViewEvent: event => {
                if (!this.current(entry)) return;
                entry.events.set(JSON.stringify([event.type,event.turnId,event.messageId,event.toolCallId,event.modelCallId]), structuredClone(event));
                for (const listener of entry.listeners) listener.onEvent?.(event);
            },
            onOutcome: outcome => {
                if (!this.current(entry)) return;
                entry.outcome = outcome;
                for (const listener of entry.listeners) listener.onOutcome?.(outcome);
            },
            onDescriptor: descriptor => {
                if (!this.current(entry)) return;
                if (descriptor.kind === 'protocol-snapshot') {
                    for (const [key, event] of entry.events) if ([event.protocolMessageId,event.assistantMessageId,event.toolMessageId,event.messageId].some(id => id && descriptor.removedMessageIds.includes(id))) entry.events.delete(key);
                    for (const id of descriptor.removedMessageIds) entry.descriptors.delete(id);
                }
                const key = descriptor.kind === 'protocol-snapshot' ? 'protocol-snapshot' : descriptor.kind === 'state' ? 'state' : 'message' in descriptor ? descriptor.message.id : descriptor.messageId;
                entry.descriptors.set(key, structuredClone(descriptor));
                for (const listener of entry.listeners) listener.onDescriptor?.(descriptor);
            },
        });
        const projectionSubscription = entry.session.subscribeProtocol(projection.subscriber);
        const terminalSubscription = entry.session.subscribeProtocol({ onRunFinishedEvent: () => { terminal = true; }, onRunErrorEvent: () => { terminal = true; } });
        entry.active = true;
        const completion = entry.session.send({ id: messageId, role: 'user', content: text }, { runId, tools: [], context: [], forwardedProps: {} })
            .then(result => { if (!terminal) { entry.uncertain = true; throw new Error('Remote stream ended without a terminal outcome'); } return result; })
            .catch(error => { if (!terminal && !(error instanceof AgUiRunError)) entry.uncertain = true; throw error; })
            .finally(() => { entry.active = false; projectionSubscription.unsubscribe(); terminalSubscription.unsubscribe(); });
        return { runId, messageId, completion };
    }
    reset(): void {
        this.generation++;
        const entries = [...this.entries.values()]; this.entries.clear();
        for (const entry of entries) { entry.listeners.clear(); entry.dispose(); entry.session.detach(); }
    }
    private key(connectionId: string, threadId: string) { return JSON.stringify([connectionId, threadId]); }
    private entry(connectionId: string, threadId: string) {
        const entry = this.entries.get(this.key(connectionId, threadId));
        if (!entry) throw new Error('Remote conversation is unavailable; ephemeral history cannot be restored');
        return entry;
    }
    private current(entry: Entry) { return this.entries.get(this.key(entry.thread.connectionId, entry.thread.threadId)) === entry; }
}
