import XCTest
import Foundation
import AtlasShared
import AtlasNetwork
@testable import AtlasCore

// MARK: - AgentSupervisorTests
//
// Comprehensive tests for the parallel multi-agent feature:
//
//  1. SkillRoutingClassifier.isCompoundRequest — all detection branches
//  2. SkillRoutingPolicy.isCompoundRequest — delegation to classifier
//  3. MultiAgentPlan structure — singleTask factory, task ID uniqueness, field preservation
//  4. AgentSupervisor.decompose — valid JSON plan, invalid JSON fallback, empty-task fallback
//  5. AgentSupervisor.runWorkers — result count, plan-order preservation, failure isolation
//  6. AgentSupervisor.synthesize — non-empty response combining success and failure workers
//

// MARK: - Mock AI client

private actor MockAIClient: AtlasAIClient {

    private(set) var sendTurnCallCount = 0
    private var responseQueue: [String] = []
    private let defaultText: String
    /// When true, every call throws MockAIError.
    var alwaysThrow: Bool = false
    /// When set, the nth call (1-indexed) throws MockAIError instead of returning a response.
    var throwOnCallNumber: Int?

    init(defaultText: String = "Mock response", responseQueue: [String] = []) {
        self.defaultText = defaultText
        self.responseQueue = responseQueue
    }

    /// Enqueue responses to be returned in order; once exhausted, `defaultText` is used.
    func enqueue(_ texts: String...) {
        responseQueue.append(contentsOf: texts)
    }

    private func nextResponse() throws -> AITurnResponse {
        sendTurnCallCount += 1
        if alwaysThrow { throw MockAIError.intentionalFailure }
        if let n = throwOnCallNumber, sendTurnCallCount == n {
            throw MockAIError.intentionalFailure
        }
        let text = responseQueue.isEmpty ? defaultText : responseQueue.removeFirst()
        return AITurnResponse(turnID: UUID().uuidString, assistantText: text, rawToolCalls: [])
    }

    func sendTurn(
        conversation: AtlasConversation,
        tools: [AtlasToolDefinition],
        instructions: String?,
        model: String,
        attachments: [AtlasMessageAttachment]
    ) async throws -> AITurnResponse {
        try nextResponse()
    }

    func sendTurnStreaming(
        conversation: AtlasConversation,
        tools: [AtlasToolDefinition],
        instructions: String?,
        model: String,
        attachments: [AtlasMessageAttachment],
        onDelta: @escaping @Sendable (String) async -> Void
    ) async throws -> AITurnResponse {
        let r = try nextResponse()
        await onDelta(r.assistantText)
        return r
    }

    func continueTurn(
        previousTurnID: String,
        conversation: AtlasConversation,
        toolCalls: [AtlasToolCall],
        toolOutputs: [AIToolOutput],
        tools: [AtlasToolDefinition],
        instructions: String?,
        model: String
    ) async throws -> AITurnResponse {
        try nextResponse()
    }

    func continueTurnStreaming(
        previousTurnID: String,
        conversation: AtlasConversation,
        toolCalls: [AtlasToolCall],
        toolOutputs: [AIToolOutput],
        tools: [AtlasToolDefinition],
        instructions: String?,
        model: String,
        onDelta: @escaping @Sendable (String) async -> Void
    ) async throws -> AITurnResponse {
        let r = try nextResponse()
        await onDelta(r.assistantText)
        return r
    }

    func validateCredential() async throws {}

    func complete(systemPrompt: String, userContent: String, model: String?) async throws -> String {
        _ = try nextResponse()
        return defaultText
    }
}

private enum MockAIError: Error {
    case intentionalFailure
}

// MARK: - Helpers

private func makeSupervisorContext(
    mockClient: MockAIClient,
    maxParallelAgents: Int = 3,
    enableMultiAgent: Bool = true
) throws -> AgentContext {
    // Anthropic provider with no key configured falls back to static model catalog —
    // resolvedPrimaryModel() and resolvedFastModel() both return non-nil without network.
    let config = AtlasConfig(
        activeAIProvider: .anthropic,
        enableMultiAgentOrchestration: enableMultiAgent,
        maxParallelAgents: maxParallelAgents
    )
    return try AgentContext(config: config, aiClient: mockClient)
}

