public enum SkillRiskLevel: String, Codable, CaseIterable, Comparable, Hashable, Sendable {
    case low
    case medium
    case high
    case critical

    public static func < (lhs: SkillRiskLevel, rhs: SkillRiskLevel) -> Bool {
        lhs.sortOrder < rhs.sortOrder
    }

    private var sortOrder: Int {
        switch self {
        case .low:
            return 0
        case .medium:
            return 1
        case .high:
            return 2
        case .critical:
            return 3
        }
    }
}
