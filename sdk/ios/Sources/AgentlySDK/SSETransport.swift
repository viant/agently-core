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
                var components = URLComponents(url: endpoint.baseURL, resolvingAgainstBaseURL: false)
                components?.path = endpoint.baseURL.path.trimmingCharacters(in: CharacterSet(charactersIn: "/")).isEmpty
                    ? path
                    : endpoint.baseURL.path + path
                guard let url = components?.url else {
                    throw URLError(.badURL)
                }

                var request = URLRequest(url: url)
                request.httpMethod = "GET"
                request.setValue("text/event-stream", forHTTPHeaderField: "Accept")
                endpoint.headers.forEach { request.setValue($1, forHTTPHeaderField: $0) }

                let (bytes, response) = try await session.bytes(for: request)
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