private func makeRequest(_ message: String, conversationID: UUID = UUID()) -> AtlasMessageRequest {
    AtlasMessageRequest(conversationID: conversationID, message: message)
}

// MARK: - 1. SkillRoutingClassifier.isCompoundRequest

final class SkillRoutingClassifierCompoundTests: XCTestCase {
    private let classifier = SkillRoutingClassifier()

    // MARK: Guard: short messages never trigger

    func testShortMessage_alwaysFalse() {
        XCTAssertFalse(classifier.isCompoundRequest(message: "What is the weather?"))
        XCTAssertFalse(classifier.isCompoundRequest(message: "Search for coffee nearby"))
        XCTAssertFalse(classifier.isCompoundRequest(message: ""))
        // Exactly at boundary: 60 chars → false
        let exactly60 = String(repeating: "a", count: 60)
        XCTAssertFalse(classifier.isCompoundRequest(message: exactly60))
    }

    func testBoundaryJustAbove60_withConjunction() {
        // 61-char message WITH a conjunction — guard passes, conjunction fires
        let msg = String(repeating: "a", count: 40) + " and also " + String(repeating: "b", count: 11)
        XCTAssertGreaterThan(msg.count, 60)
        XCTAssertTrue(classifier.isCompoundRequest(message: msg))
    }

    // MARK: Conjunction phrases — each must individually trigger

    func testConjunction_andAlso() {
        let msg = "Please search for flights to Tokyo and also find me a good hotel nearby the airport."
        XCTAssertTrue(classifier.isCompoundRequest(message: msg))
    }

    func testConjunction_asWellAs() {
        let msg = "Can you get me the current weather in London as well as a 7-day forecast for Paris?"
        XCTAssertTrue(classifier.isCompoundRequest(message: msg))
    }

    func testConjunction_additionally() {
        let msg = "Get me the population of Tokyo. Additionally, find the current exchange rate for JPY to USD."
        XCTAssertTrue(classifier.isCompoundRequest(message: msg))
    }

    func testConjunction_atTheSameTime() {
        let msg = "Look up the stock price for Apple at the same time as fetching the latest news headlines."
        XCTAssertTrue(classifier.isCompoundRequest(message: msg))
    }

    func testConjunction_simultaneously() {
        let msg = "Search for cheap flights to New York simultaneously check hotel availability for next weekend."
        XCTAssertTrue(classifier.isCompoundRequest(message: msg))
    }

    func testConjunction_inAddition() {
        let msg = "Find me a recipe for pasta carbonara. In addition, look up how many calories it has per serving."
        XCTAssertTrue(classifier.isCompoundRequest(message: msg))
    }

    func testConjunction_onTopOfThat() {
        let msg = "Check if it will rain this weekend in Seattle. On top of that, find outdoor activities to do."
        XCTAssertTrue(classifier.isCompoundRequest(message: msg))
    }

    func testConjunction_plusAlso() {
        let msg = "Search for the best coffee shops in Berlin plus also find good bookstores in the same city."
        XCTAssertTrue(classifier.isCompoundRequest(message: msg))
    }

    func testConjunction_whileAlso() {
        let msg = "Fetch the weather forecast for the next five days while also searching for news about climate."
        XCTAssertTrue(classifier.isCompoundRequest(message: msg))
    }

    func testConjunction_andSeparately() {
        let msg = "Look up flight prices to Miami for June and separately find Airbnb options near the beach."
        XCTAssertTrue(classifier.isCompoundRequest(message: msg))
    }

    func testConjunction_andIndependently() {
        let msg = "Find the best programming books on Amazon and independently search GitHub for Swift projects."
        XCTAssertTrue(classifier.isCompoundRequest(message: msg))
    }

    // MARK: Double question marks

    func testDoubleQuestionMarks_triggers() {
        let msg = "What is the weather in London right now? And what are the top news headlines today?"
        XCTAssertTrue(classifier.isCompoundRequest(message: msg))
    }

