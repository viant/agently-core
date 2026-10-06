import Foundation
import XCTest
@testable import AgentlySDK

private actor AgUiActionCounter {var count=0;func increment(){count+=1};func next()->Int{count+=1;return count}}
final class AgUiActionsTests: XCTestCase {
    private func value(_ raw: String)throws->AgUiValue {try AgUiValue.parse(raw)}
    private func snapshot(outcome: String = #"{"type":"success","pendingToolCallIds":["call"]}"#, extraCall:Bool=false)throws->AgUiSnapshot {
        var fields=try value(#"{"id":"owner","role":"assistant","toolCalls":[{"id":"call","type":"function","function":{"name":"browser","arguments":"{\"n\":9007199254740993}"}}]}"#).object!
        if extraCall {var calls=fields["toolCalls"]!.array!;calls.append(try value(#"{"id":"second","type":"function","function":{"name":"unstable","arguments":"{}"}}"#));fields["toolCalls"] = .array(calls)}
        let message=try AgUiMessage(value:fields)
        let input=try AgUiRunInput(threadId:"thread",runId:"run",messages:[message])
        let store=try AgUiStore(input:input)
        _=try store.receive(AgUiEvent(value:value(#"{"type":"RUN_STARTED","threadId":"thread","runId":"run"}"#).object!))
        _=try store.receive(AgUiEvent(value:value("{\"type\":\"RUN_FINISHED\",\"threadId\":\"thread\",\"runId\":\"run\",\"outcome\":"+outcome+"}").object!))
        return store.snapshot
    }
    func testAuthorizedClientToolDispatchCachesMediaResultAndBuildsContinuation()async throws {
        let snapshot=try snapshot(),counter=AgUiActionCounter(),dispatcher=AgUiClientToolDispatcher()
        let tool=try AgUiClientTool(definition:value(#"{"name":"browser","description":"authorized","parameters":{"type":"object","properties":{"n":{"type":"integer"}},"required":["n"],"additionalProperties":false}}"#)){args,call in
            XCTAssertEqual(args["n"],.number("9007199254740993"));XCTAssertEqual(call["id"],.string("call"));await counter.increment()
            return AgUiClientToolResult(content:.array([.object(["type":.string("text"),"text":.string("result")])]),metadata:.object(["opaque":.string("kept")]))
        }
        let first=try await dispatcher.executeClientTools(snapshot:snapshot,tools:[tool]);let again=try await dispatcher.executeClientTools(snapshot:snapshot,tools:[tool]);XCTAssertEqual(first,again);let count=await counter.count;XCTAssertEqual(count,1)
        let next=try snapshot.toolResultsInput(runId:"continued",results:again);XCTAssertEqual(next.value["messages"]?.array?.last?["metadata"]?["opaque"],.string("kept"));XCTAssertEqual(next.value["messages"]?.array?.last?["content"],first[0].value["content"])
        do {_=try await dispatcher.executeClientTools(snapshot:snapshot,tools:[]);XCTFail("missing handler accepted")}catch{}
    }
    func testPartialBatchRetryDoesNotRepeatSuccessfulHandler()async throws {
        let snapshot=try snapshot(outcome:#"{"type":"success","pendingToolCallIds":["call","second"]}"#,extraCall:true),first=AgUiActionCounter(),second=AgUiActionCounter(),dispatcher=AgUiClientToolDispatcher()
        let toolA=try AgUiClientTool(definition:value(#"{"name":"browser","description":"authorized","parameters":{"type":"object"}}"#)){_,_ in await first.increment();return AgUiClientToolResult(content:.string("first"))}
        let toolB=try AgUiClientTool(definition:value(#"{"name":"unstable","description":"authorized","parameters":{"type":"object"}}"#)){_,_ in if await second.next()==1 {throw NSError(domain:"fixture-handler",code:1)};return AgUiClientToolResult(content:.string("second"))}
        do{_=try await dispatcher.executeClientTools(snapshot:snapshot,tools:[toolA,toolB]);XCTFail("Handler failure accepted")}catch{}
        let results=try await dispatcher.executeClientTools(snapshot:snapshot,tools:[toolA,toolB]);let a=await first.count,b=await second.count;XCTAssertEqual(a,1);XCTAssertEqual(b,2);XCTAssertEqual(results.count,2)
        XCTAssertEqual(try snapshot.toolResultsInput(runId:"next",results:results).value["messages"]?.array?.count,3)
    }
    func testResumeIdentitySchemaAndCommandCancellationAreExplicit()throws {
        let snapshot=try snapshot(outcome:#"{"type":"interrupt","interrupts":[{"id":"ask","reason":"input","responseSchema":{"type":"object","properties":{"answer":{"type":"string","enum":["yes"]}},"required":["answer"],"additionalProperties":false}}]}"#)
        XCTAssertTrue(snapshot.pendingToolCallIds.isEmpty)
        let valid=try value(#"{"interruptId":"ask","status":"resolved","payload":{"answer":"yes"}}"#)
        XCTAssertNoThrow(try snapshot.nextInput(runId:"next",resume:[valid]))
        XCTAssertThrowsError(try snapshot.nextInput(runId:"next",resume:[valid,valid]))
        XCTAssertThrowsError(try snapshot.nextInput(runId:"next",resume:[value(#"{"interruptId":"other","status":"cancelled"}"#)]))
        XCTAssertThrowsError(try snapshot.nextInput(runId:"next",resume:[value(#"{"interruptId":"ask","status":"resolved","payload":{"answer":"no"}}"#)]))
        XCTAssertThrowsError(try AgUiSchema.validateResponse(.string("tiny"),schema:value(#"{"type":"string","minLength":10}"#)))
        XCTAssertNoThrow(try AgUiSchema.validateResponse(.string("yes"),schema:value(##"{"$defs":{"Choice":{"enum":["yes"]}},"$ref":"#/$defs/Choice"}"##)))
        XCTAssertThrowsError(try AgUiSchema.validateResponse(.string("yes"),schema:value(#"{"anyOf":[{"$ref":"https://invalid.test/schema"},{"type":"string"}]}"#)))
        XCTAssertNoThrow(try AgUiSchema.validateResponse(.string("🌍a"),schema:value(#"{"type":"string","minLength":2,"maxLength":2}"#)))
        let command=try AgentlyAgUiExtensions.cancelRun(targetRunId:"target",requestId:"cancel-command")
        XCTAssertEqual(command["agently"]?["operation"],.string("run.cancel"));XCTAssertEqual(command["agently"]?["payload"]?["runId"],.string("target"))
        XCTAssertTrue(try self.snapshot(outcome:#"{"type":"cancelled"}"#).pendingToolCallIds.isEmpty)
    }
    func testVersionedInterruptToolExecutionLeavesHumanApprovalForCaller()async throws {
        let snapshot=try snapshot(outcome:#"{"type":"interrupt","interrupts":[{"id":"nested","reason":"agently.client_tool","toolCallId":"call","responseSchema":{"type":"object","properties":{"content":{"anyOf":[{"type":"string"},{"type":"array","items":{"type":"object"}}]},"error":{"type":"string"}},"required":["content"],"additionalProperties":false},"metadata":{"agently":{"version":"1","kind":"client-tool"}}},{"id":"approval","reason":"approval"}]}"#)
        let counter=AgUiActionCounter(),dispatcher=AgUiClientToolDispatcher()
        let tool=try AgUiClientTool(definition:value(#"{"name":"browser","description":"authorized","parameters":{"type":"object"}}"#)){_,_ in await counter.increment();return AgUiClientToolResult(content:.string("browser result"))}
        let answers=try await dispatcher.executeClientToolInterrupts(snapshot:snapshot,tools:[tool]);XCTAssertEqual(answers.count,1);XCTAssertEqual(answers[0]["interruptId"],.string("nested"))
        XCTAssertThrowsError(try snapshot.nextInput(runId:"next",resume:answers))
        XCTAssertNoThrow(try snapshot.nextInput(runId:"next",resume:answers+[value(#"{"interruptId":"approval","status":"cancelled"}"#)]))
        _=try await dispatcher.executeClientToolInterrupts(snapshot:snapshot,tools:[tool]);let count=await counter.count;XCTAssertEqual(count,1)
    }
}
