import { ElicitationTracker, type PendingElicitation as TrackedElicitation } from './elicitation';
import { FeedTracker } from './feedTracker';
import {
    applyEvent as applyMessageEvent,
    newMessageBuffer,
    reconcileFromTranscript,
    reconcileMessages,
    type MessageBuffer,
} from './reconcile';
import type { ActiveFeed, Message, SSEEvent, Turn } from './types';

export interface ConversationStreamSnapshot {
    activeTurnId: string | null;
    feeds: ActiveFeed[];
    pendingElicitation: TrackedElicitation | null;
    bufferedMessages: Partial<Message>[];
}

export class ConversationStreamTracker {
    private readonly _messages: MessageBuffer;
    private readonly _feeds: FeedTracker;
    private readonly _elicitation: ElicitationTracker;

    constructor() {
        this._messages = newMessageBuffer();
        this._feeds = new FeedTracker();
        this._elicitation = new ElicitationTracker();
    }

    get state(): ConversationStreamSnapshot {
        return this.snapshot();
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
            activeTurnId: this.activeTurnId,
            feeds: this.feeds,
            pendingElicitation: this.pendingElicitation,
            bufferedMessages: this.bufferedMessages,
        };
    }

    clear(): void {
        this._messages.byId.clear();
        this._messages.activeTurnId = null;
        this._feeds.clear();
        this._elicitation.clear();
    }

    reset(): void {
        this.clear();
    }

    applyEvent(event: SSEEvent): { id: string; content: string; final: boolean } | null {
        this._feeds.applyEvent(event);
        this._elicitation.applyEvent(event);
        return applyMessageEvent(this._messages, event);
    }

    reconcileTranscript(turns: Turn[]): void {
        reconcileFromTranscript(this._messages, turns);
    }

    applyTranscript(turns: Turn[]): void {
        this.reconcileTranscript(turns);
    }

    reconcile(serverMessages: Message[]): Message[] {
        return reconcileMessages(this._messages, serverMessages);
    }
}
