import { afterEach, describe, expect, it } from 'vitest';
import { createServer, type Server } from 'node:http';
import { readFileSync } from 'node:fs';
import { EventSchema } from '@ag-ui/core/schemas';
import { AgUiClient, parseAgentlyCapabilities } from './agui';

const servers: Server[] = [];
afterEach(async () => { await Promise.all(servers.splice(0).map(server => new Promise<void>(resolve => server.close(() => resolve())))); });
async function fixture(events: object[]) {
    let request: any;
    const server = createServer(async (req, res) => {
        let body = ''; for await (const chunk of req) body += chunk;
        request = JSON.parse(body);
        res.writeHead(200, { 'Content-Type': 'text/event-stream' });
        for (const event of events) {
            const frame = `data: ${JSON.stringify(event)}\n\n`;
            // Deliberately split the SSE prefix, JSON fields and UTF-8 text.
            const bytes = Buffer.from(frame);
            for (let offset = 0; offset < bytes.length; offset += 7) {
                res.write(bytes.subarray(offset, offset + 7));
                await new Promise(resolve => setTimeout(resolve, 1));
            }
        }
        res.end();
    });
    servers.push(server);
    await new Promise<void>(resolve => server.listen(0, '127.0.0.1', resolve));
    const address = server.address();
    if (!address || typeof address === 'string') throw new Error('Missing fixture address');
    return { url: `http://127.0.0.1:${address.port}/run`, request: () => request };
}
const start = { type: 'RUN_STARTED', threadId: 'thread', runId: 'run' };
const finish = { type: 'RUN_FINISHED', threadId: 'thread', runId: 'run' };

