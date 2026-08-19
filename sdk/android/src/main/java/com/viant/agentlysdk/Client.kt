package com.viant.agentlysdk

import com.viant.agentlysdk.stream.SSEEvent
import com.viant.agentlysdk.stream.ConversationStreamSnapshot
import com.viant.agentlysdk.stream.ConversationStreamTracker
import com.viant.agentlysdk.stream.openEventStream
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.CoroutineStart
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.cancelAndJoin
import kotlinx.coroutines.delay
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.channelFlow
import kotlinx.coroutines.flow.collect
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import kotlinx.serialization.KSerializer
import kotlinx.serialization.builtins.ListSerializer
import kotlinx.serialization.builtins.MapSerializer
import kotlinx.serialization.builtins.serializer
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.decodeFromJsonElement
import okhttp3.MediaType.Companion.toMediaTypeOrNull
import okhttp3.MultipartBody
import okhttp3.RequestBody.Companion.toRequestBody
import okhttp3.Request
import java.net.URLEncoder
import java.nio.charset.StandardCharsets

class AgentlyClient(
    endpoints: EndpointRegistry,
    internal val endpointName: String = "appAPI",
    internal val json: Json = Json { ignoreUnknownKeys = true }
) {
    internal val endpointRegistry = endpoints

    constructor(
        endpoints: Map<String, EndpointConfig>,
        endpointName: String = "appAPI",
        json: Json = Json { ignoreUnknownKeys = true },
        sessionDebug: SessionDebugOptions? = null
    ) : this(EndpointRegistry(applySessionDebug(endpoints, sessionDebug)), endpointName, json)

    private val restClient = RestClient(endpoints)

    suspend fun authProviders(): List<AuthProvider> = withContext(Dispatchers.IO) {
        val root = parseJson(restClient.get(endpointName, "/v1/api/auth/providers") { it })
        when (root) {
            is JsonArray -> json.decodeFromJsonElement(ListSerializer(AuthProvider.serializer()), root)
            is JsonObject -> decode(root, AuthProvidersEnvelope.serializer()).providers
            else -> emptyList()
        }
    }

    suspend fun authMe(): AuthUser = withContext(Dispatchers.IO) {
        get("/v1/api/auth/me", AuthUser.serializer())
    }

    suspend fun localLogin(input: LocalLoginInput): LocalLoginOutput = withContext(Dispatchers.IO) {
        post("/v1/api/auth/local/login", input, LocalLoginOutput.serializer())
    }

    suspend fun logout(): Unit = withContext(Dispatchers.IO) {
        post("/v1/api/auth/logout", emptyMap<String, JsonElement>(), EmptyResponse.serializer())
    }

    suspend fun oauthInitiate(input: OAuthInitiateInput = OAuthInitiateInput()): OAuthInitiateOutput = withContext(Dispatchers.IO) {
        post("/v1/api/auth/oauth/initiate", input, OAuthInitiateOutput.serializer())
    }

    suspend fun oauthMobileInitiate(input: OAuthInitiateInput): OAuthInitiateOutput = withContext(Dispatchers.IO) {
        post("/v1/api/auth/oauth/mobile/initiate", input, OAuthInitiateOutput.serializer())
    }

    suspend fun oauthCallback(input: OAuthCallbackInput): OAuthCallbackOutput = withContext(Dispatchers.IO) {
        post("/v1/api/auth/oauth/callback", input, OAuthCallbackOutput.serializer())
    }

    suspend fun oauthMobileCallback(input: OAuthCallbackInput): OAuthCallbackOutput = withContext(Dispatchers.IO) {
        post("/v1/api/auth/oauth/mobile/callback", input, OAuthCallbackOutput.serializer())
    }

    suspend fun getOAuthConfig(): OAuthConfigOutput = withContext(Dispatchers.IO) {
        get("/v1/api/auth/oauth/config", OAuthConfigOutput.serializer())
    }

    suspend fun createAuthSession(input: CreateSessionInput): CreateSessionOutput = withContext(Dispatchers.IO) {
        post("/v1/api/auth/session", input, CreateSessionOutput.serializer())
    }

    suspend fun attachAuthSession(input: AttachSessionInput): AttachSessionOutput = withContext(Dispatchers.IO) {
        post("/v1/api/auth/session/attach", input, AttachSessionOutput.serializer())
    }

    suspend fun oobLogin(input: OOBLoginInput): OOBLoginOutput = withContext(Dispatchers.IO) {
        post("/v1/api/auth/oob", input, OOBLoginOutput.serializer())
    }

    suspend fun idpDelegate(): IDPDelegateOutput = withContext(Dispatchers.IO) {
        post("/v1/api/auth/idp/delegate", emptyMap<String, JsonElement>(), IDPDelegateOutput.serializer())
    }

    suspend fun getWorkspaceMetadata(targetContext: MetadataTargetContext? = null): WorkspaceMetadata = withContext(Dispatchers.IO) {
        val query = targetContext.toTargetQuery()
        val path = appendRepeatedQuery("/v1/workspace/metadata", query)
        val root = parseJson(restClient.get(endpointName, path) { it })
        val payload = (root as? JsonObject)?.get("data") ?: root
        val metadata = decode(payload, WorkspaceMetadata.serializer())
        metadata.copy(
            defaultAgent = metadata.defaultAgent ?: metadata.defaults?.agent,
            defaultModel = metadata.defaultModel ?: metadata.defaults?.model,
            defaultEmbedder = metadata.defaultEmbedder ?: metadata.defaults?.embedder
        )
    }

    suspend fun query(input: QueryInput): QueryOutput = withContext(Dispatchers.IO) {
        postLongRunning("/v1/agent/query", input, QueryOutput.serializer())
    }

    suspend fun createConversation(input: CreateConversationInput): Conversation = withContext(Dispatchers.IO) {
        post("/v1/conversations", input, Conversation.serializer())
    }

    suspend fun listConversations(input: ListConversationsInput = ListConversationsInput()): ConversationPage = withContext(Dispatchers.IO) {
        get(buildConversationListPath(input), ConversationPage.serializer())
    }

    suspend fun getConversation(conversationId: String): Conversation = withContext(Dispatchers.IO) {
        get("/v1/conversations/${encodePath(conversationId)}", Conversation.serializer())
    }

    suspend fun getRun(id: String): RunView = withContext(Dispatchers.IO) {
        get("/v1/runs/${encodePath(id)}", RunView.serializer())
    }

    suspend fun updateConversation(conversationId: String, input: UpdateConversationInput): Conversation = withContext(Dispatchers.IO) {
        patch("/v1/conversations/${encodePath(conversationId)}", input, Conversation.serializer())
    }

    suspend fun deleteConversation(conversationId: String): Unit = withContext(Dispatchers.IO) {
        delete("/v1/conversations/${encodePath(conversationId)}", EmptyResponse.serializer())
    }

    suspend fun getGoal(conversationId: String): Goal? = withContext(Dispatchers.IO) {
        get("/v1/conversations/${encodePath(conversationId)}/goal", GoalEnvelope.serializer()).goal
    }

    suspend fun listAsyncOperations(conversationId: String, tool: String? = null, mode: String? = null): ListAsyncOperationsOutput = withContext(Dispatchers.IO) {
        val query = linkedMapOf<String, String>()
        if (!tool.isNullOrBlank()) {
            query["tool"] = tool
        }
        if (!mode.isNullOrBlank()) {
            query["mode"] = mode
        }
        get(
            appendQuery("/v1/conversations/${encodePath(conversationId)}/async", query),
            ListAsyncOperationsOutput.serializer()
        )
    }

    suspend fun createGoal(conversationId: String, input: CreateGoalInput): Goal = withContext(Dispatchers.IO) {
        post("/v1/conversations/${encodePath(conversationId)}/goal", input, Goal.serializer())
    }

    suspend fun updateGoal(conversationId: String, input: UpdateGoalInput): Goal = withContext(Dispatchers.IO) {
        patch("/v1/conversations/${encodePath(conversationId)}/goal", input, Goal.serializer())
    }

    suspend fun clearGoal(conversationId: String): Unit = withContext(Dispatchers.IO) {
        delete("/v1/conversations/${encodePath(conversationId)}/goal", EmptyResponse.serializer())
    }

    suspend fun cancelTurn(turnId: String): Boolean = withContext(Dispatchers.IO) {
        post("/v1/turns/${encodePath(turnId)}/cancel", emptyMap<String, JsonElement>(), CancelTurnEnvelope.serializer()).cancelled
    }

    suspend fun steerTurn(input: SteerTurnInput): SteerTurnOutput = withContext(Dispatchers.IO) {
        post(
            "/v1/conversations/${encodePath(input.conversationId)}/turns/${encodePath(input.turnId)}/steer",
            input,
            SteerTurnOutput.serializer()
        )
    }

    suspend fun cancelQueuedTurn(conversationId: String, turnId: String): Unit = withContext(Dispatchers.IO) {
        delete("/v1/conversations/${encodePath(conversationId)}/turns/${encodePath(turnId)}", EmptyResponse.serializer())
    }

    suspend fun moveQueuedTurn(input: MoveQueuedTurnInput): Unit = withContext(Dispatchers.IO) {
        post(
            "/v1/conversations/${encodePath(input.conversationId)}/turns/${encodePath(input.turnId)}/move",
            input,
            EmptyResponse.serializer()
        )
    }

    suspend fun editQueuedTurn(input: EditQueuedTurnInput): Unit = withContext(Dispatchers.IO) {
        patch(
            "/v1/conversations/${encodePath(input.conversationId)}/turns/${encodePath(input.turnId)}",
            input,
            EmptyResponse.serializer()
        )
    }

    suspend fun forceSteerQueuedTurn(conversationId: String, turnId: String): SteerTurnOutput = withContext(Dispatchers.IO) {
        post(
            "/v1/conversations/${encodePath(conversationId)}/turns/${encodePath(turnId)}/force-steer",
            emptyMap<String, JsonElement>(),
            SteerTurnOutput.serializer()
        )
    }

    suspend fun getMessages(input: GetMessagesInput): MessagePage = withContext(Dispatchers.IO) {
        get(buildMessagesPath(input), MessagePage.serializer())
    }

    suspend fun getTranscript(
        input: GetTranscriptInput,
        maxResponseBytes: Long = DEFAULT_MAX_TRANSCRIPT_RESPONSE_BYTES
    ): ConversationStateResponse = withContext(Dispatchers.IO) {
        get(buildTranscriptPath(input), ConversationStateResponse.serializer(), maxResponseBytes)
    }

    suspend fun getLiveState(
        conversationId: String,
        includeFeeds: Boolean = false,
        maxResponseBytes: Long = DEFAULT_MAX_TRANSCRIPT_RESPONSE_BYTES
    ): ConversationStateResponse = withContext(Dispatchers.IO) {
        val query = linkedMapOf<String, String>()
        if (includeFeeds) {
            query["includeFeeds"] = "true"
        }
        get(
            appendQuery("/v1/conversations/${encodePath(conversationId)}/live-state", query),
            ConversationStateResponse.serializer(),
            maxResponseBytes
        )
    }

    suspend fun listLinkedConversations(input: ListLinkedConversationsInput): LinkedConversationPage = withContext(Dispatchers.IO) {
        get(buildLinkedConversationPath(input), LinkedConversationPage.serializer())
    }

    suspend fun getFeedData(feedId: String, conversationId: String): FeedDataResponse = withContext(Dispatchers.IO) {
        val query = linkedMapOf<String, String>()
        if (conversationId.isNotBlank()) {
            query["conversationId"] = conversationId
        }
        val root = parseJson(restClient.get(endpointName, appendQuery("/v1/feeds/${encodePath(feedId)}/data", query)) { it })
        json.decodeFromJsonElement(FeedDataResponse.serializer(), root)
    }

    suspend fun listPendingElicitations(input: ListPendingElicitationsInput): List<PendingElicitationRecord> = withContext(Dispatchers.IO) {
        val query = appendQuery("/v1/elicitations", linkedMapOf("conversationId" to input.conversationId))
        val response = get(query, PendingElicitationsEnvelope.serializer())
        response.rows
    }

    suspend fun resolveElicitation(input: ResolveElicitationInput): Unit = withContext(Dispatchers.IO) {
        val path = "/v1/elicitations/${encodePath(input.conversationId)}/${encodePath(input.elicitationId)}/resolve"
        post(path, input, EmptyResponse.serializer())
    }

    suspend fun terminateConversation(conversationId: String): Unit = withContext(Dispatchers.IO) {
        post("/v1/conversations/${encodePath(conversationId)}/terminate", emptyMap<String, JsonElement>(), EmptyResponse.serializer())
    }

    suspend fun compactConversation(conversationId: String): Unit = withContext(Dispatchers.IO) {
        post("/v1/conversations/${encodePath(conversationId)}/compact", emptyMap<String, JsonElement>(), EmptyResponse.serializer())
    }

    suspend fun pruneConversation(conversationId: String): Unit = withContext(Dispatchers.IO) {
        post("/v1/conversations/${encodePath(conversationId)}/prune", emptyMap<String, JsonElement>(), EmptyResponse.serializer())
    }

    suspend fun listPendingToolApprovals(input: ListPendingToolApprovalsInput = ListPendingToolApprovalsInput()): List<PendingToolApproval> = withContext(Dispatchers.IO) {
        listPendingToolApprovalsPage(input).rows
    }

    suspend fun listPendingToolApprovalsPage(input: ListPendingToolApprovalsInput = ListPendingToolApprovalsInput()): PendingToolApprovalPage = withContext(Dispatchers.IO) {
        val query = linkedMapOf<String, String>()
        input.userId?.takeIf { it.isNotBlank() }?.let { query["userId"] = it }
        input.conversationId?.takeIf { it.isNotBlank() }?.let { query["conversationId"] = it }
        input.status?.takeIf { it.isNotBlank() }?.let { query["status"] = it }
        input.limit?.takeIf { it > 0 }?.let { query["limit"] = it.toString() }
        input.offset?.takeIf { it > 0 }?.let { query["offset"] = it.toString() }
        val root = parseJson(restClient.get(endpointName, appendQuery("/v1/tool-approvals/pending", query)) { it })
        when (root) {
            is JsonArray -> PendingToolApprovalPage(rows = decode(root, ListSerializer(PendingToolApproval.serializer())))
            is JsonObject -> when {
                root["rows"] != null -> json.decodeFromJsonElement(PendingToolApprovalPage.serializer(), root)
                root["data"] != null -> PendingToolApprovalPage(rows = json.decodeFromJsonElement(ToolApprovalsEnvelope.serializer(), root).data)
                else -> PendingToolApprovalPage()
            }
            else -> PendingToolApprovalPage()
        }
    }

    suspend fun decideToolApproval(input: DecideToolApprovalInput): DecideToolApprovalOutput = withContext(Dispatchers.IO) {
        val path = "/v1/tool-approvals/${encodePath(input.id)}/decision"
        post(path, input, DecideToolApprovalOutput.serializer())
    }

    suspend fun listResources(input: ListResourcesInput): ListResourcesOutput = withContext(Dispatchers.IO) {
        get(appendQuery("/v1/workspace/resources", linkedMapOf("kind" to input.kind)), ListResourcesOutput.serializer())
    }

    suspend fun getResource(input: ResourceRef): ResourcePayload = withContext(Dispatchers.IO) {
        get("/v1/workspace/resources/${encodePath(input.kind)}/${encodePath(input.name)}", ResourcePayload.serializer())
    }

    suspend fun saveResource(input: SaveResourceInput): Unit = withContext(Dispatchers.IO) {
        put("/v1/workspace/resources/${encodePath(input.kind)}/${encodePath(input.name)}", input.data, EmptyResponse.serializer())
    }

    suspend fun deleteResource(input: ResourceRef): Unit = withContext(Dispatchers.IO) {
        delete("/v1/workspace/resources/${encodePath(input.kind)}/${encodePath(input.name)}", EmptyResponse.serializer())
    }

    suspend fun exportResources(input: ExportResourcesInput): ExportResourcesOutput = withContext(Dispatchers.IO) {
        post("/v1/workspace/resources/export", input, ExportResourcesOutput.serializer())
    }

    suspend fun importResources(input: ImportResourcesInput): ImportResourcesOutput = withContext(Dispatchers.IO) {
        post("/v1/workspace/resources/import", input, ImportResourcesOutput.serializer())
    }

    suspend fun getSchedule(id: String): Schedule? = withContext(Dispatchers.IO) {
        get("/v1/api/agently/scheduler/schedule/${encodePath(id)}", ScheduleEnvelope.serializer()).data
    }

    suspend fun listSchedules(): List<Schedule> = withContext(Dispatchers.IO) {
        get("/v1/api/agently/scheduler/", ScheduleListEnvelope.serializer()).data.schedules
    }

    suspend fun upsertSchedules(schedules: List<Schedule>): Unit = withContext(Dispatchers.IO) {
        patch("/v1/api/agently/scheduler/", SchedulePatchInput(schedules), EmptyResponse.serializer())
    }

    suspend fun runScheduleNow(id: String): Unit = withContext(Dispatchers.IO) {
        post("/v1/api/agently/scheduler/run-now/${encodePath(id)}", emptyMap<String, JsonElement>(), EmptyResponse.serializer())
    }

    suspend fun listFiles(input: ListFilesInput): ListFilesOutput = withContext(Dispatchers.IO) {
        get(appendQuery("/v1/files", linkedMapOf("conversationId" to input.conversationId)), ListFilesOutput.serializer())
    }

    suspend fun uploadFile(input: UploadFileInput): UploadFileOutput = withContext(Dispatchers.IO) {
        require(input.conversationId.isNotBlank()) { "conversationId is required" }
        require(input.data.isNotEmpty()) { "file data is required" }

        val endpoint = requireNotNull(endpointRegistry.resolve(endpointName)) {
            "Endpoint not found: $endpointName"
        }
        val mediaType = input.contentType?.takeIf { it.isNotBlank() }?.toMediaTypeOrNull()
        val body = MultipartBody.Builder()
            .setType(MultipartBody.FORM)
            .addFormDataPart("conversationId", input.conversationId)
            .apply {
                input.name.takeIf { it.isNotBlank() }?.let { addFormDataPart("name", it) }
                input.contentType?.takeIf { it.isNotBlank() }?.let { addFormDataPart("contentType", it) }
                addFormDataPart(
                    "file",
                    input.name.ifBlank { "upload.bin" },
                    input.data.toRequestBody(mediaType)
                )
            }
            .build()

        val request = Request.Builder()
            .url(endpoint.baseUrl.trimEnd('/') + "/v1/files")
            .applyEndpointConfig(endpoint)
            .post(body)
            .build()
        val client = endpoint.httpClient ?: okhttp3.OkHttpClient()
        client.newCall(request).execute().use { response ->
            if (!response.isSuccessful) {
                error("POST ${request.url} failed: ${response.code}")
            }
            val root = parseJson(response.body?.string().orEmpty())
            return@withContext decode(root, UploadFileOutput.serializer())
        }
    }

    suspend fun getPayload(id: String, options: GetPayloadOptions = GetPayloadOptions()): PayloadView = withContext(Dispatchers.IO) {
        get(buildPayloadPath(id, options), PayloadView.serializer())
    }

    suspend fun getPayloads(ids: List<String>): Map<String, PayloadView> = withContext(Dispatchers.IO) {
        val payloadIds = ids.map { it.trim() }
            .filter { it.isNotEmpty() }
            .distinct()
        if (payloadIds.isEmpty()) {
            return@withContext emptyMap()
        }
        post(
            "/v1/api/payloads",
            GetPayloadsInput(payloadIds),
            MapSerializer(String.serializer(), PayloadView.serializer())
        )
    }

    suspend fun downloadPayload(
        id: String,
        maxBytes: Long? = null
    ): DownloadFileOutput = withContext(Dispatchers.IO) {
        downloadBinary(
            appendQuery("/v1/api/payload/${encodePath(id)}", linkedMapOf("raw" to "1")),
            maxBytes
        )
    }

    suspend fun listGeneratedFiles(conversationId: String): List<GeneratedFileEntry> = withContext(Dispatchers.IO) {
        get("/v1/api/conversations/${encodePath(conversationId)}/generated-files", GeneratedFileListSerializer)
    }

    suspend fun downloadGeneratedFile(id: String): DownloadFileOutput = withContext(Dispatchers.IO) {
        downloadBinary("/v1/api/generated-files/${encodePath(id)}/download")
    }

    suspend fun downloadFile(conversationId: String, fileId: String): DownloadFileOutput = withContext(Dispatchers.IO) {
        downloadBinary(
            appendQuery("/v1/files/${encodePath(fileId)}", linkedMapOf("conversationId" to conversationId, "raw" to "1"))
        )
    }

    suspend fun downloadWorkspaceFile(uri: String): String = withContext(Dispatchers.IO) {
        downloadText(appendQuery("/v1/workspace/file-browser/download", linkedMapOf("uri" to uri)))
    }

    suspend fun listWorkspaceFiles(uri: String): List<WorkspaceFileEntry> = withContext(Dispatchers.IO) {
        val target = appendQuery("/v1/workspace/file-browser/list", linkedMapOf("uri" to uri))
        get(target, WorkspaceFileEntriesEnvelope.serializer()).entries
    }

    suspend fun listToolDefinitions(): List<ToolDefinitionInfo> = withContext(Dispatchers.IO) {
        get("/v1/tools", ToolDefinitionsSerializer)
    }

    suspend fun executeTool(
        name: String,
        args: Map<String, JsonElement> = emptyMap(),
        conversationId: String? = null
    ): String = withContext(Dispatchers.IO) {
        val path = if (conversationId.isNullOrBlank()) {
            "/v1/tools/${encodePath(name)}/execute"
        } else {
            appendQuery("/v1/tools/${encodePath(name)}/execute", linkedMapOf("conversationId" to conversationId))
        }
        post(path, args, ToolExecuteEnvelope.serializer()).result.orEmpty()
    }

    suspend fun listUIEvents(input: ListUIEventsInput): ListUIEventsOutput = withContext(Dispatchers.IO) {
        require(input.conversationId.isNotBlank()) { "conversationId is required" }
        val args = linkedMapOf<String, JsonElement>()
        input.clientId?.trim()?.takeIf { it.isNotEmpty() }?.let { args["clientId"] = JsonPrimitive(it) }
        input.windowId?.trim()?.takeIf { it.isNotEmpty() }?.let { args["windowId"] = JsonPrimitive(it) }
        input.windowKey?.trim()?.takeIf { it.isNotEmpty() }?.let { args["windowKey"] = JsonPrimitive(it) }
        input.kinds
            .map { it.trim() }
            .filter { it.isNotEmpty() }
            .takeIf { it.isNotEmpty() }
            ?.let { kinds -> args["kinds"] = JsonArray(kinds.map { JsonPrimitive(it) }) }
        input.sinceSeq?.let { if (it > 0) args["sinceSeq"] = JsonPrimitive(it) }
        input.limit?.let { if (it > 0) args["limit"] = JsonPrimitive(it) }
        val raw = executeTool("ui/events:list", args, conversationId = input.conversationId)
        if (raw.isBlank()) {
            return@withContext ListUIEventsOutput(conversationId = input.conversationId)
        }
        json.decodeFromString(ListUIEventsOutput.serializer(), raw)
    }

    suspend fun executeMCPUIToolCall(input: MCPUIToolCallInput): MCPUIToolCallOutput = withContext(Dispatchers.IO) {
        post("/v1/api/mcp-ui/tools/call", input, MCPUIToolCallOutput.serializer())
    }

    @Suppress("UNUSED_PARAMETER")
    suspend fun listTemplates(input: ListTemplatesInput = ListTemplatesInput()): ListTemplatesOutput = withContext(Dispatchers.IO) {
        get("/v1/templates", ListTemplatesOutput.serializer())
    }

    suspend fun getTemplate(input: GetTemplateInput): GetTemplateOutput = withContext(Dispatchers.IO) {
        val name = input.name.trim()
        require(name.isNotEmpty()) { "template name is required" }
        val query = linkedMapOf<String, String>()
        input.includeDocument?.let { query["includeDocument"] = it.toString() }
        get(appendQuery("/v1/templates/${encodePath(name)}", query), GetTemplateOutput.serializer())
    }

    suspend fun listSkills(input: ListSkillsInput): ListSkillsOutput = withContext(Dispatchers.IO) {
        val query = linkedMapOf<String, String>()
        input.conversationId?.takeIf { it.isNotBlank() }?.let { query["conversationId"] = it }
        get(appendQuery("/v1/skills", query), ListSkillsOutput.serializer())
    }

    suspend fun activateSkill(input: ActivateSkillInput): ActivateSkillOutput = withContext(Dispatchers.IO) {
        val name = input.name?.takeIf { it.isNotBlank() } ?: error("Skill name is required")
        val query = linkedMapOf<String, String>()
        input.conversationId?.takeIf { it.isNotBlank() }?.let { query["conversationId"] = it }
        val body = buildMap<String, JsonElement> {
            input.args?.let { put("args", JsonPrimitive(it)) }
        }
        post(appendQuery("/v1/skills/${encodePath(name)}/activate", query), body, ActivateSkillOutput.serializer())
    }

    suspend fun getSkillDiagnostics(): SkillDiagnosticsOutput = withContext(Dispatchers.IO) {
        get("/v1/skills/diagnostics", SkillDiagnosticsOutput.serializer())
    }

    suspend fun getA2AAgentCard(agentId: String): AgentCard = withContext(Dispatchers.IO) {
        get("/v1/api/a2a/agents/${encodePath(agentId)}/card", AgentCard.serializer())
    }

    suspend fun sendA2AMessage(agentId: String, request: SendA2AMessageRequest): SendA2AMessageResponse = withContext(Dispatchers.IO) {
        post("/v1/api/a2a/agents/${encodePath(agentId)}/message", request, SendA2AMessageResponse.serializer())
    }

    suspend fun listA2AAgents(agentIds: List<String>): List<String> = withContext(Dispatchers.IO) {
        val ids = agentIds.joinToString(",")
        get("/v1/api/a2a/agents?ids=${urlEncode(ids)}", A2AAgentsEnvelope.serializer()).agents
    }

    fun streamEvents(conversationId: String): Flow<SSEEvent> =
        streamEvents(conversationId, onOpen = null)

    private fun streamEvents(conversationId: String, onOpen: (() -> Unit)?): Flow<SSEEvent> {
        val endpoint = requireNotNull(endpointRegistry.resolve(endpointName)) {
            "Endpoint not found: $endpointName"
        }
        return openEventStream(
            endpoint = endpoint,
            path = appendQuery("/v1/stream", linkedMapOf("conversationId" to conversationId)),
            conversationId = conversationId,
            json = json,
            onOpen = onOpen
        )
    }

    fun trackConversation(
        conversationId: String,
        maxResponseBytes: Long = DEFAULT_MAX_TRANSCRIPT_RESPONSE_BYTES
    ): Flow<ConversationStreamSnapshot> = channelFlow {
        val tracker = ConversationStreamTracker(conversationId)
        var reconnectDelayMs = 500L
        while (true) {
            val streamReady = CompletableDeferred<Unit>()
            val events = Channel<SSEEvent>(Channel.BUFFERED)
            val streamJob = launch(start = CoroutineStart.UNDISPATCHED) {
                try {
                    streamEvents(
                        conversationId,
                        onOpen = {
                            if (!streamReady.isCompleted) {
                                streamReady.complete(Unit)
                            }
                        }
                    ).collect { event ->
                        events.send(event)
                    }
                    if (!streamReady.isCompleted) {
                        streamReady.complete(Unit)
                    }
                } catch (error: Throwable) {
                    if (!streamReady.isCompleted) {
                        streamReady.completeExceptionally(error)
                    }
                    events.close(error)
                } finally {
                    events.close()
                }
            }
            val retryAfterMs = try {
                streamReady.await()
                val initialState = getLiveState(
                    conversationId,
                    includeFeeds = true,
                    maxResponseBytes = maxResponseBytes
                )
                tracker.hydrate(initialState)
                send(tracker.snapshot())
                for (event in events) {
                    if (event.type.trim().equals("stream_overflow", ignoreCase = true)) {
                        tracker.clear()
                        break
                    }
                    tracker.applyEvent(event, hydrationCursor = initialState.eventCursor)
                    send(tracker.snapshot())
                }
                reconnectDelayMs = 500L
                reconnectDelayMs
            } catch (error: CancellationException) {
                throw error
            } catch (_: Throwable) {
                // A mobile radio handoff or intermediary timeout must not end
                // observation of a server-side turn that is still running.
                val currentDelay = reconnectDelayMs
                reconnectDelayMs = (reconnectDelayMs * 2).coerceAtMost(5_000L)
                currentDelay
            } finally {
                streamJob.cancelAndJoin()
            }
            delay(retryAfterMs)
        }
    }

    private fun buildConversationListPath(input: ListConversationsInput): String {
        val query = linkedMapOf<String, String>()
        input.agentId?.takeIf { it.isNotBlank() }?.let { query["agentId"] = it }
        input.parentId?.takeIf { it.isNotBlank() }?.let { query["parentId"] = it }
        input.parentTurnId?.takeIf { it.isNotBlank() }?.let { query["parentTurnId"] = it }
        if (input.excludeScheduled == true) query["excludeScheduled"] = "true"
        input.query?.takeIf { it.isNotBlank() }?.let { query["q"] = it }
        input.status?.takeIf { it.isNotBlank() }?.let { query["status"] = it }
        input.page?.limit?.takeIf { it > 0 }?.let { query["limit"] = it.toString() }
        input.page?.cursor?.takeIf { it.isNotBlank() }?.let { query["cursor"] = it }
        input.page?.direction?.takeIf { it.isNotBlank() }?.let { query["direction"] = it }
        return appendQuery("/v1/conversations", query)
    }

    private fun buildMessagesPath(input: GetMessagesInput): String {
        val query = linkedMapOf<String, String>()
        query["conversationId"] = input.conversationId
        input.turnId?.takeIf { it.isNotBlank() }?.let { query["turnId"] = it }
        if (input.roles.isNotEmpty()) query["roles"] = input.roles.joinToString(",")
        if (input.types.isNotEmpty()) query["types"] = input.types.joinToString(",")
        input.page?.limit?.takeIf { it > 0 }?.let { query["limit"] = it.toString() }
        input.page?.cursor?.takeIf { it.isNotBlank() }?.let { query["cursor"] = it }
        input.page?.direction?.takeIf { it.isNotBlank() }?.let { query["direction"] = it }
        return appendQuery("/v1/messages", query)
    }

    private fun buildLinkedConversationPath(input: ListLinkedConversationsInput): String {
        val query = linkedMapOf<String, String>()
        input.parentConversationId?.takeIf { it.isNotBlank() }?.let { query["parentConversationId"] = it }
        input.parentTurnId?.takeIf { it.isNotBlank() }?.let { query["parentTurnId"] = it }
        input.page?.limit?.takeIf { it > 0 }?.let { query["limit"] = it.toString() }
        input.page?.cursor?.takeIf { it.isNotBlank() }?.let { query["cursor"] = it }
        input.page?.direction?.takeIf { it.isNotBlank() }?.let { query["direction"] = it }
        return appendQuery("/v1/conversations/linked", query)
    }

    private fun buildTranscriptPath(input: GetTranscriptInput): String {
        val query = linkedMapOf<String, String>()
        input.since?.takeIf { it.isNotBlank() }?.let { query["since"] = it }
        if (input.includeModelCalls == true) query["includeModelCalls"] = "true"
        if (input.includeToolCalls == true) query["includeToolCalls"] = "true"
        if (input.includeFeeds == true) query["includeFeeds"] = "true"
        return appendQuery("/v1/conversations/${encodePath(input.conversationId)}/transcript", query)
    }

    private fun buildPayloadPath(id: String, options: GetPayloadOptions): String {
        val query = linkedMapOf<String, String>()
        if (options.meta == true) query["meta"] = "1"
        if (options.inline == false) query["inline"] = "0"
        return appendQuery("/v1/api/payload/${encodePath(id)}", query)
    }

    private fun appendQuery(path: String, params: Map<String, String>): String {
        if (params.isEmpty()) {
            return path
        }
        val query = params.entries.joinToString("&") { (key, value) ->
            "${urlEncode(key)}=${urlEncode(value)}"
        }
        return "$path?$query"
    }

    private fun encodePath(value: String): String = urlEncode(value).replace("+", "%20")

    private fun urlEncode(value: String): String = URLEncoder.encode(value, StandardCharsets.UTF_8.toString())

    private fun parseJson(body: String): JsonElement = json.parseToJsonElement(
        body.takeIf { it.isNotBlank() } ?: "{}"
    )

    private fun downloadBinary(path: String, maxBytes: Long? = null): DownloadFileOutput {
        val endpoint = requireNotNull(endpointRegistry.resolve(endpointName)) {
            "Endpoint not found: $endpointName"
        }
        val request = Request.Builder()
            .url(endpoint.baseUrl.trimEnd('/') + "/" + path.trimStart('/'))
            .applyEndpointConfig(endpoint)
            .get()
            .build()
        val client = endpoint.httpClient ?: okhttp3.OkHttpClient()
        client.newCall(request).execute().use { response ->
            if (!response.isSuccessful) {
                error("GET ${request.url} failed: ${response.code}")
            }
            val body = if (maxBytes == null) {
                response.body?.bytes() ?: byteArrayOf()
            } else {
                readResponseBytes(response.body, maxBytes)
            }
            val disposition = response.header("Content-Disposition").orEmpty()
            val contentType = response.header("Content-Type")
            val inferredName = disposition.substringAfter("filename=", "").trim().trim('"').ifBlank {
                request.url.encodedPath.substringAfterLast('/')
            }
            return DownloadFileOutput(
                name = inferredName.ifBlank { null },
                contentType = contentType,
                data = body
            )
        }
    }

    private fun downloadText(path: String): String {
        val endpoint = requireNotNull(endpointRegistry.resolve(endpointName)) {
            "Endpoint not found: $endpointName"
        }
        val request = Request.Builder()
            .url(endpoint.baseUrl.trimEnd('/') + "/" + path.trimStart('/'))
            .applyEndpointConfig(endpoint)
            .get()
            .build()
        val client = endpoint.httpClient ?: okhttp3.OkHttpClient()
        client.newCall(request).execute().use { response ->
            if (!response.isSuccessful) {
                error("GET ${request.url} failed: ${response.code}")
            }
            return response.body?.string().orEmpty()
        }
    }

    // Visibility note: these were historically private. They are now
    // `internal` so same-module extension files (e.g. Lookups.kt) can
    // reuse the transport without duplicating it. They are NOT part of the
    // public API — the `internal` modifier keeps them package-scoped.
    internal fun <T> get(
        path: String,
        serializer: KSerializer<T>,
        maxResponseBytes: Long? = null
    ): T {
        val root = parseJson(restClient.get(endpointName, path, maxResponseBytes) { it })
        return decode(root, serializer)
    }

    internal fun getJson(path: String): JsonElement {
        val root = parseJson(restClient.get(endpointName, path) { it })
        return root.unwrapDataEnvelope()
    }

    internal fun <I, T> post(path: String, payload: I, serializer: KSerializer<T>): T {
        val request = json.encodeToString(serializerFor(payload), payload)
        val root = parseJson(restClient.post(endpointName, path, request) { it })
        return decode(root, serializer)
    }

    internal fun <I, T> postLongRunning(path: String, payload: I, serializer: KSerializer<T>): T {
        val request = json.encodeToString(serializerFor(payload), payload)
        val root = parseJson(restClient.postLongRunning(endpointName, path, request) { it })
        return decode(root, serializer)
    }

    private fun <I, T> patch(path: String, payload: I, serializer: KSerializer<T>): T {
        val request = json.encodeToString(serializerFor(payload), payload)
        val root = parseJson(restClient.patch(endpointName, path, request) { it })
        return decode(root, serializer)
    }

    private fun <T> put(path: String, payload: String, serializer: KSerializer<T>): T {
        val root = parseJson(restClient.put(endpointName, path, payload) { it })
        return decode(root, serializer)
    }

    internal fun <T> delete(path: String, serializer: KSerializer<T>): T {
        val root = parseJson(restClient.delete(endpointName, path) { it })
        return decode(root, serializer)
    }

    private fun <T> decode(root: JsonElement, serializer: KSerializer<T>): T {
        val payload = root.unwrapDataEnvelope()
        return json.decodeFromJsonElement(serializer, payload)
    }

    @Suppress("UNCHECKED_CAST")
    private fun <T> serializerFor(payload: T): KSerializer<T> = when (payload) {
        is QueryInput -> QueryInput.serializer() as KSerializer<T>
        is LocalLoginInput -> LocalLoginInput.serializer() as KSerializer<T>
        is OAuthInitiateInput -> OAuthInitiateInput.serializer() as KSerializer<T>
        is OAuthCallbackInput -> OAuthCallbackInput.serializer() as KSerializer<T>
        is CreateSessionInput -> CreateSessionInput.serializer() as KSerializer<T>
        is AttachSessionInput -> AttachSessionInput.serializer() as KSerializer<T>
        is OOBLoginInput -> OOBLoginInput.serializer() as KSerializer<T>
        is CreateConversationInput -> CreateConversationInput.serializer() as KSerializer<T>
        is UpdateConversationInput -> UpdateConversationInput.serializer() as KSerializer<T>
        is CreateGoalInput -> CreateGoalInput.serializer() as KSerializer<T>
        is UpdateGoalInput -> UpdateGoalInput.serializer() as KSerializer<T>
        is SteerTurnInput -> SteerTurnInput.serializer() as KSerializer<T>
        is MoveQueuedTurnInput -> MoveQueuedTurnInput.serializer() as KSerializer<T>
        is EditQueuedTurnInput -> EditQueuedTurnInput.serializer() as KSerializer<T>
        is ResolveElicitationInput -> ResolveElicitationInput.serializer() as KSerializer<T>
        is DecideToolApprovalInput -> DecideToolApprovalInput.serializer() as KSerializer<T>
        is MCPUIToolCallInput -> MCPUIToolCallInput.serializer() as KSerializer<T>
        is ActivateSkillInput -> ActivateSkillInput.serializer() as KSerializer<T>
        is GetPayloadsInput -> GetPayloadsInput.serializer() as KSerializer<T>
        is ExportResourcesInput -> ExportResourcesInput.serializer() as KSerializer<T>
        is ImportResourcesInput -> ImportResourcesInput.serializer() as KSerializer<T>
        is SchedulePatchInput -> SchedulePatchInput.serializer() as KSerializer<T>
        is SendA2AMessageRequest -> SendA2AMessageRequest.serializer() as KSerializer<T>
        is FetchDatasourceInput -> FetchDatasourceInput.serializer() as KSerializer<T>
        is FetchDatasourceBody -> FetchDatasourceBody.serializer() as KSerializer<T>
        is Map<*, *> -> MapSerializer(String.serializer(), JsonElement.serializer()) as KSerializer<T>
        else -> error("Unsupported payload type: ${payload?.let { it::class.qualifiedName } ?: "null"}")
    }
}

