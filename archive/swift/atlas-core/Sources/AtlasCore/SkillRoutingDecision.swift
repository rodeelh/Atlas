import AtlasSkills

public struct SkillRoutingDecision: Codable, Hashable, Sendable {
    public let intent: SkillIntent
    public let queryType: SkillQueryType?
    public let preferredSkills: [String]
    public let deprioritizedSkills: [String]
    public let suppressedSkills: [String]
    public let routingHints: [SkillRoutingHint]
    public let confidence: Double
    public let explanation: String

    public init(
        intent: SkillIntent,
        queryType: SkillQueryType? = nil,
        preferredSkills: [String] = [],
        deprioritizedSkills: [String] = [],
        suppressedSkills: [String] = [],
        routingHints: [SkillRoutingHint] = [],
        confidence: Double = 0,
        explanation: String = ""
    ) {
        self.intent = intent
        self.queryType = queryType
        self.preferredSkills = preferredSkills
        self.deprioritizedSkills = deprioritizedSkills
        self.suppressedSkills = suppressedSkills
        self.routingHints = routingHints
        self.confidence = confidence
        self.explanation = explanation
    }

    public var hasGuidance: Bool {
        !preferredSkills.isEmpty || !deprioritizedSkills.isEmpty || !routingHints.isEmpty
    }
}
