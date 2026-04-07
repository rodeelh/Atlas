import Foundation
import AtlasNetwork
import AtlasSkills
import AtlasShared
import AtlasTools

public enum AgentOrchestratorError: LocalizedError {
    case missingOpenAICallID(UUID)
    case modelUnavailable

    public var errorDescription: String? {
        switch self {
        case .missingOpenAICallID(let id):
            return "No call ID was returned for tool call \(id.uuidString)."
        case .modelUnavailable:
            return "No model available — the model selector could not resolve a model. Check that your API key is valid and the daemon can reach the AI provider."
        }
    }
}

public struct AgentModelTurn: Sendable {
    public let responseID: String
    public let assistantMessage: String
    public let toolCalls: [AtlasToolCall]
}

public struct ToolExecutionBatch: Sendable {
    public let toolCalls: [AtlasToolCall]
    public let results: [AtlasToolResult]
    public let pendingApprovals: [ApprovalRequest]
}

public struct AgentOrchestrator: Sendable {
    private let context: AgentContext

    private struct ToolPresentationBundle {
        let toolDefinitions: [AtlasToolDefinition]
        let routingDecision: SkillRoutingDecision
    }

    public init(context: AgentContext) {
        self.context = context
    }

    /// Maps a tool name to a calm, human-readable status phrase for display in the UI.
    /// Rules: max ~3–4 words, no technical jargon, no emojis, never expose raw tool IDs.
    public static func humanReadableName(for toolName: String) -> String {
        if toolName.hasPrefix("weather.") { return "Checking the weather…" }
        if toolName.hasPrefix("web.search") { return "Searching the web…" }
        if toolName.hasPrefix("web.") { return "Looking this up…" }
        if toolName.hasPrefix("file.") { return "Reading files…" }
        if toolName.hasPrefix("forge.orchestration.propose") { return "Drafting a new skill…" }
        if toolName.hasPrefix("forge.orchestration.plan") { return "Planning this out…" }
        if toolName.hasPrefix("forge.orchestration.review") { return "Reviewing the plan…" }
        if toolName.hasPrefix("forge.orchestration.validate") { return "Verifying the details…" }
        if toolName.hasPrefix("forge.") { return "Building that for you…" }
        if toolName.hasPrefix("dashboard.") { return "Updating your dashboard…" }
        if toolName.hasPrefix("system.") { return "Running that now…" }
        if toolName.hasPrefix("applescript.") { return "Working in your apps…" }
        if toolName.hasPrefix("gremlins.") { return "Managing automations…" }
        if toolName.hasPrefix("image.") { return "Generating an image…" }
        if toolName.hasPrefix("vision.") { return "Analyzing the image…" }
        if toolName.hasPrefix("atlas.") { return "Checking Atlas…" }
        if toolName.hasPrefix("info.") { return "Checking that…" }
        return "Working on it…"   // Never expose a raw tool ID to the UI
    }

