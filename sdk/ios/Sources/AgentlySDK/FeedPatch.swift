import Foundation

public extension JSONValue {
    func applying(feedPatch operations: [FeedPatchOperation]) -> JSONValue {
        operations.reduce(self) { value, operation in
            value.applying(operation, path: Self.feedPatchTokens(operation.path), index: 0)
        }
    }

    private static func feedPatchTokens(_ path: String) -> [String] {
        path.split(separator: "/", omittingEmptySubsequences: false).dropFirst().map {
            String($0).replacingOccurrences(of: "~1", with: "/").replacingOccurrences(of: "~0", with: "~")
        }
    }

    private func applying(_ operation: FeedPatchOperation, path: [String], index: Int) -> JSONValue {
        guard index < path.count else { return operation.value ?? .null }
        let key = path[index]
        let last = index == path.count - 1
        switch self {
        case .object(let object):
            var next = object
            if last && operation.op.lowercased() == "remove" { next.removeValue(forKey: key) }
            else if last { next[key] = operation.value ?? .null }
            else { next[key] = (next[key] ?? .object([:])).applying(operation, path: path, index: index + 1) }
            return .object(next)
        case .array(let array):
            var next = array
            if last && operation.op.lowercased() == "add" && key == "-" { next.append(operation.value ?? .null) }
            else if let offset = Int(key), offset >= 0 {
                if last && operation.op.lowercased() == "remove" && offset < next.count { next.remove(at: offset) }
                else if last && operation.op.lowercased() == "add" { next.insert(operation.value ?? .null, at: min(offset, next.count)) }
                else if last && offset < next.count { next[offset] = operation.value ?? .null }
                else if offset < next.count { next[offset] = next[offset].applying(operation, path: path, index: index + 1) }
            }
            return .array(next)
        default:
            return JSONValue.object([:]).applying(operation, path: path, index: index)
        }
    }
}
