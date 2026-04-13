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

    public func oauthInitiate() async throws -> OAuthInitiateOutput {
        try await post("/v1/api/auth/oauth/initiate", body: EmptyResponse(), as: OAuthInitiateOutput.self)
    }

    public func oauthCallback(_ input: OAuthCallbackInput) async throws -> OAuthCallbackOutput {
        try await post("/v1/api/auth/oauth/callback", body: input, as: OAuthCallbackOutput.self)
    }

    public func getOAuthConfig() async throws -> OAuthConfigOutput {
        try await get("/v1/api/auth/oauth/config", as: OAuthConfigOutput.self)
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
        try await get("/v1/conversations", query: queryItems(from: input), as: ConversationPage.self)
    }

    public func getLiveState(conversationID: String) async throws -> ConversationStateResponse {
        try await get("/v1/conversations/\(conversationID)/live-state", as: ConversationStateResponse.self)
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
        let data = try await rawDataRequest(path: "/v1/tool-approvals/pending", method: "GET", query: queryItems(from: input))
        if let rows = try? decoder.decode([PendingToolApproval].self, from: data) {
            return rows
        }
        return try decoder.decode(PendingToolApprovalRows.self, from: data).rows
    }

    public func decideToolApproval(_ input: DecideToolApprovalInput) async throws {
        let _: EmptyResponse = try await post("/v1/tool-approvals/\(input.id)/decision", body: input, as: EmptyResponse.self)
    }

    public func cancelTurn(id: String) async throws {
        let _: EmptyResponse = try await post("/v1/turns/\(id)/cancel", body: EmptyResponse(), as: EmptyResponse.self)
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

    public func listGeneratedFiles(conversationID: String) async throws -> [GeneratedFileEntry] {
        try await get("/v1/api/conversations/\(conversationID)/generated-files", as: [GeneratedFileEntry].self)
    }

    public func downloadGeneratedFile(id: String) async throws -> DownloadFileOutput {
        try await downloadBinary(path: "/v1/api/generated-files/\(id)/download")
    }

    public func downloadFile(conversationID: String, fileID: String) async throws -> DownloadFileOutput {
        try await downloadBinary(
            path: "/v1/files/\(fileID)",
            query: [
                URLQueryItem(name: "conversationId", value: conversationID),
                URLQueryItem(name: "raw", value: "1")
            ]
        )
    }

    public func streamEvents(conversationID: String) -> AsyncThrowingStream<SSEEvent, Error> {
        guard let endpoint = endpoints[endpointName] else {
            return AsyncThrowingStream { continuation in
                continuation.finish(throwing: AgentlySDKError.missingEndpoint(endpointName))
            }
        }
        return openEventStream(
            endpoint: endpoint,
            path: "/v1/stream?conversationId=\(conversationID)",
            conversationID: conversationID,
            session: session
        )
    }

    private func endpoint() throws -> EndpointConfig {
        guard let endpoint = endpoints[endpointName] else {
            throw AgentlySDKError.missingEndpoint(endpointName)
        }
        return endpoint
    }

    private func get<T: Decodable>(_ path: String, query: [URLQueryItem] = [], as type: T.Type) async throws -> T {
        try await rawRequest(path: path, method: "GET", query: query, as: type)
    }

    private func post<Body: Encodable, T: Decodable>(_ path: String, body: Body, as type: T.Type) async throws -> T {
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

    private func rawRequest<T: Decodable>(
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

    private func rawDataRequest(
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
