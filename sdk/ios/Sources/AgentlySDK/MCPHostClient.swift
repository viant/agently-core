import Foundation

public struct MCPClientInfo: Codable, Sendable {
    public let name: String
    public let version: String

    public init(name: String, version: String) {
        self.name = name
        self.version = version
    }
}

public struct MCPInitializationCapabilities: Codable, Sendable {
    public let roots: [String: JSONValue]?
    public let sampling: [String: JSONValue]?
    public let elicitation: [String: JSONValue]?

    public init(
        roots: [String: JSONValue]? = nil,
        sampling: [String: JSONValue]? = nil,
        elicitation: [String: JSONValue]? = nil
    ) {
        self.roots = roots
        self.sampling = sampling
        self.elicitation = elicitation
    }
}

public struct MCPServerCapabilities: Codable, Sendable {
    public let prompts: [String: JSONValue]?
    public let resources: [String: JSONValue]?
    public let tools: [String: JSONValue]?
    public let logging: [String: JSONValue]?

    public init(
        prompts: [String: JSONValue]? = nil,
        resources: [String: JSONValue]? = nil,
        tools: [String: JSONValue]? = nil,
        logging: [String: JSONValue]? = nil
    ) {
        self.prompts = prompts
        self.resources = resources
        self.tools = tools
        self.logging = logging
    }
}

public struct MCPInitializeResult: Codable, Sendable {
    public let protocolVersion: String?
    public let capabilities: MCPServerCapabilities?
    public let serverInfo: MCPClientInfo?
    public let instructions: String?

    public init(
        protocolVersion: String? = nil,
        capabilities: MCPServerCapabilities? = nil,
        serverInfo: MCPClientInfo? = nil,
        instructions: String? = nil
    ) {
        self.protocolVersion = protocolVersion
        self.capabilities = capabilities
        self.serverInfo = serverInfo
        self.instructions = instructions
    }
}

public struct MCPTool: Codable, Sendable, Identifiable {
    public var id: String { name }
    public let name: String
    public let title: String?
    public let description: String?
    public let inputSchema: JSONValue?

    public init(name: String, title: String? = nil, description: String? = nil, inputSchema: JSONValue? = nil) {
        self.name = name
        self.title = title
        self.description = description
        self.inputSchema = inputSchema
    }
}

public struct MCPPrompt: Codable, Sendable, Identifiable {
    public var id: String { name }
    public let name: String
    public let title: String?
    public let description: String?
    public let arguments: [MCPPromptArgument]

    public init(name: String, title: String? = nil, description: String? = nil, arguments: [MCPPromptArgument] = []) {
        self.name = name
        self.title = title
        self.description = description
        self.arguments = arguments
    }
}

public struct MCPPromptArgument: Codable, Sendable, Identifiable {
    public var id: String { name }
    public let name: String
    public let description: String?
    public let required: Bool?

    public init(name: String, description: String? = nil, required: Bool? = nil) {
        self.name = name
        self.description = description
        self.required = required
    }
}

public struct MCPResource: Codable, Sendable, Identifiable {
    public var id: String { uri }
    public let uri: String
    public let name: String?
    public let title: String?
    public let description: String?
    public let mimeType: String?

    public init(
        uri: String,
        name: String? = nil,
        title: String? = nil,
        description: String? = nil,
        mimeType: String? = nil
    ) {
        self.uri = uri
        self.name = name
        self.title = title
        self.description = description
        self.mimeType = mimeType
    }
}

public struct MCPEmbeddedResource: Codable, Sendable {
    public let uri: String?
    public let mimeType: String?
    public let text: String?
    public let blob: String?

    public init(uri: String? = nil, mimeType: String? = nil, text: String? = nil, blob: String? = nil) {
        self.uri = uri
        self.mimeType = mimeType
        self.text = text
        self.blob = blob
    }
}

public struct MCPContentBlock: Codable, Sendable {
    public let type: String
    public let text: String?
    public let mimeType: String?
    public let data: String?
    public let resource: MCPEmbeddedResource?
    public let annotations: [String: JSONValue]?