    public func requestModelTurn(
        forConversationID conversationID: UUID,
        attachments: [AtlasMessageAttachment] = [],
        emitter: (@Sendable (SSEEvent) async -> Void)? = nil
    ) async throws -> AgentModelTurn {
        guard let conversation = try await context.conversationStore.fetchConversation(id: conversationID) else {
            return AgentModelTurn(responseID: UUID().uuidString, assistantMessage: "", toolCalls: [])
        }

        let (permissionLevels, approvalRequirements) = await toolPolicyMaps()
        let presentation = await buildToolPresentationBundle(for: conversation)
        let instructions = try await buildInstructions(
            for: conversation,
            routingDecision: presentation.routingDecision
        )

        guard let model = await context.modelSelector.resolvedPrimaryModel() else {
            throw AgentOrchestratorError.modelUnavailable
        }

        context.logger.info("AI request started", metadata: [
            "conversation_id": conversationID.uuidString,
            "messages": "\(conversation.messages.count)",
            "model": model,
            "provider": context.config.activeAIProvider.rawValue,
            "routing_intent": presentation.routingDecision.intent.rawValue,
            "routing_query_type": presentation.routingDecision.queryType?.rawValue ?? "none",
            "attachments": "\(attachments.count)",
            "streaming": emitter != nil ? "true" : "false"
        ])

        await emitter?(.assistantStarted())

        // When image attachments are present the image is already embedded inline in the
        // conversation input. Exposing VisionSkill alongside it causes the model to call
        // vision.analyse_image with a file_path it doesn't have, producing a missingInput
        // error. Strip vision tools so the model responds directly from the inline image.
        let hasInlineImages = attachments.contains(where: { $0.isImage })
        let effectiveTools = hasInlineImages
            ? presentation.toolDefinitions.filter { !$0.name.hasPrefix("vision.") }
            : presentation.toolDefinitions

        let response: AITurnResponse
        if let emitter {
            let onDelta: @Sendable (String) async -> Void = { delta in
                await emitter(.assistantDelta(delta))
            }
            response = try await context.aiClient.sendTurnStreaming(
                conversation: conversation,
                tools: effectiveTools,
                instructions: instructions,
                model: model,
                attachments: attachments,
                onDelta: onDelta
            )
        } else {
            response = try await context.aiClient.sendTurn(
                conversation: conversation,
                tools: effectiveTools,
                instructions: instructions,
                model: model,
                attachments: attachments
            )
        }

        await emitter?(.assistantDone())

        // Guardrail: if smart selection was active (non-Tier-3) and the model called
        // a tool that wasn't in the sent set, retry once with the full tool list.
        // Only applied to non-streaming turns to avoid disrupting live SSE streams.
        let finalResponse: AITurnResponse
        if emitter == nil && context.config.enableSmartToolSelection && presentation.routingDecision.intent != .generalReasoning {
            let sentToolNames = Set(presentation.toolDefinitions.map { $0.name })
            let unknownCalls = response.rawToolCalls.filter { !sentToolNames.contains($0.name) }
            if !unknownCalls.isEmpty {
                context.logger.warning(
                    "Smart tool selection: model called \(unknownCalls.count) tool(s) not in sent set — retrying with full list",
                    metadata: ["unknown_tools": unknownCalls.map { $0.name }.joined(separator: ",")]
                )
                let fullPresentation = await buildToolPresentationBundle(for: conversation, forceFull: true)
                let retryTools = hasInlineImages
                    ? fullPresentation.toolDefinitions.filter { !$0.name.hasPrefix("vision.") }
                    : fullPresentation.toolDefinitions
                finalResponse = try await context.aiClient.sendTurn(
                    conversation: conversation,
                    tools: retryTools,
                    instructions: instructions,
                    model: model,
                    attachments: attachments
                )
            } else {
                finalResponse = response
            }
        } else {
            finalResponse = response
        }

        let toolCalls = finalResponse.rawToolCalls.map { raw in
            AtlasToolCall(
                toolName: raw.name,
                argumentsJSON: raw.argumentsJSON,
                permissionLevel: permissionLevels[raw.name] ?? .read,
                requiresApproval: approvalRequirements[raw.name] ?? false,
                status: .pending,
                openAICallID: raw.callID
            )
        }

        for toolCall in toolCalls {
            await persistToolCall(toolCall, conversationID: conversationID)
            context.logger.info("Tool call detected", metadata: [
                "conversation_id": conversationID.uuidString,
                "tool_call_id": toolCall.id.uuidString,
                "tool": toolCall.toolName
            ])
        }

        context.logger.info("AI request completed", metadata: [
            "conversation_id": conversationID.uuidString,
            "response_id": finalResponse.turnID,
            "tool_calls": "\(toolCalls.count)"
        ])

        return AgentModelTurn(
            responseID: finalResponse.turnID,
            assistantMessage: finalResponse.assistantText,
            toolCalls: toolCalls
        )
    }

