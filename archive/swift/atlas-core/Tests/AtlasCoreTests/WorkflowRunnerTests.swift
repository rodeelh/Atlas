import XCTest
import AtlasCore
import AtlasLogging
import AtlasMemory
import AtlasShared
@testable import AtlasSkills

final class WorkflowRunnerTests: XCTestCase {
    func testWorkflowStorePersistsDefinitionAndRun() async throws {
        let directory = FileManager.default.temporaryDirectory.appendingPathComponent("WorkflowStoreTests-\(UUID().uuidString)")
        let store = WorkflowStore(directory: directory)

        let definition = AtlasWorkflowDefinition(
            id: "daily-operator",
            name: "Daily Operator",
            description: "Checks a trusted app and reports back.",
            promptTemplate: "Run the daily operator.",
            tags: ["ops"]
        )
        _ = await store.upsertDefinition(definition)
        _ = await store.upsertRun(
            AtlasWorkflowRun(
                workflowID: definition.id,
                workflowName: definition.name,
                status: .completed,
                outcome: .success,
                assistantSummary: "Done."
            )
        )

        let reloaded = WorkflowStore(directory: directory)
        let loadedDefinitions = await reloaded.listDefinitions()
        let loadedRuns = await reloaded.listRuns()

        XCTAssertEqual(loadedDefinitions.count, 1)
        XCTAssertEqual(loadedDefinitions.first?.id, definition.id)
        XCTAssertEqual(loadedRuns.count, 1)
        XCTAssertEqual(loadedRuns.first?.workflowID, definition.id)
    }

    func testWorkflowTrustGrantBypassesLiveWriteApprovalForCoveredSkillStep() async throws {
        let (_, _, store, runner) = try await makeWorkflowHarness()

        let definition = AtlasWorkflowDefinition(
            id: "live-write-flow",
            name: "Live Write Flow",
            description: "Executes a live write inside workflow trust.",
            promptTemplate: "Run the flow.",
            steps: [
                AtlasWorkflowStep(
                    id: "step-1",
                    title: "Live write step",
                    kind: .skillAction,
                    skillID: "workflow-test-skill",
                    actionID: "workflow.test_live_write",
                    inputJSON: #"{"message":"Ship it"}"#,
                    appName: "WorkflowTestApp",
                    sideEffectLevel: "live_write"
                )
            ],
            trustScope: AtlasWorkflowTrustScope(
                approvedRootPaths: [],
                allowedApps: ["WorkflowTestApp"],
                allowsSensitiveRead: false,
                allowsLiveWrite: true
            ),
            approvalMode: .workflowBoundary
        )

        _ = await store.upsertDefinition(definition)
        let waiting = await runner.run(definition: definition)
        XCTAssertEqual(waiting.status, .waitingForApproval)

        let approved = try await runner.approve(runID: waiting.id)
        XCTAssertEqual(approved.status, .completed)
        XCTAssertEqual(approved.outcome, .success)
        XCTAssertEqual(approved.stepRuns.first?.status, .completed)
        XCTAssertEqual(approved.stepRuns.first?.output, "Executed Ship it.")
    }

    func testWorkflowBoundaryRejectsOutOfScopeLiveWriteAfterApproval() async throws {
        let (_, _, store, runner) = try await makeWorkflowHarness()

        let definition = AtlasWorkflowDefinition(
            id: "out-of-scope-flow",
            name: "Out Of Scope Flow",
            description: "Attempts to run outside the approved workflow scope.",
            promptTemplate: "Run the flow.",
            steps: [
                AtlasWorkflowStep(
                    id: "step-1",
                    title: "Live write step",
                    kind: .skillAction,
                    skillID: "workflow-test-skill",
                    actionID: "workflow.test_live_write",
                    inputJSON: #"{"message":"Ship it","path":"/private/tmp/outside.txt"}"#,
                    targetPath: "/private/tmp/outside.txt",
                    sideEffectLevel: "live_write"
                )
            ],
            trustScope: AtlasWorkflowTrustScope(
                approvedRootPaths: ["/Users/ralhassan/Desktop/CODING/Project Atlas"],
                allowedApps: [],
                allowsSensitiveRead: false,
                allowsLiveWrite: true
            ),
            approvalMode: .workflowBoundary
        )

        _ = await store.upsertDefinition(definition)
        let waiting = await runner.run(definition: definition)
        XCTAssertEqual(waiting.status, .waitingForApproval)

        let approved = try await runner.approve(runID: waiting.id)
        XCTAssertEqual(approved.status, .failed)
        XCTAssertEqual(approved.stepRuns.first?.status, .failed)
        XCTAssertTrue(approved.errorMessage?.contains("does not cover path") == true)
    }

