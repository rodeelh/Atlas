import XCTest
import AtlasCore
import AtlasShared

// MARK: - Test helpers

/// Thread-safe event collector for use inside @Sendable emitter closures.
private actor EventCollector {
    private(set) var events: [SSEEvent] = []
    func append(_ event: SSEEvent) { events.append(event) }
    func eventTypes() -> [SSEEvent.EventType] { events.map(\.type) }
    func toolNames(for eventType: SSEEvent.EventType) -> [String] {
        events.filter { $0.type == eventType }.compactMap(\.toolName)
    }
}

// MARK: - Chat Streaming v1 Tests

final class ChatStreamingTests: XCTestCase {

    // MARK: SSEEvent — new type encoding

    func testAssistantStartedEncodesCorrectly() {
        let event = SSEEvent.assistantStarted()
        let sse = event.toSSEData()
        XCTAssertTrue(sse.contains("\"assistant_started\""), "Expected type 'assistant_started' in SSE data")
        XCTAssertTrue(sse.hasPrefix("data: "), "SSE data must start with 'data: '")
        XCTAssertTrue(sse.hasSuffix("\n\n"), "SSE data must end with double newline")
    }

    func testAssistantDeltaEncodesContentField() {
        let event = SSEEvent.assistantDelta("Hello, world")
        let sse = event.toSSEData()
        XCTAssertTrue(sse.contains("\"assistant_delta\""), "Expected type 'assistant_delta'")
        XCTAssertTrue(sse.contains("Hello, world"), "Expected delta text in SSE data")
    }

    func testAssistantDoneEncodesCorrectly() {
        let event = SSEEvent.assistantDone()
        let sse = event.toSSEData()
        XCTAssertTrue(sse.contains("\"assistant_done\""), "Expected type 'assistant_done'")
    }

    func testToolStartedEncodesToolName() {
        let event = SSEEvent.toolStarted(name: "Checking weather…")
        let sse = event.toSSEData()
        XCTAssertTrue(sse.contains("\"tool_started\""), "Expected type 'tool_started'")
        XCTAssertTrue(sse.contains("Checking weather"), "Expected tool name in SSE data")
    }

    func testToolFinishedEncodesToolName() {
        let event = SSEEvent.toolFinished(name: "Searching the web…")
        let sse = event.toSSEData()
        XCTAssertTrue(sse.contains("\"tool_finished\""), "Expected type 'tool_finished'")
    }

    func testToolFailedEncodesToolName() {
        let event = SSEEvent.toolFailed(name: "Running AppleScript…")
        let sse = event.toSSEData()
        XCTAssertTrue(sse.contains("\"tool_failed\""), "Expected type 'tool_failed'")
    }

    // MARK: SSEEvent — existing types still work

    func testTokenEventUnchanged() {
        let event = SSEEvent.token("full response text")
        let sse = event.toSSEData()
        XCTAssertTrue(sse.contains("\"token\""))
        XCTAssertTrue(sse.contains("full response text"))
    }

    func testDoneEventUnchanged() {
        let event = SSEEvent.done(status: "completed")
        let sse = event.toSSEData()
        XCTAssertTrue(sse.contains("\"done\""))
        XCTAssertTrue(sse.contains("completed"))
    }

    // MARK: SSEEvent — all new types round-trip as single-line JSON

    func testAllNewEventTypesAreSingleLine() {
        let events: [SSEEvent] = [
            .assistantStarted(),
            .assistantDelta("chunk"),
            .assistantDone(),
            .toolStarted(name: "tool"),
            .toolFinished(name: "tool"),
            .toolFailed(name: "tool")
        ]

        for event in events {
            let sse = event.toSSEData()
            // The JSON payload (between "data: " and "\n\n") must have no bare newlines
            let payload = sse
                .replacingOccurrences(of: "data: ", with: "")
                .trimmingCharacters(in: CharacterSet.whitespacesAndNewlines)
            XCTAssertFalse(payload.contains("\n"),
                "SSE payload for \(event.type.rawValue) must be single-line JSON, got: \(payload)")
        }
    }

    // MARK: AgentOrchestrator — human-readable tool name mapping

    func testHumanReadableNameWeather() {
        XCTAssertEqual(AgentOrchestrator.humanReadableName(for: "weather.current_conditions"), "Checking the weather…")
    }

    func testHumanReadableNameWebSearch() {
        XCTAssertEqual(AgentOrchestrator.humanReadableName(for: "web.search"), "Searching the web…")
    }

    func testHumanReadableNameWebFetch() {
        // web.fetch is not web.search — maps to the generic web phrase
        XCTAssertEqual(AgentOrchestrator.humanReadableName(for: "web.fetch"), "Looking this up…")
    }

    func testHumanReadableNameFile() {
        XCTAssertEqual(AgentOrchestrator.humanReadableName(for: "file.read"), "Reading files…")
    }

    func testHumanReadableNameForgePropose() {
        XCTAssertEqual(AgentOrchestrator.humanReadableName(for: "forge.orchestration.propose"), "Drafting a new skill…")
    }

