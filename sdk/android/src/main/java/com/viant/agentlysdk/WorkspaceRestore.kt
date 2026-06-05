package com.viant.agentlysdk

import com.viant.agentlysdk.stream.ConversationStreamSnapshot
import com.viant.agentlysdk.stream.LiveExecutionGroup
import com.viant.agentlysdk.stream.LiveToolStepState
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.booleanOrNull
import kotlinx.serialization.json.contentOrNull

fun deriveHostedWorkspaceRestoreState(
    state: ConversationStateResponse
): HostedWorkspaceRestoreState? {
    val lastTurn = state.conversation?.turns.orEmpty().lastOrNull() ?: return null
    return deriveHostedWorkspaceRestoreStateFromToolSteps(
        lastTurn.execution?.pages.orEmpty().flatMap { it.toolSteps }
    )
}

fun deriveHostedWorkspaceRestoreState(
    state: ConversationStateResponse?,
    streamSnapshot: ConversationStreamSnapshot?
): HostedWorkspaceRestoreState? {
    if (streamSnapshot != null && !streamSnapshot.activeTurnId.isNullOrBlank()) {
        return deriveHostedWorkspaceRestoreState(streamSnapshot)
    }
    if (streamSnapshot != null && streamSnapshot.liveExecutionGroupsById.isNotEmpty()) {
        return null
    }
    return state?.let(::deriveHostedWorkspaceRestoreState)
}

fun deriveHostedWorkspaceRestoreState(
    snapshot: ConversationStreamSnapshot
): HostedWorkspaceRestoreState? {
    val targetTurnId = snapshot.activeTurnId?.trim().orEmpty()
    if (targetTurnId.isBlank()) {
        return null
    }
    val groups = liveHostedWorkspaceGroups(snapshot, targetTurnId)
    if (groups.isEmpty()) {
        return null
    }
    return deriveHostedWorkspaceRestoreStateFromToolSteps(
        groups.flatMap { group -> group.toolSteps.mapNotNull(::liveToolStepToToolStepState) }
    )
}

private fun deriveHostedWorkspaceRestoreStateFromToolSteps(
    toolSteps: List<ToolStepState>
): HostedWorkspaceRestoreState? {
    val completedToolSteps = toolSteps
        .filter { it.status?.trim()?.lowercase() == "completed" }
    for (index in completedToolSteps.indices.reversed()) {
        val step = completedToolSteps[index]
        when (normalizeToolName(step.toolName)) {
            "ui/window/list" -> {
                val windows = hostedWorkspaceWindowsFromListPayload(
                    firstParsedPayload(step.responsePayload, step.content)
                )
                if (windows.isNotEmpty()) {
                    val restoredWindows = applyWindowFormDataPatches(
                        windows = windows,
                        toolSteps = completedToolSteps.drop(index + 1)
                    )
                    return HostedWorkspaceRestoreState(
                        windows = restoredWindows,
                        selectedWindowId = selectedWindowIdFromToolSteps(completedToolSteps, restoredWindows)
                            .ifBlank { null }
                    )
                }
            }
            "ui/view/open" -> {
                val windows = hostedWorkspaceWindowsFromViewOpenStep(step)
                if (windows.isNotEmpty()) {
                    val restoredWindows = applyWindowFormDataPatches(
                        windows = windows,
                        toolSteps = completedToolSteps.drop(index + 1)
                    )
                    val response = firstParsedPayload(step.responsePayload, step.content) as? JsonObject
                    val selectedWindowId = jsonString(response?.get("selectedWindowId"))
                        .ifBlank { restoredWindows.lastOrNull()?.windowId.orEmpty() }
                    return HostedWorkspaceRestoreState(
                        windows = restoredWindows,
                        selectedWindowId = selectedWindowId.ifBlank { null }
                    )
                }
            }
        }
    }
    return null
}

private fun liveHostedWorkspaceGroups(
    snapshot: ConversationStreamSnapshot,
    targetTurnId: String
): List<LiveExecutionGroup> {
    return snapshot.liveExecutionGroupsById.values
        .filter { group -> group.turnId == targetTurnId }
        .sortedWith(
            compareBy<LiveExecutionGroup> { it.sequence ?: Int.MAX_VALUE }
                .thenBy { it.iteration ?: Int.MAX_VALUE }
                .thenBy { it.pageId }
        )
}

