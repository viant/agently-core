package com.viant.agentlysdk

import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody
import java.util.concurrent.TimeUnit

class EndpointRegistry(private val endpoints: Map<String, EndpointConfig>) {
    fun resolve(name: String?): EndpointConfig? = name?.let { endpoints[it] }
}

data class EndpointConfig(
    val baseUrl: String,
    val authTokenProvider: (() -> String?)? = null,
    val defaultHeadersProvider: (() -> Map<String, String>)? = null,
    val httpClient: OkHttpClient? = null,
    val longRunningHttpClient: OkHttpClient? = null,
    val streamHttpClient: OkHttpClient? = null
)

class RestClient(
    private val endpoints: EndpointRegistry
) {
    private val fallbackClient = OkHttpClient()
    private val fallbackLongRunningClient = OkHttpClient.Builder()
        .connectTimeout(10, TimeUnit.SECONDS)
        .readTimeout(0, TimeUnit.SECONDS)
        .callTimeout(0, TimeUnit.SECONDS)
        .build()

    fun <T> get(endpoint: String?, uri: String, parser: (String) -> T): T {
        val config = endpoints.resolve(endpoint)
            ?: error("Endpoint not found: $endpoint")
        val url = config.baseUrl.trimEnd('/') + "/" + uri.trimStart('/')
        val request = Request.Builder().url(url)
            .applyEndpointConfig(config)
            .get()
            .build()
        clientFor(config).newCall(request).execute().use { resp ->
            val body = resp.body?.string() ?: ""
            if (!resp.isSuccessful) error(httpFailureMessage("GET", url, resp.code, body))
            return parser(body)
        }
    }

    fun <T> patch(endpoint: String?, uri: String, payload: String, parser: (String) -> T): T {
        val config = endpoints.resolve(endpoint)
            ?: error("Endpoint not found: $endpoint")
        val url = config.baseUrl.trimEnd('/') + "/" + uri.trimStart('/')
        val request = Request.Builder().url(url)
            .applyEndpointConfig(config)
            .patch(payload.toRequestBody("application/json".toMediaType()))
            .build()
        clientFor(config).newCall(request).execute().use { resp ->
            val body = resp.body?.string() ?: ""
            if (!resp.isSuccessful) error(httpFailureMessage("PATCH", url, resp.code, body))
            return parser(body)
        }
    }

    fun <T> post(endpoint: String?, uri: String, payload: String, parser: (String) -> T): T {
        val config = endpoints.resolve(endpoint)
            ?: error("Endpoint not found: $endpoint")
        val url = config.baseUrl.trimEnd('/') + "/" + uri.trimStart('/')
        val request = Request.Builder().url(url)
            .applyEndpointConfig(config)
            .post(payload.toRequestBody("application/json".toMediaType()))
            .build()
        clientFor(config).newCall(request).execute().use { resp ->
            val body = resp.body?.string() ?: ""
            if (!resp.isSuccessful) error(httpFailureMessage("POST", url, resp.code, body))
            return parser(body)
        }
    }

    fun <T> postLongRunning(endpoint: String?, uri: String, payload: String, parser: (String) -> T): T {
        val config = endpoints.resolve(endpoint)
            ?: error("Endpoint not found: $endpoint")
        val url = config.baseUrl.trimEnd('/') + "/" + uri.trimStart('/')
        val request = Request.Builder().url(url)
            .applyEndpointConfig(config)
            .post(payload.toRequestBody("application/json".toMediaType()))
            .build()
        longRunningClientFor(config).newCall(request).execute().use { resp ->
            val body = resp.body?.string() ?: ""
            if (!resp.isSuccessful) error(httpFailureMessage("POST", url, resp.code, body))
            return parser(body)
        }
    }

    fun <T> put(endpoint: String?, uri: String, payload: String, parser: (String) -> T): T {
        val config = endpoints.resolve(endpoint)
            ?: error("Endpoint not found: $endpoint")
        val url = config.baseUrl.trimEnd('/') + "/" + uri.trimStart('/')
        val request = Request.Builder().url(url)
            .applyEndpointConfig(config)
            .put(payload.toRequestBody("application/json".toMediaType()))
            .build()
        clientFor(config).newCall(request).execute().use { resp ->
            val body = resp.body?.string() ?: ""
            if (!resp.isSuccessful) error(httpFailureMessage("PUT", url, resp.code, body))
            return parser(body)
        }
    }

    fun <T> delete(endpoint: String?, uri: String, parser: (String) -> T): T {
        val config = endpoints.resolve(endpoint)
            ?: error("Endpoint not found: $endpoint")
        val url = config.baseUrl.trimEnd('/') + "/" + uri.trimStart('/')
        val request = Request.Builder().url(url)
            .applyEndpointConfig(config)
            .delete()
            .build()
        clientFor(config).newCall(request).execute().use { resp ->
            val body = resp.body?.string() ?: ""
            if (!resp.isSuccessful) error(httpFailureMessage("DELETE", url, resp.code, body))
            return parser(body)
        }
    }

    private fun clientFor(config: EndpointConfig): OkHttpClient = config.httpClient ?: fallbackClient

    private fun longRunningClientFor(config: EndpointConfig): OkHttpClient =
        config.longRunningHttpClient ?: config.httpClient ?: fallbackLongRunningClient
}

internal fun Request.Builder.applyEndpointConfig(config: EndpointConfig): Request.Builder {
    config.defaultHeadersProvider?.invoke()?.forEach { (name, value) ->
        if (name.isNotBlank() && value.isNotBlank()) {
            header(name, value)
        }
    }
    config.authTokenProvider?.invoke()?.takeIf { it.isNotBlank() }?.let {
        header("Authorization", "Bearer $it")
    }
    return this
}

private fun httpFailureMessage(method: String, url: String, code: Int, body: String): String {
    val detail = body.trim()
        .replace(Regex("\\s+"), " ")
        .take(500)
    return if (detail.isBlank()) {
        "$method $url failed: $code"
    } else {
        "$method $url failed: $code: $detail"
    }
}
