package com.viant.agentlysdk

import java.net.URLEncoder
import java.nio.charset.StandardCharsets
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.encodeToJsonElement
import kotlinx.serialization.json.jsonObject

@Serializable
data class ReportRun(
    val reportRunId: String,
    val ownerId: String,
    val status: String,
    val revision: Long,
    val conversationId: String? = null,
    val reportSpec: JsonElement? = null,
    val reportFill: JsonElement? = null,
    val reportPrint: JsonElement? = null,
    val builderRef: String? = null,
    val effectiveParams: JsonElement? = null,
    val requestedParams: JsonElement? = null
)
@Serializable
data class ReportContext(
    val ownerId: String,
    val conversationId: String,
    val activeReportRunId: String,
    val revision: Long
)
@Serializable
data class ReportRunResult(
    val run: ReportRun,
    val context: ReportContext? = null
)
@Serializable
data class BeginReportRunInput(
    val uiRunRequestId: String,
    val conversationId: String? = null,
    val origin: String? = null,
    val builderRef: String? = null,
    val presetId: String? = null,
    val sourceKind: String? = null,
    val sourceId: String? = null,
    val requestedParams: JsonElement? = null,
    val effectiveParams: JsonElement? = null
)
@Serializable
data class CompleteReportRunInput(
    val expectedRevision: Long,
    val reportSpec: JsonElement,
    val reportFill: JsonElement,
    val reportPrint: JsonElement,
    val conversationId: String? = null
)
@Serializable
data class FailReportRunInput(
    val expectedRevision: Long,
    val conversationId: String? = null,
    val failureCode: String? = null,
    val failureText: String? = null
)
@Serializable
data class ActivateReportRunInput(
    val conversationId: String,
    val expectedRunRevision: Long,
    val expectedContextRevision: Long,
    val source: String? = null
)
@Serializable
data class AdoptReportRunInput(
    val conversationId: String,
    val expectedRunRevision: Long,
    val expectedContextRevision: Long,
    val source: String? = null
)
@Serializable
data class ReportExportJob(
    val jobId: String,
    val status: String,
    val ownerId: String? = null,
    val conversationId: String? = null,
    val reportRunId: String? = null,
    val artifactRef: String? = null,
    val artifactId: String? = null
)
@Serializable
data class ReportArtifact(
    val artifactId: String,
    val jobId: String? = null,
    val artifactRef: String? = null,
    val contentType: String? = null,
    val data: String? = null
)
@Serializable
private data class ReportingToolResult(
    val result: String? = null
)
private fun reportSegment(value: String) = URLEncoder.encode(value, StandardCharsets.UTF_8).replace("+", "%20")

suspend fun AgentlyClient.beginReportRun(input: BeginReportRunInput): ReportRunResult = withContext(Dispatchers.IO) {
    post("/v1/api/report-runs/begin", json.encodeToJsonElement(input).jsonObject.toMap(), ReportRunResult.serializer())
}
suspend fun AgentlyClient.getReportRun(id: String, conversationId: String? = null): ReportRun = withContext(Dispatchers.IO) {
    val query = conversationId?.let { "?conversationId=${reportSegment(it)}" }.orEmpty()
    get("/v1/api/report-runs/${reportSegment(id)}$query", ReportRun.serializer())
}
suspend fun AgentlyClient.completeReportRun(id: String, input: CompleteReportRunInput): ReportRun = withContext(Dispatchers.IO) {
    post("/v1/api/report-runs/${reportSegment(id)}/complete", json.encodeToJsonElement(input).jsonObject.toMap(), ReportRun.serializer())
}
suspend fun AgentlyClient.failReportRun(id: String, input: FailReportRunInput): ReportRun = withContext(Dispatchers.IO) {
    post("/v1/api/report-runs/${reportSegment(id)}/fail", json.encodeToJsonElement(input).jsonObject.toMap(), ReportRun.serializer())
}
suspend fun AgentlyClient.activateReportRun(id: String, input: ActivateReportRunInput): ReportContext = withContext(Dispatchers.IO) {
    post("/v1/api/report-runs/${reportSegment(id)}/activate", json.encodeToJsonElement(input).jsonObject.toMap(), ReportContext.serializer())
}
suspend fun AgentlyClient.adoptReportRun(id: String, input: AdoptReportRunInput): ReportRunResult = withContext(Dispatchers.IO) {
    post("/v1/api/report-runs/${reportSegment(id)}/adopt", json.encodeToJsonElement(input).jsonObject.toMap(), ReportRunResult.serializer())
}
suspend fun AgentlyClient.submitReportRunExport(reportRunId: String, conversationId: String, exportRequestId: String): ReportExportJob = withContext(Dispatchers.IO) {
    val payload = mapOf("reportRunId" to JsonPrimitive(reportRunId), "format" to JsonPrimitive("pdf"))
    val envelope = postWithHeaders("/v1/tools/reporting:submit_export/execute?conversationId=${reportSegment(conversationId)}", payload, ReportingToolResult.serializer(), mapOf("X-Agently-Export-Request-ID" to exportRequestId))
    json.decodeFromString(ReportExportJob.serializer(), envelope.result.orEmpty())
}
suspend fun AgentlyClient.getReportExportStatus(jobId: String, conversationId: String? = null): ReportExportJob = json.decodeFromString(ReportExportJob.serializer(), executeTool("reporting:get_export_status", mapOf("jobId" to JsonPrimitive(jobId)), conversationId))
suspend fun AgentlyClient.getReportArtifact(id: String, conversationId: String? = null): ReportArtifact = json.decodeFromString(ReportArtifact.serializer(), executeTool("reporting:get_artifact", mapOf("artifactId" to JsonPrimitive(id)), conversationId))
@Serializable
data class ReportAuditEvent(
    val eventType: String,
    val artifactRef: String,
    val jobId: String? = null,
    val artifactId: String? = null,
    val actorId: String? = null,
    val metadata: Map<String, JsonElement>? = null
)
suspend fun AgentlyClient.recordReportAuditEvent(event: ReportAuditEvent, conversationId: String? = null): ReportAuditEvent = json.decodeFromString(ReportAuditEvent.serializer(), executeTool("reporting:record_audit_event", mapOf("event" to json.encodeToJsonElement(event)), conversationId))
suspend fun AgentlyClient.getReportContext(conversationId: String): ReportContext = withContext(Dispatchers.IO) { get("/v1/api/report-runs/context/${reportSegment(conversationId)}", ReportContext.serializer()) }
