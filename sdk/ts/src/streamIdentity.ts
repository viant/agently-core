import type { SSEEvent } from './types';

export function resolveEventConversationId(event: Partial<SSEEvent> | null | undefined): string {
    return String(event?.conversationId || event?.streamId || '').trim();
}

export function resolveEventTurnId(event: Partial<SSEEvent> | null | undefined): string {
    return String(event?.turnId || '').trim();
}

export function resolveEventMessageId(event: Partial<SSEEvent> | null | undefined): string {
    return String(
        event?.messageId
        || event?.id
        || event?.assistantMessageId
        || event?.modelCallId
        || event?.toolMessageId
        || event?.streamId
        || ''
    ).trim();
}

export function normalizeStreamEventIdentity(
    raw: Partial<SSEEvent> | null | undefined,
    subscribedConversationId = '',
): SSEEvent | null {
    const event = { ...(raw || {}) } as SSEEvent;
    const subscribedId = String(subscribedConversationId || '').trim();
    const normalizedConversationId = resolveEventConversationId(event) || subscribedId;
    if (!normalizedConversationId) return null;
    event.conversationId = normalizedConversationId;
    if (!event.streamId) {
        event.streamId = normalizedConversationId;
    }
    const normalizedTurnId = resolveEventTurnId(event);
    if (normalizedTurnId) {
        event.turnId = normalizedTurnId;
    }
    const normalizedMessageId = resolveEventMessageId(event);
    if (normalizedMessageId) {
        event.messageId = normalizedMessageId;
    }
    if (subscribedId && event.conversationId !== subscribedId) {
        return null;
    }
    return event;
}
