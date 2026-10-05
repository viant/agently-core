import Foundation

/// Read-only native presentation adapter. The standard AgUiStore remains the
/// sole owner of protocol messages, arguments, patches and continuation state.
public struct AgUiPresentationProjector: Sendable {
    public let conversationID: String
    public let runID: String
    private var nativeTurnID: String?
    private var owned: Set<String> = []
    private var baseline: [String: AgUiMessage]
    private var calls: [String: [String: AgUiValue]] = [:]
    private var userIDs: [String: String] = [:]
    private var nativeStatuses: [String: String] = [:]
    private var metadata: [String: [String: AgUiValue]] = [:]
    private var emitted: [String: AgUiValue] = [:]
    public private(set) var hostActivities: [AgUiMessage] = []

    public init(conversationID: String, runID: String, baseline: [AgUiMessage] = []) {
        self.conversationID = conversationID; self.runID = runID
        self.baseline = Dictionary(uniqueKeysWithValues: baseline.map { ($0.id, $0) })
    }

    public static func presentation(_ value: AgUiValue?) -> [String: AgUiValue]? {
        guard let namespace = value?["agently"]?.object else { return nil }
        guard let result = namespace["presentation"]?.object else {
            if namespace["identityVersion"]?.string == "1", let turn = namespace["nativeTurnId"]?.string {
                return ["version": .string("1"), "nativeTurnId": .string(turn)]
            }
            return nil
        }
        guard result["version"]?.string == "1" else { return nil }
        let strings = ["conversationId", "nativeTurnId", "nativeMessageId", "parentMessageId", "pageId", "modelCallId", "nativeToolCallId", "toolMessageId", "protocolRunId", "clientMessageId", "clientRequestId", "nativeUserMessageId", "executionRole", "phase", "mode", "status", "agentId", "agentName", "provider", "model", "requestPayloadId", "responsePayloadId", "providerRequestPayloadId", "providerResponsePayloadId", "streamPayloadId", "createdAt", "startedAt", "completedAt", "usageScope"]
        let counts = ["iteration", "pageIndex", "pageCount", "inputTokens", "outputTokens", "cachedInputTokens", "reasoningTokens", "embeddingTokens", "totalTokens", "cacheWriteInputTokens"]
        var accepted: [String: AgUiValue] = ["version": .string("1")]
        for key in strings where result[key] != nil { guard result[key]?.string != nil else { return nil }; accepted[key] = result[key] }
        for key in counts where result[key] != nil {
            guard case .number(let token) = result[key], let count = Int(token), count >= 0 else { return nil }
            accepted[key] = result[key]
        }
        if let latest = result["latestPage"] { guard case .bool = latest else { return nil }; accepted["latestPage"] = latest }
        return accepted
    }

    /// Admission belongs to this request only; unrelated bootstrap history cannot
    /// acknowledge a composer request, even if it includes an active native turn.
    public mutating func admittedTurn(_ update: AgUiUpdate, clientMessageID: String) -> String? {
        let event = update.event
        if event.type == "RUN_STARTED", event.value["runId"]?.string == runID,
           event.value["subagentRunId"] == nil,
           let p = Self.presentation(event.value["metadata"]), let turn = p["nativeTurnId"]?.string {
            nativeTurnID = turn
        }
        for message in update.snapshot.messages where message.role == "activity" {
            guard message.value["activityType"]?.string == "agently.user-identity",
                  let content = message.value["content"]?.object,
                  content["version"]?.string == "1", content["protocolRunId"]?.string == runID,
                  content["clientMessageId"]?.string == clientMessageID,
                  content["clientRequestId"]?.string == clientMessageID,
                  let turn = content["nativeTurnId"]?.string else { continue }
            nativeTurnID = turn
        }
        guard let turn = nativeTurnID else { return nil }
        for message in update.snapshot.messages where message.role == "activity" {
            let content = message.value["content"]
            if message.value["activityType"]?.string == "agently.turn", content?["version"]?.string == "1",
               content?["nativeTurnId"]?.string == turn,
               ["running", "queued"].contains(content?["status"]?.string ?? "") { return turn }
        }
        return nil
    }

