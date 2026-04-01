import { ElicitationTracker, type PendingElicitation as TrackedElicitation } from './elicitation';
import { FeedTracker } from './feedTracker';
import {
    applyEvent as applyMessageEvent,
    newMessageBuffer,
    reconcileFromTranscript,
    reconcileMessages,
    type MessageBuffer,
} from './reconcile';
import { resolveEventConversationId } from './streamIdentity';
import type { ActiveFeed, Message, SSEEvent, Turn } from './types';

export interface ConversationStreamSnapshot {
    conversationId: string;
    activeTurnId: string | null;
    feeds: ActiveFeed[];
    pendingElicitation: TrackedElicitation | null;
    bufferedMessages: Partial<Message>[];
}

export interface CanonicalConversationSnapshot {
    conversationId: string;
    activeTurnId: string | null;
    feeds: ActiveFeed[];
    pendingElicitation: TrackedElicitation | null;
}

export class ConversationStreamTracker {
    private readonly _messages: MessageBuffer;
    private readonly _feeds: FeedTracker;
    private readonly _elicitation: ElicitationTracker;
    private _conversationId = '';

    constructor(conversationId = '') {
        this._messages = newMessageBuffer();
        this._feeds = new FeedTracker();
        this._elicitation = new ElicitationTracker();
        this._conversationId = String(conversationId || '').trim();
    }

    /** Backward-compatible composite view; prefer `canonicalState` for canonical access. */
    get state(): ConversationStreamSnapshot {
        return this.snapshot();
    }

    get compositeState(): ConversationStreamSnapshot {
        return this.snapshot();
    }

    get canonicalState(): CanonicalConversationSnapshot {
        return this.snapshotCanonical();
    }

    get conversationId(): string {
        return this._conversationId;
    }

    get activeTurnId(): string | null {
        return this._messages.activeTurnId;
    }

    get feeds(): ActiveFeed[] {
        return this._feeds.feeds;
    }

    get pendingElicitation(): TrackedElicitation | null {
        return this._elicitation.pending;
    }

    get bufferedMessages(): Partial<Message>[] {
        return Array.from(this._messages.byId.values());
    }

    snapshot(): ConversationStreamSnapshot {
        return {
            conversationId: this.conversationId,
            activeTurnId: this.activeTurnId,
            feeds: this.feeds,
            pendingElicitation: this.pendingElicitation,
            bufferedMessages: this.bufferedMessages,
        };
    }

    snapshotComposite(): ConversationStreamSnapshot {
        return this.snapshot();
    }

    snapshotCanonical(): CanonicalConversationSnapshot {
        return {
            conversationId: this.conversationId,
            activeTurnId: this.activeTurnId,
            feeds: this.feeds,
            pendingElicitation: this.pendingElicitation,
        };
    }

    clear(): void {
        this._messages.byId.clear();
        this._messages.activeTurnId = null;
        this._feeds.clear();
        this._elicitation.clear();
        this._conversationId = '';
    }

    reset(): void {
        this.clear();
    }

    applyEvent(event: SSEEvent): { id: string; content: string; final: boolean } | null {
        const conversationId = resolveEventConversationId(event);
        if (conversationId) {
            this._conversationId = conversationId;
        }
        this._feeds.applyEvent(event);
        this._elicitation.applyEvent(event);
        return applyMessageEvent(this._messages, event);
    }

    reconcileTranscript(turns: Turn[]): void {
        const firstTurn = Array.isArray(turns) && turns.length > 0 ? turns[0] : null;
        if (firstTurn?.conversationId) {
            this._conversationId = String(firstTurn.conversationId).trim();
        }
        reconcileFromTranscript(this._messages, turns);
    }

    applyTranscript(turns: Turn[]): void {
        this.reconcileTranscript(turns);
    }

    reconcile(serverMessages: Message[]): Message[] {
        return reconcileMessages(this._messages, serverMessages);
    }
}
