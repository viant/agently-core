import Foundation

public final class AgentlyClient: Sendable {
    let endpoints: [String: EndpointConfig]
    let endpointName: String
    let session: URLSession
    let decoder: JSONDecoder
    let encoder: JSONEncoder

    public init(
        endpoints: [String: EndpointConfig],
        endpointName: String = "appAPI",
        sessionDebug: SessionDebugOptions? = nil,
        session: URLSession = .shared,
        decoder: JSONDecoder = .agently(),
        encoder: JSONEncoder = .agently()
    ) {
        if let sessionDebug {
            self.endpoints = endpoints.mapValues { endpoint in
                var copy = endpoint
                for (name, value) in sessionDebug.headerFields() {
                    copy.headers[name] = value
                }
                return copy
            }
        } else {
            self.endpoints = endpoints
        }
        self.endpointName = endpointName
        self.session = session
        self.decoder = decoder
        self.encoder = encoder
    }

    public func authProviders() async throws -> [AuthProvider] {
        try await get("/v1/api/auth/providers", as: [AuthProvider].self)
    }

    public func authMe() async throws -> AuthUser {
        try await get("/v1/api/auth/me", as: AuthUser.self)
    }

    public func localLogin(_ input: LocalLoginInput) async throws -> LocalLoginOutput {
        try await post("/v1/api/auth/local/login", body: input, as: LocalLoginOutput.self)
    }

    public func logout() async throws {
        let _: EmptyResponse = try await post("/v1/api/auth/logout", body: EmptyResponse(), as: EmptyResponse.self)
    }

    public func oauthInitiate() async throws -> OAuthInitiateOutput {
        try await post("/v1/api/auth/oauth/initiate", body: EmptyResponse(), as: OAuthInitiateOutput.self)
    }

    public func oauthCallback(_ input: OAuthCallbackInput) async throws -> OAuthCallbackOutput {
        try await post("/v1/api/auth/oauth/callback", body: input, as: OAuthCallbackOutput.self)
    }

    public func getOAuthConfig() async throws -> OAuthConfigOutput {
        try await get("/v1/api/auth/oauth/config", as: OAuthConfigOutput.self)
    }

    public func createAuthSession(_ input: CreateSessionInput) async throws -> CreateSessionOutput {
        try await post("/v1/api/auth/session", body: input, as: CreateSessionOutput.self)
    }

    public func oobLogin(_ input: OOBLoginInput) async throws -> OOBLoginOutput {
        try await post("/v1/api/auth/oob", body: input, as: OOBLoginOutput.self)
    }

    public func idpDelegate() async throws -> IDPDelegateOutput {
        try await post("/v1/api/auth/idp/delegate", body: EmptyResponse(), as: IDPDelegateOutput.self)
    }

    public func getWorkspaceMetadata(_ targetContext: MetadataTargetContext? = nil) async throws -> WorkspaceMetadata {
        var query: [URLQueryItem] = []
        if let platform = targetContext?.platform?.trimmingCharacters(in: .whitespacesAndNewlines), !platform.isEmpty {
            query.append(URLQueryItem(name: "platform", value: platform))
        }
        if let formFactor = targetContext?.formFactor?.trimmingCharacters(in: .whitespacesAndNewlines), !formFactor.isEmpty {
            query.append(URLQueryItem(name: "formFactor", value: formFactor))
        }
        if let surface = targetContext?.surface?.trimmingCharacters(in: .whitespacesAndNewlines), !surface.isEmpty {
            query.append(URLQueryItem(name: "surface", value: surface))
        }
        for capability in targetContext?.capabilities ?? [] {
            let trimmed = capability.trimmingCharacters(in: .whitespacesAndNewlines)
            if !trimmed.isEmpty {
                query.append(URLQueryItem(name: "capabilities", value: trimmed))
            }
        }
        return try await get("/v1/workspace/metadata", query: query, as: WorkspaceMetadata.self)
    }

    public func query(_ input: QueryInput) async throws -> QueryOutput {
        try await post("/v1/agent/query", body: input, as: QueryOutput.self)
    }

    public func createConversation(_ input: CreateConversationInput) async throws -> Conversation {
        try await post("/v1/conversations", body: input, as: Conversation.self)
    }

    public func listConversations(_ input: ListConversationsInput = ListConversationsInput()) async throws -> ConversationPage {
        try await get("/v1/conversations", query: conversationListQueryItems(from: input), as: ConversationPage.self)
    }

    public func getConversation(conversationID: String) async throws -> Conversation {
        try await get("/v1/conversations/\(encodePath(conversationID))", as: Conversation.self)
    }

    public func getRun(id: String) async throws -> RunView {
        try await get("/v1/runs/\(encodePath(id))", as: RunView.self)
    }

