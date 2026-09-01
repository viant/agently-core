package com.viant.agentlysdk
import kotlinx.coroutines.CoroutineStart
import kotlinx.coroutines.async
import kotlinx.coroutines.flow.take
import kotlinx.coroutines.flow.toList
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeout
import okhttp3.Interceptor
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Protocol
import okhttp3.Request
import okhttp3.Response
import okhttp3.ResponseBody.Companion.toResponseBody
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import okhttp3.mockwebserver.RecordedRequest
import okhttp3.mockwebserver.SocketPolicy
import okio.Buffer
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import kotlin.test.AfterTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertNotNull
import kotlin.test.assertTrue
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.boolean
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import kotlinx.serialization.json.put

class AgentlyClientTest {
    private val server = MockWebServer()

    @Test
    fun `deleteSchedule calls scheduler delete endpoint`() = runBlocking {
        server.enqueue(MockResponse().setResponseCode(204))
        server.start()

        client().deleteSchedule("schedule one")

        val request = server.takeRequest()
        assertEquals("DELETE", request.method)
        assertEquals("/v1/api/agently/scheduler/schedule/schedule%20one", request.path)
    }

    @Test
    fun `schedule endpoints decode single and list data envelopes once`() = runBlocking {
        server.enqueue(
            MockResponse().setBody(
                """{"data":{"id":"one","name":"Hello","agentRef":"steward","scheduleType":"cron"}}"""
            )
        )
        server.enqueue(
            MockResponse().setBody(
                """{"data":{"schedules":[{"id":"two","name":"World","agentRef":"steward","scheduleType":"interval"}]}}"""
            )
        )
        server.start()
        val client = client()

        assertEquals("one", client.getSchedule("one")?.id)
        assertEquals(listOf("two"), client.listSchedules().map { it.id })
    }

    @Test
    fun `listScheduleRuns preserves rows paging and filters`() = runBlocking {
        server.enqueue(
            MockResponse().setBody(
                """{"status":"ok","data":[{"Id":"run-1","ScheduleId":"schedule-1","Status":"succeeded"}],"info":{"pageCount":2,"totalCount":3}}"""
            )
        )
        server.start()

        val page = client().listScheduleRuns(
            scheduleId = "schedule-1",
            filters = mapOf("status" to "succ"),
            page = 2,
            size = 1
        )

        assertEquals(listOf("run-1"), page.rows.map { it.id })
        assertEquals(2, page.pageCount)
        assertEquals(3, page.totalCount)
        assertEquals(
            "/v1/api/agently/scheduler/run?scheduleId=schedule-1&status=succ&page=2&size=1",
            server.takeRequest().path
        )
    }

    @Test
    fun `bounded get accepts an exact limit response`() {
        server.enqueue(MockResponse().setBody("12345678"))
        server.start()
        val rest = RestClient(EndpointRegistry(mapOf("appAPI" to EndpointConfig(server.url("/").toString()))))

        val body = rest.get("appAPI", "/bounded", maxResponseBytes = 8) { it }

        assertEquals("12345678", body)
    }

    @Test
    fun `bounded get rejects a declared oversized response`() {
        server.enqueue(MockResponse().setBody("123456789"))
        server.start()
        val rest = RestClient(EndpointRegistry(mapOf("appAPI" to EndpointConfig(server.url("/").toString()))))

        assertFailsWith<ResponseTooLargeException> {
            rest.get<String>("appAPI", "/bounded", maxResponseBytes = 8) { it }
        }
    }

    @Test
    fun `bounded get rejects a chunked response crossing the limit`() {
        server.enqueue(MockResponse().setChunkedBody("123456789", 2))
        server.start()
        val rest = RestClient(EndpointRegistry(mapOf("appAPI" to EndpointConfig(server.url("/").toString()))))

        assertFailsWith<ResponseTooLargeException> {
            rest.get<String>("appAPI", "/bounded", maxResponseBytes = 8) { it }
        }
    }

    @Test
    fun `get retries once when response body ends early`() {
        server.enqueue(
            MockResponse()
                .setBody("incomplete response")
                .setSocketPolicy(SocketPolicy.DISCONNECT_DURING_RESPONSE_BODY)
        )
        server.enqueue(MockResponse().setBody("complete response"))
        server.start()
        val rest = RestClient(EndpointRegistry(mapOf("appAPI" to EndpointConfig(server.url("/").toString()))))

        val body = rest.get("appAPI", "/retry") { it }

        assertEquals("complete response", body)
        assertEquals(2, server.requestCount)
    }

    @AfterTest
    fun tearDown() {
        server.shutdown()
    }

    @Test
    fun `getWorkspaceMetadata unwraps data envelope and default fallbacks`() = runBlocking {
        server.enqueue(
            MockResponse().setBody(
                """
                {
                  "data": {
                    "workspaceRoot": "/tmp/workspace",
                    "appName": "Workspace",
                    "appIconRef": "builtin:viant",
                    "defaults": {
                      "appName": "Workspace",
                      "appIconRef": "builtin:viant",
                      "agent": "coder",
                      "model": "gpt-5.4",
                      "embedder": "openai_text"
                    }
                  }
                }
                """.trimIndent()
            )
        )
        server.start()
        val client = client()

        val result = client.getWorkspaceMetadata()

        assertEquals("/tmp/workspace", result.workspaceRoot)
        assertEquals("Workspace", result.appName)
        assertEquals("builtin:viant", result.appIconRef)
        assertEquals("coder", result.defaultAgent)
        assertEquals("gpt-5.4", result.defaultModel)
        assertEquals("openai_text", result.defaultEmbedder)
        assertEquals("/v1/workspace/metadata", server.takeRequest().path)
    }

    @Test
    fun `getWorkspaceMetadata appends target context query params when provided`() = runBlocking {
        server.enqueue(MockResponse().setBody("""{"workspaceRoot":"/tmp/workspace"}"""))
        server.start()
        val client = client()

        client.getWorkspaceMetadata(
            MetadataTargetContext(
                platform = " android ",
                formFactor = " tablet ",
                surface = " app ",
                capabilities = listOf(" markdown ", "", "chart")
            )
        )

        val request = server.takeRequest()
        val path = request.path!!
        assertTrue(path.startsWith("/v1/workspace/metadata?"))
        assertTrue(path.contains("platform=android"))
        assertTrue(path.contains("formFactor=tablet"))
        assertTrue(path.contains("surface=app"))
        assertEquals(listOf("markdown", "chart"), request.requestUrl!!.queryParameterValues("capabilities"))
    }

    @Test
    fun `session debug options append debug headers`() = runBlocking {
        server.enqueue(MockResponse().setBody("""{"workspaceRoot":"/tmp/workspace"}"""))
        server.start()
        val client = AgentlyClient(
            endpoints = mapOf(
                "appAPI" to EndpointConfig(baseUrl = server.url("/").toString().trimEnd('/'))
            ),
            sessionDebug = SessionDebugOptions(
                enabled = true,
                level = "trace",
                components = listOf("conversation", "reactor")
            )
        )

        client.getWorkspaceMetadata()

        val request = server.takeRequest()
        assertEquals("true", request.getHeader("X-Agently-Debug"))
        assertEquals("trace", request.getHeader("X-Agently-Debug-Level"))
        assertEquals("conversation,reactor", request.getHeader("X-Agently-Debug-Components"))
    }

    @Test
    fun `query uses long running endpoint client`() = runBlocking {
        server.enqueue(MockResponse().setBody("""{"conversationId":"conv-1","content":"done"}"""))
        server.start()
        val shortClient = OkHttpClient.Builder()
            .addInterceptor { chain ->
                chain.proceed(chain.request().newBuilder().header("X-Test-Transport", "short").build())
            }
            .build()
        val longRunningClient = OkHttpClient.Builder()
            .addInterceptor { chain ->
                chain.proceed(chain.request().newBuilder().header("X-Test-Transport", "long").build())
            }
            .build()
        val client = AgentlyClient(
            endpoints = mapOf(
                "appAPI" to EndpointConfig(
                    baseUrl = server.url("/").toString().trimEnd('/'),
                    httpClient = shortClient,
                    longRunningHttpClient = longRunningClient
                )
            )
        )

        val output = client.query(QueryInput(conversationId = "conv-1", query = "hello"))

        assertEquals("done", output.content)
        val request = server.takeRequest()
        assertEquals("/v1/agent/query", request.path)
        assertEquals("long", request.getHeader("X-Test-Transport"))
    }

