import Foundation
import AtlasShared

public protocol ActiveImageProviderManaging: Sendable {
    func activeProviderType() -> ImageProviderType?
    func provider(for providerType: ImageProviderType) throws -> any ImageProvider
    func activeProvider() throws -> any ImageProvider
    func validate(providerType: ImageProviderType) async -> ImageProviderValidation
    func validateActiveProvider() async -> ImageProviderValidation
}

public struct ActiveImageProviderManager: ActiveImageProviderManaging, Sendable {
    private let config: AtlasConfig
    private let artifactStore: ImageArtifactStore
    private let session: URLSession?

    public init(
        config: AtlasConfig = AtlasConfig(),
        artifactStore: ImageArtifactStore? = nil,
        session: URLSession? = nil
    ) {
        self.config = config
        self.artifactStore = artifactStore ?? (try? ImageArtifactStore()) ?? Self.temporaryArtifactStore()
        self.session = session
    }

    public func activeProviderType() -> ImageProviderType? {
        config.activeImageProvider
    }

    public func provider(for providerType: ImageProviderType) throws -> any ImageProvider {
        switch providerType {
        case .openAI:
            let key = try requiredKey(for: .openAI)
            return OpenAIImageProvider(apiKey: key, artifactStore: artifactStore, session: session)
        case .googleNanoBanana:
            let key = try requiredKey(for: .googleNanoBanana)
            return GoogleNanoBananaProvider(apiKey: key, artifactStore: artifactStore, session: session)
        }
    }

    public func activeProvider() throws -> any ImageProvider {
        guard let providerType = activeProviderType() else {
            throw ImageGenerationError.providerNotConfigured
        }
        return try provider(for: providerType)
    }

    public func validate(providerType: ImageProviderType) async -> ImageProviderValidation {
        do {
            let resolvedProvider = try provider(for: providerType)
            return await resolvedProvider.validateConfiguration()
        } catch let error as ImageGenerationError {
            return validationFailure(for: providerType, error: error)
        } catch {
            return ImageProviderValidation(
                providerType: providerType,
                status: .warning,
                summary: "Validation could not be completed.",
                issues: [error.localizedDescription]
            )
        }
    }

    public func validateActiveProvider() async -> ImageProviderValidation {
        guard let providerType = activeProviderType() else {
            return ImageProviderValidation(
                providerType: .openAI,
                status: .warning,
                summary: "No active image provider is selected.",
                issues: ["Choose and validate an image provider in Settings."]
            )
        }
        return await validate(providerType: providerType)
    }

    private func requiredKey(for providerType: ImageProviderType) throws -> String {
        let key: String
        switch providerType {
        case .openAI:
            key = try config.openAIImageAPIKey()
        case .googleNanoBanana:
            key = try config.googleImageAPIKey()
        }

        let trimmed = key.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else {
            throw ImageGenerationError.providerCredentialMissing(providerType)
        }
        return trimmed
    }

    private func validationFailure(for providerType: ImageProviderType, error: ImageGenerationError) -> ImageProviderValidation {
        switch error {
        case .providerCredentialMissing:
            return ImageProviderValidation(
                providerType: providerType,
                status: .warning,
                summary: "\(providerType.title) is not configured yet.",
                issues: ["Store an API key in Settings before validating \(providerType.title)."]
            )
        case .providerNotConfigured, .invalidProviderSelection:
            return ImageProviderValidation(
                providerType: providerType,
                status: .warning,
                summary: "No active provider is configured.",
                issues: ["Choose a provider and validate it in Settings."]
            )
        default:
            return ImageProviderValidation(
                providerType: providerType,
                status: .warning,
                summary: "Validation could not be completed.",
                issues: [error.localizedDescription]
            )
        }
    }

    private static func temporaryArtifactStore() -> ImageArtifactStore {
        let temporaryRoot = FileManager.default.temporaryDirectory
            .appendingPathComponent("ProjectAtlas-ImageArtifacts", isDirectory: true)
        return (try? ImageArtifactStore(rootDirectory: temporaryRoot)) ?? {
            fatalError("Unable to create a fallback image artifact store.")
        }()
    }
}