    public func updateConversation(conversationID: String, _ input: UpdateConversationInput) async throws -> Conversation {
        let data = try encoder.encode(input)
        return try await rawRequest(path: "/v1/conversations/\(encodePath(conversationID))", method: "PATCH", body: data, as: Conversation.self)
    }

    public func deleteConversation(conversationID: String) async throws {
        let _: EmptyResponse = try await rawRequest(path: "/v1/conversations/\(encodePath(conversationID))", method: "DELETE", as: EmptyResponse.self)
    }

    public func getMessages(_ input: GetMessagesInput) async throws -> MessagePage {
        try await get("/v1/messages", query: messageQueryItems(from: input), as: MessagePage.self)
    }

    public func listLinkedConversations(_ input: ListLinkedConversationsInput) async throws -> LinkedConversationPage {
        try await get("/v1/conversations/linked", query: linkedConversationQueryItems(from: input), as: LinkedConversationPage.self)
    }

    public func getLiveState(conversationID: String, includeFeeds: Bool = false) async throws -> ConversationStateResponse {
        let query = includeFeeds ? [URLQueryItem(name: "includeFeeds", value: "true")] : []
        return try await get("/v1/conversations/\(encodePath(conversationID))/live-state", query: query, as: ConversationStateResponse.self)
    }

    public func getTranscript(_ input: GetTranscriptInput) async throws -> ConversationStateResponse {
        let encodedConversationID = encodePath(input.conversationID)
        return try await get(
            "/v1/conversations/\(encodedConversationID)/transcript",
            query: queryItems(from: input).filter { $0.name != "conversationId" },
            as: ConversationStateResponse.self
        )
    }

    public func listPendingElicitations(_ input: ListPendingElicitationsInput) async throws -> [PendingElicitationRecord] {
        let data = try await rawDataRequest(path: "/v1/elicitations", method: "GET", query: queryItems(from: input))
        if let rows = try? decoder.decode([PendingElicitationRecord].self, from: data) {
            return rows
        }
        return try decoder.decode(PendingElicitationRows.self, from: data).rows
    }

    public func resolveElicitation(_ input: ResolveElicitationInput) async throws {
        let _: EmptyResponse = try await post(
            "/v1/elicitations/\(input.conversationID)/\(input.elicitationID)/resolve",
            body: input,
            as: EmptyResponse.self
        )
    }

    public func listPendingToolApprovals(_ input: ListPendingToolApprovalsInput = ListPendingToolApprovalsInput()) async throws -> [PendingToolApproval] {
        (try await listPendingToolApprovalsPage(input)).rows
    }

    public func listPendingToolApprovalsPage(_ input: ListPendingToolApprovalsInput = ListPendingToolApprovalsInput()) async throws -> PendingToolApprovalPage {
        let data = try await rawDataRequest(path: "/v1/tool-approvals/pending", method: "GET", query: try approvalQueryItems(from: input))
        if let rows = try? decoder.decode([PendingToolApproval].self, from: data) {
            return PendingToolApprovalPage(rows: rows)
        }
        if let page = try? decoder.decode(PendingToolApprovalPage.self, from: data) {
            return page
        }
        if let wrapped = try? decoder.decode(PendingToolApprovalRows.self, from: data) {
            return PendingToolApprovalPage(rows: wrapped.rows)
        }
        return try decoder.decode(ToolApprovalsEnvelope.self, from: data).page
    }

    public func decideToolApproval(_ input: DecideToolApprovalInput) async throws -> DecideToolApprovalOutput {
        try await post("/v1/tool-approvals/\(input.id)/decision", body: input, as: DecideToolApprovalOutput.self)
    }

    public func listResources(_ input: ListResourcesInput) async throws -> ListResourcesOutput {
        try await get(
            "/v1/workspace/resources",
            query: [URLQueryItem(name: "kind", value: input.kind)],
            as: ListResourcesOutput.self
        )
    }

    public func getResource(_ input: ResourceRef) async throws -> ResourcePayload {
        try await get(
            "/v1/workspace/resources/\(encodePath(input.kind))/\(encodePath(input.name))",
            as: ResourcePayload.self
        )
    }

    public func saveResource(_ input: SaveResourceInput) async throws {
        let _: EmptyResponse = try await rawRequest(
            path: "/v1/workspace/resources/\(encodePath(input.kind))/\(encodePath(input.name))",
            method: "PUT",
            body: Data(input.data.utf8),
            contentType: "text/plain; charset=utf-8",
            as: EmptyResponse.self
        )
    }

