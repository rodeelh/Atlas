import Foundation
import XCTest
import AtlasGuard
import AtlasLogging
import AtlasShared
import AtlasTools
@testable import AtlasSkills

final class AppleScriptSkillTests: XCTestCase {

    // MARK: - validateConfiguration

    func testValidateConfigurationPassesWhenExecutorSucceeds() async {
        let skill = AppleScriptSkill(executor: MockAppleScriptExecutor(result: "ok"))
        let result = await skill.validateConfiguration(context: makeValidationContext())
        XCTAssertEqual(result.status, .passed)
    }

    func testValidateConfigurationFailsWhenExecutorThrows() async {
        let skill = AppleScriptSkill(executor: MockAppleScriptExecutor(error: AtlasToolError.executionFailed("osascript failed.")))
        let result = await skill.validateConfiguration(context: makeValidationContext())
        XCTAssertEqual(result.status, .failed)
        XCTAssertTrue(result.issues.count > 0)
    }

    func testValidateConfigurationFailsWhenExecutorReturnsUnexpectedResult() async {
        let skill = AppleScriptSkill(executor: MockAppleScriptExecutor(result: "unexpected"))
        let result = await skill.validateConfiguration(context: makeValidationContext())
        XCTAssertEqual(result.status, .failed)
    }

    // MARK: - AppleScriptValidator

    func testValidatorBlocksDoShellScript() {
        let validator = AppleScriptValidator()
        XCTAssertThrowsError(try validator.validate("do shell script \"rm -rf /\""))
    }

    func testValidatorBlocksDisplayDialog() {
        let validator = AppleScriptValidator()
        XCTAssertThrowsError(try validator.validate("display dialog \"hello\""))
    }

    func testValidatorBlocksDisplayAlert() {
        let validator = AppleScriptValidator()
        XCTAssertThrowsError(try validator.validate("display alert \"warning\""))
    }

    func testValidatorBlocksKeystroke() {
        let validator = AppleScriptValidator()
        XCTAssertThrowsError(try validator.validate("tell application \"System Events\" to keystroke \"a\""))
    }

    func testValidatorBlocksKeyCode() {
        let validator = AppleScriptValidator()
        XCTAssertThrowsError(try validator.validate("tell application \"System Events\" to key code 36"))
    }

    func testValidatorBlocksChooseFile() {
        let validator = AppleScriptValidator()
        XCTAssertThrowsError(try validator.validate("set f to choose file"))
    }

    func testValidatorBlocksChooseFolder() {
        let validator = AppleScriptValidator()
        XCTAssertThrowsError(try validator.validate("set f to choose folder"))
    }

    func testValidatorPassesWellFormedScript() {
        let validator = AppleScriptValidator()
        XCTAssertNoThrow(try validator.validate("""
        tell application "Finder"
            return name of front window
        end tell
        """))
    }

    func testValidatorIsCaseInsensitive() {
        let validator = AppleScriptValidator()
        XCTAssertThrowsError(try validator.validate("DO SHELL SCRIPT \"echo hi\""))
        XCTAssertThrowsError(try validator.validate("Display Dialog \"hi\""))
    }

    // MARK: - safari_navigate rejects javascript: URLs

