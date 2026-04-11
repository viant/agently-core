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
}
