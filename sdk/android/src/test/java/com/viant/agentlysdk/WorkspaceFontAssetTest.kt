package com.viant.agentlysdk

import kotlinx.coroutines.runBlocking
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import org.junit.Test
import kotlin.test.*
import java.util.concurrent.TimeUnit

class WorkspaceFontAssetTest {
    @Test fun nativeFontUsesAuthenticatedRouteAndVerifiesBytes() = runBlocking {
        val server = MockWebServer(); server.start()
        try {
            val bytes = "native-font-fixture".toByteArray()
            val digest = java.security.MessageDigest.getInstance("SHA-256").digest(bytes).joinToString("") { "%02x".format(it) }
            val asset = WorkspaceFontAsset("/v1/workspace/ui/fonts/$digest.ttf", "ttf", digest, bytes.size)
            val client = AgentlyClient(mapOf("appAPI" to EndpointConfig(server.url("/").toString().trimEnd('/'), authTokenProvider = { "fixture" })))
            server.enqueue(MockResponse().setHeader("Content-Type", "font/ttf").setBody(okio.Buffer().write(bytes)))
            assertContentEquals(bytes, client.getWorkspaceFont(asset))
            val request = server.takeRequest(2, TimeUnit.SECONDS)!!
            assertEquals(asset.href, request.path)
            assertEquals("Bearer fixture", request.getHeader("Authorization"))
            val corrupted = bytes.copyOf().also { it[0] = (it[0].toInt() xor 1).toByte() }
            server.enqueue(MockResponse().setHeader("Content-Type", "font/ttf").setBody(okio.Buffer().write(corrupted)))
            assertFailsWith<IllegalArgumentException> { client.getWorkspaceFont(asset) }
            assertFailsWith<IllegalArgumentException> { client.getWorkspaceFont(asset.copy(href = "https://other.example/font.ttf")) }
            assertFailsWith<IllegalArgumentException> { client.getWorkspaceFont(asset.copy(sizeBytes = 1048577)) }
            Unit
        } finally { server.shutdown() }
    }
}
