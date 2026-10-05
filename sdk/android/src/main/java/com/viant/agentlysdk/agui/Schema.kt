package com.viant.agentlysdk.agui

import kotlinx.serialization.json.*

class AgUiProtocolException(message: String) : IllegalArgumentException(message)
internal fun fail(message: String): Nothing = throw AgUiProtocolException(message)
internal fun JsonElement.obj(): JsonObject = this as? JsonObject ?: fail("Expected JSON object")
internal fun JsonObject.string(key: String): String? = (get(key) as? JsonPrimitive)?.takeIf { it.isString }?.content
internal fun JsonObject.requiredString(key: String): String = string(key) ?: fail("Missing string $key")
internal fun JsonObject.with(vararg fields: Pair<String, JsonElement?>): JsonObject = JsonObject(toMutableMap().apply { for ((key, value) in fields) { if (value == null) remove(key) else put(key, value) } })
internal fun jstring(value: String): JsonPrimitive = JsonPrimitive(value)

/** Validator for exactly the keywords used by the bundled protocol 1.0 schema. */
object AgUiSchema {
    private val root: JsonObject by lazy {
        val stream = AgUiSchema::class.java.getResourceAsStream("/agui/schema-1.0.json") ?: error("Missing AG-UI schema resource")
        stream.use { Json.parseToJsonElement(it.bufferedReader().readText()).obj() }
    }
    fun validate(value: JsonElement, definition: String, tolerateUnknownFields: Boolean = false) {
        val schema = root["\$defs"]!!.obj()[definition] ?: fail("Unknown schema definition $definition")
        evaluate(value, schema, "$definition", tolerateUnknownFields)
    }
    /** Fail closed when an application schema needs unsupported JSON Schema keywords. */
    fun validateResponse(value: JsonElement, schema: JsonElement) {
        checkResponseSchema(schema, schema as? JsonObject ?: root)
        evaluate(value, schema, "response", false, schema as? JsonObject ?: root)
    }
    private fun checkResponseSchema(schema: JsonElement, document: JsonObject) {
        if (schema == JsonPrimitive(true) || schema == JsonPrimitive(false)) return
        schema.obj()["\$ref"]?.let {reference->
            val text=(reference as? JsonPrimitive)?.takeIf {it.isString}?.content
            if(text==null || !text.startsWith("#/\$defs/") || (document["\$defs"] as? JsonObject)?.get(text.removePrefix("#/\$defs/"))==null)fail("Unsupported response schema reference; supply an application validator")
        }
        val supported = setOf("\$schema","\$id","\$defs","\$ref","\$comment","title","description","default","examples","deprecated","readOnly","writeOnly","type","const","enum","not","allOf","oneOf","anyOf","required","properties","additionalProperties","unevaluatedProperties","items","minItems","maxItems","minLength","maxLength","pattern","minimum","maximum")
        for ((key,value) in schema.obj()) {
            if (key=="type" && (value as? JsonPrimitive)?.isString != true && (value as? JsonArray)?.all { (it as? JsonPrimitive)?.isString==true } != true) fail("Invalid response schema type union")
            if (key !in supported) fail("Unsupported response schema keyword $key; supply an application validator")
            if (key in setOf("properties","\$defs")) value.obj().values.forEach { checkResponseSchema(it, document) }
            if (key in setOf("not","items","additionalProperties")) checkResponseSchema(value, document)
            if (key in setOf("allOf","oneOf","anyOf")) value.jsonArray.forEach {checkResponseSchema(it, document)}
        }
    }
    private fun evaluate(value: JsonElement, schema: JsonElement, path: String, tolerant: Boolean, schemaRoot: JsonObject = root, depth: Int = 0): Set<String> {
        if (depth > 256) fail("Schema recursion limit exceeded")
        if (schema == JsonPrimitive(true)) return (value as? JsonObject)?.keys ?: emptySet()
        if (schema == JsonPrimitive(false)) fail("$path is forbidden")
        val s = schema.obj()
        val evaluated = mutableSetOf<String>()
        s.string("\$ref")?.let { ref ->
            val name = ref.removePrefix("#/\$defs/")
            evaluated += evaluate(value, schemaRoot["\$defs"]?.obj()?.get(name)?.takeIf { ref.startsWith("#/\$defs/") } ?: fail("Invalid schema reference"), path, tolerant, schemaRoot, depth + 1)
        }
        (s["not"] as? JsonObject)?.let { not -> if (runCatching { evaluate(value, not, path, tolerant, schemaRoot, depth + 1) }.isSuccess) fail("$path fails not constraint") }
        for (branch in (s["allOf"] as? JsonArray).orEmpty()) evaluated += evaluate(value, branch, path, tolerant, schemaRoot, depth + 1)
        (s["oneOf"] as? JsonArray)?.let { branches ->
            val matches = branches.mapNotNull { runCatching { evaluate(value, it, path, tolerant, schemaRoot, depth + 1) }.getOrNull() }
            if (matches.size != 1) fail("$path must match exactly one schema (matched ${matches.size})")
            evaluated += matches.single()
        }
        (s["anyOf"] as? JsonArray)?.let { branches ->
            val matches=branches.mapNotNull {runCatching {evaluate(value,it,path,tolerant,schemaRoot,depth+1)}.getOrNull()}
            if(matches.isEmpty()) fail("$path fails anyOf");matches.forEach {evaluated+=it}
        }
        (s["type"] as? JsonArray)?.let {types->if(types.none {runCatching {evaluate(value,s.with("type" to it),path,tolerant,schemaRoot,depth+1)}.isSuccess})fail("$path fails type union")}
        s.string("type")?.let { type ->
            val valid = when (type) {
                "object" -> value is JsonObject
                "array" -> value is JsonArray
                "string" -> value is JsonPrimitive && value.isString
                "boolean" -> value is JsonPrimitive && !value.isString && value.booleanOrNull != null
                "null" -> value == JsonNull
                "number", "integer" -> value is JsonPrimitive && !value.isString && AgUiNumbers.valid(value.content) && (type != "integer" || AgUiNumbers.integral(value.content))
                else -> fail("Unsupported schema type $type")
            }
            if (!valid) fail("$path must be $type")
        }
        if (s.containsKey("const") && !AgUiJsonPatch.equivalent(value, s.getValue("const"))) fail("$path fails const")
        (s["enum"] as? JsonArray)?.let { values -> if (values.none { AgUiJsonPatch.equivalent(value, it) }) fail("$path fails enum") }
        if (value is JsonObject) {
            for (required in (s["required"] as? JsonArray).orEmpty()) if (!value.containsKey(required.jsonPrimitive.content)) fail("$path missing ${required.jsonPrimitive.content}")
            val properties = s["properties"] as? JsonObject ?: JsonObject(emptyMap())
            for ((key, property) in properties) if (value.containsKey(key)) { evaluate(value.getValue(key), property, "$path/$key", tolerant, schemaRoot, depth + 1); evaluated += key }
            s["additionalProperties"]?.let { additional ->
                for ((key, child) in value) if (!properties.containsKey(key)) {
                    if (additional != JsonPrimitive(false) || !tolerant) evaluate(child, additional, "$path/$key", tolerant, schemaRoot, depth + 1)
                    evaluated += key
                }
            }
            if (s["unevaluatedProperties"] == JsonPrimitive(false) && !tolerant) {
                val extra = value.keys - evaluated
                if (extra.isNotEmpty()) fail("$path unknown fields: $extra")
            }
        }
        if (value is JsonArray) {
            s["minItems"]?.jsonPrimitive?.int?.let { if (value.size < it) fail("$path too few items") }
            s["maxItems"]?.jsonPrimitive?.int?.let {if(value.size>it)fail("$path too many items")}
            s["items"]?.let { items -> value.forEachIndexed { i, child -> evaluate(child, items, "$path/$i", tolerant, schemaRoot, depth + 1) } }
        }
        if(value is JsonPrimitive && value.isString){
            val count=value.content.codePointCount(0,value.content.length)
            s["minLength"]?.jsonPrimitive?.int?.let {if(count<it)fail("$path too short")}
            s["maxLength"]?.jsonPrimitive?.int?.let {if(count>it)fail("$path too long")}
        }
        if (value is JsonPrimitive && value.isString) s.string("pattern")?.let { if (!Regex(it).containsMatchIn(value.content)) fail("$path fails pattern") }
        if (value is JsonPrimitive && !value.isString && AgUiNumbers.valid(value.content)) {
            s["minimum"]?.jsonPrimitive?.content?.let { if (AgUiNumbers.compare(value.content, it) < 0) fail("$path below minimum") }
            s["maximum"]?.jsonPrimitive?.content?.let { if (AgUiNumbers.compare(value.content, it) > 0) fail("$path above maximum") }
        }
        return evaluated
    }
}
