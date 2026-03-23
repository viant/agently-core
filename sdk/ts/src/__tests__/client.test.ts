import { describe, it, expect, vi, beforeEach } from 'vitest';
import { AgentlyClient } from '../client';
import { HttpError } from '../errors';

class MockEventSource {
    static instances: MockEventSource[] = [];
    url: string;
    withCredentials: boolean;
    onmessage: ((event: MessageEvent) => void) | null = null;
    onerror: ((event?: any) => void) | null = null;
    closed = false;

    constructor(url: string, init?: { withCredentials?: boolean }) {
        this.url = url;
        this.withCredentials = !!init?.withCredentials;
        MockEventSource.instances.push(this);
    }

    close(): void {
        this.closed = true;
    }

    emit(data: any): void {
        this.onmessage?.({ data: typeof data === 'string' ? data : JSON.stringify(data) } as MessageEvent);
    }
}

// ─── Mock fetch ────────────────────────────────────────────────────────────────

function mockFetch(status: number, body: any, headers?: Record<string, string>): typeof fetch {
    return vi.fn().mockResolvedValue({
        ok: status >= 200 && status < 300,
        status,
        statusText: status === 200 ? 'OK' : 'Error',
        text: () => Promise.resolve(typeof body === 'string' ? body : JSON.stringify(body)),
        json: () => Promise.resolve(body),
        headers: new Headers(headers ?? {}),
        arrayBuffer: () => Promise.resolve(new ArrayBuffer(0)),
    } as any);
}

function client(fetchImpl: typeof fetch, baseURL = 'http://localhost:8585/v1'): AgentlyClient {
    return new AgentlyClient({ baseURL, fetchImpl, timeoutMs: 0 });
}

function lastCall(fn: ReturnType<typeof vi.fn>): { url: string; method: string; body?: any; headers?: any } {
    const [url, opts] = fn.mock.calls[fn.mock.calls.length - 1];
    let body: any = undefined;
    if (opts?.body !== undefined) {
        if (typeof opts.body === 'string') {
            try {
                body = JSON.parse(opts.body);
            } catch {
                body = opts.body;
            }
        } else {
            body = opts.body;
        }
    }
    return {
        url,
        method: opts?.method || 'GET',
        body,
        headers: opts?.headers,
    };
}

beforeEach(() => {
    MockEventSource.instances = [];
    vi.stubGlobal('EventSource', MockEventSource as any);
});

// ─── Conversations ─────────────────────────────────────────────────────────────

describe('Conversations', () => {
    it('createConversation sends POST with body', async () => {
        const f = mockFetch(200, { id: 'conv_1', title: 'Test' });
        const c = client(f);
        const res = await c.createConversation({ agentId: 'coder', title: 'Test' });

        expect(res.id).toBe('conv_1');
        const call = lastCall(f);
        expect(call.method).toBe('POST');
        expect(call.url).toBe('http://localhost:8585/v1/conversations');
        expect(call.body).toEqual({ agentId: 'coder', title: 'Test' });
    });

    it('listConversations with search query', async () => {
        const f = mockFetch(200, { Rows: [{ id: 'c1' }], HasMore: false, NextCursor: 'c1' });
        const c = client(f);
        const res = await c.listConversations({ query: 'sales', excludeScheduled: true, page: { limit: 10 } });

        expect(res.data).toHaveLength(1);
        expect(res.page?.hasMore).toBe(false);
        expect(res.page?.cursor).toBe('c1');
        const call = lastCall(f);
        expect(call.method).toBe('GET');
        expect(call.url).toContain('q=sales');
        expect(call.url).toContain('excludeScheduled=true');
        expect(call.url).toContain('limit=10');
    });

    it('listConversations without params', async () => {
        const f = mockFetch(200, { data: [], page: {} });
        const c = client(f);
        await c.listConversations();

        const call = lastCall(f);
        expect(call.url).toBe('http://localhost:8585/v1/conversations');
    });

    it('getConversation sends GET with ID', async () => {
        const f = mockFetch(200, { id: 'conv_1' });
        const c = client(f);
        await c.getConversation('conv_1');

        const call = lastCall(f);
        expect(call.url).toBe('http://localhost:8585/v1/conversations/conv_1');
    });

    it('updateConversation sends PATCH', async () => {
        const f = mockFetch(200, { id: 'conv_1', visibility: 'public' });
        const c = client(f);
        await c.updateConversation('conv_1', { visibility: 'public', shareable: true });

        const call = lastCall(f);
        expect(call.method).toBe('PATCH');
        expect(call.body).toEqual({ visibility: 'public', shareable: true });
    });
});

// ─── Messages ──────────────────────────────────────────────────────────────────

describe('Messages', () => {
    it('getMessages with filters', async () => {
        const f = mockFetch(200, { Rows: [], HasMore: false, NextCursor: '' });
        const c = client(f);
        await c.getMessages({
            conversationId: 'conv_1',
            turnId: 'turn_1',
            roles: ['assistant', 'tool'],
            page: { limit: 50, direction: 'latest' },
        });

        const call = lastCall(f);
        expect(call.url).toContain('conversationId=conv_1');
        expect(call.url).toContain('turnId=turn_1');
        expect(call.url).toContain('roles=assistant%2Ctool');
        expect(call.url).toContain('limit=50');
        expect(call.url).toContain('direction=latest');
    });
});