    public func continueModelTurn(
        forConversationID conversationID: UUID,
        previousResponseID: String,
        toolCalls: [AtlasToolCall],
        toolResults: [AtlasToolResult],
        emitter: (@Sendable (SSEEvent) async -> Void)? = nil
    ) async throws -> AgentModelTurn {
        let (permissionLevels, approvalRequirements) = await toolPolicyMaps()
        let presentation = try await continuationToolPresentationBundle(forConversationID: conversationID)
        let instructions = try await continuationInstructions(
            forConversationID: conversationID,
            routingDecision: presentation.routingDecision
        )

        let outputs = try toolResults.compactMap { result -> AIToolOutput? in
            guard
                let toolCall = toolCalls.first(where: { $0.id == result.toolCallID }),
                let callID = toolCall.openAICallID
            else {
                // Only throw for successful results with missing IDs — failed results can be dropped
                guard result.success else { return nil }
                throw AgentOrchestratorError.missingOpenAICallID(result.toolCallID)
            }

            if result.success {
                return AIToolOutput(callID: callID, output: result.output)
            } else {
                let errorText = result.errorMessage ?? result.output
                return AIToolOutput(callID: callID, output: "Error: \(errorText)")
            }
        }

        guard let model = await context.modelSelector.resolvedPrimaryModel() else {
            throw AgentOrchestratorError.modelUnavailable
        }

        // For stateless providers we need the full conversation to reconstruct the message history.
        let conversation = try await context.conversationStore.fetchConversation(id: conversationID)

        context.logger.info("AI continuation started", metadata: [
            "previous_response_id": previousResponseID,
            "model": model,
            "provider": context.config.activeAIProvider.rawValue,
            "tool_outputs": "\(outputs.count)",
            "streaming": emitter != nil ? "true" : "false"
        ])

        await emitter?(.assistantStarted())

        let response: AITurnResponse
        if let emitter {
            let onDelta: @Sendable (String) async -> Void = { delta in
                await emitter(.assistantDelta(delta))
            }
            response = try await context.aiClient.continueTurnStreaming(
                previousTurnID: previousResponseID,
                conversation: conversation ?? AtlasConversation(id: conversationID),
                toolCalls: toolCalls,
                toolOutputs: outputs,
                tools: presentation.toolDefinitions,
                instructions: instructions,
                model: model,
                onDelta: onDelta
            )
        } else {
            response = try await context.aiClient.continueTurn(
                previousTurnID: previousResponseID,
                conversation: conversation ?? AtlasConversation(id: conversationID),
                toolCalls: toolCalls,
                toolOutputs: outputs,
                tools: presentation.toolDefinitions,
                instructions: instructions,
                model: model
            )
        }

        await emitter?(.assistantDone())

        let nextToolCalls = response.rawToolCalls.map { raw in
            AtlasToolCall(
                toolName: raw.name,
                argumentsJSON: raw.argumentsJSON,
                permissionLevel: permissionLevels[raw.name] ?? .read,
                requiresApproval: approvalRequirements[raw.name] ?? false,
                status: .pending,
                openAICallID: raw.callID
            )
        }

        context.logger.info("AI continuation completed", metadata: [
            "response_id": response.turnID,
            "tool_calls": "\(nextToolCalls.count)"
        ])

        return AgentModelTurn(
            responseID: response.turnID,
            assistantMessage: response.assistantText,
            toolCalls: nextToolCalls
        )
    }

