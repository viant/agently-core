import Foundation

public struct SSEEvent: Codable, Sendable, Equatable {
    public let event: String?
    public let data: String

    public init(event: String? = nil, data: String) {
        self.event = event
        self.data = data
    }
}

public struct PlannedToolCall: Codable, Sendable, Equatable {
    public let toolCallID: String?
    public let toolName: String?

    enum CodingKeys: String, CodingKey {
        case toolCallID = "toolCallId"
        case toolName
    }

    public init(toolCallID: String? = nil, toolName: String? = nil) {
        self.toolCallID = toolCallID
        self.toolName = toolName
    }
}

public struct LiveModelStepState: Codable, Sendable, Equatable, Identifiable {
    public var id: String { modelCallID }
    public let modelCallID: String
    public let assistantMessageID: String?
    public let provider: String?
    public let model: String?
    public let status: String?
    public let errorMessage: String?
    public let requestPayloadID: String?
    public let responsePayloadID: String?
    public let providerRequestPayloadID: String?
    public let providerResponsePayloadID: String?
    public let streamPayloadID: String?

    public init(
        modelCallID: String,
        assistantMessageID: String? = nil,
        provider: String? = nil,
        model: String? = nil,
        status: String? = nil,
        errorMessage: String? = nil,
        requestPayloadID: String? = nil,
        responsePayloadID: String? = nil,
        providerRequestPayloadID: String? = nil,
        providerResponsePayloadID: String? = nil,
        streamPayloadID: String? = nil
    ) {
        self.modelCallID = modelCallID
        self.assistantMessageID = assistantMessageID
        self.provider = provider
        self.model = model
        self.status = status
        self.errorMessage = errorMessage
        self.requestPayloadID = requestPayloadID
        self.responsePayloadID = responsePayloadID
        self.providerRequestPayloadID = providerRequestPayloadID
        self.providerResponsePayloadID = providerResponsePayloadID
        self.streamPayloadID = streamPayloadID
    }
}

public struct LiveToolStepState: Codable, Sendable, Equatable, Identifiable {
    public var id: String {
        toolCallID ?? toolMessageID ?? toolName ?? UUID().uuidString
    }
    public let toolCallID: String?
    public let toolMessageID: String?
    public let toolName: String?
    public let status: String?
    public let errorMessage: String?
    public let requestPayloadID: String?
    public let responsePayloadID: String?
    public let linkedConversationID: String?
    public let linkedConversationAgentID: String?
    public let linkedConversationTitle: String?

    public init(
        toolCallID: String? = nil,
        toolMessageID: String? = nil,
        toolName: String? = nil,
        status: String? = nil,
        errorMessage: String? = nil,
        requestPayloadID: String? = nil,
        responsePayloadID: String? = nil,
        linkedConversationID: String? = nil,
        linkedConversationAgentID: String? = nil,
        linkedConversationTitle: String? = nil
    ) {
        self.toolCallID = toolCallID
        self.toolMessageID = toolMessageID
        self.toolName = toolName
        self.status = status
        self.errorMessage = errorMessage
        self.requestPayloadID = requestPayloadID
        self.responsePayloadID = responsePayloadID
        self.linkedConversationID = linkedConversationID
        self.linkedConversationAgentID = linkedConversationAgentID
        self.linkedConversationTitle = linkedConversationTitle
    }
}

public struct LiveExecutionGroup: Codable, Sendable, Equatable, Identifiable {
    public var id: String { pageID }
    public let pageID: String
    public let assistantMessageID: String
    public let turnID: String?
    public let parentMessageID: String?
    public let sequence: Int?
    public let iteration: Int?
    public let narration: String?
    public let content: String?
    public let errorMessage: String?
    public let status: String?
    public let finalResponse: Bool?
    public let modelSteps: [LiveModelStepState]
    public let toolSteps: [LiveToolStepState]
    public let toolCallsPlanned: [PlannedToolCall]
    public let createdAt: String?
    public let startedAt: String?
    public let completedAt: String?

    public init(
        pageID: String,
        assistantMessageID: String,
        turnID: String? = nil,
        parentMessageID: String? = nil,
        sequence: Int? = nil,
        iteration: Int? = nil,
        narration: String? = nil,
        content: String? = nil,
        errorMessage: String? = nil,
        status: String? = nil,
        finalResponse: Bool? = nil,
        modelSteps: [LiveModelStepState] = [],
        toolSteps: [LiveToolStepState] = [],
        toolCallsPlanned: [PlannedToolCall] = [],
        createdAt: String? = nil,
        startedAt: String? = nil,
        completedAt: String? = nil
    ) {
        self.pageID = pageID
        self.assistantMessageID = assistantMessageID
        self.turnID = turnID
        self.parentMessageID = parentMessageID
        self.sequence = sequence
        self.iteration = iteration
        self.narration = narration
        self.content = content
        self.errorMessage = errorMessage
        self.status = status
        self.finalResponse = finalResponse
        self.modelSteps = modelSteps
        self.toolSteps = toolSteps
        self.toolCallsPlanned = toolCallsPlanned
        self.createdAt = createdAt
        self.startedAt = startedAt
        self.completedAt = completedAt
    }
}

