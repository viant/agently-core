import Foundation
#if canImport(Security)
import Security
#endif

public protocol AgentlySessionCookieStoring: Sendable {
    func cookieHeader(for url: URL) -> String?
    func storeCookies(from response: HTTPURLResponse, requestURL: URL)
    func clear()
}

public final class AgentlyPersistentSessionCookieStore: AgentlySessionCookieStoring, @unchecked Sendable {
    private let storage: AgentlySessionCookieStorage
    private let lock = NSLock()
    private var cookies: [StoredSessionCookie]

    public convenience init(namespace: String) {
        #if canImport(Security)
        self.init(storage: KeychainSessionCookieStorage(namespace: namespace))
        #else
        self.init(storage: UserDefaultsSessionCookieStorage(namespace: namespace))
        #endif
    }

    init(storage: AgentlySessionCookieStorage) {
        self.storage = storage
        self.cookies = storage.load().filter { !$0.isExpired }
    }

    public func cookieHeader(for url: URL) -> String? {
        lock.lock()
        defer { lock.unlock() }
        pruneExpiredCookiesLocked()
        let values = cookies.compactMap { $0.cookieHeaderPair(for: url) }
        return values.isEmpty ? nil : values.joined(separator: "; ")
    }

    public func storeCookies(from response: HTTPURLResponse, requestURL: URL) {
        let headerFields = response.allHeaderFields.reduce(into: [String: String]()) { result, entry in
            guard let key = entry.key as? String else { return }
            if let value = entry.value as? String {
                result[key] = value
            } else {
                result[key] = String(describing: entry.value)
            }
        }
        let parsed = HTTPCookie.cookies(withResponseHeaderFields: headerFields, for: requestURL)
        guard !parsed.isEmpty else { return }
        let stored = parsed.compactMap { StoredSessionCookie(cookie: $0, requestURL: requestURL) }
        guard !stored.isEmpty else { return }

        lock.lock()
        defer { lock.unlock() }
        cookies.removeAll { existing in
            stored.contains { $0.matchesIdentity(of: existing) }
        }
        cookies.append(contentsOf: stored.filter { !$0.isExpired })
        pruneExpiredCookiesLocked()
        storage.save(cookies)
    }

    public func clear() {
        lock.lock()
        defer { lock.unlock() }
        cookies = []
        storage.save([])
    }

    private func pruneExpiredCookiesLocked() {
        let before = cookies.count
        cookies.removeAll(where: \.isExpired)
        if before != cookies.count {
            storage.save(cookies)
        }
    }
}

protocol AgentlySessionCookieStorage: Sendable {
    func load() -> [StoredSessionCookie]
    func save(_ cookies: [StoredSessionCookie])
}

struct StoredSessionCookie: Codable, Equatable, Sendable {
    let name: String
    let value: String
    let domain: String
    let path: String
    let secure: Bool
    let hostOnly: Bool
    let expiresAt: TimeInterval?

    init?(cookie: HTTPCookie, requestURL: URL) {
        let name = cookie.name.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !name.isEmpty else { return nil }
        self.name = name
        self.value = cookie.value
        self.domain = cookie.domain.trimmingCharacters(in: CharacterSet(charactersIn: ".")).lowercased()
        self.path = cookie.path.isEmpty ? "/" : cookie.path
        self.secure = cookie.isSecure
        let header = (cookie.properties?[.domain] as? String)?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
        self.hostOnly = header.isEmpty
        self.expiresAt = cookie.expiresDate?.timeIntervalSince1970
    }

    var isExpired: Bool {
        guard let expiresAt else { return false }
        return expiresAt <= Date().timeIntervalSince1970
    }

    func matchesIdentity(of other: StoredSessionCookie) -> Bool {
        name.caseInsensitiveCompare(other.name) == .orderedSame
            && domain.caseInsensitiveCompare(other.domain) == .orderedSame
            && path == other.path
    }

    func cookieHeaderPair(for url: URL) -> String? {
        guard !isExpired,
              let host = url.host?.lowercased(),
              pathMatches(url.path.isEmpty ? "/" : url.path),
              domainMatches(host),
              !secure || url.scheme?.lowercased() == "https" else {
            return nil
        }
        return "\(name)=\(value)"
    }

    private func domainMatches(_ host: String) -> Bool {
        if hostOnly {
            return host == domain
        }
        return host == domain || host.hasSuffix(".\(domain)")
    }

    private func pathMatches(_ requestPath: String) -> Bool {
        requestPath == path || requestPath.hasPrefix(path.hasSuffix("/") ? path : "\(path)/")
    }
}

final class UserDefaultsSessionCookieStorage: AgentlySessionCookieStorage, @unchecked Sendable {
    private let defaults: UserDefaults
    private let key: String

    init(namespace: String, defaults: UserDefaults = .standard) {
        self.defaults = defaults
        self.key = "agently.sdk.sessionCookies.\(namespace)"
    }

    func load() -> [StoredSessionCookie] {
        guard let data = defaults.data(forKey: key) else { return [] }
        return (try? JSONDecoder().decode([StoredSessionCookie].self, from: data)) ?? []
    }

    func save(_ cookies: [StoredSessionCookie]) {
        if cookies.isEmpty {
            defaults.removeObject(forKey: key)
            return
        }
        if let data = try? JSONEncoder().encode(cookies) {
            defaults.set(data, forKey: key)
        }
    }
}

#if canImport(Security)
final class KeychainSessionCookieStorage: AgentlySessionCookieStorage, @unchecked Sendable {
    private let service = "com.viant.agently.sdk.sessionCookies"
    private let account: String

    init(namespace: String) {
        self.account = namespace
    }

    func load() -> [StoredSessionCookie] {
        var query = baseQuery()
        query[kSecReturnData as String] = true
        query[kSecMatchLimit as String] = kSecMatchLimitOne
        var result: CFTypeRef?
        let status = SecItemCopyMatching(query as CFDictionary, &result)
        guard status == errSecSuccess, let data = result as? Data else {
            return []
        }
        return (try? JSONDecoder().decode([StoredSessionCookie].self, from: data)) ?? []
    }

    func save(_ cookies: [StoredSessionCookie]) {
        var query = baseQuery()
        if cookies.isEmpty {
            SecItemDelete(query as CFDictionary)
            return
        }
        guard let data = try? JSONEncoder().encode(cookies) else {
            return
        }
        let attributes = [kSecValueData as String: data]
        let status = SecItemUpdate(query as CFDictionary, attributes as CFDictionary)
        if status == errSecItemNotFound {
            query[kSecValueData as String] = data
            SecItemAdd(query as CFDictionary, nil)
        }
    }

    private func baseQuery() -> [String: Any] {
        [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: account
        ]
    }
}
#endif
