import Foundation
import AtlasShared
import AtlasLogging

public enum ToolApprovalManagerError: LocalizedError {
    case requestNotFound(UUID)

    public var errorDescription: String? {
        switch self {
        case .requestNotFound(let id):
            return "No pending approval request was found for tool call \(id.uuidString)."
        }
    }
}

public actor ToolApprovalManager {
    private let logger = AtlasLogger.security
    private var requests: [UUID: ApprovalRequest] = [:]

    public init() {}

    public func createApprovalRequest(toolCall: AtlasToolCall, conversationID: UUID? = nil) -> ApprovalRequest {
        let request = ApprovalRequest(
            id: toolCall.id,
            toolCall: toolCall.updatingStatus(.pending),
            conversationID: conversationID,
            deferredExecutionID: nil,
            deferredExecutionStatus: nil,
            lastError: nil,
            createdAt: .now,
            resolvedAt: nil,
            status: .pending
        )

        upsert(request)
        return request
    }

    public func upsert(_ request: ApprovalRequest) {
        requests[request.toolCallID] = request
        logger.info("Stored approval request", metadata: [
            "tool_call_id": request.toolCallID.uuidString,
            "tool": request.toolCall.toolName,
            "permission": request.toolCall.permissionLevel.rawValue,
            "status": request.status.rawValue,
            "deferred_status": request.deferredExecutionStatus?.rawValue ?? "none"
        ])
    }

    public func approval(for toolCallID: UUID) -> ApprovalRequest? {
        requests[toolCallID]
    }

    public func allApprovals() -> [ApprovalRequest] {
        requests.values.sorted { $0.createdAt < $1.createdAt }
    }

    public func approve(toolCallID: UUID) throws -> ApprovalRequest {
        guard let request = requests[toolCallID] else {
            throw ToolApprovalManagerError.requestNotFound(toolCallID)
        }

        let approved = request.updatingStatus(.approved)
        requests[toolCallID] = approved
        logger.info("Approved tool call", metadata: ["tool_call_id": toolCallID.uuidString])
        return approved
    }

    public func deny(toolCallID: UUID) throws -> ApprovalRequest {
        guard let request = requests[toolCallID] else {
            throw ToolApprovalManagerError.requestNotFound(toolCallID)
        }

        let denied = request.updatingStatus(.denied)
        requests[toolCallID] = denied
        logger.warning("Denied tool call", metadata: ["tool_call_id": toolCallID.uuidString])
        return denied
    }

    public func status(for toolCallID: UUID) -> ApprovalStatus? {
        requests[toolCallID]?.status
    }

    public func getPendingApprovals() -> [ApprovalRequest] {
        requests.values
            .filter { $0.status == .pending }
            .sorted { $0.createdAt < $1.createdAt }
    }
}
