import Foundation
import AtlasLogging
import AtlasNetwork
import AtlasShared

public enum AtlasCredentialKind: String, Identifiable, CaseIterable {
    case openAI
    case anthropic
    case gemini
    case lmStudio
    case telegram
    case discord
    case slackBotToken
    case slackAppToken
    case openAIImage
    case googleNanoBananaImage
    case braveSearch

    public var id: String { rawValue }

    var title: String {
        switch self {
        case .openAI:             return "OpenAI"
        case .anthropic:          return "Anthropic"
        case .gemini:             return "Gemini"
        case .lmStudio:           return "LM Studio"
        case .telegram:           return "Telegram"
        case .discord:            return "Discord"
        case .slackBotToken:      return "Slack Bot Token"
        case .slackAppToken:      return "Slack App Token"
        case .openAIImage:        return "OpenAI Images"
        case .googleNanoBananaImage: return "Google Nano Banana"
        case .braveSearch:        return "Brave Search"
        }
    }

    var fieldTitle: String {
        switch self {
        case .openAI, .anthropic, .gemini, .lmStudio, .openAIImage, .googleNanoBananaImage, .braveSearch:
            return "API Key"
        case .telegram, .discord, .slackBotToken, .slackAppToken:
            return "Token"
        }
    }

    var updateActionTitle: String {
        switch self {
        case .openAI, .anthropic, .gemini, .lmStudio, .openAIImage, .googleNanoBananaImage, .braveSearch:
            return "Update Key"
        case .telegram, .discord, .slackBotToken, .slackAppToken:
            return "Update Token"
        }
    }
}

public enum CredentialValidationState: Equatable {
    case notConfigured
    case notValidated
    case validating
    case connected
    case invalid(String)
    case validationFailed(String)
    case keychainError(String)

    var statusLabel: String {
        switch self {
        case .notConfigured:   return "Not Configured"
        case .notValidated:    return "Not Validated"
        case .validating:      return "Validating…"
        case .connected:       return "Connected"
        case .invalid:         return "Invalid"
        case .validationFailed: return "Validation Failed"
        case .keychainError:   return "Keychain Error"
        }
    }

    var detail: String? {
        switch self {
        case .invalid(let detail), .validationFailed(let detail), .keychainError(let detail): return detail
        default: return nil
        }
    }
}

public enum CredentialAvailability: Equatable {
    case configured
    case missing
    case keychainError(String)

    var isConfigured: Bool {
        if case .configured = self { return true }
        return false
    }
}

protocol AtlasCredentialManaging: Sendable {
    func availability(_ kind: AtlasCredentialKind) -> CredentialAvailability
    func isConfigured(_ kind: AtlasCredentialKind) -> Bool
    func store(_ secret: String, for kind: AtlasCredentialKind) throws
    func clear(_ kind: AtlasCredentialKind) throws
    func validate(_ kind: AtlasCredentialKind) async throws
}

struct AtlasCredentialManager: AtlasCredentialManaging, Sendable {
    private let config: AtlasConfig
    private let store: any CredentialStore
    private let logger: AtlasLogger

    init(
        config: AtlasConfig,
        store: any CredentialStore = SecretBackendFactory.defaultCredentialStore(),
        logger: AtlasLogger = .security
    ) {
        self.config = config
        self.store = store
        self.logger = logger
    }

    func availability(_ kind: AtlasCredentialKind) -> CredentialAvailability {
        let target = target(for: kind)

        do {
            let secret = try store.readSecret(service: target.service, account: target.account)
            return secret.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty ? .missing : .configured
        } catch let error as KeychainSecretStoreError {
            switch error {
            case .secretNotFound:
                return .missing
            case .invalidSecretEncoding, .osStatus:
                return .keychainError(error.localizedDescription)
            }
        } catch {
            return .keychainError(error.localizedDescription)
        }
    }

    func isConfigured(_ kind: AtlasCredentialKind) -> Bool {
        availability(kind).isConfigured
    }

    func store(_ secret: String, for kind: AtlasCredentialKind) throws {
        let trimmed = secret.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else {
            throw CredentialManagementError.emptySecret
        }
        let target = target(for: kind)
        try store.storeSecret(trimmed, service: target.service, account: target.account)
        logger.info("\(kind.title) credential updated")
    }

    func clear(_ kind: AtlasCredentialKind) throws {
        let target = target(for: kind)
        try store.deleteSecret(service: target.service, account: target.account)
        logger.info("\(kind.title) credential cleared")
    }

    func validate(_ kind: AtlasCredentialKind) async throws {
        switch kind {
        case .openAI:
            let model = await resolvedValidationModel(for: .openAI)
            let client = OpenAIClient(config: config)
            try await client.validateCredential(model: model)
            logger.info("OpenAI validation succeeded", metadata: ["model": model ?? "auto"])
        case .anthropic:
            let model = await resolvedValidationModel(for: .anthropic)
            let client = AnthropicClient(config: config)
            try await client.validateCredential(model: model)
            logger.info("Anthropic validation succeeded", metadata: ["model": model ?? "auto"])
        case .gemini:
            let model = await resolvedValidationModel(for: .gemini)
            let client = GeminiClient(config: config)
            try await client.validateCredential(model: model)
            logger.info("Gemini validation succeeded", metadata: ["model": model ?? "auto"])
        case .lmStudio:
            let model = await resolvedValidationModel(for: .lmStudio)
            let client = LMStudioClient(config: config)
            try await client.validateCredential(model: model)
            logger.info("LM Studio validation succeeded", metadata: ["model": model ?? "auto"])
        case .telegram:
            let client = TelegramClient(config: config)
            _ = try await client.getMe()
            logger.info("Telegram validation succeeded")
        case .discord:
            let client = DiscordClient(config: config)
            _ = try await client.getCurrentUser()
            logger.info("Discord validation succeeded")
        case .slackBotToken, .slackAppToken,
             .openAIImage, .googleNanoBananaImage, .braveSearch:
            throw CredentialManagementError.unsupportedValidation
        }
    }

    private func resolvedValidationModel(for provider: AIProvider) async -> String? {
        let providerConfig = config.updatingActiveAIProvider(provider)
        let selector = ProviderAwareModelSelector(config: providerConfig)
        return await selector.resolvedPrimaryModel()
    }

    private func target(for kind: AtlasCredentialKind) -> (service: String, account: String) {
        switch kind {
        case .openAI:            return (config.openAIServiceName, config.openAIAccountName)
        case .anthropic:         return ("com.projectatlas.anthropic", "default")
        case .gemini:            return ("com.projectatlas.gemini", "default")
        case .lmStudio:          return ("com.projectatlas.lmstudio", "default")
        case .telegram:          return (config.telegramServiceName, config.telegramAccountName)
        case .discord:           return (config.discordServiceName, config.discordAccountName)
        case .slackBotToken:     return (config.slackBotServiceName, config.slackBotAccountName)
        case .slackAppToken:     return (config.slackAppServiceName, config.slackAppAccountName)
        case .openAIImage:       return (config.openAIImageServiceName, config.openAIImageAccountName)
        case .googleNanoBananaImage: return (config.googleImageServiceName, config.googleImageAccountName)
        case .braveSearch:       return (config.braveSearchServiceName, config.braveSearchAccountName)
        }
    }
}

enum CredentialManagementError: LocalizedError {
    case emptySecret
    case unsupportedValidation

    var errorDescription: String? {
        switch self {
        case .emptySecret:
            return "Enter a value before saving."
        case .unsupportedValidation:
            return "This credential must be validated through the Image Generation settings flow."
        }
    }
}
