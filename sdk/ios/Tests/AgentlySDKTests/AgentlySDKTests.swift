import XCTest
@testable import AgentlySDK

final class AgentlySDKTests: XCTestCase {
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
}
