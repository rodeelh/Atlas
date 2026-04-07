import Foundation
import AtlasShared

// MARK: - Shared response types

/// A normalized model-turn response, provider-agnostic.
public struct AITurnResponse: Sendable {
    /// Provider-specific turn identifier.
    /// For OpenAI (Responses API) this is the response ID used as `previousResponseID` on the next
    /// continuation. For stateless providers (Anthropic, Gemini, LM Studio) a synthetic UUID is used.
    public let turnID: String
    /// The assistant's text output for this turn. May be empty when the model only issues tool calls.
    public let assistantText: String
    /// Raw tool calls extracted from the model output, before permission mapping.
    public let rawToolCalls: [AIRawToolCall]

    public init(turnID: String, assistantText: String, rawToolCalls: [AIRawToolCall]) {
        self.turnID = turnID
        self.assistantText = assistantText
        self.rawToolCalls = rawToolCalls
    }
}

/// A raw tool call from the model output, before it is promoted to an `AtlasToolCall`.
public struct AIRawToolCall: Sendable {
    /// The registered tool name.
    public let name: String
    /// JSON-encoded arguments string (may be `"{}"` when the model passes no arguments).
    public let argumentsJSON: String
    /// Provider-specific call identifier used to match results on continuation.
    /// Maps to `AtlasToolCall.openAICallID` in the persistence layer.
    public let callID: String

    public init(name: String, argumentsJSON: String, callID: String) {
        self.name = name
        self.argumentsJSON = argumentsJSON
        self.callID = callID
    }
}

/// A resolved tool execution result, ready to be submitted to the model on a continuation turn.
public struct AIToolOutput: Sendable {
    /// The provider call ID (matches `AIRawToolCall.callID` / `AtlasToolCall.openAICallID`).
    public let callID: String
    /// The tool's text output. Error outputs are prefixed with `"Error: "`.
    public let output: String

    public init(callID: String, output: String) {
        self.callID = callID
        self.output = output
    }
}

// MARK: - AtlasAIClient protocol

/// Abstraction over any AI provider used for Atlas agent conversations.
///
/// Conforming types: `OpenAIClient`, `AnthropicClient`, `GeminiClient`, `LMStudioClient`.
///
/// Also refines `OpenAIQuerying` so the same instance can be injected into
/// `MindReflectionService`, `SkillsEngine`, and `DashboardPlanner` without changes
/// to those call sites.
public protocol AtlasAIClient: OpenAIQuerying {

    // MARK: - Initial turn

    /// Send the full conversation to the model and return the next assistant turn.
    func sendTurn(
        conversation: AtlasConversation,
        tools: [AtlasToolDefinition],
        instructions: String?,
        model: String,
        attachments: [AtlasMessageAttachment]
    ) async throws -> AITurnResponse

    /// Streaming variant of `sendTurn`. Calls `onDelta` for each text chunk as it arrives,
    /// then returns the complete turn once the stream closes.
    func sendTurnStreaming(
        conversation: AtlasConversation,
        tools: [AtlasToolDefinition],
        instructions: String?,
        model: String,
        attachments: [AtlasMessageAttachment],
        onDelta: @escaping @Sendable (String) async -> Void
    ) async throws -> AITurnResponse

    // MARK: - Continuation turn

    /// Submit tool execution results and receive the next assistant turn.
    ///
    /// Provider-specific behaviour:
    /// - **OpenAI / LM Studio (Responses API)**: uses `previousTurnID` as the `previousResponseID`.
    ///   `conversation` and `toolCalls` are ignored — the server holds full context.
    /// - **Stateless providers (Anthropic, Gemini)**: `previousTurnID` is ignored.
    ///   `conversation` is used to reconstruct prior message history; `toolCalls` provides the
    ///   tool_use / functionCall blocks for the previous assistant message.
    func continueTurn(
        previousTurnID: String,
        conversation: AtlasConversation,
        toolCalls: [AtlasToolCall],
        toolOutputs: [AIToolOutput],
        tools: [AtlasToolDefinition],
        instructions: String?,
        model: String
    ) async throws -> AITurnResponse

    /// Streaming variant of `continueTurn`.
    func continueTurnStreaming(
        previousTurnID: String,
        conversation: AtlasConversation,
        toolCalls: [AtlasToolCall],
        toolOutputs: [AIToolOutput],
        tools: [AtlasToolDefinition],
        instructions: String?,
        model: String,
        onDelta: @escaping @Sendable (String) async -> Void
    ) async throws -> AITurnResponse

    /// Verify that the stored credential for this provider is valid.
    func validateCredential() async throws
}
