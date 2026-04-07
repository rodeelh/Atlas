import Foundation
import AtlasShared
import AtlasLogging

/// Errors thrown by `LMStudioClient`.
public enum LMStudioClientError: LocalizedError {
    case invalidBaseURL(String)
    case invalidResponse
    case unexpectedStatusCode(Int, String)
    case noOutputInResponse

    public var errorDescription: String? {
        switch self {
        case .invalidBaseURL(let url):
            return "LM Studio base URL is invalid: \(url)"
        case .invalidResponse:
            return "LM Studio returned an invalid response."
        case .unexpectedStatusCode(let code, let message):
            return "LM Studio returned status \(code): \(message)"
        case .noOutputInResponse:
            return "LM Studio response contained no output items."
        }
    }
}

// MARK: - LMStudioClient

/// An `AtlasAIClient` that uses LM Studio's OpenAI-compatible `/v1/responses` endpoint.
///
/// **Stateless mode**: Every request uses `store: false` and `previous_response_id: nil`.
/// `sendTurn` sends the full conversation history; `continueTurn` rebuilds the full input
/// (conversation + tool outputs) the same way Anthropic/Gemini do. This avoids the context
/// overflow that LM Studio's stateful mode (`store: true`) can trigger on small local models.
///
/// **Optional authentication**: When an LM Studio API key has been configured in Atlas
/// credentials (Settings → Credentials → LM Studio), a `Bearer` token is injected into every
/// request header. If no key is set, requests are sent unauthenticated (the LM Studio default).
public final class LMStudioClient: Sendable, AtlasAIClient {
    private let session: URLSession
    private let config: AtlasConfig
    private let logger = AtlasLogger.network

    public init(session: URLSession = .shared, config: AtlasConfig = AtlasConfig()) {
        self.session = session
        self.config  = config
    }

    // MARK: - AtlasAIClient

    public func sendTurn(
        conversation: AtlasConversation,
        tools: [AtlasToolDefinition],
        instructions: String?,
        model: String,
        attachments: [AtlasMessageAttachment]
    ) async throws -> AITurnResponse {
        let input = buildConversationInput(from: conversation, attachments: attachments)
        let apiTools = tools.isEmpty ? nil : tools.map(OpenAIToolDefinition.init(definition:))
        let instructionsText = instructions ?? config.baseSystemPrompt
        do {
            let response = try await sendRequest(
                input: input, tools: apiTools,
                previousResponseID: nil, instructions: instructionsText,
                model: model, stream: false, onDelta: nil, store: false
            )
            return AITurnResponse(turnID: response.id,
                                  assistantText: stripStopTokens(response.latestAssistantMessageText()),
                                  rawToolCalls: extractRawToolCalls(from: response))
        } catch LMStudioClientError.unexpectedStatusCode(0, _) where apiTools != nil {
            // Context overflow: retry without tools so a plain-text response can still be returned.
            logger.warning("LM Studio context overflow on sendTurn — retrying without tools")
            let response = try await sendRequest(
                input: input, tools: nil,
                previousResponseID: nil, instructions: instructionsText,
                model: model, stream: false, onDelta: nil, store: false
            )
            return AITurnResponse(turnID: response.id,
                                  assistantText: stripStopTokens(response.latestAssistantMessageText()),
                                  rawToolCalls: [])
        }
    }

