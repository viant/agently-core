import Foundation
import XCTest
@testable import AgentlySDK

private func nativeConversationIdentityFixture(_ id: String, wireThreadID: String? = nil) async throws -> Conversation {
    var object: [String: Any] = ["id": id]
    if let wireThreadID { object["aguiThreadId"] = wireThreadID }
    return try JSONDecoder.agently().decode(Conversation.self, from: JSONSerialization.data(withJSONObject: object))
}

private final class ConversationScript: @unchecked Sendable {
    let lock = NSLock()
    private var posted: [AgUiRunInput] = []
    var pendingRun = false
    var interrupt = false
    var failFirstChat = false
    var holdBootstrap: AsyncStream<Void>.Continuation?
    var waitForBootstrap: AsyncStream<Void>?
    var holdChat: AsyncStream<Void>.Continuation?
    var waitForChat: AsyncStream<Void>?
    var inputs: [AgUiRunInput] { lock.lock(); defer { lock.unlock() }; return posted }
    func run(_ input: AgUiRunInput) throws -> AsyncThrowingStream<AgUiUpdate, Error> {
        lock.lock(); posted.append(input); let ordinal = posted.count
        let chatAttempt = posted.filter { $0.value["forwardedProps"]?["agently"]?["operation"]?.string == "chat" }.count
        lock.unlock()
        return AsyncThrowingStream { continuation in
            let task = Task {
                do {
                    let store = try AgUiStore(input: input)
                    func emit(_ row: [String: AgUiValue]) throws {
                        let raw = try AgUiEvent(value: row)
                        _ = try store.receive(raw) { event in continuation.yield(AgUiUpdate(sourceEvent: raw, event: event, snapshot: store.snapshot)) }
                    }
                    let operation = input.value["forwardedProps"]?["agently"]?["operation"]?.string ?? "chat"
                    let identity: [String: AgUiValue] = ["agently": .object(["presentation": .object(["version": .string("1"), "nativeTurnId": .string("native-turn")])])]
                    try emit(["type": .string("RUN_STARTED"), "threadId": .string(input.threadId), "runId": .string(input.runId), "metadata": .object(identity)])
                    if operation == "conversation.bootstrap" {
                        if let waitForBootstrap { for await _ in waitForBootstrap { break } }
                        try Task.checkCancellation()
                        let value = try AgUiValue.parse(#"{"version":"1","threadId":"thread","transcript":{"schemaVersion":"1","conversation":{"conversationId":"thread","turns":[{"turnId":"history","status":"completed"}]},"feeds":[{"feedId":"report","title":"Report","data":{"rows":[{"amount":17}]}}]},"messages":[{"id":"history-user","role":"user","content":"same"}],"state":{"page":"planning"},"hostActivities":[{"id":"host","role":"activity","activityType":"mcp-apps","content":{"resource":"ui://report"}}],"unavailableHostActivityIds":["host-old"],"runs":[],"projection":{"lossless":true,"unavailableMessageIds":[]}}"#)
                        var result = value.object!
                        result["threadId"] = .string(input.threadId)
                        result["ordinal"] = .number(String(ordinal))
                        if pendingRun { result["runs"] = .array([.object(["threadId": .string(input.threadId), "runId": .string("owned-existing"), "kind": .string("chat"), "status": .string("running"), "revision": .number("1"), "lastSequence": .number("2")])]) }
                        try emit(["type": .string("RUN_FINISHED"), "threadId": .string(input.threadId), "runId": .string(input.runId), "outcome": .object(["type": .string("success")]), "result": .object(result)])
                    } else {
                        if operation == "chat", failFirstChat, chatAttempt == 1 { throw URLError(.networkConnectionLost) }
                        if let waitForChat { for await _ in waitForChat { break } }
                        try Task.checkCancellation()
                        try emit(["type": .string("ACTIVITY_SNAPSHOT"), "messageId": .string("turn-activity"), "activityType": .string("agently.turn"), "content": .object(["version": .string("1"), "nativeTurnId": .string("native-turn"), "status": .string("running"), "queueSequence": .string("1")]), "metadata": .object(identity)])
                        try emit(["type": .string("TEXT_MESSAGE_CHUNK"), "messageId": .string("new-answer"), "role": .string("assistant"), "delta": .string("same"), "metadata": .object(identity)])
                        let outcome: AgUiValue = interrupt ? .object(["type": .string("interrupt"), "interrupts": .array([.object(["id": .string("approval"), "reason": .string("approval")])])]) : .object(["type": .string("success")])
                        try emit(["type": .string("RUN_FINISHED"), "threadId": .string(input.threadId), "runId": .string(input.runId), "outcome": outcome])
                    }
                    _ = try store.finish(); continuation.finish()
                } catch { continuation.finish(throwing: error) }
            }
            continuation.onTermination = { _ in task.cancel() }
        }
    }
}

private final class FakeConversationCookies: AgentlySessionCookieStoring, @unchecked Sendable {
    var value = "fixture-session=first"
    func cookieHeader(for url: URL) -> String? { value }
    func storeCookies(from response: HTTPURLResponse, requestURL: URL) { value = "fixture-session=refreshed" }
    func clear() { value = "" }
}

private final class ConversationURLProtocol: URLProtocol, @unchecked Sendable {
    static let lock = NSLock()
    static var requests: [URLRequest] = []
    static var readOnlyHistory = false
    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }
    override func startLoading() {
        Self.lock.lock(); Self.requests.append(request); Self.lock.unlock()
        let metadata = request.httpMethod == "GET"
        let response = HTTPURLResponse(url: request.url!, statusCode: metadata ? 200 : 403, httpVersion: "HTTP/1.1", headerFields: ["Content-Type": metadata ? "application/json" : "text/event-stream"])!
        client?.urlProtocol(self, didReceive: response, cacheStoragePolicy: .notAllowed)
        let history = Self.readOnlyHistory && request.url?.path.hasSuffix("/transcript") == true
        let data = history ? Data(#"{"schemaVersion":"2","conversation":{"conversationId":"thread","turns":[]}}"#.utf8) : (metadata ? Data(#"{"id":"thread"}"#.utf8) : Data("forbidden".utf8))
        client?.urlProtocol(self, didLoad: data)
        client?.urlProtocolDidFinishLoading(self)
    }
    override func stopLoading() {}
}

final class AgUiConversationTransportTests: XCTestCase {
    private func client(cookies: AgentlySessionCookieStoring? = nil, session: URLSession = .shared, fixtureTag: String = "test") -> AgentlyClient {
        AgentlyClient(endpoints: ["appAPI": EndpointConfig(baseURL: URL(string: "https://fixture.invalid/bff")!, headers: ["X-Fixture": fixtureTag])], session: session, sessionCookieStore: cookies)
    }
    private func waitUntil(_ predicate: () -> Bool) async throws {
        for _ in 0..<300 { if predicate() { return }; try await Task.sleep(nanoseconds: 1_000_000) }
        XCTFail("Timed out waiting for fixture action")
    }
    func testOpaqueHistoryBindingKeepsNativePresentationAndExactWireThread() async throws {
        let host = client(), script = ConversationScript(), wire = "  Wire-雪\t"
        let transport = AgUiConversationTransport(client: host, run: { try script.run($0) }, nativeIdentity: { id in try await nativeConversationIdentityFixture(id, wireThreadID: wire) })
        let history = try await transport.refresh(conversationID: "thread")
        XCTAssertEqual(history.transcript.conversation?.conversationID, "thread")
        let output = try await transport.query(QueryInput(conversationID: "thread", query: "same native UI"))
        XCTAssertEqual(output.conversationID, "thread")
        XCTAssertTrue(script.inputs.allSatisfy { $0.threadId == wire })
        await transport.reset()
    }

