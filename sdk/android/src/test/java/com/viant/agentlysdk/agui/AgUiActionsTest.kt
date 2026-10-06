package com.viant.agentlysdk.agui
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import kotlinx.coroutines.cancelAndJoin
import kotlinx.coroutines.flow.collect
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.flow.toList
import kotlinx.serialization.json.*
import kotlin.test.*
class AgUiActionsTest {
    private fun json(raw:String)=Json.parseToJsonElement(raw)
    private fun snapshot(outcome:String="""{"type":"success","pendingToolCallIds":["call"]}""",extraCall:Boolean=false):AgUiSnapshot {
        var fields=json("""{"id":"owner","role":"assistant","toolCalls":[{"id":"call","type":"function","function":{"name":"browser","arguments":"{\"n\":9007199254740993}"}}]}""").obj()
        if(extraCall)fields=fields.with("toolCalls" to JsonArray(fields.getValue("toolCalls").jsonArray+json("""{"id":"second","type":"function","function":{"name":"unstable","arguments":"{}"}}""")))
        val message=AgUiMessage(fields)
        val store=AgUiStore(AgUiRunInput.create("thread","run",listOf(message)))
        store.receive(AgUiEvent(json("""{"type":"RUN_STARTED","threadId":"thread","runId":"run"}""").obj()))
        store.receive(AgUiEvent(json("""{"type":"RUN_FINISHED","threadId":"thread","runId":"run","outcome":$outcome}""").obj()))
        return store.snapshot
    }
    @Test fun authorizedClientToolDispatchCachesMediaAndBuildsContinuation()=runBlocking<Unit> {
        val snapshot=snapshot();val dispatcher=AgUiClientToolDispatcher();var count=0
        val tool=AgUiClientTool(json("""{"name":"browser","description":"authorized","parameters":{"type":"object","properties":{"n":{"type":"integer"}},"required":["n"],"additionalProperties":false}}""").obj()){args,call->
            assertEquals("9007199254740993",args.obj()["n"]?.jsonPrimitive?.content);assertEquals("call",call.string("id"));count++
            AgUiClientToolResult(json("""[{"type":"text","text":"result"}]"""),metadata=json("""{"opaque":"kept"}""").obj())
        }
        val first=dispatcher.executeClientTools(snapshot,listOf(tool));val again=dispatcher.executeClientTools(snapshot,listOf(tool));assertEquals(first,again);assertEquals(1,count)
        val next=snapshot.toolResultsInput("continued",again);assertEquals("kept",next.value["messages"]!!.jsonArray.last().obj()["metadata"]!!.obj().string("opaque"));assertEquals(first[0].value["content"],next.value["messages"]!!.jsonArray.last().obj()["content"])
        assertFailsWith<AgUiProtocolException>{dispatcher.executeClientTools(snapshot,emptyList())}
    }
    @Test fun partialBatchRetryDoesNotRepeatSuccessfulHandler()=runBlocking<Unit>{
        val snapshot=snapshot("""{"type":"success","pendingToolCallIds":["call","second"]}""",extraCall=true);val dispatcher=AgUiClientToolDispatcher();var first=0;var second=0
        val toolA=AgUiClientTool(json("""{"name":"browser","description":"authorized","parameters":{"type":"object"}}""").obj()){_,_->first++;AgUiClientToolResult(JsonPrimitive("first"))}
        val toolB=AgUiClientTool(json("""{"name":"unstable","description":"authorized","parameters":{"type":"object"}}""").obj()){_,_->second++;if(second==1)throw IllegalStateException("fixture-handler");AgUiClientToolResult(JsonPrimitive("second"))}
        assertFailsWith<IllegalStateException>{dispatcher.executeClientTools(snapshot,listOf(toolA,toolB))}
        val results=dispatcher.executeClientTools(snapshot,listOf(toolA,toolB));assertEquals(1,first);assertEquals(2,second);assertEquals(2,results.size);assertEquals(3,snapshot.toolResultsInput("next",results).value["messages"]!!.jsonArray.size)
    }
    @Test fun resumeIdentitySchemaAndExplicitBackendCancellation(){
        val snapshot=snapshot("""{"type":"interrupt","interrupts":[{"id":"ask","reason":"input","responseSchema":{"type":"object","properties":{"answer":{"type":"string","enum":["yes"]}},"required":["answer"],"additionalProperties":false}}]}""")
        assertTrue(snapshot.pendingToolCallIds.isEmpty())
        val valid=json("""{"interruptId":"ask","status":"resolved","payload":{"answer":"yes"}}""")
        snapshot.nextInput("next",resume=JsonArray(listOf(valid)))
        assertFailsWith<AgUiProtocolException>{snapshot.nextInput("next",resume=JsonArray(listOf(valid,valid)))}
        assertFailsWith<AgUiProtocolException>{snapshot.nextInput("next",resume=json("""[{"interruptId":"other","status":"cancelled"}]""").jsonArray)}
        assertFailsWith<AgUiProtocolException>{snapshot.nextInput("next",resume=json("""[{"interruptId":"ask","status":"resolved","payload":{"answer":"no"}}]""").jsonArray)}
        assertFailsWith<AgUiProtocolException>{AgUiSchema.validateResponse(JsonPrimitive("tiny"),json("""{"type":"string","minLength":10}"""))}
        AgUiSchema.validateResponse(JsonPrimitive("yes"),json("""{"${'$'}defs":{"Choice":{"enum":["yes"]}},"${'$'}ref":"#/${'$'}defs/Choice"}"""))
        assertFailsWith<AgUiProtocolException>{AgUiSchema.validateResponse(JsonPrimitive("yes"),json("""{"anyOf":[{"${'$'}ref":"https://invalid.test/schema"},{"type":"string"}]}"""))}
        AgUiSchema.validateResponse(JsonPrimitive("🌍a"),json("""{"type":"string","minLength":2,"maxLength":2}"""))
        val command=AgentlyAgUiExtensions.cancelRun("target","cancel-command");assertEquals("run.cancel",command["agently"]!!.obj().string("operation"));assertEquals("target",command["agently"]!!.obj()["payload"]!!.obj().string("runId"))
        assertTrue(snapshot("""{"type":"cancelled"}""").pendingToolCallIds.isEmpty())
    }
    @Test fun versionedInterruptDispatchLeavesHumanApprovalUnanswered()=runBlocking<Unit> {
        val snapshot=snapshot("""{"type":"interrupt","interrupts":[{"id":"nested","reason":"agently.client_tool","toolCallId":"call","responseSchema":{"type":"object","properties":{"content":{"anyOf":[{"type":"string"},{"type":"array","items":{"type":"object"}}]},"error":{"type":"string"}},"required":["content"],"additionalProperties":false},"metadata":{"agently":{"version":"1","kind":"client-tool"}}},{"id":"approval","reason":"approval"}]}""")
        val dispatcher=AgUiClientToolDispatcher();var count=0
        val tool=AgUiClientTool(json("""{"name":"browser","description":"authorized","parameters":{"type":"object"}}""").obj()){_,_->count++;AgUiClientToolResult(JsonPrimitive("browser result"))}
        val answers=dispatcher.executeClientToolInterrupts(snapshot,listOf(tool));assertEquals(1,answers.size);assertEquals("nested",answers[0].obj().string("interruptId"))
        assertFailsWith<AgUiProtocolException>{snapshot.nextInput("next",resume=answers)}
        snapshot.nextInput("next",resume=JsonArray(answers+json("""{"interruptId":"approval","status":"cancelled"}""")))
        dispatcher.executeClientToolInterrupts(snapshot,listOf(tool));assertEquals(1,count)
    }
}

