import Foundation

/// Atomic RFC 6902 operations over immutable JSON values, including RFC 6901 pointers.
public enum AgUiJsonPatch {
    public static func apply(_ document: AgUiValue, operations: [AgUiValue]) throws -> AgUiValue {
        try AgUiSchema.validate(.array(operations), definition: "JsonPatch")
        var result = document
        for raw in operations {
            guard let operation = raw.object else { try aguiFail("Expected patch object") }
            let op = try operation.requiredString("op"), pointer = try operation.requiredString("path"), path = try tokens(pointer)
            switch op {
            case "add", "replace": result = try edit(result, path: path, operation: op, replacement: operation["value"])
            case "remove": result = try edit(result, path: path, operation: op, replacement: nil)
            case "copy", "move":
                let from = try tokens(operation.requiredString("from"))
                if op == "move" && path.count > from.count && Array(path.prefix(from.count)) == from { try aguiFail("Cannot move into own descendant") }
                let value = try get(result, path: from)
                if op == "move" { result = try edit(result, path: from, operation: "remove", replacement: nil) }
                result = try edit(result, path: path, operation: "add", replacement: value)
            case "test": guard try get(result, path: path).equivalent(operation["value"]!) else { try aguiFail("JSON patch test failed at \(pointer)") }
            default: try aguiFail("Invalid JSON patch operation")
            }
        }
        return result
    }
    private static func tokens(_ pointer: String) throws -> [String] {
        if pointer.isEmpty { return [] }
        guard pointer.range(of: "^(/([^/~]|~[01])*)*$", options: .regularExpression) != nil else { try aguiFail("Invalid JSON pointer") }
        return pointer.dropFirst().split(separator: "/", omittingEmptySubsequences: false).map { String($0).replacingOccurrences(of: "~1", with: "/").replacingOccurrences(of: "~0", with: "~") }
    }
    private static func index(_ token: String, count: Int, append: Bool) throws -> Int {
        if token == "-" && append { return count }
        guard token.range(of: "^(0|[1-9][0-9]*)$", options: .regularExpression) != nil, let index = Int(token), index < count || append && index == count else { try aguiFail("Invalid array index") }
        return index
    }
    private static func get(_ value: AgUiValue, path: [String]) throws -> AgUiValue {
        guard let token = path.first else { return value }
        let next: AgUiValue
        switch value {
        case .object(let object): guard let child = object[token] else { try aguiFail("Missing JSON pointer member") }; next = child
        case .array(let array): next = array[try index(token, count: array.count, append: false)]
        default: try aguiFail("Cannot traverse scalar")
        }
        return try get(next, path: Array(path.dropFirst()))
    }
    private static func edit(_ value: AgUiValue, path: [String], operation: String, replacement: AgUiValue?) throws -> AgUiValue {
        guard let token = path.first else { return operation == "remove" ? .null : replacement! }
        let rest = Array(path.dropFirst())
        switch value {
        case .object(var object):
            if !rest.isEmpty { guard let child = object[token] else { try aguiFail("Missing patch parent") }; object[token] = try edit(child, path: rest, operation: operation, replacement: replacement) }
            else { if operation != "add" && object[token] == nil { try aguiFail("Missing patch member") }; object[token] = operation == "remove" ? nil : replacement }
            return .object(object)
        case .array(var array):
            let i = try index(token, count: array.count, append: rest.isEmpty && operation == "add")
            if !rest.isEmpty { array[i] = try edit(array[i], path: rest, operation: operation, replacement: replacement) }
            else if operation == "add" { array.insert(replacement!, at: i) }
            else if operation == "remove" { array.remove(at: i) }
            else { array[i] = replacement! }
            return .array(array)
        default: try aguiFail("Cannot edit scalar child")
        }
    }
}