    @Test
    fun `datasource fetch uses long running endpoint client`() = runBlocking {
        server.enqueue(MockResponse().setBody("""{"rows":[]}"""))
        server.start()
        val shortClient = OkHttpClient.Builder()
            .addInterceptor { chain ->
                chain.proceed(chain.request().newBuilder().header("X-Test-Transport", "short").build())
            }
            .build()
        val longRunningClient = OkHttpClient.Builder()
            .addInterceptor { chain ->
                chain.proceed(chain.request().newBuilder().header("X-Test-Transport", "long").build())
            }
            .build()
        val client = AgentlyClient(
            endpoints = mapOf(
                "appAPI" to EndpointConfig(
                    baseUrl = server.url("/").toString().trimEnd('/'),
                    httpClient = shortClient,
                    longRunningHttpClient = longRunningClient
                )
            )
        )

        client.fetchDatasource(FetchDatasourceInput(id = "report"))

        val request = server.takeRequest()
        assertEquals("/v1/api/datasources/report/fetch", request.path)
        assertEquals("long", request.getHeader("X-Test-Transport"))
    }

    @Test
    fun `fetchForgeWindowMetadata unwraps data envelope and encodes window key`() = runBlocking {
        server.enqueue(
            MockResponse().setBody(
                """
                {
                  "data": {
                    "view": {
                      "content": {
                        "containers": [
                          { "id": "reportRoot" }
                        ]
                      }
                    }
                  }
                }
                """.trimIndent()
            )
        )
        server.start()
        val client = client()

        val result = client.fetchForgeWindowMetadata(" report/review ")

        assertEquals("reportRoot", result.jsonObject["view"]!!
            .jsonObject["content"]!!
            .jsonObject["containers"]!!
            .jsonArray.first().jsonObject["id"]!!.jsonPrimitive.content)
        assertEquals(
            "/v1/api/agently/forge/window/report%2Freview",
            server.takeRequest().path
        )
    }

    @Test
    fun `fetchForgeWindowMetadata rejects blank window keys before dispatch`() = runBlocking {
        server.start()
        val client = client()

        assertFailsWith<IllegalArgumentException> {
            client.fetchForgeWindowMetadata("   ")
        }
        assertEquals(0, server.requestCount)
    }

    @Test
    fun `fetchForgeWindowMetadata appends target context query params when provided`() = runBlocking {
        server.enqueue(MockResponse().setBody("""{"data":{"view":{"content":{"containers":[]}}}}"""))
        server.start()
        val client = client()

        client.fetchForgeWindowMetadata(
            "order",
            MetadataTargetContext(
                platform = " android ",
                formFactor = " phone ",
                surface = " app ",
                capabilities = listOf(" markdown ", " ", "chart")
            )
        )

        val request = server.takeRequest()
        val path = request.path!!
        assertTrue(path.startsWith("/v1/api/agently/forge/window/order?"))
        assertTrue(path.contains("platform=android"))
        assertTrue(path.contains("formFactor=phone"))
        assertTrue(path.contains("surface=app"))
        assertEquals(listOf("markdown", "chart"), request.requestUrl!!.queryParameterValues("capabilities"))
    }

    @Test
    fun `getForgeWindowMetadata aliases fetch helper for cross sdk naming parity`() = runBlocking {
        server.enqueue(MockResponse().setBody("""{"data":{"view":{"content":{"containers":[]}}}}"""))
        server.start()
        val client = client()

        client.getForgeWindowMetadata(
            "order",
            MetadataTargetContext(
                platform = " android ",
                formFactor = " tablet ",
                surface = " app ",
                capabilities = listOf(" markdown ", "", "chart")
            )
        )

        val request = server.takeRequest()
        val path = request.path!!
        assertTrue(path.startsWith("/v1/api/agently/forge/window/order?"))
        assertTrue(path.contains("platform=android"))
        assertTrue(path.contains("formFactor=tablet"))
        assertTrue(path.contains("surface=app"))
        assertEquals(listOf("markdown", "chart"), request.requestUrl!!.queryParameterValues("capabilities"))
    }

    @Test
    fun `ui bridge rpc client addresses command plane by explicit client id`() = runBlocking {
        server.enqueue(
            MockResponse()
                .setHeader("Mcp-Session-Id", "session-123")
                .setBody("""{"jsonrpc":"2.0","result":{"accepted":true}}""")
        )
        server.enqueue(
            MockResponse()
                .setBody("""{"jsonrpc":"2.0","result":{"ok":true}}""")
        )
        server.enqueue(
            MockResponse()
                .setBody("""{"jsonrpc":"2.0","result":null}""")
        )
        server.enqueue(
            MockResponse()
                .setBody("""{"jsonrpc":"2.0","result":{"ok":true}}""")
        )
        server.start()
        val client = client()
        val bridge = UIBridgeRpcClient(client)

        bridge.hello("android-ui-test")
        bridge.snapshot(
            clientId = "android-ui-test",
            data = buildJsonObject {
                put("windows", buildJsonObject { })
            }
        )
        bridge.poll("android-ui-test", timeoutMs = 10)
        bridge.respond(commandId = "cmd-1", ok = true, result = buildJsonObject { put("ok", JsonPrimitive(true)) })

        val helloRequest: RecordedRequest = server.takeRequest()
        assertEquals("/v1/ui/rpc", helloRequest.path)
        assertEquals(null, helloRequest.getHeader("Mcp-Session-Id"))
        assertTrue(helloRequest.body.readUtf8().contains("\"jsonrpc\":\"2.0\""))

        val snapshotRequest: RecordedRequest = server.takeRequest()
        assertEquals("/v1/ui/rpc", snapshotRequest.path)
        assertEquals(null, snapshotRequest.getHeader("Mcp-Session-Id"))
        val snapshotBody = snapshotRequest.body.readUtf8()
        assertTrue(snapshotBody.contains("\"jsonrpc\":\"2.0\""))
        assertTrue(snapshotBody.contains("\"clientId\":\"android-ui-test\""))

        val pollRequest: RecordedRequest = server.takeRequest()
        assertEquals("/v1/ui/rpc", pollRequest.path)
        assertEquals(null, pollRequest.getHeader("Mcp-Session-Id"))
        val pollBody = pollRequest.body.readUtf8()
        assertTrue(pollBody.contains("\"jsonrpc\":\"2.0\""))
        assertTrue(pollBody.contains("\"clientId\":\"android-ui-test\""))

        val responseRequest: RecordedRequest = server.takeRequest()
        assertEquals("/v1/ui/rpc", responseRequest.path)
        assertEquals(null, responseRequest.getHeader("Mcp-Session-Id"))
        val responseBody = responseRequest.body.readUtf8()
        assertTrue(responseBody.contains("\"jsonrpc\":\"2.0\""))
        assertTrue(responseBody.contains("\"id\":\"cmd-1\""))
    }

    @Test
    fun `ui bridge long poll does not block snapshot rpc`() = runBlocking {
        server.start()
        val pollEntered = CountDownLatch(1)
        val snapshotEntered = CountDownLatch(1)
        val releasePoll = CountDownLatch(1)
        val httpClient = OkHttpClient.Builder()
            .addInterceptor(Interceptor { chain ->
                val request = chain.request()
                val body = requestBodyText(request)
                when {
                    body.contains("\"method\":\"ui.hello\"") -> rpcTestResponse(
                        request = request,
                        body = """{"jsonrpc":"2.0","result":{"accepted":true}}""",
                        sessionId = "session-123"
                    )
                    body.contains("\"method\":\"ui.poll\"") -> {
                        pollEntered.countDown()
                        if (!snapshotEntered.await(2, TimeUnit.SECONDS)) {
                            throw AssertionError("snapshot RPC did not enter while poll was in flight")
                        }
                        releasePoll.await(2, TimeUnit.SECONDS)
                        rpcTestResponse(
                            request = request,
                            body = """{"jsonrpc":"2.0","result":{"commands":[]}}"""
                        )
                    }
                    body.contains("\"method\":\"ui.snapshot\"") -> {
                        snapshotEntered.countDown()
                        releasePoll.countDown()
                        rpcTestResponse(
                            request = request,
                            body = """{"jsonrpc":"2.0","result":{"ok":true}}"""
                        )
                    }
                    else -> rpcTestResponse(
                        request = request,
                        code = 400,
                        body = """{"jsonrpc":"2.0","error":{"code":400,"message":"unexpected method"}}"""
                    )
                }
            })
            .build()
        val client = AgentlyClient(
            endpoints = mapOf(
                "appAPI" to EndpointConfig(
                    baseUrl = server.url("/").toString().trimEnd('/'),
                    httpClient = httpClient
                )
            )
        )
        val bridge = UIBridgeRpcClient(client)

        bridge.hello("android-ui-test")

        val pollJob = async(start = CoroutineStart.UNDISPATCHED) { bridge.poll("android-ui-test", timeoutMs = 5_000) }
        assertTrue(pollEntered.await(1, TimeUnit.SECONDS))

        val snapshot = withTimeout(1000) {
            bridge.snapshot(
                clientId = "android-ui-test",
                data = buildJsonObject {
                    put("windows", buildJsonObject { })
                }
            )
        }

        assertEquals(true, snapshot?.get("ok")?.jsonPrimitive?.boolean)
        pollJob.await()
        Unit
    }

