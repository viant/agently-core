import Foundation

public struct EndpointConfig: Sendable {
    public var baseURL: URL
    public var headers: [String: String]

    public init(baseURL: URL, headers: [String: String] = [:]) {
        self.baseURL = baseURL
        self.headers = headers
    }
}

public struct EmptyResponse: Codable, Sendable {
    public init() {}
}

public struct AuthProvider: Codable, Sendable, Identifiable {
    public var id: String { name ?? type }
    public let type: String
    public let name: String?

    public init(type: String, name: String? = nil) {
        self.type = type
        self.name = name
    }
}

public struct AuthUser: Codable, Sendable {
    public let id: String?
    public let email: String?
    public let displayName: String?

    public init(id: String? = nil, email: String? = nil, displayName: String? = nil) {
        self.id = id
        self.email = email
        self.displayName = displayName
    }
}

public struct OAuthInitiateOutput: Codable, Sendable {
    public let authURL: String?
    public let authUrl: String?
}

public struct OAuthCallbackInput: Codable, Sendable {
    public let code: String
    public let state: String

    public init(code: String, state: String) {
        self.code = code
        self.state = state
    }
}

public struct OAuthCallbackOutput: Codable, Sendable {
    public let success: Bool?
}

public struct OAuthConfigOutput: Codable, Sendable {
    public let scopes: [String]

    public init(scopes: [String] = []) {
        self.scopes = scopes
    }
}

public struct WorkspaceDefaults: Codable, Sendable {
    public let agent: String?
    public let model: String?
    public let embedder: String?
    public let autoSelectTools: Bool?

    public init(agent: String? = nil, model: String? = nil, embedder: String? = nil, autoSelectTools: Bool? = nil) {
        self.agent = agent
        self.model = model
        self.embedder = embedder
        self.autoSelectTools = autoSelectTools
    }
}

public struct WorkspaceCapabilities: Codable, Sendable {
    public let agentAutoSelection: Bool?
    public let modelAutoSelection: Bool?
    public let toolAutoSelection: Bool?
    public let compactConversation: Bool?
    public let pruneConversation: Bool?
    public let anonymousSession: Bool?
    public let messageCursor: Bool?
    public let structuredElicitation: Bool?
    public let turnStartedEvent: Bool?

    public init(
        agentAutoSelection: Bool? = nil,
        modelAutoSelection: Bool? = nil,
        toolAutoSelection: Bool? = nil,
        compactConversation: Bool? = nil,
        pruneConversation: Bool? = nil,
        anonymousSession: Bool? = nil,
        messageCursor: Bool? = nil,
        structuredElicitation: Bool? = nil,
        turnStartedEvent: Bool? = nil
    ) {
        self.agentAutoSelection = agentAutoSelection
        self.modelAutoSelection = modelAutoSelection
        self.toolAutoSelection = toolAutoSelection
        self.compactConversation = compactConversation
        self.pruneConversation = pruneConversation
        self.anonymousSession = anonymousSession
        self.messageCursor = messageCursor
        self.structuredElicitation = structuredElicitation
        self.turnStartedEvent = turnStartedEvent
    }
}

public struct StarterTask: Codable, Sendable, Identifiable {
    public var id: String { rawID ?? UUID().uuidString }
    public let rawID: String?
    public let title: String?
    public let prompt: String?
    public let description: String?
    public let icon: String?

    enum CodingKeys: String, CodingKey {
        case rawID = "id"
        case title
        case prompt
        case description
        case icon
    }

    public init(
        id: String? = nil,
        title: String? = nil,
        prompt: String? = nil,
        description: String? = nil,
        icon: String? = nil
    ) {
        self.rawID = id
        self.title = title
        self.prompt = prompt
        self.description = description
        self.icon = icon
    }
}

public struct WorkspaceAgentInfo: Codable, Sendable, Identifiable {
    public var id: String { agentID ?? UUID().uuidString }
    public let agentID: String?
    public let name: String?
    public let modelRef: String?
    public let internalAgent: Bool?
    public let starterTasks: [StarterTask]

    enum CodingKeys: String, CodingKey {
        case agentID = "id"
        case name
        case modelRef
        case internalAgent = "internal"
        case starterTasks
    }