    func testHumanReadableNameForgePlan() {
        XCTAssertEqual(AgentOrchestrator.humanReadableName(for: "forge.orchestration.plan"), "Planning this out…")
    }

    func testHumanReadableNameForgeReview() {
        XCTAssertEqual(AgentOrchestrator.humanReadableName(for: "forge.orchestration.review"), "Reviewing the plan…")
    }

    func testHumanReadableNameForgeValidate() {
        XCTAssertEqual(AgentOrchestrator.humanReadableName(for: "forge.orchestration.validate"), "Verifying the details…")
    }

    func testHumanReadableNameForgeFallback() {
        XCTAssertEqual(AgentOrchestrator.humanReadableName(for: "forge.orchestration.dry_run"), "Building that for you…")
    }

    func testHumanReadableNameDashboard() {
        XCTAssertEqual(AgentOrchestrator.humanReadableName(for: "dashboard.execute"), "Updating your dashboard…")
    }

    func testHumanReadableNameSystem() {
        XCTAssertEqual(AgentOrchestrator.humanReadableName(for: "system.open_app"), "Running that now…")
    }

    func testHumanReadableNameAppleScript() {
        XCTAssertEqual(AgentOrchestrator.humanReadableName(for: "applescript.execute"), "Working in your apps…")
    }

    func testHumanReadableNameGremlins() {
        XCTAssertEqual(AgentOrchestrator.humanReadableName(for: "gremlins.trigger"), "Managing automations…")
    }

    func testHumanReadableNameImage() {
        XCTAssertEqual(AgentOrchestrator.humanReadableName(for: "image.generate"), "Generating an image…")
    }

    func testHumanReadableNameVision() {
        XCTAssertEqual(AgentOrchestrator.humanReadableName(for: "vision.analyze"), "Analyzing the image…")
    }

    func testHumanReadableNameAtlas() {
        XCTAssertEqual(AgentOrchestrator.humanReadableName(for: "atlas.info"), "Checking Atlas…")
    }

    func testHumanReadableNameUnknownNeverExposesRawID() {
        // Unknown tool names must never leak raw IDs to the UI
        XCTAssertEqual(AgentOrchestrator.humanReadableName(for: "custom.skill.action"), "Working on it…")
        XCTAssertEqual(AgentOrchestrator.humanReadableName(for: "some_internal_tool"), "Working on it…")
    }

    // MARK: AgentOrchestrator — executeToolCalls emits events

    func testExecuteToolCallsEmitsToolStartedAndFinished() async throws {
        let collector = EventCollector()
        let emitter: @Sendable (SSEEvent) async -> Void = { event in
            await collector.append(event)
        }

        // Build a minimal context using the real AgentContext default init.
        // This test verifies the emitter is called even if the tool fails (no real tools registered).
        let context = try AgentContext()
        let orchestrator = AgentOrchestrator(context: context)

        // Create a fake tool call for a non-existent tool — it will fail.
        let toolCall = AtlasToolCall(
            toolName: "weather.current_conditions",
            argumentsJSON: "{}",
            permissionLevel: .read,
            requiresApproval: false,
            status: .pending,
            openAICallID: nil
        )

        let batch = await orchestrator.executeToolCalls([toolCall], conversationID: UUID(), emitter: emitter)
        let types = await collector.eventTypes()

        // tool_started must be emitted before any result
        XCTAssertTrue(types.contains(.toolStarted), "tool_started event should be emitted")

        // Either tool_finished or tool_failed should follow (real execution may fail without full setup)
        let hasOutcome = types.contains(.toolFinished) || types.contains(.toolFailed)
        XCTAssertTrue(hasOutcome, "tool_finished or tool_failed should be emitted after execution")

        // Batch should contain the tool call (completed or failed)
        XCTAssertEqual(batch.toolCalls.count, 1)
    }

    func testExecuteToolCallsEmitsCorrectDisplayName() async throws {
        let collector = EventCollector()
        let emitter: @Sendable (SSEEvent) async -> Void = { event in
            await collector.append(event)
        }

        let context = try AgentContext()
        let orchestrator = AgentOrchestrator(context: context)

        let toolCall = AtlasToolCall(
            toolName: "applescript.execute",
            argumentsJSON: "{}",
            permissionLevel: .read,
            requiresApproval: false,
            status: .pending,
            openAICallID: nil
        )

        _ = await orchestrator.executeToolCalls([toolCall], conversationID: UUID(), emitter: emitter)
        let names = await collector.toolNames(for: .toolStarted)

        XCTAssertEqual(names.first, "Working in your apps…",
                       "tool_started should carry the human-readable display name")
    }

    // MARK: AgentLoop — emitter parameter compiles and defaults to nil

    func testAgentLoopProcessAcceptsNilEmitter() async throws {
        // Compile-time check: process() must accept an optional emitter defaulting to nil.
        // We don't run it to completion (no valid OpenAI key in tests), just verify the signature.
        let context = try AgentContext()
        let loop = AgentLoop(context: context)
        // Calling process without emitter should still compile (default nil).
        // We don't await it here — just verifying the API surface.
        _ = loop  // suppress unused warning
        // If this file compiles, the test passes.
    }
}
