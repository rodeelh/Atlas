import Foundation
import AtlasShared
import AtlasLogging

public enum OpenAIClientError: LocalizedError {
    case missingAPIKey
    case invalidResponse
    case unexpectedStatusCode(Int, String)

    public var errorDescription: String? {
        switch self {
        case .missingAPIKey:
            return "No OpenAI API key was found in the macOS Keychain."
        case .invalidResponse:
            return "The OpenAI Responses API returned an invalid response."
        case .unexpectedStatusCode(let code, let message):
            return "The OpenAI Responses API returned status code \(code): \(message)"
        }
    }
}

public final class OpenAIClient: Sendable, OpenAIQuerying {
    private let session: URLSession
    private let config: AtlasConfig
    private let logger = AtlasLogger.network

    public init(
        session: URLSession = .shared,
        config: AtlasConfig = AtlasConfig()
    ) {
        self.session = session
        self.config = config
    }

    public func sendMessage(
        conversation: AtlasConversation,
        instructions: String? = nil,
        model: String? = nil,
        attachments: [AtlasMessageAttachment] = []
    ) async throws -> OpenAIResponsesCreateResponse {
        try await sendRequest(
            input: buildConversationInput(from: conversation, attachments: attachments),
            tools: nil,
            previousResponseID: nil,
            instructions: instructions,
            model: model ?? config.defaultOpenAIModel
        )
    }

    public func sendMessageWithTools(
        conversation: AtlasConversation,
        tools: [AtlasToolDefinition],
        instructions: String? = nil,
        model: String? = nil,
        attachments: [AtlasMessageAttachment] = []
    ) async throws -> OpenAIResponsesCreateResponse {
        try await sendRequest(
            input: buildConversationInput(from: conversation, attachments: attachments),
            tools: tools.map(OpenAIToolDefinition.init(definition:)),
            previousResponseID: nil,
            instructions: instructions,
            model: model ?? config.defaultOpenAIModel
        )
    }

    public func continueResponse(
        previousResponseID: String,
        toolOutputs: [OpenAIToolOutput],
        tools: [AtlasToolDefinition],
        instructions: String? = nil,
        model: String? = nil
    ) async throws -> OpenAIResponsesCreateResponse {
        let input = toolOutputs.map { output in
            OpenAIInputItem.functionCallOutput(
                OpenAIFunctionCallOutputInput(callID: output.callID, output: output.output)
            )
        }

        return try await sendRequest(
            input: input,
            tools: tools.map(OpenAIToolDefinition.init(definition:)),
            previousResponseID: previousResponseID,
            instructions: instructions,
            model: model ?? config.defaultOpenAIModel
        )
    }

    // MARK: - Streaming variants

    /// Streaming version of `sendMessageWithTools`. Calls `onDelta` for each text chunk
    /// as the model generates output, then returns the full completed response.
    public func sendMessageWithToolsStreaming(
        conversation: AtlasConversation,
        tools: [AtlasToolDefinition],
        instructions: String? = nil,
        model: String? = nil,
        attachments: [AtlasMessageAttachment] = [],
        onDelta: @escaping @Sendable (String) async -> Void
    ) async throws -> OpenAIResponsesCreateResponse {
        try await sendRequestStreaming(
            input: buildConversationInput(from: conversation, attachments: attachments),
            tools: tools.map(OpenAIToolDefinition.init(definition:)),
            previousResponseID: nil,
            instructions: instructions,
            model: model ?? config.defaultOpenAIModel,
            onDelta: onDelta
        )
    }

    /// Streaming version of `continueResponse`. Calls `onDelta` for each text chunk,
    /// then returns the full completed response.
    public func continueResponseStreaming(
        previousResponseID: String,
        toolOutputs: [OpenAIToolOutput],
        tools: [AtlasToolDefinition],
        instructions: String? = nil,
        model: String? = nil,
        onDelta: @escaping @Sendable (String) async -> Void
    ) async throws -> OpenAIResponsesCreateResponse {
        let input = toolOutputs.map { output in
            OpenAIInputItem.functionCallOutput(
                OpenAIFunctionCallOutputInput(callID: output.callID, output: output.output)
            )
        }
        return try await sendRequestStreaming(
            input: input,
            tools: tools.map(OpenAIToolDefinition.init(definition:)),
            previousResponseID: previousResponseID,
            instructions: instructions,
            model: model ?? config.defaultOpenAIModel,
            onDelta: onDelta
        )
    }