    public init(
        agentID: String? = nil,
        name: String? = nil,
        modelRef: String? = nil,
        internalAgent: Bool? = nil,
        starterTasks: [StarterTask] = []
    ) {
        self.agentID = agentID
        self.name = name
        self.modelRef = modelRef
        self.internalAgent = internalAgent
        self.starterTasks = starterTasks
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        self.agentID = try container.decodeIfPresent(String.self, forKey: .agentID)
        self.name = try container.decodeIfPresent(String.self, forKey: .name)
        self.modelRef = try container.decodeIfPresent(String.self, forKey: .modelRef)
        self.internalAgent = try container.decodeIfPresent(Bool.self, forKey: .internalAgent)
        self.starterTasks = try container.decodeIfPresent([StarterTask].self, forKey: .starterTasks) ?? []
    }
}

public struct WorkspaceModelInfo: Codable, Sendable, Identifiable {
    public var id: String { modelID ?? UUID().uuidString }
    public let modelID: String?
    public let name: String?

    enum CodingKeys: String, CodingKey {
        case modelID = "id"
        case name
    }

    public init(modelID: String? = nil, name: String? = nil) {
        self.modelID = modelID
        self.name = name
    }
}

public struct WorkspaceMetadata: Codable, Sendable {
    public let workspaceRoot: String?
    public let defaultAgent: String?
    public let defaultModel: String?
    public let defaultEmbedder: String?
    public let agents: [String]
    public let models: [String]
    public let agentInfos: [WorkspaceAgentInfo]
    public let modelInfos: [WorkspaceModelInfo]
    public let defaults: WorkspaceDefaults?
    public let capabilities: WorkspaceCapabilities?
    public let version: String?

    public init(
        workspaceRoot: String? = nil,
        defaultAgent: String? = nil,
        defaultModel: String? = nil,
        defaultEmbedder: String? = nil,
        agents: [String] = [],
        models: [String] = [],
        agentInfos: [WorkspaceAgentInfo] = [],
        modelInfos: [WorkspaceModelInfo] = [],
        defaults: WorkspaceDefaults? = nil,
        capabilities: WorkspaceCapabilities? = nil,
        version: String? = nil
    ) {
        self.workspaceRoot = workspaceRoot
        self.defaultAgent = defaultAgent
        self.defaultModel = defaultModel
        self.defaultEmbedder = defaultEmbedder
        self.agents = agents
        self.models = models
        self.agentInfos = agentInfos
        self.modelInfos = modelInfos
        self.defaults = defaults
        self.capabilities = capabilities
        self.version = version
    }
}

public struct PageInput: Codable, Sendable {
    public let limit: Int?
    public let cursor: String?
    public let direction: String?

    public init(limit: Int? = nil, cursor: String? = nil, direction: String? = nil) {
        self.limit = limit
        self.cursor = cursor
        self.direction = direction
    }
}

public struct Conversation: Codable, Sendable, Identifiable {
    public let id: String
    public let agentID: String?
    public let title: String?
    public let summary: String?
    public let stage: String?
    public let lastActivity: String?

    enum CodingKeys: String, CodingKey {
        case id = "Id"
        case agentID = "AgentId"
        case title = "Title"
        case summary = "Summary"
        case stage = "Stage"
        case lastActivity = "LastActivity"
    }
}

public struct ConversationPage: Codable, Sendable {
    public let rows: [Conversation]
    public let nextCursor: String?
    public let prevCursor: String?
    public let hasMore: Bool

    enum CodingKeys: String, CodingKey {
        case rows = "Rows"
        case nextCursor = "NextCursor"
        case prevCursor = "PrevCursor"
        case hasMore = "HasMore"
    }
}

public struct ListConversationsInput: Codable, Sendable {
    public let agentID: String?
    public let query: String?
    public let status: String?
    public let page: PageInput?

    enum CodingKeys: String, CodingKey {
        case agentID = "agentId"
        case query = "q"
        case status
        case page
    }

    public init(agentID: String? = nil, query: String? = nil, status: String? = nil, page: PageInput? = nil) {
        self.agentID = agentID
        self.query = query
        self.status = status
        self.page = page
    }
}

public struct CreateConversationInput: Codable, Sendable {
    public let agentID: String?
    public let title: String?
    public let metadata: [String: JSONValue]

    enum CodingKeys: String, CodingKey {
        case agentID = "agentId"
        case title
        case metadata
    }

    public init(agentID: String? = nil, title: String? = nil, metadata: [String: JSONValue] = [:]) {
        self.agentID = agentID
        self.title = title
        self.metadata = metadata
    }
}

public struct QueryAttachment: Codable, Sendable, Identifiable {
    public var id: String { uri }
    public let name: String
    public let uri: String
    public let size: Int64?
    public let mime: String?

    public init(name: String, uri: String, size: Int64? = nil, mime: String? = nil) {
        self.name = name
        self.uri = uri
        self.size = size
        self.mime = mime
    }
}

