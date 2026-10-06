import Foundation
import XCTest
@testable import AgentlySDK

final class AgUiTests: XCTestCase {
    private func json(_ text: String) throws -> AgUiValue { try AgUiValue.parse(text) }
    private func fixture() throws -> AgUiValue {
        let url = try XCTUnwrap(Bundle.module.url(forResource: "agui-conformance", withExtension: "json"))
        return try AgUiValue.parse(Data(contentsOf: url))
    }
    private func input() throws -> AgUiRunInput { try AgUiRunInput(value: fixture()["input"]!.object!) }
    private func event(_ text: String) throws -> AgUiEvent { try AgUiEvent(value: json(text).object!) }
    private func start() throws -> AgUiEvent { try event(#"{"type":"RUN_STARTED","threadId":"thread","runId":"run"}"#) }

    func testAll31VariantsMatchOfficialHttpAgentReference() throws {
        let f = try fixture(), store = try AgUiStore(input: input()); var types: [AgUiValue] = []
        for raw in f["events"]!.array! {
            try AgUiSchema.validate(raw, definition: "Event")
            types += try store.receive(AgUiEvent(value: raw.object!)).map { .string($0.type) }
        }
        _ = try store.finish()
        XCTAssertEqual(f["expectedNormalizedTypes"], .array(types))
        XCTAssertEqual(store.messages.map { $0.id }, f["expectedMessages"]!.array!.map { $0["id"]!.string! })
        for expected in f["expectedMessages"]!.array! { let id = expected["id"]!.string!; XCTAssertEqual(try store.messages.first { $0.id == id }.map { try AgUiValue.object($0.value).jsonString() }, try expected.jsonString(), "Message \(id)") }
        XCTAssertEqual(f["expectedState"], store.state)
        XCTAssertEqual(AgUiEventType.allCases.count, 31)
        let allTypes = Set((f["events"]!.array! + f["errorEvents"]!.array!).map { $0["type"]!.string! })
        XCTAssertEqual(allTypes.count, 31); XCTAssertNotNil(store.capabilities); XCTAssertEqual(store.subagents.count, 2)
        XCTAssertEqual(store.snapshot.subagents["s1"]?.started.value.string("name"), "Worker")
        XCTAssertEqual(store.snapshot.subagents["s2"]?.terminal?.type, "SUBAGENT_ERROR")
        let next = try store.nextInput(runId: "next", forwardedProps: AgentlyAgUiExtensions.forwardedProps(operation: "chat", agentId: "agent", model: "model"))
        XCTAssertEqual(next.value["messages"], .array(store.messages.filter { $0.role != "activity" }.map { .object($0.value) }))
        XCTAssertEqual(try store.nextInput(runId: "explicit", includeActivityMessages: true).value["messages"], .array(store.messages.map { .object($0.value) })); XCTAssertEqual(next.value["state"], store.state)
    }
    func testInterruptResumeCancellationAndRunError() throws {
        let f = try fixture(), store = try AgUiStore(input: input())
        for raw in f["interruptEvents"]!.array! { _ = try store.receive(AgUiEvent(value: raw.object!)) }
        _ = try store.finish(); XCTAssertEqual(store.pendingInterrupts.first?["id"]?.string, "approval")
        let resume = try json(#"[{"interruptId":"approval","status":"resolved","payload":{"editedArgs":{"id":42}},"metadata":{"signed":"opaque"}}]"#).array!
        XCTAssertEqual(try store.nextInput(runId: "resumed", resume: resume).value["resume"], .array(resume))
        let error = try AgUiStore(input: input()); _ = try error.receive(AgUiEvent(value: f["errorEvents"]!.array![0].object!)); _ = try error.finish()
        XCTAssertEqual(error.terminalEvent?.value.string("code"), "DENIED")
        let cancelled = try AgUiStore(input: input()); _ = try cancelled.receive(start()); _ = try cancelled.receive(event(#"{"type":"RUN_FINISHED","threadId":"thread","runId":"run","outcome":{"type":"cancelled","reason":"user","interruptIds":["approval"]}}"#)); _ = try cancelled.finish()
        XCTAssertEqual(cancelled.terminalEvent?.value["outcome"]?["type"]?.string, "cancelled")
    }
    func testSchemaValidationAndExactOpaqueNumbers() throws {
        XCTAssertThrowsError(try event(#"{"type":"TEXT_MESSAGE_CONTENT","messageId":"m","delta":null}"#))
        XCTAssertThrowsError(try event(#"{"type":"SUBAGENT_STARTED","subagentRunId":null,"name":"child"}"#))
        XCTAssertThrowsError(try AgUiSchema.validate(json(#"{"type":"RUN_ERROR","message":"bad","extra":1}"#), definition: "Event"))
        let future = try event(#"{"type":"RUN_ERROR","message":"bad","future":{"n":90071992547409931234567890}}"#)
        XCTAssertEqual(future.value["future"]?["n"], .number("90071992547409931234567890"))
        let roundTrip = try json(AgUiValue.object(future.value).jsonString()); XCTAssertEqual(roundTrip["future"]?["n"], .number("90071992547409931234567890"))
        let store = try AgUiStore(input: input()); _ = try store.receive(start())
        let unknown = try event(#"{"type":"FUTURE_EVENT","opaque":[1,false]}"#)
        XCTAssertEqual(try store.receive(unknown), [unknown])
    }
    func testLifecycleOwnershipIdentityAndMissingTerminal() throws {
        func store() throws -> AgUiStore { let value = try AgUiStore(input: input()); _ = try value.receive(start()); return value }
        XCTAssertThrowsError(try store().receive(event(#"{"type":"TEXT_MESSAGE_CONTENT","messageId":"missing","delta":"bad"}"#)))
        XCTAssertThrowsError(try store().receive(event(#"{"type":"RUN_FINISHED","threadId":"wrong","runId":"run"}"#)))
        XCTAssertThrowsError(try store().finish())
        let attributed = try store(); _ = try attributed.receive(event(#"{"type":"TEXT_MESSAGE_START","messageId":"m"}"#))
        XCTAssertThrowsError(try attributed.receive(event(#"{"type":"TEXT_MESSAGE_CONTENT","messageId":"m","delta":"bad","subagentRunId":"child"}"#)))
        let step = try store(); _ = try step.receive(event(#"{"type":"STEP_STARTED","stepName":"same"}"#))
        XCTAssertThrowsError(try step.receive(event(#"{"type":"STEP_FINISHED","stepName":"same","subagentRunId":""}"#)))
        let reasoning = try store(); _ = try reasoning.receive(event(#"{"type":"REASONING_START","messageId":"r"}"#))
        XCTAssertThrowsError(try reasoning.receive(event(#"{"type":"RUN_FINISHED","threadId":"thread","runId":"run"}"#)))
    }
    func testChunkInterleavingAmbiguityAndOpenerAgreements() throws {
        let store = try AgUiStore(input: input()); _ = try store.receive(start())
        _ = try store.receive(event(#"{"type":"TEXT_MESSAGE_CHUNK","messageId":"a","delta":"A","subagentRunId":"s1"}"#))
        _ = try store.receive(event(#"{"type":"TEXT_MESSAGE_CHUNK","messageId":"b","delta":"B","subagentRunId":"s2"}"#))
        XCTAssertThrowsError(try store.receive(event(#"{"type":"TEXT_MESSAGE_CHUNK","delta":"ambiguous"}"#)))
        XCTAssertThrowsError(try store.receive(event(#"{"type":"TEXT_MESSAGE_CHUNK","messageId":"a","role":"user"}"#)))
        let good = try AgUiStore(input: input()); _ = try good.receive(start())
        _ = try good.receive(event(#"{"type":"TEXT_MESSAGE_CHUNK","messageId":"a","delta":"A","subagentRunId":"s1"}"#))
        _ = try good.receive(event(#"{"type":"TEXT_MESSAGE_CHUNK","messageId":"b","delta":"B","subagentRunId":"s2"}"#))
        _ = try good.receive(event(#"{"type":"TEXT_MESSAGE_CHUNK","messageId":"a","delta":"2"}"#))
        _ = try good.receive(event(#"{"type":"RUN_FINISHED","threadId":"thread","runId":"run"}"#)); _ = try good.finish()
        XCTAssertEqual(good.messages.first { $0.id == "a" }?.value.string("content"), "A2")
        XCTAssertEqual(good.messages.first { $0.id == "b" }?.value.string("content"), "B")
    }
    func testJsonPatchAtomicRootEscapesEqualityAndMoves() throws {
        let original = try json(#"{"a/b":{"~key":[1,2]},"large":90071992547409931234567890}"#)
        let patch = try json(#"[{"op":"test","path":"/large","value":90071992547409931234567890.0},{"op":"move","from":"/a~1b/~0key/0","path":"/a~1b/~0key/1"},{"op":"copy","from":"/large","path":"/copy"}]"#).array!
        let result = try AgUiJsonPatch.apply(original, operations: patch)
        XCTAssertEqual(result["a/b"]?["~key"], try json("[2,1]"))
        XCTAssertThrowsError(try AgUiJsonPatch.apply(original, operations: json(#"[{"op":"replace","path":"/large","value":0},{"op":"remove","path":"/missing"}]"#).array!))
        XCTAssertEqual(original["large"], .number("90071992547409931234567890"))
        XCTAssertEqual(try AgUiJsonPatch.apply(original, operations: json(#"[{"op":"replace","path":"","value":[1,false]}]"#).array!), try json("[1,false]"))
        XCTAssertThrowsError(try AgUiJsonPatch.apply(original, operations: json(#"[{"op":"move","from":"/a~1b","path":"/a~1b/child"}]"#).array!))
        XCTAssertThrowsError(try AgUiJsonPatch.apply(original, operations: json(#"[{"op":"remove","path":"/a~1b/~0key/01"}]"#).array!))
    }
    func testSSEByteSplitsMultilineBOMCRLFEofAndLimit() throws {
        let raw = "\u{FEFF}: heartbeat\r\n\r\ndata: {\r\ndata: \"type\":\"CUSTOM\",\"name\":\"fixture\",\"value\":\"héllo 🌍\"}\r\n\r\ndata: trailing"
        var parser = AgUiSSEParser(); var frames: [Data] = []
        for byte in raw.utf8 { frames += try parser.feed(Data([byte])) }
        parser.finish(); XCTAssertEqual(frames.count, 1)
        XCTAssertEqual(try AgUiValue.parse(frames[0])["value"], .string("héllo 🌍"))
        var limited = AgUiSSEParser(maxFrameBytes: 3); XCTAssertThrowsError(try limited.feed(Data("data: 1234".utf8)))
    }
    func testSnapshotReconciliationPreservesClientActivityReasoningAndOpaqueRestoredTools() throws {
        let store = try AgUiStore(input: input()); _ = try store.receive(start())
        for raw in [
            #"{"type":"ACTIVITY_SNAPSHOT","messageId":"local","activityType":"client","content":{"keep":true}}"#,
            #"{"type":"ACTIVITY_SNAPSHOT","messageId":"server","activityType":"progress","content":{"stale":true}}"#,
            #"{"type":"REASONING_MESSAGE_START","messageId":"local-reason","role":"reasoning"}"#,
            #"{"type":"REASONING_MESSAGE_CONTENT","messageId":"local-reason","delta":"keep rationale"}"#,
            #"{"type":"REASONING_MESSAGE_END","messageId":"local-reason"}"#
        ] { _ = try store.receive(event(raw)) }
        let user = try input().value["messages"]!.array![0].object!.with(["metadata": json(#"{"revision":{"number":2}}"#)])
        let saved = try json(#"{"id":"saved","role":"assistant","encryptedValue":"opaque-saved","subagentRunId":"saved-agent","toolCalls":[{"id":"saved-call","type":"function","function":{"name":"lookup","arguments":"{}"},"encryptedValue":"opaque-call"}]}"#)
        _ = try store.receive(AgUiEvent(value: ["type": .string("MESSAGES_SNAPSHOT"),"messages": .array([.object(user),saved]),"metadata": json(#"{"@ag-ui/client":{"authoritativeActivityTypes":["progress"]}}"#)]))
        XCTAssertEqual(store.messages.map { $0.id }, ["user","local","local-reason","saved"])
        XCTAssertEqual(store.messages.first?.value["content"], try input().value["messages"]!.array![0]["content"])
        _ = try store.receive(event(#"{"type":"TOOL_CALL_START","toolCallId":"saved-call","toolCallName":"lookup","parentMessageId":"saved"}"#))
        _ = try store.receive(event(#"{"type":"TOOL_CALL_END","toolCallId":"saved-call","subagentRunId":"saved-agent"}"#))
        _ = try store.receive(event(#"{"type":"REASONING_ENCRYPTED_VALUE","subtype":"tool-call","entityId":"saved-call","encryptedValue":"opaque-next","subagentRunId":"saved-agent"}"#))
        _ = try store.receive(AgUiEvent(value: ["type": .string("TOOL_CALL_RESULT"),"toolCallId": .string("saved-call"),"messageId": .string("saved-result"),"content": user["content"]!]))
        _ = try store.receive(event(#"{"type":"STATE_SNAPSHOT","snapshot":null}"#))
        _ = try store.receive(event(#"{"type":"RUN_FINISHED","threadId":"thread","runId":"run"}"#)); _ = try store.finish()
        let next = try store.snapshot.nextInput(runId: "revision")
        XCTAssertNil(next.value["state"]); XCTAssertEqual(store.state,.null)
        XCTAssertFalse(next.value["messages"]!.array!.contains { $0["role"]?.string == "activity" })
        XCTAssertEqual(store.messages.first { $0.id == "saved" }?.value["toolCalls"]?.array?[0]["encryptedValue"]?.string,"opaque-next")
        XCTAssertEqual(store.messages.first { $0.id == "saved-result" }?.value["content"],user["content"])
    }
    func testPinnedMirrorsMatchCanonicalRepositoryFixtures() throws {
        let directory = URL(fileURLWithPath: #filePath).deletingLastPathComponent()
        let root = directory.appendingPathComponent("../../../../protocol/agui").standardizedFileURL
        XCTAssertEqual(try Data(contentsOf: root.appendingPathComponent("schema-1.0.json")), try Data(contentsOf: directory.appendingPathComponent("../../Sources/AgentlySDK/Resources/AGUI/schema-1.0.json")))
        XCTAssertEqual(try Data(contentsOf: root.appendingPathComponent("testdata/conformance.json")), try Data(contentsOf: directory.appendingPathComponent("Fixtures/agui-conformance.json")))
    }
    func testInheritedToolOwnershipResumeCoverageAndClientResults() throws {
        let store = try AgUiStore(input: input()); _ = try store.receive(start())
        _ = try store.receive(event(#"{"type":"TEXT_MESSAGE_START","messageId":"owner","subagentRunId":"child"}"#))
        _ = try store.receive(event(#"{"type":"TEXT_MESSAGE_END","messageId":"owner"}"#))
        _ = try store.receive(event(#"{"type":"TOOL_CALL_START","toolCallId":"client-tool","toolCallName":"lookup","parentMessageId":"owner"}"#))
        _ = try store.receive(event(#"{"type":"TOOL_CALL_ARGS","toolCallId":"client-tool","delta":"{}","subagentRunId":"child"}"#))
        _ = try store.receive(event(#"{"type":"TOOL_CALL_END","toolCallId":"client-tool"}"#))
        _ = try store.receive(event(#"{"type":"RUN_FINISHED","threadId":"thread","runId":"run","outcome":{"type":"success","pendingToolCallIds":[]}}"#)); _ = try store.finish()
        XCTAssertEqual(store.pendingToolCallIds, ["client-tool"])
        let result = try AgUiMessage(value: json(#"{"id":"client-result","role":"tool","toolCallId":"client-tool","content":[{"type":"text","text":"done"}]}"#).object!)
        XCTAssertThrowsError(try store.toolResultsInput(runId: "continue", results: []))
        let next = try store.toolResultsInput(runId: "continue", results: [result])
        XCTAssertEqual(next.value["messages"]!.array!.last?["id"]?.string, "client-result"); XCTAssertEqual(next.value["tools"], try input().value["tools"])
        let interrupted = try AgUiStore(input: input()); _ = try interrupted.receive(start())
        _ = try interrupted.receive(event(#"{"type":"RUN_FINISHED","threadId":"thread","runId":"run","outcome":{"type":"interrupt","interrupts":[{"id":"old","reason":"approval","expiresAt":"2000-01-01T00:00:00Z"}]}}"#))
        XCTAssertThrowsError(try interrupted.nextInput(runId: "resumed"))
        XCTAssertThrowsError(try interrupted.nextInput(runId: "resumed", resume: json(#"[{"interruptId":"old","status":"resolved"}]"#).array!))
        XCTAssertNoThrow(try interrupted.nextInput(runId: "resumed", resume: json(#"[{"interruptId":"old","status":"cancelled"}]"#).array!))
    }
    func testLiveStandardEndpointWhenConfigured() async throws {
        guard let address = ProcessInfo.processInfo.environment["AGENTLY_AGUI_LIVE_URL"], let url = URL(string: address) else { throw XCTSkip("Set AGENTLY_AGUI_LIVE_URL for actual assembled backend transport proof") }
        let client = AgUiClient(endpoint: url)
        let discovery = try AgUiRunInput(threadId: UUID().uuidString, runId: UUID().uuidString, messages: [], forwardedProps: AgentlyAgUiExtensions.forwardedProps(operation: "capabilities"))
        var advertised: AgUiValue?
        for try await update in client.run(discovery) { advertised = update.snapshot.capabilities ?? advertised; XCTAssertNotEqual(update.event.type, "RUN_ERROR") }
        XCTAssertNotNil(advertised)
        let request = try AgUiRunInput(threadId: UUID().uuidString, runId: UUID().uuidString, messages: [.user(id: UUID().uuidString, content: .string("Hello local fixture"))])
        var final: AgUiSnapshot?
        for try await update in client.run(request) { final = update.snapshot; XCTAssertNotEqual(update.event.type, "RUN_ERROR", update.event.value.string("message") ?? "") }
        XCTAssertEqual(final?.terminalEvent?.type, "RUN_FINISHED")
        XCTAssertTrue(final?.messages.contains { $0.role == "assistant" && !($0.value.string("content") ?? "").isEmpty } ?? false)
        let next = try XCTUnwrap(final).nextInput(runId: UUID().uuidString)
        XCTAssertEqual(next.value["messages"]!.array!.filter { $0["role"]?.string == "assistant" }.count, final?.messages.filter { $0.role == "assistant" }.count)
    }
    func testCancellingConsumingTaskAbortsTransportWithoutSynthesizingSuccess() async throws {
        AgUiURLProtocol.payload = Data("data: {\"type\":\"RUN_STARTED\",\"threadId\":\"thread\",\"runId\":\"run\"}\n\n".utf8)
        AgUiURLProtocol.holdOpen = true; AgUiURLProtocol.status = 200
        let opened = expectation(description: "Received RUN_STARTED"), stopped = expectation(description: "HTTP transport cancelled")
        AgUiURLProtocol.stopHook = { stopped.fulfill() }
        let config = URLSessionConfiguration.ephemeral; config.protocolClasses = [AgUiURLProtocol.self]
        let session = URLSession(configuration: config)
        defer { AgUiURLProtocol.holdOpen = false; AgUiURLProtocol.stopHook = nil; session.invalidateAndCancel() }
        let client = AgUiClient(endpoint: URL(string: "https://fixture.invalid/run")!, session: session)
        let request = try input()
        let consuming = Task {
            do { for try await update in client.run(request) { XCTAssertEqual(update.event.type, "RUN_STARTED"); XCTAssertNil(update.snapshot.terminalEvent); opened.fulfill() } }
            catch { XCTAssertTrue(Task.isCancelled) }
        }
        await fulfillment(of: [opened], timeout: 2)
        consuming.cancel(); await consuming.value
        await fulfillment(of: [stopped], timeout: 2)
    }
    func testDisconnectThenFullReplayRebuildsAcceptedInputWithoutDuplicatingContent() async throws {
        let accepted=try input()
        let prefix="data: {\"type\":\"RUN_STARTED\",\"threadId\":\"thread\",\"runId\":\"run\"}\n\n" +
            "data: {\"type\":\"TEXT_MESSAGE_START\",\"messageId\":\"assistant\",\"role\":\"assistant\"}\n\n" +
            "data: {\"type\":\"TEXT_MESSAGE_CONTENT\",\"messageId\":\"assistant\",\"delta\":\"partial \"}\n\n"
        AgUiURLProtocol.payload=Data(prefix.utf8);AgUiURLProtocol.failAfterPayload=true;AgUiURLProtocol.status=200
        let config=URLSessionConfiguration.ephemeral;config.protocolClasses=[AgUiURLProtocol.self]
        let session=URLSession(configuration:config);defer{AgUiURLProtocol.failAfterPayload=false;session.invalidateAndCancel()}
        let client=AgUiClient(endpoint:URL(string:"https://fixture.invalid/run")!,session:session)
        var partial:[AgUiUpdate]=[],failed=false
        do{for try await update in client.run(accepted){partial.append(update)}}catch{failed=true}
        XCTAssertTrue(failed);let partialSnapshot=try XCTUnwrap(partial.last?.snapshot)
        XCTAssertEqual(partialSnapshot.messages.first{$0.id=="assistant"}?.value.string("content"),"partial ")
        let originalBody=AgUiURLProtocol.body(try XCTUnwrap(AgUiURLProtocol.captured))
        let user=accepted.value["messages"]!.array![0]
        let snapshot=try AgUiValue.object(["type":.string("MESSAGES_SNAPSHOT"),"messages":.array([user,json(#"{"id":"assistant","role":"assistant","content":"partial final","encryptedValue":"opaque-replayed"}"#)])]).jsonString()
        let rest="data: {\"type\":\"TEXT_MESSAGE_CONTENT\",\"messageId\":\"assistant\",\"delta\":\"final\"}\n\n" +
            "data: {\"type\":\"TEXT_MESSAGE_END\",\"messageId\":\"assistant\"}\n\n" + "data: \(snapshot)\n\n" +
            "data: {\"type\":\"RUN_FINISHED\",\"threadId\":\"thread\",\"runId\":\"run\"}\n\n"
        AgUiURLProtocol.payload=Data((prefix+rest).utf8);AgUiURLProtocol.failAfterPayload=false
        var replay:[AgUiUpdate]=[];for try await update in client.run(partialSnapshot.input){replay.append(update)}
        let final=try XCTUnwrap(replay.last?.snapshot)
        XCTAssertEqual(final.messages.first{$0.id=="assistant"}?.value.string("content"),"partial final");XCTAssertEqual(final.messages.filter{$0.id=="assistant"}.count,1)
        XCTAssertEqual(final.messages.first{$0.id=="assistant"}?.value.string("encryptedValue"),"opaque-replayed");XCTAssertEqual(final.messages.first?.value,user.object)
        XCTAssertEqual(originalBody,AgUiURLProtocol.body(try XCTUnwrap(AgUiURLProtocol.captured)))
    }
    func testActualURLSessionPostSSEWithSplitUTF8AndLosslessState() async throws {
        let f = try fixture()
        let wire = try f["events"]!.array!.enumerated().map { index, value in let ending = index % 3 == 0 ? "\r\n" : index % 3 == 1 ? "\n" : "\r"; return "data: \(try value.jsonString())\(ending)\(ending)" }.joined()
        AgUiURLProtocol.payload = Data(wire.utf8); AgUiURLProtocol.captured = nil; AgUiURLProtocol.status = 200
        let config = URLSessionConfiguration.ephemeral; config.protocolClasses = [AgUiURLProtocol.self]
        let session = URLSession(configuration: config); defer { session.invalidateAndCancel() }
        let client = AgUiClient(endpoint: URL(string: "https://fixture.invalid/run")!, headers: ["X-Test": "fixture"], session: session)
        var updates: [AgUiUpdate] = []
        for try await update in client.run(try input()) { updates.append(update) }
        let request = try XCTUnwrap(AgUiURLProtocol.captured)
        XCTAssertEqual(request.httpMethod, "POST"); XCTAssertEqual(request.value(forHTTPHeaderField: "X-Test"), "fixture")
        XCTAssertEqual(try AgUiValue.parse(AgUiURLProtocol.body(request)), .object(try input().value))
        XCTAssertEqual(updates.last?.snapshot.messages.map { $0.id }, f["expectedMessages"]!.array!.map { $0["id"]!.string! })
        for expected in f["expectedMessages"]!.array! { let id = expected["id"]!.string!; XCTAssertEqual(try updates.last?.snapshot.messages.first { $0.id == id }.map { try AgUiValue.object($0.value).jsonString() }, try expected.jsonString(), "HTTP message \(id)") }
        XCTAssertEqual(updates.last?.snapshot.state, f["expectedState"])
        XCTAssertEqual(updates.map { AgUiValue.string($0.event.type) }, f["expectedNormalizedTypes"]!.array!)
        AgUiURLProtocol.payload = Data("data: {\"type\":\"RUN_ERROR\",\"message\":\"server failed\"}\n\n".utf8)
        var errorUpdates: [AgUiUpdate] = []; for try await update in client.run(try input()) { errorUpdates.append(update) }
        XCTAssertEqual(errorUpdates.last?.event.type, "RUN_ERROR")
    }
}

private final class AgUiURLProtocol: URLProtocol {
    static var payload = Data(), captured: URLRequest?, status = 200
    static var failAfterPayload = false
    static var holdOpen = false, stopHook: (() -> Void)?
    private var heldOpen = false
    override class func canInit(with request: URLRequest) -> Bool { request.url?.host == "fixture.invalid" }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }
    override func startLoading() {
        Self.captured = request; heldOpen = Self.holdOpen
        let response = HTTPURLResponse(url: request.url!, statusCode: Self.status, httpVersion: "HTTP/1.1", headerFields: ["Content-Type": "text/event-stream; charset=utf-8"])!
        client?.urlProtocol(self, didReceive: response, cacheStoragePolicy: .notAllowed)
        for offset in stride(from: 0, to: Self.payload.count, by: 7) { client?.urlProtocol(self, didLoad: Self.payload.subdata(in: offset..<min(offset+7,Self.payload.count))) }
        if Self.failAfterPayload { DispatchQueue.global().asyncAfter(deadline: .now()+0.05) { self.client?.urlProtocol(self,didFailWithError:URLError(.networkConnectionLost)) } } else if !heldOpen { client?.urlProtocolDidFinishLoading(self) }
    }
    override func stopLoading() { if heldOpen { heldOpen = false; Self.stopHook?() } }
    static func body(_ request: URLRequest) -> Data {
        if let body = request.httpBody { return body }
        guard let stream = request.httpBodyStream else { return Data() }
        stream.open(); defer { stream.close() }; var data = Data(); var buffer = [UInt8](repeating: 0,count: 4096)
        while stream.hasBytesAvailable { let count = stream.read(&buffer,maxLength: buffer.count); if count <= 0 { break }; data.append(contentsOf: buffer.prefix(count)) }
        return data
    }
}

extension AgUiTests {
    func testClientToolHandlerAndTypedResultRoundTripThroughURLSession() async throws {
        let definition = try json(#"{"name":"browser","description":"authorized","parameters":{"type":"object","properties":{"n":{"type":"integer"}},"required":["n"]}}"#)
        let request = try AgUiRunInput(threadId:"thread",runId:"run",messages:[],tools:[definition])
        let frames = [#"{"type":"RUN_STARTED","threadId":"thread","runId":"run"}"#,#"{"type":"TEXT_MESSAGE_START","messageId":"owner","role":"assistant"}"#,#"{"type":"TEXT_MESSAGE_END","messageId":"owner"}"#,#"{"type":"TOOL_CALL_START","toolCallId":"call","toolCallName":"browser","parentMessageId":"owner"}"#,#"{"type":"TOOL_CALL_ARGS","toolCallId":"call","delta":"{\"n\":42}"}"#,#"{"type":"TOOL_CALL_END","toolCallId":"call"}"#,#"{"type":"RUN_FINISHED","threadId":"thread","runId":"run","outcome":{"type":"success","pendingToolCallIds":["call"]}}"#]
        AgUiURLProtocol.status=200;AgUiURLProtocol.payload=Data(frames.map{"data: \($0)\n\n"}.joined().utf8)
        let config=URLSessionConfiguration.ephemeral;config.protocolClasses=[AgUiURLProtocol.self];let session=URLSession(configuration:config);defer{session.invalidateAndCancel()}
        let client=AgUiClient(endpoint:URL(string:"https://fixture.invalid/run")!,session:session)
        var latest:AgUiSnapshot?;for try await update in client.run(request){latest=update.snapshot}
        let tool=try AgUiClientTool(definition:definition){args,_ in XCTAssertEqual(args["n"],.number("42"));return AgUiClientToolResult(content:.array([.object(["type":.string("text"),"text":.string("héllo 🌍")])]),metadata:.object(["opaque":.string("kept")]))}
        let dispatcher=AgUiClientToolDispatcher();let results=try await dispatcher.executeClientTools(snapshot:try XCTUnwrap(latest),tools:[tool]);let continuation=try latest!.toolResultsInput(runId:"continued",results:results)
        AgUiURLProtocol.payload=Data("data: {\"type\":\"RUN_STARTED\",\"threadId\":\"thread\",\"runId\":\"continued\"}\n\ndata: {\"type\":\"RUN_FINISHED\",\"threadId\":\"thread\",\"runId\":\"continued\"}\n\n".utf8)
        for try await _ in client.run(continuation){}
        let posted=try AgUiValue.parse(AgUiURLProtocol.body(try XCTUnwrap(AgUiURLProtocol.captured)))
        XCTAssertNil(posted["forwardedProps"]);XCTAssertEqual(posted["tools"]?.array?.first,definition)
        XCTAssertEqual(posted["messages"]?.array?.last?["content"],results[0].value["content"]);XCTAssertEqual(posted["messages"]?.array?.last?["metadata"]?["opaque"],.string("kept"))
    }
    func testBackendCancelCommandUsesIndependentPOSTWhileOriginalTransportIsOpen() async throws {
        AgUiURLProtocol.status=200;AgUiURLProtocol.holdOpen=true;AgUiURLProtocol.payload=Data("data: {\"type\":\"RUN_STARTED\",\"threadId\":\"thread\",\"runId\":\"target\"}\n\n".utf8)
        let config=URLSessionConfiguration.ephemeral;config.protocolClasses=[AgUiURLProtocol.self];let session=URLSession(configuration:config)
        defer{AgUiURLProtocol.holdOpen=false;AgUiURLProtocol.stopHook=nil;session.invalidateAndCancel()}
        let client=AgUiClient(endpoint:URL(string:"https://fixture.invalid/run")!,headers:["X-Test":"same-config"],session:session)
        let opened=expectation(description:"original opened")
        let original=Task {for try await update in client.run(try AgUiRunInput(threadId:"thread",runId:"target",messages:[])){if update.event.type=="RUN_STARTED"{opened.fulfill()}}}
        await fulfillment(of:[opened],timeout:2)
        AgUiURLProtocol.holdOpen=false;AgUiURLProtocol.payload=Data("data: {\"type\":\"RUN_STARTED\",\"threadId\":\"thread\",\"runId\":\"cancel-command\"}\n\ndata: {\"type\":\"CUSTOM\",\"name\":\"agently.run.cancel\",\"value\":{\"version\":\"1\",\"cancelled\":true}}\n\ndata: {\"type\":\"RUN_FINISHED\",\"threadId\":\"thread\",\"runId\":\"cancel-command\"}\n\n".utf8)
        for try await _ in try client.cancelRun(threadId:"thread",targetRunId:"target",commandRunId:"cancel-command"){}
        XCTAssertFalse(original.isCancelled)
        let captured=try XCTUnwrap(AgUiURLProtocol.captured),posted=try AgUiValue.parse(AgUiURLProtocol.body(captured))
        XCTAssertEqual(captured.value(forHTTPHeaderField:"X-Test"),"same-config");XCTAssertEqual(posted["agently"],nil);XCTAssertEqual(posted["forwardedProps"]?["agently"]?["operation"],.string("run.cancel"));XCTAssertEqual(posted["forwardedProps"]?["agently"]?["payload"]?["runId"],.string("target"))
        original.cancel();_=await original.result
    }
}

extension AgUiTests {
    func testLiveClientToolHandlerContinuationAndIdenticalReplayWhenConfigured() async throws {
        guard let address=ProcessInfo.processInfo.environment["AGENTLY_AGUI_LIVE_URL"],let url=URL(string:address) else {throw XCTSkip("Set AGENTLY_AGUI_LIVE_URL for actual native handler/continuation proof")}
        // The loopback fixture is HTTP but emits a Secure anonymous cookie. Use
        // explicit test-only identity rather than weaken the transport cookie policy.
        let config=URLSessionConfiguration.ephemeral;config.httpShouldSetCookies=false
        let session=URLSession(configuration:config);defer{session.invalidateAndCancel()}
        let cookie="agently_anonymous_user=anonymous:sdk-swift-"+UUID().uuidString
        let client=AgUiClient(endpoint:url,headers:["Cookie":cookie],session:session),definition=try json(#"{"name":"ui_lookup","description":"SDK authorized fixture","parameters":{"type":"object","properties":{"value":{"type":"string"}},"required":["value"]}}"#)
        let input=try AgUiRunInput(threadId:UUID().uuidString,runId:UUID().uuidString,messages:[.user(id:UUID().uuidString,content:.string("fixture-client-tool"))],tools:[definition])
        var latest:AgUiSnapshot?
        for try await update in client.run(input){XCTAssertNotEqual(update.event.type,"RUN_ERROR",update.event.value.string("message") ?? "");latest=update.snapshot}
        let pending=try XCTUnwrap(latest);XCTAssertEqual(pending.pendingToolCallIds.count,1)
        let tool=try AgUiClientTool(definition:definition){args,_ in XCTAssertEqual(args["value"],.string("client fixture"));return AgUiClientToolResult(content:.array([.object(["type":.string("text"),"text":.string("native SDK handled")])]),metadata:.object(["sdk":.string("swift")]))}
        let results=try await AgUiClientToolDispatcher().executeClientTools(snapshot:pending,tools:[tool]);let next=try pending.toolResultsInput(runId:UUID().uuidString,results:results)
        var finished:AgUiSnapshot?
        for try await update in client.run(next){XCTAssertNotEqual(update.event.type,"RUN_ERROR",update.event.value.string("message") ?? "");finished=update.snapshot}
        XCTAssertEqual(finished?.terminalEvent?.value["outcome"]?["type"],.string("success"));XCTAssertTrue(finished?.pendingToolCallIds.isEmpty ?? false)
        var replay:AgUiSnapshot?
        for try await update in client.run(next){XCTAssertNotEqual(update.event.type,"RUN_ERROR");replay=update.snapshot}
        XCTAssertEqual(replay?.messages,finished?.messages);XCTAssertEqual(replay?.state,finished?.state)
    }
}
