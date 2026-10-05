// Actual published middleware against a credential-free MCP server, not backend proof.
import assert from 'node:assert/strict';
import {randomUUID} from 'node:crypto';
import {MCPAppsMiddleware,getServerHash} from '@ag-ui/mcp-apps-middleware';
import {EventSchema} from '@ag-ui/core/schemas';
const config={type:'http',url:process.env.MCP_APP_URL??'http://127.0.0.1:18243/mcp',serverId:'fixture'};
const middleware=new MCPAppsMiddleware({mcpServers:[config]});
const hash=getServerHash(config);
for(const instance of ['first','second'])for(const method of ['resources/read','tools/call']) {
 const runId=randomUUID();const input={runId,threadId:'original-conversation',forwardedProps:{__proxiedMCPRequest:{serverId:'fixture',serverHash:hash,method,params:method==='resources/read'?{uri:'ui://fixture/app.html'}:{name:'fixture_view',arguments:{instance}}}}};
 const events=[];await new Promise((resolve,reject)=>middleware.run(input).subscribe({next:e=>events.push(e),error:reject,complete:resolve}));
 assert.equal(events[0].threadId,runId);assert.equal(events.at(-1).threadId,runId);events.forEach(e=>EventSchema.parse(e));
 const result=events.at(-1).result;
 if(method==='tools/call'){assert.equal(result._meta['fixture/private'].instance,instance);assert.deepEqual(result.structuredContent,{instance,nested:{values:[1,false,null]}});assert.equal(result.content[1].type,'image');assert.equal(result.content[2].type,'resource_link');assert.equal(result.isError,false);}
 else{assert.equal(result._meta['fixture/read'],'host-only');assert.deepEqual(result.contents[0]._meta['fixture/resource'],{preserve:true});}
 console.log(JSON.stringify({scope:'official-middleware-to-fixture',instance,method,runId,threadId:events.at(-1).threadId,result}));
}
console.log('Official middleware contract passed; backend authorization has not been tested by this script.');
