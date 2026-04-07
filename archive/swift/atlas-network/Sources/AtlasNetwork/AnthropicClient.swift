import Foundation
import AtlasShared
import AtlasLogging

public enum AnthropicClientError: LocalizedError {
    case missingAPIKey
    case invalidResponse
    case unexpectedStatusCode(Int, String)

    public var errorDescription: String? {
        switch self {
        case .missingAPIKey:
            return "No Anthropic API key found in the macOS Keychain."
        case .invalidResponse:
            return "The Anthropic Messages API returned an invalid response."
        case .unexpectedStatusCode(let code, let message):
            return "The Anthropic Messages API returned status \(code): \(message)"
        }
    }
}

public final class AnthropicClient: Sendable, AtlasAIClient {
    private let session: URLSession
    private let config: AtlasConfig
    private let logger = AtlasLogger.network

    private static let apiVersion   = "2023-06-01"
    private static let maxTokens    = 8096
    private static let endpoint     = URL(string: "https://api.anthropic.com/v1/messages")!

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
        let messages = buildMessages(from: conversation, attachments: attachments)
        return try await request(
            messages: messages,
            system: instructions ?? config.baseSystemPrompt,
            tools: tools,
            model: model,
            stream: false,
            onDelta: nil
        )
    }

    public func sendTurnStreaming(
        conversation: AtlasConversation,
        tools: [AtlasToolDefinition],
        instructions: String?,
        model: String,
        attachments: [AtlasMessageAttachment],
        onDelta: @escaping @Sendable (String) async -> Void
    ) async throws -> AITurnResponse {
        let messages = buildMessages(from: conversation, attachments: attachments)
        return try await request(
            messages: messages,
            system: instructions ?? config.baseSystemPrompt,
            tools: tools,
            model: model,
            stream: true,
            onDelta: onDelta
        )
    }

    public func continueTurn(
        previousTurnID: String,
        conversation: AtlasConversation,
        toolCalls: [AtlasToolCall],
        toolOutputs: [AIToolOutput],
        tools: [AtlasToolDefinition],
        instructions: String?,
        model: String
    ) async throws -> AITurnResponse {
        let messages = buildContinuationMessages(
            conversation: conversation,
            toolCalls: toolCalls,
            toolOutputs: toolOutputs
        )
        return try await request(
            messages: messages,
            system: instructions ?? config.baseSystemPrompt,
            tools: tools,
            model: model,
            stream: false,
            onDelta: nil
        )
    }

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
        let messages = buildContinuationMessages(
            conversation: conversation,
            toolCalls: toolCalls,
            toolOutputs: toolOutputs
        )
        return try await request(
            messages: messages,
            system: instructions ?? config.baseSystemPrompt,
            tools: tools,
            model: model,
            stream: true,
            onDelta: onDelta
        )
    }

    public func validateCredential() async throws {
        try await validateCredential(model: nil)
    }

    public func validateCredential(model: String?) async throws {
        let key = try fetchAPIKey()
        var req = URLRequest(url: Self.endpoint)
        req.httpMethod = "POST"
        applyHeaders(to: &req, apiKey: key)
        let resolvedModel = model ?? AIProvider.anthropic.defaultPrimaryModel
        let body: [String: Any] = [
            "model": resolvedModel,
            "max_tokens": 1,
            "messages": [["role": "user", "content": "."]]
        ]
        req.httpBody = try JSONSerialization.data(withJSONObject: body)
        let (data, response) = try await session.data(for: req)
        guard let http = response as? HTTPURLResponse else {
            throw AnthropicClientError.invalidResponse
        }
        // 200 = success; 400 may mean the model is unavailable but the key is valid
        guard (200..<300).contains(http.statusCode) || http.statusCode == 400 else {
            throw AnthropicClientError.unexpectedStatusCode(http.statusCode, decodeError(data))
        }
        logger.info("Anthropic credential validated — status: \(http.statusCode)", metadata: ["model": resolvedModel])
    }

    // MARK: - OpenAIQuerying

    public func complete(systemPrompt: String, userContent: String, model: String? = nil) async throws -> String {
        let resolvedModel = model ?? config.activeAIProvider.defaultFastModel
        let messages = [AnthropicMessage(role: "user", content: .text(userContent))]
        let response = try await request(
            messages: messages,
            system: systemPrompt,
            tools: [],
            model: resolvedModel,
            stream: false,
            onDelta: nil
        )
        return response.assistantText
    }

    // MARK: - Message building

    private func buildMessages(
        from conversation: AtlasConversation,
        attachments: [AtlasMessageAttachment]
    ) -> [AnthropicMessage] {
        let source = windowedMessages(conversation.messages)
        let lastUserID = source.last(where: { $0.role == .user })?.id
        var messages: [AnthropicMessage] = []

        for message in source {
            switch message.role {
            case .system:
                continue // system goes in the top-level `system` parameter

            case .user:
                if message.id == lastUserID, !attachments.filter(\.isImage).isEmpty {
                    var blocks: [AnthropicContentBlock] = [.textBlock(message.content)]
                    for att in attachments.filter(\.isImage) {
                        // Inline base64 image support — extract type from dataURI
                        if let (mediaType, base64) = splitDataURI(att.dataURI) {
                            blocks.append(AnthropicContentBlock(
                                type: "image",
                                text: nil,
                                id: nil,
                                name: nil,
                                input: [
                                    "type": .string("base64"),
                                    "media_type": .string(mediaType),
                                    "data": .string(base64)
                                ]
                            ))
                        }
                    }
                    messages.append(AnthropicMessage(role: "user", content: .blocks(blocks)))
                } else {
                    messages.append(AnthropicMessage(role: "user", content: .text(message.content)))
                }

            case .assistant:
                messages.append(AnthropicMessage(role: "assistant", content: .text(message.content)))

            case .tool:
                // Tool results from a prior turn that we can't reconstruct properly
                // (no original tool_use_id) — include as plain user text for context.
                messages.append(AnthropicMessage(role: "user", content: .text(message.content)))
            }
        }

        // Anthropic requires the first message to be from "user".
        while messages.first?.role == "assistant" { messages.removeFirst() }
        return messages
    }

    /// Reconstructs the proper Anthropic message history for a continuation turn.
    ///
    /// At this point `conversation.messages` contains:
    ///   [...prior turns..., assistant_text, tool_result_1, tool_result_2, ...]
    ///
    /// We need Anthropic's format:
    ///   [...prior turns..., {assistant: text + tool_use blocks}, {user: tool_result blocks}]
    private func buildContinuationMessages(
        conversation: AtlasConversation,
        toolCalls: [AtlasToolCall],
        toolOutputs: [AIToolOutput]
    ) -> [AnthropicMessage] {
        var allMessages = windowedMessages(conversation.messages)

        // Strip trailing .tool messages — we rebuild them as tool_result blocks.
        while allMessages.last?.role == .tool { allMessages.removeLast() }

        // Pop the last assistant message — we rebuild it with tool_use blocks appended.
        let assistantText: String
        if allMessages.last?.role == .assistant {
            assistantText = allMessages.removeLast().content
        } else {
            assistantText = ""
        }

        // Rebuild prior turns.
        var messages: [AnthropicMessage] = []
        for message in allMessages {
            switch message.role {
            case .system:   continue
            case .user:     messages.append(AnthropicMessage(role: "user", content: .text(message.content)))
            case .assistant: messages.append(AnthropicMessage(role: "assistant", content: .text(message.content)))
            case .tool:     messages.append(AnthropicMessage(role: "user", content: .text(message.content)))
            }
        }

        // Rebuilt assistant message: text + one tool_use block per tool call.
        var assistantBlocks: [AnthropicContentBlock] = []
        if !assistantText.isEmpty { assistantBlocks.append(.textBlock(assistantText)) }
        for tc in toolCalls {
            guard let callID = tc.openAICallID else { continue }
            let input = AnthropicJSONValue.dict(fromJSONString: tc.argumentsJSON)
            assistantBlocks.append(.toolUseBlock(id: callID, name: tc.toolName, input: input))
        }
        if !assistantBlocks.isEmpty {
            messages.append(AnthropicMessage(role: "assistant", content: .blocks(assistantBlocks)))
        }

        // User message with one tool_result block per output.
        let resultBlocks = toolOutputs.map { output in
            AnthropicContentBlock.toolResultBlock(toolUseID: output.callID, content: output.output)
        }
        if !resultBlocks.isEmpty {
            messages.append(AnthropicMessage(role: "user", content: .blocks(resultBlocks)))
        }

        // Guard: first message must be user.
        while messages.first?.role == "assistant" { messages.removeFirst() }
        return messages
    }

    // MARK: - HTTP

    private func request(
        messages: [AnthropicMessage],
        system: String?,
        tools: [AtlasToolDefinition],
        model: String,
        stream: Bool,
        onDelta: (@Sendable (String) async -> Void)?
    ) async throws -> AITurnResponse {
        let apiKey   = try fetchAPIKey()
        let anthropicTools = tools.isEmpty ? nil : tools.map(buildTool(from:))
        let body = AnthropicMessagesRequest(
            model: model,
            maxTokens: Self.maxTokens,
            system: system,
            messages: messages,
            tools: anthropicTools,
            stream: stream ? true : nil
        )

        var req = URLRequest(url: Self.endpoint)
        req.httpMethod = "POST"
        applyHeaders(to: &req, apiKey: apiKey)
        req.httpBody = try AtlasJSON.encoder.encode(body)

        logger.info("Anthropic request — model: \(model), messages: \(messages.count), tools: \(tools.count), streaming: \(stream)")

        if stream, let onDelta {
            return try await requestStreaming(req: req, onDelta: onDelta)
        } else {
            return try await requestBlocking(req: req)
        }
    }

    private func requestBlocking(req: URLRequest) async throws -> AITurnResponse {
        let (data, response) = try await session.data(for: req)
        guard let http = response as? HTTPURLResponse else { throw AnthropicClientError.invalidResponse }
        guard (200..<300).contains(http.statusCode) else {
            let msg = decodeError(data)
            logger.error("Anthropic request failed", metadata: ["status": "\(http.statusCode)", "message": msg])
            throw AnthropicClientError.unexpectedStatusCode(http.statusCode, msg)
        }
        let decoded = try AtlasJSON.decoder.decode(AnthropicMessagesResponse.self, from: data)
        logger.info("Anthropic response received", metadata: ["response_id": decoded.id])
        return extractTurnResponse(from: decoded)
    }

    private func requestStreaming(
        req: URLRequest,
        onDelta: @escaping @Sendable (String) async -> Void
    ) async throws -> AITurnResponse {
        let (byteStream, response) = try await session.bytes(for: req)
        guard let http = response as? HTTPURLResponse else { throw AnthropicClientError.invalidResponse }
        guard (200..<300).contains(http.statusCode) else {
            var data = Data()
            for try await byte in byteStream { data.append(byte) }
            throw AnthropicClientError.unexpectedStatusCode(http.statusCode, decodeError(data))
        }

        var assistantText = ""
        var inProgress: [Int: (id: String, name: String, args: String)] = [:]
        var completed: [AIRawToolCall] = []
        var responseID = UUID().uuidString

        for try await line in byteStream.lines {
            guard line.hasPrefix("data: ") else { continue }
            let json = String(line.dropFirst(6))
            guard !json.isEmpty,
                  let data = json.data(using: .utf8),
                  let event = try? AtlasJSON.decoder.decode(AnthropicStreamEvent.self, from: data)
            else { continue }

            switch event.type {
            case "message_start":
                break  // message ID is in the message object (not parsed here for brevity)

            case "content_block_start":
                guard let idx = event.index, let block = event.contentBlock else { break }
                if block.type == "tool_use", let id = block.id, let name = block.name {
                    inProgress[idx] = (id: id, name: name, args: "")
                }

            case "content_block_delta":
                guard let idx = event.index, let delta = event.delta else { break }
                if delta.type == "text_delta", let text = delta.text, !text.isEmpty {
                    assistantText += text
                    await onDelta(text)
                } else if delta.type == "input_json_delta", let partial = delta.partialJson {
                    inProgress[idx]?.args += partial
                }

            case "content_block_stop":
                guard let idx = event.index, let tc = inProgress.removeValue(forKey: idx) else { break }
                completed.append(AIRawToolCall(
                    name: tc.name,
                    argumentsJSON: tc.args.isEmpty ? "{}" : tc.args,
                    callID: tc.id
                ))

            default:
                break
            }
        }

        logger.info("Anthropic stream completed", metadata: ["response_id": responseID, "tool_calls": "\(completed.count)"])
        return AITurnResponse(turnID: responseID, assistantText: assistantText, rawToolCalls: completed)
    }

    // MARK: - Helpers

    private func extractTurnResponse(from response: AnthropicMessagesResponse) -> AITurnResponse {
        var text = ""
        var toolCalls: [AIRawToolCall] = []

        for block in response.content {
            switch block.type {
            case "text":
                text += block.text ?? ""
            case "tool_use":
                guard let id = block.id, let name = block.name else { continue }
                let argsJSON: String
                if let input = block.input,
                   let data = try? JSONEncoder().encode(input),
                   let str  = String(data: data, encoding: .utf8) {
                    argsJSON = str
                } else {
                    argsJSON = "{}"
                }
                toolCalls.append(AIRawToolCall(name: name, argumentsJSON: argsJSON, callID: id))
            default:
                break
            }
        }
        return AITurnResponse(turnID: response.id, assistantText: text, rawToolCalls: toolCalls)
    }

    private func buildTool(from definition: AtlasToolDefinition) -> AnthropicTool {
        let props = definition.inputSchema.properties.mapValues { p in
            AnthropicTool.PropertySchema(type: p.type, description: p.description)
        }
        return AnthropicTool(
            name: definition.name,
            description: definition.description,
            inputSchema: AnthropicTool.InputSchema(
                properties: props,
                required: definition.inputSchema.required
            )
        )
    }

    private func applyHeaders(to req: inout URLRequest, apiKey: String) {
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        req.setValue(apiKey, forHTTPHeaderField: "x-api-key")
        req.setValue(Self.apiVersion, forHTTPHeaderField: "anthropic-version")
    }

    private func fetchAPIKey() throws -> String {
        do { return try config.anthropicAPIKey() } catch { throw AnthropicClientError.missingAPIKey }
    }

    private func decodeError(_ data: Data) -> String {
        if let env = try? AtlasJSON.decoder.decode(AnthropicErrorEnvelope.self, from: data) {
            return env.error.message ?? "Unknown Anthropic error."
        }
        return String(decoding: data, as: UTF8.self)
    }

    /// Trims conversation history to the configured window size so we don't blow
    /// through provider rate limits on long conversations.
    private func windowedMessages(_ messages: [AtlasMessage]) -> [AtlasMessage] {
        let limit = config.conversationWindowLimit
        guard limit > 0, messages.count > limit else { return messages }
        var result = Array(messages.suffix(limit))
        // Must begin with a user message — strip leading assistant/tool messages.
        while let first = result.first, first.role != .user { result.removeFirst() }
        logger.info("AnthropicClient: conversation window trimmed to \(result.count)/\(messages.count) messages")
        return result
    }

    /// Split a `data:<mediaType>;base64,<data>` URI into (mediaType, base64).
    private func splitDataURI(_ uri: String) -> (String, String)? {
        guard uri.hasPrefix("data:"),
              let commaIdx = uri.firstIndex(of: ",")
        else { return nil }
        let header = String(uri[uri.index(uri.startIndex, offsetBy: 5)..<commaIdx])
        let base64 = String(uri[uri.index(after: commaIdx)...])
        let mediaType = header.replacingOccurrences(of: ";base64", with: "")
        return (mediaType, base64)
    }
}
