package com.viant.agentlysdk.agui

import com.viant.agentlysdk.*
import com.viant.agentlysdk.stream.*
import kotlinx.coroutines.*
import kotlinx.coroutines.flow.*
import kotlinx.serialization.json.*
import okhttp3.*
import okhttp3.mockwebserver.*
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicInteger
import kotlin.test.*

class AgUiConversationTransportTest {
    private val json = Json { ignoreUnknownKeys = true }
    private fun obj(text: String) = json.parseToJsonElement(text).jsonObject
    private fun bootstrap(marker: Int = 0) = obj("""{"version":"1","threadId":"thread","transcript":{"schemaVersion":"1","conversation":{"conversationId":"thread","turns":[]},"feeds":[{"feedId":"report","title":"Report $marker","itemCount":2,"data":{"rows":[1,2]}}]},"messages":[],"hostActivities":[{"id":"host","role":"activity","activityType":"mcp-apps","content":{"resourceUri":"ui://report"}}],"unavailableHostActivityIds":["missing"],"state":{},"runs":[],"projection":{"lossless":true,"unavailableMessageIds":[]}}""")
    private fun sse(input: JsonObject, result: JsonElement): MockResponse {
        val run = input.getValue("runId").jsonPrimitive.content
        val wire = input.getValue("threadId").toString()
        return MockResponse().setHeader("Content-Type", "text/event-stream").setBody("data: {\"type\":\"RUN_STARTED\",\"threadId\":$wire,\"runId\":\"$run\"}\n\ndata: {\"type\":\"RUN_FINISHED\",\"threadId\":$wire,\"runId\":\"$run\",\"outcome\":{\"type\":\"success\"},\"result\":$result}\n\n")
    }
    private fun client(server: MockWebServer, http: OkHttpClient = OkHttpClient(), wireThreadId: String? = null): AgentlyClient {
        val prior = server.dispatcher
        server.dispatcher = object : okhttp3.mockwebserver.Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse {
                if (request.method == "GET" && request.path?.substringBefore('?')?.matches(Regex("/v1/conversations/[^/]+")) == true) {
                    assertEquals("test", request.getHeader("X-App-Client"))
                    val id = request.path!!.substringAfterLast('/').substringBefore('?')
                    val metadata = buildJsonObject { put("id", id); wireThreadId?.let { put("aguiThreadId", it) } }
                    return MockResponse().setHeader("Content-Type", "application/json").setBody(metadata.toString())
                }
                return prior.dispatch(request)
            }
        }
        return AgentlyClient(mapOf("appAPI" to EndpointConfig(server.url("/").toString().removeSuffix("/"), httpClient = http, streamHttpClient = http, defaultHeadersProvider = { mapOf("X-App-Client" to "test") })))
    }

    @Test fun opaqueHistoryKeepsNativeIdentityAndExactWireBinding() = runBlocking {
        val server = MockWebServer(); server.start()
        val wire = "  Wire-雪\t"
        val posted = mutableListOf<JsonObject>()
        server.dispatcher = object : okhttp3.mockwebserver.Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse {
                val input = obj(request.body.readUtf8()); posted += input
                val original = bootstrap()
                val transcript = JsonObject(original.getValue("transcript").jsonObject + ("aguiThreadId" to JsonPrimitive(wire)))
                return sse(input, JsonObject(original + mapOf("threadId" to JsonPrimitive(wire), "transcript" to transcript)))
            }
        }
        try {
            val host = client(server, wireThreadId = wire)
            val history = host.getTranscript(GetTranscriptInput("thread"))
            assertEquals("thread", history.conversation?.conversationId)
            assertEquals(wire, history.aguiThreadId)
            assertEquals(wire, posted.single().getValue("threadId").jsonPrimitive.content)
        } finally { server.shutdown() }
    }

    @Test fun sharedReaderUsesDedicatedHistoryOnlyAfterForbiddenJournal() = runBlocking {
        val server=MockWebServer();server.start()
        val requests=mutableListOf<String>()
        server.dispatcher=object:okhttp3.mockwebserver.Dispatcher(){
            override fun dispatch(request:RecordedRequest):MockResponse{
                requests+=request.path.orEmpty()
                return when{
                    request.path=="/v1/conversations/thread" -> MockResponse().setBody("""{"id":"thread"}""")
                    request.path=="/v1/ag-ui/run" -> MockResponse().setResponseCode(403)
                    request.path?.startsWith("/v1/conversations/thread/transcript")==true -> MockResponse().setBody("""{"schemaVersion":"2","conversation":{"conversationId":"thread","turns":[]}}""")
                    else -> MockResponse().setResponseCode(500)
                }
            }
        }
        try{
            val host=client(server)
            val history=host.getTranscript(GetTranscriptInput("thread"))
            assertEquals("thread",history.conversation?.conversationId)
            assertEquals(listOf("/v1/ag-ui/run","/v1/conversations/thread/transcript"),requests)
            assertTrue(requests.none{it.contains("/agent/query")})
        }finally{server.shutdown()}
    }

    @Test fun sharedReaderObservationHydratesHistoryWithoutProtocolRunProjection() = runBlocking {
        val server=MockWebServer();server.start()
        val requests=java.util.Collections.synchronizedList(mutableListOf<String>())
        server.dispatcher=object:okhttp3.mockwebserver.Dispatcher(){
            override fun dispatch(request:RecordedRequest):MockResponse{
                requests+=request.path.orEmpty()
                return when {
                    request.path=="/v1/ag-ui/run" -> MockResponse().setResponseCode(403)
                    request.path?.startsWith("/v1/conversations/thread/transcript")==true -> MockResponse().setBody("""{"schemaVersion":"2","conversation":{"conversationId":"thread","turns":[]}}""")
                    request.path?.startsWith("/v1/application-events?conversationId=thread")==true -> MockResponse().setHeader("Content-Type","text/event-stream").setBody("")
                    else -> MockResponse().setResponseCode(500)
                }
            }
        }
        val host=client(server)
        try {
            val snapshot=withTimeout(5000){host.trackConversation("thread").first{it.canonicalTranscript!=null}}
            assertEquals("thread",snapshot.conversationId)
            assertEquals("thread",snapshot.canonicalTranscript?.conversation?.conversationId)
            assertTrue(snapshot.protocolRuns.isEmpty())
            assertNull(snapshot.rawCanonicalTranscript)
            assertNull(snapshot.transportError)
            assertTrue(requests.none{it.contains("/agent/query")})
        } finally {host.resetConversationTransport();server.shutdown()}
    }

    @Test fun queryBootstrapShareInjectedCookiesHeadersAndUseOnlyAgUi() = runBlocking {
        val server = MockWebServer(); server.start(); val requests = mutableListOf<RecordedRequest>()
        server.dispatcher = object : okhttp3.mockwebserver.Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse {
                synchronized(requests) { requests += request }
                val input = obj(request.body.readUtf8())
                val operation = input["forwardedProps"]!!.jsonObject["agently"]!!.jsonObject["operation"]!!.jsonPrimitive.content
                if (operation == "conversation.bootstrap") return sse(input, bootstrap()).setHeader("Set-Cookie", "bff=test; Path=/")
                val run = input.getValue("runId").jsonPrimitive.content
                return MockResponse().setHeader("Content-Type", "text/event-stream").setBody("data: {\"type\":\"RUN_STARTED\",\"threadId\":\"thread\",\"runId\":\"$run\",\"metadata\":{\"agently\":{\"presentation\":{\"version\":\"1\",\"nativeTurnId\":\"own-turn\",\"protocolRunId\":\"$run\"}}}}\n\ndata: {\"type\":\"RUN_FINISHED\",\"threadId\":\"thread\",\"runId\":\"$run\",\"outcome\":{\"type\":\"success\"}}\n\n")
            }
        }
        val jar = AgUiSessionCookies(); val http = OkHttpClient.Builder().cookieJar(jar).build(); val host = client(server, http)
        try {
            assertEquals("own-turn", withTimeout(5000) { host.query(QueryInput(conversationId = "thread", messageId = "client", query = "same text")) }.messageId)
            val chat = synchronized(requests) { requests.last() }
            assertEquals("bff=test", chat.getHeader("Cookie")); assertEquals("test", chat.getHeader("X-App-Client"))
            assertTrue(synchronized(requests) { requests.all { it.path == "/v1/ag-ui/run" || (it.method=="GET" && it.path=="/v1/conversations/thread") } })
        } finally { host.resetConversationTransport(); server.shutdown() }
    }

    @Test fun nullBootstrapStateIsOmittedFromChatAndRetainsServerState() = runBlocking {
        val server = MockWebServer(); server.start(); var postedChat: JsonObject? = null
        server.dispatcher = object : okhttp3.mockwebserver.Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse {
                val input = obj(request.body.readUtf8())
                val extension = input.getValue("forwardedProps").jsonObject.getValue("agently").jsonObject
                if (extension.getValue("operation").jsonPrimitive.content == "conversation.bootstrap")
                    return sse(input, JsonObject(bootstrap() + ("state" to JsonNull)))
                postedChat = input
                val run = input.getValue("runId").jsonPrimitive.content
                return MockResponse().setHeader("Content-Type", "text/event-stream").setBody("data: {\"type\":\"RUN_STARTED\",\"threadId\":\"thread\",\"runId\":\"$run\",\"metadata\":{\"agently\":{\"presentation\":{\"version\":\"1\",\"nativeTurnId\":\"null-state-turn\",\"protocolRunId\":\"$run\"}}}}\n\ndata: {\"type\":\"RUN_FINISHED\",\"threadId\":\"thread\",\"runId\":\"$run\",\"outcome\":{\"type\":\"success\"}}\n\n")
            }
        }
        val host = client(server)
        try {
            assertEquals("null-state-turn", withTimeout(5000) { host.query(QueryInput(conversationId = "thread", messageId = "user", query = "plan")) }.messageId)
            val posted = assertNotNull(postedChat)
            assertFalse(posted.containsKey("state"))
            val payload = posted.getValue("forwardedProps").jsonObject.getValue("agently").jsonObject.getValue("payload").jsonObject
            assertEquals(JsonPrimitive(true), payload["useServerState"])
        } finally { host.resetConversationTransport(); server.shutdown() }
    }

    @Test fun freshReconcileReadsAgainAfterOlderInflightRead() = runBlocking {
        val server = MockWebServer(); server.start(); val entered = CountDownLatch(1); val release = CountDownLatch(1); val count = AtomicInteger()
        server.dispatcher = object : okhttp3.mockwebserver.Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse {
                val input = obj(request.body.readUtf8()); val n = count.incrementAndGet()
                if (n == 1) { entered.countDown(); assertTrue(release.await(5, TimeUnit.SECONDS)) }
                return sse(input, bootstrap(n))
            }
        }
        val host = client(server)
        try {
            val older = async(Dispatchers.IO) { host.getLiveState("thread", true) }
            assertTrue(entered.await(5, TimeUnit.SECONDS)); val fresh = async(Dispatchers.IO) { host.reconcileConversation("thread") }; release.countDown()
            assertEquals("Report 1", older.await().feeds.single().title); assertEquals("Report 2", fresh.await().feeds.single().title); assertEquals(2, count.get())
        } finally { release.countDown(); host.resetConversationTransport(); server.shutdown() }
    }

    @Test fun resetFencesLateBootstrapAndPreventsSubmission() = runBlocking {
        val server = MockWebServer(); server.start(); val entered = CountDownLatch(1); val release = CountDownLatch(1); val operations=mutableListOf<String>()
        server.dispatcher = object : okhttp3.mockwebserver.Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse { val input = obj(request.body.readUtf8()); synchronized(operations){operations += input["forwardedProps"]!!.jsonObject["agently"]!!.jsonObject["operation"]!!.jsonPrimitive.content}; entered.countDown(); release.await(5, TimeUnit.SECONDS); return sse(input, bootstrap()) }
        }
        val host = client(server)
        try {
            val work = async(Dispatchers.IO) { runCatching { host.query(QueryInput(conversationId = "thread", query = "private")) } }
            assertTrue(entered.await(5, TimeUnit.SECONDS)); host.resetConversationTransport(); release.countDown()
            assertTrue(work.await().isFailure); assertEquals(2, server.requestCount)
            assertEquals("/v1/conversations/thread",server.takeRequest().path)
            val pending=server.takeRequest();assertEquals("/v1/ag-ui/run",pending.path)
            assertEquals(listOf("conversation.bootstrap"),synchronized(operations){operations.toList()})
        } finally { release.countDown(); host.resetConversationTransport(); server.shutdown() }
    }

    @Test fun nativeBootstrapAndHostActivitiesStaySeparateAndObservationIsScoped() = runBlocking {
        val server = MockWebServer(); server.start(); val paths = mutableListOf<String>()
        server.dispatcher = object : okhttp3.mockwebserver.Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse {
                synchronized(paths) { paths += request.path.orEmpty() }
                if (request.method == "GET") return MockResponse().setSocketPolicy(SocketPolicy.NO_RESPONSE)
                return sse(obj(request.body.readUtf8()), bootstrap())
            }
        }
        val host = client(server)
        try {
            val snapshot = withTimeout(5000) { host.trackConversation("thread").first { it.canonicalTranscript != null } }
            assertEquals("Report 0", snapshot.feeds.single().title); assertEquals("host", snapshot.hostActivities.single().id)
            assertEquals(listOf("missing"), snapshot.unavailableHostActivityIds); assertTrue(snapshot.bufferedMessages.none { it.id == "host" })
            assertTrue(synchronized(paths) { paths.filter { it.startsWith("/v1/application-events") }.all { it.contains("conversationId=thread") } })
        } finally { host.resetConversationTransport(); server.shutdown() }
    }

    @Test fun snapshotsReplaceTextWithoutContentDedupAndKeepExactAlias() {
        val input = AgUiRunInput.create("thread", "run", listOf(AgUiMessage.user("client", JsonPrimitive("same text"))))
        val store = AgUiStore(input); val tracker = ConversationStreamTracker("thread"); tracker.hydrate(ConversationStateResponse(conversation = ConversationState("thread")))
        val aliases = mutableMapOf<String, String>(); val projector = AgUiNativePresentation("thread", "run", emptyList(), "client", { tracker.applyEvent(it) }, {}, { c, n -> aliases[c] = n })
        fun receive(text: String) { store.receive(AgUiEvent(obj(text))) { e -> projector.consume(AgUiUpdate(e, e, store.snapshot)) } }
        val metadata = """{"agently":{"presentation":{"version":"1","nativeTurnId":"native-turn","nativeMessageId":"native-assistant","protocolRunId":"run"}}}"""
        receive("""{"type":"RUN_STARTED","threadId":"thread","runId":"run","metadata":$metadata}""")
        receive("""{"type":"ACTIVITY_SNAPSHOT","messageId":"identity","activityType":"agently.user-identity","content":{"version":"1","protocolRunId":"run","nativeTurnId":"native-turn","clientMessageId":"client","clientRequestId":"client","nativeUserMessageId":"native-user"},"metadata":$metadata}""")
        receive("""{"type":"TEXT_MESSAGE_START","messageId":"protocol-assistant","role":"assistant","metadata":$metadata}""")
        receive("""{"type":"TEXT_MESSAGE_CONTENT","messageId":"protocol-assistant","delta":"repeat repeat","metadata":$metadata}""")
        receive("""{"type":"TEXT_MESSAGE_CONTENT","messageId":"protocol-assistant","delta":" repeat","metadata":$metadata}""")
        assertEquals("repeat repeat repeat", tracker.snapshot().bufferedMessages.single { it.id == "native-assistant" }.content)
        assertEquals("repeat repeat repeat", tracker.snapshot().liveExecutionGroupsById["native-assistant"]?.content)
        assertEquals(mapOf("client" to "native-user"), aliases)
    }

    @Test fun interruptsWaitAndDisconnectDoesNotSynthesizeCompletion() {
        val input = AgUiRunInput.create("thread", "run", emptyList()); val store = AgUiStore(input); val events = mutableListOf<SSEEvent>()
        val projector = AgUiNativePresentation("thread", "run", emptyList(), null, { events += it }, {}, { _, _ -> })
        fun receive(text: String) { store.receive(AgUiEvent(obj(text))) { e -> projector.consume(AgUiUpdate(e, e, store.snapshot)) } }
        receive("""{"type":"RUN_STARTED","threadId":"thread","runId":"run","metadata":{"agently":{"identityVersion":"1","nativeTurnId":"native"}}}""")
        assertTrue(events.none { it.type in setOf("turn_completed", "turn_failed", "turn_canceled") })
        receive("""{"type":"RUN_FINISHED","threadId":"thread","runId":"run","outcome":{"type":"interrupt","interrupts":[{"id":"approval","reason":"approval","payload":{"message":"Approve"}}]}}""")
        assertTrue(events.any { it.type == "elicitation_requested" && it.elicitationId == "approval" }); assertTrue(events.none { it.type == "turn_completed" })
    }

    @Test fun malformedPresentationDoesNotSupplyNativeIds() {
        assertNull(presentation(obj("""{"agently":{"presentation":{"version":"1","nativeTurnId":42}}}""")))
        assertNull(presentation(obj("""{"agently":{"presentation":{"version":"1","iteration":-1}}}""")))
        assertEquals("native", presentation(obj("""{"agently":{"presentation":{"version":"1","nativeTurnId":"native","future":{"opaque":true}}}}"""))?.string("nativeTurnId"))
    }
    @Test fun unrelatedHistoryCannotAdmitOwnRequestAndAgUiFailureNeverFallsBack() = runBlocking {
        val server = MockWebServer(); server.start(); val paths = mutableListOf<String>()
        server.dispatcher = object : okhttp3.mockwebserver.Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse {
                synchronized(paths) { paths += request.path.orEmpty() }
                val input = obj(request.body.readUtf8())
                val operation = input["forwardedProps"]!!.jsonObject["agently"]!!.jsonObject["operation"]!!.jsonPrimitive.content
                if (operation == "conversation.bootstrap") return sse(input, bootstrap())
                val run = input.getValue("runId").jsonPrimitive.content
                return MockResponse().setHeader("Content-Type", "text/event-stream").setBody("data: {\"type\":\"RUN_STARTED\",\"threadId\":\"thread\",\"runId\":\"$run\"}\n\ndata: {\"type\":\"MESSAGES_SNAPSHOT\",\"messages\":[{\"id\":\"old\",\"role\":\"activity\",\"activityType\":\"agently.turn\",\"content\":{\"version\":\"1\",\"nativeTurnId\":\"older-turn\",\"status\":\"running\",\"queueSequence\":\"1\"},\"metadata\":{\"agently\":{\"presentation\":{\"version\":\"1\",\"nativeTurnId\":\"older-turn\"}}}}]}\n\ndata: {\"type\":\"RUN_FINISHED\",\"threadId\":\"thread\",\"runId\":\"$run\",\"outcome\":{\"type\":\"success\"}}\n\n")
            }
        }
        val host = client(server)
        try {
            assertTrue(withTimeout(5000) { runCatching { host.query(QueryInput(conversationId = "thread", messageId = "own-client", query = "same text")) } }.isFailure)
            assertTrue(synchronized(paths) { paths.all { it == "/v1/ag-ui/run" || it == "/v1/conversations/thread" } })
            server.dispatcher = object : okhttp3.mockwebserver.Dispatcher() { override fun dispatch(request: RecordedRequest) = MockResponse().setResponseCode(404) }
            assertTrue(runCatching { host.query(QueryInput(conversationId = "thread", query = "hello")) }.isFailure)
            val observed=List(server.requestCount){server.takeRequest(1,TimeUnit.SECONDS)!!}
            assertTrue(observed.any{it.method=="GET" && it.path=="/v1/conversations/thread"})
            assertTrue(observed.any{it.method=="POST" && it.path=="/v1/ag-ui/run"})
            assertTrue(observed.all{it.path in setOf("/v1/conversations/thread","/v1/ag-ui/run")})
        } finally { host.resetConversationTransport(); server.shutdown() }
    }

    @Test fun durableApprovalDiscoversSuccessorWithoutRepeatingDecision() = runBlocking {
        val server = MockWebServer(); server.start(); val decisions = AtomicInteger(); val attached = mutableListOf<String>()
        server.dispatcher = object : okhttp3.mockwebserver.Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse {
                if (request.path?.startsWith("/v1/tool-approvals") == true) return MockResponse().setBody("{\"rows\":[{\"id\":\"approval\",\"conversationId\":\"thread\",\"toolName\":\"write\",\"status\":\"pending\"}]}")
                val input = obj(request.body.readUtf8()); val extension = input["forwardedProps"]!!.jsonObject["agently"]!!.jsonObject
                val operation = extension["operation"]!!.jsonPrimitive.content
                return when (operation) {
                    "conversation.bootstrap" -> sse(input, JsonObject(bootstrap() + ("runs" to obj("{\"runs\":[{\"runId\":\"original\",\"status\":\"interrupted\",\"kind\":\"chat\"}]}").getValue("runs"))))
                    "run.attach" -> {
                        val run = input.getValue("runId").jsonPrimitive.content; synchronized(attached) { attached += run }
                        if (run == "original") MockResponse().setHeader("Content-Type", "text/event-stream").setBody("data: {\"type\":\"RUN_STARTED\",\"threadId\":\"thread\",\"runId\":\"original\",\"metadata\":{\"agently\":{\"identityVersion\":\"1\",\"nativeTurnId\":\"native\"}}}\n\ndata: {\"type\":\"RUN_FINISHED\",\"threadId\":\"thread\",\"runId\":\"original\",\"outcome\":{\"type\":\"interrupt\",\"interrupts\":[{\"id\":\"approval\",\"reason\":\"approval\"}]}}\n\n")
                        else sse(input, JsonObject(emptyMap()))
                    }
                    "approval.decide" -> { decisions.incrementAndGet(); sse(input, obj("{\"status\":\"ok\",\"protocol\":{\"continuationRunId\":\"successor\"}}")) }
                    "run.get" -> sse(input, obj("{\"version\":\"1\",\"runId\":\"original\",\"resumedByRunId\":\"successor\"}"))
                    else -> MockResponse().setResponseCode(400)
                }
            }
        }
        val host = client(server)
        try {
            assertEquals("ok", withTimeout(5000) { host.decideToolApproval(DecideToolApprovalInput("approval", "approve")) }.status)
            withTimeout(5000) { while (synchronized(attached) { "successor" !in attached }) delay(10) }
            assertEquals(1, decisions.get()); assertTrue(synchronized(attached) { "original" in attached && "successor" in attached })
        } finally { host.resetConversationTransport(); server.shutdown() }
    }

}
