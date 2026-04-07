import XCTest
@testable import AtlasSkills
import AtlasGuard
import AtlasLogging
import AtlasShared
import AtlasTools

final class AtlasSkillsTests: XCTestCase {
    func testBuiltInSkillsProviderIncludesBuiltInSkills() async throws {
        let registry = SkillRegistry()
        try await registry.register(BuiltInSkillsProvider().makeSkills())

        let skills = await registry.listAll()

        XCTAssertTrue(skills.contains(where: { $0.manifest.id == "atlas.info" }))
        XCTAssertTrue(skills.contains(where: { $0.manifest.id == "info" }))
        XCTAssertTrue(skills.contains(where: { $0.manifest.id == "image-generation" }))
        XCTAssertTrue(skills.contains(where: { $0.manifest.id == "weather" }))
        XCTAssertTrue(skills.contains(where: { $0.manifest.id == "web-research" }))
        XCTAssertTrue(skills.contains(where: { $0.manifest.id == "file-system" }))
        XCTAssertTrue(skills.contains(where: { $0.manifest.id == "system-actions" }))
    }

    func testValidationDoesNotReEnableDisabledSkill() async throws {
        let defaults = UserDefaults(suiteName: "AtlasSkillsTests.\(UUID().uuidString)")!
        let registry = SkillRegistry(defaults: defaults)
        try await registry.register(BuiltInSkillsProvider().makeSkills())

        _ = try await registry.disable(skillID: "atlas.info")
        let validated = try await registry.validate(
            skillID: "atlas.info",
            context: SkillValidationContext(
                config: AtlasConfig(),
                logger: AtlasLogger(category: "test")
            )
        )

        XCTAssertEqual(validated.manifest.lifecycleState, SkillLifecycleState.disabled)
    }

    func testEnabledActionCatalogTracksLifecycleState() async throws {
        let defaults = UserDefaults(suiteName: "AtlasSkillsTests.\(UUID().uuidString)")!
        let registry = SkillRegistry(defaults: defaults)
        try await registry.register(BuiltInSkillsProvider().makeSkills())

        let initialCatalog = await registry.enabledActionCatalog()
        XCTAssertEqual(initialCatalog.count, 68)
        XCTAssertTrue(initialCatalog.contains(where: { $0.skillID == "info" && $0.action.id == "info.current_time" }))
        XCTAssertTrue(initialCatalog.contains(where: { $0.skillID == "image-generation" && $0.action.id == "image.generate" }))
        XCTAssertTrue(initialCatalog.contains(where: { $0.skillID == "weather" && $0.action.id == "weather.current" }))
        XCTAssertTrue(initialCatalog.contains(where: { $0.skillID == "web-research" && $0.action.id == "web.search" }))
        XCTAssertTrue(initialCatalog.contains(where: { $0.skillID == "file-system" && $0.action.id == "fs.list_directory" }))
        XCTAssertTrue(initialCatalog.contains(where: { $0.skillID == "system-actions" && $0.action.id == "system.open_app" }))

        _ = try await registry.disable(skillID: "atlas.info")

        let disabledCatalog = await registry.enabledActionCatalog()
        XCTAssertEqual(disabledCatalog.count, 65)
        XCTAssertFalse(disabledCatalog.contains(where: { $0.skillID == "atlas.info" }))
    }

    func testGatewayExecutesEnabledSkillAndRejectsDisabledSkill() async throws {
        let defaults = UserDefaults(suiteName: "AtlasSkillsTests.\(UUID().uuidString)")!
        let registry = SkillRegistry(defaults: defaults)
        try await registry.register(BuiltInSkillsProvider().makeSkills())

        let gateway = SkillExecutionGateway(
            registry: registry,
            policyEngine: SkillPolicyEngine(),
            policyStore: ActionPolicyStore(),
            approvalManager: ToolApprovalManager(),
            auditStore: SkillAuditStore()
        )

        let context = SkillExecutionContext(
            conversationID: nil,
            logger: AtlasLogger(category: "test"),
            config: AtlasConfig(),
            permissionManager: PermissionManager(grantedPermissions: [.read]),
            runtimeStatusProvider: {
                AtlasRuntimeStatus(
                    isRunning: true,
                    activeConversationCount: 1,
                    lastMessageAt: nil,
                    lastError: nil,
                    state: .ready,
                    runtimePort: 1984,
                    startedAt: .now,
                    activeRequests: 0,
                    pendingApprovalCount: 0,
                    details: "Ready"
                )
            },
            enabledSkillsProvider: {
                await registry.listEnabled()
            }
        )

        let result = try await gateway.execute(
            SkillExecutionRequest(
                skillID: "atlas.info",
                actionID: "get_runtime_status",
                input: AtlasToolInput(argumentsJSON: "{}"),
                conversationID: nil,
                toolCallID: UUID()
            ),
            context: context
        )

        XCTAssertTrue(result.output.contains("Runtime state: ready"))

        _ = try await registry.disable(skillID: "atlas.info")

        do {
            _ = try await gateway.execute(
                SkillExecutionRequest(
                    skillID: "atlas.info",
                    actionID: "get_runtime_status",
                    input: AtlasToolInput(argumentsJSON: "{}"),
                    conversationID: nil,
                    toolCallID: UUID()
                ),
                context: context
            )
            XCTFail("Expected disabled skill execution to fail.")
        } catch let error as SkillExecutionGatewayError {
            if case .skillNotEnabled = error {
            } else {
                XCTFail("Unexpected gateway error: \(error.localizedDescription)")
            }
        }
    }

