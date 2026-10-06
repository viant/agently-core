// Regenerates expected semantics with the pinned @ag-ui/client 1.0.1, not a copied oracle.
import { createRequire } from 'node:module';
import { fileURLToPath } from 'node:url';
import { readFileSync, writeFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
const require=createRequire(new URL('../../../../sdk/ts/package.json',import.meta.url));
const {transformChunks,verifyEvents,defaultApplyEvents,PROTOCOL_VERSION}=require('@ag-ui/client');
const manifest=JSON.parse(readFileSync(resolve(dirname(require.resolve('@ag-ui/client')),'../package.json'),'utf8'));
if(manifest.version!=='1.0.1'||PROTOCOL_VERSION!=='1.0')throw new Error('Fixture oracle must be pinned AG-UI client 1.0.1 / protocol 1.0');
const {from,lastValueFrom,toArray}=require('rxjs');
const path=fileURLToPath(new URL('../../testdata/reducer-semantics.json',import.meta.url));
const cases=JSON.parse(readFileSync(path,'utf8'));
for(const fixture of cases) {
 let normalized=[];
 try {
  normalized=await lastValueFrom(from(structuredClone(fixture.events)).pipe(transformChunks(),verifyEvents(),toArray()));
  fixture.accepted=true;
  fixture.normalized=normalized;
  const input={threadId:'t',runId:'r',state:fixture.initialState??{},messages:fixture.initialMessages??[],tools:[],context:[],forwardedProps:{}};
  const agent={messages:input.messages,pendingInterrupts:[]};
  const changes=await lastValueFrom(defaultApplyEvents(input,from(structuredClone(normalized)),agent,[]).pipe(toArray()));
  let messages=input.messages,state=input.state;
  for(const change of changes){if(change.messages!==undefined)messages=change.messages;if(change.state!==undefined)state=change.state;}
  fixture.expectedMessages=messages;fixture.expectedState=state;
 } catch(error) {fixture.accepted=false;fixture.error=String(error.message);delete fixture.normalized;delete fixture.expectedMessages;delete fixture.expectedState;}
}
writeFileSync(path,JSON.stringify(cases,null,2)+'\n');
