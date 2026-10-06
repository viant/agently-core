import Foundation

final class AgUiChunks {
    struct Lane { let kind: String; let fields: [String: AgUiValue] }
    private var lanes: [String?: Lane] = [:]
    private var order: [String?] = []
    private func close(_ owner: String?) throws -> [AgUiEvent] {
        guard let lane = lanes.removeValue(forKey: owner) else { return [] }
        order.removeAll { $0 == owner }
        let type = lane.kind == "text" ? "TEXT_MESSAGE_END" : lane.kind == "tool" ? "TOOL_CALL_END" : "REASONING_MESSAGE_END"
        let idKey = lane.kind == "tool" ? "toolCallId" : "messageId"
        return [try AgUiEvent(value: ["type": .string(type), idKey: lane.fields[idKey]!].with(["subagentRunId": lane.fields["subagentRunId"]]))]
    }
    func receive(_ event: AgUiEvent) throws -> [AgUiEvent] {
        let raw = event.value, type = event.type, tag = raw.string("subagentRunId")
        let kind: String? = type == "TEXT_MESSAGE_CHUNK" ? "text" : type == "TOOL_CALL_CHUNK" ? "tool" : type == "REASONING_MESSAGE_CHUNK" ? "reasoning" : nil
        guard let kind else {
            switch type {
            case "RUN_STARTED", "RUN_FINISHED", "RUN_ERROR", "MESSAGES_SNAPSHOT": return try finish() + [event]
            case "RAW", "ACTIVITY_SNAPSHOT", "ACTIVITY_DELTA", "REASONING_ENCRYPTED_VALUE", "SUBAGENT_STARTED": return [event]
            case "SUBAGENT_FINISHED", "SUBAGENT_ERROR": return try close(tag) + [event]
            default: return event.knownType != nil ? try close(tag) + [event] : [event]
            }
        }
        let idKey = kind == "tool" ? "toolCallId" : "messageId", id = raw.string(kind == "tool" ? "toolCallId" : "messageId")
        let holder = id.flatMap { id in lanes.first { $0.value.kind == kind && $0.value.fields.string(idKey) == id } }
        let owner: String?
        if let holder {
            if let tag, tag != holder.key { try aguiFail("Chunk attribution disagrees with opener") }; owner = holder.key
        } else if id != nil || tag != nil { owner = tag }
        else if lanes[nil]?.kind == kind { owner = nil }
        else {
            let candidates = order.filter { lanes[$0]?.kind == kind }
            guard candidates.count <= 1 else { try aguiFail("Ambiguous \(type); specify entity ID or subagentRunId") }
            owner = candidates.first ?? nil
        }
        var output: [AgUiEvent] = []; var lane = lanes[owner]
        let opening = lane?.kind != kind || id != nil && id != lane?.fields.string(idKey)
        if opening {
            output += try close(owner)
            guard let id else { try aguiFail("First \(type) requires \(idKey)") }
            if kind == "tool" && raw.string("toolCallName") == nil { try aguiFail("First TOOL_CALL_CHUNK requires toolCallName") }
            var fields: [String: AgUiValue] = [idKey: .string(id)]
            fields["subagentRunId"] = raw["subagentRunId"]
            if kind == "text" { fields["role"] = raw["role"] ?? .string("assistant"); fields["name"] = raw["name"] }
            if kind == "tool" { fields["toolCallName"] = raw["toolCallName"]; fields["parentMessageId"] = raw["parentMessageId"] }
            if kind == "reasoning" { fields["role"] = .string("reasoning") }
            lane = Lane(kind: kind, fields: fields); lanes[owner] = lane; order.append(owner)
            let start = kind == "text" ? "TEXT_MESSAGE_START" : kind == "tool" ? "TOOL_CALL_START" : "REASONING_MESSAGE_START"
            output.append(try AgUiEvent(value: fields.with(["type": .string(start), "metadata": raw["metadata"]])))
        } else {
            let agreements = kind == "text" ? ["role", "name"] : kind == "tool" ? ["toolCallName", "parentMessageId"] : []
            for field in agreements where raw[field] != nil && raw[field] != lane?.fields[field] { try aguiFail("Chunk \(field) disagrees with opener") }
        }
        guard let lane else { try aguiFail("Missing chunk lane") }
        let described = Set(["type", idKey, "role", "name", "toolCallName", "parentMessageId", "subagentRunId", "timestamp"])
        if opening, raw["delta"] == nil, raw["rawEvent"] == nil, let opener = output.last {
            var fields = opener.value
            for (key,value) in raw where !described.union(["delta","rawEvent","metadata"]).contains(key) && fields[key] == nil {fields[key] = value}
            output[output.count-1] = try AgUiEvent(value:fields)
        }
        if raw["delta"] != nil || raw["rawEvent"] != nil || !opening && (raw["metadata"] != nil || !Set(raw.keys).subtracting(described).isEmpty) {
            let contentType = kind == "text" ? "TEXT_MESSAGE_CONTENT" : kind == "tool" ? "TOOL_CALL_ARGS" : "REASONING_MESSAGE_CONTENT"
            var fields = raw
            for key in ["role", "name", "toolCallName", "parentMessageId"] { fields[key] = nil }
            fields["type"] = .string(contentType); fields[idKey] = lane.fields[idKey]; fields["delta"] = raw["delta"] ?? .string("")
            fields["subagentRunId"] = lane.fields["subagentRunId"]
            output.append(try AgUiEvent(value: fields))
        }
        return output
    }
    func finish() throws -> [AgUiEvent] { var events: [AgUiEvent] = []; for owner in order { events += try close(owner) }; return events }
}
