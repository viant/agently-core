import Foundation

public struct ApplyPermissionInput: Sendable {
    public var conversationId: String
    public var authorizationSchemaVersion: Int?
    public var resource: [String: JSONValue]
    public var windowParams: [String: JSONValue]?
    public var targetContext: MetadataTargetContext?

    public init(
        conversationId: String,
        authorizationSchemaVersion: Int? = nil,
        resource: [String: JSONValue],
        windowParams: [String: JSONValue]? = nil,
        targetContext: MetadataTargetContext? = nil
    ) {
        self.conversationId = conversationId
        self.authorizationSchemaVersion = authorizationSchemaVersion
        self.resource = resource
        self.windowParams = windowParams
        self.targetContext = targetContext
    }
}

public extension AgentlyClient {
    func getForgeWindowMetadata(
        windowKey: String,
        targetContext: MetadataTargetContext? = nil
    ) async throws -> JSONValue {
        let key = try normalizedForgeWindowKey(windowKey)
        let path = "/v1/api/agently/forge/window/\(encodeForgeWindowKey(key))"
        let data = try await rawMetadataDataRequest(
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

    func applyPermission(windowKey: String, input: ApplyPermissionInput) async throws -> JSONValue {
        let key = try normalizedForgeWindowKey(windowKey)
        var query = metadataTargetQueryItems(from: input.targetContext)
        query.append(URLQueryItem(name: "applyPermission", value: "true"))
        if let version = input.authorizationSchemaVersion {
            guard version == 1 || version == 2 else { throw AgentlySDKError.invalidArgument("authorization schema version must be 1 or 2") }
            query.append(URLQueryItem(name: "authorizationSchemaVersion", value: String(version)))
        }
        let conversationId = input.conversationId.trimmingCharacters(in: .whitespacesAndNewlines)
        if !conversationId.isEmpty {
            query.append(URLQueryItem(name: "conversationId", value: conversationId))
        }
        if !input.resource.isEmpty {
            let resourceData = try encoder.encode(JSONValue.object(input.resource))
            query.append(URLQueryItem(name: "resource", value: String(decoding: resourceData, as: UTF8.self)))
        }
        if let params = input.windowParams {
            let encoded = try encoder.encode(JSONValue.object(params))
            query.append(URLQueryItem(name: "windowParams", value: String(decoding: encoded, as: UTF8.self)))
        }
        let path = "/v1/api/agently/forge/window/\(encodeForgeWindowKey(key))"
        let data = try await rawMetadataDataRequest(path: path, method: "GET", query: query)
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
