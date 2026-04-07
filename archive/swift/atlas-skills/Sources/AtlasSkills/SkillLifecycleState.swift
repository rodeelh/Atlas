public enum SkillLifecycleState: String, Codable, CaseIterable, Hashable, Sendable {
    case known
    case proposed
    case installed
    case configured
    case enabled
    case disabled
    case failedValidation = "failed_validation"
}
