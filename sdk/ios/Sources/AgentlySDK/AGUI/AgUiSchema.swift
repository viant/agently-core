import Foundation

/// Evaluates precisely the JSON Schema keywords used by the pinned AG-UI 1.0 schema.
public enum AgUiSchema {
    private static let root: AgUiValue = {
        guard let url = Bundle.module.url(forResource: "schema-1.0", withExtension: "json"), let data = try? Data(contentsOf: url), let value = try? AgUiValue.parse(data) else { preconditionFailure("Missing AG-UI schema") }
        return value
    }()
    public static func validate(_ value: AgUiValue, definition: String, tolerateUnknownFields: Bool = false) throws {
        guard let schema = root["$defs"]?[definition] else { try aguiFail("Unknown schema definition \(definition)") }
        _ = try evaluate(value, schema: schema, path: definition, tolerant: tolerateUnknownFields)
    }
    /// Fail closed on schema keywords outside the bundled evaluator's supported subset.
    /// Applications needing broader JSON Schema may pass their own validator to continuation/dispatch helpers.
    public static func validateResponse(_ value: AgUiValue, schema: AgUiValue) throws {
        try checkResponseSchema(schema, document: schema)
        _ = try evaluate(value, schema: schema, path: "response", tolerant: false, schemaRoot: schema)
    }
    private static func checkResponseSchema(_ schema: AgUiValue, document: AgUiValue) throws {
        if schema == .bool(true) || schema == .bool(false) {return}
        guard let object = schema.object else {try aguiFail("Invalid response schema")}
        if let reference = object["$ref"] {
            guard let text=reference.string,text.hasPrefix("#/$defs/"),document["$defs"]?[String(text.dropFirst("#/$defs/".count))] != nil else {try aguiFail("Unsupported response schema reference; supply an application validator")}
        }
        let supported: Set<String> = ["$schema","$id","$defs","$ref","$comment","title","description","default","examples","deprecated","readOnly","writeOnly","type","const","enum","not","allOf","oneOf","anyOf","required","properties","additionalProperties","unevaluatedProperties","items","minItems","maxItems","minLength","maxLength","pattern","minimum","maximum"]
        for (key,value) in object {
            if key == "type", value.string == nil, value.array?.allSatisfy({$0.string != nil}) != true {try aguiFail("Invalid response schema type union")}
            guard supported.contains(key) else {try aguiFail("Unsupported response schema keyword \(key); supply an application validator")}
            if ["properties","$defs"].contains(key) {for child in value.object?.values ?? [:].values {try checkResponseSchema(child, document: document)}}
            if ["not","items","additionalProperties"].contains(key) {try checkResponseSchema(value, document: document)}
            if ["allOf","oneOf","anyOf"].contains(key) {for child in value.array ?? [] {try checkResponseSchema(child, document: document)}}
        }
    }
    private static func evaluate(_ value: AgUiValue, schema: AgUiValue, path: String, tolerant: Bool, schemaRoot: AgUiValue = root, depth: Int = 0) throws -> Set<String> {
        guard depth <= 256 else {try aguiFail("Schema recursion limit exceeded")}
        if schema == .bool(true) { return Set(value.object?.keys.map { $0 } ?? []) }
        if schema == .bool(false) { try aguiFail("\(path) is forbidden") }
        guard let s = schema.object else { try aguiFail("Invalid bundled schema") }
        var evaluated = Set<String>()
        if let reference = s.string("$ref") {
            let name = String(reference.dropFirst("#/$defs/".count))
            guard reference.hasPrefix("#/$defs/"), let ref = schemaRoot["$defs"]?[name] else { try aguiFail("Invalid schema reference") }
            evaluated.formUnion(try evaluate(value, schema: ref, path: path, tolerant: tolerant, schemaRoot: schemaRoot, depth: depth + 1))
        }
        if let not = s["not"], (try? evaluate(value, schema: not, path: path, tolerant: tolerant, schemaRoot: schemaRoot, depth: depth + 1)) != nil { try aguiFail("\(path) fails not constraint") }
        for child in s["allOf"]?.array ?? [] { evaluated.formUnion(try evaluate(value, schema: child, path: path, tolerant: tolerant, schemaRoot: schemaRoot, depth: depth + 1)) }
        if let branches = s["oneOf"]?.array {
            let matches = branches.compactMap { try? evaluate(value, schema: $0, path: path, tolerant: tolerant, schemaRoot: schemaRoot, depth: depth + 1) }
            guard matches.count == 1 else { try aguiFail("\(path) must match exactly one schema") }; evaluated.formUnion(matches[0])
        }
        if let branches = s["anyOf"]?.array {
            let matches = branches.compactMap {try? evaluate(value,schema:$0,path:path,tolerant:tolerant,schemaRoot:schemaRoot,depth:depth+1)}
            guard !matches.isEmpty else {try aguiFail("\(path) fails anyOf")};for match in matches {evaluated.formUnion(match)}
        }
        if let types = s["type"]?.array {
            guard types.contains(where:{(try? evaluate(value,schema:.object(s.with(["type":$0])),path:path,tolerant:tolerant,schemaRoot:schemaRoot,depth:depth+1)) != nil}) else {try aguiFail("\(path) fails type union")}
        }
        if let type = s.string("type") {
            let valid: Bool
            switch (type,value) {
            case ("object", .object), ("array", .array), ("string", .string), ("boolean", .bool), ("null", .null): valid = true
            case ("number", .number): valid = true
            case ("integer", .number(let token)): let canonical = AgUiValue.canonicalNumber(token); valid = canonical == "0" || !(canonical.split(separator: "e").last?.hasPrefix("-") ?? false)
            default: valid = false
            }
            guard valid else { try aguiFail("\(path) must be \(type)") }
        }
        if let constant = s["const"], !value.equivalent(constant) { try aguiFail("\(path) fails const") }
        if let values = s["enum"]?.array, !values.contains(where: { value.equivalent($0) }) { try aguiFail("\(path) fails enum") }
        if let object = value.object {
            for required in s["required"]?.array ?? [] { guard let key = required.string, object[key] != nil else { try aguiFail("\(path) missing required field") } }
            let properties = s["properties"]?.object ?? [:]
            for (key,child) in properties { if let field = object[key] { _ = try evaluate(field, schema: child, path: "\(path)/\(key)", tolerant: tolerant, schemaRoot: schemaRoot, depth: depth + 1); evaluated.insert(key) } }
            if let additional = s["additionalProperties"] {
                for (key,child) in object where properties[key] == nil {
                    if additional != .bool(false) || !tolerant { _ = try evaluate(child, schema: additional, path: "\(path)/\(key)", tolerant: tolerant, schemaRoot: schemaRoot, depth: depth + 1) }
                    evaluated.insert(key)
                }
            }
            if s["unevaluatedProperties"] == .bool(false) && !tolerant && !Set(object.keys).subtracting(evaluated).isEmpty { try aguiFail("\(path) unknown fields") }
        }
        if let array = value.array {
            if case .number(let token) = s["minItems"], let minimum = Int(token), array.count < minimum { try aguiFail("\(path) too few items") }
            if case .number(let token) = s["maxItems"], let maximum = Int(token), array.count > maximum {try aguiFail("\(path) too many items")}
            if let items = s["items"] { for (index,child) in array.enumerated() { _ = try evaluate(child, schema: items, path: "\(path)/\(index)", tolerant: tolerant, schemaRoot: schemaRoot, depth: depth + 1) } }
        }
        if let string = value.string {
            if case .number(let token) = s["minLength"],let minimum=Int(token),string.unicodeScalars.count<minimum {try aguiFail("\(path) too short")}
            if case .number(let token) = s["maxLength"],let maximum=Int(token),string.unicodeScalars.count>maximum {try aguiFail("\(path) too long")}
        }
        if let string = value.string, let pattern = s.string("pattern"), string.range(of: pattern, options: .regularExpression) == nil { try aguiFail("\(path) fails pattern") }
        if case .number(let token) = value {
            if case .number(let minimum) = s["minimum"], AgUiValue.compareNumbers(token,minimum) < 0 { try aguiFail("\(path) below minimum") }
            if case .number(let maximum) = s["maximum"], AgUiValue.compareNumbers(token,maximum) > 0 { try aguiFail("\(path) above maximum") }
        }
        return evaluated
    }
}
