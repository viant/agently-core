package com.viant.agentlysdk.agui

import com.viant.agentlysdk.EndpointConfig
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.flow.toList
import kotlinx.coroutines.flow.take
import kotlinx.serialization.json.*
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import java.util.concurrent.TimeUnit
import kotlin.test.*

class AgUiTest {
    private fun json(text: String) = Json.parseToJsonElement(text)
    private val fixture get() = javaClass.getResourceAsStream("/agui/conformance.json")!!.use { json(it.bufferedReader().readText()).obj() }
    private fun input() = AgUiRunInput(fixture.getValue("input").obj())
    private fun event(text: String) = AgUiEvent(json(text).obj())
    private fun start() = event("""{"type":"RUN_STARTED","threadId":"thread","runId":"run"}""")

    @Test fun all31VariantsMatchOfficialHttpAgentReference() {
        val f = fixture; val store = AgUiStore(input()); val types = mutableListOf<String>()
        for (raw in f.getValue("events") as JsonArray) {
            AgUiSchema.validate(raw, "Event")
            types += store.receive(AgUiEvent(raw.obj())).map { it.type }
        }
        store.finish()
        assertEquals(f["expectedNormalizedTypes"], JsonArray(types.map { jstring(it) }))
        assertEquals(f["expectedMessages"], JsonArray(store.messages.map { it.value }))
        assertEquals(f["expectedState"], store.state)
        assertEquals("opaque-tool", store.messages.find { it.id == "chunk-parent" }!!.value["toolCalls"]!!.jsonArray[0].obj()["encryptedValue"]?.jsonPrimitive?.content)
        assertEquals(31, (f["events"]!!.jsonArray + f["errorEvents"]!!.jsonArray).map { it.obj().requiredString("type") }.toSet().size)
        assertEquals(31, AgUiEventType.entries.size)
        assertNotNull(store.capabilities)
        assertEquals(2, store.subagents.size)
        assertEquals("Worker", store.snapshot.subagents.getValue("s1").started.value.string("name"))
        assertEquals("SUBAGENT_ERROR", store.snapshot.subagents.getValue("s2").terminal!!.type)
        val next = store.nextInput("next", forwardedProps = AgentlyAgUiExtensions.forwardedProps("chat", "agent", "model"))
        assertEquals(JsonArray(store.messages.filter { it.role != "activity" }.map { it.value }), next.value["messages"])
        assertEquals(JsonArray(store.messages.map { it.value }), store.nextInput("explicit", includeActivityMessages = true).value["messages"])
        assertEquals(store.state, next.value["state"])
    }
    @Test fun resumeInterruptCancellationAndRunErrorRemainLossless() {
        val store = AgUiStore(input())
        for (raw in fixture["interruptEvents"]!!.jsonArray) store.receive(AgUiEvent(raw.obj()))
        store.finish(); assertEquals("approval", store.pendingInterrupts[0].obj().string("id"))
        val resume = json("""[{"interruptId":"approval","status":"resolved","payload":{"editedArgs":{"id":42}},"metadata":{"signed":"opaque"}}]""").jsonArray
        val next = store.nextInput("resumed", resume = resume)
        assertEquals(resume, next.value["resume"])
        val error = AgUiStore(input()); error.receive(AgUiEvent(fixture["errorEvents"]!!.jsonArray[0].obj())); error.finish()
        assertEquals("DENIED", error.terminalEvent!!.value.string("code"))
        val cancelled = AgUiStore(input()); cancelled.receive(start()); cancelled.receive(event("""{"type":"RUN_FINISHED","threadId":"thread","runId":"run","outcome":{"type":"cancelled","reason":"user","interruptIds":["approval"]}}""")); cancelled.finish()
        assertEquals("cancelled", cancelled.terminalEvent!!.value["outcome"]!!.obj().string("type"))
    }
    @Test fun schemaRejectsInvalidPayloadsAndRetainsFutureProperties() {
        assertFailsWith<AgUiProtocolException> { event("""{"type":"TEXT_MESSAGE_CONTENT","messageId":"m","delta":null}""") }
        assertFailsWith<AgUiProtocolException> { event("""{"type":"SUBAGENT_STARTED","subagentRunId":null,"name":"child"}""") }
        assertFailsWith<AgUiProtocolException> { AgUiSchema.validate(json("""{"type":"RUN_ERROR","message":"bad","extra":1}"""), "Event") }
        val future = event("""{"type":"RUN_ERROR","message":"bad","future":{"n":90071992547409931234567890}}""")
        assertEquals("90071992547409931234567890", future.value["future"]!!.obj()["n"]!!.jsonPrimitive.content)
        val store = AgUiStore(input()); store.receive(start())
        val unknown = event("""{"type":"FUTURE_EVENT","opaque":[1,false]}""")
        assertEquals(unknown, store.receive(unknown).single())
    }
    @Test fun lifecycleAndOwnershipRejectInvalidSequences() {
        fun store() = AgUiStore(input()).apply { receive(start()) }
        assertFailsWith<AgUiProtocolException> { store().receive(event("""{"type":"TEXT_MESSAGE_CONTENT","messageId":"missing","delta":"bad"}""")) }
        assertFailsWith<AgUiProtocolException> { store().receive(event("""{"type":"RUN_FINISHED","threadId":"wrong","runId":"run"}""")) }
        assertFailsWith<AgUiProtocolException> { store().finish() }
        val attributed = store(); attributed.receive(event("""{"type":"TEXT_MESSAGE_START","messageId":"m"}"""))
        assertFailsWith<AgUiProtocolException> { attributed.receive(event("""{"type":"TEXT_MESSAGE_CONTENT","messageId":"m","delta":"bad","subagentRunId":"child"}""")) }
        val step = store(); step.receive(event("""{"type":"STEP_STARTED","stepName":"same"}"""))
        assertFailsWith<AgUiProtocolException> { step.receive(event("""{"type":"STEP_FINISHED","stepName":"same","subagentRunId":""}""")) }
        val reasoning = store(); reasoning.receive(event("""{"type":"REASONING_START","messageId":"r"}"""))
        assertFailsWith<AgUiProtocolException> { reasoning.receive(event("""{"type":"RUN_FINISHED","threadId":"thread","runId":"run"}""")) }
    }
    @Test fun chunkLanesInterleaveAndRejectAmbiguityAndOpenerChanges() {
        val store = AgUiStore(input()); store.receive(start())
        store.receive(event("""{"type":"TEXT_MESSAGE_CHUNK","messageId":"a","delta":"A","subagentRunId":"s1"}"""))
        store.receive(event("""{"type":"TEXT_MESSAGE_CHUNK","messageId":"b","delta":"B","subagentRunId":"s2"}"""))
        assertFailsWith<AgUiProtocolException> { store.receive(event("""{"type":"TEXT_MESSAGE_CHUNK","delta":"ambiguous"}""")) }
        assertFailsWith<AgUiProtocolException> { store.receive(event("""{"type":"TEXT_MESSAGE_CHUNK","messageId":"a","role":"user"}""")) }
        val good = AgUiStore(input()); good.receive(start())
        good.receive(event("""{"type":"TEXT_MESSAGE_CHUNK","messageId":"a","delta":"A","subagentRunId":"s1"}"""))
        good.receive(event("""{"type":"TEXT_MESSAGE_CHUNK","messageId":"b","delta":"B","subagentRunId":"s2"}"""))
        good.receive(event("""{"type":"TEXT_MESSAGE_CHUNK","messageId":"a","delta":"2"}"""))
        good.receive(event("""{"type":"RUN_FINISHED","threadId":"thread","runId":"run"}""")); good.finish()
        assertEquals("A2", good.messages.find { it.id == "a" }!!.value.string("content"))
        assertEquals("B", good.messages.find { it.id == "b" }!!.value.string("content"))
    }
    @Test fun jsonPatchIsAtomicAndSupportsRootEscapesNumericEqualityAndArrayMoves() {
        val original = json("""{"a/b":{"~key":[1,2]},"large":90071992547409931234567890}""")
        val result = AgUiJsonPatch.apply(original, json("""[{"op":"test","path":"/large","value":90071992547409931234567890.0},{"op":"move","from":"/a~1b/~0key/0","path":"/a~1b/~0key/1"},{"op":"copy","from":"/large","path":"/copy"}]""").jsonArray)
        assertEquals(json("""[2,1]"""), result.obj()["a/b"]?.obj()?.get("~key"))
        assertFailsWith<AgUiProtocolException> { AgUiJsonPatch.apply(original, json("""[{"op":"replace","path":"/large","value":0},{"op":"remove","path":"/missing"}]""").jsonArray) }
        assertEquals("90071992547409931234567890", original.obj()["large"]!!.jsonPrimitive.content)
        assertEquals(json("""[1,false]"""), AgUiJsonPatch.apply(original, json("""[{"op":"replace","path":"","value":[1,false]}]""").jsonArray))
        assertFailsWith<AgUiProtocolException> { AgUiJsonPatch.apply(original, json("""[{"op":"move","from":"/a~1b","path":"/a~1b/child"}]""").jsonArray) }
        assertFailsWith<AgUiProtocolException> { AgUiJsonPatch.apply(original, json("""[{"op":"remove","path":"/a~1b/~0key/01"}]""").jsonArray) }
    }
    @Test fun snapshotReconciliationPreservesClientActivityReasoningAndOpaqueRestoredTools() {
        val store = AgUiStore(input()); store.receive(start())
        for (raw in listOf(
            """{"type":"ACTIVITY_SNAPSHOT","messageId":"local","activityType":"client","content":{"keep":true}}""",
            """{"type":"ACTIVITY_SNAPSHOT","messageId":"server","activityType":"progress","content":{"stale":true}}""",
            """{"type":"REASONING_MESSAGE_START","messageId":"local-reason","role":"reasoning"}""",
            """{"type":"REASONING_MESSAGE_CONTENT","messageId":"local-reason","delta":"keep rationale"}""",
            """{"type":"REASONING_MESSAGE_END","messageId":"local-reason"}"""
        )) store.receive(event(raw))
        val user = input().value["messages"]!!.jsonArray[0].obj().with("metadata" to json("""{"revision":{"number":2}}"""))
        val saved = json("""{"id":"saved","role":"assistant","encryptedValue":"opaque-saved","subagentRunId":"saved-agent","toolCalls":[{"id":"saved-call","type":"function","function":{"name":"lookup","arguments":"{}"},"encryptedValue":"opaque-call"}]}""")
        store.receive(AgUiEvent(buildJsonObject { put("type","MESSAGES_SNAPSHOT"); put("messages",JsonArray(listOf(user,saved))); put("metadata",json("""{"@ag-ui/client":{"authoritativeActivityTypes":["progress"]}}""")) }))
        assertEquals(listOf("user","local","local-reason","saved"), store.messages.map { it.id })
        assertEquals(input().value["messages"]!!.jsonArray[0].obj()["content"], store.messages.first().value["content"])
        store.receive(event("""{"type":"TOOL_CALL_START","toolCallId":"saved-call","toolCallName":"lookup","parentMessageId":"saved"}"""))
        store.receive(event("""{"type":"TOOL_CALL_END","toolCallId":"saved-call","subagentRunId":"saved-agent"}"""))
        store.receive(event("""{"type":"REASONING_ENCRYPTED_VALUE","subtype":"tool-call","entityId":"saved-call","encryptedValue":"opaque-next","subagentRunId":"saved-agent"}"""))
        store.receive(AgUiEvent(buildJsonObject { put("type","TOOL_CALL_RESULT"); put("toolCallId","saved-call"); put("messageId","saved-result"); put("content",user.getValue("content")) }))
        store.receive(event("""{"type":"STATE_SNAPSHOT","snapshot":null}"""))
        store.receive(event("""{"type":"RUN_FINISHED","threadId":"thread","runId":"run"}""")); store.finish()
        val next = store.snapshot.nextInput("revision")
        assertFalse(next.value.containsKey("state")); assertEquals(JsonNull,store.state)
        assertFalse(next.value["messages"]!!.jsonArray.any { it.obj().string("role") == "activity" })
        assertEquals("opaque-next",store.messages.find { it.id == "saved" }!!.value["toolCalls"]!!.jsonArray[0].obj().string("encryptedValue"))
        assertEquals(user["content"],store.messages.find { it.id == "saved-result" }!!.value["content"])
    }
    @Test fun pinnedMirrorsMatchCanonicalRepositoryFixtures() {
        val root = java.io.File(System.getProperty("user.dir"), "../../protocol/agui")
        assertTrue(root.isDirectory, "Run from the SDK Gradle project")
        assertEquals(root.resolve("schema-1.0.json").readText(), javaClass.getResourceAsStream("/agui/schema-1.0.json")!!.bufferedReader().readText())
        assertEquals(root.resolve("testdata/conformance.json").readText(), javaClass.getResourceAsStream("/agui/conformance.json")!!.bufferedReader().readText())
    }
    @Test fun sseParserHandlesMultilineAndRejectsLimits() {
        val parser = AgUiSSEParser()
        val wire = "\uFEFF: heartbeat\r\n\r\ndata: {\r\ndata: \"type\":\"CUSTOM\",\"name\":\"fixture\",\"value\":\"héllo 🌍\"}\r\n\r\ndata: trailing"
        val frames = wire.toByteArray().flatMap { parser.feed(byteArrayOf(it)) }; parser.finish()
        assertEquals(1, frames.size); assertEquals("héllo 🌍", json(frames.single()).obj().string("value"))
        assertFailsWith<AgUiProtocolException> { AgUiSSEParser(3).feed("data: 1234".toByteArray()) }
    }
    @Test fun sseParserRetainsLargeToolPayloadWithoutBoxingOrKeepingLargeBuffer() {
        val parser = AgUiSSEParser()
        val oldBufferField = parser.javaClass.getDeclaredField("line").apply { isAccessible = true }
        val initialBuffer = oldBufferField.get(parser)
        assertTrue(parser.feed("\uFEFFdata: ".toByteArray()).isEmpty())
        val size = 17_391_048 // Actual native report tool-result frame that exhausted Android's 192 MiB heap.
        val chunk = ByteArray(8192) { 'x'.code.toByte() }
        repeat(size / chunk.size) { assertTrue(parser.feed(chunk).isEmpty()) }
        assertTrue(parser.feed(chunk.copyOf(size % chunk.size)).isEmpty())
        val frames = parser.feed("\r\n\r\n".toByteArray())
        assertEquals(1, frames.size)
        assertEquals(size, frames.single().length)
        assertTrue(frames.single().all { it == 'x' })
        assertNotSame(initialBuffer, oldBufferField.get(parser))
        assertEquals(listOf("next"), parser.feed("data: next\n\n".toByteArray()))
        parser.finish()
    }
    @Test fun inheritedToolOwnershipResumeCoverageAndClientResults() {
        val store = AgUiStore(input()); store.receive(start())
        store.receive(event("""{"type":"TEXT_MESSAGE_START","messageId":"owner","subagentRunId":"child"}"""))
        store.receive(event("""{"type":"TEXT_MESSAGE_END","messageId":"owner"}"""))
        store.receive(event("""{"type":"TOOL_CALL_START","toolCallId":"client-tool","toolCallName":"lookup","parentMessageId":"owner"}"""))
        store.receive(event("""{"type":"TOOL_CALL_ARGS","toolCallId":"client-tool","delta":"{}","subagentRunId":"child"}"""))
        store.receive(event("""{"type":"TOOL_CALL_END","toolCallId":"client-tool"}"""))
        store.receive(event("""{"type":"RUN_FINISHED","threadId":"thread","runId":"run","outcome":{"type":"success","pendingToolCallIds":[]}}""")); store.finish()
        assertEquals(listOf("client-tool"), store.pendingToolCallIds)
        val result = AgUiMessage(json("""{"id":"client-result","role":"tool","toolCallId":"client-tool","content":[{"type":"text","text":"done"}]}""").obj())
        assertFailsWith<AgUiProtocolException> { store.toolResultsInput("continue", emptyList()) }
        val next = store.toolResultsInput("continue", listOf(result))
        assertEquals("client-result", next.value["messages"]!!.jsonArray.last().obj().string("id"))
        assertEquals(input().value["tools"], next.value["tools"])
        val interrupted = AgUiStore(input()); interrupted.receive(start())
        interrupted.receive(event("""{"type":"RUN_FINISHED","threadId":"thread","runId":"run","outcome":{"type":"interrupt","interrupts":[{"id":"old","reason":"approval","expiresAt":"2000-01-01T00:00:00Z"}]}}"""))
        assertFailsWith<AgUiProtocolException> { interrupted.nextInput("resumed") }
        assertFailsWith<AgUiProtocolException> { interrupted.nextInput("resumed", resume = json("""[{"interruptId":"old","status":"resolved"}]""").jsonArray) }
        assertNotNull(interrupted.nextInput("resumed", resume = json("""[{"interruptId":"old","status":"cancelled"}]""").jsonArray))
    }
    @Test fun liveStandardEndpointWhenConfigured(): Unit = runBlocking {
        val address = System.getenv("AGENTLY_AGUI_LIVE_URL")
        org.junit.Assume.assumeTrue("Set AGENTLY_AGUI_LIVE_URL for actual assembled backend transport proof", !address.isNullOrBlank())
        val client = AgUiClient(EndpointConfig(address!!))
        val discovery = AgUiRunInput.create(java.util.UUID.randomUUID().toString(), java.util.UUID.randomUUID().toString(), emptyList(), forwardedProps = AgentlyAgUiExtensions.forwardedProps("capabilities"))
        val capabilities = client.run(discovery).toList()
        assertTrue(capabilities.none { it.event.type == "RUN_ERROR" }); assertNotNull(capabilities.last().snapshot.capabilities)
        val request = AgUiRunInput.create(java.util.UUID.randomUUID().toString(), java.util.UUID.randomUUID().toString(), listOf(AgUiMessage.user(java.util.UUID.randomUUID().toString(), jstring("Hello local fixture"))))
        val updates = client.run(request).toList()
        assertTrue(updates.none { it.event.type == "RUN_ERROR" }, updates.lastOrNull()?.event?.value.toString())
        val final = updates.last().snapshot
        assertEquals("RUN_FINISHED", final.terminalEvent!!.type)
        assertTrue(final.messages.any { it.role == "assistant" && !it.value.string("content").isNullOrEmpty() })
        assertEquals(JsonArray(final.messages.filter { it.role != "activity" }.map { it.value }), final.nextInput(java.util.UUID.randomUUID().toString()).value["messages"])
    }
    @Test fun cancellingCollectionStopsTransportWithoutSynthesizingSuccess(): Unit = runBlocking {
        val server = MockWebServer(); server.start()
        try {
            val body = "data: {\"type\":\"RUN_STARTED\",\"threadId\":\"thread\",\"runId\":\"run\"}\n\n" + ": " + "x".repeat(100000)
            server.enqueue(MockResponse().setHeader("Content-Type", "text/event-stream").setBody(body).throttleBody(100,100,TimeUnit.MILLISECONDS))
            val received = AgUiClient(EndpointConfig(server.url("/run").toString())).run(input()).take(1).toList()
            assertEquals(listOf("RUN_STARTED"), received.map { it.event.type })
            assertNull(received.single().snapshot.terminalEvent)
        } finally { server.shutdown() }
    }
    @Test fun disconnectThenFullReplayRebuildsFromAcceptedInputWithoutDuplicatingContent(): Unit = runBlocking {
        val server=MockWebServer();server.start()
        try {
            val accepted=input()
            val prefix="data: {\"type\":\"RUN_STARTED\",\"threadId\":\"thread\",\"runId\":\"run\"}\n\n"+
                "data: {\"type\":\"TEXT_MESSAGE_START\",\"messageId\":\"assistant\",\"role\":\"assistant\"}\n\n"+
                "data: {\"type\":\"TEXT_MESSAGE_CONTENT\",\"messageId\":\"assistant\",\"delta\":\"partial \"}\n\n"
            server.enqueue(MockResponse().setHeader("Content-Type","text/event-stream").setBody(prefix).setHeader("Content-Length",prefix.toByteArray().size+1000).setSocketPolicy(okhttp3.mockwebserver.SocketPolicy.DISCONNECT_AT_END))
            val client=AgUiClient(EndpointConfig(server.url("/run").toString()))
            val partial=mutableListOf<AgUiUpdate>()
            assertFails { client.run(accepted).collect { partial+=it } }
            assertTrue(partial.isNotEmpty());assertEquals("partial ",partial.last().snapshot.messages.find { it.id=="assistant" }!!.value.string("content"))
            val user=accepted.value["messages"]!!.jsonArray[0]
            val snapshot=buildJsonObject { put("type","MESSAGES_SNAPSHOT");put("messages",JsonArray(listOf(user,json("""{"id":"assistant","role":"assistant","content":"partial final","encryptedValue":"opaque-replayed"}""")))) }
            val rest="data: {\"type\":\"TEXT_MESSAGE_CONTENT\",\"messageId\":\"assistant\",\"delta\":\"final\"}\n\n"+
                "data: {\"type\":\"TEXT_MESSAGE_END\",\"messageId\":\"assistant\"}\n\n"+"data: $snapshot\n\n"+
                "data: {\"type\":\"RUN_FINISHED\",\"threadId\":\"thread\",\"runId\":\"run\"}\n\n"
            server.enqueue(MockResponse().setHeader("Content-Type","text/event-stream").setBody(prefix+rest))
            val replay=client.run(partial.last().snapshot.input).toList();val final=replay.last().snapshot
            assertEquals("partial final",final.messages.find { it.id=="assistant" }!!.value.string("content"));assertEquals(1,final.messages.count { it.id=="assistant" })
            assertEquals("opaque-replayed",final.messages.find { it.id=="assistant" }!!.value.string("encryptedValue"))
            assertEquals(user,final.messages.first().value)
            assertEquals(server.takeRequest().body.readUtf8(),server.takeRequest().body.readUtf8())
        }finally{server.shutdown()}
    }
    @Test fun actualPostSseSplitBytesPreservesHeadersAndAllProtocolState(): Unit = runBlocking {
        val server = MockWebServer(); server.start()
        try {
            val f = fixture
            val wire = "\uFEFF: heartbeat\r\n\r\n" + f["events"]!!.jsonArray.mapIndexed { index, raw ->
                val line = if (index % 3 == 0) "\r\n" else if (index % 3 == 1) "\n" else "\r"
                "data: $raw$line$line"
            }.joinToString("")
            server.enqueue(MockResponse().setHeader("Content-Type", "text/event-stream; charset=utf-8").setBody(wire).throttleBody(7, 1, TimeUnit.MILLISECONDS))
            val updates = AgUiClient(EndpointConfig(server.url("/run").toString(), defaultHeadersProvider = { mapOf("X-Test" to "fixture") })).run(input()).toList()
            val request = server.takeRequest(2, TimeUnit.SECONDS)!!
            assertEquals("POST", request.method); assertEquals("fixture", request.getHeader("X-Test"))
            assertEquals(input().value, json(request.body.readUtf8()))
            assertEquals(f["expectedMessages"], JsonArray(updates.last().snapshot.messages.map { it.value }))
            assertEquals(f["expectedState"], updates.last().snapshot.state)
            assertEquals(f["expectedNormalizedTypes"], JsonArray(updates.map { jstring(it.event.type) }))
            server.enqueue(MockResponse().setHeader("Content-Type", "text/event-stream").setBody("data: {\"type\":\"RUN_ERROR\",\"message\":\"server failed\"}\n\n"))
            assertEquals("RUN_ERROR", AgUiClient(EndpointConfig(server.url("/run").toString())).run(input()).toList().last().event.type)
            server.enqueue(MockResponse().setHeader("Content-Type", "text/event-stream").setBody("data: {\"type\":\"RUN_STARTED\",\"threadId\":\"thread\",\"runId\":\"run\"}\n\n"))
            assertFailsWith<AgUiProtocolException> { AgUiClient(EndpointConfig(server.url("/run").toString())).run(input()).toList() }
        } finally { server.shutdown() }
    }
}
