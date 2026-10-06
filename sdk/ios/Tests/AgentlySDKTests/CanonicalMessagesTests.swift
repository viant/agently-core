import XCTest
@testable import AgentlySDK

final class CanonicalMessagesTests: XCTestCase {
    func testCanonicalTaskMessagesAndAggregatesKeepExactIdentities() {
        let turn = TurnState(turnID: "t", status: "completed", messages: [
            TurnMessageState(messageID: "router", role: "assistant", content: "{\"classification\":true}", sequence: 1, mode: "router"),
            TurnMessageState(messageID: "interim", role: "assistant", content: "Preliminary findings", sequence: 2, mode: "task"),
            TurnMessageState(messageID: "final", role: "assistant", content: "Final report", sequence: 4, mode: "task")
        ], assistant: AssistantState(narration: AssistantMessageState(messageID: "narration", content: "Checking launch day"), final: AssistantMessageState(messageID: "final", content: "Final report")))
        let messages = canonicalAssistantMessages(turn)
        XCTAssertEqual(messages.map(\.messageID), ["interim", "narration", "final"])
        XCTAssertEqual(messages.filter { $0.messageID == "final" }.count, 1)
        let sameBody = TurnState(turnID: "t", messages: [
            TurnMessageState(messageID: "a", role: "assistant", content: "same", mode: "task"),
            TurnMessageState(messageID: "b", role: "assistant", content: "same", mode: "task")
        ])
        XCTAssertEqual(canonicalAssistantMessages(sameBody).map(\.messageID), ["a", "b"])
    }
    func testRepeatedCanonicalIdentityEnrichesAndBlankIdentityIsIgnored() {
        let turn = TurnState(turnID: "t", messages: [
            TurnMessageState(messageID: "", role: "assistant", content: "invalid"),
            TurnMessageState(messageID: "a", role: "assistant", content: "body", sequence: 2, mode: "task"),
            TurnMessageState(messageID: "a", role: "assistant", status: "completed")
        ])
        let messages = canonicalAssistantMessages(turn)
        XCTAssertEqual(messages.count, 1)
        XCTAssertEqual(messages.first?.content, "body")
        XCTAssertEqual(messages.first?.sequence, 2)
        XCTAssertEqual(messages.first?.status, "completed")
    }

    func testActualGoCanonicalizerModelInclusiveFixtureRetainsVisibleTimeline() throws {
        let url = try XCTUnwrap(Bundle.module.url(forResource: "canonical-native-timeline", withExtension: "json"))
        let decoder = JSONDecoder()
        decoder.dateDecodingStrategy = .iso8601
        let turn = try decoder.decode(TurnState.self, from: Data(contentsOf: url))
        XCTAssertEqual(turn.user?.messageID, "user")
        let messages = canonicalAssistantMessages(turn)
        XCTAssertEqual(messages.map(\.messageID), ["interim", "narration", "final"])
        XCTAssertEqual(messages.compactMap(\.content), ["Preliminary findings", "Checking results", "Final report"])
        XCTAssertEqual(turn.execution?.pages.flatMap(\.modelSteps).count, 3)
    }

}
