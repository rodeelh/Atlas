import XCTest
import AtlasShared
@testable import AtlasCore

// MARK: - AgentLoop fallback synthesis tests

/// Tests for the fallback message system that ensures failed turns always produce
/// a human-readable assistant message, never leaving the UI with an empty bubble.
///
/// Coverage:
/// - AgentLoop.synthesizeFallback: message quality for known failure patterns
/// - AgentRuntime.humanizeSystemError: system-level errors are humanized
/// - Key contract: no raw tool IDs, HTTP codes, or stack traces ever surface
final class AgentLoopFallbackTests: XCTestCase {

    // MARK: synthesizeFallback — denial

    func testSynthesizeFallback_denied() {
        let result = AtlasToolResult(
            toolCallID: UUID(), output: "", success: false,
            errorMessage: "The action was denied by the user."
        )
        let message = AgentLoop.synthesizeFallback(for: result)
        XCTAssertEqual(message, "That action was declined.")
        XCTAssertFalse(message.isEmpty)
    }

    func testSynthesizeFallback_declined_variant() {
        let result = AtlasToolResult(
            toolCallID: UUID(), output: "", success: false,
            errorMessage: "declined by policy"
        )
        let message = AgentLoop.synthesizeFallback(for: result)
        XCTAssertEqual(message, "That action was declined.")
    }

    // MARK: synthesizeFallback — auth / credential

    func testSynthesizeFallback_authError_401() {
        let result = AtlasToolResult(
            toolCallID: UUID(), output: "", success: false,
            errorMessage: "Authentication failed (HTTP 401)"
        )
        let message = AgentLoop.synthesizeFallback(for: result)
        XCTAssertTrue(message.lowercased().contains("credentials") || message.lowercased().contains("missing"),
                      "Expected credential-related message, got: \(message)")
        XCTAssertFalse(message.contains("401"), "HTTP codes must not surface in fallback messages")
    }

    func testSynthesizeFallback_missingCredential() {
        let result = AtlasToolResult(
            toolCallID: UUID(), output: "", success: false,
            errorMessage: "credential not found in Keychain"
        )
        let message = AgentLoop.synthesizeFallback(for: result)
        XCTAssertTrue(message.lowercased().contains("credentials") || message.lowercased().contains("missing"),
                      "Expected credential hint, got: \(message)")
    }

    // MARK: synthesizeFallback — network / timeout

    func testSynthesizeFallback_networkTimeout() {
        let result = AtlasToolResult(
            toolCallID: UUID(), output: "", success: false,
            errorMessage: "The request timed out after 10 seconds"
        )
        let message = AgentLoop.synthesizeFallback(for: result)
        XCTAssertTrue(message.lowercased().contains("timed out") || message.lowercased().contains("connection"),
                      "Expected timeout message, got: \(message)")
    }

    func testSynthesizeFallback_networkError() {
        let result = AtlasToolResult(
            toolCallID: UUID(), output: "", success: false,
            errorMessage: "network connection lost"
        )
        let message = AgentLoop.synthesizeFallback(for: result)
        XCTAssertTrue(message.lowercased().contains("connection") || message.lowercased().contains("timed out"),
                      "Expected network message, got: \(message)")
    }

    // MARK: synthesizeFallback — API / validation

    func testSynthesizeFallback_apiValidationFailure() {
        let result = AtlasToolResult(
            toolCallID: UUID(), output: "", success: false,
            errorMessage: "API validation failed: response is an empty JSON array"
        )
        let message = AgentLoop.synthesizeFallback(for: result)
        XCTAssertTrue(message.lowercased().contains("api") || message.lowercased().contains("cleanly"),
                      "Expected API-related message, got: \(message)")
        XCTAssertFalse(message.contains("JSON"), "Internal terms must not surface")
        XCTAssertFalse(message.contains("array"), "Internal terms must not surface")
    }

    func testSynthesizeFallback_httpError() {
        let result = AtlasToolResult(
            toolCallID: UUID(), output: "", success: false,
            errorMessage: "HTTP 500 server error from endpoint"
        )
        let message = AgentLoop.synthesizeFallback(for: result)
        XCTAssertFalse(message.contains("500"), "HTTP codes must not surface")
        XCTAssertFalse(message.isEmpty)
    }

    // MARK: synthesizeFallback — Forge / skill

    func testSynthesizeFallback_forgePlanFailure() {
        let result = AtlasToolResult(
            toolCallID: UUID(), output: "", success: false,
            errorMessage: "Forge skill proposal was rejected at validation gate"
        )
        let message = AgentLoop.synthesizeFallback(for: result)
        XCTAssertTrue(message.lowercased().contains("build") || message.lowercased().contains("finish"),
                      "Expected forge-context message, got: \(message)")
    }

    // MARK: synthesizeFallback — permission / approval

    func testSynthesizeFallback_permissionRequired() {
        let result = AtlasToolResult(
            toolCallID: UUID(), output: "", success: false,
            errorMessage: "permission not granted for this action"
        )
        let message = AgentLoop.synthesizeFallback(for: result)
        XCTAssertTrue(message.lowercased().contains("approval") || message.lowercased().contains("permission"),
                      "Expected approval hint, got: \(message)")
    }

