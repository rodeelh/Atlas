import Foundation
import AtlasShared
import AtlasLogging

public enum GeminiClientError: LocalizedError {
    case missingAPIKey
    case invalidResponse
    case unexpectedStatusCode(Int, String)

    public var errorDescription: String? {
        switch self {
        case .missingAPIKey:
            return "No Gemini API key found in the macOS Keychain."
        case .invalidResponse:
            return "The Gemini API returned an invalid response."
        case .unexpectedStatusCode(let code, let message):
            return "The Gemini API returned status \(code): \(message)"
        }
    }
}

public final class GeminiClient: Sendable, AtlasAIClient {
    private let session: URLSession
    private let config: AtlasConfig
    private let logger = AtlasLogger.network

    private static let baseURL = "https://generativelanguage.googleapis.com/v1beta/models"

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
        let contents = buildContents(from: conversation, attachments: attachments)
        return try await request(
            model: model,
            system: instructions ?? config.baseSystemPrompt,
            contents: contents,
            tools: tools,
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
        let contents = buildContents(from: conversation, attachments: attachments)
        return try await request(
            model: model,
            system: instructions ?? config.baseSystemPrompt,
            contents: contents,
            tools: tools,
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
        let contents = buildContinuationContents(
            conversation: conversation,
            toolCalls: toolCalls,
            toolOutputs: toolOutputs
        )
        return try await request(
            model: model,
            system: instructions ?? config.baseSystemPrompt,
            contents: contents,
            tools: tools,
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
        let contents = buildContinuationContents(
            conversation: conversation,
            toolCalls: toolCalls,
            toolOutputs: toolOutputs
        )
        return try await request(
            model: model,
            system: instructions ?? config.baseSystemPrompt,
            contents: contents,
            tools: tools,
            stream: true,
            onDelta: onDelta
        )
    }

    public func validateCredential() async throws {
        try await validateCredential(model: nil)
    }

    public func validateCredential(model: String?) async throws {
        let key = try fetchAPIKey()
        let resolvedModel = model ?? AIProvider.gemini.defaultPrimaryModel
        let url = endpointURL(model: resolvedModel, stream: false, apiKey: key)
        var req = URLRequest(url: url)
        req.httpMethod = "POST"
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        let body = GeminiGenerateContentRequest(
            systemInstruction: nil,
            contents: [GeminiContent(role: "user", parts: [.textPart(".")])],
            tools: nil
        )
        req.httpBody = try AtlasJSON.encoder.encode(body)
        let (data, response) = try await session.data(for: req)
        guard let http = response as? HTTPURLResponse else { throw GeminiClientError.invalidResponse }
        guard (200..<300).contains(http.statusCode) else {
            throw GeminiClientError.unexpectedStatusCode(http.statusCode, decodeError(data))
        }
        logger.info("Gemini credential validated", metadata: ["model": resolvedModel])
    }

    // MARK: - OpenAIQuerying

    public func complete(systemPrompt: String, userContent: String, model: String? = nil) async throws -> String {
        let resolvedModel = model ?? config.activeAIProvider.defaultFastModel
        let contents = [GeminiContent(role: "user", parts: [.textPart(userContent)])]
        let response = try await request(
            model: resolvedModel,
            system: systemPrompt,
            contents: contents,
            tools: [],
            stream: false,
            onDelta: nil
        )
        return response.assistantText
    }

    // MARK: - Content building

    private func buildContents(
        from conversation: AtlasConversation,
        attachments: [AtlasMessageAttachment]
    ) -> [GeminiContent] {
        // TODO: Add inline image support for Gemini (Vision skill rebuild).
        // GeminiPart needs an `inlineData` field ({ mimeType, data }) and a corresponding
        // factory method. Then embed image attachments on the last user message here,
        // mirroring the pattern used in OpenAIClient and AnthropicClient.
        // See: https://ai.google.dev/api/generate-content#v1beta.Part

        // Gemini uses "user" and "model" roles
        var contents: [GeminiContent] = []
        for message in windowedMessages(conversation.messages) {
            switch message.role {
            case .system:
                continue // system goes in systemInstruction
            case .user:
                contents.append(GeminiContent(role: "user", parts: [.textPart(message.content)]))
            case .assistant:
                contents.append(GeminiContent(role: "model", parts: [.textPart(message.content)]))
            case .tool:
                // Plain text fallback for historical tool results
                contents.append(GeminiContent(role: "user", parts: [.textPart(message.content)]))
            }
        }
        // Gemini requires first content to be "user"
        while contents.first?.role == "model" { contents.removeFirst() }
        return contents
    }

    /// Reconstruct proper Gemini contents for a continuation turn.
    ///
    /// Conversation at this point: [...prior..., assistant_text, tool_results...]
    /// Needed by Gemini: [...prior..., {model: text + functionCall parts}, {user: functionResponse parts}]
    private func buildContinuationContents(
        conversation: AtlasConversation,
        toolCalls: [AtlasToolCall],
        toolOutputs: [AIToolOutput]
    ) -> [GeminiContent] {
        var allMessages = windowedMessages(conversation.messages)

        // Strip trailing tool messages
        while allMessages.last?.role == .tool { allMessages.removeLast() }

        // Pop last assistant message
        let assistantText: String
        if allMessages.last?.role == .assistant {
            assistantText = allMessages.removeLast().content
        } else {
            assistantText = ""
        }

        // Rebuild prior turns
        var contents: [GeminiContent] = []
        for message in allMessages {
            switch message.role {
            case .system:    continue
            case .user:      contents.append(GeminiContent(role: "user", parts: [.textPart(message.content)]))
            case .assistant: contents.append(GeminiContent(role: "model", parts: [.textPart(message.content)]))
            case .tool:      contents.append(GeminiContent(role: "user", parts: [.textPart(message.content)]))
            }
        }

        // Rebuild model turn: text + functionCall parts
        var modelParts: [GeminiPart] = []
        if !assistantText.isEmpty { modelParts.append(.textPart(assistantText)) }
        for tc in toolCalls {
            guard tc.openAICallID != nil else { continue }
            let args = GeminiJSONValue.dict(fromJSONString: tc.argumentsJSON)
            modelParts.append(.functionCallPart(name: tc.toolName, args: args))
        }
        if !modelParts.isEmpty {
            contents.append(GeminiContent(role: "model", parts: modelParts))
        }

        // User turn: functionResponse parts
        // Match outputs to tool calls by callID → tool name
        let callIDToName = Dictionary(uniqueKeysWithValues: toolCalls.compactMap { tc -> (String, String)? in
            guard let id = tc.openAICallID else { return nil }
            return (id, tc.toolName)
        })
        let responseParts = toolOutputs.compactMap { output -> GeminiPart? in
            guard let name = callIDToName[output.callID] else { return nil }
            return .functionResponsePart(name: name, response: output.output)
        }
        if !responseParts.isEmpty {
            contents.append(GeminiContent(role: "user", parts: responseParts))
        }

        while contents.first?.role == "model" { contents.removeFirst() }
        return contents
    }

    // MARK: - HTTP

    private func request(
        model: String,
        system: String?,
        contents: [GeminiContent],
        tools: [AtlasToolDefinition],
        stream: Bool,
        onDelta: (@Sendable (String) async -> Void)?
    ) async throws -> AITurnResponse {
        let apiKey = try fetchAPIKey()
        let url = endpointURL(model: model, stream: stream, apiKey: apiKey)

        let systemInstruction = system.map { text in
            GeminiContent(role: nil, parts: [.textPart(text)])
        }
        let geminiTools = tools.isEmpty ? nil : [GeminiTools(
            functionDeclarations: tools.map(buildFunctionDeclaration(from:))
        )]
        let body = GeminiGenerateContentRequest(
            systemInstruction: systemInstruction,
            contents: contents,
            tools: geminiTools
        )

        var req = URLRequest(url: url)
        req.httpMethod = "POST"
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        req.httpBody = try AtlasJSON.encoder.encode(body)

        logger.info("Gemini request — model: \(model), contents: \(contents.count), tools: \(tools.count), streaming: \(stream)")

        if stream, let onDelta {
            return try await requestStreaming(req: req, onDelta: onDelta)
        } else {
            return try await requestBlocking(req: req)
        }
    }

    private func requestBlocking(req: URLRequest) async throws -> AITurnResponse {
        let (data, response) = try await session.data(for: req)
        guard let http = response as? HTTPURLResponse else { throw GeminiClientError.invalidResponse }
        guard (200..<300).contains(http.statusCode) else {
            let msg = decodeError(data)
            logger.error("Gemini request failed", metadata: ["status": "\(http.statusCode)", "message": msg])
            throw GeminiClientError.unexpectedStatusCode(http.statusCode, msg)
        }
        let decoded = try AtlasJSON.decoder.decode(GeminiGenerateContentResponse.self, from: data)
        return extractTurnResponse(from: decoded)
    }

    private func requestStreaming(
        req: URLRequest,
        onDelta: @escaping @Sendable (String) async -> Void
    ) async throws -> AITurnResponse {
        let (byteStream, response) = try await session.bytes(for: req)
        guard let http = response as? HTTPURLResponse else { throw GeminiClientError.invalidResponse }
        guard (200..<300).contains(http.statusCode) else {
            var data = Data()
            for try await byte in byteStream { data.append(byte) }
            throw GeminiClientError.unexpectedStatusCode(http.statusCode, decodeError(data))
        }

        var assistantText = ""
        var toolCalls: [AIRawToolCall] = []

        for try await line in byteStream.lines {
            guard line.hasPrefix("data: ") else { continue }
            let json = String(line.dropFirst(6))
            guard !json.isEmpty,
                  let data = json.data(using: .utf8),
                  let chunk = try? AtlasJSON.decoder.decode(GeminiStreamChunk.self, from: data)
            else { continue }

            let turnResponse = extractTurnResponse(from: GeminiGenerateContentResponse(candidates: chunk.candidates))
            if !turnResponse.assistantText.isEmpty {
                assistantText += turnResponse.assistantText
                await onDelta(turnResponse.assistantText)
            }
            toolCalls.append(contentsOf: turnResponse.rawToolCalls)
        }

        return AITurnResponse(turnID: UUID().uuidString, assistantText: assistantText, rawToolCalls: toolCalls)
    }

    // MARK: - Helpers

    private func extractTurnResponse(from response: GeminiGenerateContentResponse) -> AITurnResponse {
        guard let candidate = response.candidates?.first,
              let content = candidate.content
        else {
            return AITurnResponse(turnID: UUID().uuidString, assistantText: "", rawToolCalls: [])
        }

        var text = ""
        var toolCalls: [AIRawToolCall] = []

        for part in content.parts {
            if let t = part.text { text += t }
            if let fc = part.functionCall {
                let argsJSON: String
                if let data = try? JSONEncoder().encode(fc.args),
                   let str  = String(data: data, encoding: .utf8) {
                    argsJSON = str
                } else {
                    argsJSON = "{}"
                }
                // Gemini doesn't return a call ID — synthesize one
                toolCalls.append(AIRawToolCall(
                    name: fc.name,
                    argumentsJSON: argsJSON,
                    callID: "gemini-\(UUID().uuidString)"
                ))
            }
        }
        return AITurnResponse(turnID: UUID().uuidString, assistantText: text, rawToolCalls: toolCalls)
    }

    private func buildFunctionDeclaration(from definition: AtlasToolDefinition) -> GeminiFunctionDeclaration {
        let props = definition.inputSchema.properties.mapValues { p in
            let itemsSchema: GeminiItemsSchema? = p.type.lowercased() == "array"
                ? GeminiItemsSchema(type: p.items?.type ?? "string")
                : nil
            return GeminiPropertySchema(type: p.type, description: p.description, items: itemsSchema)
        }
        return GeminiFunctionDeclaration(
            name: definition.name,
            description: definition.description,
            parameters: GeminiParametersSchema(properties: props, required: definition.inputSchema.required)
        )
    }

    private func windowedMessages(_ messages: [AtlasMessage]) -> [AtlasMessage] {
        let limit = config.conversationWindowLimit
        guard limit > 0, messages.count > limit else { return messages }
        var result = Array(messages.suffix(limit))
        while let first = result.first, first.role != .user { result.removeFirst() }
        logger.info("GeminiClient: conversation window trimmed to \(result.count)/\(messages.count) messages")
        return result
    }

    private func endpointURL(model: String, stream: Bool, apiKey: String) -> URL {
        let action = stream ? "streamGenerateContent" : "generateContent"
        var components = URLComponents(string: "\(Self.baseURL)/\(model):\(action)")!
        components.queryItems = [URLQueryItem(name: "key", value: apiKey)]
        if stream {
            components.queryItems?.append(URLQueryItem(name: "alt", value: "sse"))
        }
        return components.url!
    }

    private func fetchAPIKey() throws -> String {
        do { return try config.geminiAPIKey() } catch { throw GeminiClientError.missingAPIKey }
    }

    private func decodeError(_ data: Data) -> String {
        if let env = try? AtlasJSON.decoder.decode(GeminiErrorEnvelope.self, from: data) {
            return env.error.message ?? "Unknown Gemini error."
        }
        return String(decoding: data, as: UTF8.self)
    }
}
