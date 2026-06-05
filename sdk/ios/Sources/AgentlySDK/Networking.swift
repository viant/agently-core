import Foundation

public enum AgentlySDKError: Error, LocalizedError, Sendable {
    case missingEndpoint(String)
    case invalidResponse
    case httpStatus(Int, String?)
    case rpcError(Int, String)

    public var errorDescription: String? {
        switch self {
        case .missingEndpoint(let name):
            return "Missing endpoint configuration: \(name)."
        case .invalidResponse:
            return "The server returned an unexpected response."
        case .httpStatus(let statusCode, let message):
            if let message, !message.isEmpty {
                return "Request failed with status \(statusCode): \(message)"
            }
            return "Request failed with status \(statusCode)."
        case .rpcError(let code, let message):
            return "RPC request failed with code \(code): \(message)"
        }
    }
}

struct RequestBuilder {
    let endpoint: EndpointConfig
    let encoder: JSONEncoder

    func makeRequest(
        path: String,
        method: String,
        queryItems: [URLQueryItem] = [],
        body: Data? = nil,
        contentType: String = "application/json"
    ) throws -> URLRequest {
        var components = URLComponents(url: endpoint.baseURL, resolvingAgainstBaseURL: false)
        let basePath = (components?.percentEncodedPath ?? "").trimmingCharacters(in: CharacterSet(charactersIn: "/"))
        components?.percentEncodedPath = basePath.isEmpty ? path : "/\(basePath)\(path)"
        components?.percentEncodedQuery = queryItems.isEmpty ? nil : agentlyPercentEncodedQuery(queryItems)
        guard let url = components?.url else {
            throw URLError(.badURL)
        }
        var request = URLRequest(url: url)
        request.httpMethod = method
        request.httpBody = body
        request.setValue(contentType, forHTTPHeaderField: "Content-Type")
        endpoint.headers.forEach { request.setValue($1, forHTTPHeaderField: $0) }
        return request
    }
}

func agentlyPercentEncodedPathSegment(_ value: String) -> String {
    agentlyPercentEncode(value)
}

func agentlyPercentEncodedQuery(_ items: [URLQueryItem]) -> String {
    items.map { item in
        let name = agentlyPercentEncode(item.name)
        guard let value = item.value else {
            return name
        }
        return "\(name)=\(agentlyPercentEncode(value))"
    }.joined(separator: "&")
}

private func agentlyPercentEncode(_ value: String) -> String {
    let allowed = CharacterSet.alphanumerics.union(CharacterSet(charactersIn: "-._~"))
    return value.addingPercentEncoding(withAllowedCharacters: allowed) ?? value
}

public extension JSONDecoder {
    static func agently() -> JSONDecoder {
        let decoder = JSONDecoder()
        decoder.keyDecodingStrategy = .useDefaultKeys
        return decoder
    }
}

public extension JSONEncoder {
    static func agently() -> JSONEncoder {
        let encoder = JSONEncoder()
        encoder.outputFormatting = [.sortedKeys]
        return encoder
    }
}
