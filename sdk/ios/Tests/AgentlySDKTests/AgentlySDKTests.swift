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
}
