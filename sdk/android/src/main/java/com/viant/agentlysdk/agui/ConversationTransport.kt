package com.viant.agentlysdk.agui

import com.viant.agentlysdk.*
import com.viant.agentlysdk.stream.*
import kotlinx.coroutines.*
import kotlinx.coroutines.channels.awaitClose
import kotlinx.coroutines.flow.*
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.serialization.json.*
import java.util.UUID


data class AgUiConversationRun(val runId: String, val status: String, val parentRunId: String? = null, val kind: String? = null)

/** Canonical history and authorized host activities are distinct from model protocol history. */
internal data class AgUiConversationBootstrap(
    val transcript: ConversationStateResponse, val rawTranscript: JsonObject, val messages: List<AgUiMessage>, val state: JsonElement,
    val hostActivities: List<AgUiMessage>, val unavailableHostActivityIds: List<String>, val runs: List<AgUiConversationRun>
)

/** Native orchestration only. Every protocol run is reduced by the independent standard AgUiStore. */
internal class AgUiConversationTransport(private val host: AgentlyClient, private val protocol: AgUiClient, private val endpoint: EndpointConfig) {
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
    private val entries = mutableMapOf<String, Entry>()
    private var generation = 0L
    private class Entry(val id: String, val generation: Long) {
        var protocolThreadId: String? = null
        val mutation = Mutex()
        val reads = Mutex()
        val updates = MutableSharedFlow<ConversationStreamSnapshot>(replay = 1, extraBufferCapacity = 64)
        val tracker = ConversationStreamTracker(id)
        val runs = linkedMapOf<String, Slot>()
        val hostActivities = linkedMapOf<String, AgUiMessage>()
        val aliases = linkedMapOf<String, String>()
        var bootstrap: AgUiConversationBootstrap? = null
        var readOnlyTranscript: ConversationStateResponse? = null
        var publicationRevision = 0L
        var error: String? = null
        var observation: Job? = null
        var listeners = 0
    }
    private class Slot(val runId: String, val input: AgUiRunInput, val inputMessageId: String?) {
        var nativeTurnId: String? = null
        var latest: AgUiSnapshot? = null
        var job: Job? = null
        var terminal = false
        val projected = linkedMapOf<String, SSEEvent>()
        val admitted = CompletableDeferred<QueryOutput>()
    }
    @Synchronized private fun entry(id: String): Entry {
        require(id.isNotBlank()) { "Conversation identity is required" }
        return entries.getOrPut(id) { Entry(id, generation) }
    }
    @Synchronized private fun current(entry: Entry) = entry.generation == generation && entries[entry.id] === entry

    /** Authentication/logout boundary detaches requests without cancelling backend execution. */
    @Synchronized fun reset() {
        generation++
        scope.coroutineContext.cancelChildren()
        entries.clear()
    }

    fun track(conversationId: String): Flow<ConversationStreamSnapshot> = callbackFlow {
        val e = entry(conversationId)
        e.mutation.withLock { e.listeners++; ensureObservation(e) }
        val delivery = launch { e.updates.collect { if (current(e)) send(it) } }
        val initial = launch { try { refreshObservation(e) } catch (error: Throwable) { if (error is CancellationException) throw error; failRead(e, error) } }
        awaitClose {
            delivery.cancel(); initial.cancel()
            scope.launch { e.mutation.withLock { e.listeners--; if (e.listeners == 0) { e.observation?.cancel(); e.observation = null } } }
        }
    }

