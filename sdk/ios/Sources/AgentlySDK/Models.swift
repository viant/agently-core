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
    public let redirectURI: String?
}

public struct OAuthInitiateInput: Codable, Sendable {
    public let redirectURI: String?
    public let returnURL: String?
    public let scopes: [String]

    public init(redirectURI: String? = nil, returnURL: String? = nil, scopes: [String] = []) {
        self.redirectURI = redirectURI
        self.returnURL = returnURL
        self.scopes = scopes
    }
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
    public let status: String?
    public let sessionID: String?
    public let username: String?
    public let provider: String?

    enum CodingKeys: String, CodingKey {
        case success
        case status
        case sessionID = "sessionId"
        case username
        case provider
    }
}

public struct OAuthConfigOutput: Codable, Sendable {
    public let scopes: [String]
    public let webUIScopes: [String]
    public let mobileUIScopes: [String]
    public let cliScopes: [String]
    public let redirectURIs: [String]

    public init(scopes: [String] = [], webUIScopes: [String] = [], mobileUIScopes: [String] = [], cliScopes: [String] = [], redirectURIs: [String] = []) {
        self.scopes = scopes
        self.webUIScopes = webUIScopes
        self.mobileUIScopes = mobileUIScopes
        self.cliScopes = cliScopes
        self.redirectURIs = redirectURIs
    }

    enum CodingKeys: String, CodingKey {
        case scopes
        case webUIScopes
        case mobileUIScopes
        case cliScopes
        case redirectURIs
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        scopes = try container.decodeIfPresent([String].self, forKey: .scopes) ?? []
        webUIScopes = try container.decodeIfPresent([String].self, forKey: .webUIScopes) ?? []
        mobileUIScopes = try container.decodeIfPresent([String].self, forKey: .mobileUIScopes) ?? []
        cliScopes = try container.decodeIfPresent([String].self, forKey: .cliScopes) ?? []
        redirectURIs = try container.decodeIfPresent([String].self, forKey: .redirectURIs) ?? []
    }
}

public struct WorkspaceDefaults: Codable, Sendable {
    public let appName: String?
    public let appIconRef: String?
    public let agent: String?
    public let model: String?
    public let embedder: String?
    public let autoSelectTools: Bool?

    public init(appName: String? = nil, appIconRef: String? = nil, agent: String? = nil, model: String? = nil, embedder: String? = nil, autoSelectTools: Bool? = nil) {
        self.appName = appName
        self.appIconRef = appIconRef
        self.agent = agent
        self.model = model
        self.embedder = embedder
        self.autoSelectTools = autoSelectTools
    }
}

public struct MetadataTargetContext: Codable, Sendable {
    public let platform: String?
    public let formFactor: String?
    public let surface: String?
    public let capabilities: [String]

    public init(
        platform: String? = nil,
        formFactor: String? = nil,
        surface: String? = nil,
        capabilities: [String] = []
    ) {
        self.platform = platform
        self.formFactor = formFactor
        self.surface = surface
        self.capabilities = capabilities
    }
}

public struct SessionDebugOptions: Codable, Sendable {
    public let enabled: Bool
    public let level: String?
    public let components: [String]

    public init(enabled: Bool = true, level: String? = nil, components: [String] = []) {
        self.enabled = enabled
        self.level = level
        self.components = components
    }

    public func headerFields() -> [String: String] {
        guard enabled else { return [:] }
        var headers: [String: String] = ["X-Agently-Debug": "true"]
        let trimmedLevel = level?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
        if !trimmedLevel.isEmpty {
            headers["X-Agently-Debug-Level"] = trimmedLevel
        }
        let cleanedComponents = components
            .map { $0.trimmingCharacters(in: .whitespacesAndNewlines) }
            .filter { !$0.isEmpty }
        if !cleanedComponents.isEmpty {
            headers["X-Agently-Debug-Components"] = cleanedComponents.joined(separator: ",")
        }
        return headers
    }
}

public struct WorkspaceCapabilities: Codable, Sendable {
    public let agentAutoSelection: Bool?
    public let modelAutoSelection: Bool?
    public let toolAutoSelection: Bool?
    public let goals: Bool?
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
        goals: Bool? = nil,
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
        self.goals = goals
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
    public let workspaceVersion: String?
    public let metadataVersion: String?
    public let appName: String?
    public let appIconRef: String?
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
        workspaceVersion: String? = nil,
        metadataVersion: String? = nil,
        appName: String? = nil,
        appIconRef: String? = nil,
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
        self.workspaceVersion = workspaceVersion
        self.metadataVersion = metadataVersion
        self.appName = appName
        self.appIconRef = appIconRef
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

public struct RunView: Codable, Sendable, Equatable {
    public let id: String
    public let turnID: String?
    public let conversationID: String?
    public let model: String?
    public let provider: String?
    public let status: String?
    public let createdAt: String?

    enum CodingKeys: String, CodingKey {
        case id = "Id"
        case turnID = "TurnId"
        case conversationID = "ConversationId"
        case model = "Model"
        case provider = "ModelProvider"
        case status = "Status"
        case createdAt = "CreatedAt"
    }

    public init(
        id: String,
        turnID: String? = nil,
        conversationID: String? = nil,
        model: String? = nil,
        provider: String? = nil,
        status: String? = nil,
        createdAt: String? = nil
    ) {
        self.id = id
        self.turnID = turnID
        self.conversationID = conversationID
        self.model = model
        self.provider = provider
        self.status = status
        self.createdAt = createdAt
    }
}

public struct WorkspaceWindowSnapshot: Codable, Sendable, Equatable {
    public let windowId: String
    public let conversationId: String?
    public let windowKey: String
    public let windowTitle: String?
    public let presentation: String?
    public let region: String?
    public let parentKey: String?
    public let workspaceSharePct: Int?
    public let workspaceMinHeight: Int?
    public let inTab: Bool?
    public let parameters: [String: JSONValue]?
    public let windowForm: [String: JSONValue]?

    public init(
        windowId: String,
        conversationId: String? = nil,
        windowKey: String,
        windowTitle: String? = nil,
        presentation: String? = nil,
        region: String? = nil,
        parentKey: String? = nil,
        workspaceSharePct: Int? = nil,
        workspaceMinHeight: Int? = nil,
        inTab: Bool? = nil,
        parameters: [String: JSONValue]? = nil,
        windowForm: [String: JSONValue]? = nil
    ) {
        self.windowId = windowId
        self.conversationId = conversationId
        self.windowKey = windowKey
        self.windowTitle = windowTitle
        self.presentation = presentation
        self.region = region
        self.parentKey = parentKey
        self.workspaceSharePct = workspaceSharePct
        self.workspaceMinHeight = workspaceMinHeight
        self.inTab = inTab
        self.parameters = parameters
        self.windowForm = windowForm
    }
}

public struct HostedWorkspaceRestoreState: Codable, Sendable, Equatable {
    public let windows: [WorkspaceWindowSnapshot]
    public let selectedWindowId: String?

    public init(windows: [WorkspaceWindowSnapshot] = [], selectedWindowId: String? = nil) {
        self.windows = windows
        self.selectedWindowId = selectedWindowId
    }
}

public struct UIEvent: Codable, Sendable, Equatable {
    public let seq: Int64
    public let at: String?
    public let conversationId: String?
    public let clientId: String?
    public let windowId: String?
    public let windowKey: String?
    public let kind: String?
    public let actor: String?
    public let detail: [String: JSONValue]?

    public init(
        seq: Int64,
        at: String? = nil,
        conversationId: String? = nil,
        clientId: String? = nil,
        windowId: String? = nil,
        windowKey: String? = nil,
        kind: String? = nil,
        actor: String? = nil,
        detail: [String: JSONValue]? = nil
    ) {
        self.seq = seq
        self.at = at
        self.conversationId = conversationId
        self.clientId = clientId
        self.windowId = windowId
        self.windowKey = windowKey
        self.kind = kind
        self.actor = actor
        self.detail = detail
    }
}

public struct ListUIEventsInput: Sendable, Equatable {
    public let conversationId: String
    public let clientId: String?
    public let windowId: String?
    public let windowKey: String?
    public let kinds: [String]
    public let sinceSeq: Int64?
    public let limit: Int?

    public init(
        conversationId: String,
        clientId: String? = nil,
        windowId: String? = nil,
        windowKey: String? = nil,
        kinds: [String] = [],
        sinceSeq: Int64? = nil,
        limit: Int? = nil
    ) {
        self.conversationId = conversationId
        self.clientId = clientId
        self.windowId = windowId
        self.windowKey = windowKey
        self.kinds = kinds
        self.sinceSeq = sinceSeq
        self.limit = limit
    }
}

public struct ListUIEventsOutput: Codable, Sendable, Equatable {
    public let conversationId: String?
    public let clientId: String?
    public let events: [UIEvent]

    public init(conversationId: String? = nil, clientId: String? = nil, events: [UIEvent] = []) {
        self.conversationId = conversationId
        self.clientId = clientId
        self.events = events
    }
}

public struct ListTemplatesInput: Codable, Sendable {
    public init() {}
}

public struct TemplateListItem: Codable, Sendable, Equatable {
    public let name: String
    public let description: String?
    public let format: String?

    public init(name: String, description: String? = nil, format: String? = nil) {
        self.name = name
        self.description = description
        self.format = format
    }
}

public struct ListTemplatesOutput: Codable, Sendable {
    public let items: [TemplateListItem]

    public init(items: [TemplateListItem] = []) {
        self.items = items
    }
}

public struct GetTemplateInput: Codable, Sendable {
    public let name: String
    public let includeDocument: Bool?