describe('AG-UI HTTP integration', () => {
    it('validates shared Go producer fixtures with the pinned upstream schema', () => {
        const events = JSON.parse(readFileSync(new URL('../../../protocol/agui/testdata/events.json', import.meta.url), 'utf8'));
        for (const event of events) expect(() => EventSchema.parse(event)).not.toThrow();
    });
    it('uses standard external agents and preserves text, tool arguments/results and snapshots', async () => {
        const history = { id: 'history', role: 'system', content: 'instructions', encryptedValue: 'opaque', metadata: { source: 'fixture' } };
        const endpoint = await fixture([
            start,
            { type: 'MESSAGES_SNAPSHOT', messages: [history] },
            { type: 'STATE_SNAPSHOT', snapshot: { count: 1, nested: { keep: true } } },
            { type: 'STATE_DELTA', delta: [{ op: 'replace', path: '/count', value: 2 }] },
            { type: 'TEXT_MESSAGE_START', messageId: 'assistant', role: 'assistant' },
            { type: 'TEXT_MESSAGE_CONTENT', messageId: 'assistant', delta: 'héllo 🌍' },
            { type: 'TEXT_MESSAGE_END', messageId: 'assistant' },
            { type: 'TOOL_CALL_START', toolCallId: 'tool', toolCallName: 'lookup', parentMessageId: 'assistant' },
            { type: 'TOOL_CALL_ARGS', toolCallId: 'tool', delta: '{"id":' },
            { type: 'TOOL_CALL_ARGS', toolCallId: 'tool', delta: '42}' },
            { type: 'TOOL_CALL_END', toolCallId: 'tool' },
            { type: 'TOOL_CALL_RESULT', toolCallId: 'tool', messageId: 'result', content: '{"ok":true}', role: 'tool' },
            { type: 'CUSTOM', name: 'external.opaque', value: { keep: [1, 2] } },
            finish,
        ]);
        const client = new AgUiClient({ url: endpoint.url, threadId: 'thread' });
        const custom: unknown[] = [];
        await client.run({ runId: 'run' }, { onCustomEvent: ({ event }) => { custom.push(event.value); } });
        expect(endpoint.request().forwardedProps).not.toHaveProperty('agently');
        expect(client.messages[0]).toEqual(history);
        expect(client.messages.find(message => message.id === 'assistant')).toMatchObject({ content: 'héllo 🌍', toolCalls: [{ id: 'tool', function: { name: 'lookup', arguments: '{"id":42}' } }] });
        expect(client.messages.find(message => message.id === 'result')).toMatchObject({ role: 'tool', toolCallId: 'tool', content: '{"ok":true}' });
        expect(client.state).toEqual({ count: 2, nested: { keep: true } });
        expect(custom).toEqual([{ keep: [1, 2] }]);
        const nextHistory = structuredClone(client.messages);
        await client.run({ runId: 'run' });
        expect(endpoint.request().messages).toEqual(nextHistory);
        expect(endpoint.request().state).toEqual({ count: 2, nested: { keep: true } });
    });

    it('discovers versioned capabilities and forwards execution selection', async () => {
        const capabilities = { custom: { agently: { version: '1', operations: ['chat', 'capabilities'], execution: { agentId: true, model: true } } } };
        const endpoint = await fixture([start, { type: 'CUSTOM', name: 'agently.capabilities', value: { version: '1', capabilities } }, finish]);
        const client = new AgUiClient({ url: endpoint.url, threadId: 'thread' });
        expect(await client.discoverCapabilities({ runId: 'run' })).toEqual(capabilities);
        expect(endpoint.request().forwardedProps.agently).toEqual({ version: '1', operation: 'capabilities' });
        await client.runAgently({ agentId: 'a', model: 'm' }, { runId: 'run', forwardedProps: { external: true } });
        expect(endpoint.request().forwardedProps).toEqual({ external: true, agently: { version: '1', operation: 'chat', payload: { agentId: 'a', model: 'm' } } });
        expect(parseAgentlyCapabilities({ version: '2', capabilities })).toBeUndefined();
    });

    it('delivers RUN_ERROR and rejects invalid event ordering through HttpAgent', async () => {
        const endpoint = await fixture([start, { type: 'RUN_ERROR', message: 'backend failed', code: 'EXECUTION_FAILED' }]);
        const client = new AgUiClient({ url: endpoint.url, threadId: 'thread' });
        const errors: string[] = [];
        await client.run({ runId: 'run' }, { onRunErrorEvent: ({ event }) => { errors.push(event.message); } });
        expect(errors).toEqual(['backend failed']);
        const invalid = await fixture([start, { type: 'TEXT_MESSAGE_CONTENT', messageId: 'missing', delta: 'bad' }, finish]);
        await expect(new AgUiClient({ url: invalid.url, threadId: 'thread' }).run({ runId: 'run' })).rejects.toThrow();
    });
});

