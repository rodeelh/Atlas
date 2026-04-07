import Foundation
import XCTest
import AtlasGuard
import AtlasShared
@testable import AtlasTools

final class ToolExecutionFlowTests: XCTestCase {
    func testReadToolExecutesInsideSandbox() async throws {
        let sandboxURL = try makeSandboxDirectory()
        let fileURL = sandboxURL.appendingPathComponent("note.txt", isDirectory: false)
        try "Atlas sandbox file".write(to: fileURL, atomically: true, encoding: .utf8)

        let permissionManager = PermissionManager(
            grantedPermissions: [.read, .draft, .execute],
            autoApproveDraftTools: false
        )
        let approvalManager = ToolApprovalManager()
        let registry = ToolRegistry()
        await registry.registerDefaultTools()

        let executor = ToolExecutor(
            registry: registry,
            permissionManager: permissionManager,
            approvalManager: approvalManager,
            fileAccessScope: sandboxURL
        )

        let toolCall = AtlasToolCall(
            toolName: "read_file",
            argumentsJSON: #"{"path":"note.txt"}"#,
            permissionLevel: .read,
            requiresApproval: false
        )

        let result = try await executor.execute(toolCall: toolCall, conversationID: UUID())
        XCTAssertTrue(result.success)
        XCTAssertEqual(result.output, "Atlas sandbox file")
    }

    func testDraftToolRequiresApprovalThenExecutes() async throws {
        let sandboxURL = try makeSandboxDirectory()
        let permissionManager = PermissionManager(
            grantedPermissions: [.read, .draft, .execute],
            autoApproveDraftTools: false
        )
        let approvalManager = ToolApprovalManager()
        let registry = ToolRegistry()
        await registry.registerDefaultTools()

        let executor = ToolExecutor(
            registry: registry,
            permissionManager: permissionManager,
            approvalManager: approvalManager,
            fileAccessScope: sandboxURL
        )

        let toolCall = AtlasToolCall(
            toolName: "summarize_text",
            argumentsJSON: #"{"text":"Atlas verifies approval-aware tool execution."}"#,
            permissionLevel: .draft,
            requiresApproval: true
        )

        do {
            _ = try await executor.execute(toolCall: toolCall, conversationID: UUID())
            XCTFail("Expected approval requirement before executing draft tool.")
        } catch let error as ToolExecutionError {
            switch error {
            case .approvalRequired(let request):
                XCTAssertEqual(request.toolCall.toolName, "summarize_text")
            default:
                XCTFail("Expected approvalRequired, got \(error)")
            }
        }

        let pendingApprovals = await approvalManager.getPendingApprovals()
        XCTAssertEqual(pendingApprovals.count, 1)

        _ = try await approvalManager.approve(toolCallID: toolCall.id)
        let result = try await executor.execute(toolCall: toolCall, conversationID: UUID())

        XCTAssertTrue(result.success)
        XCTAssertTrue(result.output.contains("Summary"))
    }

    private func makeSandboxDirectory() throws -> URL {
        let url = FileManager.default.temporaryDirectory
            .appendingPathComponent("AtlasToolsTests-\(UUID().uuidString)", isDirectory: true)
        try FileManager.default.createDirectory(at: url, withIntermediateDirectories: true)
        return url
    }
}
