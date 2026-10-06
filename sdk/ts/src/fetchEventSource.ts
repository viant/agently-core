// EventSource-compatible transport for clients using bearer or custom headers.
// The cookie-only browser transport remains native EventSource.
export class FetchEventSource {
    onmessage: ((event: { data: string }) => void) | null = null;
    onerror: (() => void) | null = null;
    onopen: (() => void) | null = null;
    private controller = new AbortController();
    private closed = false;
    private retryMs = 1000;
    private lastEventId = '';
    private retryTimer?: ReturnType<typeof setTimeout>;
    constructor(url: string, fetchImpl: typeof fetch, headers: () => Promise<Record<string, string>>, credentials: RequestCredentials) {
        void this.read(url, fetchImpl, headers, credentials);
    }
    close(): void { this.closed = true; if (this.retryTimer) clearTimeout(this.retryTimer); this.controller.abort(); }
    private async read(url: string, fetchImpl: typeof fetch, headers: () => Promise<Record<string, string>>, credentials: RequestCredentials): Promise<void> {
        try {
            const response = await fetchImpl(url, { headers: { ...await headers(), Accept: 'text/event-stream', ...(this.lastEventId ? { 'Last-Event-ID': this.lastEventId } : {}) }, credentials, signal: this.controller.signal });
            if (!response.ok || !response.body) { await response.body?.cancel(); throw new Error('SSE connection failed'); }
            if (!this.closed) this.onopen?.();
            const reader = response.body.getReader();
            const decoder = new TextDecoder();
            let buffer = '', data: string[] = [], eventType = '';
            try {
                while (!this.closed) {
                    const part = await reader.read();
                    if (part.done) break;
                    buffer += decoder.decode(part.value, { stream: true });
                    let end: number;
                    while ((end = buffer.indexOf('\n')) >= 0) {
                        let line = buffer.slice(0, end); buffer = buffer.slice(end + 1);
                        if (line.endsWith('\r')) line = line.slice(0, -1);
                        if (line === '') {
                            if (data.length && !this.closed && (!eventType || eventType === 'message')) this.onmessage?.({ data: data.join('\n') });
                            data = []; eventType = '';
                        } else if (line.startsWith('data:')) {
                            let value = line.slice(5); if (value.startsWith(' ')) value = value.slice(1);
                            data.push(value);
                        } else if (line.startsWith('id:')) {
                            const value = line.slice(3).replace(/^ /, '');
                            if (!value.includes('\0')) this.lastEventId = value;
                        } else if (line.startsWith('event:')) {
                            eventType = line.slice(6).replace(/^ /, '');
                        } else if (/^retry: ?[0-9]+$/.test(line)) {
                            this.retryMs = Number(line.slice(6).trim());
                        }
                    }
                }
            } finally { await reader.cancel().catch(() => undefined); reader.releaseLock(); }
            if (!this.closed) this.onerror?.();
        } catch { if (!this.closed) this.onerror?.(); }
        if (!this.closed) this.retryTimer = setTimeout(() => { void this.read(url, fetchImpl, headers, credentials); }, this.retryMs);
    }
}