public struct BufferedStreamMessage: Sendable, Equatable, Identifiable {
    public let id: String
    public let conversationID: String?
    public let turnID: String?
    public let role: String
    public let type: String
    public let content: String?
    public let narration: String?
    public let status: String?
    public let interim: Int
    public let createdAt: String?
    public let sequence: Int?
    public let linkedConversationID: String?
    public let toolName: String?

    public init(
        id: String,
        conversationID: String? = nil,
        turnID: String? = nil,
        role: String = "assistant",
        type: String = "text",
        content: String? = nil,
        narration: String? = nil,
        status: String? = nil,
        interim: Int = 1,
        createdAt: String? = nil,
        sequence: Int? = nil,
        linkedConversationID: String? = nil,
        toolName: String? = nil
    ) {
        self.id = id
        self.conversationID = conversationID
        self.turnID = turnID
        self.role = role
        self.type = type
        self.content = content
        self.narration = narration
        self.status = status
        self.interim = interim
        self.createdAt = createdAt
        self.sequence = sequence
        self.linkedConversationID = linkedConversationID
        self.toolName = toolName
    }
}

public struct ConversationStreamSnapshot: Sendable {
    public var conversationID: String?
    public var activeTurnID: String?
    public var feeds: [ActiveFeedState]
    public var pendingElicitation: PendingElicitation?
    public var bufferedMessages: [BufferedStreamMessage]
    public var liveExecutionGroupsByID: [String: LiveExecutionGroup]

    public init(
        conversationID: String? = nil,
        activeTurnID: String? = nil,
        feeds: [ActiveFeedState] = [],
        pendingElicitation: PendingElicitation? = nil,
        bufferedMessages: [BufferedStreamMessage] = [],
        liveExecutionGroupsByID: [String: LiveExecutionGroup] = [:]
    ) {
        self.conversationID = conversationID
        self.activeTurnID = activeTurnID
        self.feeds = feeds
        self.pendingElicitation = pendingElicitation
        self.bufferedMessages = bufferedMessages
        self.liveExecutionGroupsByID = liveExecutionGroupsByID
    }
}

public actor ConversationStreamTracker {
    private var snapshot = ConversationStreamSnapshot()
    private var messagesByID: [String: BufferedStreamMessage] = [:]
    private var feedsByID: [String: ActiveFeedState] = [:]
    private var executionGroupsByID: [String: LiveExecutionGroup] = [:]

    public init() {}

    public func apply(_ event: SSEEvent) -> ConversationStreamSnapshot {
        guard let payload = decodePayload(from: event.data) else {
            return snapshot
        }

        if let activeConversationID = snapshot.conversationID?.trimmedNonEmpty,
           let eventConversationID = payload.conversationID?.trimmedNonEmpty,
           eventConversationID != activeConversationID {
            return snapshot
        }

        if let conversationID = payload.conversationID?.trimmedNonEmpty {
            snapshot.conversationID = conversationID
        }

        executionGroupsByID = applyExecutionEvent(payload, to: executionGroupsByID)
        applyFeedEvent(payload)
        applyElicitationEvent(payload)
        applyMessageEvent(payload)

        snapshot.feeds = sortedFeeds()
        snapshot.liveExecutionGroupsByID = executionGroupsByID
        snapshot.bufferedMessages = sortedBufferedMessages()
        return snapshot
    }

    public func currentSnapshot() -> ConversationStreamSnapshot {
        snapshot
    }

    public func hydrate(_ response: ConversationStateResponse) {
        if let conversationID = response.conversation?.conversationID.trimmedNonEmpty {
            snapshot.conversationID = conversationID
        }
        messagesByID = reconcileMessages(from: response.conversation?.turns ?? [])
        feedsByID.removeAll()
        for feed in response.feeds {
            if let feedID = feed.feedID?.trimmedNonEmpty {
                feedsByID[feedID] = feed
            }
        }
        snapshot.feeds = sortedFeeds()
        snapshot.bufferedMessages = sortedBufferedMessages()
    }

    public func reset(conversationID: String? = nil) {
        snapshot = ConversationStreamSnapshot(conversationID: conversationID)
        messagesByID.removeAll()
        feedsByID.removeAll()
        executionGroupsByID.removeAll()
    }
}

