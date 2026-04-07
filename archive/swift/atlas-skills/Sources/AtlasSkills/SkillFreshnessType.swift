public enum SkillFreshnessType: String, Codable, CaseIterable, Hashable, Sendable {
    case staticKnowledge = "static_knowledge"
    case local
    case live
    case external
}
