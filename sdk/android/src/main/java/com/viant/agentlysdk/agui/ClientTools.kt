package com.viant.agentlysdk.agui

import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.serialization.json.*
import java.util.UUID

data class AgUiClientToolResult(val content: JsonElement, val error: String? = null, val metadata: JsonObject? = null)
class AgUiClientTool(val definition: JsonObject, val validateArguments: ((JsonElement,JsonElement)->Unit)? = null, val execute: suspend (JsonElement,JsonObject)->AgUiClientToolResult) {
    init {AgUiSchema.validate(definition,"Tool")}
}
/** Successful handler outputs are cached; continuation submission remains explicit. */
class AgUiClientToolDispatcher {
    private val mutex = Mutex()
    private val completed = mutableMapOf<String,AgUiMessage>()
    private val signatures = mutableMapOf<String,JsonElement>()
    /** Clear after durable continuation acceptance, never merely after a transport drop. */
    suspend fun clearCompleted() = mutex.withLock { completed.clear(); signatures.clear() }
    suspend fun executeClientTools(snapshot: AgUiSnapshot, tools: List<AgUiClientTool>): List<AgUiMessage> {
        if (snapshot.terminalEvent?.type != "RUN_FINISHED" || (snapshot.terminalEvent.value["outcome"] as? JsonObject)?.string("type")?.let {it!="success"}==true) fail("Client tool dispatch requires successful terminal")
        return dispatch(snapshot,snapshot.pendingToolCallIds,tools)
    }
    suspend fun executeClientToolInterrupts(snapshot: AgUiSnapshot, tools: List<AgUiClientTool>): JsonArray {
        if(snapshot.pendingInterrupts.isNotEmpty() && (snapshot.terminalEvent?.type!="RUN_FINISHED" || (snapshot.terminalEvent?.value?.get("outcome") as? JsonObject)?.string("type")!="interrupt"))fail("Interrupt tool dispatch requires interrupt terminal")
        val interrupts=snapshot.pendingInterrupts.map {it.obj()}.filter {val profile=(it["metadata"] as? JsonObject)?.get("agently") as? JsonObject;it.string("reason")=="agently.client_tool" && profile?.string("version")=="1" && profile.string("kind")=="client-tool"}
        for (interrupt in interrupts) if (interrupt.string("expiresAt")?.let {java.time.Instant.parse(it).isAfter(java.time.Instant.now())}==false) fail("Expired client tool interrupt must be cancelled")
        val results=dispatch(snapshot,interrupts.map {it.requiredString("toolCallId")},tools)
        return JsonArray(interrupts.zip(results).map {(interrupt,result)->buildJsonObject {
            put("interruptId",interrupt.getValue("id"));put("status","resolved");put("payload",buildJsonObject {put("content",result.value.getValue("content"));result.value["error"]?.let{put("error",it)}});result.value["metadata"]?.let{put("metadata",it)}
        }})
    }
    private suspend fun dispatch(snapshot: AgUiSnapshot, ids: List<String>, tools: List<AgUiClientTool>): List<AgUiMessage> = mutex.withLock {
        val registry=tools.associateBy {it.definition.requiredString("name")}
        if (registry.size!=tools.size) fail("Duplicate client tool")
        val calls=ids.map {id->
            val call=snapshot.messages.flatMap {(it.value["toolCalls"] as? JsonArray).orEmpty()}.map{it.obj()}.find{it.string("id")==id} ?: fail("Missing pending client tool call")
            val function=call.getValue("function").obj();val handler=registry[function.requiredString("name")] ?: fail("Missing authorized client tool handler")
            val args=Json.parseToJsonElement(function.requiredString("arguments"));val schema=handler.definition.getValue("parameters")
            if (handler.validateArguments!=null) handler.validateArguments.invoke(args,schema) else AgUiSchema.validateResponse(args,schema)
            Triple(JsonArray(listOf(snapshot.input.value.getValue("threadId"),snapshot.input.value.getValue("runId"),JsonPrimitive(id))).toString(),call,Pair(handler,args))
        }
        calls.map {(key,call,handlerArgs)->completed[key]?.also {if(signatures[key] != call["function"]) fail("Completed client tool identity has conflicting arguments")} ?: run {
            val result=handlerArgs.first.execute(handlerArgs.second,call)
            val message=AgUiMessage(buildJsonObject {put("id",UUID.randomUUID().toString());put("role","tool");put("toolCallId",call.getValue("id"));put("content",result.content);result.error?.let{put("error",it)};result.metadata?.let{put("metadata",it)}})
            completed[key]=message;signatures[key]=call.getValue("function");message
        }}
    }
}
