import AtlasLogging
import AtlasShared

public struct SkillPolicyDecision: Hashable, Sendable {
    public let isAllowed: Bool
    public let requiresApproval: Bool
    public let approvalPolicy: ActionApprovalPolicy
    public let reason: String

    public init(isAllowed: Bool, requiresApproval: Bool, approvalPolicy: ActionApprovalPolicy, reason: String) {
        self.isAllowed = isAllowed
        self.requiresApproval = requiresApproval
        self.approvalPolicy = approvalPolicy
        self.reason = reason
    }
}

public struct SkillPolicyEngine: Sendable {
    private let logger: AtlasLogger

    public init(logger: AtlasLogger = AtlasLogger(category: "skills-policy")) {
        self.logger = logger
    }

    /// Evaluate whether a skill action is allowed and what approval policy applies.
    ///
    /// The `effectivePolicy` parameter is the user's stored preference (or the permission-level
    /// default) resolved by the gateway before calling this method. The engine uses it together
    /// with the action's side-effect level to produce a final decision.
    public func evaluate(
        manifest: SkillManifest,
        action: SkillActionDefinition,
        config: AtlasConfig,
        effectivePolicy: ActionApprovalPolicy,
        workflowExecution: AtlasWorkflowExecutionContext? = nil
    ) -> SkillPolicyDecision {
        let decision: SkillPolicyDecision
        let workflowGrantAllowsSensitiveRead = workflowExecution?.trustScope.allowsSensitiveRead == true &&
            workflowExecution?.approvalGrantedAt != nil
        let workflowGrantAllowsLiveWrite = workflowExecution?.trustScope.allowsLiveWrite == true &&
            workflowExecution?.approvalGrantedAt != nil

        switch action.sideEffectLevel {
        case .safeRead:
            decision = SkillPolicyDecision(
                isAllowed: true,
                requiresApproval: effectivePolicy != .autoApprove,
                approvalPolicy: effectivePolicy,
                reason: "Safe read action."
            )
        case .sensitiveRead:
            if workflowGrantAllowsSensitiveRead {
                decision = SkillPolicyDecision(
                    isAllowed: true,
                    requiresApproval: false,
                    approvalPolicy: .autoApprove,
                    reason: "Workflow trust grant covers sensitive read access."
                )
            } else {
                // Sensitive reads always require approval regardless of user policy
                decision = SkillPolicyDecision(
                    isAllowed: true,
                    requiresApproval: true,
                    approvalPolicy: .alwaysAsk,
                    reason: "Sensitive read access requires approval."
                )
            }
        case .draftWrite:
            let requiresApproval = effectivePolicy != .autoApprove
            decision = SkillPolicyDecision(
                isAllowed: true,
                requiresApproval: requiresApproval,
                approvalPolicy: effectivePolicy,
                reason: requiresApproval
                    ? "Draft write action requires approval."
                    : "Draft write action is auto-approved by user policy."
            )
        case .liveWrite:
            if workflowGrantAllowsLiveWrite {
                decision = SkillPolicyDecision(
                    isAllowed: true,
                    requiresApproval: false,
                    approvalPolicy: .autoApprove,
                    reason: "Workflow trust grant covers live write access."
                )
            } else {
                // Live writes always require approval regardless of user policy
                decision = SkillPolicyDecision(
                    isAllowed: true,
                    requiresApproval: true,
                    approvalPolicy: .alwaysAsk,
                    reason: "Live write action always requires approval."
                )
            }
        case .destructive:
            decision = SkillPolicyDecision(
                isAllowed: false,
                requiresApproval: false,
                approvalPolicy: .alwaysAsk,
                reason: "Destructive skill actions are prohibited in Skills System v1."
            )
        }

        logger.debug("Evaluated skill policy", metadata: [
            "skill_id": manifest.id,
            "action_id": action.id,
            "allowed": decision.isAllowed ? "true" : "false",
            "requires_approval": decision.requiresApproval ? "true" : "false",
            "approval_policy": decision.approvalPolicy.rawValue,
            "risk": manifest.riskLevel.rawValue
        ])

        return decision
    }

}
