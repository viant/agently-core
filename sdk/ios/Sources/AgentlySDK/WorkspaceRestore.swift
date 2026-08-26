import Foundation

public func deriveHostedWorkspaceRestoreState(
    from response: ConversationStateResponse
) -> HostedWorkspaceRestoreState? {
    let toolSteps = response.conversation?.turns.flatMap { turn in
        turn.execution?.pages.flatMap(\.toolSteps).map(HostedWorkspaceToolStep.init) ?? []
    } ?? []
    guard !toolSteps.isEmpty else { return nil }
    return deriveHostedWorkspaceRestoreState(from: toolSteps)
}

public func deriveHostedWorkspaceRestoreState(
    from response: ConversationStateResponse?,
    streamSnapshot: ConversationStreamSnapshot?
) -> HostedWorkspaceRestoreState? {
    if let streamSnapshot,
       streamSnapshot.activeTurnID?.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty == false {
        return deriveHostedWorkspaceRestoreState(from: streamSnapshot)
    }
    if let streamSnapshot, !streamSnapshot.liveExecutionGroupsByID.isEmpty {
        return nil
    }
    return response.flatMap { deriveHostedWorkspaceRestoreState(from: $0) }
}

public func deriveHostedWorkspaceRestoreState(
    from snapshot: ConversationStreamSnapshot
) -> HostedWorkspaceRestoreState? {
    let targetTurnID = snapshot.activeTurnID?.trimmedNonEmpty ?? ""
    guard !targetTurnID.isEmpty else {
        return nil
    }
    let groups = liveHostedWorkspaceGroups(snapshot, targetTurnID: targetTurnID)
    guard !groups.isEmpty else {
        return nil
    }
    let toolSteps = groups.flatMap { group in
        group.toolSteps.map(HostedWorkspaceToolStep.init)
    }
    return deriveHostedWorkspaceRestoreState(from: toolSteps)
}

private struct HostedWorkspaceToolStep {
    let toolName: String?
    let status: String?
    let content: String?
    let requestPayload: JSONValue?
    let responsePayload: JSONValue?

    init(_ step: ToolStepState) {
        self.toolName = step.toolName
        self.status = step.status
        self.content = step.content
        self.requestPayload = step.requestPayload
        self.responsePayload = step.responsePayload
    }

    init(_ step: LiveToolStepState) {
        self.toolName = step.toolName
        self.status = step.status
        self.content = nil
        self.requestPayload = step.requestPayload
        self.responsePayload = step.responsePayload
    }
}

private func deriveHostedWorkspaceRestoreState(
    from toolSteps: [HostedWorkspaceToolStep]
) -> HostedWorkspaceRestoreState? {
    let completedToolSteps = toolSteps.filter {
        ($0.status ?? "").trimmingCharacters(in: .whitespacesAndNewlines).lowercased() == "completed"
    }
    for index in completedToolSteps.indices.reversed() {
        let step = completedToolSteps[index]
        let allCompletedToolSteps = completedToolSteps[...]
        let toolName = normalizeHostedWorkspaceToolName(step.toolName)
        if toolName == "ui/window/list" {
            let windows = applyWindowFormDataPatches(
                hostedWorkspaceWindowsFromListPayload(
                    firstParsedPayload(step.responsePayload, step.content)
                ),
                allCompletedToolSteps
            )
            if !windows.isEmpty {
                let selectedWindowID = selectedWindowIDFromToolSteps(completedToolSteps, windows)
                return HostedWorkspaceRestoreState(
                    windows: windows,
                    selectedWindowId: selectedWindowID.isEmpty ? windows.last?.windowId : selectedWindowID
                )
            }
        }
        if toolName == "ui/view/open" || toolName == "ui/window/open" {
            let windows = applyWindowFormDataPatches(
                hostedWorkspaceWindowsFromViewOpenStep(step),
                allCompletedToolSteps
            )
            if !windows.isEmpty {
                let responsePayload = firstParsedPayload(step.responsePayload, step.content)
                let selectedWindowID = responsePayload?.objectValue?["selectedWindowId"]?.stringValue
                    ?? windows.last?.windowId
                return HostedWorkspaceRestoreState(
                    windows: windows,
                    selectedWindowId: selectedWindowID?.isEmpty == false ? selectedWindowID : nil
                )
            }
        }
    }
    return nil
}