private extension ConversationStreamTracker {
    struct EventModel: Decodable {
        let provider: String?
        let model: String?
        let kind: String?
    }

    struct StreamPayload: Decodable {
        let id: String?
        let streamID: String?
        let conversationID: String?
        let turnID: String?
        let messageID: String?
        let eventSeq: Int?
        let mode: String?
        let agentIDUsed: String?
        let assistantMessageID: String?
        let parentMessageID: String?
        let requestID: String?
        let responseID: String?
        let toolCallID: String?
        let toolMessageID: String?
        let requestPayloadID: String?
        let responsePayloadID: String?
        let providerRequestPayloadID: String?
        let providerResponsePayloadID: String?
        let streamPayloadID: String?
        let linkedConversationID: String?
        let linkedConversationAgentID: String?
        let linkedConversationTitle: String?
        let type: String
        let op: String?
        let patch: [String: JSONValue]?
        let content: String?
        let narration: String?
        let toolName: String?
        let error: String?
        let status: String?
        let iteration: Int?
        let pageIndex: Int?
        let pageCount: Int?
        let latestPage: Bool?
        let finalResponse: Bool?
        let model: EventModel?
        let toolCallsPlanned: [PlannedToolCall]?
        let createdAt: String?
        let completedAt: String?
        let startedAt: String?
        let userMessageID: String?
        let modelCallID: String?
        let provider: String?
        let modelName: String?
        let elicitationID: String?
        let elicitationData: ElicitationData?
        let callbackURL: String?
        let feedID: String?
        let feedTitle: String?
        let feedItemCount: Int?
        let feedData: JSONValue?

        enum CodingKeys: String, CodingKey {
            case id
            case streamID = "streamId"
            case conversationID = "conversationId"
            case turnID = "turnId"
            case messageID = "messageId"
            case eventSeq
            case mode
            case agentIDUsed = "agentIdUsed"
            case assistantMessageID = "assistantMessageId"
            case parentMessageID = "parentMessageId"
            case requestID = "requestId"
            case responseID = "responseId"
            case toolCallID = "toolCallId"
            case toolMessageID = "toolMessageId"
            case requestPayloadID = "requestPayloadId"
            case responsePayloadID = "responsePayloadId"
            case providerRequestPayloadID = "providerRequestPayloadId"
            case providerResponsePayloadID = "providerResponsePayloadId"
            case streamPayloadID = "streamPayloadId"
            case linkedConversationID = "linkedConversationId"
            case linkedConversationAgentID = "linkedConversationAgentId"
            case linkedConversationTitle = "linkedConversationTitle"
            case type
            case op
            case patch
            case content
            case narration
            case toolName
            case error
            case status
            case iteration
            case pageIndex
            case pageCount
            case latestPage
            case finalResponse
            case model
            case toolCallsPlanned
            case createdAt
            case completedAt
            case startedAt
            case userMessageID = "userMessageId"
            case modelCallID = "modelCallId"
            case provider
            case modelName
            case elicitationID = "elicitationId"
            case elicitationData
            case callbackURL = "callbackUrl"
            case feedID = "feedId"
            case feedTitle
            case feedItemCount
            case feedData
        }

        var resolvedMessageID: String? {
            messageID?.trimmedNonEmpty ??
                assistantMessageID?.trimmedNonEmpty ??
                id?.trimmedNonEmpty
        }
    }

    struct ElicitationData: Decodable {
        let mode: String?
        let url: String?
        let requestedSchema: JSONValue?
        let schema: JSONValue?

        var resolvedRequestedSchema: JSONValue? {
            requestedSchema ?? schema
        }
    }

    func decodePayload(from rawData: String) -> StreamPayload? {
        guard let data = rawData.data(using: .utf8) else {
            return nil
        }
        return try? JSONDecoder.agently().decode(StreamPayload.self, from: data)
    }

    func applyFeedEvent(_ payload: StreamPayload) {
        switch payload.type.lowercased() {
        case "tool_feed_active":
            guard let feedID = payload.feedID?.trimmedNonEmpty else { return }
            feedsByID[feedID] = ActiveFeedState(
                feedID: feedID,
                name: payload.feedTitle ?? feedID,
                title: payload.feedTitle ?? feedID,
                itemCount: payload.feedItemCount ?? 0,
                conversationID: payload.conversationID?.trimmedNonEmpty,
                turnID: payload.turnID?.trimmedNonEmpty,
                updatedAt: Int64(Date().timeIntervalSince1970 * 1000),
                data: payload.feedData
            )
        case "tool_feed_inactive":
            guard let feedID = payload.feedID?.trimmedNonEmpty else { return }
            feedsByID.removeValue(forKey: feedID)
        default:
            break
        }
    }