describe('AG-UI consumer conformance', () => {
    it('reduces reasoning, activity, chunks, attribution and every RFC6902 operation over HTTP', async () => {
        const events = [start,
            { type: 'STEP_STARTED', stepName: 'work' },
            { type: 'STATE_SNAPSHOT', snapshot: { list: [1, 2], source: { value: 3 }, remove: true, 'a/b': { '~key': 1 } } },
            { type: 'STATE_DELTA', delta: [
                { op: 'test', path: '/source/value', value: 3 },
                { op: 'add', path: '/list/-', value: 4 },
                { op: 'copy', from: '/source', path: '/copy' },
                { op: 'move', from: '/copy/value', path: '/moved' },
                { op: 'replace', path: '/a~1b/~0key', value: 9 },
                { op: 'remove', path: '/remove' },
            ] },
            { type: 'SUBAGENT_STARTED', subagentRunId: 'child', name: 'research' },
            { type: 'REASONING_START', messageId: 'span', subagentRunId: 'child' },
            { type: 'REASONING_MESSAGE_START', role: 'reasoning', messageId: 'reason', subagentRunId: 'child', metadata: { phase: 1 } },
            { type: 'REASONING_MESSAGE_CONTENT', messageId: 'reason', delta: 'think', subagentRunId: 'child' },
            { type: 'REASONING_MESSAGE_END', messageId: 'reason', subagentRunId: 'child' },
            { type: 'REASONING_ENCRYPTED_VALUE', subtype: 'message', entityId: 'reason', encryptedValue: 'opaque' },
            { type: 'REASONING_MESSAGE_CHUNK', messageId: 'reason-chunk', delta: 'more', subagentRunId: 'child' },
            { type: 'REASONING_END', messageId: 'span', subagentRunId: 'child' },
            { type: 'ACTIVITY_SNAPSHOT', messageId: 'activity', activityType: 'plan', content: { tasks: ['one'], status: 'open' }, metadata: { source: 'agent' }, subagentRunId: 'child' },
            { type: 'ACTIVITY_DELTA', messageId: 'activity', activityType: 'plan', patch: [{ op: 'add', path: '/tasks/-', value: 'two' }, { op: 'replace', path: '/status', value: 'done' }] },
            { type: 'TEXT_MESSAGE_CHUNK', messageId: 'chunk', role: 'assistant', delta: 'hello ', subagentRunId: 'child' },
            { type: 'TEXT_MESSAGE_CHUNK', delta: 'world' },
            { type: 'TOOL_CALL_CHUNK', toolCallId: 'chunk-call', toolCallName: 'lookup', parentMessageId: 'chunk', delta: '{"n":' },
            { type: 'TOOL_CALL_CHUNK', delta: '1}' },
            { type: 'TOOL_CALL_RESULT', toolCallId: 'chunk-call', messageId: 'chunk-result', content: [{ type: 'text', text: 'found' }] },
            { type: 'RAW', event: { provider: 'opaque', values: [1, 2] }, source: 'external' },
            { type: 'CUSTOM', name: 'external.extension', value: { preserved: true } },
            { type: 'SUBAGENT_FINISHED', subagentRunId: 'child', result: { ok: true } },
            { type: 'SUBAGENT_STARTED', subagentRunId: 'failed', name: 'optional' },
            { type: 'SUBAGENT_ERROR', subagentRunId: 'failed', message: 'optional failed', code: 'OPTIONAL' },
            { type: 'STEP_FINISHED', stepName: 'work' },
            { ...finish, outcome: { type: 'success' }, result: { answer: 42 } },
        ];
        for (const event of events) EventSchema.parse(event);
        const endpoint = await fixture(events);
        const client = new AgUiClient({ url: endpoint.url, threadId: 'thread' });
        const observed: string[] = [];
        const subscriber = {
            onRawEvent: ({ event }: any) => { observed.push(event.type); expect(event.event).toEqual({ provider: 'opaque', values: [1, 2] }); },
            onSubagentStartedEvent: ({ event }: any) => { observed.push(event.subagentRunId); },
            onSubagentFinishedEvent: ({ event }: any) => { observed.push(event.type); },
            onSubagentErrorEvent: ({ event }: any) => { observed.push(event.code); },
            onStepStartedEvent: ({ event }: any) => { observed.push(event.type); },
            onStepFinishedEvent: ({ event }: any) => { observed.push(event.type); },
        };
        const result = await client.run({ runId: 'run' }, subscriber);
        expect(result.result).toEqual({ answer: 42 });
        expect(client.state).toEqual({ list: [1, 2, 4], source: { value: 3 }, copy: {}, moved: 3, 'a/b': { '~key': 9 } });
        expect(client.messages.find(message => message.id === 'reason')).toMatchObject({ role: 'reasoning', content: 'think', encryptedValue: 'opaque', subagentRunId: 'child', metadata: { phase: 1 } });
        expect(client.messages.find(message => message.id === 'reason-chunk')).toMatchObject({ role: 'reasoning', content: 'more' });
        expect(client.messages.find(message => message.id === 'activity')).toMatchObject({ role: 'activity', content: { tasks: ['one', 'two'], status: 'done' }, metadata: { source: 'agent' } });
        expect(client.messages.find(message => message.id === 'chunk')).toMatchObject({ content: 'hello world', toolCalls: [{ id: 'chunk-call', function: { arguments: '{"n":1}' } }] });
        expect(client.messages.find(message => message.id === 'chunk-result')).toMatchObject({ content: [{ type: 'text', text: 'found' }] });
        expect(observed).toEqual(['STEP_STARTED', 'child', 'RAW', 'SUBAGENT_FINISHED', 'failed', 'OPTIONAL', 'STEP_FINISHED']);
    });

    it('executes registered frontend tools once and sends results in the next standard request', async () => {
        const endpoint = await fixture([start,
            { type: 'TOOL_CALL_START', toolCallId: 'front', toolCallName: 'lookup', parentMessageId: 'assistant' },
            { type: 'TOOL_CALL_ARGS', toolCallId: 'front', delta: '{"id":7}' },
            { type: 'TOOL_CALL_END', toolCallId: 'front' },
            { ...finish, outcome: { type: 'success', pendingToolCallIds: ['front'] } },
        ]);
        const client = new AgUiClient({ url: endpoint.url, threadId: 'thread' });
        const tool = { name: 'lookup', description: 'Client lookup', parameters: { type: 'object' } };
        await client.run({ runId: 'run', tools: [tool] });
        await expect(client.executeClientTools([])).rejects.toThrow('No client handler');
        let executions = 0;
        const handlers = [{ tool, execute: (args: unknown) => { executions++; expect(args).toEqual({ id: 7 }); return { content: JSON.stringify({ found: true }) }; } }];
        const results = await client.executeClientTools(handlers);
        expect(results[0]).toMatchObject({ role: 'tool', toolCallId: 'front', content: '{"found":true}' });
        expect(await client.executeClientTools(handlers)).toEqual([]);
        expect(executions).toBe(1);
        await client.run({ runId: 'run', tools: [tool] });
        expect(endpoint.request().messages).toContainEqual(results[0]);
        expect(endpoint.request().tools).toEqual([tool]);
        expect(endpoint.request().forwardedProps).not.toHaveProperty('agently');
    });

    it('requires every open interrupt response, preserves payload/metadata and clears after success', async () => {
        const endpoint = await fixture([start, { ...finish, outcome: { type: 'interrupt', interrupts: [
            { id: 'approval', reason: 'approval', toolCallId: 'call', metadata: { signature: 'opaque' } },
            { id: 'input', reason: 'input', responseSchema: { type: 'string' } },
        ] } }]);
        const client = new AgUiClient({ url: endpoint.url, threadId: 'thread' });
        await client.run({ runId: 'run' });
        expect(client.pendingInterrupts.map(item => item.id)).toEqual(['approval', 'input']);
        expect(() => client.resume({ approval: { status: 'resolved', payload: true } })).toThrow('missing responses');
        const success = await fixture([start, finish]);
        client.agent.url = success.url;
        await client.resume({ approval: { status: 'resolved', payload: true, metadata: { proof: 'signed' } }, input: { status: 'cancelled' } }, { runId: 'run' });
        expect(success.request().resume).toEqual([{ interruptId: 'approval', status: 'resolved', payload: true, metadata: { proof: 'signed' } }, { interruptId: 'input', status: 'cancelled' }]);
        expect(client.pendingInterrupts).toEqual([]);
    });
});

