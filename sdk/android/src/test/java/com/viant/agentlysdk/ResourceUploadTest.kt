package com.viant.agentlysdk

import kotlinx.coroutines.runBlocking
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonPrimitive
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertNotNull
import kotlin.test.assertNull
import kotlin.test.assertTrue

class ResourceUploadTest {
    @Test
    fun `upload then AGUI query preserves resource reference and auth`() = runBlocking {
        for (conversationId in listOf("conv-1", null)) {
            val server = MockWebServer()
            server.start()
            try {
                server.dispatcher=object:okhttp3.mockwebserver.Dispatcher(){
                    override fun dispatch(request:okhttp3.mockwebserver.RecordedRequest):MockResponse{
                        assertEquals("Bearer fixture-token",request.getHeader("Authorization"))
                        if(request.path=="/upload" || request.path=="/v1/files")return MockResponse().setBody("""{"id":"a1","uri":"scratchpad://artifact/a1","name":"customers.xlsx","size":5,"resource":{"uri":"scratchpad://artifact/a1","id":"a1","name":"customers.xlsx","mimeType":"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet","sizeBytes":5,"sha256":"digest"}}""")
                        if(request.method=="GET")return MockResponse().setBody("""{"id":"conv-1"}""")
                        val input=Json.parseToJsonElement(request.body.clone().readUtf8()).jsonObject
                        val thread=input.getValue("threadId").toString();val run=input.getValue("runId").toString()
                        val extension=input.getValue("forwardedProps").jsonObject.getValue("agently").jsonObject
                        var frames="data: {\"type\":\"RUN_STARTED\",\"threadId\":$thread,\"runId\":$run,\"metadata\":{\"agently\":{\"identityVersion\":\"1\",\"nativeTurnId\":\"native-turn\"}}}\n\n"
                        if(extension.getValue("operation").jsonPrimitive.content=="conversation.bootstrap"){
                            frames+="data: {\"type\":\"RUN_FINISHED\",\"threadId\":$thread,\"runId\":$run,\"outcome\":{\"type\":\"success\"},\"result\":{\"version\":\"1\",\"threadId\":$thread,\"transcript\":{\"schemaVersion\":\"2\",\"conversation\":{\"conversationId\":\"conv-1\",\"turns\":[]}},\"state\":{},\"messages\":[],\"runs\":[],\"projection\":{\"lossless\":true,\"unavailableMessageIds\":[]}}}\n\n"
                        }else{
                            assertEquals("scratchpad://artifact/a1",extension.getValue("payload").jsonObject.getValue("resourceURIs").jsonArray.single().jsonPrimitive.content)
                            frames+="data: {\"type\":\"ACTIVITY_SNAPSHOT\",\"messageId\":\"turn\",\"activityType\":\"agently.turn\",\"content\":{\"version\":\"1\",\"nativeTurnId\":\"native-turn\",\"status\":\"running\"}}\n\ndata: {\"type\":\"RUN_FINISHED\",\"threadId\":$thread,\"runId\":$run,\"outcome\":{\"type\":\"success\"}}\n\n"
                        }
                        return MockResponse().setHeader("Content-Type","text/event-stream").setBody(frames)
                    }
                }
                val client = AgentlyClient(endpoints = mapOf("appAPI" to EndpointConfig(
                    baseUrl = server.url("/").toString().trimEnd('/'), authTokenProvider = { "fixture-token" }
                )))
                val bytes = byteArrayOf(0x50, 0x4b, 0, 0xff.toByte(), 1)
                val upload = client.uploadFile(UploadFileInput(conversationId, "customers.xlsx",
                    "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", bytes))
                val resource = assertNotNull(upload.resource)
                assertEquals(5L, resource.sizeBytes)
                val sent = server.takeRequest()
                assertEquals(if (conversationId == null) "/upload" else "/v1/files", sent.path)
                assertEquals("Bearer fixture-token", sent.getHeader("Authorization"))
                val body = sent.body.readByteArray()
                assertTrue(body.asList().windowed(bytes.size).any { it == bytes.asList() })
                assertEquals(conversationId != null, body.decodeToString().contains("name=\"conversationId\""))
                val result = client.query(QueryInput(conversationId = "conv-1", query = "Inspect it", resourceURIs = listOf(resource.uri)))
                assertEquals("conv-1",result.conversationId)
                assertEquals("/v1/conversations/conv-1",server.takeRequest().path)
                val bootstrap=server.takeRequest();assertEquals("/v1/ag-ui/run",bootstrap.path)
                val query=server.takeRequest();assertEquals("/v1/ag-ui/run",query.path)
                assertEquals("Bearer fixture-token",query.getHeader("Authorization"))
                val json=Json.parseToJsonElement(query.body.readUtf8()).jsonObject
                val payload=json.getValue("forwardedProps").jsonObject.getValue("agently").jsonObject.getValue("payload").jsonObject
                assertEquals(resource.uri,payload.getValue("resourceURIs").jsonArray.single().jsonPrimitive.content)
            } finally { server.shutdown() }
        }
    }

    @Test
    fun `upload decodes native and anonymous response fields`() {
        for (raw in listOf("""{"id":"a","uri":"/v1/files/a"}""", """{"ID":"a","URI":"/v1/files/a"}""")) {
            val decoded = Json.decodeFromString(UploadFileOutput.serializer(), raw)
            assertEquals("a", decoded.id)
            assertEquals("/v1/files/a", decoded.uri)
            assertNull(decoded.resource)
        }
        val staged = Json.decodeFromString(UploadFileOutput.serializer(), """{"uri":"agently-staging-uploads/id/file.bin"}""")
        assertEquals("", staged.id)
        assertNull(staged.resource)
    }

    @Test
    fun `empty upload rejected and HTTP failures surfaced`(): Unit = runBlocking {
        val server = MockWebServer(); server.start()
        try {
            val client = AgentlyClient(endpoints = mapOf("appAPI" to EndpointConfig(baseUrl = server.url("/").toString().trimEnd('/'))))
            assertFailsWith<IllegalArgumentException> { client.uploadFile(UploadFileInput(name = "empty", data = byteArrayOf())) }
            assertEquals(0, server.requestCount)
            server.enqueue(MockResponse().setResponseCode(413).setBody("too large"))
            assertFailsWith<IllegalStateException> { client.uploadFile(UploadFileInput(name = "x", data = byteArrayOf(1))) }
        } finally { server.shutdown() }
    }
}
