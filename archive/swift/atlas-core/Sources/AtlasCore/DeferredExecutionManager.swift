import Foundation
import AtlasGuard
import AtlasLogging
import AtlasMemory
import AtlasShared
import AtlasSkills

public actor DeferredExecutionManager: DeferredExecutionManaging {
    private let memoryStore: MemoryStore
    private let approvalManager: ToolApprovalManager
    private let logger: AtlasLogger

    public init(
        memoryStore: MemoryStore,
        approvalManager: ToolApprovalManager,
        logger: AtlasLogger = AtlasLogger(category: "deferred-execution")
    ) {
        self.memoryStore = memoryStore
        self.approvalManager = approvalManager
        self.logger = logger
    }

    public func hydrate() async {
        do {
            let requests = try await memoryStore.listDeferredExecutions()
            for request in requests {
                await approvalManager.upsert(approvalRequest(from: request))
            }

            if !requests.isEmpty {
                logger.info("Hydrated deferred executions", metadata: [
                    "count": "\(requests.count)"
                ])
            }
        } catch {
            logger.error("Failed to hydrate deferred executions", metadata: [
                "error": error.localizedDescription
            ])
        }
    }

    public func allApprovalRequests() async -> [ApprovalRequest] {
        do {
            return try await memoryStore.listDeferredExecutions().map(approvalRequest(from:))
        } catch {
            logger.error("Failed to list deferred approvals", metadata: [
                "error": error.localizedDescription
            ])
            return await approvalManager.allApprovals()
        }
    }

    public func pendingApprovalCount() async -> Int {
        let requests = await allApprovalRequests()
        return requests.filter { $0.status == .pending }.count
    }

    public func approvalStatus(for toolCallID: UUID) async -> ApprovalStatus? {
        guard let deferred = try? await memoryStore.fetchDeferredExecution(toolCallID: toolCallID) else {
            return await approvalManager.approval(for: toolCallID)?.status
        }

        return approvalStatus(from: deferred.status)
    }

    public func approvalRequest(for toolCallID: UUID) async -> ApprovalRequest? {
        guard let deferred = try? await memoryStore.fetchDeferredExecution(toolCallID: toolCallID) else {
            return await approvalManager.approval(for: toolCallID)
        }

        let approval = approvalRequest(from: deferred)
        await approvalManager.upsert(approval)
        return approval
    }

    public func deferredExecution(for toolCallID: UUID) async -> DeferredExecutionRequest? {
        try? await memoryStore.fetchDeferredExecution(toolCallID: toolCallID)
    }

    public func createDeferredSkillApproval(
        skillID: String,
        actionID: String,
        toolCallID: UUID,
        normalizedInputJSON: String,
        conversationID: UUID?,
        originatingMessageID: String?,
        summary: String,
        permissionLevel: PermissionLevel,
        riskLevel: String
    ) async throws -> ApprovalRequest {
        let deferred = DeferredExecutionRequest(
            sourceType: .skill,
            skillID: skillID,
            actionID: actionID,
            toolCallID: toolCallID,
            normalizedInputJSON: normalizedInputJSON,
            conversationID: conversationID,
            originatingMessageID: originatingMessageID,
            approvalID: UUID(),
            summary: summary,
            permissionLevel: permissionLevel,
            riskLevel: riskLevel
        )

        _ = try await memoryStore.upsertDeferredExecution(deferred)
        let approval = approvalRequest(from: deferred)
        await approvalManager.upsert(approval)

        logger.info("Created deferred skill execution", metadata: [
            "tool_call_id": toolCallID.uuidString,
            "skill_id": skillID,
            "action_id": actionID,
            "approval_id": approval.id.uuidString,
            "status": deferred.status.rawValue
        ])

        return approval
    }

    public func createDeferredToolApproval(
        toolCall: AtlasToolCall,
        conversationID: UUID?,
        summary: String,
        riskLevel: String
    ) async throws -> ApprovalRequest {
        let deferred = DeferredExecutionRequest(
            sourceType: .tool,
            toolID: toolCall.toolName,
            toolCallID: toolCall.id,
            normalizedInputJSON: toolCall.argumentsJSON,
            conversationID: conversationID,
            approvalID: UUID(),
            summary: summary,
            permissionLevel: toolCall.permissionLevel,
            riskLevel: riskLevel
        )

        _ = try await memoryStore.upsertDeferredExecution(deferred)
        let approval = approvalRequest(from: deferred)
        await approvalManager.upsert(approval)

        logger.info("Created deferred tool execution", metadata: [
            "tool_call_id": toolCall.id.uuidString,
            "tool": toolCall.toolName,
            "approval_id": approval.id.uuidString,
            "status": deferred.status.rawValue
        ])

        return approval
    }

    public func approve(toolCallID: UUID) async throws -> ApprovalRequest {
        guard let deferred = try await memoryStore.fetchDeferredExecution(toolCallID: toolCallID) else {
            throw ToolApprovalManagerError.requestNotFound(toolCallID)
        }

        let updated: DeferredExecutionRequest
        if deferred.status == .pendingApproval || deferred.status == .failed {
            updated = deferred.updatingStatus(.approved, updatedAt: .now)
            _ = try await memoryStore.upsertDeferredExecution(updated)
            logger.info("Approved deferred execution", metadata: [
                "tool_call_id": toolCallID.uuidString,
                "approval_id": deferred.approvalID.uuidString
            ])
        } else {
            updated = deferred
        }

        let approval = approvalRequest(from: updated)
        await approvalManager.upsert(approval)
        return approval
    }

    public func deny(toolCallID: UUID) async throws -> ApprovalRequest {
        guard let deferred = try await memoryStore.fetchDeferredExecution(toolCallID: toolCallID) else {
            throw ToolApprovalManagerError.requestNotFound(toolCallID)
        }

        let updated = deferred.updatingStatus(.denied, updatedAt: .now)
        _ = try await memoryStore.upsertDeferredExecution(updated)

        let approval = approvalRequest(from: updated)
        await approvalManager.upsert(approval)

        logger.warning("Denied deferred execution", metadata: [
            "tool_call_id": toolCallID.uuidString,
            "approval_id": deferred.approvalID.uuidString
        ])

        return approval
    }

    public func markRunning(toolCallID: UUID) async throws {
        guard let deferred = try await memoryStore.fetchDeferredExecution(toolCallID: toolCallID) else {
            return
        }

        guard deferred.status == .approved || deferred.status == .running else {
            return
        }

        let updated = deferred.updatingStatus(.running, updatedAt: .now)
        _ = try await memoryStore.upsertDeferredExecution(updated)
        await approvalManager.upsert(approvalRequest(from: updated))

        logger.info("Deferred execution running", metadata: [
            "tool_call_id": toolCallID.uuidString,
            "approval_id": deferred.approvalID.uuidString
        ])
    }

    public func markCompleted(toolCallID: UUID, result: DeferredExecutionResult) async throws {
        guard let deferred = try await memoryStore.fetchDeferredExecution(toolCallID: toolCallID) else {
            return
        }

        let updated = deferred.updatingStatus(.completed, result: result, updatedAt: .now)
        _ = try await memoryStore.upsertDeferredExecution(updated)
        await approvalManager.upsert(approvalRequest(from: updated))

        logger.info("Deferred execution completed", metadata: [
            "tool_call_id": toolCallID.uuidString,
            "approval_id": deferred.approvalID.uuidString
        ])
    }

    public func markFailed(toolCallID: UUID, errorMessage: String) async throws {
        guard let deferred = try await memoryStore.fetchDeferredExecution(toolCallID: toolCallID) else {
            return
        }

        let updated = deferred.updatingStatus(.failed, lastError: errorMessage, updatedAt: .now)
        _ = try await memoryStore.upsertDeferredExecution(updated)
        await approvalManager.upsert(approvalRequest(from: updated))

        logger.error("Deferred execution failed", metadata: [
            "tool_call_id": toolCallID.uuidString,
            "approval_id": deferred.approvalID.uuidString,
            "error": errorMessage
        ])
    }

    private func approvalRequest(from request: DeferredExecutionRequest) -> ApprovalRequest {
        ApprovalRequest(
            id: request.approvalID,
            toolCall: AtlasToolCall(
                id: request.toolCallID,
                toolName: toolName(for: request),
                argumentsJSON: request.normalizedInputJSON,
                permissionLevel: request.permissionLevel,
                requiresApproval: request.status == .pendingApproval,
                status: toolCallStatus(for: request.status)
            ),
            conversationID: request.conversationID,
            deferredExecutionID: request.id,
            deferredExecutionStatus: request.status,
            lastError: request.lastError,
            createdAt: request.createdAt,
            resolvedAt: request.status == .pendingApproval ? nil : request.updatedAt,
            status: approvalStatus(from: request.status)
        )
    }

    private func toolCallStatus(for status: DeferredExecutionStatus) -> AtlasToolCallStatus {
        switch status {
        case .pendingApproval:
            return .pending
        case .approved:
            return .approved
        case .running:
            return .running
        case .completed:
            return .completed
        case .failed:
            return .failed
        case .denied, .cancelled:
            return .denied
        }
    }

    private func approvalStatus(from status: DeferredExecutionStatus) -> ApprovalStatus {
        switch status {
        case .pendingApproval:
            return .pending
        case .approved, .running, .completed, .failed:
            return .approved
        case .denied, .cancelled:
            return .denied
        }
    }

    private func toolName(for request: DeferredExecutionRequest) -> String {
        switch request.sourceType {
        case .skill:
            guard let skillID = request.skillID, let actionID = request.actionID else {
                return "skill__unknown"
            }
            return SkillActionCatalogItem.toolName(skillID: skillID, actionID: actionID)
        case .tool:
            return request.toolID ?? "tool__unknown"
        }
    }
}
