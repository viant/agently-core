export interface NarrationBubbleGroupLike {
    finalResponse?: boolean;
    finalContent?: string;
    narrationContent?: string;
}

export function shouldShowNarrationBubble(
    visibleGroups: NarrationBubbleGroupLike[] = [],
    visibleText = '',
    responseContent = '',
): boolean {
    const text = String(visibleText || '').trim();
    if (!text) return false;
    const groups = Array.isArray(visibleGroups) ? visibleGroups : [];
    if (groups.length === 0) return true;
    const hasFinalVisibleGroup = groups.some((group) => {
        const finalText = String(group?.finalContent || '').trim();
        return !!group?.finalResponse && finalText !== '';
    });
    if (hasFinalVisibleGroup) return true;
    const hasNarrationGroup = groups.some((group) => String(group?.narrationContent || '').trim() !== '');
    if (hasNarrationGroup) return true;
    return String(responseContent || '').trim() !== '';
}
