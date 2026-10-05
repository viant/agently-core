package com.viant.agentlysdk.agui

import kotlinx.serialization.json.*

    fun AgUiSnapshot.nextInput(runId: String, forwardedProps: JsonElement? = null, tools: JsonArray? = null, context: JsonArray? = null, resume: JsonArray? = null, includeActivityMessages: Boolean = false, responseValidator: ((JsonElement, JsonElement) -> Unit)? = null): AgUiRunInput {
        if (terminalEvent == null) fail("Cannot construct continuation during active run")
        validateResume(resume, responseValidator)
        val next = input.value.toMutableMap()
        next["runId"] = jstring(runId)
        next["messages"] = JsonArray(messages.filter { includeActivityMessages || it.role != "activity" }.map { it.value })
        if (state == JsonNull) next.remove("state") else next["state"] = state
        forwardedProps?.let { next["forwardedProps"] = it }; tools?.let { next["tools"] = it }; context?.let { next["context"] = it }
        next.remove("resume"); resume?.let { next["resume"] = it }
        return AgUiRunInput(JsonObject(next))
    }
    private fun AgUiSnapshot.validateResume(resume: JsonArray?, validator: ((JsonElement, JsonElement) -> Unit)?) {
        val entries = resume.orEmpty().map { it.obj() }
        val expected = pendingInterrupts.map { it.obj().requiredString("id") }.toSet()
        val ids = entries.map { it.requiredString("interruptId") }
        if (ids.size != ids.toSet().size || ids.toSet() != expected) fail("Provide exactly one response for every pending interrupt")
        for (interrupt in pendingInterrupts) {
            val i = interrupt.obj(); val id = i.requiredString("id")
            val entry = entries.find { it.string("interruptId") == id } ?: fail("Pending interrupt $id is not addressed by resume")
            AgUiSchema.validate(entry,"ResumeEntry")
            if (entry.string("status")=="resolved" && i["responseSchema"]!=null) {
                val payload=entry["payload"] ?: fail("Interrupt requires response payload")
                if (validator!=null) validator(payload,i.getValue("responseSchema")) else AgUiSchema.validateResponse(payload,i.getValue("responseSchema"))
            }
            val expiry = i.string("expiresAt")?.let { runCatching { java.time.Instant.parse(it) }.getOrNull() }
            if (expiry != null && !expiry.isAfter(java.time.Instant.now()) && entry.string("status") != "cancelled") fail("Expired interrupt $id must be cancelled")
        }
    }
    /** Caller executes advertised client tools, then returns these typed results in a new run. */
    fun AgUiSnapshot.toolResultsInput(runId: String, results: List<AgUiMessage>, forwardedProps: JsonElement? = null, includeActivityMessages: Boolean = false): AgUiRunInput {
        if (terminalEvent?.type != "RUN_FINISHED" || (terminalEvent.value.get("outcome") as? JsonObject)?.string("type")?.let { it != "success" } == true) fail("Client tool continuation requires a successful run")
        val pending = pendingToolCallIds.toSet()
        val resultIds = results.map { if (it.role != "tool") fail("Expected tool message"); it.value.requiredString("toolCallId") }
        if (resultIds.size != resultIds.toSet().size || resultIds.toSet() != pending) fail("Provide exactly one result for every pending client tool")
        val next = nextInput(runId, forwardedProps, includeActivityMessages = includeActivityMessages)
        val history = next.value.getValue("messages").jsonArray.map { AgUiMessage(it.obj()) }.toMutableList()
        for (result in results) {
            val callId = result.value.requiredString("toolCallId")
            val owner = history.indexOfFirst { (it.value["toolCalls"] as? JsonArray).orEmpty().any { it.obj().string("id") == callId } }
            if (owner < 0) fail("Pending client tool has no assistant owner")
            var position = owner + 1
            while (position < history.size && history[position].role == "tool") position++
            history.add(position, result)
        }
        return AgUiRunInput(next.value.with("messages" to JsonArray(history.map { it.value })))
    }
