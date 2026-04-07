import Foundation
import AtlasShared

public enum AtlasToolError: LocalizedError {
    case unknownTool(String)
    case invalidInput(String)
    case executionFailed(String)

    public var errorDescription: String? {
        switch self {
        case .unknownTool(let toolName):
            return "Unknown tool '\(toolName)'."
        case .invalidInput(let details):
            return details
        case .executionFailed(let details):
            return details
        }
    }
}

public enum AtlasToolApprovalBehavior: Sendable {
    case managedByToolExecutor
    case toolManaged
}

public protocol AtlasTool: Sendable {
    var toolName: String { get }
    var description: String { get }
    var permissionLevel: PermissionLevel { get }
    var inputSchema: AtlasToolInputSchema { get }
    var approvalBehavior: AtlasToolApprovalBehavior { get }

    func execute(input: AtlasToolInput, context: ToolExecutionContext) async throws -> String
}

public extension AtlasTool {
    var approvalBehavior: AtlasToolApprovalBehavior {
        .managedByToolExecutor
    }

    var definition: AtlasToolDefinition {
        AtlasToolDefinition(
            name: toolName,
            description: description,
            inputSchema: inputSchema
        )
    }
}
