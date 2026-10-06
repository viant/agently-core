import { describe, expect, it } from 'vitest';
import type { Message } from '@ag-ui/core';
import { readAgentlyPresentation, readAgentlyPresentationActivity, readAgentlyUsage } from './aguiPresentation';
const activity=(kind:string,content:Record<string,unknown>):Message=>({id:'activity',role:'activity',activityType:kind,content});
describe('versioned Agently presentation readers',()=>{
    it('returns only safe scalar metadata and preserves explicit zero/false',()=>{
        expect(readAgentlyPresentation({agently:{presentation:{version:'1',nativeTurnId:'turn',pageId:'page',iteration:0,inputTokens:0,latestPage:false,_meta:{secret:true},rawPatch:{secret:true}}}})).toEqual({version:'1',nativeTurnId:'turn',pageId:'page',iteration:0,inputTokens:0,latestPage:false});
        expect(readAgentlyPresentation({agently:{presentation:{version:'2',nativeTurnId:'turn'}}})).toBeUndefined();
        expect(readAgentlyPresentation({agently:{identityVersion:'1',nativeTurnId:'turn'}})).toEqual({version:'1',nativeTurnId:'turn'});
        expect(readAgentlyPresentation({agently:{presentation:{version:'1',pageId:{raw:'payload'}}}})).toBeUndefined();
    });
    it('keeps actual feed removal separate from stream completion and unknown activation',()=>{
        expect(readAgentlyPresentationActivity(activity('agently.feed',{version:'1',feedId:'feed',active:false,title:'Feed',itemCount:0}))).toMatchObject({kind:'agently.feed',active:false,activationKnown:true,itemCount:0});
        expect(readAgentlyPresentationActivity(activity('agently.feed',{version:'1',feed:{feedId:'feed',title:'Feed',data:{rows:[1]},ui:{type:'table'}},active:null,activationKnown:false}))).toMatchObject({kind:'agently.feed',active:undefined,activationKnown:false,data:{rows:[1]},ui:{type:'table'}});
        expect(readAgentlyPresentationActivity(activity('agently.feed',{version:'1',feed:{feedId:'feed',data:{rows:[1]}}}))).toMatchObject({activationKnown:false,active:undefined});
    });
    it('validates optional feed provenance without promoting original facts over the reduced view',()=>{
        const fact={version:'1',feedId:'feed',active:false,activationAt:'2026-10-04T12:00:00Z',activationSource:{runId:'original',sequence:2}};
        const content={version:'1',feed:{feedId:'feed'},active:null,activationKnown:false,activationFact:fact};
        expect(readAgentlyPresentationActivity(activity('agently.feed',content))).toMatchObject({activationKnown:false,active:undefined});
        expect(readAgentlyPresentationActivity(activity('agently.feed',{...content,active:false,activationKnown:true,activationAt:fact.activationAt,activationSource:fact.activationSource}))).toMatchObject({active:false,activationAt:fact.activationAt,activationSource:fact.activationSource});
        for(const origin of [{runId:'',sequence:2},{runId:'run',sequence:0},{runId:'run',sequence:1.5},{runId:'run',sequence:9007199254740992},{runId:'run',sequence:2,threadId:'foreign'}])expect(readAgentlyPresentationActivity(activity('agently.feed',{...content,activationSource:origin}))).toBeUndefined();
        expect(readAgentlyPresentationActivity(activity('agently.feed',{...content,activationAt:'today'}))).toBeUndefined();
        expect(readAgentlyPresentationActivity(activity('agently.feed',{...content,activationFact:{...fact,active:'false'}}))).toBeUndefined();
        expect(readAgentlyPresentationActivity(activity('agently.feed',{...content,activationFact:{...fact,feedId:'foreign'}}))).toBeUndefined();
    });
    it('preserves planner false, queued identities and planned descriptors without promoting calls',()=>{
        expect(readAgentlyPresentationActivity(activity('agently.planner',{version:'1',status:'validated',attempt:0,trigger:'',staticProfile:'',strategyFamily:'',secondPolicy:'',validated:false,outputPayloadId:'reference',rawOutput:{private:true}}))).toMatchObject({validated:false,attempt:0,outputPayloadId:'reference'});
        expect(readAgentlyPresentationActivity(activity('agently.turn',{version:'1',nativeTurnId:'turn',status:'queued',queueSequence:'9007199254740993',goalId:'goal'}))).toMatchObject({status:'queued',queueSequence:'9007199254740993',goalId:'goal'});
        expect(readAgentlyPresentationActivity(activity('agently.tools-planned',{version:'1',calls:[{toolCallId:'scoped',toolName:'tool',arguments:{private:true}}]}))).toMatchObject({calls:[{toolCallId:'scoped',toolName:'tool'}]});
        expect(readAgentlyPresentationActivity(activity('agently.user-identity',{version:'1',protocolRunId:'run',nativeTurnId:'turn',clientMessageId:'client',clientRequestId:'client',nativeUserMessageId:'native-user'}))).toMatchObject({kind:'agently.user-identity',clientMessageId:'client',nativeUserMessageId:'native-user'});
    });
    it('does not consume unknown/custom host receipt activities',()=>{
        expect(readAgentlyPresentationActivity(activity('mcp-apps',{version:'1',result:{_meta:{host:true}}}))).toBeUndefined();
        expect(readAgentlyPresentationActivity(activity('agently.narration',{version:'2',text:'unknown'}))).toBeUndefined();
        expect(readAgentlyPresentationActivity({id:'text',role:'assistant',content:'plain'})).toBeUndefined();
    });
    it('reads effect timing independently of model tool request completion',()=>{
        expect(readAgentlyPresentationActivity(activity('agently.tool',{version:'1',toolCallId:'scoped',phase:'execution',status:'running',toolMessageId:'native',startedAt:'2026-10-03T12:00:00Z'}))).toMatchObject({kind:'agently.tool',status:'running',startedAt:'2026-10-03T12:00:00Z'});
        const waiting=readAgentlyPresentationActivity(activity('agently.tool',{version:'1',toolCallId:'scoped',phase:'waiting',status:'waiting_for_user'}));
        expect(waiting).toMatchObject({phase:'waiting'});
        expect(waiting).not.toHaveProperty('startedAt');
    });
    it('reads scoped absolute usage without carrying unknown host fields',()=>{
        expect(readAgentlyUsage({version:'1',scope:'conversation',nativeTurnId:'turn',usage:{inputTokens:0,outputTokens:0,totalTokens:0,_meta:{private:true}}})).toEqual({version:'1',scope:'conversation',nativeTurnId:'turn',usage:{inputTokens:0,outputTokens:0,totalTokens:0}});
        expect(readAgentlyUsage({version:'1',modelCallId:'model',usage:{inputTokens:1,outputTokens:2}})).toMatchObject({scope:'model_call',modelCallId:'model'});
        expect(readAgentlyUsage({version:'1',scope:'unknown',usage:{}})).toBeUndefined();
    });
});
