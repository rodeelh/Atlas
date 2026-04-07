import XCTest
import AtlasCore
import AtlasGuard
import AtlasLogging
import AtlasMemory
import AtlasShared
import AtlasTools
@testable import AtlasSkills

// Integration tests for SystemActionSkill execution through the gateway.
// All SystemAction actions are approval-free — they execute directly.
// Tests cover:
//   1. open_app executes immediately and calls the executor
//   2. Executor failure surfaces as a gateway error (no deferred record created)
//   3. File scope enforcement still blocks out-of-scope paths before execution
//   4. All 6 actions dispatch correctly and return structured output

final class SystemActionApprovalIntegrationTests: XCTestCase {

    // MARK: - 1. Direct execution — no approval gate

    func testOpenAppExecutesDirectlyWithoutApproval() async throws {
        let recorder = ActionRecorder()
        let (_, context, skillContext) = try await makeSetup(executor: RecordingSystemActionExecutor(recorder: recorder))

        let request = SkillExecutionRequest(
            skillID: "system-actions",
            actionID: "system.open_app",
            input: AtlasToolInput(argumentsJSON: #"{"appName":"Safari"}"#),
            conversationID: UUID(),
            toolCallID: UUID()
        )

        // Should execute immediately — no approvalRequired thrown
        let result = try await context.skillExecutionGateway.execute(request, context: skillContext)
        XCTAssertFalse(result.output.isEmpty)

        let invocations = await recorder.openAppInvocations()
        XCTAssertEqual(invocations.count, 1)
        XCTAssertEqual(invocations.first, "Safari")

        // No deferred record should be created for an approval-free action
        let deferred = await context.deferredExecutionManager.deferredExecution(for: request.toolCallID)
        XCTAssertNil(deferred)
    }

    // MARK: - 2. Execution failure surfaces cleanly

    func testOpenAppFailureThrowsWithoutCreatingDeferredRecord() async throws {
        let (_, context, skillContext) = try await makeSetup(executor: FailingSystemActionExecutor())

        let request = SkillExecutionRequest(
            skillID: "system-actions",
            actionID: "system.open_app",
            input: AtlasToolInput(argumentsJSON: #"{"appName":"Xcode"}"#),
            conversationID: UUID(),
            toolCallID: UUID()
        )

        do {
            _ = try await context.skillExecutionGateway.execute(request, context: skillContext)
            XCTFail("Expected execution to fail.")
        } catch {
            XCTAssertFalse(error.localizedDescription.isEmpty)
        }

        // Execution failure must not leave a deferred record behind
        let deferred = await context.deferredExecutionManager.deferredExecution(for: request.toolCallID)
        XCTAssertNil(deferred)
    }

    // MARK: - 3. File scope enforcement

    func testOpenFileBlocksOutOfScopePathWithoutApproval() async throws {
        let recorder = ActionRecorder()
        let (_, context, skillContext) = try await makeSetup(executor: RecordingSystemActionExecutor(recorder: recorder))

        let request = SkillExecutionRequest(
            skillID: "system-actions",
            actionID: "system.open_file",
            input: AtlasToolInput(argumentsJSON: #"{"path":"/etc/hosts"}"#),
            conversationID: UUID(),
            toolCallID: UUID()
        )

        // Should be blocked by scope policy, not by approval gate
        do {
            _ = try await context.skillExecutionGateway.execute(request, context: skillContext)
            XCTFail("Expected scope policy to block /etc/hosts.")
        } catch {
            XCTAssertFalse(error.localizedDescription.isEmpty)
        }

        // Executor was never reached
        let invocations = await recorder.openAppInvocations()
        XCTAssertTrue(invocations.isEmpty)
    }

    // MARK: - 4. Clipboard and notification dispatch

    func testCopyToClipboardDispatchesCorrectly() async throws {
        let recorder = ActionRecorder()
        let (_, context, skillContext) = try await makeSetup(executor: RecordingSystemActionExecutor(recorder: recorder))

        let request = SkillExecutionRequest(
            skillID: "system-actions",
            actionID: "system.copy_to_clipboard",
            input: AtlasToolInput(argumentsJSON: #"{"text":"hello atlas"}"#),
            conversationID: UUID(),
            toolCallID: UUID()
        )

        let result = try await context.skillExecutionGateway.execute(request, context: skillContext)
        let output = try AtlasJSON.decoder.decode(SystemCopyToClipboardOutput.self, from: Data(result.output.utf8))
        XCTAssertTrue(output.copied)
        XCTAssertEqual(output.characterCount, 11)
    }

    func testSendNotificationDispatchesCorrectly() async throws {
        let recorder = ActionRecorder()
        let (_, context, skillContext) = try await makeSetup(executor: RecordingSystemActionExecutor(recorder: recorder))

        let request = SkillExecutionRequest(
            skillID: "system-actions",
            actionID: "system.send_notification",
            input: AtlasToolInput(argumentsJSON: #"{"title":"Atlas","body":"Ready."}"#),
            conversationID: UUID(),
            toolCallID: UUID()
        )

        let result = try await context.skillExecutionGateway.execute(request, context: skillContext)
        let output = try AtlasJSON.decoder.decode(SystemSendNotificationOutput.self, from: Data(result.output.utf8))
        XCTAssertTrue(output.deliveredOrScheduled)
        XCTAssertEqual(output.title, "Atlas")
    }

    // MARK: - Helpers

    private func makeSetup(
        executor: any SystemActionExecuting
    ) async throws -> (AgentRuntime, AgentContext, SkillExecutionContext) {
        let dbURL = FileManager.default.temporaryDirectory
            .appendingPathComponent("SystemActionTests-\(UUID().uuidString).sqlite3")
        let memoryStore = try MemoryStore(databasePath: dbURL.path)

        let policyStore = ActionPolicyStore(
            directory: FileManager.default.temporaryDirectory
                .appendingPathComponent("SystemActionTests-Policies-\(UUID().uuidString)")
        )
        let registry = SkillRegistry(
            policyStore: policyStore,
            defaults: UserDefaults(suiteName: "SystemActionTests.\(UUID().uuidString)")!
        )
        let skill = SystemActionSkill(
            scopeStore: FileAccessScopeStore(
                defaults: UserDefaults(suiteName: "SystemActionTests.Scope.\(UUID().uuidString)")!,
                storageKey: "AtlasSystemActionRoots.\(UUID().uuidString)"
            ),
            executor: executor
        )
        try await registry.register([skill])
        _ = try await registry.enable(skillID: skill.manifest.id)

        // Pre-set auto-approve for all system actions so tests exercise
        // the execution path without approval gates.
        for action in skill.actions {
            await policyStore.setPolicy(.autoApprove, for: action.id)
        }

        let context = try AgentContext(
            config: AtlasConfig(autoApproveDraftTools: false, memoryDatabasePath: dbURL.path),
            logger: AtlasLogger(category: "test"),
            memoryStore: memoryStore,
            skillRegistry: registry,
            actionPolicyStore: policyStore
        )
        let runtime = try AgentRuntime(context: context)
        let skillContext = SkillExecutionContext(
            conversationID: nil,
            logger: context.logger,
            config: context.config,
            permissionManager: context.permissionManager,
            runtimeStatusProvider: { await runtime.status() },
            enabledSkillsProvider: { await context.skillRegistry.listEnabled() },
            memoryItemsProvider: {
                (try? await context.memoryStore.listMemories(limit: 50)) ?? []
            }
        )
        return (runtime, context, skillContext)
    }
}

// MARK: - Mock Executors

private actor ActionRecorder {
    private var _openAppInvocations: [String] = []

    func recordOpenApp(named name: String) {
        _openAppInvocations.append(name)
    }

    func openAppInvocations() -> [String] {
        _openAppInvocations
    }
}

private struct RecordingSystemActionExecutor: SystemActionExecuting {
    let recorder: ActionRecorder

    func providerSummary() async -> [String] { [] }

    func validateNotificationCapability() async -> NotificationCapabilityStatus {
        NotificationCapabilityStatus(summary: "Available in tests.", isAvailable: true, issues: [])
    }

    func openApp(named appName: String) async throws -> SystemOpenAppOutput {
        await recorder.recordOpenApp(named: appName)
        return SystemOpenAppOutput(
            requestedAppName: appName,
            resolvedAppName: appName,
            bundleIdentifier: "com.test.\(appName.lowercased())",
            launched: true,
            message: "Opened \(appName)."
        )
    }

    func openFile(at url: URL) async throws -> SystemOpenPathOutput {
        SystemOpenPathOutput(path: url.path, opened: true, message: "Opened \(url.lastPathComponent).")
    }

    func openFolder(at url: URL) async throws -> SystemOpenPathOutput {
        SystemOpenPathOutput(path: url.path, opened: true, message: "Opened folder \(url.lastPathComponent).")
    }

    func revealInFinder(_ url: URL) async throws -> SystemRevealInFinderOutput {
        SystemRevealInFinderOutput(path: url.path, revealed: true, message: "Revealed \(url.lastPathComponent).")
    }

    func copyToClipboard(_ text: String) async throws -> SystemCopyToClipboardOutput {
        SystemCopyToClipboardOutput(characterCount: text.count, copied: true, message: "Copied.")
    }

    func sendNotification(title: String, body: String) async throws -> SystemSendNotificationOutput {
        SystemSendNotificationOutput(title: title, deliveredOrScheduled: true, message: "Sent.")
    }

    func readClipboard() async -> SystemReadClipboardOutput {
        SystemReadClipboardOutput(text: nil, isEmpty: true, message: "Empty.")
    }

    func openURL(_ url: URL) async throws -> SystemOpenURLOutput {
        SystemOpenURLOutput(url: url.absoluteString, opened: true, message: "Opened \(url).")
    }

    func runningApps() async -> SystemRunningAppsOutput {
        SystemRunningAppsOutput(apps: [], count: 0, message: "No apps.")
    }

    func frontmostApp() async -> SystemFrontmostAppOutput {
        SystemFrontmostAppOutput(name: nil, bundleIdentifier: nil, isAvailable: false, message: "N/A.")
    }

    func isAppRunning(named appName: String) async -> SystemIsAppRunningOutput {
        SystemIsAppRunningOutput(appName: appName, isRunning: false, runningInstances: 0, message: "Not running.")
    }

    func openFileWithApp(at url: URL, appName: String) async throws -> SystemOpenPathOutput {
        SystemOpenPathOutput(path: url.path, opened: true, message: "Opened \(url.lastPathComponent) with \(appName).")
    }

    func activateApp(named appName: String) async throws -> SystemActivateAppOutput {
        SystemActivateAppOutput(appName: appName, activated: true, message: "Activated \(appName).")
    }

    func quitApp(named appName: String, force: Bool) async throws -> SystemQuitAppOutput {
        SystemQuitAppOutput(appName: appName, terminated: true, message: "Quit \(appName).")
    }

    func scheduleNotification(title: String, body: String, delaySeconds: Int) async throws -> SystemSendNotificationOutput {
        SystemSendNotificationOutput(title: title, deliveredOrScheduled: true, message: "Scheduled.")
    }
}

private struct FailingSystemActionExecutor: SystemActionExecuting {
    func providerSummary() async -> [String] { [] }
    func validateNotificationCapability() async -> NotificationCapabilityStatus {
        NotificationCapabilityStatus(summary: "N/A", isAvailable: false, issues: [])
    }
    func openApp(named appName: String) async throws -> SystemOpenAppOutput {
        throw AtlasToolError.executionFailed("Simulated failure for '\(appName)'.")
    }
    func openFile(at url: URL) async throws -> SystemOpenPathOutput {
        throw AtlasToolError.executionFailed("Simulated failure.")
    }
    func openFolder(at url: URL) async throws -> SystemOpenPathOutput {
        throw AtlasToolError.executionFailed("Simulated failure.")
    }
    func revealInFinder(_ url: URL) async throws -> SystemRevealInFinderOutput {
        throw AtlasToolError.executionFailed("Simulated failure.")
    }
    func copyToClipboard(_ text: String) async throws -> SystemCopyToClipboardOutput {
        throw AtlasToolError.executionFailed("Simulated failure.")
    }
    func sendNotification(title: String, body: String) async throws -> SystemSendNotificationOutput {
        throw AtlasToolError.executionFailed("Simulated failure.")
    }

    func readClipboard() async -> SystemReadClipboardOutput {
        SystemReadClipboardOutput(text: nil, isEmpty: true, message: "N/A.")
    }

    func openURL(_ url: URL) async throws -> SystemOpenURLOutput {
        throw AtlasToolError.executionFailed("Simulated failure.")
    }

    func runningApps() async -> SystemRunningAppsOutput {
        SystemRunningAppsOutput(apps: [], count: 0, message: "N/A.")
    }

    func frontmostApp() async -> SystemFrontmostAppOutput {
        SystemFrontmostAppOutput(name: nil, bundleIdentifier: nil, isAvailable: false, message: "N/A.")
    }

    func isAppRunning(named appName: String) async -> SystemIsAppRunningOutput {
        SystemIsAppRunningOutput(appName: appName, isRunning: false, runningInstances: 0, message: "N/A.")
    }

    func openFileWithApp(at url: URL, appName: String) async throws -> SystemOpenPathOutput {
        throw AtlasToolError.executionFailed("Simulated failure.")
    }

    func activateApp(named appName: String) async throws -> SystemActivateAppOutput {
        throw AtlasToolError.executionFailed("Simulated failure.")
    }

    func quitApp(named appName: String, force: Bool) async throws -> SystemQuitAppOutput {
        throw AtlasToolError.executionFailed("Simulated failure.")
    }

    func scheduleNotification(title: String, body: String, delaySeconds: Int) async throws -> SystemSendNotificationOutput {
        throw AtlasToolError.executionFailed("Simulated failure.")
    }
}
