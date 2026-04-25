// Lookups.swift — mobile parity for the datasource + overlay feature.
//
// Wire types mirror sdk/api/datasource.go 1:1 so JSON round-trips with the Go
// server without custom coding keys. Pure token helpers port the algorithms
// from ui/src/components/lookups/tokens.js; both flavours (this file and the
// Kotlin equivalent) are validated by platform-specific unit tests that run
// the same scenarios as lookups-test.mjs T12-T17.
//
// Overlay matching is strictly server-side (see lookups.md §5). iOS never
// loads overlay YAML, never evaluates match modes, never composes priorities.

import Foundation

// MARK: - Wire types

public struct FetchDatasourceInput: Codable, Sendable {
    public var id: String
    public var inputs: [String: JSONValue]?
    public var cache: DatasourceCacheHints?

    public init(id: String, inputs: [String: JSONValue]? = nil, cache: DatasourceCacheHints? = nil) {
        self.id = id
        self.inputs = inputs
        self.cache = cache
    }
}

public struct DatasourceCacheHints: Codable, Sendable {
    public var bypassCache: Bool?
    public var writeThrough: Bool?

    public init(bypassCache: Bool? = nil, writeThrough: Bool? = nil) {
        self.bypassCache = bypassCache
        self.writeThrough = writeThrough
    }
}

public struct FetchDatasourceOutput: Codable, Sendable {
    public var rows: [[String: JSONValue]]
    public var dataInfo: [String: JSONValue]?
    public var cache: DatasourceCacheMeta?
}

public struct DatasourceCacheMeta: Codable, Sendable {
    public var hit: Bool
    public var stale: Bool?
    public var fetchedAt: String
    public var ttlSeconds: Int?
}

public struct InvalidateDatasourceCacheInput: Codable, Sendable {
    public var id: String
    public var inputsHash: String?

    public init(id: String, inputsHash: String? = nil) {
        self.id = id
        self.inputsHash = inputsHash
    }
}

public struct ListLookupRegistryInput: Codable, Sendable {
    public var context: String

    public init(context: String) { self.context = context }
}

public struct LookupRegistryEntry: Codable, Sendable, Identifiable {
    public var name: String
    public var dataSource: String
    public var trigger: String?
    public var required: Bool?
    public var display: String?
    public var token: LookupTokenFormat?
    public var inputs: [LookupParameter]?
    public var outputs: [LookupParameter]?

    public var id: String { name }
}

public struct LookupTokenFormat: Codable, Sendable {
    public var store: String?
    public var display: String?
    public var modelForm: String?
}

public struct LookupParameter: Codable, Sendable {
    public var from: String?
    public var to: String?
    public var name: String
    public var location: String?
}

public struct ListLookupRegistryOutput: Codable, Sendable {
    public var entries: [LookupRegistryEntry]
}

// MARK: - Client methods

extension AgentlyClient {
    /// POST /v1/api/datasources/{id}/fetch
    ///
    /// Forwards both `inputs` and the optional `cache` hints so that
    /// `bypassCache` / `writeThrough` actually reach the server. Earlier
    /// drafts silently dropped `input.cache`, producing cross-platform
    /// drift with the Go + Kotlin clients.
    public func fetchDatasource(_ input: FetchDatasourceInput) async throws -> FetchDatasourceOutput {
        let path = "/v1/api/datasources/\(percentEncoded(input.id))/fetch"
        struct Body: Encodable {
            let inputs: [String: JSONValue]?
            let cache: DatasourceCacheHints?
        }
        return try await post(
            path,
            body: Body(inputs: input.inputs, cache: input.cache),
            as: FetchDatasourceOutput.self
        )
    }

    /// DELETE /v1/api/datasources/{id}/cache[?inputsHash=…]
    public func invalidateDatasourceCache(_ input: InvalidateDatasourceCacheInput) async throws {
        var q: [URLQueryItem] = []
        if let h = input.inputsHash, !h.isEmpty {
            q.append(URLQueryItem(name: "inputsHash", value: h))
        }
        let path = "/v1/api/datasources/\(percentEncoded(input.id))/cache"
        _ = try await rawRequest(path: path, method: "DELETE", query: q, as: EmptyResponse.self)
    }

