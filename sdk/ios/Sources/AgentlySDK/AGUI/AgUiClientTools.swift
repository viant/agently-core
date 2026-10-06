import Foundation

public struct AgUiClientToolResult: Sendable {
    public let content: AgUiValue
    public let error: String?
    public let metadata: AgUiValue?
    public init(content: AgUiValue, error: String? = nil, metadata: AgUiValue? = nil) { self.content = content; self.error = error; self.metadata = metadata }
}
public struct AgUiClientTool: Sendable {
    public let definition: AgUiValue
    public let validateArguments: AgUiResponseValidator?
    public let execute: @Sendable (AgUiValue, AgUiValue) async throws -> AgUiClientToolResult
    public init(definition: AgUiValue, validateArguments: AgUiResponseValidator? = nil, execute: @escaping @Sendable (AgUiValue, AgUiValue) async throws -> AgUiClientToolResult) throws {
        try AgUiSchema.validate(definition, definition: "Tool")
        self.definition = definition; self.validateArguments = validateArguments; self.execute = execute
    }
}
/// Explicit authorized execution only. Successful outputs survive partial batch
/// failure/replay; callers separately submit the resulting continuation input.
public actor AgUiClientToolDispatcher {
    private var completed: [String: AgUiMessage] = [:]
    private var signatures: [String:AgUiValue] = [:]
    private var dispatching = false
    public init() {}
    /// Clear only after the server has durably accepted the corresponding continuation.
    public func clearCompleted() throws {guard !dispatching else {try aguiFail("Dispatch active")};completed.removeAll();signatures.removeAll()}
    public func executeClientTools(snapshot: AgUiSnapshot, tools: [AgUiClientTool]) async throws -> [AgUiMessage] {
        guard snapshot.terminalEvent?.type == "RUN_FINISHED", snapshot.terminalEvent?.value["outcome"]?["type"]?.string.map({$0 == "success"}) ?? true else {try aguiFail("Client tool dispatch requires successful terminal")}
        return try await dispatch(snapshot: snapshot, ids: snapshot.pendingToolCallIds, tools: tools)
    }
    public func executeClientToolInterrupts(snapshot: AgUiSnapshot, tools: [AgUiClientTool]) async throws -> [AgUiValue] {
        if !snapshot.pendingInterrupts.isEmpty {guard snapshot.terminalEvent?.type == "RUN_FINISHED", snapshot.terminalEvent?.value["outcome"]?["type"]?.string == "interrupt" else {try aguiFail("Interrupt tool dispatch requires interrupt terminal")}}
        let interrupts = snapshot.pendingInterrupts.filter { $0["reason"]?.string == "agently.client_tool" && $0["metadata"]?["agently"]?["version"]?.string == "1" && $0["metadata"]?["agently"]?["kind"]?.string == "client-tool" }
        for interrupt in interrupts {
            if let date = interrupt["expiresAt"]?.string {let f = ISO8601DateFormatter(); var expiry = f.date(from: date); if expiry == nil { f.formatOptions.insert(.withFractionalSeconds); expiry = f.date(from: date) }; guard let expiry else {try aguiFail("Invalid interrupt expiry")}; if expiry <= Date() {try aguiFail("Expired client tool interrupt must be cancelled")}}
        }
        let ids = try interrupts.map { value -> String in guard let id = value["toolCallId"]?.string else {try aguiFail("Client tool interrupt has no call identity")}; return id }
        let results = try await dispatch(snapshot: snapshot, ids: ids, tools: tools)
        return zip(interrupts, results).map { interrupt,result in
            var payload: [String:AgUiValue] = ["content":result.value["content"]!]; payload["error"] = result.value["error"]
            var answer: [String:AgUiValue] = ["interruptId":interrupt["id"]!, "status":.string("resolved"), "payload":.object(payload)]; answer["metadata"] = result.value["metadata"]
            return .object(answer)
        }
    }
    private func dispatch(snapshot: AgUiSnapshot, ids: [String], tools: [AgUiClientTool]) async throws -> [AgUiMessage] {
        guard !dispatching else {try aguiFail("Client tool dispatch already active")}
        var registry: [String:AgUiClientTool] = [:]
        for tool in tools {let name = tool.definition["name"]!.string!; guard registry[name] == nil else {try aguiFail("Duplicate client tool")}; registry[name] = tool}
        let calls = try ids.map { id -> (String, AgUiValue, AgUiClientTool, AgUiValue) in
            guard let call = snapshot.messages.flatMap({$0.value["toolCalls"]?.array ?? []}).first(where: {$0["id"]?.string == id}), let name = call["function"]?["name"]?.string, let handler = registry[name], let text = call["function"]?["arguments"]?.string else {try aguiFail("Missing authorized client tool handler/call")}
            let args = try AgUiValue.parse(Data(text.utf8)); let schema = handler.definition["parameters"]!
            if let validator = handler.validateArguments {try validator(args,schema)} else {try AgUiSchema.validateResponse(args,schema:schema)}
            let key = try AgUiValue.array([snapshot.input.value["threadId"]!,snapshot.input.value["runId"]!,.string(id)]).jsonString()
            return (key, call,handler,args)
        }
        dispatching = true; defer {dispatching = false}
        var results: [AgUiMessage] = []
        for (key,call,handler,args) in calls {
            if let cached = completed[key] {guard signatures[key] == call["function"] else {try aguiFail("Completed client tool identity has conflicting arguments")}; results.append(cached); continue}
            let result = try await handler.execute(args,call)
            var fields: [String:AgUiValue] = ["id":.string(UUID().uuidString), "role":.string("tool"), "toolCallId":call["id"]!, "content":result.content]
            fields["error"] = result.error.map {.string($0)}; fields["metadata"] = result.metadata
            let message = try AgUiMessage(value:fields); completed[key] = message; signatures[key] = call["function"]; results.append(message)
        }
        return results
    }
}