public struct QueryInput: Codable, Sendable {
    public let conversationID: String?
    public let agentID: String?
    public let query: String
    public let attachments: [QueryAttachment]
    public let model: String?
    public let context: [String: JSONValue]

    enum CodingKeys: String, CodingKey {
        case conversationID = "conversationId"
        case agentID = "agentId"
        case query
        case attachments
        case model
        case context
    }

    public init(
        conversationID: String? = nil,
        agentID: String? = nil,
        query: String,
        attachments: [QueryAttachment] = [],
        model: String? = nil,
        context: [String: JSONValue] = [:]
    ) {
        self.conversationID = conversationID
        self.agentID = agentID
        self.query = query
        self.attachments = attachments
        self.model = model
        self.context = context
    }
}

public struct QueryOutput: Codable, Sendable {
    public let conversationID: String?
    public let content: String
    public let model: String?
    public let messageID: String?
    public let elicitation: JSONValue?

    enum CodingKeys: String, CodingKey {
        case conversationID = "conversationId"
        case content
        case model
        case messageID = "messageId"
        case elicitation
    }
}

public struct ConversationStateResponse: Decodable, Sendable {
    public let conversation: ConversationState?
    public let feeds: [ActiveFeedState]

    enum CodingKeys: String, CodingKey {
        case conversation
        case feeds
    }

    public init(conversation: ConversationState? = nil, feeds: [ActiveFeedState] = []) {
        self.conversation = conversation
        self.feeds = feeds
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        self.conversation = try container.decodeIfPresent(ConversationState.self, forKey: .conversation)
        self.feeds = try container.decodeIfPresent([ActiveFeedState].self, forKey: .feeds) ?? []
    }
}

public struct ConversationState: Decodable, Sendable {
    public let conversationID: String
    public let turns: [ConversationTurn]
    public let feeds: [ActiveFeedState]

    enum CodingKeys: String, CodingKey {
        case conversationID = "conversationId"
        case turns
        case feeds
    }

    public init(
        conversationID: String,
        turns: [ConversationTurn] = [],
        feeds: [ActiveFeedState] = []
    ) {
        self.conversationID = conversationID
        self.turns = turns
        self.feeds = feeds
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        self.conversationID = try container.decode(String.self, forKey: .conversationID)
        self.turns = try container.decodeIfPresent([ConversationTurn].self, forKey: .turns) ?? []
        self.feeds = try container.decodeIfPresent([ActiveFeedState].self, forKey: .feeds) ?? []
    }
}

public struct ConversationTurn: Decodable, Sendable, Identifiable {
    public let id: String
    public let createdAt: String?
    public let user: ConversationMessagePart?
    public let assistant: AssistantTurnPart?

    enum CodingKeys: String, CodingKey {
        case id
        case turnID = "turnId"
        case createdAt
        case user
        case assistant
    }

    public init(
        id: String,
        createdAt: String? = nil,
        user: ConversationMessagePart? = nil,
        assistant: AssistantTurnPart? = nil
    ) {
        self.id = id
        self.createdAt = createdAt
        self.user = user
        self.assistant = assistant
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        self.id =
            try container.decodeIfPresent(String.self, forKey: .id) ??
            container.decodeIfPresent(String.self, forKey: .turnID) ??
            container.decodeIfPresent(ConversationMessagePart.self, forKey: .user)?.messageID ??
            UUID().uuidString
        self.createdAt = try container.decodeIfPresent(String.self, forKey: .createdAt)
        self.user = try container.decodeIfPresent(ConversationMessagePart.self, forKey: .user)
        self.assistant = try container.decodeIfPresent(AssistantTurnPart.self, forKey: .assistant)
    }
}

public struct ConversationMessagePart: Codable, Sendable {
    public let messageID: String
    public let content: String?

    enum CodingKeys: String, CodingKey {
        case messageID = "messageId"
        case content
    }
}

public struct AssistantTurnPart: Codable, Sendable {
    public let preamble: ConversationMessagePart?
    public let final: ConversationMessagePart?
}

public struct ActiveFeedState: Codable, Sendable, Identifiable {
    public var id: String { feedID ?? UUID().uuidString }
    public let feedID: String?
    public let name: String?

    enum CodingKeys: String, CodingKey {
        case feedID = "feedId"
        case name
    }
}

public struct PendingElicitation: Codable, Sendable, Identifiable {
    public let elicitationID: String
    public let conversationID: String?
    public let message: String?
    public let mode: String?
    public let url: String?
    public let requestedSchema: JSONValue?

    enum CodingKeys: String, CodingKey {
        case elicitationID = "elicitationId"
        case conversationID = "conversationId"
        case message
        case mode
        case url
        case requestedSchema
    }