    func applyElicitationEvent(_ payload: StreamPayload) {
        switch payload.type.lowercased() {
        case "elicitation_requested":
            guard let elicitationID = payload.elicitationID?.trimmedNonEmpty else { return }
            snapshot.pendingElicitation = PendingElicitation(
                elicitationID: elicitationID,
                conversationID: payload.conversationID,
                turnID: payload.turnID,
                message: payload.content,
                mode: payload.elicitationData?.mode ?? payload.mode,
                url: payload.elicitationData?.url ?? payload.callbackURL,
                callbackURL: payload.callbackURL,
                requestedSchema: payload.elicitationData?.resolvedRequestedSchema
            )
        case "elicitation_resolved":
            snapshot.pendingElicitation = nil
        default:
            break
        }
    }

    func applyMessageEvent(_ payload: StreamPayload) {
        let type = payload.type.lowercased()
        let conversationID = payload.conversationID?.trimmedNonEmpty
        let turnID = payload.turnID?.trimmedNonEmpty

        if type == "turn_started", let turnID {
            snapshot.activeTurnID = turnID
        }
        if ["turn_completed", "turn_failed", "turn_canceled", "error"].contains(type) {
            snapshot.activeTurnID = nil
            if let turnID {
                markTurnTerminal(turnID: turnID, status: terminalStatus(for: type))
            }
        }

        guard let messageID = payload.resolvedMessageID?.trimmedNonEmpty else { return }
        let existing = ensureMessageEntry(
            id: messageID,
            payload: payload,
            conversationID: conversationID,
            turnID: turnID
        )

        switch type {
        case "text_delta":
            messagesByID[messageID] = existing.with(
                content: (existing.content ?? "") + (payload.content ?? ""),
                status: payload.status ?? existing.status ?? "running"
            )
        case "reasoning_delta":
            messagesByID[messageID] = existing.with(
                narration: (existing.narration ?? "") + (payload.content ?? "")
            )
        case "narration":
            messagesByID[messageID] = existing.with(
                narration: payload.content ?? payload.narration ?? existing.narration,
                status: payload.status ?? existing.status ?? "running"
            )
        case "assistant", "message_appended":
            let patched = applyPatch(existing: existing, patch: payload.patch)
            let role = (payload.patch?["role"]?.stringValue ?? "").lowercased()
            guard role == "assistant" || role == "user" else { break }
            messagesByID[messageID] = patched.with(
                role: role,
                type: payload.patch?["type"]?.stringValue ?? patched.type,
                content: payload.content ?? payload.patch?["content"]?.stringValue ?? patched.content,
                narration: payload.narration ?? payload.patch?["narration"]?.stringValue ?? patched.narration,
                status: payload.status ?? payload.patch?["status"]?.stringValue ?? patched.status ?? "completed",
                interim: 0,
                sequence: payload.patch?["sequence"]?.intValue ?? payload.eventSeq ?? patched.sequence
            )
        case "model_started":
            messagesByID[messageID] = existing.with(status: payload.status ?? existing.status ?? "running")
        case "model_completed":
            messagesByID[messageID] = existing.with(status: payload.status ?? existing.status ?? "completed")
        case "control" where payload.op == "message_patch":
            messagesByID[messageID] = applyPatch(existing: existing, patch: payload.patch)
        case "turn_completed", "turn_failed", "turn_canceled":
            messagesByID[messageID] = existing.with(status: terminalStatus(for: type), interim: 0)
        default:
            break
        }
    }

    func applyPatch(existing: BufferedStreamMessage, patch: [String: JSONValue]?) -> BufferedStreamMessage {
        guard let patch else { return existing }
        return existing.with(
            role: patch["role"]?.stringValue ?? existing.role,
            type: patch["type"]?.stringValue ?? existing.type,
            content: patch["content"]?.stringValue ?? existing.content,
            narration: patch["narration"]?.stringValue ?? existing.narration,
            status: patch["status"]?.stringValue ?? existing.status,
            interim: patch["interim"]?.intValue ?? existing.interim,
            linkedConversationID: patch["linkedConversationId"]?.stringValue ?? existing.linkedConversationID,
            toolName: patch["toolName"]?.stringValue ?? existing.toolName
        )
    }

