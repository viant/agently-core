package com.viant.agentlysdk

import java.net.URLEncoder
import java.nio.charset.StandardCharsets

internal fun MetadataTargetContext?.toTargetQuery(): List<Pair<String, String>> {
    val query = mutableListOf<Pair<String, String>>()
    this?.platform?.trim()?.takeIf { it.isNotEmpty() }?.let { query += "platform" to it }
    this?.formFactor?.trim()?.takeIf { it.isNotEmpty() }?.let { query += "formFactor" to it }
    this?.surface?.trim()?.takeIf { it.isNotEmpty() }?.let { query += "surface" to it }
    this?.capabilities
        ?.map { it.trim() }
        ?.filter { it.isNotEmpty() }
        ?.forEach { query += "capabilities" to it }
    return query
}

internal fun appendRepeatedQuery(path: String, params: List<Pair<String, String>>): String {
    if (params.isEmpty()) {
        return path
    }
    val query = params.joinToString("&") { (key, value) ->
        "${targetQueryUrlEncode(key)}=${targetQueryUrlEncode(value)}"
    }
    return "$path?$query"
}

private fun targetQueryUrlEncode(value: String): String =
    URLEncoder.encode(value, StandardCharsets.UTF_8.toString())

internal fun targetPathSegmentEncode(value: String): String =
    targetQueryUrlEncode(value).replace("+", "%20")