    public func sendTurnStreaming(
        conversation: AtlasConversation,
        tools: [AtlasToolDefinition],
        instructions: String?,
        model: String,
        attachments: [AtlasMessageAttachment],
        onDelta: @escaping @Sendable (String) async -> Void
    ) async throws -> AITurnResponse {
        let input = buildConversationInput(from: conversation, attachments: attachments)
        let apiTools = tools.isEmpty ? nil : tools.map(OpenAIToolDefinition.init(definition:))
        let instructionsText = instructions ?? config.baseSystemPrompt
        do {
            let response = try await sendRequest(
                input: input, tools: apiTools,
                previousResponseID: nil, instructions: instructionsText,
                model: model, stream: true, onDelta: onDelta, store: false
            )
            return AITurnResponse(turnID: response.id,
                                  assistantText: stripStopTokens(response.latestAssistantMessageText()),
                                  rawToolCalls: extractRawToolCalls(from: response))
        } catch LMStudioClientError.unexpectedStatusCode(0, _) where apiTools != nil {
            // Context overflow: retry without tools so a plain-text response can still be returned.
            logger.warning("LM Studio context overflow on sendTurnStreaming — retrying without tools")
            let response = try await sendRequest(
                input: input, tools: nil,
                previousResponseID: nil, instructions: instructionsText,
                model: model, stream: true, onDelta: onDelta, store: false
            )
            return AITurnResponse(turnID: response.id,
                                  assistantText: stripStopTokens(response.latestAssistantMessageText()),
                                  rawToolCalls: [])
        }
    }

    /// Stateless continuation — rebuilds full conversation + tool outputs (no previous_response_id).
    /// LM Studio uses `store: false` to avoid context-window overflow on small local models.
    public func continueTurn(
        previousTurnID: String,
        conversation: AtlasConversation,
        toolCalls: [AtlasToolCall],
        toolOutputs: [AIToolOutput],
        tools: [AtlasToolDefinition],
        instructions: String?,
        model: String
    ) async throws -> AITurnResponse {
        let input = buildContinuationInput(from: conversation, toolOutputs: toolOutputs)
        let response = try await sendRequest(
            input: input,
            tools: tools.isEmpty ? nil : tools.map(OpenAIToolDefinition.init(definition:)),
            previousResponseID: nil,
            instructions: instructions ?? config.baseSystemPrompt,
            model: model,
            stream: false,
            onDelta: nil,
            store: false
        )
        return AITurnResponse(
            turnID: response.id,
            assistantText: stripStopTokens(response.latestAssistantMessageText()),
            rawToolCalls: extractRawToolCalls(from: response)
        )
    }

    /// Stateless streaming continuation.
    public func continueTurnStreaming(
        previousTurnID: String,
        conversation: AtlasConversation,
        toolCalls: [AtlasToolCall],
        toolOutputs: [AIToolOutput],
        tools: [AtlasToolDefinition],
        instructions: String?,
        model: String,
        onDelta: @escaping @Sendable (String) async -> Void
    ) async throws -> AITurnResponse {
        let input = buildContinuationInput(from: conversation, toolOutputs: toolOutputs)
        let response = try await sendRequest(
            input: input,
            tools: tools.isEmpty ? nil : tools.map(OpenAIToolDefinition.init(definition:)),
            previousResponseID: nil,
            instructions: instructions ?? config.baseSystemPrompt,
            model: model,
            stream: true,
            onDelta: onDelta,
            store: false
        )
        return AITurnResponse(
            turnID: response.id,
            assistantText: stripStopTokens(response.latestAssistantMessageText()),
            rawToolCalls: extractRawToolCalls(from: response)
        )
    }

    public func validateCredential() async throws {
        try await validateCredential(model: nil)
    }

    public func validateCredential(model: String?) async throws {
        let resolvedModel = model ?? config.selectedLMStudioModel
        guard !resolvedModel.isEmpty else {
            let url = try modelsListURL()
            var req = URLRequest(url: url)
            req.httpMethod = "GET"
            injectAuth(into: &req)
            let (_, response) = try await session.data(for: req)
            guard let http = response as? HTTPURLResponse,
                  (200..<300).contains(http.statusCode) else {
                throw LMStudioClientError.invalidResponse
            }
            logger.info("LM Studio connection validated")
            return
        }

        _ = try await sendRequest(
            input: [.message(OpenAIConversationInput(role: "user", text: ".", contentType: .inputText))],
            tools: nil,
            previousResponseID: nil,
            instructions: "Validate that LM Studio can answer with the configured chat model.",
            model: resolvedModel,
            stream: false,
            onDelta: nil,
            store: false
        )
        logger.info("LM Studio connection validated", metadata: ["model": resolvedModel])
    }

    // MARK: - OpenAIQuerying

