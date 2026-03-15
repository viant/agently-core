import { describe, it, expect, vi, beforeEach } from 'vitest';
import { AgentlyClient } from '../client';
import { HttpError } from '../errors';

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
        const res = await c.listConversations({ query: 'sales', page: { limit: 10 } });

        expect(res.data).toHaveLength(1);
        expect(res.page?.hasMore).toBe(false);
        expect(res.page?.cursor).toBe('c1');
        const call = lastCall(f);
        expect(call.method).toBe('GET');
        expect(call.url).toContain('q=sales');
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

    it('listFiles throws explicit unsupported-route error', async () => {
        const f = mockFetch(200, {});
        const c = client(f);
        await expect(c.listFiles('conv_1')).rejects.toThrow('/v1/files');
    });

    it('downloadFile throws explicit unsupported-route error', async () => {
        const f = mockFetch(200, {});
        const c = client(f);
        await expect(c.downloadFile('conv_1', 'file_1')).rejects.toThrow('/v1/files/{id}');
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
