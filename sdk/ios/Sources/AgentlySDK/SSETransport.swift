import Foundation

public func openEventStream(
    endpoint: EndpointConfig,
    path: String,
    conversationID: String,
    session: URLSession = .shared
) -> AsyncThrowingStream<SSEEvent, Error> {
    AsyncThrowingStream { continuation in
        let task = Task {
            do {
                guard let url = makeEventStreamURL(baseURL: endpoint.baseURL, path: path) else {
                    throw URLError(.badURL)
                }

                let streamingSession = makeStreamingSession(from: session)
                var request = URLRequest(url: url)
                request.httpMethod = "GET"
                request.timeoutInterval = 60 * 60 * 24
                request.setValue("text/event-stream", forHTTPHeaderField: "Accept")
                endpoint.headers.forEach { request.setValue($1, forHTTPHeaderField: $0) }

                let (bytes, response) = try await streamingSession.bytes(for: request)
                guard let http = response as? HTTPURLResponse else {
                    throw AgentlySDKError.invalidResponse
                }
                guard (200..<300).contains(http.statusCode) else {
                    throw AgentlySDKError.httpStatus(http.statusCode, nil)
                }

                var dataLines: [String] = []
                for try await line in bytes.lines {
                    if Task.isCancelled { break }
                    if line.hasPrefix("data:") {
                        dataLines.append(String(line.dropFirst(5)))
                    } else if line.hasPrefix(":") {
                        continue
                    } else if line.isEmpty {
                        if let event = decodeSSEPayload(dataLines.joined(separator: "\n"), conversationID: conversationID) {
                            continuation.yield(event)
                        }
                        dataLines.removeAll(keepingCapacity: true)
                    }
                }
                if let event = decodeSSEPayload(dataLines.joined(separator: "\n"), conversationID: conversationID) {
                    continuation.yield(event)
                }
                continuation.finish()
            } catch {
                continuation.finish(throwing: error)
            }
        }

        continuation.onTermination = { _ in
            task.cancel()
        }
    }
}

private func makeStreamingSession(from session: URLSession) -> URLSession {
    guard let configuration = session.configuration.copy() as? URLSessionConfiguration else {
        let fallback = URLSessionConfiguration.default
        fallback.timeoutIntervalForRequest = 60 * 60 * 24
        fallback.timeoutIntervalForResource = 60 * 60 * 24
        fallback.waitsForConnectivity = false
        return URLSession(configuration: fallback)
    }
    configuration.timeoutIntervalForRequest = 60 * 60 * 24
    configuration.timeoutIntervalForResource = 60 * 60 * 24
    configuration.waitsForConnectivity = false
    return URLSession(configuration: configuration)
}

private func makeEventStreamURL(baseURL: URL, path: String) -> URL? {
    guard let relative = URLComponents(string: path) else {
        return nil
    }
    var components = URLComponents(url: baseURL, resolvingAgainstBaseURL: false)
    let basePath = baseURL.path.trimmingCharacters(in: CharacterSet(charactersIn: "/"))
    let relativePath = relative.path.trimmingCharacters(in: CharacterSet(charactersIn: "/"))

    if basePath.isEmpty {
        components?.path = "/" + relativePath
    } else if relativePath.isEmpty {
        components?.path = "/" + basePath
    } else {
        components?.path = "/" + basePath + "/" + relativePath
    }

    components?.percentEncodedQuery = relative.percentEncodedQuery
    return components?.url
}

private func decodeSSEPayload(_ payload: String, conversationID: String) -> SSEEvent? {
    guard !payload.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else {
        return nil
    }
    if let data = payload.data(using: .utf8),
       let decoded = try? JSONDecoder.agently().decode(SSEEvent.self, from: data) {
        return decoded
    }
    return SSEEvent(event: "message", data: payload)
}