const val DEFAULT_MAX_TRANSCRIPT_RESPONSE_BYTES: Long = 16L * 1024 * 1024
const val DEFAULT_MAX_PAYLOAD_RESPONSE_BYTES: Long = 1L * 1024 * 1024

private fun applySessionDebug(
    endpoints: Map<String, EndpointConfig>,
    sessionDebug: SessionDebugOptions?
): Map<String, EndpointConfig> {
    val headers = sessionDebug?.headerMap().orEmpty()
    if (headers.isEmpty()) return endpoints
    return endpoints.mapValues { (_, endpoint) ->
        val existingProvider = endpoint.defaultHeadersProvider
        endpoint.copy(
            defaultHeadersProvider = {
                val base = linkedMapOf<String, String>()
                existingProvider?.invoke()?.forEach { (name, value) ->
                    if (name.isNotBlank() && value.isNotBlank()) {
                        base[name] = value
                    }
                }
                headers.forEach { (name, value) -> base[name] = value }
                base
            }
        )
    }
}

@kotlinx.serialization.Serializable
private data class PendingElicitationsEnvelope(
    val rows: List<PendingElicitationRecord> = emptyList()
)

@kotlinx.serialization.Serializable
private data class AuthProvidersEnvelope(
    val providers: List<AuthProvider> = emptyList()
)

