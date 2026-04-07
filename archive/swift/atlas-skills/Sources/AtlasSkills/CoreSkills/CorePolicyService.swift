import Foundation
import AtlasGuard
import AtlasLogging
import AtlasShared

// MARK: - Output Types

/// The result of evaluating an action's policy requirements.
public struct CorePolicyEvaluationResult: Sendable {
    public let actionID: String
    public let permissionLevel: PermissionLevel
    public let policy: ActionApprovalPolicy
    public let requiresApproval: Bool
    public let reason: String
}

// MARK: - CorePolicyService

/// Internal policy evaluation and audit primitive for CoreSkills and Forge.
///
/// This does NOT replace `SkillPolicyEngine` or `SkillExecutionGateway` — it is a
/// lightweight introspection layer for internal components that need to pre-check
/// policy before routing a request through the governed execution path.
///
/// All actual skill execution still goes through `SkillExecutionGateway`.
public struct CorePolicyService: Sendable {
    private let logger: AtlasLogger

    public init(logger: AtlasLogger = AtlasLogger(category: "core.policy")) {
        self.logger = logger
    }

    /// Evaluate whether an action at a given permission level requires approval
    /// under the specified policy. Returns the result without executing any action.
    public func evaluate(
        actionID: String,
        permissionLevel: PermissionLevel,
        policy: ActionApprovalPolicy
    ) -> CorePolicyEvaluationResult {
        // Risk floors from SkillPolicyEngine:
        // execute-level actions always require approval regardless of user policy.
        let requiresApproval: Bool
        let reason: String

        switch permissionLevel {
        case .execute:
            requiresApproval = true
            reason = "Execute-level actions always require approval (risk floor)."
        case .draft:
            requiresApproval = (policy == .alwaysAsk || policy == .askOnce)
            reason = requiresApproval
                ? "Draft-level action requires approval under '\(policy.rawValue)' policy."
                : "Draft-level action is auto-approved under '\(policy.rawValue)' policy."
        case .read:
            requiresApproval = (policy == .alwaysAsk)
            reason = requiresApproval
                ? "Read-level action requires approval under '\(policy.rawValue)' policy."
                : "Read-level action is auto-approved."
        }

        return CorePolicyEvaluationResult(
            actionID: actionID,
            permissionLevel: permissionLevel,
            policy: policy,
            requiresApproval: requiresApproval,
            reason: reason
        )
    }

    /// Emit a structured internal audit event via the logger.
    /// All values passed here should be safe for logs — no secrets, no user content.
    public func audit(
        event: String,
        skillID: String,
        actionID: String,
        metadata: [String: String] = [:]
    ) {
        var enriched = metadata
        enriched["event"]     = event
        enriched["skill_id"]  = skillID
        enriched["action_id"] = actionID
        logger.info("CorePolicy audit", metadata: enriched)
    }
}
