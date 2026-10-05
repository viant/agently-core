package com.viant.agentlysdk

import kotlinx.coroutines.runBlocking
import kotlinx.serialization.json.*
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import kotlin.test.Test
import kotlin.test.assertEquals

class ReportActivationTransportTest {
    @Test fun activationUsesSeparateScopedCasEndpointAndExistingHeaders() = runBlocking {
        val server = MockWebServer()
        server.start()
        try {
            server.enqueue(MockResponse().setHeader("Content-Type", "application/json").setBody("""{"ownerId":"owner","conversationId":"conversation","activeReportRunId":"run","revision":1}"""))
            val client = AgentlyClient(mapOf("appAPI" to EndpointConfig(server.url("/").toString().removeSuffix("/"), defaultHeadersProvider = { mapOf("X-App-Client" to "fixture") })))
            val context = client.activateReportRun("run", ActivateReportRunInput("conversation", 2, 0, "prompt"))
            assertEquals("run", context.activeReportRunId)
            val request = server.takeRequest()
            assertEquals("POST", request.method)
            assertEquals("/v1/api/report-runs/run/activate", request.path)
            assertEquals("fixture", request.getHeader("X-App-Client"))
            val body = Json.parseToJsonElement(request.body.readUtf8()).jsonObject
            assertEquals(JsonPrimitive("conversation"), body["conversationId"])
            assertEquals(JsonPrimitive(2), body["expectedRunRevision"])
            assertEquals(JsonPrimitive(0), body["expectedContextRevision"])
        } finally { server.shutdown() }
    }
}
