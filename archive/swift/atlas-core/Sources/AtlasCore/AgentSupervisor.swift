import Foundation
import AtlasShared
import AtlasLogging
import AtlasNetwork
import AtlasSkills
import AtlasMemory

/// Decomposes compound user requests into parallel sub-tasks, runs them across a
/// bounded TaskGroup of worker `AgentLoop` instances, and synthesizes their outputs
/// into a single coherent assistant response.
///
/// Call order:
///   1. `decompose(request:conversationID:)` — fast-model LLM call → `MultiAgentPlan`
///   2. `runWorkers(plan:parentConversationID:emitter:)` — parallel workers → `[AgentTaskResult]`
///   3. `synthesize(plan:results:conversationID:emitter:)` — primary-model LLM call → `AgentModelTurn`
public struct AgentSupervisor: Sendable {
    private let context: AgentContext
    private let logger: AtlasLogger

    public init(context: AgentContext) {
        self.context = context
        self.logger = AtlasLogger(category: "supervisor")
    }

    // MARK: - Decompose

    /// Calls the fast model to break `request` into a `MultiAgentPlan`.
    /// Falls back to a single-task plan if the model response cannot be parsed.
    public func decompose(
        request: AtlasMessageRequest,
        conversationID: UUID
    ) async throws -> MultiAgentPlan {
        guard let fastModel = await context.modelSelector.resolvedFastModel() else {
            logger.warning("Fast model unavailable for decomposition — using single-task fallback")
            return .singleTask(prompt: request.message)
        }

        let systemPrompt = """
        You are a task planner. The user has sent a compound request that contains \
        multiple distinct goals. Break it into 2–\(min(context.config.maxParallelAgents, 5)) \
        independent sub-tasks that can run in parallel without needing each other's results.

        Respond ONLY with valid JSON, no markdown fences, in this exact shape:
        {
          "tasks": [
            { "title": "Short label", "prompt": "Full self-contained prompt for this sub-task" }
          ],
          "synthesisContext": "One sentence describing what the final synthesis should emphasise"
        }

        Rules:
        - Every prompt must be fully self-contained — the worker has no memory of the parent request.
        - Keep titles under 6 words.
        - Do not exceed \(min(context.config.maxParallelAgents, 5)) tasks.
        - If the request cannot be meaningfully parallelised, return exactly 1 task.
        """

        let planConversation = AtlasConversation(
            id: UUID(),
            messages: [AtlasMessage(role: .user, content: request.message)],
            createdAt: .now,
            updatedAt: .now
        )

        do {
            let response = try await context.aiClient.sendTurn(
                conversation: planConversation,
                tools: [],
                instructions: systemPrompt,
                model: fastModel,
                attachments: []
            )

            let plan = try parsePlan(from: response.assistantText, originalPrompt: request.message)
            logger.info(
                "Decomposed request into \(plan.tasks.count) task(s)",
                metadata: ["conversation_id": conversationID.uuidString]
            )
            return plan
        } catch {
            logger.warning(
                "Decomposition failed — falling back to single-task plan",
                metadata: ["error": error.localizedDescription]
            )
            return .singleTask(prompt: request.message)
        }
    }

    // MARK: - Run workers

    /// Executes each task in the plan as a scoped `AgentLoop` worker, capped at
    /// `config.maxParallelAgents` concurrent workers. Returns all results regardless
    /// of individual success/failure so the synthesis step always has something to work with.
    public func runWorkers(
        plan: MultiAgentPlan,
        parentConversationID: UUID,
        emitter: (@Sendable (SSEEvent) async -> Void)? = nil
    ) async -> [AgentTaskResult] {
        let cap = max(2, min(context.config.maxParallelAgents, 5))
        var results: [AgentTaskResult] = []

        await withTaskGroup(of: AgentTaskResult.self) { group in
            var launched = 0
            var taskIndex = 0

            // Seed the initial batch up to cap
            while taskIndex < plan.tasks.count && launched < cap {
                let task = plan.tasks[taskIndex]
                let workerContext = buildWorkerContext(base: context, allowedSkillIDs: task.allowedSkillIDs)
                group.addTask {
                    await self.runWorker(task: task, context: workerContext, emitter: emitter)
                }
                launched += 1
                taskIndex += 1
            }

            // Collect results, launching remaining tasks as slots free up
            for await result in group {
                results.append(result)
                if taskIndex < plan.tasks.count {
                    let task = plan.tasks[taskIndex]
                    let workerContext = buildWorkerContext(base: context, allowedSkillIDs: task.allowedSkillIDs)
                    group.addTask {
                        await self.runWorker(task: task, context: workerContext, emitter: emitter)
                    }
                    taskIndex += 1
                }
            }
        }

        // Restore original task order so synthesis sees them in plan order
        let taskOrder = Dictionary(uniqueKeysWithValues: plan.tasks.enumerated().map { ($0.element.id, $0.offset) })
        return results.sorted { (taskOrder[$0.taskID] ?? 0) < (taskOrder[$1.taskID] ?? 0) }
    }