    /// Single-turn completion without tool use — used by MindReflectionService, SkillsEngine, etc.
    public func complete(systemPrompt: String, userContent: String, model: String? = nil) async throws -> String {
        let resolvedModel = model ?? config.activeAIProvider.defaultFastModel
        let input: [OpenAIInputItem] = [
            .message(OpenAIConversationInput(role: "user", text: userContent, contentType: .inputText))
        ]
        // Use store: false for one-shot completions — no need to persist server-side
        let response = try await sendRequest(
            input: input,
            tools: nil,
            previousResponseID: nil,
            instructions: systemPrompt,
            model: resolvedModel,
            stream: false,
            onDelta: nil,
            store: false
        )
        return stripStopTokens(response.latestAssistantMessageText())
    }

    // MARK: - HTTP

    private func sendRequest(
        input: [OpenAIInputItem],
        tools: [OpenAIToolDefinition]?,
        previousResponseID: String?,
        instructions: String,
        model: String,
        stream: Bool,
        onDelta: (@Sendable (String) async -> Void)?,
        store: Bool = true
    ) async throws -> OpenAIResponsesCreateResponse {
        let url = try responsesURL()
        let reasoning: OpenAIReasoningConfig? = model.lowercased().contains("gpt-oss")
            ? OpenAIReasoningConfig(effort: .low)
            : nil
        let requestBody = OpenAIResponsesCreateRequest(
            model: model,
            instructions: instructions,
            input: input,
            tools: tools,
            store: store,
            previousResponseID: previousResponseID,
            stream: stream,
            reasoning: reasoning
        )

        var req = URLRequest(url: url)
        req.httpMethod = "POST"
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        injectAuth(into: &req)
        req.httpBody = try AtlasJSON.encoder.encode(requestBody)

        logger.info("LM Studio request — model: \(model), items: \(input.count), tools: \(tools?.count ?? 0), streaming: \(stream), prev: \(previousResponseID ?? "nil")")

        if stream, let onDelta {
            return try await requestStreaming(req: req, onDelta: onDelta)
        } else {
            return try await requestBlocking(req: req)
        }
    }

    private func requestBlocking(req: URLRequest) async throws -> OpenAIResponsesCreateResponse {
        let (data, response) = try await session.data(for: req)
        guard let http = response as? HTTPURLResponse else { throw LMStudioClientError.invalidResponse }
        guard (200..<300).contains(http.statusCode) else {
            let msg = decodeError(data)
            logger.error("LM Studio request failed", metadata: ["status": "\(http.statusCode)", "message": msg])
            throw LMStudioClientError.unexpectedStatusCode(http.statusCode, msg)
        }
        let decoded: OpenAIResponsesCreateResponse
        do {
            decoded = try AtlasJSON.decoder.decode(OpenAIResponsesCreateResponse.self, from: data)
        } catch {
            logger.error("Failed to decode LM Studio response", metadata: [
                "error": error.localizedDescription,
                "payload": String(decoding: data, as: UTF8.self)
            ])
            throw error
        }
        logger.info("LM Studio response received", metadata: [
            "response_id": decoded.id,
            "output_items": "\(decoded.output.count)"
        ])
        return decoded
    }

