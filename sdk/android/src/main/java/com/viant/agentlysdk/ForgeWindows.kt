package com.viant.agentlysdk

import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import kotlinx.serialization.json.JsonElement
import java.net.URLEncoder
import java.nio.charset.StandardCharsets

suspend fun AgentlyClient.fetchForgeWindowMetadata(
    windowKey: String
): JsonElement = withContext(Dispatchers.IO) {
    val path = "/v1/api/agently/forge/window/${encodeForgeWindowSegment(windowKey)}"
    getJson(path)
}

private fun encodeForgeWindowSegment(value: String): String =
    URLEncoder.encode(value, StandardCharsets.UTF_8.toString()).replace("+", "%20")