    suspend fun query(input: QueryInput): QueryOutput {
        require(input.elicitationMode == null || input.elicitationMode == "deferred") { "AG-UI uses deferred interrupts" }
        val id = input.conversationId ?: host.createConversation(CreateConversationInput(agentId = input.agentId)).id
        val e = entry(id)
        val bootstrap = refresh(e)
        check(current(e)) { "Conversation session invalidated" }
        val runId = UUID.randomUUID().toString()
        val messageId = input.messageId ?: UUID.randomUUID().toString()
        val selection = host.json.encodeToJsonElement(QueryInput.serializer(), input).jsonObject.toMutableMap().apply {
            val allowed = setOf("agentId", "model", "tools", "toolBundles", "autoSelectTools", "context", "reasoningEffort", "autoSummarize", "disableChains", "allowedChains", "resourceURIs", "toolCallExposure", "attachments")
            keys.retainAll(allowed)
            remove("tools")?.let { put("backendTools", it) }
            if (input.attachments.isNotEmpty()) put("attachments", JsonArray(input.attachments.map { attachment -> buildJsonObject {
                put("name", attachment.name); put("uri", attachment.uri)
                attachment.mime?.let { put("mime", it) }; attachment.stagingFolder?.let { put("stagingFolder", it) }
            } }))
            put("useServerState", JsonPrimitive(true))
        }
        val posted = AgUiRunInput.create(e.protocolThreadId ?: id, runId, bootstrap.messages.filter { it.role != "activity" } + AgUiMessage.user(messageId, JsonPrimitive(input.query)),
            state = bootstrap.state.takeUnless { it == JsonNull }, tools = JsonArray(emptyList()), context = JsonArray(emptyList()),
            forwardedProps = AgentlyAgUiExtensions.command("chat", messageId, JsonObject(selection)))
        val slot = Slot(runId, posted, messageId)
        e.mutation.withLock { check(current(e)); e.runs[runId] = slot; start(e, slot, bootstrap.messages) }
        // Only this request's admitted native identity can complete the composer admission.
        return slot.admitted.await()
    }

    suspend fun transcript(conversationId: String, fresh: Boolean = false): ConversationStateResponse =
        refresh(entry(conversationId), fresh).transcript

    /** A fresh read waits behind any older in-flight read, then always executes another command. */
    suspend fun reconcile(conversationId: String): ConversationStateResponse = transcript(conversationId, fresh = true)

    private suspend fun refresh(e: Entry, fresh: Boolean = false): AgUiConversationBootstrap = e.reads.withLock {
        check(current(e)) { "Conversation session invalidated" }
        // Reads are serialized rather than reusing an older result after a committed action.
        val result = command(e.id, "conversation.bootstrap", buildJsonObject {
            put("mode", "live"); put("includeModelCalls", true); put("includeToolCalls", true); put("includeFeeds", true)
        }) as? JsonObject ?: error("Missing conversation bootstrap")
        val bootstrap = decodeBootstrap(result, e.id, e.protocolThreadId ?: e.id)
        e.mutation.withLock {
            check(current(e)) { "Conversation bootstrap invalidated" }
            e.publicationRevision++; e.bootstrap = bootstrap; e.readOnlyTranscript = null; e.error = null
            e.hostActivities.clear(); bootstrap.hostActivities.forEach { e.hostActivities[it.id] = it }
            // Replace canonical history authoritatively; reduced live runs can subsequently overlay newer snapshots.
            e.tracker.clear(); e.tracker.hydrate(bootstrap.transcript)
            for (message in bootstrap.messages) {
                val p = presentation(message.value["metadata"]) ?: continue
                val clientId = p.string("clientMessageId") ?: continue
                val turn = bootstrap.transcript.conversation?.turns?.firstOrNull { it.turnId == p.string("nativeTurnId") }
                val nativeId = p.string("nativeUserMessageId") ?: turn?.startedByMessageId
                if (nativeId != null) e.aliases[clientId] = nativeId
            }
            for (slot in e.runs.values) if (!slot.terminal || slot.latest?.pendingInterrupts?.isNotEmpty() == true) for (event in slot.projected.values) {
                val terminalTurn = bootstrap.transcript.conversation?.turns?.any { it.turnId == event.turnId && it.status in terminalStatuses } == true
                if (!terminalTurn || event.type !in activeEventTypes) e.tracker.applyEvent(event)
            }
            publish(e)
            if (e.listeners > 0) for (run in bootstrap.runs) {
                if (run.kind in setOf("resource", "mcp-app")) continue
                val existing = e.runs[run.runId]
                if (existing != null && (existing.job?.isActive == true || existing.terminal || fresh)) continue
                attach(e, run.runId, bootstrap.messages)
            }
        }
        bootstrap
    }

    private fun attach(e: Entry, runId: String, baseline: List<AgUiMessage>) {
        val input = AgUiRunInput.create(e.protocolThreadId ?: e.id, runId, emptyList(), tools = JsonArray(emptyList()), context = JsonArray(emptyList()),
            forwardedProps = AgentlyAgUiExtensions.command("run.attach", UUID.randomUUID().toString(), JsonObject(emptyMap())))
        val slot = Slot(runId, input, null); e.runs[runId] = slot; start(e, slot, baseline)
    }