    public init(name: String, includeDocument: Bool? = nil) {
        self.name = name
        self.includeDocument = includeDocument
    }
}

public struct GetTemplateOutput: Codable, Sendable {
    public let name: String?
    public let format: String?
    public let description: String?
    public let instructions: String?
    public let fences: JSONValue?
    public let schema: JSONValue?
    public let examples: JSONValue?
    public let includedDocument: Bool

    public init(
        name: String? = nil,
        format: String? = nil,
        description: String? = nil,
        instructions: String? = nil,
        fences: JSONValue? = nil,
        schema: JSONValue? = nil,
        examples: JSONValue? = nil,
        includedDocument: Bool = false
    ) {
        self.name = name
        self.format = format
        self.description = description
        self.instructions = instructions
        self.fences = fences
        self.schema = schema
        self.examples = examples
        self.includedDocument = includedDocument
    }
}

public struct ListSkillsInput: Codable, Sendable {
    public let conversationID: String?

    enum CodingKeys: String, CodingKey {
        case conversationID = "conversationId"
    }

    public init(conversationID: String? = nil) {
        self.conversationID = conversationID
    }
}

public struct SkillItem: Codable, Sendable, Equatable {
    public let name: String?
    public let description: String?

    public init(name: String? = nil, description: String? = nil) {
        self.name = name
        self.description = description
    }
}

public struct ListSkillsOutput: Codable, Sendable {
    public let items: [SkillItem]
    public let diagnostics: [String]

    public init(items: [SkillItem] = [], diagnostics: [String] = []) {
        self.items = items
        self.diagnostics = diagnostics
    }
}

public struct ActivateSkillInput: Codable, Sendable {
    public let conversationID: String?
    public let name: String?
    public let args: String?

    enum CodingKeys: String, CodingKey {
        case conversationID = "conversationId"
        case name
        case args
    }

    public init(conversationID: String? = nil, name: String? = nil, args: String? = nil) {
        self.conversationID = conversationID
        self.name = name
        self.args = args
    }
}

public struct ActivateSkillOutput: Codable, Sendable {
    public let name: String?
    public let body: String?

    public init(name: String? = nil, body: String? = nil) {
        self.name = name
        self.body = body
    }
}

public struct SkillDiagnosticsOutput: Codable, Sendable {
    public let items: [String]