    /// GET /v1/api/lookups/registry?context=<kind>:<id>
    public func listLookupRegistry(_ input: ListLookupRegistryInput) async throws -> ListLookupRegistryOutput {
        return try await get(
            "/v1/api/lookups/registry",
            query: [URLQueryItem(name: "context", value: input.context)],
            as: ListLookupRegistryOutput.self
        )
    }

    private func percentEncoded(_ s: String) -> String {
        s.addingPercentEncoding(withAllowedCharacters: .urlPathAllowed) ?? s
    }
}

// MARK: - Pure token helpers (Activations b + c)

public enum LookupTokens {
    /// Serialize a resolved row into the stored token form `@{name:id "label"}`.
    public static func serializeToken(entry: LookupRegistryEntry, resolved: [String: Any]) -> String {
        let storeTpl = entry.token?.store ?? "${id}"
        let displayTpl = entry.token?.display ?? "${name}"
        let id = applyTemplate(storeTpl, resolved)
        let label = applyTemplate(displayTpl, resolved)
            .replacingOccurrences(of: "\"", with: "\\\"")
        return "@{\(entry.name):\(id) \"\(label)\"}"
    }

    public struct Parsed: Equatable {
        public let raw: String
        public let range: Range<String.Index>
        public let name: String
        public let id: String
        public let label: String
    }

    /// Parse every @{…} occurrence in text.
    public static func parseTokens(_ text: String) -> [Parsed] {
        let pattern = #"@\{([a-zA-Z][a-zA-Z0-9_-]*):([^\s"]+)\s+"((?:[^"\\]|\\.)*)"\}"#
        guard let re = try? NSRegularExpression(pattern: pattern) else { return [] }
        let range = NSRange(text.startIndex..., in: text)
        var out: [Parsed] = []
        for m in re.matches(in: text, range: range) {
            guard
                let rawR = Range(m.range, in: text),
                let n1 = Range(m.range(at: 1), in: text),
                let n2 = Range(m.range(at: 2), in: text),
                let n3 = Range(m.range(at: 3), in: text)
            else { continue }
            let label = String(text[n3]).replacingOccurrences(of: "\\\"", with: "\"")
            out.append(Parsed(
                raw: String(text[rawR]),
                range: rawR,
                name: String(text[n1]),
                id: String(text[n2]),
                label: label
            ))
        }
        return out
    }

    /// Flatten a stored string with rich tokens into the text sent to the LLM.
    /// Unknown names fall back to `${id}`.
    public static func flattenStored(_ stored: String, registry: [LookupRegistryEntry]) -> String {
        guard !stored.isEmpty else { return "" }
        let byName: [String: LookupRegistryEntry] = Dictionary(uniqueKeysWithValues: registry.map { ($0.name, $0) })
        var result = stored
        for parsed in parseTokens(stored).reversed() {
            let entry = byName[parsed.name]
            let tpl = entry?.token?.modelForm ?? "${id}"
            let rendered = applyTemplate(tpl, [
                "id": parsed.id,
                "name": parsed.label,
                "label": parsed.label,
            ])
            result.replaceSubrange(parsed.range, with: rendered)
        }
        return result
    }

    private static func applyTemplate(_ tpl: String, _ row: [String: Any]) -> String {
        guard !tpl.isEmpty else { return "" }
        let pattern = #"\$\{(\w+)\}"#
        guard let re = try? NSRegularExpression(pattern: pattern) else { return tpl }
        let ns = tpl as NSString
        let matches = re.matches(in: tpl, range: NSRange(location: 0, length: ns.length))
        var out = tpl
        for m in matches.reversed() {
            guard let r = Range(m.range, in: out), let k = Range(m.range(at: 1), in: out) else { continue }
            let key = String(out[k])
            if let v = row[key] { out.replaceSubrange(r, with: "\(v)") } else { out.replaceSubrange(r, with: "") }
        }
        return out
    }
}
