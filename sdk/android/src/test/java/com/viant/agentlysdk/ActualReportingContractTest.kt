package com.viant.agentlysdk

import java.io.File
import java.util.UUID
import kotlinx.coroutines.runBlocking
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import org.junit.Assume.assumeTrue
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFails
import kotlin.test.assertNull
import kotlin.test.assertTrue

class ActualReportingContractTest {
    @Test fun publicReportingExportAuditAndQueueSDK() = runBlocking {
        val path = System.getenv("AGENTLY_SDK_REPORT_CONTRACT_READY")
        assumeTrue("Actual reporting server ready file required", path != null)
        val ready = Json.parseToJsonElement(File(requireNotNull(path)).readText()).jsonObject
        fun value(key: String) = ready.getValue(key).jsonPrimitive.content
        fun client(key: String) = AgentlyClient(mapOf("appAPI" to EndpointConfig(value("url"), authTokenProvider = { File(value(key)).readText().trim() })))
        val owner = client("ownerTokenFile"); val other = client("otherTokenFile")
        val conversation = value("androidConversationId")
        val snapshots = Json.parseToJsonElement(File(value("reportingSnapshotsFile")).readText()).jsonObject
        val begin = BeginReportRunInput("android-${UUID.randomUUID()}", origin = "manual")
        val begun = owner.beginReportRun(begin)
        assertEquals(begun.run.reportRunId, owner.beginReportRun(begin).run.reportRunId)
        val completed = owner.completeReportRun(begun.run.reportRunId, CompleteReportRunInput(1, snapshots.getValue("reportSpec"), snapshots.getValue("reportFill"), snapshots.getValue("reportPrint")))
        assertEquals(2L, completed.revision)
        val previous = try { owner.getReportContext(conversation) } catch (error: IllegalStateException) { assertTrue(error.message.orEmpty().contains("failed: 404")); null }
        val adoption = AdoptReportRunInput(conversation, completed.revision, previous?.revision ?: 0, "adopt")
        assertTrue(assertFails { other.adoptReportRun(completed.reportRunId, adoption) }.message.orEmpty().contains("failed: 404"))
        val adopted = owner.adoptReportRun(completed.reportRunId, adoption)
        assertEquals(completed.reportRunId, adopted.context?.activeReportRunId)
        assertEquals(conversation, owner.getReportRun(completed.reportRunId, conversation).conversationId)
        val operation = "android-export-${UUID.randomUUID()}"
        val job = owner.submitReportRunExport(completed.reportRunId, conversation, operation)
        assertEquals(job.jobId, owner.submitReportRunExport(completed.reportRunId, conversation, operation).jobId)
        assertEquals(completed.reportRunId, job.reportRunId)
        assertEquals("queued", owner.getReportExportStatus(job.jobId, conversation).status)
        val audit = owner.recordReportAuditEvent(ReportAuditEvent("report.download", requireNotNull(job.artifactRef), jobId = job.jobId, metadata = mapOf("via" to JsonPrimitive("android-sdk"))))
        assertEquals(job.ownerId, audit.actorId)
        val artifact = owner.getReportArtifact(value("androidArtifactId"))
        assertEquals("application/pdf", artifact.contentType)
        assertNull(artifact.data)
        assertTrue(assertFails { other.getReportArtifact(value("androidArtifactId")) }.message.orEmpty().contains("failed: 404"))
        val turn = value("androidTurnId"); val content = "android SDK edit ${UUID.randomUUID()}"
        owner.editQueuedTurn(EditQueuedTurnInput(conversation, turn, content))
        assertEquals(content, owner.getTranscript(GetTranscriptInput(conversation)).conversation?.turns?.first { it.turnId == turn }?.user?.content)
    }
}
