import Foundation
import AtlasShared
import AtlasTools

public struct SkillActionToolAdapter: AtlasTool {
    public let toolName: String
    public let description: String
    public let permissionLevel: PermissionLevel
    public let inputSchema: AtlasToolInputSchema
    public let approvalBehavior: AtlasToolApprovalBehavior

    private let skillID: String
    private let actionID: String
    private let gateway: SkillExecutionGateway
    private let executionContextBuilder: @Sendable (ToolExecutionContext) async -> SkillExecutionContext

    public init(
        catalogItem: SkillActionCatalogItem,
        gateway: SkillExecutionGateway,
        executionContextBuilder: @escaping @Sendable (ToolExecutionContext) async -> SkillExecutionContext
    ) {
        self.toolName = catalogItem.toolName
        self.description = "\(catalogItem.skillName): \(catalogItem.action.description)"
        self.permissionLevel = catalogItem.action.permissionLevel
        self.inputSchema = catalogItem.action.inputSchema
        self.approvalBehavior = .toolManaged
        self.skillID = catalogItem.skillID
        self.actionID = catalogItem.action.id
        self.gateway = gateway
        self.executionContextBuilder = executionContextBuilder
    }

    public func execute(input: AtlasToolInput, context: ToolExecutionContext) async throws -> String {
        let skillContext = await executionContextBuilder(context)
        let result = try await gateway.execute(
            SkillExecutionRequest(
                skillID: skillID,
                actionID: actionID,
                input: input,
                conversationID: context.conversationID,
                toolCallID: context.toolCallID ?? UUID()
            ),
            context: skillContext
        )

        // `result.success` is intentionally ignored here. Skills like Forge use
        // `success: false` for non-terminal outcomes (gate refusals, dry-run failures,
        // missing inputs) where the output is a message the LLM should read and act on.
        // Propagating `success: false` as a hard tool failure would short-circuit the
        // agent loop via AgentOrchestrator and surface the generic fallback error instead.
        // Returning the output keeps the loop alive so the model can respond normally.
        return result.output
    }
}
