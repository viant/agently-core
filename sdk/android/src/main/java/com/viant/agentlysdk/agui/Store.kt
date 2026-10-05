package com.viant.agentlysdk.agui

import kotlinx.serialization.json.*

/** Protocol state, separate from UI rows. One store receives one POST run. */
class AgUiStore(val input: AgUiRunInput) {
    private val chunks = AgUiChunks()
    private val verifier = AgUiVerifier(input)
    var messages: List<AgUiMessage> = (input.value.getValue("messages") as JsonArray).map { AgUiMessage(it.obj()) }; private set
    var state: JsonElement = input.value["state"] ?: JsonObject(emptyMap()); private set
    var terminalEvent: AgUiEvent? = null; private set
    var capabilities: JsonObject? = null; private set
    var pendingInterrupts: JsonArray = JsonArray(emptyList()); private set
    private val subagentEvents: MutableMap<String, AgUiSubagentInvocation> = linkedMapOf()
    val subagents: Map<String, AgUiSubagentInvocation> get() = subagentEvents.toMap()
    val isTerminal get() = verifier.terminal
    private val runCalls = linkedSetOf<String>()
    private val answeredCalls = mutableSetOf<String>()
    val pendingToolCallIds: List<String> get() = if (terminalEvent?.type != "RUN_FINISHED" || (terminalEvent?.value?.get("outcome") as? JsonObject)?.string("type")?.let { it != "success" } == true) emptyList() else ((terminalEvent?.value?.get("outcome") as? JsonObject)?.get("pendingToolCallIds") as? JsonArray)?.takeIf { it.isNotEmpty() }?.map { it.jsonPrimitive.content }?.filter { it !in answeredCalls } ?: runCalls.filter { it !in answeredCalls }

