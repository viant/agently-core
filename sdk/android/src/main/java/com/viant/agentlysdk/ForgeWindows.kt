package com.viant.agentlysdk

import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonObject

data class ApplyPermissionInput(
    val conversationId: String,
    val resource: Map<String, JsonElement>,
    val windowParams: Map<String, JsonElement>? = null,
    val targetContext: MetadataTargetContext? = null
)

suspend fun AgentlyClient.getForgeWindowMetadata(
    windowKey: String,
    targetContext: MetadataTargetContext? = null
): JsonElement = fetchForgeWindowMetadata(windowKey, targetContext)

suspend fun AgentlyClient.fetchForgeWindowMetadata(
    windowKey: String,
    targetContext: MetadataTargetContext? = null
): JsonElement = withContext(Dispatchers.IO) {
    val key = normalizedForgeWindowKey(windowKey)
    val path = appendRepeatedQuery(
        "/v1/api/agently/forge/window/${encodeForgeWindowSegment(key)}",
        targetContext.toTargetQuery()
    )
    getJson(path)
}

suspend fun AgentlyClient.applyPermission(
    windowKey: String,
    input: ApplyPermissionInput
): JsonElement = withContext(Dispatchers.IO) {
    val key = normalizedForgeWindowKey(windowKey)
    val query = input.targetContext.toTargetQuery().toMutableList()
    query += "applyPermission" to "true"
    input.conversationId.trim().takeIf { it.isNotEmpty() }?.let { query += "conversationId" to it }
    if (input.resource.isNotEmpty()) query += "resource" to JsonObject(input.resource).toString()
    input.windowParams?.let { query += "windowParams" to JsonObject(it).toString() }
    val path = appendRepeatedQuery(
        "/v1/api/agently/forge/window/${encodeForgeWindowSegment(key)}",
        query
    )
    val root = getJson(path)
    (root as? JsonObject)?.get("data") ?: root
}

private fun normalizedForgeWindowKey(value: String): String {
    val key = value.trim()
    require(key.isNotEmpty()) { "window key is required" }
    return key
}

private fun encodeForgeWindowSegment(value: String): String =
    targetPathSegmentEncode(value)