    public func deleteResource(_ input: ResourceRef) async throws {
        let _: EmptyResponse = try await rawRequest(
            path: "/v1/workspace/resources/\(encodePath(input.kind))/\(encodePath(input.name))",
            method: "DELETE",
            as: EmptyResponse.self
        )
    }

    public func exportResources(_ input: ExportResourcesInput) async throws -> ExportResourcesOutput {
        try await post("/v1/workspace/resources/export", body: input, as: ExportResourcesOutput.self)
    }

    public func importResources(_ input: ImportResourcesInput) async throws -> ImportResourcesOutput {
        try await post("/v1/workspace/resources/import", body: input, as: ImportResourcesOutput.self)
    }

    public func getSchedule(id: String) async throws -> Schedule? {
        try await get("/v1/api/agently/scheduler/schedule/\(encodePath(id))", as: ScheduleEnvelope.self).data
    }

    public func listSchedules() async throws -> [Schedule] {
        try await get("/v1/api/agently/scheduler/", as: ScheduleListEnvelope.self).data.schedules
    }

    public func upsertSchedules(_ schedules: [Schedule]) async throws {
        let _: EmptyResponse = try await rawRequest(
            path: "/v1/api/agently/scheduler/",
            method: "PATCH",
            body: try encoder.encode(SchedulePatchInput(schedules: schedules)),
            as: EmptyResponse.self
        )
    }

    public func runScheduleNow(id: String) async throws {
        let _: EmptyResponse = try await post(
            "/v1/api/agently/scheduler/run-now/\(encodePath(id))",
            body: EmptyResponse(),
            as: EmptyResponse.self
        )
    }

    public func cancelTurn(id: String) async throws {
        _ = try await cancelTurn(turnID: id)
    }

    public func cancelTurn(turnID: String) async throws -> Bool {
        try await post("/v1/turns/\(encodePath(turnID))/cancel", body: EmptyResponse(), as: CancelTurnResponse.self).cancelled
    }

    public func steerTurn(_ input: SteerTurnInput) async throws -> SteerTurnOutput {
        try await post(
            "/v1/conversations/\(encodePath(input.conversationID))/turns/\(encodePath(input.turnID))/steer",
            body: input,
            as: SteerTurnOutput.self
        )
    }

    public func cancelQueuedTurn(conversationID: String, turnID: String) async throws {
        let _: EmptyResponse = try await rawRequest(
            path: "/v1/conversations/\(encodePath(conversationID))/turns/\(encodePath(turnID))",
            method: "DELETE",
            as: EmptyResponse.self
        )
    }

    public func moveQueuedTurn(_ input: MoveQueuedTurnInput) async throws {
        let _: EmptyResponse = try await post(
            "/v1/conversations/\(encodePath(input.conversationID))/turns/\(encodePath(input.turnID))/move",
            body: input,
            as: EmptyResponse.self
        )
    }

    public func editQueuedTurn(_ input: EditQueuedTurnInput) async throws {
        let _: EmptyResponse = try await rawRequest(
            path: "/v1/conversations/\(encodePath(input.conversationID))/turns/\(encodePath(input.turnID))",
            method: "PATCH",
            body: try encoder.encode(input),
            as: EmptyResponse.self
        )
    }

    public func forceSteerQueuedTurn(conversationID: String, turnID: String) async throws -> SteerTurnOutput {
        try await post(
            "/v1/conversations/\(encodePath(conversationID))/turns/\(encodePath(turnID))/force-steer",
            body: EmptyResponse(),
            as: SteerTurnOutput.self
        )
    }

    public func terminateConversation(conversationID: String) async throws {
        let _: EmptyResponse = try await post(
            "/v1/conversations/\(encodePath(conversationID))/terminate",
            body: EmptyResponse(),
            as: EmptyResponse.self
        )
    }

    public func compactConversation(conversationID: String) async throws {
        let _: EmptyResponse = try await post(
            "/v1/conversations/\(encodePath(conversationID))/compact",
            body: EmptyResponse(),
            as: EmptyResponse.self
        )
    }

    public func pruneConversation(conversationID: String) async throws {
        let _: EmptyResponse = try await post(
            "/v1/conversations/\(encodePath(conversationID))/prune",
            body: EmptyResponse(),
            as: EmptyResponse.self
        )
    }

    public func uploadFile(_ input: UploadFileInput) async throws -> UploadFileOutput {
        let boundary = "Boundary-\(UUID().uuidString)"
        let body = makeMultipartBody(input: input, boundary: boundary)
        return try await rawRequest(
            path: "/v1/files",
            method: "POST",
            body: body,
            contentType: "multipart/form-data; boundary=\(boundary)",
            as: UploadFileOutput.self
        )
    }

