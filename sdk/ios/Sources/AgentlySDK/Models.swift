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
    public let workspaceVersion: String?
    public let metadataVersion: String?
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

public struct WorkspaceWindowSnapshot: Codable, Sendable, Equatable {
    public let windowId: String
    public let conversationId: String?
    public let windowKey: String
    public let windowTitle: String?
    public let presentation: String?
    public let region: String?
    public let parentKey: String?
    public let inTab: Bool?
    public let parameters: [String: JSONValue]?

    public init(
        windowId: String,
        conversationId: String? = nil,
        windowKey: String,
        windowTitle: String? = nil,
        presentation: String? = nil,
        region: String? = nil,
        parentKey: String? = nil,
        inTab: Bool? = nil,
        parameters: [String: JSONValue]? = nil
    ) {
        self.windowId = windowId
        self.conversationId = conversationId
        self.windowKey = windowKey
        self.windowTitle = windowTitle
        self.presentation = presentation
        self.region = region
        self.parentKey = parentKey
        self.inTab = inTab
        self.parameters = parameters
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

    enum CodingKeys: String, CodingKey {
        case elicitationID = "elicitationId"
        case conversationID = "conversationId"
        case turnID = "turnId"
        case message
        case mode
        case url
        case callbackURL = "callbackUrl"
        case requestedSchema
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
        requestedSchema: JSONValue? = nil
    ) {
        self.elicitationID = elicitationID
        self.conversationID = conversationID
        self.turnID = turnID
        self.message = message
        self.mode = mode
        self.url = url
        self.callbackURL = callbackURL
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

    public init(
        approval: ApprovalMeta? = nil,
        editedFields: [String: JSONValue] = [:],
        originalArgs: [String: JSONValue] = [:]
    ) {
        self.approval = approval
        self.editedFields = editedFields
        self.originalArgs = originalArgs
    }
}

public struct ApprovalCallbackResult: Codable, Sendable {
    public let allow: Bool?
    public let message: String?
    public let payload: [String: JSONValue]

    public init(allow: Bool? = nil, message: String? = nil, payload: [String: JSONValue] = [:]) {
        self.allow = allow
        self.message = message
        self.payload = payload
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
