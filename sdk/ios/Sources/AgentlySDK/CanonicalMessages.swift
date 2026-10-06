import Foundation

/// User-facing assistant messages deduplicated by exact canonical message ID.
public func canonicalAssistantMessages(_ turn: ConversationTurn) -> [TurnMessageState] {
    let hidden = canonicalInternalAssistantMessageIDs(turn)
    var ordered: [TurnMessageState] = []
    func put(_ message: TurnMessageState) {
        guard !message.messageID.isEmpty, !hidden.contains(message.messageID) else { return }
        if let index = ordered.firstIndex(where: { $0.messageID == message.messageID }) {
            let prior = ordered[index]
            ordered[index] = TurnMessageState(messageID: message.messageID, role: message.role,
                content: message.content ?? prior.content, renderedContent: message.renderedContent ?? prior.renderedContent,
                createdAt: message.createdAt ?? prior.createdAt, sequence: message.sequence ?? prior.sequence,
                interim: message.interim ?? prior.interim, mode: message.mode ?? prior.mode, status: message.status ?? prior.status)
        }
        else { ordered.append(message) }
    }
    for message in turn.messages where message.role == "assistant" { put(message) }
    func merge(_ value: AssistantMessageState?) {
        guard let value, !hidden.contains(value.messageID) else { return }
        let prior = ordered.first(where: { $0.messageID == value.messageID })
        put(TurnMessageState(messageID: value.messageID, role: "assistant", content: value.content?.isEmpty == false ? value.content : prior?.content,
            renderedContent: value.renderedContent ?? prior?.renderedContent, createdAt: prior?.createdAt ?? value.createdAt,
            sequence: prior?.sequence, interim: prior?.interim, mode: prior?.mode, status: prior?.status ?? turn.status))
    }
    for message in turn.assistant?.messages ?? [] { merge(message) }
    if let narration = turn.assistant?.narration, !ordered.contains(where: { $0.messageID == narration.messageID }),
       let finalID = turn.assistant?.final?.messageID, let finalIndex = ordered.firstIndex(where: { $0.messageID == finalID }) {
        let final = ordered.remove(at: finalIndex)
        merge(narration); put(final)
    } else { merge(turn.assistant?.narration) }
    merge(turn.assistant?.final)
    return ordered
}

func canonicalInternalAssistantMessageIDs(_ turn: ConversationTurn) -> Set<String> {
    var ids = Set(turn.messages.filter { isInternalMessageMode($0.mode) }.map(\.messageID))
    for page in turn.execution?.pages ?? [] {
        let hidden = isInternalMessageMode(page.mode)
        if hidden, let id = page.assistantMessageID { ids.insert(id) }
        for step in page.modelSteps where isInternalMessageMode(step.mode) || (step.mode?.isEmpty != false && (hidden || isInternalMessageMode(step.executionRole))) {
            if let id = step.assistantMessageID { ids.insert(id) }
        }
    }
    return ids
}
