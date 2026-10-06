package com.viant.agentlysdk

import java.io.File
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.withTimeout
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import org.junit.Assume.assumeTrue
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFails
import kotlin.test.assertTrue

/** Opt-in integration gate against e2e/sdkcontract/cmd/server. */
class ActualServerContractTest {
    @Test
    fun actualSDK1JSONAuthErrorsAndSSE() = runBlocking {
        val configuredPath = System.getenv("AGENTLY_SDK_CONTRACT_READY")
        assumeTrue("Set AGENTLY_SDK_CONTRACT_READY to the actual server ready file", configuredPath != null)
        val readyPath = requireNotNull(configuredPath)
        val ready = Json.parseToJsonElement(File(readyPath).readText()).jsonObject
        fun value(key: String) = ready.getValue(key).jsonPrimitive.content
        fun client(tokenKey: String?) = AgentlyClient(mapOf("appAPI" to EndpointConfig(
            baseUrl = value("url"),
            authTokenProvider = tokenKey?.let { key -> { File(value(key)).readText().trim() } }
        )))
        val owner = client("ownerTokenFile")
        val id = value("conversationId")
        assertEquals(id, owner.getConversation(id).id)
        val transcript = owner.getTranscript(GetTranscriptInput(id))
        assertEquals(id, transcript.conversation?.conversationId)
        assertTrue(transcript.conversation?.turns.orEmpty().any { it.turnId == "sdk-contract-turn" && it.user?.content == "fixture prompt" })
        assertTrue(owner.listConversations().rows.any { it.id == id })
        assertTrue(assertFails { client(null).getConversation(id) }.message.orEmpty().contains("failed: 401"))
        val other = client("otherTokenFile")
        assertEquals(id, other.getConversation(id).id) // Existing detail-read ownership gap; tracked separately.
        assertTrue(assertFails { other.deleteConversation(id) }.message.orEmpty().contains("failed: 403"))
        assertEquals(id, owner.getConversation(id).id)
        assertFails { owner.getConversation("sdk-contract-missing") } // Existing API returns 200 null; non-null SDK model rejects it.
        val event = withTimeout(10000) { owner.streamApplicationEvents(id).first { it.type == "usage" } }
        assertEquals(id, event.conversationId)
        assertEquals("11", event.patch?.get("inputTokens")?.jsonPrimitive?.content)
        assertEquals("7", event.patch?.get("outputTokens")?.jsonPrimitive?.content)
    }
}
