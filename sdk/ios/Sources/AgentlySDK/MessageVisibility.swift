import Foundation

func isInternalMessageMode(_ mode: String?) -> Bool {
    ["router", "chain"].contains(mode?.trimmingCharacters(in: .whitespacesAndNewlines).lowercased() ?? "")
}
