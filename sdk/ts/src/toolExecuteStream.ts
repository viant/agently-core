import { HttpError } from './errors';

export interface ToolExecuteOptions {
    conversationId?: string;
    /** Invocation-local producer metadata. Result rows are never progress. */
    onProgress?: (progress: unknown) => void;
    /** Explicitly opt into producer-authorized intermediate output. */
    onUpdate?: (update: unknown) => void;
}

const maxFrame = 64 * 1024 * 1024;
const maxProgressFrame = 16 * 1024;

/** Read one execution exactly once. A truncated stream must never replay a tool. */
export async function readToolExecuteStream(response: Response, onProgress: (progress: unknown) => void, onUpdate?: (update: unknown) => void): Promise<string> {
    if (!response.body) throw new Error('Tool stream has no response body');
    const reader = response.body.getReader();
    const decoder = new TextDecoder('utf-8', { fatal: true });
    let buffer = '', event = '', data: string[] = [], size = 0;
    const frame = (): string | undefined => {
        const name = event;
        const raw = data.join('\n');
        event = ''; data = []; size = 0;
        if (!raw || !['progress', 'update', 'result', 'error'].includes(name)) return;
        if (name === 'progress' && raw.length > maxProgressFrame) throw new Error('Tool progress frame exceeds limit');
        const value = JSON.parse(raw);
        if (name === 'progress') {
            // Consumer rendering must not break the underlying execution.
            try { onProgress(value); } catch (_) { /* consumer owns its callback */ }
            return;
        }
        if (name === 'update') {
            // Update validation failures must fail the execution, never leave
            // invalid provisional content visible. No request is replayed.
            onUpdate?.(value);
            return;
        }
        if (name === 'error') {
            const status = Number.isInteger(value?.status) ? value.status : 500;
            const { status: _, ...body } = value;
            throw new HttpError(status, 'Tool execution failed', JSON.stringify(body));
        }
        if (typeof value?.result !== 'string') throw new Error('Invalid tool stream result');
        return value.result;
    };
    try {
        for (;;) {
            const { value, done } = await reader.read();
            buffer += done ? decoder.decode() : decoder.decode(value, { stream: true });
            let index: number;
            while ((index = buffer.indexOf('\n')) >= 0) {
                let line = buffer.slice(0, index); buffer = buffer.slice(index + 1);
                if (line.endsWith('\r')) line = line.slice(0, -1);
                if (!line) {
                    const result = frame();
                    if (result !== undefined) return result;
                } else {
                    size += line.length;
                    if (size > maxFrame) throw new Error('Tool stream frame exceeds limit');
                    if (line.startsWith('event:')) event = line.slice(6).trim();
                    if (line.startsWith('data:')) data.push(line.slice(5).replace(/^ /, ''));
                }
            }
            if (size + buffer.length > maxFrame) throw new Error('Tool stream frame exceeds limit');
            if (done) throw new Error('Tool stream ended before a result');
        }
    } finally {
        await reader.cancel().catch(() => {});
        reader.releaseLock();
    }
}
