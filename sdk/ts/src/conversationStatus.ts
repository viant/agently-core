export function isLiveConversationState(conversation: Record<string, any> | null | undefined): boolean {
    if (!conversation || typeof conversation !== 'object') return false;
    const stage = String((conversation as any)?.stage || (conversation as any)?.Stage || '').trim().toLowerCase();
    const status = String((conversation as any)?.status || (conversation as any)?.Status || '').trim().toLowerCase();
    if (['thinking', 'executing', 'eliciting'].includes(stage)) return true;
    if (['running', 'thinking', 'processing', 'waiting_for_user', 'in_progress'].includes(status)) return true;
    return false;
}