    public func listFiles(_ input: ListFilesInput) async throws -> ListFilesOutput {
        try await get("/v1/files", query: queryItems(from: input), as: ListFilesOutput.self)
    }

    public func getPayload(id: String, options: GetPayloadOptions = GetPayloadOptions()) async throws -> PayloadView {
        try await get("/v1/api/payload/\(encodePath(id))", query: payloadQueryItems(from: options), as: PayloadView.self)
    }

    public func getPayloads(ids: [String]) async throws -> [String: PayloadView] {
        var seen = Set<String>()
        let payloadIDs = ids.compactMap { rawID -> String? in
            let payloadID = rawID.trimmingCharacters(in: .whitespacesAndNewlines)
            guard !payloadID.isEmpty, seen.insert(payloadID).inserted else {
                return nil
            }
            return payloadID
        }
        guard !payloadIDs.isEmpty else {
            return [:]
        }
        return try await post("/v1/api/payloads", body: GetPayloadsInput(ids: payloadIDs), as: [String: PayloadView].self)
    }

    public func downloadPayload(id: String) async throws -> DownloadFileOutput {
        try await downloadBinary(
            path: "/v1/api/payload/\(encodePath(id))",
            query: [URLQueryItem(name: "raw", value: "1")]
        )
    }

    public func listGeneratedFiles(conversationID: String) async throws -> [GeneratedFileEntry] {
        try await get("/v1/api/conversations/\(encodePath(conversationID))/generated-files", as: [GeneratedFileEntry].self)
    }

    public func downloadGeneratedFile(id: String) async throws -> DownloadFileOutput {
        try await downloadBinary(path: "/v1/api/generated-files/\(encodePath(id))/download")
    }

    public func downloadFile(conversationID: String, fileID: String) async throws -> DownloadFileOutput {
        try await downloadBinary(
            path: "/v1/files/\(encodePath(fileID))",
            query: [
                URLQueryItem(name: "conversationId", value: conversationID),
                URLQueryItem(name: "raw", value: "1")
            ]
        )
    }

    public func getFeedData(feedID: String, conversationID: String) async throws -> FeedDataResponse {
        let query = conversationID.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
            ? []
            : [URLQueryItem(name: "conversationId", value: conversationID)]
        return try await get("/v1/feeds/\(encodePath(feedID))/data", query: query, as: FeedDataResponse.self)
    }

    public func listWorkspaceFiles(uri: String) async throws -> [WorkspaceFileEntry] {
        let data = try await rawDataRequest(
            path: "/v1/workspace/file-browser/list",
            method: "GET",
            query: [URLQueryItem(name: "uri", value: uri)]
        )
        if let entries = try? decoder.decode([WorkspaceFileEntry].self, from: data) {
            return entries
        }
        return try decoder.decode(WorkspaceFileEntriesEnvelope.self, from: data).entries
    }

    public func downloadWorkspaceFile(uri: String) async throws -> String {
        let data = try await rawDataRequest(
            path: "/v1/workspace/file-browser/download",
            method: "GET",
            query: [URLQueryItem(name: "uri", value: uri)]
        )
        return String(data: data, encoding: .utf8) ?? ""
    }

    public func listToolDefinitions() async throws -> [ToolDefinitionInfo] {
        try await get("/v1/tools", as: [ToolDefinitionInfo].self)
    }

    public func executeTool(name: String, args: [String: JSONValue] = [:]) async throws -> String {
        try await post("/v1/tools/\(encodePath(name))/execute", body: args, as: ToolExecuteEnvelope.self).result ?? ""
    }

    public func executeMCPUIToolCall(_ input: MCPUIToolCallInput) async throws -> MCPUIToolCallOutput {
        try await post("/v1/api/mcp-ui/tools/call", body: input, as: MCPUIToolCallOutput.self)
    }

    public func listTemplates(_ input: ListTemplatesInput = ListTemplatesInput()) async throws -> ListTemplatesOutput {
        _ = input
        return try await get("/v1/templates", as: ListTemplatesOutput.self)
    }

    public func getTemplate(_ input: GetTemplateInput) async throws -> GetTemplateOutput {
        let name = input.name.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !name.isEmpty else {
            throw AgentlySDKError.invalidResponse
        }
        let query = input.includeDocument.map {
            [URLQueryItem(name: "includeDocument", value: String($0))]
        } ?? []
        return try await get("/v1/templates/\(encodePath(name))", query: query, as: GetTemplateOutput.self)
    }

    public func listSkills(_ input: ListSkillsInput) async throws -> ListSkillsOutput {
        try await get("/v1/skills", query: try queryItems(from: input), as: ListSkillsOutput.self)
    }