    public var id: String { elicitationID }

    public init(
        elicitationID: String,
        conversationID: String? = nil,
        message: String? = nil,
        mode: String? = nil,
        url: String? = nil,
        requestedSchema: JSONValue? = nil
    ) {
        self.elicitationID = elicitationID
        self.conversationID = conversationID
        self.message = message
        self.mode = mode
        self.url = url
        self.requestedSchema = requestedSchema
    }
}

public struct PendingElicitationRecord: Codable, Sendable, Identifiable {
    public let conversationID: String
    public let elicitationID: String
    public let messageID: String
    public let status: String
    public let role: String
    public let type: String
    public let content: String?
    public let elicitation: JSONValue?

    enum CodingKeys: String, CodingKey {
        case conversationID = "conversationId"
        case elicitationID = "elicitationId"
        case messageID = "messageId"
        case status
        case role
        case type
        case content
        case elicitation
    }

    public var id: String { elicitationID }
}

public struct ListPendingElicitationsInput: Codable, Sendable {
    public let conversationID: String

    enum CodingKeys: String, CodingKey {
        case conversationID = "conversationId"
    }

    public init(conversationID: String) {
        self.conversationID = conversationID
    }
}

public struct PendingElicitationRows: Codable, Sendable {
    public let rows: [PendingElicitationRecord]

    public init(rows: [PendingElicitationRecord] = []) {
        self.rows = rows
    }
}

public struct ResolveElicitationInput: Codable, Sendable {
    public let conversationID: String
    public let elicitationID: String
    public let action: String
    public let payload: [String: JSONValue]

    enum CodingKeys: String, CodingKey {
        case conversationID = "conversationId"
        case elicitationID = "elicitationId"
        case action
        case payload
    }

    public init(
        conversationID: String,
        elicitationID: String,
        action: String,
        payload: [String: JSONValue] = [:]
    ) {
        self.conversationID = conversationID
        self.elicitationID = elicitationID
        self.action = action
        self.payload = payload
    }
}

public struct PendingToolApproval: Codable, Sendable, Identifiable {
    public let id: String
    public let conversationID: String?
    public let messageID: String?
    public let toolName: String
    public let title: String?
    public let arguments: JSONValue?
    public let metadata: JSONValue?
    public let status: String

    enum CodingKeys: String, CodingKey {
        case id
        case conversationID = "conversationId"
        case messageID = "messageId"
        case toolName
        case title
        case arguments
        case metadata
        case status
    }
}

public struct ListPendingToolApprovalsInput: Codable, Sendable {
    public let conversationID: String?
    public let status: String?
    public let limit: Int?

    enum CodingKeys: String, CodingKey {
        case conversationID = "conversationId"
        case status
        case limit
    }

    public init(conversationID: String? = nil, status: String? = nil, limit: Int? = nil) {
        self.conversationID = conversationID
        self.status = status
        self.limit = limit
    }
}

public struct PendingToolApprovalRows: Codable, Sendable {
    public let rows: [PendingToolApproval]

    public init(rows: [PendingToolApproval] = []) {
        self.rows = rows
    }
}

public struct DecideToolApprovalInput: Codable, Sendable {
    public let id: String
    public let action: String
    public let editedFields: [String: JSONValue]

    public init(id: String, action: String, editedFields: [String: JSONValue] = [:]) {
        self.id = id
        self.action = action
        self.editedFields = editedFields
    }
}

public struct ApprovalMeta: Codable, Sendable {
    public let type: String?
    public let toolName: String?
    public let title: String?
    public let message: String?
    public let acceptLabel: String?
    public let rejectLabel: String?
    public let cancelLabel: String?
    public let forge: ApprovalForgeView?
    public let editors: [ApprovalEditor]?

    public init(type: String? = nil, toolName: String? = nil, title: String? = nil,
                message: String? = nil, acceptLabel: String? = nil,
                rejectLabel: String? = nil, cancelLabel: String? = nil,
                forge: ApprovalForgeView? = nil,
                editors: [ApprovalEditor]? = nil) {
        self.type = type
        self.toolName = toolName
        self.title = title
        self.message = message
        self.acceptLabel = acceptLabel
        self.rejectLabel = rejectLabel
        self.cancelLabel = cancelLabel
        self.forge = forge
        self.editors = editors
    }
}

public struct ApprovalForgeView: Codable, Sendable {
    public let windowRef: String?
    public let containerRef: String?
    public let dataSource: String?
    public let callbacks: [ApprovalCallback]?

