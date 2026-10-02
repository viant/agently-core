import Foundation
import XCTest
@testable import AgentlySDK

/// Opt-in integration gate against migration/cmd/sdkcontractserver.
final class ActualServerContractTests: XCTestCase {
    func testSSETransportPreservesBlankFramesAndLineEndings() async throws {
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [AgentlySDKTests.URLProtocolStub.self]
        let session = URLSession(configuration: configuration)
        defer { session.invalidateAndCancel(); AgentlySDKTests.URLProtocolStub.requestHandler = nil }
        AgentlySDKTests.URLProtocolStub.requestHandler = { request in
            let response = HTTPURLResponse(url: request.url!, statusCode: 200, httpVersion: nil, headerFields: ["Content-Type": "text/event-stream"])!
            // Comments, LF, CRLF, CR, multiple data lines and a final frame without a delimiter.
            return (response, Data(": comment\ndata:first\n\ndata:second\r\n\r\ndata:third\r\rdata:fourth\ndata:continued".utf8))
        }
        var data: [String] = []
        for try await event in openEventStream(endpoint: EndpointConfig(baseURL: URL(string: "https://fixture.invalid")!), path: "/stream", conversationID: "c", session: session) {
            data.append(event.data.trimmingCharacters(in: .whitespacesAndNewlines))
        }
        XCTAssertEqual(data, ["first", "second", "third", "fourth\ncontinued"])
    }

    func testActualSDK1JSONAuthErrorsAndSSE() async throws {
        guard let path = ProcessInfo.processInfo.environment["AGENTLY_SDK_CONTRACT_READY"] else {
            throw XCTSkip("Set AGENTLY_SDK_CONTRACT_READY to the actual server ready file")
        }
        let ready = try JSONDecoder().decode([String: String].self, from: Data(contentsOf: URL(fileURLWithPath: path)))
        func value(_ key: String) throws -> String { try XCTUnwrap(ready[key]) }
        func client(_ tokenKey: String?) throws -> AgentlyClient {
            var headers: [String: String] = [:]
            if let tokenKey {
                let token = try String(contentsOfFile: value(tokenKey), encoding: .utf8).trimmingCharacters(in: .whitespacesAndNewlines)
                headers["Authorization"] = "Bearer \(token)"
            }
            let session = URLSession(configuration: .ephemeral)
            return AgentlyClient(endpoints: ["appAPI": EndpointConfig(baseURL: try XCTUnwrap(URL(string: value("url"))), headers: headers)], session: session)
        }
        let owner = try client("ownerTokenFile")
        let id = try value("conversationId")
        let conversation = try await owner.getConversation(conversationID: id)
        XCTAssertEqual(conversation.id, id)
        let transcript = try await owner.getTranscript(GetTranscriptInput(conversationID: id))
        XCTAssertEqual(transcript.conversation?.conversationID, id)
        XCTAssertTrue(transcript.conversation?.turns.contains { $0.turnID == "sdk-contract-turn" && $0.user?.content == "fixture prompt" } == true)
        let page = try await owner.listConversations()
        XCTAssertTrue(page.rows.contains { $0.id == id })
        do {
            _ = try await client(nil).getConversation(conversationID: id)
            XCTFail("Unauthenticated conversation read succeeded")
        } catch AgentlySDKError.httpStatus(let status, _) {
            XCTAssertEqual(status, 401)
        } catch { XCTFail("Expected HTTP error, received \(error)") }
        let other = try client("otherTokenFile")
        let baselineOtherRead = try await other.getConversation(conversationID: id)
        XCTAssertEqual(baselineOtherRead.id, id) // Existing detail-read ownership gap; tracked separately.
        do {
            try await other.deleteConversation(conversationID: id)
            XCTFail("Other owner deleted the conversation")
        } catch AgentlySDKError.httpStatus(let status, _) {
            XCTAssertEqual(status, 403)
        } catch { XCTFail("Expected HTTP error, received \(error)") }
        let preserved = try await owner.getConversation(conversationID: id)
        XCTAssertEqual(preserved.id, id)
        do {
            _ = try await owner.getConversation(conversationID: "sdk-contract-missing")
            XCTFail("Missing conversation read succeeded")
        } catch is DecodingError {
            // Existing API returns 200 null; the non-null SDK model rejects it.
        } catch {
            XCTFail("Expected missing-object decode error, received \(error)")
        }
        let event = try await withThrowingTaskGroup(of: SSEEvent.self) { group in
            group.addTask {
                for try await event in owner.streamEvents(conversationID: id) {
                    return event
                }
                throw NSError(domain: "ActualServerContract", code: 1)
            }
            group.addTask {
                try await Task.sleep(nanoseconds: 10_000_000_000)
                throw NSError(domain: "ActualServerContractTimeout", code: 1)
            }
            defer { group.cancelAll() }
            let result = try await group.next()
            return try XCTUnwrap(result)
        }
        let root = try XCTUnwrap(try JSONSerialization.jsonObject(with: Data(event.data.utf8)) as? [String: Any])
        XCTAssertEqual(root["type"] as? String, "usage")
        XCTAssertEqual(root["conversationId"] as? String, id)
        let patch = try XCTUnwrap(root["patch"] as? [String: Any])
        XCTAssertEqual(patch["inputTokens"] as? Int, 11)
        XCTAssertEqual(patch["outputTokens"] as? Int, 7)
    }
}
