import { describe, expect, it, vi } from 'vitest';
import { AgentlyClient } from './client';
import { HttpError } from './errors';
import { readToolExecuteStream } from './toolExecuteStream';
function stream(parts: string[]) {
 const encoder = new TextEncoder();
 return new Response(new ReadableStream({start(controller) {for (const part of parts) controller.enqueue(encoder.encode(part));controller.close();}}), {headers:{'Content-Type':'text/event-stream'}});
}
describe('single execution progress transport', () => {
 it('parses split frames, CRLF and UTF-8 and returns the exact result', async () => {
  const progress=vi.fn();
  const response=stream(['event: pro','gress\r\ndata: {"label":"Sample α","completed":1}\r\n\r','\nevent: result\ndata: {"result":"{\\"ok\\":true}"}\n\n']);
  expect(await readToolExecuteStream(response,progress)).toBe('{"ok":true}');
  expect(progress).toHaveBeenCalledTimes(1);expect(progress).toHaveBeenCalledWith({label:'Sample α',completed:1});
 });
 it('fails on truncation and bounds producer progress metadata',async()=>{
  await expect(readToolExecuteStream(stream(['event: progress\ndata: {}\n\n']),vi.fn())).rejects.toThrow('before a result');
  await expect(readToolExecuteStream(stream([`event: progress\ndata: ${JSON.stringify({label:'x'.repeat(17000)})}\n\n`]),vi.fn())).rejects.toThrow('exceeds limit');
 });
 it('preserves error envelopes without releasing a result',async()=>{
  try {await readToolExecuteStream(stream(['event: error\ndata: {"status":403,"error":"permission denied"}\n\n']),vi.fn());throw new Error('expected failure');}
  catch(error) {expect(error).toBeInstanceOf(HttpError);expect((error as HttpError).status).toBe(403);expect(JSON.parse((error as HttpError).body)).toEqual({error:'permission denied'});}
 });
 it('sends auth/cookies and conversation scope once without replay on truncation',async()=>{
  const fetchImpl=vi.fn().mockResolvedValue(stream(['event: progress\ndata: {"total":2}\n\n']));
  const onError=vi.fn(), onProgress=vi.fn();
  const client=new AgentlyClient({baseURL:'https://example.test/v1',fetchImpl,tokenProvider:()=> 'sample-token',useCookies:true,retries:4,onError});
  await expect(client.executeTool('sample:read',{}, {conversationId:'sample conversation',onProgress})).rejects.toThrow('before a result');
  expect(fetchImpl).toHaveBeenCalledTimes(1);
  const [url, options]=fetchImpl.mock.calls[0];
  expect(url).toContain('conversationId=sample+conversation');
  expect(options.headers.Authorization).toBe('Bearer sample-token');expect(options.headers.Accept).toBe('text/event-stream');expect(options.credentials).toBe('include');
  expect(onProgress).toHaveBeenCalledTimes(1);expect(onProgress).toHaveBeenCalledWith({total:2});expect(onError).toHaveBeenCalledTimes(1);
 });
 it('supports JSON responses from older servers and existing JSON executions',async()=>{
  const fetchImpl=vi.fn().mockImplementation(()=>Promise.resolve(new Response(JSON.stringify({result:'done'}),{headers:{'Content-Type':'application/json'}})));
  const client=new AgentlyClient({baseURL:'https://example.test/v1',fetchImpl});
  expect(await client.executeTool('sample',{}, {onProgress:vi.fn()})).toBe('done');expect(await client.executeTool('sample',{})).toBe('done');
  expect(fetchImpl.mock.calls[1][1].headers.Accept).toBe('application/json');
 });
 it('dispatches streamed and initial HTTP authentication errors through the same hook',async()=>{
  for(const response of [stream(['event: error\ndata: {"status":401,"error":"authentication required"}\n\n']),new Response('{"error":"authentication required"}',{status:401})]) {
   const unauthorized=vi.fn(),fetchImpl=vi.fn().mockResolvedValue(response);
   const client=new AgentlyClient({baseURL:'https://example.test/v1',fetchImpl,onUnauthorized:unauthorized});
   await expect(client.executeTool('sample',{}, {onProgress:vi.fn()})).rejects.toMatchObject({status:401});
   expect(unauthorized).toHaveBeenCalledTimes(1);expect(fetchImpl).toHaveBeenCalledTimes(1);
  }
 });
});

it('delivers authorized intermediate updates separately and waits for terminal result',async()=>{
 const update=vi.fn(),progress=vi.fn();
 const response=stream(['event: progress\ndata: {"total":2}\n\nevent: update\ndata: {"kind":"sampleReady","rows":[{"value":7}]}\n\nevent: result\ndata: {"result":"complete"}\n\n']);
 const fetchImpl=vi.fn().mockResolvedValue(response),client=new AgentlyClient({baseURL:'https://example.test/v1',fetchImpl});
 expect(await client.executeTool('sample',{}, {onProgress:progress,onUpdate:update})).toBe('complete');
 expect(fetchImpl).toHaveBeenCalledTimes(1);
 expect(fetchImpl.mock.calls[0][1].headers['X-Agently-Tool-Updates']).toBe('true');
 expect(update).toHaveBeenCalledWith({kind:'sampleReady',rows:[{value:7}]});
 expect(progress).toHaveBeenCalledTimes(1);expect(progress).toHaveBeenCalledWith({total:2});
});
it('supports update-only consumers and fails validation or truncation without replay',async()=>{
 for(const rejectUpdate of [false,true]) {
  const response=stream(['event: update\ndata: {"kind":"sampleReady"}\n\n']);
  const update=vi.fn(()=>{if(rejectUpdate)throw new Error('sample validation failed');});
  const fetchImpl=vi.fn().mockResolvedValue(response),client=new AgentlyClient({baseURL:'https://example.test/v1',fetchImpl,retries:4});
  await expect(client.executeTool('sample',{}, {onUpdate:update})).rejects.toThrow(rejectUpdate?'sample validation failed':'before a result');
  expect(update).toHaveBeenCalledTimes(1);expect(fetchImpl).toHaveBeenCalledTimes(1);
 }
});