class AgUiActionsHttpTest {
    private fun frames(events:List<String>)=events.joinToString(""){"data: $it\n\n"}
    @Test fun clientToolHandlerResultRoundTripsOverActualHTTP()=runBlocking<Unit>{
        val server=okhttp3.mockwebserver.MockWebServer();server.start()
        try {
            val first=listOf("""{"type":"RUN_STARTED","threadId":"thread","runId":"run"}""","""{"type":"TEXT_MESSAGE_START","messageId":"owner","role":"assistant"}""","""{"type":"TEXT_MESSAGE_END","messageId":"owner"}""","""{"type":"TOOL_CALL_START","toolCallId":"call","toolCallName":"browser","parentMessageId":"owner"}""","""{"type":"TOOL_CALL_ARGS","toolCallId":"call","delta":"{\"n\":42}"}""","""{"type":"TOOL_CALL_END","toolCallId":"call"}""","""{"type":"RUN_FINISHED","threadId":"thread","runId":"run","outcome":{"type":"success","pendingToolCallIds":["call"]}}""")
            server.enqueue(okhttp3.mockwebserver.MockResponse().setHeader("Content-Type","text/event-stream").setBody(frames(first)).throttleBody(7,1,java.util.concurrent.TimeUnit.MILLISECONDS))
            server.enqueue(okhttp3.mockwebserver.MockResponse().setHeader("Content-Type","text/event-stream").setBody(frames(listOf("""{"type":"RUN_STARTED","threadId":"thread","runId":"continued"}""","""{"type":"RUN_FINISHED","threadId":"thread","runId":"continued"}"""))))
            val definition=Json.parseToJsonElement("""{"name":"browser","description":"authorized","parameters":{"type":"object","properties":{"n":{"type":"integer"}},"required":["n"]}}""").obj()
            val client=AgUiClient(com.viant.agentlysdk.EndpointConfig(server.url("/run").toString()))
            val snapshot=client.run(AgUiRunInput.create("thread","run",emptyList(),tools=JsonArray(listOf(definition)))).toList().last().snapshot
            val tool=AgUiClientTool(definition){args,_->assertEquals("42",args.obj()["n"]?.jsonPrimitive?.content);AgUiClientToolResult(Json.parseToJsonElement("""[{"type":"text","text":"héllo 🌍"}]"""),metadata=Json.parseToJsonElement("""{"opaque":"kept"}""").obj())}
            val results=AgUiClientToolDispatcher().executeClientTools(snapshot,listOf(tool));client.run(snapshot.toolResultsInput("continued",results)).toList()
            val firstRequest=server.takeRequest();val second=Json.parseToJsonElement(server.takeRequest().body.readUtf8()).obj();assertEquals("POST",firstRequest.method);assertNull(second["forwardedProps"]);assertEquals(definition,second["tools"]!!.jsonArray.first());assertEquals(results[0].value["content"],second["messages"]!!.jsonArray.last().obj()["content"]);assertEquals("kept",second["messages"]!!.jsonArray.last().obj()["metadata"]!!.obj().string("opaque"))
        }finally{server.shutdown()}
    }
}