    func testGatewayExecutesImageGenerationWithoutApproval() async throws {
        let defaults = UserDefaults(suiteName: "AtlasSkillsTests.\(UUID().uuidString)")!
        let registry = SkillRegistry(defaults: defaults)
        let auditStore = SkillAuditStore()
        try await registry.register([
            ImageGenerationSkill(
                providerManagerFactory: { _ in MockImageProviderManager() }
            )
        ])

        let gateway = SkillExecutionGateway(
            registry: registry,
            policyEngine: SkillPolicyEngine(),
            policyStore: ActionPolicyStore(),
            approvalManager: ToolApprovalManager(),
            auditStore: auditStore
        )

        let context = SkillExecutionContext(
            conversationID: nil,
            logger: AtlasLogger(category: "test"),
            config: AtlasConfig(activeImageProvider: .openAI),
            permissionManager: PermissionManager(grantedPermissions: [.read, .draft]),
            runtimeStatusProvider: { nil },
            enabledSkillsProvider: { await registry.listEnabled() }
        )

        let result = try await gateway.execute(
            SkillExecutionRequest(
                skillID: "image-generation",
                actionID: "image.generate",
                input: AtlasToolInput(argumentsJSON: #"{"prompt":"Create a minimal app icon"}"#),
                conversationID: nil,
                toolCallID: UUID()
            ),
            context: context
        )

        XCTAssertTrue(result.summary.contains("Generated 1 image"))

        let events = await auditStore.recentEvents(limit: 5)
        XCTAssertTrue(events.contains(where: { $0.actionID == "image.generate" && $0.approvalRequired == false && $0.outcome == .executed }))
        XCTAssertFalse(events.contains(where: { $0.inputSummary?.contains("\"prompt\"") == true }))
    }
}

private struct MockSystemActionExecutor: SystemActionExecuting {
    func providerSummary() async -> [String] { [] }
    func validateNotificationCapability() async -> NotificationCapabilityStatus {
        NotificationCapabilityStatus(summary: "Notifications available.", isAvailable: true, issues: [])
    }
    func openApp(named appName: String) async throws -> SystemOpenAppOutput {
        SystemOpenAppOutput(
            requestedAppName: appName,
            resolvedAppName: appName,
            bundleIdentifier: "com.example.\(appName.lowercased())",
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
        SystemReadClipboardOutput(text: "mock", isEmpty: false, message: "Mock clipboard.")
    }
    func openURL(_ url: URL) async throws -> SystemOpenURLOutput {
        SystemOpenURLOutput(url: url.absoluteString, opened: true, message: "Opened URL.")
    }
    func runningApps() async -> SystemRunningAppsOutput {
        SystemRunningAppsOutput(apps: [], count: 0, message: "No running apps.")
    }
    func frontmostApp() async -> SystemFrontmostAppOutput {
        SystemFrontmostAppOutput(name: nil, bundleIdentifier: nil, isAvailable: false, message: "No frontmost app.")
    }
    func isAppRunning(named appName: String) async -> SystemIsAppRunningOutput {
        SystemIsAppRunningOutput(appName: appName, isRunning: false, runningInstances: 0, message: "\(appName) is not running.")
    }
    func openFileWithApp(at url: URL, appName: String) async throws -> SystemOpenPathOutput {
        SystemOpenPathOutput(path: url.path, opened: true, message: "Opened with \(appName).")
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

private struct MockImageProviderManager: ActiveImageProviderManaging {
    func activeProviderType() -> ImageProviderType? { .openAI }

    func provider(for providerType: ImageProviderType) throws -> any ImageProvider {
        MockImageProvider(providerID: providerType, displayName: providerType.title)
    }

    func activeProvider() throws -> any ImageProvider {
        MockImageProvider(providerID: .openAI, displayName: "OpenAI")
    }

    func validate(providerType: ImageProviderType) async -> ImageProviderValidation {
        ImageProviderValidation(providerType: providerType, status: .passed, summary: "Ready.")
    }

    func validateActiveProvider() async -> ImageProviderValidation {
        ImageProviderValidation(providerType: .openAI, status: .passed, summary: "Ready.")
    }
}

private struct MockImageProvider: ImageProvider {
    let providerID: ImageProviderType
    let displayName: String
    let supportsEdit = true

    func validateConfiguration() async -> ImageProviderValidation {
        ImageProviderValidation(providerType: providerID, status: .passed, summary: "Ready.")
    }

    func generateImage(request: ImageProviderGenerateRequest) async throws -> ImageGenerationOutput {
        ImageGenerationOutput(
            providerUsed: displayName,
            promptUsed: request.prompt,
            imageCount: 1,
            images: [
                ImageArtifact(
                    id: UUID(),
                    filePath: "/tmp/mock-output.png",
                    fileName: "mock-output.png",
                    mimeType: "image/png",
                    byteCount: 128
                )
            ],
            metadataSummary: "Generated 1 image with \(displayName)."
        )
    }

    func editImage(request: ImageProviderEditRequest) async throws -> ImageGenerationOutput {
        ImageGenerationOutput(
            providerUsed: displayName,
            promptUsed: request.prompt,
            imageCount: 1,
            images: [
                ImageArtifact(
                    id: UUID(),
                    filePath: "/tmp/mock-edit.png",
                    fileName: "mock-edit.png",
                    mimeType: "image/png",
                    byteCount: 128
                )
            ],
            metadataSummary: "Edited 1 image with \(displayName)."
        )
    }
}
