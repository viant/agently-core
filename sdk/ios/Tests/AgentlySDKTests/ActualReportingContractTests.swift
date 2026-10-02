import Foundation
import XCTest

@testable import AgentlySDK

final class ActualReportingContractTests: XCTestCase {
  func testQueuedEditAcceptsNoContentButObjectReadStillRequiresJSON() async throws {
    let configuration = URLSessionConfiguration.ephemeral
    configuration.protocolClasses = [AgentlySDKTests.URLProtocolStub.self]
    let session = URLSession(configuration: configuration)
    defer {
      session.invalidateAndCancel()
      AgentlySDKTests.URLProtocolStub.requestHandler = nil
    }
    AgentlySDKTests.URLProtocolStub.requestHandler = { request in
      (
        HTTPURLResponse(url: request.url!, statusCode: 204, httpVersion: nil, headerFields: nil)!,
        Data()
      )
    }
    let client = AgentlyClient(
      endpoints: ["appAPI": EndpointConfig(baseURL: URL(string: "https://fixture.invalid")!)],
      session: session)
    try await client.editQueuedTurn(
      EditQueuedTurnInput(conversationID: "c", turnID: "t", content: "updated"))
    do {
      _ = try await client.getConversation(conversationID: "c")
      XCTFail("Object read accepted an empty body")
    } catch is DecodingError {}
  }

  func testPublicReportingExportAuditAndQueueSDK() async throws {
    guard let path = ProcessInfo.processInfo.environment["AGENTLY_SDK_REPORT_CONTRACT_READY"] else {
      throw XCTSkip("Actual reporting server ready file required")
    }
    let ready = try JSONDecoder().decode(
      [String: String].self, from: Data(contentsOf: URL(fileURLWithPath: path)))
    func value(_ key: String) throws -> String { try XCTUnwrap(ready[key]) }
    func client(_ tokenKey: String) throws -> AgentlyClient {
      let token = try String(contentsOfFile: value(tokenKey), encoding: .utf8).trimmingCharacters(
        in: .whitespacesAndNewlines)
      return AgentlyClient(
        endpoints: [
          "appAPI": EndpointConfig(
            baseURL: try XCTUnwrap(URL(string: value("url"))),
            headers: ["Authorization": "Bearer \(token)"])
        ], session: URLSession(configuration: .ephemeral))
    }
    let owner = try client("ownerTokenFile")
    let other = try client("otherTokenFile")
    let conversation = try value("swiftConversationId")
    let snapshots = try JSONDecoder().decode(
      [String: JSONValue].self,
      from: Data(contentsOf: URL(fileURLWithPath: value("reportingSnapshotsFile"))))
    let begin = BeginReportRunInput(uiRunRequestId: "swift-\(UUID().uuidString)", origin: "manual")
    let begun = try await owner.beginReportRun(begin)
    let duplicate = try await owner.beginReportRun(begin)
    XCTAssertEqual(begun.run.reportRunId, duplicate.run.reportRunId)
    let completed = try await owner.completeReportRun(
      id: begun.run.reportRunId,
      input: CompleteReportRunInput(
        expectedRevision: 1, reportSpec: try XCTUnwrap(snapshots["reportSpec"]),
        reportFill: try XCTUnwrap(snapshots["reportFill"]),
        reportPrint: try XCTUnwrap(snapshots["reportPrint"])))
    XCTAssertEqual(completed.revision, 2)
    var previous: ReportContext?
    do {
      previous = try await owner.getReportContext(conversationID: conversation)
    } catch AgentlySDKError.httpStatus(let status, _) { XCTAssertEqual(status, 404) }
    let adoption = AdoptReportRunInput(
      conversationId: conversation, expectedRunRevision: completed.revision,
      expectedContextRevision: previous?.revision ?? 0, source: "adopt")
    do {
      _ = try await other.adoptReportRun(id: completed.reportRunId, input: adoption)
      XCTFail("Foreign adoption succeeded")
    } catch AgentlySDKError.httpStatus(let status, _) { XCTAssertEqual(status, 404) }
    let adopted = try await owner.adoptReportRun(id: completed.reportRunId, input: adoption)
    XCTAssertEqual(adopted.context?.activeReportRunId, completed.reportRunId)
    let loaded = try await owner.getReportRun(
      id: completed.reportRunId, conversationID: conversation)
    XCTAssertEqual(loaded.conversationId, conversation)
    let operation = "swift-export-\(UUID().uuidString)"
    let job = try await owner.submitReportRunExport(
      reportRunID: completed.reportRunId, conversationID: conversation, exportRequestID: operation)
    let retry = try await owner.submitReportRunExport(
      reportRunID: completed.reportRunId, conversationID: conversation, exportRequestID: operation)
    XCTAssertEqual(job.jobId, retry.jobId)
    XCTAssertEqual(job.reportRunId, completed.reportRunId)
    let status = try await owner.getReportExportStatus(
      jobID: job.jobId, conversationID: conversation)
    XCTAssertEqual(status.status, "queued")
    let audit = try await owner.recordReportAuditEvent(
      ReportAuditEvent(
        eventType: "report.download", artifactRef: try XCTUnwrap(job.artifactRef), jobId: job.jobId,
        metadata: ["via": .string("swift-sdk")]))
    XCTAssertEqual(audit.actorId, job.ownerId)
    let artifact = try await owner.getReportArtifact(id: value("swiftArtifactId"))
    XCTAssertEqual(artifact.contentType, "application/pdf")
    XCTAssertNil(artifact.data)
    do {
      _ = try await other.getReportArtifact(id: value("swiftArtifactId"))
      XCTFail("Foreign artifact read succeeded")
    } catch AgentlySDKError.httpStatus(let status, _) { XCTAssertEqual(status, 404) }
    let turn = try value("swiftTurnId")
    let content = "swift SDK edit \(UUID().uuidString)"
    try await owner.editQueuedTurn(
      EditQueuedTurnInput(conversationID: conversation, turnID: turn, content: content))
    let transcript = try await owner.getTranscript(GetTranscriptInput(conversationID: conversation))
    XCTAssertEqual(
      transcript.conversation?.turns.first { $0.turnID == turn }?.user?.content, content)
  }
}