// ─── Transcript ────────────────────────────────────────────────────────────────

describe('Transcript', () => {
    it('getTranscript with tool and model calls', async () => {
        const f = mockFetch(200, { turns: [] });
        const c = client(f);
        await c.getTranscript({
            conversationId: 'conv_1',
            since: 'turn_5',
            includeToolCalls: true,
            includeModelCalls: true,
        });

        const call = lastCall(f);
        expect(call.url).toContain('/conversations/conv_1/transcript');
        expect(call.url).toContain('since=turn_5');
        expect(call.url).toContain('includeToolCalls=true');
        expect(call.url).toContain('includeModelCalls=true');
    });

    it('getTranscript serializes execution-group selector helpers', async () => {
        const f = mockFetch(200, { turns: [] });
        const c = client(f);
        await c.getTranscript(
            { conversationId: 'conv_1' },
            {
                executionGroupLimit: 5,
                executionGroupOffset: 2,
            },
        );

        const call = lastCall(f);
        const rawSelectors = new URL(call.url).searchParams.get('selectors');
        expect(rawSelectors).toBeTruthy();
        const selectors = JSON.parse(String(rawSelectors));
        expect(selectors.ExecutionGroup.limit).toBe(5);
        expect(selectors.ExecutionGroup.offset).toBe(2);
    });
});

// ─── Query ─────────────────────────────────────────────────────────────────────

describe('Query', () => {
    it('sends POST /agent/query with full input', async () => {
        const f = mockFetch(200, { conversationId: 'conv_1', content: 'Hello', messageId: 'msg_1' });
        const c = client(f);
        const res = await c.query({
            conversationId: 'conv_1',
            query: 'Analyze sales',
            agentId: 'coder',
            model: 'gpt-5.3-codex',
            toolBundles: ['system/exec'],
        });

        expect(res.content).toBe('Hello');
        const call = lastCall(f);
        expect(call.method).toBe('POST');
        expect(call.url).toBe('http://localhost:8585/v1/agent/query');
        expect(call.body.query).toBe('Analyze sales');
        expect(call.body.agentId).toBe('coder');
    });
});

describe('Streaming', () => {
    it('dispatches callbacks based on the JSON payload type', () => {
        const f = mockFetch(200, {});
        const c = client(f);
        const seen: string[] = [];
        const text: string[] = [];
        const tools: string[] = [];
        const turns: string[] = [];
        const feeds: string[] = [];

        const sub = c.streamEvents('conv_1', {
            onEvent: (event) => seen.push(event.type),
            onTextDelta: (content) => text.push(content),
            onToolEvent: (event) => tools.push(event.type),
            onTurnEnd: (event) => turns.push(event.type),
            onFeedEvent: (event) => feeds.push(event.type),
        });

        expect(MockEventSource.instances).toHaveLength(1);
        const es = MockEventSource.instances[0];
        expect(es.url).toBe('http://localhost:8585/v1/stream?conversationId=conv_1');
        expect(es.withCredentials).toBe(false);

        es.emit({ type: 'text_delta', streamId: 'conv_1', content: 'hello' });
        es.emit({ type: 'tool_call_started', streamId: 'conv_1', toolName: 'system/exec' });
        es.emit({ type: 'tool_feed_active', streamId: 'conv_1', feedId: 'feed-1' });
        es.emit({ type: 'turn_completed', streamId: 'conv_1', status: 'completed' });

        expect(seen).toEqual(['text_delta', 'tool_call_started', 'tool_feed_active', 'turn_completed']);
        expect(text).toEqual(['hello']);
        expect(tools).toEqual(['tool_call_started']);
        expect(feeds).toEqual(['tool_feed_active']);
        expect(turns).toEqual(['turn_completed']);

        sub.close();
        expect(es.closed).toBe(true);
    });
});

// ─── Steer ─────────────────────────────────────────────────────────────────────

describe('Steer', () => {
    it('steerTurn sends POST with content', async () => {
        const f = mockFetch(202, { messageId: 'msg_5', status: 'accepted' });
        const c = client(f);
        const res = await c.steerTurn('conv_1', 'turn_1', { content: 'also check tests' });

        expect(res.messageId).toBe('msg_5');
        const call = lastCall(f);
        expect(call.method).toBe('POST');
        expect(call.url).toContain('/conversations/conv_1/turns/turn_1/steer');
        expect(call.body).toEqual({ content: 'also check tests', role: 'user' });
    });

    it('steerTurn defaults role to user', async () => {
        const f = mockFetch(202, { messageId: 'msg_5' });
        const c = client(f);
        await c.steerTurn('conv_1', 'turn_1', { content: 'test' });

        const call = lastCall(f);
        expect(call.body.role).toBe('user');
    });
});

// ─── Queue Management ──────────────────────────────────────────────────────────

