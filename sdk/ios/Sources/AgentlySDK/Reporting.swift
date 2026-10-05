import Foundation

public struct ReportRun: Codable, Sendable {
  public let reportRunId: String
  public let ownerId: String
  public let conversationId: String?
  public let status: String
  public let revision: Int64
  public let builderRef: String?
  public let effectiveParams: JSONValue?
  public let requestedParams: JSONValue?
  public let reportSpec: JSONValue?
  public let reportFill: JSONValue?
  public let reportPrint: JSONValue?
}
public struct ReportContext: Codable, Sendable {
  public let ownerId: String
  public let conversationId: String
  public let activeReportRunId: String
  public let revision: Int64
}
public struct ReportRunResult: Codable, Sendable {
  public let run: ReportRun
  public let context: ReportContext?
}
public struct BeginReportRunInput: Codable, Sendable {
  public var uiRunRequestId: String
  public var conversationId: String?
  public var origin: String?
  public var builderRef: String?
  public var presetId: String?
  public var sourceKind: String?
  public var sourceId: String?
  public var requestedParams: JSONValue?
  public var effectiveParams: JSONValue?
  public init(
    uiRunRequestId: String, conversationId: String? = nil, origin: String? = nil,
    builderRef: String? = nil, presetId: String? = nil, sourceKind: String? = nil,
    sourceId: String? = nil, requestedParams: JSONValue? = nil, effectiveParams: JSONValue? = nil
  ) {
    self.uiRunRequestId = uiRunRequestId
    self.conversationId = conversationId
    self.origin = origin
    self.builderRef = builderRef
    self.presetId = presetId
    self.sourceKind = sourceKind
    self.sourceId = sourceId
    self.requestedParams = requestedParams
    self.effectiveParams = effectiveParams
  }
}
public struct CompleteReportRunInput: Codable, Sendable {
  public var expectedRevision: Int64
  public var reportSpec: JSONValue
  public var reportFill: JSONValue
  public var reportPrint: JSONValue
  public var conversationId: String?
  public init(
    expectedRevision: Int64, reportSpec: JSONValue, reportFill: JSONValue, reportPrint: JSONValue,
    conversationId: String? = nil
  ) {
    self.expectedRevision = expectedRevision
    self.reportSpec = reportSpec
    self.reportFill = reportFill
    self.reportPrint = reportPrint
    self.conversationId = conversationId
  }
}
public struct FailReportRunInput: Codable, Sendable {
  public var expectedRevision: Int64
  public var conversationId: String?
  public var failureCode: String?
  public var failureText: String?
  public init(expectedRevision: Int64, conversationId: String? = nil, failureCode: String? = nil, failureText: String? = nil) {
    self.expectedRevision = expectedRevision
    self.conversationId = conversationId
    self.failureCode = failureCode
    self.failureText = failureText
  }
}
public struct ActivateReportRunInput: Codable, Sendable {
  public var conversationId: String
  public var expectedRunRevision: Int64
  public var expectedContextRevision: Int64
  public var source: String?
  public init(conversationId: String, expectedRunRevision: Int64, expectedContextRevision: Int64, source: String? = nil) {
    self.conversationId = conversationId
    self.expectedRunRevision = expectedRunRevision
    self.expectedContextRevision = expectedContextRevision
    self.source = source
  }
}
public struct AdoptReportRunInput: Codable, Sendable {
  public var conversationId: String
  public var expectedRunRevision: Int64
  public var expectedContextRevision: Int64
  public var source: String?
  public init(
    conversationId: String, expectedRunRevision: Int64, expectedContextRevision: Int64,
    source: String? = nil
  ) {
    self.conversationId = conversationId
    self.expectedRunRevision = expectedRunRevision
    self.expectedContextRevision = expectedContextRevision
    self.source = source
  }
}
public struct ReportExportJob: Codable, Sendable {
  public let jobId: String
  public let ownerId: String?
  public let conversationId: String?
  public let reportRunId: String?
  public let artifactRef: String?
  public let artifactId: String?
  public let status: String
}
public struct ReportArtifact: Codable, Sendable {
  public let artifactId: String
  public let jobId: String?
  public let artifactRef: String?
  public let contentType: String?
  public let data: String?
}
private struct ReportingToolResult: Decodable { let result: String? }
extension AgentlyClient {
  public func beginReportRun(_ input: BeginReportRunInput) async throws -> ReportRunResult {
    try await post("/v1/api/report-runs/begin", body: input, as: ReportRunResult.self)
  }
  public func getReportRun(id: String, conversationID: String? = nil) async throws -> ReportRun {
    try await get(
      "/v1/api/report-runs/\(agentlyPercentEncodedPathSegment(id))",
      query: conversationID.map { [URLQueryItem(name: "conversationId", value: $0)] } ?? [],
      as: ReportRun.self)
  }
  public func getReportContext(conversationID: String) async throws -> ReportContext {
    try await get(
      "/v1/api/report-runs/context/\(agentlyPercentEncodedPathSegment(conversationID))",
      as: ReportContext.self)
  }
  public func completeReportRun(id: String, input: CompleteReportRunInput) async throws -> ReportRun
  {
    try await post(
      "/v1/api/report-runs/\(agentlyPercentEncodedPathSegment(id))/complete", body: input,
      as: ReportRun.self)
  }
  public func failReportRun(id: String, input: FailReportRunInput) async throws -> ReportRun {
    try await post(
      "/v1/api/report-runs/\(agentlyPercentEncodedPathSegment(id))/fail", body: input,
      as: ReportRun.self)
  }
  public func activateReportRun(id: String, input: ActivateReportRunInput) async throws -> ReportContext {
    try await post("/v1/api/report-runs/\(agentlyPercentEncodedPathSegment(id))/activate", body: input, as: ReportContext.self)
  }
  public func adoptReportRun(id: String, input: AdoptReportRunInput) async throws -> ReportRunResult
  {
    try await post(
      "/v1/api/report-runs/\(agentlyPercentEncodedPathSegment(id))/adopt", body: input,
      as: ReportRunResult.self)
  }
  public func submitReportRunExport(
    reportRunID: String, conversationID: String, exportRequestID: String
  ) async throws -> ReportExportJob {
    let args: [String: JSONValue] = ["reportRunId": .string(reportRunID), "format": .string("pdf")]
    let envelope = try await rawRequest(
      path: "/v1/tools/reporting:submit_export/execute", method: "POST",
      query: [URLQueryItem(name: "conversationId", value: conversationID)],
      body: encoder.encode(args),
      additionalHeaders: ["X-Agently-Export-Request-ID": exportRequestID],
      as: ReportingToolResult.self)
    return try decoder.decode(ReportExportJob.self, from: Data((envelope.result ?? "").utf8))
  }
  public func getReportExportStatus(jobID: String, conversationID: String? = nil) async throws
    -> ReportExportJob
  {
    let raw = try await executeTool(
      name: "reporting:get_export_status", args: ["jobId": .string(jobID)],
      conversationID: conversationID)
    return try decoder.decode(ReportExportJob.self, from: Data(raw.utf8))
  }
  public func getReportArtifact(id: String, conversationID: String? = nil) async throws
    -> ReportArtifact
  {
    let raw = try await executeTool(
      name: "reporting:get_artifact", args: ["artifactId": .string(id)],
      conversationID: conversationID)
    return try decoder.decode(ReportArtifact.self, from: Data(raw.utf8))
  }
  public func recordReportAuditEvent(_ event: ReportAuditEvent, conversationID: String? = nil)
    async throws -> ReportAuditEvent
  {
    let payload = try decoder.decode(JSONValue.self, from: encoder.encode(event))
    let raw = try await executeTool(
      name: "reporting:record_audit_event", args: ["event": payload], conversationID: conversationID
    )
    return try decoder.decode(ReportAuditEvent.self, from: Data(raw.utf8))
  }
}

public struct ReportAuditEvent: Codable, Sendable {
  public var eventType: String
  public var artifactRef: String
  public var jobId: String?
  public var artifactId: String?
  public var actorId: String?
  public var metadata: [String: JSONValue]?
  public init(
    eventType: String, artifactRef: String, jobId: String? = nil, artifactId: String? = nil,
    metadata: [String: JSONValue]? = nil
  ) {
    self.eventType = eventType
    self.artifactRef = artifactRef
    self.jobId = jobId
    self.artifactId = artifactId
    self.metadata = metadata
  }
}