    public func activateSkill(_ input: ActivateSkillInput) async throws -> ActivateSkillOutput {
        let name = (input.name ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
        guard !name.isEmpty else {
            throw AgentlySDKError.invalidResponse
        }
        let query = input.conversationID.map {
            [URLQueryItem(name: "conversationId", value: $0)]
        } ?? []
        struct ActivateSkillRequest: Encodable {
            let args: String?
        }
        return try await rawRequest(
            path: "/v1/skills/\(encodePath(name))/activate",
            method: "POST",
            query: query,
            body: try encoder.encode(ActivateSkillRequest(args: input.args)),
            as: ActivateSkillOutput.self
        )
    }

    public func getSkillDiagnostics() async throws -> SkillDiagnosticsOutput {
        try await get("/v1/skills/diagnostics", as: SkillDiagnosticsOutput.self)
    }

    public func getA2AAgentCard(agentID: String) async throws -> AgentCard {
        try await get("/v1/api/a2a/agents/\(encodePath(agentID))/card", as: AgentCard.self)
    }

    public func sendA2AMessage(agentID: String, request: SendA2AMessageRequest) async throws -> SendA2AMessageResponse {
        try await post("/v1/api/a2a/agents/\(encodePath(agentID))/message", body: request, as: SendA2AMessageResponse.self)
    }

    public func listA2AAgents(agentIDs: [String]) async throws -> [String] {
        let ids = agentIDs.joined(separator: ",")
        return try await get(
            "/v1/api/a2a/agents",
            query: [URLQueryItem(name: "ids", value: ids)],
            as: A2AAgentsEnvelope.self
        ).agents
    }

    public func streamEvents(conversationID: String) -> AsyncThrowingStream<SSEEvent, Error> {
        guard let endpoint = endpoints[endpointName] else {
            return AsyncThrowingStream { continuation in
                continuation.finish(throwing: AgentlySDKError.missingEndpoint(endpointName))
            }
        }
        let query = agentlyPercentEncodedQuery([
            URLQueryItem(name: "conversationId", value: conversationID)
        ])
        return openEventStream(
            endpoint: endpoint,
            path: "/v1/stream?\(query)",
            conversationID: conversationID,
            session: session
        )
    }

    public func trackConversation(conversationID: String) -> AsyncThrowingStream<ConversationStreamSnapshot, Error> {
        trackConversation(
            conversationID: conversationID,
            initialStateLoader: { [self] id in
                try await getLiveState(conversationID: id, includeFeeds: true)
            },
            eventStream: { [self] id in
                streamEvents(conversationID: id)
            }
        )
    }

    func trackConversation(
        conversationID: String,
        initialStateLoader: @escaping @Sendable (String) async throws -> ConversationStateResponse,
        eventStream: @escaping @Sendable (String) -> AsyncThrowingStream<SSEEvent, Error>
    ) -> AsyncThrowingStream<ConversationStreamSnapshot, Error> {
        AsyncThrowingStream { continuation in
            let task = Task {
                do {
                    let tracker = ConversationStreamTracker()
                    let events = eventStream(conversationID)
                    let initialState = try await initialStateLoader(conversationID)
                    await tracker.hydrate(initialState)
                    continuation.yield(await tracker.currentSnapshot())
                    for try await event in events {
                        continuation.yield(await tracker.apply(event, hydrationCursor: initialState.eventCursor))
                    }
                    continuation.finish()
                } catch {
                    continuation.finish(throwing: error)
                }
            }
            continuation.onTermination = { _ in
                task.cancel()
            }
        }
    }

    private func endpoint() throws -> EndpointConfig {
        guard let endpoint = endpoints[endpointName] else {
            throw AgentlySDKError.missingEndpoint(endpointName)
        }
        return endpoint
    }

    // `internal` (not `private`) so extensions on AgentlyClient in
    // sibling files (e.g. Lookups.swift) can call the shared transport
    // helpers without each file re-implementing them. Module-scope
    // access is the narrowest level that still works across files in
    // Swift — extensions cannot reach `private` declarations even
    // within the same module.
    func get<T: Decodable>(_ path: String, query: [URLQueryItem] = [], as type: T.Type) async throws -> T {
        try await rawRequest(path: path, method: "GET", query: query, as: type)
    }

    func post<Body: Encodable, T: Decodable>(_ path: String, body: Body, as type: T.Type) async throws -> T {
        let data = try encoder.encode(body)
        return try await rawRequest(path: path, method: "POST", body: data, as: type)
    }

    private func downloadBinary(path: String, query: [URLQueryItem] = []) async throws -> DownloadFileOutput {
        let builder = RequestBuilder(endpoint: try endpoint(), encoder: encoder)
        let request = try builder.makeRequest(
            path: path,
            method: "GET",
            queryItems: query,
            contentType: "application/octet-stream"
        )
        let (data, response) = try await session.data(for: request)
        guard let http = response as? HTTPURLResponse else {
            throw AgentlySDKError.invalidResponse
        }
        guard (200..<300).contains(http.statusCode) else {
            let message = String(data: data, encoding: .utf8)?
                .trimmingCharacters(in: .whitespacesAndNewlines)
            throw AgentlySDKError.httpStatus(http.statusCode, message)
        }
        let disposition = http.value(forHTTPHeaderField: "Content-Disposition")
        let inferredName = inferredFilename(from: disposition) ?? request.url?.lastPathComponent
        return DownloadFileOutput(
            name: inferredName?.trimmingCharacters(in: .whitespacesAndNewlines),
            contentType: http.value(forHTTPHeaderField: "Content-Type"),
            data: data
        )
    }

    func rawRequest<T: Decodable>(
        path: String,
        method: String,
        query: [URLQueryItem] = [],
        body: Data? = nil,
        contentType: String = "application/json",
        as type: T.Type
    ) async throws -> T {
        let builder = RequestBuilder(endpoint: try endpoint(), encoder: encoder)
        let request = try builder.makeRequest(
            path: path,
            method: method,
            queryItems: query,
            body: body,
            contentType: contentType
        )
        let (data, response) = try await session.data(for: request)
        try validate(response: response, data: data)
        return try decoder.decode(T.self, from: data)
    }

    func rawDataRequest(
        path: String,
        method: String,
        query: [URLQueryItem] = [],
        body: Data? = nil,
        contentType: String = "application/json"
    ) async throws -> Data {
        let builder = RequestBuilder(endpoint: try endpoint(), encoder: encoder)
        let request = try builder.makeRequest(
            path: path,
            method: method,
            queryItems: query,
            body: body,
            contentType: contentType
        )
        let (data, response) = try await session.data(for: request)
        try validate(response: response, data: data)
        return data
    }

    private func validate(response: URLResponse, data: Data) throws {
        guard let http = response as? HTTPURLResponse else {
            throw AgentlySDKError.invalidResponse
        }
        guard (200..<300).contains(http.statusCode) else {
            let message = String(data: data, encoding: .utf8)?
                .trimmingCharacters(in: .whitespacesAndNewlines)
            throw AgentlySDKError.httpStatus(http.statusCode, message)
        }
    }

    private func queryItems<Body: Encodable>(from value: Body) throws -> [URLQueryItem] {
        let data = try encoder.encode(value)
        let object = try JSONSerialization.jsonObject(with: data) as? [String: Any] ?? [:]
        return object.compactMap { key, value in
            guard !(value is NSNull) else { return nil }
            return URLQueryItem(name: key, value: String(describing: value))
        }
    }

    private func approvalQueryItems(from value: ListPendingToolApprovalsInput) throws -> [URLQueryItem] {
        var items: [URLQueryItem] = []
        if let userID = value.userID?.trimmingCharacters(in: .whitespacesAndNewlines), !userID.isEmpty {
            items.append(URLQueryItem(name: "userId", value: userID))
        }
        if let conversationID = value.conversationID?.trimmingCharacters(in: .whitespacesAndNewlines), !conversationID.isEmpty {
            items.append(URLQueryItem(name: "conversationId", value: conversationID))
        }
        if let status = value.status?.trimmingCharacters(in: .whitespacesAndNewlines), !status.isEmpty {
            items.append(URLQueryItem(name: "status", value: status))
        }
        if let limit = value.limit {
            items.append(URLQueryItem(name: "limit", value: String(limit)))
        }
        if let offset = value.offset {
            items.append(URLQueryItem(name: "offset", value: String(offset)))
        }
        return items
    }

    private func messageQueryItems(from value: GetMessagesInput) -> [URLQueryItem] {
        var items = [URLQueryItem(name: "conversationId", value: value.conversationID)]
        if let turnID = value.turnID?.trimmingCharacters(in: .whitespacesAndNewlines), !turnID.isEmpty {
            items.append(URLQueryItem(name: "turnId", value: turnID))
        }
        if !value.roles.isEmpty {
            items.append(URLQueryItem(name: "roles", value: value.roles.joined(separator: ",")))
        }
        if !value.types.isEmpty {
            items.append(URLQueryItem(name: "types", value: value.types.joined(separator: ",")))
        }
        appendPageItems(value.page, to: &items)
        return items
    }

    private func linkedConversationQueryItems(from value: ListLinkedConversationsInput) -> [URLQueryItem] {
        var items: [URLQueryItem] = []
        if let parentConversationID = value.parentConversationID?.trimmingCharacters(in: .whitespacesAndNewlines), !parentConversationID.isEmpty {
            items.append(URLQueryItem(name: "parentConversationId", value: parentConversationID))
        }
        if let parentTurnID = value.parentTurnID?.trimmingCharacters(in: .whitespacesAndNewlines), !parentTurnID.isEmpty {
            items.append(URLQueryItem(name: "parentTurnId", value: parentTurnID))
        }
        appendPageItems(value.page, to: &items)
        return items
    }

    private func payloadQueryItems(from value: GetPayloadOptions) -> [URLQueryItem] {
        var items: [URLQueryItem] = []
        if value.meta == true {
            items.append(URLQueryItem(name: "meta", value: "1"))
        }
        if value.inline == false {
            items.append(URLQueryItem(name: "inline", value: "0"))
        }
        return items
    }

    private func appendPageItems(_ page: PageInput?, to items: inout [URLQueryItem]) {
        if let limit = page?.limit, limit > 0 {
            items.append(URLQueryItem(name: "limit", value: String(limit)))
        }
        if let cursor = page?.cursor?.trimmingCharacters(in: .whitespacesAndNewlines), !cursor.isEmpty {
            items.append(URLQueryItem(name: "cursor", value: cursor))
        }
        if let direction = page?.direction?.trimmingCharacters(in: .whitespacesAndNewlines), !direction.isEmpty {
            items.append(URLQueryItem(name: "direction", value: direction))
        }
    }

    private func conversationListQueryItems(from value: ListConversationsInput) -> [URLQueryItem] {
        var items: [URLQueryItem] = []
        if let agentID = value.agentID?.trimmingCharacters(in: .whitespacesAndNewlines), !agentID.isEmpty {
            items.append(URLQueryItem(name: "agentId", value: agentID))
        }
        if let parentID = value.parentID?.trimmingCharacters(in: .whitespacesAndNewlines), !parentID.isEmpty {
            items.append(URLQueryItem(name: "parentId", value: parentID))
        }
        if let parentTurnID = value.parentTurnID?.trimmingCharacters(in: .whitespacesAndNewlines), !parentTurnID.isEmpty {
            items.append(URLQueryItem(name: "parentTurnId", value: parentTurnID))
        }
        if let excludeScheduled = value.excludeScheduled {
            items.append(URLQueryItem(name: "excludeScheduled", value: excludeScheduled ? "true" : "false"))
        }
        if let query = value.query?.trimmingCharacters(in: .whitespacesAndNewlines), !query.isEmpty {
            items.append(URLQueryItem(name: "q", value: query))
        }
        if let status = value.status?.trimmingCharacters(in: .whitespacesAndNewlines), !status.isEmpty {
            items.append(URLQueryItem(name: "status", value: status))
        }
        if let limit = value.page?.limit {
            items.append(URLQueryItem(name: "limit", value: String(limit)))
        }
        if let cursor = value.page?.cursor?.trimmingCharacters(in: .whitespacesAndNewlines), !cursor.isEmpty {
            items.append(URLQueryItem(name: "cursor", value: cursor))
        }
        if let direction = value.page?.direction?.trimmingCharacters(in: .whitespacesAndNewlines), !direction.isEmpty {
            items.append(URLQueryItem(name: "direction", value: direction))
        }
        return items
    }

    private func encodePath(_ value: String) -> String {
        agentlyPercentEncodedPathSegment(value)
    }

    private func makeMultipartBody(input: UploadFileInput, boundary: String) -> Data {
        var data = Data()
        func append(_ string: String) {
            data.append(string.data(using: .utf8)!)
        }

        append("--\(boundary)\r\n")
        append("Content-Disposition: form-data; name=\"conversationId\"\r\n\r\n")
        append("\(input.conversationID)\r\n")
        append("--\(boundary)\r\n")
        append("Content-Disposition: form-data; name=\"file\"; filename=\"\(input.name)\"\r\n")
        append("Content-Type: \(input.contentType ?? "application/octet-stream")\r\n\r\n")
        data.append(input.data)
        append("\r\n--\(boundary)--\r\n")
        return data
    }

    private func inferredFilename(from contentDisposition: String?) -> String? {
        guard let contentDisposition, !contentDisposition.isEmpty else {
            return nil
        }
        let filename = contentDisposition
            .components(separatedBy: "filename=")
            .dropFirst()
            .first?
            .trimmingCharacters(in: .whitespacesAndNewlines)
            .trimmingCharacters(in: CharacterSet(charactersIn: "\""))
        return filename?.isEmpty == false ? filename : nil
    }
}

private struct ToolApprovalsEnvelope: Codable {
    let data: [PendingToolApproval]