describe('Queue Management', () => {
    it('cancelQueuedTurn sends DELETE', async () => {
        const f = mockFetch(200, '');
        const c = client(f);
        await c.cancelQueuedTurn('conv_1', 'turn_q1');

        const call = lastCall(f);
        expect(call.method).toBe('DELETE');
        expect(call.url).toContain('/conversations/conv_1/turns/turn_q1');
    });

    it('moveQueuedTurn sends POST with direction', async () => {
        const f = mockFetch(200, '');
        const c = client(f);
        await c.moveQueuedTurn('conv_1', 'turn_q1', { direction: 'up' });

        const call = lastCall(f);
        expect(call.method).toBe('POST');
        expect(call.url).toContain('/turns/turn_q1/move');
        expect(call.body).toEqual({ direction: 'up' });
    });

    it('editQueuedTurn sends PATCH with content', async () => {
        const f = mockFetch(200, '');
        const c = client(f);
        await c.editQueuedTurn('conv_1', 'turn_q1', { content: 'updated text' });

        const call = lastCall(f);
        expect(call.method).toBe('PATCH');
        expect(call.url).toContain('/conversations/conv_1/turns/turn_q1');
        expect(call.body).toEqual({ content: 'updated text' });
    });

    it('forceSteerQueuedTurn sends POST', async () => {
        const f = mockFetch(202, { messageId: 'msg_6', canceledTurnId: 'turn_q1' });
        const c = client(f);
        const res = await c.forceSteerQueuedTurn('conv_1', 'turn_q1');

        expect(res.canceledTurnId).toBe('turn_q1');
        const call = lastCall(f);
        expect(call.method).toBe('POST');
        expect(call.url).toContain('/turns/turn_q1/force-steer');
    });
});

// ─── Turn Management ───────────────────────────────────────────────────────────

describe('Turns', () => {
    it('cancelTurn sends POST', async () => {
        const f = mockFetch(200, { canceled: true });
        const c = client(f);
        const res = await c.cancelTurn('turn_1');

        expect(res).toBe(true);
        const call = lastCall(f);
        expect(call.method).toBe('POST');
        expect(call.url).toContain('/turns/turn_1/cancel');
    });

    it('getRun throws explicit unsupported-route error', async () => {
        const f = mockFetch(200, {});
        const c = client(f);
        await expect(c.getRun('run_1')).rejects.toThrow('/v1/runs/run_1 is not exposed');
    });
});

// ─── Tool Approvals ────────────────────────────────────────────────────────────

describe('Tool Approvals', () => {
    it('listPendingToolApprovals with status filter', async () => {
        const f = mockFetch(200, { rows: [{ id: 'ta_1', toolName: 'exec', status: 'pending' }] });
        const c = client(f);
        const res = await c.listPendingToolApprovals({ status: 'pending' });

        expect(res).toHaveLength(1);
        const call = lastCall(f);
        expect(call.url).toContain('status=pending');
    });

    it('decideToolApproval sends POST with action', async () => {
        const f = mockFetch(200, { status: 'approved' });
        const c = client(f);
        await c.decideToolApproval('ta_1', { action: 'approve' });

        const call = lastCall(f);
        expect(call.method).toBe('POST');
        expect(call.url).toContain('/tool-approvals/ta_1/decision');
        expect(call.body.action).toBe('approve');
    });

    it('decideToolApproval reject with reason', async () => {
        const f = mockFetch(200, { status: 'rejected' });
        const c = client(f);
        await c.decideToolApproval('ta_1', { action: 'reject', reason: 'too dangerous' });

        const call = lastCall(f);
        expect(call.body).toEqual({ action: 'reject', reason: 'too dangerous' });
    });
});

// ─── Elicitations ──────────────────────────────────────────────────────────────

describe('Elicitations', () => {
    it('listPendingElicitations filters by conversationId', async () => {
        const f = mockFetch(200, { rows: [] });
        const c = client(f);
        await c.listPendingElicitations('conv_1');

        const call = lastCall(f);
        expect(call.url).toContain('conversationId=conv_1');
    });

    it('resolveElicitation sends POST', async () => {
        const f = mockFetch(200, '');
        const c = client(f);
        await c.resolveElicitation('conv_1', 'elic_1', { action: 'submit', payload: { name: 'test' } });

        const call = lastCall(f);
        expect(call.method).toBe('POST');
        expect(call.url).toContain('/elicitations/conv_1/elic_1/resolve');
    });
});

// ─── Workspace Resources ───────────────────────────────────────────────────────

