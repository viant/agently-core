// Upstream limits an incomplete SSE buffer to 10 MiB and exposes no override.
// Deliver complete, bounded frames so large canonical snapshots retain upstream
// event parsing/validation without depending on network chunk boundaries.
export const MAX_AGUI_SSE_FRAME_BYTES = 64 * 1024 * 1024;
const framedResponses = new WeakSet<Response>();

export function preserveSSEFrames(response: Response, source: Response): Response {
    if (framedResponses.has(source)) framedResponses.add(response);
    return response;
}

export function boundedSSEFrames(response: Response, maxBytes = MAX_AGUI_SSE_FRAME_BYTES): Response {
    if (!response.body || framedResponses.has(response) || !response.headers.get('content-type')?.toLowerCase().includes('text/event-stream')) return response;
    if (!Number.isSafeInteger(maxBytes) || maxBytes <= 0) throw new RangeError('SSE frame limit must be a positive integer');
    const decoder = new TextDecoder('utf-8', { fatal: true });
    const encoder = new TextEncoder();
    let lines: string[] = [], line: string[] = [], lineLength = 0, bytes = 0, skipLF = false;
    const reserve = (count: number) => {
        bytes += count;
        if (bytes > maxBytes) throw new RangeError(`AG-UI SSE event exceeds ${maxBytes} bytes`);
    };
    const append = (part: string) => {
        if (!part) return;
        reserve(encoder.encode(part).byteLength);
        line.push(part); lineLength += part.length;
    };
    const accept = (text: string, controller: TransformStreamDefaultController<Uint8Array>) => {
        let start = 0;
        for (let i = 0; i < text.length; i++) {
            if (skipLF) {
                skipLF = false;
                if (text[i] === '\n') { start = i + 1; continue; }
            }
            if (text[i] !== '\r' && text[i] !== '\n') continue;
            append(text.slice(start, i));
            reserve(1);
            if (lineLength) lines.push(line.join(''), '\n');
            else {
                lines.push('\n');
                controller.enqueue(encoder.encode(lines.join('')));
                lines = []; bytes = 0;
            }
            line = []; lineLength = 0;
            skipLF = text[i] === '\r'; start = i + 1;
        }
        append(text.slice(start));
    };
    const body = response.body.pipeThrough(new TransformStream<Uint8Array, Uint8Array>({
        transform(chunk, controller) { accept(decoder.decode(chunk, { stream: true }), controller); },
        flush(controller) {
            accept(decoder.decode(), controller);
            // Keep EOF semantics with the upstream parser; never synthesize a
            // blank delimiter, completion event or identity for an unfinished run.
            if (lines.length || lineLength) controller.enqueue(encoder.encode(lines.join('') + line.join('')));
        },
    }));
    const headers = new Headers(response.headers);
    headers.delete('content-length');
    const result = new Response(body, { status: response.status, statusText: response.statusText, headers });
    framedResponses.add(result);
    return result;
}
