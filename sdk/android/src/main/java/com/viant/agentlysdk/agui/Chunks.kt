package com.viant.agentlysdk.agui

import kotlinx.serialization.json.*

internal class AgUiChunks {
    private data class Lane(val kind: String, val fields: JsonObject)
    private val lanes = linkedMapOf<String?, Lane>()
    private fun close(owner: String?): List<AgUiEvent> {
        val lane = lanes.remove(owner) ?: return emptyList()
        val end = when (lane.kind) { "text" -> "TEXT_MESSAGE_END"; "tool" -> "TOOL_CALL_END"; else -> "REASONING_MESSAGE_END" }
        val idKey = if (lane.kind == "tool") "toolCallId" else "messageId"
        return listOf(AgUiEvent(buildJsonObject { put("type", end); put(idKey, lane.fields.getValue(idKey)); lane.fields["subagentRunId"]?.let { put("subagentRunId", it) } }))
    }
    fun receive(event: AgUiEvent): List<AgUiEvent> {
        val type = event.type; val raw = event.value; val tag = raw.string("subagentRunId")
        val kind = when(type) { "TEXT_MESSAGE_CHUNK" -> "text"; "TOOL_CALL_CHUNK" -> "tool"; "REASONING_MESSAGE_CHUNK" -> "reasoning"; else -> null }
        if (kind == null) {
            return when (type) {
                "RUN_STARTED", "RUN_FINISHED", "RUN_ERROR", "MESSAGES_SNAPSHOT" -> lanes.keys.toList().flatMap { close(it) } + event
                "RAW", "ACTIVITY_SNAPSHOT", "ACTIVITY_DELTA", "REASONING_ENCRYPTED_VALUE", "SUBAGENT_STARTED" -> listOf(event)
                "SUBAGENT_FINISHED", "SUBAGENT_ERROR" -> close(tag) + event
                else -> if (event.knownType != null) close(tag) + event else listOf(event)
            }
        }
        val idKey = if (kind == "tool") "toolCallId" else "messageId"
        val id = raw.string(idKey)
        val holder = if (id != null) lanes.entries.firstOrNull { it.value.kind == kind && it.value.fields.string(idKey) == id } else null
        val owner: String? = when {
            holder != null -> { if (tag != null && tag != holder.key) fail("Chunk attribution disagrees with opener"); holder.key }
            id != null || tag != null -> tag
            lanes[null]?.kind == kind -> null
            else -> {
                val candidates = lanes.filterValues { it.kind == kind }.keys.toList()
                if (candidates.size > 1) fail("Ambiguous $type; specify entity ID or subagentRunId")
                candidates.singleOrNull()
            }
        }
        val out = mutableListOf<AgUiEvent>()
        var lane = lanes[owner]
        val opening = lane?.kind != kind || (id != null && id != lane.fields.string(idKey))
        if (opening) {
            out += close(owner)
            if (id == null) fail("First $type requires $idKey")
            if (kind == "tool" && raw.string("toolCallName") == null) fail("First TOOL_CALL_CHUNK requires toolCallName")
            val fields = buildJsonObject {
                put(idKey, id)
                raw["subagentRunId"]?.let { put("subagentRunId", it) }
                if (kind == "text") { put("role", raw["role"] ?: jstring("assistant")); raw["name"]?.let { put("name", it) } }
                if (kind == "tool") { put("toolCallName", raw.getValue("toolCallName")); raw["parentMessageId"]?.let { put("parentMessageId", it) } }
                if (kind == "reasoning") put("role", "reasoning")
            }
            lane = Lane(kind, fields); lanes[owner] = lane
            val startType = when(kind) { "text" -> "TEXT_MESSAGE_START"; "tool" -> "TOOL_CALL_START"; else -> "REASONING_MESSAGE_START" }
            out += AgUiEvent(fields.with("type" to jstring(startType), "metadata" to raw["metadata"]))
        } else {
            val agreements = when(kind) { "text" -> listOf("role", "name"); "tool" -> listOf("toolCallName", "parentMessageId"); else -> emptyList() }
            for (field in agreements) if (raw.containsKey(field) && raw[field] != lane!!.fields[field]) fail("Chunk $field disagrees with opener")
        }
        val active = lane ?: fail("Missing chunk lane")
        if(opening && !raw.containsKey("delta") && !raw.containsKey("rawEvent")) {
            val fields=out.last().value.toMutableMap()
            for((key,value) in raw)if(key !in setOf("type",idKey,"role","name","toolCallName","parentMessageId","subagentRunId","timestamp","delta","rawEvent","metadata") && !fields.containsKey(key))fields[key]=value
            out[out.lastIndex]=AgUiEvent(JsonObject(fields))
        }
        if (raw.containsKey("delta") || raw.containsKey("rawEvent") || !opening && (raw.containsKey("metadata") || raw.keys.any { it !in setOf("type", idKey, "role", "name", "toolCallName", "parentMessageId", "subagentRunId", "timestamp") })) {
            val contentType = when(kind) { "text" -> "TEXT_MESSAGE_CONTENT"; "tool" -> "TOOL_CALL_ARGS"; else -> "REASONING_MESSAGE_CONTENT" }
            val fields = raw.toMutableMap().apply {
                remove("role"); remove("name"); remove("toolCallName"); remove("parentMessageId")
                put("type", jstring(contentType)); put(idKey, active.fields.getValue(idKey)); put("delta", raw["delta"] ?: jstring(""))
                active.fields["subagentRunId"]?.let { put("subagentRunId", it) }
            }
            out += AgUiEvent(JsonObject(fields))
        }
        return out
    }
    fun finish(): List<AgUiEvent> = lanes.keys.toList().flatMap { close(it) }
}