    @Synchronized private fun start(e: Entry, slot: Slot, baseline: List<AgUiMessage>) {
        if (!current(e)) { slot.admitted.cancel(CancellationException("Conversation session invalidated")); return }
        val projector = AgUiNativePresentation(e.id, slot.runId, baseline, slot.inputMessageId,
            emit = { event ->
                if (current(e)) {
                    // Journal history must never reactivate a canonically terminal turn.
                    val terminal = e.bootstrap?.transcript?.conversation?.turns?.any { it.turnId == event.turnId && it.status in terminalStatuses } == true
                    slot.projected["${event.type}:${event.turnId}:${event.messageId ?: event.toolCallId ?: event.modelCallId ?: event.feedId}"] = event
                    if (!terminal || event.type !in activeEventTypes) e.tracker.applyEvent(event)
                    if (slot.inputMessageId != null && !slot.admitted.isCompleted && event.turnId == slot.nativeTurnId && event.type in setOf("turn_started", "turn_queued", "model_started"))
                        slot.admitted.complete(QueryOutput(conversationId = e.id, messageId = event.turnId))
                }
            }, hostActivity = { e.hostActivities[it.id] = it }, alias = { client, native -> e.aliases[client] = native })
        slot.job = scope.launch {
            try {
                protocol.run(slot.input).collect { update ->
                    e.mutation.withLock {
                        if (!current(e)) return@collect
                        slot.latest = update.snapshot
                        if (update.event.type == "RUN_STARTED") {
                            slot.nativeTurnId = presentation(update.event.value["metadata"])?.string("nativeTurnId")
                            if (slot.inputMessageId != null && slot.nativeTurnId != null && !slot.admitted.isCompleted)
                                slot.admitted.complete(QueryOutput(conversationId = e.id, messageId = slot.nativeTurnId))
                        }
                        projector.consume(update)
                        slot.nativeTurnId = slot.nativeTurnId ?: projector.nativeTurnId
                        if (update.event.type in setOf("RUN_FINISHED", "RUN_ERROR")) slot.terminal = true
                        publish(e)
                    }
                }
                if (!slot.admitted.isCompleted && slot.inputMessageId != null)
                    slot.admitted.completeExceptionally(IllegalStateException("Request ended without its native admission identity"))
            } catch (error: Throwable) {
                if (error is CancellationException) { slot.admitted.cancel(error); throw error }
                // A broken connection is not a turn failure or cancellation. Never repeat an uncertain POST.
                if (!slot.admitted.isCompleted) slot.admitted.completeExceptionally(error)
                failRead(e, error)
            } finally {
                if (current(e) && e.listeners > 0) scope.launch { try { refresh(e, true) } catch (error: Throwable) { if (error !is CancellationException) failRead(e, error) } }
            }
        }
        slot.job?.invokeOnCompletion { error -> if (error is CancellationException) slot.admitted.cancel(error) }
    }

    suspend fun cancel(conversationId: String) {
        val e = entry(conversationId)
        val bootstrap = refresh(e)
        for (run in bootstrap.runs) if (run.kind !in setOf("resource", "mcp-app") && run.status !in terminalStatuses)
            command(e.id, "run.cancel", buildJsonObject { put("runId", run.runId) })
        reconcile(e.id)
    }

    suspend fun requestAdmitted(conversationId: String, clientId: String): Boolean {
        val e = entry(conversationId)
        refresh(e)
        return e.mutation.withLock { current(e) && e.aliases.containsKey(clientId) }
    }

    private suspend fun interruptedSlot(e: Entry, interruptId: String): Slot {
        fun find() = e.runs.values.firstOrNull { s -> s.latest?.pendingInterrupts?.any { (it as? JsonObject)?.string("id") == interruptId } == true }
        e.mutation.withLock {
            find()?.let { return it }
            for (run in e.bootstrap?.runs.orEmpty()) if (run.kind !in setOf("resource", "mcp-app") && run.runId !in e.runs)
                attach(e, run.runId, e.bootstrap?.messages.orEmpty())
        }
        withTimeout(15_000) { e.updates.first { e.mutation.withLock { find() != null || !current(e) } } }
        return e.mutation.withLock { check(current(e)); find() ?: error("Pending AG-UI interrupt unavailable") }
    }