    public func executeToolCalls(
        _ toolCalls: [AtlasToolCall],
        conversationID: UUID,
        emitter: (@Sendable (SSEEvent) async -> Void)? = nil
    ) async -> ToolExecutionBatch {
        var executedCalls: [AtlasToolCall] = []
        var results: [AtlasToolResult] = []
        var pendingApprovals: [ApprovalRequest] = []

        for toolCall in toolCalls {
            let displayName = AgentOrchestrator.humanReadableName(for: toolCall.toolName)
            await emitter?(.toolStarted(name: displayName))

            do {
                let result = try await context.toolExecutor.execute(toolCall: toolCall, conversationID: conversationID)
                let completedCall = toolCall.updatingStatus(.completed)
                executedCalls.append(completedCall)
                results.append(result)
                await persistToolResult(result, conversationID: conversationID)
                await emitter?(.toolFinished(name: displayName))
            } catch let error as ToolExecutionError {
                switch error {
                case .approvalRequired(let request):
                    let pendingCall = toolCall.updatingStatus(.pending)
                    executedCalls.append(pendingCall)
                    pendingApprovals.append(request)
                    await persistApproval(request, conversationID: conversationID)
                    // Not a failure — approval is a normal gate, no toolFailed event.
                    context.logger.info("Tool execution awaiting approval", metadata: [
                        "tool_call_id": toolCall.id.uuidString,
                        "tool": toolCall.toolName
                    ])
                case .denied(let message):
                    let deniedCall = toolCall.updatingStatus(.denied)
                    let failedResult = AtlasToolResult(
                        toolCallID: toolCall.id,
                        output: "",
                        success: false,
                        errorMessage: message
                    )
                    executedCalls.append(deniedCall)
                    results.append(failedResult)
                    await persistToolResult(failedResult, conversationID: conversationID)
                    await emitter?(.toolFailed(name: displayName))
                case .failed(let message):
                    let failedCall = toolCall.updatingStatus(.failed)
                    let failedResult = AtlasToolResult(
                        toolCallID: toolCall.id,
                        output: "",
                        success: false,
                        errorMessage: message
                    )
                    executedCalls.append(failedCall)
                    results.append(failedResult)
                    await persistToolResult(failedResult, conversationID: conversationID)
                    await emitter?(.toolFailed(name: displayName))
                }
            } catch {
                let failedCall = toolCall.updatingStatus(.failed)
                let failedResult = AtlasToolResult(
                    toolCallID: toolCall.id,
                    output: "",
                    success: false,
                    errorMessage: error.localizedDescription
                )
                executedCalls.append(failedCall)
                results.append(failedResult)
                await persistToolResult(failedResult, conversationID: conversationID)
                await emitter?(.toolFailed(name: displayName))
            }
        }

        return ToolExecutionBatch(
            toolCalls: executedCalls,
            results: results,
            pendingApprovals: pendingApprovals
        )
    }

    @discardableResult
    public func appendToolResultMessages(
        _ results: [AtlasToolResult],
        toolCalls: [AtlasToolCall],
        to conversationID: UUID
    ) async throws -> AtlasConversation {
        var latestConversation = try await context.conversationStore.fetchConversation(id: conversationID) ??
            AtlasConversation(id: conversationID)

        for result in results {
            guard let toolCall = toolCalls.first(where: { $0.id == result.toolCallID }) else {
                continue
            }

            let content: String
            if result.success {
                content = "Tool \(toolCall.toolName) output:\n\(result.output)"
            } else {
                let errorText = result.errorMessage ?? result.output
                content = "Tool \(toolCall.toolName) failed: \(errorText)"
            }
            let toolMessage = AtlasMessage(role: .tool, content: content)
            latestConversation = try await context.conversationStore.appendMessage(toolMessage, to: conversationID)
        }

        return latestConversation
    }

    private func toolPolicyMaps() async -> ([String: PermissionLevel], [String: Bool]) {
        let definitions = await context.toolRegistry.definitions()
        var permissionLevels: [String: PermissionLevel] = [:]
        var approvalRequirements: [String: Bool] = [:]

        for definition in definitions {
            guard let tool = await context.toolRegistry.tool(named: definition.name) else {
                continue
            }

            permissionLevels[definition.name] = tool.permissionLevel
            approvalRequirements[definition.name] = await context.permissionManager.requiresApproval(for: tool.permissionLevel)
        }

        return (permissionLevels, approvalRequirements)
    }

