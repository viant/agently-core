package com.viant.agentlysdk.agui

import com.viant.agentlysdk.stream.isInternalMessageMode

import com.viant.agentlysdk.RenderedContent
import com.viant.agentlysdk.stream.SSEEvent
import com.viant.agentlysdk.stream.EventModel
import com.viant.agentlysdk.stream.PlannedToolCall
import kotlinx.serialization.json.*

/** Only the approved scalar presentation namespace crosses into native renderer identities. */
internal fun presentation(metadata: JsonElement?): JsonObject? {
    val namespace = (metadata as? JsonObject)?.get("agently") as? JsonObject ?: return null
    val value = namespace["presentation"] as? JsonObject
    if (value == null) return if (namespace.string("identityVersion") == "1" && namespace.string("nativeTurnId") != null)
        buildJsonObject { put("version", "1"); put("nativeTurnId", namespace.getValue("nativeTurnId")) } else null
    if (value.string("version") != "1") return null
    val strings = setOf("conversationId", "nativeTurnId", "nativeMessageId", "parentMessageId", "pageId", "modelCallId", "nativeToolCallId", "toolMessageId", "executionRole", "phase", "mode", "status", "agentId", "agentName", "provider", "model", "requestPayloadId", "responsePayloadId", "providerRequestPayloadId", "providerResponsePayloadId", "streamPayloadId", "createdAt", "startedAt", "completedAt", "usageScope", "protocolRunId", "clientMessageId", "clientRequestId", "nativeUserMessageId")
    val counts = setOf("iteration", "pageIndex", "pageCount", "inputTokens", "outputTokens", "cachedInputTokens", "reasoningTokens", "embeddingTokens", "totalTokens", "cacheWriteInputTokens")
    if (strings.any { value.containsKey(it) && ((value[it] as? JsonPrimitive)?.isString != true) }) return null
    if (counts.any { value.containsKey(it) && ((value[it] as? JsonPrimitive)?.longOrNull?.let { count -> count < 0 || count > 9007199254740991L } != false) }) return null
    if (value.containsKey("latestPage") && (value["latestPage"] as? JsonPrimitive)?.booleanOrNull == null) return null
    return JsonObject(value.filterKeys { it in strings || it in counts || it in setOf("version", "latestPage") })
}

