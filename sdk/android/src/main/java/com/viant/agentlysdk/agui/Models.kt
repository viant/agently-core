package com.viant.agentlysdk.agui

import kotlinx.serialization.json.*

const val AG_UI_PROTOCOL_VERSION = "1.0"
enum class AgUiEventType {
    TEXT_MESSAGE_START,
    TEXT_MESSAGE_CONTENT,
    TEXT_MESSAGE_END,
    TEXT_MESSAGE_CHUNK,
    TOOL_CALL_START,
    TOOL_CALL_ARGS,
    TOOL_CALL_END,
    TOOL_CALL_CHUNK,
    TOOL_CALL_RESULT,
    STATE_SNAPSHOT,
    STATE_DELTA,
    MESSAGES_SNAPSHOT,
    ACTIVITY_SNAPSHOT,
    ACTIVITY_DELTA,
    RAW,
    CUSTOM,
    RUN_STARTED,
    RUN_FINISHED,
    RUN_ERROR,
    STEP_STARTED,
    STEP_FINISHED,
    REASONING_START,
    REASONING_MESSAGE_START,
    REASONING_MESSAGE_CONTENT,
    REASONING_MESSAGE_END,
    REASONING_MESSAGE_CHUNK,
    REASONING_END,
    REASONING_ENCRYPTED_VALUE,
    SUBAGENT_STARTED,
    SUBAGENT_FINISHED,
    SUBAGENT_ERROR;
    companion object { fun fromWire(type: String): AgUiEventType? = entries.firstOrNull { it.name == type } }
}

/** The entire JSON tree is retained; projections must never reconstruct history. */
data class AgUiMessage(val value: JsonObject) {
    init { AgUiSchema.validate(value, "Message", true) }
    val id get() = value.requiredString("id")
    val role get() = value.requiredString("role")
    companion object {
        fun user(id: String, content: JsonElement) = AgUiMessage(buildJsonObject { put("id", id); put("role", "user"); put("content", content) })
    }
}
data class AgUiRunInput(val value: JsonObject) {
    init { AgUiSchema.validate(value, "RunAgentInput", true) }
    val threadId get() = value.requiredString("threadId")
    val runId get() = value.requiredString("runId")
    companion object {
        fun create(threadId: String, runId: String, messages: List<AgUiMessage>, state: JsonElement? = null, tools: JsonArray? = null, context: JsonArray? = null, forwardedProps: JsonElement? = null, resume: JsonArray? = null, parentRunId: String? = null) = AgUiRunInput(buildJsonObject {
            put("threadId", threadId); put("runId", runId); put("protocolVersion", AG_UI_PROTOCOL_VERSION)
            put("messages", JsonArray(messages.map { it.value }))
            state?.let { put("state", it) }; tools?.let { put("tools", it) }; context?.let { put("context", it) }
            forwardedProps?.let { put("forwardedProps", it) }; resume?.let { put("resume", it) }; parentRunId?.let { put("parentRunId", it) }
        })
    }
}
data class AgUiEvent(val value: JsonObject) {
    val type get() = value.requiredString("type")
    val knownType get() = AgUiEventType.fromWire(type)
    init { if (AgUiEventType.fromWire(value.requiredString("type")) != null) AgUiSchema.validate(value, "Event", true) }
}

data class AgUiSubagentInvocation(val started: AgUiEvent, val terminal: AgUiEvent? = null)

object AgentlyAgUiExtensions {
    const val VERSION = "1"
    fun forwardedProps(operation: String, agentId: String? = null, model: String? = null, requestId: String? = null, existing: JsonObject = JsonObject(emptyMap())): JsonObject {
        require(operation in setOf("chat", "capabilities"))
        val extension = buildJsonObject {
            put("version", VERSION); put("operation", operation)
            requestId?.let { put("requestId", it) }
            if (agentId != null || model != null) put("payload", buildJsonObject { agentId?.let { put("agentId", it) }; model?.let { put("model", it) } })
        }
        return existing.with("agently" to extension)
    }
    fun command(operation: String, requestId: String, payload: JsonObject, existing: JsonObject = JsonObject(emptyMap())): JsonObject {
        require(operation.isNotEmpty() && requestId.isNotEmpty())
        return existing.with("agently" to buildJsonObject {put("version",VERSION);put("operation",operation);put("requestId",requestId);put("payload",payload)})
    }
    fun cancelRun(targetRunId: String, requestId: String): JsonObject { require(targetRunId.isNotEmpty()); return command("run.cancel",requestId,buildJsonObject {put("runId",targetRunId)}) }
    fun cancelInput(threadId: String, targetRunId: String, commandRunId: String): AgUiRunInput = AgUiRunInput.create(threadId,commandRunId,emptyList(),forwardedProps=cancelRun(targetRunId,commandRunId))
    fun capabilities(event: AgUiEvent): JsonObject? {
        if (event.type != "CUSTOM" || event.value.string("name") != "agently.capabilities") return null
        val envelope = event.value["value"] as? JsonObject ?: return null
        if (envelope.string("version") != VERSION) return null
        val capabilities = envelope["capabilities"] as? JsonObject ?: return null
        AgUiSchema.validate(capabilities, "AgentCapabilities", true)
        return capabilities
    }
}
