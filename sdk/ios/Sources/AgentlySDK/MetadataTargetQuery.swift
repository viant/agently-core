import Foundation

func metadataTargetQueryItems(from targetContext: MetadataTargetContext?) -> [URLQueryItem] {
    var query: [URLQueryItem] = []
    if let platform = targetContext?.platform?.trimmingCharacters(in: .whitespacesAndNewlines), !platform.isEmpty {
        query.append(URLQueryItem(name: "platform", value: platform))
    }
    if let formFactor = targetContext?.formFactor?.trimmingCharacters(in: .whitespacesAndNewlines), !formFactor.isEmpty {
        query.append(URLQueryItem(name: "formFactor", value: formFactor))
    }
    if let surface = targetContext?.surface?.trimmingCharacters(in: .whitespacesAndNewlines), !surface.isEmpty {
        query.append(URLQueryItem(name: "surface", value: surface))
    }
    for capability in targetContext?.capabilities ?? [] {
        let trimmed = capability.trimmingCharacters(in: .whitespacesAndNewlines)
        if !trimmed.isEmpty {
            query.append(URLQueryItem(name: "capabilities", value: trimmed))
        }
    }
    return query
}