@kotlinx.serialization.Serializable
private data class ToolApprovalsEnvelope(
    val data: List<PendingToolApproval> = emptyList()
)

@kotlinx.serialization.Serializable
private data class ToolExecuteEnvelope(
    val result: String? = null
)

@kotlinx.serialization.Serializable
private data class CancelTurnEnvelope(
    val cancelled: Boolean = false
)

private object ToolDefinitionsSerializer : KSerializer<List<ToolDefinitionInfo>> by kotlinx.serialization.builtins.ListSerializer(ToolDefinitionInfo.serializer())
private object GeneratedFileListSerializer : KSerializer<List<GeneratedFileEntry>> by kotlinx.serialization.builtins.ListSerializer(GeneratedFileEntry.serializer())

@kotlinx.serialization.Serializable
private data class A2AAgentsEnvelope(
    val agents: List<String> = emptyList()
)

@kotlinx.serialization.Serializable
private data class WorkspaceFileEntriesEnvelope(
    val entries: List<WorkspaceFileEntry> = emptyList()
)

@kotlinx.serialization.Serializable
private data class ScheduleEnvelope(
    val status: String? = null,
    val data: Schedule? = null
)

@kotlinx.serialization.Serializable
private data class ScheduleListEnvelopeData(
    val schedules: List<Schedule> = emptyList()
)

@kotlinx.serialization.Serializable
private data class ScheduleListEnvelope(
    val status: String? = null,
    val data: ScheduleListEnvelopeData = ScheduleListEnvelopeData()
)

@kotlinx.serialization.Serializable
private data class SchedulePatchInput(
    val schedules: List<Schedule> = emptyList()
)

@kotlinx.serialization.Serializable
internal data class EmptyResponse(
    val status: String? = null
)

private fun JsonElement.unwrapDataEnvelope(): JsonElement {
    val obj = this as? JsonObject ?: return this
    return obj["data"] ?: this
}
