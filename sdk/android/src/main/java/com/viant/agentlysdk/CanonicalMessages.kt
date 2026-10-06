package com.viant.agentlysdk

import com.viant.agentlysdk.stream.isInternalMessageMode

/** User-facing assistant messages, keyed by canonical ID rather than content. */
fun canonicalAssistantMessages(turn: TurnState): List<TurnMessageState> {
    val internalIds = canonicalInternalAssistantMessageIds(turn)
    val ordered = turn.messages.filter { it.role == "assistant" && it.messageId.isNotBlank() && it.messageId !in internalIds }.toMutableList()
    // Canonical input order is durable. Repeated projections enrich one identity.
    val messages = linkedMapOf<String, TurnMessageState>()
    ordered.forEach { value ->
        val prior = messages[value.messageId]
        messages[value.messageId] = prior?.copy(content = value.content ?: prior.content,
            renderedContent = value.renderedContent ?: prior.renderedContent, createdAt = value.createdAt ?: prior.createdAt,
            sequence = value.sequence ?: prior.sequence, interim = value.interim ?: prior.interim,
            mode = value.mode ?: prior.mode, status = value.status ?: prior.status) ?: value
    }
    fun merge(value: AssistantMessageState?) {
        if (value == null || value.messageId in internalIds || value.messageId.isBlank()) return
        val prior = messages[value.messageId]
        messages[value.messageId] = prior?.copy(
            content = value.content?.takeIf { it.isNotBlank() } ?: prior.content,
            renderedContent = value.renderedContent ?: prior.renderedContent,
            createdAt = prior.createdAt ?: value.createdAt
        ) ?: TurnMessageState(value.messageId, "assistant", value.content, value.renderedContent, value.createdAt, status = turn.status)
    }
    turn.assistant?.messages.orEmpty().forEach(::merge)
    val finalId = turn.assistant?.final?.messageId
    val narration = turn.assistant?.narration
    if (narration != null && narration.messageId !in messages && narration.messageId !in internalIds) {
        // Aggregates can omit timestamps/sequence. Their narration precedes final.
        val final = finalId?.let { messages.remove(it) }
        merge(narration)
        if (final != null) messages[final.messageId] = final
    } else merge(narration)
    merge(turn.assistant?.final)
    return messages.values.toList()
}

internal fun canonicalInternalAssistantMessageIds(turn: TurnState): Set<String> = buildSet {
    turn.messages.filter { isInternalMessageMode(it.mode) }.forEach { add(it.messageId) }
    turn.execution?.pages.orEmpty().forEach { page ->
        val hidden = isInternalMessageMode(page.mode)
        if (hidden) page.assistantMessageId?.let(::add)
        page.modelSteps.filter { isInternalMessageMode(it.mode) || it.mode.isNullOrEmpty() && (hidden || isInternalMessageMode(it.executionRole)) }.forEach { it.assistantMessageId?.let(::add) }
    }
}
