import type { SSEEvent } from './types';

export function resolveEventConversationId(event: Partial<SSEEvent> | null | undefined): string {
    return String(event?.conversationId || event?.streamId || '').trim();
}

export function resolveEventTurnId(event: Partial<SSEEvent> | null | undefined): string {
    return String(event?.turnId || event?.streamId || '').trim();
}

export function resolveEventMessageId(event: Partial<SSEEvent> | null | undefined): string {
    return String(
        event?.id
        || event?.assistantMessageId
        || event?.modelCallId
        || event?.toolMessageId
        || event?.streamId
        || ''
    ).trim();
}