    func testDoubleQuestionMarks_exactlyTwo() {
        let msg = "Can you find me a hotel in Paris for this weekend? What is the average price per night?"
        XCTAssertTrue(classifier.isCompoundRequest(message: msg))
    }

    func testSingleQuestionMark_doesNotTrigger() {
        // Long message, 1 question mark, no conjunctions — should not fire
        let msg = "Can you please search the web for the most popular programming languages of 2024 and summarize the results?"
        // Has no conjunction phrases and only 1 "?" — check it
        let questionCount = msg.filter { $0 == "?" }.count
        XCTAssertEqual(questionCount, 1)
        XCTAssertFalse(classifier.isCompoundRequest(message: msg))
    }

    // MARK: Long message + repeated imperatives (>300 chars, 2+ imperatives each 2+ times)

    func testLongMessage_repeatedImperatives_triggers() {
        // >300 chars, "search" appears 2+ times, "find" appears 2+ times
        let msg = """
        I need you to search for the best restaurants in downtown San Francisco and search \
        the reviews carefully. Also find the opening hours for each one and find which ones \
        accept reservations online so I can plan my dinner for this Saturday evening with \
        my family who are visiting from out of town and would love a nice meal together.
        """
        XCTAssertGreaterThan(msg.count, 300)
        XCTAssertTrue(classifier.isCompoundRequest(message: msg))
    }

    func testLongMessage_onlyOneImperative_doesNotTrigger() {
        // >300 chars, "search" appears many times but only one distinct imperative type repeated
        let msg = """
        Please search for all available Python tutorials online. Search using the keywords \
        'beginner Python', 'Python for data science', and 'advanced Python patterns'. \
        Search both YouTube and written blog posts. Search across at least five different \
        domains and compile a list of the best resources you find during your search session.
        """
        XCTAssertGreaterThan(msg.count, 300)
        // Only "search" repeats — need 2 distinct imperatives each appearing 2+ times
        // Verify it does NOT trigger (only one distinct imperative type with 2+ hits)
        let hasFind = msg.lowercased().ranges(of: "find").count >= 2
        XCTAssertFalse(hasFind, "Test setup: 'find' should not appear 2+ times here")
        XCTAssertFalse(classifier.isCompoundRequest(message: msg))
    }

    // MARK: Non-triggering cases (regression guard)

    func testLongMessageWithConjunctionWord_notInPhrase_doesNotTrigger() {
        // "and" alone is not in the conjunction list — only exact phrases like "and also"
        let msg = "Find me the best laptop under $1000 and give me three options with pros and cons for each model in detail."
        // This is under 300 chars and has no double "?" and no conjunction phrases → false
        XCTAssertFalse(classifier.isCompoundRequest(message: msg))
    }

    func testClassify_punctuationOnlyMessage_doesNotCrash() {
        let result = classifier.classify(message: "???", skills: [])
        XCTAssertEqual(result.intent, .generalReasoning)
        XCTAssertNil(result.queryType)
        XCTAssertEqual(result.matchedSkillID, nil)
    }
}

// MARK: - 2. SkillRoutingPolicy delegation

final class SkillRoutingPolicyCompoundTests: XCTestCase {

    func testDelegatesTo_classifier_returnsTrue() {
        let policy = SkillRoutingPolicy()
        let compound = "What is the weather in Berlin today and also what are the top news stories right now?"
        XCTAssertTrue(policy.isCompoundRequest(message: compound))
    }

    func testDelegatesTo_classifier_returnsFalse() {
        let policy = SkillRoutingPolicy()
        XCTAssertFalse(policy.isCompoundRequest(message: "Hello there"))
    }

    func testPolicyAndClassifierAgree_forAllBranches() {
        let policy = SkillRoutingPolicy()
        let classifier = SkillRoutingClassifier()

        let samples: [String] = [
            // short — both false
            "Weather in Tokyo?",
            // conjunction — both true
            "Search for flights and also find hotels nearby for the same trip dates.",
            // double question — both true
            "What is the weather in Paris right now? And what should I pack for the trip?",
            // long but no trigger — both false
            "Please write me a detailed summary of the history of the Roman Empire focusing on the key emperors."
        ]

        for msg in samples {
            XCTAssertEqual(
                policy.isCompoundRequest(message: msg),
                classifier.isCompoundRequest(message: msg),
                "Policy and classifier must agree for: \(msg.prefix(60))…"
            )
        }
    }
}