    public init(
        type: String,
        text: String? = nil,
        mimeType: String? = nil,
        data: String? = nil,
        resource: MCPEmbeddedResource? = nil,
        annotations: [String: JSONValue]? = nil
    ) {
        self.type = type
        self.text = text
        self.mimeType = mimeType
        self.data = data
        self.resource = resource
        self.annotations = annotations
    }
}

public struct MCPPromptMessage: Codable, Sendable {
    public let role: String
    public let content: MCPContentBlock

    public init(role: String, content: MCPContentBlock) {
        self.role = role
        self.content = content
    }
}

public struct MCPToolCallResult: Codable, Sendable {
    public let content: [MCPContentBlock]
    public let isError: Bool?
    public let structuredContent: JSONValue?

    public init(content: [MCPContentBlock] = [], isError: Bool? = nil, structuredContent: JSONValue? = nil) {
        self.content = content
        self.isError = isError
        self.structuredContent = structuredContent
    }
}

public struct MCPReadResourceResult: Codable, Sendable {
    public let contents: [MCPContentBlock]

    public init(contents: [MCPContentBlock] = []) {
        self.contents = contents
    }
}

public struct MCPGetPromptResult: Codable, Sendable {
    public let description: String?
    public let messages: [MCPPromptMessage]

    public init(description: String? = nil, messages: [MCPPromptMessage] = []) {
        self.description = description
        self.messages = messages
    }
}

public struct MCPCursorPage<T: Codable & Sendable>: Codable, Sendable {
    public let nextCursor: String?
    public let items: [T]

    public init(nextCursor: String? = nil, items: [T] = []) {
        self.nextCursor = nextCursor
        self.items = items
    }
}

public enum MCPHostClientError: Error, LocalizedError, Sendable {
    case invalidResponse
    case rpcError(code: Int, message: String, data: JSONValue?)
    case unsupportedResultShape(String)

    public var errorDescription: String? {
        switch self {
        case .invalidResponse:
            return "The MCP host returned an invalid response."
        case .rpcError(let code, let message, _):
            return "MCP request failed with code \(code): \(message)"
        case .unsupportedResultShape(let method):
            return "The MCP host returned an unsupported result shape for \(method)."
        }
    }
}

public final class MCPHostClient: Sendable {
    private let endpoint: EndpointConfig
    private let session: URLSession
    private let decoder: JSONDecoder
    private let encoder: JSONEncoder
    private let path: String

    public init(
        endpoint: EndpointConfig,
        path: String = "/mcp",
        session: URLSession = .shared,
        decoder: JSONDecoder = .agently(),
        encoder: JSONEncoder = .agently()
    ) {
        self.endpoint = endpoint
        self.path = path
        self.session = session
        self.decoder = decoder
        self.encoder = encoder
    }

    public func initialize(
        clientInfo: MCPClientInfo,
        protocolVersion: String = "2024-11-05",
        capabilities: MCPInitializationCapabilities = MCPInitializationCapabilities()
    ) async throws -> MCPInitializeResult {
        try await call(
            method: "initialize",
            params: MCPInitializeParams(
                protocolVersion: protocolVersion,
                capabilities: capabilities,
                clientInfo: clientInfo
            ),
            as: MCPInitializeResult.self
        )
    }

    public func listTools(cursor: String? = nil) async throws -> MCPCursorPage<MCPTool> {
        let result: MCPListToolsResult = try await call(
            method: "tools/list",
            params: MCPListCursorParams(cursor: cursor),
            as: MCPListToolsResult.self
        )
        return MCPCursorPage(nextCursor: result.nextCursor, items: result.tools)
    }

    public func callTool(name: String, arguments: [String: JSONValue] = [:]) async throws -> MCPToolCallResult {
        try await call(
            method: "tools/call",
            params: MCPCallToolParams(name: name, arguments: arguments.isEmpty ? nil : arguments),
            as: MCPToolCallResult.self
        )
    }

    public func listPrompts(cursor: String? = nil) async throws -> MCPCursorPage<MCPPrompt> {
        let result: MCPListPromptsResult = try await call(
            method: "prompts/list",
            params: MCPListCursorParams(cursor: cursor),
            as: MCPListPromptsResult.self
        )
        return MCPCursorPage(nextCursor: result.nextCursor, items: result.prompts)
    }