    private func requestStreaming(
        req: URLRequest,
        onDelta: @escaping @Sendable (String) async -> Void
    ) async throws -> OpenAIResponsesCreateResponse {
        let (byteStream, response) = try await session.bytes(for: req)
        guard let http = response as? HTTPURLResponse else { throw LMStudioClientError.invalidResponse }
        guard (200..<300).contains(http.statusCode) else {
            var data = Data()
            for try await byte in byteStream { data.append(byte) }
            throw LMStudioClientError.unexpectedStatusCode(http.statusCode, decodeError(data))
        }

        var completedResponse: OpenAIResponsesCreateResponse?
        var streamedText = ""
        var streamedResponseID = UUID().uuidString
        var toolCallsInProgress: [String: (name: String, args: String, callID: String)] = [:]

        for try await line in byteStream.lines {
            guard line.hasPrefix("data: ") else { continue }
            let json = String(line.dropFirst(6))
            guard json != "[DONE]", !json.isEmpty else { continue }
            guard let data = json.data(using: .utf8) else { continue }

            // Parse the event type from the JSON payload.
            guard let raw = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
                  let eventType = raw["type"] as? String else { continue }

            switch eventType {
            case "response.output_text.delta":
                if let delta = raw["delta"] as? String, !delta.isEmpty {
                    streamedText += delta
                    let cleanDelta = stripStopTokens(delta)
                    if !cleanDelta.isEmpty {
                        await onDelta(cleanDelta)
                    }
                }
            case "response.function_call_arguments.delta":
                if let callID = raw["call_id"] as? String,
                   let delta = raw["delta"] as? String {
                    var entry = toolCallsInProgress[callID] ?? (name: "", args: "", callID: callID)
                    entry.args += delta
                    toolCallsInProgress[callID] = entry
                }
            case "response.output_item.added":
                // Capture function call items when first added.
                if let item = raw["item"] as? [String: Any],
                   item["type"] as? String == "function_call",
                   let callID = item["call_id"] as? String,
                   let name = item["name"] as? String {
                    toolCallsInProgress[callID] = (name: name, args: "", callID: callID)
                }
            case "response.created":
                // Extract the response ID for use in the streaming fallback.
                if let resp = raw["response"] as? [String: Any],
                   let id = resp["id"] as? String {
                    streamedResponseID = id
                }
            case "response.completed":
                // Prefer the full completed response when available.
                if let event = try? AtlasJSON.decoder.decode(OpenAIStreamEvent.self, from: data),
                   let completed = event.response {
                    completedResponse = completed
                }
            case "error", "response.failed":
                // LM Studio signals a server-side error (e.g., context overflow).
                let msg = (raw["error"] as? [String: Any])?["message"] as? String
                    ?? (raw["response"] as? [String: Any]).flatMap { ($0["error"] as? [String: Any])?["message"] as? String }
                    ?? "Unknown LM Studio streaming error"
                logger.error("LM Studio streaming error event", metadata: ["type": eventType, "message": msg])
                throw LMStudioClientError.unexpectedStatusCode(0, msg)
            default:
                break
            }
        }

        if let completed = completedResponse {
            logger.info("LM Studio streaming response completed", metadata: [
                "response_id": completed.id,
                "output_items": "\(completed.output.count)"
            ])
            return completed
        }

        // Fallback: build a synthetic response from streamed content.
        // This handles LM Studio models/versions that don't emit response.completed.
        logger.warning("LM Studio streaming: no response.completed event — building synthetic response from streamed content")
        let toolCallOutputItems = toolCallsInProgress.values.map { tc in
            OpenAIOutputItem.makeFunctionCall(id: tc.callID, name: tc.name, arguments: tc.args)
        }
        let messageItem = OpenAIOutputItem.makeMessage(text: streamedText)
        let allItems = toolCallOutputItems + (streamedText.isEmpty && toolCallOutputItems.isEmpty ? [] : [messageItem])
        return OpenAIResponsesCreateResponse(id: streamedResponseID, output: allItems.isEmpty ? [messageItem] : allItems)
    }

    // MARK: - Helpers

    /// Strips ChatML / EOS stop tokens that some local models leak into their output.
    /// These are model-internal tokens that the inference server should remove but sometimes doesn't.
    /// Does NOT trim surrounding whitespace — space/newline-only deltas must pass through unchanged
    /// when used in the streaming path.
    private func stripStopTokens(_ text: String) -> String {
        let stopTokens = ["<|im_end|>", "<|eot_id|>", "<|endoftext|>", "</s>"]
        var result = text
        for token in stopTokens {
            result = result.replacingOccurrences(of: token, with: "")
        }
        return result
    }

    private func extractRawToolCalls(from response: OpenAIResponsesCreateResponse) -> [AIRawToolCall] {
        response.output.compactMap { item in
            guard item.type == "function_call", let name = item.name, let callID = item.callID else {
                return nil
            }
            return AIRawToolCall(
                name: name,
                argumentsJSON: item.arguments ?? "{}",
                callID: callID
            )
        }
    }

