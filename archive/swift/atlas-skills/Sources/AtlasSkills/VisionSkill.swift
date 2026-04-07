import Foundation
import AtlasShared
import AtlasTools

// MARK: - VisionSkill

/// Lets the agent deeply analyse images, documents, and screenshots by making
/// direct OpenAI vision calls.  Uses the Chat Completions API with `gpt-4o`
/// so that it works independently of the Responses API pipeline.
public struct VisionSkill: AtlasSkill {
    public let manifest: SkillManifest
    public let actions: [SkillActionDefinition]

    public init() {
        self.manifest = SkillManifest(
            id: "vision",
            name: "Vision",
            version: "1.0.0",
            description: "Analyse images, documents, and screenshots from local file paths or URLs. Use this skill for detailed visual analysis, OCR-style text extraction from images, describing what is visible in a photo, or interpreting a screenshot.",
            category: .research,
            lifecycleState: .installed,
            capabilities: [.imageRead],
            requiredPermissions: [.localRead],
            requiredSecrets: ["OpenAI API key (same key used for chat)"],
            riskLevel: .low,
            trustProfile: .exactStructured,
            freshnessType: .external,
            preferredQueryTypes: [.imageRead],
            routingPriority: 72,
            restrictionsSummary: [
                "Reads local files only within approved scopes",
                "Makes outbound calls to OpenAI vision API",
                "Does not store or modify any files"
            ],
            supportsReadOnlyMode: true,
            isUserVisible: true,
            isEnabledByDefault: true,
            author: "Project Atlas",
            source: "built_in",
            tags: ["vision", "image", "ocr", "screenshot", "document"],
            intent: .generalReasoning,
            triggers: [
                .init("what is in this image", queryType: .imageRead),
                .init("analyse this image", queryType: .imageRead),
                .init("describe this photo", queryType: .imageRead),
                .init("read this screenshot", queryType: .imageRead),
                .init("extract text from image", queryType: .imageRead),
                .init("what does this show", queryType: .imageRead),
                .init("look at this file", queryType: .imageRead),
                .init("analyse screenshot", queryType: .imageRead),
                .init("read document image", queryType: .imageRead)
            ]
        )

        let imageInputSchema = AtlasToolInputSchema(
            properties: [
                "file_path": AtlasToolInputProperty(
                    type: "string",
                    description: "Absolute path to the image file on disk (JPEG, PNG, GIF, WebP, or PDF rendered as image)."
                ),
                "image_url": AtlasToolInputProperty(
                    type: "string",
                    description: "HTTPS URL of the image to analyse. Provide either file_path or image_url, not both."
                ),
                "prompt": AtlasToolInputProperty(
                    type: "string",
                    description: "What to look for or describe. Defaults to a comprehensive visual description."
                )
            ],
            additionalProperties: false
        )

        self.actions = [
            SkillActionDefinition(
                id: "vision.analyse_image",
                name: "Analyse Image",
                description: "Visually describe and analyse an image from a file path or URL. Returns a detailed description including objects, text, colours, layout, and anything notable.",
                inputSchemaSummary: "file_path OR image_url (one required); prompt (optional — defaults to full description).",
                outputSchemaSummary: "Detailed natural-language description of the image contents.",
                permissionLevel: .read,
                sideEffectLevel: .safeRead,
                preferredQueryTypes: [.imageRead],
                routingPriority: 72,
                inputSchema: imageInputSchema
            ),
            SkillActionDefinition(
                id: "vision.read_document",
                name: "Read Document",
                description: "Extract and transcribe text content from a document image (scanned PDF page, photo of text, whiteboard, etc.). Preserves structure where possible.",
                inputSchemaSummary: "file_path OR image_url (one required); prompt (optional — defaults to full text extraction).",
                outputSchemaSummary: "Transcribed text content from the document image.",
                permissionLevel: .read,
                sideEffectLevel: .safeRead,
                preferredQueryTypes: [.imageRead],
                routingPriority: 70,
                inputSchema: imageInputSchema
            ),
            SkillActionDefinition(
                id: "vision.analyse_screenshot",
                name: "Analyse Screenshot",
                description: "Interpret a screenshot — identify UI elements, read visible text, describe the application state, and answer questions about what is shown.",
                inputSchemaSummary: "file_path OR image_url (one required); prompt (optional — defaults to UI description).",
                outputSchemaSummary: "Description of the screenshot contents, UI state, and visible text.",
                permissionLevel: .read,
                sideEffectLevel: .safeRead,
                preferredQueryTypes: [.imageRead],
                routingPriority: 68,
                inputSchema: imageInputSchema
            )
        ]
    }

    // MARK: - AtlasSkill

    public func validateConfiguration(context: SkillValidationContext) async -> SkillValidationResult {
        do {
            _ = try context.config.openAIAPIKey()
            return SkillValidationResult(
                skillID: manifest.id,
                status: .passed,
                summary: "OpenAI API key is available for vision analysis."
            )
        } catch {
            return SkillValidationResult(
                skillID: manifest.id,
                status: .failed,
                summary: "No OpenAI API key found. Vision analysis requires the same key used for chat.",
                issues: ["OpenAI API key is not set in the Keychain."]
            )
        }
    }

