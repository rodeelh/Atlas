/// A trigger phrase that routes user messages to a skill.
/// A longer, more specific phrase always wins over a shorter, generic one when
/// multiple skills match the same message.
public struct SkillTrigger: Codable, Hashable, Sendable {
    /// Lowercase phrase to match against the normalised user message.
    public let phrase: String
    /// Optional query-type hint carried by this trigger. Falls back to the
    /// skill's first `preferredQueryTypes` entry when nil.
    public let queryType: SkillQueryType?

    public init(_ phrase: String, queryType: SkillQueryType? = nil) {
        self.phrase = phrase
        self.queryType = queryType
    }
}

public struct SkillManifest: Codable, Identifiable, Hashable, Sendable {
    public let id: String
    public let name: String
    public let version: String
    public let description: String
    public let category: SkillCategory
    public let lifecycleState: SkillLifecycleState
    public let capabilities: [SkillCapability]
    public let requiredPermissions: [SkillPermission]
    public let requiredSecrets: [String]
    public let riskLevel: SkillRiskLevel
    public let trustProfile: SkillTrustProfile
    public let freshnessType: SkillFreshnessType
    public let preferredQueryTypes: [SkillQueryType]
    public let routingPriority: Int
    public let canAnswerStructuredLiveData: Bool
    public let canHandleLocalData: Bool
    public let canHandleExploratoryQueries: Bool
    public let allowedDomains: [String]?
    public let restrictionsSummary: [String]
    public let supportsReadOnlyMode: Bool
    public let isUserVisible: Bool
    public let isEnabledByDefault: Bool
    public let author: String?
    public let source: String?
    public let tags: [String]

    // MARK: - Routing contract (new in v0.3)

    /// The routing intent this skill serves. The classifier uses this to set
    /// the conversation intent when a trigger phrase matches.
    public let intent: SkillIntent

    /// Natural-language trigger phrases that route messages to this skill.
    /// The classifier picks the *longest* matching phrase across all skills,
    /// breaking ties by `routingPriority`. Each trigger may carry an optional
    /// `queryType` hint for fine-grained sub-routing.
    public let triggers: [SkillTrigger]

    /// When true, this skill's tools are included in every AI request regardless
    /// of smart tool selection tier. Use for critical infrastructure skills
    /// (e.g. AtlasInfoSkill) that must always be available.
    public let alwaysInclude: Bool

    public init(
        id: String,
        name: String,
        version: String,
        description: String,
        category: SkillCategory,
        lifecycleState: SkillLifecycleState,
        capabilities: [SkillCapability],
        requiredPermissions: [SkillPermission],
        requiredSecrets: [String] = [],
        riskLevel: SkillRiskLevel,
        trustProfile: SkillTrustProfile = .general,
        freshnessType: SkillFreshnessType = .staticKnowledge,
        preferredQueryTypes: [SkillQueryType] = [],
        routingPriority: Int = 0,
        canAnswerStructuredLiveData: Bool = false,
        canHandleLocalData: Bool = false,
        canHandleExploratoryQueries: Bool = false,
        allowedDomains: [String]? = nil,
        restrictionsSummary: [String] = [],
        supportsReadOnlyMode: Bool,
        isUserVisible: Bool = true,
        isEnabledByDefault: Bool = false,
        author: String? = nil,
        source: String? = nil,
        tags: [String] = [],
        intent: SkillIntent = .generalReasoning,
        triggers: [SkillTrigger] = [],
        alwaysInclude: Bool = false
    ) {
        self.id = id
        self.name = name
        self.version = version
        self.description = description
        self.category = category
        self.lifecycleState = lifecycleState
        self.capabilities = capabilities
        self.requiredPermissions = requiredPermissions
        self.requiredSecrets = requiredSecrets
        self.riskLevel = riskLevel
        self.trustProfile = trustProfile
        self.freshnessType = freshnessType
        self.preferredQueryTypes = preferredQueryTypes
        self.routingPriority = routingPriority
        self.canAnswerStructuredLiveData = canAnswerStructuredLiveData
        self.canHandleLocalData = canHandleLocalData
        self.canHandleExploratoryQueries = canHandleExploratoryQueries
        self.allowedDomains = allowedDomains
        self.restrictionsSummary = restrictionsSummary
        self.supportsReadOnlyMode = supportsReadOnlyMode
        self.isUserVisible = isUserVisible
        self.isEnabledByDefault = isEnabledByDefault
        self.author = author
        self.source = source
        self.tags = tags
        self.intent = intent
        self.triggers = triggers
        self.alwaysInclude = alwaysInclude
    }

    public func updatingLifecycleState(_ lifecycleState: SkillLifecycleState) -> SkillManifest {
        SkillManifest(
            id: id,
            name: name,
            version: version,
            description: description,
            category: category,
            lifecycleState: lifecycleState,
            capabilities: capabilities,
            requiredPermissions: requiredPermissions,
            requiredSecrets: requiredSecrets,
            riskLevel: riskLevel,
            trustProfile: trustProfile,
            freshnessType: freshnessType,
            preferredQueryTypes: preferredQueryTypes,
            routingPriority: routingPriority,
            canAnswerStructuredLiveData: canAnswerStructuredLiveData,
            canHandleLocalData: canHandleLocalData,
            canHandleExploratoryQueries: canHandleExploratoryQueries,
            allowedDomains: allowedDomains,
            restrictionsSummary: restrictionsSummary,
            supportsReadOnlyMode: supportsReadOnlyMode,
            isUserVisible: isUserVisible,
            isEnabledByDefault: isEnabledByDefault,
            author: author,
            source: source,
            tags: tags,
            intent: intent,
            triggers: triggers,
            alwaysInclude: alwaysInclude
        )
    }
}