    func ensureMessageEntry(
        id: String,
        payload: StreamPayload,
        conversationID: String?,
        turnID: String?
    ) -> BufferedStreamMessage {
        if let existing = messagesByID[id] {
            return existing.with(
                conversationID: existing.conversationID ?? conversationID,
                turnID: existing.turnID ?? turnID,
                createdAt: existing.createdAt ?? payload.createdAt,
                sequence: max(existing.sequence ?? 0, payload.eventSeq ?? 0)
            )
        }
        return BufferedStreamMessage(
            id: id,
            conversationID: conversationID,
            turnID: turnID,
            role: "assistant",
            type: "text",
            content: "",
            narration: nil,
            status: nil,
            interim: 1,
            createdAt: payload.createdAt,
            sequence: payload.eventSeq,
            linkedConversationID: payload.linkedConversationID,
            toolName: payload.toolName
        )
    }

    func markTurnTerminal(turnID: String, status: String) {
        for (id, message) in messagesByID where message.turnID == turnID {
            messagesByID[id] = message.with(status: status, interim: 0)
        }
    }

    func reconcileMessages(from turns: [ConversationTurn]) -> [String: BufferedStreamMessage] {
        var merged: [String: BufferedStreamMessage] = [:]
        for turn in turns {
            if let narration = turn.assistant?.narration, let messageID = narration.messageID.trimmedNonEmpty {
                merged[messageID] = BufferedStreamMessage(
                    id: messageID,
                    conversationID: snapshot.conversationID,
                    turnID: turn.id,
                    role: "assistant",
                    type: "text",
                    content: nil,
                    narration: narration.content,
                    status: "running",
                    interim: 1,
                    createdAt: turn.createdAt
                )
            }
            if let final = turn.assistant?.final, let messageID = final.messageID.trimmedNonEmpty {
                merged[messageID] = BufferedStreamMessage(
                    id: messageID,
                    conversationID: snapshot.conversationID,
                    turnID: turn.id,
                    role: "assistant",
                    type: "text",
                    content: final.content,
                    narration: turn.assistant?.narration?.content,
                    status: "completed",
                    interim: 0,
                    createdAt: turn.createdAt
                )
            }
        }
        return merged
    }

    func sortedBufferedMessages() -> [BufferedStreamMessage] {
        messagesByID.values.sorted { lhs, rhs in
            if lhs.sequence != rhs.sequence {
                return (lhs.sequence ?? 0) < (rhs.sequence ?? 0)
            }
            if lhs.createdAt != rhs.createdAt {
                return (lhs.createdAt ?? "") < (rhs.createdAt ?? "")
            }
            return lhs.id < rhs.id
        }
    }

    func sortedFeeds() -> [ActiveFeedState] {
        feedsByID.values.sorted { ($0.feedID ?? "") < ($1.feedID ?? "") }
    }

    func applyExecutionEvent(
        _ payload: StreamPayload,
        to groups: [String: LiveExecutionGroup]
    ) -> [String: LiveExecutionGroup] {
        var next = groups
        let type = payload.type.lowercased()
        let assistantMessageID = payload.assistantMessageID?.trimmedNonEmpty ?? payload.id?.trimmedNonEmpty ?? ""

        if type == "model_started", !assistantMessageID.isEmpty {
            next[assistantMessageID] = mergeExecutionGroup(
                next[assistantMessageID],
                incoming: createExecutionGroup(payload, assistantMessageID: assistantMessageID)
            )
            return next
        }
        if (type == "narration" || type == "reasoning_delta"), !assistantMessageID.isEmpty {
            guard let current = ensureExecutionGroup(next, assistantMessageID: assistantMessageID, payload: payload) else {
                return next
            }
            let narration = type == "reasoning_delta"
                ? (current.narration ?? "") + (payload.content ?? "")
                : (payload.content ?? payload.narration ?? current.narration)
            let updated = current.copy(
                turnID: payload.turnID ?? current.turnID,
                sequence: payload.eventSeq ?? current.sequence,
                iteration: payload.iteration ?? current.iteration,
                narration: narration,
                status: payload.status ?? current.status ?? "running"
            )
            next[assistantMessageID] = mergePrimaryModelStep(current: updated, payload: payload, fallbackStatus: updated.status ?? "running")
            return next
        }
        if (type == "text_delta" || type == "model_completed"), !assistantMessageID.isEmpty {
            guard let current = ensureExecutionGroup(next, assistantMessageID: assistantMessageID, payload: payload) else {
                return next
            }
            let content = type == "text_delta"
                ? (current.content ?? "") + (payload.content ?? "")
                : (payload.content ?? current.content)
            let updated = current.copy(
                turnID: payload.turnID ?? current.turnID,
                sequence: payload.eventSeq ?? current.sequence,
                iteration: payload.iteration ?? current.iteration,
                narration: payload.narration ?? current.narration,
                content: content,
                errorMessage: payload.error ?? current.errorMessage,
                status: payload.status ?? current.status ?? (type == "model_completed" ? "completed" : "running"),
                finalResponse: payload.finalResponse ?? current.finalResponse
            )
            next[assistantMessageID] = mergePrimaryModelStep(current: updated, payload: payload, fallbackStatus: current.status ?? "running")
            return next
        }
        if ["tool_call_started", "tool_call_completed", "linked_conversation_attached"].contains(type), !assistantMessageID.isEmpty {
            guard let current = ensureExecutionGroup(next, assistantMessageID: assistantMessageID, payload: payload) else {
                return next
            }
            next[assistantMessageID] = upsertToolStep(current: current, payload: payload)
            return next
        }
        if ["turn_completed", "turn_failed", "turn_canceled"].contains(type) {
            let terminalStatus = payload.status ?? terminalStatus(for: type)
            if !assistantMessageID.isEmpty, let current = next[assistantMessageID] {
                next[assistantMessageID] = applyTerminalState(current: current, status: terminalStatus, error: payload.error)
                return next
            }
            if let turnID = payload.turnID?.trimmedNonEmpty {
                next = next.mapValues { group in
                    group.turnID == turnID ? applyTerminalState(current: group, status: terminalStatus, error: payload.error) : group
                }
            }
        }
        return next
    }

