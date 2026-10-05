import { describe, expect, it, vi } from 'vitest';
import { AgentlyClient } from '../client';
describe('native steering request identity',()=>{
    it('forwards the exact optimistic identity and retains legacy calls without it',async()=>{
        const fetchImpl=vi.fn(async(_url:RequestInfo|URL,_init?:RequestInit)=>new Response(JSON.stringify({messageId:'native-message',status:'accepted'}),{status:202,headers:{'Content-Type':'application/json'}}));
        const client=new AgentlyClient({baseURL:'/v1',fetchImpl});
        await client.steerTurn('conversation','turn',{content:'Same prompt',clientRequestId:'exact-id'});
        await client.steerTurn('conversation','turn',{content:'Same prompt'});
        expect(JSON.parse(String(fetchImpl.mock.calls[0][1]?.body))).toEqual({content:'Same prompt',role:'user',clientRequestId:'exact-id'});
        expect(JSON.parse(String(fetchImpl.mock.calls[1][1]?.body))).toEqual({content:'Same prompt',role:'user'});
    });
});