describe('Workspace Resources', () => {
    it('listResources with kind filter', async () => {
        const f = mockFetch(200, { names: ['chatter', 'coder'] });
        const c = client(f);
        const res = await c.listResources('agent');

        expect(res.names).toEqual(['chatter', 'coder']);
        const call = lastCall(f);
        expect(call.url).toContain('kind=agent');
    });

    it('getResource fetches by kind and name', async () => {
        const f = mockFetch(200, { kind: 'agent', name: 'coder', data: 'yaml...' });
        const c = client(f);
        await c.getResource('agent', 'coder');

        const call = lastCall(f);
        expect(call.url).toContain('/workspace/resources/agent/coder');
    });

    it('saveResource sends PUT', async () => {
        const f = mockFetch(200, '');
        const c = client(f);
        await c.saveResource('agent', 'coder', 'id: coder\nmodel: gpt-5');

        const call = lastCall(f);
        expect(call.method).toBe('PUT');
        expect(call.url).toContain('/workspace/resources/agent/coder');
        expect(call.body).toBe('id: coder\nmodel: gpt-5');
    });

    it('deleteResource sends DELETE', async () => {
        const f = mockFetch(200, '');
        const c = client(f);
        await c.deleteResource('agent', 'old-agent');

        const call = lastCall(f);
        expect(call.method).toBe('DELETE');
    });

    it('exportResources sends POST with kinds', async () => {
        const f = mockFetch(200, { resources: [] });
        const c = client(f);
        await c.exportResources(['agent', 'model']);

        const call = lastCall(f);
        expect(call.method).toBe('POST');
        expect(call.body.kinds).toEqual(['agent', 'model']);
    });

    it('importResources sends POST', async () => {
        const f = mockFetch(200, { imported: 2, skipped: 0 });
        const c = client(f);
        const res = await c.importResources([{ kind: 'agent', name: 'x', data: 'y' }], true);

        expect(res.imported).toBe(2);
        const call = lastCall(f);
        expect(call.body.replace).toBe(true);
    });
});

describe('Files', () => {
    it('uploadFile throws explicit unsupported-route error', async () => {
        const f = mockFetch(200, {});
        const c = client(f);
        await expect(c.uploadFile('conv_1', new Blob(['x']))).rejects.toThrow('/v1/files');
    });

    it('listFiles uses the exposed GET /v1/files route', async () => {
        const f = mockFetch(200, { files: [{ id: 'file_1', name: 'report.csv', contentType: 'text/csv', size: 42 }] });
        const c = client(f);
        const res = await c.listFiles('conv_1');

        expect(res).toHaveLength(1);
        expect(res[0].name).toBe('report.csv');
        const call = lastCall(f);
        expect(call.method).toBe('GET');
        expect(call.url).toBe('http://localhost:8585/v1/files?conversationId=conv_1');
    });

    it('downloadFile uses the exposed GET /v1/files/{id} route in raw mode', async () => {
        const payload = new Uint8Array([1, 2, 3]).buffer;
        const f = vi.fn().mockResolvedValue({
            ok: true,
            status: 200,
            statusText: 'OK',
            headers: new Headers({
                'content-type': 'text/csv',
                'content-disposition': 'attachment; filename=\"report.csv\"',
            }),
            arrayBuffer: () => Promise.resolve(payload),
            text: () => Promise.resolve(''),
        } as any);
        const c = client(f);
        const res = await c.downloadFile('conv_1', 'file_1');

        expect(res.name).toBe('report.csv');
        expect(res.contentType).toBe('text/csv');
        expect(res.data).toBe(payload);
        const call = lastCall(f);
        expect(call.method).toBe('GET');
        expect(call.url).toBe('http://localhost:8585/v1/files/file_1?conversationId=conv_1&raw=1');
    });
});

// ─── Conversation Maintenance ──────────────────────────────────────────────────

describe('Conversation Maintenance', () => {
    it('terminateConversation sends POST', async () => {
        const f = mockFetch(200, '');
        const c = client(f);
        await c.terminateConversation('conv_1');

        const call = lastCall(f);
        expect(call.url).toContain('/conversations/conv_1/terminate');
    });

    it('compactConversation sends POST', async () => {
        const f = mockFetch(200, '');
        const c = client(f);
        await c.compactConversation('conv_1');

        const call = lastCall(f);
        expect(call.url).toContain('/conversations/conv_1/compact');
    });

    it('pruneConversation sends POST', async () => {
        const f = mockFetch(200, '');
        const c = client(f);
        await c.pruneConversation('conv_1');

        const call = lastCall(f);
        expect(call.url).toContain('/conversations/conv_1/prune');
    });
});

// ─── Tools ─────────────────────────────────────────────────────────────────────

describe('Tools', () => {
    it('executeTool sends POST with args', async () => {
        const f = mockFetch(200, { result: 'done' });
        const c = client(f);
        const res = await c.executeTool('system/exec', { command: 'ls' });

        expect(res).toContain('done');
        const call = lastCall(f);
        expect(call.url).toContain('/tools/system%2Fexec/execute');
        expect(call.body).toEqual({ command: 'ls' });
    });
});

// ─── Error Handling ────────────────────────────────────────────────────────────

describe('Error Handling', () => {
    it('throws HttpError on non-2xx response', async () => {
        const f = mockFetch(404, 'not found');
        const c = client(f);

        await expect(c.getConversation('missing')).rejects.toThrow(HttpError);
        await expect(c.getConversation('missing')).rejects.toThrow(/404/);
    });

    it('throws HttpError with body on 409 Conflict', async () => {
        const f = mockFetch(409, '{"error":"turn is not currently running"}');
        const c = client(f);

        try {
            await c.steerTurn('conv_1', 'turn_done', { content: 'test' });
            expect.unreachable();
        } catch (e: any) {
            expect(e).toBeInstanceOf(HttpError);
            expect(e.status).toBe(409);
            expect(e.body).toContain('not currently running');
        }
    });
});

// ─── Auth ──────────────────────────────────────────────────────────────────────

