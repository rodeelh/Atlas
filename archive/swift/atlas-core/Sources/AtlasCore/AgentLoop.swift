import Foundation
import AtlasMemory
import AtlasShared

public struct AgentLoop: Sendable {
    private let context: AgentContext
    private let orchestrator: AgentOrchestrator

    public init(context: AgentContext) {
        self.context = context
        self.orchestrator = AgentOrchestrator(context: context)
    }

    public func process(
        _ request: AtlasMessageRequest,
        emitter: (@Sendable (SSEEvent) async -> Void)? = nil
    ) async throws -> AtlasMessageResponseEnvelope {
        let conversationID = request.conversationID ?? UUID()
        let existingConversation = try await context.conversationStore.fetchConversation(id: conversationID)
        let baseConversation: AtlasConversation

        if let existingConversation {
            baseConversation = existingConversation
        } else {
            baseConversation = try await context.conversationStore.createConversation(id: conversationID)
        }

        let userMessage = AtlasMessage(role: .user, content: request.message)
        _ = try await context.conversationStore.appendMessage(userMessage, to: baseConversation.id)

        context.logger.info("Received runtime message", metadata: [
            "conversation_id": baseConversation.id.uuidString,
            "characters": "\(request.message.count)"
        ])

        // Multi-agent gate
        // If multi-agent orchestration is enabled and the routing classifier identifies
        // the message as a compound request, delegate to AgentSupervisor and return early.
        // The normal single-agent path below is completely unaffected when the gate does not fire.
        if context.config.enableMultiAgentOrchestration,
           SkillRoutingClassifier().isCompoundRequest(message: request.message) {
            context.logger.info("Multi-agent: compound request detected — delegating to AgentSupervisor",
                                metadata: ["conversation_id": baseConversation.id.uuidString])
            let supervisor = AgentSupervisor(context: context)
            let plan = try await supervisor.decompose(request: request, conversationID: baseConversation.id)
            let workerResults = await supervisor.runWorkers(
                plan: plan,
                parentConversationID: baseConversation.id,
                emitter: emitter
            )
            let synthTurn = try await supervisor.synthesize(
                plan: plan,
                results: workerResults,
                conversationID: baseConversation.id,
                emitter: emitter
            )
            let assistantMessage = synthTurn.assistantMessage.trimmingCharacters(in: .whitespacesAndNewlines)
            let finalMessage = assistantMessage.isEmpty ? "Done." : assistantMessage
            return try await finalizeTurn(
                conversationID: baseConversation.id,
                userMessage: userMessage,
                assistantMessage: finalMessage,
                status: .completed,
                toolCalls: [],
                toolResults: []
            )
        }

        // Normal single-agent path
        var toolCalls: [AtlasToolCall] = []
        var toolResults: [AtlasToolResult] = []
        var currentTurn = try await orchestrator.requestModelTurn(
            forConversationID: baseConversation.id,
            attachments: request.attachments,
            emitter: emitter
        )
        var iteration = 0

        while true {
            if currentTurn.toolCalls.isEmpty {
                // Model is done. If it produced no text (tool-heavy turn that ended silently),
                // synthesize and stream a brief completion placeholder so the bubble is never empty.
                let assistantMessage = try await streamFallbackIfNeeded(
                    modelText: currentTurn.assistantMessage,
                    fallback: "Done.",
                    emitter: emitter
                )
                return try await finalizeTurn(
                    conversationID: baseConversation.id,
                    userMessage: userMessage,
                    assistantMessage: assistantMessage,
                    status: .completed,
                    toolCalls: toolCalls,
                    toolResults: toolResults
                )
            }

            let execution = await orchestrator.executeToolCalls(
                currentTurn.toolCalls,
                conversationID: baseConversation.id,
                emitter: emitter
            )
            toolCalls.append(contentsOf: execution.toolCalls)
            toolResults.append(contentsOf: execution.results)

            if !execution.pendingApprovals.isEmpty {
                let assistantMessage = approvalMessage(
                    base: currentTurn.assistantMessage,
                    count: execution.pendingApprovals.count
                )
                return try await finalizeTurn(
                    conversationID: baseConversation.id,
                    userMessage: userMessage,
                    assistantMessage: assistantMessage,
                    status: .waitingForApproval,
                    toolCalls: toolCalls,
                    toolResults: toolResults,
                    pendingApprovals: execution.pendingApprovals
                )
            }

            // PATH B failure: a tool execution returned success: false.
            // The model produced no text (only tool calls), so nothing was streamed.
            // Synthesize a human-readable message and emit it before finalizing.
            if let failedResult = execution.results.first(where: { !$0.success }) {
                let fallback = Self.synthesizeFallback(for: failedResult)
                let assistantMessage = try await streamFallbackIfNeeded(
                    modelText: currentTurn.assistantMessage,
                    fallback: fallback,
                    emitter: emitter
                )
                return try await finalizeTurn(
                    conversationID: baseConversation.id,
                    userMessage: userMessage,
                    assistantMessage: assistantMessage,
                    status: .failed,
                    toolCalls: toolCalls,
                    toolResults: toolResults,
                    errorMessage: failedResult.errorMessage
                )
            }

            _ = try await orchestrator.appendToolResultMessages(
                execution.results,
                toolCalls: execution.toolCalls,
                to: baseConversation.id
            )

            iteration += 1
            guard iteration < context.config.effectiveMaxAgentIterations else {
                let message = "I've been working on this for a while and need to pause here. Let me know how you'd like to continue."
                await emitter?(.assistantDelta(message))
                await emitter?(.assistantDone())
                return try await finalizeTurn(
                    conversationID: baseConversation.id,
                    userMessage: userMessage,
                    assistantMessage: message,
                    status: .failed,
                    toolCalls: toolCalls,
                    toolResults: toolResults,
                    errorMessage: message
                )
            }

            currentTurn = try await orchestrator.continueModelTurn(
                forConversationID: baseConversation.id,
                previousResponseID: currentTurn.responseID,
                toolCalls: execution.toolCalls,
                toolResults: execution.results,
                emitter: emitter
            )
        }
    }

