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
    fun `upload then query preserves native resource reference and auth`() = runBlocking {
        for (conversationId in listOf("conv-1", null)) {
            val server = MockWebServer()
            server.start()
            try {
                server.enqueue(MockResponse().setBody("""{"id":"a1","uri":"scratchpad://artifact/a1","name":"customers.xlsx","size":5,"resource":{"uri":"scratchpad://artifact/a1","id":"a1","name":"customers.xlsx","mimeType":"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet","sizeBytes":5,"sha256":"digest"}}"""))
                server.enqueue(MockResponse().setBody("""{"content":"done"}"""))
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
                assertEquals("done", result.content)
                val query = server.takeRequest()
                assertEquals("/v1/agent/query", query.path)
                assertEquals("Bearer fixture-token", query.getHeader("Authorization"))
                val json = Json.parseToJsonElement(query.body.readUtf8()).jsonObject
                assertEquals(resource.uri, json.getValue("resourceURIs").jsonArray.single().jsonPrimitive.content)
            } finally { server.shutdown() }
        }
    }

    @Test
    fun `upload accepts lowercase legacy uppercase and anonymous responses`() {
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
