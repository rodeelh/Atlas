import AtlasSkills

public struct SkillRoutingContext: Sendable {
    public let userMessage: String
    public let enabledSkills: [AtlasSkillRecord]
    public let actionCatalog: [SkillActionCatalogItem]

    public init(
        userMessage: String,
        enabledSkills: [AtlasSkillRecord],
        actionCatalog: [SkillActionCatalogItem]
    ) {
        self.userMessage = userMessage
        self.enabledSkills = enabledSkills
        self.actionCatalog = actionCatalog
    }
}
