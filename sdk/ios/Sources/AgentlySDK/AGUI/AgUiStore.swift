import Foundation

/// Authoritative protocol store for one POST run; keep UI rows as projections.
public final class AgUiStore {
    public let input: AgUiRunInput
    private let chunks = AgUiChunks()
    private let verifier: AgUiVerifier
    public private(set) var messages: [AgUiMessage]
    public private(set) var state: AgUiValue
    public private(set) var terminalEvent: AgUiEvent?
    public private(set) var capabilities: AgUiValue?
    public private(set) var pendingInterrupts: [AgUiValue] = []
    public private(set) var subagents: [String: AgUiSubagentInvocation] = [:]
    private var runCalls: [String] = [], answeredCalls = Set<String>()
    public var isTerminal: Bool { verifier.terminal }
    public var pendingToolCallIds: [String] { guard terminalEvent?.type == "RUN_FINISHED", terminalEvent?.value["outcome"]?["type"]?.string.map({$0 == "success"}) ?? true else { return [] }; if let named = terminalEvent?.value["outcome"]?["pendingToolCallIds"]?.array, !named.isEmpty { return named.compactMap { $0.string }.filter { !answeredCalls.contains($0) } }; return runCalls.filter { !answeredCalls.contains($0) } }
    public init(input: AgUiRunInput) throws {
        self.input = input; self.verifier = AgUiVerifier(input: input)
        messages = try input.value["messages"]!.array!.map { try AgUiMessage(value: $0.object!) }
        state = input.value["state"] ?? .object([:])
    }
    public func receive(_ event: AgUiEvent, onApplied: ((AgUiEvent) -> Void)? = nil) throws -> [AgUiEvent] {
        let events = try chunks.receive(event)
        for normalized in events { try verifier.receive(normalized); try reduce(normalized); onApplied?(normalized) }
        return events
    }
    public func finish() throws -> [AgUiEvent] {
        let events = try chunks.finish()
        for event in events { try verifier.receive(event); try reduce(event) }
        try verifier.finish(); return events
    }
    public func nextInput(runId: String, forwardedProps: AgUiValue? = nil, tools: [AgUiValue]? = nil, context: [AgUiValue]? = nil, resume: [AgUiValue]? = nil, includeActivityMessages: Bool = false, responseValidator: AgUiResponseValidator? = nil) throws -> AgUiRunInput {
        try snapshot.nextInput(runId: runId, forwardedProps: forwardedProps, tools: tools, context: context, resume: resume, includeActivityMessages: includeActivityMessages, responseValidator: responseValidator)
    }
    public func toolResultsInput(runId: String, results: [AgUiMessage], forwardedProps: AgUiValue? = nil, includeActivityMessages: Bool = false) throws -> AgUiRunInput {
        try snapshot.toolResultsInput(runId: runId, results: results, forwardedProps: forwardedProps, includeActivityMessages: includeActivityMessages)
    }
    private func metadata(_ target: [String: AgUiValue], _ event: [String: AgUiValue]) -> [String: AgUiValue] {
        guard let incoming = event["metadata"]?.object else { return target }
        let merged = (target["metadata"]?.object ?? [:]).merging(incoming) { _, incoming in incoming }
        return target.with(["metadata": .object(merged)])
    }
    private func changeMessage(_ id: String, _ transform: ([String: AgUiValue]) throws -> [String: AgUiValue]) throws {
        messages = try messages.map { try $0.id == id ? AgUiMessage(value: transform($0.value)) : $0 }
    }
    private func tool(_ id: String, _ transform: ([String: AgUiValue]) throws -> [String: AgUiValue]) throws {
        messages = try messages.map { message in
            guard let calls = message.value["toolCalls"]?.array, calls.contains(where: { $0["id"]?.string == id }) else { return message }
            let changed = try calls.map { call in try call["id"]?.string == id ? AgUiValue.object(transform(call.object!)) : call }
            return try AgUiMessage(value: message.value.with(["toolCalls": .array(changed)]))
        }
    }
    private func reduce(_ event: AgUiEvent) throws {
        let e = event.value
        switch event.type {
        case "TEXT_MESSAGE_START", "REASONING_MESSAGE_START":
            let id = try e.requiredString("messageId")
            if messages.contains(where: { $0.id == id && $0.role == "activity" }) { return }
            if !messages.contains(where: { $0.id == id }) {
                messages.append(try AgUiMessage(value: ["id": .string(id), "role": e["role"] ?? .string("assistant"), "content": .string("")].with(["name": e["name"], "subagentRunId": e["subagentRunId"]])))
            }
            try changeMessage(id) { metadata($0, e) }
        case "TEXT_MESSAGE_CONTENT", "REASONING_MESSAGE_CONTENT":
            try changeMessage(e.requiredString("messageId")) { message in
                if message.string("role") == "activity" { return message }
                let delta = try e.requiredString("delta"), content = message["content"]
                let next: AgUiValue
                if let parts = content?.array { next = .array(parts + [.object(["type": .string("text"), "text": .string(delta)])]) }
                else { next = .string((content?.string ?? "") + delta) }
                return metadata(message.with(["content": next]), e)
            }
        case "TEXT_MESSAGE_END", "REASONING_MESSAGE_END": try changeMessage(e.requiredString("messageId")) { $0.string("role") == "activity" ? $0 : metadata($0, e) }
        case "TOOL_CALL_START":
            let id = try e.requiredString("toolCallId"), name = try e.requiredString("toolCallName")
            if !runCalls.contains(id) { runCalls.append(id) }
            let exists = messages.contains { ($0.value["toolCalls"]?.array ?? []).contains { $0["id"]?.string == id } }
            if exists { try tool(id) { metadata($0.with(["function": .object($0["function"]!.object!.with(["name": .string(name)]))]), e) } }
            else {
                let parent = e.string("parentMessageId"), match = messages.first { $0.id == parent }
                let target = match != nil && match?.role != "assistant" ? id : parent ?? id
                if !messages.contains(where: { $0.id == target && $0.role == "assistant" }) { messages.append(try AgUiMessage(value: ["id": .string(target), "role": .string("assistant"), "toolCalls": .array([])].with(["subagentRunId": e["subagentRunId"]]))) }
                let call = metadata(["id": .string(id), "type": .string("function"), "function": .object(["name": .string(name), "arguments": .string("")])], e)
                try changeMessage(target) { $0.string("role") != "assistant" ? $0 : $0.with(["toolCalls": .array(($0["toolCalls"]?.array ?? []) + [.object(call)])]) }
            }
        case "TOOL_CALL_ARGS":
            try tool(e.requiredString("toolCallId")) { call in
                let function = call["function"]!.object!, arguments = try function.requiredString("arguments") + e.requiredString("delta")
                return metadata(call.with(["function": .object(function.with(["arguments": .string(arguments)]))]), e)
            }
        case "TOOL_CALL_END": try tool(e.requiredString("toolCallId")) { metadata($0, e) }
        case "TOOL_CALL_RESULT":
            let id = try e.requiredString("messageId"); answeredCalls.insert(try e.requiredString("toolCallId"))
            let value = metadata(["id": .string(id), "role": .string("tool"), "toolCallId": e["toolCallId"]!, "content": e["content"]!].with(["subagentRunId": e["subagentRunId"]]), e)
            messages.removeAll { $0.id == id }
            let callId = try e.requiredString("toolCallId")
            let owner = messages.firstIndex { message in message.role == "assistant" && (message.value["toolCalls"]?.array ?? []).contains { $0["id"]?.string == callId } }
            var insertAt = owner.map { $0 + 1 } ?? messages.count
            while insertAt < messages.count && messages[insertAt].role == "tool" { insertAt += 1 }
            messages.insert(try AgUiMessage(value: value), at: insertAt)
        case "STATE_SNAPSHOT": state = e["snapshot"]!
        case "STATE_DELTA": state = try AgUiJsonPatch.apply(state, operations: e["delta"]!.array!)
        case "MESSAGES_SNAPSHOT":
            let fresh = try e["messages"]!.array!.map { try AgUiMessage(value: $0.object!) }
            let map = Dictionary(fresh.map { ($0.id, $0) }, uniquingKeysWith: { _, new in new })
            let explicit = e["metadata"]?["@ag-ui/client"]?["authoritativeActivityTypes"], owned = explicit?.array
            let ownsAll = explicit == .null || explicit == nil && fresh.contains { $0.role == "activity" }
            let hasReasoning = fresh.contains { $0.role == "reasoning" }
            messages = messages.filter { message in
                map[message.id] != nil || message.role == "activity" && !ownsAll && (owned == nil || !(owned ?? []).contains { $0.string == message.value.string("activityType") }) || message.role == "reasoning" && !hasReasoning
            }.map { map[$0.id] ?? $0 }
            let ids = Set(messages.map { $0.id }); messages += fresh.filter { !ids.contains($0.id) }
        case "ACTIVITY_SNAPSHOT":
            let id = try e.requiredString("messageId"), old = messages.first { $0.id == id }, replace = e["replace"] != .bool(false)
            if old == nil || replace {
                let base = old?.role == "activity" ? old!.value : ["id": .string(id), "role": .string("activity")]
                let value = metadata(base.with(["activityType": e["activityType"], "content": e["content"], "subagentRunId": e["subagentRunId"]]), e)
                let message = try AgUiMessage(value: value)
                if old == nil { messages.append(message) } else { messages = messages.map { $0.id == id ? message : $0 } }
            } else if old?.role == "activity" { try changeMessage(id) { metadata($0, e) } }
        case "ACTIVITY_DELTA":
            try changeMessage(e.requiredString("messageId")) { message in
                guard message.string("role") == "activity" else { return message }
                let content = try AgUiJsonPatch.apply(message["content"]!, operations: e["patch"]!.array!)
                return metadata(message.with(["content": content, "activityType": e["activityType"]]), e)
            }
        case "REASONING_ENCRYPTED_VALUE":
            if e.string("subtype") == "tool-call" { try tool(e.requiredString("entityId")) { $0.with(["encryptedValue": e["encryptedValue"]]) } }
            else { try changeMessage(e.requiredString("entityId")) { $0.string("role") == "activity" ? $0 : $0.with(["encryptedValue": e["encryptedValue"]]) } }
        case "RUN_STARTED":
            for value in e["input"]?["messages"]?.array ?? [] { if !messages.contains(where: { $0.id == value["id"]?.string }) { messages.append(try AgUiMessage(value: value.object!)) } }
        case "RUN_FINISHED": terminalEvent = event; pendingInterrupts = e["outcome"]?["interrupts"]?.array ?? []
        case "RUN_ERROR": terminalEvent = event
        case "CUSTOM": if let capabilities = try AgentlyAgUiExtensions.capabilities(event) { self.capabilities = capabilities }
        case "SUBAGENT_STARTED": subagents[try e.requiredString("subagentRunId")] = AgUiSubagentInvocation(started: event, terminal: nil)
        case "SUBAGENT_FINISHED", "SUBAGENT_ERROR":
            let id = try e.requiredString("subagentRunId")
            subagents[id] = AgUiSubagentInvocation(started: subagents[id]!.started, terminal: event)
        default: break // Span/step/raw/custom/future events remain observable without synthetic messages.
        }
    }
}
