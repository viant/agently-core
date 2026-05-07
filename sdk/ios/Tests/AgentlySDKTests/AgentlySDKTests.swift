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

    func testConversationStateDecodesCanonicalExecutionFields() throws {
        let json = """
        {
          "schemaVersion": "2026-05-06",
          "eventCursor": "cursor-7",
          "usage": {
            "totalInputTokens": 120,
            "totalOutputTokens": 45
          },
          "conversation": {
            "conversationId": "conv-1",
            "turns": [
              {
                "turnId": "turn-1",
                "status": "running",
                "users": [
                  { "messageId": "user-1", "content": "hello" }
                ],
                "messages": [
                  { "messageId": "msg-1", "role": "user", "content": "hello", "sequence": 1, "status": "completed" }
                ],
                "assistant": {
                  "narration": { "messageId": "narr-1", "content": "thinking", "createdAt": "2026-05-06T10:00:00Z" },
                  "final": { "messageId": "final-1", "content": "done", "createdAt": "2026-05-06T10:00:02Z" },
                  "messages": [
                    { "messageId": "narr-1", "content": "thinking", "createdAt": "2026-05-06T10:00:00Z" },
                    { "messageId": "final-1", "content": "done", "createdAt": "2026-05-06T10:00:02Z" }
                  ]
                },
                "execution": {
                  "pages": [
                    {
                      "pageId": "page-1",
                      "assistantMessageId": "final-1",
                      "parentMessageId": "user-1",
                      "turnId": "turn-1",
                      "iteration": 1,
                      "sequence": 2,
                      "executionRole": "main",
                      "phase": "intake",
                      "status": "running",
                      "finalResponse": false,
                      "modelSteps": [
                        {
                          "modelCallId": "mc-1",
                          "assistantMessageId": "final-1",
                          "executionRole": "main",
                          "phase": "intake",
                          "provider": "openai",
                          "model": "gpt-5.4"
                        }
                      ],
                      "toolSteps": [
                        {
                          "toolCallId": "tc-1",
                          "toolMessageId": "tm-1",
                          "parentMessageId": "user-1",
                          "toolName": "system/exec:start",
                          "content": "running",
                          "executionRole": "sidecar",
                          "operationId": "op-1",
                          "status": "waiting",
                          "asyncOperation": {
                            "operationId": "op-1",
                            "status": "running",
                            "message": "still running"
                          }
                        }
                      ]
                    }
                  ]
                }
              }
            ]
          }
        }
        """

        let data = try XCTUnwrap(json.data(using: .utf8))
        let decoded = try JSONDecoder.agently().decode(ConversationStateResponse.self, from: data)

        XCTAssertEqual(decoded.schemaVersion, "2026-05-06")
        XCTAssertEqual(decoded.eventCursor, "cursor-7")
        XCTAssertEqual(decoded.usage?.totalInputTokens, 120)
        let turn = try XCTUnwrap(decoded.conversation?.turns.first)
        XCTAssertEqual(turn.turnID, "turn-1")
        XCTAssertEqual(turn.users.first?.messageID, "user-1")
        XCTAssertEqual(turn.messages.first?.messageID, "msg-1")
        XCTAssertEqual(turn.assistant?.messages.last?.messageID, "final-1")
        let page = try XCTUnwrap(turn.execution?.pages.first)
        XCTAssertEqual(page.sequence, 2)
        XCTAssertEqual(page.executionRole, "main")
        XCTAssertEqual(page.phase, "intake")
        XCTAssertEqual(page.modelSteps.first?.executionRole, "main")
        let toolStep = try XCTUnwrap(page.toolSteps.first)
        XCTAssertEqual(toolStep.parentMessageID, "user-1")
        XCTAssertEqual(toolStep.executionRole, "sidecar")
        XCTAssertEqual(toolStep.operationID, "op-1")
        XCTAssertEqual(toolStep.asyncOperation?.status, "running")
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

    func testListConversationsBuildsExpectedQueryParameters() async throws {
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [URLProtocolStub.self]
        let session = URLSession(configuration: configuration)
        let endpoint = EndpointConfig(baseURL: try XCTUnwrap(URL(string: "http://localhost:8585")))
        let client = AgentlyClient(endpoints: ["appAPI": endpoint], session: session)

        let expectation = expectation(description: "list conversations request captured")
        URLProtocolStub.requestHandler = { request in
            let url = try XCTUnwrap(request.url)
            XCTAssertEqual(url.path, "/v1/conversations")
            let components = try XCTUnwrap(URLComponents(url: url, resolvingAgainstBaseURL: false))
            let items = components.queryItems ?? []
            func value(for name: String) -> String? {
                items.first(where: { $0.name == name })?.value
            }
            XCTAssertEqual(value(for: "agentId"), "coder")
            XCTAssertEqual(value(for: "parentId"), "parent-1")
            XCTAssertEqual(value(for: "parentTurnId"), "turn-2")
            XCTAssertEqual(value(for: "excludeScheduled"), "true")
            XCTAssertEqual(value(for: "q"), "android qa")
            XCTAssertEqual(value(for: "status"), "active")
            XCTAssertEqual(value(for: "limit"), "25")
            XCTAssertEqual(value(for: "cursor"), "cursor-1")
            XCTAssertEqual(value(for: "direction"), "next")
            expectation.fulfill()
            let response = HTTPURLResponse(url: url, statusCode: 200, httpVersion: nil, headerFields: [
                "Content-Type": "application/json"
            ])!
            let data = #"{"Rows":[],"HasMore":false}"#.data(using: .utf8)!
            return (response, data)
        }

        _ = try await client.listConversations(
            ListConversationsInput(
                agentID: "coder",
                parentID: "parent-1",
                parentTurnID: "turn-2",
                excludeScheduled: true,
                query: "android qa",
                status: "active",
                page: PageInput(limit: 25, cursor: "cursor-1", direction: "next")
            )
        )

        await fulfillment(of: [expectation], timeout: 2.0)
        URLProtocolStub.requestHandler = nil
    }

    func testCreateConversationDecodesParentFields() async throws {
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [URLProtocolStub.self]
        let session = URLSession(configuration: configuration)
        let endpoint = EndpointConfig(baseURL: try XCTUnwrap(URL(string: "http://localhost:8585")))
        let client = AgentlyClient(endpoints: ["appAPI": endpoint], session: session)
        let input = CreateConversationInput(
            agentID: "coder",
            title: "iOS QA",
            parentConversationID: "parent-1",
            parentTurnID: "turn-9"
        )

        let encoded = try JSONEncoder.agently().encode(input)
        let encodedObject = try JSONDecoder.agently().decode([String: JSONValue].self, from: encoded)
        XCTAssertEqual(encodedObject["parentConversationId"], .string("parent-1"))
        XCTAssertEqual(encodedObject["parentTurnId"], .string("turn-9"))

        URLProtocolStub.requestHandler = { request in
            let response = HTTPURLResponse(
                url: try XCTUnwrap(request.url),
                statusCode: 200,
                httpVersion: nil,
                headerFields: ["Content-Type": "application/json"]
            )!
            let data = """
            {
              "Id": "conv-1",
              "AgentId": "coder",
              "Title": "iOS QA",
              "ConversationParentId": "parent-1",
              "ConversationParentTurnId": "turn-9",
              "Shareable": 1
            }
            """.data(using: .utf8)!
            return (response, data)
        }

        let result = try await client.createConversation(input)

        XCTAssertEqual(result.id, "conv-1")
        XCTAssertEqual(result.conversationParentID, "parent-1")
        XCTAssertEqual(result.conversationParentTurnID, "turn-9")
        XCTAssertEqual(result.shareable, 1)
        URLProtocolStub.requestHandler = nil
    }

    func testListPendingToolApprovalsPageDecodesRowsAndPagination() async throws {
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [URLProtocolStub.self]
        let session = URLSession(configuration: configuration)
        let endpoint = EndpointConfig(baseURL: try XCTUnwrap(URL(string: "http://localhost:8585")))
        let client = AgentlyClient(endpoints: ["appAPI": endpoint], session: session)

        let expectation = expectation(description: "pending approvals request captured")
        URLProtocolStub.requestHandler = { request in
            let url = try XCTUnwrap(request.url)
            let components = try XCTUnwrap(URLComponents(url: url, resolvingAgainstBaseURL: false))
            let items = components.queryItems ?? []
            func value(for name: String) -> String? {
                items.first(where: { $0.name == name })?.value
            }
            XCTAssertEqual(value(for: "conversationId"), "conv-3")
            XCTAssertEqual(value(for: "status"), "pending")
            XCTAssertEqual(value(for: "limit"), "5")
            XCTAssertEqual(value(for: "offset"), "5")
            expectation.fulfill()
            let response = HTTPURLResponse(url: url, statusCode: 200, httpVersion: nil, headerFields: [
                "Content-Type": "application/json"
            ])!
            let data = """
            {
              "rows": [
                {
                  "id": "approval-3",
                  "userId": "user-1",
                  "conversationId": "conv-3",
                  "turnId": "turn-1",
                  "toolName": "system.exec",
                  "status": "pending",
                  "createdAt": "2026-05-06T10:00:00Z"
                }
              ],
              "total": 11,
              "offset": 5,
              "limit": 5,
              "hasMore": true
            }
            """.data(using: .utf8)!
            return (response, data)
        }

        let result = try await client.listPendingToolApprovalsPage(
            ListPendingToolApprovalsInput(
                conversationID: "conv-3",
                status: "pending",
                limit: 5,
                offset: 5
            )
        )

        XCTAssertEqual(result.rows.count, 1)
        XCTAssertEqual(result.rows.first?.id, "approval-3")
        XCTAssertEqual(result.rows.first?.userID, "user-1")
        XCTAssertEqual(result.total, 11)
        XCTAssertEqual(result.offset, 5)
        XCTAssertEqual(result.limit, 5)
        XCTAssertTrue(result.hasMore)
        await fulfillment(of: [expectation], timeout: 2.0)
        URLProtocolStub.requestHandler = nil
    }

    func testApprovalCallbackResultDecodesCanonicalCallbackContract() throws {
        let json = """
        {
          "allow": true,
          "message": "approved",
          "payload": {
            "action": "approve"
          }
        }
        """

        let data = try XCTUnwrap(json.data(using: .utf8))
        let decoded = try JSONDecoder.agently().decode(ApprovalCallbackResult.self, from: data)

        XCTAssertEqual(decoded.allow, true)
        XCTAssertEqual(decoded.message, "approved")
        XCTAssertEqual(decoded.payload["action"], .string("approve"))
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
        _ = await tracker.apply(SSEEvent(data: #"{"type":"narration","conversationId":"conv-1","turnId":"turn-1","assistantMessageId":"msg-1","content":"Thinking...","status":"running"}"#))
        _ = await tracker.apply(SSEEvent(data: #"{"type":"text_delta","conversationId":"conv-1","turnId":"turn-1","assistantMessageId":"msg-1","content":"Hello "}"#))
        let snapshot = await tracker.apply(SSEEvent(data: #"{"type":"assistant","conversationId":"conv-1","turnId":"turn-1","messageId":"msg-1","content":"Hello world","status":"completed","narration":"Thinking...","patch":{"role":"assistant"}}"#))

        XCTAssertEqual(snapshot.conversationID, "conv-1")
        XCTAssertEqual(snapshot.activeTurnID, "turn-1")
        XCTAssertEqual(snapshot.bufferedMessages.count, 1)
        XCTAssertEqual(snapshot.bufferedMessages.first?.id, "msg-1")
        XCTAssertEqual(snapshot.bufferedMessages.first?.narration, "Thinking...")
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
        _ = await tracker.apply(SSEEvent(data: #"{"type":"assistant","conversationId":"conv-1","turnId":"turn-1","messageId":"msg-1","content":"First conversation","status":"completed","patch":{"role":"assistant"}}"#))
        let snapshot = await tracker.apply(SSEEvent(data: #"{"type":"assistant","conversationId":"conv-2","turnId":"turn-9","messageId":"msg-9","content":"Wrong conversation","status":"completed","patch":{"role":"assistant"}}"#))

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
