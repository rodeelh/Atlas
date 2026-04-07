import AtlasShared

public struct SkillActionCatalogItem: Codable, Hashable, Sendable {
    public let skillID: String
    public let skillName: String
    public let skillDescription: String
    public let skillCategory: SkillCategory
    public let trustProfile: SkillTrustProfile
    public let freshnessType: SkillFreshnessType
    public let action: SkillActionDefinition
    public let riskLevel: SkillRiskLevel
    public let preferredQueryTypes: [SkillQueryType]
    public let routingPriority: Int
    public let canAnswerStructuredLiveData: Bool
    public let canHandleLocalData: Bool
    public let canHandleExploratoryQueries: Bool
    public let alwaysInclude: Bool

    public init(
        skillID: String,
        skillName: String,
        skillDescription: String,
        skillCategory: SkillCategory,
        trustProfile: SkillTrustProfile,
        freshnessType: SkillFreshnessType,
        action: SkillActionDefinition,
        riskLevel: SkillRiskLevel,
        preferredQueryTypes: [SkillQueryType],
        routingPriority: Int,
        canAnswerStructuredLiveData: Bool,
        canHandleLocalData: Bool,
        canHandleExploratoryQueries: Bool,
        alwaysInclude: Bool = false
    ) {
        self.skillID = skillID
        self.skillName = skillName
        self.skillDescription = skillDescription
        self.skillCategory = skillCategory
        self.trustProfile = trustProfile
        self.freshnessType = freshnessType
        self.action = action
        self.riskLevel = riskLevel
        self.preferredQueryTypes = preferredQueryTypes
        self.routingPriority = routingPriority
        self.canAnswerStructuredLiveData = canAnswerStructuredLiveData
        self.canHandleLocalData = canHandleLocalData
        self.canHandleExploratoryQueries = canHandleExploratoryQueries
        self.alwaysInclude = alwaysInclude
    }

    public var toolName: String {
        Self.toolName(skillID: skillID, actionID: action.id)
    }

    public var toolDefinition: AtlasToolDefinition {
        AtlasToolDefinition(
            name: toolName,
            description: "\(skillName): \(action.description)",
            inputSchema: action.inputSchema
        )
    }

    public static func toolName(skillID: String, actionID: String) -> String {
        let normalizedSkillID = normalize(skillID)
        let normalizedActionID = normalize(actionID)
        return "skill__\(normalizedSkillID)__\(normalizedActionID)"
    }

    private static func normalize(_ value: String) -> String {
        let lowered = value.lowercased()
        let scalars = lowered.unicodeScalars.map { scalar -> Character in
            switch scalar {
            case "a"..."z", "0"..."9":
                return Character(scalar)
            default:
                return "_"
            }
        }

        let raw = String(scalars)
        let components = raw.split(separator: "_").filter { !$0.isEmpty }
        return components.joined(separator: "_")
    }
}