private func applyWindowFormDataPatches(
    _ windows: [WorkspaceWindowSnapshot],
    _ toolSteps: ArraySlice<HostedWorkspaceToolStep>
) -> [WorkspaceWindowSnapshot] {
    guard !windows.isEmpty, !toolSteps.isEmpty else {
        return windows
    }
    var patchedWindows = windows
    for step in toolSteps {
        if normalizeHostedWorkspaceToolName(step.toolName) == "ui/window/get",
           let snapshot = hostedWorkspaceWindowFromGetStep(step) {
            let targets = hostedWorkspaceFormPatchTargets(
                windows: patchedWindows,
                windowID: snapshot.windowId,
                windowKey: snapshot.windowKey
            )
            if !targets.isEmpty {
                patchedWindows = patchedWindows.map { window in
                    targets.contains(window.windowId) ? snapshot : window
                }
            }
            continue
        }
        guard normalizeHostedWorkspaceToolName(step.toolName) == "ui/window/setformdata" else {
            continue
        }
        let requestPayload = firstParsedPayload(step.requestPayload, nil)?.objectValue ?? [:]
        let responsePayload = firstParsedPayload(step.responsePayload, step.content)?.objectValue ?? [:]
        let windowID = responsePayload["windowId"]?.stringValue
            ?? requestPayload["windowId"]?.stringValue
            ?? ""
        let windowKey = responsePayload["windowKey"]?.stringValue
            ?? requestPayload["windowKey"]?.stringValue
            ?? ""
        let authoritativeWindowForm = responsePayload["windowForm"]?.objectValue
        guard let nextValues = authoritativeWindowForm
            ?? requestPayload["values"]?.objectValue
            ?? requestPayload["parameters"]?.objectValue else {
            continue
        }
        let targetWindowIDs = hostedWorkspaceFormPatchTargets(
            windows: patchedWindows,
            windowID: windowID,
            windowKey: windowKey
        )
        guard !targetWindowIDs.isEmpty else {
            continue
        }
        let replace = requestPayload["replace"]?.boolValue == true
        patchedWindows = patchedWindows.map { window in
            guard targetWindowIDs.contains(window.windowId) else {
                return window
            }
            let nextWindowForm = authoritativeWindowForm != nil || replace
                ? nextValues
                : mergeJSONObjects(
                    base: invalidateDerivedReportBuilderState(
                        current: window.windowForm ?? [:],
                        patch: nextValues
                    ),
                    override: nextValues
                )
            return replacingWindowForm(window, windowForm: nextWindowForm)
        }
    }
    return patchedWindows
}

private func invalidateDerivedReportBuilderState(
    current: [String: JSONValue],
    patch: [String: JSONValue]
) -> [String: JSONValue] {
    guard patch["reportDefinition"]?.objectValue != nil else { return current }
    return current.filter { !$0.key.hasPrefix("reportBuilder:") }
}

private func hostedWorkspaceWindowFromGetStep(
    _ step: HostedWorkspaceToolStep
) -> WorkspaceWindowSnapshot? {
    guard let payload = firstParsedPayload(step.responsePayload, step.content)?.objectValue else {
        return nil
    }
    return normalizeHostedWorkspaceWindow(payload["window"]?.objectValue ?? payload)
}

private func hostedWorkspaceFormPatchTargets(
    windows: [WorkspaceWindowSnapshot],
    windowID: String,
    windowKey: String
) -> Set<String> {
    let normalizedWindowID = windowID.trimmingCharacters(in: .whitespacesAndNewlines)
    if !normalizedWindowID.isEmpty {
        return Set(windows.filter { $0.windowId == normalizedWindowID }.map(\.windowId))
    }
    let normalizedWindowKey = windowKey.trimmingCharacters(in: .whitespacesAndNewlines)
    guard !normalizedWindowKey.isEmpty else {
        return []
    }
    let matches = windows.filter { $0.windowKey == normalizedWindowKey }
    return matches.count == 1 ? Set(matches.map(\.windowId)) : []
}

private func replacingWindowForm(
    _ window: WorkspaceWindowSnapshot,
    windowForm: [String: JSONValue]
) -> WorkspaceWindowSnapshot {
    WorkspaceWindowSnapshot(
        windowId: window.windowId,
        conversationId: window.conversationId,
        windowKey: window.windowKey,
        windowTitle: window.windowTitle,
        presentation: window.presentation,
        region: window.region,
        parentKey: window.parentKey,
        workspaceSharePct: window.workspaceSharePct,
        workspaceMinHeight: window.workspaceMinHeight,
        inTab: window.inTab,
        parameters: window.parameters,
        windowForm: windowForm
    )
}