describe('AG-UI lossless history and recovery', () => {
    it('preserves every role and media source, following upstream activity input exclusion', async () => {
        const parts = [
            { type: 'text', text: 'inspect', id: 'text', metadata: ['opaque'] },
            { type: 'image', source: { type: 'url', value: 'https://example.test/image', mimeType: 'image/png' }, metadata: { width: 12 } },
            { type: 'audio', source: { type: 'data', value: 'AQID', mimeType: 'audio/wav' } },
            { type: 'video', source: { type: 'file', value: 'opaque:file', provider: 'external', mimeType: 'video/mp4' } },
            { type: 'document', source: { type: 'url', value: 'https://example.test/document' } },
        ];
        const messages = [
            { id: 'system', role: 'system', content: 'instructions', name: 'system', metadata: { trace: [1] } },
            { id: 'developer', role: 'developer', content: 'policy' },
            { id: 'user', role: 'user', content: parts, encryptedValue: 'user-opaque' },
            { id: 'assistant', role: 'assistant', toolCalls: [{ id: 'past-call', type: 'function', function: { name: 'past', arguments: '{}' }, encryptedValue: 'tool-opaque', metadata: { key: 'value' } }] },
            { id: 'tool', role: 'tool', toolCallId: 'past-call', content: parts, error: 'partial failure', metadata: { partial: true } },
            { id: 'reasoning', role: 'reasoning', content: 'thought', encryptedValue: 'opaque' },
            { id: 'activity', role: 'activity', activityType: 'plan', content: { tasks: [] }, metadata: { visible: true } },
        ];
        const snapshot = { type: 'MESSAGES_SNAPSHOT', messages };
        EventSchema.parse(snapshot);
        const endpoint = await fixture([start, snapshot, finish]);
        const client = new AgUiClient({ url: endpoint.url, threadId: 'thread' });
        await client.run({ runId: 'run' });
        expect(client.messages).toEqual(messages);
        await client.run({ runId: 'run' });
        expect(endpoint.request().messages).toEqual(messages.filter(message => message.role !== 'activity'));
    });

    it('retains a failed client call for explicit retry and accepts multipart handler results', async () => {
        const endpoint = await fixture([start,
            { type: 'TOOL_CALL_CHUNK', toolCallId: 'first', toolCallName: 'local', parentMessageId: 'assistant', delta: '{"n":1}' },
            { type: 'TOOL_CALL_CHUNK', toolCallId: 'second', toolCallName: 'local', parentMessageId: 'assistant', delta: '{"n":2}' }, finish]);
        const client = new AgUiClient({ url: endpoint.url, threadId: 'thread' });
        await client.run({ runId: 'run' });
        let fail = true;
        const calls: string[] = [];
        const tool = { tool: { name: 'local', description: '', parameters: {} }, execute: (_args: unknown, call: any) => {
            calls.push(call.id);
            if (call.id === 'second' && fail) throw new Error('handler failed');
            return { content: [{ type: 'text' as const, text: call.id }], metadata: { client: true } };
        } };
        await expect(client.executeClientTools([tool])).rejects.toThrow('handler failed');
        expect(client.messages.filter(message => message.role === 'tool')).toHaveLength(1);
        fail = false;
        const results = await client.executeClientTools([tool]);
        expect(results[0]).toMatchObject({ toolCallId: 'second', content: [{ type: 'text', text: 'second' }], metadata: { client: true } });
        expect(calls).toEqual(['first', 'second', 'second']);
    });

    it('reports cancellation as distinct from success and permits a fresh recovered run after RUN_ERROR', async () => {
        const cancelled = await fixture([start, { ...finish, outcome: { type: 'cancelled' } }]);
        const client = new AgUiClient({ url: cancelled.url, threadId: 'thread' });
        const outcomes: string[] = [];
        await client.run({ runId: 'run' }, { onRunFinishedEvent: params => { outcomes.push(params.outcome); } });
        expect(outcomes).toEqual(['cancelled']);
        expect(await client.executeClientTools([])).toEqual([]);
        const error = await fixture([start, { type: 'RUN_ERROR', message: 'temporary', code: 'RETRY' }]);
        client.agent.url = error.url;
        await client.run({ runId: 'run' });
        const recovered = await fixture([start, { type: 'TEXT_MESSAGE_CHUNK', messageId: 'recovered', role: 'assistant', delta: 'recovered' }, finish]);
        client.agent.url = recovered.url;
        await client.run({ runId: 'run' });
        expect(client.messages.find(message => message.id === 'recovered')).toMatchObject({ content: 'recovered' });
    });
});