/** Read-only presentation adapter. Protocol bodies come exclusively from AgUiStore's reduced graph. */
internal class AgUiNativePresentation(
    private val conversationId: String,
    val runId: String,
    baseline: List<AgUiMessage>,
    private val inputMessageId: String? = null,
    private val emit: (SSEEvent) -> Unit,
    private val hostActivity: (AgUiMessage) -> Unit,
    private val alias: (String, String) -> Unit
) {
    var nativeTurnId: String? = null; private set
    private val baselineById = baseline.associate { it.id to it.value }
    private val lanes = mutableMapOf<String, JsonObject>()
    private val calls = mutableMapOf<String, JsonObject>()
    private val owned = mutableSetOf<String>()
    private val emitted = mutableMapOf<String, SSEEvent>()
    private val rendered = mutableMapOf<String, RenderedContent>()
    private val nativeUsers = mutableMapOf<String, String>()
    private val turnStatus = mutableMapOf<String, String>()
    private val json = Json { ignoreUnknownKeys = true }

    fun consume(update: AgUiUpdate) {
        val event = update.event.value
        val p = presentation(event["metadata"])
        if (update.event.type == "RUN_STARTED" && event.string("subagentRunId") == null) {
            nativeTurnId = p?.string("nativeTurnId") ?: nativeTurnId
        }
        val mid = event.string("messageId")
        if (mid != null && update.event.type != "MESSAGES_SNAPSHOT") {
            owned += mid
            if (p != null) lanes[mid] = p
        }
        event.string("toolCallId")?.let { id ->
            if (p != null) calls[id] = p
            event.string("parentMessageId")?.let { owned += it; if (p != null) lanes[it] = p }
        }
        if (update.event.type in setOf("STEP_STARTED", "STEP_FINISHED")) {
            send(base(p).copy(type = if (update.event.type == "STEP_STARTED") "model_started" else "model_completed",
                modelCallId = p?.string("modelCallId") ?: event.string("stepName"),
                assistantMessageId = p?.string("nativeMessageId"), id = p?.string("nativeMessageId"),
                status = if (update.event.type == "STEP_STARTED") "running" else "completed"))
        }
        // Activity identities/lifecycle must precede the messages they identify.
        val messages = update.snapshot.messages
        for (message in messages) if (message.role == "activity" && isOwned(message)) activity(message)
        for (message in messages) if (message.role != "activity" && isOwned(message)) project(message)
        if (update.event.type == "CUSTOM" && event.string("name") == "agently.usage") {
            val value = event["value"] as? JsonObject
            val usage = value?.get("usage") as? JsonObject
            if (value?.string("version") == "1" && usage != null) send(base(p).copy(type = "usage",
                usageInputTokens = usage.int("inputTokens"), usageOutputTokens = usage.int("outputTokens"),
                usageEmbeddingTokens = usage.int("embeddingTokens"), usageTotalTokens = usage.int("totalTokens")))
        }
        if (update.event.type == "RUN_FINISHED") {
            val outcome = event["outcome"] as? JsonObject
            when (outcome?.string("type") ?: "success") {
                "success" -> if (update.snapshot.pendingToolCallIds.isEmpty()) send(base(p).copy(type = "turn_completed", status = "completed"))
                "cancelled" -> send(base(p).copy(type = "turn_canceled", status = "canceled"))
                "interrupt" -> for (raw in update.snapshot.pendingInterrupts) {
                    val interrupt = raw as? JsonObject ?: continue
                    val details = interrupt["payload"] as? JsonObject ?: interrupt
                    send(base(p).copy(type = "elicitation_requested", elicitationId = interrupt.string("id"),
                        content = details.string("message"), status = "pending", elicitationData = buildJsonObject {
                            put("requestedSchema", interrupt["responseSchema"] ?: details["requestedSchema"] ?: JsonObject(emptyMap()))
                            put("protocolRunId", runId); put("interrupt", interrupt)
                        }))
                }
            }
        } else if (update.event.type == "RUN_ERROR") send(base(p).copy(type = "turn_failed", status = "failed", error = event.string("message")))
    }

    private fun isOwned(message: AgUiMessage): Boolean {
        if (message.id in owned) return true
        val p = presentation(message.value["metadata"])
        if (nativeTurnId != null && p?.string("nativeTurnId") == nativeTurnId && baselineById[message.id] != message.value) {
            owned += message.id; if (p != null) lanes[message.id] = p; return true
        }
        return message.id == inputMessageId && nativeTurnId != null
    }

    private fun base(p: JsonObject?) = SSEEvent(type = "", conversationId = p?.string("conversationId") ?: conversationId,
        turnId = p?.string("nativeTurnId") ?: nativeTurnId,
        id = p?.string("nativeMessageId"), messageId = p?.string("nativeMessageId"),
        assistantMessageId = p?.string("nativeMessageId"), parentMessageId = p?.string("parentMessageId"),
        pageId = p?.string("pageId"),
        model = if (p?.string("provider") != null || p?.string("model") != null) EventModel(p.string("provider"), p.string("model")) else null,
        modelCallId = p?.string("modelCallId"), iteration = p?.int("iteration"), mode = p?.string("mode"),
        pageIndex = p?.int("pageIndex"), pageCount = p?.int("pageCount"), latestPage = p?.bool("latestPage"),
        agentIdUsed = p?.string("agentId"), agentName = p?.string("agentName"), provider = p?.string("provider"), modelName = p?.string("model"),
        requestPayloadId = p?.string("requestPayloadId"), responsePayloadId = p?.string("responsePayloadId"),
        providerRequestPayloadId = p?.string("providerRequestPayloadId"), providerResponsePayloadId = p?.string("providerResponsePayloadId"),
        streamPayloadId = p?.string("streamPayloadId"), createdAt = p?.string("createdAt"), startedAt = p?.string("startedAt"), completedAt = p?.string("completedAt"))

    private fun send(event: SSEEvent, key: String = "${event.type}:${event.turnId}:${event.messageId ?: event.toolCallId ?: event.modelCallId ?: event.feedId}") {
        if (event.turnId.isNullOrBlank() || event.conversationId != conversationId || emitted[key] == event) return
        emitted[key] = event
        emit(event)
    }

    private fun project(message: AgUiMessage) {
        val m = message.value; val p = presentation(m["metadata"]) ?: lanes[message.id]
        val text = (m["content"] as? JsonPrimitive)?.takeIf { it.isString }?.content
        val nativeId = p?.string("nativeMessageId") ?: message.id
        when (message.role) {
            "user" -> if (text != null && turnStatus[p?.string("nativeTurnId") ?: nativeTurnId] == "running") {
                val nativeUser = p?.string("nativeUserMessageId") ?: nativeUsers[p?.string("nativeTurnId") ?: nativeTurnId] ?: message.id
                send(base(p).copy(type = "message_appended", id = nativeUser, messageId = nativeUser, userMessageId = nativeUser,
                    content = text, contentMode = "snapshot", patch = buildJsonObject { put("role", "user") }))
            }
            "assistant", "reasoning" -> {
                if (text != null && !isInternalMessageMode(p?.string("mode"))) send(base(p).copy(type = if (message.role == "reasoning") "reasoning_delta" else "text_delta",
                    id = nativeId, messageId = nativeId, assistantMessageId = nativeId,
                    content = text, contentMode = "snapshot", renderedContent = rendered[nativeId]))
                for (raw in (m["toolCalls"] as? JsonArray).orEmpty()) {
                    val call = raw as? JsonObject ?: continue
                    val id = call.string("id") ?: continue
                    val f = call["function"] as? JsonObject ?: continue
                    val cp = presentation(call["metadata"]) ?: calls[id] ?: p
                    calls[id] = cp ?: JsonObject(emptyMap())
                    send(base(cp).copy(type = "tool_call_started", assistantMessageId = nativeId,
                        toolCallId = cp?.string("nativeToolCallId") ?: id, toolName = f.string("name"),
                        arguments = f.string("arguments")?.let { runCatching { json.parseToJsonElement(it) }.getOrNull() }, status = "requested"), "tool-request:$id")
                }
            }
            "tool" -> {
                val protocolCallId = m.string("toolCallId") ?: return
                val cp = p ?: calls[protocolCallId]
                if (text != null) send(base(cp).copy(type = if (m.string("error") != null) "tool_call_failed" else "tool_call_completed",
                    toolCallId = cp?.string("nativeToolCallId") ?: protocolCallId, toolMessageId = cp?.string("toolMessageId") ?: message.id,
                    content = text, responsePayload = runCatching { json.parseToJsonElement(text) }.getOrElse { buildJsonObject { put("text", text) } },
                    error = m.string("error"), status = if (m.string("error") != null) "failed" else "completed"), "tool-result:${message.id}")
            }
        }
    }

    private fun activity(message: AgUiMessage) {
        val m = message.value; val content = m["content"] as? JsonObject ?: return
        val p = presentation(m["metadata"]) ?: lanes[message.id]
        val type = m.string("activityType") ?: return
        if (type == "mcp-apps") { hostActivity(message); return }
        if (content.string("version") != "1") return
        when (type) {
            "agently.user-identity" -> {
                val turn = content.string("nativeTurnId") ?: return
                val native = content.string("nativeUserMessageId") ?: return
                if (content.string("protocolRunId") != runId) return
                nativeUsers[turn] = native
                content.string("clientMessageId")?.let { alias(it, native) }
                content.string("clientRequestId")?.let { alias(it, native) }
            }
            "agently.turn" -> {
                val turn = content.string("nativeTurnId") ?: return
                val status = content.string("status") ?: return
                turnStatus[turn] = status
                val eventType = when (status) { "queued" -> "turn_queued"; "running" -> "turn_started"; "completed" -> "turn_completed"; "failed" -> "turn_failed"; "canceled" -> "turn_canceled"; else -> return }
                send(base(p).copy(type = eventType, turnId = turn, status = status,
                    userMessageId = content.string("startedByMessageId") ?: nativeUsers[turn]))
            }
            "agently.tool" -> {
                val id = content.string("toolCallId") ?: return
                val cp = p ?: calls[id]
                val status = content.string("status") ?: return
                val eventType = when { content.string("phase") == "waiting" -> "tool_call_waiting"; status == "completed" -> "tool_call_completed"; status == "failed" -> "tool_call_failed"; status == "canceled" -> "tool_call_canceled"; else -> "tool_call_started" }
                send(base(cp).copy(type = eventType, toolCallId = cp?.string("nativeToolCallId") ?: id,
                    toolMessageId = content.string("toolMessageId"), status = status,
                    startedAt = content.string("startedAt"), completedAt = content.string("completedAt")), "tool-effect:$id")
            }
            "agently.feed" -> {
                val feed = content["feed"] as? JsonObject ?: content
                val id = feed.string("feedId") ?: return
                val active = if (content.bool("activationKnown") == false) null else content.bool("active")
                val fp = feed["presentation"] as? JsonObject
                send(base(p).copy(type = when (active) { true -> "tool_feed_active"; false -> "tool_feed_inactive"; null -> "tool_feed_unknown" },
                    feedId = id, feedTitle = feed.string("title") ?: id, feedItemCount = feed.int("itemCount"), feedData = feed["data"],
                    feedDeveloperOnly = feed.bool("developerOnly"), feedIcon = fp?.string("icon"), feedAccent = fp?.string("accent"), feedTarget = fp?.string("target")), "feed:$id")
            }
            "agently.planner" -> send(base(p).copy(type = "planner.${content.string("status")}",
                plannerTrigger = content.string("trigger"), plannerStaticProfile = content.string("staticProfile"), plannerStrategyFamily = content.string("strategyFamily"),
                plannerAttempt = content.int("attempt"), plannerSecondPolicy = content.string("secondPolicy"), plannerOutputPayloadId = content.string("outputPayloadId"), plannerValidated = content.bool("validated")), "planner:${message.id}")
            "agently.tools-planned" -> send(base(p).copy(type = "tool_calls_planned", toolCallsPlanned = (content["calls"] as? JsonArray).orEmpty().mapNotNull {
                val call = it as? JsonObject ?: return@mapNotNull null
                val id = call.string("toolCallId") ?: return@mapNotNull null
                PlannedToolCall(calls[id]?.string("nativeToolCallId") ?: id, call.string("toolName"))
            }), "planned:${message.id}")
            "agently.narration" -> send(base(p).copy(type = "narration", narration = content.string("text"), status = content.string("status")), "narration:${message.id}")
            "agently.rendered-content" -> {
                val id = p?.string("nativeMessageId") ?: message.id.removeSuffix("/activity")
                content["renderedContent"]?.let { raw -> runCatching { json.decodeFromJsonElement(RenderedContent.serializer(), raw) }.getOrNull()?.let { rendered[id] = it } }
            }
        }
    }
}

internal fun JsonObject.int(key: String): Int? = (get(key) as? JsonPrimitive)?.intOrNull
internal fun JsonObject.bool(key: String): Boolean? = (get(key) as? JsonPrimitive)?.booleanOrNull