    // MARK: - Fallback message synthesis

    /// If the model produced text, return it as-is (it was already streamed).
    /// If the model was silent (tool-only turn), emit `fallback` as a delta so
    /// the frontend bubble is never left empty, then return the fallback text.
    private func streamFallbackIfNeeded(
        modelText: String,
        fallback: String,
        emitter: (@Sendable (SSEEvent) async -> Void)?
    ) async throws -> String {
        let trimmed = modelText.trimmingCharacters(in: .whitespacesAndNewlines)
        if !trimmed.isEmpty { return trimmed }
        // Model was silent — stream the fallback so the bubble gets populated.
        await emitter?(.assistantDelta(fallback))
        await emitter?(.assistantDone())
        return fallback
    }

    /// Synthesizes a short, human-readable fallback from a failed tool result.
    /// Inspects the error message for known patterns and returns a contextual phrase.
    /// Never exposes raw tool IDs, HTTP codes, stack traces, or internal error text.
    static func synthesizeFallback(for failedResult: AtlasToolResult) -> String {
        let reason = (failedResult.errorMessage ?? "").lowercased()

        if reason.contains("denied") || reason.contains("declined") {
            return "That action was declined."
        }
        if reason.contains("401") || reason.contains("403")
            || reason.contains("auth") || reason.contains("credential")
            || reason.contains("key") || reason.contains("token") {
            return "I'm missing the credentials needed for this. Once those are configured, I can try again."
        }
        if reason.contains("timeout") || reason.contains("timed out")
            || reason.contains("network") || reason.contains("connection") {
            return "The connection timed out. Try again in a moment."
        }
        if reason.contains("api") || reason.contains("endpoint")
            || reason.contains("http") || reason.contains("validation")
            || reason.contains("url") {
            return "That didn't come together cleanly. I need to re-check the API before I can build it."
        }
        if reason.contains("forge") || reason.contains("skill")
            || reason.contains("proposal") || reason.contains("plan") {
            return "I couldn't finish building that. Let me know if you'd like to try with different details."
        }
        if reason.contains("permission") || reason.contains("approval") {
            return "This needs approval before I can continue. Check the Approvals screen to review it."
        }
        return "I ran into an issue completing that. Let me know if you'd like to try again."
    }

    private func normalizedAssistantMessage(_ message: String, fallback: String) -> String {
        let trimmed = message.trimmingCharacters(in: .whitespacesAndNewlines)
        return trimmed.isEmpty ? fallback : trimmed
    }

    private func approvalMessage(base: String, count: Int) -> String {
        let trimmed = base.trimmingCharacters(in: .whitespacesAndNewlines)
        let suffix = count == 1 ? "1 tool approval is pending." : "\(count) tool approvals are pending."
        return trimmed.isEmpty ? "Atlas requires approval before it can continue. \(suffix)" : "\(trimmed)\n\n\(suffix)"
    }

    private func finalizeTurn(
        conversationID: UUID,
        userMessage: AtlasMessage,
        assistantMessage: String,
        status: AtlasAgentResponseStatus,
        toolCalls: [AtlasToolCall],
        toolResults: [AtlasToolResult],
        pendingApprovals: [ApprovalRequest] = [],
        errorMessage: String? = nil
    ) async throws -> AtlasMessageResponseEnvelope {
        let assistantRecord = AtlasMessage(role: .assistant, content: assistantMessage)
        let conversation = try await context.conversationStore.appendMessage(assistantRecord, to: conversationID)

        // Fire MIND.md + SKILLS.md reflection asynchronously — does not block the response
        let mindTurn = MindTurnRecord(
            conversationID: conversationID,
            userMessage: userMessage.content,
            assistantResponse: assistantMessage,
            toolCallSummaries: toolCalls.map { $0.toolName },
            toolResultSummaries: toolResults.map(\.output),
            timestamp: .now
        )
        await context.mindEngine.reflect(turn: mindTurn)
        await context.skillsEngine.reflectAfterTurn(mindTurn)

        return AtlasMessageResponseEnvelope(
            conversation: conversation,
            response: AtlasAgentResponse(
                assistantMessage: assistantMessage,
                toolCalls: toolCalls,
                status: status,
                toolResults: toolResults,
                pendingApprovals: pendingApprovals,
                errorMessage: errorMessage
            )
        )
    }

}
