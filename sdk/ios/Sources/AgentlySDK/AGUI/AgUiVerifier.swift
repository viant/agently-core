import Foundation

final class AgUiVerifier {
    let input: AgUiRunInput
    private var started = false
    private(set) var terminal = false
    private var errored = false
    private var open: [String: Set<String>] = [:]
    private var owners: [String: [String: String?]] = [:]
    private var steps: [String?: Set<String>] = [:]
    private var activeSubagents = Set<String>(), closedSubagents = Set<String>()
    init(input: AgUiRunInput) { self.input = input }
    private func ownerCheck(_ kind: String, _ id: String, _ tag: String?) throws {
        if let tag, let owner = owners[kind]?[id], owner != tag { try aguiFail("Attribution does not match \(kind) \(id) opener") }
    }
    private func start(_ kind: String, _ id: String, _ tag: String?) throws {
        guard !(open[kind] ?? []).contains(id) else { try aguiFail("\(kind) \(id) already open") }
        open[kind, default: []].insert(id)
        let ownerKind = kind == "reasoningSpan" ? "reasoning" : kind
        try ownerCheck(ownerKind, id, tag)
        if owners[ownerKind]?[id] == nil { owners[ownerKind, default: [:]][id] = .some(tag) }
    }
    private func continuation(_ kind: String, _ id: String, _ tag: String?, close: Bool = false) throws {
        guard (open[kind] ?? []).contains(id) else { try aguiFail("No open \(kind) \(id)") }
        try ownerCheck(kind == "reasoningSpan" ? "reasoning" : kind, id, tag)
        if close { open[kind]?.remove(id) }
    }
    private func seed(_ messages: [AgUiValue], authoritative: Bool) throws {
        for raw in messages {
            guard let message = raw.object else { try aguiFail("Invalid history message") }
            let id = try message.requiredString("id"), tag = message.string("subagentRunId"), role = try message.requiredString("role")
            let kind = role == "activity" ? "activity" : role == "reasoning" ? "reasoning" : "message"
            if authoritative || owners[kind]?[id] == nil { owners[kind, default: [:]][id] = .some(tag) }
            for call in message["toolCalls"]?.array ?? [] {
                let callId = try call.object!.requiredString("id")
                if authoritative || owners["tool"]?[callId] == nil { owners["tool", default: [:]][callId] = .some(tag) }
            }
        }
    }
    func receive(_ event: AgUiEvent) throws {
        let e = event.value, type = event.type, tag = e.string("subagentRunId")
        guard !terminal || type == "RUN_ERROR" && !errored else { try aguiFail("Event after terminal run event") }
        if !started && type != "RUN_STARTED" && type != "RUN_ERROR" { try aguiFail("First event must be RUN_STARTED or RUN_ERROR") }
        switch type {
        case "RUN_STARTED":
            guard !started else { try aguiFail("Run already started") }
            guard e.string("threadId") == input.threadId, e.string("runId") == input.runId else { try aguiFail("RUN_STARTED identity mismatch") }
            started = true
            if let messages = e["input"]?["messages"]?.array { try seed(messages, authoritative: false) }
        case "RUN_FINISHED":
            guard e.string("threadId") == input.threadId, e.string("runId") == input.runId else { try aguiFail("RUN_FINISHED identity mismatch") }
            guard !open.values.contains(where: { !$0.isEmpty }), !steps.values.contains(where: { !$0.isEmpty }), activeSubagents.isEmpty else { try aguiFail("RUN_FINISHED with open entities") }
            terminal = true
        case "RUN_ERROR": terminal = true; errored = true
        case "TEXT_MESSAGE_START": try start("message", e.requiredString("messageId"), tag)
        case "TEXT_MESSAGE_CONTENT": try continuation("message", e.requiredString("messageId"), tag)
        case "TEXT_MESSAGE_END": try continuation("message", e.requiredString("messageId"), tag, close: true)
        case "TOOL_CALL_START":
            let id = try e.requiredString("toolCallId"), parent = e.string("parentMessageId")
            guard !(open["tool"] ?? []).contains(id) else { try aguiFail("Tool call already open") }; open["tool", default: []].insert(id)
            let inheritedKnown = parent.map { owners["message"]?[$0] != nil } ?? false
            let inherited: String? = parent.flatMap { owners["message"]?[$0] ?? nil }
            if let parent { try ownerCheck("message", parent, tag) }
            if owners["tool"]?[id] != nil {
                try ownerCheck("tool", id, tag)
                if tag == nil && inheritedKnown && (owners["tool"]?[id] ?? nil) != inherited { try aguiFail("Tool owner disagrees with parent") }
            } else { owners["tool", default: [:]][id] = .some(tag ?? inherited) }
        case "TOOL_CALL_ARGS": try continuation("tool", e.requiredString("toolCallId"), tag)
        case "TOOL_CALL_END": try continuation("tool", e.requiredString("toolCallId"), tag, close: true)
        case "TOOL_CALL_RESULT": owners["message", default: [:]][try e.requiredString("messageId")] = .some(tag)
        case "REASONING_START": try start("reasoningSpan", e.requiredString("messageId"), tag)
        case "REASONING_END": try continuation("reasoningSpan", e.requiredString("messageId"), tag, close: true)
        case "REASONING_MESSAGE_START": try start("reasoning", e.requiredString("messageId"), tag)
        case "REASONING_MESSAGE_CONTENT": try continuation("reasoning", e.requiredString("messageId"), tag)
        case "REASONING_MESSAGE_END": try continuation("reasoning", e.requiredString("messageId"), tag, close: true)
        case "REASONING_ENCRYPTED_VALUE":
            let id = try e.requiredString("entityId")
            if e.string("subtype") == "tool-call" { try ownerCheck("tool", id, tag) }
            else { try ownerCheck("message", id, tag); try ownerCheck("reasoning", id, tag) }
        case "ACTIVITY_SNAPSHOT":
            let id = try e.requiredString("messageId")
            if owners["activity"]?[id] == nil || e["replace"] != .bool(false) { owners["activity", default: [:]][id] = .some(tag) }
        case "ACTIVITY_DELTA": try ownerCheck("activity", e.requiredString("messageId"), tag)
        case "STEP_STARTED":
            let name = try e.requiredString("stepName")
            guard !(steps[tag] ?? []).contains(name) else { try aguiFail("Step already open") }; steps[tag, default: []].insert(name)
        case "STEP_FINISHED": guard steps[tag]?.remove(try e.requiredString("stepName")) != nil else { try aguiFail("No open step for this owner") }
        case "SUBAGENT_STARTED":
            let id = try e.requiredString("subagentRunId")
            guard !activeSubagents.contains(id), !closedSubagents.contains(id) else { try aguiFail("Duplicate subagent invocation") }
            if let parent = e.string("parentSubagentRunId"), !activeSubagents.contains(parent), !closedSubagents.contains(parent) { try aguiFail("Unknown parent subagent") }
            activeSubagents.insert(id)
        case "SUBAGENT_FINISHED", "SUBAGENT_ERROR":
            let id = try e.requiredString("subagentRunId"); guard activeSubagents.remove(id) != nil else { try aguiFail("No active subagent") }; closedSubagents.insert(id)
        case "MESSAGES_SNAPSHOT": try seed(e["messages"]!.array!, authoritative: true)
        case "TEXT_MESSAGE_CHUNK", "TOOL_CALL_CHUNK", "REASONING_MESSAGE_CHUNK": try aguiFail("Chunks must be normalized first")
        default: break
        }
    }
    func finish() throws { guard terminal else { try aguiFail("SSE ended without terminal event") } }
}
