import Foundation
import AtlasShared
import AtlasTools

public struct ImageGenerationSkill: AtlasSkill {
    public let manifest: SkillManifest
    public let actions: [SkillActionDefinition]

    private let providerManagerFactory: @Sendable (AtlasConfig) -> any ActiveImageProviderManaging

    public init(
        providerManagerFactory: (@Sendable (AtlasConfig) -> any ActiveImageProviderManaging)? = nil
    ) {
        self.providerManagerFactory = providerManagerFactory ?? { config in
            ActiveImageProviderManager(config: config)
        }
        self.manifest = SkillManifest(
            id: "image-generation",
            name: "Image Generation",
            version: "1.0.0",
            description: "Generate and edit images with the single active provider configured in Settings. Never ask the user to choose a provider in chat and never switch providers per request.",
            category: .creative,
            lifecycleState: .installed,
            capabilities: [
                .imageGenerate,
                .imageEdit
            ],
            requiredPermissions: [
                .draftWrite
            ],
            requiredSecrets: [
                "One active provider key (OpenAI Images or Google Nano Banana)"
            ],
            riskLevel: .medium,
            trustProfile: .exactStructured,
            freshnessType: .external,
            preferredQueryTypes: [
                .imageGenerate,
                .imageEdit
            ],
            routingPriority: 78,
            restrictionsSummary: [
                "Uses exactly one active provider selected in Settings",
                "API keys are stored only in macOS Keychain",
                "Provider cannot be chosen per request",
                "Generated images are saved locally in Application Support",
                "Atlas saves artifacts only inside its managed image store"
            ],
            supportsReadOnlyMode: false,
            isUserVisible: true,
            isEnabledByDefault: true,
            author: "Project Atlas",
            source: "built_in",
            tags: ["image", "creative", "provider"],
            intent: .generalReasoning,
            triggers: [
                .init("generate an illustration", queryType: .imageGenerate),
                .init("generate illustration", queryType: .imageGenerate),
                .init("create artwork", queryType: .imageGenerate),
                .init("design a poster", queryType: .imageGenerate),
                .init("generate image", queryType: .imageGenerate),
                .init("create image", queryType: .imageGenerate),
                .init("make an image", queryType: .imageGenerate),
                .init("make image", queryType: .imageGenerate),
                .init("create icon", queryType: .imageGenerate),
                .init("create logo", queryType: .imageGenerate),
                .init("create art", queryType: .imageGenerate),
                .init("app icon", queryType: .imageGenerate),
                .init("edit image", queryType: .imageEdit),
                .init("redesign visual", queryType: .imageEdit),
                .init("update this image", queryType: .imageEdit)
            ]
        )
        self.actions = [
            SkillActionDefinition(
                id: "image.generate",
                name: "Generate Image",
                description: "Generate one or more images with the single active provider already configured in Settings. Do not ask the user which provider to use and ignore provider requests made in chat.",
                inputSchemaSummary: "prompt is required; size, quality, and styleHint are optional.",
                outputSchemaSummary: "Provider, prompt, artifact count, saved image references, and a metadata summary.",
                permissionLevel: .read,
                sideEffectLevel: .draftWrite,

                preferredQueryTypes: [.imageGenerate],
                routingPriority: 60,
                inputSchema: AtlasToolInputSchema(
                    properties: [
                        "prompt": AtlasToolInputProperty(type: "string", description: "Image generation prompt."),
                        "size": AtlasToolInputProperty(type: "string", description: "Optional size such as 1024x1024, 1536x1024, or 1024x1536."),
                        "quality": AtlasToolInputProperty(type: "string", description: "Optional quality hint such as standard or high."),
                        "styleHint": AtlasToolInputProperty(type: "string", description: "Optional style hint appended to the prompt.")
                    ],
                    required: ["prompt"]
                )
            ),
            SkillActionDefinition(
                id: "image.edit",
                name: "Edit Image",
                description: "Edit an existing local image with the single active provider already configured in Settings when supported. Do not ask the user which provider to use and ignore provider requests made in chat.",
                inputSchemaSummary: "prompt and inputImageReference are required; size and quality are optional.",
                outputSchemaSummary: "Provider, prompt, artifact count, saved image references, and a metadata summary.",
                permissionLevel: .read,
                sideEffectLevel: .draftWrite,

                preferredQueryTypes: [.imageEdit],
                routingPriority: 60,
                inputSchema: AtlasToolInputSchema(
                    properties: [
                        "prompt": AtlasToolInputProperty(type: "string", description: "Editing instruction prompt."),
                        "inputImageReference": AtlasToolInputProperty(type: "string", description: "Absolute file path or file URL for the input image."),
                        "size": AtlasToolInputProperty(type: "string", description: "Optional output size such as 1024x1024, 1536x1024, or 1024x1536."),
                        "quality": AtlasToolInputProperty(type: "string", description: "Optional quality hint such as standard or high.")
                    ],
                    required: ["prompt", "inputImageReference"]
                )
            )
        ]
    }

    public func validateConfiguration(context: SkillValidationContext) async -> SkillValidationResult {
        let providerManager = providerManagerFactory(context.config)
        let validation = await providerManager.validateActiveProvider()

        let summary: String
        if let active = providerManager.activeProviderType() {
            summary = "\(active.title): \(validation.summary)"
        } else {
            summary = "Choose an active image provider in Settings, then validate it before Atlas generates images."
        }

        return SkillValidationResult(
            skillID: manifest.id,
            status: validation.status,
            summary: summary,
            issues: validation.issues,
            validatedAt: .now
        )
    }