    public func parseResponse(_ response: OpenAIResponsesCreateResponse) -> String {
        response.latestAssistantMessageText()
    }

    public func parseToolCalls(
        _ response: OpenAIResponsesCreateResponse,
        permissionLevels: [String: PermissionLevel] = [:],
        approvalRequirements: [String: Bool] = [:]
    ) -> [AtlasToolCall] {
        response.output.compactMap { item in
            guard item.type == "function_call", let name = item.name else {
                return nil
            }

            return AtlasToolCall(
                toolName: name,
                argumentsJSON: item.arguments ?? "{}",
                permissionLevel: permissionLevels[name] ?? .read,
                requiresApproval: approvalRequirements[name] ?? false,
                status: .pending,
                openAICallID: item.callID
            )
        }
    }

    public func validateCredential() async throws {
        try await validateCredential(model: nil)
    }

    public func validateCredential(model: String?) async throws {
        let apiKey = try fetchAPIKey()
        let resolvedModel = model ?? config.defaultOpenAIModel
        let requestBody = OpenAIResponsesCreateRequest(
            model: resolvedModel,
            instructions: "Validate that this OpenAI credential can access the configured chat model.",
            input: [.message(OpenAIConversationInput(role: "user", text: ".", contentType: .inputText))],
            tools: nil,
            store: false,
            previousResponseID: nil
        )

        var request = URLRequest(url: URL(string: "https://api.openai.com/v1/responses")!)
        request.httpMethod = "POST"
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        request.setValue("Bearer \(apiKey)", forHTTPHeaderField: "Authorization")
        request.httpBody = try AtlasJSON.encoder.encode(requestBody)

        logger.info("Validating OpenAI credential", metadata: ["model": resolvedModel])

        let (data, response) = try await session.data(for: request)
        guard let httpResponse = response as? HTTPURLResponse else {
            logger.error("OpenAI validation response was not HTTP")
            throw OpenAIClientError.invalidResponse
        }

        guard (200..<300).contains(httpResponse.statusCode) else {
            let message = decodeErrorMessage(from: data)
            logger.error("OpenAI credential validation failed", metadata: [
                "status_code": "\(httpResponse.statusCode)"
            ])
            throw OpenAIClientError.unexpectedStatusCode(httpResponse.statusCode, message)
        }

        logger.info("OpenAI credential validation succeeded")
    }

    private func sendRequest(
        input: [OpenAIInputItem],
        tools: [OpenAIToolDefinition]?,
        previousResponseID: String?,
        instructions: String?,
        model: String
    ) async throws -> OpenAIResponsesCreateResponse {
        let apiKey = try fetchAPIKey()
        let requestBody = OpenAIResponsesCreateRequest(
            model: model,
            instructions: instructions ?? config.baseSystemPrompt,
            input: input,
            tools: tools,
            store: true,
            previousResponseID: previousResponseID
        )

        var request = URLRequest(url: URL(string: "https://api.openai.com/v1/responses")!)
        request.httpMethod = "POST"
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        request.setValue("Bearer \(apiKey)", forHTTPHeaderField: "Authorization")
        request.httpBody = try AtlasJSON.encoder.encode(requestBody)

        let imageCount = input.filter {
            if case .message(let m) = $0 { return m.content.contains { if case .image = $0 { return true }; return false } }
            return false
        }.count
        logger.info("Sending to OpenAI — model: \(model), items: \(input.count), images: \(imageCount), tools: \(tools?.count ?? 0)")

        let (data, response) = try await session.data(for: request)
        guard let httpResponse = response as? HTTPURLResponse else {
            logger.error("OpenAI response was not HTTP")
            throw OpenAIClientError.invalidResponse
        }

        guard (200..<300).contains(httpResponse.statusCode) else {
            let message = decodeErrorMessage(from: data)
            logger.error("OpenAI request failed", metadata: [
                "status_code": "\(httpResponse.statusCode)",
                "message": message
            ])
            throw OpenAIClientError.unexpectedStatusCode(httpResponse.statusCode, message)
        }

        let decoded: OpenAIResponsesCreateResponse
        do {
            decoded = try AtlasJSON.decoder.decode(OpenAIResponsesCreateResponse.self, from: data)
        } catch {
            logger.error("Failed to decode OpenAI response", metadata: [
                "error": error.localizedDescription,
                "payload": String(decoding: data, as: UTF8.self)
            ])
            throw error
        }
        logger.info("Received OpenAI response", metadata: [
            "response_id": decoded.id,
            "output_items": "\(decoded.output.count)"
        ])
        return decoded
    }