    func testSafariNavigateRejectsJavascriptURL() async throws {
        let skill = AppleScriptSkill(executor: MockAppleScriptExecutor(result: ""))
        await XCTAssertThrowsErrorAsync(
            try await skill.execute(
                actionID: "applescript.safari_navigate",
                input: AtlasToolInput(argumentsJSON: #"{"url":"javascript:alert(1)"}"#),
                context: makeContext()
            )
        )
    }

    func testSafariNavigateRejectsJavascriptURLCaseInsensitive() async throws {
        let skill = AppleScriptSkill(executor: MockAppleScriptExecutor(result: ""))
        await XCTAssertThrowsErrorAsync(
            try await skill.execute(
                actionID: "applescript.safari_navigate",
                input: AtlasToolInput(argumentsJSON: #"{"url":"JAVASCRIPT:alert(1)"}"#),
                context: makeContext()
            )
        )
    }

    // MARK: - run_custom blocks disallowed constructs

    func testRunCustomBlocksDoShellScript() async throws {
        let executor = MockAppleScriptExecutor(result: "should not reach")
        let skill = AppleScriptSkill(executor: executor)
        await XCTAssertThrowsErrorAsync(
            try await skill.execute(
                actionID: "applescript.run_custom",
                input: AtlasToolInput(argumentsJSON: #"{"script":"do shell script \"ls\"","description":"list files"}"#),
                context: makeContext()
            )
        )
        XCTAssertFalse(executor.didRun, "Executor must not be called when validation fails.")
    }

    func testRunCustomBlocksDisplayDialog() async throws {
        let executor = MockAppleScriptExecutor(result: "should not reach")
        let skill = AppleScriptSkill(executor: executor)
        await XCTAssertThrowsErrorAsync(
            try await skill.execute(
                actionID: "applescript.run_custom",
                input: AtlasToolInput(argumentsJSON: #"{"script":"display dialog \"hi\"","description":"greet"}"#),
                context: makeContext()
            )
        )
        XCTAssertFalse(executor.didRun)
    }

    // MARK: - music_control generates and runs the correct script

    func testMusicControlPlayRunsScript() async throws {
        let executor = MockAppleScriptExecutor(result: "Playback started.")
        let skill = AppleScriptSkill(executor: executor)
        let result = try await skill.execute(
            actionID: "applescript.music_control",
            input: AtlasToolInput(argumentsJSON: #"{"command":"play"}"#),
            context: makeContext()
        )
        XCTAssertTrue(executor.didRun)
        XCTAssertTrue(result.success)
        XCTAssertTrue(result.output.contains("Playback started."))
        XCTAssertTrue(executor.lastScript?.contains("play") ?? false)
        XCTAssertTrue(executor.lastScript?.contains("Music") ?? false)
    }

    func testMusicControlPauseRunsScript() async throws {
        let executor = MockAppleScriptExecutor(result: "Playback paused.")
        let skill = AppleScriptSkill(executor: executor)
        let result = try await skill.execute(
            actionID: "applescript.music_control",
            input: AtlasToolInput(argumentsJSON: #"{"command":"pause"}"#),
            context: makeContext()
        )
        XCTAssertTrue(result.success)
        XCTAssertTrue(executor.lastScript?.contains("pause") ?? false)
    }

    func testMusicControlSetVolumeRequiresVolumeParam() async throws {
        let skill = AppleScriptSkill(executor: MockAppleScriptExecutor(result: ""))
        await XCTAssertThrowsErrorAsync(
            try await skill.execute(
                actionID: "applescript.music_control",
                input: AtlasToolInput(argumentsJSON: #"{"command":"set_volume"}"#),
                context: makeContext()
            )
        )
    }

    func testMusicControlSetVolumeClamps() async throws {
        let executor = MockAppleScriptExecutor(result: "Volume set to 100.")
        let skill = AppleScriptSkill(executor: executor)
        _ = try await skill.execute(
            actionID: "applescript.music_control",
            input: AtlasToolInput(argumentsJSON: #"{"command":"set_volume","volume":200}"#),
            context: makeContext()
        )
        XCTAssertTrue(executor.lastScript?.contains("sound volume to 100") ?? false)
    }

    func testMusicControlSpotifyTarget() async throws {
        let executor = MockAppleScriptExecutor(result: "Playback started.")
        let skill = AppleScriptSkill(executor: executor)
        _ = try await skill.execute(
            actionID: "applescript.music_control",
            input: AtlasToolInput(argumentsJSON: #"{"command":"play","targetApp":"Spotify"}"#),
            context: makeContext()
        )
        XCTAssertTrue(executor.lastScript?.contains("Spotify") ?? false)
    }

    // MARK: - Executor error becomes success:false result

    func testExecutorErrorBecomesFailedResult() async throws {
        let executor = MockAppleScriptExecutor(error: AtlasToolError.executionFailed("Access denied."))
        let skill = AppleScriptSkill(executor: executor)
        let result = try await skill.execute(
            actionID: "applescript.music_read",
            input: AtlasToolInput(argumentsJSON: #"{"action":"now_playing"}"#),
            context: makeContext()
        )
        XCTAssertFalse(result.success)
        XCTAssertEqual(result.summary, "Access denied.")
    }

    // MARK: - Manifest

    func testManifestIsEnabledByDefault() {
        let skill = AppleScriptSkill(executor: MockAppleScriptExecutor(result: ""))
        XCTAssertTrue(skill.manifest.isEnabledByDefault)
    }

    func testManifestHas13Actions() {
        let skill = AppleScriptSkill(executor: MockAppleScriptExecutor(result: ""))
        XCTAssertEqual(skill.actions.count, 17)
    }

    func testUnknownActionIDThrows() async throws {
        let skill = AppleScriptSkill(executor: MockAppleScriptExecutor(result: ""))
        await XCTAssertThrowsErrorAsync(
            try await skill.execute(
                actionID: "applescript.does_not_exist",
                input: AtlasToolInput(argumentsJSON: "{}"),
                context: makeContext()
            )
        )
    }

    // MARK: - Helpers

    private func makeContext() -> SkillExecutionContext {
        SkillExecutionContext(
            conversationID: nil,
            logger: AtlasLogger(category: "test"),
            config: AtlasConfig(),
            permissionManager: PermissionManager(grantedPermissions: [.read, .draft, .execute]),
            runtimeStatusProvider: { nil },
            enabledSkillsProvider: { [] }
        )
    }

    private func makeValidationContext() -> SkillValidationContext {
        SkillValidationContext(
            config: AtlasConfig(),
            logger: AtlasLogger(category: "test")
        )
    }
}

// MARK: - Mock executor

private final class MockAppleScriptExecutor: AppleScriptExecuting, @unchecked Sendable {
    private let result: String?
    private let error: Error?
    private(set) var didRun = false
    private(set) var lastScript: String?

    init(result: String) {
        self.result = result
        self.error = nil
    }

    init(error: Error) {
        self.result = nil
        self.error = error
    }

    func run(_ script: String, timeout: TimeInterval) async throws -> String {
        didRun = true
        lastScript = script
        if let error { throw error }
        return result ?? ""
    }
}

// MARK: - Async throw helper

private func XCTAssertThrowsErrorAsync(
    _ expression: @autoclosure () async throws -> some Any,
    file: StaticString = #filePath,
    line: UInt = #line
) async {
    do {
        _ = try await expression()
        XCTFail("Expected an error to be thrown.", file: file, line: line)
    } catch {}
}
