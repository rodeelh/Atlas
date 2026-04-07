import Foundation
import AtlasGuard
import AtlasLogging
import AtlasShared

public enum ToolExecutionError: LocalizedError {
    case approvalRequired(ApprovalRequest)
    case denied(String)
    case failed(String)

    public var errorDescription: String? {
        switch self {
        case .approvalRequired(let request):
            return "Approval is required before Atlas can run '\(request.toolCall.toolName)'."
        case .denied(let message):
            return message
        case .failed(let message):
            return message
        }
    }
}

public actor ToolExecutor: AtlasToolExecuting {
    private let registry: ToolRegistry
    private let permissionManager: PermissionManager
    private let approvalManager: ToolApprovalManager
    private let deferredExecutionManager: (any DeferredExecutionManaging)?
    private let logger: AtlasLogger
    private let fileAccessScope: URL

    public init(
        registry: ToolRegistry,
        permissionManager: PermissionManager,
        approvalManager: ToolApprovalManager,
        deferredExecutionManager: (any DeferredExecutionManaging)? = nil,
        logger: AtlasLogger = .runtime,
        fileAccessScope: URL
    ) {
        self.registry = registry
        self.permissionManager = permissionManager
        self.approvalManager = approvalManager
        self.deferredExecutionManager = deferredExecutionManager
        self.logger = logger
        self.fileAccessScope = fileAccessScope.standardizedFileURL
    }

    public func execute(toolCall: AtlasToolCall, conversationID: UUID) async throws -> AtlasToolResult {
        guard let tool = await registry.tool(named: toolCall.toolName) else {
            throw AtlasToolError.unknownTool(toolCall.toolName)
        }

        try await permissionManager.validate(level: tool.permissionLevel)
        let requiresApproval = tool.approvalBehavior == .managedByToolExecutor
            ? await permissionManager.requiresApproval(for: tool.permissionLevel)
            : false
        let preparedCall = AtlasToolCall(
            id: toolCall.id,
            toolName: tool.toolName,
            argumentsJSON: toolCall.argumentsJSON,
            permissionLevel: tool.permissionLevel,
            requiresApproval: requiresApproval,
            status: toolCall.status,
            openAICallID: toolCall.openAICallID,
            timestamp: toolCall.timestamp
        )

        if requiresApproval {
            if let deferredExecutionManager {
                switch await deferredExecutionManager.approvalStatus(for: preparedCall.id) {
                case .approved:
                    logger.info("Running approved tool call", metadata: [
                        "tool_call_id": preparedCall.id.uuidString,
                        "tool": preparedCall.toolName
                    ])
                case .denied:
                    throw ToolExecutionError.denied("Tool execution was denied for '\(preparedCall.toolName)'.")
                case .pending:
                    if let existing = await deferredExecutionManager.approvalRequest(for: preparedCall.id) {
                        throw ToolExecutionError.approvalRequired(existing)
                    }

                    let request = try await deferredExecutionManager.createDeferredToolApproval(
                        toolCall: preparedCall.updatingStatus(.pending),
                        conversationID: conversationID,
                        summary: preparedCall.toolName,
                        riskLevel: preparedCall.permissionLevel.rawValue
                    )
                    throw ToolExecutionError.approvalRequired(request)
                case nil:
                    let request = try await deferredExecutionManager.createDeferredToolApproval(
                        toolCall: preparedCall.updatingStatus(.pending),
                        conversationID: conversationID,
                        summary: preparedCall.toolName,
                        riskLevel: preparedCall.permissionLevel.rawValue
                    )
                    throw ToolExecutionError.approvalRequired(request)
                }
            } else {
                switch await approvalManager.status(for: preparedCall.id) {
                case .approved:
                    logger.info("Running approved tool call", metadata: [
                        "tool_call_id": preparedCall.id.uuidString,
                        "tool": preparedCall.toolName
                    ])
                case .denied:
                    throw ToolExecutionError.denied("Tool execution was denied for '\(preparedCall.toolName)'.")
                case .pending:
                    if let existing = await approvalManager.getPendingApprovals().first(where: { $0.toolCallID == preparedCall.id }) {
                        throw ToolExecutionError.approvalRequired(existing)
                    }
                    fallthrough
                case nil:
                    let request = await approvalManager.createApprovalRequest(
                        toolCall: preparedCall.updatingStatus(.pending),
                        conversationID: conversationID
                    )
                    throw ToolExecutionError.approvalRequired(request)
                }
            }
        }

        let context = ToolExecutionContext(
            logger: logger,
            permissionManager: permissionManager,
            fileAccessScope: fileAccessScope,
            conversationID: conversationID,
            toolCallID: preparedCall.id
        )

        if let deferredExecutionManager {
            try? await deferredExecutionManager.markRunning(toolCallID: preparedCall.id)
        }

        logger.info("Executing Atlas tool", metadata: [
            "tool_call_id": preparedCall.id.uuidString,
            "tool": preparedCall.toolName,
            "permission": preparedCall.permissionLevel.rawValue
        ])

        do {
            let output = try await tool.execute(input: preparedCall.input, context: context)
            if let deferredExecutionManager {
                try? await deferredExecutionManager.markCompleted(
                    toolCallID: preparedCall.id,
                    result: DeferredExecutionResult(
                        output: output,
                        success: true,
                        summary: String(output.prefix(200)),
                        metadata: [
                            "tool": preparedCall.toolName
                        ]
                    )
                )
            }
            logger.info("Tool execution completed", metadata: [
                "tool_call_id": preparedCall.id.uuidString,
                "tool": preparedCall.toolName
            ])
            return AtlasToolResult(
                toolCallID: preparedCall.id,
                output: output,
                success: true
            )
        } catch let error as ToolExecutionError {
            // approvalRequired is expected control flow — the deferred record was just created
            // and is awaiting user approval. Calling markFailed() here would corrupt its status.
            if let deferredExecutionManager, case .approvalRequired = error {
                // intentionally skip markFailed
            } else if let deferredExecutionManager {
                try? await deferredExecutionManager.markFailed(
                    toolCallID: preparedCall.id,
                    errorMessage: error.localizedDescription
                )
            }
            throw error
        } catch {
            if let deferredExecutionManager {
                try? await deferredExecutionManager.markFailed(
                    toolCallID: preparedCall.id,
                    errorMessage: error.localizedDescription
                )
            }
            logger.error("Tool execution failed", metadata: [
                "tool_call_id": preparedCall.id.uuidString,
                "tool": preparedCall.toolName,
                "error": error.localizedDescription
            ])
            throw ToolExecutionError.failed(error.localizedDescription)
        }
    }
}
