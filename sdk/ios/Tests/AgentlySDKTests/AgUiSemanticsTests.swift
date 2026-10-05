import Foundation
import XCTest
@testable import AgentlySDK
final class AgUiSemanticsTests: XCTestCase {
    func testAll35SharedReducerSemanticCases() throws {
        let url=try XCTUnwrap(Bundle.module.url(forResource:"agui-reducer-semantics",withExtension:"json"))
        let cases=try XCTUnwrap(AgUiValue.parse(Data(contentsOf:url)).array);XCTAssertEqual(cases.count,35)
        for fixture in cases {
            let name=fixture["name"]!.string!,accepted=fixture["accepted"] == .bool(true)
            let start=fixture["events"]!.array!.first{$0["type"]?.string=="RUN_STARTED"}
            let input=try AgUiRunInput(threadId:start?["threadId"]?.string ?? "t",runId:start?["runId"]?.string ?? "r",messages:(fixture["initialMessages"]?.array ?? []).map{try AgUiMessage(value:$0.object!)},state:fixture["initialState"])
            let store=try AgUiStore(input:input);var normalized:[AgUiValue]=[];var failure:Error?
            do {for raw in fixture["events"]!.array! {normalized += try store.receive(AgUiEvent(value:raw.object!)).map{.object($0.value)}};normalized += try store.finish().map{.object($0.value)}}catch{failure=error}
            if !accepted {XCTAssertNotNil(failure,"Expected rejection: \(name)");continue}
            if let failure {XCTFail("Accepted reference case \(name) failed: \(failure)");continue}
            XCTAssertEqual(.array(store.messages.map{.object($0.value)}),fixture["expectedMessages"],"Messages: \(name)")
            XCTAssertEqual(store.state,fixture["expectedState"],"State: \(name)")
            XCTAssertEqual(.array(normalized),fixture["normalized"],"Normalization: \(name)")
        }
    }
    func testSemanticMirrorMatchesCanonical() throws {
        let url=try XCTUnwrap(Bundle.module.url(forResource:"agui-reducer-semantics",withExtension:"json"))
        let root=URL(fileURLWithPath:#filePath).deletingLastPathComponent().appendingPathComponent("../../../../protocol/agui/testdata/reducer-semantics.json").standardizedFileURL
        XCTAssertEqual(try Data(contentsOf:url),try Data(contentsOf:root))
    }
}

extension AgUiSemanticsTests {
    func testSharedIntegerSchemaSemanticsAndOpaqueNumberTokens() throws {
        let url=try XCTUnwrap(Bundle.module.url(forResource:"agui-integer-semantics",withExtension:"json"))
        let cases=try XCTUnwrap(AgUiValue.parse(Data(contentsOf:url)).array);XCTAssertEqual(cases.count,66)
        for fixture in cases {
            let name=fixture["name"]!.string!,value=try AgUiValue.parse(fixture["raw"]!.string!)
            if fixture["accepted"] != .bool(true) {XCTAssertThrowsError(try AgUiSchema.validate(value,definition:fixture["definition"]!.string!),name);continue}
            XCTAssertNoThrow(try AgUiSchema.validate(value,definition:fixture["definition"]!.string!),name)
            var probe=value;for key in fixture["probePath"]!.array! {probe=try XCTUnwrap(probe[key.string!])}
            XCTAssertEqual(try probe.jsonString(),fixture["expectedToken"]!.string!,"Raw token: \(name)")
        }
    }
    func testIntegerMirrorMatchesCanonical() throws {
        let url=try XCTUnwrap(Bundle.module.url(forResource:"agui-integer-semantics",withExtension:"json"))
        let root=URL(fileURLWithPath:#filePath).deletingLastPathComponent().appendingPathComponent("../../../../protocol/agui/testdata/integer-semantics.json").standardizedFileURL
        XCTAssertEqual(try Data(contentsOf:url),try Data(contentsOf:root))
    }
}