    public func execute(
        actionID: String,
        input: AtlasToolInput,
        context: SkillExecutionContext
    ) async throws -> SkillExecutionResult {
        switch actionID {
        case "vision.analyse_image":
            return try await runVision(actionID: actionID, input: input, context: context,
                defaultPrompt: "Describe this image in detail: objects, text, colours, layout, and anything notable.")
        case "vision.read_document":
            return try await runVision(actionID: actionID, input: input, context: context,
                defaultPrompt: "Extract and transcribe all text from this document image, preserving structure where possible.")
        case "vision.analyse_screenshot":
            return try await runVision(actionID: actionID, input: input, context: context,
                defaultPrompt: "Describe this screenshot: identify the application, UI elements, visible text, and current state.")
        default:
            throw VisionSkillError.unknownAction(actionID)
        }
    }

    // MARK: - Private

    private func runVision(
        actionID: String,
        input: AtlasToolInput,
        context: SkillExecutionContext,
        defaultPrompt: String
    ) async throws -> SkillExecutionResult {
        struct VisionInput: Decodable {
            let file_path: String?
            let image_url: String?
            let prompt: String?
        }

        let params = try input.decode(VisionInput.self)
        let prompt = params.prompt.flatMap { $0.isEmpty ? nil : $0 } ?? defaultPrompt

        let imageURI: String
        if let path = params.file_path, !path.isEmpty {
            let url = URL(fileURLWithPath: path)
            let data = try Data(contentsOf: url)
            let base64 = data.base64EncodedString()
            let mime = mimeType(for: path)
            imageURI = "data:\(mime);base64,\(base64)"
        } else if let urlStr = params.image_url, !urlStr.isEmpty {
            imageURI = urlStr
        } else {
            throw VisionSkillError.missingInput
        }

        let apiKey = try context.config.openAIAPIKey()
        let analysis = try await callVisionAPI(prompt: prompt, imageURI: imageURI, apiKey: apiKey)

        context.logger.info("Vision analysis completed", metadata: [
            "action": actionID,
            "characters": "\(analysis.count)"
        ])

        return SkillExecutionResult(
            skillID: manifest.id,
            actionID: actionID,
            output: analysis,
            success: true,
            summary: "Vision analysis returned \(analysis.count) characters."
        )
    }

    /// Makes a direct OpenAI Chat Completions call with a vision-capable model.
    private func callVisionAPI(prompt: String, imageURI: String, apiKey: String) async throws -> String {
        struct ChatRequest: Encodable {
            let model = "gpt-4o"
            let messages: [ChatMessage]
            let max_tokens = 2048

            struct ChatMessage: Encodable {
                let role: String
                let content: [ContentItem]
            }
            struct ContentItem: Encodable {
                let type: String
                let text: String?
                let image_url: ImageURL?
                struct ImageURL: Encodable {
                    let url: String
                    let detail = "auto"
                }
            }
        }

        let body = ChatRequest(messages: [
            ChatRequest.ChatMessage(
                role: "user",
                content: [
                    ChatRequest.ContentItem(type: "text", text: prompt, image_url: nil),
                    ChatRequest.ContentItem(type: "image_url", text: nil, image_url: .init(url: imageURI))
                ]
            )
        ])

        var request = URLRequest(url: URL(string: "https://api.openai.com/v1/chat/completions")!)
        request.httpMethod = "POST"
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        request.setValue("Bearer \(apiKey)", forHTTPHeaderField: "Authorization")
        request.httpBody = try JSONEncoder().encode(body)

        let (data, response) = try await URLSession.shared.data(for: request)
        guard let http = response as? HTTPURLResponse, (200..<300).contains(http.statusCode) else {
            let message = (try? JSONDecoder().decode(VisionErrorEnvelope.self, from: data))?.error.message
                ?? String(decoding: data, as: UTF8.self)
            throw VisionSkillError.apiError(message)
        }

        struct ChatResponse: Decodable {
            let choices: [Choice]
            struct Choice: Decodable {
                let message: Message
                struct Message: Decodable { let content: String? }
            }
        }

        let decoded = try JSONDecoder().decode(ChatResponse.self, from: data)
        return decoded.choices.first?.message.content ?? "No response from vision API."
    }

    private func mimeType(for path: String) -> String {
        switch (path as NSString).pathExtension.lowercased() {
        case "jpg", "jpeg": return "image/jpeg"
        case "png":         return "image/png"
        case "gif":         return "image/gif"
        case "webp":        return "image/webp"
        case "pdf":         return "application/pdf"
        default:            return "application/octet-stream"
        }
    }
}

// MARK: - Error types

enum VisionSkillError: LocalizedError {
    case missingInput
    case unknownAction(String)
    case apiError(String)

    var errorDescription: String? {
        switch self {
        case .missingInput:
            return "Provide either file_path or image_url."
        case .unknownAction(let id):
            return "Unknown VisionSkill action: \(id)."
        case .apiError(let msg):
            return "OpenAI vision API error: \(msg)"
        }
    }
}

private struct VisionErrorEnvelope: Decodable {
    let error: VisionAPIError
    struct VisionAPIError: Decodable { let message: String? }
}