    // MARK: synthesizeFallback — generic fallback

    func testSynthesizeFallback_unknownError_doesNotLeakRawMessage() {
        let result = AtlasToolResult(
            toolCallID: UUID(), output: "", success: false,
            errorMessage: "unexpected internal error: NullPointerException at line 47"
        )
        let message = AgentLoop.synthesizeFallback(for: result)
        XCTAssertFalse(message.contains("NullPointerException"), "Stack trace text must not surface")
        XCTAssertFalse(message.contains("line 47"), "Internal references must not surface")
        XCTAssertFalse(message.isEmpty)
    }

    func testSynthesizeFallback_nilErrorMessage_returnsGeneric() {
        let result = AtlasToolResult(
            toolCallID: UUID(), output: "", success: false,
            errorMessage: nil
        )
        let message = AgentLoop.synthesizeFallback(for: result)
        XCTAssertFalse(message.isEmpty, "Must return a non-empty fallback even with nil errorMessage")
        // Generic fallback should be short and first-person
        XCTAssertTrue(message.hasPrefix("I ") || message.hasPrefix("That") || message.hasPrefix("This"),
                      "Fallback must be first-person or human-facing, got: \(message)")
    }

    // MARK: synthesizeFallback — tone and length checks

    func testSynthesizeFallback_allCases_areShortAndHumanReadable() {
        // Pairs of (raw error input, technical term that must NOT appear in fallback output).
        // Trigger words like "declined" / "timed out" naturally reappear in human-readable
        // output — that's intentional. We only check that internal technical strings are hidden.
        let cases: [(input: String, forbiddenInOutput: String)] = [
            ("denied", "denied by"),           // passthrough phrase suppressed; "declined" is fine
            ("HTTP 401 Unauthorized", "401"),
            ("HTTP 403 Forbidden", "403"),
            ("auth_token_invalid", "auth_token_invalid"),
            ("credentialStore lookup failed", "credentialStore"),
            ("URLSession timeout after 30s", "URLSession"),
            ("network error: ECONNREFUSED", "ECONNREFUSED"),
            ("APIValidationService rejected plan", "APIValidationService"),
            ("HTTP 500 Internal Server Error", "500"),
            ("endpoint URL nil", "URL nil"),
            ("ForgeOrchestrationSkill gate 2 failed", "ForgeOrchestrationSkill"),
            ("SkillExecutionResult success=false", "SkillExecutionResult"),
            ("PermissionManager denied level=execute", "PermissionManager"),
            ("DeferredExecution approval timeout", "DeferredExecution"),
            ("randomXYZinternalError_code_9999", "randomXYZinternalError_code_9999")
        ]
        for (input, forbidden) in cases {
            let result = AtlasToolResult(
                toolCallID: UUID(), output: "", success: false,
                errorMessage: input
            )
            let message = AgentLoop.synthesizeFallback(for: result)
            XCTAssertFalse(message.isEmpty, "Fallback must not be empty for: \(input)")
            XCTAssertLessThan(message.count, 160, "Fallback should be short (<160 chars), got \(message.count) for: \(input)")
            XCTAssertFalse(message.lowercased().contains(forbidden.lowercased()),
                           "Technical text '\(forbidden)' must not surface in fallback for: \(input)\n  Got: \(message)")
        }
    }

    // MARK: AgentRuntime.humanizeSystemError

    func testHumanizeSystemError_modelUnavailable() {
        let error = AgentOrchestratorError.modelUnavailable
        let message = AgentRuntime.humanizeSystemError(error)
        XCTAssertTrue(message.lowercased().contains("model") || message.lowercased().contains("api key"),
                      "Should hint at model/key issue, got: \(message)")
        XCTAssertFalse(message.contains("modelUnavailable"), "Must not expose raw enum name")
    }

    func testHumanizeSystemError_neverExposesLocalizationDescription() {
        // A contrived error whose localizedDescription contains technical text
        struct TechnicalError: Error {
            var localizedDescription: String { "OpenAI API key invalid: sk-abc123 returned 401 Unauthorized" }
        }
        let message = AgentRuntime.humanizeSystemError(TechnicalError())
        XCTAssertFalse(message.contains("sk-"), "Must not expose API key fragments")
        XCTAssertFalse(message.contains("401"), "Must not expose HTTP codes")
        XCTAssertFalse(message.isEmpty)
    }

    func testHumanizeSystemError_networkFailure() {
        struct NetworkError: Error {
            var localizedDescription: String { "network connection was lost" }
        }
        let message = AgentRuntime.humanizeSystemError(NetworkError())
        XCTAssertTrue(message.lowercased().contains("connection") || message.lowercased().contains("network"),
                      "Should hint at connectivity, got: \(message)")
    }

    func testHumanizeSystemError_unknownError_returnsGeneric() {
        struct RandomError: Error {
            var localizedDescription: String { "unexpected failure in subsystem alpha-7" }
        }
        let message = AgentRuntime.humanizeSystemError(RandomError())
        XCTAssertFalse(message.contains("alpha-7"), "Must not leak internal subsystem names")
        XCTAssertFalse(message.isEmpty)
    }
}