    var page: PendingToolApprovalPage {
        PendingToolApprovalPage(rows: data)
    }
}

private struct WorkspaceFileEntriesEnvelope: Codable {
    let entries: [WorkspaceFileEntry]
}

private struct ToolExecuteEnvelope: Codable {
    let result: String?
}

private struct A2AAgentsEnvelope: Codable {
    let agents: [String]
}

private struct ScheduleEnvelope: Codable {
    let status: String?
    let data: Schedule?
}

private struct ScheduleListEnvelope: Codable {
    let status: String?
    let data: ScheduleListEnvelopeData
}

private struct ScheduleListEnvelopeData: Codable {
    let schedules: [Schedule]
}

private struct SchedulePatchInput: Codable {
    let schedules: [Schedule]
}

private let uiBridgeSessionHeader = "Mcp-Session-Id"

public actor UIBridgeRPCClient {
    private let client: AgentlyClient
    private var sessionID: String?

    public init(client: AgentlyClient) {
        self.client = client
    }

    public func resetSession() {
        sessionID = nil
    }

    public func hello(clientID: String) async throws -> [String: JSONValue]? {
        try await rpcObject(
            method: "ui.hello",
            params: [
                "clientId": .string(clientID)
            ],
            includeSession: false
        )
    }

    public func poll(clientID: String, timeoutMs: Int = 20_000) async throws -> [String: JSONValue]? {
        try await rpcObject(
            method: "ui.poll",
            params: [
                "clientId": .string(clientID),
                "timeoutMs": .number(Double(timeoutMs))
            ]
        )
    }

    @discardableResult
    public func respond(
        commandID: String,
        ok: Bool,
        result: JSONValue? = nil,
        error: String? = nil
    ) async throws -> [String: JSONValue]? {
        var params: [String: JSONValue] = [
            "id": .string(commandID),
            "ok": .bool(ok)
        ]
        if let result {
            params["result"] = result
        }
        if let error, !error.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
            params["error"] = .string(error)
        }
        return try await rpcObject(method: "ui.response", params: params)
    }