    public func execute(
        actionID: String,
        input: AtlasToolInput,
        context: SkillExecutionContext
    ) async throws -> SkillExecutionResult {
        switch actionID {
        case "image.generate":
            return try await generateImage(input: input, context: context)
        case "image.edit":
            return try await editImage(input: input, context: context)
        default:
            throw AtlasToolError.invalidInput("The action '\(actionID)' is not supported by Image Generation.")
        }
    }

    private func generateImage(
        input: AtlasToolInput,
        context: SkillExecutionContext
    ) async throws -> SkillExecutionResult {
        let payload = try input.decode(ImageGenerateInput.self)
        let request = try normalizedGenerateRequest(from: payload)
        let providerManager = providerManagerFactory(context.config)
        let provider = try providerManager.activeProvider()

        context.logger.info("Executing image generation request", metadata: [
            "skill_id": manifest.id,
            "action_id": "image.generate",
            "provider": provider.displayName,
            "prompt_summary": summarize(payload.prompt),
            "quality": request.quality ?? "default",
            "size": request.size
        ])

        let output = try await provider.generateImage(request: request)
        return SkillExecutionResult(
            skillID: manifest.id,
            actionID: "image.generate",
            output: try encode(output),
            summary: output.metadataSummary,
            metadata: [
                "provider": output.providerUsed,
                "image_count": String(output.imageCount)
            ]
        )
    }

    private func editImage(
        input: AtlasToolInput,
        context: SkillExecutionContext
    ) async throws -> SkillExecutionResult {
        let payload = try input.decode(ImageEditInput.self)
        let providerManager = providerManagerFactory(context.config)
        let provider = try providerManager.activeProvider()
        guard provider.supportsEdit else {
            throw ImageGenerationError.editNotSupported(provider.providerID)
        }

        let request = try normalizedEditRequest(from: payload)
        context.logger.info("Executing image edit request", metadata: [
            "skill_id": manifest.id,
            "action_id": "image.edit",
            "provider": provider.displayName,
            "prompt_summary": summarize(payload.prompt),
            "image_reference": summarizePath(request.inputImageURL.path)
        ])

        let output = try await provider.editImage(request: request)
        return SkillExecutionResult(
            skillID: manifest.id,
            actionID: "image.edit",
            output: try encode(output),
            summary: output.metadataSummary,
            metadata: [
                "provider": output.providerUsed,
                "image_count": String(output.imageCount)
            ]
        )
    }

    private func normalizedGenerateRequest(from input: ImageGenerateInput) throws -> ImageProviderGenerateRequest {
        let prompt = input.prompt.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !prompt.isEmpty else {
            throw ImageGenerationError.promptRequired
        }

        return ImageProviderGenerateRequest(
            prompt: prompt,
            size: try normalizedSize(input.size),
            quality: normalizedQuality(input.quality),
            styleHint: normalizedOptionalText(input.styleHint)
        )
    }

    private func normalizedEditRequest(from input: ImageEditInput) throws -> ImageProviderEditRequest {
        let prompt = input.prompt.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !prompt.isEmpty else {
            throw ImageGenerationError.promptRequired
        }

        let imageURL = try resolveInputImageURL(input.inputImageReference)
        return ImageProviderEditRequest(
            prompt: prompt,
            inputImageURL: imageURL,
            size: try normalizedSize(input.size),
            quality: normalizedQuality(input.quality)
        )
    }

    private func normalizedSize(_ value: String?) throws -> String {
        let candidate = normalizedOptionalText(value) ?? "1024x1024"
        let supported = [
            "1024x1024",
            "1536x1024",
            "1024x1536"
        ]
        guard supported.contains(candidate) else {
            throw ImageGenerationError.invalidSize(candidate)
        }
        return candidate
    }

    private func normalizedQuality(_ value: String?) -> String? {
        guard let candidate = normalizedOptionalText(value)?.lowercased() else {
            return nil
        }
        switch candidate {
        case "standard", "high":
            return candidate
        default:
            return nil
        }
    }

    private func normalizedOptionalText(_ value: String?) -> String? {
        guard let trimmed = value?.trimmingCharacters(in: .whitespacesAndNewlines), !trimmed.isEmpty else {
            return nil
        }
        return trimmed
    }

    private func resolveInputImageURL(_ reference: String) throws -> URL {
        let trimmed = reference.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else {
            throw ImageGenerationError.inputImageMissing(reference)
        }

        let url: URL
        if trimmed.lowercased().hasPrefix("file://"), let parsed = URL(string: trimmed), parsed.isFileURL {
            url = parsed
        } else {
            url = URL(fileURLWithPath: trimmed)
        }

        let standardizedURL = url.standardizedFileURL
        var isDirectory: ObjCBool = false
        guard FileManager.default.fileExists(atPath: standardizedURL.path, isDirectory: &isDirectory) else {
            throw ImageGenerationError.inputImageMissing(standardizedURL.path)
        }
        guard !isDirectory.boolValue else {
            throw ImageGenerationError.inputImageUnreadable(standardizedURL.path)
        }
        guard FileManager.default.isReadableFile(atPath: standardizedURL.path) else {
            throw ImageGenerationError.inputImageUnreadable(standardizedURL.path)
        }
        return standardizedURL
    }

    private func encode<T: Encodable>(_ value: T) throws -> String {
        String(decoding: try AtlasJSON.encoder.encode(value), as: UTF8.self)
    }

    private func summarize(_ value: String) -> String {
        let trimmed = value.trimmingCharacters(in: .whitespacesAndNewlines)
        guard trimmed.count > 120 else { return trimmed }
        return String(trimmed.prefix(117)) + "..."
    }

    private func summarizePath(_ value: String) -> String {
        let url = URL(fileURLWithPath: value)
        let parent = url.deletingLastPathComponent().lastPathComponent
        if parent.isEmpty {
            return url.lastPathComponent
        }
        return "\(parent)/\(url.lastPathComponent)"
    }
}