class AgUiCancelHttpTest {
    @Test fun backendCancelIsIndependentPOSTWhileOriginalCallRemainsOpen()=runBlocking<Unit>{
        val server=okhttp3.mockwebserver.MockWebServer();server.start()
        val client=AgUiClient(com.viant.agentlysdk.EndpointConfig(server.url("/run").toString()))
        server.enqueue(okhttp3.mockwebserver.MockResponse().setSocketPolicy(okhttp3.mockwebserver.SocketPolicy.NO_RESPONSE))
        server.enqueue(okhttp3.mockwebserver.MockResponse().setHeader("Content-Type","text/event-stream").setBody("data: {\"type\":\"RUN_STARTED\",\"threadId\":\"thread\",\"runId\":\"cancel-command\"}\n\ndata: {\"type\":\"CUSTOM\",\"name\":\"agently.run.cancel\",\"value\":{\"version\":\"1\",\"cancelled\":true}}\n\ndata: {\"type\":\"RUN_FINISHED\",\"threadId\":\"thread\",\"runId\":\"cancel-command\"}\n\n"))
        val original=launch {client.run(AgUiRunInput.create("thread","target",emptyList())).collect{}}
        try {
            withContext(kotlinx.coroutines.Dispatchers.IO){assertNotNull(server.takeRequest(2,java.util.concurrent.TimeUnit.SECONDS))}
            client.cancelRun("thread","target","cancel-command").toList();assertTrue(original.isActive)
            val request=server.takeRequest();val posted=Json.parseToJsonElement(request.body.readUtf8()).obj();assertEquals("POST",request.method);assertEquals("/run",request.path);assertEquals("run.cancel",posted["forwardedProps"]!!.obj()["agently"]!!.obj().string("operation"));assertEquals("target",posted["forwardedProps"]!!.obj()["agently"]!!.obj()["payload"]!!.obj().string("runId"))
        }finally{original.cancelAndJoin();server.shutdown()}
    }
}

