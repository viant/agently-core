import Foundation

public let agUiProtocolVersion = "1.0"
public enum AgUiEventType: String, Sendable, CaseIterable {
    case textMessageStart = "TEXT_MESSAGE_START"
    case textMessageContent = "TEXT_MESSAGE_CONTENT"
    case textMessageEnd = "TEXT_MESSAGE_END"
    case textMessageChunk = "TEXT_MESSAGE_CHUNK"
    case toolCallStart = "TOOL_CALL_START"
    case toolCallArgs = "TOOL_CALL_ARGS"
    case toolCallEnd = "TOOL_CALL_END"
    case toolCallChunk = "TOOL_CALL_CHUNK"
    case toolCallResult = "TOOL_CALL_RESULT"
    case stateSnapshot = "STATE_SNAPSHOT"
    case stateDelta = "STATE_DELTA"
    case messagesSnapshot = "MESSAGES_SNAPSHOT"
    case activitySnapshot = "ACTIVITY_SNAPSHOT"
    case activityDelta = "ACTIVITY_DELTA"
    case raw = "RAW"
    case custom = "CUSTOM"
    case runStarted = "RUN_STARTED"
    case runFinished = "RUN_FINISHED"
    case runError = "RUN_ERROR"
    case stepStarted = "STEP_STARTED"
    case stepFinished = "STEP_FINISHED"
    case reasoningStart = "REASONING_START"
    case reasoningMessageStart = "REASONING_MESSAGE_START"
    case reasoningMessageContent = "REASONING_MESSAGE_CONTENT"
    case reasoningMessageEnd = "REASONING_MESSAGE_END"
    case reasoningMessageChunk = "REASONING_MESSAGE_CHUNK"
    case reasoningEnd = "REASONING_END"
    case reasoningEncryptedValue = "REASONING_ENCRYPTED_VALUE"
    case subagentStarted = "SUBAGENT_STARTED"
    case subagentFinished = "SUBAGENT_FINISHED"
    case subagentError = "SUBAGENT_ERROR"
}

public struct AgUiMessage: Sendable, Equatable {
    public let value: [String: AgUiValue]
    public var id: String { value["id"]!.string! }
    public var role: String { value["role"]!.string! }
    public init(value: [String: AgUiValue]) throws { try AgUiSchema.validate(.object(value), definition: "Message", tolerateUnknownFields: true); self.value = value }
    public static func user(id: String, content: AgUiValue) throws -> AgUiMessage { try AgUiMessage(value: ["id": .string(id), "role": .string("user"), "content": content]) }
}
public struct AgUiRunInput: Sendable, Equatable {
    public let value: [String: AgUiValue]
    public var threadId: String { value["threadId"]!.string! }
    public var runId: String { value["runId"]!.string! }
    public init(value: [String: AgUiValue]) throws { try AgUiSchema.validate(.object(value), definition: "RunAgentInput", tolerateUnknownFields: true); self.value = value }
    public init(threadId: String, runId: String, messages: [AgUiMessage], state: AgUiValue? = nil, tools: [AgUiValue]? = nil, context: [AgUiValue]? = nil, forwardedProps: AgUiValue? = nil, resume: [AgUiValue]? = nil, parentRunId: String? = nil) throws {
        var value: [String: AgUiValue] = ["threadId": .string(threadId), "runId": .string(runId), "protocolVersion": .string(agUiProtocolVersion), "messages": .array(messages.map { .object($0.value) })]
        value["state"] = state; value["tools"] = tools.map { .array($0) }; value["context"] = context.map { .array($0) }; value["forwardedProps"] = forwardedProps; value["resume"] = resume.map { .array($0) }; value["parentRunId"] = parentRunId.map { .string($0) }
        try self.init(value: value)
    }
}
public struct AgUiEvent: Sendable, Equatable {
    public let value: [String: AgUiValue]
    public var type: String { value["type"]!.string! }
    public var knownType: AgUiEventType? { AgUiEventType(rawValue: type) }
    public init(value: [String: AgUiValue]) throws {
        let type = try value.requiredString("type")
        if AgUiEventType(rawValue: type) != nil { try AgUiSchema.validate(.object(value), definition: "Event", tolerateUnknownFields: true) }
        self.value = value
    }
}
public struct AgUiSubagentInvocation: Sendable, Equatable {
    public let started: AgUiEvent
    public let terminal: AgUiEvent?
}

public enum AgentlyAgUiExtensions {
    public static let version = "1"
    public static func forwardedProps(operation: String, agentId: String? = nil, model: String? = nil, requestId: String? = nil, existing: [String: AgUiValue] = [:]) throws -> AgUiValue {
        guard ["chat", "capabilities"].contains(operation) else { try aguiFail("Unsupported Agently operation") }
        var extensionValue: [String: AgUiValue] = ["version": .string(version), "operation": .string(operation)]
        extensionValue["requestId"] = requestId.map { .string($0) }
        if agentId != nil || model != nil { var payload: [String: AgUiValue] = [:]; payload["agentId"] = agentId.map { .string($0) }; payload["model"] = model.map { .string($0) }; extensionValue["payload"] = .object(payload) }
        return .object(existing.with(["agently": .object(extensionValue)]))
    }
    /// Resource commands are explicit server extensions, distinct from aborting HTTP.
    public static func command(operation: String, requestId: String, payload: AgUiValue, existing: [String:AgUiValue] = [:]) throws -> AgUiValue {
        guard !operation.isEmpty, !requestId.isEmpty, payload.object != nil else {try aguiFail("Command requires operation, request identity and object payload")}
        return .object(existing.with(["agently":.object(["version":.string(version),"operation":.string(operation),"requestId":.string(requestId),"payload":payload])]))
    }
    public static func cancelRun(targetRunId: String, requestId: String) throws -> AgUiValue {
        guard !targetRunId.isEmpty else {try aguiFail("Cancellation requires target run identity")}
        return try command(operation:"run.cancel",requestId:requestId,payload:.object(["runId":.string(targetRunId)]))
    }
    public static func cancelInput(threadId: String, targetRunId: String, commandRunId: String) throws -> AgUiRunInput {
        try AgUiRunInput(threadId:threadId,runId:commandRunId,messages:[],forwardedProps:cancelRun(targetRunId:targetRunId,requestId:commandRunId))
    }
    public static func capabilities(_ event: AgUiEvent) throws -> AgUiValue? {
        guard event.type == "CUSTOM", event.value.string("name") == "agently.capabilities", let envelope = event.value["value"]?.object, envelope.string("version") == version, let capabilities = envelope["capabilities"] else { return nil }
        try AgUiSchema.validate(capabilities, definition: "AgentCapabilities", tolerateUnknownFields: true)
        return capabilities
    }
}
