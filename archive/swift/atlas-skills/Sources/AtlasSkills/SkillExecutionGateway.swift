import Foundation
import AtlasGuard
import AtlasLogging
import AtlasShared
import AtlasTools

public enum SkillExecutionGatewayError: LocalizedError {
    case skillNotFound(String)
    case skillNotEnabled(String)
    case actionNotFound(String)
    case actionDisabled(String)
    case validationFailed(String)

    public var errorDescription: String? {
        switch self {
        case .skillNotFound(let skillID):
            return "The skill '\(skillID)' is not registered."
        case .skillNotEnabled(let skillName):
            return "The skill '\(skillName)' is not enabled."
        case .actionNotFound(let actionID):
            return "The action '\(actionID)' is not available."
        case .actionDisabled(let actionName):
            return "The action '\(actionName)' is disabled."
        case .validationFailed(let message):
            return message
        }
    }
}

public actor SkillExecutionGateway {
    private let registry: SkillRegistry
    private let policyEngine: SkillPolicyEngine
    private let policyStore: ActionPolicyStore
    private let approvalManager: ToolApprovalManager
    private let deferredExecutionManager: (any DeferredExecutionManaging)?
    private let auditStore: SkillAuditStore
    private let logger: AtlasLogger

    public init(
        registry: SkillRegistry,
        policyEngine: SkillPolicyEngine,
        policyStore: ActionPolicyStore,
        approvalManager: ToolApprovalManager,
        deferredExecutionManager: (any DeferredExecutionManaging)? = nil,
        auditStore: SkillAuditStore,
        logger: AtlasLogger = AtlasLogger(category: "skills")
    ) {
        self.registry = registry
        self.policyEngine = policyEngine
        self.policyStore = policyStore
        self.approvalManager = approvalManager
        self.deferredExecutionManager = deferredExecutionManager
        self.auditStore = auditStore
        self.logger = logger
    }

    public func execute(
        _ request: SkillExecutionRequest,
        context: SkillExecutionContext
    ) async throws -> SkillExecutionResult {
        guard let record = await registry.skill(id: request.skillID) else {
            throw SkillExecutionGatewayError.skillNotFound(request.skillID)
        }

        guard let skill = await registry.skillImplementation(id: request.skillID) else {
            throw SkillExecutionGatewayError.skillNotFound(request.skillID)
        }

        guard record.manifest.lifecycleState == .enabled else {
            throw SkillExecutionGatewayError.skillNotEnabled(record.manifest.name)
        }

        if let validation = record.validation, validation.status == .failed {
            throw SkillExecutionGatewayError.validationFailed(
                validation.summary.isEmpty
                    ? "The skill '\(record.manifest.name)' failed validation."
                    : validation.summary
            )
        }

        guard let action = record.actions.first(where: { $0.id == request.actionID }) else {
            throw SkillExecutionGatewayError.actionNotFound(request.actionID)
        }

        guard action.isEnabled else {
            throw SkillExecutionGatewayError.actionDisabled(action.name)
        }

        await auditStore.record(
            SkillAuditEvent(
                skillID: request.skillID,
                actionID: request.actionID,
                actionName: action.name,
                conversationID: request.conversationID,
                approvalRequired: false,
                outcome: .requested,
                inputSummary: inputSummary(for: request)
            )
        )

        do {
            try await context.permissionManager.validate(level: action.permissionLevel)
        } catch {
            await auditStore.record(
                SkillAuditEvent(
                    skillID: request.skillID,
                    actionID: request.actionID,
                    actionName: action.name,
                    conversationID: request.conversationID,
                    approvalRequired: false,
                    approvalResult: .denied,
                    outcome: .denied,
                    inputSummary: inputSummary(for: request),
                    errorMessage: error.localizedDescription
                )
            )
            throw ToolExecutionError.denied(error.localizedDescription)
        }

        // Resolve effective policy from the store
        let effectivePolicy = await policyStore.effectivePolicy(
            for: request.actionID,
            permissionLevel: action.permissionLevel
        )

        let decision = policyEngine.evaluate(
            manifest: record.manifest,
            action: action,
            config: context.config,
            effectivePolicy: effectivePolicy,
            workflowExecution: context.workflowExecution
        )

        guard decision.isAllowed else {
            await auditStore.record(
                SkillAuditEvent(
                    skillID: request.skillID,
                    actionID: request.actionID,
                    actionName: action.name,
                    conversationID: request.conversationID,
                    approvalRequired: false,
                    approvalResult: .denied,
                    outcome: .denied,
                    inputSummary: inputSummary(for: request),
                    errorMessage: decision.reason
                )
            )
            throw ToolExecutionError.denied(decision.reason)
        }

        if decision.requiresApproval {
            try await routeApproval(
                request: request,
                record: record,
                action: action,
                decision: decision
            )
        }

        // Mark running if deferred manager is present
        if let deferredExecutionManager {
            try? await deferredExecutionManager.markRunning(toolCallID: request.toolCallID)
        }

        do {
            let result = try await skill.execute(
                actionID: request.actionID,
                input: request.input,
                context: context
            )

            if let deferredExecutionManager {
                try? await deferredExecutionManager.markCompleted(
                    toolCallID: request.toolCallID,
                    result: DeferredExecutionResult(
                        output: result.output,
                        success: true,
                        summary: result.summary,
                        metadata: result.metadata
                    )
                )
            }

            // If this was an askOnce action that just succeeded, promote to autoApprove
            if decision.approvalPolicy == .askOnce {
                await policyStore.promoteToAutoApprove(actionID: request.actionID)
            }

            await auditStore.record(
                SkillAuditEvent(
                    skillID: request.skillID,
                    actionID: request.actionID,
                    actionName: action.name,
                    conversationID: request.conversationID,
                    approvalRequired: decision.requiresApproval,
                    approvalResult: decision.requiresApproval ? .approved : nil,
                    outcome: .executed,
                    inputSummary: inputSummary(for: request),
                    outputSummary: summarize(result.summary)
                )
            )

            return result
        } catch let error as ToolExecutionError {
            if let _ = deferredExecutionManager, case .approvalRequired = error {
                // intentionally skip markFailed — deferred record stays in .pendingApproval
            } else if let deferredExecutionManager {
                try? await deferredExecutionManager.markFailed(
                    toolCallID: request.toolCallID,
                    errorMessage: error.localizedDescription
                )
            }
            await auditStore.record(
                SkillAuditEvent(
                    skillID: request.skillID,
                    actionID: request.actionID,
                    actionName: action.name,
                    conversationID: request.conversationID,
                    approvalRequired: decision.requiresApproval,
                    approvalResult: decision.requiresApproval ? .denied : nil,
                    outcome: .failed,
                    inputSummary: inputSummary(for: request),
                    errorMessage: error.localizedDescription
                )
            )
            throw error
        } catch {
            if let deferredExecutionManager {
                try? await deferredExecutionManager.markFailed(
                    toolCallID: request.toolCallID,
                    errorMessage: error.localizedDescription
                )
            }
            await auditStore.record(
                SkillAuditEvent(
                    skillID: request.skillID,
                    actionID: request.actionID,
                    actionName: action.name,
                    conversationID: request.conversationID,
                    approvalRequired: decision.requiresApproval,
                    approvalResult: decision.requiresApproval ? .approved : nil,
                    outcome: .failed,
                    inputSummary: inputSummary(for: request),
                    errorMessage: error.localizedDescription
                )
            )
            throw ToolExecutionError.failed(error.localizedDescription)
        }
    }

    // MARK: - Approval Routing

    private func routeApproval(
        request: SkillExecutionRequest,
        record: AtlasSkillRecord,
        action: SkillActionDefinition,
        decision: SkillPolicyDecision
    ) async throws {
        if let deferredExecutionManager {
            try await routeDeferredApproval(
                request: request,
                record: record,
                action: action,
                decision: decision,
                manager: deferredExecutionManager
            )
        } else {
            try await routeLegacyApproval(
                request: request,
                record: record,
                action: action,
                decision: decision
            )
        }
    }

    private func routeDeferredApproval(
        request: SkillExecutionRequest,
        record: AtlasSkillRecord,
        action: SkillActionDefinition,
        decision: SkillPolicyDecision,
        manager: any DeferredExecutionManaging
    ) async throws {
        switch await manager.approvalStatus(for: request.toolCallID) {
        case .approved:
            logger.info("Running approved skill action", metadata: [
                "skill_id": request.skillID,
                "action_id": request.actionID
            ])
        case .denied:
            await auditStore.record(
                SkillAuditEvent(
                    skillID: request.skillID,
                    actionID: request.actionID,
                    actionName: action.name,
                    conversationID: request.conversationID,
                    approvalRequired: true,
                    approvalResult: .denied,
                    outcome: .denied,
                    inputSummary: inputSummary(for: request),
                    errorMessage: "Skill execution was denied."
                )
            )
            throw ToolExecutionError.denied("Skill execution was denied for '\(record.manifest.name)'.")
        case .pending:
            if let existing = await manager.approvalRequest(for: request.toolCallID) {
                throw ToolExecutionError.approvalRequired(existing)
            }

            let pending = try await createDeferredApproval(
                request: request, record: record, action: action, manager: manager
            )
            throw ToolExecutionError.approvalRequired(pending)
        case nil:
            let pending = try await createDeferredApproval(
                request: request, record: record, action: action, manager: manager
            )
            throw ToolExecutionError.approvalRequired(pending)
        }
    }

    private func createDeferredApproval(
        request: SkillExecutionRequest,
        record: AtlasSkillRecord,
        action: SkillActionDefinition,
        manager: any DeferredExecutionManaging
    ) async throws -> ApprovalRequest {
        let pending = try await manager.createDeferredSkillApproval(
            skillID: request.skillID,
            actionID: request.actionID,
            toolCallID: request.toolCallID,
            normalizedInputJSON: request.input.argumentsJSON,
            conversationID: request.conversationID,
            originatingMessageID: nil,
            summary: action.name,
            permissionLevel: action.permissionLevel,
            riskLevel: record.manifest.riskLevel.rawValue
        )

        await auditStore.record(
            SkillAuditEvent(
                skillID: request.skillID,
                actionID: request.actionID,
                actionName: action.name,
                conversationID: request.conversationID,
                approvalRequired: true,
                approvalResult: .pending,
                outcome: .requested,
                inputSummary: inputSummary(for: request)
            )
        )
        return pending
    }

    private func routeLegacyApproval(
        request: SkillExecutionRequest,
        record: AtlasSkillRecord,
        action: SkillActionDefinition,
        decision: SkillPolicyDecision
    ) async throws {
        let approval = try await approvalRequest(
            for: request,
            record: record,
            action: action
        )

        switch await approvalManager.status(for: request.toolCallID) {
        case .approved:
            logger.info("Running approved skill action", metadata: [
                "skill_id": request.skillID,
                "action_id": request.actionID
            ])
        case .denied:
            await auditStore.record(
                SkillAuditEvent(
                    skillID: request.skillID,
                    actionID: request.actionID,
                    actionName: action.name,
                    conversationID: request.conversationID,
                    approvalRequired: true,
                    approvalResult: .denied,
                    outcome: .denied,
                    inputSummary: inputSummary(for: request),
                    errorMessage: "Skill execution was denied."
                )
            )
            throw ToolExecutionError.denied("Skill execution was denied for '\(record.manifest.name)'.")
        case .pending:
            if let existing = await approvalManager.getPendingApprovals().first(where: { $0.toolCallID == request.toolCallID }) {
                throw ToolExecutionError.approvalRequired(existing)
            }
            fallthrough
        case nil:
            let pending = await approvalManager.createApprovalRequest(
                toolCall: approval.toolCall,
                conversationID: request.conversationID
            )

            await auditStore.record(
                SkillAuditEvent(
                    skillID: request.skillID,
                    actionID: request.actionID,
                    actionName: action.name,
                    conversationID: request.conversationID,
                    approvalRequired: true,
                    approvalResult: .pending,
                    outcome: .requested,
                    inputSummary: inputSummary(for: request)
                )
            )
            throw ToolExecutionError.approvalRequired(pending)
        }
    }

    // MARK: - Helpers

    private func approvalRequest(
        for request: SkillExecutionRequest,
        record: AtlasSkillRecord,
        action: SkillActionDefinition
    ) async throws -> ApprovalRequest {
        let toolCall = AtlasToolCall(
            id: request.toolCallID,
            toolName: SkillActionCatalogItem.toolName(skillID: record.manifest.id, actionID: action.id),
            argumentsJSON: request.input.argumentsJSON,
            permissionLevel: action.permissionLevel,
            requiresApproval: true,
            status: .pending
        )

        return ApprovalRequest(
            id: UUID(),
            toolCall: toolCall,
            conversationID: request.conversationID,
            createdAt: .now,
            resolvedAt: nil,
            status: .pending
        )
    }

    private func summarize(_ value: String?, limit: Int = 200) -> String? {
        guard let value else { return nil }
        let trimmed = value.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else { return nil }
        return String(trimmed.prefix(limit))
    }

    private func inputSummary(for request: SkillExecutionRequest) -> String? {
        guard
            let data = request.input.argumentsJSON.data(using: .utf8),
            let object = try? JSONSerialization.jsonObject(with: data) as? [String: Any]
        else {
            return summarize(request.input.argumentsJSON)
        }

        switch (request.skillID, request.actionID) {
        case ("system-actions", "system.copy_to_clipboard"):
            let count = (object["text"] as? String)?.count ?? 0
            return "Copy \(count) characters to clipboard"
        case ("system-actions", "system.send_notification"):
            let title = summarize(object["title"] as? String) ?? "Notification"
            return "Send local notification: \(title)"
        case ("system-actions", "system.open_app"):
            let appName = summarize(object["appName"] as? String) ?? "App"
            return "Open app: \(appName)"
        case ("system-actions", "system.open_file"),
             ("system-actions", "system.open_folder"),
             ("system-actions", "system.reveal_in_finder"):
            let path = (object["path"] as? String) ?? ""
            let name = URL(fileURLWithPath: path).lastPathComponent
            return "\(request.actionID): \(name.isEmpty ? (summarize(path) ?? "target") : name)"
        case ("info", "info.currency_convert"):
            let amount = (object["amount"] as? NSNumber)?.doubleValue ?? 0
            let from = summarize(object["fromCurrency"] as? String) ?? ""
            let to = summarize(object["toCurrency"] as? String) ?? ""
            return "Convert \(amount) \(from) to \(to)"
        case ("info", "info.currency_for_location"):
            let query = summarize(object["locationQuery"] as? String) ?? "location"
            return "Currency lookup for \(query)"
        case ("info", "info.current_time"),
             ("info", "info.current_date"):
            if let timezoneID = summarize(object["timezoneID"] as? String) {
                return "\(request.actionID) for timezone \(timezoneID)"
            }
            if let location = summarize(object["locationQuery"] as? String) {
                return "\(request.actionID) for \(location)"
            }
            return "\(request.actionID) using local context"
        case ("info", "info.timezone_convert"):
            let sourceTime = summarize(object["sourceTime"] as? String) ?? "time"
            let source = summarize(object["sourceTimezoneID"] as? String) ?? "local"
            let destination = summarize(object["destinationTimezoneID"] as? String)
                ?? summarize(object["destinationLocationQuery"] as? String)
                ?? "destination"
            return "Timezone convert \(sourceTime) from \(source) to \(destination)"
        case ("image-generation", "image.generate"):
            let prompt = summarize(object["prompt"] as? String, limit: 120) ?? "prompt"
            let size = summarize(object["size"] as? String) ?? "default size"
            return "Generate image (\(size)): \(prompt)"
        case ("image-generation", "image.edit"):
            let prompt = summarize(object["prompt"] as? String, limit: 120) ?? "prompt"
            let reference = (object["inputImageReference"] as? String).map {
                let url = URL(fileURLWithPath: $0)
                return url.lastPathComponent.isEmpty ? (summarize($0) ?? "image") : url.lastPathComponent
            } ?? "image"
            return "Edit image \(reference): \(prompt)"
        default:
            return summarize(request.input.argumentsJSON)
        }
    }
}
