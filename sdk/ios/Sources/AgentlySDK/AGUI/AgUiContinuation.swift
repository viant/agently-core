import Foundation

public typealias AgUiResponseValidator = @Sendable (AgUiValue, AgUiValue) throws -> Void

public extension AgUiSnapshot {
    func nextInput(runId: String, forwardedProps: AgUiValue? = nil, tools: [AgUiValue]? = nil, context: [AgUiValue]? = nil, resume: [AgUiValue]? = nil, includeActivityMessages: Bool = false, responseValidator: AgUiResponseValidator? = nil) throws -> AgUiRunInput {
        guard terminalEvent != nil else { try aguiFail("Cannot construct continuation during active run") }
        try validateResume(resume, validator: responseValidator)
        var value = input.value
        value["runId"] = .string(runId); value["messages"] = .array(messages.filter { includeActivityMessages || $0.role != "activity" }.map { .object($0.value) })
        value["state"] = state == .null ? nil : state
        if let forwardedProps { value["forwardedProps"] = forwardedProps }; if let tools { value["tools"] = .array(tools) }; if let context { value["context"] = .array(context) }
        value["resume"] = resume.map { .array($0) }
        return try AgUiRunInput(value: value)
    }
    private func validateResume(_ resume: [AgUiValue]?, validator: AgUiResponseValidator?) throws {
        let entries = resume ?? []
        let expected = Set(pendingInterrupts.compactMap { $0["id"]?.string })
        let ids = try entries.map { entry -> String in guard let id = entry["interruptId"]?.string else {try aguiFail("Missing interrupt identity")}; return id }
        guard ids.count == Set(ids).count, Set(ids) == expected else {try aguiFail("Provide exactly one response for every pending interrupt")}
        for interrupt in pendingInterrupts {
            let id = interrupt["id"]!.string!
            guard let entry = (resume ?? []).first(where: { $0["interruptId"]?.string == id }) else { try aguiFail("Pending interrupt \(id) is not addressed by resume") }
            try AgUiSchema.validate(entry, definition: "ResumeEntry")
            if entry["status"]?.string == "resolved", let schema = interrupt["responseSchema"] {
                guard let payload = entry["payload"] else {try aguiFail("Interrupt requires response payload")}
                if let validator {try validator(payload,schema)} else {try AgUiSchema.validateResponse(payload,schema:schema)}
            }
            if let expiration = interrupt["expiresAt"]?.string {
                let formatter = ISO8601DateFormatter()
                var expiry = formatter.date(from: expiration)
                if expiry == nil { formatter.formatOptions.insert(.withFractionalSeconds); expiry = formatter.date(from: expiration) }
                if let expiry, expiry <= Date(), entry["status"]?.string != "cancelled" { try aguiFail("Expired interrupt \(id) must be cancelled") }
            }
        }
    }
    /// Caller executes advertised client tools, then returns typed results in a new run.
    func toolResultsInput(runId: String, results: [AgUiMessage], forwardedProps: AgUiValue? = nil, includeActivityMessages: Bool = false) throws -> AgUiRunInput {
        guard terminalEvent?.type == "RUN_FINISHED", terminalEvent?.value["outcome"]?["type"]?.string.map({ $0 == "success" }) ?? true else { try aguiFail("Client tool continuation requires a successful run") }
        let pending = Set(pendingToolCallIds)
        let resultIds = try results.map { message -> String in guard message.role == "tool" else { try aguiFail("Expected tool message") }; return try message.value.requiredString("toolCallId") }
        guard resultIds.count == Set(resultIds).count, Set(resultIds) == pending else { try aguiFail("Provide exactly one result for every pending client tool") }
        let next = try nextInput(runId: runId, forwardedProps: forwardedProps, includeActivityMessages: includeActivityMessages)
        var history = try next.value["messages"]!.array!.map { try AgUiMessage(value: $0.object!) }
        for result in results {
            let callId = try result.value.requiredString("toolCallId")
            guard let owner = history.firstIndex(where: { ($0.value["toolCalls"]?.array ?? []).contains { $0["id"]?.string == callId } }) else { try aguiFail("Pending client tool has no assistant owner") }
            var position = owner + 1
            while position < history.count && history[position].role == "tool" { position += 1 }
            history.insert(result, at: position)
        }
        return try AgUiRunInput(value: next.value.with(["messages": .array(history.map { .object($0.value) })]))
    }
}