    private func buildConversationInput(
        from conversation: AtlasConversation,
        attachments: [AtlasMessageAttachment]
    ) -> [OpenAIInputItem] {
        let windowed = windowedMessages(conversation.messages)
        let lastUserMessageID = windowed.last(where: { $0.role == .user })?.id
        let imageAttachments = attachments.filter(\.isImage)

        return windowed.compactMap { message in
            let role: String
            var content: [OpenAIMessageContent]

            switch message.role {
            case .system:
                return nil
            case .tool:
                role = "user"
                content = [.text("Tool result:\n\(message.content)", .inputText)]
            case .user:
                role = "user"
                content = [.text(message.content, .inputText)]
                if message.id == lastUserMessageID && !imageAttachments.isEmpty {
                    // Local models do not support vision input — replace image data with a
                    // plain-text note so the model can respond gracefully instead of crashing.
                    let names = imageAttachments.map { $0.filename }.joined(separator: ", ")
                    content.append(.text(
                        "[The user attached image(s): \(names). Vision input is not supported by the active local model. Let the user know you cannot process images locally and suggest switching to a cloud provider for vision tasks.]",
                        .inputText
                    ))
                }
            case .assistant:
                role = "assistant"
                content = [.text(message.content, .outputText)]
            }

            return .message(OpenAIConversationInput(role: role, content: content))
        }
    }

    private func windowedMessages(_ messages: [AtlasMessage]) -> [AtlasMessage] {
        let limit = config.effectiveContextWindowLimit
        guard limit > 0, messages.count > limit else { return messages }
        var result = Array(messages.suffix(limit))
        while let first = result.first, first.role != .user { result.removeFirst() }
        logger.info("LMStudioClient: conversation window trimmed to \(result.count)/\(messages.count) messages")
        return result
    }

    /// Builds conversation input + function-call outputs for stateless tool-use continuation.
    /// Strips trailing `.tool` messages (they come in via `toolOutputs`), then appends
    /// each tool output as a `functionCallOutput` item.
    private func buildContinuationInput(
        from conversation: AtlasConversation,
        toolOutputs: [AIToolOutput]
    ) -> [OpenAIInputItem] {
        // Strip trailing .tool messages — results arrive via toolOutputs below.
        var messages = windowedMessages(conversation.messages)
        while messages.last?.role == .tool { messages.removeLast() }

        let truncated = AtlasConversation(id: conversation.id, messages: messages)
        var items = buildConversationInput(from: truncated, attachments: [])

        // Append tool outputs as function_call_output items.
        for output in toolOutputs {
            items.append(.functionCallOutput(
                OpenAIFunctionCallOutputInput(callID: output.callID, output: output.output)
            ))
        }
        return items
    }

    /// Inject Bearer token if an LM Studio API key is configured. No-op if none set.
    private func injectAuth(into req: inout URLRequest) {
        if let key = try? config.lmStudioAPIKey(), !key.isEmpty {
            req.setValue("Bearer \(key)", forHTTPHeaderField: "Authorization")
        }
    }

    private func responsesURL() throws -> URL {
        let base = config.lmStudioBaseURL.trimmingCharacters(in: CharacterSet(charactersIn: "/"))
        guard let url = URL(string: "\(base)/v1/responses") else {
            throw LMStudioClientError.invalidBaseURL(config.lmStudioBaseURL)
        }
        return url
    }

    private func modelsListURL() throws -> URL {
        let base = config.lmStudioBaseURL.trimmingCharacters(in: CharacterSet(charactersIn: "/"))
        guard let url = URL(string: "\(base)/v1/models") else {
            throw LMStudioClientError.invalidBaseURL(config.lmStudioBaseURL)
        }
        return url
    }

    private func decodeError(_ data: Data) -> String {
        if let envelope = try? AtlasJSON.decoder.decode(OpenAIErrorEnvelope.self, from: data) {
            return envelope.error.message ?? "Unknown LM Studio error."
        }
        return String(decoding: data, as: UTF8.self)
    }
}