    private func buildInstructions(
        for conversation: AtlasConversation,
        routingDecision: SkillRoutingDecision
    ) async throws -> String {
        let latestUserInput = conversation.messages.last(where: { $0.role == .user })?.content ?? ""
        let mindContent = await context.mindEngine.systemPromptBlock()
        let skillsBlock = await context.skillsEngine.selectiveBlock(for: latestUserInput)
        let sessionContext = PersonaSessionContext(
            conversationID: conversation.id,
            latestUserInput: latestUserInput,
            messageCount: conversation.messages.count,
            platformContext: conversation.platformContext
        )
        let enabledSkills = await context.skillRegistry.listEnabled().map(\.manifest)

        var prompt = context.personaPromptAssembler.assemblePrompt(
            mindContent: mindContent,
            sessionContext: sessionContext,
            routingDecision: routingDecision,
            enabledSkills: enabledSkills,
            skillsBlock: skillsBlock
        )

        // Append Forge orchestration instructions when the skill is enabled.
        if enabledSkills.contains(where: { $0.id == ForgeOrchestrationSkill.skillID }) {
            prompt += "\n\n" + ForgeOrchestrationSkill.systemPromptBlock

            // Append available custom API key entries so the agent sets authSecretKey
            // to the exact Keychain key rather than inventing one the Keychain won't resolve.
            let keyEntries = context.config.customKeyEntries()
            if !keyEntries.isEmpty {
                let entryList = keyEntries
                    .map { "- \($0.keychainKey)  (\($0.displayName))" }
                    .joined(separator: "\n")
                prompt += """


## Available Custom API Keys

The following custom API keys are stored in Keychain. When proposing a Forge skill that needs one of these, set `authSecretKey` to the **keychain key** shown below (the value before the parenthesis):

\(entryList)
"""
            }
        }

        // Append configured / available integrations summary.
        prompt += buildIntegrationsBlock()

        return prompt
    }

    private func buildIntegrationsBlock() -> String {
        let config = AtlasConfig()
        let integrations = config.integrations()
        guard !integrations.isEmpty else { return "" }

        let configured   = integrations.filter { $0.isConfigured }
        let unconfigured = integrations.filter { !$0.isConfigured }

        var lines: [String] = ["\n\n## Configured Integrations"]

        if !configured.isEmpty {
            lines.append("Active:")
            for i in configured {
                lines.append("- \(i.name) ✓ — \(i.description)")
            }
        }

        if !unconfigured.isEmpty {
            lines.append("Available but not configured:")
            for i in unconfigured {
                lines.append("- \(i.name) ✗ — \(i.setupHint)")
            }
        }

        return lines.joined(separator: "\n")
    }

    private func continuationInstructions(
        forConversationID conversationID: UUID,
        routingDecision: SkillRoutingDecision
    ) async throws -> String? {
        guard let conversation = try await context.conversationStore.fetchConversation(id: conversationID) else {
            return nil
        }

        return try await buildInstructions(for: conversation, routingDecision: routingDecision)
    }

    private func continuationToolPresentationBundle(forConversationID conversationID: UUID) async throws -> ToolPresentationBundle {
        guard let conversation = try await context.conversationStore.fetchConversation(id: conversationID) else {
            return await fallbackToolPresentationBundle()
        }
        return await buildToolPresentationBundle(for: conversation)
    }

    private func buildToolPresentationBundle(for conversation: AtlasConversation, forceFull: Bool = false) async -> ToolPresentationBundle {
        let latestUserInput = conversation.messages.last(where: { $0.role == .user })?.content ?? ""
        let enabledSkills = await context.skillRegistry.listEnabled()
        let actionCatalog = await context.skillRegistry.enabledActionCatalog()
        let decision = context.skillRoutingPolicy.decision(
            for: SkillRoutingContext(
                userMessage: latestUserInput,
                enabledSkills: enabledSkills,
                actionCatalog: actionCatalog
            )
        )
        let orderedDefinitions = await orderedToolDefinitions(actionCatalog: actionCatalog, decision: decision, forceFull: forceFull)
        return ToolPresentationBundle(
            toolDefinitions: orderedDefinitions,
            routingDecision: decision
        )
    }