    func createExecutionGroup(_ payload: StreamPayload, assistantMessageID: String) -> LiveExecutionGroup {
        let modelStep: [LiveModelStepState]
        if payload.model != nil || payload.modelCallID?.trimmedNonEmpty != nil {
            modelStep = [
                LiveModelStepState(
                    modelCallID: payload.modelCallID?.trimmedNonEmpty ?? assistantMessageID,
                    assistantMessageID: payload.assistantMessageID?.trimmedNonEmpty,
                    provider: payload.model?.provider ?? payload.provider,
                    model: payload.model?.model ?? payload.modelName,
                    status: payload.status ?? "running",
                    errorMessage: payload.error,
                    requestPayloadID: payload.requestPayloadID,
                    responsePayloadID: payload.responsePayloadID,
                    providerRequestPayloadID: payload.providerRequestPayloadID,
                    providerResponsePayloadID: payload.providerResponsePayloadID,
                    streamPayloadID: payload.streamPayloadID
                )
            ]
        } else {
            modelStep = []
        }
        return LiveExecutionGroup(
            pageID: assistantMessageID,
            assistantMessageID: assistantMessageID,
            turnID: payload.turnID?.trimmedNonEmpty,
            parentMessageID: payload.parentMessageID?.trimmedNonEmpty,
            sequence: payload.eventSeq,
            iteration: payload.iteration,
            narration: payload.narration?.trimmedNonEmpty,
            content: payload.content?.trimmedNonEmpty,
            errorMessage: payload.error?.trimmedNonEmpty,
            status: payload.status ?? "running",
            finalResponse: payload.finalResponse ?? false,
            modelSteps: modelStep,
            toolSteps: [],
            toolCallsPlanned: payload.toolCallsPlanned ?? [],
            createdAt: payload.createdAt,
            startedAt: payload.startedAt,
            completedAt: payload.completedAt
        )
    }

    func ensureExecutionGroup(
        _ groups: [String: LiveExecutionGroup],
        assistantMessageID: String,
        payload: StreamPayload
    ) -> LiveExecutionGroup? {
        groups[assistantMessageID] ?? createExecutionGroup(payload, assistantMessageID: assistantMessageID)
    }

    func mergePrimaryModelStep(current: LiveExecutionGroup, payload: StreamPayload, fallbackStatus: String) -> LiveExecutionGroup {
        let existing = current.modelSteps.first
        let step = LiveModelStepState(
            modelCallID: payload.modelCallID?.trimmedNonEmpty ?? existing?.modelCallID ?? current.assistantMessageID,
            assistantMessageID: payload.assistantMessageID?.trimmedNonEmpty ?? existing?.assistantMessageID,
            provider: payload.model?.provider ?? payload.provider ?? existing?.provider,
            model: payload.model?.model ?? payload.modelName ?? existing?.model,
            status: payload.status ?? existing?.status ?? fallbackStatus,
            errorMessage: payload.error ?? existing?.errorMessage,
            requestPayloadID: payload.requestPayloadID ?? existing?.requestPayloadID,
            responsePayloadID: payload.responsePayloadID ?? existing?.responsePayloadID,
            providerRequestPayloadID: payload.providerRequestPayloadID ?? existing?.providerRequestPayloadID,
            providerResponsePayloadID: payload.providerResponsePayloadID ?? existing?.providerResponsePayloadID,
            streamPayloadID: payload.streamPayloadID ?? existing?.streamPayloadID
        )
        return current.copy(modelSteps: [step])
    }