describe('nested frontend tool interrupt profile', () => {
    it('executes only the explicit versioned client-tool profile and resumes standard payloads', async () => {
        const endpoint = await fixture([start,
            { type: 'TOOL_CALL_CHUNK', toolCallId: 'leaf', toolCallName: 'browser', parentMessageId: 'child-message', delta: '{"q":"lookup"}' },
            { ...finish, outcome: { type: 'interrupt', interrupts: [
                { id: 'leaf-input', reason: 'agently.client_tool', toolCallId: 'leaf', metadata: { agently: { version: '1', kind: 'client-tool' } } },
                { id: 'approval', reason: 'approval', toolCallId: 'leaf' },
            ] } },
        ]);
        const client = new AgUiClient({ url: endpoint.url, threadId: 'thread' });
        await client.run({ runId: 'run' });
        let executions = 0;
        const handlers = [{ tool: { name: 'browser', description: '', parameters: {} }, execute: (args: unknown) => {
            executions++; expect(args).toEqual({ q: 'lookup' });
            return { content: [{ type: 'text' as const, text: 'found' }], error: 'partial', metadata: { proof: true } };
        } }];
        const responses = await client.executeClientToolInterrupts(handlers);
        expect(responses).toEqual({ 'leaf-input': { status: 'resolved', payload: { content: [{ type: 'text', text: 'found' }], error: 'partial' }, metadata: { proof: true } } });
        expect(await client.executeClientToolInterrupts(handlers)).toEqual(responses);
        expect(executions).toBe(1);
        expect(client.messages.filter(message => message.role === 'tool')).toEqual([]);
        expect(() => client.resume(responses)).toThrow('missing responses');
        const success = await fixture([start, finish]);
        client.agent.url = success.url;
        await client.resume({ ...responses, approval: { status: 'cancelled' } }, { runId: 'run' });
        expect(success.request().resume).toEqual([
            { interruptId: 'leaf-input', ...responses['leaf-input'] },
            { interruptId: 'approval', status: 'cancelled' },
        ]);
    });
    it('does not execute a mismatched or unknown interrupt profile', async () => {
        const endpoint = await fixture([start, { ...finish, outcome: { type: 'interrupt', interrupts: [
            { id: 'approval', reason: 'approval', toolCallId: 'leaf', metadata: { agently: { version: '1', kind: 'client-tool' } } },
            { id: 'future', reason: 'agently.client_tool', toolCallId: 'leaf', metadata: { agently: { version: '2', kind: 'client-tool' } } },
        ] } }]);
        const client = new AgUiClient({ url: endpoint.url, threadId: 'thread' });
        await client.run({ runId: 'run' });
        expect(await client.executeClientToolInterrupts([])).toEqual({});
    });
});

