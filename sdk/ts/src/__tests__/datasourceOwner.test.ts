import {expect,it,vi} from 'vitest';
import {AgentlyClient} from '../client';

it('carries a logical report owner through the existing datasource fetch endpoint',async()=>{
 const request=vi.fn(async(_url:RequestInfo|URL,_init?:RequestInit)=>new Response(JSON.stringify({rows:[{value:0,label:'Zero'}]}),{headers:{'Content-Type':'application/json'}}));
 const client=new AgentlyClient({baseURL:'http://example.invalid/v1',fetchImpl:request,useCookies:true,timeoutMs:0});
 const result=await client.fetchDatasource({id:'options',owner:{uri:'report://example/summary'},inputs:{scope:0}});
 expect(result.rows).toEqual([{value:0,label:'Zero'}]);
 expect(request).toHaveBeenCalledOnce();
 expect(String(request.mock.calls[0][0])).toBe('http://example.invalid/v1/api/datasources/options/fetch');
 expect(JSON.parse(String(request.mock.calls[0][1]?.body))).toEqual({owner:{uri:'report://example/summary'},inputs:{scope:0}});
});
