public enum SkillTrustProfile: String, Codable, CaseIterable, Hashable, Sendable {
    case exactStructured = "exact_structured"
    case localExact = "local_exact"
    case exploratory
    case operational
    case general
}
