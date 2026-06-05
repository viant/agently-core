package com.viant.agentlysdk

import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import kotlinx.serialization.json.JsonElement
import java.net.URLEncoder
import java.nio.charset.StandardCharsets

suspend fun AgentlyClient.fetchForgeWindowMetadata(
    windowKey: String,
    targetContext: MetadataTargetContext? = null
): JsonElement = withContext(Dispatchers.IO) {
    val path = appendForgeWindowQuery(
        "/v1/api/agently/forge/window/${encodeForgeWindowSegment(windowKey)}",
        forgeWindowTargetQuery(targetContext)
    )
    getJson(path)
}

private fun forgeWindowTargetQuery(targetContext: MetadataTargetContext?): Map<String, String> {
    val query = linkedMapOf<String, String>()
    targetContext?.platform?.takeIf { it.isNotBlank() }?.let { query["platform"] = it }
    targetContext?.formFactor?.takeIf { it.isNotBlank() }?.let { query["formFactor"] = it }
    targetContext?.surface?.takeIf { it.isNotBlank() }?.let { query["surface"] = it }
    targetContext?.capabilities
        ?.map { it.trim() }
        ?.filter { it.isNotEmpty() }
        ?.takeIf { it.isNotEmpty() }
        ?.let { query["capabilities"] = it.joinToString(",") }
    return query
}

private fun appendForgeWindowQuery(path: String, params: Map<String, String>): String {
    if (params.isEmpty()) {
        return path
    }
    val query = params.entries.joinToString("&") { (key, value) ->
        "${forgeWindowUrlEncode(key)}=${forgeWindowUrlEncode(value)}"
    }
    return "$path?$query"
}

private fun encodeForgeWindowSegment(value: String): String =
    forgeWindowUrlEncode(value).replace("+", "%20")

private fun forgeWindowUrlEncode(value: String): String =
    URLEncoder.encode(value, StandardCharsets.UTF_8.toString())
