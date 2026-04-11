import Foundation

public struct SSEEvent: Codable, Sendable, Equatable {
    public let event: String?
    public let data: String

    public init(event: String? = nil, data: String) {
        self.event = event
        self.data = data
    }
}

public struct BufferedStreamMessage: Sendable, Equatable, Identifiable {
    public let id: String
    public let conversationID: String?
    public let turnID: String?
    public let content: String?
    public let preamble: String?
    public let status: String?

    public init(
        id: String,
        conversationID: String? = nil,
        turnID: String? = nil,
        content: String? = nil,
        preamble: String? = nil,
        status: String? = nil
    ) {
        self.id = id
        self.conversationID = conversationID
        self.turnID = turnID
        self.content = content
        self.preamble = preamble
        self.status = status
    }
}

public struct ConversationStreamSnapshot: Sendable {
    public var conversationID: String?
    public var activeTurnID: String?
    public var pendingElicitation: PendingElicitation?
    public var bufferedMessages: [BufferedStreamMessage]

    public init(
        conversationID: String? = nil,
        activeTurnID: String? = nil,
        pendingElicitation: PendingElicitation? = nil,
        bufferedMessages: [BufferedStreamMessage] = []
    ) {
        self.conversationID = conversationID
        self.activeTurnID = activeTurnID
        self.pendingElicitation = pendingElicitation
        self.bufferedMessages = bufferedMessages
    }
}

public actor ConversationStreamTracker {
    private var snapshot = ConversationStreamSnapshot()
    private var messagesByID: [String: BufferedStreamMessage] = [:]

    public init() {}

    public func apply(_ event: SSEEvent) -> ConversationStreamSnapshot {
        guard let payload = decodePayload(from: event.data) else {
            return snapshot
        }

        if let conversationID = payload.conversationID?.trimmedNonEmpty {
            snapshot.conversationID = conversationID
        }

        let type = payload.type.lowercased()
        if type == "turn_started", let turnID = payload.turnID?.trimmedNonEmpty {
            snapshot.activeTurnID = turnID
        }
        if ["turn_completed", "turn_failed", "turn_canceled", "error"].contains(type) {
            snapshot.activeTurnID = nil
        }

        if type == "elicitation_requested",
           let elicitationID = payload.elicitationID?.trimmedNonEmpty {
            let elicitationData = payload.elicitationData
            snapshot.pendingElicitation = PendingElicitation(
                elicitationID: elicitationID,
                conversationID: payload.conversationID,
                message: payload.content,
                mode: elicitationData?.mode ?? payload.mode,
                url: elicitationData?.url ?? payload.callbackURL,
                requestedSchema: elicitationData?.resolvedRequestedSchema
            )
        } else if type == "elicitation_resolved" {
            snapshot.pendingElicitation = nil
        }

        guard let messageID = payload.resolvedMessageID else {
            snapshot.bufferedMessages = sortedBufferedMessages()
            return snapshot
        }

        let existing = messagesByID[messageID] ?? BufferedStreamMessage(
            id: messageID,
            conversationID: payload.conversationID,
            turnID: payload.turnID
        )

        switch type {
        case "text_delta":
            messagesByID[messageID] = BufferedStreamMessage(
                id: messageID,
                conversationID: payload.conversationID ?? existing.conversationID,
                turnID: payload.turnID ?? existing.turnID,
                content: (existing.content ?? "") + (payload.content ?? ""),
                preamble: existing.preamble,
                status: payload.status ?? existing.status ?? "running"
            )
        case "assistant_preamble", "reasoning_delta":
            messagesByID[messageID] = BufferedStreamMessage(
                id: messageID,
                conversationID: payload.conversationID ?? existing.conversationID,
                turnID: payload.turnID ?? existing.turnID,
                content: existing.content,
                preamble: (existing.preamble ?? "") + (payload.content ?? payload.preamble ?? ""),
                status: payload.status ?? existing.status ?? "running"
            )
        case "assistant_final":
            messagesByID[messageID] = BufferedStreamMessage(
                id: messageID,
                conversationID: payload.conversationID ?? existing.conversationID,
                turnID: payload.turnID ?? existing.turnID,
                content: payload.content ?? existing.content,
                preamble: payload.preamble ?? existing.preamble,
                status: payload.status ?? existing.status ?? "completed"
            )
        case "model_started", "model_completed":
            messagesByID[messageID] = BufferedStreamMessage(
                id: messageID,
                conversationID: payload.conversationID ?? existing.conversationID,
                turnID: payload.turnID ?? existing.turnID,
                content: existing.content,
                preamble: existing.preamble,
                status: payload.status ?? existing.status ?? (type == "model_completed" ? "completed" : "running")
            )
        case "turn_completed", "turn_failed", "turn_canceled":
            messagesByID[messageID] = BufferedStreamMessage(
                id: messageID,
                conversationID: payload.conversationID ?? existing.conversationID,
                turnID: payload.turnID ?? existing.turnID,
                content: existing.content,
                preamble: existing.preamble,
                status: terminalStatus(for: type)
            )
        default:
            break
        }

        snapshot.bufferedMessages = sortedBufferedMessages()
        return snapshot
    }

    public func currentSnapshot() -> ConversationStreamSnapshot {
        snapshot
    }

    public func reset(conversationID: String? = nil) {
        snapshot = ConversationStreamSnapshot(conversationID: conversationID)
        messagesByID.removeAll()
    }

    private func sortedBufferedMessages() -> [BufferedStreamMessage] {
        messagesByID.values.sorted { $0.id < $1.id }
    }
}

private extension ConversationStreamTracker {
    struct StreamPayload: Decodable {
        let id: String?
        let type: String
        let conversationID: String?
        let turnID: String?
        let messageID: String?
        let assistantMessageID: String?
        let content: String?
        let preamble: String?
        let status: String?
        let elicitationID: String?
        let mode: String?
        let callbackURL: String?
        let elicitationData: ElicitationData?

        enum CodingKeys: String, CodingKey {
            case id
            case type
            case conversationID = "conversationId"
            case turnID = "turnId"
            case messageID = "messageId"
            case assistantMessageID = "assistantMessageId"
            case content
            case preamble
            case status
            case elicitationID = "elicitationId"
            case mode
            case callbackURL = "callbackUrl"
            case elicitationData
        }

        var resolvedMessageID: String? {
            messageID?.trimmedNonEmpty ?? assistantMessageID?.trimmedNonEmpty ?? id?.trimmedNonEmpty
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

private extension String {
    var trimmedNonEmpty: String? {
        let trimmed = trimmingCharacters(in: .whitespacesAndNewlines)
        return trimmed.isEmpty ? nil : trimmed
    }
}
