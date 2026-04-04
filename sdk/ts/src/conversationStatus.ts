import type { ConversationStateLike } from './types';

export function isLiveConversationState(conversation: ConversationStateLike | null | undefined): boolean {
    if (!conversation || typeof conversation !== 'object') return false;
    const stage = String(conversation.stage || conversation.Stage || '').trim().toLowerCase();
    const status = String(conversation.status || conversation.Status || '').trim().toLowerCase();
    if (['thinking', 'executing', 'eliciting'].includes(stage)) return true;
    if (['running', 'thinking', 'processing', 'waiting_for_user', 'in_progress'].includes(status)) return true;
    return false;
}