    public mutating func project(_ update: AgUiUpdate) throws -> [SSEEvent] {
        let event = update.event, p = Self.presentation(event.value["metadata"])
        if event.type == "RUN_STARTED", event.value["runId"]?.string == runID, event.value["subagentRunId"] == nil {
            nativeTurnID = p?["nativeTurnId"]?.string ?? nativeTurnID
        }
        if let id = event.value["messageId"]?.string { owned.insert(id); if let p { metadata[id] = p } }
        if let parent = event.value["parentMessageId"]?.string { owned.insert(parent) }
        if let call = event.value["toolCallId"]?.string, let p { calls[call] = p }
        if let user = update.snapshot.input.value["messages"]?.array?.last, user["role"]?.string == "user", let id = user["id"]?.string, baseline[id] == nil { owned.insert(id) }
        var result: [SSEEvent] = []
        func fields(_ presentation: [String: AgUiValue]?) -> [String: AgUiValue] {
            var row: [String: AgUiValue] = ["conversationId": .string(conversationID), "protocolRunId": .string(runID), "contentMode": .string("snapshot")]
            row["turnId"] = presentation?["nativeTurnId"] ?? nativeTurnID.map { .string($0) }
            for key in ["pageId", "iteration", "phase", "mode", "executionRole", "modelCallId", "provider", "createdAt", "startedAt", "completedAt", "requestPayloadId", "responsePayloadId", "providerRequestPayloadId", "providerResponsePayloadId", "streamPayloadId"] { row[key] = presentation?[key] }
            row["agentIdUsed"] = presentation?["agentId"]; row["modelName"] = presentation?["model"]
            return row
        }
        func emit(_ row: [String: AgUiValue], key: String) throws {
            let value = AgUiValue.object(row)
            guard emitted[key] != value else { return }; emitted[key] = value
            result.append(SSEEvent(data: try value.jsonString()))
        }
        // Identity/lifecycle descriptors are resolved before visible messages.
        for message in update.snapshot.messages where message.role == "activity" {
            let content = message.value["content"]
            if message.value["activityType"]?.string == "agently.user-identity", content?["version"]?.string == "1",
               content?["protocolRunId"]?.string == runID, let turn = content?["nativeTurnId"]?.string,
               let user = content?["nativeUserMessageId"]?.string { userIDs[turn] = user }
            if message.value["activityType"]?.string == "agently.turn", content?["version"]?.string == "1",
               let turn = content?["nativeTurnId"]?.string, let status = content?["status"]?.string { nativeStatuses[turn] = status }
        }
        hostActivities = update.snapshot.messages.filter { $0.role == "activity" && $0.value["activityType"]?.string == "mcp-apps" && owned.contains($0.id) }
        for message in update.snapshot.messages {
            let mp = Self.presentation(message.value["metadata"]) ?? metadata[message.id]
            let sameTurn = nativeTurnID != nil && mp?["nativeTurnId"]?.string == nativeTurnID
            guard owned.contains(message.id) || (sameTurn && baseline[message.id] != message) else { continue }
            owned.insert(message.id)
            var row = fields(mp)
            row["messageId"] = mp?["nativeMessageId"] ?? .string(message.id)
            row["protocolMessageId"] = .string(message.id)
            let content = message.value["content"]
            switch message.role {
            case "user":
                guard let turn = row["turnId"]?.string, nativeStatuses[turn] == "running", content?.string != nil else { continue }
                row["type"] = .string("message_appended"); row["patch"] = .object(["role": .string("user")])
                row["messageId"] = userIDs[turn].map { .string($0) } ?? mp?["nativeUserMessageId"] ?? .string(message.id)
                row["userMessageId"] = row["messageId"]; row["content"] = content
                try emit(row, key: "user/" + message.id)
            case "assistant", "reasoning":
                if content?.string != nil {
                    row["type"] = .string(message.role == "reasoning" ? "reasoning_delta" : "text_delta")
                    row["content"] = content; row["assistantMessageId"] = row["messageId"]
                    row["pageId"] = row["pageId"] ?? row["messageId"]
                    try emit(row, key: "message/" + message.id)
                }
                for call in message.value["toolCalls"]?.array ?? [] {
                    var tool = fields(calls[call["id"]?.string ?? ""] ?? mp); tool["type"] = .string("tool_call_started")
                    tool["toolCallId"] = calls[call["id"]?.string ?? ""]?["nativeToolCallId"] ?? call["id"]; tool["toolName"] = call["function"]?["name"]
                    tool["assistantMessageId"] = row["messageId"]; tool["status"] = .string("requested")
                    if let args = call["function"]?["arguments"]?.string { tool["arguments"] = try? AgUiValue.parse(args) }
                    try emit(tool, key: "request/" + (call["id"]?.string ?? message.id))
                }
            case "tool":
                row["type"] = .string(message.value["error"] == nil ? "tool_call_completed" : "tool_call_failed")
                row["toolCallId"] = mp?["nativeToolCallId"] ?? calls[message.value["toolCallId"]?.string ?? ""]?["nativeToolCallId"] ?? message.value["toolCallId"]
                row["toolMessageId"] = mp?["toolMessageId"] ?? .string(message.id)
                row["content"] = content; row["error"] = message.value["error"]
                if let text = content?.string { row["responsePayload"] = (try? AgUiValue.parse(text)) ?? .object(["text": .string(text)]) }
                row["status"] = .string(message.value["error"] == nil ? "completed" : "failed")
                try emit(row, key: "result/" + message.id)
            case "activity":
                guard content?["version"]?.string == "1" else { continue }
                switch message.value["activityType"]?.string {
                case "agently.turn":
                    guard let turn = content?["nativeTurnId"]?.string, let status = content?["status"]?.string else { continue }
                    row["turnId"] = .string(turn); row["status"] = .string(status)
                    let types = ["running": "turn_started", "queued": "turn_queued", "completed": "turn_completed", "failed": "turn_failed", "canceled": "turn_canceled"]
                    guard let type = types[status] else { continue }; row["type"] = .string(type)
                    row["userMessageId"] = content?["startedByMessageId"]
                    try emit(row, key: "turn/" + turn)
                case "agently.tool":
                    row["toolCallId"] = mp?["nativeToolCallId"] ?? calls[content?["toolCallId"]?.string ?? ""]?["nativeToolCallId"] ?? content?["toolCallId"]
                    row["status"] = content?["status"]; row["toolMessageId"] = content?["toolMessageId"]
                    row["startedAt"] = content?["startedAt"]; row["completedAt"] = content?["completedAt"]
                    let status = content?["status"]?.string ?? "running"
                    row["type"] = .string(content?["phase"]?.string == "waiting" ? "tool_call_waiting" : status == "completed" ? "tool_call_completed" : status == "failed" ? "tool_call_failed" : status == "canceled" ? "tool_call_canceled" : "tool_call_started")
                    try emit(row, key: "effect/" + (row["toolCallId"]?.string ?? message.id))
                case "agently.feed":
                    let feed = content?["feed"] ?? content
                    guard let id = feed?["feedId"]?.string else { continue }
                    row["feedId"] = .string(id); row["feedTitle"] = feed?["title"]
                    row["feedDeveloperOnly"] = feed?["developerOnly"]; row["feedItemCount"] = feed?["itemCount"]
                    row["feedData"] = feed?["data"]; row["feedIcon"] = feed?["presentation"]?["icon"]
                    row["feedAccent"] = feed?["presentation"]?["accent"]; row["feedTarget"] = feed?["presentation"]?["target"]
                    let active = content?["active"]
                    row["type"] = .string(active == .bool(true) ? "tool_feed_active" : active == .bool(false) ? "tool_feed_inactive" : "tool_feed_unknown")
                    try emit(row, key: "feed/" + id)
                case "agently.planner":
                    guard let status = content?["status"]?.string else { continue }
                    row["type"] = .string("planner." + status)
                    for (source, target) in [("trigger", "plannerTrigger"), ("staticProfile", "plannerStaticProfile"), ("strategyFamily", "plannerStrategyFamily"), ("attempt", "plannerAttempt"), ("secondPolicy", "plannerSecondPolicy"), ("validated", "plannerValidated"), ("outputPayloadId", "plannerOutputPayloadId")] { row[target] = content?[source] }
                    try emit(row, key: "planner/" + message.id)
                case "agently.tools-planned":
                    row["type"] = .string("tool_calls_planned"); row["toolCallsPlanned"] = content?["calls"]
                    try emit(row, key: "planned/" + message.id)
                case "agently.narration":
                    row["type"] = .string("narration"); row["narration"] = content?["text"]; row["status"] = content?["status"]
                    try emit(row, key: "narration/" + message.id)
                case "agently.rendered-content":
                    row["type"] = .string("text_delta"); row["renderedContent"] = content?["renderedContent"]
                    row["assistantMessageId"] = mp?["nativeMessageId"]; row["messageId"] = mp?["nativeMessageId"]
                    try emit(row, key: "rendered/" + message.id)
                default: break
                }
            default: break
            }
        }
        if ["STEP_STARTED", "STEP_FINISHED"].contains(event.type) {
            var row = fields(p); row["type"] = .string(event.type == "STEP_STARTED" ? "model_started" : "model_completed")
            row["modelCallId"] = p?["modelCallId"] ?? event.value["stepName"]
            row["status"] = .string(event.type == "STEP_STARTED" ? "running" : "completed")
            try emit(row, key: "step/" + (row["modelCallId"]?.string ?? ""))
        }
        if event.type == "CUSTOM", event.value["name"]?.string == "agently.usage", event.value["value"]?["version"]?.string == "1" {
            var row = fields(p); row["type"] = .string("usage"); row["usage"] = event.value["value"]?["usage"]
            row["modelCallId"] = event.value["value"]?["modelCallId"]; row["turnId"] = event.value["value"]?["nativeTurnId"] ?? row["turnId"]
            try emit(row, key: "usage/" + (row["modelCallId"]?.string ?? ""))
        }
        if event.type == "RUN_ERROR" {
            var row = fields(p); row["type"] = .string("turn_failed"); row["error"] = event.value["message"]; row["status"] = .string("failed")
            try emit(row, key: "terminal")
        }
        // RUN_FINISHED interrupt is a protocol boundary, not native completion.
        return result
    }
}