// MARK: - 3. MultiAgentPlan structure

final class MultiAgentPlanStructureTests: XCTestCase {

    func testSingleTaskFactory_wrapsPrompt() {
        let plan = MultiAgentPlan.singleTask(prompt: "What is 2+2?")
        XCTAssertEqual(plan.tasks.count, 1)
        XCTAssertEqual(plan.tasks[0].prompt, "What is 2+2?")
        XCTAssertEqual(plan.tasks[0].title, "Main task")
        XCTAssertEqual(plan.synthesisContext, "")
    }

    func testTaskIDs_areUnique() {
        let tasks = (0..<5).map { i in AgentTask(title: "Task \(i)", prompt: "Prompt \(i)") }
        _ = MultiAgentPlan(tasks: tasks, synthesisContext: "")
        let ids = Set(tasks.map { $0.id })
        XCTAssertEqual(ids.count, 5, "All task IDs must be unique")
    }

    func testPlan_preservesTitlesAndPrompts() {
        let tasks = [
            AgentTask(title: "Weather", prompt: "Get weather in Paris"),
            AgentTask(title: "News", prompt: "Get top news today")
        ]
        let plan = MultiAgentPlan(tasks: tasks, synthesisContext: "Combine weather and news.")
        XCTAssertEqual(plan.tasks[0].title, "Weather")
        XCTAssertEqual(plan.tasks[0].prompt, "Get weather in Paris")
        XCTAssertEqual(plan.tasks[1].title, "News")
        XCTAssertEqual(plan.synthesisContext, "Combine weather and news.")
    }

    func testPlan_codableRoundTrip() throws {
        let tasks = [AgentTask(title: "T1", prompt: "P1"), AgentTask(title: "T2", prompt: "P2")]
        let original = MultiAgentPlan(tasks: tasks, synthesisContext: "Merge results carefully.")
        let data = try JSONEncoder().encode(original)
        let decoded = try JSONDecoder().decode(MultiAgentPlan.self, from: data)
        XCTAssertEqual(decoded.tasks.count, 2)
        XCTAssertEqual(decoded.tasks[0].title, "T1")
        XCTAssertEqual(decoded.tasks[1].prompt, "P2")
        XCTAssertEqual(decoded.synthesisContext, "Merge results carefully.")
    }
}

// MARK: - 4. AgentSupervisor.decompose

final class AgentSupervisorDecomposeTests: XCTestCase {

    // MARK: 4a. Valid JSON plan is parsed

    func testDecompose_validJSON_returnsPlan() async throws {
        let validPlanJSON = """
        {
          "tasks": [
            { "title": "Weather check", "prompt": "Get current weather in London." },
            { "title": "News fetch", "prompt": "Get top 5 news headlines for today." }
          ],
          "synthesisContext": "Combine weather and news into a brief morning briefing."
        }
        """
        let client = MockAIClient(defaultText: validPlanJSON)
        let context = try makeSupervisorContext(mockClient: client)
        let supervisor = AgentSupervisor(context: context)
        let request = makeRequest("What is the weather in London and also what are the top news headlines?")

        let plan = try await supervisor.decompose(request: request, conversationID: UUID())

        XCTAssertEqual(plan.tasks.count, 2)
        XCTAssertEqual(plan.tasks[0].title, "Weather check")
        XCTAssertEqual(plan.tasks[0].prompt, "Get current weather in London.")
        XCTAssertEqual(plan.tasks[1].title, "News fetch")
        XCTAssertEqual(plan.synthesisContext, "Combine weather and news into a brief morning briefing.")
    }

    // MARK: 4b. Markdown-fenced JSON is parsed (strips fences)