    private func fallbackToolPresentationBundle() async -> ToolPresentationBundle {
        ToolPresentationBundle(
            toolDefinitions: await context.toolRegistry.definitions(),
            routingDecision: SkillRoutingDecision(
                intent: .unknown,
                confidence: 0,
                explanation: "No conversation context was available for routing."
            )
        )
    }

    private func orderedToolDefinitions(
        actionCatalog: [SkillActionCatalogItem],
        decision: SkillRoutingDecision,
        forceFull: Bool = false
    ) async -> [AtlasToolDefinition] {
        let rankedCatalog = context.skillRoutingPolicy.rank(actionCatalog, with: decision)

        // Full list: all ranked skill tools + non-skill registry tools
        func fullToolList() async -> [AtlasToolDefinition] {
            let skillDefs = rankedCatalog.map { $0.toolDefinition }
            let skillNames = Set(skillDefs.map { $0.name })
            let registryDefs = await context.toolRegistry.definitions()
                .filter { !skillNames.contains($0.name) }
            return skillDefs + registryDefs
        }

        // Smart selection disabled or forced full → current behaviour
        guard context.config.enableSmartToolSelection && !forceFull else {
            return await fullToolList()
        }

        // Tier 1: Conversational — send zero tools (pure text response)
        if decision.intent == .conversational {
            context.logger.info("Smart tool selection: Tier 1 (conversational) — 0 tools sent")
            return []
        }

        // Tier 2: Single unambiguous skill match (confidence ≥ 0.85)
        // Send only that skill's tools + alwaysInclude tools + registry tools
        if !decision.preferredSkills.isEmpty && decision.confidence >= 0.85 {
            let preferredDefs = rankedCatalog
                .filter { decision.preferredSkills.contains($0.skillID) }
                .map { $0.toolDefinition }
            let alwaysIncludeDefs = rankedCatalog
                .filter { $0.alwaysInclude && !decision.preferredSkills.contains($0.skillID) }
                .map { $0.toolDefinition }
            let sentNames = Set((preferredDefs + alwaysIncludeDefs).map { $0.name })
            let registryDefs = await context.toolRegistry.definitions()
                .filter { !sentNames.contains($0.name) }
            let result = preferredDefs + alwaysIncludeDefs + registryDefs
            context.logger.info(
                "Smart tool selection: Tier 2 (skill match) — \(result.count) tools",
                metadata: ["skills": decision.preferredSkills.joined(separator: ",")]
            )
            return result
        }

        // Tier 3: Ambiguous / multi-skill / general reasoning — full tool list
        let full = await fullToolList()
        context.logger.info("Smart tool selection: Tier 3 (full) — \(full.count) tools")
        return full
    }

    private func persistToolCall(_ toolCall: AtlasToolCall, conversationID: UUID) async {
        do {
            try await context.eventLogStore.persist(toolCall: toolCall, conversationID: conversationID)
        } catch {
            context.logger.error("Failed to persist tool call", metadata: [
                "tool_call_id": toolCall.id.uuidString,
                "error": error.localizedDescription
            ])
        }
    }

    private func persistToolResult(_ result: AtlasToolResult, conversationID: UUID) async {
        do {
            try await context.eventLogStore.persist(toolResult: result, conversationID: conversationID)
        } catch {
            context.logger.error("Failed to persist tool result", metadata: [
                "tool_call_id": result.toolCallID.uuidString,
                "error": error.localizedDescription
            ])
        }
    }

    private func persistApproval(_ request: ApprovalRequest, conversationID: UUID) async {
        do {
            try await context.eventLogStore.persist(approvalRequest: request, conversationID: conversationID)
        } catch {
            context.logger.error("Failed to persist approval request", metadata: [
                "tool_call_id": request.toolCallID.uuidString,
                "error": error.localizedDescription
            ])
        }
    }
}