describe('Auth', () => {
    it('injects Bearer token from tokenProvider', async () => {
        const f = mockFetch(200, { id: 'conv_1' });
        const c = new AgentlyClient({
            baseURL: 'http://localhost:8585/v1',
            fetchImpl: f,
            tokenProvider: () => 'my-token',
            timeoutMs: 0,
        });
        await c.getConversation('conv_1');

        const call = lastCall(f);
        expect(call.headers?.Authorization).toBe('Bearer my-token');
    });

    it('supports async tokenProvider', async () => {
        const f = mockFetch(200, { id: 'conv_1' });
        const c = new AgentlyClient({
            baseURL: 'http://localhost:8585/v1',
            fetchImpl: f,
            tokenProvider: async () => 'async-token',
            timeoutMs: 0,
        });
        await c.getConversation('conv_1');

        const call = lastCall(f);
        expect(call.headers?.Authorization).toBe('Bearer async-token');
    });

    it('no auth header when tokenProvider returns null', async () => {
        const f = mockFetch(200, { id: 'conv_1' });
        const c = new AgentlyClient({
            baseURL: 'http://localhost:8585/v1',
            fetchImpl: f,
            tokenProvider: () => null,
            timeoutMs: 0,
        });
        await c.getConversation('conv_1');

        const call = lastCall(f);
        expect(call.headers?.Authorization).toBeUndefined();
    });
});

// ─── URL encoding ──────────────────────────────────────────────────────────────

describe('URL Encoding', () => {
    it('encodes special characters in path segments', async () => {
        const f = mockFetch(200, { id: 'c/1' });
        const c = client(f);
        await c.getConversation('conv/with/slashes');

        const call = lastCall(f);
        expect(call.url).toContain('conv%2Fwith%2Fslashes');
    });
});

// ─── Scheduler ─────────────────────────────────────────────────────────────

describe('Scheduler', () => {
    it('getSchedule sends GET with ID', async () => {
        const f = mockFetch(200, { id: 'sched_1', name: 'Daily Sync', enabled: true });
        const c = client(f);
        const res = await c.getSchedule('sched_1');

        expect(res.id).toBe('sched_1');
        const call = lastCall(f);
        expect(call.method).toBe('GET');
        expect(call.url).toBe('http://localhost:8585/v1/api/agently/scheduler/schedule/sched_1');
    });

    it('listSchedules sends GET', async () => {
        const f = mockFetch(200, { schedules: [{ id: 's1', name: 'Nightly' }] });
        const c = client(f);
        const res = await c.listSchedules();

        expect(res.schedules).toHaveLength(1);
        const call = lastCall(f);
        expect(call.method).toBe('GET');
        expect(call.url).toBe('http://localhost:8585/v1/api/agently/scheduler/');
    });

    it('upsertSchedules sends PATCH with schedules body', async () => {
        const f = mockFetch(204, '');
        const c = client(f);
        await c.upsertSchedules([
            { id: 's1', name: 'Daily', agentRef: 'coder', enabled: true, scheduleType: 'cron', cronExpr: '0 9 * * *', createdAt: '2025-01-01T00:00:00Z', updatedAt: '2025-01-01T00:00:00Z' },
        ]);

        const call = lastCall(f);
        expect(call.method).toBe('PATCH');
        expect(call.url).toBe('http://localhost:8585/v1/api/agently/scheduler/');
        expect(call.body.schedules).toHaveLength(1);
        expect(call.body.schedules[0].name).toBe('Daily');
    });

    it('runScheduleNow sends POST with ID', async () => {
        const f = mockFetch(204, '');
        const c = client(f);
        await c.runScheduleNow('sched_1');

        const call = lastCall(f);
        expect(call.method).toBe('POST');
        expect(call.url).toBe('http://localhost:8585/v1/api/agently/scheduler/run-now/sched_1');
    });

    it('getSchedule encodes special characters in ID', async () => {
        const f = mockFetch(200, { id: 'sched/1' });
        const c = client(f);
        await c.getSchedule('sched/1');

        const call = lastCall(f);
        expect(call.url).toContain('sched%2F1');
    });
});

// ─── A2A ───────────────────────────────────────────────────────────────────────

describe('A2A', () => {
    it('getA2AAgentCard sends GET', async () => {
        const f = mockFetch(200, { name: 'research' });
        const c = client(f);
        await c.getA2AAgentCard('research');

        const call = lastCall(f);
        expect(call.url).toContain('/api/a2a/agents/research/card');
    });

    it('sendA2AMessage sends POST', async () => {
        const f = mockFetch(200, { taskId: 't1' });
        const c = client(f);
        await c.sendA2AMessage('research', { message: 'hello' });

        const call = lastCall(f);
        expect(call.method).toBe('POST');
        expect(call.url).toContain('/api/a2a/agents/research/message');
    });

    it('listA2AAgents sends GET with ids and unwraps response', async () => {
        const f = mockFetch(200, { agents: ['agent1', 'agent2'] });
        const c = client(f);
        const out = await c.listA2AAgents(['agent1', 'agent2']);

        expect(out).toEqual(['agent1', 'agent2']);
        const call = lastCall(f);
        expect(call.url).toContain('/api/a2a/agents');
        expect(call.url).toContain('ids=agent1%2Cagent2');
    });
});

