export function firstString(...values: unknown[]): string {
    for (const value of values) {
        const text = String(value || '').trim();
        if (text) return text;
    }
    return '';
}

export function firstNumber(...values: unknown[]): number {
    for (const value of values) {
        const num = Number(value);
        if (Number.isFinite(num)) return num;
    }
    return 0;
}

export function firstPositiveNumber(...values: unknown[]): number {
    for (const value of values) {
        const num = Number(value);
        if (Number.isFinite(num) && num > 0) return num;
    }
    return 0;
}

export function temporalSequenceValue(item: Record<string, any> = {}): number {
    const value = Number(item?.sequence ?? item?.eventSeq ?? item?.iteration ?? 0);
    return Number.isFinite(value) ? value : 0;
}

export function temporalTimeValue(item: Record<string, any> = {}): number {
    const raw = firstString(item?.createdAt, item?.updatedAt);
    if (!raw) return 0;
    const parsed = Date.parse(raw);
    return Number.isFinite(parsed) ? parsed : 0;
}

export function compareTemporalEntries(left: Record<string, any> = {}, right: Record<string, any> = {}): number {
    const leftTurnId = firstString(left?.turnId);
    const rightTurnId = firstString(right?.turnId);
    if (leftTurnId && rightTurnId && leftTurnId === rightTurnId) {
        const leftRole = firstString(left?.role).toLowerCase();
        const rightRole = firstString(right?.role).toLowerCase();
        const leftIsUser = leftRole === 'user';
        const rightIsUser = rightRole === 'user';
        if (leftIsUser !== rightIsUser) return leftIsUser ? -1 : 1;
    }
    const leftTime = temporalTimeValue(left);
    const rightTime = temporalTimeValue(right);
    if (leftTime !== rightTime) return leftTime - rightTime;
    const leftSeq = temporalSequenceValue(left);
    const rightSeq = temporalSequenceValue(right);
    if (leftSeq !== rightSeq) return leftSeq - rightSeq;
    return firstString(left?.id, left?.messageId, left?.assistantMessageId).localeCompare(
        firstString(right?.id, right?.messageId, right?.assistantMessageId),
    );
}

export function compareExecutionGroups(left: Record<string, any> = {}, right: Record<string, any> = {}): number {
    const leftSeq = temporalSequenceValue(left);
    const rightSeq = temporalSequenceValue(right);
    if (leftSeq !== rightSeq) return leftSeq - rightSeq;
    return firstString(left?.assistantMessageId, left?.pageId, left?.modelMessageId, left?.parentMessageId).localeCompare(
        firstString(right?.assistantMessageId, right?.pageId, right?.modelMessageId, right?.parentMessageId),
    );
}
