import XCTest
@testable import AgentlySDK

final class WorkspaceContextRestoreTests: XCTestCase {
    func testChronologicalContextSnapshotKeepsExactKnownConversationAndLaterEdits() throws {
        func window(_ id: String, conversation: String = "conversation", state: [String: Any]) -> [String: Any] {
            ["windowId": id, "conversationId": conversation, "windowKey": "report", "presentation": "hosted", "region": "chat.top", "parentKey": "chat/new", "windowForm": ["reportBuilder:builder": state]]
        }
        let admitted: [String: Any] = ["activeDynamicFilterKeys": ["orderIds"], "dynamicFilterValues": ["orderIds": "7"], "opaque": "original"]
        let known = window("report__conversation", state: admitted)
        let context: [String: Any] = ["windows": [["window": known], ["window": window("unknown", state: ["opaque": "unknown"])], ["window": window("report__conversation", conversation: "foreign", state: ["opaque": "foreign"])]]]
        var steps: [[String: Any]] = [
            ["toolCallId": "open", "toolName": "ui/view/open", "status": "completed", "responsePayload": window("report__conversation", state: [:])],
            ["toolCallId": "context", "toolName": "ui/context/get", "status": "completed", "responsePayload": context]
        ]
        func restore() throws -> HostedWorkspaceRestoreState? {
            let value: [String: Any] = ["conversation": ["conversationId": "conversation", "turns": [["turnId": "turn", "execution": ["pages": [["pageId": "page", "toolSteps": steps]]]]]]]
            return deriveHostedWorkspaceRestoreState(from: try JSONDecoder.agently().decode(ConversationStateResponse.self, from: JSONSerialization.data(withJSONObject: value)))
        }
        let initial = try restore()
        XCTAssertEqual(initial?.windows.count, 1)
        XCTAssertEqual(initial?.windows.first?.windowForm?["reportBuilder:builder"], .object(["activeDynamicFilterKeys": .array([.string("orderIds")]), "dynamicFilterValues": .object(["orderIds": .string("7")]), "opaque": .string("original")]))
        steps.append(["toolCallId": "edit", "toolName": "ui/window/setFormData", "status": "completed", "requestPayload": ["windowId": "report__conversation", "values": ["reportBuilder:builder": ["opaque": "edited"]]], "responsePayload": ["ok": true]])
        let edited = try restore()
        guard case .object(let editedState)? = edited?.windows.first?.windowForm?["reportBuilder:builder"] else { return XCTFail("Missing edited state") }
        XCTAssertEqual(editedState["opaque"], .string("edited"))
    }
}