    func testAgUiBFFClientSharesSessionAndCurrentCookies() throws {
        let cookies = FakeConversationCookies(), configuration = URLSessionConfiguration.ephemeral
        let session = URLSession(configuration: configuration)
        let host = client(cookies: cookies, session: session), first = try host.agUiClient()
        XCTAssertTrue(first.session === session)
        XCTAssertEqual(first.endpoint.absoluteString, "https://fixture.invalid/bff/v1/ag-ui/run")
        XCTAssertEqual(first.headers["Cookie"], "fixture-session=first")
        XCTAssertEqual(first.headers["X-Fixture"], "test")
        first.onResponse?(HTTPURLResponse(url: first.endpoint, statusCode: 200, httpVersion: nil, headerFields: [:])!)
        XCTAssertEqual(try host.agUiClient().headers["Cookie"], "fixture-session=refreshed")
        let old = host.agUiConversations
        host.clearSessionCookies()
        XCTAssertFalse(old === host.agUiConversations)
        first.onResponse?(HTTPURLResponse(url: first.endpoint, statusCode: 200, httpVersion: nil, headerFields: [:])!)
        XCTAssertEqual(cookies.value, "")
    }
    func testCanonicalBootstrapRetainsFeedsStateRunRefsAndSeparateHostActivities() async throws {
        let host = client(), script = ConversationScript()
        let transport = AgUiConversationTransport(client: host, run: { try script.run($0) }, nativeIdentity: { id in try await nativeConversationIdentityFixture(id) })
        let result = try await transport.refresh(conversationID: "thread")
        XCTAssertEqual(result.transcript.conversation?.turns.first?.turnID, "history")
        XCTAssertEqual(result.transcript.feeds.first?.feedID, "report")
        XCTAssertEqual(result.state["page"]?.string, "planning")
        XCTAssertEqual(result.hostActivities.first?.id, "host")
        XCTAssertFalse(result.messages.contains { $0.id == "host" })
        let unavailable = await transport.unavailableHostActivityIDs(conversationID: "thread")
        XCTAssertEqual(unavailable, ["host-old"])
        XCTAssertEqual(script.inputs.first?.value["forwardedProps"]?["agently"]?["payload"]?["includeModelCalls"], .bool(true))
    }
    func testQueryUsesOneStableRunRequestAndProtocolUserIdentityWithoutNativeQuery() async throws {
        let host = client(), script = ConversationScript()
        let transport = AgUiConversationTransport(client: host, run: { try script.run($0) }, nativeIdentity: { id in try await nativeConversationIdentityFixture(id) })
        let output = try await transport.query(QueryInput(conversationID: "thread", messageID: "request-user", agentID: "Steward", query: "same", resourceURIs: ["workspace://attachment"]))
        XCTAssertEqual(output.conversationID, "thread"); XCTAssertEqual(output.messageID, "native-turn")
        let post = try XCTUnwrap(script.inputs.first { $0.value["forwardedProps"]?["agently"]?["operation"]?.string == "chat" })
        XCTAssertEqual(post.value["forwardedProps"]?["agently"]?["requestId"]?.string, "request-user")
        XCTAssertEqual(post.value["messages"]?.array?.last?["id"]?.string, "request-user")
        XCTAssertEqual(post.value["forwardedProps"]?["agently"]?["payload"]?["useServerState"], .bool(true))
        XCTAssertNotEqual(post.runId, "native-turn")
        XCTAssertFalse(post.value["messages"]?.array?.contains { $0["id"]?.string == "host" } ?? true)
    }
    func testNetworkDropReplaysExactInputWithoutNewRequestOrRunIdentity() async throws {
        let host = client(), script = ConversationScript(); script.failFirstChat = true
        let transport = AgUiConversationTransport(client: host, run: { try script.run($0) }, nativeIdentity: { id in try await nativeConversationIdentityFixture(id) })
        let output = try await transport.query(QueryInput(conversationID: "thread", messageID: "stable-user", query: "same"))
        XCTAssertEqual(output.messageID, "native-turn")
        let chats = script.inputs.filter { $0.value["forwardedProps"]?["agently"]?["operation"]?.string == "chat" }
        XCTAssertEqual(chats.count, 2)
        XCTAssertEqual(chats.first, chats.last)
    }
    func testFreshReconcileWaitsForOlderReadAndReadsAgain() async throws {
        let host = client(), script = ConversationScript()
        let gate = AsyncStream<Void>.makeStream(); script.waitForBootstrap = gate.stream; script.holdBootstrap = gate.continuation
        let transport = AgUiConversationTransport(client: host, run: { try script.run($0) }, nativeIdentity: { id in try await nativeConversationIdentityFixture(id) })
        let first = Task { try await transport.refresh(conversationID: "thread") }
        try await waitUntil { script.inputs.count == 1 }
        let afterCommand = Task { try await transport.reconcile(conversationID: "thread") }
        try await Task.sleep(nanoseconds: 5_000_000)
        gate.continuation.yield(()); gate.continuation.finish()
        _ = try await first.value
        let fresh = try await afterCommand.value
        XCTAssertEqual(script.inputs.count, 2)
        XCTAssertEqual(fresh.value["ordinal"], .number("2"))
    }
    func testResetFencesInFlightBootstrapBeforeItCanPublish() async throws {
        let host = client(), script = ConversationScript(), gate = AsyncStream<Void>.makeStream()
        script.waitForBootstrap = gate.stream
        let transport = AgUiConversationTransport(client: host, run: { try script.run($0) }, nativeIdentity: { id in try await nativeConversationIdentityFixture(id) })
        let first = Task { try await transport.refresh(conversationID: "thread") }
        try await waitUntil { script.inputs.count == 1 }
        transport.invalidate()
        gate.continuation.yield(()); gate.continuation.finish()
        do { _ = try await first.value; XCTFail("Invalid account read published") } catch { }
        await transport.reset()
    }
    func testOwnedAttachUsesRunAttachWithoutResubmittingChatAndDetachDoesNotCancel() async throws {
        let host = client(), script = ConversationScript(), gate = AsyncStream<Void>.makeStream()
        script.pendingRun = true; script.waitForChat = gate.stream
        let transport = AgUiConversationTransport(client: host, run: { try script.run($0) }, nativeIdentity: { id in try await nativeConversationIdentityFixture(id) })
        let observer = Task { for try await _ in transport.subscribe(conversationID: "thread") { try Task.checkCancellation() } }
        try await waitUntil { script.inputs.contains { $0.runId == "owned-existing" } }
        observer.cancel(); _ = try? await observer.value
        let operations = script.inputs.compactMap { $0.value["forwardedProps"]?["agently"]?["operation"]?.string }
        XCTAssertTrue(operations.contains("run.attach")); XCTAssertFalse(operations.contains("chat")); XCTAssertFalse(operations.contains("run.cancel"))
        gate.continuation.yield(()); gate.continuation.finish()
        await transport.reset()
    }
    func testSharedReaderUsesDedicatedHistoryWithoutQueryOrRefreshRecursion() async throws {
        ConversationURLProtocol.lock.withLock { ConversationURLProtocol.requests=[];ConversationURLProtocol.readOnlyHistory=true }
        defer { ConversationURLProtocol.lock.withLock { ConversationURLProtocol.readOnlyHistory=false } }
        let configuration=URLSessionConfiguration.ephemeral;configuration.protocolClasses=[ConversationURLProtocol.self]
        let host=client(session:URLSession(configuration:configuration),fixtureTag:"history-read")
        let snapshot=try await host.getTranscript(GetTranscriptInput(conversationID:"thread"))
        XCTAssertEqual(snapshot.conversation?.conversationID,"thread")
        // A preceding observation can finish cancellation after this test starts.
        // Capture only this host's scoped requests, rather than another session.
        let requests=ConversationURLProtocol.lock.withLock{ConversationURLProtocol.requests.filter { $0.value(forHTTPHeaderField:"X-Fixture")=="history-read" }}
        XCTAssertEqual(requests.map{$0.httpMethod},["GET","POST","GET"])
        XCTAssertEqual(requests.map{$0.url?.path},["/bff/v1/conversations/thread","/bff/v1/ag-ui/run","/bff/v1/conversations/thread/transcript"])
        var bootstrapBody = requests[1].httpBody ?? Data()
        if bootstrapBody.isEmpty, let stream = requests[1].httpBodyStream {
            stream.open(); defer { stream.close() }
            var buffer = [UInt8](repeating: 0, count: 4096)
            while true {
                let count = stream.read(&buffer, maxLength: buffer.count)
                if count <= 0 { break }
                bootstrapBody.append(contentsOf: buffer.prefix(count))
            }
        }
        XCTAssertFalse(bootstrapBody.isEmpty)
        let bootstrap = try JSONSerialization.jsonObject(with: bootstrapBody) as? [String:Any]
        XCTAssertEqual(bootstrap?["threadId"] as? String,"thread")
        XCTAssertTrue(requests.last?.url?.path.hasSuffix("/transcript")==true)
        XCTAssertFalse(requests.contains{$0.url?.path.contains("/agent/query")==true})
    }
    func testSharedReaderObservationHydratesAuthorizedHistoryWithoutQuery() async throws {
        ConversationURLProtocol.lock.withLock { ConversationURLProtocol.requests=[];ConversationURLProtocol.readOnlyHistory=true }
        defer { ConversationURLProtocol.lock.withLock { ConversationURLProtocol.readOnlyHistory=false } }
        let configuration=URLSessionConfiguration.ephemeral;configuration.protocolClasses=[ConversationURLProtocol.self]
        let host=client(session:URLSession(configuration:configuration))
        var observed: ConversationStreamSnapshot?
        for try await snapshot in host.trackConversation(conversationID: "thread") { observed=snapshot; break }
        XCTAssertEqual(observed?.conversationID,"thread")
        let requests=ConversationURLProtocol.lock.withLock{ConversationURLProtocol.requests}
        XCTAssertTrue(requests.contains{$0.url?.path.hasSuffix("/transcript")==true})
        XCTAssertFalse(requests.contains{$0.url?.path.contains("/agent/query")==true})
        host.clearSessionCookies()
    }
    func testUnauthorizedAGUIBootstrapDoesNotResubmitQuery() async throws {
        ConversationURLProtocol.lock.withLock { ConversationURLProtocol.requests = [] }
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [ConversationURLProtocol.self]
        let host = client(session: URLSession(configuration: configuration))
        do { _ = try await host.getLiveState(conversationID: "thread"); XCTFail("Expected authorization failure") }
        catch { XCTAssertTrue(error is AgentlySDKError, "malformed read-only history must not be accepted") }
        let requests = ConversationURLProtocol.lock.withLock { ConversationURLProtocol.requests }
        XCTAssertEqual(requests.count, 3)
        XCTAssertEqual(requests.first?.httpMethod, "GET")
        XCTAssertEqual(requests[1].url?.path, "/bff/v1/ag-ui/run")
        XCTAssertEqual(requests[1].httpMethod, "POST")
    }
    func testMalformedForeignBootstrapRejected() throws {
        let value = try AgUiValue.parse(#"{"version":"1","threadId":"foreign","transcript":{},"messages":[],"state":{},"runs":[],"projection":{"lossless":true,"unavailableMessageIds":[]}}"#)
        XCTAssertThrowsError(try AgUiConversationBootstrap(value: value, conversationID: "thread"))
    }
    func testAdmissionRejectsUnrelatedSnapshotActivityAndSubagentStart() throws {
        let input = try AgUiRunInput(threadId: "thread", runId: "owned", messages: [])
        let store = try AgUiStore(input: input)
        let start = try AgUiEvent(value: ["type": .string("RUN_STARTED"), "threadId": .string("thread"), "runId": .string("owned")])
        _ = try store.receive(start)
        let snapshot = try AgUiEvent(value: try AgUiValue.parse(#"{"type":"MESSAGES_SNAPSHOT","messages":[{"id":"old","role":"activity","activityType":"agently.turn","content":{"version":"1","nativeTurnId":"other","status":"running","queueSequence":"1"}}]}"#).object!)
        _ = try store.receive(snapshot)
        var projector = AgUiPresentationProjector(conversationID: "thread", runID: "owned")
        XCTAssertNil(projector.admittedTurn(AgUiUpdate(sourceEvent: snapshot, event: snapshot, snapshot: store.snapshot), clientMessageID: "mine"))
        let subagent = try AgUiEvent(value: ["type": .string("RUN_STARTED"), "threadId": .string("thread"), "runId": .string("owned"), "subagentRunId": .string("child"), "metadata": .object(["agently": .object(["presentation": .object(["version": .string("1"), "nativeTurnId": .string("other")])])])])
        XCTAssertNil(projector.admittedTurn(AgUiUpdate(sourceEvent: subagent, event: subagent, snapshot: store.snapshot), clientMessageID: "mine"))
    }
    func testProjectionUsesCompleteReducerContentAndIdentitiesNotTextDeduplication() throws {
        let history = try AgUiMessage(value: ["id": .string("history"), "role": .string("assistant"), "content": .string("same")])
        let input = try AgUiRunInput(threadId: "thread", runId: "run", messages: [history]), store = try AgUiStore(input: input)
        var projector = AgUiPresentationProjector(conversationID: "thread", runID: "run", baseline: [history])
        let rows: [[String: AgUiValue]] = [
            ["type": .string("RUN_STARTED"), "threadId": .string("thread"), "runId": .string("run")],
            ["type": .string("TEXT_MESSAGE_CHUNK"), "messageId": .string("answer"), "role": .string("assistant"), "delta": .string("same")],
            ["type": .string("TEXT_MESSAGE_CHUNK"), "messageId": .string("answer"), "delta": .string(" again")],
            ["type": .string("TEXT_MESSAGE_CHUNK"), "messageId": .string("second"), "role": .string("assistant"), "delta": .string("same")]
        ]
        var projected: [AgUiValue] = []
        for row in rows {
            let event = try AgUiEvent(value: row)
            _ = try store.receive(event) { applied in
                projected += (try? projector.project(AgUiUpdate(sourceEvent: event, event: applied, snapshot: store.snapshot)).map { try AgUiValue.parse($0.data) }) ?? []
            }
        }
        XCTAssertFalse(projected.contains { $0["messageId"]?.string == "history" })
        XCTAssertTrue(projected.contains { $0["messageId"]?.string == "second" && $0["content"]?.string == "same" })
        XCTAssertEqual(projected.last { $0["messageId"]?.string == "answer" }?["content"]?.string, "same again")
        XCTAssertTrue(projected.allSatisfy { $0["contentMode"]?.string == "snapshot" })
    }
    func testProjectedSnapshotsReplaceNativeBufferAndExecutionContentWhileLegacyDeltasAppend() async throws {
        let tracker = ConversationStreamTracker()
        let textRows = [
            #"{"type":"text_delta","conversationId":"thread","turnId":"turn","messageId":"message","assistantMessageId":"message","content":"same","contentMode":"snapshot"}"#,
            #"{"type":"text_delta","conversationId":"thread","turnId":"turn","messageId":"message","assistantMessageId":"message","content":"same again","contentMode":"snapshot"}"#,
            #"{"type":"reasoning_delta","conversationId":"thread","turnId":"turn","messageId":"message","assistantMessageId":"message","content":"think","contentMode":"snapshot"}"#,
            #"{"type":"reasoning_delta","conversationId":"thread","turnId":"turn","messageId":"message","assistantMessageId":"message","content":"think once","contentMode":"snapshot"}"#
        ]
        for row in textRows { _ = await tracker.apply(SSEEvent(data: row)) }
        var snapshot = await tracker.currentSnapshot()
        XCTAssertEqual(snapshot.bufferedMessages.first?.content, "same again")
        XCTAssertEqual(snapshot.bufferedMessages.first?.narration, "think once")
        XCTAssertEqual(snapshot.liveExecutionGroupsByID["message"]?.content, "same again")
        XCTAssertEqual(snapshot.liveExecutionGroupsByID["message"]?.narration, "think once")
        _ = await tracker.apply(SSEEvent(data: #"{"type":"text_delta","conversationId":"thread","turnId":"turn","messageId":"message","assistantMessageId":"message","content":"!"}"#))
        snapshot = await tracker.currentSnapshot()
        XCTAssertEqual(snapshot.bufferedMessages.first?.content, "same again!")
        XCTAssertEqual(snapshot.liveExecutionGroupsByID["message"]?.content, "same again!")
    }
    func testInterruptBoundaryDoesNotInventNativeCompletion() throws {
        let input = try AgUiRunInput(threadId: "thread", runId: "run", messages: []), store = try AgUiStore(input: input)
        var projector = AgUiPresentationProjector(conversationID: "thread", runID: "run")
        _ = try store.receive(AgUiEvent(value: ["type": .string("RUN_STARTED"), "threadId": .string("thread"), "runId": .string("run")]))
        let terminal = try AgUiEvent(value: try AgUiValue.parse(#"{"type":"RUN_FINISHED","threadId":"thread","runId":"run","outcome":{"type":"interrupt","interrupts":[{"id":"approval","reason":"approval"}]}}"#).object!)
        _ = try store.receive(terminal)
        XCTAssertEqual(store.snapshot.pendingInterrupts.count, 1)
        XCTAssertTrue(try projector.project(AgUiUpdate(sourceEvent: terminal, event: terminal, snapshot: store.snapshot)).isEmpty)
    }
}