    public init(items: [String] = []) {
        self.items = items
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
    public let lastTurnID: String?
    public let agentID: String?
    public let title: String?
    public let summary: String?
    public let stage: String?
    public let visibility: String?
    public let shareable: Int?
    public let conversationParentID: String?
    public let conversationParentTurnID: String?
    public let createdAt: String?
    public let lastActivity: String?
    public let createdByUserID: String?
    public let promptTokens: Int?
    public let completionTokens: Int?
    public let totalTokens: Int?
    public let cost: Double?

    enum CodingKeys: String, CodingKey {
        case id = "Id"
        case lastTurnID = "LastTurnId"
        case agentID = "AgentId"
        case title = "Title"
        case summary = "Summary"
        case stage = "Stage"
        case visibility = "Visibility"
        case shareable = "Shareable"
        case conversationParentID = "ConversationParentId"
        case conversationParentTurnID = "ConversationParentTurnId"
        case createdAt = "CreatedAt"
        case lastActivity = "LastActivity"
        case createdByUserID = "CreatedByUserId"
        case promptTokens = "UsageInputTokens"
        case completionTokens = "UsageOutputTokens"
        case totalTokens = "UsageEmbeddingTokens"
        case cost
    }

    public init(
        id: String,
        lastTurnID: String? = nil,
        agentID: String? = nil,
        title: String? = nil,
        summary: String? = nil,
        stage: String? = nil,
        visibility: String? = nil,
        shareable: Int? = nil,
        conversationParentID: String? = nil,
        conversationParentTurnID: String? = nil,
        createdAt: String? = nil,
        lastActivity: String? = nil,
        createdByUserID: String? = nil,
        promptTokens: Int? = nil,
        completionTokens: Int? = nil,
        totalTokens: Int? = nil,
        cost: Double? = nil
    ) {
        self.id = id
        self.lastTurnID = lastTurnID
        self.agentID = agentID
        self.title = title
        self.summary = summary
        self.stage = stage
        self.visibility = visibility
        self.shareable = shareable
        self.conversationParentID = conversationParentID
        self.conversationParentTurnID = conversationParentTurnID
        self.createdAt = createdAt
        self.lastActivity = lastActivity
        self.createdByUserID = createdByUserID
        self.promptTokens = promptTokens
        self.completionTokens = completionTokens
        self.totalTokens = totalTokens
        self.cost = cost
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
    public let parentID: String?
    public let parentTurnID: String?
    public let excludeScheduled: Bool?
    public let query: String?
    public let status: String?
    public let page: PageInput?

    enum CodingKeys: String, CodingKey {
        case agentID = "agentId"
        case parentID = "parentId"
        case parentTurnID = "parentTurnId"
        case excludeScheduled
        case query = "q"
        case status
        case page
    }

    public init(
        agentID: String? = nil,
        parentID: String? = nil,
        parentTurnID: String? = nil,
        excludeScheduled: Bool? = nil,
        query: String? = nil,
        status: String? = nil,
        page: PageInput? = nil
    ) {
        self.agentID = agentID
        self.parentID = parentID
        self.parentTurnID = parentTurnID
        self.excludeScheduled = excludeScheduled
        self.query = query
        self.status = status
        self.page = page
    }
}

public struct CreateConversationInput: Codable, Sendable {
    public let agentID: String?
    public let title: String?
    public let metadata: [String: JSONValue]
    public let parentConversationID: String?
    public let parentTurnID: String?

    enum CodingKeys: String, CodingKey {
        case agentID = "agentId"
        case title
        case metadata
        case parentConversationID = "parentConversationId"
        case parentTurnID = "parentTurnId"
    }

    public init(
        agentID: String? = nil,
        title: String? = nil,
        metadata: [String: JSONValue] = [:],
        parentConversationID: String? = nil,
        parentTurnID: String? = nil
    ) {
        self.agentID = agentID
        self.title = title
        self.metadata = metadata
        self.parentConversationID = parentConversationID
        self.parentTurnID = parentTurnID
    }
}

public struct GetTranscriptInput: Codable, Sendable {
    public let conversationID: String
    public let since: String?
    public let includeModelCalls: Bool?
    public let includeToolCalls: Bool?
    public let includeFeeds: Bool?

    enum CodingKeys: String, CodingKey {
        case conversationID = "conversationId"
        case since
        case includeModelCalls
        case includeToolCalls
        case includeFeeds
    }

    public init(
        conversationID: String,
        since: String? = nil,
        includeModelCalls: Bool? = nil,
        includeToolCalls: Bool? = nil,
        includeFeeds: Bool? = nil
    ) {
        self.conversationID = conversationID
        self.since = since
        self.includeModelCalls = includeModelCalls
        self.includeToolCalls = includeToolCalls
        self.includeFeeds = includeFeeds
    }
}

public struct QueryAttachment: Codable, Sendable, Identifiable {
    public var id: String { uri }
    public let name: String
    public let uri: String
    public let size: Int64?
    public let mime: String?
    public let stagingFolder: String?

    public init(name: String, uri: String, size: Int64? = nil, mime: String? = nil, stagingFolder: String? = nil) {
        self.name = name
        self.uri = uri
        self.size = size
        self.mime = mime
        self.stagingFolder = stagingFolder
    }
}

public struct QueryInput: Codable, Sendable {
    public let conversationID: String?
    public let parentConversationID: String?
    public let conversationTitle: String?
    public let messageID: String?
    public let agentID: String?
    public let userID: String?
    public let query: String
    public let attachments: [QueryAttachment]
    public let model: String?
    public let tools: [String]
    public let toolBundles: [String]
    public let autoSelectTools: Bool?
    public let context: [String: JSONValue]
    public let reasoningEffort: String?
    public let elicitationMode: String?
    public let autoSummarize: Bool?
    public let disableChains: Bool?
    public let allowedChains: [String]
    public let toolCallExposure: String?

    enum CodingKeys: String, CodingKey {
        case conversationID = "conversationId"
        case parentConversationID = "parentConversationId"
        case conversationTitle
        case messageID = "messageId"
        case agentID = "agentId"
        case userID = "userId"
        case query
        case attachments
        case model
        case tools
        case toolBundles
        case autoSelectTools
        case context
        case reasoningEffort
        case elicitationMode
        case autoSummarize
        case disableChains
        case allowedChains
        case toolCallExposure
    }

    public init(
        conversationID: String? = nil,
        parentConversationID: String? = nil,
        conversationTitle: String? = nil,
        messageID: String? = nil,
        agentID: String? = nil,
        userID: String? = nil,
        query: String,
        attachments: [QueryAttachment] = [],
        model: String? = nil,
        tools: [String] = [],
        toolBundles: [String] = [],
        autoSelectTools: Bool? = nil,
        context: [String: JSONValue] = [:],
        reasoningEffort: String? = nil,
        elicitationMode: String? = nil,
        autoSummarize: Bool? = nil,
        disableChains: Bool? = nil,
        allowedChains: [String] = [],
        toolCallExposure: String? = nil
    ) {
        self.conversationID = conversationID
        self.parentConversationID = parentConversationID
        self.conversationTitle = conversationTitle
        self.messageID = messageID
        self.agentID = agentID
        self.userID = userID
        self.query = query
        self.attachments = attachments
        self.model = model
        self.tools = tools
        self.toolBundles = toolBundles
        self.autoSelectTools = autoSelectTools
        self.context = context
        self.reasoningEffort = reasoningEffort
        self.elicitationMode = elicitationMode
        self.autoSummarize = autoSummarize
        self.disableChains = disableChains
        self.allowedChains = allowedChains
        self.toolCallExposure = toolCallExposure
    }
}

public struct QueryOutput: Codable, Sendable {
    public let conversationID: String?
    public let content: String
    public let model: String?
    public let messageID: String?
    public let elicitation: JSONValue?
    public let plan: JSONValue?
    public let usage: JSONValue?
    public let warnings: [String]
    public let projection: JSONValue?

    enum CodingKeys: String, CodingKey {
        case conversationID = "conversationId"
        case content
        case model
        case messageID = "messageId"
        case elicitation
        case plan
        case usage
        case warnings
        case projection
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        conversationID = try container.decodeIfPresent(String.self, forKey: .conversationID)
        content = try container.decodeIfPresent(String.self, forKey: .content) ?? ""
        model = try container.decodeIfPresent(String.self, forKey: .model)
        messageID = try container.decodeIfPresent(String.self, forKey: .messageID)
        elicitation = try container.decodeIfPresent(JSONValue.self, forKey: .elicitation)
        plan = try container.decodeIfPresent(JSONValue.self, forKey: .plan)
        usage = try container.decodeIfPresent(JSONValue.self, forKey: .usage)
        warnings = try container.decodeIfPresent([String].self, forKey: .warnings) ?? []
        projection = try container.decodeIfPresent(JSONValue.self, forKey: .projection)
    }

    public init(
        conversationID: String? = nil,
        content: String = "",
        model: String? = nil,
        messageID: String? = nil,
        elicitation: JSONValue? = nil,
        plan: JSONValue? = nil,
        usage: JSONValue? = nil,
        warnings: [String] = [],
        projection: JSONValue? = nil
    ) {
        self.conversationID = conversationID
        self.content = content
        self.model = model
        self.messageID = messageID
        self.elicitation = elicitation
        self.plan = plan
        self.usage = usage
        self.warnings = warnings
        self.projection = projection
    }
}

public struct ConversationStateResponse: Decodable, Sendable {
    public let schemaVersion: String?
    public let conversation: ConversationState?
    public let feeds: [ActiveFeedState]
    public let usage: UsageSummary?
    public let eventCursor: String?

    enum CodingKeys: String, CodingKey {
        case schemaVersion
        case conversation
        case feeds
        case usage
        case eventCursor
    }

    public init(
        schemaVersion: String? = nil,
        conversation: ConversationState? = nil,
        feeds: [ActiveFeedState] = [],
        usage: UsageSummary? = nil,
        eventCursor: String? = nil
    ) {
        self.schemaVersion = schemaVersion
        self.conversation = conversation
        self.feeds = feeds
        self.usage = usage
        self.eventCursor = eventCursor
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        self.schemaVersion = try container.decodeIfPresent(String.self, forKey: .schemaVersion)
        self.conversation = try container.decodeIfPresent(ConversationState.self, forKey: .conversation)
        self.feeds = try container.decodeIfPresent([ActiveFeedState].self, forKey: .feeds) ?? []
        self.usage = try container.decodeIfPresent(UsageSummary.self, forKey: .usage)
        self.eventCursor = try container.decodeIfPresent(String.self, forKey: .eventCursor)
    }
}

public struct ConversationState: Decodable, Sendable {
    public let conversationID: String
    public let turns: [TurnState]
    public let feeds: [ActiveFeedState]

    enum CodingKeys: String, CodingKey {
        case conversationID = "conversationId"
        case turns
        case feeds
    }

    public init(
        conversationID: String,
        turns: [TurnState] = [],
        feeds: [ActiveFeedState] = []
    ) {
        self.conversationID = conversationID
        self.turns = turns
        self.feeds = feeds
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        self.conversationID = try container.decode(String.self, forKey: .conversationID)
        self.turns = try container.decodeIfPresent([TurnState].self, forKey: .turns) ?? []
        self.feeds = try container.decodeIfPresent([ActiveFeedState].self, forKey: .feeds) ?? []
    }
}

public struct TurnState: Codable, Sendable, Identifiable {
    public var id: String { turnID }
    public let turnID: String
    public let status: String?
    public let user: UserMessageState?
    public let users: [UserMessageState]
    public let messages: [TurnMessageState]
    public let execution: ExecutionState?
    public let assistant: AssistantState?
    public let planner: PlannerState?
    public let elicitation: ElicitationState?
    public let linkedConversations: [LinkedConversationState]
    public let createdAt: String?
    public let queueSeq: Int?
    public let startedByMessageID: String?

    enum CodingKeys: String, CodingKey {
        case turnID = "turnId"
        case status
        case user
        case users
        case messages
        case execution
        case assistant
        case planner
        case elicitation
        case linkedConversations
        case createdAt
        case queueSeq
        case startedByMessageID = "startedByMessageId"
    }

    public init(
        turnID: String,
        status: String? = nil,
        user: UserMessageState? = nil,
        users: [UserMessageState] = [],
        messages: [TurnMessageState] = [],
        execution: ExecutionState? = nil,
        assistant: AssistantState? = nil,
        planner: PlannerState? = nil,
        elicitation: ElicitationState? = nil,
        linkedConversations: [LinkedConversationState] = [],
        createdAt: String? = nil,
        queueSeq: Int? = nil,
        startedByMessageID: String? = nil
    ) {
        self.turnID = turnID
        self.status = status
        self.user = user
        self.users = users
        self.messages = messages
        self.execution = execution
        self.assistant = assistant
        self.planner = planner
        self.elicitation = elicitation
        self.linkedConversations = linkedConversations
        self.createdAt = createdAt
        self.queueSeq = queueSeq
        self.startedByMessageID = startedByMessageID
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        self.turnID = try container.decode(String.self, forKey: .turnID)
        self.status = try container.decodeIfPresent(String.self, forKey: .status)
        self.user = try container.decodeIfPresent(UserMessageState.self, forKey: .user)
        self.users = try container.decodeIfPresent([UserMessageState].self, forKey: .users) ?? []
        self.messages = try container.decodeIfPresent([TurnMessageState].self, forKey: .messages) ?? []
        self.execution = try container.decodeIfPresent(ExecutionState.self, forKey: .execution)
        self.assistant = try container.decodeIfPresent(AssistantState.self, forKey: .assistant)
        self.planner = try container.decodeIfPresent(PlannerState.self, forKey: .planner)
        self.elicitation = try container.decodeIfPresent(ElicitationState.self, forKey: .elicitation)
        self.linkedConversations = try container.decodeIfPresent([LinkedConversationState].self, forKey: .linkedConversations) ?? []
        self.createdAt = try container.decodeIfPresent(String.self, forKey: .createdAt)
        self.queueSeq = try container.decodeIfPresent(Int.self, forKey: .queueSeq)
        self.startedByMessageID = try container.decodeIfPresent(String.self, forKey: .startedByMessageID)
    }
}

public struct UserMessageState: Codable, Sendable {
    public let messageID: String
    public let content: String?

    enum CodingKeys: String, CodingKey {
        case messageID = "messageId"
        case content
    }

    public init(messageID: String, content: String? = nil) {
        self.messageID = messageID
        self.content = content
    }
}

public struct TurnMessageState: Codable, Sendable {
    public let messageID: String
    public let role: String
    public let content: String?
    public let createdAt: String?
    public let sequence: Int?
    public let interim: Int?
    public let mode: String?
    public let status: String?

    enum CodingKeys: String, CodingKey {
        case messageID = "messageId"
        case role
        case content
        case createdAt
        case sequence
        case interim
        case mode
        case status
    }
}

public struct AssistantState: Codable, Sendable {
    public let narration: AssistantMessageState?
    public let final: AssistantMessageState?
    public let messages: [AssistantMessageState]

    enum CodingKeys: String, CodingKey {
        case narration
        case final
        case messages
    }

    public init(
        narration: AssistantMessageState? = nil,
        final: AssistantMessageState? = nil,
        messages: [AssistantMessageState] = []
    ) {
        self.narration = narration
        self.final = final
        self.messages = messages
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        self.narration = try container.decodeIfPresent(AssistantMessageState.self, forKey: .narration)
        self.final = try container.decodeIfPresent(AssistantMessageState.self, forKey: .final)
        self.messages = try container.decodeIfPresent([AssistantMessageState].self, forKey: .messages) ?? []
    }
}

public struct AssistantMessageState: Codable, Sendable {
    public let messageID: String
    public let content: String?
    public let createdAt: String?

    enum CodingKeys: String, CodingKey {
        case messageID = "messageId"
        case content
        case createdAt
    }

    public init(messageID: String, content: String? = nil, createdAt: String? = nil) {
        self.messageID = messageID
        self.content = content
        self.createdAt = createdAt
    }
}

public struct PlannerState: Codable, Sendable {
    public let status: String?
    public let trigger: String?
    public let staticProfile: String?
    public let strategyFamily: String?
    public let attempt: Int?
    public let secondPolicy: String?
    public let outputPayloadID: String?
    public let validated: Bool?

    enum CodingKeys: String, CodingKey {
        case status
        case trigger
        case staticProfile
        case strategyFamily
        case attempt
        case secondPolicy
        case outputPayloadID = "outputPayloadId"
        case validated
    }
}

public struct ExecutionState: Codable, Sendable {
    public let pages: [ExecutionPageState]
    public let activePageIndex: Int?
    public let totalElapsedMs: Int64?

    enum CodingKeys: String, CodingKey {
        case pages
        case activePageIndex
        case totalElapsedMs
    }

    public init(
        pages: [ExecutionPageState] = [],
        activePageIndex: Int? = nil,
        totalElapsedMs: Int64? = nil
    ) {
        self.pages = pages
        self.activePageIndex = activePageIndex
        self.totalElapsedMs = totalElapsedMs
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        self.pages = try container.decodeIfPresent([ExecutionPageState].self, forKey: .pages) ?? []
        self.activePageIndex = try container.decodeIfPresent(Int.self, forKey: .activePageIndex)
        self.totalElapsedMs = try container.decodeIfPresent(Int64.self, forKey: .totalElapsedMs)
    }
}

public struct ExecutionPageState: Codable, Sendable, Identifiable {
    public var id: String { pageID }
    public let pageID: String
    public let assistantMessageID: String?
    public let parentMessageID: String?
    public let turnID: String?
    public let iteration: Int?
    public let sequence: Int?
    public let executionRole: String?
    public let phase: String?
    public let mode: String?
    public let status: String?
    public let modelSteps: [ModelStepState]
    public let toolSteps: [ToolStepState]
    public let narrationMessageID: String?
    public let finalAssistantMessageID: String?
    public let narration: String?
    public let content: String?
    public let finalResponse: Bool

    enum CodingKeys: String, CodingKey {
        case pageID = "pageId"
        case assistantMessageID = "assistantMessageId"
        case parentMessageID = "parentMessageId"
        case turnID = "turnId"
        case iteration
        case sequence
        case executionRole
        case phase
        case mode
        case status
        case modelSteps
        case toolSteps
        case narrationMessageID = "narrationMessageId"
        case finalAssistantMessageID = "finalAssistantMessageId"
        case narration
        case content
        case finalResponse
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        self.pageID = try container.decode(String.self, forKey: .pageID)
        self.assistantMessageID = try container.decodeIfPresent(String.self, forKey: .assistantMessageID)
        self.parentMessageID = try container.decodeIfPresent(String.self, forKey: .parentMessageID)
        self.turnID = try container.decodeIfPresent(String.self, forKey: .turnID)
        self.iteration = try container.decodeIfPresent(Int.self, forKey: .iteration)
        self.sequence = try container.decodeIfPresent(Int.self, forKey: .sequence)
        self.executionRole = try container.decodeIfPresent(String.self, forKey: .executionRole)
        self.phase = try container.decodeIfPresent(String.self, forKey: .phase)
        self.mode = try container.decodeIfPresent(String.self, forKey: .mode)
        self.status = try container.decodeIfPresent(String.self, forKey: .status)
        self.modelSteps = try container.decodeIfPresent([ModelStepState].self, forKey: .modelSteps) ?? []
        self.toolSteps = try container.decodeIfPresent([ToolStepState].self, forKey: .toolSteps) ?? []
        self.narrationMessageID = try container.decodeIfPresent(String.self, forKey: .narrationMessageID)
        self.finalAssistantMessageID = try container.decodeIfPresent(String.self, forKey: .finalAssistantMessageID)
        self.narration = try container.decodeIfPresent(String.self, forKey: .narration)
        self.content = try container.decodeIfPresent(String.self, forKey: .content)
        self.finalResponse = try container.decodeIfPresent(Bool.self, forKey: .finalResponse) ?? false
    }
}

public struct ModelStepState: Codable, Sendable, Identifiable {
    public var id: String { modelCallID }
    public let modelCallID: String
    public let assistantMessageID: String?
    public let executionRole: String?
    public let phase: String?
    public let provider: String?
    public let model: String?
    public let status: String?
    public let requestPayloadID: String?
    public let responsePayloadID: String?
    public let providerRequestPayloadID: String?
    public let providerResponsePayloadID: String?
    public let streamPayloadID: String?
    public let requestPayload: JSONValue?
    public let responsePayload: JSONValue?
    public let providerRequestPayload: JSONValue?
    public let providerResponsePayload: JSONValue?
    public let streamPayload: JSONValue?
    public let startedAt: String?
    public let completedAt: String?

    enum CodingKeys: String, CodingKey {
        case modelCallID = "modelCallId"
        case assistantMessageID = "assistantMessageId"
        case executionRole
        case phase
        case provider
        case model
        case status
        case requestPayloadID = "requestPayloadId"
        case responsePayloadID = "responsePayloadId"
        case providerRequestPayloadID = "providerRequestPayloadId"
        case providerResponsePayloadID = "providerResponsePayloadId"
        case streamPayloadID = "streamPayloadId"
        case requestPayload
        case responsePayload
        case providerRequestPayload
        case providerResponsePayload
        case streamPayload
        case startedAt
        case completedAt
    }
}

public struct ToolStepState: Codable, Sendable, Identifiable {
    public var id: String { toolCallID }
    public let toolCallID: String
    public let toolMessageID: String?
    public let parentMessageID: String?
    public let toolName: String
    public let content: String?
    public let executionRole: String?
    public let operationID: String?
    public let status: String?
    public let requestPayloadID: String?
    public let responsePayloadID: String?
    public let requestPayload: JSONValue?
    public let responsePayload: JSONValue?
    public let linkedConversationID: String?
    public let linkedConversationAgentID: String?
    public let linkedConversationTitle: String?
    public let startedAt: String?
    public let completedAt: String?
    public let asyncOperation: AsyncOperationState?

    enum CodingKeys: String, CodingKey {
        case toolCallID = "toolCallId"
        case toolMessageID = "toolMessageId"
        case parentMessageID = "parentMessageId"
        case toolName
        case content
        case executionRole
        case operationID = "operationId"
        case status
        case requestPayloadID = "requestPayloadId"
        case responsePayloadID = "responsePayloadId"
        case requestPayload
        case responsePayload
        case linkedConversationID = "linkedConversationId"
        case linkedConversationAgentID = "linkedConversationAgentId"
        case linkedConversationTitle = "linkedConversationTitle"
        case startedAt
        case completedAt
        case asyncOperation
    }
}

public struct AsyncOperationState: Codable, Sendable {
    public let operationID: String
    public let status: String?
    public let message: String?
    public let error: String?
    public let response: JSONValue?

    enum CodingKeys: String, CodingKey {
        case operationID = "operationId"
        case status
        case message
        case error
        case response
    }
}

public struct ElicitationState: Codable, Sendable {
    public let elicitationID: String
    public let status: String?
    public let message: String?
    public let requestedSchema: JSONValue?
    public let callbackURL: String?
    public let responsePayload: JSONValue?

    enum CodingKeys: String, CodingKey {
        case elicitationID = "elicitationId"
        case status
        case message
        case requestedSchema
        case callbackURL = "callbackUrl"
        case responsePayload
    }
}

public struct LinkedConversationState: Codable, Sendable, Identifiable {
    public var id: String { conversationID }
    public let conversationID: String
    public let parentConversationID: String?
    public let parentTurnID: String?
    public let toolCallID: String?
    public let agentID: String?
    public let title: String?
    public let status: String?
    public let response: String?
    public let createdAt: String?
    public let updatedAt: String?

    enum CodingKeys: String, CodingKey {
        case conversationID = "conversationId"
        case parentConversationID = "parentConversationId"
        case parentTurnID = "parentTurnId"
        case toolCallID = "toolCallId"
        case agentID = "agentId"
        case title
        case status
        case response
        case createdAt
        case updatedAt
    }
}

public struct UsageSummary: Codable, Sendable {
    public let totalInputTokens: Int?
    public let totalOutputTokens: Int?
}

public typealias ConversationTurn = TurnState
public typealias ConversationMessagePart = AssistantMessageState
public typealias AssistantTurnPart = AssistantState

public struct ActiveFeedState: Codable, Sendable, Identifiable {
    public var id: String { feedID ?? UUID().uuidString }
    public let feedID: String?
    public let name: String?
    public let title: String?
    public let itemCount: Int?
    public let conversationID: String?
    public let turnID: String?
    public let updatedAt: Int64?
    public let data: JSONValue?

    enum CodingKeys: String, CodingKey {
        case feedID = "feedId"
        case name
        case title
        case itemCount
        case conversationID = "conversationId"
        case turnID = "turnId"
        case updatedAt
        case data
    }

    public init(
        feedID: String? = nil,
        name: String? = nil,
        title: String? = nil,
        itemCount: Int? = nil,
        conversationID: String? = nil,
        turnID: String? = nil,
        updatedAt: Int64? = nil,
        data: JSONValue? = nil
    ) {
        self.feedID = feedID
        self.name = name
        self.title = title
        self.itemCount = itemCount
        self.conversationID = conversationID
        self.turnID = turnID
        self.updatedAt = updatedAt
        self.data = data
    }
}

public struct PendingElicitation: Codable, Sendable, Identifiable {
    public let elicitationID: String
    public let conversationID: String?
    public let turnID: String?
    public let message: String?
    public let mode: String?
    public let url: String?
    public let callbackURL: String?
    public let requestedSchema: JSONValue?
    public let status: String?

    enum CodingKeys: String, CodingKey {
        case elicitationID = "elicitationId"
        case conversationID = "conversationId"
        case turnID = "turnId"
        case message
        case mode
        case url
        case callbackURL = "callbackUrl"
        case requestedSchema
        case status
    }

    public var id: String { elicitationID }

    public init(
        elicitationID: String,
        conversationID: String? = nil,
        turnID: String? = nil,
        message: String? = nil,
        mode: String? = nil,
        url: String? = nil,
        callbackURL: String? = nil,
        requestedSchema: JSONValue? = nil,
        status: String? = nil
    ) {
        self.elicitationID = elicitationID
        self.conversationID = conversationID
        self.turnID = turnID
        self.message = message
        self.mode = mode
        self.url = url
        self.callbackURL = callbackURL
        self.requestedSchema = requestedSchema
        self.status = status
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
    public let userID: String?
    public let conversationID: String?
    public let turnID: String?
    public let messageID: String?
    public let toolName: String
    public let title: String?
    public let arguments: JSONValue?
    public let metadata: JSONValue?
    public let status: String
    public let decision: String?
    public let createdAt: String?
    public let updatedAt: String?
    public let errorMessage: String?

    enum CodingKeys: String, CodingKey {
        case id
        case userID = "userId"
        case conversationID = "conversationId"
        case turnID = "turnId"
        case messageID = "messageId"
        case toolName
        case title
        case arguments
        case metadata
        case status
        case decision
        case createdAt
        case updatedAt
        case errorMessage
    }
}

public struct ListPendingToolApprovalsInput: Codable, Sendable {
    public let userID: String?
    public let conversationID: String?
    public let status: String?
    public let limit: Int?
    public let offset: Int?

    enum CodingKeys: String, CodingKey {
        case userID = "userId"
        case conversationID = "conversationId"
        case status
        case limit
        case offset
    }

    public init(
        userID: String? = nil,
        conversationID: String? = nil,
        status: String? = nil,
        limit: Int? = nil,
        offset: Int? = nil
    ) {
        self.userID = userID
        self.conversationID = conversationID
        self.status = status
        self.limit = limit
        self.offset = offset
    }
}

public struct PendingToolApprovalRows: Codable, Sendable {
    public let rows: [PendingToolApproval]

    public init(rows: [PendingToolApproval] = []) {
        self.rows = rows
    }
}

public struct PendingToolApprovalPage: Codable, Sendable {
    public let rows: [PendingToolApproval]
    public let total: Int
    public let offset: Int
    public let limit: Int
    public let hasMore: Bool

    public init(
        rows: [PendingToolApproval] = [],
        total: Int = 0,
        offset: Int = 0,
        limit: Int = 0,
        hasMore: Bool = false
    ) {
        self.rows = rows
        self.total = total
        self.offset = offset
        self.limit = limit
        self.hasMore = hasMore
    }
}

public struct DecideToolApprovalInput: Codable, Sendable {
    public let id: String
    public let action: String
    public let userID: String?
    public let reason: String?
    public let note: String?
    public let editedFields: [String: JSONValue]
    public let callbackState: [String: JSONValue]
    public let payload: [String: JSONValue]

    enum CodingKeys: String, CodingKey {
        case id
        case action
        case userID = "userId"
        case reason
        case note
        case editedFields
        case callbackState
        case payload
    }

    public init(
        id: String,
        action: String,
        userID: String? = nil,
        reason: String? = nil,
        note: String? = nil,
        editedFields: [String: JSONValue] = [:],
        callbackState: [String: JSONValue] = [:],
        payload: [String: JSONValue] = [:]
    ) {
        self.id = id
        self.action = action
        self.userID = userID
        self.reason = reason
        self.note = note
        self.editedFields = editedFields
        self.callbackState = callbackState
        self.payload = payload
    }
}

public struct DecideToolApprovalOutput: Codable, Sendable {
    public let status: String?
    public let message: String?

    public init(status: String? = nil, message: String? = nil) {
        self.status = status
        self.message = message
    }
}

public struct ResourceRef: Codable, Sendable {
    public let kind: String
    public let name: String

    public init(kind: String, name: String) {
        self.kind = kind
        self.name = name
    }
}

public struct ListResourcesInput: Codable, Sendable {
    public let kind: String

    public init(kind: String) {
        self.kind = kind
    }
}

public struct ListResourcesOutput: Codable, Sendable {
    public let names: [String]

    public init(names: [String] = []) {
        self.names = names
    }
}

public struct ResourcePayload: Codable, Sendable {
    public let kind: String
    public let name: String
    public let data: String?

    public init(kind: String, name: String, data: String? = nil) {
        self.kind = kind
        self.name = name
        self.data = data
    }
}

public struct SaveResourceInput: Codable, Sendable {
    public let kind: String
    public let name: String
    public let data: String

    public init(kind: String, name: String, data: String) {
        self.kind = kind
        self.name = name
        self.data = data
    }
}

public struct ExportResourcesInput: Codable, Sendable {
    public let kinds: [String]

    public init(kinds: [String] = []) {
        self.kinds = kinds
    }
}

public struct ExportResourcesOutput: Codable, Sendable {
    public let resources: [ResourcePayload]

    public init(resources: [ResourcePayload] = []) {
        self.resources = resources
    }
}

public struct ImportResourcesInput: Codable, Sendable {
    public let resources: [ResourcePayload]
    public let replace: Bool

    public init(resources: [ResourcePayload] = [], replace: Bool = false) {
        self.resources = resources
        self.replace = replace
    }
}

public struct ImportResourcesOutput: Codable, Sendable {
    public let imported: Int
    public let skipped: Int

    public init(imported: Int = 0, skipped: Int = 0) {
        self.imported = imported
        self.skipped = skipped
    }
}

public struct Schedule: Codable, Sendable, Identifiable {
    public let id: String
    public let name: String
    public let description: String?
    public let createdByUserID: String?
    public let visibility: String?
    public let agentRef: String
    public let modelOverride: String?
    public let userCredURL: String?
    public let enabled: Bool
    public let startAt: String?
    public let endAt: String?
    public let scheduleType: String
    public let cronExpr: String?
    public let intervalSeconds: Int?
    public let timezone: String?
    public let timeoutSeconds: Int?
    public let taskPromptURI: String?
    public let taskPrompt: String?
    public let nextRunAt: String?
    public let lastRunAt: String?
    public let lastStatus: String?
    public let lastError: String?
    public let leaseOwner: String?
    public let leaseUntil: String?
    public let createdAt: String?
    public let updatedAt: String?

    enum CodingKeys: String, CodingKey {
        case id
        case name
        case description
        case createdByUserID = "createdByUserId"
        case visibility
        case agentRef
        case modelOverride
        case userCredURL = "userCredUrl"
        case enabled
        case startAt
        case endAt
        case scheduleType
        case cronExpr
        case intervalSeconds
        case timezone
        case timeoutSeconds
        case taskPromptURI = "taskPromptUri"
        case taskPrompt
        case nextRunAt
        case lastRunAt
        case lastStatus
        case lastError
        case leaseOwner
        case leaseUntil
        case createdAt
        case updatedAt
    }

    public init(
        id: String,
        name: String,
        description: String? = nil,
        createdByUserID: String? = nil,
        visibility: String? = nil,
        agentRef: String,
        modelOverride: String? = nil,
        userCredURL: String? = nil,
        enabled: Bool = false,
        startAt: String? = nil,
        endAt: String? = nil,
        scheduleType: String,
        cronExpr: String? = nil,
        intervalSeconds: Int? = nil,
        timezone: String? = nil,
        timeoutSeconds: Int? = nil,
        taskPromptURI: String? = nil,
        taskPrompt: String? = nil,
        nextRunAt: String? = nil,
        lastRunAt: String? = nil,
        lastStatus: String? = nil,
        lastError: String? = nil,
        leaseOwner: String? = nil,
        leaseUntil: String? = nil,
        createdAt: String? = nil,
        updatedAt: String? = nil
    ) {
        self.id = id
        self.name = name
        self.description = description
        self.createdByUserID = createdByUserID
        self.visibility = visibility
        self.agentRef = agentRef
        self.modelOverride = modelOverride
        self.userCredURL = userCredURL
        self.enabled = enabled
        self.startAt = startAt
        self.endAt = endAt
        self.scheduleType = scheduleType
        self.cronExpr = cronExpr
        self.intervalSeconds = intervalSeconds
        self.timezone = timezone
        self.timeoutSeconds = timeoutSeconds
        self.taskPromptURI = taskPromptURI
        self.taskPrompt = taskPrompt
        self.nextRunAt = nextRunAt
        self.lastRunAt = lastRunAt
        self.lastStatus = lastStatus
        self.lastError = lastError
        self.leaseOwner = leaseOwner
        self.leaseUntil = leaseUntil
        self.createdAt = createdAt
        self.updatedAt = updatedAt
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
    public let elementID: String?
    public let event: String?
    public let handler: String?

    enum CodingKeys: String, CodingKey {
        case elementID = "elementId"
        case event
        case handler
    }

    public init(elementID: String? = nil, event: String? = nil, handler: String? = nil) {
        self.elementID = elementID
        self.event = event
        self.handler = handler
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
    public let label: String
    public let description: String?
    public let item: JSONValue?
    public let selected: Bool

    public init(id: String, label: String, description: String? = nil,
                item: JSONValue? = nil, selected: Bool = false) {
        self.id = id
        self.label = label
        self.description = description
        self.item = item
        self.selected = selected
    }
}

public struct ApprovalCallbackPayload: Codable, Sendable {
    public let approval: ApprovalMeta?
    public let editedFields: [String: JSONValue]
    public let originalArgs: [String: JSONValue]
    public let action: String?

    public init(
        approval: ApprovalMeta? = nil,
        editedFields: [String: JSONValue] = [:],
        originalArgs: [String: JSONValue] = [:],
        action: String? = nil
    ) {
        self.approval = approval
        self.editedFields = editedFields
        self.originalArgs = originalArgs
        self.action = action
    }
}

public struct ApprovalCallbackResult: Codable, Sendable {
    public let allow: Bool?
    public let message: String?
    public let editedFields: [String: JSONValue]
    public let action: String?
    public let payload: [String: JSONValue]

    enum CodingKeys: String, CodingKey {
        case allow
        case message
        case editedFields
        case action
        case payload
    }

    public init(
        allow: Bool? = nil,
        message: String? = nil,
        editedFields: [String: JSONValue] = [:],
        action: String? = nil,
        payload: [String: JSONValue] = [:]
    ) {
        self.allow = allow
        self.message = message
        self.editedFields = editedFields
        self.action = action
        self.payload = payload
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        allow = try container.decodeIfPresent(Bool.self, forKey: .allow)
        message = try container.decodeIfPresent(String.self, forKey: .message)
        editedFields = try container.decodeIfPresent([String: JSONValue].self, forKey: .editedFields) ?? [:]
        action = try container.decodeIfPresent(String.self, forKey: .action)
        payload = try container.decodeIfPresent([String: JSONValue].self, forKey: .payload) ?? [:]
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
    public let id: String?
    public let uri: String

    public init(id: String? = nil, uri: String) {
        self.id = id
        self.uri = uri
    }
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

// MARK: - Phase 1 Android-parity models

struct DynamicCodingKey: CodingKey {
    var stringValue: String
    var intValue: Int? { nil }

    init(_ stringValue: String) {
        self.stringValue = stringValue
    }

    init?(stringValue: String) {
        self.stringValue = stringValue
    }

    init?(intValue: Int) {
        return nil
    }
}

public struct LocalLoginInput: Codable, Sendable {
    public let username: String

    public init(username: String) {
        self.username = username
    }
}

public struct LocalLoginOutput: Codable, Sendable {
    public let sessionID: String
    public let username: String?
    public let provider: String?

    enum CodingKeys: String, CodingKey {
        case sessionID = "sessionId"
        case username
        case provider
    }

    public init(sessionID: String, username: String? = nil, provider: String? = nil) {
        self.sessionID = sessionID
        self.username = username
        self.provider = provider
    }
}

public struct CreateSessionInput: Codable, Sendable {
    public let username: String?
    public let accessToken: String?
    public let idToken: String?
    public let refreshToken: String?

    public init(
        username: String? = nil,
        accessToken: String? = nil,
        idToken: String? = nil,
        refreshToken: String? = nil
    ) {
        self.username = username
        self.accessToken = accessToken
        self.idToken = idToken
        self.refreshToken = refreshToken
    }
}

public struct CreateSessionOutput: Codable, Sendable {
    public let sessionID: String
    public let username: String?

    enum CodingKeys: String, CodingKey {
        case sessionID = "sessionId"
        case username
    }

    public init(sessionID: String, username: String? = nil) {
        self.sessionID = sessionID
        self.username = username
    }
}

public struct AttachSessionInput: Codable, Sendable {
    public let sessionID: String

    enum CodingKeys: String, CodingKey {
        case sessionID = "sessionId"
    }

    public init(sessionID: String) {
        self.sessionID = sessionID
    }
}

public struct AttachSessionOutput: Codable, Sendable {
    public let status: String?
    public let sessionID: String?
    public let username: String?

    enum CodingKeys: String, CodingKey {
        case status
        case sessionID = "sessionId"
        case username
    }

    public init(status: String? = nil, sessionID: String? = nil, username: String? = nil) {
        self.status = status
        self.sessionID = sessionID
        self.username = username
    }
}

public struct OOBLoginInput: Codable, Sendable {
    public let secretsURL: String?
    public let scopes: [String]
    public let accessToken: String?
    public let idToken: String?
    public let refreshToken: String?
    public let username: String?

    public init(
        secretsURL: String? = nil,
        scopes: [String] = [],
        accessToken: String? = nil,
        idToken: String? = nil,
        refreshToken: String? = nil,
        username: String? = nil
    ) {
        self.secretsURL = secretsURL
        self.scopes = scopes
        self.accessToken = accessToken
        self.idToken = idToken
        self.refreshToken = refreshToken
        self.username = username
    }
}

public struct OOBLoginOutput: Codable, Sendable {
    public let sessionID: String?
    public let status: String?
    public let username: String?
    public let provider: String?

    enum CodingKeys: String, CodingKey {
        case sessionID = "sessionId"
        case status
        case username
        case provider
    }

    public init(sessionID: String? = nil, status: String? = nil, username: String? = nil, provider: String? = nil) {
        self.sessionID = sessionID
        self.status = status
        self.username = username
        self.provider = provider
    }
}

public struct IDPDelegateOutput: Codable, Sendable {
    public let mode: String?
    public let idpLogin: String?
    public let provider: String?
    public let authURL: String?
    public let state: String?
    public let expiresIn: Int?
    public let status: String?
    public let message: String?

    enum CodingKeys: String, CodingKey {
        case mode
        case idpLogin
        case provider
        case authURL = "authUrl"
        case state
        case expiresIn
        case status
        case message
    }

    public init(
        mode: String? = nil,
        idpLogin: String? = nil,
        provider: String? = nil,
        authURL: String? = nil,
        state: String? = nil,
        expiresIn: Int? = nil,
        status: String? = nil,
        message: String? = nil
    ) {
        self.mode = mode
        self.idpLogin = idpLogin
        self.provider = provider
        self.authURL = authURL
        self.state = state
        self.expiresIn = expiresIn
        self.status = status
        self.message = message
    }
}

public struct UpdateConversationInput: Codable, Sendable {
    public let title: String?
    public let visibility: String?
    public let shareable: Bool?

    public init(title: String? = nil, visibility: String? = nil, shareable: Bool? = nil) {
        self.title = title
        self.visibility = visibility
        self.shareable = shareable
    }
}

public struct Goal: Codable, Sendable, Identifiable {
    public let id: String
    public let conversationID: String?
    public let objective: String
    public let status: String
    public let statusReason: String?
    public let pauseReason: String?
    public let controllerSpec: String?
    public let controllerSchedule: GoalControllerSchedule?
    public let tokenBudget: Int64?
    public let tokensUsed: Int64?
    public let timeUsedSeconds: Int64?

    enum CodingKeys: String, CodingKey {
        case id
        case conversationID = "conversationId"
        case objective
        case status
        case statusReason
        case pauseReason
        case controllerSpec
        case controllerSchedule
        case tokenBudget
        case tokensUsed
        case timeUsedSeconds
    }
}

public struct GoalControllerSchedule: Codable, Sendable {
    public let mode: String?
    public let preview: String?
    public let wakeAt: String?

    public init(mode: String? = nil, preview: String? = nil, wakeAt: String? = nil) {
        self.mode = mode
        self.preview = preview
        self.wakeAt = wakeAt
    }
}

public struct GoalEnvelope: Codable, Sendable {
    public let goal: Goal?
}

public struct AsyncOperation: Codable, Sendable {
    public let operationID: String
    public let tool: String
    public let statusTool: String?
    public let operationIDArg: String?
    public let sameToolRecall: Bool?
    public let statusArgs: [String: JSONValue]?
    public let executionMode: String?
    public let state: String?
    public let intent: String?
    public let summary: String?
    public let updatedAt: String?

    enum CodingKeys: String, CodingKey {
        case operationID = "operationId"
        case tool
        case statusTool
        case operationIDArg = "operationIdArg"
        case sameToolRecall
        case statusArgs
        case executionMode
        case state
        case intent
        case summary
        case updatedAt
    }
}

public struct ListAsyncOperationsOutput: Codable, Sendable {
    public let ops: [AsyncOperation]

    public init(ops: [AsyncOperation] = []) {
        self.ops = ops
    }
}

public struct CreateGoalInput: Codable, Sendable {
    public let objective: String
    public let tokenBudget: Int64?
    public let controllerSpec: String?

    public init(objective: String, tokenBudget: Int64? = nil, controllerSpec: String? = nil) {
        self.objective = objective
        self.tokenBudget = tokenBudget
        self.controllerSpec = controllerSpec
    }
}

public struct UpdateGoalInput: Codable, Sendable {
    public let objective: String?
    public let status: String?
    public let statusReason: String?
    public let tokenBudget: Int64?

    public init(
        objective: String? = nil,
        status: String? = nil,
        statusReason: String? = nil,
        tokenBudget: Int64? = nil
    ) {
        self.objective = objective
        self.status = status
        self.statusReason = statusReason
        self.tokenBudget = tokenBudget
    }
}

public struct SteerTurnInput: Codable, Sendable {
    public let conversationID: String
    public let turnID: String
    public let content: String
    public let role: String?

    enum CodingKeys: String, CodingKey {
        case conversationID = "conversationId"
        case turnID = "turnId"
        case content
        case role
    }

    public init(
        conversationID: String,
        turnID: String,
        content: String,
        role: String? = nil
    ) {
        self.conversationID = conversationID
        self.turnID = turnID
        self.content = content
        self.role = role
    }
}

public struct SteerTurnOutput: Codable, Sendable {
    public let messageID: String?
    public let turnID: String?
    public let status: String?
    public let canceledTurnID: String?

    enum CodingKeys: String, CodingKey {
        case messageID = "messageId"
        case turnID = "turnId"
        case status
        case canceledTurnID = "canceledTurnId"
    }

    public init(
        messageID: String? = nil,
        turnID: String? = nil,
        status: String? = nil,
        canceledTurnID: String? = nil
    ) {
        self.messageID = messageID
        self.turnID = turnID
        self.status = status
        self.canceledTurnID = canceledTurnID
    }
}

public struct MoveQueuedTurnInput: Codable, Sendable {
    public let conversationID: String
    public let turnID: String
    public let direction: String

    enum CodingKeys: String, CodingKey {
        case conversationID = "conversationId"
        case turnID = "turnId"
        case direction
    }

    public init(conversationID: String, turnID: String, direction: String) {
        self.conversationID = conversationID
        self.turnID = turnID
        self.direction = direction
    }
}

public struct EditQueuedTurnInput: Codable, Sendable {
    public let conversationID: String
    public let turnID: String
    public let content: String

    enum CodingKeys: String, CodingKey {
        case conversationID = "conversationId"
        case turnID = "turnId"
        case content
    }

    public init(conversationID: String, turnID: String, content: String) {
        self.conversationID = conversationID
        self.turnID = turnID
        self.content = content
    }
}

public struct GetMessagesInput: Codable, Sendable {
    public let conversationID: String
    public let turnID: String?
    public let roles: [String]
    public let types: [String]
    public let page: PageInput?

    enum CodingKeys: String, CodingKey {
        case conversationID = "conversationId"
        case turnID = "turnId"
        case roles
        case types
        case page
    }

    public init(
        conversationID: String,
        turnID: String? = nil,
        roles: [String] = [],
        types: [String] = [],
        page: PageInput? = nil
    ) {
        self.conversationID = conversationID
        self.turnID = turnID
        self.roles = roles
        self.types = types
        self.page = page
    }
}

/// Canonical message row. The backend serializes message rows using Go field
/// names (capitalized, e.g. `Id`, `ConversationId`), while the SDK contract uses
/// camelCase. The custom decoder normalizes both shapes by lower-casing the
/// first character of every key so either wire form decodes cleanly.
public struct Message: Decodable, Sendable, Identifiable {
    public let id: String
    public let conversationID: String?
    public let turnID: String?
    public let role: String?
    public let type: String?
    public let content: String?
    public let rawContent: String?
    public let status: String?
    public let interim: Int?
    public let iteration: Int?
    public let narration: String?
    public let phase: String?
    public let mode: String?
    public let sequence: Int?
    public let createdAt: String?
    public let updatedAt: String?
    public let parentMessageID: String?
    public let linkedConversationID: String?
    public let toolName: String?

    public init(from decoder: Decoder) throws {
        let raw = try decoder.singleValueContainer().decode([String: JSONValue].self)
        var norm: [String: JSONValue] = [:]
        for (key, value) in raw {
            if norm[key] == nil { norm[key] = value }
            let lowered = key.prefix(1).lowercased() + key.dropFirst()
            if norm[lowered] == nil { norm[lowered] = value }
        }
        func str(_ key: String) -> String? {
            guard let value = norm[key] else { return nil }
            if case .string(let string) = value {
                return string
            }
            return nil
        }
        func int(_ key: String) -> Int? {
            guard let value = norm[key] else { return nil }
            if case .number(let number) = value {
                return Int(number)
            }
            return nil
        }

        self.id = str("id") ?? ""
        self.conversationID = str("conversationId")
        self.turnID = str("turnId")
        self.role = str("role")
        self.type = str("type")
        self.content = str("content")
        self.rawContent = str("rawContent")
        self.status = str("status")
        self.interim = int("interim")
        self.iteration = int("iteration")
        self.narration = str("narration")
        self.phase = str("phase")
        self.mode = str("mode")
        self.sequence = int("sequence")
        self.createdAt = str("createdAt")
        self.updatedAt = str("updatedAt")
        self.parentMessageID = str("parentMessageId")
        self.linkedConversationID = str("linkedConversationId")
        self.toolName = str("toolName")
    }
}

public struct MessagePage: Decodable, Sendable {
    public let rows: [Message]
    public let nextCursor: String?
    public let prevCursor: String?
    public let hasMore: Bool

    public init(rows: [Message] = [], nextCursor: String? = nil, prevCursor: String? = nil, hasMore: Bool = false) {
        self.rows = rows
        self.nextCursor = nextCursor
        self.prevCursor = prevCursor
        self.hasMore = hasMore
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: DynamicCodingKey.self)
        self.rows =
            (try? container.decodeIfPresent([Message].self, forKey: DynamicCodingKey("rows")) ?? nil) ??
            (try? container.decodeIfPresent([Message].self, forKey: DynamicCodingKey("Rows")) ?? nil) ??
            []
        self.nextCursor =
            (try? container.decodeIfPresent(String.self, forKey: DynamicCodingKey("nextCursor")) ?? nil) ??
            (try? container.decodeIfPresent(String.self, forKey: DynamicCodingKey("NextCursor")) ?? nil)
        self.prevCursor =
            (try? container.decodeIfPresent(String.self, forKey: DynamicCodingKey("prevCursor")) ?? nil) ??
            (try? container.decodeIfPresent(String.self, forKey: DynamicCodingKey("PrevCursor")) ?? nil)
        self.hasMore =
            ((try? container.decodeIfPresent(Bool.self, forKey: DynamicCodingKey("hasMore")) ?? nil) ??
             (try? container.decodeIfPresent(Bool.self, forKey: DynamicCodingKey("HasMore")) ?? nil)) ?? false
    }
}

public struct ListLinkedConversationsInput: Codable, Sendable {
    public let parentConversationID: String?
    public let parentTurnID: String?
    public let page: PageInput?

    enum CodingKeys: String, CodingKey {
        case parentConversationID = "parentConversationId"
        case parentTurnID = "parentTurnId"
        case page
    }

    public init(parentConversationID: String? = nil, parentTurnID: String? = nil, page: PageInput? = nil) {
        self.parentConversationID = parentConversationID
        self.parentTurnID = parentTurnID
        self.page = page
    }
}

public struct LinkedConversationEntry: Codable, Sendable, Identifiable {
    public var id: String { conversationID }
    public let conversationID: String
    public let parentConversationID: String?
    public let parentTurnID: String?
    public let agentID: String?
    public let title: String?
    public let status: String?
    public let response: String?
    public let createdAt: String?
    public let updatedAt: String?

    enum CodingKeys: String, CodingKey {
        case conversationID = "conversationId"
        case parentConversationID = "parentConversationId"
        case parentTurnID = "parentTurnId"
        case agentID = "agentId"
        case title
        case status
        case response
        case createdAt
        case updatedAt
    }
}

public struct LinkedConversationPage: Decodable, Sendable {
    public let rows: [LinkedConversationEntry]
    public let nextCursor: String?
    public let prevCursor: String?
    public let hasMore: Bool

    public init(
        rows: [LinkedConversationEntry] = [],
        nextCursor: String? = nil,
        prevCursor: String? = nil,
        hasMore: Bool = false
    ) {
        self.rows = rows
        self.nextCursor = nextCursor
        self.prevCursor = prevCursor
        self.hasMore = hasMore
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: DynamicCodingKey.self)
        self.rows =
            (try? container.decodeIfPresent([LinkedConversationEntry].self, forKey: DynamicCodingKey("rows")) ?? nil) ??
            (try? container.decodeIfPresent([LinkedConversationEntry].self, forKey: DynamicCodingKey("Rows")) ?? nil) ??
            []
        self.nextCursor =
            (try? container.decodeIfPresent(String.self, forKey: DynamicCodingKey("nextCursor")) ?? nil) ??
            (try? container.decodeIfPresent(String.self, forKey: DynamicCodingKey("NextCursor")) ?? nil)
        self.prevCursor =
            (try? container.decodeIfPresent(String.self, forKey: DynamicCodingKey("prevCursor")) ?? nil) ??
            (try? container.decodeIfPresent(String.self, forKey: DynamicCodingKey("PrevCursor")) ?? nil)
        self.hasMore =
            ((try? container.decodeIfPresent(Bool.self, forKey: DynamicCodingKey("hasMore")) ?? nil) ??
             (try? container.decodeIfPresent(Bool.self, forKey: DynamicCodingKey("HasMore")) ?? nil)) ?? false
    }
}

public struct FeedDataResponse: Codable, Sendable {
    public let feedID: String?
    public let title: String?
    public let data: JSONValue?
    public let dataSources: JSONValue?
    public let ui: JSONValue?

    enum CodingKeys: String, CodingKey {
        case feedID = "feedId"
        case title
        case data
        case dataSources
        case ui
    }

    public init(
        feedID: String? = nil,
        title: String? = nil,
        data: JSONValue? = nil,
        dataSources: JSONValue? = nil,
        ui: JSONValue? = nil
    ) {
        self.feedID = feedID
        self.title = title
        self.data = data
        self.dataSources = dataSources
        self.ui = ui
    }
}

public struct GetPayloadOptions: Sendable {
    public let raw: Bool?
    public let meta: Bool?
    public let inline: Bool?

    public init(raw: Bool? = nil, meta: Bool? = nil, inline: Bool? = nil) {
        self.raw = raw
        self.meta = meta
        self.inline = inline
    }
}

public struct GetPayloadsInput: Codable, Sendable {
    public let ids: [String]

    public init(ids: [String]) {
        self.ids = ids
    }
}

/// Structured payload metadata. The backend serializes the Go `Payload` struct
/// with its capitalized field names (e.g. `Id`, `MimeType`, `URI`), so the
/// CodingKeys map the camelCase Swift properties onto those wire keys.
public struct PayloadView: Codable, Sendable, Identifiable {
    public let id: String
    public let tenantID: String?
    public let kind: String?
    public let subtype: String?
    public let mimeType: String?
    public let sizeBytes: Int64?
    public let digest: String?
    public let storage: String?
    public let inlineBody: String?
    public let uri: String?
    public let compression: String?
    public let encryptionKMSKeyID: String?
    public let redactionPolicyVersion: String?
    public let redacted: Int?
    public let createdAt: String?
    public let schemaRef: String?

    enum CodingKeys: String, CodingKey {
        case id = "Id"
        case tenantID = "TenantID"
        case kind = "Kind"
        case subtype = "Subtype"
        case mimeType = "MimeType"
        case sizeBytes = "SizeBytes"
        case digest = "Digest"
        case storage = "Storage"
        case inlineBody = "InlineBody"
        case uri = "URI"
        case compression = "Compression"
        case encryptionKMSKeyID = "EncryptionKMSKeyID"
        case redactionPolicyVersion = "RedactionPolicyVersion"
        case redacted = "Redacted"
        case createdAt = "CreatedAt"
        case schemaRef = "SchemaRef"
    }

    public init(
        id: String,
        tenantID: String? = nil,
        kind: String? = nil,
        subtype: String? = nil,
        mimeType: String? = nil,
        sizeBytes: Int64? = nil,
        digest: String? = nil,
        storage: String? = nil,
        inlineBody: String? = nil,
        uri: String? = nil,
        compression: String? = nil,
        encryptionKMSKeyID: String? = nil,
        redactionPolicyVersion: String? = nil,
        redacted: Int? = nil,
        createdAt: String? = nil,
        schemaRef: String? = nil
    ) {
        self.id = id
        self.tenantID = tenantID
        self.kind = kind
        self.subtype = subtype
        self.mimeType = mimeType
        self.sizeBytes = sizeBytes
        self.digest = digest
        self.storage = storage
        self.inlineBody = inlineBody
        self.uri = uri
        self.compression = compression
        self.encryptionKMSKeyID = encryptionKMSKeyID
        self.redactionPolicyVersion = redactionPolicyVersion
        self.redacted = redacted
        self.createdAt = createdAt
        self.schemaRef = schemaRef
    }
}

public struct WorkspaceFileEntry: Codable, Sendable, Identifiable {
    public var id: String { uri ?? url ?? name ?? UUID().uuidString }
    public let uri: String?
    public let url: String?
    public let name: String?
    public let isFolder: Bool?
    public let size: Int64?
    public let modifiedAt: String?
    public let childNodes: [WorkspaceFileEntry]

    enum CodingKeys: String, CodingKey {
        case uri
        case url
        case name
        case isFolder = "isDir"
        case size
        case modifiedAt = "modTime"
        case childNodes
    }

    public init(
        uri: String? = nil,
        url: String? = nil,
        name: String? = nil,
        isFolder: Bool? = nil,
        size: Int64? = nil,
        modifiedAt: String? = nil,
        childNodes: [WorkspaceFileEntry] = []
    ) {
        self.uri = uri
        self.url = url
        self.name = name
        self.isFolder = isFolder
        self.size = size
        self.modifiedAt = modifiedAt
        self.childNodes = childNodes
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        self.uri = try container.decodeIfPresent(String.self, forKey: .uri)
        self.url = try container.decodeIfPresent(String.self, forKey: .url)
        self.name = try container.decodeIfPresent(String.self, forKey: .name)
        self.isFolder = try container.decodeIfPresent(Bool.self, forKey: .isFolder)
        self.size = try container.decodeIfPresent(Int64.self, forKey: .size)
        self.modifiedAt = try container.decodeIfPresent(String.self, forKey: .modifiedAt)
        self.childNodes = try container.decodeIfPresent([WorkspaceFileEntry].self, forKey: .childNodes) ?? []
    }
}

public struct ToolDefinitionInfo: Codable, Sendable, Identifiable {
    public var id: String { name }
    public let name: String
    public let description: String?
    public let parameters: JSONValue?
    public let required: [String]
    public let outputSchema: JSONValue?

    enum CodingKeys: String, CodingKey {
        case name
        case description
        case parameters
        case required
        case outputSchema = "output_schema"
    }

    public init(
        name: String,
        description: String? = nil,
        parameters: JSONValue? = nil,
        required: [String] = [],
        outputSchema: JSONValue? = nil
    ) {
        self.name = name
        self.description = description
        self.parameters = parameters
        self.required = required
        self.outputSchema = outputSchema
    }
}

public struct MCPUIToolCallInput: Codable, Sendable {
    public let conversationID: String?
    public let toolName: String
    public let arguments: [String: JSONValue]
    public let assistantText: String?
    public let toolBundles: [String]

    enum CodingKeys: String, CodingKey {
        case conversationID = "conversationId"
        case toolName
        case arguments
        case assistantText
        case toolBundles
    }

    public init(
        conversationID: String? = nil,
        toolName: String,
        arguments: [String: JSONValue] = [:],
        assistantText: String? = nil,
        toolBundles: [String] = []
    ) {
        self.conversationID = conversationID
        self.toolName = toolName
        self.arguments = arguments
        self.assistantText = assistantText
        self.toolBundles = toolBundles
    }
}

public struct MCPUIToolCallOutput: Codable, Sendable {
    public let conversationID: String?
    public let turnID: String?
    public let status: String
    public let result: String?
    public let source: String?
    public let approval: PendingToolApproval?

    enum CodingKeys: String, CodingKey {
        case conversationID = "conversationId"
        case turnID = "turnId"
        case status
        case result
        case source
        case approval
    }

    public init(
        conversationID: String? = nil,
        turnID: String? = nil,
        status: String,
        result: String? = nil,
        source: String? = nil,
        approval: PendingToolApproval? = nil
    ) {
        self.conversationID = conversationID
        self.turnID = turnID
        self.status = status
        self.result = result
        self.source = source
        self.approval = approval
    }
}

public struct AgentCard: Codable, Sendable {
    public let name: String
    public let title: String?
    public let version: String?
    public let description: String?
    public let endpoints: [String: String]
    public let authentication: JSONValue?
    public let capabilities: AgentCapabilities?

    public init(
        name: String,
        title: String? = nil,
        version: String? = nil,
        description: String? = nil,
        endpoints: [String: String] = [:],
        authentication: JSONValue? = nil,
        capabilities: AgentCapabilities? = nil
    ) {
        self.name = name
        self.title = title
        self.version = version
        self.description = description
        self.endpoints = endpoints
        self.authentication = authentication
        self.capabilities = capabilities
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: DynamicCodingKey.self)
        self.name = try container.decode(String.self, forKey: DynamicCodingKey("name"))
        self.title = try container.decodeIfPresent(String.self, forKey: DynamicCodingKey("title"))
        self.version = try container.decodeIfPresent(String.self, forKey: DynamicCodingKey("version"))
        self.description = try container.decodeIfPresent(String.self, forKey: DynamicCodingKey("description"))
        self.endpoints = try container.decodeIfPresent([String: String].self, forKey: DynamicCodingKey("endpoints")) ?? [:]
        self.authentication = try container.decodeIfPresent(JSONValue.self, forKey: DynamicCodingKey("authentication"))
        self.capabilities = try container.decodeIfPresent(AgentCapabilities.self, forKey: DynamicCodingKey("capabilities"))
    }
}

public struct AgentCapabilities: Codable, Sendable {
    public let streaming: Bool?
    public let pushNotifications: Bool?
    public let stateTransitionHistory: Bool?

    public init(
        streaming: Bool? = nil,
        pushNotifications: Bool? = nil,
        stateTransitionHistory: Bool? = nil
    ) {
        self.streaming = streaming
        self.pushNotifications = pushNotifications
        self.stateTransitionHistory = stateTransitionHistory
    }
}

public struct A2APart: Codable, Sendable {
    public let type: String
    public let text: String?
    public let uri: String?
    public let mimeType: String?
    public let data: JSONValue?

    public init(
        type: String,
        text: String? = nil,
        uri: String? = nil,
        mimeType: String? = nil,
        data: JSONValue? = nil
    ) {
        self.type = type
        self.text = text
        self.uri = uri
        self.mimeType = mimeType
        self.data = data
    }
}

public struct A2AMessage: Codable, Sendable {
    public let role: String
    public let parts: [A2APart]

    public init(role: String, parts: [A2APart] = []) {
        self.role = role
        self.parts = parts
    }
}

public struct SendA2AMessageRequest: Codable, Sendable {
    public let messages: [A2AMessage]
    public let message: A2AMessage?
    public let taskID: String?
    public let contextID: String?

    enum CodingKeys: String, CodingKey {
        case messages
        case message
        case taskID = "taskId"
        case contextID = "contextId"
    }

    public init(
        messages: [A2AMessage] = [],
        message: A2AMessage? = nil,
        taskID: String? = nil,
        contextID: String? = nil
    ) {
        self.messages = messages
        self.message = message
        self.taskID = taskID
        self.contextID = contextID
    }
}

public struct A2ATaskStatus: Codable, Sendable {
    public let state: String
    public let message: A2APart?
    public let error: String?
    public let updatedAt: String?

    public init(state: String, message: A2APart? = nil, error: String? = nil, updatedAt: String? = nil) {
        self.state = state
        self.message = message
        self.error = error
        self.updatedAt = updatedAt
    }
}

public struct A2AArtifact: Codable, Sendable, Identifiable {
    public let id: String
    public let createdAt: String?
    public let parts: [A2APart]

    public init(id: String, createdAt: String? = nil, parts: [A2APart] = []) {
        self.id = id
        self.createdAt = createdAt
        self.parts = parts
    }
}

public struct A2ATask: Codable, Sendable, Identifiable {
    public let id: String
    public let contextID: String?
    public let status: A2ATaskStatus
    public let artifacts: [A2AArtifact]

    enum CodingKeys: String, CodingKey {
        case id
        case contextID = "contextId"
        case status
        case artifacts
    }

    public init(id: String, contextID: String? = nil, status: A2ATaskStatus, artifacts: [A2AArtifact] = []) {
        self.id = id
        self.contextID = contextID
        self.status = status
        self.artifacts = artifacts
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        self.id = try container.decode(String.self, forKey: .id)
        self.contextID = try container.decodeIfPresent(String.self, forKey: .contextID)
        self.status = try container.decode(A2ATaskStatus.self, forKey: .status)
        self.artifacts = try container.decodeIfPresent([A2AArtifact].self, forKey: .artifacts) ?? []
    }
}

public struct SendA2AMessageResponse: Codable, Sendable {
    public let task: A2ATask

    public init(task: A2ATask) {
        self.task = task
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
