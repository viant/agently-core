import Foundation
import XCTest
@testable import AgentlySDK

final class ConversationWireDecodingTests: XCTestCase {
    func testCanonicalRowsDecodeWithoutDiscardingConversationIdentity() throws {
        let data = Data(#"{"Rows":[{"id":"canonical","agentId":"Steward","title":"Plan","lastActivity":"2026-10-04T20:00:00Z","promptTokens":7}],"HasMore":false}"#.utf8)
        let page = try JSONDecoder.agently().decode(ConversationPage.self, from: data)
        XCTAssertEqual(page.rows.first?.id, "canonical")
        XCTAssertEqual(page.rows.first?.agentID, "Steward")
        XCTAssertEqual(page.rows.first?.title, "Plan")
        XCTAssertEqual(page.rows.first?.promptTokens, 7)
    }
    func testLegacyAndGoInitialismIdentitiesRemainSupported() throws {
        for key in ["Id", "ID"] {
            let data = Data("{\"Rows\":[{\"\(key)\":\"legacy\",\"Title\":\"Existing\"}],\"HasMore\":true}".utf8)
            let page = try JSONDecoder.agently().decode(ConversationPage.self, from: data)
            XCTAssertEqual(page.rows.first?.id, "legacy"); XCTAssertTrue(page.hasMore)
        }
    }
    func testLowercasePageEnvelopeAndLegacyEncodingRemainCompatible() throws {
        let page = try JSONDecoder.agently().decode(ConversationPage.self, from: Data(#"{"rows":[{"id":"canonical"}],"nextCursor":"next","hasMore":true}"#.utf8))
        XCTAssertEqual(page.nextCursor, "next"); XCTAssertTrue(page.hasMore)
        let encoded = try AgUiValue.parse(JSONEncoder.agently().encode(page.rows[0]))
        XCTAssertEqual(encoded["Id"]?.string, "canonical")
    }
    func testMissingAndInvalidIDsAreRejectedInsteadOfInvented() {
        XCTAssertThrowsError(try JSONDecoder.agently().decode(Conversation.self, from: Data(#"{"title":"missing"}"#.utf8)))
        XCTAssertThrowsError(try JSONDecoder.agently().decode(Conversation.self, from: Data(#"{"id":17}"#.utf8)))
    }
}
