import XCTest
@testable import AgentlySDK

final class AgentlySDKTests: XCTestCase {
    func testLookupFlatteningToleratesDuplicateRegistryNames() throws {
        let registry = try JSONDecoder().decode([LookupRegistryEntry].self, from: Data(#"""
        [
          {"name":"order","title":"Order","dataSource":"orders","required":true,"token":{"modelForm":"first ${id}"}},
          {"name":"order","title":"Ad Order","dataSource":"orders-v2","required":true,"token":{"modelForm":"order ${id}"}}
        ]
        """#.utf8))

        XCTAssertEqual(
            LookupTokens.flattenStored(#"Troubleshoot @{order:2667501 "Order 2667501"}"#, registry: registry),
            "Troubleshoot order 2667501"
        )
    }

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

    final class MemoryCookieStorage: AgentlySessionCookieStorage, @unchecked Sendable {
        private var values: [StoredSessionCookie] = []

        func load() -> [StoredSessionCookie] {
            values
        }

        func save(_ cookies: [StoredSessionCookie]) {
            values = cookies
        }
    }

    private func requestBodyString(_ request: URLRequest) -> String? {
        if let body = request.httpBody {
            return String(data: body, encoding: .utf8)
        }
        guard let stream = request.httpBodyStream else {
            return nil
        }
        stream.open()
        defer { stream.close() }
        let bufferSize = 1024
        let buffer = UnsafeMutablePointer<UInt8>.allocate(capacity: bufferSize)
        defer { buffer.deallocate() }
        var data = Data()
        while stream.hasBytesAvailable {
            let read = stream.read(buffer, maxLength: bufferSize)
            if read <= 0 { break }
            data.append(buffer, count: read)
        }
        return String(data: data, encoding: .utf8)
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

    func testOAuthConfigDecodesNullScopesAsEmpty() throws {
        let json = """
        {
          "clientID": "",
          "configURL": "fixture-auth-config",
          "discoveryURL": "",
          "mode": "bff",
          "redirectSameTab": true,
          "redirectURI": "",
          "scopes": null,
          "usePopupLogin": false
        }
        """

        let output = try JSONDecoder.agently().decode(
            OAuthConfigOutput.self,
            from: XCTUnwrap(json.data(using: .utf8))
        )

        XCTAssertEqual(output.scopes, [])
    }

    func testOAuthInitiatePostsMobileRedirectURIWhenProvided() async throws {
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [URLProtocolStub.self]
        let session = URLSession(configuration: configuration)
        let client = AgentlyClient(
            endpoints: ["appAPI": EndpointConfig(baseURL: try XCTUnwrap(URL(string: "http://localhost:9292")))],
            session: session
        )
        URLProtocolStub.requestHandler = { request in
            XCTAssertEqual(request.url?.path, "/v1/api/auth/oauth/initiate")
            XCTAssertEqual(request.httpMethod, "POST")
            let body = try XCTUnwrap(self.requestBodyString(request))
            XCTAssertTrue(body.contains(#""redirectURI":"agently-ios:\/\/oauth\/callback""#))
            XCTAssertTrue(body.contains(#""scopes":["XXX_WEBUI"]"#))
            let response = HTTPURLResponse(
                url: try XCTUnwrap(request.url),
                statusCode: 200,
                httpVersion: nil,
                headerFields: [:]
            )!
            let data = #"{"authURL":"https://idp.example.test/authorize","redirectURI":"agently-ios://oauth/callback"}"#.data(using: .utf8)!
            return (response, data)
        }

        let output = try await client.oauthInitiate(
            OAuthInitiateInput(redirectURI: "agently-ios://oauth/callback", scopes: ["XXX_WEBUI"])
        )

        XCTAssertEqual(output.redirectURI, "agently-ios://oauth/callback")
        URLProtocolStub.requestHandler = nil
    }

    func testOAuthMobileInitiatePostsMobileEndpoint() async throws {
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [URLProtocolStub.self]
        let session = URLSession(configuration: configuration)
        let client = AgentlyClient(
            endpoints: ["appAPI": EndpointConfig(baseURL: try XCTUnwrap(URL(string: "http://localhost:9292")))],
            session: session
        )
        URLProtocolStub.requestHandler = { request in
            XCTAssertEqual(request.url?.path, "/v1/api/auth/oauth/mobile/initiate")
            XCTAssertEqual(request.httpMethod, "POST")
            let body = try XCTUnwrap(self.requestBodyString(request))
            XCTAssertTrue(body.contains(#""redirectURI":"agently-ios:\/\/oauth\/callback""#))
            XCTAssertTrue(body.contains(#""scopes":["XXX_MOBILEUI"]"#))
            let response = HTTPURLResponse(
                url: try XCTUnwrap(request.url),
                statusCode: 200,
                httpVersion: nil,
                headerFields: [:]
            )!
            let data = #"{"authURL":"https://idp.example.test/authorize","redirectURI":"agently-ios://oauth/callback","pkce":true,"mobile":true}"#.data(using: .utf8)!
            return (response, data)
        }

        let output = try await client.oauthMobileInitiate(
            OAuthInitiateInput(redirectURI: "agently-ios://oauth/callback", scopes: ["XXX_MOBILEUI"])
        )

        XCTAssertEqual(output.redirectURI, "agently-ios://oauth/callback")
        URLProtocolStub.requestHandler = nil
    }

    func testPersistentSessionCookieStoreReplaysCookieForFreshClient() async throws {
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [URLProtocolStub.self]
        configuration.httpCookieStorage = HTTPCookieStorage()
        let endpoint = EndpointConfig(baseURL: try XCTUnwrap(URL(string: "https://steward.agently.viantinc.com")))
        let storage = MemoryCookieStorage()
        let firstClient = AgentlyClient(
            endpoints: ["appAPI": endpoint],
            session: URLSession(configuration: configuration),
            sessionCookieStore: AgentlyPersistentSessionCookieStore(storage: storage)
        )
        var seenCookieHeader: String?
        var responseIndex = 0
        URLProtocolStub.requestHandler = { request in
            responseIndex += 1
            if responseIndex == 2 {
                seenCookieHeader = request.value(forHTTPHeaderField: "Cookie")
            }
            let headers = responseIndex == 1
                ? ["Content-Type": "application/json", "Set-Cookie": "agently_session=session-123; Path=/; HttpOnly; Secure"]
                : ["Content-Type": "application/json"]
            let response = HTTPURLResponse(
                url: try XCTUnwrap(request.url),
                statusCode: 200,
                httpVersion: nil,
                headerFields: headers
            )!
            return (response, Data(#"{"id":"user-1"}"#.utf8))
        }
        defer { URLProtocolStub.requestHandler = nil }

        _ = try await firstClient.authMe()
        let secondClient = AgentlyClient(
            endpoints: ["appAPI": endpoint],
            session: URLSession(configuration: configuration),
            sessionCookieStore: AgentlyPersistentSessionCookieStore(storage: storage)
        )
        _ = try await secondClient.authMe()

        XCTAssertEqual(seenCookieHeader, "agently_session=session-123")
    }

    func testClearSessionCookiesRemovesPersistedCookie() async throws {
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [URLProtocolStub.self]
        configuration.httpCookieStorage = HTTPCookieStorage()
        let endpoint = EndpointConfig(baseURL: try XCTUnwrap(URL(string: "https://steward.agently.viantinc.com")))
        let storage = MemoryCookieStorage()
        let client = AgentlyClient(
            endpoints: ["appAPI": endpoint],
            session: URLSession(configuration: configuration),
            sessionCookieStore: AgentlyPersistentSessionCookieStore(storage: storage)
        )
        URLProtocolStub.requestHandler = { request in
            let response = HTTPURLResponse(
                url: try XCTUnwrap(request.url),
                statusCode: 200,
                httpVersion: nil,
                headerFields: ["Content-Type": "application/json", "Set-Cookie": "agently_session=session-123; Path=/; HttpOnly; Secure"]
            )!
            return (response, Data(#"{"id":"user-1"}"#.utf8))
        }
        defer { URLProtocolStub.requestHandler = nil }

        _ = try await client.authMe()
        XCTAssertEqual(storage.load().count, 1)

        client.clearSessionCookies()

        XCTAssertTrue(storage.load().isEmpty)
    }

    func testOAuthMobileCallbackPostsMobileEndpoint() async throws {
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [URLProtocolStub.self]
        let session = URLSession(configuration: configuration)
        let client = AgentlyClient(
            endpoints: ["appAPI": EndpointConfig(baseURL: try XCTUnwrap(URL(string: "http://localhost:9292")))],
            session: session
        )
        URLProtocolStub.requestHandler = { request in
            XCTAssertEqual(request.url?.path, "/v1/api/auth/oauth/mobile/callback")
            XCTAssertEqual(request.httpMethod, "POST")
            let body = try XCTUnwrap(self.requestBodyString(request))
            XCTAssertTrue(body.contains(#""code":"code-1""#))
            XCTAssertTrue(body.contains(#""state":"state-1""#))
            let response = HTTPURLResponse(
                url: try XCTUnwrap(request.url),
                statusCode: 200,
                httpVersion: nil,
                headerFields: [:]
            )!
            let data = #"{"status":"ok","sessionId":"session-1"}"#.data(using: .utf8)!
            return (response, data)
        }

        let output = try await client.oauthMobileCallback(
            OAuthCallbackInput(code: "code-1", state: "state-1")
        )

        XCTAssertEqual(output.sessionID, "session-1")
        URLProtocolStub.requestHandler = nil
    }

    func testHostedWorkspaceRestorePreservesWorkspaceAcrossLaterNonWorkspaceTurn() throws {
        let json = """
        {
          "conversation": {
            "conversationId": "conv-1",
            "turns": [
              {
                "turnId": "turn-1",
                "execution": {
                  "pages": [
                    {
                      "pageId": "page-1",
                      "toolSteps": [
                        {
                          "toolCallId": "tool-1",
                          "toolName": "ui/window/list",
                          "status": "completed",
                          "responsePayload": {
                            "items": [
                              {
                                "windowId": "legacy__conv-1",
                                "conversationId": "conv-1",
                                "windowKey": "report",
                                "presentation": "hosted",
                                "region": "chat.top",
                                "parentKey": "chat/new"
                              }
                            ]
                          }
                        }
                      ]
                    }
                  ]
                }
              },
              {
                "turnId": "turn-2",
                "execution": {
                  "pages": [
                    {
                      "pageId": "page-2",
                      "toolSteps": [
                        {
                          "toolCallId": "tool-2",
                          "toolName": "message/reply",
                          "status": "completed"
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
        let response = try JSONDecoder.agently().decode(
            ConversationStateResponse.self,
            from: XCTUnwrap(json.data(using: .utf8))
        )

        let restored = deriveHostedWorkspaceRestoreState(from: response)
        XCTAssertEqual(restored?.selectedWindowId, "legacy__conv-1")
        XCTAssertEqual(restored?.windows.first?.windowId, "legacy__conv-1")
    }

    func testHostedWorkspaceRestoreUsesLiveStreamPayloads() throws {
        let snapshot = ConversationStreamSnapshot(
            conversationID: "conv-1",
            activeTurnID: "turn-1",
            liveExecutionGroupsByID: [
                "assistant-1": LiveExecutionGroup(
                    pageID: "page-1",
                    assistantMessageID: "assistant-1",
                    turnID: "turn-1",
                    toolSteps: [
                        LiveToolStepState(
                            toolCallID: "tool-1",
                            toolName: "ui/view/open",
                            status: "completed",
                            requestPayload: .object([
                                "id": .string("reportWindow")
                            ]),
                            responsePayload: .object([
                                "windowId": .string("reportWindow__conv-1"),
                                "conversationId": .string("conv-1"),
                                "windowKey": .string("reportWindow"),
                                "windowTitle": .string("Report Review"),
                                "presentation": .string("hosted"),
                                "region": .string("chat.top"),
                                "parentKey": .string("chat/new")
                            ])
                        )
                    ]
                )
            ]
        )

        let restore = deriveHostedWorkspaceRestoreState(from: snapshot)

        XCTAssertEqual(restore?.selectedWindowId, "reportWindow__conv-1")
        XCTAssertEqual(restore?.windows.first?.windowTitle, "Report Review")
    }

    func testHostedWorkspaceRestoreUsesWindowOpenPayloads() throws {
        let json = """
        {
          "conversation": {
            "conversationId": "conv-1",
            "turns": [
              {
                "turnId": "turn-1",
                "execution": {
                  "pages": [
                    {
                      "pageId": "page-1",
                      "toolSteps": [
                        {
                          "toolCallId": "tool-open",
                          "toolName": "ui/window/open",
                          "status": "completed",
                          "requestPayload": {
                            "windowKey": "reportBuilder",
                            "parameters": {
                              "reportBuilderRef": "capacityBuilder"
                            }
                          },
                          "responsePayload": {
                            "windowId": "reportBuilder__conv-1",
                            "conversationId": "conv-1",
                            "windowKey": "reportBuilder",
                            "windowTitle": "Capacity Builder",
                            "presentation": "hosted",
                            "region": "chat.top",
                            "parentKey": "chat/new",
                            "workspaceMinHeight": 500,
                            "workspaceSharePct": 72
                          }
                        },
                        {
                          "toolCallId": "tool-prefill",
                          "toolName": "ui/window:setFormData",
                          "status": "completed",
                          "requestPayload": {
                            "windowId": "reportBuilder__conv-1",
                            "values": {
                              "prefill": {
                                "scope": {
                                  "targetKey": "record:12345"
                                }
                              }
                            }
                          },
                          "responsePayload": {
                            "windowId": "reportBuilder__conv-1",
                            "windowForm": {
                              "reportBuilderRef": "capacityBuilder",
                              "prefill": {
                                "scope": {
                                  "targetKey": "record:12345"
                                }
                              }
                            }
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
        let response = try JSONDecoder.agently().decode(
            ConversationStateResponse.self,
            from: XCTUnwrap(json.data(using: .utf8))
        )

        let restore = deriveHostedWorkspaceRestoreState(from: response)
        let window = restore?.windows.first

        XCTAssertEqual(restore?.selectedWindowId, "reportBuilder__conv-1")
        XCTAssertEqual(window?.windowKey, "reportBuilder")
        XCTAssertEqual(window?.windowTitle, "Capacity Builder")
        XCTAssertEqual(window?.workspaceMinHeight, 500)
        XCTAssertEqual(window?.workspaceSharePct, 72)
        XCTAssertEqual(window?.windowForm?["reportBuilderRef"], .string("capacityBuilder"))
        guard case .object(let prefill)? = window?.windowForm?["prefill"],
              case .object(let scope)? = prefill["scope"] else {
            XCTFail("Expected restored report builder prefill scope")
            return
        }
        XCTAssertEqual(scope["targetKey"], .string("record:12345"))
    }

    func testHostedWorkspaceRestoreDecodesPersistedPayloadEnvelopes() throws {
        let openResponse = #"{"windowId":"forecastingCubeBuilder__conv-1","selectedWindowId":"forecastingCubeBuilder__conv-1","windowKey":"reportBuilder","windowTitle":"Forecasting","conversationId":"conv-1","presentation":"hosted","region":"chat.top","parentKey":"chat/new","workspaceSharePct":72,"workspaceMinHeight":500,"parameters":{"reportBuilderRef":"forecastingCubeBuilder"},"items":[{"windowId":"forecastingCubeBuilder__conv-1","windowKey":"reportBuilder","windowTitle":"Forecasting","conversationId":"conv-1","presentation":"hosted","region":"chat.top","parentKey":"chat/new","workspaceSharePct":72,"workspaceMinHeight":500,"parameters":{"reportBuilderRef":"forecastingCubeBuilder"}}],"ok":true}"#
        let content = try XCTUnwrap(String(data: try JSONEncoder().encode(openResponse), encoding: .utf8))
        let json = """
        {
          "conversation": {
            "conversationId": "conv-1",
            "turns": [{
              "turnId": "turn-1",
              "execution": {"pages": [{
                "pageId": "page-1",
                "toolSteps": [{
                  "toolCallId": "tool-open",
                  "toolName": "ui/view/open",
                  "status": "completed",
                  "content": \(content),
                  "responsePayload": {
                    "Id": "payload-1",
                    "InlineBody": \(content),
                    "Compression": "none"
                  }
                }]
              }]}
            }]
          }
        }
        """
        let response = try JSONDecoder.agently().decode(
            ConversationStateResponse.self,
            from: XCTUnwrap(json.data(using: .utf8))
        )

        let restore = deriveHostedWorkspaceRestoreState(from: response)

        XCTAssertEqual(restore?.selectedWindowId, "forecastingCubeBuilder__conv-1")
        XCTAssertEqual(restore?.windows.first?.parameters?["reportBuilderRef"], .string("forecastingCubeBuilder"))
    }

    func testHostedWorkspaceRestoreFoldsLaterWindowFormDataIntoOpenedWindow() throws {
        let json = """
        {
          "conversation": {
            "conversationId": "conv-1",
            "turns": [
              {
                "turnId": "turn-1",
                "execution": {
                  "pages": [
                    {
                      "pageId": "page-1",
                      "toolSteps": [
                        {
                          "toolCallId": "tool-open",
                          "toolName": "ui/view/open",
                          "status": "completed",
                          "requestPayload": {
                            "id": "reportWindow"
                          },
                          "responsePayload": {
                            "windowId": "reportWindow__conv-1",
                            "conversationId": "conv-1",
                            "windowKey": "reportWindow",
                            "windowTitle": "Report Review",
                            "presentation": "hosted",
                            "region": "chat.top",
                            "parentKey": "chat/new",
                            "windowForm": {
                              "prefill": {
                                "accountId": 7
                              }
                            }
                          }
                        },
                        {
                          "toolCallId": "tool-form",
                          "toolName": "ui/window:setFormData",
                          "status": "completed",
                          "requestPayload": {
                            "windowKey": "reportWindow",
                            "values": {
                              "prefill": {
                                "recordId": 123
                              }
                            }
                          },
                          "responsePayload": {
                            "ok": true
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
        let response = try JSONDecoder.agently().decode(
            ConversationStateResponse.self,
            from: XCTUnwrap(json.data(using: .utf8))
        )

        let restore = deriveHostedWorkspaceRestoreState(from: response)

        XCTAssertEqual(restore?.selectedWindowId, "reportWindow__conv-1")
        guard case .object(let prefill)? = restore?.windows.first?.windowForm?["prefill"] else {
            XCTFail("Expected restored window form prefill object")
            return
        }
        XCTAssertEqual(prefill["accountId"], .number(7))
        XCTAssertEqual(prefill["recordId"], .number(123))
    }

    func testHostedWorkspaceRestoreFoldsInlineBodyWindowFormDataIntoOpenedWindow() throws {
        let openRequest = #"{"clientId":"ios-ui","id":"reportBuilder","openMode":"show","parameters":{}}"#
        let openResponse = #"{"clientId":"ios-ui","conversationId":"conv-1","items":[{"conversationId":"conv-1","parentKey":"chat/new","presentation":"hosted","region":"chat.top","windowId":"reportBuilder__conv-1","windowKey":"reportBuilder","windowTitle":"Report Builder","workspaceMinHeight":500,"workspaceSharePct":72}],"ok":true,"selectedWindowId":"reportBuilder__conv-1","windowId":"reportBuilder__conv-1","windowKey":"reportBuilder","windowTitle":"Report Builder"}"#
        let formRequest = #"{"clientId":"ios-ui","values":{"prefill":{"country":["US"],"recordIds":[123],"scope":{"workspaceIds":[456]}}},"windowId":"reportBuilder__conv-1"}"#
        let formResponse = #"{"clientId":"ios-ui","ok":true}"#
        let json = """
        {
          "conversation": {
            "conversationId": "conv-1",
            "turns": [
              {
                "turnId": "turn-1",
                "execution": {
                  "pages": [
                    {
                      "pageId": "page-1",
                      "toolSteps": [
                        {
                          "toolCallId": "tool-open",
                          "toolName": "ui/view/open",
                          "status": "completed",
                          "requestPayload": { "Id": "open-req", "InlineBody": \(String(reflecting: openRequest)), "Compression": "" },
                          "responsePayload": { "Id": "open-res", "InlineBody": \(String(reflecting: openResponse)), "Compression": "" }
                        },
                        {
                          "toolCallId": "tool-form",
                          "toolName": "ui/window/setFormData",
                          "status": "completed",
                          "requestPayload": { "Id": "form-req", "InlineBody": \(String(reflecting: formRequest)), "Compression": "" },
                          "responsePayload": { "Id": "form-res", "InlineBody": \(String(reflecting: formResponse)), "Compression": "" }
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
        let response = try JSONDecoder.agently().decode(
            ConversationStateResponse.self,
            from: XCTUnwrap(json.data(using: .utf8))
        )

        let restore = deriveHostedWorkspaceRestoreState(from: response)

        XCTAssertEqual(restore?.selectedWindowId, "reportBuilder__conv-1")
        guard case .object(let prefill)? = restore?.windows.first?.windowForm?["prefill"] else {
            XCTFail("Expected restored report builder prefill object")
            return
        }
        XCTAssertEqual(prefill["country"], .array([.string("US")]))
        XCTAssertEqual(prefill["recordIds"], .array([.number(123)]))
    }

    func testHostedWorkspaceRestoreUsesLaterWindowGetAsAuthoritativeLegacyFormSnapshot() throws {
        let json = """
        {
          "conversation": {
            "conversationId": "conv-1",
            "turns": [{
              "turnId": "turn-1",
              "execution": {"pages": [{
                "pageId": "page-1",
                "toolSteps": [
                  {
                    "toolCallId": "tool-open",
                    "toolName": "ui/view/open",
                    "status": "completed",
                    "responsePayload": {
                      "windowId": "reportBuilder__conv-1",
                      "conversationId": "conv-1",
                      "windowKey": "reportBuilder",
                      "windowTitle": "Performance Metrics",
                      "presentation": "hosted",
                      "region": "chat.top",
                      "parentKey": "chat/new"
                    }
                  },
                  {
                    "toolCallId": "tool-form",
                    "toolName": "ui/window/setFormData",
                    "status": "completed",
                    "requestPayload": {
                      "Id": "compressed-request",
                      "InlineBody": "not-json-gzip-content",
                      "Compression": "gzip"
                    },
                    "responsePayload": {"ok": true}
                  },
                  {
                    "toolCallId": "tool-get",
                    "toolName": "ui/window/get",
                    "status": "completed",
                    "responsePayload": {
                      "window": {
                        "windowId": "reportBuilder__conv-1",
                        "conversationId": "conv-1",
                        "windowKey": "reportBuilder",
                        "windowTitle": "Performance Metrics",
                        "presentation": "hosted",
                        "region": "chat.top",
                        "parentKey": "chat/new",
                        "parameters": {"reportBuilderRef": "metricsCubeBuilder"},
                        "metadata": {
                          "reportBuilder": {
                            "builderRef": "metricsCubeBuilder",
                            "dataSources": [
                              {"id": "delivery_summary", "dataSourceRef": "metrics_ad_cube_report"}
                            ]
                          }
                        },
                        "windowForm": {
                          "reportBuilder:metricsCubeBuilder": {
                            "reportDocumentBlocks": [
                              {"id": "overview", "kind": "sectionBlock", "title": "Overview"}
                            ],
                            "reportDocument": {
                              "title": "Order Performance Report",
                              "blocks": [
                                {"id": "overview", "kind": "sectionBlock", "title": "Overview"}
                              ]
                            }
                          }
                        }
                      }
                    }
                  }
                ]
              }]}
            }]
          }
        }
        """
        let response = try JSONDecoder.agently().decode(
            ConversationStateResponse.self,
            from: XCTUnwrap(json.data(using: .utf8))
        )

        let restore = deriveHostedWorkspaceRestoreState(from: response)
        let form = try XCTUnwrap(restore?.windows.first?.windowForm)
        guard case .object(let builder)? = form["reportBuilder:metricsCubeBuilder"],
              case .object(let document)? = builder["reportDocument"],
              case .array(let blocks)? = builder["reportDocumentBlocks"] else {
            XCTFail("Expected the authoritative report builder snapshot")
            return
        }

        XCTAssertEqual(document["title"], .string("Order Performance Report"))
        XCTAssertEqual(blocks.count, 1)
        guard case .object(let metadata)? = form["__agentlyWindowMetadata"],
              case .object(let reportBuilder)? = metadata["reportBuilder"],
              case .array(let dataSources)? = reportBuilder["dataSources"] else {
            XCTFail("Expected the published window metadata to survive restoration")
            return
        }
        XCTAssertEqual(dataSources.count, 1)
    }

    func testHostedWorkspaceRestoreFallsBackToPlainWindowGetContentWhenPayloadIsCompressed() throws {
        let windowGetContent = #"{"window":{"windowId":"reportBuilder__conv-1","conversationId":"conv-1","windowKey":"reportBuilder","windowTitle":"Performance Metrics","presentation":"hosted","region":"chat.top","parentKey":"chat/new","parameters":{"reportBuilderRef":"metricsCubeBuilder"},"windowForm":{"reportBuilder:metricsCubeBuilder":{"reportDocumentBlocks":[{"id":"overview","kind":"sectionBlock","title":"Overview"}],"reportDocument":{"title":"Order Performance Report","blocks":[{"id":"overview","kind":"sectionBlock","title":"Overview"}]}}}}}"#
        let encodedContent = try XCTUnwrap(String(data: JSONEncoder().encode(windowGetContent), encoding: .utf8))
        let json = """
        {
          "conversation": {
            "conversationId": "conv-1",
            "turns": [{
              "turnId": "turn-1",
              "execution": {"pages": [{
                "pageId": "page-1",
                "toolSteps": [
                  {
                    "toolCallId": "tool-open",
                    "toolName": "ui/view/open",
                    "status": "completed",
                    "responsePayload": {
                      "windowId": "reportBuilder__conv-1",
                      "conversationId": "conv-1",
                      "windowKey": "reportBuilder",
                      "windowTitle": "Performance Metrics",
                      "presentation": "hosted",
                      "region": "chat.top",
                      "parentKey": "chat/new"
                    }
                  },
                  {
                    "toolCallId": "tool-get",
                    "toolName": "ui/window/get",
                    "status": "completed",
                    "responsePayload": {
                      "Id": "compressed-response",
                      "InlineBody": "not-json-gzip-content",
                      "Compression": "gzip"
                    },
                    "content": \(encodedContent)
                  }
                ]
              }]}
            }]
          }
        }
        """
        let response = try JSONDecoder.agently().decode(
            ConversationStateResponse.self,
            from: XCTUnwrap(json.data(using: .utf8))
        )

        let restore = deriveHostedWorkspaceRestoreState(from: response)
        let form = try XCTUnwrap(restore?.windows.first?.windowForm)
        guard case .object(let builder)? = form["reportBuilder:metricsCubeBuilder"],
              case .object(let document)? = builder["reportDocument"] else {
            XCTFail("Expected the plain transcript content to restore the authored report")
            return
        }
        XCTAssertEqual(document["title"], .string("Order Performance Report"))
    }

    func testHostedWorkspaceRestoreDoesNotRequireAppPlacementFields() throws {
        let json = """
        {
          "conversation": {
            "conversationId": "conv-1",
            "turns": [
              {
                "turnId": "turn-1",
                "execution": {
                  "pages": [
                    {
                      "pageId": "page-1",
                      "toolSteps": [
                        {
                          "toolCallId": "tool-1",
                          "toolName": "ui/view/open",
                          "status": "completed",
                          "responsePayload": {
                            "windowId": "generic__conv-1",
                            "windowKey": "generic-report",
                            "windowTitle": "Generic Report"
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
        let response = try JSONDecoder.agently().decode(
            ConversationStateResponse.self,
            from: XCTUnwrap(json.data(using: .utf8))
        )

        let restore = deriveHostedWorkspaceRestoreState(from: response)

        XCTAssertEqual(restore?.selectedWindowId, "generic__conv-1")
        XCTAssertEqual(restore?.windows.first?.windowKey, "generic-report")
    }

    func testHostedWorkspaceRestoreIgnoresLiveGroupsWithoutActiveTurn() throws {
        let snapshot = ConversationStreamSnapshot(
            conversationID: "conv-1",
            activeTurnID: nil,
            liveExecutionGroupsByID: [
                "assistant-old": LiveExecutionGroup(
                    pageID: "page-old",
                    assistantMessageID: "assistant-old",
                    turnID: "turn-old",
                    toolSteps: [
                        LiveToolStepState(
                            toolCallID: "tool-old",
                            toolName: "ui/view/open",
                            status: "completed",
                            responsePayload: .object([
                                "windowId": .string("old__conv-1"),
                                "windowKey": .string("old-report")
                            ])
                        )
                    ]
                )
            ]
        )

        XCTAssertNil(deriveHostedWorkspaceRestoreState(from: snapshot))
    }

    func testConversationStreamTrackerPreservesToolResponsePayload() async throws {
        let tracker = ConversationStreamTracker()
        let payload = """
        {
          "type": "tool_call_completed",
          "conversationId": "conv-1",
          "turnId": "turn-1",
          "assistantMessageId": "assistant-1",
          "toolCallId": "tool-1",
          "toolName": "ui/view/open",
          "responsePayload": {
            "windowId": "reportWindow__conv-1",
            "conversationId": "conv-1",
            "windowKey": "reportWindow",
            "presentation": "hosted"
          }
        }
        """

        let snapshot = await tracker.apply(SSEEvent(data: payload))
        let step = snapshot.liveExecutionGroupsByID["assistant-1"]?.toolSteps.first

        XCTAssertEqual(step?.status, "completed")
        XCTAssertEqual(
            step?.responsePayload,
            .object([
                "windowId": .string("reportWindow__conv-1"),
                "conversationId": .string("conv-1"),
                "windowKey": .string("reportWindow"),
                "presentation": .string("hosted")
            ])
        )
    }

    func testConversationStreamTrackerPreservesNonObjectToolPayloads() async throws {
        let tracker = ConversationStreamTracker()
        let payload = """
        {
          "type": "tool_call_completed",
          "conversationId": "conv-1",
          "turnId": "turn-1",
          "assistantMessageId": "assistant-1",
          "toolCallId": "tool-1",
          "toolName": "ui/window/show",
          "arguments": "window-1",
          "responsePayload": ["ok", true]
        }
        """

        let snapshot = await tracker.apply(SSEEvent(data: payload))
        let step = snapshot.liveExecutionGroupsByID["assistant-1"]?.toolSteps.first

        XCTAssertEqual(step?.requestPayload, .string("window-1"))
        XCTAssertEqual(step?.responsePayload, .array([.string("ok"), .bool(true)]))
    }

    func testConversationStreamTrackerMergesToolTurnIDIntoExistingGroup() async throws {
        let tracker = ConversationStreamTracker()

        _ = await tracker.apply(SSEEvent(data: #"{"type":"model_started","conversationId":"conv-1","assistantMessageId":"assistant-1","model":{"provider":"openai","model":"gpt-5-mini"}}"#))
        let snapshot = await tracker.apply(SSEEvent(data: #"{"type":"tool_call_completed","conversationId":"conv-1","turnId":"turn-1","assistantMessageId":"assistant-1","toolCallId":"tool-1","toolName":"ui/view/open","responsePayload":{"windowId":"window-1","windowKey":"report"}}"#))

        XCTAssertEqual(snapshot.liveExecutionGroupsByID["assistant-1"]?.turnID, "turn-1")
    }

    func testConversationStreamTrackerPreservesToolOrderWhenMergingUpdates() async throws {
        let tracker = ConversationStreamTracker()

        _ = await tracker.apply(SSEEvent(data: #"{"type":"tool_call_started","conversationId":"conv-1","turnId":"turn-1","assistantMessageId":"assistant-1","toolCallId":"tool-1","toolName":"first-tool"}"#))
        _ = await tracker.apply(SSEEvent(data: #"{"type":"tool_call_started","conversationId":"conv-1","turnId":"turn-1","assistantMessageId":"assistant-1","toolCallId":"tool-2","toolName":"second-tool"}"#))
        let snapshot = await tracker.apply(SSEEvent(data: #"{"type":"tool_call_completed","conversationId":"conv-1","turnId":"turn-1","assistantMessageId":"assistant-1","toolCallId":"tool-1","toolName":"first-tool","responsePayload":{"ok":true}}"#))

        let steps = try XCTUnwrap(snapshot.liveExecutionGroupsByID["assistant-1"]?.toolSteps)
        XCTAssertEqual(steps.map { $0.toolCallID ?? "" }, ["tool-1", "tool-2"])
        XCTAssertEqual(steps.map { $0.status ?? "" }, ["completed", "running"])
    }

    func testUIBridgeRPCClientOmitsSessionHeaderForExplicitCommandPlane() async throws {
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [URLProtocolStub.self]
        let session = URLSession(configuration: configuration)
        let client = AgentlyClient(
            endpoints: ["appAPI": EndpointConfig(baseURL: URL(string: "http://localhost:9292")!)],
            session: session
        )
        let bridge = UIBridgeRPCClient(client: client)
        var requestCount = 0
        URLProtocolStub.requestHandler = { request in
            requestCount += 1
            XCTAssertEqual(request.url?.path, "/v1/ui/rpc")
            XCTAssertEqual(request.httpMethod, "POST")
            let body = try XCTUnwrap(self.requestBodyString(request))
            if requestCount == 1 {
                XCTAssertFalse(body.contains("ui.snapshot"))
                XCTAssertNil(request.value(forHTTPHeaderField: "Mcp-Session-Id"))
                let response = HTTPURLResponse(
                    url: try XCTUnwrap(request.url),
                    statusCode: 200,
                    httpVersion: nil,
                    headerFields: ["Mcp-Session-Id": "session-123"]
                )!
                let data = #"{"jsonrpc":"2.0","result":{"accepted":true}}"#.data(using: .utf8)!
                return (response, data)
            }
            XCTAssertTrue(body.contains("ui.snapshot"))
            XCTAssertNil(request.value(forHTTPHeaderField: "Mcp-Session-Id"))
            let response = HTTPURLResponse(
                url: try XCTUnwrap(request.url),
                statusCode: 200,
                httpVersion: nil,
                headerFields: [:]
            )!
            let data = #"{"jsonrpc":"2.0","result":{"ok":true}}"#.data(using: .utf8)!
            return (response, data)
        }

        _ = try await bridge.hello(clientID: "ios-ui-test")
        _ = try await bridge.snapshot(
            clientID: "ios-ui-test",
            data: .object(["windows": .array([])])
        )

        XCTAssertEqual(requestCount, 2)
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

    func testQueryOutputDefaultsMissingWarnings() throws {
        let json = """
        {
          "conversationId": "conv-1",
          "content": "done",
          "messageId": "msg-1"
        }
        """

        let data = try XCTUnwrap(json.data(using: .utf8))
        let decoded = try JSONDecoder.agently().decode(QueryOutput.self, from: data)

        XCTAssertEqual(decoded.conversationID, "conv-1")
        XCTAssertEqual(decoded.content, "done")
        XCTAssertEqual(decoded.messageID, "msg-1")
        XCTAssertEqual(decoded.warnings, [])
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
                          "model": "gpt-5.4",
                          "usage": {"inputTokens":120,"outputTokens":30,"cachedInputTokens":40,"reasoningTokens":12,"totalTokens":150}
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
        XCTAssertEqual(page.modelSteps.first?.usage?.totalTokens, 150)
        XCTAssertEqual(page.modelSteps.first?.usage?.cachedInputTokens, 40)
        let toolStep = try XCTUnwrap(page.toolSteps.first)
        XCTAssertEqual(toolStep.parentMessageID, "user-1")
        XCTAssertEqual(toolStep.executionRole, "sidecar")
        XCTAssertEqual(toolStep.operationID, "op-1")
        XCTAssertEqual(toolStep.asyncOperation?.status, "running")
    }

    func testRenderedContentDecodesWithoutDiagnostics() throws {
        let json = #"{"schemaVersion":"1","parts":[{"kind":"forgeData","data":{"id":"summary_metrics","payload":[{"label":"Spend"}]}}]}"#

        let decoded = try JSONDecoder.agently().decode(RenderedContent.self, from: try XCTUnwrap(json.data(using: .utf8)))

        XCTAssertEqual(decoded.schemaVersion, "1")
        XCTAssertEqual(decoded.parts.first?.data?.id, "summary_metrics")
        XCTAssertEqual(decoded.diagnostics, [])
    }

    func testRenderedContentDecodesProgressiveInlineReport() throws {
        let json = #"{"schemaVersion":"1","parts":[{"kind":"forgeReport","report":{"version":1,"id":"brief","sequence":2,"mode":"start","payload":{"id":"brief"}}}],"reports":[{"scope":"campaign","id":"brief","grammar":"dashboard-v1","status":"committed","sequence":2,"source":{"title":"Delivery","blocks":[]},"dataSources":{"rows":{"version":2,"reportRef":"brief","sequence":1,"id":"rows","payload":[{"spend":12}]}}}]}"#

        let decoded = try JSONDecoder.agently().decode(RenderedContent.self, from: try XCTUnwrap(json.data(using: .utf8)))

        XCTAssertEqual(decoded.parts.first?.report?.id, "brief")
        XCTAssertEqual(decoded.reports.first?.grammar, "dashboard-v1")
        XCTAssertEqual(decoded.reports.first?.status, "committed")
        XCTAssertEqual(decoded.reports.first?.dataSources["rows"]?.reportRef, "brief")
    }

    func testSharedProgressiveRenderedContentFixture() throws {
        let testFile = URL(fileURLWithPath: #filePath)
        let fixtureURL = testFile.deletingLastPathComponent()
            .deletingLastPathComponent()
            .deletingLastPathComponent()
            .deletingLastPathComponent()
            .appendingPathComponent("testdata/rendered_content_progressive.json")
        let decoded = try JSONDecoder.agently().decode(RenderedContent.self, from: Data(contentsOf: fixtureURL))

        let report = try XCTUnwrap(decoded.reports.first)
        XCTAssertEqual(report.scope, "campaign")
        XCTAssertEqual(report.id, "delivery")
        XCTAssertEqual(report.grammar, "dashboard-v1")
        XCTAssertEqual(report.status, "committed")
        XCTAssertEqual(report.sequence, 3)
        XCTAssertEqual(report.resetVersion, 1)
        guard case .object(let source)? = report.source else { return XCTFail("Expected report source object") }
        XCTAssertEqual(source["title"], .string("Delivery"))
        guard case .array(let blocks)? = source["blocks"] else { return XCTFail("Expected report blocks") }
        XCTAssertEqual(blocks.count, 1)
        XCTAssertEqual(report.dataSources["rows"]?.reportRef, "delivery")
        guard case .array(let rows)? = report.dataSources["rows"]?.payload,
              case .object(let firstRow)? = rows.first else { return XCTFail("Expected report rows") }
        XCTAssertEqual(firstRow["channel"], .string("CTV"))
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
            let data = #"{"workspaceRoot":"/tmp/workspace","appName":"Workspace","appIconRef":"builtin:viant","agents":[],"models":[],"agentInfos":[],"modelInfos":[]}"#.data(using: .utf8)!
            return (response, data)
        }

        let metadata = try await client.getWorkspaceMetadata(
            MetadataTargetContext(
                platform: " ios ",
                formFactor: " tablet ",
                surface: " app ",
                capabilities: [" markdown ", "", "chart"]
            )
        )
        XCTAssertEqual(metadata.appName, "Workspace")
        XCTAssertEqual(metadata.appIconRef, "builtin:viant")

        await fulfillment(of: [expectation], timeout: 2.0)
        URLProtocolStub.requestHandler = nil
    }

    func testGetWorkspaceMetadataUnwrapsDataEnvelopeAndAppliesDefaultFallbacks() async throws {
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [URLProtocolStub.self]
        let session = URLSession(configuration: configuration)
        let endpoint = EndpointConfig(baseURL: try XCTUnwrap(URL(string: "http://localhost:8585")))
        let client = AgentlyClient(endpoints: ["appAPI": endpoint], session: session)

        let expectation = expectation(description: "workspace metadata envelope request captured")
        URLProtocolStub.requestHandler = { request in
            let url = try XCTUnwrap(request.url)
            XCTAssertEqual(url.path, "/v1/workspace/metadata")
            expectation.fulfill()
            let response = HTTPURLResponse(url: url, statusCode: 200, httpVersion: nil, headerFields: [
                "Content-Type": "application/json"
            ])!
            let data = """
            {
              "data": {
                "workspaceRoot": "/tmp/workspace",
                "appName": "Workspace",
                "appIconRef": "builtin:viant",
                "agents": [],
                "models": [],
                "agentInfos": [],
                "modelInfos": [],
                "defaults": {
                  "agent": "coder",
                  "model": "gpt-5.4",
                  "embedder": "openai_text"
                }
              }
            }
            """.data(using: .utf8)!
            return (response, data)
        }

        let metadata = try await client.getWorkspaceMetadata()

        XCTAssertEqual(metadata.workspaceRoot, "/tmp/workspace")
        XCTAssertEqual(metadata.appName, "Workspace")
        XCTAssertEqual(metadata.appIconRef, "builtin:viant")
        XCTAssertEqual(metadata.defaultAgent, "coder")
        XCTAssertEqual(metadata.defaultModel, "gpt-5.4")
        XCTAssertEqual(metadata.defaultEmbedder, "openai_text")

        await fulfillment(of: [expectation], timeout: 2.0)
        URLProtocolStub.requestHandler = nil
    }

    func testGetForgeWindowMetadataUnwrapsDataEnvelope() async throws {
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [URLProtocolStub.self]
        let session = URLSession(configuration: configuration)
        let endpoint = EndpointConfig(baseURL: try XCTUnwrap(URL(string: "http://localhost:8585")))
        let client = AgentlyClient(endpoints: ["appAPI": endpoint], session: session)

        let expectation = expectation(description: "forge window metadata request captured")
        URLProtocolStub.requestHandler = { request in
            let url = try XCTUnwrap(request.url)
            let components = try XCTUnwrap(URLComponents(url: url, resolvingAgainstBaseURL: false))
            XCTAssertEqual(components.percentEncodedPath, "/v1/api/agently/forge/window/report%2Freview")
            expectation.fulfill()
            let response = HTTPURLResponse(url: url, statusCode: 200, httpVersion: nil, headerFields: [
                "Content-Type": "application/json"
            ])!
            let data = #"{"data":{"view":{"content":{"containers":[{"id":"reportRoot"}]}}}}"#.data(using: .utf8)!
            return (response, data)
        }

        let metadata = try await client.getForgeWindowMetadata(windowKey: " report/review ")
        let root = try XCTUnwrap({
            if case .object(let value) = metadata { return value }
            return nil
        }())
        let view = try XCTUnwrap({
            if case .object(let value) = root["view"] { return value }
            return nil
        }())
        let content = try XCTUnwrap({
            if case .object(let value) = view["content"] { return value }
            return nil
        }())
        let containers = try XCTUnwrap({
            if case .array(let value) = content["containers"] { return value }
            return nil
        }())
        let firstContainer = try XCTUnwrap(containers.first)
        let containerObject = try XCTUnwrap({
            if case .object(let value) = firstContainer { return value }
            return nil
        }())
        XCTAssertEqual(containerObject["id"], .string("reportRoot"))
        await fulfillment(of: [expectation], timeout: 2.0)
        URLProtocolStub.requestHandler = nil
    }

    func testGetForgeWindowMetadataRejectsBlankWindowKeyBeforeDispatch() async throws {
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [URLProtocolStub.self]
        let session = URLSession(configuration: configuration)
        let endpoint = EndpointConfig(baseURL: try XCTUnwrap(URL(string: "http://localhost:8585")))
        let client = AgentlyClient(endpoints: ["appAPI": endpoint], session: session)

        var requestCount = 0
        URLProtocolStub.requestHandler = { request in
            requestCount += 1
            let url = try XCTUnwrap(request.url)
            let response = HTTPURLResponse(url: url, statusCode: 500, httpVersion: nil, headerFields: nil)!
            return (response, Data())
        }

        do {
            _ = try await client.getForgeWindowMetadata(windowKey: "   ")
            XCTFail("expected blank window key to throw")
        } catch AgentlySDKError.invalidArgument(let message) {
            XCTAssertEqual(message, "window key is required")
        }

        XCTAssertEqual(requestCount, 0)
        URLProtocolStub.requestHandler = nil
    }

    func testGetForgeWindowMetadataAppendsTargetContextQueryItems() async throws {
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [URLProtocolStub.self]
        let session = URLSession(configuration: configuration)
        let endpoint = EndpointConfig(baseURL: try XCTUnwrap(URL(string: "http://localhost:8585")))
        let client = AgentlyClient(endpoints: ["appAPI": endpoint], session: session)

        let expectation = expectation(description: "forge window target query request captured")
        URLProtocolStub.requestHandler = { request in
            let url = try XCTUnwrap(request.url)
            let components = try XCTUnwrap(URLComponents(url: url, resolvingAgainstBaseURL: false))
            XCTAssertEqual(components.percentEncodedPath, "/v1/api/agently/forge/window/order")
            let items = components.queryItems ?? []
            func values(for name: String) -> [String] {
                items.filter { $0.name == name }.compactMap(\.value)
            }
            XCTAssertEqual(values(for: "platform").first, "ios")
            XCTAssertEqual(values(for: "formFactor").first, "tablet")
            XCTAssertEqual(values(for: "surface").first, "app")
            XCTAssertEqual(Set(values(for: "capabilities")), Set(["markdown", "chart"]))
            expectation.fulfill()
            let response = HTTPURLResponse(url: url, statusCode: 200, httpVersion: nil, headerFields: [
                "Content-Type": "application/json"
            ])!
            let data = #"{"data":{"view":{"content":{"containers":[]}}}}"#.data(using: .utf8)!
            return (response, data)
        }

        _ = try await client.getForgeWindowMetadata(
            windowKey: "order",
            targetContext: MetadataTargetContext(
                platform: " ios ",
                formFactor: " tablet ",
                surface: " app ",
                capabilities: [" markdown ", "", "chart"]
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
            XCTAssertEqual(value(for: "scheduleId"), "schedule-3")
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
                scheduleID: "schedule-3",
                excludeScheduled: true,
                query: "android qa",
                status: "active",
                page: PageInput(limit: 25, cursor: "cursor-1", direction: "next")
            )
        )

        await fulfillment(of: [expectation], timeout: 2.0)
        URLProtocolStub.requestHandler = nil
    }

    func testGetRunUsesSharedRunRoute() async throws {
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [URLProtocolStub.self]
        let session = URLSession(configuration: configuration)
        let endpoint = EndpointConfig(baseURL: try XCTUnwrap(URL(string: "http://localhost:8585")))
        let client = AgentlyClient(endpoints: ["appAPI": endpoint], session: session)

        URLProtocolStub.requestHandler = { request in
            let url = try XCTUnwrap(request.url)
            XCTAssertEqual(url.path, "/v1/runs/run-1")
            let response = HTTPURLResponse(url: url, statusCode: 200, httpVersion: nil, headerFields: [
                "Content-Type": "application/json"
            ])!
            let body = #"{"Id":"run-1","TurnId":"turn-1","ConversationId":"conv-1","Model":"gpt-5.5","ModelProvider":"openai","Status":"running","CreatedAt":"2026-06-03T12:00:00Z"}"#
            return (response, body.data(using: .utf8)!)
        }

        let run = try await client.getRun(id: "run-1")

        XCTAssertEqual(run.id, "run-1")
        XCTAssertEqual(run.turnID, "turn-1")
        XCTAssertEqual(run.conversationID, "conv-1")
        XCTAssertEqual(run.model, "gpt-5.5")
        XCTAssertEqual(run.provider, "openai")
        XCTAssertEqual(run.status, "running")
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

    func testApprovalCallbackResultDecodesCurrentEditedFieldsAndActionContract() throws {
        let json = """
        {
          "editedFields": {
            "names": ["prod"]
          },
          "action": "decline"
        }
        """

        let data = try XCTUnwrap(json.data(using: .utf8))
        let decoded = try JSONDecoder.agently().decode(ApprovalCallbackResult.self, from: data)

        XCTAssertEqual(decoded.action, "decline")
        XCTAssertEqual(decoded.editedFields["names"], .array([.string("prod")]))
        XCTAssertEqual(decoded.payload, [:])
    }

    func testApprovalCallbackPayloadDecodesActionContract() throws {
        let json = """
        {
          "action": "approve",
          "editedFields": {
            "names": ["prod"]
          },
          "originalArgs": {
            "names": ["dev", "prod"]
          }
        }
        """

        let data = try XCTUnwrap(json.data(using: .utf8))
        let decoded = try JSONDecoder.agently().decode(ApprovalCallbackPayload.self, from: data)

        XCTAssertEqual(decoded.action, "approve")
        XCTAssertEqual(decoded.editedFields["names"], .array([.string("prod")]))
        XCTAssertEqual(decoded.originalArgs["names"], .array([.string("dev"), .string("prod")]))
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

    func testConversationStreamTrackerCarriesCanonicalProgressiveReport() async throws {
        let tracker = ConversationStreamTracker()
        let payload = #"{"type":"text_delta","conversationId":"conv-1","turnId":"turn-1","assistantMessageId":"msg-1","content":"report fragment","renderedContent":{"schemaVersion":"1","parts":[],"reports":[{"scope":"campaign","id":"delivery","grammar":"dashboard-v1","status":"committed","sequence":2,"source":{"title":"Delivery","blocks":[]},"dataSources":{}}]}}"#

        let snapshot = await tracker.apply(SSEEvent(data: payload))

        let report = try XCTUnwrap(snapshot.liveExecutionGroupsByID["msg-1"]?.renderedContent?.reports.first)
        XCTAssertEqual(report.id, "delivery")
        XCTAssertEqual(report.status, "committed")
    }

    func testConversationStreamTrackerPreservesPendingElicitationStatus() async throws {
        let tracker = ConversationStreamTracker()

        let snapshot = await tracker.apply(SSEEvent(data: #"{"type":"elicitation_requested","conversationId":"conv-1","turnId":"turn-1","elicitationId":"elicit-1","content":"Approve this?","status":"resolved","elicitationData":{"requestedSchema":{"type":"object"}}}"#))

        XCTAssertEqual(snapshot.pendingElicitation?.elicitationID, "elicit-1")
        XCTAssertEqual(snapshot.pendingElicitation?.conversationID, "conv-1")
        XCTAssertEqual(snapshot.pendingElicitation?.turnID, "turn-1")
        XCTAssertEqual(snapshot.pendingElicitation?.message, "Approve this?")
        XCTAssertEqual(snapshot.pendingElicitation?.status, "resolved")
    }

    func testConversationStreamTrackerAppliesControlMessagePatchAndExecutionGroup() async throws {
        let tracker = ConversationStreamTracker()

        _ = await tracker.apply(SSEEvent(data: #"{"type":"model_started","conversationId":"conv-1","turnId":"turn-1","assistantMessageId":"msg-1","modelCallId":"mc-1","status":"running","model":{"provider":"openai","model":"gpt-5-mini"}}"#))
        _ = await tracker.apply(SSEEvent(data: #"{"type":"tool_feed_active","conversationId":"conv-1","turnId":"turn-1","feedId":"feed-1","feedTitle":"Feed","feedDeveloperOnly":true,"feedIcon":"chart","feedAccent":"purple","feedItemCount":2}"#))
        let snapshot = await tracker.apply(SSEEvent(data: #"{"type":"control","op":"message_patch","conversationId":"conv-1","turnId":"turn-1","assistantMessageId":"msg-1","patch":{"content":"Patched","status":"running","toolName":"prompt-get","linkedConversationId":"linked-1"}}"#))

        XCTAssertEqual(snapshot.feeds.count, 1)
        XCTAssertEqual(snapshot.feeds.first?.feedID, "feed-1")
        XCTAssertEqual(snapshot.feeds.first?.developerOnly, true)
        XCTAssertEqual(snapshot.feeds.first?.presentation, FeedPresentation(icon: "chart", accent: "purple"))
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

            XCTAssertEqual(value(for: "includeModelCall"), "true")
            XCTAssertEqual(value(for: "includeToolCall"), "true")
            XCTAssertEqual(value(for: "includeFeeds"), "true")
            XCTAssertNil(value(for: "includeModelCalls"))
            XCTAssertNil(value(for: "includeToolCalls"))
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

    func testGetTranscriptRejectsResponseAboveMobileSafetyLimit() async throws {
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [URLProtocolStub.self]
        let session = URLSession(configuration: configuration)
        let endpoint = EndpointConfig(baseURL: try XCTUnwrap(URL(string: "http://localhost:8585")))
        let client = AgentlyClient(endpoints: ["appAPI": endpoint], session: session)

        URLProtocolStub.requestHandler = { request in
            let response = HTTPURLResponse(
                url: try XCTUnwrap(request.url),
                statusCode: 200,
                httpVersion: nil,
                headerFields: [
                    "Content-Type": "application/json",
                    "Content-Length": "512"
                ]
            )!
            return (response, Data(repeating: 0x20, count: 512))
        }

        do {
            _ = try await client.getTranscript(
                GetTranscriptInput(conversationID: "large-conversation"),
                maxResponseBytes: 64
            )
            XCTFail("Expected the bounded transcript request to reject the response")
        } catch AgentlySDKError.responseTooLarge(let limit) {
            XCTAssertEqual(limit, 64)
        }
        URLProtocolStub.requestHandler = nil
    }

    func testGetTranscriptRejectsStreamedResponseWithoutContentLengthAboveMobileSafetyLimit() async throws {
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [URLProtocolStub.self]
        let session = URLSession(configuration: configuration)
        let endpoint = EndpointConfig(baseURL: try XCTUnwrap(URL(string: "http://localhost:8585")))
        let client = AgentlyClient(endpoints: ["appAPI": endpoint], session: session)

        URLProtocolStub.requestHandler = { request in
            let response = HTTPURLResponse(
                url: try XCTUnwrap(request.url),
                statusCode: 200,
                httpVersion: nil,
                headerFields: ["Content-Type": "application/json"]
            )!
            return (response, Data(repeating: 0x20, count: 512))
        }

        do {
            _ = try await client.getTranscript(
                GetTranscriptInput(conversationID: "streamed-large-conversation"),
                maxResponseBytes: 64
            )
            XCTFail("Expected the streamed transcript request to enforce its byte limit")
        } catch AgentlySDKError.responseTooLarge(let limit) {
            XCTAssertEqual(limit, 64)
        }
        URLProtocolStub.requestHandler = nil
    }

    func testPhase1AuthBackfillUsesAndroidEquivalentRoutes() async throws {
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [URLProtocolStub.self]
        let session = URLSession(configuration: configuration)
        let endpoint = EndpointConfig(baseURL: try XCTUnwrap(URL(string: "http://localhost:8585")))
        let client = AgentlyClient(endpoints: ["appAPI": endpoint], session: session)

        var seen: [String] = []
        URLProtocolStub.requestHandler = { request in
            let url = try XCTUnwrap(request.url)
            seen.append("\(request.httpMethod ?? "") \(url.path)")
            let response = HTTPURLResponse(url: url, statusCode: 200, httpVersion: nil, headerFields: [
                "Content-Type": "application/json"
            ])!
            let body: String
            switch url.path {
            case "/v1/api/auth/local/login":
                body = #"{"sessionId":"sess-local","username":"test-user","provider":"local"}"#
            case "/v1/api/auth/logout":
                body = #"{}"#
            case "/v1/api/auth/session":
                body = #"{"sessionId":"sess-created","username":"test-user"}"#
            case "/v1/api/auth/session/attach":
                body = #"{"status":"ok","sessionId":"sess-attached","username":"test-user"}"#
            case "/v1/api/auth/oob":
                body = #"{"sessionId":"sess-oob","status":"ok","username":"test-user","provider":"idp"}"#
            case "/v1/api/auth/idp/delegate":
                body = #"{"mode":"oob","idpLogin":"enabled","provider":"idp","authUrl":"https://idp.example/auth","state":"state-1","expiresIn":300}"#
            default:
                XCTFail("unexpected path \(url.path)")
                body = #"{}"#
            }
            return (response, body.data(using: .utf8)!)
        }

        let local = try await client.localLogin(LocalLoginInput(username: "test-user"))
        try await client.logout()
        let sessionOutput = try await client.createAuthSession(CreateSessionInput(username: "test-user", accessToken: "token"))
        let attachedSession = try await client.attachAuthSession(AttachSessionInput(sessionID: "sess-created"))
        let oob = try await client.oobLogin(OOBLoginInput(secretsURL: "mem://secret", scopes: ["openid"]))
        let delegate = try await client.idpDelegate()

        XCTAssertEqual(local.sessionID, "sess-local")
        XCTAssertEqual(sessionOutput.sessionID, "sess-created")
        XCTAssertEqual(attachedSession.sessionID, "sess-attached")
        XCTAssertEqual(oob.sessionID, "sess-oob")
        XCTAssertEqual(delegate.authURL, "https://idp.example/auth")
        XCTAssertEqual(seen, [
            "POST /v1/api/auth/local/login",
            "POST /v1/api/auth/logout",
            "POST /v1/api/auth/session",
            "POST /v1/api/auth/session/attach",
            "POST /v1/api/auth/oob",
            "POST /v1/api/auth/idp/delegate"
        ])
        URLProtocolStub.requestHandler = nil
    }

    func testPhase1ConversationBackfillUsesExpectedRoutes() async throws {
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [URLProtocolStub.self]
        let session = URLSession(configuration: configuration)
        let endpoint = EndpointConfig(baseURL: try XCTUnwrap(URL(string: "http://localhost:8585")))
        let client = AgentlyClient(endpoints: ["appAPI": endpoint], session: session)

        var seen: [String] = []
        URLProtocolStub.requestHandler = { request in
            let url = try XCTUnwrap(request.url)
            seen.append("\(request.httpMethod ?? "") \(url.path)")
            let response = HTTPURLResponse(url: url, statusCode: 200, httpVersion: nil, headerFields: [
                "Content-Type": "application/json"
            ])!
            let body: String
            switch (request.httpMethod ?? "", url.path) {
            case ("GET", "/v1/conversations/conv-1"):
                body = #"{"Id":"conv-1","Title":"Before","Shareable":0}"#
            case ("PATCH", "/v1/conversations/conv-1"):
                body = #"{"Id":"conv-1","Title":"After","Shareable":1}"#
            case ("DELETE", "/v1/conversations/conv-1"):
                body = #"{}"#
            default:
                XCTFail("unexpected request \(request.httpMethod ?? "") \(url.path)")
                body = #"{}"#
            }
            return (response, body.data(using: .utf8)!)
        }

        let conversation = try await client.getConversation(conversationID: "conv-1")
        let updated = try await client.updateConversation(conversationID: "conv-1", UpdateConversationInput(title: "After", shareable: true))
        try await client.deleteConversation(conversationID: "conv-1")

        XCTAssertEqual(conversation.title, "Before")
        XCTAssertEqual(updated.title, "After")
        XCTAssertEqual(seen, [
            "GET /v1/conversations/conv-1",
            "PATCH /v1/conversations/conv-1",
            "DELETE /v1/conversations/conv-1"
        ])
        URLProtocolStub.requestHandler = nil
    }

    func testPhase1CanonicalTranscriptBackfillRoutesAndDecoding() async throws {
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [URLProtocolStub.self]
        let session = URLSession(configuration: configuration)
        let endpoint = EndpointConfig(baseURL: try XCTUnwrap(URL(string: "http://localhost:8585")))
        let client = AgentlyClient(endpoints: ["appAPI": endpoint], session: session)

        URLProtocolStub.requestHandler = { request in
            let url = try XCTUnwrap(request.url)
            let components = try XCTUnwrap(URLComponents(url: url, resolvingAgainstBaseURL: false))
            let items = components.queryItems ?? []
            func value(for name: String) -> String? {
                items.first(where: { $0.name == name })?.value
            }
            let response = HTTPURLResponse(url: url, statusCode: 200, httpVersion: nil, headerFields: [
                "Content-Type": "application/json"
            ])!
            let body: String
            switch url.path {
            case "/v1/messages":
                XCTAssertEqual(value(for: "conversationId"), "conv-1")
                XCTAssertEqual(value(for: "turnId"), "turn-1")
                XCTAssertEqual(value(for: "roles"), "user,assistant")
                XCTAssertEqual(value(for: "types"), "text")
                XCTAssertEqual(value(for: "limit"), "10")
                body = #"{"Rows":[{"Id":"msg-1","ConversationId":"conv-1","TurnId":"turn-1","Role":"assistant","Content":"hello","Sequence":2}],"HasMore":false}"#
            case "/v1/conversations/linked":
                XCTAssertEqual(value(for: "parentConversationId"), "conv-1")
                XCTAssertEqual(value(for: "parentTurnId"), "turn-1")
                body = #"{"Rows":[{"conversationId":"linked-1","parentConversationId":"conv-1","parentTurnId":"turn-1","agentId":"agent"}],"HasMore":false}"#
            case "/v1/conversations/conv-1/live-state":
                XCTAssertEqual(value(for: "includeFeeds"), "true")
                body = #"{"conversation":{"conversationId":"conv-1","turns":[]},"feeds":[{"feedId":"feed-1","title":"Feed"}]}"#
            case "/v1/feeds/feed-1/data":
                XCTAssertEqual(value(for: "conversationId"), "conv-1")
                body = #"{"feedId":"feed-1","title":"Feed","data":{"rows":[{"name":"row"}]}}"#
            default:
                XCTFail("unexpected path \(url.path)")
                body = #"{}"#
            }
            return (response, body.data(using: .utf8)!)
        }

        let messages = try await client.getMessages(
            GetMessagesInput(
                conversationID: "conv-1",
                turnID: "turn-1",
                roles: ["user", "assistant"],
                types: ["text"],
                page: PageInput(limit: 10)
            )
        )
        let linked = try await client.listLinkedConversations(
            ListLinkedConversationsInput(parentConversationID: "conv-1", parentTurnID: "turn-1")
        )
        let live = try await client.getLiveState(conversationID: "conv-1", includeFeeds: true)
        let feed = try await client.getFeedData(feedID: "feed-1", conversationID: "conv-1")

        XCTAssertEqual(messages.rows.first?.id, "msg-1")
        XCTAssertEqual(messages.rows.first?.conversationID, "conv-1")
        XCTAssertEqual(linked.rows.first?.conversationID, "linked-1")
        XCTAssertEqual(live.feeds.first?.feedID, "feed-1")
        XCTAssertEqual(feed.feedID, "feed-1")
        URLProtocolStub.requestHandler = nil
    }

    func testPhase1PayloadAndWorkspaceFileBackfillRoutes() async throws {
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [URLProtocolStub.self]
        let session = URLSession(configuration: configuration)
        let endpoint = EndpointConfig(baseURL: try XCTUnwrap(URL(string: "http://localhost:8585")))
        let client = AgentlyClient(endpoints: ["appAPI": endpoint], session: session)

        URLProtocolStub.requestHandler = { request in
            let url = try XCTUnwrap(request.url)
            let components = try XCTUnwrap(URLComponents(url: url, resolvingAgainstBaseURL: false))
            let items = components.queryItems ?? []
            func value(for name: String) -> String? {
                items.first(where: { $0.name == name })?.value
            }
            switch url.path {
            case "/v1/api/payload/payload-1" where value(for: "raw") == nil:
                XCTAssertEqual(value(for: "meta"), "1")
                XCTAssertEqual(value(for: "inline"), "0")
                let response = HTTPURLResponse(url: url, statusCode: 200, httpVersion: nil, headerFields: [
                    "Content-Type": "application/json"
                ])!
                let data = #"{"Id":"payload-1","MimeType":"application/json","SizeBytes":42,"URI":"mem://payload"}"#.data(using: .utf8)!
                return (response, data)
            case "/v1/api/payload/payload-1" where value(for: "raw") == "1":
                let response = HTTPURLResponse(url: url, statusCode: 200, httpVersion: nil, headerFields: [
                    "Content-Type": "application/octet-stream",
                    "Content-Disposition": "attachment; filename=\"payload.bin\""
                ])!
                return (response, Data([0x01, 0x02, 0x03]))
            case "/v1/workspace/file-browser/list":
                XCTAssertEqual(value(for: "uri"), "workspace://reports")
                let response = HTTPURLResponse(url: url, statusCode: 200, httpVersion: nil, headerFields: [
                    "Content-Type": "application/json"
                ])!
                let data = #"{"entries":[{"uri":"workspace://reports/report.md","name":"report.md","isDir":false,"size":12}]}"#.data(using: .utf8)!
                return (response, data)
            case "/v1/workspace/file-browser/download":
                XCTAssertEqual(value(for: "uri"), "workspace://reports/report.md")
                let response = HTTPURLResponse(url: url, statusCode: 200, httpVersion: nil, headerFields: [
                    "Content-Type": "text/markdown"
                ])!
                return (response, "# Report".data(using: .utf8)!)
            default:
                XCTFail("unexpected path \(url.path)")
                let response = HTTPURLResponse(url: url, statusCode: 404, httpVersion: nil, headerFields: nil)!
                return (response, Data())
            }
        }

        let payload = try await client.getPayload(id: "payload-1", options: GetPayloadOptions(meta: true, inline: false))
        let download = try await client.downloadPayload(id: "payload-1")
        let entries = try await client.listWorkspaceFiles(uri: "workspace://reports")
        let text = try await client.downloadWorkspaceFile(uri: "workspace://reports/report.md")

        XCTAssertEqual(payload.id, "payload-1")
        XCTAssertEqual(payload.mimeType, "application/json")
        XCTAssertEqual(download.name, "payload.bin")
        XCTAssertEqual(download.data, Data([0x01, 0x02, 0x03]))
        XCTAssertEqual(entries.first?.name, "report.md")
        XCTAssertEqual(text, "# Report")
        URLProtocolStub.requestHandler = nil
    }

    func testGetPayloadsUsesBatchEndpointWithDeduplicatedIDs() async throws {
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [URLProtocolStub.self]
        let session = URLSession(configuration: configuration)
        let endpoint = EndpointConfig(baseURL: try XCTUnwrap(URL(string: "http://localhost:8585")))
        let client = AgentlyClient(endpoints: ["appAPI": endpoint], session: session)

        var seen: [String] = []
        var requestedIDs: [String] = []
        URLProtocolStub.requestHandler = { request in
            let url = try XCTUnwrap(request.url)
            seen.append("\(request.httpMethod ?? "") \(url.path)")
            XCTAssertEqual(url.path, "/v1/api/payloads")
            let body = try XCTUnwrap(self.requestBodyString(request)?.data(using: .utf8))
            let decoded = try XCTUnwrap(JSONSerialization.jsonObject(with: body) as? [String: Any])
            requestedIDs = try XCTUnwrap(decoded["ids"] as? [String])
            let response = HTTPURLResponse(url: url, statusCode: 200, httpVersion: nil, headerFields: [
                "Content-Type": "application/json"
            ])!
            return (response, #"{"p1":{"Id":"p1","MimeType":"text/plain","SizeBytes":1,"Storage":"inline","Compression":"none"},"p2":{"Id":"p2","MimeType":"text/plain","SizeBytes":2,"Storage":"inline","Compression":"none"}}"#.data(using: .utf8)!)
        }

        let result = try await client.getPayloads(ids: ["p1", "p2", "missing", "p1", ""])

        XCTAssertEqual(result.count, 2)
        XCTAssertEqual(result["p1"]?.id, "p1")
        XCTAssertEqual(result["p2"]?.id, "p2")
        XCTAssertEqual(seen, ["POST /v1/api/payloads"])
        XCTAssertEqual(requestedIDs, ["p1", "p2", "missing"])
        URLProtocolStub.requestHandler = nil
    }

    func testPhase1ResourcesAndSchedulerBackfillRoutes() async throws {
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [URLProtocolStub.self]
        let session = URLSession(configuration: configuration)
        let endpoint = EndpointConfig(baseURL: try XCTUnwrap(URL(string: "http://localhost:8585")))
        let client = AgentlyClient(endpoints: ["appAPI": endpoint], session: session)

        var seen: [String] = []
        URLProtocolStub.requestHandler = { request in
            let url = try XCTUnwrap(request.url)
            let components = try XCTUnwrap(URLComponents(url: url, resolvingAgainstBaseURL: false))
            seen.append("\(request.httpMethod ?? "") \(components.percentEncodedPath)")
            let items = components.queryItems ?? []
            func value(for name: String) -> String? {
                items.first(where: { $0.name == name })?.value
            }
            let response = HTTPURLResponse(url: url, statusCode: 200, httpVersion: nil, headerFields: [
                "Content-Type": "application/json"
            ])!
            let body: String
            switch (request.httpMethod ?? "", components.percentEncodedPath) {
            case ("GET", "/v1/workspace/resources"):
                XCTAssertEqual(value(for: "kind"), "prompt")
                body = #"{"names":["alpha.md","beta.md"]}"#
            case ("GET", "/v1/workspace/resources/prompt/alpha.md"):
                body = #"{"kind":"prompt","name":"alpha.md","data":"hello"}"#
            case ("PUT", "/v1/workspace/resources/prompt/alpha.md"):
                let raw = self.requestBodyString(request)
                XCTAssertEqual(raw, "updated")
                body = #"{}"#
            case ("DELETE", "/v1/workspace/resources/prompt/alpha.md"):
                body = #"{}"#
            case ("POST", "/v1/workspace/resources/export"):
                body = #"{"resources":[{"kind":"prompt","name":"alpha.md","data":"hello"}]}"#
            case ("POST", "/v1/workspace/resources/import"):
                body = #"{"imported":1,"skipped":0}"#
            case ("GET", "/v1/api/agently/scheduler/schedule/schedule-1"):
                body = #"{"status":"ok","data":{"id":"schedule-1","name":"Daily","agentRef":"agent","enabled":true,"scheduleType":"cron","cronExpr":"0 0 * * *"}}"#
            case ("GET", "/v1/api/agently/scheduler/"):
                body = #"{"status":"ok","data":{"schedules":[{"id":"schedule-1","name":"Daily","agentRef":"agent","enabled":true,"scheduleType":"cron","cronExpr":"0 0 * * *"}]}}"#
            case ("PATCH", "/v1/api/agently/scheduler/"):
                let patch = self.requestBodyString(request) ?? ""
                XCTAssertTrue(patch.contains("\"scheduleType\":\"cron\""))
                body = #"{}"#
            case ("POST", "/v1/api/agently/scheduler/run-now/schedule-1"):
                body = #"{}"#
            default:
                XCTFail("unexpected request \(request.httpMethod ?? "") \(components.percentEncodedPath)")
                body = #"{}"#
            }
            return (response, body.data(using: .utf8)!)
        }

        let names = try await client.listResources(ListResourcesInput(kind: "prompt"))
        let resource = try await client.getResource(ResourceRef(kind: "prompt", name: "alpha.md"))
        try await client.saveResource(SaveResourceInput(kind: "prompt", name: "alpha.md", data: "updated"))
        try await client.deleteResource(ResourceRef(kind: "prompt", name: "alpha.md"))
        let exported = try await client.exportResources(ExportResourcesInput(kinds: ["prompt"]))
        let imported = try await client.importResources(ImportResourcesInput(resources: [ResourcePayload(kind: "prompt", name: "alpha.md", data: "hello")], replace: true))
        let schedule = try await client.getSchedule(id: "schedule-1")
        let schedules = try await client.listSchedules()
        try await client.upsertSchedules([Schedule(id: "schedule-1", name: "Daily", agentRef: "agent", enabled: true, scheduleType: "cron", cronExpr: "0 0 * * *")])
        try await client.runScheduleNow(id: "schedule-1")

        XCTAssertEqual(names.names, ["alpha.md", "beta.md"])
        XCTAssertEqual(resource.data, "hello")
        XCTAssertEqual(exported.resources.first?.name, "alpha.md")
        XCTAssertEqual(imported.imported, 1)
        XCTAssertEqual(schedule?.id, "schedule-1")
        XCTAssertEqual(schedules.first?.scheduleType, "cron")
        XCTAssertEqual(seen, [
            "GET /v1/workspace/resources",
            "GET /v1/workspace/resources/prompt/alpha.md",
            "PUT /v1/workspace/resources/prompt/alpha.md",
            "DELETE /v1/workspace/resources/prompt/alpha.md",
            "POST /v1/workspace/resources/export",
            "POST /v1/workspace/resources/import",
            "GET /v1/api/agently/scheduler/schedule/schedule-1",
            "GET /v1/api/agently/scheduler/",
            "PATCH /v1/api/agently/scheduler/",
            "POST /v1/api/agently/scheduler/run-now/schedule-1"
        ])
        URLProtocolStub.requestHandler = nil
    }

    func testStreamEventsEncodesConversationIDQuery() async throws {
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [URLProtocolStub.self]
        let session = URLSession(configuration: configuration)
        let endpoint = EndpointConfig(baseURL: try XCTUnwrap(URL(string: "http://localhost:8585")))
        let client = AgentlyClient(endpoints: ["appAPI": endpoint], session: session)

        let expectation = expectation(description: "stream request captured")
        URLProtocolStub.requestHandler = { request in
            let url = try XCTUnwrap(request.url)
            let components = try XCTUnwrap(URLComponents(url: url, resolvingAgainstBaseURL: false))
            XCTAssertEqual(components.percentEncodedPath, "/v1/stream")
            XCTAssertEqual(components.percentEncodedQuery, "conversationId=conv%2B1%2Fmain")
            expectation.fulfill()
            let response = HTTPURLResponse(url: url, statusCode: 200, httpVersion: nil, headerFields: [
                "Content-Type": "text/event-stream"
            ])!
            let data = """
            data: {"type":"status","conversationId":"conv+1/main"}

            """.data(using: .utf8)!
            return (response, data)
        }

        var iterator = client.streamEvents(conversationID: "conv+1/main").makeAsyncIterator()
        _ = try await iterator.next()

        await fulfillment(of: [expectation], timeout: 2.0)
        URLProtocolStub.requestHandler = nil
    }

    func testStreamEventsReplaysPersistentSessionCookie() async throws {
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [URLProtocolStub.self]
        configuration.httpCookieStorage = HTTPCookieStorage()
        let baseURL = try XCTUnwrap(URL(string: "https://steward.agently.viantinc.com"))
        let endpoint = EndpointConfig(baseURL: baseURL)
        let store = AgentlyPersistentSessionCookieStore(storage: MemoryCookieStorage())
        let cookieResponse = try XCTUnwrap(HTTPURLResponse(
            url: baseURL,
            statusCode: 200,
            httpVersion: nil,
            headerFields: ["Set-Cookie": "agently_session=session-123; Path=/; HttpOnly; Secure"]
        ))
        store.storeCookies(from: cookieResponse, requestURL: baseURL)
        let client = AgentlyClient(
            endpoints: ["appAPI": endpoint],
            session: URLSession(configuration: configuration),
            sessionCookieStore: store
        )

        let expectation = expectation(description: "authenticated stream request captured")
        URLProtocolStub.requestHandler = { request in
            XCTAssertEqual(request.value(forHTTPHeaderField: "Cookie"), "agently_session=session-123")
            expectation.fulfill()
            let url = try XCTUnwrap(request.url)
            let response = try XCTUnwrap(HTTPURLResponse(
                url: url,
                statusCode: 200,
                httpVersion: nil,
                headerFields: ["Content-Type": "text/event-stream"]
            ))
            return (response, Data("data: {}\n\n".utf8))
        }
        defer { URLProtocolStub.requestHandler = nil }

        var iterator = client.streamEvents(conversationID: "conversation-1").makeAsyncIterator()
        _ = try await iterator.next()

        await fulfillment(of: [expectation], timeout: 2.0)
    }

    func testPhase1ToolsAndA2ABackfillRoutes() async throws {
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [URLProtocolStub.self]
        let session = URLSession(configuration: configuration)
        let endpoint = EndpointConfig(baseURL: try XCTUnwrap(URL(string: "http://localhost:8585")))
        let client = AgentlyClient(endpoints: ["appAPI": endpoint], session: session)

        URLProtocolStub.requestHandler = { request in
            let url = try XCTUnwrap(request.url)
            let components = try XCTUnwrap(URLComponents(url: url, resolvingAgainstBaseURL: false))
            let items = components.queryItems ?? []
            func value(for name: String) -> String? {
                items.first(where: { $0.name == name })?.value
            }
            let response = HTTPURLResponse(url: url, statusCode: 200, httpVersion: nil, headerFields: [
                "Content-Type": "application/json"
            ])!
            let body: String
            switch (request.httpMethod ?? "", url.path) {
            case ("GET", "/v1/tools"):
                body = #"[{"name":"system.exec","description":"Execute","required":["cmd"],"output_schema":{"type":"string"}}]"#
            case ("POST", "/v1/tools/system.exec/execute"):
                body = #"{"result":"ok"}"#
            case ("POST", "/v1/tools/system%2Fgoal%3Acreate/execute"), ("POST", "/v1/tools/system/goal:create/execute"):
                XCTAssertEqual(value(for: "conversationId"), "conv-goal-1")
                body = #"{"result":"ok"}"#
            case ("GET", "/v1/conversations/conv-goal-1/goal"):
                body = #"{"goal":{"id":"goal-1","objective":"finish refactor","status":"active"}}"#
            case ("POST", "/v1/conversations/conv-goal-1/goal"):
                body = #"{"id":"goal-1","objective":"finish refactor","status":"active"}"#
            case ("PATCH", "/v1/conversations/conv-goal-1/goal"):
                body = #"{"id":"goal-1","objective":"finish refactor","status":"paused","statusReason":"user paused"}"#
            case ("DELETE", "/v1/conversations/conv-goal-1/goal"):
                body = #"{}"#
            case ("GET", "/v1/api/a2a/agents/agent-1/card"):
                body = #"{"name":"agent-1","title":"Agent One","endpoints":{},"capabilities":{"streaming":true}}"#
            case ("POST", "/v1/api/a2a/agents/agent-1/message"):
                body = #"{"task":{"id":"task-1","contextId":"ctx-1","status":{"state":"running"}}}"#
            case ("GET", "/v1/api/a2a/agents"):
                XCTAssertEqual(value(for: "ids"), "agent-1,agent-2")
                body = #"{"agents":["agent-1","agent-2"]}"#
            default:
                XCTFail("unexpected request \(request.httpMethod ?? "") \(url.path)")
                body = #"{}"#
            }
            return (response, body.data(using: .utf8)!)
        }

        let tools = try await client.listToolDefinitions()
        let result = try await client.executeTool(name: "system.exec", args: ["cmd": .string("pwd")])
        let goalResult = try await client.executeTool(
            name: "system/goal:create",
            args: ["objective": .string("finish refactor")],
            conversationID: "conv-goal-1"
        )
        let goal = try await client.getGoal(conversationID: "conv-goal-1")
        let createdGoal = try await client.createGoal(conversationID: "conv-goal-1", CreateGoalInput(objective: "finish refactor"))
        let updatedGoal = try await client.updateGoal(conversationID: "conv-goal-1", UpdateGoalInput(status: "paused", statusReason: "user paused"))
        try await client.clearGoal(conversationID: "conv-goal-1")
        let card = try await client.getA2AAgentCard(agentID: "agent-1")
        let response = try await client.sendA2AMessage(
            agentID: "agent-1",
            request: SendA2AMessageRequest(message: A2AMessage(role: "user", parts: [A2APart(type: "text", text: "hello")]))
        )
        let agents = try await client.listA2AAgents(agentIDs: ["agent-1", "agent-2"])

        XCTAssertEqual(tools.first?.name, "system.exec")
        XCTAssertEqual(result, "ok")
        XCTAssertEqual(goalResult, "ok")
        XCTAssertEqual(goal?.id, "goal-1")
        XCTAssertEqual(createdGoal.id, "goal-1")
        XCTAssertEqual(updatedGoal.status, "paused")
        XCTAssertEqual(updatedGoal.statusReason, "user paused")
        XCTAssertEqual(card.capabilities?.streaming, true)
        XCTAssertEqual(response.task.id, "task-1")
        XCTAssertEqual(agents, ["agent-1", "agent-2"])
        URLProtocolStub.requestHandler = nil
    }

    func testListUIEventsExecutesScopedToolAndDecodesStructuredDetails() async throws {
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [URLProtocolStub.self]
        let session = URLSession(configuration: configuration)
        let endpoint = EndpointConfig(baseURL: try XCTUnwrap(URL(string: "http://localhost:8585")))
        let client = AgentlyClient(endpoints: ["appAPI": endpoint], session: session)

        URLProtocolStub.requestHandler = { request in
            let url = try XCTUnwrap(request.url)
            let components = try XCTUnwrap(URLComponents(url: url, resolvingAgainstBaseURL: false))
            let items = components.queryItems ?? []
            let response = HTTPURLResponse(url: url, statusCode: 200, httpVersion: nil, headerFields: [
                "Content-Type": "application/json"
            ])!
            XCTAssertEqual(request.httpMethod, "POST")
            XCTAssertEqual(components.percentEncodedPath, "/v1/tools/ui%2Fevents%3Alist/execute")
            XCTAssertEqual(items.first(where: { $0.name == "conversationId" })?.value, "conv-ui-1")
            let body = try XCTUnwrap(self.requestBodyString(request))
            XCTAssertTrue(body.contains(#""clientId":"mobile-client-1""#))
            XCTAssertTrue(body.contains(#""kinds":["error"]"#))
            XCTAssertTrue(body.contains(#""limit":20"#))
            let result = #"{"conversationId":"conv-ui-1","clientId":"mobile-client-1","events":[{"seq":7,"kind":"error","actor":"agent","detail":{"payload":{"invalidWorkspaceId":"legacyAlias","availableWorkspaceIds":["orders"]}}}]}"#
            let envelope = try JSONEncoder.agently().encode(["result": result])
            return (response, envelope)
        }

        let output = try await client.listUIEvents(
            ListUIEventsInput(
                conversationId: "conv-ui-1",
                clientId: "mobile-client-1",
                kinds: ["error"],
                limit: 20
            )
        )

        XCTAssertEqual(output.conversationId, "conv-ui-1")
        XCTAssertEqual(output.clientId, "mobile-client-1")
        let event = try XCTUnwrap(output.events.first)
        XCTAssertEqual(event.seq, 7)
        XCTAssertEqual(event.kind, "error")
        guard case .object(let payload) = try XCTUnwrap(event.detail?["payload"]) else {
            XCTFail("expected object payload")
            return
        }
        XCTAssertEqual(payload["invalidWorkspaceId"], .string("legacyAlias"))
        XCTAssertEqual(payload["availableWorkspaceIds"], .array([.string("orders")]))
        URLProtocolStub.requestHandler = nil
    }

    func testTemplatesSkillsAndMCPUIToolCallRoutesMatchSharedClientContract() async throws {
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [URLProtocolStub.self]
        let session = URLSession(configuration: configuration)
        let endpoint = EndpointConfig(baseURL: try XCTUnwrap(URL(string: "http://localhost:8585")))
        let client = AgentlyClient(endpoints: ["appAPI": endpoint], session: session)

        var seen: [String] = []
        URLProtocolStub.requestHandler = { request in
            let url = try XCTUnwrap(request.url)
            let components = try XCTUnwrap(URLComponents(url: url, resolvingAgainstBaseURL: false))
            seen.append("\(request.httpMethod ?? "") \(components.percentEncodedPath)")
            let items = components.queryItems ?? []
            func value(for name: String) -> String? {
                items.first(where: { $0.name == name })?.value
            }
            let response = HTTPURLResponse(url: url, statusCode: 200, httpVersion: nil, headerFields: [
                "Content-Type": "application/json"
            ])!
            let body: String
            switch (request.httpMethod ?? "", components.percentEncodedPath) {
            case ("GET", "/v1/templates"):
                body = #"{"items":[{"name":"brief","description":"Summary","format":"markdown"}]}"#
            case ("GET", "/v1/templates/brief"):
                XCTAssertEqual(value(for: "includeDocument"), "true")
                body = #"{"name":"brief","format":"markdown","description":"Summary","instructions":"Use bullets","includedDocument":true}"#
            case ("GET", "/v1/skills"):
                XCTAssertEqual(value(for: "conversationId"), "conv-1")
                body = #"{"items":[{"name":"playwright-cli","description":"Automate browser"}],"diagnostics":["ok"]}"#
            case ("POST", "/v1/skills/playwright-cli/activate"):
                XCTAssertEqual(value(for: "conversationId"), "conv-1")
                let raw = try XCTUnwrap(self.requestBodyString(request))
                XCTAssertEqual(raw, #"{"args":"https:\/\/example.com"}"#)
                body = #"{"name":"playwright-cli","body":"Loaded skill"}"#
            case ("GET", "/v1/skills/diagnostics"):
                body = #"{"items":["shadowed demo"]}"#
            case ("POST", "/v1/api/mcp-ui/tools/call"):
                let raw = try XCTUnwrap(self.requestBodyString(request))
                XCTAssertTrue(raw.contains(#""toolName":"system.exec""#))
                body = #"{"conversationId":"conv-1","turnId":"turn-1","status":"queued","result":"","source":"approval","approval":{"id":"approval-1","toolName":"system.exec","status":"pending","createdAt":"2026-06-03T12:00:00Z","userId":"","conversationId":"conv-1"}}"#
            default:
                XCTFail("unexpected request \(request.httpMethod ?? "") \(url.path)")
                body = #"{}"#
            }
            return (response, body.data(using: .utf8)!)
        }

        let templates = try await client.listTemplates()
        let template = try await client.getTemplate(GetTemplateInput(name: "brief", includeDocument: true))
        let skills = try await client.listSkills(ListSkillsInput(conversationID: "conv-1"))
        let skill = try await client.activateSkill(ActivateSkillInput(conversationID: "conv-1", name: "playwright-cli", args: "https://example.com"))
        let diagnostics = try await client.getSkillDiagnostics()
        let toolCall = try await client.executeMCPUIToolCall(
            MCPUIToolCallInput(
                conversationID: "conv-1",
                toolName: "system.exec",
                arguments: ["cmd": .string("pwd")],
                assistantText: "Running tool",
                toolBundles: ["system/exec"]
            )
        )

        XCTAssertEqual(templates.items.first?.name, "brief")
        XCTAssertEqual(template.name, "brief")
        XCTAssertEqual(skills.items.first?.name, "playwright-cli")
        XCTAssertEqual(skill.body, "Loaded skill")
        XCTAssertEqual(diagnostics.items, ["shadowed demo"])
        XCTAssertEqual(toolCall.status, "queued")
        XCTAssertEqual(toolCall.approval?.id, "approval-1")
        XCTAssertEqual(seen, [
            "GET /v1/templates",
            "GET /v1/templates/brief",
            "GET /v1/skills",
            "POST /v1/skills/playwright-cli/activate",
            "GET /v1/skills/diagnostics",
            "POST /v1/api/mcp-ui/tools/call"
        ])
        URLProtocolStub.requestHandler = nil
    }

    func testTemplateAndSkillTransportsEncodeSlashBearingPathSegments() async throws {
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [URLProtocolStub.self]
        let session = URLSession(configuration: configuration)
        let endpoint = EndpointConfig(baseURL: try XCTUnwrap(URL(string: "http://localhost:8585")))
        let client = AgentlyClient(endpoints: ["appAPI": endpoint], session: session)

        var seen: [String] = []
        URLProtocolStub.requestHandler = { request in
            let url = try XCTUnwrap(request.url)
            let components = try XCTUnwrap(URLComponents(url: url, resolvingAgainstBaseURL: false))
            seen.append("\(request.httpMethod ?? "") \(components.percentEncodedPath)")
            let response = HTTPURLResponse(url: url, statusCode: 200, httpVersion: nil, headerFields: [
                "Content-Type": "application/json"
            ])!
            switch (request.httpMethod ?? "", components.percentEncodedPath) {
            case ("GET", "/v1/templates/templates%2Fbrief"):
                return (response, #"{"name":"templates/brief","includedDocument":true}"#.data(using: .utf8)!)
            case ("POST", "/v1/skills/skills%2Fplaywright-cli/activate"):
                let raw = try XCTUnwrap(self.requestBodyString(request))
                XCTAssertEqual(raw, #"{"args":"args"}"#)
                let queryItems = components.queryItems ?? []
                XCTAssertEqual(queryItems.first(where: { $0.name == "conversationId" })?.value, "conv-1")
                return (response, #"{"name":"skills/playwright-cli","body":"Loaded skill"}"#.data(using: .utf8)!)
            default:
                XCTFail("unexpected request \(request.httpMethod ?? "") \(url.path)")
                return (response, #"{}"#.data(using: .utf8)!)
            }
        }

        _ = try await client.getTemplate(GetTemplateInput(name: "templates/brief", includeDocument: true))
        _ = try await client.activateSkill(ActivateSkillInput(conversationID: "conv-1", name: "skills/playwright-cli", args: "args"))

        XCTAssertEqual(seen, [
            "GET /v1/templates/templates%2Fbrief",
            "POST /v1/skills/skills%2Fplaywright-cli/activate"
        ])
        URLProtocolStub.requestHandler = nil
    }

    func testTurnAndConversationControlRoutesMatchSharedClientContract() async throws {
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [URLProtocolStub.self]
        let session = URLSession(configuration: configuration)
        let endpoint = EndpointConfig(baseURL: try XCTUnwrap(URL(string: "http://localhost:8585")))
        let client = AgentlyClient(endpoints: ["appAPI": endpoint], session: session)

        var seen: [(String, String)] = []
        URLProtocolStub.requestHandler = { request in
            let url = try XCTUnwrap(request.url)
            seen.append((request.httpMethod ?? "", url.path))
            let response = HTTPURLResponse(url: url, statusCode: 200, httpVersion: nil, headerFields: [
                "Content-Type": "application/json"
            ])!
            let body: String
            switch (request.httpMethod ?? "", url.path) {
            case ("POST", "/v1/turns/turn-1/cancel"):
                body = #"{"cancelled":true}"#
            case ("POST", "/v1/conversations/conv-1/turns/turn-1/steer"):
                let raw = try XCTUnwrap(self.requestBodyString(request))
                XCTAssertTrue(raw.contains(#""content":"follow up""#))
                body = #"{"messageId":"msg-1","turnId":"turn-1","status":"accepted"}"#
            case ("DELETE", "/v1/conversations/conv-1/turns/turn-queued"):
                body = #"{}"#
            case ("POST", "/v1/conversations/conv-1/turns/turn-queued/move"):
                let raw = try XCTUnwrap(self.requestBodyString(request))
                XCTAssertTrue(raw.contains(#""direction":"up""#))
                body = #"{}"#
            case ("PATCH", "/v1/conversations/conv-1/turns/turn-queued"):
                let raw = try XCTUnwrap(self.requestBodyString(request))
                XCTAssertTrue(raw.contains(#""content":"edited""#))
                body = #"{}"#
            case ("POST", "/v1/conversations/conv-1/turns/turn-queued/force-steer"):
                body = #"{"messageId":"msg-2","turnId":"turn-queued","status":"accepted"}"#
            case ("POST", "/v1/conversations/conv-1/terminate"):
                body = #"{}"#
            case ("POST", "/v1/conversations/conv-1/compact"):
                body = #"{}"#
            case ("POST", "/v1/conversations/conv-1/prune"):
                body = #"{}"#
            default:
                XCTFail("unexpected request \(request.httpMethod ?? "") \(url.path)")
                body = #"{}"#
            }
            return (response, body.data(using: .utf8)!)
        }

        let cancelled = try await client.cancelTurn(turnID: "turn-1")
        let steer = try await client.steerTurn(
            SteerTurnInput(
                conversationID: "conv-1",
                turnID: "turn-1",
                content: "follow up",
                role: "user"
            )
        )
        try await client.cancelQueuedTurn(conversationID: "conv-1", turnID: "turn-queued")
        try await client.moveQueuedTurn(MoveQueuedTurnInput(conversationID: "conv-1", turnID: "turn-queued", direction: "up"))
        try await client.editQueuedTurn(EditQueuedTurnInput(conversationID: "conv-1", turnID: "turn-queued", content: "edited"))
        let forced = try await client.forceSteerQueuedTurn(conversationID: "conv-1", turnID: "turn-queued")
        try await client.terminateConversation(conversationID: "conv-1")
        try await client.compactConversation(conversationID: "conv-1")
        try await client.pruneConversation(conversationID: "conv-1")

        XCTAssertTrue(cancelled)
        XCTAssertEqual(steer.messageID, "msg-1")
        XCTAssertEqual(forced.messageID, "msg-2")
        XCTAssertEqual(seen.map(\.0), ["POST", "POST", "DELETE", "POST", "PATCH", "POST", "POST", "POST", "POST"])
        XCTAssertEqual(seen.map(\.1), [
            "/v1/turns/turn-1/cancel",
            "/v1/conversations/conv-1/turns/turn-1/steer",
            "/v1/conversations/conv-1/turns/turn-queued",
            "/v1/conversations/conv-1/turns/turn-queued/move",
            "/v1/conversations/conv-1/turns/turn-queued",
            "/v1/conversations/conv-1/turns/turn-queued/force-steer",
            "/v1/conversations/conv-1/terminate",
            "/v1/conversations/conv-1/compact",
            "/v1/conversations/conv-1/prune"
        ])
        URLProtocolStub.requestHandler = nil
    }

    func testDatasourceAndLookupRoutesEncodeSlashBearingIdentifiers() async throws {
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [URLProtocolStub.self]
        let session = URLSession(configuration: configuration)
        let endpoint = EndpointConfig(baseURL: try XCTUnwrap(URL(string: "http://localhost:8585")))
        let client = AgentlyClient(endpoints: ["appAPI": endpoint], session: session)

        var seen: [String] = []
        URLProtocolStub.requestHandler = { request in
            let url = try XCTUnwrap(request.url)
            let components = try XCTUnwrap(URLComponents(url: url, resolvingAgainstBaseURL: false))
            seen.append("\(request.httpMethod ?? "") \(components.percentEncodedPath)?\(components.percentEncodedQuery ?? "")")
            let response = HTTPURLResponse(url: url, statusCode: 200, httpVersion: nil, headerFields: [
                "Content-Type": "application/json"
            ])!
            let body: String
            switch (request.httpMethod ?? "", components.percentEncodedPath) {
            case ("POST", "/v1/api/datasources/sources%2Fmain/fetch"):
                let bodyString = try XCTUnwrap(self.requestBodyString(request))
                let bodyData = try XCTUnwrap(bodyString.data(using: .utf8))
                let payload = try XCTUnwrap(JSONSerialization.jsonObject(with: bodyData) as? [String: Any])
                XCTAssertEqual(payload["conversationId"] as? String, "conv-1")
                let inputs = payload["inputs"] as? [String: Any]
                XCTAssertEqual(inputs?["query"] as? String, "find")
                body = #"{"rows":[]}"#
            case ("DELETE", "/v1/api/datasources/sources%2Fmain/cache"):
                XCTAssertEqual(components.percentEncodedQuery, "inputsHash=hash%2Bone%2Ftwo")
                body = #"{}"#
            case ("GET", "/v1/api/lookups/registry"):
                XCTAssertEqual(components.percentEncodedQuery, "context=dialog%3Amain%2Fform%2Bsearch")
                body = #"{"entries":[]}"#
            default:
                XCTFail("unexpected request \(request.httpMethod ?? "") \(url.path)")
                body = #"{}"#
            }
            return (response, body.data(using: .utf8)!)
        }

        _ = try await client.fetchDatasource(
            FetchDatasourceInput(
                id: " sources/main ",
                inputs: ["query": .string("find")],
                conversationId: " conv-1 "
            )
        )
        try await client.invalidateDatasourceCache(InvalidateDatasourceCacheInput(id: " sources/main ", inputsHash: " hash+one/two "))
        _ = try await client.listLookupRegistry(ListLookupRegistryInput(context: " dialog:main/form+search "))

        XCTAssertEqual(seen, [
            "POST /v1/api/datasources/sources%2Fmain/fetch?",
            "DELETE /v1/api/datasources/sources%2Fmain/cache?inputsHash=hash%2Bone%2Ftwo",
            "GET /v1/api/lookups/registry?context=dialog%3Amain%2Fform%2Bsearch"
        ])
        URLProtocolStub.requestHandler = nil
    }

    func testDatasourceAndLookupRoutesRejectBlankIdentifiersBeforeDispatch() async throws {
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [URLProtocolStub.self]
        let session = URLSession(configuration: configuration)
        let endpoint = EndpointConfig(baseURL: try XCTUnwrap(URL(string: "http://localhost:8585")))
        let client = AgentlyClient(endpoints: ["appAPI": endpoint], session: session)

        var requestCount = 0
        URLProtocolStub.requestHandler = { request in
            requestCount += 1
            let url = try XCTUnwrap(request.url)
            let response = HTTPURLResponse(url: url, statusCode: 500, httpVersion: nil, headerFields: nil)!
            return (response, Data())
        }

        do {
            _ = try await client.fetchDatasource(FetchDatasourceInput(id: "   "))
            XCTFail("expected blank datasource id to throw")
        } catch AgentlySDKError.invalidArgument(let message) {
            XCTAssertEqual(message, "datasource id is required")
        }

        do {
            try await client.invalidateDatasourceCache(InvalidateDatasourceCacheInput(id: "   "))
            XCTFail("expected blank datasource id to throw")
        } catch AgentlySDKError.invalidArgument(let message) {
            XCTAssertEqual(message, "datasource id is required")
        }

        do {
            _ = try await client.listLookupRegistry(ListLookupRegistryInput(context: "   "))
            XCTFail("expected blank lookup context to throw")
        } catch AgentlySDKError.invalidArgument(let message) {
            XCTAssertEqual(message, "lookup context is required")
        }

        XCTAssertEqual(requestCount, 0)
        URLProtocolStub.requestHandler = nil
    }

    func testTrackConversationHydratesThenAppliesEvents() async throws {
        let client = AgentlyClient(endpoints: ["appAPI": EndpointConfig(baseURL: try XCTUnwrap(URL(string: "http://localhost:8585")))])
        let initial = ConversationStateResponse(
            conversation: ConversationState(conversationID: "conv-1", turns: []),
            feeds: [ActiveFeedState(feedID: "feed-1", title: "Initial", itemCount: 1)]
        )
        let events = AsyncThrowingStream<SSEEvent, Error> { continuation in
            continuation.yield(SSEEvent(data: #"{"type":"turn_started","conversationId":"conv-1","turnId":"turn-1"}"#))
            continuation.yield(SSEEvent(data: #"{"type":"assistant","conversationId":"conv-1","turnId":"turn-1","messageId":"msg-1","content":"hello","status":"completed","patch":{"role":"assistant"}}"#))
            continuation.finish()
        }

        let stream = client.trackConversation(
            conversationID: "conv-1",
            initialStateLoader: { id in
                XCTAssertEqual(id, "conv-1")
                return initial
            },
            eventStream: { id in
                XCTAssertEqual(id, "conv-1")
                return events
            }
        )

        var snapshots: [ConversationStreamSnapshot] = []
        for try await snapshot in stream {
            snapshots.append(snapshot)
        }

        XCTAssertEqual(snapshots.count, 3)
        XCTAssertEqual(snapshots.first?.feeds.first?.feedID, "feed-1")
        XCTAssertEqual(snapshots.last?.activeTurnID, "turn-1")
        XCTAssertEqual(snapshots.last?.bufferedMessages.first?.content, "hello")
    }

    func testTrackConversationHydratesRunningTurnAsActiveBeforeNewEvents() async throws {
        let client = AgentlyClient(endpoints: ["appAPI": EndpointConfig(baseURL: try XCTUnwrap(URL(string: "http://localhost:8585")))])
        let initial = ConversationStateResponse(
            conversation: ConversationState(
                conversationID: "conv-1",
                turns: [
                    TurnState(
                        turnID: "turn-running",
                        status: "running",
                        assistant: AssistantState(
                            final: AssistantMessageState(
                                messageID: "msg-running",
                                content: "visible live content",
                                createdAt: "2026-06-05T09:45:00Z"
                            )
                        ),
                        createdAt: "2026-06-05T09:45:00Z"
                    )
                ]
            )
        )
        let events = AsyncThrowingStream<SSEEvent, Error> { continuation in
            continuation.finish()
        }

        let stream = client.trackConversation(
            conversationID: "conv-1",
            initialStateLoader: { id in
                XCTAssertEqual(id, "conv-1")
                return initial
            },
            eventStream: { id in
                XCTAssertEqual(id, "conv-1")
                return events
            }
        )

        var snapshots: [ConversationStreamSnapshot] = []
        for try await snapshot in stream {
            snapshots.append(snapshot)
        }

        XCTAssertEqual(snapshots.count, 1)
        XCTAssertEqual(snapshots.first?.activeTurnID, "turn-running")
        XCTAssertEqual(snapshots.first?.bufferedMessages.first?.content, "visible live content")
    }

    func testConversationStreamTrackerHydrateDoesNotOverwriteActiveTurnMessageOwnedBySSE() async throws {
        let tracker = ConversationStreamTracker()

        _ = await tracker.apply(SSEEvent(data: #"{"type":"turn_started","conversationId":"conv-1","turnId":"turn-live"}"#))
        _ = await tracker.apply(SSEEvent(data: #"{"type":"assistant","conversationId":"conv-1","turnId":"turn-live","messageId":"assistant-live","assistantMessageId":"assistant-live","content":"SSE active response","patch":{"role":"assistant"}}"#))
        _ = await tracker.apply(SSEEvent(data: #"{"type":"model_started","conversationId":"conv-1","turnId":"turn-live","assistantMessageId":"assistant-live","status":"running"}"#))

        await tracker.hydrate(
            try JSONDecoder.agently().decode(
                ConversationStateResponse.self,
                from: XCTUnwrap("""
                {
                  "conversation": {
                    "conversationId": "conv-1",
                    "turns": [
                      {
                        "turnId": "turn-history",
                        "status": "completed",
                        "assistant": {
                          "final": {
                            "messageId": "assistant-history",
                            "content": "Historical response"
                          }
                        }
                      },
                      {
                        "turnId": "turn-live",
                        "status": "running",
                        "assistant": {
                          "final": {
                            "messageId": "assistant-live",
                            "content": "stale transcript response"
                          }
                        },
                        "execution": {
                          "pages": [
                            {
                              "pageId": "stale-page",
                              "assistantMessageId": "assistant-live",
                              "turnId": "turn-live",
                              "status": "completed",
                              "content": "stale transcript execution"
                            }
                          ]
                        }
                      }
                    ]
                  }
                }
                """.data(using: .utf8))
            )
        )

        let snapshot = await tracker.currentSnapshot()
        XCTAssertEqual(snapshot.activeTurnID, "turn-live")
        XCTAssertEqual(snapshot.bufferedMessages.first(where: { $0.id == "assistant-live" })?.content, "SSE active response")
        XCTAssertEqual(snapshot.bufferedMessages.first(where: { $0.id == "assistant-history" })?.content, "Historical response")
        XCTAssertEqual(snapshot.liveExecutionGroupsByID["assistant-live"]?.status, "running")
        XCTAssertNil(snapshot.liveExecutionGroupsByID["assistant-live"]?.content)
    }

    func testConversationStreamTrackerPlannerEventsUpdateTurnPlannerState() async throws {
        let tracker = ConversationStreamTracker()

        _ = await tracker.apply(SSEEvent(data: #"{"type":"turn_started","conversationId":"conv-1","turnId":"turn-1"}"#))
        _ = await tracker.apply(SSEEvent(data: #"{"type":"planner.selected","conversationId":"conv-1","turnId":"turn-1","plannerTrigger":"exploratory_strategy","plannerStaticProfile":"repo_analysis"}"#))
        _ = await tracker.apply(SSEEvent(data: #"{"type":"planner.output","conversationId":"conv-1","turnId":"turn-1","plannerStrategyFamily":"troubleshoot","plannerAttempt":1,"plannerOutputPayloadId":"planner-output:conv-1:turn-1"}"#))
        _ = await tracker.apply(SSEEvent(data: #"{"type":"planner.validated","conversationId":"conv-1","turnId":"turn-1","plannerAttempt":1,"plannerValidated":true}"#))

        let planner = await tracker.currentSnapshot().plannerByTurnID["turn-1"]
        XCTAssertEqual(planner?.status, "validated")
        XCTAssertEqual(planner?.trigger, "exploratory_strategy")
        XCTAssertEqual(planner?.staticProfile, "repo_analysis")
        XCTAssertEqual(planner?.strategyFamily, "troubleshoot")
        XCTAssertEqual(planner?.attempt, 1)
        XCTAssertEqual(planner?.outputPayloadID, "planner-output:conv-1:turn-1")
        XCTAssertEqual(planner?.validated, true)
    }

    func testConversationStreamTrackerHydratesPlannerStateForPastTurns() async throws {
        let tracker = ConversationStreamTracker()

        await tracker.hydrate(
            try JSONDecoder.agently().decode(
                ConversationStateResponse.self,
                from: XCTUnwrap("""
                {
                  "conversation": {
                    "conversationId": "conv-1",
                    "turns": [
                      {
                        "turnId": "turn-past",
                        "status": "completed",
                        "planner": {
                          "status": "failed",
                          "trigger": "low_confidence",
                          "strategyFamily": "troubleshoot",
                          "attempt": 2,
                          "secondPolicy": "clarify",
                          "outputPayloadId": "planner-output:conv-1:turn-past",
                          "validated": false
                        }
                      }
                    ]
                  }
                }
                """.data(using: .utf8))
            )
        )

        let planner = await tracker.currentSnapshot().plannerByTurnID["turn-past"]
        XCTAssertEqual(planner?.status, "failed")
        XCTAssertEqual(planner?.trigger, "low_confidence")
        XCTAssertEqual(planner?.strategyFamily, "troubleshoot")
        XCTAssertEqual(planner?.attempt, 2)
        XCTAssertEqual(planner?.secondPolicy, "clarify")
        XCTAssertEqual(planner?.outputPayloadID, "planner-output:conv-1:turn-past")
        XCTAssertEqual(planner?.validated, false)
    }

    func testConversationStreamTrackerTranscriptDoesNotOverwriteActivePlannerStateOwnedBySSE() async throws {
        let tracker = ConversationStreamTracker()

        _ = await tracker.apply(SSEEvent(data: #"{"type":"turn_started","conversationId":"conv-1","turnId":"turn-live"}"#))
        _ = await tracker.apply(SSEEvent(data: #"{"type":"planner.failed","conversationId":"conv-1","turnId":"turn-live","plannerTrigger":"exploratory_strategy","plannerAttempt":2,"plannerSecondPolicy":"block"}"#))

        await tracker.hydrate(
            try JSONDecoder.agently().decode(
                ConversationStateResponse.self,
                from: XCTUnwrap("""
                {
                  "conversation": {
                    "conversationId": "conv-1",
                    "turns": [
                      {
                        "turnId": "turn-live",
                        "status": "running",
                        "planner": {
                          "status": "selected",
                          "trigger": "low_confidence",
                          "attempt": 1
                        }
                      }
                    ]
                  }
                }
                """.data(using: .utf8))
            )
        )

        let planner = await tracker.currentSnapshot().plannerByTurnID["turn-live"]
        XCTAssertEqual(planner?.status, "failed")
        XCTAssertEqual(planner?.trigger, "exploratory_strategy")
        XCTAssertEqual(planner?.attempt, 2)
        XCTAssertEqual(planner?.secondPolicy, "block")
    }

    func testTrackConversationStartsStreamBeforeHydrationAndSkipsHydratedEventSequences() async throws {
        let client = AgentlyClient(endpoints: ["appAPI": EndpointConfig(baseURL: try XCTUnwrap(URL(string: "http://localhost:8585")))])
        let streamConstructed = DispatchSemaphore(value: 0)
        let initialJSON = """
        {
          "eventCursor": "2026-06-05T09:45:00Z",
          "conversation": {
            "conversationId": "conv-1",
            "turns": [
              {
                "turnId": "turn-running",
                "status": "running",
                "createdAt": "2026-06-05T09:45:00Z",
                "messages": [
                  {
                    "messageId": "msg-running",
                    "role": "assistant",
                    "content": "visible live content",
                    "sequence": 7,
                    "status": "running"
                  }
                ],
                "assistant": {
                  "final": {
                    "messageId": "msg-running",
                    "content": "visible live content",
                    "createdAt": "2026-06-05T09:45:00Z"
                  }
                }
              }
            ]
          }
        }
        """
        let initial = try JSONDecoder.agently().decode(
            ConversationStateResponse.self,
            from: XCTUnwrap(initialJSON.data(using: .utf8))
        )

        let stream = client.trackConversation(
            conversationID: "conv-1",
            initialStateLoader: { id in
                XCTAssertEqual(id, "conv-1")
                XCTAssertEqual(streamConstructed.wait(timeout: .now()), .success)
                return initial
            },
            eventStream: { id in
                XCTAssertEqual(id, "conv-1")
                streamConstructed.signal()
                return AsyncThrowingStream<SSEEvent, Error> { continuation in
                    continuation.yield(SSEEvent(data: #"{"type":"text_delta","conversationId":"conv-1","turnId":"turn-running","assistantMessageId":"msg-running","eventSeq":7,"createdAt":"2026-06-05T09:45:01Z","content":" duplicate"}"#))
                    continuation.finish()
                }
            }
        )

        var snapshots: [ConversationStreamSnapshot] = []
        for try await snapshot in stream {
            snapshots.append(snapshot)
        }

        XCTAssertEqual(snapshots.count, 2)
        XCTAssertEqual(snapshots.last?.activeTurnID, "turn-running")
        XCTAssertEqual(snapshots.last?.bufferedMessages.first?.content, "visible live content")
    }

    func testTrackConversationSkipsPreCursorEventAndAppliesLaterLiveEvent() async throws {
        let client = AgentlyClient(endpoints: ["appAPI": EndpointConfig(baseURL: try XCTUnwrap(URL(string: "http://localhost:8585")))])
        let initialJSON = """
        {
          "eventCursor": "2026-06-05T10:00:00Z",
          "conversation": {
            "conversationId": "conv-1",
            "turns": [
              {
                "turnId": "turn-1",
                "status": "running",
                "assistant": {
                  "final": {
                    "messageId": "assistant-1",
                    "content": "Hello"
                  }
                }
              }
            ]
          }
        }
        """
        let initial = try JSONDecoder.agently().decode(
            ConversationStateResponse.self,
            from: XCTUnwrap(initialJSON.data(using: .utf8))
        )
        let events = AsyncThrowingStream<SSEEvent, Error> { continuation in
            continuation.yield(SSEEvent(data: #"{"type":"text_delta","conversationId":"conv-1","turnId":"turn-1","messageId":"assistant-1","assistantMessageId":"assistant-1","eventSeq":1,"content":" stale","createdAt":"2026-06-05T10:00:00Z"}"#))
            continuation.yield(SSEEvent(data: #"{"type":"text_delta","conversationId":"conv-1","turnId":"turn-1","messageId":"assistant-1","assistantMessageId":"assistant-1","eventSeq":2,"content":" live","createdAt":"2026-06-05T10:00:01Z"}"#))
            continuation.finish()
        }

        let stream = client.trackConversation(
            conversationID: "conv-1",
            initialStateLoader: { id in
                XCTAssertEqual(id, "conv-1")
                return initial
            },
            eventStream: { id in
                XCTAssertEqual(id, "conv-1")
                return events
            }
        )

        var snapshots: [ConversationStreamSnapshot] = []
        for try await snapshot in stream {
            snapshots.append(snapshot)
        }

        XCTAssertEqual(snapshots.count, 3)
        XCTAssertEqual(snapshots[0].bufferedMessages.first(where: { $0.id == "assistant-1" })?.content, "Hello")
        XCTAssertEqual(snapshots[1].bufferedMessages.first(where: { $0.id == "assistant-1" })?.content, "Hello")
        XCTAssertEqual(snapshots[2].bufferedMessages.first(where: { $0.id == "assistant-1" })?.content, "Hello live")
    }

    func testConversationStreamTrackerHydrateUsesCursorNotTranscriptMessageSequenceForLiveDelta() async throws {
        let tracker = ConversationStreamTracker()
        let cursor = "2026-06-05T10:00:00Z"
        let initialJSON = """
        {
          "eventCursor": "2026-06-05T10:00:00Z",
          "conversation": {
            "conversationId": "conv-1",
            "turns": [
              {
                "turnId": "turn-1",
                "status": "running",
                "messages": [
                  {
                    "messageId": "assistant-1",
                    "role": "assistant",
                    "content": "Hello",
                    "sequence": 100
                  }
                ],
                "assistant": {
                  "final": {
                    "messageId": "assistant-1",
                    "content": "Hello"
                  }
                }
              }
            ]
          }
        }
        """
        let initial = try JSONDecoder.agently().decode(
            ConversationStateResponse.self,
            from: XCTUnwrap(initialJSON.data(using: .utf8))
        )

        await tracker.hydrate(initial)
        _ = await tracker.apply(
            SSEEvent(data: #"{"type":"text_delta","conversationId":"conv-1","turnId":"turn-1","messageId":"assistant-1","assistantMessageId":"assistant-1","eventSeq":7,"content":" duplicate","createdAt":"2026-06-05T10:00:00Z"}"#),
            hydrationCursor: cursor
        )
        _ = await tracker.apply(
            SSEEvent(data: #"{"type":"text_delta","conversationId":"conv-1","turnId":"turn-1","messageId":"assistant-1","assistantMessageId":"assistant-1","content":" stale","createdAt":"2026-06-05T10:00:00Z"}"#),
            hydrationCursor: cursor
        )
        _ = await tracker.apply(
            SSEEvent(data: #"{"type":"text_delta","conversationId":"conv-1","turnId":"turn-1","messageId":"assistant-1","assistantMessageId":"assistant-1","eventSeq":100,"content":" duplicate","createdAt":"2026-06-05T10:00:01Z"}"#),
            hydrationCursor: cursor
        )
        _ = await tracker.apply(
            SSEEvent(data: #"{"type":"text_delta","conversationId":"conv-1","turnId":"turn-1","messageId":"assistant-1","assistantMessageId":"assistant-1","eventSeq":1,"content":" live","createdAt":"2026-06-05T10:00:01Z"}"#),
            hydrationCursor: cursor
        )

        let snapshot = await tracker.currentSnapshot()
        XCTAssertEqual(snapshot.activeTurnID, "turn-1")
        XCTAssertEqual(snapshot.bufferedMessages.first(where: { $0.id == "assistant-1" })?.content, "Hello live")
    }

    func testConversationStreamTrackerHydratesExecutionGroupsFromTranscriptToolPayloads() async throws {
        let json = """
        {
          "conversation": {
            "conversationId": "conv-1",
            "turns": [
              {
                "turnId": "turn-running",
                "status": "running",
                "createdAt": "2026-06-05T09:45:00Z",
                "execution": {
                  "pages": [
                    {
                      "pageId": "page-1",
                      "assistantMessageId": "assistant-1",
                      "turnId": "turn-running",
                      "sequence": 9,
                      "status": "running",
                      "toolSteps": [
                        {
                          "toolCallId": "tool-1",
                          "toolName": "ui/view/open",
                          "status": "completed",
                          "requestPayload": { "id": "reportWindow" },
                          "responsePayload": {
                            "windowId": "reportWindow__conv-1",
                            "conversationId": "conv-1",
                            "windowKey": "reportWindow",
                            "windowTitle": "Report Review",
                            "presentation": "hosted",
                            "region": "chat.top",
                            "parentKey": "chat/new"
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
        let response = try JSONDecoder.agently().decode(
            ConversationStateResponse.self,
            from: XCTUnwrap(json.data(using: .utf8))
        )
        let tracker = ConversationStreamTracker()

        await tracker.hydrate(response)
        let snapshot = await tracker.currentSnapshot()
        let restore = deriveHostedWorkspaceRestoreState(from: snapshot)

        XCTAssertEqual(snapshot.activeTurnID, "turn-running")
        XCTAssertEqual(
            snapshot.liveExecutionGroupsByID["assistant-1"]?.toolSteps.first?.responsePayload,
            .object([
                "windowId": .string("reportWindow__conv-1"),
                "conversationId": .string("conv-1"),
                "windowKey": .string("reportWindow"),
                "windowTitle": .string("Report Review"),
                "presentation": .string("hosted"),
                "region": .string("chat.top"),
                "parentKey": .string("chat/new")
            ])
        )
        XCTAssertEqual(restore?.selectedWindowId, "reportWindow__conv-1")
        XCTAssertEqual(restore?.windows.first?.windowTitle, "Report Review")
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

    func testConversationStreamTrackerPreservesUsageEvents() async {
        let tracker = ConversationStreamTracker()
        let payload = """
        {"type":"usage","conversationId":"conv-1","usageInputTokens":120,"usageOutputTokens":30,"usageEmbeddingTokens":5,"usageTotalTokens":155}
        """

        let snapshot = await tracker.apply(SSEEvent(data: payload))

        XCTAssertEqual(snapshot.usage?.inputTokens, 120)
        XCTAssertEqual(snapshot.usage?.outputTokens, 30)
        XCTAssertEqual(snapshot.usage?.embeddingTokens, 5)
        XCTAssertEqual(snapshot.usage?.totalTokens, 155)
    }
}