    @Test
    fun `trackConversation connects stream before live state and applies post cursor event`() = runBlocking {
        server.enqueue(
            MockResponse()
                .setHeader("Content-Type", "text/event-stream")
                .setBody(
                    """
                    data: {"type":"text_delta","conversationId":"conv-1","turnId":"turn-1","messageId":"assistant-1","assistantMessageId":"assistant-1","eventSeq":2,"content":" live","createdAt":"2026-06-05T10:00:01Z"}

                    """.trimIndent()
                )
        )
        server.enqueue(
            MockResponse().setBody(
                """
                {
                  "eventCursor": "2026-06-05T10:00:00Z",
                  "conversation": {
                    "conversationId": "conv-1",
                    "turns": [
                      {
                        "turnId": "turn-1",
                        "status": "running",
                        "assistant": {
                          "final": {
                            "messageId": "assistant-1",
                            "content": "Hello"
                          }
                        },
                        "execution": {
                          "pages": [
                            {
                              "pageId": "page-1",
                              "assistantMessageId": "assistant-1",
                              "turnId": "turn-1",
                              "sequence": 1,
                              "status": "running"
                            }
                          ]
                        }
                      }
                    ]
                  }
                }
                """.trimIndent()
            )
        )
        server.start()
        val client = client()

        val snapshots = client.trackConversation("conv-1").take(2).toList()

        val streamRequest = assertNotNull(server.takeRequest(1, TimeUnit.SECONDS))
        val liveStateRequest = assertNotNull(server.takeRequest(1, TimeUnit.SECONDS))
        assertTrue(streamRequest.path.orEmpty().startsWith("/v1/stream?"))
        assertEquals("/v1/conversations/conv-1/live-state?includeFeeds=true&includeModelCalls=true&includeToolCalls=true", liveStateRequest.path)
        assertEquals("Hello", snapshots[0].bufferedMessages.single { it.id == "assistant-1" }.content)
        assertEquals("Hello live", snapshots[1].bufferedMessages.single { it.id == "assistant-1" }.content)
    }

    @Test
    fun `trackConversation uses stream endpoint client`() = runBlocking {
        server.enqueue(
            MockResponse()
                .setHeader("Content-Type", "text/event-stream")
                .setBody("")
        )
        server.enqueue(
            MockResponse().setBody(
                """
                {
                  "eventCursor": "cursor-1",
                  "conversation": {
                    "conversationId": "conv-1",
                    "turns": []
                  }
                }
                """.trimIndent()
            )
        )
        server.start()
        val shortClient = OkHttpClient.Builder()
            .addInterceptor { chain ->
                chain.proceed(chain.request().newBuilder().header("X-Test-Transport", "short").build())
            }
            .build()
        val streamClient = OkHttpClient.Builder()
            .addInterceptor { chain ->
                chain.proceed(chain.request().newBuilder().header("X-Test-Transport", "stream").build())
            }
            .build()
        val client = AgentlyClient(
            endpoints = mapOf(
                "appAPI" to EndpointConfig(
                    baseUrl = server.url("/").toString().trimEnd('/'),
                    httpClient = shortClient,
                    streamHttpClient = streamClient
                )
            )
        )

        val snapshots = client.trackConversation("conv-1").take(1).toList()

        assertEquals("conv-1", snapshots.single().conversationId)
        val streamRequest = assertNotNull(server.takeRequest(1, TimeUnit.SECONDS))
        val liveStateRequest = assertNotNull(server.takeRequest(1, TimeUnit.SECONDS))
        assertTrue(streamRequest.path.orEmpty().startsWith("/v1/stream?"))
        assertEquals("stream", streamRequest.getHeader("X-Test-Transport"))
        assertEquals("/v1/conversations/conv-1/live-state?includeFeeds=true&includeModelCalls=true&includeToolCalls=true", liveStateRequest.path)
        assertEquals("short", liveStateRequest.getHeader("X-Test-Transport"))
    }

    @Test
    fun `trackConversation skips pre cursor event and applies later live event`() = runBlocking {
        server.enqueue(
            MockResponse()
                .setHeader("Content-Type", "text/event-stream")
                .setBody(
                    """
                    data: {"type":"text_delta","conversationId":"conv-1","turnId":"turn-1","messageId":"assistant-1","assistantMessageId":"assistant-1","eventSeq":1,"content":" stale","createdAt":"2026-06-05T10:00:00Z"}

                    data: {"type":"text_delta","conversationId":"conv-1","turnId":"turn-1","messageId":"assistant-1","assistantMessageId":"assistant-1","eventSeq":2,"content":" live","createdAt":"2026-06-05T10:00:01Z"}

                    """.trimIndent()
                )
        )
        server.enqueue(
            MockResponse().setBody(
                """
                {
                  "eventCursor": "2026-06-05T10:00:00Z",
                  "conversation": {
                    "conversationId": "conv-1",
                    "turns": [
                      {
                        "turnId": "turn-1",
                        "status": "running",
                        "assistant": {
                          "final": {
                            "messageId": "assistant-1",
                            "content": "Hello"
                          }
                        }
                      }
                    ]
                  }
                }
                """.trimIndent()
            )
        )
        server.start()
        val client = client()

        val snapshots = client.trackConversation("conv-1").take(3).toList()

        assertEquals("Hello", snapshots[0].bufferedMessages.single { it.id == "assistant-1" }.content)
        assertEquals("Hello", snapshots[1].bufferedMessages.single { it.id == "assistant-1" }.content)
        assertEquals("Hello live", snapshots[2].bufferedMessages.single { it.id == "assistant-1" }.content)
    }

    @Test
    fun `trackConversation buffers pre hydration SSE burst without dropping deltas`() = runBlocking {
        val eventCount = 150
        val sseBody = (1..eventCount).joinToString(separator = "\n\n", postfix = "\n\n") { index ->
            """data: {"type":"text_delta","conversationId":"conv-1","turnId":"turn-1","messageId":"assistant-1","assistantMessageId":"assistant-1","eventSeq":$index,"content":"x","createdAt":"2026-06-05T10:00:01Z"}"""
        }
        server.enqueue(
            MockResponse()
                .setHeader("Content-Type", "text/event-stream")
                .setBody(sseBody)
        )
        server.enqueue(
            MockResponse().setBody(
                """
                {
                  "eventCursor": "2026-06-05T10:00:00Z",
                  "conversation": {
                    "conversationId": "conv-1",
                    "turns": [
                      {
                        "turnId": "turn-1",
                        "status": "running",
                        "assistant": {
                          "final": {
                            "messageId": "assistant-1",
                            "content": "Hello"
                          }
                        }
                      }
                    ]
                  }
                }
                """.trimIndent()
            )
        )
        server.start()
        val client = client()

        val snapshots = client.trackConversation("conv-1").take(eventCount + 1).toList()

        assertEquals(eventCount + 1, snapshots.size)
        assertEquals(
            "Hello" + "x".repeat(eventCount),
            snapshots.last().bufferedMessages.single { it.id == "assistant-1" }.content
        )
    }

