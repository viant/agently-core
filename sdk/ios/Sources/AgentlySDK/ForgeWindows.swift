import Foundation

public extension AgentlyClient {
    func getForgeWindowMetadata(windowKey: String) async throws -> JSONValue {
        let path = "/v1/api/agently/forge/window/\(encodeForgeWindowKey(windowKey))"
        let data = try await rawDataRequest(path: path, method: "GET")
        let decoded = try decoder.decode(JSONValue.self, from: data)
        if case .object(let object) = decoded, let payload = object["data"] {
            return payload
        }
        return decoded
    }
}

private func encodeForgeWindowKey(_ value: String) -> String {
    value.addingPercentEncoding(withAllowedCharacters: .urlPathAllowed) ?? value
}
