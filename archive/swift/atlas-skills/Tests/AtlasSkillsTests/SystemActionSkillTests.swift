import Foundation
import XCTest
import AtlasGuard
import AtlasLogging
import AtlasShared
@testable import AtlasSkills

final class SystemActionSkillTests: XCTestCase {
    func testSystemActionSkillBlocksOutOfScopeFileTargets() async throws {
        let workspace = FileManager.default.temporaryDirectory
            .appendingPathComponent("SystemActionSkillTests-\(UUID().uuidString)", isDirectory: true)
        try FileManager.default.createDirectory(at: workspace, withIntermediateDirectories: true)
        let allowedFile = workspace.appendingPathComponent("notes.txt")
        try Data("Atlas".utf8).write(to: allowedFile)

        let scopeStore = FileAccessScopeStore(
            defaults: UserDefaults(suiteName: "SystemActionSkillTests.\(UUID().uuidString)")!,
            storageKey: "AtlasSystemActionRoots.\(UUID().uuidString)"
        )
        let bookmark = try FileAccessScopeStore.makeBookmarkData(for: workspace)
        _ = try await scopeStore.addRoot(bookmarkData: bookmark)

        let skill = SystemActionSkill(scopeStore: scopeStore, executor: MockSystemActionExecutor())
        let context = makeContext()

        await XCTAssertThrowsErrorAsync(
            try await skill.execute(
                actionID: "system.open_file",
                input: AtlasToolInput(argumentsJSON: #"{"path":"/etc/hosts"}"#),
                context: context
            )
        )
    }

    func testSystemActionSkillReturnsStructuredOutputs() async throws {
        let skill = SystemActionSkill(
            scopeStore: FileAccessScopeStore(
                defaults: UserDefaults(suiteName: "SystemActionSkillTests.\(UUID().uuidString)")!,
                storageKey: "AtlasSystemActionRoots.\(UUID().uuidString)"
            ),
            executor: MockSystemActionExecutor()
        )

        let appResult = try await skill.execute(
            actionID: "system.open_app",
            input: AtlasToolInput(argumentsJSON: #"{"appName":"Xcode"}"#),
            context: makeContext()
        )
        let appOutput = try AtlasJSON.decoder.decode(SystemOpenAppOutput.self, from: Data(appResult.output.utf8))
        XCTAssertEqual(appOutput.resolvedAppName, "Xcode")
        XCTAssertTrue(appOutput.launched)

        let clipboardResult = try await skill.execute(
            actionID: "system.copy_to_clipboard",
            input: AtlasToolInput(argumentsJSON: #"{"text":"Ship it"}"#),
            context: makeContext()
        )
        let clipboardOutput = try AtlasJSON.decoder.decode(SystemCopyToClipboardOutput.self, from: Data(clipboardResult.output.utf8))
        XCTAssertEqual(clipboardOutput.characterCount, 7)
        XCTAssertTrue(clipboardOutput.copied)
    }

    private func makeContext() -> SkillExecutionContext {
        SkillExecutionContext(
            conversationID: nil,
            logger: AtlasLogger(category: "test"),
            config: AtlasConfig(),
            permissionManager: PermissionManager(grantedPermissions: [.read, .draft]),
            runtimeStatusProvider: { nil },
            enabledSkillsProvider: { [] }
        )
    }
}

private func XCTAssertThrowsErrorAsync(
    _ expression: @autoclosure () async throws -> some Any,
    file: StaticString = #filePath,
    line: UInt = #line
) async {
    do {
        _ = try await expression()
        XCTFail("Expected error to be thrown.", file: file, line: line)
    } catch {
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