    @Test
    fun `trackConversation rehydrates after stream overflow terminal event`() = runBlocking {
        server.enqueue(
            MockResponse()
                .setHeader("Content-Type", "text/event-stream")
                .setBody(
                    """
                    data: {"type":"text_delta","conversationId":"conv-1","turnId":"turn-1","messageId":"assistant-1","assistantMessageId":"assistant-1","eventSeq":1,"content":" live","createdAt":"2026-06-05T10:00:01Z"}

                    data: {"type":"stream_overflow","conversationId":"conv-1","turnId":"turn-1","eventSeq":1,"status":"overflow","createdAt":"2026-06-05T10:00:02Z"}

                    """.trimIndent()
                )
        )
        server.enqueue(
            MockResponse().setBody(
                """
                {
                  "eventCursor": "2026-06-05T10:00:00Z",
                  "conversation": {
                    "conversationId": "conv-1",
                    "turns": [
                      {
                        "turnId": "turn-1",
                        "status": "running",
                        "assistant": {
                          "final": {
                            "messageId": "assistant-1",
                            "content": "Hello"
                          }
                        }
                      }
                    ]
                  }
                }
                """.trimIndent()
            )
        )
        server.enqueue(
            MockResponse()
                .setHeader("Content-Type", "text/event-stream")
                .setBody("")
        )
        server.enqueue(
            MockResponse().setBody(
                """
                {
                  "eventCursor": "2026-06-05T10:00:03Z",
                  "conversation": {
                    "conversationId": "conv-1",
                    "turns": [
                      {
                        "turnId": "turn-1",
                        "status": "running",
                        "assistant": {
                          "final": {
                            "messageId": "assistant-1",
                            "content": "Recovered"
                          }
                        }
                      }
                    ]
                  }
                }
                """.trimIndent()
            )
        )
        server.start()
        val client = client()

        val snapshots = client.trackConversation("conv-1").take(3).toList()

        assertEquals("Hello", snapshots[0].bufferedMessages.single { it.id == "assistant-1" }.content)
        assertEquals("Hello live", snapshots[1].bufferedMessages.single { it.id == "assistant-1" }.content)
        assertEquals("Recovered", snapshots[2].bufferedMessages.single { it.id == "assistant-1" }.content)
        assertTrue(server.takeRequest().path.orEmpty().startsWith("/v1/stream?"))
        assertEquals("/v1/conversations/conv-1/live-state?includeFeeds=true&includeModelCalls=true&includeToolCalls=true", server.takeRequest().path)
        assertTrue(server.takeRequest().path.orEmpty().startsWith("/v1/stream?"))
        assertEquals("/v1/conversations/conv-1/live-state?includeFeeds=true&includeModelCalls=true&includeToolCalls=true", server.takeRequest().path)
    }

    @Test
    fun `listPendingToolApprovals accepts bare array response`() = runBlocking {
        server.enqueue(
            MockResponse().setBody(
                """
                [
                  {
                    "id": "approval-1",
                    "conversationId": "conv-1",
                    "toolName": "shell.exec",
                    "status": "pending"
                  }
                ]
                """.trimIndent()
            )
        )
        server.start()
        val client = client()

        val result = client.listPendingToolApprovals(
            ListPendingToolApprovalsInput(conversationId = "conv-1")
        )

        assertEquals(1, result.size)
        assertEquals("approval-1", result.first().id)
        assertEquals("shell.exec", result.first().toolName)
        assertTrue(server.takeRequest().path!!.contains("/v1/tool-approvals/pending"))
    }

    @Test
    fun `listPendingToolApprovals accepts wrapped object response`() = runBlocking {
        server.enqueue(
            MockResponse().setBody(
                """
                {
                  "data": [
                    {
                      "id": "approval-2",
                      "conversationId": "conv-2",
                      "toolName": "browser.open",
                      "status": "pending"
                    }
                  ]
                }
                """.trimIndent()
            )
        )
        server.start()
        val client = client()

        val result = client.listPendingToolApprovals(
            ListPendingToolApprovalsInput(conversationId = "conv-2")
        )

        assertEquals(1, result.size)
        assertEquals("approval-2", result.first().id)
        assertEquals("browser.open", result.first().toolName)
    }

    @Test
    fun `listPendingToolApprovalsPage decodes rows and pagination metadata`() = runBlocking {
        server.enqueue(
            MockResponse().setBody(
                """
                {
                  "rows": [
                    {
                      "id": "approval-3",
                      "conversationId": "conv-3",
                      "toolName": "system.exec",
                      "status": "pending"
                    }
                  ],
                  "total": 11,
                  "offset": 5,
                  "limit": 5,
                  "hasMore": true
                }
                """.trimIndent()
            )
        )
        server.start()
        val client = client()

        val result = client.listPendingToolApprovalsPage(
            ListPendingToolApprovalsInput(conversationId = "conv-3", status = "pending", limit = 5, offset = 5)
        )

        assertEquals(1, result.rows.size)
        assertEquals("approval-3", result.rows.first().id)
        assertEquals(11, result.total)
        assertEquals(5, result.offset)
        assertEquals(5, result.limit)
        assertEquals(true, result.hasMore)
        val path = server.takeRequest().path.orEmpty()
        assertTrue(path.contains("conversationId=conv-3"))
        assertTrue(path.contains("status=pending"))
        assertTrue(path.contains("limit=5"))
        assertTrue(path.contains("offset=5"))
    }

    @Test
    fun `conversationStateResponse decodes canonical execution fields`() {
        val json = Json { ignoreUnknownKeys = true }
        val payload = """
            {
              "schemaVersion": "2026-05-06",
              "eventCursor": "cursor-7",
              "usage": {
                "totalInputTokens": 120,
                "totalOutputTokens": 45
              },
              "conversation": {
                "conversationId": "conv-1",
                "turns": [
                  {
                    "turnId": "turn-1",
                    "status": "running",
                    "users": [
                      { "messageId": "user-1", "content": "hello" }
                    ],
                    "messages": [
                      { "messageId": "msg-1", "role": "user", "content": "hello", "sequence": 1, "status": "completed" }
                    ],
                    "assistant": {
                      "narration": { "messageId": "narr-1", "content": "thinking", "createdAt": "2026-05-06T10:00:00Z" },
                      "final": { "messageId": "final-1", "content": "done", "createdAt": "2026-05-06T10:00:02Z" },
                      "messages": [
                        { "messageId": "narr-1", "content": "thinking", "createdAt": "2026-05-06T10:00:00Z" },
                        { "messageId": "final-1", "content": "done", "createdAt": "2026-05-06T10:00:02Z" }
                      ]
                    },
                    "execution": {
                      "pages": [
                        {
                          "pageId": "page-1",
                          "assistantMessageId": "final-1",
                          "parentMessageId": "user-1",
                          "turnId": "turn-1",
                          "iteration": 1,
                          "sequence": 2,
                          "executionRole": "main",
                          "phase": "intake",
                          "status": "running",
                          "finalResponse": false,
                          "modelSteps": [
                            {
                              "modelCallId": "mc-1",
                              "assistantMessageId": "final-1",
                              "executionRole": "main",
                              "phase": "intake",
                              "provider": "openai",
                              "model": "gpt-5.4",
                              "usage": {"inputTokens":120,"outputTokens":30,"cachedInputTokens":40,"reasoningTokens":12,"totalTokens":150}
                            }
                          ],
                          "toolSteps": [
                            {
                              "toolCallId": "tc-1",
                              "toolMessageId": "tm-1",
                              "parentMessageId": "user-1",
                              "toolName": "system/exec:start",
                              "content": "running",
                              "executionRole": "sidecar",
                              "operationId": "op-1",
                              "status": "waiting",
                              "asyncOperation": {
                                "operationId": "op-1",
                                "status": "running",
                                "message": "still running"
                              }
                            }
                          ]
                        }
                      ]
                    }
                  }
                ]
              }
            }
        """.trimIndent()

        val decoded = json.decodeFromString(ConversationStateResponse.serializer(), payload)

        assertEquals("2026-05-06", decoded.schemaVersion)
        assertEquals("cursor-7", decoded.eventCursor)
        assertEquals(120, decoded.usage?.totalInputTokens)
        val turn = decoded.conversation?.turns?.first()
        assertEquals("turn-1", turn?.turnId)
        assertEquals("user-1", turn?.users?.first()?.messageId)
        assertEquals("msg-1", turn?.messages?.first()?.messageId)
        assertEquals("final-1", turn?.assistant?.messages?.last()?.messageId)
        val page = turn?.execution?.pages?.first()
        assertEquals(2, page?.sequence)
        assertEquals("main", page?.executionRole)
        assertEquals("intake", page?.phase)
        assertEquals("main", page?.modelSteps?.first()?.executionRole)
        assertEquals(150, page?.modelSteps?.first()?.usage?.totalTokens)
        assertEquals(40, page?.modelSteps?.first()?.usage?.cachedInputTokens)
        val toolStep = page?.toolSteps?.first()
        assertEquals("user-1", toolStep?.parentMessageId)
        assertEquals("sidecar", toolStep?.executionRole)
        assertEquals("op-1", toolStep?.operationId)
        assertEquals("running", toolStep?.asyncOperation?.status)
    }

    @Test
    fun `approval callback result decodes canonical callback contract`() {
        val json = Json { ignoreUnknownKeys = true }
        val payload = """
            {
              "allow": true,
              "message": "approved",
              "payload": {
                "action": "approve"
              }
            }
        """.trimIndent()

        val decoded = json.decodeFromString(ApprovalCallbackResult.serializer(), payload)

        assertEquals(true, decoded.allow)
        assertEquals("approved", decoded.message)
        assertEquals("approve", decoded.payload["action"]?.jsonPrimitive?.content)
    }

