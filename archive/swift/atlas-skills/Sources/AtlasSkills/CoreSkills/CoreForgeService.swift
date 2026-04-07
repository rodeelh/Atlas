import Foundation
import AtlasGuard
import AtlasLogging
import AtlasShared

// MARK: - Forge Error

public enum ForgeError: LocalizedError, Sendable {
    case invalidSpec(String)
    case scaffoldFailed(String)

    public var errorDescription: String? {
        switch self {
        case .invalidSpec(let msg):    return "Invalid Forge skill spec: \(msg)"
        case .scaffoldFailed(let msg): return "Forge scaffold failed: \(msg)"
        }
    }
}

// MARK: - Input Types

/// Specification for a single action within a Forge-generated skill.
public struct ForgeActionSpec: Codable, Sendable {
    public let id: String
    public let name: String
    public let description: String
    public let permissionLevel: PermissionLevel
    /// The input schema presented to the LLM. Declares parameter names, types,
    /// and descriptions so the agent knows what to pass to this action.
    /// An empty schema means the action takes no parameters.
    public let inputSchema: AtlasToolInputSchema

    public init(
        id: String,
        name: String,
        description: String,
        permissionLevel: PermissionLevel,
        inputSchema: AtlasToolInputSchema = AtlasToolInputSchema(properties: [:], additionalProperties: false)
    ) {
        self.id = id
        self.name = name
        self.description = description
        self.permissionLevel = permissionLevel
        self.inputSchema = inputSchema
    }
}

/// Full specification for scaffolding a new Forge-generated skill.
public struct ForgeSkillSpec: Codable, Sendable {
    public let id: String
    public let name: String
    public let description: String
    public let category: SkillCategory
    public let riskLevel: SkillRiskLevel
    public let tags: [String]
    public let actions: [ForgeActionSpec]

    public init(
        id: String,
        name: String,
        description: String,
        category: SkillCategory,
        riskLevel: SkillRiskLevel,
        tags: [String] = [],
        actions: [ForgeActionSpec] = []
    ) {
        self.id = id
        self.name = name
        self.description = description
        self.category = category
        self.riskLevel = riskLevel
        self.tags = tags
        self.actions = actions
    }
}

// MARK: - Output Types

/// Result of a Forge scaffold operation — ready to register with `CoreSkillService.install(_:)`.
public struct ForgeScaffoldResult: Sendable {
    public let manifest: SkillManifest
    public let actionDefinitions: [SkillActionDefinition]
    /// Notes and warnings from scaffold for the caller to surface.
    public let notes: [String]
}

/// Result of validating a `ForgeSkillSpec` before scaffolding.
public struct ForgeValidationResult: Sendable {
    public let isValid: Bool
    public let issues: [String]
    public let warnings: [String]

    public var summary: String {
        if isValid && warnings.isEmpty { return "Skill spec is valid." }
        if isValid { return "Skill spec is valid with \(warnings.count) warning(s)." }
        return "Skill spec has \(issues.count) issue(s) and cannot be scaffolded."
    }
}

// MARK: - CoreForgeService

/// Forge scaffolding and validation primitives for the Dynamic Skill Runtime.
///
/// `validate(spec:)` checks spec correctness without side effects.
/// `scaffold(spec:)` generates a `SkillManifest` + action definitions that can be passed
/// directly to `CoreSkillService.install(_:)` after the caller implements `AtlasSkill.execute`.
///
/// Forge does not execute skills — it prepares them. The governed execution path
/// (`SkillExecutionGateway`) handles all runtime execution.
public struct CoreForgeService: Sendable {
    private let logger: AtlasLogger

    public init(logger: AtlasLogger = AtlasLogger(category: "core.forge")) {
        self.logger = logger
    }

    // MARK: - Validation

    /// Validate a `ForgeSkillSpec` before scaffolding. Pure — no side effects.
    public func validate(spec: ForgeSkillSpec) -> ForgeValidationResult {
        var issues: [String] = []
        var warnings: [String] = []

        // ID rules
        if spec.id.isEmpty {
            issues.append("Skill ID must not be empty.")
        } else if spec.id.contains(" ") {
            issues.append("Skill ID must not contain spaces — use hyphens.")
        } else if spec.id.hasPrefix("core.") || spec.id.hasPrefix("atlas.") {
            issues.append("Skill ID must not use reserved prefixes 'core.' or 'atlas.'")
        }

        // Name / description
        if spec.name.isEmpty {
            issues.append("Skill name must not be empty.")
        } else if spec.name.count > 64 {
            warnings.append("Skill name exceeds 64 characters — consider shortening.")
        }

        if spec.description.isEmpty {
            issues.append("Skill description must not be empty.")
        }

        // Actions
        if spec.actions.isEmpty {
            warnings.append("Skill has no actions defined — it will not be callable by the agent.")
        }

        let actionIDs = spec.actions.map(\.id)
        if Set(actionIDs).count != actionIDs.count {
            issues.append("Duplicate action IDs detected.")
        }

        for action in spec.actions {
            if action.id.isEmpty {
                issues.append("Action name '\(action.name)' has an empty ID.")
            } else if !action.id.hasPrefix(spec.id + ".") {
                warnings.append("Action '\(action.id)' should be namespaced under '\(spec.id).' for clarity.")
            }
        }

        return ForgeValidationResult(isValid: issues.isEmpty, issues: issues, warnings: warnings)
    }

