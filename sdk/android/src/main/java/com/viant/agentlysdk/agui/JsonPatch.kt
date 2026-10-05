package com.viant.agentlysdk.agui

import kotlinx.serialization.json.*

/** RFC 6902; operations are transactional because JSON trees are immutable. */
object AgUiJsonPatch {
    fun apply(document: JsonElement, operations: JsonArray): JsonElement {
        AgUiSchema.validate(operations, "JsonPatch")
        var result = document
        for (raw in operations) {
            val op = raw.obj(); val path = tokens(op.requiredString("path"))
            when (op.requiredString("op")) {
                "add" -> result = edit(result, path, "add", op.getValue("value"))
                "replace" -> result = edit(result, path, "replace", op.getValue("value"))
                "remove" -> result = edit(result, path, "remove", null)
                "copy", "move" -> {
                    val from = tokens(op.requiredString("from"))
                    if (op.requiredString("op") == "move" && path.size > from.size && path.take(from.size) == from) fail("Cannot move into own descendant")
                    val value = get(result, from)
                    if (op.requiredString("op") == "move") result = edit(result, from, "remove", null)
                    result = edit(result, path, "add", value)
                }
                "test" -> if (!equivalent(get(result, path), op.getValue("value"))) fail("JSON patch test failed at ${op.requiredString("path")}")
            }
        }
        return result
    }
    internal fun equivalent(a: JsonElement, b: JsonElement): Boolean {
        if (a is JsonPrimitive && b is JsonPrimitive && !a.isString && !b.isString) {
            if (AgUiNumbers.valid(a.content) && AgUiNumbers.valid(b.content)) return AgUiNumbers.compare(a.content, b.content) == 0
        }
        if (a is JsonArray && b is JsonArray) return a.size == b.size && a.indices.all { equivalent(a[it], b[it]) }
        if (a is JsonObject && b is JsonObject) return a.keys == b.keys && a.all { (k,v) -> equivalent(v, b.getValue(k)) }
        return a == b
    }
    private fun tokens(pointer: String): List<String> {
        if (pointer.isEmpty()) return emptyList()
        if (!Regex("^(/([^/~]|~[01])*)*$").matches(pointer)) fail("Invalid JSON pointer")
        return pointer.substring(1).split('/').map { it.replace("~1", "/").replace("~0", "~") }
    }
    private fun index(token: String, size: Int, append: Boolean): Int {
        if (token == "-" && append) return size
        if (!Regex("0|[1-9][0-9]*").matches(token)) fail("Invalid array index $token")
        val value = token.toIntOrNull() ?: fail("Array index overflow")
        if (value < 0 || value > size || (!append && value == size)) fail("Array index out of bounds")
        return value
    }
    private fun get(value: JsonElement, path: List<String>): JsonElement {
        if (path.isEmpty()) return value
        val next = when (value) {
            is JsonObject -> value[path.first()] ?: fail("Missing JSON pointer member")
            is JsonArray -> value[index(path.first(), value.size, false)]
            else -> fail("Cannot traverse scalar")
        }
        return get(next, path.drop(1))
    }
    private fun edit(value: JsonElement, path: List<String>, operation: String, replacement: JsonElement?): JsonElement {
        if (path.isEmpty()) return if (operation == "remove") JsonNull else replacement!!
        val token = path.first(); val rest = path.drop(1)
        return when (value) {
            is JsonObject -> JsonObject(value.toMutableMap().apply {
                if (rest.isNotEmpty()) put(token, edit(value[token] ?: fail("Missing parent"), rest, operation, replacement))
                else when (operation) {
                    "add" -> put(token, replacement!!)
                    "replace" -> { if (!containsKey(token)) fail("Missing replace member"); put(token, replacement!!) }
                    "remove" -> { if (!containsKey(token)) fail("Missing remove member"); remove(token) }
                }
            })
            is JsonArray -> JsonArray(value.toMutableList().apply {
                val i = index(token, size, rest.isEmpty() && operation == "add")
                if (rest.isNotEmpty()) this[i] = edit(this[i], rest, operation, replacement)
                else when (operation) { "add" -> add(i, replacement!!); "replace" -> this[i] = replacement!!; "remove" -> removeAt(i) }
            })
            else -> fail("Cannot edit scalar child")
        }
    }
}