    func testStepByStepModePausesBeforeEachRiskyStep() async throws {
        let (_, _, store, runner) = try await makeWorkflowHarness()

        let definition = AtlasWorkflowDefinition(
            id: "step-by-step-flow",
            name: "Step By Step Flow",
            description: "Requires approval per risky step.",
            promptTemplate: "Run the flow.",
            steps: [
                AtlasWorkflowStep(
                    id: "step-1",
                    title: "First write",
                    kind: .skillAction,
                    skillID: "workflow-test-skill",
                    actionID: "workflow.test_live_write",
                    inputJSON: #"{"message":"One","path":"/Users/ralhassan/Desktop/CODING/Project Atlas/one.txt"}"#,
                    targetPath: "/Users/ralhassan/Desktop/CODING/Project Atlas/one.txt",
                    sideEffectLevel: "live_write"
                ),
                AtlasWorkflowStep(
                    id: "step-2",
                    title: "Second write",
                    kind: .skillAction,
                    skillID: "workflow-test-skill",
                    actionID: "workflow.test_live_write",
                    inputJSON: #"{"message":"Two","path":"/Users/ralhassan/Desktop/CODING/Project Atlas/two.txt"}"#,
                    targetPath: "/Users/ralhassan/Desktop/CODING/Project Atlas/two.txt",
                    sideEffectLevel: "live_write"
                )
            ],
            trustScope: AtlasWorkflowTrustScope(
                approvedRootPaths: ["/Users/ralhassan/Desktop/CODING/Project Atlas"],
                allowedApps: [],
                allowsSensitiveRead: false,
                allowsLiveWrite: true
            ),
            approvalMode: .stepByStep
        )

        _ = await store.upsertDefinition(definition)
        let firstPause = await runner.run(definition: definition)
        XCTAssertEqual(firstPause.status, .waitingForApproval)
        XCTAssertEqual(firstPause.stepRuns.first?.status, .waitingForApproval)

        let secondPause = try await runner.approve(runID: firstPause.id)
        XCTAssertEqual(secondPause.status, .waitingForApproval)
        XCTAssertEqual(secondPause.stepRuns.first?.status, .completed)
        XCTAssertEqual(secondPause.stepRuns.last?.status, .waitingForApproval)

        let completed = try await runner.approve(runID: secondPause.id)
        XCTAssertEqual(completed.status, .completed)
        XCTAssertEqual(completed.stepRuns.map(\.status), [.completed, .completed])
    }

    func testRuntimeCreateWorkflowSanitizesClientSuppliedTrustScope() async throws {
        let (runtime, _, _, _) = try await makeWorkflowHarness()
        let workflowID = "sanitize-\(UUID().uuidString)"

        let created = try await runtime.createWorkflow(
            AtlasWorkflowDefinition(
                id: workflowID,
                name: "Sanitize Workflow",
                description: "Ignores client-supplied elevated trust.",
                promptTemplate: "Run it.",
                steps: [
                    AtlasWorkflowStep(
                        id: "step-1",
                        title: "Live write step",
                        kind: .skillAction,
                        skillID: "workflow-test-skill",
                        actionID: "workflow.test_live_write",
                        inputJSON: #"{"message":"Ship it","path":"/Users/ralhassan/Desktop/CODING/Project Atlas/safe.txt"}"#,
                        appName: "InjectedApp",
                        targetPath: "/",
                        sideEffectLevel: "safe_read"
                    )
                ],
                trustScope: AtlasWorkflowTrustScope(
                    approvedRootPaths: ["/"],
                    allowedApps: ["InjectedApp"],
                    allowsSensitiveRead: true,
                    allowsLiveWrite: true
                ),
                approvalMode: .workflowBoundary
            )
        )
        defer { Task { _ = try? await runtime.deleteWorkflow(id: workflowID) } }

        XCTAssertEqual(created.trustScope.approvedRootPaths, ["/Users/ralhassan/Desktop/CODING/Project Atlas/safe.txt"])
        XCTAssertTrue(created.trustScope.allowedApps.isEmpty)
        XCTAssertFalse(created.trustScope.allowsSensitiveRead)
        XCTAssertTrue(created.trustScope.allowsLiveWrite)
        XCTAssertEqual(created.steps.first?.sideEffectLevel, "live_write")
        XCTAssertNil(created.steps.first?.appName)
    }