    private func sendRequestStreaming(
        input: [OpenAIInputItem],
        tools: [OpenAIToolDefinition]?,
        previousResponseID: String?,
        instructions: String?,
        model: String,
        onDelta: @escaping @Sendable (String) async -> Void
    ) async throws -> OpenAIResponsesCreateResponse {
        let apiKey = try fetchAPIKey()
        let requestBody = OpenAIResponsesCreateRequest(
            model: model,
            instructions: instructions ?? config.baseSystemPrompt,
            input: input,
            tools: tools,
            store: true,
            previousResponseID: previousResponseID,
            stream: true
        )

        var request = URLRequest(url: URL(string: "https://api.openai.com/v1/responses")!)
        request.httpMethod = "POST"
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        request.setValue("Bearer \(apiKey)", forHTTPHeaderField: "Authorization")
        request.httpBody = try AtlasJSON.encoder.encode(requestBody)

        let imageCount = input.filter {
            if case .message(let m) = $0 { return m.content.contains { if case .image = $0 { return true }; return false } }
            return false
        }.count
        logger.info("Sending streaming request to OpenAI — model: \(model), items: \(input.count), images: \(imageCount), tools: \(tools?.count ?? 0)")

        let (byteStream, response) = try await session.bytes(for: request)
        guard let httpResponse = response as? HTTPURLResponse else {
            logger.error("OpenAI streaming response was not HTTP")
            throw OpenAIClientError.invalidResponse
        }

        guard (200..<300).contains(httpResponse.statusCode) else {
            // Drain the error body so it can be decoded.
            var errorData = Data()
            for try await byte in byteStream { errorData.append(byte) }
            let message = decodeErrorMessage(from: errorData)
            logger.error("OpenAI streaming request failed", metadata: [
                "status_code": "\(httpResponse.statusCode)",
                "message": message
            ])
            throw OpenAIClientError.unexpectedStatusCode(httpResponse.statusCode, message)
        }

        var completedResponse: OpenAIResponsesCreateResponse?

        for try await line in byteStream.lines {
            // SSE lines are either "data: <json>" or "event: <name>" or blank.
            guard line.hasPrefix("data: ") else { continue }
            let jsonStr = String(line.dropFirst(6))
            guard jsonStr != "[DONE]" else { continue }

            guard let data = jsonStr.data(using: .utf8),
                  let event = try? AtlasJSON.decoder.decode(OpenAIStreamEvent.self, from: data) else {
                continue
            }

            switch event.type {
            case "response.output_text.delta":
                if let delta = event.delta, !delta.isEmpty {
                    await onDelta(delta)
                }
            case "response.completed":
                completedResponse = event.response
            default:
                break
            }
        }

        guard let completed = completedResponse else {
            logger.error("OpenAI streaming ended without a response.completed event")
            throw OpenAIClientError.invalidResponse
        }

        logger.info("OpenAI streaming response completed", metadata: [
            "response_id": completed.id,
            "output_items": "\(completed.output.count)"
        ])
        return completed
    }

    private func windowedMessages(_ messages: [AtlasMessage]) -> [AtlasMessage] {
        let limit = config.conversationWindowLimit
        guard limit > 0, messages.count > limit else { return messages }
        var result = Array(messages.suffix(limit))
        while let first = result.first, first.role != .user { result.removeFirst() }
        logger.info("OpenAIClient: conversation window trimmed to \(result.count)/\(messages.count) messages")
        return result
    }

    private func buildConversationInput(
        from conversation: AtlasConversation,
        attachments: [AtlasMessageAttachment] = []
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
                // Embed image attachments on the last user message
                if message.id == lastUserMessageID && !imageAttachments.isEmpty {
                    content += imageAttachments.map { .image($0.dataURI) }
                }
            case .assistant:
                role = "assistant"
                content = [.text(message.content, .outputText)]
            }