    @Test
    fun `approval callback result decodes current edited fields and action contract`() {
        val json = Json { ignoreUnknownKeys = true }
        val payload = """
            {
              "editedFields": {
                "names": ["prod"]
              },
              "action": "decline"
            }
        """.trimIndent()

        val decoded = json.decodeFromString(ApprovalCallbackResult.serializer(), payload)

        assertEquals("decline", decoded.action)
        assertEquals("prod", decoded.editedFields?.jsonObject?.get("names")?.jsonArray?.first()?.jsonPrimitive?.content)
        assertEquals(emptyMap<String, JsonElement>(), decoded.payload)
    }

    @Test
    fun `approval callback payload decodes action contract`() {
        val json = Json { ignoreUnknownKeys = true }
        val payload = """
            {
              "action": "approve",
              "editedFields": {
                "names": ["prod"]
              },
              "originalArgs": {
                "names": ["dev", "prod"]
              }
            }
        """.trimIndent()

        val decoded = json.decodeFromString(ApprovalCallbackPayload.serializer(), payload)

        assertEquals("approve", decoded.action)
        assertEquals("prod", decoded.editedFields?.jsonObject?.get("names")?.jsonArray?.first()?.jsonPrimitive?.content)
        assertEquals("dev", decoded.originalArgs?.jsonObject?.get("names")?.jsonArray?.first()?.jsonPrimitive?.content)
    }

    @Test
    fun `uploadFile sends multipart form data`() = runBlocking {
        server.enqueue(
            MockResponse().setBody(
                """
                {
                  "ID": "payload-1",
                  "URI": "conversation://conv-1/file-1"
                }
                """.trimIndent()
            )
        )
        server.start()
        val client = client()

        val result = client.uploadFile(
            UploadFileInput(
                conversationId = "conv-1",
                name = "fixture.txt",
                contentType = "text/plain",
                data = "hello from test".encodeToByteArray()
            )
        )

        assertEquals("payload-1", result.id)
        assertEquals("conversation://conv-1/file-1", result.uri)
        val request = server.takeRequest()
        assertEquals("/v1/files", request.path)
        assertEquals("POST", request.method)
        val body = request.body.readUtf8()
        assertNotNull(request.getHeader("Content-Type"))
        assertTrue(request.getHeader("Content-Type")!!.contains("multipart/form-data"))
        assertTrue(body.contains("name=\"conversationId\""))
        assertTrue(body.contains("conv-1"))
        assertTrue(body.contains("name=\"file\""))
        assertTrue(body.contains("fixture.txt"))
        assertTrue(body.contains("hello from test"))
    }

    @Test
    fun `createConversation decodes PascalCase fields`() = runBlocking {
        server.enqueue(
            MockResponse().setBody(
                """
                {
                  "Id": "conv-1",
                  "AgentId": "coder",
                  "Title": "Android QA",
                  "ConversationParentId": "parent-1",
                  "ConversationParentTurnId": "turn-9",
                  "Shareable": 1
                }
                """.trimIndent()
            )
        )
        server.start()
        val client = client()

        val result = client.createConversation(
            CreateConversationInput(
                agentId = "coder",
                title = "Android QA",
                parentConversationId = "parent-1",
                parentTurnId = "turn-9"
            )
        )

        assertEquals("conv-1", result.id)
        assertEquals("coder", result.agentId)
        assertEquals("Android QA", result.title)
        assertEquals("parent-1", result.conversationParentId)
        assertEquals("turn-9", result.conversationParentTurnId)
        assertEquals(1, result.shareable)
        val request = server.takeRequest()
        assertEquals("/v1/conversations", request.path)
        assertEquals("POST", request.method)
        assertTrue(request.body.readUtf8().contains("\"parentConversationId\":\"parent-1\""))
    }

    @Test
    fun `downloadFile returns body content type and inferred filename`() = runBlocking {
        server.enqueue(
            MockResponse()
                .setHeader("Content-Type", "text/plain")
                .setHeader("Content-Disposition", "attachment; filename=\"artifact.txt\"")
                .setBody("downloaded artifact")
        )
        server.start()
        val client = client()

        val result = client.downloadFile("conv-1", "file-9")

        assertEquals("artifact.txt", result.name)
        assertEquals("text/plain", result.contentType)
        assertEquals("downloaded artifact", result.data.toString(Charsets.UTF_8))
        val request = server.takeRequest()
        assertTrue(request.path!!.contains("/v1/files/file-9"))
        assertTrue(request.path!!.contains("conversationId=conv-1"))
        assertTrue(request.path!!.contains("raw=1"))
    }

    @Test
    fun `oauthInitiate decodes configured and error fields`() = runBlocking {
        server.enqueue(
            MockResponse().setBody(
                """
                {
                  "status": "error",
                  "message": "oauth client not configured",
                  "provider": "google"
                }
                """.trimIndent()
            )
        )
        server.start()
        val client = client()

        val result = client.oauthInitiate()

        assertEquals("error", result.status)
        assertEquals("oauth client not configured", result.message)
        assertEquals("google", result.provider)
        val request = server.takeRequest()
        assertEquals("/v1/api/auth/oauth/initiate", request.path)
        assertEquals("POST", request.method)
    }