    func upsertToolStep(current: LiveExecutionGroup, payload: StreamPayload) -> LiveExecutionGroup {
        let key = firstNonEmpty(
            payload.toolCallID,
            payload.toolMessageID,
            payload.id,
            payload.toolName
        )
        guard let key else { return current }
        var toolSteps = current.toolSteps
        let newStep = LiveToolStepState(
            toolCallID: payload.toolCallID,
            toolMessageID: payload.toolMessageID ?? payload.id,
            toolName: payload.toolName,
            status: payload.status,
            errorMessage: payload.error,
            requestPayloadID: payload.requestPayloadID,
            responsePayloadID: payload.responsePayloadID,
            linkedConversationID: payload.linkedConversationID,
            linkedConversationAgentID: payload.linkedConversationAgentID,
            linkedConversationTitle: payload.linkedConversationTitle
        )
        if let index = toolSteps.firstIndex(where: { firstNonEmpty($0.toolCallID, $0.toolMessageID, $0.toolName) == key }) {
            let prior = toolSteps[index]
            toolSteps[index] = LiveToolStepState(
                toolCallID: newStep.toolCallID ?? prior.toolCallID,
                toolMessageID: newStep.toolMessageID ?? prior.toolMessageID,
                toolName: newStep.toolName ?? prior.toolName,
                status: newStep.status ?? prior.status,
                errorMessage: newStep.errorMessage ?? prior.errorMessage,
                requestPayloadID: newStep.requestPayloadID ?? prior.requestPayloadID,
                responsePayloadID: newStep.responsePayloadID ?? prior.responsePayloadID,
                linkedConversationID: newStep.linkedConversationID ?? prior.linkedConversationID,
                linkedConversationAgentID: newStep.linkedConversationAgentID ?? prior.linkedConversationAgentID,
                linkedConversationTitle: newStep.linkedConversationTitle ?? prior.linkedConversationTitle
            )
        } else {
            toolSteps.append(newStep)
        }
        return current.copy(toolSteps: toolSteps)
    }

    func mergeExecutionGroup(_ existing: LiveExecutionGroup?, incoming: LiveExecutionGroup) -> LiveExecutionGroup {
        guard let existing else { return incoming }
        var mergedTools: [String: LiveToolStepState] = [:]
        for step in existing.toolSteps + incoming.toolSteps {
            if let key = firstNonEmpty(step.toolCallID, step.toolMessageID, step.toolName) {
                if let prior = mergedTools[key] {
                    mergedTools[key] = LiveToolStepState(
                        toolCallID: step.toolCallID ?? prior.toolCallID,
                        toolMessageID: step.toolMessageID ?? prior.toolMessageID,
                        toolName: step.toolName ?? prior.toolName,
                        status: step.status ?? prior.status,
                        errorMessage: step.errorMessage ?? prior.errorMessage,
                        requestPayloadID: step.requestPayloadID ?? prior.requestPayloadID,
                        responsePayloadID: step.responsePayloadID ?? prior.responsePayloadID,
                        linkedConversationID: step.linkedConversationID ?? prior.linkedConversationID,
                        linkedConversationAgentID: step.linkedConversationAgentID ?? prior.linkedConversationAgentID,
                        linkedConversationTitle: step.linkedConversationTitle ?? prior.linkedConversationTitle
                    )
                } else {
                    mergedTools[key] = step
                }
            }
        }
        return existing.copy(
            turnID: incoming.turnID ?? existing.turnID,
            parentMessageID: incoming.parentMessageID ?? existing.parentMessageID,
            sequence: incoming.sequence ?? existing.sequence,
            iteration: incoming.iteration ?? existing.iteration,
            narration: incoming.narration ?? existing.narration,
            content: incoming.content ?? existing.content,
            errorMessage: incoming.errorMessage ?? existing.errorMessage,
            status: incoming.status ?? existing.status,
            finalResponse: incoming.finalResponse ?? existing.finalResponse,
            modelSteps: incoming.modelSteps.isEmpty ? existing.modelSteps : incoming.modelSteps,
            toolSteps: Array(mergedTools.values),
            toolCallsPlanned: incoming.toolCallsPlanned.isEmpty ? existing.toolCallsPlanned : incoming.toolCallsPlanned,
            createdAt: incoming.createdAt ?? existing.createdAt,
            startedAt: incoming.startedAt ?? existing.startedAt,
            completedAt: incoming.completedAt ?? existing.completedAt
        )
    }

