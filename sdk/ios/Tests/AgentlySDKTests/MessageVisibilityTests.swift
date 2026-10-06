import Foundation
import XCTest
@testable import AgentlySDK

final class MessageVisibilityTests: XCTestCase {
    func testInternalModesDoNotHideVisibleJSONOrMergeDistinctIDs() async throws {
        let tracker = ConversationStreamTracker()
        let body = "{\"classification\":true}"
        func event(_ type: String, _ id: String, _ mode: String? = nil) throws -> SSEEvent {
            var payload: [String: Any] = ["type": type, "conversationId": "c", "turnId": "t", "messageId": id, "content": body, "contentMode": "snapshot"]
            if let mode { payload["mode"] = mode }
            let bytes = try JSONSerialization.data(withJSONObject: payload)
            return SSEEvent(data: String(decoding: bytes, as: UTF8.self))
        }
        _ = await tracker.apply(try event("model_started", "router", "router"))
        _ = await tracker.apply(try event("text_delta", "router"))
        _ = await tracker.apply(try event("text_delta", "task", "task"))
        _ = await tracker.apply(try event("text_delta", "other", "task"))
        let snapshot = await tracker.apply(try event("text_delta", "task", "task"))
        XCTAssertEqual(Set(snapshot.bufferedMessages.map(\.id)), Set(["task", "other"]))
        XCTAssertEqual(snapshot.bufferedMessages.first(where: { $0.id == "task" })?.content, body)
    }
    func testModePolicyUsesOnlyKnownMetadata() {
        XCTAssertTrue(isInternalMessageMode(" ROUTER "))
        XCTAssertTrue(isInternalMessageMode("chain"))
        XCTAssertFalse(isInternalMessageMode("task"))
        XCTAssertFalse(isInternalMessageMode(nil))
    }
}