    // MARK: - Synthesize

    /// Makes a primary-model LLM call that combines all worker outputs into a
    /// single coherent assistant response, streaming deltas via `emitter`.
    public func synthesize(
        plan: MultiAgentPlan,
        results: [AgentTaskResult],
        conversationID: UUID,
        emitter: (@Sendable (SSEEvent) async -> Void)? = nil
    ) async throws -> AgentModelTurn {
        guard let model = await context.modelSelector.resolvedPrimaryModel() else {
            throw AgentOrchestratorError.modelUnavailable
        }

        let resultSections = results.map { result -> String in
            if result.success {
                return "## \(result.taskTitle)\n\(result.output)"
            } else {
                let reason = result.errorMessage ?? "Unknown error"
                return "## \(result.taskTitle)\n[Task failed: \(reason)]"
            }
        }.joined(separator: "\n\n")

        let synthesisInstruction = plan.synthesisContext.isEmpty
            ? "Combine the following sub-task results into a single, coherent response for the user."
            : "Combine the following sub-task results into a single, coherent response. \(plan.synthesisContext)"

        let synthesisPrompt = """
        \(synthesisInstruction)

        Present the information clearly and naturally — do not reference the internal task structure \
        or mention that multiple agents were used. Write as if you gathered all this yourself.

        \(resultSections)
        """

        let synthesisConversation = AtlasConversation(
            id: conversationID,
            messages: [AtlasMessage(role: .user, content: synthesisPrompt)],
            createdAt: .now,
            updatedAt: .now
        )

        await emitter?(.assistantStarted())

        let response: AITurnResponse
        if let emitter {
            let onDelta: @Sendable (String) async -> Void = { delta in
                await emitter(.assistantDelta(delta))
            }
            response = try await context.aiClient.sendTurnStreaming(
                conversation: synthesisConversation,
                tools: [],
                instructions: nil,
                model: model,
                attachments: [],
                onDelta: onDelta
            )
        } else {
            response = try await context.aiClient.sendTurn(
                conversation: synthesisConversation,
                tools: [],
                instructions: nil,
                model: model,
                attachments: []
            )
        }

        await emitter?(.assistantDone())

        logger.info(
            "Synthesis complete",
            metadata: [
                "conversation_id": conversationID.uuidString,
                "response_id": response.turnID
            ]
        )

        return AgentModelTurn(
            responseID: response.turnID,
            assistantMessage: response.assistantText,
            toolCalls: []
        )
    }

    // MARK: - Private helpers

    private func runWorker(
        task: AgentTask,
        context: AgentContext,
        emitter: (@Sendable (SSEEvent) async -> Void)?
    ) async -> AgentTaskResult {
        let workerConversationID = UUID()
        let label = task.title

        // Prefix all SSE tool events with the worker label so the UI shows
        // distinct status lines per worker (e.g. "Weather check: Checking the weather…")
        let workerEmitter: (@Sendable (SSEEvent) async -> Void)? = emitter.map { emit in
            { @Sendable event in
                switch event.type {
                case .toolStarted:
                    let prefixed = SSEEvent.toolStarted(name: "\(label): \(event.toolName ?? "")")
                    await emit(prefixed)
                case .toolFinished:
                    let prefixed = SSEEvent.toolFinished(name: "\(label): \(event.toolName ?? "")")
                    await emit(prefixed)
                case .toolFailed:
                    let prefixed = SSEEvent.toolFailed(name: "\(label): \(event.toolName ?? "")")
                    await emit(prefixed)
                default:
                    break   // Workers don't stream text — only the synthesis turn does
                }
            }
        }

        let workerRequest = AtlasMessageRequest(
            conversationID: workerConversationID,
            message: task.prompt
        )

        do {
            let envelope = try await AgentLoop(context: context).process(workerRequest, emitter: workerEmitter)
            return AgentTaskResult(
                taskID: task.id,
                taskTitle: task.title,
                output: envelope.response.assistantMessage,
                conversationID: workerConversationID,
                success: envelope.response.status == .completed,
                errorMessage: envelope.response.errorMessage
            )
        } catch {
            logger.error(
                "Worker failed",
                metadata: ["task": task.title, "error": error.localizedDescription]
            )
            return AgentTaskResult(
                taskID: task.id,
                taskTitle: task.title,
                output: "",
                conversationID: workerConversationID,
                success: false,
                errorMessage: error.localizedDescription
            )
        }
    }

