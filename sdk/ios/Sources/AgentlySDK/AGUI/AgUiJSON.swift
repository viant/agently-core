import Foundation

public enum AgUiError: Error, Equatable, CustomStringConvertible {
    case invalidProtocol(String)
    case httpStatus(Int)
    public var description: String { switch self { case .invalidProtocol(let message): return message; case .httpStatus(let code): return "AG-UI POST failed: HTTP \(code)" } }
}
func aguiFail(_ message: String) throws -> Never { throw AgUiError.invalidProtocol(message) }

/// Preserves numeric tokens as well as every opaque JSON field. No Double conversion.
public indirect enum AgUiValue: Sendable, Equatable {
    case object([String: AgUiValue]), array([AgUiValue]), string(String), number(String), bool(Bool), null
    public var object: [String: AgUiValue]? { if case .object(let value) = self { return value }; return nil }
    public var array: [AgUiValue]? { if case .array(let value) = self { return value }; return nil }
    public var string: String? { if case .string(let value) = self { return value }; return nil }
    public subscript(_ key: String) -> AgUiValue? { object?[key] }
    public static func parse(_ data: Data) throws -> AgUiValue {
        var parser = AgUiJSONParser(bytes: Array(data)); let result = try parser.value(); parser.whitespace()
        guard parser.index == parser.bytes.count else { try aguiFail("Trailing JSON data") }
        return result
    }
    public static func parse(_ text: String) throws -> AgUiValue { try parse(Data(text.utf8)) }
    public func encodedData() throws -> Data { Data(try jsonString().utf8) }
    public func jsonString() throws -> String {
        switch self {
        case .object(let object): return "{" + (try object.keys.sorted().map { key in try AgUiValue.string(key).jsonString() + ":" + object[key]!.jsonString() }).joined(separator: ",") + "}"
        case .array(let array): return "[" + (try array.map { try $0.jsonString() }).joined(separator: ",") + "]"
        case .string(let string): return String(decoding: try JSONEncoder().encode(string), as: UTF8.self)
        case .number(let number):
            guard number.range(of: "^-?(0|[1-9][0-9]*)(\\.[0-9]+)?([eE][+-]?[0-9]+)?$", options: .regularExpression) != nil else { try aguiFail("Invalid numeric token") }
            return number
        case .bool(let bool): return bool ? "true" : "false"
        case .null: return "null"
        }
    }
    func equivalent(_ other: AgUiValue) -> Bool {
        switch (self, other) {
        case (.number(let a), .number(let b)):
            return AgUiValue.canonicalNumber(a) == AgUiValue.canonicalNumber(b)
        case (.array(let a), .array(let b)): return a.count == b.count && zip(a,b).allSatisfy { $0.equivalent($1) }
        case (.object(let a), .object(let b)): return Set(a.keys) == Set(b.keys) && a.allSatisfy { key, value in value.equivalent(b[key]!) }
        default: return self == other
        }
    }
    static func canonicalNumber(_ token: String) -> String {
        var number = token.lowercased(); let sign = number.hasPrefix("-") ? "-" : ""
        if !sign.isEmpty { number.removeFirst() }
        let parts = number.split(separator: "e", omittingEmptySubsequences: false)
        let decimal = parts[0].split(separator: ".", omittingEmptySubsequences: false)
        var digits = decimal.joined()
        while digits.first == "0" { digits.removeFirst() }
        if digits.isEmpty { return "0" }
        var trailing = 0
        while digits.last == "0" { digits.removeLast(); trailing += 1 }
        let exponent = parts.count == 2 ? String(parts[1]) : "0"
        let power = addDecimalInteger(exponent, trailing - (decimal.count == 2 ? decimal[1].count : 0))
        return "\(sign)\(digits)e\(power)"
    }
    static func compareNumbers(_ a: String, _ b: String) -> Int {
        func parts(_ token: String) -> (negative: Bool, digits: String, exponent: String) {
            let canonical = canonicalNumber(token), negative = canonical.hasPrefix("-")
            if canonical == "0" { return (false,"0","0") }
            let split = canonical.drop(while: { $0 == "-" }).split(separator: "e")
            return (negative,String(split[0]),String(split[1]))
        }
        func signedIntegerCompare(_ x: String, _ y: String) -> Int {
            let negativeX = x.hasPrefix("-"), negativeY = y.hasPrefix("-")
            if negativeX != negativeY { return negativeX ? -1 : 1 }
            let a = x.filter { $0.isNumber }, b = y.filter { $0.isNumber }
            let magnitude = a.count != b.count ? (a.count < b.count ? -1 : 1) : a == b ? 0 : a < b ? -1 : 1
            return negativeX ? -magnitude : magnitude
        }
        let x = parts(a), y = parts(b)
        if x.digits == "0" && y.digits == "0" { return 0 }
        if x.negative != y.negative { return x.negative ? -1 : 1 }
        if x.digits == "0" { return y.negative ? 1 : -1 }
        if y.digits == "0" { return x.negative ? -1 : 1 }
        var magnitude = signedIntegerCompare(addDecimalInteger(x.exponent,x.digits.count),addDecimalInteger(y.exponent,y.digits.count))
        if magnitude == 0 {
            let size = max(x.digits.count,y.digits.count)
            let a = x.digits + String(repeating: "0",count: size-x.digits.count), b = y.digits + String(repeating: "0",count: size-y.digits.count)
            magnitude = a == b ? 0 : a < b ? -1 : 1
        }
        return x.negative ? -magnitude : magnitude
    }
    // Exponents are arbitrary-length integers too; do not coerce them through Int.
    private static func addDecimalInteger(_ a: String, _ offset: Int) -> String {
        func parts(_ value: String) -> (Bool, [Int]) {
            let negative = value.hasPrefix("-")
            var digits = value.filter { $0.isNumber }.compactMap { $0.wholeNumberValue }
            while digits.first == 0 && digits.count > 1 { digits.removeFirst() }
            return (negative && digits != [0], digits)
        }
        let (negativeA, aDigits) = parts(a), (negativeB, bDigits) = parts(String(offset))
        var result: [Int] = [], negative = false
        if negativeA == negativeB {
            negative = negativeA; var carry = 0
            let x = Array(aDigits.reversed()), y = Array(bDigits.reversed())
            for i in 0..<max(x.count,y.count) { let sum = (i < x.count ? x[i] : 0) + (i < y.count ? y[i] : 0) + carry; result.append(sum % 10); carry = sum / 10 }
            if carry > 0 { result.append(carry) }
        } else {
            let aGreater = aDigits.count > bDigits.count || aDigits.count == bDigits.count && !aDigits.lexicographicallyPrecedes(bDigits)
            let x = Array((aGreater ? aDigits : bDigits).reversed()), y = Array((aGreater ? bDigits : aDigits).reversed())
            negative = aGreater ? negativeA : negativeB; var borrow = 0
            for i in 0..<x.count { var digit = x[i] - (i < y.count ? y[i] : 0) - borrow; borrow = digit < 0 ? 1 : 0; if digit < 0 { digit += 10 }; result.append(digit) }
        }
        while result.last == 0 && result.count > 1 { result.removeLast() }
        if result == [0] { negative = false }
        return (negative ? "-" : "") + result.reversed().map(String.init).joined()
    }

}
private struct AgUiJSONParser {
    var bytes: [UInt8]; var index = 0
    mutating func whitespace() { while index < bytes.count && [9,10,13,32].contains(bytes[index]) { index += 1 } }
    mutating func value() throws -> AgUiValue {
        whitespace(); guard index < bytes.count else { try aguiFail("Unexpected JSON EOF") }
        switch bytes[index] {
        case 123:
            index += 1; whitespace(); var object: [String: AgUiValue] = [:]
            if consume(125) { return .object(object) }
            while true {
                whitespace(); let key = try string(); whitespace(); guard consume(58) else { try aguiFail("Expected colon") }
                guard object[key] == nil else { try aguiFail("Duplicate JSON object key") }
                object[key] = try value(); whitespace()
                if consume(125) { return .object(object) }; guard consume(44) else { try aguiFail("Expected comma") }
            }
        case 91:
            index += 1; whitespace(); var array: [AgUiValue] = []
            if consume(93) { return .array(array) }
            while true { array.append(try value()); whitespace(); if consume(93) { return .array(array) }; guard consume(44) else { try aguiFail("Expected comma") } }
        case 34: return .string(try string())
        case 116: try literal("true"); return .bool(true)
        case 102: try literal("false"); return .bool(false)
        case 110: try literal("null"); return .null
        default:
            let start = index
            while index < bytes.count && ([45,43,46,69,101].contains(bytes[index]) || bytes[index] >= 48 && bytes[index] <= 57) { index += 1 }
            guard start < index else { try aguiFail("Invalid JSON token") }
            let number = String(decoding: bytes[start..<index], as: UTF8.self)
            let value = AgUiValue.number(number); _ = try value.jsonString(); return value
        }
    }
    mutating func consume(_ byte: UInt8) -> Bool { if index < bytes.count && bytes[index] == byte { index += 1; return true }; return false }
    mutating func literal(_ text: String) throws { let literal = Array(text.utf8); guard index + literal.count <= bytes.count && Array(bytes[index..<index+literal.count]) == literal else { try aguiFail("Invalid JSON literal") }; index += literal.count }
    mutating func string() throws -> String {
        let start = index; guard consume(34) else { try aguiFail("Expected JSON string") }
        var escaped = false
        while index < bytes.count {
            let byte = bytes[index]; index += 1
            if escaped { escaped = false; continue }
            if byte == 92 { escaped = true }
            else if byte == 34 { return try JSONDecoder().decode(String.self, from: Data(bytes[start..<index])) }
        }
        try aguiFail("Unterminated JSON string")
    }
}
extension Dictionary where Key == String, Value == AgUiValue {
    func string(_ key: String) -> String? { self[key]?.string }
    func requiredString(_ key: String) throws -> String { guard let value = string(key) else { try aguiFail("Missing string \(key)") }; return value }
    func with(_ fields: [String: AgUiValue?]) -> [String: AgUiValue] { var copy = self; for (key,value) in fields { copy[key] = value }; return copy }
}