// ─── Workspace Metadata ───────────────────────────────────────────────────────

describe('Workspace Metadata', () => {
    it('getWorkspaceMetadata sends GET to /workspace/metadata', async () => {
        const body = {
            defaultAgent: 'coder',
            defaultModel: 'gpt-4',
            defaults: { agent: 'coder', model: 'gpt-4', autoSelectTools: true },
            capabilities: { agentAutoSelection: true, toolAutoSelection: true },
            agents: ['coder', 'researcher'],
            models: ['gpt-4', 'claude'],
            agentInfos: [
                {
                    id: 'coder',
                    name: 'Coder',
                    modelRef: 'gpt-4',
                    starterTasks: [{ id: 'analyze', title: 'Analyze', prompt: 'Analyze this repo.' }],
                },
            ],
        };
        const f = mockFetch(200, body);
        const c = client(f);
        const res = await c.getWorkspaceMetadata();

        expect(res.defaultAgent).toBe('coder');
        expect(res.agents).toEqual(['coder', 'researcher']);
        expect(res.defaults?.autoSelectTools).toBe(true);
        expect(res.capabilities?.agentAutoSelection).toBe(true);
        expect(res.agentInfos?.[0]?.starterTasks?.[0]?.id).toBe('analyze');
        const call = lastCall(f);
        expect(call.method).toBe('GET');
        expect(call.url).toBe('http://localhost:8585/v1/workspace/metadata');
    });
});

// ─── Payload ──────────────────────────────────────────────────────────────────

describe('Payload', () => {
    it('getPayload sends GET to /api/payload/{id}', async () => {
        const body = { id: 'p1', kind: 'request', mimeType: 'application/json' };
        const f = mockFetch(200, body);
        const c = client(f);
        const res = await c.getPayload('p1');

        expect(res.id).toBe('p1');
        const call = lastCall(f);
        expect(call.method).toBe('GET');
        expect(call.url).toBe('http://localhost:8585/v1/api/payload/p1');
    });

    it('getPayload with meta option', async () => {
        const f = mockFetch(200, { id: 'p1' });
        const c = client(f);
        await c.getPayload('p1', { meta: true });

        const call = lastCall(f);
        expect(call.url).toContain('meta=1');
    });

    it('getPayload with raw option returns ArrayBuffer', async () => {
        const buf = new ArrayBuffer(4);
        const f = vi.fn().mockResolvedValue({
            ok: true,
            status: 200,
            statusText: 'OK',
            headers: new Headers({ 'content-type': 'text/plain' }),
            arrayBuffer: () => Promise.resolve(buf),
        } as any);
        const c = client(f);
        const res = await c.getPayload('p1', { raw: true });

        expect(res.contentType).toBe('text/plain');
        expect(res.data).toBe(buf);
        const call = lastCall(f);
        expect(call.url).toContain('raw=1');
    });

    it('getPayload encodes special characters in ID', async () => {
        const f = mockFetch(200, { id: 'a/b' });
        const c = client(f);
        await c.getPayload('a/b');

        const call = lastCall(f);
        expect(call.url).toContain('a%2Fb');
    });
});

// ─── File Browser ─────────────────────────────────────────────────────────────

describe('File Browser', () => {
    it('downloadWorkspaceFile sends GET with uri param', async () => {
        const f = vi.fn().mockResolvedValue({
            ok: true,
            status: 200,
            statusText: 'OK',
            text: () => Promise.resolve('file content here'),
        } as any);
        const c = client(f);
        const res = await c.downloadWorkspaceFile('/workspace/main.go');

        expect(res).toBe('file content here');
        const call = lastCall(f);
        expect(call.method).toBe('GET');
        expect(call.url).toContain('/workspace/file-browser/download');
        expect(call.url).toContain('uri=%2Fworkspace%2Fmain.go');
    });

    it('listWorkspaceFiles sends GET to /workspace/file-browser/list', async () => {
        const f = mockFetch(200, { files: [{ name: 'a.go' }] });
        const c = client(f);
        await c.listWorkspaceFiles('/src');

        const call = lastCall(f);
        expect(call.method).toBe('GET');
        expect(call.url).toContain('/workspace/file-browser/list');
        expect(call.url).toContain('path=%2Fsrc');
    });
});

// ─── Linked Conversations ─────────────────────────────────────────────────────