    fun receive(event: AgUiEvent, onApplied: ((AgUiEvent) -> Unit)? = null): List<AgUiEvent> {
        val events = chunks.receive(event)
        for (normalized in events) { verifier.receive(normalized); reduce(normalized); onApplied?.invoke(normalized) }
        return events
    }
    fun finish(): List<AgUiEvent> {
        val ends = chunks.finish()
        for (event in ends) { verifier.receive(event); reduce(event) }
        verifier.finish()
        return ends
    }
    fun nextInput(runId: String, forwardedProps: JsonElement? = null, tools: JsonArray? = null, context: JsonArray? = null, resume: JsonArray? = null, includeActivityMessages: Boolean = false, responseValidator: ((JsonElement, JsonElement) -> Unit)? = null) = snapshot.nextInput(runId, forwardedProps, tools, context, resume, includeActivityMessages, responseValidator)
    fun toolResultsInput(runId: String, results: List<AgUiMessage>, forwardedProps: JsonElement? = null, includeActivityMessages: Boolean = false) = snapshot.toolResultsInput(runId, results, forwardedProps, includeActivityMessages)
    private fun metadata(target: JsonObject, event: JsonObject): JsonObject {
        val incoming = event["metadata"] as? JsonObject ?: return target
        val existing = target["metadata"] as? JsonObject ?: JsonObject(emptyMap())
        // Metadata merges at the top level; each namespace's value is opaque.
        return target.with("metadata" to JsonObject(existing + incoming))
    }
    private fun changeMessage(id: String, transform: (JsonObject) -> JsonObject) {
        messages = messages.map { if (it.id == id) AgUiMessage(transform(it.value)) else it }
    }
    private fun tool(id: String, transform: (JsonObject) -> JsonObject) {
        messages = messages.map { message ->
            val calls = message.value["toolCalls"] as? JsonArray ?: return@map message
            if (calls.none { it.obj().string("id") == id }) return@map message
            AgUiMessage(message.value.with("toolCalls" to JsonArray(calls.map { if (it.obj().string("id") == id) transform(it.obj()) else it })))
        }
    }
    private fun reduce(event: AgUiEvent) {
        val e = event.value
        when (event.type) {
            "TEXT_MESSAGE_START", "REASONING_MESSAGE_START" -> {
                val id = e.requiredString("messageId")
                if (messages.any { it.id == id && it.role == "activity" }) return
                if (messages.none { it.id == id }) messages = messages + AgUiMessage(buildJsonObject {
                    put("id", id); put("role", e["role"] ?: jstring("assistant")); put("content", "")
                    e["name"]?.let { put("name", it) }; e["subagentRunId"]?.let { put("subagentRunId", it) }
                })
                changeMessage(id) { metadata(it, e) }
            }
            "TEXT_MESSAGE_CONTENT", "REASONING_MESSAGE_CONTENT" -> changeMessage(e.requiredString("messageId")) { m ->
                if (m.string("role") == "activity") return@changeMessage m
                val delta = e.requiredString("delta")
                val content = m["content"]
                val next = if (content is JsonArray) JsonArray(content + buildJsonObject { put("type", "text"); put("text", delta) }) else jstring((content as? JsonPrimitive)?.content.orEmpty() + delta)
                metadata(m.with("content" to next), e)
            }
            "TEXT_MESSAGE_END", "REASONING_MESSAGE_END" -> changeMessage(e.requiredString("messageId")) { if (it.string("role") == "activity") it else metadata(it, e) }
            "TOOL_CALL_START" -> {
                val id = e.requiredString("toolCallId"); val name = e.requiredString("toolCallName"); runCalls.add(id)
                val existing = messages.any { m -> (m.value["toolCalls"] as? JsonArray).orEmpty().any { it.obj().string("id") == id } }
                if (existing) tool(id) { metadata(it.with("function" to it.getValue("function").obj().with("name" to jstring(name))), e) }
                else {
                    val parent = e.string("parentMessageId"); val matched = messages.find { it.id == parent }
                    val targetId = if (matched != null && matched.role != "assistant") id else parent ?: id
                    if (messages.none { it.id == targetId && it.role == "assistant" }) messages = messages + AgUiMessage(buildJsonObject {
                        put("id", targetId); put("role", "assistant"); put("toolCalls", JsonArray(emptyList())); e["subagentRunId"]?.let { put("subagentRunId", it) }
                    })
                    val call = metadata(buildJsonObject { put("id", id); put("type", "function"); put("function", buildJsonObject { put("name", name); put("arguments", "") }) }, e)
                    changeMessage(targetId) { m -> if (m.string("role") != "assistant") m else m.with("toolCalls" to JsonArray((m["toolCalls"] as? JsonArray).orEmpty() + call)) }
                }
            }
            "TOOL_CALL_ARGS" -> tool(e.requiredString("toolCallId")) { call ->
                val f = call.getValue("function").obj()
                metadata(call.with("function" to f.with("arguments" to jstring(f.requiredString("arguments") + e.requiredString("delta")))), e)
            }
            "TOOL_CALL_END" -> tool(e.requiredString("toolCallId")) { metadata(it, e) }
            "TOOL_CALL_RESULT" -> {
                val id = e.requiredString("messageId"); answeredCalls.add(e.requiredString("toolCallId"))
                val m = metadata(buildJsonObject {
                    put("id", id); put("role", "tool"); put("toolCallId", e.getValue("toolCallId")); put("content", e.getValue("content")); e["subagentRunId"]?.let { put("subagentRunId", it) }
                }, e)
                val updated = messages.filter { it.id != id }.toMutableList()
                val owner = updated.indexOfFirst { message -> message.role == "assistant" && (message.value["toolCalls"] as? JsonArray).orEmpty().any { it.obj().string("id") == e.requiredString("toolCallId") } }
                var insertAt = if (owner < 0) updated.size else owner + 1
                while (insertAt < updated.size && updated[insertAt].role == "tool") insertAt++
                updated.add(insertAt, AgUiMessage(m)); messages = updated
            }
            "STATE_SNAPSHOT" -> state = e.getValue("snapshot")
            "STATE_DELTA" -> state = AgUiJsonPatch.apply(state, e.getValue("delta") as JsonArray)
            "MESSAGES_SNAPSHOT" -> {
                val fresh = (e.getValue("messages") as JsonArray).map { AgUiMessage(it.obj()) }; val map = fresh.associateBy { it.id }
                val metadata = e["metadata"] as? JsonObject
                val clientMetadata = metadata?.get("@ag-ui/client") as? JsonObject
                val explicit = clientMetadata?.get("authoritativeActivityTypes")
                val owned = explicit as? JsonArray
                val ownsAll = explicit == JsonNull || (explicit == null && fresh.any { it.role == "activity" })
                val hasReasoning = fresh.any { it.role == "reasoning" }
                messages = messages.filter { m -> m.id in map || (m.role == "activity" && !ownsAll && (owned == null || owned.none { it.jsonPrimitive.content == m.value.string("activityType") })) || (m.role == "reasoning" && !hasReasoning) }.map { map[it.id] ?: it }
                val ids = messages.map { it.id }.toSet(); messages = messages + fresh.filter { it.id !in ids }
            }
            "ACTIVITY_SNAPSHOT" -> {
                val id = e.requiredString("messageId"); val old = messages.find { it.id == id }; val replace = e["replace"] != JsonPrimitive(false)
                if (old == null || replace) {
                    var m = if (old?.role == "activity") old.value else buildJsonObject { put("id", id); put("role", "activity") }
                    m = m.with("activityType" to e.getValue("activityType"), "content" to e.getValue("content"), "subagentRunId" to e["subagentRunId"])
                    val result = AgUiMessage(metadata(m, e)); messages = if (old == null) messages + result else messages.map { if (it.id == id) result else it }
                } else if (old.role == "activity") changeMessage(id) { metadata(it, e) }
            }
            "ACTIVITY_DELTA" -> changeMessage(e.requiredString("messageId")) { m ->
                if (m.string("role") != "activity") m else metadata(m.with("content" to AgUiJsonPatch.apply(m.getValue("content"), e.getValue("patch") as JsonArray), "activityType" to e.getValue("activityType")), e)
            }
            "REASONING_ENCRYPTED_VALUE" -> {
                if (e.string("subtype") == "tool-call") tool(e.requiredString("entityId")) { it.with("encryptedValue" to e.getValue("encryptedValue")) }
                else changeMessage(e.requiredString("entityId")) { if (it.string("role") == "activity") it else it.with("encryptedValue" to e.getValue("encryptedValue")) }
            }
            "RUN_STARTED" -> (e["input"] as? JsonObject)?.get("messages")?.let { raw ->
                for (m in raw as JsonArray) if (messages.none { it.id == m.obj().string("id") }) messages = messages + AgUiMessage(m.obj())
            }
            "RUN_FINISHED" -> { terminalEvent = event; pendingInterrupts = (e["outcome"] as? JsonObject)?.get("interrupts") as? JsonArray ?: JsonArray(emptyList()) }
            "RUN_ERROR" -> terminalEvent = event
            "CUSTOM" -> AgentlyAgUiExtensions.capabilities(event)?.let { capabilities = it }
            "SUBAGENT_STARTED" -> subagentEvents[e.requiredString("subagentRunId")] = AgUiSubagentInvocation(event)
            "SUBAGENT_FINISHED", "SUBAGENT_ERROR" -> {
                val id = e.requiredString("subagentRunId")
                subagentEvents[id] = subagentEvents.getValue(id).copy(terminal = event)
            }
            else -> Unit // reasoning spans, steps, raw/custom/future payloads remain observable events.
        }
    }
}