private func mergeJSONObjects(
    base: [String: JSONValue],
    override: [String: JSONValue]
) -> [String: JSONValue] {
    var merged = base
    for (key, value) in override {
        if case .object(let currentObject)? = merged[key],
           case .object(let overrideObject) = value {
            merged[key] = .object(mergeJSONObjects(base: currentObject, override: overrideObject))
        } else {
            merged[key] = value
        }
    }
    return merged
}

private func liveHostedWorkspaceGroups(
    _ snapshot: ConversationStreamSnapshot,
    targetTurnID: String
) -> [LiveExecutionGroup] {
    snapshot.liveExecutionGroupsByID.values
        .filter { group in
            group.turnID == targetTurnID
        }
        .sorted {
            let lhsSequence = $0.sequence ?? Int.max
            let rhsSequence = $1.sequence ?? Int.max
            if lhsSequence != rhsSequence { return lhsSequence < rhsSequence }
            let lhsIteration = $0.iteration ?? Int.max
            let rhsIteration = $1.iteration ?? Int.max
            if lhsIteration != rhsIteration { return lhsIteration < rhsIteration }
            return $0.pageID < $1.pageID
        }
}

private func hostedWorkspaceWindowsFromListPayload(_ raw: JSONValue?) -> [WorkspaceWindowSnapshot] {
    guard let payload = raw,
          let items = payload.objectValue?["items"]?.arrayValue else {
        return []
    }
    return items.compactMap { normalizeHostedWorkspaceWindow($0.objectValue) }
}

private func hostedWorkspaceWindowsFromViewOpenStep(_ step: HostedWorkspaceToolStep) -> [WorkspaceWindowSnapshot] {
    let responsePayload = firstParsedPayload(step.responsePayload, step.content)
    if let items = responsePayload?.objectValue?["items"]?.arrayValue, !items.isEmpty {
        return items.compactMap { normalizeHostedWorkspaceWindow($0.objectValue) }
    }
    let requestPayload = firstParsedPayload(step.requestPayload, nil)
    var merged: [String: JSONValue] = [
        "windowId": responsePayload?.objectValue?["windowId"] ?? .string(""),
        "conversationId": responsePayload?.objectValue?["conversationId"] ?? .string(""),
        "windowKey": responsePayload?.objectValue?["windowKey"]
            ?? requestPayload?.objectValue?["id"]
            ?? requestPayload?.objectValue?["windowKey"]
            ?? .string(""),
        "windowTitle": responsePayload?.objectValue?["windowTitle"] ?? .string(""),
        "presentation": responsePayload?.objectValue?["presentation"] ?? .string(""),
        "region": responsePayload?.objectValue?["region"] ?? .string(""),
        "parentKey": responsePayload?.objectValue?["parentKey"] ?? .string(""),
        "parameters": requestPayload?.objectValue?["parameters"] ?? .object([:])
    ]
    if let share = responsePayload?.objectValue?["workspaceSharePct"] {
        merged["workspaceSharePct"] = share
    }
    if let minHeight = responsePayload?.objectValue?["workspaceMinHeight"] {
        merged["workspaceMinHeight"] = minHeight
    }
    if let inTab = responsePayload?.objectValue?["inTab"] {
        merged["inTab"] = inTab
    }
    if let windowForm = responsePayload?.objectValue?["windowForm"] {
        merged["windowForm"] = windowForm
    }
    return [normalizeHostedWorkspaceWindow(merged)].compactMap { $0 }
}

private func normalizeHostedWorkspaceWindow(_ raw: [String: JSONValue]?) -> WorkspaceWindowSnapshot? {
    guard let raw else { return nil }
    let parentKey = raw["parentKey"]?.stringValue ?? ""
    let windowID = raw["windowId"]?.stringValue ?? ""
    let windowKey = raw["windowKey"]?.stringValue ?? ""
    guard !windowID.isEmpty, !windowKey.isEmpty else { return nil }
    var windowForm = raw["windowForm"]?.objectValue ?? [:]
    if let metadata = raw["metadata"] {
        windowForm["__agentlyWindowMetadata"] = metadata
    }
    return WorkspaceWindowSnapshot(
        windowId: windowID,
        conversationId: raw["conversationId"]?.stringValue?.nonEmpty,
        windowKey: windowKey,
        windowTitle: raw["windowTitle"]?.stringValue?.nonEmpty ?? windowKey,
        presentation: raw["presentation"]?.stringValue?.nonEmpty,
        region: raw["region"]?.stringValue?.nonEmpty,
        parentKey: parentKey,
        workspaceSharePct: raw["workspaceSharePct"]?.intValue,
        workspaceMinHeight: raw["workspaceMinHeight"]?.intValue,
        inTab: raw["inTab"]?.boolValue ?? true,
        parameters: raw["parameters"]?.objectValue,
        windowForm: windowForm.isEmpty ? nil : windowForm
    )
}