describe('Linked Conversations', () => {
    it('listLinkedConversations sends GET with parentId', async () => {
        const f = mockFetch(200, {
            Rows: [{ conversationId: 'child_1', parentConversationId: 'conv_1', parentTurnId: 't1', createdAt: '2025-01-01' }],
            HasMore: false,
            NextCursor: '',
            PrevCursor: '',
        });
        const c = client(f);
        const res = await c.listLinkedConversations({ parentConversationId: 'conv_1' });

        expect(res.data).toHaveLength(1);
        expect(res.data[0].conversationId).toBe('child_1');
        const call = lastCall(f);
        expect(call.method).toBe('GET');
        expect(call.url).toContain('/conversations/linked');
        expect(call.url).toContain('parentId=conv_1');
    });

    it('listLinkedConversations accepts lowercase rows payloads', async () => {
        const f = mockFetch(200, {
            rows: [{ conversationId: 'child_2', parentConversationId: 'conv_1', parentTurnId: 't1', createdAt: '2025-01-01', status: 'running' }],
            hasMore: false,
            cursor: '',
            prevCursor: '',
        });
        const c = client(f);
        const res = await c.listLinkedConversations({ parentConversationId: 'conv_1' });

        expect(res.data).toHaveLength(1);
        expect(res.data[0].conversationId).toBe('child_2');
        expect(res.data[0].status).toBe('running');
    });

    it('listLinkedConversations with parentTurnId and pagination', async () => {
        const f = mockFetch(200, {
            Rows: [],
            HasMore: true,
            NextCursor: 'c2',
            PrevCursor: 'c0',
        });
        const c = client(f);
        const res = await c.listLinkedConversations({
            parentConversationId: 'conv_1',
            parentTurnId: 'turn_1',
            page: { limit: 5, cursor: 'c1', direction: 'after' },
        });

        expect(res.page?.hasMore).toBe(true);
        expect(res.page?.cursor).toBe('c2');
        expect(res.page?.prevCursor).toBe('c0');
        const call = lastCall(f);
        expect(call.url).toContain('parentTurnId=turn_1');
        expect(call.url).toContain('limit=5');
        expect(call.url).toContain('cursor=c1');
        expect(call.url).toContain('direction=after');
    });
});

// ─── Error Hooks ──────────────────────────────────────────────────────────────

describe('Error Hooks', () => {
    it('onUnauthorized is called on 401', async () => {
        const f = mockFetch(401, 'Unauthorized');
        const onUnauthorized = vi.fn();
        const c = new AgentlyClient({
            baseURL: 'http://localhost:8585/v1',
            fetchImpl: f,
            timeoutMs: 0,
            onUnauthorized,
        });

        await expect(c.getConversation('x')).rejects.toThrow();
        expect(onUnauthorized).toHaveBeenCalledTimes(1);
        expect(onUnauthorized.mock.calls[0][0]).toBeInstanceOf(HttpError);
        expect(onUnauthorized.mock.calls[0][0].status).toBe(401);
    });

    it('onError is called on non-401 errors', async () => {
        const f = mockFetch(500, 'Server Error');
        const onError = vi.fn();
        const c = new AgentlyClient({
            baseURL: 'http://localhost:8585/v1',
            fetchImpl: f,
            timeoutMs: 0,
            retries: 1,
            onError,
        });

        await expect(c.createConversation({ agentId: 'x' })).rejects.toThrow();
        expect(onError).toHaveBeenCalledTimes(1);
        expect(onError.mock.calls[0][0].status).toBe(500);
    });

    it('onError is not called on 401', async () => {
        const f = mockFetch(401, 'Unauthorized');
        const onError = vi.fn();
        const onUnauthorized = vi.fn();
        const c = new AgentlyClient({
            baseURL: 'http://localhost:8585/v1',
            fetchImpl: f,
            timeoutMs: 0,
            onError,
            onUnauthorized,
        });

        await expect(c.getConversation('x')).rejects.toThrow();
        expect(onError).not.toHaveBeenCalled();
        expect(onUnauthorized).toHaveBeenCalledTimes(1);
    });

    it('onError is called on transport/network failures (P2)', async () => {
        const f = vi.fn().mockRejectedValue(new TypeError('Failed to fetch'));
        const onError = vi.fn();
        const c = new AgentlyClient({
            baseURL: 'http://localhost:8585/v1',
            fetchImpl: f,
            timeoutMs: 0,
            retries: 1,
            onError,
        });

        await expect(c.createConversation({ agentId: 'x' })).rejects.toThrow('Failed to fetch');
        expect(onError).toHaveBeenCalledTimes(1);
        expect(onError.mock.calls[0][0].status).toBe(0);
        expect(onError.mock.calls[0][0].body).toContain('Failed to fetch');
    });

    it('onError is called on timeout abort (P2)', async () => {
        const f = vi.fn().mockRejectedValue(new DOMException('The operation was aborted', 'AbortError'));
        const onError = vi.fn();
        const c = new AgentlyClient({
            baseURL: 'http://localhost:8585/v1',
            fetchImpl: f,
            timeoutMs: 0,
            retries: 1,
            onError,
        });

        await expect(c.getConversation('x')).rejects.toThrow();
        expect(onError).toHaveBeenCalledTimes(1);
        expect(onError.mock.calls[0][0].status).toBe(0);
    });
});

// ─── UpdateConversation title ─────────────────────────────────────────────────

describe('UpdateConversation with title', () => {
    it('sends title in PATCH body', async () => {
        const f = mockFetch(200, { id: 'conv_1', title: 'New Title' });
        const c = client(f);
        await c.updateConversation('conv_1', { title: 'New Title' });

        const call = lastCall(f);
        expect(call.method).toBe('PATCH');
        expect(call.body).toEqual({ title: 'New Title' });
    });

    it('sends title + visibility together', async () => {
        const f = mockFetch(200, { id: 'conv_1' });
        const c = client(f);
        await c.updateConversation('conv_1', { title: 'T', visibility: 'archived' });

        const call = lastCall(f);
        expect(call.body).toEqual({ title: 'T', visibility: 'archived' });
    });
});

