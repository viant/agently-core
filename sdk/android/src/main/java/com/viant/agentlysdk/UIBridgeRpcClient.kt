package com.viant.agentlysdk

import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext
import kotlinx.serialization.Serializable
import kotlinx.serialization.encodeToString
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.buildJsonObject
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody
import java.util.UUID

private const val UI_BRIDGE_SESSION_HEADER = "Mcp-Session-Id"

class UIBridgeRpcClient(
    private val client: AgentlyClient,
    private val endpointName: String = "appAPI"
) {
    private val rpcMutex = Mutex()

    @Volatile
    private var sessionId: String? = null

    suspend fun resetSession() {
        sessionId = null
    }

    suspend fun hello(clientId: String): JsonObject? = rpcObject(
        method = "ui.hello",
        params = buildJsonObject {
            put("clientId", JsonPrimitive(clientId))
        },
        includeSession = false
    )

    suspend fun poll(clientId: String, timeoutMs: Int = 20_000): JsonObject? = rpcObject(
        method = "ui.poll",
        params = buildJsonObject {
            put("clientId", JsonPrimitive(clientId))
            put("timeoutMs", JsonPrimitive(timeoutMs))
        }
    )

    suspend fun respond(
        commandId: String,
        ok: Boolean,
        result: JsonObject? = null,
        error: String? = null
    ): JsonObject? {
        val params = buildJsonObject {
            put("id", JsonPrimitive(commandId))
            put("ok", JsonPrimitive(ok))
            result?.let { put("result", it) }
            error?.takeIf { it.isNotBlank() }?.let { put("error", JsonPrimitive(it)) }
        }
        return rpcObject(method = "ui.response", params = params)
    }

    suspend fun snapshot(clientId: String, data: JsonObject): JsonObject? = rpcObject(
        method = "ui.snapshot",
        params = buildJsonObject {
            put("clientId", JsonPrimitive(clientId))
            put("data", data)
        }
    )

    private suspend fun rpcObject(
        method: String,
        params: JsonObject,
        includeSession: Boolean = true
    ): JsonObject? = rpcMutex.withLock {
        val config = requireNotNull(client.endpointRegistry.resolve(endpointName)) {
            "Endpoint not found: $endpointName"
        }
        val requestBody = UIBridgeRpcRequest(
            id = UUID.randomUUID().toString(),
            method = method,
            params = params
        )
        val requestBuilder = Request.Builder()
            .url("${config.baseUrl.trimEnd('/')}/v1/ui/rpc")
            .applyEndpointConfig(config)
            .header("Content-Type", "application/json")
            .post(client.json.encodeToString(requestBody).toRequestBody("application/json".toMediaType()))
        if (includeSession) {
            sessionId?.takeIf { it.isNotBlank() }?.let {
                requestBuilder.header(UI_BRIDGE_SESSION_HEADER, it)
            }
        }
        val response = withContext(Dispatchers.IO) {
            (config.httpClient ?: okhttp3.OkHttpClient()).newCall(requestBuilder.build()).execute()
        }
        response.use { httpResponse ->
            httpResponse.header(UI_BRIDGE_SESSION_HEADER)?.takeIf { it.isNotBlank() }?.let {
                sessionId = it
            }
            val bodyText = httpResponse.body?.string().orEmpty()
            if (httpResponse.code == 401 || httpResponse.code == 403 || httpResponse.code == 404) {
                sessionId = null
                return@withLock null
            }
            if (!httpResponse.isSuccessful) {
                throw IllegalStateException(bodyText.ifBlank { "UI bridge request failed with HTTP ${httpResponse.code}" })
            }
            if (bodyText.isBlank()) {
                return@withLock null
            }
            val envelope = client.json.decodeFromString(UIBridgeRpcEnvelope.serializer(), bodyText)
            envelope.error?.let {
                throw IllegalStateException("RPC ${it.code}: ${it.message}")
            }
            return@withLock envelope.result as? JsonObject
        }
    }
}

@Serializable
private data class UIBridgeRpcRequest(
    val jsonrpc: String = "2.0",
    val id: String,
    val method: String,
    val params: JsonObject
)

@Serializable
private data class UIBridgeRpcEnvelope(
    val jsonrpc: String? = null,
    val id: JsonElement? = null,
    val result: JsonElement? = null,
    val error: UIBridgeRpcError? = null
)

@Serializable
private data class UIBridgeRpcError(
    val code: Int,
    val message: String
)