private func selectedWindowIDFromToolSteps(
    _ toolSteps: [HostedWorkspaceToolStep],
    _ windows: [WorkspaceWindowSnapshot]
) -> String {
    let windowIDs = Set(windows.map(\.windowId))
    for step in toolSteps.reversed() {
        let toolName = normalizeHostedWorkspaceToolName(step.toolName)
        if toolName == "ui/window/show" {
            let requestPayload = firstParsedPayload(step.requestPayload, nil)
            let windowID = requestPayload?.objectValue?["windowId"]?.stringValue ?? ""
            if windowIDs.contains(windowID) {
                return windowID
            }
        }
        if toolName == "ui/window/list" {
            let responsePayload = firstParsedPayload(step.responsePayload, step.content)
            let focusedWindowID = responsePayload?.objectValue?["focusedWindowId"]?.stringValue ?? ""
            if windowIDs.contains(focusedWindowID) {
                return focusedWindowID
            }
        }
    }
    return ""
}

private func firstParsedPayload(_ raw: JSONValue?, _ rawText: String?) -> JSONValue? {
    var candidates: [JSONValue] = []
    if let raw {
        candidates.append(raw)
    }
    if let rawText,
       !rawText.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
        candidates.append(.string(rawText))
    }
    for candidate in candidates {
        guard let parsed = parsePayload(candidate) else {
            continue
        }
        if isPayloadEnvelope(parsed) {
            continue
        }
        return parsed
    }
    return nil
}

private func parsePayload(_ raw: JSONValue) -> JSONValue? {
    switch raw {
    case .string(let value):
        let trimmed = value.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty, let data = trimmed.data(using: .utf8) else { return nil }
        return try? JSONDecoder.agently().decode(JSONValue.self, from: data)
    case .object(let object):
        if let inlineBody = object["inlineBody"]?.stringValue ?? object["InlineBody"]?.stringValue,
           !inlineBody.isEmpty,
           let data = inlineBody.data(using: .utf8),
           let decoded = try? JSONDecoder.agently().decode(JSONValue.self, from: data) {
            return decoded
        }
        return raw
    default:
        return raw
    }
}

private func isPayloadEnvelope(_ value: JSONValue) -> Bool {
    guard let object = value.objectValue else {
        return false
    }
    let hasInlineBody = object["inlineBody"]?.stringValue != nil || object["InlineBody"]?.stringValue != nil
    let hasCompression = object["compression"]?.stringValue != nil || object["Compression"]?.stringValue != nil
    let hasDirectWorkspaceShape = object["items"] != nil || object["windowId"] != nil || object["focusedWindowId"] != nil
    return (hasInlineBody || hasCompression) && !hasDirectWorkspaceShape
}

private func normalizeHostedWorkspaceToolName(_ raw: String?) -> String {
    String(raw ?? "")
        .trimmingCharacters(in: .whitespacesAndNewlines)
        .lowercased()
        .replacingOccurrences(of: ":", with: "/")
        .replacingOccurrences(of: ".", with: "/")
}

private extension JSONValue {
    var stringValue: String? {
        if case .string(let value) = self {
            return value.trimmingCharacters(in: .whitespacesAndNewlines)
        }
        return nil
    }

    var objectValue: [String: JSONValue]? {
        if case .object(let value) = self {
            return value
        }
        return nil
    }

    var arrayValue: [JSONValue]? {
        if case .array(let value) = self {
            return value
        }
        return nil
    }

    var intValue: Int? {
        switch self {
        case .number(let value):
            return Int(value)
        case .string(let value):
            return Int(value.trimmingCharacters(in: .whitespacesAndNewlines))
        default:
            return nil
        }
    }

    var boolValue: Bool? {
        switch self {
        case .bool(let value):
            return value
        case .string(let value):
            switch value.trimmingCharacters(in: .whitespacesAndNewlines).lowercased() {
            case "true":
                return true
            case "false":
                return false
            default:
                return nil
            }
        default:
            return nil
        }
    }
}

private extension String {
    var trimmedNonEmpty: String? {
        let trimmed = trimmingCharacters(in: .whitespacesAndNewlines)
        return trimmed.isEmpty ? nil : trimmed
    }

    var nonEmpty: String? {
        isEmpty ? nil : self
    }
}