// ─── listConversations prevCursor ─────────────────────────────────────────────

describe('listConversations prevCursor', () => {
    it('includes prevCursor in page output', async () => {
        const f = mockFetch(200, {
            Rows: [{ id: 'c1', createdAt: '2025-01-01' }],
            HasMore: true,
            NextCursor: 'next_1',
            PrevCursor: 'prev_1',
        });
        const c = client(f);
        const res = await c.listConversations();

        expect(res.page?.cursor).toBe('next_1');
        expect(res.page?.prevCursor).toBe('prev_1');
        expect(res.page?.hasMore).toBe(true);
    });
});

// ─── Auth ─────────────────────────────────────────────────────────────────────

describe('Auth', () => {
    it('getAuthProviders returns providers array', async () => {
        const f = mockFetch(200, { providers: [{ name: 'local', type: 'local', label: 'Local User' }] });
        const c = client(f);
        const res = await c.getAuthProviders();

        expect(res).toHaveLength(1);
        expect(res[0].name).toBe('local');
        const call = lastCall(f);
        expect(call.method).toBe('GET');
        expect(call.url).toContain('/api/auth/providers');
    });

    it('getAuthProviders unwraps flat array response', async () => {
        const f = mockFetch(200, [{ name: 'oauth', type: 'bff' }]);
        const c = client(f);
        const res = await c.getAuthProviders();

        expect(res).toHaveLength(1);
        expect(res[0].type).toBe('bff');
    });

    it('getAuthMe returns user', async () => {
        const f = mockFetch(200, { subject: 'user1', email: 'u@t.com', provider: 'session' });
        const c = client(f);
        const res = await c.getAuthMe();

        expect(res?.subject).toBe('user1');
        expect(res?.email).toBe('u@t.com');
    });

    it('getAuthMe returns null on 401', async () => {
        const f = mockFetch(401, { error: 'not authenticated' });
        const c = client(f);
        const res = await c.getAuthMe();

        expect(res).toBeNull();
    });

    it('localLogin sends POST with username', async () => {
        const f = mockFetch(200, { sessionId: 's1', username: 'dev' });
        const c = client(f);
        const res = await c.localLogin({ username: 'dev' });

        expect(res.sessionId).toBe('s1');
        const call = lastCall(f);
        expect(call.method).toBe('POST');
        expect(call.url).toContain('/api/auth/local/login');
        expect(call.body).toEqual({ username: 'dev' });
    });

    it('logout sends POST', async () => {
        const f = mockFetch(200, { status: 'ok' });
        const c = client(f);
        await c.logout();

        const call = lastCall(f);
        expect(call.method).toBe('POST');
        expect(call.url).toContain('/api/auth/logout');
    });

    it('oauthInitiate sends POST', async () => {
        const f = mockFetch(200, { authURL: 'https://idp/auth', state: 'abc' });
        const c = client(f);
        const res = await c.oauthInitiate();

        expect(res.authURL).toBe('https://idp/auth');
        expect(res.state).toBe('abc');
        const call = lastCall(f);
        expect(call.method).toBe('POST');
    });

    it('oauthCallback sends POST with code and state', async () => {
        const f = mockFetch(200, { status: 'ok', username: 'user1' });
        const c = client(f);
        const res = await c.oauthCallback({ code: 'xyz', state: 'abc' });

        expect(res.status).toBe('ok');
        const call = lastCall(f);
        expect(call.body).toEqual({ code: 'xyz', state: 'abc' });
    });

    it('getOAuthConfig sends GET', async () => {
        const f = mockFetch(200, { mode: 'bff', clientId: 'c1' });
        const c = client(f);
        const res = await c.getOAuthConfig();

        expect(res.mode).toBe('bff');
        const call = lastCall(f);
        expect(call.method).toBe('GET');
        expect(call.url).toContain('/api/auth/oauth/config');
    });

    it('createAuthSession sends POST', async () => {
        const f = mockFetch(200, { sessionId: 's2', username: 'anon' });
        const c = client(f);
        const res = await c.createAuthSession({ username: 'anon' });

        expect(res.sessionId).toBe('s2');
        const call = lastCall(f);
        expect(call.url).toContain('/api/auth/session');
    });

    it('idpDelegate sends POST', async () => {
        const f = mockFetch(200, { mode: 'delegated', authURL: 'https://idp/auth', state: 'enc', idpLogin: 'https://idp/auth' });
        const c = client(f);
        const res = await c.idpDelegate();

        expect(res.mode).toBe('delegated');
        expect(res.authURL).toBe('https://idp/auth');
    });

    it('idpLoginURL builds correct URL', () => {
        const c = client(mockFetch(200, {}));
        expect(c.idpLoginURL()).toBe('http://localhost:8585/v1/api/auth/idp/login');
        expect(c.idpLoginURL('/dashboard')).toBe('http://localhost:8585/v1/api/auth/idp/login?returnURL=%2Fdashboard');
    });
});
