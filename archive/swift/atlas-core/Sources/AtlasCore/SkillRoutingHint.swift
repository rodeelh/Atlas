public struct SkillRoutingHint: Codable, Hashable, Sendable {
    public let text: String
    public let targetSkillID: String?

    public init(text: String, targetSkillID: String? = nil) {
        self.text = text
        self.targetSkillID = targetSkillID
    }
}
