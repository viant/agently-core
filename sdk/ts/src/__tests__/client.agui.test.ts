import { describe, expect, it, vi } from 'vitest';
import { AgentlyClient } from '../client';

function completed(threadId: string, runId: string) {
    return new Response([
        { type: 'RUN_STARTED', threadId, runId }, { type: 'RUN_FINISHED', threadId, runId },
    ].map(event => `data: ${JSON.stringify(event)}\n\n`).join(''), { headers: { 'Content-Type': 'text/event-stream' } });
}

describe('AG-UI through the existing BFF client', () => {
    it('uses AG-UI for default query with BFF credentials and no legacy POST', async () => {
        const posted: any[] = [];
        const fetchImpl = vi.fn(async (url: RequestInfo | URL, init?: RequestInit) => {
            expect(init?.credentials).toBe('include');
            expect(new Headers(init?.headers).get('Authorization')).toBe('Bearer fixture-token');
            if (url === '/v1/conversations/thread') {
                expect(init?.method).toBe('GET');
                return new Response(JSON.stringify({id:'thread'}), {headers:{'Content-Type':'application/json'}});
            }
            expect(url).toBe('/v1/ag-ui/run');
            const input = JSON.parse(String(init?.body));
            posted.push(input);
            const bootstrap = input.forwardedProps.agently.operation === 'conversation.bootstrap';
            const events: any[] = [
                {type:'RUN_STARTED', threadId:input.threadId, runId:input.runId,
                 metadata:{agently:{identityVersion:'1', nativeTurnId:'native-turn'}}},
            ];
            if (!bootstrap) events.push({type:'ACTIVITY_SNAPSHOT', messageId:'turn', activityType:'agently.turn',
                content:{version:'1', nativeTurnId:'native-turn', status:'running'}});
            events.push({type:'RUN_FINISHED', threadId:input.threadId, runId:input.runId,
                result: bootstrap ? {version:'1', threadId:input.threadId,
                    transcript:{schemaVersion:'1', conversation:{conversationId:input.threadId, turns:[]}},
                    messages:[], state:{}, runs:[], hostActivities:[], unavailableHostActivityIds:[],
                    projection:{lossless:true, unavailableMessageIds:[]}} : undefined});
            return new Response(events.map(event=>`data: ${JSON.stringify(event)}\n\n`).join(''),
                {headers:{'Content-Type':'text/event-stream'}});
        });
        const client = new AgentlyClient({baseURL:'/v1', useCookies:true, tokenProvider:()=> 'fixture-token', fetchImpl});
        const output = await client.query({conversationId:'thread', query:'hello'});
        expect(output.turnId).toBe('native-turn');
        expect(posted.map(input=>input.forwardedProps.agently.operation)).toEqual(['conversation.bootstrap','chat']);
        expect(posted[1].messages).toEqual([expect.objectContaining({role:'user',content:'hello'})]);
        client.resetAgUiInteractions();
    });

    it('keeps cookies, dynamic credentials and debug policy on the configured BFF transport', async () => {
        let token = 'first';
        const fetchImpl = vi.fn(async (_url: RequestInfo | URL, init?: RequestInit) => {
            const input = JSON.parse(String(init?.body));
            return completed(input.threadId, input.runId);
        });
        const client = new AgentlyClient({ baseURL: '/v1', useCookies: true, tokenProvider: () => token,
            headers: { 'X-Client': 'web' }, sessionDebug: { level: 'info' }, fetchImpl });
        const session = client.createAgUiSession({ threadId: 'thread' });
        await session.send({ id: 'u1', role: 'user', content: 'one' }, { runId: 'r1' });
        token = 'refreshed';
        await session.send({ id: 'u2', role: 'user', content: 'two' }, { runId: 'r2' });
        for (const [index, call] of fetchImpl.mock.calls.entries()) {
            expect(call[0]).toBe('/v1/ag-ui/run');
            expect(call[1]).toMatchObject({ credentials: 'include', redirect: 'error', method: 'POST' });
            const headers = new Headers(call[1]?.headers);
            expect(headers.get('Authorization')).toBe(`Bearer ${index ? 'refreshed' : 'first'}`);
            expect(headers.get('Accept')).toBe('text/event-stream');
            expect(headers.get('X-Client')).toBe('web');
            expect(headers.get('X-Agently-Debug-Level')).toBe('info');
        }
    });

    it('routes independent backends through the BFF and sends only standard protocol input', async () => {
        const fetchImpl = vi.fn(async (_url: RequestInfo | URL, init?: RequestInit) => {
            const input = JSON.parse(String(init?.body)); return completed(input.threadId, input.runId);
        });
        const client = new AgentlyClient({ baseURL: '/v1', useCookies: true, fetchImpl });
        await client.createAgUiSession({ threadId: 'thread', connectionId: 'langgraph' })
            .send({ id: 'u', role: 'user', content: 'hello' }, { runId: 'r' });
        expect(fetchImpl.mock.calls[0][0]).toBe('/v1/ag-ui/backends/langgraph/run');
        expect(JSON.parse(String(fetchImpl.mock.calls[0][1]?.body)).forwardedProps).not.toHaveProperty('agently');
        await expect(client.agUiTransport().fetch!('https://another-host.invalid/run', {})).rejects.toThrow('configured BFF route');
        expect(fetchImpl).toHaveBeenCalledTimes(1);
    });

    it('preserves the unauthorized hook exactly once and never automatically retries a run POST', async () => {
        const onUnauthorized = vi.fn();
        const onError = vi.fn();
        const fetchImpl = vi.fn(async () => new Response('Session expired', { status: 401 }));
        const client = new AgentlyClient({ baseURL: '/v1', useCookies: true, fetchImpl, retries: 3, onUnauthorized, onError });
        await expect(client.createAgUiSession({ threadId: 'thread' }).send({ id: 'u', role: 'user', content: 'hello' }))
            .rejects.toMatchObject({ status: 401 });
        expect(onUnauthorized).toHaveBeenCalledTimes(1);
        expect(onError).not.toHaveBeenCalled();
        expect(fetchImpl).toHaveBeenCalledTimes(1);
    });

    it('distinguishes forbidden from unauthenticated and does not replay a failing command', async () => {
        const onUnauthorized = vi.fn();
        const onError = vi.fn();
        const fetchImpl = vi.fn(async () => new Response('Not permitted', { status: 403 }));
        const client = new AgentlyClient({ baseURL: '/v1', useCookies: true, fetchImpl, onUnauthorized, onError });
        await expect(client.createAgUiSession({ threadId: 'thread' }).send({ id: 'u', role: 'user', content: 'hello' }))
            .rejects.toMatchObject({ status: 403 });
        expect(onUnauthorized).not.toHaveBeenCalled();
        expect(onError).toHaveBeenCalledTimes(1);
        expect(fetchImpl).toHaveBeenCalledTimes(1);
    });
});
