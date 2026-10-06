import Foundation

public struct AgUiSnapshot: Sendable, Equatable {
    public let input: AgUiRunInput
    public let messages: [AgUiMessage]
    public let state: AgUiValue
    public let terminalEvent: AgUiEvent?
    public let pendingInterrupts: [AgUiValue]
    public let pendingToolCallIds: [String]
    public let capabilities: AgUiValue?
    public let subagents: [String: AgUiSubagentInvocation]
}
public struct AgUiUpdate: Sendable, Equatable {
    public let sourceEvent: AgUiEvent?
    public let event: AgUiEvent
    public let snapshot: AgUiSnapshot
}
extension AgUiStore {
    public var snapshot: AgUiSnapshot { AgUiSnapshot(input: input, messages: messages, state: state, terminalEvent: terminalEvent, pendingInterrupts: pendingInterrupts, pendingToolCallIds: pendingToolCallIds, capabilities: capabilities, subagents: subagents) }
}

/// Incremental WHATWG SSE framing across byte, UTF-8 and CR/LF boundaries.
public struct AgUiSSEParser {
    private var line: [UInt8] = [], dataLines: [[UInt8]] = []
    private var previousCR = false, firstLine = true
    private var frameBytes = 0
    public let maxFrameBytes: Int
    public init(maxFrameBytes: Int = 64 * 1024 * 1024) { self.maxFrameBytes = maxFrameBytes }
    public mutating func feed(_ data: Data) throws -> [Data] {
        var output: [Data] = []
        for byte in data { try consumeByte(byte, &output) }
        return output
    }
    public mutating func feed(_ byte: UInt8) throws -> Data? {
        var output: [Data] = []; try consumeByte(byte, &output); return output.first
    }
    private mutating func consumeByte(_ byte: UInt8, _ output: inout [Data]) throws {
        if byte == 13 { try consumeLine(&output); previousCR = true }
        else if byte == 10 { if !previousCR { try consumeLine(&output) }; previousCR = false }
        else {
            previousCR = false; line.append(byte)
            if line.count + frameBytes > maxFrameBytes { try aguiFail("SSE frame exceeds configured limit") }
        }
    }
    private mutating func consumeLine(_ output: inout [Data]) throws {
        if firstLine { firstLine = false; if line.starts(with: [0xEF,0xBB,0xBF]) { line.removeFirst(3) } }
        if line.isEmpty {
            if !dataLines.isEmpty { var data = Data(); for (index,bytes) in dataLines.enumerated() { if index > 0 { data.append(10) }; data.append(contentsOf: bytes) }; output.append(data) }
            dataLines.removeAll(keepingCapacity: true); frameBytes = 0
        } else if line.starts(with: Array("data:".utf8)) {
            var bytes = Array(line.dropFirst(5)); if bytes.first == 32 { bytes.removeFirst() }
            frameBytes += bytes.count + 1; dataLines.append(bytes)
            if frameBytes > maxFrameBytes { try aguiFail("SSE frame exceeds configured limit") }
        }
        line.removeAll(keepingCapacity: true)
    }
    /// The SSE specification discards a frame without its terminating blank line.
    public mutating func finish() { line.removeAll(); dataLines.removeAll(); frameBytes = 0 }
}

/// Explicit endpoint URL. No Agently dependency or native-route fallback for standard runs.
public struct AgUiClient: Sendable {
    public let endpoint: URL
    public let headers: [String: String]
    public let session: URLSession
    public let maxFrameBytes: Int
    public let onResponse: (@Sendable (URLResponse) -> Void)?
    public init(endpoint: URL, headers: [String: String] = [:], session: URLSession = .shared, maxFrameBytes: Int = 64 * 1024 * 1024, onResponse: (@Sendable (URLResponse) -> Void)? = nil) {
        self.endpoint = endpoint; self.headers = headers; self.session = session; self.maxFrameBytes = maxFrameBytes; self.onResponse = onResponse
    }
    /// Cancelling the consuming task aborts transport; it does not claim backend cancellation.
    /// Separate resource request; consuming-task cancellation only disconnects HTTP.
    public func cancelRun(threadId: String, targetRunId: String, commandRunId: String) throws -> AsyncThrowingStream<AgUiUpdate, Error> {
        run(try AgentlyAgUiExtensions.cancelInput(threadId:threadId,targetRunId:targetRunId,commandRunId:commandRunId))
    }
    public func run(_ input: AgUiRunInput) -> AsyncThrowingStream<AgUiUpdate, Error> {
        AsyncThrowingStream { continuation in
            let task = Task {
                do {
                    var request = URLRequest(url: endpoint)
                    request.httpMethod = "POST"; request.timeoutInterval = 60 * 60 * 24
                    request.setValue("text/event-stream", forHTTPHeaderField: "Accept")
                    request.setValue("application/json", forHTTPHeaderField: "Content-Type")
                    for (key,value) in headers { request.setValue(value, forHTTPHeaderField: key) }
                    request.httpBody = try AgUiValue.object(input.value).encodedData()
                    let (bytes,response) = try await session.bytes(for: request)
                    onResponse?(response)
                    guard let http = response as? HTTPURLResponse else { try aguiFail("Invalid HTTP response") }
                    guard (200..<300).contains(http.statusCode) else { throw AgUiError.httpStatus(http.statusCode) }
                    guard http.value(forHTTPHeaderField: "Content-Type")?.components(separatedBy: ";").first?.trimmingCharacters(in: .whitespaces).lowercased() == "text/event-stream" else { try aguiFail("AG-UI response must be text/event-stream") }
                    let store = try AgUiStore(input: input); var parser = AgUiSSEParser(maxFrameBytes: maxFrameBytes)
                    for try await byte in bytes {
                        try Task.checkCancellation()
                        if let payload = try parser.feed(byte) {
                            guard let value = try AgUiValue.parse(payload).object else { try aguiFail("Expected event object") }
                            let raw = try AgUiEvent(value: value)
                            _ = try store.receive(raw) { event in continuation.yield(AgUiUpdate(sourceEvent: raw, event: event, snapshot: store.snapshot)) }
                        }
                    }
                    parser.finish()
                    for event in try store.finish() { continuation.yield(AgUiUpdate(sourceEvent: nil, event: event, snapshot: store.snapshot)) }
                    continuation.finish()
                } catch { continuation.finish(throwing: error) }
            }
            continuation.onTermination = { _ in task.cancel() }
        }
    }
}