    public func getPrompt(name: String, arguments: [String: String] = [:]) async throws -> MCPGetPromptResult {
        try await call(
            method: "prompts/get",
            params: MCPGetPromptParams(name: name, arguments: arguments.isEmpty ? nil : arguments),
            as: MCPGetPromptResult.self
        )
    }

    public func listResources(cursor: String? = nil) async throws -> MCPCursorPage<MCPResource> {
        let result: MCPListResourcesResult = try await call(
            method: "resources/list",
            params: MCPListCursorParams(cursor: cursor),
            as: MCPListResourcesResult.self
        )
        return MCPCursorPage(nextCursor: result.nextCursor, items: result.resources)
    }

    public func readResource(uri: String) async throws -> MCPReadResourceResult {
        try await call(
            method: "resources/read",
            params: MCPReadResourceParams(uri: uri),
            as: MCPReadResourceResult.self
        )
    }

    private func call<Params: Encodable, Result: Decodable>(
        method: String,
        params: Params,
        as type: Result.Type
    ) async throws -> Result {
        let requestBody = try encoder.encode(
            MCPJSONRPCRequest(
                id: UUID().uuidString,
                method: method,
                params: AnyEncodable(params)
            )
        )

        let builder = RequestBuilder(endpoint: endpoint, encoder: encoder)
        let request = try builder.makeRequest(
            path: path,
            method: "POST",
            body: requestBody,
            contentType: "application/json"
        )

        let (data, response) = try await session.data(for: request)
        guard let http = response as? HTTPURLResponse else {
            throw MCPHostClientError.invalidResponse
        }
        guard (200..<300).contains(http.statusCode) else {
            let message = String(data: data, encoding: .utf8)?.trimmingCharacters(in: .whitespacesAndNewlines)
            throw AgentlySDKError.httpStatus(http.statusCode, message)
        }

        let envelope = try decoder.decode(MCPJSONRPCResponse.self, from: data)
        if let error = envelope.error {
            throw MCPHostClientError.rpcError(code: error.code, message: error.message, data: error.data)
        }
        guard let resultValue = envelope.result else {
            throw MCPHostClientError.unsupportedResultShape(method)
        }

        let resultData = try encoder.encode(resultValue)
        return try decoder.decode(Result.self, from: resultData)
    }
}

public extension AgentlyClient {
    func makeMCPHostClient(endpointName: String = "appAPI", path: String = "/mcp") throws -> MCPHostClient {
        guard let endpoint = endpoints[endpointName] else {
            throw AgentlySDKError.missingEndpoint(endpointName)
        }
        return MCPHostClient(
            endpoint: endpoint,
            path: path,
            session: session,
            decoder: decoder,
            encoder: encoder
        )
    }
}

private struct MCPInitializeParams: Codable {
    let protocolVersion: String
    let capabilities: MCPInitializationCapabilities
    let clientInfo: MCPClientInfo
}

private struct MCPListCursorParams: Codable {
    let cursor: String?
}

private struct MCPCallToolParams: Codable {
    let name: String
    let arguments: [String: JSONValue]?
}

private struct MCPGetPromptParams: Codable {
    let name: String
    let arguments: [String: String]?
}

private struct MCPReadResourceParams: Codable {
    let uri: String
}

private struct MCPListToolsResult: Codable {
    let tools: [MCPTool]
    let nextCursor: String?
}

private struct MCPListPromptsResult: Codable {
    let prompts: [MCPPrompt]
    let nextCursor: String?
}

private struct MCPListResourcesResult: Codable {
    let resources: [MCPResource]
    let nextCursor: String?
}

private struct MCPJSONRPCRequest: Encodable {
    let jsonrpc = "2.0"
    let id: String
    let method: String
    let params: AnyEncodable?
}

private struct MCPJSONRPCResponse: Decodable {
    let jsonrpc: String?
    let id: JSONValue?
    let result: JSONValue?
    let error: MCPJSONRPCError?
}

private struct MCPJSONRPCError: Decodable {
    let code: Int
    let message: String
    let data: JSONValue?
}

private struct AnyEncodable: Encodable {
    private let encodeImpl: (Encoder) throws -> Void

    init<T: Encodable>(_ value: T) {
        self.encodeImpl = value.encode(to:)
    }

    func encode(to encoder: Encoder) throws {
        try encodeImpl(encoder)
    }
}
