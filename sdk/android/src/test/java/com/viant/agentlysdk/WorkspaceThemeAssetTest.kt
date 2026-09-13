package com.viant.agentlysdk

import kotlinx.coroutines.*
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import okhttp3.mockwebserver.SocketPolicy
import kotlin.test.*
import java.util.concurrent.TimeUnit

class WorkspaceThemeAssetTest {
    @Test fun catalogRequestUsesAuthAndRejectsOtherUrls() = runBlocking {
        val server = MockWebServer(); server.start()
        try {
            val revision = "a".repeat(64)
            val path = "/v1/workspace/ui/themes/$revision.json"
            server.enqueue(MockResponse().setHeader("Content-Type", "application/json").setBody("{}"))
            val client = AgentlyClient(mapOf("appAPI" to EndpointConfig(server.url("/").toString().trimEnd('/'), authTokenProvider = { "fixture" })))
            assertEquals("{}", client.getWorkspaceThemeCatalog(WorkspaceAssetDescriptor(1, revision, path)))
            val request = server.takeRequest(2, TimeUnit.SECONDS)!!
            assertEquals(path, request.path)
            assertEquals("Bearer fixture", request.getHeader("Authorization"))
            assertFailsWith<IllegalArgumentException> { client.getWorkspaceThemeCatalog(WorkspaceAssetDescriptor(1, revision, "https://other.example/theme.json")) }
            assertEquals(1, server.requestCount)
        } finally { server.shutdown() }
    }
    @Test fun catalogRequestCanBeCancelled() = runBlocking {
        val server = MockWebServer(); server.start()
        try {
            val revision = "b".repeat(64)
            server.enqueue(MockResponse().setSocketPolicy(SocketPolicy.NO_RESPONSE))
            val client = AgentlyClient(mapOf("appAPI" to EndpointConfig(server.url("/").toString().trimEnd('/'))))
            val task = async(Dispatchers.Default) { client.getWorkspaceThemeCatalog(WorkspaceAssetDescriptor(1, revision, "/v1/workspace/ui/themes/$revision.json")) }
            assertNotNull(server.takeRequest(3, TimeUnit.SECONDS))
            withTimeout(2000) { task.cancelAndJoin() }
            assertTrue(task.isCancelled)
        } finally { server.shutdown() }
    }
}