    func applyTerminalState(current: LiveExecutionGroup, status: String, error: String?) -> LiveExecutionGroup {
        current.copy(
            errorMessage: error ?? current.errorMessage,
            status: status,
            modelSteps: current.modelSteps.map {
                LiveModelStepState(
                    modelCallID: $0.modelCallID,
                    assistantMessageID: $0.assistantMessageID,
                    provider: $0.provider,
                    model: $0.model,
                    status: status,
                    errorMessage: error ?? $0.errorMessage,
                    requestPayloadID: $0.requestPayloadID,
                    responsePayloadID: $0.responsePayloadID,
                    providerRequestPayloadID: $0.providerRequestPayloadID,
                    providerResponsePayloadID: $0.providerResponsePayloadID,
                    streamPayloadID: $0.streamPayloadID
                )
            },
            toolSteps: current.toolSteps.map {
                LiveToolStepState(
                    toolCallID: $0.toolCallID,
                    toolMessageID: $0.toolMessageID,
                    toolName: $0.toolName,
                    status: status,
                    errorMessage: error ?? $0.errorMessage,
                    requestPayloadID: $0.requestPayloadID,
                    responsePayloadID: $0.responsePayloadID,
                    linkedConversationID: $0.linkedConversationID,
                    linkedConversationAgentID: $0.linkedConversationAgentID,
                    linkedConversationTitle: $0.linkedConversationTitle
                )
            }
        )
    }

    func terminalStatus(for type: String) -> String {
        switch type {
        case "turn_failed":
            return "failed"
        case "turn_canceled":
            return "canceled"
        default:
            return "completed"
        }
    }
}

private extension BufferedStreamMessage {
    func with(
        conversationID: String? = nil,
        turnID: String? = nil,
        role: String? = nil,
        type: String? = nil,
        content: String? = nil,
        narration: String? = nil,
        status: String? = nil,
        interim: Int? = nil,
        createdAt: String? = nil,
        sequence: Int? = nil,
        linkedConversationID: String? = nil,
        toolName: String? = nil
    ) -> BufferedStreamMessage {
        BufferedStreamMessage(
            id: id,
            conversationID: conversationID ?? self.conversationID,
            turnID: turnID ?? self.turnID,
            role: role ?? self.role,
            type: type ?? self.type,
            content: content ?? self.content,
            narration: narration ?? self.narration,
            status: status ?? self.status,
            interim: interim ?? self.interim,
            createdAt: createdAt ?? self.createdAt,
            sequence: sequence ?? self.sequence,
            linkedConversationID: linkedConversationID ?? self.linkedConversationID,
            toolName: toolName ?? self.toolName
        )
    }
}

private extension LiveExecutionGroup {
    func copy(
        turnID: String? = nil,
        parentMessageID: String? = nil,
        sequence: Int? = nil,
        iteration: Int? = nil,
        narration: String? = nil,
        content: String? = nil,
        errorMessage: String? = nil,
        status: String? = nil,
        finalResponse: Bool? = nil,
        modelSteps: [LiveModelStepState]? = nil,
        toolSteps: [LiveToolStepState]? = nil,
        toolCallsPlanned: [PlannedToolCall]? = nil,
        createdAt: String? = nil,
        startedAt: String? = nil,
        completedAt: String? = nil
    ) -> LiveExecutionGroup {
        LiveExecutionGroup(
            pageID: pageID,
            assistantMessageID: assistantMessageID,
            turnID: turnID ?? self.turnID,
            parentMessageID: parentMessageID ?? self.parentMessageID,
            sequence: sequence ?? self.sequence,
            iteration: iteration ?? self.iteration,
            narration: narration ?? self.narration,
            content: content ?? self.content,
            errorMessage: errorMessage ?? self.errorMessage,
            status: status ?? self.status,
            finalResponse: finalResponse ?? self.finalResponse,
            modelSteps: modelSteps ?? self.modelSteps,
            toolSteps: toolSteps ?? self.toolSteps,
            toolCallsPlanned: toolCallsPlanned ?? self.toolCallsPlanned,
            createdAt: createdAt ?? self.createdAt,
            startedAt: startedAt ?? self.startedAt,
            completedAt: completedAt ?? self.completedAt
        )
    }
}

private extension JSONValue {
    var stringValue: String? {
        switch self {
        case .string(let value):
            return value
        default:
            return nil
        }
    }

    var intValue: Int? {
        switch self {
        case .number(let value):
            return Int(value)
        default:
            return nil
        }
    }
}

private func firstNonEmpty(_ values: String?...) -> String? {
    values.first { ($0?.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty == false) }
        ?? nil
}

private extension String {
    var trimmedNonEmpty: String? {
        let trimmed = trimmingCharacters(in: .whitespacesAndNewlines)
        return trimmed.isEmpty ? nil : trimmed
    }
}
