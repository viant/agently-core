import Foundation
import OSLog


public struct AgUiConversationBootstrap: Sendable {
    public let value: AgUiValue
    public let transcript: ConversationStateResponse
    public let messages: [AgUiMessage]
    public let state: AgUiValue
    public let runs: [AgUiValue]
    public let hostActivities: [AgUiMessage]
    public let unavailableHostActivityIDs: [String]
    public init(value: AgUiValue, conversationID: String, protocolThreadID: String? = nil) throws {
        let wireThreadID = protocolThreadID ?? conversationID
        guard value["version"]?.string == "1", value["threadId"]?.string == wireThreadID,
              let transcript = value["transcript"], transcript["schemaVersion"]?.string != nil,
              transcript["conversation"]?["conversationId"]?.string == conversationID,
              let messages = value["messages"]?.array, let runs = value["runs"]?.array,
              value.object?.keys.contains("state") == true,
              case .bool = value["projection"]?["lossless"],
              let unavailable = value["projection"]?["unavailableMessageIds"]?.array,
              unavailable.allSatisfy({ $0.string != nil }) else { try aguiFail("Malformed conversation bootstrap") }
        self.value = value
        self.transcript = try JSONDecoder.agently().decode(ConversationStateResponse.self, from: transcript.encodedData())
        self.messages = try messages.map { guard let object = $0.object else { try aguiFail("Invalid bootstrap message") }; return try AgUiMessage(value: object) }
        self.state = value["state"] ?? .null
        self.runs = runs
        self.hostActivities = try (value["hostActivities"]?.array ?? []).map {
            guard let object = $0.object else { try aguiFail("Invalid host activity") }
            let message = try AgUiMessage(value: object)
            guard message.role == "activity" else { try aguiFail("Host activity must have activity role") }; return message
        }
        self.unavailableHostActivityIDs = try (value["unavailableHostActivityIds"]?.array ?? []).map {
            guard let id = $0.string else { try aguiFail("Invalid unavailable host identity") }; return id
        }
        for run in runs {
            guard run["threadId"]?.string == wireThreadID, run["runId"]?.string != nil, run["status"]?.string != nil else { try aguiFail("Invalid bootstrap run reference") }
        }
    }
}