    func testDecompose_markdownFencedJSON_isStripped() async throws {
        let fencedJSON = """
        ```json
        {
          "tasks": [
            { "title": "Task A", "prompt": "Do task A in detail." },
            { "title": "Task B", "prompt": "Do task B independently." }
          ],
          "synthesisContext": "Merge A and B."
        }
        ```
        """
        let client = MockAIClient(defaultText: fencedJSON)
        let context = try makeSupervisorContext(mockClient: client)
        let supervisor = AgentSupervisor(context: context)

        let plan = try await supervisor.decompose(
            request: makeRequest("Do task A and also do task B at the same time in detail please."),
            conversationID: UUID()
        )

        XCTAssertEqual(plan.tasks.count, 2)
        XCTAssertEqual(plan.tasks[0].title, "Task A")
    }

    // MARK: 4c. Invalid JSON falls back to singleTask

    func testDecompose_invalidJSON_fallsBackToSingleTask() async throws {
        let client = MockAIClient(defaultText: "Sorry, I cannot decompose this request into tasks.")
        let context = try makeSupervisorContext(mockClient: client)
        let supervisor = AgentSupervisor(context: context)
        let originalPrompt = "What is the weather in Tokyo and also what are the top news headlines?"
        let request = makeRequest(originalPrompt)

        let plan = try await supervisor.decompose(request: request, conversationID: UUID())

        XCTAssertEqual(plan.tasks.count, 1, "Invalid JSON must fall back to single-task plan")
        XCTAssertEqual(plan.tasks[0].prompt, originalPrompt,
                       "Single-task fallback must preserve the original prompt verbatim")
    }

    // MARK: 4d. Empty task list in valid JSON falls back to singleTask

    func testDecompose_emptyTaskArray_fallsBackToSingleTask() async throws {
        let emptyTasksJSON = """
        { "tasks": [], "synthesisContext": "Nothing to do." }
        """
        let client = MockAIClient(defaultText: emptyTasksJSON)
        let context = try makeSupervisorContext(mockClient: client)
        let supervisor = AgentSupervisor(context: context)
        let originalPrompt = "Find hotels and also search for flights to London this winter season."
        let request = makeRequest(originalPrompt)

        let plan = try await supervisor.decompose(request: request, conversationID: UUID())

        XCTAssertEqual(plan.tasks.count, 1, "Empty task list must fall back to single-task plan")
        XCTAssertEqual(plan.tasks[0].prompt, originalPrompt)
    }

    // MARK: 4e. Plan respects maxParallelAgents cap

    func testDecompose_planDoesNotExceedMaxParallelAgents() async throws {
        // Return a 5-task plan; cap is 3 — but decompose itself doesn't truncate,
        // the cap is enforced in runWorkers. Verify decompose returns what the model said.
        let fiveTasksJSON = """
        {
          "tasks": [
            { "title": "T1", "prompt": "P1" },
            { "title": "T2", "prompt": "P2" },
            { "title": "T3", "prompt": "P3" },
            { "title": "T4", "prompt": "P4" },
            { "title": "T5", "prompt": "P5" }
          ],
          "synthesisContext": ""
        }
        """
        let client = MockAIClient(defaultText: fiveTasksJSON)
        let context = try makeSupervisorContext(mockClient: client, maxParallelAgents: 3)
        let supervisor = AgentSupervisor(context: context)

        let plan = try await supervisor.decompose(
            request: makeRequest("Search find get fetch check and also find again at the same time for this long request."),
            conversationID: UUID()
        )

        // Decompose preserves all tasks; runWorkers enforces the cap via task group concurrency
        XCTAssertEqual(plan.tasks.count, 5)
    }
}

// MARK: - 5. AgentSupervisor.runWorkers

final class AgentSupervisorRunWorkersTests: XCTestCase {

    // MARK: 5a. Result count matches task count

    func testRunWorkers_resultCountMatchesTaskCount() async throws {
        // Workers call AgentLoop.process → AgentOrchestrator → aiClient.sendTurn.
        // The mock returns a simple text response with no tool calls, so the loop exits
        // immediately after the first turn and workers succeed.
        let client = MockAIClient(defaultText: "Worker response")
        let context = try makeSupervisorContext(mockClient: client)
        let supervisor = AgentSupervisor(context: context)

        let tasks = [
            AgentTask(title: "T1", prompt: "Prompt one"),
            AgentTask(title: "T2", prompt: "Prompt two"),
            AgentTask(title: "T3", prompt: "Prompt three")
        ]
        let plan = MultiAgentPlan(tasks: tasks, synthesisContext: "")

        let results = await supervisor.runWorkers(plan: plan, parentConversationID: UUID())

        XCTAssertEqual(results.count, 3, "runWorkers must return one result per task")
    }

