package com.viant.agentlysdk

import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonNull
import kotlinx.serialization.json.JsonObject

object FeedPatch {
    fun apply(input: JsonElement, operations: List<FeedPatchOperation>): JsonElement =
        operations.fold(input) { state, operation -> patch(state, tokens(operation.path), 0, operation) }

    private fun tokens(path: String): List<String> = path.split('/').drop(1)
        .map { it.replace("~1", "/").replace("~0", "~") }

    private fun patch(node: JsonElement, path: List<String>, index: Int, operation: FeedPatchOperation): JsonElement {
        if (index >= path.size) return operation.value ?: JsonNull
        val key = path[index]
        val last = index == path.lastIndex
        return when (node) {
            is JsonArray -> {
                val values = node.toMutableList()
                if (last) {
                    when (operation.op.lowercase()) {
                        "remove" -> key.toIntOrNull()?.takeIf { it in values.indices }?.let(values::removeAt)
                        "add" -> if (key == "-") values.add(operation.value ?: JsonNull) else key.toIntOrNull()?.let { values.add(it.coerceIn(0, values.size), operation.value ?: JsonNull) }
                        else -> key.toIntOrNull()?.takeIf { it in values.indices }?.let { values[it] = operation.value ?: JsonNull }
                    }
                } else key.toIntOrNull()?.takeIf { it in values.indices }?.let { values[it] = patch(values[it], path, index + 1, operation) }
                JsonArray(values)
            }
            is JsonObject -> {
                val values = node.toMutableMap()
                if (last && operation.op.equals("remove", true)) values.remove(key)
                else if (last) values[key] = operation.value ?: JsonNull
                else values[key] = patch(values[key] ?: JsonObject(emptyMap()), path, index + 1, operation)
                JsonObject(values)
            }
            else -> patch(JsonObject(emptyMap()), path, index, operation)
        }
    }
}
