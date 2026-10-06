package com.viant.agentlysdk.agui

import kotlinx.serialization.json.*

internal class AgUiVerifier(private val input: AgUiRunInput) {
    private var started = false
    var terminal = false; private set
    private var errored = false
    private val open = mutableMapOf<String, MutableSet<String>>()
    private val owners = mutableMapOf<String, MutableMap<String, String?>>()
    private val steps = mutableMapOf<String?, MutableSet<String>>()
    private val activeSubagents = mutableSetOf<String>()
    private val closedSubagents = mutableSetOf<String>()
    private fun bucket(kind: String) = owners.getOrPut(kind) { mutableMapOf() }
    private fun active(kind: String) = open.getOrPut(kind) { mutableSetOf() }
    private fun ownerCheck(kind: String, id: String, tag: String?) {
        val owner = bucket(kind)
        if (tag != null && owner.containsKey(id) && owner[id] != tag) fail("Attribution does not match $kind $id opener")
    }
    private fun start(kind: String, id: String, tag: String?) {
        if (!active(kind).add(id)) fail("$kind $id is already open")
        val ownerKind = if (kind == "reasoningSpan") "reasoning" else kind
        ownerCheck(ownerKind, id, tag)
        if (!bucket(ownerKind).containsKey(id)) bucket(ownerKind)[id] = tag
    }
    private fun continueEntity(kind: String, id: String, tag: String?, close: Boolean = false) {
        if (id !in active(kind)) fail("No open $kind $id")
        ownerCheck(if (kind == "reasoningSpan") "reasoning" else kind, id, tag)
        if (close) active(kind).remove(id)
    }
    private fun seed(messages: JsonArray, authoritative: Boolean) {
        for (raw in messages) {
            val m = raw.obj(); val id = m.requiredString("id"); val tag = m.string("subagentRunId")
            val kind = when (m.requiredString("role")) { "activity" -> "activity"; "reasoning" -> "reasoning"; else -> "message" }
            if (authoritative || !bucket(kind).containsKey(id)) bucket(kind)[id] = tag
            for (call in (m["toolCalls"] as? JsonArray).orEmpty()) {
                val callId = call.obj().requiredString("id")
                if (authoritative || !bucket("tool").containsKey(callId)) bucket("tool")[callId] = tag
            }
        }
    }
    fun receive(event: AgUiEvent) {
        val e = event.value; val type = event.type; val tag = e.string("subagentRunId")
        if (terminal && (type != "RUN_ERROR" || errored)) fail("Event after terminal run event")
        if (!started && type != "RUN_STARTED" && type != "RUN_ERROR") fail("First event must be RUN_STARTED or RUN_ERROR")
        when (type) {
            "RUN_STARTED" -> {
                if (started) fail("Run already started")
                if (e.requiredString("threadId") != input.threadId || e.requiredString("runId") != input.runId) fail("RUN_STARTED identity mismatch")
                started = true
                (e["input"] as? JsonObject)?.get("messages")?.let { seed(it as JsonArray, false) }
            }
            "RUN_FINISHED" -> {
                if (e.requiredString("threadId") != input.threadId || e.requiredString("runId") != input.runId) fail("RUN_FINISHED identity mismatch")
                if (open.values.any { it.isNotEmpty() } || steps.values.any { it.isNotEmpty() } || activeSubagents.isNotEmpty()) fail("RUN_FINISHED with open entities")
                terminal = true
            }
            "RUN_ERROR" -> {terminal = true; errored = true}
            "TEXT_MESSAGE_START" -> start("message", e.requiredString("messageId"), tag)
            "TEXT_MESSAGE_CONTENT" -> continueEntity("message", e.requiredString("messageId"), tag)
            "TEXT_MESSAGE_END" -> continueEntity("message", e.requiredString("messageId"), tag, true)
            "TOOL_CALL_START" -> {
                val id = e.requiredString("toolCallId")
                if (!active("tool").add(id)) fail("Tool call already open")
                val parent = e.string("parentMessageId")
                val inheritedKnown = parent != null && bucket("message").containsKey(parent)
                val inherited = if (inheritedKnown) bucket("message")[parent] else null
                if (parent != null) ownerCheck("message", parent, tag)
                if (bucket("tool").containsKey(id)) {
                    ownerCheck("tool", id, tag)
                    if (tag == null && inheritedKnown && bucket("tool")[id] != inherited) fail("Tool owner disagrees with parent")
                } else bucket("tool")[id] = tag ?: inherited
            }
            "TOOL_CALL_ARGS" -> continueEntity("tool", e.requiredString("toolCallId"), tag)
            "TOOL_CALL_END" -> continueEntity("tool", e.requiredString("toolCallId"), tag, true)
            "TOOL_CALL_RESULT" -> bucket("message")[e.requiredString("messageId")] = tag
            "REASONING_START" -> start("reasoningSpan", e.requiredString("messageId"), tag)
            "REASONING_END" -> continueEntity("reasoningSpan", e.requiredString("messageId"), tag, true)
            "REASONING_MESSAGE_START" -> start("reasoning", e.requiredString("messageId"), tag)
            "REASONING_MESSAGE_CONTENT" -> continueEntity("reasoning", e.requiredString("messageId"), tag)
            "REASONING_MESSAGE_END" -> continueEntity("reasoning", e.requiredString("messageId"), tag, true)
            "REASONING_ENCRYPTED_VALUE" -> {
                val id = e.requiredString("entityId")
                if (e.string("subtype") == "tool-call") ownerCheck("tool", id, tag)
                else { ownerCheck("message", id, tag); ownerCheck("reasoning", id, tag) }
            }
            "ACTIVITY_SNAPSHOT" -> {
                val id = e.requiredString("messageId")
                if (!bucket("activity").containsKey(id) || e["replace"] != JsonPrimitive(false)) bucket("activity")[id] = tag
            }
            "ACTIVITY_DELTA" -> ownerCheck("activity", e.requiredString("messageId"), tag)
            "STEP_STARTED" -> if (!steps.getOrPut(tag) { mutableSetOf() }.add(e.requiredString("stepName"))) fail("Step already open")
            "STEP_FINISHED" -> if (steps[tag]?.remove(e.requiredString("stepName")) != true) fail("No open step for this owner")
            "SUBAGENT_STARTED" -> {
                val id = e.requiredString("subagentRunId")
                if (id in activeSubagents || id in closedSubagents) fail("Duplicate subagent invocation")
                e.string("parentSubagentRunId")?.let { if (it !in activeSubagents && it !in closedSubagents) fail("Unknown parent subagent") }
                activeSubagents.add(id)
            }
            "SUBAGENT_FINISHED", "SUBAGENT_ERROR" -> {
                val id = e.requiredString("subagentRunId")
                if (!activeSubagents.remove(id)) fail("No active subagent $id")
                closedSubagents.add(id)
            }
            "MESSAGES_SNAPSHOT" -> seed(e.getValue("messages") as JsonArray, true)
            "TEXT_MESSAGE_CHUNK", "TOOL_CALL_CHUNK", "REASONING_MESSAGE_CHUNK" -> fail("Chunks must be normalized first")
            else -> Unit // state, raw, custom and future event types have no bracketed entity.
        }
    }
    fun finish() { if (!terminal) fail("SSE ended without terminal event") }
}