    @Test
    fun `oauthInitiate posts redirect uri when provided`() = runBlocking {
        server.enqueue(
            MockResponse().setBody(
                """
                {
                  "authURL": "https://idp.example.test/authorize",
                  "redirectURI": "agently-ios://oauth/callback"
                }
                """.trimIndent()
            )
        )
        server.start()
        val client = client()

        val result = client.oauthInitiate(
            OAuthInitiateInput(
                redirectURI = "agently-ios://oauth/callback",
                scopes = listOf("XXX_WEBUI")
            )
        )

        assertEquals("agently-ios://oauth/callback", result.redirectURI)
        val request = server.takeRequest()
        assertEquals("/v1/api/auth/oauth/initiate", request.path)
        assertEquals("POST", request.method)
        val body = request.body.readUtf8()
        assertTrue(body.contains(""""redirectURI":"agently-ios://oauth/callback""""))
        assertTrue(body.contains(""""scopes":["XXX_WEBUI"]"""))
    }

    @Test
    fun `MCP OAuth status and initiate preserve session transport and CSRF`() = runBlocking {
        server.enqueue(MockResponse().setBody("""{"server":"media/planner","connected":false,"csrfToken":"csrf-1"}"""))
        server.enqueue(MockResponse().setBody("""{"status":"connect","authorizationURL":"https://idp.example.test/authorize"}"""))
        server.start()
        val client = client()

        val status = client.getMCPAuthStatus("media/planner")
        assertEquals("csrf-1", status.csrfToken)
        val initiated = client.initiateMCPAuth("media/planner", status.csrfToken!!, "/conversation/one")
        assertEquals("connect", initiated.status)

        assertEquals("/v1/api/auth/mcp/media%2Fplanner/status", server.takeRequest().path)
        val initiateRequest = server.takeRequest()
        assertEquals("/v1/api/auth/mcp/media%2Fplanner/initiate?returnURL=%2Fconversation%2Fone", initiateRequest.path)
        assertEquals("csrf-1", initiateRequest.getHeader("X-Agently-Csrf"))
    }

    @Test
    fun `oauthMobileInitiate posts mobile endpoint`() = runBlocking {
        server.enqueue(
            MockResponse().setBody(
                """
                {
                  "authURL": "https://idp.example.test/authorize",
                  "redirectURI": "agently-android://oauth/callback",
                  "pkce": true,
                  "mobile": true
                }
                """.trimIndent()
            )
        )
        server.start()
        val client = client()

        val result = client.oauthMobileInitiate(
            OAuthInitiateInput(
                redirectURI = "agently-android://oauth/callback",
                scopes = listOf("XXX_MOBILEUI")
            )
        )

        assertEquals("agently-android://oauth/callback", result.redirectURI)
        val request = server.takeRequest()
        assertEquals("/v1/api/auth/oauth/mobile/initiate", request.path)
        assertEquals("POST", request.method)
        val body = request.body.readUtf8()
        assertTrue(body.contains(""""redirectURI":"agently-android://oauth/callback""""))
        assertTrue(body.contains(""""scopes":["XXX_MOBILEUI"]"""))
    }

    @Test
    fun `oauthMobileCallback posts mobile endpoint`() = runBlocking {
        server.enqueue(
            MockResponse().setBody(
                """
                {
                  "status": "ok",
                  "sessionId": "session-1"
                }
                """.trimIndent()
            )
        )
        server.start()
        val client = client()

        val result = client.oauthMobileCallback(
            OAuthCallbackInput(code = "code-1", state = "state-1")
        )

        assertEquals("session-1", result.sessionId)
        val request = server.takeRequest()
        assertEquals("/v1/api/auth/oauth/mobile/callback", request.path)
        assertEquals("POST", request.method)
        val body = request.body.readUtf8()
        assertTrue(body.contains(""""code":"code-1""""))
        assertTrue(body.contains(""""state":"state-1""""))
    }

    @Test
    fun `attachAuthSession posts attach endpoint`() = runBlocking {
        server.enqueue(
            MockResponse().setBody(
                """
                {
                  "status": "ok",
                  "sessionId": "session-1",
                  "username": "test-user"
                }
                """.trimIndent()
            )
        )
        server.start()
        val client = client()

        val result = client.attachAuthSession(AttachSessionInput(sessionId = "session-1"))

        assertEquals("ok", result.status)
        assertEquals("session-1", result.sessionId)
        assertEquals("test-user", result.username)
        val request = server.takeRequest()
        assertEquals("/v1/api/auth/session/attach", request.path)
        assertEquals("POST", request.method)
        val body = request.body.readUtf8()
        assertTrue(body.contains(""""sessionId":"session-1""""))
    }

    @Test
    fun `getOAuthConfig decodes null scopes as empty`() = runBlocking {
        server.enqueue(
            MockResponse().setBody(
                """
                {
                  "clientID": "",
                  "configURL": "workspace-oauth-config",
                  "discoveryURL": "",
                  "mode": "bff",
                  "redirectSameTab": true,
                  "redirectURI": "",
                  "scopes": null,
                  "usePopupLogin": false
                }
                """.trimIndent()
            )
        )
        server.start()
        val client = client()

        val result = client.getOAuthConfig()

        assertEquals(emptyList(), result.scopes)
        val request = server.takeRequest()
        assertEquals("/v1/api/auth/oauth/config", request.path)
        assertEquals("GET", request.method)
    }

    @Test
    fun `templates skills and mediated mcp ui routes match shared client contract`() = runBlocking {
        server.enqueue(MockResponse().setBody("""{"items":[{"name":"brief","description":"Summary","format":"markdown"}]}"""))
        server.enqueue(MockResponse().setBody("""{"name":"brief","format":"markdown","description":"Summary","instructions":"Use bullets","includedDocument":true}"""))
        server.enqueue(MockResponse().setBody("""{"items":[{"name":"playwright-cli","description":"Automate browser"}],"diagnostics":["ok"]}"""))
        server.enqueue(MockResponse().setBody("""{"name":"playwright-cli","body":"Loaded skill"}"""))
        server.enqueue(MockResponse().setBody("""{"items":["shadowed demo"]}"""))
        server.enqueue(
            MockResponse().setBody(
                """
                {
                  "conversationId":"conv-1",
                  "turnId":"turn-1",
                  "status":"queued",
                  "result":"",
                  "source":"approval",
                  "approval":{"id":"approval-1","toolName":"system.exec","status":"pending","createdAt":"2026-06-03T12:00:00Z","userId":"","conversationId":"conv-1"}
                }
                """.trimIndent()
            )
        )
        server.start()
        val client = client()

        val templates = client.listTemplates()
        val template = client.getTemplate(GetTemplateInput(name = "brief", includeDocument = true))
        val skills = client.listSkills(ListSkillsInput(conversationId = "conv-1"))
        val skill = client.activateSkill(ActivateSkillInput(conversationId = "conv-1", name = "playwright-cli", args = "https://example.com"))
        val diagnostics = client.getSkillDiagnostics()
        val toolCall = client.executeMCPUIToolCall(
            MCPUIToolCallInput(
                conversationId = "conv-1",
                toolName = "system.exec",
                arguments = mapOf("cmd" to kotlinx.serialization.json.JsonPrimitive("pwd")),
                assistantText = "Running tool",
                toolBundles = listOf("system/exec")
            )
        )

        assertEquals("brief", templates.items.first().name)
        assertEquals("brief", template.name)
        assertEquals("playwright-cli", skills.items.first().name)
        assertEquals("Loaded skill", skill.body)
        assertEquals("shadowed demo", diagnostics.items.first())
        assertEquals("queued", toolCall.status)
        assertEquals("approval-1", toolCall.approval?.id)

        val r1 = server.takeRequest()
        assertEquals("/v1/templates", r1.path)
        assertEquals("GET", r1.method)

        val r2 = server.takeRequest()
        assertEquals("/v1/templates/brief?includeDocument=true", r2.path)
        assertEquals("GET", r2.method)

        val r3 = server.takeRequest()
        assertEquals("/v1/skills?conversationId=conv-1", r3.path)
        assertEquals("GET", r3.method)

        val r4 = server.takeRequest()
        assertEquals("/v1/skills/playwright-cli/activate?conversationId=conv-1", r4.path)
        assertEquals("POST", r4.method)
        assertEquals("""{"args":"https://example.com"}""", r4.body.readUtf8())

        val r5 = server.takeRequest()
        assertEquals("/v1/skills/diagnostics", r5.path)
        assertEquals("GET", r5.method)

        val r6 = server.takeRequest()
        assertEquals("/v1/api/mcp-ui/tools/call", r6.path)
        assertEquals("POST", r6.method)
        assertTrue(r6.body.readUtf8().contains("\"toolName\":\"system.exec\""))
    }

    @Test
    fun `listUIEvents executes scoped ui events tool and decodes structured details`() = runBlocking {
        server.enqueue(
            MockResponse().setBody(
                """
                {
                  "result": "{\"conversationId\":\"conv-ui-1\",\"clientId\":\"mobile-client-1\",\"events\":[{\"seq\":7,\"kind\":\"error\",\"actor\":\"agent\",\"detail\":{\"payload\":{\"invalidWorkspaceId\":\"legacyAlias\",\"availableWorkspaceIds\":[\"orders\"]}}}]}"
                }
                """.trimIndent()
            )
        )
        server.start()
        val client = client()

        val result = client.listUIEvents(
            ListUIEventsInput(
                conversationId = "conv-ui-1",
                clientId = "mobile-client-1",
                kinds = listOf("error"),
                limit = 20
            )
        )

        assertEquals("conv-ui-1", result.conversationId)
        assertEquals("mobile-client-1", result.clientId)
        val event = result.events.single()
        assertEquals(7, event.seq)
        assertEquals("error", event.kind)
        val payload = event.detail?.get("payload")?.jsonObject
        assertEquals("legacyAlias", payload?.get("invalidWorkspaceId")?.jsonPrimitive?.content)
        assertEquals("orders", payload?.get("availableWorkspaceIds")?.jsonArray?.first()?.jsonPrimitive?.content)

        val request = server.takeRequest()
        assertEquals("/v1/tools/ui%2Fevents%3Alist/execute?conversationId=conv-ui-1", request.path)
        assertEquals(
            """{"clientId":"mobile-client-1","kinds":["error"],"limit":20}""",
            request.body.readUtf8()
        )
    }

    @Test
    fun `feed draft facade wraps scoped get and update tools`() = runBlocking {
        server.enqueue(MockResponse().setBody("{\"result\":\"{\\\"clientId\\\":\\\"android-ui\\\",\\\"data\\\":{\\\"dataSources\\\":{}}}\"}"))
        server.enqueue(MockResponse().setBody("{\"result\":\"{\\\"clientId\\\":\\\"android-ui\\\",\\\"ok\\\":true}\"}"))
        server.start()
        val client = client()

        val read = client.getFeedDraft(GetFeedDraftInput("conv-1", "plan", listOf("draft")))
        val update = client.updateFeedDraft(UpdateFeedDraftInput(
            conversationId = "conv-1",
            feedId = "plan",
            operations = listOf(FeedPatchOperation("draft", "remove", "/channels/1"))
        ))

        assertEquals("android-ui", read.clientId)
        assertTrue(update.ok)
        val readRequest = server.takeRequest()
        assertEquals("/v1/tools/ui%2Ffeed%3Aget/execute?conversationId=conv-1", readRequest.path)
        assertTrue(readRequest.body.readUtf8().contains("\"dataSourceRefs\":[\"draft\"]"))
        val updateRequest = server.takeRequest()
        assertEquals("/v1/tools/ui%2Ffeed%3Aupdate/execute?conversationId=conv-1", updateRequest.path)
        assertTrue(updateRequest.body.readUtf8().contains("\"op\":\"remove\""))
    }

    @Test
    fun `template and skill transports encode slash-bearing path segments`() = runBlocking {
        server.enqueue(MockResponse().setBody("""{"name":"templates/brief","includedDocument":true}"""))
        server.enqueue(MockResponse().setBody("""{"name":"skills/playwright-cli","body":"Loaded skill"}"""))
        server.start()
        val client = client()

        client.getTemplate(GetTemplateInput(name = "templates/brief", includeDocument = true))
        client.activateSkill(ActivateSkillInput(conversationId = "conv-1", name = "skills/playwright-cli", args = "args"))

        val r1 = server.takeRequest()
        assertEquals("/v1/templates/templates%2Fbrief?includeDocument=true", r1.path)
        assertEquals("GET", r1.method)
        val r2 = server.takeRequest()
        assertEquals("/v1/skills/skills%2Fplaywright-cli/activate?conversationId=conv-1", r2.path)
        assertEquals("POST", r2.method)
        assertEquals("""{"args":"args"}""", r2.body.readUtf8())
    }

    @Test
    fun `turn and conversation control routes match shared client contract`() = runBlocking {
        server.enqueue(MockResponse().setBody("""{"cancelled":true}"""))
        server.enqueue(MockResponse().setBody("""{"messageId":"msg-1","turnId":"turn-1","status":"accepted"}"""))
        server.enqueue(MockResponse().setBody("""{}"""))
        server.enqueue(MockResponse().setBody("""{}"""))
        server.enqueue(MockResponse().setBody("""{}"""))
        server.enqueue(MockResponse().setBody("""{"messageId":"msg-2","turnId":"turn-queued","status":"accepted"}"""))
        server.enqueue(MockResponse().setBody("""{}"""))
        server.enqueue(MockResponse().setBody("""{}"""))
        server.enqueue(MockResponse().setBody("""{}"""))
        server.start()
        val client = client()

        val cancelled = client.cancelTurn("turn-1")
        val steer = client.steerTurn(
            SteerTurnInput(
                conversationId = "conv-1",
                turnId = "turn-1",
                content = "follow up",
                role = "user"
            )
        )
        client.cancelQueuedTurn("conv-1", "turn-queued")
        client.moveQueuedTurn(MoveQueuedTurnInput("conv-1", "turn-queued", "up"))
        client.editQueuedTurn(EditQueuedTurnInput("conv-1", "turn-queued", "edited"))
        val forced = client.forceSteerQueuedTurn("conv-1", "turn-queued")
        client.terminateConversation("conv-1")
        client.compactConversation("conv-1")
        client.pruneConversation("conv-1")

        assertEquals(true, cancelled)
        assertEquals("msg-1", steer.messageId)
        assertEquals("accepted", steer.status)
        assertEquals("msg-2", forced.messageId)

        val r1 = server.takeRequest()
        assertEquals("/v1/turns/turn-1/cancel", r1.path)
        assertEquals("POST", r1.method)

        val r2 = server.takeRequest()
        assertEquals("/v1/conversations/conv-1/turns/turn-1/steer", r2.path)
        assertEquals("POST", r2.method)
        assertTrue(r2.body.readUtf8().contains("\"content\":\"follow up\""))

        val r3 = server.takeRequest()
        assertEquals("/v1/conversations/conv-1/turns/turn-queued", r3.path)
        assertEquals("DELETE", r3.method)

        val r4 = server.takeRequest()
        assertEquals("/v1/conversations/conv-1/turns/turn-queued/move", r4.path)
        assertEquals("POST", r4.method)
        assertTrue(r4.body.readUtf8().contains("\"direction\":\"up\""))

        val r5 = server.takeRequest()
        assertEquals("/v1/conversations/conv-1/turns/turn-queued", r5.path)
        assertEquals("PATCH", r5.method)
        assertTrue(r5.body.readUtf8().contains("\"content\":\"edited\""))

        val r6 = server.takeRequest()
        assertEquals("/v1/conversations/conv-1/turns/turn-queued/force-steer", r6.path)
        assertEquals("POST", r6.method)

        val r7 = server.takeRequest()
        assertEquals("/v1/conversations/conv-1/terminate", r7.path)
        assertEquals("POST", r7.method)

        val r8 = server.takeRequest()
        assertEquals("/v1/conversations/conv-1/compact", r8.path)
        assertEquals("POST", r8.method)

        val r9 = server.takeRequest()
        assertEquals("/v1/conversations/conv-1/prune", r9.path)
        assertEquals("POST", r9.method)
    }

    @Test
    fun `getFeedData keeps top level feed metadata alongside data field`() = runBlocking {
        server.enqueue(
            MockResponse().setBody(
                """
                {
                  "feedId": "plan",
                  "title": "Plan",
                  "data": {
                    "output": {
                      "plan": [
                        { "status": "pending", "step": "Ship Android feed UI" }
                      ]
                    }
                  },
                  "dataSources": {
                    "planInfo": { "source": "output" }
                  },
                  "ui": {
                    "title": "Plan",
                    "containers": [
                      { "id": "header", "items": [{ "id": "explanation", "type": "label" }] }
                    ]
                  }
                }
                """.trimIndent()
            )
        )
        server.start()
        val client = client()

        val result = client.getFeedData("plan", "conv-1")

        assertEquals("plan", result.feedId)
        assertEquals("Plan", result.title)
        assertNotNull(result.data)
        assertNotNull(result.dataSources)
        assertNotNull(result.ui)
        val request = server.takeRequest()
        assertEquals("/v1/feeds/plan/data?conversationId=conv-1", request.path)
    }

    @Test
    fun `listConversations builds query parameters`() = runBlocking {
        server.enqueue(
            MockResponse().setBody(
                """
                {
                  "rows": [],
                  "hasMore": false
                }
                """.trimIndent()
            )
        )
        server.start()
        val client = client()

        client.listConversations(
            ListConversationsInput(
                agentId = "coder",
                parentId = "parent-1",
                parentTurnId = "turn-2",
                scheduleId = "schedule-3",
                excludeScheduled = true,
                query = "android qa",
                status = "active",
                page = PageInput(limit = 25, cursor = "cursor-1", direction = "next")
            )
        )

        val path = server.takeRequest().path.orEmpty()
        assertTrue(path.startsWith("/v1/conversations?"))
        assertTrue(path.contains("agentId=coder"))
        assertTrue(path.contains("parentId=parent-1"))
        assertTrue(path.contains("parentTurnId=turn-2"))
        assertTrue(path.contains("scheduleId=schedule-3"))
        assertTrue(path.contains("excludeScheduled=true"))
        assertTrue(path.contains("q=android+qa"))
        assertTrue(path.contains("status=active"))
        assertTrue(path.contains("limit=25"))
        assertTrue(path.contains("cursor=cursor-1"))
        assertTrue(path.contains("direction=next"))
    }

    @Test
    fun `listConversations decodes PascalCase page envelope`() = runBlocking {
        server.enqueue(
            MockResponse().setBody(
                """
                {
                  "Rows": [
                    {
                      "Id": "conv-9",
                      "AgentId": "chatter",
                      "Title": "Mobile QA",
                      "LastActivity": "2026-04-06T08:51:45.014809-07:00",
                      "CreatedAt": "2026-04-06T08:51:42.660325-07:00"
                    }
                  ],
                  "NextCursor": "conv-8",
                  "PrevCursor": "conv-10",
                  "HasMore": true
                }
                """.trimIndent()
            )
        )
        server.start()
        val client = client()

        val result = client.listConversations()

        assertEquals(1, result.rows.size)
        assertEquals("conv-9", result.rows.first().id)
        assertEquals("chatter", result.rows.first().agentId)
        assertEquals("Mobile QA", result.rows.first().title)
        assertEquals("conv-8", result.nextCursor)
        assertEquals("conv-10", result.prevCursor)
        assertTrue(result.hasMore)
    }

    @Test
    fun `listLinkedConversations decodes PascalCase page envelope`() = runBlocking {
        server.enqueue(
            MockResponse().setBody(
                """
                {
                  "Rows": [
                    {
                      "conversationId": "child-1",
                      "parentConversationId": "parent-1",
                      "title": "Linked child",
                      "status": "done"
                    }
                  ],
                  "NextCursor": "cursor-next",
                  "PrevCursor": "cursor-prev",
                  "HasMore": false
                }
                """.trimIndent()
            )
        )
        server.start()
        val client = client()

        val result = client.listLinkedConversations(
            ListLinkedConversationsInput(parentConversationId = "parent-1")
        )

        assertEquals(1, result.rows.size)
        assertEquals("child-1", result.rows.first().conversationId)
        assertEquals("parent-1", result.rows.first().parentConversationId)
        assertEquals("cursor-next", result.nextCursor)
        assertEquals("cursor-prev", result.prevCursor)
        assertEquals(false, result.hasMore)
    }

    @Test
    fun `getRun uses shared run route`() = runBlocking {
        server.enqueue(
            MockResponse().setBody(
                """
                {
                  "Id":"run-1",
                  "TurnId":"turn-1",
                  "ConversationId":"conv-1",
                  "Model":"gpt-5.5",
                  "ModelProvider":"openai",
                  "Status":"running",
                  "CreatedAt":"2026-06-03T12:00:00Z"
                }
                """.trimIndent()
            )
        )
        server.start()
        val client = client()

        val result = client.getRun("run-1")

        assertEquals("run-1", result.id)
        assertEquals("turn-1", result.turnId)
        assertEquals("conv-1", result.conversationId)
        assertEquals("gpt-5.5", result.model)
        assertEquals("openai", result.provider)
        assertEquals("running", result.status)
        assertEquals("/v1/runs/run-1", server.takeRequest().path)
    }

    @Test
    fun `query posts payload and decodes response`() = runBlocking {
        server.enqueue(
            MockResponse().setBody(
                """
                {
                  "conversationId": "conv-7",
                  "content": "Hello from backend",
                  "model": "gpt-5.4",
                  "messageId": "msg-7",
                  "warnings": ["warn-1"],
                  "projection": {
                    "scope": "conversation",
                    "hiddenTurnIds": ["turn-1"],
                    "hiddenMessageIds": ["msg-9"],
                    "reason": "tool call supersession",
                    "tokensFreed": 42
                  }
                }
                """.trimIndent()
            )
        )
        server.start()
        val client = client()

        val result = client.query(
            QueryInput(
                conversationId = "conv-7",
                agentId = "coder",
                query = "hello",
                context = mapOf("mode" to buildJsonObject { put("kind", "qa") })
            )
        )

        assertEquals("conv-7", result.conversationId)
        assertEquals("Hello from backend", result.content)
        assertEquals("gpt-5.4", result.model)
        assertEquals("msg-7", result.messageId)
        assertEquals(listOf("warn-1"), result.warnings)
        assertNotNull(result.projection)
        assertEquals(
            "conversation",
            result.projection!!.jsonObject["scope"]?.jsonPrimitive?.content
        )
        assertEquals(
            "tool call supersession",
            result.projection!!.jsonObject["reason"]?.jsonPrimitive?.content
        )
        val request = server.takeRequest()
        assertEquals("/v1/agent/query", request.path)
        assertEquals("POST", request.method)
        val body = request.body.readUtf8()
        assertTrue(body.contains("\"conversationId\":\"conv-7\""))
        assertTrue(body.contains("\"agentId\":\"coder\""))
        assertTrue(body.contains("\"query\":\"hello\""))
    }

    @Test
    fun `downloadGeneratedFile uses expected path and headers`() = runBlocking {
        server.enqueue(
            MockResponse()
                .setHeader("Content-Type", "application/json")
                .setHeader("Content-Disposition", "attachment; filename=\"artifact.json\"")
                .setBody("""{"ok":true}""")
        )
        server.start()
        val client = client()

        val result = client.downloadGeneratedFile("gen-1")

        assertEquals("artifact.json", result.name)
        assertEquals("application/json", result.contentType)
        assertEquals("""{"ok":true}""", result.data.toString(Charsets.UTF_8))
        val request = server.takeRequest()
        assertEquals("/v1/api/generated-files/gen-1/download", request.path)
        assertEquals("GET", request.method)
    }

    @Test
    fun `getPayloads uses batch endpoint with deduplicated ids`() = runBlocking {
        server.enqueue(
            MockResponse().setBody(
                """
                {
                  "p1": {"Id":"p1","Kind":"text","MimeType":"text/plain","SizeBytes":1,"Storage":"inline","Compression":"none"},
                  "p2": {"Id":"p2","Kind":"text","MimeType":"text/plain","SizeBytes":2,"Storage":"inline","Compression":"none"}
                }
                """.trimIndent()
            )
        )
        server.start()
        val client = client()

        val result = client.getPayloads(listOf("p1", "p2", "missing", "p1", ""))

        assertEquals(2, result.size)
        assertEquals("p1", result["p1"]?.id)
        assertEquals("p2", result["p2"]?.id)

        val request = server.takeRequest()
        assertEquals("/v1/api/payloads", request.path)
        assertEquals("POST", request.method)
        val body = Json.parseToJsonElement(request.body.readUtf8()).jsonObject
        val requestedIds = body["ids"]!!.jsonArray.map { it.jsonPrimitive.content }
        assertEquals(listOf("p1", "p2", "missing"), requestedIds)
        assertEquals(1, server.requestCount)
    }

    @Test
    fun `datasource and lookup extension routes encode slash-bearing identifiers`() = runBlocking {
        server.enqueue(MockResponse().setBody("""{"rows":[]}"""))
        server.enqueue(MockResponse().setBody("""{}"""))
        server.enqueue(MockResponse().setBody("""{"entries":[]}"""))
        server.start()
        val client = client()

        client.fetchDatasource(
            FetchDatasourceInput(
                id = " sources/main ",
                inputs = mapOf("q" to JsonPrimitive("acme")),
                cache = DatasourceCacheHints(bypassCache = true, writeThrough = true),
                conversationId = " conv-1 "
            )
        )
        client.invalidateDatasourceCache(InvalidateDatasourceCacheInput(id = " sources/main ", inputsHash = " hash/one "))
        client.listLookupRegistry(ListLookupRegistryInput(context = " dialog:main/form "))

        val fetch = server.takeRequest()
        assertEquals("/v1/api/datasources/sources%2Fmain/fetch", fetch.path)
        assertEquals("POST", fetch.method)
        val fetchBody = Json.parseToJsonElement(fetch.body.readUtf8()).jsonObject
        assertTrue(!fetchBody.containsKey("id"))
        assertEquals("conv-1", fetchBody["conversationId"]!!.jsonPrimitive.content)
        assertEquals("acme", fetchBody["inputs"]!!.jsonObject["q"]!!.jsonPrimitive.content)
        assertEquals(true, fetchBody["cache"]!!.jsonObject["bypassCache"]!!.jsonPrimitive.boolean)
        assertEquals(true, fetchBody["cache"]!!.jsonObject["writeThrough"]!!.jsonPrimitive.boolean)

        val invalidate = server.takeRequest()
        assertEquals("/v1/api/datasources/sources%2Fmain/cache?inputsHash=hash%2Fone", invalidate.path)
        assertEquals("DELETE", invalidate.method)

        val registry = server.takeRequest()
        assertEquals("/v1/api/lookups/registry?context=dialog%3Amain%2Fform", registry.path)
        assertEquals("GET", registry.method)
    }

    @Test
    fun `fetch datasource normalizes null rows to an empty collection`() = runBlocking {
        server.enqueue(MockResponse().setBody("""{"rows":null}"""))
        server.start()

        val result = client().fetchDatasource(FetchDatasourceInput(id = "empty"))

        assertEquals(emptyList(), result.rows)
        assertEquals("/v1/api/datasources/empty/fetch", server.takeRequest().path)
    }

    @Test
    fun `datasource and lookup extensions reject blank identifiers before dispatch`() = runBlocking {
        server.start()
        val client = client()

        assertFailsWith<IllegalArgumentException> {
            client.fetchDatasource(FetchDatasourceInput(id = "   "))
        }
        assertFailsWith<IllegalArgumentException> {
            client.invalidateDatasourceCache(InvalidateDatasourceCacheInput(id = "   "))
        }
        assertFailsWith<IllegalArgumentException> {
            client.listLookupRegistry(ListLookupRegistryInput(context = "   "))
        }
        assertEquals(0, server.requestCount)
    }

    private fun client(): AgentlyClient {
        return AgentlyClient(
            endpoints = mapOf(
                "appAPI" to EndpointConfig(baseUrl = server.url("/").toString().trimEnd('/'))
            )
        )
    }

    private fun requestBodyText(request: Request): String {
        val buffer = Buffer()
        request.body?.writeTo(buffer)
        return buffer.readUtf8()
    }

    private fun rpcTestResponse(
        request: Request,
        code: Int = 200,
        body: String,
        sessionId: String? = null
    ): Response {
        val mediaType = "application/json".toMediaType()
        return Response.Builder()
            .request(request)
            .protocol(Protocol.HTTP_1_1)
            .code(code)
            .message(if (code in 200..299) "OK" else "Error")
            .apply {
                sessionId?.let { header("Mcp-Session-Id", it) }
            }
            .body(body.toResponseBody(mediaType))
            .build()
    }
}
