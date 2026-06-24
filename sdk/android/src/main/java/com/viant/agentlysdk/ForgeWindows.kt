package com.viant.agentlysdk

import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import kotlinx.serialization.json.JsonElement

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

private fun normalizedForgeWindowKey(value: String): String {
    val key = value.trim()
    require(key.isNotEmpty()) { "window key is required" }
    return key
}

private fun encodeForgeWindowSegment(value: String): String =
    targetPathSegmentEncode(value)
