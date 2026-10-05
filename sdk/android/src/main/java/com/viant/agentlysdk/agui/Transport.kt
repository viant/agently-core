package com.viant.agentlysdk.agui

import com.viant.agentlysdk.EndpointConfig
import com.viant.agentlysdk.applyEndpointConfig
import kotlinx.coroutines.channels.awaitClose
import kotlinx.coroutines.channels.trySendBlocking
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.callbackFlow
import kotlinx.serialization.json.*
import okhttp3.*
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.RequestBody.Companion.toRequestBody
import java.util.concurrent.TimeUnit
import kotlin.concurrent.thread

data class AgUiSnapshot(val input: AgUiRunInput, val messages: List<AgUiMessage>, val state: JsonElement, val terminalEvent: AgUiEvent?, val pendingInterrupts: JsonArray, val pendingToolCallIds: List<String>, val capabilities: JsonObject?, val subagents: Map<String, AgUiSubagentInvocation>)
data class AgUiUpdate(val sourceEvent: AgUiEvent?, val event: AgUiEvent, val snapshot: AgUiSnapshot)
val AgUiStore.snapshot: AgUiSnapshot get() = AgUiSnapshot(input, messages.toList(), state, terminalEvent, pendingInterrupts, pendingToolCallIds.toList(), capabilities, subagents)

/** POST SSE. Cancelling collection aborts transport, not necessarily backend work. */
class AgUiClient(private val endpoint: EndpointConfig, private val path: String = "", private val maxFrameBytes: Int = 64 * 1024 * 1024) {
    companion object {
        /** Optional host diagnostics: framing phase and character count only, never payload content. */
        @Volatile var frameObservation: ((String, Int) -> Unit)? = null
    }
    private val fallback = OkHttpClient.Builder().cookieJar(AgUiSessionCookies()).connectTimeout(10, TimeUnit.SECONDS).readTimeout(0, TimeUnit.SECONDS).callTimeout(0, TimeUnit.SECONDS).build()
    /** Independent server resource command, distinct from cancelling collection. */
    fun cancelRun(threadId: String, targetRunId: String, commandRunId: String): Flow<AgUiUpdate> = run(AgentlyAgUiExtensions.cancelInput(threadId,targetRunId,commandRunId))
    fun run(input: AgUiRunInput): Flow<AgUiUpdate> = callbackFlow {
        AgUiSchema.validate(input.value, "RunAgentInput", true)
        val url = if (path.isEmpty()) endpoint.baseUrl else endpoint.baseUrl.trimEnd('/') + "/" + path.trimStart('/')
        val request = Request.Builder().url(url).header("Accept", "text/event-stream").applyEndpointConfig(endpoint)
            .post(input.value.toString().toRequestBody("application/json".toMediaType())).build()
        val client = endpoint.streamHttpClient ?: endpoint.longRunningHttpClient ?: endpoint.httpClient ?: fallback
        // Explicit application Cookie headers take precedence over our fallback jar.
        val selected = if (client === fallback && request.header("Cookie") != null) client.newBuilder().cookieJar(okhttp3.CookieJar.NO_COOKIES).build() else client
        val call = selected.newBuilder().followRedirects(false).followSslRedirects(false).build().newCall(request)
        val worker = thread(name = "agui-${input.runId}", isDaemon = true) {
            try {
                call.execute().use { response ->
                    if (!response.isSuccessful) throw AgUiHttpException(response.code)
                    if (response.header("Content-Type")?.substringBefore(';')?.trim()?.lowercase() != "text/event-stream") fail("AG-UI response must be text/event-stream")
                    val body = response.body ?: fail("Missing response body")
                    val store = AgUiStore(input); val parser = AgUiSSEParser(maxFrameBytes)
                    body.byteStream().use { stream ->
                        val buffer = ByteArray(8192)
                        while (!call.isCanceled()) {
                            val count = stream.read(buffer); if (count < 0) break
                            for (payload in parser.feed(buffer.copyOf(count))) {
                                frameObservation?.invoke("framed", payload.length)
                                val raw = AgUiEvent(Json.parseToJsonElement(payload).obj())
                                frameObservation?.invoke("decoded:${raw.type}", payload.length)
                                store.receive(raw) { event -> trySendBlocking(AgUiUpdate(raw, event, store.snapshot)) }
                                frameObservation?.invoke("applied:${raw.type}", payload.length)
                            }
                        }
                        parser.finish()
                        for (event in store.finish()) trySendBlocking(AgUiUpdate(null, event, store.snapshot))
                    }
                }
                close()
            } catch (error: Throwable) { if (!call.isCanceled()) close(error) }
        }
        awaitClose { call.cancel(); worker.interrupt() }
    }
}

/** Per-consumer fallback session; injected clients retain their own cookie policy. */
internal class AgUiSessionCookies : okhttp3.CookieJar {
    private val stored=mutableListOf<okhttp3.Cookie>()
    @Synchronized override fun saveFromResponse(url: okhttp3.HttpUrl, cookies: List<okhttp3.Cookie>) {
        for(cookie in cookies){stored.removeAll{it.name==cookie.name && it.domain==cookie.domain && it.path==cookie.path};if(cookie.expiresAt>System.currentTimeMillis())stored.add(cookie)}
    }
    @Synchronized override fun loadForRequest(url: okhttp3.HttpUrl): List<okhttp3.Cookie> {
        stored.removeAll{it.expiresAt<=System.currentTimeMillis()};return stored.filter{it.matches(url)}
    }
}

class AgUiHttpException(val statusCode: Int) : java.io.IOException("AG-UI POST failed: HTTP $statusCode")