    func testGremlinWorkflowReferenceFailsSafeWhenWorkflowIsMissing() async throws {
        let (_, context, _, _) = try await makeWorkflowHarness()
        let memoryStore = try MemoryStore(
            databasePath: FileManager.default.temporaryDirectory
                .appendingPathComponent("WorkflowGremlinTests-\(UUID().uuidString).sqlite3")
                .path
        )
        let fileStore = GremlinsFileStore(
            fileURL: FileManager.default.temporaryDirectory
                .appendingPathComponent("WorkflowGremlinTests-\(UUID().uuidString).md"),
            memoryStore: memoryStore
        )
        let scheduler = GremlinScheduler(fileStore: fileStore, context: context, workflowRunner: nil)

        let run = await scheduler.runNow(
            GremlinItem(
                id: "missing-workflow",
                name: "Missing Workflow",
                prompt: "This prompt should not run as a fallback.",
                scheduleRaw: "once 09:00",
                createdAt: ISO8601DateFormatter().string(from: .now),
                workflowID: "missing-workflow-id"
            )
        )

        XCTAssertEqual(run.status, .failed)
        XCTAssertNil(run.workflowRunID)
        XCTAssertTrue(run.errorMessage?.contains("workflow runner is not configured") == true)
    }

    private func makeWorkflowHarness() async throws -> (AgentRuntime, AgentContext, WorkflowStore, WorkflowRunner) {
        let dbURL = FileManager.default.temporaryDirectory
            .appendingPathComponent("WorkflowRunnerTests-\(UUID().uuidString).sqlite3")
        let memoryStore = try MemoryStore(databasePath: dbURL.path)
        let defaults = UserDefaults(suiteName: "WorkflowRunnerTests.\(UUID().uuidString)")!
        let policyStore = ActionPolicyStore(
            directory: FileManager.default.temporaryDirectory
                .appendingPathComponent("WorkflowRunnerPolicies-\(UUID().uuidString)")
        )
        let registry = SkillRegistry(policyStore: policyStore, defaults: defaults)
        try await registry.register([WorkflowTestSkill()])
        _ = try await registry.enable(skillID: "workflow-test-skill")

        let context = try AgentContext(
            config: AtlasConfig(autoApproveDraftTools: false, memoryDatabasePath: dbURL.path),
            logger: AtlasLogger(category: "test"),
            memoryStore: memoryStore,
            skillRegistry: registry,
            actionPolicyStore: policyStore
        )
        let runtime = try AgentRuntime(context: context)
        let store = WorkflowStore(
            directory: FileManager.default.temporaryDirectory
                .appendingPathComponent("WorkflowRunnerStore-\(UUID().uuidString)")
        )
        let runner = WorkflowRunner(context: context, store: store)
        return (runtime, context, store, runner)
    }
}

private struct WorkflowTestSkill: AtlasSkill {
    let manifest = SkillManifest(
        id: "workflow-test-skill",
        name: "Workflow Test Skill",
        version: "1.0.0",
        description: "Test-only workflow skill.",
        category: .system,
        lifecycleState: .installed,
        capabilities: [],
        requiredPermissions: [.liveWrite],
        riskLevel: .high,
        trustProfile: .operational,
        freshnessType: .local,
        preferredQueryTypes: [],
        routingPriority: 1,
        supportsReadOnlyMode: false,
        isUserVisible: false,
        isEnabledByDefault: false,
        author: "Tests",
        source: "test",
        tags: ["test"],
        intent: .atlasSystemTask
    )

    let actions: [SkillActionDefinition] = [
        SkillActionDefinition(
            id: "workflow.test_live_write",
            name: "Test Live Write",
            description: "A workflow test live write action.",
            inputSchemaSummary: "message is required.",
            outputSchemaSummary: "Returns a confirmation.",
            permissionLevel: .execute,
            sideEffectLevel: .liveWrite,
                inputSchema: AtlasToolInputSchema(
                    properties: [
                        "message": AtlasToolInputProperty(type: "string", description: "Message"),
                        "appName": AtlasToolInputProperty(type: "string", description: "App name"),
                        "path": AtlasToolInputProperty(type: "string", description: "Target path")
                    ],
                    required: ["message"]
                )
        )
    ]

    func execute(actionID: String, input: AtlasToolInput, context: SkillExecutionContext) async throws -> SkillExecutionResult {
        let args = try input.dictionary()
        return SkillExecutionResult(
            skillID: manifest.id,
            actionID: actionID,
            output: "Executed test live write.",
            summary: args["message"].map { "Executed \($0)." } ?? "Executed."
        )
    }
}