/// SDK-owned work survives a view detaching. Each protocol run uses AgUiClient's
/// typed reducer; ConversationStreamTracker is only the existing presentation DTO.
public actor AgUiConversationTransport {
    private let logger = Logger(subsystem: "com.viant.agently.sdk", category: "AgUiConversation")
    public typealias Run = @Sendable (AgUiRunInput) throws -> AsyncThrowingStream<AgUiUpdate, Error>
    private weak var host: AgentlyClient?
    public typealias NativeIdentity = @Sendable (String) async throws -> Conversation
    private let nativeIdentityOverride: NativeIdentity?
    private let runOverride: Run?
    private let epoch = AgUiTransportEpoch()
    private final class Entry {
        var protocolThreadID: String?
        let id: String; let generation: Int
        let tracker = ConversationStreamTracker()
        var bootstrap: AgUiConversationBootstrap?
        var loading: Task<AgUiConversationBootstrap, Error>?
        var readRevision = 0
        var publicationRevision = 0
        var listeners: [UUID: AsyncThrowingStream<ConversationStreamSnapshot, Error>.Continuation] = [:]
        var runs: [String: Task<Void, Never>] = [:]
        var terminalNativeTurns: Set<String> = []
        var observedTerminalRuns: Set<String> = []
        var nativeRunIDs: [String: String] = [:]
        var notifications: Task<Void, Never>?
        var hostActivities: [AgUiMessage] = []
        var unavailableHostActivityIDs: [String] = []
        init(id: String, generation: Int) { self.id = id; self.generation = generation }
    }
    private var entries: [String: Entry] = [:]
    public init(client: AgentlyClient, run: Run? = nil, nativeIdentity: NativeIdentity? = nil) { host = client; runOverride = run; nativeIdentityOverride = nativeIdentity }
    private func entry(_ id: String) throws -> Entry {
        guard epoch.value == 0 else { throw CancellationError() }
        guard !id.isEmpty else { try aguiFail("Conversation identity required") }
        if let entry = entries[id] { return entry }
        let entry = Entry(id: id, generation: epoch.value); entries[id] = entry; return entry
    }
    private func current(_ entry: Entry) -> Bool { entry.generation == epoch.value && entries[entry.id] === entry }
    private func run(_ input: AgUiRunInput) throws -> AsyncThrowingStream<AgUiUpdate, Error> {
        if let runOverride { return try runOverride(input) }
        guard let host else { try aguiFail("Conversation client released") }
        return try host.agUiClient().run(input)
    }

    /// Recover a transport drop with the exact immutable POST identity/input.
    /// Schema, sequence, authorization and server RUN_ERROR failures never retry.
    private func replayableRun(_ input: AgUiRunInput) -> AsyncThrowingStream<AgUiUpdate, Error> {
        let fence = epoch.value
        return AsyncThrowingStream { continuation in
            let task = Task {
                for attempt in 0..<2 {
                    do {
                        for try await update in try self.run(input) {
                            guard fence == self.epoch.value, !Task.isCancelled else { throw CancellationError() }
                            continuation.yield(update)
                        }
                        continuation.finish(); return
                    } catch {
                        let incomplete = error as? AgUiError == .invalidProtocol("SSE ended without terminal event")
                        if attempt == 0, (error is URLError || incomplete), !Task.isCancelled, fence == self.epoch.value { continue }
                        continuation.finish(throwing: error); return
                    }
                }
            }
            continuation.onTermination = { _ in task.cancel() }
        }
    }

    private func resolveProtocolThread(_ entry: Entry) async throws -> String {
        if let wire = entry.protocolThreadID { return wire }
        guard let host else { throw CancellationError() }
        let conversation: Conversation
        if let nativeIdentityOverride { conversation = try await nativeIdentityOverride(entry.id) }
        else { conversation = try await host.getConversation(conversationID: entry.id) }
        guard current(entry), conversation.id == entry.id else { try aguiFail("Native conversation binding mismatch") }
        let wire = conversation.aguiThreadID ?? entry.id
        guard !wire.isEmpty else { try aguiFail("Empty protocol thread binding") }
        entry.protocolThreadID = wire
        return wire
    }

    public func command(operation: String, conversationID: String, payload: AgUiValue, requestID: String = UUID().uuidString) async throws -> AgUiValue {
        let fence = epoch.value
        guard fence == 0 else { throw CancellationError() }
        let wireThreadID = try await resolveProtocolThread(entry(conversationID))
        let input = try AgUiRunInput(threadId: wireThreadID, runId: requestID, messages: [], tools: [], context: [], forwardedProps: AgentlyAgUiExtensions.command(operation: operation, requestId: requestID, payload: payload))
        var result: AgUiValue?, terminal = false
        for try await update in replayableRun(input) {
            guard fence == epoch.value else { throw CancellationError() }
            if update.event.type == "RUN_ERROR" { try aguiFail(update.event.value["message"]?.string ?? "Command failed") }
            if update.event.type == "RUN_FINISHED" {
                guard update.event.value["outcome"]?["type"]?.string == "success" else { try aguiFail("Command did not succeed") }
                terminal = true; result = update.event.value["result"]
            }
        }
        guard fence == epoch.value, terminal, let result else { try aguiFail("Command ended without a result") }
        return result
    }

    public func refresh(conversationID: String) async throws -> AgUiConversationBootstrap {
        let entry = try entry(conversationID)
        if let pending = entry.loading { return try await pending.value }
        entry.readRevision += 1
        let revision = entry.readRevision
        let pending = Task { try await self.readBootstrap(entry) }
        entry.loading = pending
        do {
            let result = try await pending.value
            guard current(entry) else { throw CancellationError() }
            if entry.readRevision == revision { entry.loading = nil }
            return result
        } catch { if current(entry), entry.readRevision == revision { entry.loading = nil }; throw error }
    }
    /// A hint following a command waits for an older read, then starts a fresh read.
    public func reconcile(conversationID: String) async throws -> AgUiConversationBootstrap {
        let entry = try entry(conversationID)
        if let older = entry.loading {
            let revision = entry.readRevision
            _ = try await older.value; guard current(entry) else { throw CancellationError() }
            if entry.readRevision == revision { entry.loading = nil }
        }
        return try await refresh(conversationID: conversationID)
    }
    private func readBootstrap(_ entry: Entry) async throws -> AgUiConversationBootstrap {
        let result = try await command(operation: "conversation.bootstrap", conversationID: entry.id, payload: .object(["mode": .string("live"), "includeModelCalls": .bool(true), "includeToolCalls": .bool(true), "includeFeeds": .bool(true)]))
        let bootstrap = try AgUiConversationBootstrap(value: result, conversationID: entry.id, protocolThreadID: entry.protocolThreadID)
        guard current(entry) else { throw CancellationError() }
        entry.terminalNativeTurns = Set((bootstrap.transcript.conversation?.turns ?? []).filter { ["completed", "succeeded", "failed", "canceled", "cancelled"].contains($0.status ?? "") }.map { $0.turnID })
        entry.publicationRevision += 1
        entry.bootstrap = bootstrap; entry.hostActivities = bootstrap.hostActivities
        entry.unavailableHostActivityIDs = bootstrap.unavailableHostActivityIDs
        await entry.tracker.hydrate(bootstrap.transcript)
        await publish(entry)
        if !entry.listeners.isEmpty { attachKnownRuns(entry, bootstrap: bootstrap) }
        return bootstrap
    }
    public func hostActivities(conversationID: String) -> [AgUiMessage] { entries[conversationID]?.hostActivities ?? [] }
    public func unavailableHostActivityIDs(conversationID: String) -> [String] { entries[conversationID]?.unavailableHostActivityIDs ?? [] }

    public nonisolated func subscribe(conversationID: String) -> AsyncThrowingStream<ConversationStreamSnapshot, Error> {
        AsyncThrowingStream { continuation in
            let id = UUID()
            Task { await self.addListener(id, conversationID: conversationID, continuation: continuation) }
            continuation.onTermination = { _ in Task { await self.removeListener(id, conversationID: conversationID) } }
        }
    }
    private func addListener(_ id: UUID, conversationID: String, continuation: AsyncThrowingStream<ConversationStreamSnapshot, Error>.Continuation) async {
        do {
            let entry = try entry(conversationID); entry.listeners[id] = continuation
            if entry.bootstrap != nil { continuation.yield(await entry.tracker.currentSnapshot()) }
            try await refreshObservation(entry)
            guard current(entry), entry.listeners[id] != nil else { return }
            startNotifications(entry)
        } catch { continuation.finish(throwing: error) }
    }
    private func removeListener(_ id: UUID, conversationID: String) {
        guard let entry = entries[conversationID] else { return }; entry.listeners.removeValue(forKey: id)
        if entry.listeners.isEmpty { entry.notifications?.cancel(); entry.notifications = nil }
        // Submitted runs are owned by the coordinator, not this view.
    }
    private func refreshObservation(_ entry: Entry, fresh: Bool = false) async throws {
        let revision = entry.publicationRevision
        do {
            if fresh { _ = try await reconcile(conversationID: entry.id) }
            else { _ = try await refresh(conversationID: entry.id) }
        } catch AgUiError.httpStatus(403) {
            guard let host else { throw CancellationError() }
            let transcript = try await host.readConversationHistory(GetTranscriptInput(conversationID: entry.id))
            guard current(entry), entry.publicationRevision == revision, transcript.conversation?.conversationID == entry.id else { throw CancellationError() }
            entry.publicationRevision += 1
            entry.bootstrap = nil; entry.hostActivities = []; entry.unavailableHostActivityIDs = []
            await entry.tracker.hydrate(transcript)
            await publish(entry)
        }
    }
    private func publish(_ entry: Entry) async {
        let snapshot = await entry.tracker.currentSnapshot()
        guard current(entry) else { return }
        for listener in entry.listeners.values { listener.yield(snapshot) }
    }

    public func query(_ query: QueryInput) async throws -> QueryOutput {
        guard query.elicitationMode == nil || query.elicitationMode == "deferred" else { try aguiFail("AG-UI uses deferred interrupts") }
        guard let host else { try aguiFail("Conversation client released") }
        let conversationID: String
        if let id = query.conversationID { conversationID = id }
        else { conversationID = try await host.createConversation(CreateConversationInput(agentID: query.agentID)).id }
        let entry = try entry(conversationID), bootstrap = try await refresh(conversationID: conversationID)
        guard current(entry) else { throw CancellationError() }
        let runID = UUID().uuidString, messageID = query.messageID ?? UUID().uuidString
        var selection = try AgUiValue.parse(JSONEncoder.agently().encode(query)).object ?? [:]
        selection.removeValue(forKey: "query"); selection.removeValue(forKey: "conversationId"); selection.removeValue(forKey: "messageId")
        selection["backendTools"] = selection.removeValue(forKey: "tools"); selection["useServerState"] = .bool(true)
        let props: AgUiValue = .object(["agently": .object(["version": .string("1"), "operation": .string("chat"), "requestId": .string(messageID), "payload": .object(selection)])])
        logger.info("Submit chat thread=\(conversationID, privacy: .public) protocolRun=\(runID, privacy: .public) clientRequest=\(messageID, privacy: .public)")
        let input = try AgUiRunInput(threadId: entry.protocolThreadID ?? conversationID, runId: runID, messages: bootstrap.messages + [AgUiMessage.user(id: messageID, content: .string(query.query))], state: bootstrap.state == .null ? .object([:]) : bootstrap.state, tools: [], context: [], forwardedProps: props)
        return try await withCheckedThrowingContinuation { continuation in
            entry.runs[runID] = Task { await self.consume(entry, input: input, baseline: bootstrap.messages, clientMessageID: messageID, admission: continuation) }
        }
    }
    private func attachKnownRuns(_ entry: Entry, bootstrap: AgUiConversationBootstrap) {
        for run in bootstrap.runs {
            guard let id = run["runId"]?.string, entry.runs[id] == nil,
                  !entry.observedTerminalRuns.contains(id), run["kind"]?.string != "resource", run["kind"]?.string != "mcp-app" else { continue }
            do {
                let input = try AgUiRunInput(threadId: entry.protocolThreadID ?? entry.id, runId: id, messages: [], tools: [], context: [], forwardedProps: AgentlyAgUiExtensions.command(operation: "run.attach", requestId: UUID().uuidString, payload: .object([:])))
                entry.runs[id] = Task { await self.consume(entry, input: input, baseline: bootstrap.messages, clientMessageID: nil, admission: nil) }
            } catch { fail(entry, error) }
        }
    }
    private func consume(_ entry: Entry, input: AgUiRunInput, baseline: [AgUiMessage], clientMessageID: String?, admission: CheckedContinuation<QueryOutput, Error>?) async {
        var pending = admission, projector = AgUiPresentationProjector(conversationID: entry.id, runID: input.runId, baseline: baseline)
        var terminal: AgUiEvent?
        do {
            for try await update in replayableRun(input) {
                guard current(entry), !Task.isCancelled else { throw CancellationError() }
                if let messageID = clientMessageID, let turn = projector.admittedTurn(update, clientMessageID: messageID) {
                    entry.nativeRunIDs[turn] = input.runId
                    if pending != nil { logger.info("Admit chat protocolRun=\(input.runId, privacy: .public) clientRequest=\(messageID, privacy: .public) nativeTurn=\(turn, privacy: .public)") }
                    pending?.resume(returning: QueryOutput(conversationID: entry.id, messageID: turn)); pending = nil
                }
                if update.event.type == "RUN_STARTED", update.event.value["subagentRunId"] == nil,
                   let turn = AgUiPresentationProjector.presentation(update.event.value["metadata"])?["nativeTurnId"]?.string {
                    entry.nativeRunIDs[turn] = input.runId
                }
                for event in try projector.project(update) {
                    if let value = try? AgUiValue.parse(event.data), let turn = value["turnId"]?.string,
                       entry.terminalNativeTurns.contains(turn),
                       let type = value["type"]?.string,
                       type.hasPrefix("turn_") || type.hasPrefix("model_") || type.hasPrefix("tool_call_") || ["text_delta", "reasoning_delta", "message_appended", "narration"].contains(type) { continue }
                    _ = await entry.tracker.apply(event)
                }
                if !projector.hostActivities.isEmpty {
                    var activities = Dictionary(uniqueKeysWithValues: entry.hostActivities.map { ($0.id, $0) })
                    for activity in projector.hostActivities { activities[activity.id] = activity }
                    entry.hostActivities = activities.values.sorted { $0.id < $1.id }
                }
                await publish(entry)
                if update.event.type == "RUN_ERROR" { try aguiFail(update.event.value["message"]?.string ?? "Run failed") }
                if update.event.type == "RUN_FINISHED" { terminal = update.event }
            }
            guard current(entry), let terminal else { try aguiFail("Run ended without terminal event") }
            guard terminal.value["outcome"]?["type"]?.string != "cancelled" else { throw CancellationError() }
            pending?.resume(returning: QueryOutput(conversationID: entry.id)); pending = nil
        } catch {
            pending?.resume(throwing: error); pending = nil
            if current(entry), !(error is CancellationError) { fail(entry, error) }
        }
        if terminal != nil { entry.observedTerminalRuns.insert(input.runId) }
        entry.runs.removeValue(forKey: input.runId)
        // Interrupt remains waiting. Bootstrap discovers explicit successors and
        // restores native state; it never resubmits an interrupted chat input.
        if current(entry), terminal != nil, !entry.listeners.isEmpty {
            do { _ = try await reconcile(conversationID: entry.id) } catch { if current(entry) { fail(entry, error) } }
        }
    }
    public func cancel(conversationID: String, nativeTurnID: String) async throws {
        let entry = try entry(conversationID)
        var runID = entry.nativeRunIDs[nativeTurnID]
        if runID == nil {
            let bootstrap = try await reconcile(conversationID: conversationID)
            runID = bootstrap.runs.first(where: { $0["nativeTurnId"]?.string == nativeTurnID })?["runId"]?.string
        }
        guard let runID else { try aguiFail("Active native turn has no owned protocol run") }
        _ = try await command(operation: "run.cancel", conversationID: conversationID, payload: .object(["runId": .string(runID)]))
        _ = try await reconcile(conversationID: conversationID)
    }
    private func startNotifications(_ entry: Entry) {
        guard entry.notifications == nil, runOverride == nil, let host else { return }
        entry.notifications = Task {
            do {
                for try await event in host.streamApplicationEvents(conversationID: entry.id) {
                    guard self.current(entry), !Task.isCancelled else { return }
                    guard let value = try? AgUiValue.parse(event.data), let type = value["type"]?.string else { continue }
                    if type == "compatibility_reconcile" || (["turn_completed", "turn_failed", "turn_canceled"].contains(type)) || (type == "conversation_meta_updated" && value["patch"]?["aguiUpdated"] == .bool(true)) {
                        try await self.refreshObservation(entry, fresh: true)
                    } else { _ = await entry.tracker.apply(event); await self.publish(entry) }
                }
            } catch { if self.current(entry), !Task.isCancelled { self.fail(entry, error) } }
            entry.notifications = nil
        }
    }
    private func fail(_ entry: Entry, _ error: Error) {
        guard current(entry) else { return }
        for listener in entry.listeners.values { listener.finish(throwing: error) }; entry.listeners.removeAll()
    }
    public nonisolated func invalidate() { epoch.invalidate() }
    public func reset() {
        epoch.invalidate()
        for entry in entries.values {
            entry.loading?.cancel(); entry.notifications?.cancel()
            for task in entry.runs.values { task.cancel() }
            for listener in entry.listeners.values { listener.finish(throwing: CancellationError()) }
        }
        entries.removeAll()
    }
}

final class AgUiConversationTransportStorage: @unchecked Sendable {
    private let lock = NSLock()
    private var transport: AgUiConversationTransport?
    func get(client: AgentlyClient) -> AgUiConversationTransport {
        lock.lock(); defer { lock.unlock() }
        if let transport { return transport }
        let created = AgUiConversationTransport(client: client); transport = created; return created
    }
    func reset() {
        lock.lock(); let previous = transport; transport = nil; lock.unlock()
        if let previous { previous.invalidate(); Task { await previous.reset() } }
    }
}

final class AgUiTransportEpoch: @unchecked Sendable {
    private let lock = NSLock()
    private var generation = 0
    var value: Int { lock.lock(); defer { lock.unlock() }; return generation }
    func invalidate() { lock.lock(); generation += 1; lock.unlock() }
}