    /// Builds a worker `AgentContext` sharing all of the parent's stores and services,
    /// but with `enableMultiAgentOrchestration` forced to false (workers never recurse)
    /// and `maxAgentIterations` capped to `config.workerMaxIterations`.
    ///
    /// Note: Skill scoping via `allowedSkillIDs` is reserved for future implementation.
    /// Workers currently have access to all skills the parent does.
    private func buildWorkerContext(
        base: AgentContext,
        allowedSkillIDs: [String]?   // reserved — not yet applied
    ) -> AgentContext {
        // Build a minimal config overlay: only override the fields that need to differ.
        // Everything else (API keys, provider, models) stays identical to the parent.
        let workerConfig = AtlasConfig(
            runtimePort: base.config.runtimePort,
            telegramEnabled: base.config.telegramEnabled,
            discordEnabled: base.config.discordEnabled,
            discordClientID: base.config.discordClientID,
            slackEnabled: base.config.slackEnabled,
            defaultOpenAIModel: base.config.defaultOpenAIModel,
            baseSystemPrompt: base.config.baseSystemPrompt,
            maxAgentIterations: base.config.workerMaxIterations,
            conversationWindowLimit: base.config.conversationWindowLimit,
            memoryEnabled: base.config.memoryEnabled,
            maxRetrievedMemoriesPerTurn: base.config.maxRetrievedMemoriesPerTurn,
            personaName: base.config.personaName,
            actionSafetyMode: base.config.actionSafetyMode,
            activeAIProvider: base.config.activeAIProvider,
            lmStudioBaseURL: base.config.lmStudioBaseURL,
            selectedAnthropicModel: base.config.selectedAnthropicModel,
            selectedGeminiModel: base.config.selectedGeminiModel,
            selectedOpenAIPrimaryModel: base.config.selectedOpenAIPrimaryModel,
            selectedOpenAIFastModel: base.config.selectedOpenAIFastModel,
            selectedAnthropicFastModel: base.config.selectedAnthropicFastModel,
            selectedGeminiFastModel: base.config.selectedGeminiFastModel,
            selectedLMStudioModel: base.config.selectedLMStudioModel,
            enableSmartToolSelection: base.config.enableSmartToolSelection,
            enableMultiAgentOrchestration: false   // workers never recurse into multi-agent
        )

        do {
            return try AgentContext(
                config: workerConfig,
                logger: base.logger,
                modelSelector: base.modelSelector,
                aiClient: base.aiClient,
                telegramClient: base.telegramClient,
                memoryStore: base.memoryStore,
                permissionManager: base.permissionManager,
                approvalManager: base.approvalManager,
                toolRegistry: base.toolRegistry,
                toolExecutor: base.toolExecutor,
                skillRegistry: base.skillRegistry,
                skillPolicyEngine: base.skillPolicyEngine,
                skillAuditStore: base.skillAuditStore,
                skillExecutionGateway: base.skillExecutionGateway,
                actionPolicyStore: base.actionPolicyStore,
                fileAccessScopeStore: base.fileAccessScopeStore,
                personaEngine: base.personaEngine,
                personaPromptAssembler: base.personaPromptAssembler,
                skillRoutingPolicy: base.skillRoutingPolicy,
                mindEngine: base.mindEngine,
                skillsEngine: base.skillsEngine
            )
        } catch {
            // AgentContext.init only throws if MemoryStore.init fails, which won't happen
            // here because we inject the already-open store. Log and fall back to the
            // parent context so the worker still runs.
            logger.error("Failed to build worker context — falling back to parent", metadata: ["error": error.localizedDescription])
            return base
        }
    }

    // MARK: - JSON parsing

    private func parsePlan(from text: String, originalPrompt: String) throws -> MultiAgentPlan {
        // Strip any accidental markdown fences
        var cleaned = text
            .trimmingCharacters(in: .whitespacesAndNewlines)
        if cleaned.hasPrefix("```") {
            cleaned = cleaned
                .components(separatedBy: "\n")
                .dropFirst()   // drop ```json line
                .joined(separator: "\n")
            if cleaned.hasSuffix("```") {
                cleaned = String(cleaned.dropLast(3))
            }
        }
        cleaned = cleaned.trimmingCharacters(in: .whitespacesAndNewlines)

        guard let data = cleaned.data(using: .utf8) else {
            throw PlanParseError.invalidUTF8
        }

        struct RawTask: Decodable {
            let title: String
            let prompt: String
        }
        struct RawPlan: Decodable {
            let tasks: [RawTask]
            let synthesisContext: String?
        }

        let raw = try JSONDecoder().decode(RawPlan.self, from: data)
        guard !raw.tasks.isEmpty else {
            throw PlanParseError.emptyTaskList
        }

        let tasks = raw.tasks.map { AgentTask(title: $0.title, prompt: $0.prompt) }
        return MultiAgentPlan(tasks: tasks, synthesisContext: raw.synthesisContext ?? "")
    }

    private enum PlanParseError: LocalizedError {
        case invalidUTF8
        case emptyTaskList

        var errorDescription: String? {
            switch self {
            case .invalidUTF8: return "Plan JSON contained invalid UTF-8."
            case .emptyTaskList: return "Plan contained no tasks."
            }
        }
    }
}