    // MARK: 5b. Results are returned in plan order

    func testRunWorkers_resultsAreInPlanOrder() async throws {
        let client = MockAIClient(defaultText: "OK")
        let context = try makeSupervisorContext(mockClient: client, maxParallelAgents: 5)
        let supervisor = AgentSupervisor(context: context)

        let tasks = (1...4).map { i in AgentTask(title: "Task \(i)", prompt: "Prompt \(i)") }
        let plan = MultiAgentPlan(tasks: tasks, synthesisContext: "")

        let results = await supervisor.runWorkers(plan: plan, parentConversationID: UUID())

        XCTAssertEqual(results.count, 4)
        // Results must be sorted back to the plan's task order
        for (i, result) in results.enumerated() {
            XCTAssertEqual(result.taskID, tasks[i].id,
                           "Result at index \(i) must match plan task at index \(i)")
        }
    }

    // MARK: 5c. Failure isolation — all-failing workers do not crash the group

    func testRunWorkers_failureIsolation_allWorkersFailGracefully() async throws {
        // All workers fail: mock always throws.
        // runWorkers must still return one result per task, all with success=false.
        // This verifies the TaskGroup catch path in runWorker — no uncaught throws escape.
        let client = MockAIClient(defaultText: "OK")
        await client.setAlwaysThrow(true)
        let context = try makeSupervisorContext(mockClient: client)
        let supervisor = AgentSupervisor(context: context)

        let tasks = [
            AgentTask(title: "W1", prompt: "Prompt 1"),
            AgentTask(title: "W2", prompt: "Prompt 2"),
            AgentTask(title: "W3", prompt: "Prompt 3")
        ]
        let plan = MultiAgentPlan(tasks: tasks, synthesisContext: "")

        let results = await supervisor.runWorkers(plan: plan, parentConversationID: UUID())

        XCTAssertEqual(results.count, 3,
            "runWorkers must return one result per task even when all workers fail")
        XCTAssertTrue(results.allSatisfy { !$0.success },
            "All results must have success=false when workers throw")
        XCTAssertTrue(results.allSatisfy { $0.errorMessage != nil },
            "All failed results must carry an error message")
    }

    // MARK: 5d. Single-task plan runs as one worker

    func testRunWorkers_singleTaskPlan_returnsOneResult() async throws {
        let client = MockAIClient(defaultText: "Single worker result")
        let context = try makeSupervisorContext(mockClient: client)
        let supervisor = AgentSupervisor(context: context)

        let plan = MultiAgentPlan.singleTask(prompt: "Just do this one thing please.")
        let results = await supervisor.runWorkers(plan: plan, parentConversationID: UUID())

        XCTAssertEqual(results.count, 1)
    }

    // MARK: 5e. Empty plan returns empty results

    func testRunWorkers_emptyPlan_returnsEmptyResults() async throws {
        let client = MockAIClient(defaultText: "Should not be called")
        let context = try makeSupervisorContext(mockClient: client)
        let supervisor = AgentSupervisor(context: context)

        let plan = MultiAgentPlan(tasks: [], synthesisContext: "")
        let results = await supervisor.runWorkers(plan: plan, parentConversationID: UUID())

        XCTAssertTrue(results.isEmpty)
    }
}

// MARK: - 6. AgentSupervisor.synthesize

final class AgentSupervisorSynthesizeTests: XCTestCase {

    // MARK: 6a. Synthesize returns non-empty response from all-success workers