describe('explicit replay and backend commands', () => {
    it('reposts identical original input after drop and rebuilds full history without duplicated deltas', async () => {
        const bodies: string[] = [];
        const server = createServer(async (req,res) => {
            const chunks: Buffer[]=[];for await(const chunk of req) chunks.push(Buffer.from(chunk));bodies.push(Buffer.concat(chunks).toString());
            res.writeHead(200,{'Content-Type':'text/event-stream'});
            for(const event of [start,{type:'TEXT_MESSAGE_START',messageId:'answer',role:'assistant'},{type:'TEXT_MESSAGE_CONTENT',messageId:'answer',delta:bodies.length===1?'partial':'complete'}]) res.write(`data: ${JSON.stringify(event)}\n\n`);
            if(bodies.length===1){await new Promise(resolve=>setTimeout(resolve,30));res.destroy();return;}
            res.end(`data: ${JSON.stringify({type:'TEXT_MESSAGE_END',messageId:'answer'})}\n\ndata: ${JSON.stringify(finish)}\n\n`);
        });servers.push(server);await new Promise<void>(resolve=>server.listen(0,'127.0.0.1',resolve));const address=server.address();if(!address||typeof address==='string')throw new Error('fixture address');
        const client=new AgUiClient({url:`http://127.0.0.1:${address.port}/run`,threadId:'thread',initialState:{opaque:['keep']}});
        client.addMessage({id:'user',role:'user',content:[{type:'text',text:'input'},{type:'image',source:{type:'url',value:'https://example.test/image.png'}}],encryptedValue:'opaque-input'});
        await expect(client.run({runId:'run',context:[{description:'keep',value:'context'}],forwardedProps:{external:{opaque:true}}})).rejects.toThrow();
        const original=client.lastPostedInput!;expect(original.messages).toHaveLength(1);original.messages.length=0;expect(client.lastPostedInput!.messages).toHaveLength(1);
        await client.reconnectFull();expect(bodies).toHaveLength(2);expect(bodies[1]).toBe(bodies[0]);expect(client.messages.filter(message=>message.id==='answer')).toEqual([{id:'answer',role:'assistant',content:'complete'}]);
    });
    it('backend cancellation is a separate versioned command on the configured endpoint',async()=>{
        const endpoint=await fixture([start,{type:'CUSTOM',name:'agently.run.cancel',value:{version:'1',cancelled:true}},finish]);
        const client=new AgUiClient({url:endpoint.url,threadId:'thread'});
        await client.cancelRun('target',{runId:'run'});
        expect((endpoint.request() as any).forwardedProps.agently).toEqual({version:'1',operation:'run.cancel',requestId:'run',payload:{runId:'target'}});
        expect(()=>new AgUiClient({url:endpoint.url}).reconnectFull()).toThrow('No original POST');
    });
});

