import AtlasShared

public enum SkillSideEffectLevel: String, Codable, CaseIterable, Hashable, Sendable {
    case safeRead = "safe_read"
    case sensitiveRead = "sensitive_read"
    case draftWrite = "draft_write"
    case liveWrite = "live_write"
    case destructive
}

public struct SkillActionDefinition: Codable, Identifiable, Hashable, Sendable {
    public let id: String
    public let name: String
    public let description: String
    public let inputSchemaSummary: String
    public let outputSchemaSummary: String
    public let permissionLevel: PermissionLevel
    public let sideEffectLevel: SkillSideEffectLevel
    public let isEnabled: Bool
    public let preferredQueryTypes: [SkillQueryType]
    public let routingPriority: Int
    public let inputSchema: AtlasToolInputSchema

    /// The effective approval policy for this action.
    /// Set at runtime by the registry based on the user's stored preference.
    /// Defaults to the permission-level default when not explicitly overridden.
    public let approvalPolicy: ActionApprovalPolicy

    public init(
        id: String,
        name: String,
        description: String,
        inputSchemaSummary: String,
        outputSchemaSummary: String,
        permissionLevel: PermissionLevel,
        sideEffectLevel: SkillSideEffectLevel,
        isEnabled: Bool = true,
        preferredQueryTypes: [SkillQueryType] = [],
        routingPriority: Int = 0,
        inputSchema: AtlasToolInputSchema = AtlasToolInputSchema(properties: [:], additionalProperties: false),
        approvalPolicy: ActionApprovalPolicy? = nil
    ) {
        self.id = id
        self.name = name
        self.description = description
        self.inputSchemaSummary = inputSchemaSummary
        self.outputSchemaSummary = outputSchemaSummary
        self.permissionLevel = permissionLevel
        self.sideEffectLevel = sideEffectLevel
        self.isEnabled = isEnabled
        self.preferredQueryTypes = preferredQueryTypes
        self.routingPriority = routingPriority
        self.inputSchema = inputSchema
        self.approvalPolicy = approvalPolicy ?? ActionApprovalPolicy.defaultPolicy(for: permissionLevel)
    }

    public func updatingEnabled(_ enabled: Bool) -> SkillActionDefinition {
        SkillActionDefinition(
            id: id,
            name: name,
            description: description,
            inputSchemaSummary: inputSchemaSummary,
            outputSchemaSummary: outputSchemaSummary,
            permissionLevel: permissionLevel,
            sideEffectLevel: sideEffectLevel,
            isEnabled: enabled,
            preferredQueryTypes: preferredQueryTypes,
            routingPriority: routingPriority,
            inputSchema: inputSchema,
            approvalPolicy: approvalPolicy
        )
    }

    public func updatingApprovalPolicy(_ policy: ActionApprovalPolicy) -> SkillActionDefinition {
        SkillActionDefinition(
            id: id,
            name: name,
            description: description,
            inputSchemaSummary: inputSchemaSummary,
            outputSchemaSummary: outputSchemaSummary,
            permissionLevel: permissionLevel,
            sideEffectLevel: sideEffectLevel,
            isEnabled: isEnabled,
            preferredQueryTypes: preferredQueryTypes,
            routingPriority: routingPriority,
            inputSchema: inputSchema,
            approvalPolicy: policy
        )
    }
}