    public init(
        windowRef: String? = nil,
        containerRef: String? = nil,
        dataSource: String? = nil,
        callbacks: [ApprovalCallback]? = nil
    ) {
        self.windowRef = windowRef
        self.containerRef = containerRef
        self.dataSource = dataSource
        self.callbacks = callbacks
    }
}

public struct ApprovalCallback: Codable, Sendable {
    public let event: String?
    public let handler: String?
    public let args: [String: JSONValue]?

    public init(event: String? = nil, handler: String? = nil, args: [String: JSONValue]? = nil) {
        self.event = event
        self.handler = handler
        self.args = args
    }
}

public struct ApprovalEditor: Codable, Sendable {
    public let name: String
    public let kind: String?       // "radio", "multiSelect", "text"
    public let path: String?
    public let label: String?
    public let description: String?
    public let options: [ApprovalOption]?

    public init(name: String, kind: String? = nil, path: String? = nil, label: String? = nil,
                description: String? = nil, options: [ApprovalOption]? = nil) {
        self.name = name
        self.kind = kind
        self.path = path
        self.label = label
        self.description = description
        self.options = options
    }
}

public struct ApprovalOption: Codable, Sendable {
    public let id: String
    public let label: String?
    public let description: String?
    public let item: JSONValue?
    public let selected: Bool?

    public init(id: String, label: String? = nil, description: String? = nil,
                item: JSONValue? = nil, selected: Bool? = nil) {
        self.id = id
        self.label = label
        self.description = description
        self.item = item
        self.selected = selected
    }
}

public struct GeneratedFileEntry: Codable, Sendable, Identifiable {
    public let id: String
    public let filename: String?
    public let mimeType: String?
    public let messageID: String?

    enum CodingKeys: String, CodingKey {
        case id
        case filename
        case mimeType
        case messageID = "messageId"
    }
}

public struct FileEntry: Codable, Sendable, Identifiable {
    public let id: String
    public let name: String?
    public let uri: String?
    public let contentType: String?
}

public struct ListFilesInput: Codable, Sendable {
    public let conversationID: String

    enum CodingKeys: String, CodingKey {
        case conversationID = "conversationId"
    }

    public init(conversationID: String) {
        self.conversationID = conversationID
    }
}

public struct ListFilesOutput: Decodable, Sendable {
    public let files: [FileEntry]

    enum CodingKeys: String, CodingKey {
        case files
        case capitalizedFiles = "Files"
    }

    public init(files: [FileEntry] = []) {
        self.files = files
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        self.files =
            try container.decodeIfPresent([FileEntry].self, forKey: .files) ??
            container.decodeIfPresent([FileEntry].self, forKey: .capitalizedFiles) ??
            []
    }
}

public struct UploadFileInput: Sendable {
    public let conversationID: String
    public let name: String
    public let contentType: String?
    public let data: Data

    public init(conversationID: String, name: String, contentType: String? = nil, data: Data) {
        self.conversationID = conversationID
        self.name = name
        self.contentType = contentType
        self.data = data
    }
}

public struct UploadFileOutput: Codable, Sendable {
    public let uri: String
}

public struct DownloadFileOutput: Sendable {
    public let name: String?
    public let contentType: String?
    public let data: Data

    public init(name: String? = nil, contentType: String? = nil, data: Data) {
        self.name = name
        self.contentType = contentType
        self.data = data
    }
}

public enum JSONValue: Codable, Sendable, Equatable {
    case string(String)
    case number(Double)
    case bool(Bool)
    case object([String: JSONValue])
    case array([JSONValue])
    case null

    public init(from decoder: Decoder) throws {
        let container = try decoder.singleValueContainer()
        if container.decodeNil() {
            self = .null
        } else if let value = try? container.decode(Bool.self) {
            self = .bool(value)
        } else if let value = try? container.decode(Double.self) {
            self = .number(value)
        } else if let value = try? container.decode(String.self) {
            self = .string(value)
        } else if let value = try? container.decode([String: JSONValue].self) {
            self = .object(value)
        } else if let value = try? container.decode([JSONValue].self) {
            self = .array(value)
        } else {
            throw DecodingError.dataCorruptedError(in: container, debugDescription: "Unsupported JSON value")
        }
    }

    public func encode(to encoder: Encoder) throws {
        var container = encoder.singleValueContainer()
        switch self {
        case .string(let value):
            try container.encode(value)
        case .number(let value):
            try container.encode(value)
        case .bool(let value):
            try container.encode(value)
        case .object(let value):
            try container.encode(value)
        case .array(let value):
            try container.encode(value)
        case .null:
            try container.encodeNil()
        }
    }
}
