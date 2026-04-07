import XCTest
import AtlasCore
import AtlasGuard
import AtlasLogging
import AtlasMemory
import AtlasShared
import AtlasSkills
import AtlasTools

final class DeferredExecutionRuntimeTests: XCTestCase {
    func testApprovalRequiredSkillCreatesDeferredRequestAndResumesAfterApproval() async throws {
        let databaseURL = temporaryDatabaseURL()
        let memoryStore = try MemoryStore(databasePath: databaseURL.path)
        let registry = SkillRegistry(defaults: UserDefaults(suiteName: "AtlasDeferredExecutionTests.\(UUID().uuidString)")!)
        let recorder = DeferredExecutionRecorder()
        let skill = DeferredApprovalTestSkill(recorder: recorder)
        try await registry.register([skill])
        _ = try await registry.enable(skillID: skill.manifest.id)

        let context = try AgentContext(
            config: AtlasConfig(autoApproveDraftTools: false, memoryDatabasePath: databaseURL.path),
            logger: AtlasLogger(category: "test"),
            memoryStore: memoryStore,
            skillRegistry: registry
        )
        let runtime = try AgentRuntime(context: context)

        let request = SkillExecutionRequest(
            skillID: skill.manifest.id,
            actionID: skill.actions[0].id,
            input: AtlasToolInput(argumentsJSON: #"{"message":"resume me"}"#),
            conversationID: UUID(),
            toolCallID: UUID()
        )

        do {
            _ = try await context.skillExecutionGateway.execute(
                request,
                context: await makeSkillExecutionContext(runtime: runtime, context: context)
            )
            XCTFail("Expected the action to require approval before execution.")
        } catch let error as ToolExecutionError {
            guard case .approvalRequired(let approval) = error else {
                XCTFail("Expected approvalRequired, got \(error.localizedDescription)")
                return
            }

            XCTAssertEqual(approval.status, .pending)
            XCTAssertEqual(approval.deferredExecutionStatus, .pendingApproval)
            XCTAssertNotNil(approval.deferredExecutionID)

            let deferredBeforeResume = await context.deferredExecutionManager.deferredExecution(for: request.toolCallID)
            XCTAssertEqual(deferredBeforeResume?.status, .pendingApproval)

            // runtime.approve now returns AtlasMessageResponseEnvelope; verify completion
            // via the deferredExecutionManager below.
            _ = try await runtime.approve(toolCallID: request.toolCallID)

            let invocations = await recorder.invocationCount()
            XCTAssertEqual(invocations, 1)

            let deferredAfterResume = await context.deferredExecutionManager.deferredExecution(for: request.toolCallID)
            XCTAssertEqual(deferredAfterResume?.status, .completed)
            XCTAssertEqual(deferredAfterResume?.result?.success, true)

            let reopenedStore = try MemoryStore(databasePath: databaseURL.path)
            let reopenedManager = DeferredExecutionManager(
                memoryStore: reopenedStore,
                approvalManager: ToolApprovalManager()
            )
            let reopenedDeferred = await reopenedManager.deferredExecution(for: request.toolCallID)
            XCTAssertEqual(reopenedDeferred?.status, .completed)
            XCTAssertEqual(reopenedDeferred?.result?.success, true)
            return
        }
    }

    private func makeSkillExecutionContext(
        runtime: AgentRuntime,
        context: AgentContext
    ) async -> SkillExecutionContext {
        SkillExecutionContext(
            conversationID: nil,
            logger: context.logger,
            config: context.config,
            permissionManager: context.permissionManager,
            runtimeStatusProvider: {
                await runtime.status()
            },
            enabledSkillsProvider: {
                await context.skillRegistry.listEnabled()
            },
            memoryItemsProvider: {
                do {
                    return try await context.memoryStore.listMemories(limit: 50)
                } catch {
                    return []
                }
            }
        )
    }

    private func temporaryDatabaseURL() -> URL {
        FileManager.default.temporaryDirectory
            .appendingPathComponent("AtlasDeferredExecutionTests-\(UUID().uuidString).sqlite3")
    }
}

private actor DeferredExecutionRecorder {
    private var count = 0

    func record() {
        count += 1
    }

    func invocationCount() -> Int {
        count
    }
}

private struct DeferredApprovalTestSkill: AtlasSkill {
    let recorder: DeferredExecutionRecorder

    var manifest: SkillManifest {
        SkillManifest(
            id: "deferred-approval-test",
            name: "Deferred Approval Test",
            version: "1.0.0",
            description: "A test skill that requires approval and then resumes.",
            category: .productivity,
            lifecycleState: .enabled,
            capabilities: [.capabilitySummary],
            requiredPermissions: [.draftWrite],
            riskLevel: .medium,
            trustProfile: .general,
            freshnessType: .staticKnowledge,
            preferredQueryTypes: [],
            routingPriority: 0,
            canAnswerStructuredLiveData: false,
            canHandleLocalData: false,
            canHandleExploratoryQueries: false,
            restrictionsSummary: ["Requires approval in tests."],
            supportsReadOnlyMode: false,
            isUserVisible: false,
            isEnabledByDefault: true
        )
    }

    var actions: [SkillActionDefinition] {
        [
            SkillActionDefinition(
                id: "deferred.approval",
                name: "Deferred Approval",
                description: "Requires approval before execution can continue.",
                inputSchemaSummary: "message: String",
                outputSchemaSummary: "A short success string.",
                permissionLevel: .draft,
                sideEffectLevel: .draftWrite,
                isEnabled: true,
                routingPriority: 0
            )
        ]
    }

    func validateConfiguration(context: SkillValidationContext) async -> SkillValidationResult {
        SkillValidationResult(
            skillID: manifest.id,
            status: .passed,
            summary: "Test skill ready."
        )
    }

    func execute(actionID: String, input: AtlasToolInput, context: SkillExecutionContext) async throws -> SkillExecutionResult {
        await recorder.record()
        return SkillExecutionResult(
            skillID: manifest.id,
            actionID: actionID,
            output: "Deferred execution completed.",
            summary: "Deferred execution completed."
        )
    }
}