class AgUiLiveActionsTest {
    @Test fun liveHandlerContinuationAndIdenticalReplay()=runBlocking<Unit>{
        val address=System.getenv("AGENTLY_AGUI_LIVE_URL");org.junit.Assume.assumeTrue("Set AGENTLY_AGUI_LIVE_URL for actual native handler/continuation proof",!address.isNullOrEmpty())
        val cookie="agently_anonymous_user=anonymous:sdk-kotlin-"+java.util.UUID.randomUUID().toString()
        val client=AgUiClient(com.viant.agentlysdk.EndpointConfig(address!!,defaultHeadersProvider={mapOf("Cookie" to cookie)}));val definition=Json.parseToJsonElement("""{"name":"ui_lookup","description":"SDK authorized fixture","parameters":{"type":"object","properties":{"value":{"type":"string"}},"required":["value"]}}""").obj()
        val input=AgUiRunInput.create(java.util.UUID.randomUUID().toString(),java.util.UUID.randomUUID().toString(),listOf(AgUiMessage.user(java.util.UUID.randomUUID().toString(),JsonPrimitive("fixture-client-tool"))),tools=JsonArray(listOf(definition)))
        val updates=client.run(input).toList();assertTrue(updates.none{it.event.type=="RUN_ERROR"},updates.last().event.value.toString());val pending=updates.last().snapshot;assertEquals(1,pending.pendingToolCallIds.size)
        val tool=AgUiClientTool(definition){args,_->assertEquals("client fixture",args.obj().string("value"));AgUiClientToolResult(Json.parseToJsonElement("""[{"type":"text","text":"native SDK handled"}]"""),metadata=Json.parseToJsonElement("""{"sdk":"kotlin"}""").obj())}
        val results=AgUiClientToolDispatcher().executeClientTools(pending,listOf(tool));val next=pending.toolResultsInput(java.util.UUID.randomUUID().toString(),results)
        val continued=client.run(next).toList();assertTrue(continued.none{it.event.type=="RUN_ERROR"},continued.last().event.value.toString());val finished=continued.last().snapshot;assertEquals("success",finished.terminalEvent!!.value["outcome"]!!.obj().string("type"));assertTrue(finished.pendingToolCallIds.isEmpty())
        val replay=client.run(next).toList();assertTrue(replay.none{it.event.type=="RUN_ERROR"});assertEquals(finished.messages,replay.last().snapshot.messages);assertEquals(finished.state,replay.last().snapshot.state)
    }
}

class AgUiSessionHttpTest {
    @Test fun fallbackRetainsSessionCookieAndExplicitHeadersTakePrecedence()=runBlocking<Unit>{
        val server=okhttp3.mockwebserver.MockWebServer();server.start()
        val response="data: {\"type\":\"RUN_STARTED\",\"threadId\":\"thread\",\"runId\":\"run\"}\n\ndata: {\"type\":\"RUN_FINISHED\",\"threadId\":\"thread\",\"runId\":\"run\"}\n\n"
        try {
            repeat(3){server.enqueue(okhttp3.mockwebserver.MockResponse().setHeader("Content-Type","text/event-stream").setHeader("Set-Cookie","fixture_session=retained; Path=/; HttpOnly").setBody(response))}
            var explicit=false
            val client=AgUiClient(com.viant.agentlysdk.EndpointConfig(server.url("/run").toString(),defaultHeadersProvider={if(explicit)mapOf("Cookie" to "application_session=explicit") else emptyMap()}))
            val input=AgUiRunInput.create("thread","run",emptyList())
            client.run(input).toList();client.run(input).toList();explicit=true;client.run(input).toList()
            assertNull(server.takeRequest().getHeader("Cookie"));assertEquals("fixture_session=retained",server.takeRequest().getHeader("Cookie"));assertEquals("application_session=explicit",server.takeRequest().getHeader("Cookie"))
        }finally{server.shutdown()}
    }
}

class AgUiSecureCookiePolicyTest {
    @Test fun secureSessionJarRetainsAndScopesCookiesToHTTPS(){
        val jar=AgUiSessionCookies();val https=okhttp3.HttpUrl.Builder().scheme("https").host("fixture.invalid").addPathSegment("run").build();val http=https.newBuilder().scheme("http").build()
        val cookie=okhttp3.Cookie.parse(https,"fixture_secure=retained; Path=/; HttpOnly; Secure")!!
        jar.saveFromResponse(https,listOf(cookie));assertEquals(listOf(cookie),jar.loadForRequest(https));assertTrue(jar.loadForRequest(http).isEmpty());assertTrue(jar.loadForRequest(https.newBuilder().host("other.invalid").build()).isEmpty())
        val expired=okhttp3.Cookie.parse(https,"fixture_secure=removed; Path=/; Secure; Max-Age=0")!!;jar.saveFromResponse(https,listOf(expired));assertTrue(jar.loadForRequest(https).isEmpty())
    }
}
