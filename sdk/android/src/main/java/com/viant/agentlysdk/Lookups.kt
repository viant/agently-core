// Lookups.kt — Android parity for the datasource + overlay feature.
//
// Wire types mirror sdk/api/datasource.go 1:1 so JSON round-trips with the
// Go server without custom coding keys. Pure token helpers port the
// algorithms from ui/src/components/lookups/tokens.js; both flavours (this
// file and the Swift equivalent) are validated by platform-specific unit
// tests that run the same scenarios as lookups-test.mjs T12-T17.
//
// Overlay matching is strictly server-side (see lookups.md §5). Android
// never loads overlay YAML, never evaluates match modes, never composes
// priorities.

package com.viant.agentlysdk

import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import kotlinx.serialization.Serializable
import kotlinx.serialization.builtins.serializer
import kotlinx.serialization.json.JsonElement
import java.net.URLEncoder
import java.nio.charset.StandardCharsets

// region Wire types ---------------------------------------------------------

@Serializable
data class FetchDatasourceInput(
    val id: String,
    val inputs: Map<String, JsonElement>? = null,
    val cache: DatasourceCacheHints? = null
)

@Serializable
data class DatasourceCacheHints(
    val bypassCache: Boolean? = null,
    val writeThrough: Boolean? = null
)

@Serializable
data class FetchDatasourceOutput(
    val rows: List<Map<String, JsonElement>> = emptyList(),
    val dataInfo: Map<String, JsonElement>? = null,
    val cache: DatasourceCacheMeta? = null
)

@Serializable
data class DatasourceCacheMeta(
    val hit: Boolean,
    val stale: Boolean? = null,
    val fetchedAt: String,
    val ttlSeconds: Int? = null
)

@Serializable
data class InvalidateDatasourceCacheInput(
    val id: String,
    val inputsHash: String? = null
)

@Serializable
data class ListLookupRegistryInput(val context: String)

@Serializable
data class LookupTokenFormat(
    val store: String? = null,
    val display: String? = null,
    val modelForm: String? = null,
    val queryInput: String? = null,
    val resolveInput: String? = null
)

@Serializable
data class LookupParameter(
    val from: String? = null,
    val to: String? = null,
    val name: String,
    val location: String? = null
)

@Serializable
data class LookupRegistryEntry(
    val name: String,
    val dataSource: String,
    val trigger: String? = null,
    val required: Boolean? = null,
    val display: String? = null,
    val token: LookupTokenFormat? = null,
    val inputs: List<LookupParameter>? = null,
    val outputs: List<LookupParameter>? = null
)

@Serializable
data class ListLookupRegistryOutput(
    val entries: List<LookupRegistryEntry> = emptyList()
)

// endregion ----------------------------------------------------------------

/**
 * Extension methods on AgentlyClient adding the three datasource/lookup
 * endpoints. These dispatch through the same `internal` generic transport
 * helpers used by every other AgentlyClient method (Client.kt). No custom
 * transport, no fallback throws — what you import is what runs.
 *
 * Dispatchers.IO is used for parity with the rest of Client.kt; the
 * underlying RestClient is blocking.
 */
suspend fun AgentlyClient.fetchDatasource(
    input: FetchDatasourceInput
): FetchDatasourceOutput = withContext(Dispatchers.IO) {
    val path = "/v1/api/datasources/${encodeSegment(input.id)}/fetch"
    post(path, input, FetchDatasourceOutput.serializer())
}

suspend fun AgentlyClient.invalidateDatasourceCache(
    input: InvalidateDatasourceCacheInput
) = withContext(Dispatchers.IO) {
    val base = "/v1/api/datasources/${encodeSegment(input.id)}/cache"
    val path = if (!input.inputsHash.isNullOrEmpty()) {
        base + "?inputsHash=" + URLEncoder.encode(input.inputsHash, StandardCharsets.UTF_8.toString())
    } else base
    // Reuse the existing EmptyResponse declared in Models.kt — no new type.
    delete(path, EmptyResponse.serializer())
    Unit
}

suspend fun AgentlyClient.listLookupRegistry(
    input: ListLookupRegistryInput
): ListLookupRegistryOutput = withContext(Dispatchers.IO) {
    val q = URLEncoder.encode(input.context, StandardCharsets.UTF_8.toString())
    val path = "/v1/api/lookups/registry?context=$q"
    get(path, ListLookupRegistryOutput.serializer())
}

private fun encodeSegment(v: String): String =
    URLEncoder.encode(v, StandardCharsets.UTF_8.toString()).replace("+", "%20")

// region Pure token helpers (Activations b + c) ----------------------------

object LookupTokens {
    private val TOKEN_RE =
        Regex("""@\{([a-zA-Z][a-zA-Z0-9_-]*):([^\s"]+)\s+"((?:[^"\\]|\\.)*)"\}""")
    private val PLACEHOLDER_RE = Regex("""\$\{(\w+)\}""")

    data class Parsed(
        val raw: String,
        val range: IntRange,
        val name: String,
        val id: String,
        val label: String
    )

    /** Serialize a resolved row into the stored token form `@{name:id "label"}`. */
    fun serializeToken(entry: LookupRegistryEntry, resolved: Map<String, Any?>): String {
        val storeTpl = entry.token?.store ?: "\${id}"
        val displayTpl = entry.token?.display ?: "\${name}"
        val id = applyTemplate(storeTpl, resolved)
        val label = applyTemplate(displayTpl, resolved).replace("\"", "\\\"")
        return "@{${entry.name}:$id \"$label\"}"
    }

    /** Parse every @{…} occurrence in text. */
    fun parseTokens(text: String): List<Parsed> =
        TOKEN_RE.findAll(text).map {
            Parsed(
                raw = it.value,
                range = it.range,
                name = it.groupValues[1],
                id = it.groupValues[2],
                label = it.groupValues[3].replace("\\\"", "\"")
            )
        }.toList()

    /**
     * Flatten a stored string with rich tokens into the text sent to the LLM.
     * Unknown names fall back to `${id}`.
     */
    fun flattenStored(stored: String, registry: List<LookupRegistryEntry>): String {
        if (stored.isEmpty()) return ""
        val byName = registry.associateBy { it.name }
        val sb = StringBuilder(stored)
        // Replace from the end so earlier indices stay valid.
        parseTokens(stored).asReversed().forEach { p ->
            val tpl = byName[p.name]?.token?.modelForm ?: "\${id}"
            val rendered = applyTemplate(
                tpl,
                mapOf("id" to p.id, "name" to p.label, "label" to p.label)
            )
            sb.replace(p.range.first, p.range.last + 1, rendered)
        }
        return sb.toString()
    }

    private fun applyTemplate(tpl: String, row: Map<String, Any?>): String =
        if (tpl.isEmpty()) "" else PLACEHOLDER_RE.replace(tpl) { m ->
            row[m.groupValues[1]]?.toString() ?: ""
        }
}

// endregion ----------------------------------------------------------------