    @discardableResult
    public func snapshot(clientID: String, data: JSONValue) async throws -> [String: JSONValue]? {
        try await rpcObject(
            method: "ui.snapshot",
            params: [
                "clientId": .string(clientID),
                "data": data
            ]
        )
    }

    private func rpcObject(
        method: String,
        params: [String: JSONValue],
        includeSession: Bool = true
    ) async throws -> [String: JSONValue]? {
        guard let endpoint = client.endpoints[client.endpointName] else {
            throw AgentlySDKError.missingEndpoint(client.endpointName)
        }
        let builder = RequestBuilder(endpoint: endpoint, encoder: client.encoder)
        let payload = UIBridgeRPCRequest(
            id: UUID().uuidString,
            method: method,
            params: .object(params)
        )
        var request = try builder.makeRequest(
            path: "/v1/ui/rpc",
            method: "POST",
            body: try client.encoder.encode(payload),
            contentType: "application/json"
        )
        if includeSession, let sessionID, !sessionID.isEmpty {
            request.setValue(sessionID, forHTTPHeaderField: uiBridgeSessionHeader)
        }
        let (data, response) = try await client.session.data(for: request)
        guard let http = response as? HTTPURLResponse else {
            throw AgentlySDKError.invalidResponse
        }
        if let updatedSessionID = http.value(forHTTPHeaderField: uiBridgeSessionHeader)?
            .trimmingCharacters(in: .whitespacesAndNewlines),
           !updatedSessionID.isEmpty {
            sessionID = updatedSessionID
        }
        if http.statusCode == 401 || http.statusCode == 403 || http.statusCode == 404 {
            sessionID = nil
            return nil
        }
        guard (200..<300).contains(http.statusCode) else {
            throw AgentlySDKError.httpStatus(http.statusCode, String(data: data, encoding: .utf8))
        }
        guard !data.isEmpty else {
            return nil
        }
        let envelope = try client.decoder.decode(UIBridgeRPCEnvelope.self, from: data)
        if let error = envelope.error {
            throw AgentlySDKError.rpcError(error.code, error.message)
        }
        switch envelope.result {
        case .object(let value):
            return value
        default:
            return nil
        }
    }
}

private struct CancelTurnResponse: Codable, Sendable {
    let cancelled: Bool
}

private struct UIBridgeRPCRequest: Encodable {
    let jsonrpc = "2.0"
    let id: String
    let method: String
    let params: JSONValue
}

private struct UIBridgeRPCEnvelope: Decodable {
    let jsonrpc: String?
    let id: JSONValue?
    let result: JSONValue?
    let error: UIBridgeRPCError?
}

private struct UIBridgeRPCError: Decodable {
    let code: Int
    let message: String
}