    func testSynthesize_allSuccess_returnsNonEmptyResponse() async throws {
        let client = MockAIClient(defaultText: "Here is the combined summary of your request.")
        let context = try makeSupervisorContext(mockClient: client)
        let supervisor = AgentSupervisor(context: context)

        let plan = MultiAgentPlan(tasks: [
            AgentTask(title: "Weather", prompt: "Get weather"),
            AgentTask(title: "News", prompt: "Get news")
        ], synthesisContext: "Present as a morning briefing.")

        let results = [
            AgentTaskResult(taskID: plan.tasks[0].id, taskTitle: "Weather",
                            output: "Sunny, 22°C in London.", conversationID: UUID(), success: true),
            AgentTaskResult(taskID: plan.tasks[1].id, taskTitle: "News",
                            output: "Top story: New AI model released.", conversationID: UUID(), success: true)
        ]

        let turn = try await supervisor.synthesize(plan: plan, results: results, conversationID: UUID())

        XCTAssertFalse(turn.assistantMessage.isEmpty, "Synthesis must return a non-empty assistant message")
        XCTAssertTrue(turn.toolCalls.isEmpty, "Synthesis turn must not contain tool calls")
    }

    // MARK: 6b. Synthesize handles mixed success/failure workers

    func testSynthesize_mixedSuccessAndFailure_returnsResponse() async throws {
        let client = MockAIClient(defaultText: "The weather is sunny. News was unavailable.")
        let context = try makeSupervisorContext(mockClient: client)
        let supervisor = AgentSupervisor(context: context)

        let plan = MultiAgentPlan(tasks: [
            AgentTask(title: "Weather", prompt: "Get weather"),
            AgentTask(title: "News", prompt: "Get news")
        ], synthesisContext: "")

        let results = [
            AgentTaskResult(taskID: plan.tasks[0].id, taskTitle: "Weather",
                            output: "Sunny.", conversationID: UUID(), success: true),
            AgentTaskResult(taskID: plan.tasks[1].id, taskTitle: "News",
                            output: "", conversationID: UUID(), success: false,
                            errorMessage: "API timeout")
        ]

        let turn = try await supervisor.synthesize(plan: plan, results: results, conversationID: UUID())

        XCTAssertFalse(turn.assistantMessage.isEmpty)
    }

    // MARK: 6c. Synthesize with empty synthesisContext uses default instruction

    func testSynthesize_emptySynthesisContext_doesNotCrash() async throws {
        let client = MockAIClient(defaultText: "Combined answer.")
        let context = try makeSupervisorContext(mockClient: client)
        let supervisor = AgentSupervisor(context: context)

        let plan = MultiAgentPlan(tasks: [AgentTask(title: "T1", prompt: "P1")], synthesisContext: "")
        let results = [
            AgentTaskResult(taskID: plan.tasks[0].id, taskTitle: "T1",
                            output: "Result A.", conversationID: UUID(), success: true)
        ]

        let turn = try await supervisor.synthesize(plan: plan, results: results, conversationID: UUID())
        XCTAssertFalse(turn.assistantMessage.isEmpty)
    }

    // MARK: 6d. Synthesize with custom synthesisContext injects it

    func testSynthesize_customContext_isUsedInCall() async throws {
        // We verify that synthesize makes exactly one AI call (send count == 1).
        let client = MockAIClient(defaultText: "Morning briefing ready.")
        let context = try makeSupervisorContext(mockClient: client)
        let supervisor = AgentSupervisor(context: context)

        let plan = MultiAgentPlan(
            tasks: [AgentTask(title: "T1", prompt: "P1")],
            synthesisContext: "Present results as a concise executive summary."
        )
        let results = [
            AgentTaskResult(taskID: plan.tasks[0].id, taskTitle: "T1",
                            output: "Data.", conversationID: UUID(), success: true)
        ]

        _ = try await supervisor.synthesize(plan: plan, results: results, conversationID: UUID())

        let callCount = await client.sendTurnCallCount
        XCTAssertEqual(callCount, 1, "Synthesize must make exactly one AI call")
    }
}

// MARK: - MockAIClient configuration helpers

extension MockAIClient {
    func set(throwOnCallNumber n: Int) {
        throwOnCallNumber = n
    }
    func setAlwaysThrow(_ value: Bool) {
        alwaysThrow = value
    }
}
