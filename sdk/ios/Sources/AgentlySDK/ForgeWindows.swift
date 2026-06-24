import Foundation

public extension AgentlyClient {
    func getForgeWindowMetadata(
        windowKey: String,
        targetContext: MetadataTargetContext? = nil
    ) async throws -> JSONValue {
        let key = try normalizedForgeWindowKey(windowKey)
        let path = "/v1/api/agently/forge/window/\(encodeForgeWindowKey(key))"
        let data = try await rawDataRequest(
            path: path,
            method: "GET",
            query: metadataTargetQueryItems(from: targetContext)
        )
        let decoded = try decoder.decode(JSONValue.self, from: data)
        if case .object(let object) = decoded, let payload = object["data"] {
            return payload
        }
        return decoded
    }
}

private func normalizedForgeWindowKey(_ value: String) throws -> String {
    let key = value.trimmingCharacters(in: .whitespacesAndNewlines)
    guard !key.isEmpty else {
        throw AgentlySDKError.invalidArgument("window key is required")
    }
    return key
}

private func encodeForgeWindowKey(_ value: String) -> String {
    agentlyPercentEncodedPathSegment(value)
}
