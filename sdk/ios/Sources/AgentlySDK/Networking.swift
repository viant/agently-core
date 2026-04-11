import Foundation

public enum AgentlySDKError: Error, LocalizedError, Sendable {
    case missingEndpoint(String)
    case invalidResponse
    case httpStatus(Int, String?)

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
        components?.path = endpoint.baseURL.path.trimmingCharacters(in: CharacterSet(charactersIn: "/")) .isEmpty
            ? path
            : endpoint.baseURL.path + path
        components?.queryItems = queryItems.isEmpty ? nil : queryItems
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
