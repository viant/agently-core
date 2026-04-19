import XCTest
@testable import AgentlySDK

final class AgentlySDKTests: XCTestCase {
    final class URLProtocolStub: URLProtocol {
        static var requestHandler: ((URLRequest) throws -> (HTTPURLResponse, Data))?

        override class func canInit(with request: URLRequest) -> Bool { true }
        override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }

        override func startLoading() {
            guard let handler = Self.requestHandler else {
                XCTFail("URLProtocolStub.requestHandler was not set")
                return
            }
            do {
                let (response, data) = try handler(request)
                client?.urlProtocol(self, didReceive: response, cacheStoragePolicy: .notAllowed)
                client?.urlProtocol(self, didLoad: data)
                client?.urlProtocolDidFinishLoading(self)
            } catch {
                client?.urlProtocol(self, didFailWithError: error)
            }
        }

        override func stopLoading() {}
    }

    func testJSONValueRoundTrip() throws {
        let value = JSONValue.object([
            "client": .object([
                "platform": .string("ios"),
                "capabilities": .array([.string("markdown"), .string("attachments")])
            ])
        ])
        let data = try JSONEncoder.agently().encode(value)
        let decoded = try JSONDecoder.agently().decode(JSONValue.self, from: data)
        XCTAssertEqual(decoded, value)
    }

    func testConversationStateDefaultsMissingFeedsAndUsesTurnId() throws {
        let json = """
        {
          "conversation": {
            "conversationId": "conv-1",
            "turns": [
              {
                "turnId": "turn-1",
                "createdAt": "2026-04-12T18:28:00Z"
              }
            ]
          }
        }
        """

        let data = try XCTUnwrap(json.data(using: .utf8))
        let decoded = try JSONDecoder.agently().decode(ConversationStateResponse.self, from: data)

        XCTAssertEqual(decoded.conversation?.conversationID, "conv-1")
        XCTAssertEqual(decoded.conversation?.turns.count, 1)
        XCTAssertEqual(decoded.conversation?.turns.first?.id, "turn-1")
        XCTAssertTrue(decoded.conversation?.feeds.isEmpty ?? false)
    }

    func testListFilesOutputDecodesCapitalizedFilesKey() throws {
        let json = """
        {
          "Files": [
            {
              "id": "file-1",
              "name": "artifact.md",
              "uri": "mem://artifact.md",
              "contentType": "text/markdown"
            }
          ]
        }
        """

        let data = try XCTUnwrap(json.data(using: .utf8))
        let decoded = try JSONDecoder.agently().decode(ListFilesOutput.self, from: data)

        XCTAssertEqual(decoded.files.count, 1)
        XCTAssertEqual(decoded.files.first?.id, "file-1")
        XCTAssertEqual(decoded.files.first?.name, "artifact.md")
    }

    func testGetWorkspaceMetadataAppendsTargetContextQueryItems() async throws {
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [URLProtocolStub.self]
        let session = URLSession(configuration: configuration)
        let endpoint = EndpointConfig(baseURL: try XCTUnwrap(URL(string: "http://localhost:8585")))
        let client = AgentlyClient(endpoints: ["appAPI": endpoint], session: session)

        let expectation = expectation(description: "workspace metadata request captured")
        URLProtocolStub.requestHandler = { request in
            let url = try XCTUnwrap(request.url)
            XCTAssertEqual(url.path, "/v1/workspace/metadata")
            let components = try XCTUnwrap(URLComponents(url: url, resolvingAgainstBaseURL: false))
            let items = components.queryItems ?? []
            func values(for name: String) -> [String] {
                items.filter { $0.name == name }.compactMap(\.value)
            }
            XCTAssertEqual(values(for: "platform"), ["ios"])
            XCTAssertEqual(values(for: "formFactor"), ["tablet"])
            XCTAssertEqual(values(for: "surface"), ["app"])
            XCTAssertEqual(values(for: "capabilities"), ["markdown", "chart"])
            expectation.fulfill()
            let response = HTTPURLResponse(url: url, statusCode: 200, httpVersion: nil, headerFields: [
                "Content-Type": "application/json"
            ])!
            let data = #"{"workspaceRoot":"/tmp/workspace","agents":[],"models":[],"agentInfos":[],"modelInfos":[]}"#.data(using: .utf8)!
            return (response, data)
        }

        _ = try await client.getWorkspaceMetadata(
            MetadataTargetContext(
                platform: "ios",
                formFactor: "tablet",
                surface: "app",
                capabilities: ["markdown", "chart"]
            )
        )

        await fulfillment(of: [expectation], timeout: 2.0)
        URLProtocolStub.requestHandler = nil
    }

    func testSessionDebugOptionsAppendDebugHeaders() async throws {
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [URLProtocolStub.self]
        let session = URLSession(configuration: configuration)
        let endpoint = EndpointConfig(baseURL: try XCTUnwrap(URL(string: "http://localhost:8585")))
        let client = AgentlyClient(
            endpoints: ["appAPI": endpoint],
            sessionDebug: SessionDebugOptions(enabled: true, level: "trace", components: ["conversation", "reactor"]),
            session: session
        )

        let expectation = expectation(description: "session debug headers captured")
        URLProtocolStub.requestHandler = { request in
            XCTAssertEqual(request.value(forHTTPHeaderField: "X-Agently-Debug"), "true")
            XCTAssertEqual(request.value(forHTTPHeaderField: "X-Agently-Debug-Level"), "trace")
            XCTAssertEqual(request.value(forHTTPHeaderField: "X-Agently-Debug-Components"), "conversation,reactor")
            expectation.fulfill()
            let response = HTTPURLResponse(
                url: try XCTUnwrap(request.url),
                statusCode: 200,
                httpVersion: nil,
                headerFields: ["Content-Type": "application/json"]
            )!
            let data = #"{"workspaceRoot":"/tmp/workspace","agents":[],"models":[],"agentInfos":[],"modelInfos":[]}"#.data(using: .utf8)!
            return (response, data)
        }

        _ = try await client.getWorkspaceMetadata()

        await fulfillment(of: [expectation], timeout: 2.0)
        URLProtocolStub.requestHandler = nil
    }

    func testConversationStreamTrackerProgressivelyUpdatesAssistantMessage() async throws {
        let tracker = ConversationStreamTracker()

        _ = await tracker.apply(SSEEvent(data: #"{"type":"turn_started","conversationId":"conv-1","turnId":"turn-1"}"#))
        _ = await tracker.apply(SSEEvent(data: #"{"type":"assistant_preamble","conversationId":"conv-1","turnId":"turn-1","assistantMessageId":"msg-1","content":"Thinking...","status":"running"}"#))
        _ = await tracker.apply(SSEEvent(data: #"{"type":"text_delta","conversationId":"conv-1","turnId":"turn-1","assistantMessageId":"msg-1","content":"Hello "}"#))
        let snapshot = await tracker.apply(SSEEvent(data: #"{"type":"assistant_final","conversationId":"conv-1","turnId":"turn-1","assistantMessageId":"msg-1","content":"Hello world","status":"completed","preamble":"Thinking..."}"#))

        XCTAssertEqual(snapshot.conversationID, "conv-1")
        XCTAssertEqual(snapshot.activeTurnID, "turn-1")
        XCTAssertEqual(snapshot.bufferedMessages.count, 1)
        XCTAssertEqual(snapshot.bufferedMessages.first?.id, "msg-1")
        XCTAssertEqual(snapshot.bufferedMessages.first?.preamble, "Thinking...")
        XCTAssertEqual(snapshot.bufferedMessages.first?.content, "Hello world")
        XCTAssertEqual(snapshot.bufferedMessages.first?.status, "completed")
        XCTAssertEqual(snapshot.bufferedMessages.first?.interim, 0)
    }

    func testConversationStreamTrackerAppliesControlMessagePatchAndExecutionGroup() async throws {
        let tracker = ConversationStreamTracker()

        _ = await tracker.apply(SSEEvent(data: #"{"type":"model_started","conversationId":"conv-1","turnId":"turn-1","assistantMessageId":"msg-1","modelCallId":"mc-1","status":"running","model":{"provider":"openai","model":"gpt-5-mini"}}"#))
        _ = await tracker.apply(SSEEvent(data: #"{"type":"tool_feed_active","conversationId":"conv-1","turnId":"turn-1","feedId":"feed-1","feedTitle":"Feed","feedItemCount":2}"#))
        let snapshot = await tracker.apply(SSEEvent(data: #"{"type":"control","op":"message_patch","conversationId":"conv-1","turnId":"turn-1","assistantMessageId":"msg-1","patch":{"content":"Patched","status":"running","toolName":"prompt-get","linkedConversationId":"linked-1"}}"#))

        XCTAssertEqual(snapshot.feeds.count, 1)
        XCTAssertEqual(snapshot.feeds.first?.feedID, "feed-1")
        XCTAssertEqual(snapshot.liveExecutionGroupsByID["msg-1"]?.modelSteps.first?.modelCallID, "mc-1")
        XCTAssertEqual(snapshot.liveExecutionGroupsByID["msg-1"]?.modelSteps.first?.provider, "openai")
        XCTAssertEqual(snapshot.bufferedMessages.first?.content, "Patched")
        XCTAssertEqual(snapshot.bufferedMessages.first?.toolName, "prompt-get")
        XCTAssertEqual(snapshot.bufferedMessages.first?.linkedConversationID, "linked-1")
    }

    func testConversationStreamTrackerIgnoresEventsFromDifferentConversation() async throws {
        let tracker = ConversationStreamTracker()

        _ = await tracker.apply(SSEEvent(data: #"{"type":"turn_started","conversationId":"conv-1","turnId":"turn-1"}"#))
        _ = await tracker.apply(SSEEvent(data: #"{"type":"assistant_final","conversationId":"conv-1","turnId":"turn-1","assistantMessageId":"msg-1","content":"First conversation","status":"completed"}"#))
        let snapshot = await tracker.apply(SSEEvent(data: #"{"type":"assistant_final","conversationId":"conv-2","turnId":"turn-9","assistantMessageId":"msg-9","content":"Wrong conversation","status":"completed"}"#))

        XCTAssertEqual(snapshot.conversationID, "conv-1")
        XCTAssertEqual(snapshot.bufferedMessages.count, 1)
        XCTAssertEqual(snapshot.bufferedMessages.first?.id, "msg-1")
        XCTAssertEqual(snapshot.bufferedMessages.first?.content, "First conversation")
    }

    func testGetTranscriptUsesExpectedPathAndQuery() async throws {
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [URLProtocolStub.self]
        let session = URLSession(configuration: configuration)
        let endpoint = EndpointConfig(baseURL: try XCTUnwrap(URL(string: "http://localhost:8585")))
        let client = AgentlyClient(endpoints: ["appAPI": endpoint], session: session)

        let expectation = expectation(description: "transcript request captured")
        URLProtocolStub.requestHandler = { request in
            let url = try XCTUnwrap(request.url)
            XCTAssertEqual(url.path, "/v1/conversations/conv-1/transcript")
            let components = try XCTUnwrap(URLComponents(url: url, resolvingAgainstBaseURL: false))
            let items = components.queryItems ?? []

            func value(for name: String) -> String? {
                items.first(where: { $0.name == name })?.value
            }

            XCTAssertEqual(value(for: "includeModelCalls"), "1")
            XCTAssertEqual(value(for: "includeToolCalls"), "1")
            XCTAssertEqual(value(for: "includeFeeds"), "1")
            XCTAssertNil(value(for: "conversationId"))
            expectation.fulfill()

            let response = HTTPURLResponse(
                url: url,
                statusCode: 200,
                httpVersion: nil,
                headerFields: ["Content-Type": "application/json"]
            )!
            let data = """
            {
              "conversation": {
                "conversationId": "conv-1",
                "turns": []
              }
            }
            """.data(using: .utf8)!
            return (response, data)
        }

        _ = try await client.getTranscript(
            GetTranscriptInput(
                conversationID: "conv-1",
                includeModelCalls: true,
                includeToolCalls: true,
                includeFeeds: true
            )
        )

        await fulfillment(of: [expectation], timeout: 2.0)
        URLProtocolStub.requestHandler = nil
    }

    func testQueryInputEncodesAndroidWebParityFields() throws {
        let input = QueryInput(
            conversationID: "conv-1",
            parentConversationID: "parent-1",
            conversationTitle: "Title",
            messageID: "msg-1",
            agentID: "chatter",
            userID: "user-1",
            query: "hello",
            attachments: [QueryAttachment(name: "file.csv", uri: "mem://file.csv", size: 12, mime: "text/csv", stagingFolder: "/tmp")],
            model: "openai_gpt-5-mini",
            tools: ["system_os-getEnv"],
            toolBundles: ["prompt", "template"],
            autoSelectTools: true,
            context: ["platform": .string("ios")],
            reasoningEffort: "medium",
            elicitationMode: "async",
            autoSummarize: true,
            disableChains: true,
            allowedChains: ["chain-a"],
            toolCallExposure: "conversation"
        )

        let data = try JSONEncoder.agently().encode(input)
        let decoded = try JSONDecoder.agently().decode([String: JSONValue].self, from: data)

        XCTAssertEqual(decoded["conversationId"], .string("conv-1"))
        XCTAssertEqual(decoded["parentConversationId"], .string("parent-1"))
        XCTAssertEqual(decoded["conversationTitle"], .string("Title"))
        XCTAssertEqual(decoded["messageId"], .string("msg-1"))
        XCTAssertEqual(decoded["agentId"], .string("chatter"))
        XCTAssertEqual(decoded["userId"], .string("user-1"))
        XCTAssertEqual(decoded["toolBundles"], .array([.string("prompt"), .string("template")]))
        XCTAssertEqual(decoded["reasoningEffort"], .string("medium"))
        XCTAssertEqual(decoded["elicitationMode"], .string("async"))
        XCTAssertEqual(decoded["toolCallExposure"], .string("conversation"))
    }
}