    // MARK: - Scaffold

    /// Generate a `SkillManifest` and `SkillActionDefinition` list from a validated spec.
    ///
    /// The caller is responsible for:
    /// 1. Implementing `AtlasSkill.execute(actionID:input:context:)` for each action.
    /// 2. Calling `CoreSkillService.install(_:)` to register the skill.
    /// 3. Calling `CoreSkillService.enable(skillID:)` when the skill is ready.
    ///
    /// Throws `ForgeError.invalidSpec` if the spec fails validation.
    public func scaffold(spec: ForgeSkillSpec) throws -> ForgeScaffoldResult {
        let validation = validate(spec: spec)
        guard validation.isValid else {
            throw ForgeError.invalidSpec(validation.issues.joined(separator: "; "))
        }

        logger.info("CoreForge scaffolding skill", metadata: [
            "skill_id": spec.id,
            "actions": "\(spec.actions.count)"
        ])

        let manifest = SkillManifest(
            id: spec.id,
            name: spec.name,
            version: "1.0.0",
            description: spec.description,
            category: spec.category,
            lifecycleState: .installed,
            capabilities: [],
            requiredPermissions: [],
            riskLevel: spec.riskLevel,
            supportsReadOnlyMode: spec.riskLevel == .low,
            isUserVisible: true,
            isEnabledByDefault: false,
            author: "Forge",
            source: "forge",
            tags: spec.tags
        )

        let actionDefs = spec.actions.map { action in
            SkillActionDefinition(
                id: action.id,
                name: action.name,
                description: action.description,
                inputSchemaSummary: action.inputSchema.properties.isEmpty
                    ? "No parameters required."
                    : action.inputSchema.properties.keys.sorted().joined(separator: ", "),
                outputSchemaSummary: "Returns the raw HTTP response body as text.",
                permissionLevel: action.permissionLevel,
                sideEffectLevel: sideEffectLevel(for: action.permissionLevel),
                inputSchema: action.inputSchema
            )
        }

        var notes = validation.warnings
        notes.append("Implement AtlasSkill.execute(actionID:input:context:) for each action.")
        notes.append("Register via CoreSkillService.install(_:), then enable via CoreSkillService.enable(skillID:).")

        return ForgeScaffoldResult(
            manifest: manifest,
            actionDefinitions: actionDefs,
            notes: notes
        )
    }

    // MARK: - Package Assembly

    /// Build a complete `ForgeSkillPackage` from a validated spec and its execution plans.
    ///
    /// This is the final step before installation:
    /// 1. Call `validate(spec:)` to confirm the spec is well-formed.
    /// 2. Call `scaffold(spec:)` to generate the manifest and action definitions.
    /// 3. Call `buildPackage(spec:plans:)` with the same spec and the execution plans.
    /// 4. Pass the returned package to `CoreSkillService.installForgeSkill(package:actionDefinitions:)`.
    ///
    /// Throws `ForgeError.invalidSpec` if `plans` references action IDs not defined in `spec`.
    public func buildPackage(
        spec: ForgeSkillSpec,
        plans: [ForgeActionPlan]
    ) throws -> (package: ForgeSkillPackage, actionDefinitions: [SkillActionDefinition]) {
        let scaffoldResult = try scaffold(spec: spec)

        // Validate that every plan maps to a declared action in the spec.
        let specActionIDs = Set(spec.actions.map(\.id))
        for plan in plans {
            if !specActionIDs.contains(plan.actionID) {
                throw ForgeError.invalidSpec(
                    "Plan references action '\(plan.actionID)' which is not declared in the spec."
                )
            }
        }

        let package = ForgeSkillPackage(
            manifest: scaffoldResult.manifest,
            actions: plans
        )

        logger.info("CoreForge assembled skill package", metadata: [
            "skill_id": spec.id,
            "plans": "\(plans.count)"
        ])

        return (package, scaffoldResult.actionDefinitions)
    }

    // MARK: - Private

    private func sideEffectLevel(for permissionLevel: PermissionLevel) -> SkillSideEffectLevel {
        switch permissionLevel {
        case .read:    return .safeRead
        case .draft:   return .draftWrite
        case .execute: return .liveWrite
        }
    }
}