it('sends cancellation while the original stream is active without replacing its protocol store',async()=>{
    let targetResponse: import('node:http').ServerResponse|undefined;
    let started!:()=>void;const opened=new Promise<void>(resolve=>{started=resolve;});const requests:any[]=[];
    const server=createServer(async(req,res)=>{
        const chunks:Buffer[]=[];for await(const chunk of req)chunks.push(Buffer.from(chunk));const input=JSON.parse(Buffer.concat(chunks).toString());requests.push(input);res.writeHead(200,{'Content-Type':'text/event-stream'});
        res.write(`data: ${JSON.stringify({type:'RUN_STARTED',threadId:'thread',runId:input.runId})}\n\n`);
        if(input.forwardedProps?.agently?.operation==='run.cancel'){
            res.end(`data: ${JSON.stringify({type:'CUSTOM',name:'agently.run.cancel',value:{version:'1',cancelled:true}})}\n\ndata: ${JSON.stringify({type:'RUN_FINISHED',threadId:'thread',runId:input.runId})}\n\n`);
            targetResponse!.end(`data: ${JSON.stringify({type:'RUN_FINISHED',threadId:'thread',runId:'target',outcome:{type:'cancelled'}})}\n\n`);
        }else{targetResponse=res;started();}
    });servers.push(server);await new Promise<void>(resolve=>server.listen(0,'127.0.0.1',resolve));const address=server.address();if(!address||typeof address==='string')throw new Error('fixture address');
    const client=new AgUiClient({url:`http://127.0.0.1:${address.port}/run`,threadId:'thread'});const original=client.run({runId:'target'});await opened;
    await client.cancelRun('target',{runId:'cancel-command'});await original;
    expect(requests.map(input=>input.runId)).toEqual(['target','cancel-command']);expect(client.lastPostedInput!.runId).toBe('target');expect(client.pendingInterrupts).toEqual([]);
});

it('validates explicit resume identity and application response hooks before another POST',async()=>{
    const endpoint=await fixture([start,{...finish,outcome:{type:'interrupt',interrupts:[{id:'ask',reason:'input',responseSchema:{type:'string'}}]}}]);
    const client=new AgUiClient({url:endpoint.url,threadId:'thread'});await client.run({runId:'run'});
    expect(()=>client.resume({ask:{status:'cancelled'},unknown:{status:'cancelled'}},{runId:'next'})).toThrow(/unknown interrupt/i);
    expect(()=>client.resume({ask:{status:'resolved',payload:42}},{runId:'next'},undefined,(payload,schema)=>{expect(schema).toEqual({type:'string'});if(typeof payload!=='string')throw new Error('application response invalid');})).toThrow('application response invalid');
    expect((endpoint.request() as any).runId).toBe('run');expect(client.pendingInterrupts).toHaveLength(1);
});