    suspend fun resolve(input: ResolveElicitationInput) {
        val e = entry(input.conversationId)
        refresh(e)
        val slot = interruptedSlot(e, input.elicitationId)
        val snapshot = slot.latest ?: error("Pending interrupt snapshot unavailable")
        val answer = buildJsonObject { put("interruptId", input.elicitationId); put("status", if (input.action in setOf("cancel", "cancelled")) "cancelled" else "resolved"); put("payload", JsonObject(input.payload)) }
        val pending = snapshot.pendingInterrupts.map { (it as JsonObject).requiredString("id") }
        require(pending.size == 1) { "Resume must answer all pending interrupts together" }
        val nextId = UUID.randomUUID().toString()
        val next = snapshot.nextInput(nextId, AgentlyAgUiExtensions.command("chat", nextId, buildJsonObject { put("useServerState", true) }), resume = JsonArray(listOf(answer)))
        e.mutation.withLock { check(current(e)); val successor = Slot(nextId, next, null); e.runs[nextId] = successor; start(e, successor, emptyList()) }
    }

    suspend fun decide(input: DecideToolApprovalInput): DecideToolApprovalOutput {
        val approvals = host.listPendingToolApprovals(ListPendingToolApprovalsInput(status = "pending"))
        val approval = approvals.firstOrNull { it.id == input.id } ?: error("Approval is no longer pending")
        val id = approval.conversationId ?: error("Approval conversation identity missing")
        val e = entry(id)
        refresh(e)
        val slot = interruptedSlot(e, input.id)
        val answer = buildJsonObject {
            put("interruptId", input.id); put("status", "resolved")
            put("payload", buildJsonObject { put("action", input.action); put("editedFields", JsonObject(input.editedFields)); put("payload", JsonObject(input.payload)); input.reason?.let { put("reason", it) }; input.note?.let { put("note", it) } })
        }
        // Independent durable command. A failed/uncertain decision is discovered, never auto-resubmitted.
        var result: JsonElement? = null
        var decisionError: Throwable? = null
        try { result = command(id, "approval.decide", buildJsonObject { put("originalRunId", slot.runId); put("originalThreadId", e.protocolThreadId ?: id); put("approvalId", input.id); put("answer", answer) }) }
        catch (error: Throwable) { if (error is CancellationException) throw error; decisionError = error }
        val original = command(id, "run.get", buildJsonObject { put("runId", slot.runId) }) as? JsonObject
        val receipt = (result as? JsonObject)?.get("protocol") as? JsonObject
        (receipt?.string("continuationRunId") ?: original?.string("resumedByRunId"))?.let { successor -> e.mutation.withLock { if (current(e) && successor !in e.runs) attach(e, successor, emptyList()) } }
        reconcile(id)
        decisionError?.let { throw it }
        return host.json.decodeFromJsonElement(DecideToolApprovalOutput.serializer(), result ?: JsonObject(emptyMap()))
    }

    private fun ensureObservation(e: Entry) {
        if (e.observation?.isActive == true) return
        e.observation = scope.launch {
            while (current(e) && e.listeners > 0) {
                try {
                    openEventStream(endpoint, "/v1/application-events?conversationId=${java.net.URLEncoder.encode(e.id, "UTF-8")}", e.id, host.json).collect { event ->
                        if (!current(e) || event.conversationId != e.id) return@collect
                        if (event.type == "compatibility_reconcile" || event.type in setOf("turn_completed", "turn_failed", "turn_canceled") || event.type == "conversation_meta_updated" && event.patch?.bool("aguiUpdated") == true) {
                            scope.launch { try { refreshObservation(e, true) } catch (error: Throwable) { if (error !is CancellationException) failRead(e, error) } }
                        } else e.mutation.withLock { if (current(e)) { e.tracker.applyEvent(event); publish(e) } }
                    }
                } catch (error: Throwable) { if (error is CancellationException) throw error; failRead(e, error) }
                delay(1000)
            }
        }
    }

    private suspend fun refreshObservation(e: Entry, fresh: Boolean = false) {
        val revision = e.mutation.withLock { e.publicationRevision }
        try { refresh(e, fresh) }
        catch (error: AgUiHttpException) {
            if (error.statusCode != 403) throw error
            val transcript = host.readConversationHistory(GetTranscriptInput(e.id))
            check(transcript.conversation?.conversationId == e.id) { "Read-only conversation identity mismatch" }
            e.mutation.withLock {
                check(current(e) && e.publicationRevision == revision) { "Read-only conversation snapshot invalidated" }
                e.publicationRevision++; e.bootstrap = null; e.readOnlyTranscript = transcript; e.error = null
                e.hostActivities.clear(); e.aliases.clear()
                e.tracker.clear(); e.tracker.hydrate(transcript); publish(e)
            }
        }
    }

