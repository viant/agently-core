import type { FeedPatchOperation } from './types';

function decodePointer(path: string): string[] {
    return String(path || '').split('/').slice(1).map((part) => part.replace(/~1/g, '/').replace(/~0/g, '~'));
}

export function applyFeedPatchOperation(input: unknown, operation: FeedPatchOperation): unknown {
    const root: any = Array.isArray(input) ? [...input] : { ...((input as object) || {}) };
    const parts = decodePointer(operation.path);
    if (parts.length === 0) return root;
    let current: any = root;
    for (let index = 0; index < parts.length - 1; index += 1) {
        const key = parts[index];
        const next = current?.[key];
        current[key] = Array.isArray(next) ? [...next] : { ...(next || {}) };
        current = current[key];
    }
    const key = parts[parts.length - 1];
    if (operation.op === 'remove') {
        if (Array.isArray(current)) current.splice(Number(key), 1);
        else delete current[key];
    } else if (Array.isArray(current) && operation.op === 'add') {
        if (key === '-') current.push(operation.value);
        else {
            const offset = Number(key);
            if (Number.isInteger(offset) && offset >= 0 && offset <= current.length) {
                current.splice(offset, 0, operation.value);
            }
        }
    } else {
        current[key] = operation.value;
    }
    return root;
}

export function applyFeedPatchOperations(input: unknown, operations: FeedPatchOperation[]): unknown {
    return (operations || []).reduce((state, operation) => applyFeedPatchOperation(state, operation), input);
}
