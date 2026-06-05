import Foundation

public extension AgentlyClient {
    func getForgeWindowMetadata(
        windowKey: String,
        targetContext: MetadataTargetContext? = nil
    ) async throws -> JSONValue {
        let path = "/v1/api/agently/forge/window/\(encodeForgeWindowKey(windowKey))"
        let data = try await rawDataRequest(
            path: path,
            method: "GET",
            query: forgeWindowTargetQueryItems(from: targetContext)
        )
        let decoded = try decoder.decode(JSONValue.self, from: data)
        if case .object(let object) = decoded, let payload = object["data"] {
            return payload
        }
        return decoded
    }
}

private func encodeForgeWindowKey(_ value: String) -> String {
    agentlyPercentEncodedPathSegment(value)
}

private func forgeWindowTargetQueryItems(from targetContext: MetadataTargetContext?) -> [URLQueryItem] {
    var query: [URLQueryItem] = []
    if let platform = targetContext?.platform?.trimmingCharacters(in: .whitespacesAndNewlines), !platform.isEmpty {
        query.append(URLQueryItem(name: "platform", value: platform))
    }
    if let formFactor = targetContext?.formFactor?.trimmingCharacters(in: .whitespacesAndNewlines), !formFactor.isEmpty {
        query.append(URLQueryItem(name: "formFactor", value: formFactor))
    }
    if let surface = targetContext?.surface?.trimmingCharacters(in: .whitespacesAndNewlines), !surface.isEmpty {
        query.append(URLQueryItem(name: "surface", value: surface))
    }
    for capability in targetContext?.capabilities ?? [] {
        let trimmed = capability.trimmingCharacters(in: .whitespacesAndNewlines)
        if !trimmed.isEmpty {
            query.append(URLQueryItem(name: "capabilities", value: trimmed))
        }
    }
    return query
}