    private suspend fun failRead(e: Entry, error: Throwable) = e.mutation.withLock {
        if (current(e)) { e.error = error.message ?: "Conversation transport unavailable"; publish(e) }
    }
    private fun publish(e: Entry) {
        if (!current(e)) return
        val b = e.bootstrap
        e.updates.tryEmit(e.tracker.snapshot().copy(canonicalTranscript = b?.transcript ?: e.readOnlyTranscript, rawCanonicalTranscript = b?.rawTranscript, hostActivities = e.hostActivities.values.toList(),
            unavailableHostActivityIds = b?.unavailableHostActivityIds.orEmpty(), userMessageAliases = e.aliases.toMap(), protocolRuns = b?.runs.orEmpty(), transportError = e.error))
    }

    private suspend fun resolveProtocolThread(e: Entry): String {
        e.protocolThreadId?.let { return it }
        val conversation = host.getConversation(e.id)
        check(current(e) && conversation.id == e.id) { "Native conversation binding mismatch" }
        val wire = conversation.aguiThreadId ?: e.id
        check(wire.isNotEmpty()) { "Empty protocol thread binding" }
        e.protocolThreadId = wire
        return wire
    }

    private suspend fun command(threadId: String, operation: String, payload: JsonObject): JsonElement? {
        val wireThreadId = resolveProtocolThread(entry(threadId))
        val commandId = UUID.randomUUID().toString()
        var result: JsonElement? = null; var terminal = false
        protocol.run(AgUiRunInput.create(wireThreadId, commandId, emptyList(), tools = JsonArray(emptyList()), context = JsonArray(emptyList()),
            forwardedProps = AgentlyAgUiExtensions.command(operation, commandId, payload))).collect { update ->
            when (update.event.type) {
                "RUN_ERROR" -> error(update.event.value.string("message") ?: "AG-UI command failed")
                "RUN_FINISHED" -> {
                    val outcome = update.event.value["outcome"] as? JsonObject
                    check(outcome?.string("type")?.let { it == "success" } != false) { "AG-UI command did not succeed" }
                    result = update.event.value["result"]; terminal = true
                }
            }
        }
        check(terminal) { "Command stream ended without terminal event" }
        return result
    }

    private fun decodeBootstrap(value: JsonObject, id: String, wireThreadId: String = id): AgUiConversationBootstrap {
        val transcript = value["transcript"] as? JsonObject ?: error("Missing canonical transcript")
        check(value.string("version") == "1" && value.string("threadId") == wireThreadId && value.containsKey("state") && transcript.string("schemaVersion") != null) { "Unsupported bootstrap result" }
        val canonical = host.json.decodeFromJsonElement(ConversationStateResponse.serializer(), transcript)
        check(canonical.conversation?.conversationId == id) { "Bootstrap conversation mismatch" }
        val projection = value["projection"] as? JsonObject ?: error("Missing projection availability")
        check(projection.bool("lossless") != null && projection["unavailableMessageIds"] is JsonArray) { "Invalid projection availability" }
        val messages = (value["messages"] as? JsonArray ?: error("Missing protocol history")).map { AgUiMessage(it as JsonObject) }
        val hosts = (value["hostActivities"] as? JsonArray).orEmpty().map { AgUiMessage(it as JsonObject) }
        check(hosts.all { it.role == "activity" }) { "Invalid host activity" }
        val runs = (value["runs"] as? JsonArray ?: error("Missing run references")).map { raw ->
            val row = raw as JsonObject; AgUiConversationRun(row.requiredString("runId"), row.requiredString("status"), row.string("parentRunId"), row.string("kind"))
        }
        return AgUiConversationBootstrap(canonical, transcript, messages.filter { it.role != "activity" || it.value.string("activityType") != "mcp-apps" }, value["state"] ?: JsonNull,
            hosts, (value["unavailableHostActivityIds"] as? JsonArray).orEmpty().map { it.jsonPrimitive.content }, runs)
    }
    companion object {
        private val terminalStatuses = setOf("completed", "succeeded", "failed", "canceled", "cancelled")
        private val activeEventTypes = setOf("turn_started", "turn_queued", "model_started", "model_completed", "tool_call_started", "tool_call_waiting", "text_delta", "reasoning_delta", "message_appended")
    }
}