private fun liveToolStepToToolStepState(step: LiveToolStepState): ToolStepState? {
    val toolCallId = step.toolCallId?.trim()
        ?: step.toolMessageId?.trim()
        ?: step.toolName?.trim()
        ?: return null
    val toolName = step.toolName?.trim()?.takeIf { it.isNotEmpty() } ?: return null
    return ToolStepState(
        toolCallId = toolCallId,
        toolMessageId = step.toolMessageId,
        toolName = toolName,
        status = step.status,
        requestPayloadId = step.requestPayloadId,
        responsePayloadId = step.responsePayloadId,
        requestPayload = step.requestPayload,
        responsePayload = step.responsePayload,
        linkedConversationId = step.linkedConversationId,
        linkedConversationAgentId = step.linkedConversationAgentId,
        linkedConversationTitle = step.linkedConversationTitle
    )
}

private fun normalizeToolName(raw: String?): String {
    return raw.orEmpty().trim().lowercase().replace(":", "/")
}

private fun firstParsedPayload(rawPayload: JsonElement?, rawText: String?): JsonElement? {
    val candidates = listOfNotNull(rawPayload, rawText?.takeIf { it.isNotBlank() }?.let(::JsonPrimitive))
    candidates.forEach { candidate ->
        val parsed = parsePayload(candidate) ?: return@forEach
        if (!isPayloadEnvelope(parsed)) {
            return parsed
        }
    }
    return null
}

private fun parsePayload(raw: JsonElement): JsonElement? {
    return when (raw) {
        is JsonPrimitive -> {
            val text = raw.contentOrNull?.trim().orEmpty()
            if (text.isBlank()) {
                null
            } else {
                runCatching { Json.parseToJsonElement(text) }.getOrNull()
            }
        }
        is JsonObject -> {
            val inlineBody = raw["inlineBody"] ?: raw["InlineBody"]
            if (inlineBody is JsonPrimitive) {
                val text = inlineBody.contentOrNull?.trim().orEmpty()
                if (text.isNotBlank()) {
                    return runCatching { Json.parseToJsonElement(text) }.getOrNull() ?: raw
                }
            }
            raw
        }
        else -> raw
    }
}

private fun isPayloadEnvelope(value: JsonElement): Boolean {
    val obj = value as? JsonObject ?: return false
    val hasInlineBody = jsonString(obj["inlineBody"]).isNotBlank() || jsonString(obj["InlineBody"]).isNotBlank()
    val hasCompression = jsonString(obj["compression"]).isNotBlank() || jsonString(obj["Compression"]).isNotBlank()
    val hasDirectWorkspaceShape = obj["items"] != null || obj["windowId"] != null || obj["focusedWindowId"] != null
    return (hasInlineBody || hasCompression) && !hasDirectWorkspaceShape
}

private fun hostedWorkspaceWindowsFromListPayload(raw: JsonElement?): List<WorkspaceWindowSnapshot> {
    val payload = raw as? JsonObject ?: return emptyList()
    val items = payload["items"] as? JsonArray ?: return emptyList()
    return items.mapNotNull { normalizeHostedWorkspaceWindow(it as? JsonObject) }
}

private fun hostedWorkspaceWindowsFromViewOpenStep(
    step: ToolStepState
): List<WorkspaceWindowSnapshot> {
    val response = firstParsedPayload(step.responsePayload, step.content) as? JsonObject ?: return emptyList()
    val request = firstParsedPayload(step.requestPayload, null) as? JsonObject
    val items = response["items"] as? JsonArray
    if (items != null && items.isNotEmpty()) {
        return items.mapNotNull { normalizeHostedWorkspaceWindow(it as? JsonObject) }
    }
    val normalized = normalizeHostedWorkspaceWindow(
        JsonObject(
            buildMap {
                put("windowId", JsonPrimitive(jsonString(response["windowId"])))
                put("conversationId", JsonPrimitive(jsonString(response["conversationId"])))
                put("windowKey", JsonPrimitive(jsonString(response["windowKey"]).ifBlank { jsonString(request?.get("id")) }))
                put("windowTitle", JsonPrimitive(jsonString(response["windowTitle"])))
                put("presentation", JsonPrimitive(jsonString(response["presentation"])))
                put("region", JsonPrimitive(jsonString(response["region"])))
                put("parentKey", JsonPrimitive(jsonString(response["parentKey"])))
                response["workspaceSharePct"]?.let { put("workspaceSharePct", it) }
                response["workspaceMinHeight"]?.let { put("workspaceMinHeight", it) }
                response["inTab"]?.let { put("inTab", it) }
                request?.get("parameters")?.let { put("parameters", it) }
                response["windowForm"]?.let { put("windowForm", it) }
            }
        )
    )
    return listOfNotNull(normalized)
}

