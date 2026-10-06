import Foundation
import XCTest
@testable import AgentlySDK

final class ReportingInvocationTests: XCTestCase {
  func testActivationUsesSeparateScopedCASEndpointAndExistingHeaders() async throws {
    let configuration = URLSessionConfiguration.ephemeral
    configuration.protocolClasses = [AgentlySDKTests.URLProtocolStub.self]
    let session = URLSession(configuration: configuration)
    defer { session.invalidateAndCancel(); AgentlySDKTests.URLProtocolStub.requestHandler = nil }
    AgentlySDKTests.URLProtocolStub.requestHandler = { request in
      XCTAssertEqual(request.httpMethod, "POST")
      XCTAssertEqual(request.url?.path, "/v1/api/report-runs/run/activate")
      XCTAssertEqual(request.value(forHTTPHeaderField: "X-App-Client"), "fixture")
      var bytes = request.httpBody ?? Data()
      if let stream = request.httpBodyStream {
        stream.open(); defer { stream.close() }
        var buffer = [UInt8](repeating: 0, count: 1024)
        while stream.hasBytesAvailable { let count = stream.read(&buffer, maxLength: buffer.count); if count <= 0 { break }; bytes.append(contentsOf: buffer.prefix(count)) }
      }
      let body = try JSONDecoder().decode([String: JSONValue].self, from: bytes)
      XCTAssertEqual(body["conversationId"], .string("conversation"))
      XCTAssertEqual(body["expectedRunRevision"], .number(2))
      XCTAssertEqual(body["expectedContextRevision"], .number(0))
      return (HTTPURLResponse(url: request.url!, statusCode: 200, httpVersion: nil, headerFields: ["Content-Type": "application/json"])!, Data(#"{"ownerId":"owner","conversationId":"conversation","activeReportRunId":"run","revision":1}"#.utf8))
    }
    let client = AgentlyClient(endpoints: ["appAPI": EndpointConfig(baseURL: URL(string: "https://fixture.invalid")!, headers: ["X-App-Client": "fixture"])], session: session)
    let context = try await client.activateReportRun(id: "run", input: ActivateReportRunInput(conversationId: "conversation", expectedRunRevision: 2, expectedContextRevision: 0, source: "prompt"))
    XCTAssertEqual(context.activeReportRunId, "run")
  }

  func testBeginRetainsAdmittedBuilderAndScope() throws {
    let params: JSONValue = .object(["filters": .object(["adOrderId": .array([.number(2659534)])])])
    let input = BeginReportRunInput(uiRunRequestId: "request", conversationId: "conversation", origin: "prompt", builderRef: "delivery", presetId: "preset", sourceKind: "preset", sourceId: "source", requestedParams: params, effectiveParams: params, reportAdmissionRef: "opaque ref")
    let json = try JSONDecoder().decode([String: JSONValue].self, from: JSONEncoder().encode(input))
    XCTAssertEqual(json["builderRef"], .string("delivery"))
    XCTAssertEqual(json["requestedParams"], params)
    XCTAssertEqual(json["effectiveParams"], params)
    XCTAssertEqual(json["sourceId"], .string("source"))
    XCTAssertEqual(json["reportAdmissionRef"], .string("opaque ref"))
    if case .object(let requested)? = json["requestedParams"] { XCTAssertNil(requested["_agentlyForecastCommand"]) } else { XCTFail("Expected original request object") }
    let legacy = try JSONDecoder().decode([String: JSONValue].self, from: JSONEncoder().encode(BeginReportRunInput(uiRunRequestId: "old")))
    XCTAssertNil(legacy["builderRef"])
  }
}