            return .message(OpenAIConversationInput(role: role, content: content))
        }
    }

    // MARK: - OpenAIQuerying conformance

    /// Send a single-turn prompt without tool use — used by MindReflectionService and SkillsEngine.
    /// Provide `model` to override the default selection; nil falls back to `config.defaultOpenAIModel`.
    public func complete(systemPrompt: String, userContent: String, model: String? = nil) async throws -> String {
        let response = try await sendRequest(
            input: [.message(OpenAIConversationInput(role: "user", text: userContent, contentType: .inputText))],
            tools: nil,
            previousResponseID: nil,
            instructions: systemPrompt,
            model: model ?? config.defaultOpenAIModel
        )
        return response.latestAssistantMessageText()
    }

    private func decodeErrorMessage(from data: Data) -> String {
        if let envelope = try? AtlasJSON.decoder.decode(OpenAIErrorEnvelope.self, from: data) {
            return envelope.error.message ?? "Unknown OpenAI error."
        }

        return String(decoding: data, as: UTF8.self)
    }

    private func fetchAPIKey() throws -> String {
        do {
            return try config.openAIAPIKey()
        } catch {
            throw OpenAIClientError.missingAPIKey
        }
    }
}

// MARK: - AtlasAIClient conformance

extension OpenAIClient: AtlasAIClient {

    /// Initial turn via the Responses API.
    public func sendTurn(
        conversation: AtlasConversation,
        tools: [AtlasToolDefinition],
        instructions: String?,
        model: String,
        attachments: [AtlasMessageAttachment]
    ) async throws -> AITurnResponse {
        let response = try await sendMessageWithTools(
            conversation: conversation,
            tools: tools,
            instructions: instructions,
            model: model,
            attachments: attachments
        )
        return AITurnResponse(
            turnID: response.id,
            assistantText: parseResponse(response),
            rawToolCalls: extractRawToolCalls(from: response)
        )
    }

    /// Streaming initial turn via the Responses API.
    public func sendTurnStreaming(
        conversation: AtlasConversation,
        tools: [AtlasToolDefinition],
        instructions: String?,
        model: String,
        attachments: [AtlasMessageAttachment],
        onDelta: @escaping @Sendable (String) async -> Void
    ) async throws -> AITurnResponse {
        let response = try await sendMessageWithToolsStreaming(
            conversation: conversation,
            tools: tools,
            instructions: instructions,
            model: model,
            attachments: attachments,
            onDelta: onDelta
        )
        return AITurnResponse(
            turnID: response.id,
            assistantText: parseResponse(response),
            rawToolCalls: extractRawToolCalls(from: response)
        )
    }

    /// Continuation turn via the Responses API using `previousTurnID` as the `previousResponseID`.
    /// The `conversation` and `toolCalls` parameters are unused here — the server holds context.
    public func continueTurn(
        previousTurnID: String,
        conversation: AtlasConversation,
        toolCalls: [AtlasToolCall],
        toolOutputs: [AIToolOutput],
        tools: [AtlasToolDefinition],
        instructions: String?,
        model: String
    ) async throws -> AITurnResponse {
        let outputs = toolOutputs.map { OpenAIToolOutput(callID: $0.callID, output: $0.output) }
        let response = try await continueResponse(
            previousResponseID: previousTurnID,
            toolOutputs: outputs,
            tools: tools,
            instructions: instructions,
            model: model
        )
        return AITurnResponse(
            turnID: response.id,
            assistantText: parseResponse(response),
            rawToolCalls: extractRawToolCalls(from: response)
        )
    }

    /// Streaming continuation via the Responses API.
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
        let outputs = toolOutputs.map { OpenAIToolOutput(callID: $0.callID, output: $0.output) }
        let response = try await continueResponseStreaming(
            previousResponseID: previousTurnID,
            toolOutputs: outputs,
            tools: tools,
            instructions: instructions,
            model: model,
            onDelta: onDelta
        )
        return AITurnResponse(
            turnID: response.id,
            assistantText: parseResponse(response),
            rawToolCalls: extractRawToolCalls(from: response)
        )
    }

    // MARK: - Private helper

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
}