private fun applyWindowFormDataPatches(
    windows: List<WorkspaceWindowSnapshot>,
    toolSteps: List<ToolStepState>
): List<WorkspaceWindowSnapshot> {
    var restored = windows
    toolSteps.forEach { step ->
        if (normalizeToolName(step.toolName) != "ui/window/setformdata") {
            return@forEach
        }
        val request = firstParsedPayload(step.requestPayload, null) as? JsonObject
        val response = firstParsedPayload(step.responsePayload, step.content) as? JsonObject
        val windowId = jsonString(response?.get("windowId"))
            .ifBlank { jsonString(request?.get("windowId")) }
        if (windowId.isBlank()) {
            return@forEach
        }
        val authoritative = response?.get("windowForm") as? JsonObject
        val patch = authoritative
            ?: (request?.get("values") as? JsonObject)
            ?: (request?.get("parameters") as? JsonObject)
            ?: return@forEach
        val replace = authoritative != null || request?.get("replace")?.let(::jsonBoolean) == true
        restored = restored.map { window ->
            if (window.windowId != windowId) {
                window
            } else {
                val nextForm = if (replace) {
                    patch
                } else {
                    mergeJsonObjects(window.windowForm, patch)
                }
                window.copy(windowForm = nextForm)
            }
        }
    }
    return restored
}

private fun normalizeHostedWorkspaceWindow(raw: JsonObject?): WorkspaceWindowSnapshot? {
    raw ?: return null
    val parentKey = jsonString(raw["parentKey"])
    val windowId = jsonString(raw["windowId"])
    val windowKey = jsonString(raw["windowKey"])
    if (windowId.isBlank() || windowKey.isBlank()) return null
    return WorkspaceWindowSnapshot(
        windowId = windowId,
        conversationId = jsonString(raw["conversationId"]).ifBlank { null },
        windowKey = windowKey,
        windowTitle = jsonString(raw["windowTitle"]).ifBlank { windowKey },
        presentation = jsonString(raw["presentation"]).ifBlank { null },
        region = jsonString(raw["region"]).ifBlank { null },
        parentKey = parentKey,
        workspaceSharePct = raw["workspaceSharePct"]?.let(::jsonInt),
        workspaceMinHeight = raw["workspaceMinHeight"]?.let(::jsonInt),
        inTab = raw["inTab"]?.let(::jsonBoolean) ?: true,
        parameters = raw["parameters"] as? JsonObject,
        windowForm = raw["windowForm"] as? JsonObject
    )
}

private fun mergeJsonObjects(base: JsonObject?, patch: JsonObject): JsonObject {
    val merged = base?.toMutableMap() ?: mutableMapOf()
    patch.forEach { (key, value) ->
        val baseValue = merged[key] as? JsonObject
        val patchValue = value as? JsonObject
        merged[key] = if (baseValue != null && patchValue != null) {
            mergeJsonObjects(baseValue, patchValue)
        } else {
            value
        }
    }
    return JsonObject(merged)
}

private fun selectedWindowIdFromToolSteps(
    toolSteps: List<ToolStepState>,
    windows: List<WorkspaceWindowSnapshot>
): String {
    val windowIds = windows.map { it.windowId }.toSet()
    toolSteps.asReversed().forEach { step ->
        when (normalizeToolName(step.toolName)) {
            "ui/window/show" -> {
                val request = firstParsedPayload(step.requestPayload, null) as? JsonObject
                val windowId = jsonString(request?.get("windowId"))
                if (windowId in windowIds) return windowId
            }
            "ui/window/list" -> {
                val response = firstParsedPayload(step.responsePayload, step.content) as? JsonObject
                val focused = jsonString(response?.get("focusedWindowId"))
                if (focused in windowIds) return focused
            }
        }
    }
    return ""
}

private fun jsonString(value: JsonElement?): String {
    return (value as? JsonPrimitive)?.contentOrNull?.trim().orEmpty()
}

private fun jsonBoolean(value: JsonElement): Boolean? {
    return (value as? JsonPrimitive)?.booleanOrNull
}

private fun jsonInt(value: JsonElement): Int? {
    val primitive = value as? JsonPrimitive ?: return null
    return primitive.contentOrNull?.trim()?.toIntOrNull()
}
