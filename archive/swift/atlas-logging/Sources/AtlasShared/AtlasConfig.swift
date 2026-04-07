import Foundation

// MARK: - AtlasConfig v0.2 note
// RuntimeConfigSnapshot is the source of truth for all daemon-readable settings.
// AtlasConfig.init() reads from the pre-seeded snapshot (set via loadFromStore())
// when available, otherwise it reads the persisted shared config file directly.

public enum ImageProviderType: String, Codable, CaseIterable, Hashable, Sendable, Identifiable {
    case openAI = "openai"
    case googleNanoBanana = "google_nano_banana"

    public var id: String { rawValue }

    public var title: String {
        switch self {
        case .openAI:
            return "OpenAI"
        case .googleNanoBanana:
            return "Google Nano Banana"
        }
    }

    public var shortTitle: String {
        switch self {
        case .openAI:
            return "OpenAI"
        case .googleNanoBanana:
            return "Nano Banana"
        }
    }

    public var settingsSubtitle: String {
        switch self {
        case .openAI:
            return "Use OpenAI image generation and edit APIs."
        case .googleNanoBanana:
            return "Use Gemini image generation with Nano Banana."
        }
    }
}

public struct AtlasConfig: Sendable {
    public static let runtimePortKey = "AtlasRuntimePort"
    public static let telegramEnabledKey = "AtlasTelegramEnabled"
    public static let discordEnabledKey = "AtlasDiscordEnabled"
    public static let discordClientIDKey = "AtlasDiscordClientID"
    public static let slackEnabledKey = "AtlasSlackEnabled"
    public static let memoryEnabledKey = "AtlasMemoryEnabled"
    public static let appearanceModeKey = "AtlasAppearanceMode"
    public static let onboardingCompletedKey = "AtlasOnboardingCompleted"
    public static let personaNameKey = "AtlasPersonaName"
    public static let actionSafetyModeKey = "AtlasActionSafetyMode"
    public static let activeImageProviderKey = "AtlasActiveImageProvider"
    public static let activeAIProviderKey = "AtlasActiveAIProvider"
    public static let lmStudioBaseURLKey = "AtlasLMStudioBaseURL"
    public static let selectedAnthropicModelKey = "AtlasSelectedAnthropicModel"
    public static let selectedGeminiModelKey = "AtlasSelectedGeminiModel"
    public static let selectedOpenAIPrimaryModelKey = "AtlasSelectedOpenAIPrimaryModel"
    public static let selectedOpenAIFastModelKey = "AtlasSelectedOpenAIFastModel"
    public static let selectedAnthropicFastModelKey = "AtlasSelectedAnthropicFastModel"
    public static let selectedGeminiFastModelKey = "AtlasSelectedGeminiFastModel"
    public static let selectedLMStudioModelKey = "AtlasSelectedLMStudioModel"
    public static let lmStudioContextWindowLimitKey = "AtlasLMStudioContextWindowLimit"
    public static let lmStudioMaxAgentIterationsKey = "AtlasLMStudioMaxAgentIterations"
    public static let enableSmartToolSelectionKey = "AtlasEnableSmartToolSelection"
    public static let webResearchUseJinaReaderKey = "AtlasWebResearchUseJinaReader"
    public static let remoteAccessEnabledKey = "AtlasRemoteAccessEnabled"

    public let runtimePort: Int
    public let openAIServiceName: String
    public let openAIAccountName: String
    public let telegramServiceName: String
    public let telegramAccountName: String
    public let discordServiceName: String
    public let discordAccountName: String
    public let slackBotServiceName: String
    public let slackBotAccountName: String
    public let slackAppServiceName: String
    public let slackAppAccountName: String
    public let openAIImageServiceName: String
    public let openAIImageAccountName: String
    public let googleImageServiceName: String
    public let googleImageAccountName: String
    public let braveSearchServiceName: String
    public let braveSearchAccountName: String
    public let finnhubServiceName: String
    public let finnhubAccountName: String
    public let alphaVantageServiceName: String
    public let alphaVantageAccountName: String
    public let telegramEnabled: Bool
    public let discordEnabled: Bool
    public let discordClientID: String
    public let slackEnabled: Bool
    public let telegramPollingTimeoutSeconds: Int
    public let telegramPollingRetryBaseSeconds: Int
    public let telegramCommandPrefix: String
    public let telegramAllowedUserIDs: Set<Int64>
    public let telegramAllowedChatIDs: Set<Int64>
    public let defaultOpenAIModel: String
    public let baseSystemPrompt: String
    public let autoApproveDraftTools: Bool
    public let maxAgentIterations: Int
    /// Max messages from conversation history sent per API request. 0 = unlimited. Default 20.
    public let conversationWindowLimit: Int
    /// Context window limit for LM Studio (local) provider. Default 10 — local models have
    /// smaller context windows and slower throughput than cloud providers.
    public let lmStudioContextWindowLimit: Int
    /// Max agent iterations for LM Studio (local) provider. Default 2 — fewer loops keeps
    /// responses fast on local hardware.
    public let lmStudioMaxAgentIterations: Int

    /// Effective context window limit for the active provider.
    public var effectiveContextWindowLimit: Int {
        activeAIProvider == .lmStudio ? lmStudioContextWindowLimit : conversationWindowLimit
    }

    /// Effective max agent iterations for the active provider.
    public var effectiveMaxAgentIterations: Int {
        activeAIProvider == .lmStudio ? lmStudioMaxAgentIterations : maxAgentIterations
    }

    /// When true, only sends tools relevant to the detected intent rather than the full list.
    /// Reduces token usage significantly. Default true.
    public let enableSmartToolSelection: Bool
    /// When true, web.fetch_page and web.research use Jina Reader (r.jina.ai) as a first-pass
    /// content extractor for cleaner Markdown output. Off by default — requires network access to
    /// the Jina service. Enable in Settings > Advanced.
    public let webResearchUseJinaReader: Bool
    /// When true, compound requests are automatically decomposed and run across parallel workers.
    /// Defaults to false — must be explicitly enabled in Settings.
    public let enableMultiAgentOrchestration: Bool
    /// Maximum number of worker agents that may run in parallel. Range 2–5. Default 3.
    public let maxParallelAgents: Int
    /// Iteration cap for each worker agent loop. Separate from effectiveMaxAgentIterations.
    /// Default 4.
    public let workerMaxIterations: Int
    /// When true, the HTTP server binds on 0.0.0.0 so LAN devices can reach Atlas.
    /// The API key (stored in Keychain) is required to authenticate remote sessions.
    public let remoteAccessEnabled: Bool
    public let toolSandboxDirectory: String
    public let memoryDatabasePath: String?
    public let memoryEnabled: Bool
    public let maxRetrievedMemoriesPerTurn: Int
    public let memoryAutoSaveThreshold: Double
    public let personaName: String
    public let appearanceMode: AtlasAppearanceMode
    public let onboardingCompleted: Bool
    public let actionSafetyMode: AtlasActionSafetyMode
    public let activeImageProvider: ImageProviderType?
    /// The active AI provider used for all agent conversations.
    public let activeAIProvider: AIProvider
    /// Base URL for LM Studio when the lmStudio provider is active.
    public let lmStudioBaseURL: String
    /// User-selected primary model override for Anthropic. Empty string = auto-pick.
    public let selectedAnthropicModel: String
    /// User-selected primary model override for Gemini. Empty string = auto-pick.
    public let selectedGeminiModel: String
    /// User-selected primary model override for OpenAI. Empty string = auto-pick newest.
    public let selectedOpenAIPrimaryModel: String
    /// User-selected fast model override for OpenAI. Empty string = auto-pick newest fast.
    public let selectedOpenAIFastModel: String
    /// User-selected fast model override for Anthropic. Empty string = auto-pick.
    public let selectedAnthropicFastModel: String
    /// User-selected fast model override for Gemini. Empty string = auto-pick.
    public let selectedGeminiFastModel: String
    /// User-selected model override for LM Studio. Empty string = auto-pick (first available).
    public let selectedLMStudioModel: String
    private let credentialBundleOverride: AtlasCredentialBundle?
    private let credentialStore: any CredentialStore
    private let secretStore: any SecretStore

    // MARK: - Shared snapshot cache (seeded by loadFromStore for daemon startup path)

    /// Thread-safe cache of the last-loaded RuntimeConfigSnapshot.
    /// When non-nil, AtlasConfig.init() reads from this instead of UserDefaults.
    nonisolated(unsafe) private static var _seededSnapshot: RuntimeConfigSnapshot?

    /// Seed the shared snapshot cache. Call this once at daemon startup after loadFromStore().
    public static func seedSnapshot(_ snapshot: RuntimeConfigSnapshot) {
        _seededSnapshot = snapshot
    }

    private static func persistedSnapshotFromDisk() -> RuntimeConfigSnapshot? {
        let provider = DefaultPathProvider()
        let currentURL = provider.configFileURL()
        let legacyURL = provider.atlasSupportDirectory().appendingPathComponent("atlas-config.json")
        let fileURL = FileManager.default.fileExists(atPath: currentURL.path) ? currentURL : legacyURL

        guard FileManager.default.fileExists(atPath: fileURL.path),
              let data = try? Data(contentsOf: fileURL),
              let snapshot = try? AtlasJSON.decoder.decode(RuntimeConfigSnapshot.self, from: data)
        else {
            return nil
        }

        return snapshot
    }

    /// Async factory for the daemon startup path. Loads config from AtlasConfigStore,
    /// seeds the shared cache, and returns a fully-populated AtlasConfig.
    public static func loadFromStore() async -> AtlasConfig {
        let snapshot = await AtlasConfigStore.shared.load()
        seedSnapshot(snapshot)
        return AtlasConfig(snapshot: snapshot)
    }

    /// Convenience initialiser that builds from a RuntimeConfigSnapshot directly.
    public init(
        snapshot: RuntimeConfigSnapshot,
        credentialStore: any CredentialStore = SecretBackendFactory.defaultCredentialStore(),
        secretStore: any SecretStore = SecretBackendFactory.defaultSecretStore(),
        credentialBundleOverride: AtlasCredentialBundle? = nil
    ) {
        self.init(
            runtimePort: snapshot.runtimePort,
            telegramEnabled: snapshot.telegramEnabled,
            discordEnabled: snapshot.discordEnabled,
            discordClientID: snapshot.discordClientID,
            slackEnabled: snapshot.slackEnabled,
            telegramPollingTimeoutSeconds: snapshot.telegramPollingTimeoutSeconds,
            telegramPollingRetryBaseSeconds: snapshot.telegramPollingRetryBaseSeconds,
            telegramCommandPrefix: snapshot.telegramCommandPrefix,
            telegramAllowedUserIDs: Set(snapshot.telegramAllowedUserIDs),
            telegramAllowedChatIDs: Set(snapshot.telegramAllowedChatIDs),
            defaultOpenAIModel: snapshot.defaultOpenAIModel,
            baseSystemPrompt: snapshot.baseSystemPrompt,
            maxAgentIterations: snapshot.maxAgentIterations,
            conversationWindowLimit: snapshot.conversationWindowLimit,
            lmStudioContextWindowLimit: snapshot.lmStudioContextWindowLimit,
            lmStudioMaxAgentIterations: snapshot.lmStudioMaxAgentIterations,
            memoryEnabled: snapshot.memoryEnabled,
            maxRetrievedMemoriesPerTurn: snapshot.maxRetrievedMemoriesPerTurn,
            memoryAutoSaveThreshold: snapshot.memoryAutoSaveThreshold,
            personaName: snapshot.personaName,
            onboardingCompleted: snapshot.onboardingCompleted,
            actionSafetyMode: AtlasActionSafetyMode(rawValue: snapshot.actionSafetyMode) ?? .askOnlyForRiskyActions,
            activeImageProvider: ImageProviderType(rawValue: snapshot.activeImageProvider),
            activeAIProvider: AIProvider(rawValue: snapshot.activeAIProvider) ?? .openAI,
            lmStudioBaseURL: snapshot.lmStudioBaseURL,
            selectedAnthropicModel: snapshot.selectedAnthropicModel,
            selectedGeminiModel: snapshot.selectedGeminiModel,
            selectedOpenAIPrimaryModel: snapshot.selectedOpenAIPrimaryModel,
            selectedOpenAIFastModel: snapshot.selectedOpenAIFastModel,
            selectedAnthropicFastModel: snapshot.selectedAnthropicFastModel,
            selectedGeminiFastModel: snapshot.selectedGeminiFastModel,
            selectedLMStudioModel: snapshot.selectedLMStudioModel,
            enableSmartToolSelection: snapshot.enableSmartToolSelection,
            webResearchUseJinaReader: snapshot.webResearchUseJinaReader,
            enableMultiAgentOrchestration: snapshot.enableMultiAgentOrchestration,
            maxParallelAgents: snapshot.maxParallelAgents,
            workerMaxIterations: snapshot.workerMaxIterations,
            remoteAccessEnabled: snapshot.remoteAccessEnabled,
            credentialStore: credentialStore,
            secretStore: secretStore,
            credentialBundleOverride: credentialBundleOverride
        )
    }

    /// Builds a config from a different snapshot while preserving the active storage backends.
    /// Runtime-internal validation and reload paths should use this instead of creating a fresh
    /// AtlasConfig that silently falls back to platform default secret stores.
    public func derived(
        snapshot: RuntimeConfigSnapshot,
        credentialBundleOverride override: AtlasCredentialBundle? = nil
    ) -> AtlasConfig {
        AtlasConfig(
            snapshot: snapshot,
            credentialStore: credentialStore,
            secretStore: secretStore,
            credentialBundleOverride: override ?? credentialBundleOverride
        )
    }

    /// Returns a RuntimeConfigSnapshot built from this config's current values.
    public func asSnapshot() -> RuntimeConfigSnapshot {
        RuntimeConfigSnapshot(
            runtimePort: runtimePort,
            onboardingCompleted: onboardingCompleted,
            telegramEnabled: telegramEnabled,
            discordEnabled: discordEnabled,
            discordClientID: discordClientID,
            slackEnabled: slackEnabled,
            telegramPollingTimeoutSeconds: telegramPollingTimeoutSeconds,
            telegramPollingRetryBaseSeconds: telegramPollingRetryBaseSeconds,
            telegramCommandPrefix: telegramCommandPrefix,
            telegramAllowedUserIDs: Array(telegramAllowedUserIDs),
            telegramAllowedChatIDs: Array(telegramAllowedChatIDs),
            defaultOpenAIModel: defaultOpenAIModel,
            baseSystemPrompt: baseSystemPrompt,
            maxAgentIterations: maxAgentIterations,
            conversationWindowLimit: conversationWindowLimit,
            memoryEnabled: memoryEnabled,
            maxRetrievedMemoriesPerTurn: maxRetrievedMemoriesPerTurn,
            memoryAutoSaveThreshold: memoryAutoSaveThreshold,
            personaName: personaName,
            actionSafetyMode: actionSafetyMode.rawValue,
            activeImageProvider: activeImageProvider?.rawValue ?? ImageProviderType.openAI.rawValue,
            activeAIProvider: activeAIProvider.rawValue,
            lmStudioBaseURL: lmStudioBaseURL,
            selectedAnthropicModel: selectedAnthropicModel,
            selectedGeminiModel: selectedGeminiModel,
            selectedOpenAIPrimaryModel: selectedOpenAIPrimaryModel,
            selectedOpenAIFastModel: selectedOpenAIFastModel,
            selectedAnthropicFastModel: selectedAnthropicFastModel,
            selectedGeminiFastModel: selectedGeminiFastModel,
            selectedLMStudioModel: selectedLMStudioModel,
            lmStudioContextWindowLimit: lmStudioContextWindowLimit,
            lmStudioMaxAgentIterations: lmStudioMaxAgentIterations,
            enableSmartToolSelection: enableSmartToolSelection,
            webResearchUseJinaReader: webResearchUseJinaReader,
            enableMultiAgentOrchestration: enableMultiAgentOrchestration,
            maxParallelAgents: maxParallelAgents,
            workerMaxIterations: workerMaxIterations,
            remoteAccessEnabled: remoteAccessEnabled
        )
    }

    public init(
        runtimePort: Int? = nil,
        openAIServiceName: String = "com.projectatlas.openai",
        openAIAccountName: String = "default",
        telegramServiceName: String = "com.projectatlas.telegram",
        telegramAccountName: String = "default",
        discordServiceName: String = "com.projectatlas.discord",
        discordAccountName: String = "default",
        slackBotServiceName: String = "com.projectatlas.slack.bot",
        slackBotAccountName: String = "default",
        slackAppServiceName: String = "com.projectatlas.slack.app",
        slackAppAccountName: String = "default",
        openAIImageServiceName: String = "com.projectatlas.image.openai",
        openAIImageAccountName: String = "default",
        googleImageServiceName: String = "com.projectatlas.image.google",
        googleImageAccountName: String = "default",
        braveSearchServiceName: String = "com.projectatlas.search.brave",
        braveSearchAccountName: String = "default",
        finnhubServiceName: String = "com.projectatlas.finnhub",
        finnhubAccountName: String = "default",
        alphaVantageServiceName: String = "com.projectatlas.alpha-vantage",
        alphaVantageAccountName: String = "default",
        telegramEnabled: Bool? = nil,
        discordEnabled: Bool? = nil,
        discordClientID: String? = nil,
        slackEnabled: Bool? = nil,
        telegramPollingTimeoutSeconds: Int = 30,
        telegramPollingRetryBaseSeconds: Int = 2,
        telegramCommandPrefix: String = "/",
        telegramAllowedUserIDs: Set<Int64> = [],
        telegramAllowedChatIDs: Set<Int64> = [],
        defaultOpenAIModel: String = "gpt-4.1-mini",
        baseSystemPrompt: String = AtlasConfig.defaultSystemPrompt,
        autoApproveDraftTools: Bool = false,
        maxAgentIterations: Int = 3,
        conversationWindowLimit: Int = 20,
        lmStudioContextWindowLimit: Int? = nil,
        lmStudioMaxAgentIterations: Int? = nil,
        toolSandboxDirectory: String? = nil,
        memoryDatabasePath: String? = nil,
        memoryEnabled: Bool? = nil,
        maxRetrievedMemoriesPerTurn: Int = 4,
        memoryAutoSaveThreshold: Double = 0.75,
        personaName: String? = nil,
        appearanceMode: AtlasAppearanceMode? = nil,
        onboardingCompleted: Bool? = nil,
        actionSafetyMode: AtlasActionSafetyMode? = nil,
        activeImageProvider: ImageProviderType? = nil,
        activeAIProvider: AIProvider? = nil,
        lmStudioBaseURL: String? = nil,
        selectedAnthropicModel: String? = nil,
        selectedGeminiModel: String? = nil,
        selectedOpenAIPrimaryModel: String? = nil,
        selectedOpenAIFastModel: String? = nil,
        selectedAnthropicFastModel: String? = nil,
        selectedGeminiFastModel: String? = nil,
        selectedLMStudioModel: String? = nil,
        enableSmartToolSelection: Bool? = nil,
        webResearchUseJinaReader: Bool? = nil,
        enableMultiAgentOrchestration: Bool? = nil,
        maxParallelAgents: Int? = nil,
        workerMaxIterations: Int? = nil,
        remoteAccessEnabled: Bool? = nil,
        credentialStore: any CredentialStore = SecretBackendFactory.defaultCredentialStore(),
        secretStore: any SecretStore = SecretBackendFactory.defaultSecretStore(),
        credentialBundleOverride: AtlasCredentialBundle? = nil
    ) {
        // Runtime-owned settings come from the shared config snapshot first.
        // appearanceMode remains app-only in UserDefaults for now.
        let persisted = Self._seededSnapshot ?? Self.persistedSnapshotFromDisk()

        self.runtimePort = runtimePort ?? persisted?.runtimePort ?? 1984
        self.openAIServiceName = openAIServiceName
        self.openAIAccountName = openAIAccountName
        self.telegramServiceName = telegramServiceName
        self.telegramAccountName = telegramAccountName
        self.discordServiceName = discordServiceName
        self.discordAccountName = discordAccountName
        self.slackBotServiceName = slackBotServiceName
        self.slackBotAccountName = slackBotAccountName
        self.slackAppServiceName = slackAppServiceName
        self.slackAppAccountName = slackAppAccountName
        self.openAIImageServiceName = openAIImageServiceName
        self.openAIImageAccountName = openAIImageAccountName
        self.googleImageServiceName = googleImageServiceName
        self.googleImageAccountName = googleImageAccountName
        self.braveSearchServiceName = braveSearchServiceName
        self.braveSearchAccountName = braveSearchAccountName
        self.finnhubServiceName = finnhubServiceName
        self.finnhubAccountName = finnhubAccountName
        self.alphaVantageServiceName = alphaVantageServiceName
        self.alphaVantageAccountName = alphaVantageAccountName
        self.telegramEnabled = telegramEnabled ?? persisted?.telegramEnabled ?? false
        self.discordEnabled = discordEnabled ?? persisted?.discordEnabled ?? false
        self.discordClientID = (discordClientID ?? persisted?.discordClientID ?? "")
            .trimmingCharacters(in: .whitespacesAndNewlines)
        self.slackEnabled = slackEnabled ?? persisted?.slackEnabled ?? false
        self.telegramPollingTimeoutSeconds = max(1, telegramPollingTimeoutSeconds)
        self.telegramPollingRetryBaseSeconds = max(1, telegramPollingRetryBaseSeconds)
        self.telegramCommandPrefix = telegramCommandPrefix.isEmpty ? "/" : telegramCommandPrefix
        self.telegramAllowedUserIDs = telegramAllowedUserIDs
        self.telegramAllowedChatIDs = telegramAllowedChatIDs
        self.defaultOpenAIModel = defaultOpenAIModel
        self.baseSystemPrompt = baseSystemPrompt
        self.autoApproveDraftTools = autoApproveDraftTools
        self.maxAgentIterations = max(1, maxAgentIterations)
        self.conversationWindowLimit = max(0, conversationWindowLimit)
        self.lmStudioContextWindowLimit = max(0, lmStudioContextWindowLimit ?? persisted?.lmStudioContextWindowLimit ?? 10)
        self.lmStudioMaxAgentIterations = max(1, lmStudioMaxAgentIterations ?? persisted?.lmStudioMaxAgentIterations ?? 2)
        self.toolSandboxDirectory = toolSandboxDirectory ?? Self.defaultToolSandboxDirectory().path
        self.memoryDatabasePath = memoryDatabasePath
        self.memoryEnabled = memoryEnabled ?? persisted?.memoryEnabled ?? true
        self.maxRetrievedMemoriesPerTurn = max(1, min(maxRetrievedMemoriesPerTurn, 8))
        self.memoryAutoSaveThreshold = min(max(memoryAutoSaveThreshold, 0.1), 1.0)
        let resolvedPersonaName = (personaName ?? persisted?.personaName ?? "Atlas").trimmingCharacters(in: .whitespacesAndNewlines)
        self.personaName = resolvedPersonaName.isEmpty ? "Atlas" : resolvedPersonaName
        let persistedActionSafetyMode = persisted.flatMap { AtlasActionSafetyMode(rawValue: $0.actionSafetyMode) }
        self.actionSafetyMode = actionSafetyMode ?? persistedActionSafetyMode ?? .askOnlyForRiskyActions
        // appearanceMode remains app-only (UserDefaults only)
        self.appearanceMode = appearanceMode ?? AtlasAppearanceMode(
            rawValue: UserDefaults.standard.string(forKey: Self.appearanceModeKey) ?? AtlasAppearanceMode.system.rawValue
        ) ?? .system
        self.onboardingCompleted = onboardingCompleted ?? persisted?.onboardingCompleted ?? false
        let persistedImageProvider = persisted.flatMap { ImageProviderType(rawValue: $0.activeImageProvider) }
        self.activeImageProvider = activeImageProvider ?? persistedImageProvider
        let persistedAIProvider = persisted.flatMap { AIProvider(rawValue: $0.activeAIProvider) }
        self.activeAIProvider = activeAIProvider ?? persistedAIProvider ?? .openAI
        self.lmStudioBaseURL = lmStudioBaseURL ?? persisted?.lmStudioBaseURL ?? "http://localhost:1234"
        self.selectedAnthropicModel = selectedAnthropicModel ?? persisted?.selectedAnthropicModel ?? ""
        self.selectedGeminiModel = selectedGeminiModel ?? persisted?.selectedGeminiModel ?? ""
        self.selectedOpenAIPrimaryModel = selectedOpenAIPrimaryModel ?? persisted?.selectedOpenAIPrimaryModel ?? ""
        self.selectedOpenAIFastModel = selectedOpenAIFastModel ?? persisted?.selectedOpenAIFastModel ?? ""
        self.selectedAnthropicFastModel = selectedAnthropicFastModel ?? persisted?.selectedAnthropicFastModel ?? ""
        self.selectedGeminiFastModel = selectedGeminiFastModel ?? persisted?.selectedGeminiFastModel ?? ""
        self.selectedLMStudioModel = selectedLMStudioModel ?? persisted?.selectedLMStudioModel ?? ""
        self.enableSmartToolSelection = enableSmartToolSelection ?? persisted?.enableSmartToolSelection ?? true
        self.webResearchUseJinaReader = webResearchUseJinaReader ?? persisted?.webResearchUseJinaReader ?? false
        self.enableMultiAgentOrchestration = enableMultiAgentOrchestration ?? persisted?.enableMultiAgentOrchestration ?? false
        self.maxParallelAgents = max(2, min(maxParallelAgents ?? persisted?.maxParallelAgents ?? 3, 5))
        self.workerMaxIterations = max(1, min(workerMaxIterations ?? persisted?.workerMaxIterations ?? 4, 10))
        self.remoteAccessEnabled = remoteAccessEnabled ?? persisted?.remoteAccessEnabled ?? false
        self.credentialStore = credentialStore
        self.secretStore = secretStore
        self.credentialBundleOverride = credentialBundleOverride
    }

    private func readCredential(service: String, account: String) throws -> String {
        try credentialStore.readSecret(service: service, account: account)
    }

    private func storeCredential(_ value: String, service: String, account: String) throws {
        try credentialStore.storeSecret(value, service: service, account: account)
    }

    private func deleteCredential(service: String, account: String) throws {
        try credentialStore.deleteSecret(service: service, account: account)
    }

    public func invalidateSecretCache() {
        (credentialStore as? any SecretCacheInvalidating)?.invalidateSecretCache()
        (secretStore as? any SecretCacheInvalidating)?.invalidateSecretCache()
    }

    public func readSecretValue(for service: String) throws -> String? {
        if let value = try? readCredential(service: service, account: "bundle"), !value.isEmpty {
            return value
        }
        return try secretStore.getSecret(name: service)
    }

    // MARK: - Persist methods

    /// Persists runtime-owned settings to AtlasConfigStore.
    /// App-only preferences such as appearance mode remain outside this method.
    public func persistRuntimeSettings() {
        let snapshot = asSnapshot()
        Task {
            // Before writing to config.json, read the current persisted value and
            // preserve any daemon-managed fields that the app's local config may have stale.
            // Specifically: remoteAccessEnabled is exclusively managed via PUT /config
            // (AgentRuntime.updateConfig). The app's local AtlasConfig can have the old
            // value if LAN was toggled — writing that stale value here would overwrite the
            // daemon's authoritative save and cause the setting to revert on next restart.
            var merged = snapshot
            let existing = await AtlasConfigStore.shared.load()
            merged.onboardingCompleted = snapshot.onboardingCompleted
            merged.remoteAccessEnabled = existing.remoteAccessEnabled
            try? await AtlasConfigStore.shared.save(merged)
        }
    }

    public func persistAppearanceMode() {
        // App-only pref — UserDefaults only, no AtlasConfigStore
        UserDefaults.standard.set(appearanceMode.rawValue, forKey: Self.appearanceModeKey)
    }

    public func persistOnboardingCompleted() {
        var snapshot = asSnapshot()
        snapshot.onboardingCompleted = onboardingCompleted
        Task {
            try? await AtlasConfigStore.shared.save(snapshot)
        }
    }

    public func openAIAPIKey() throws -> String {
        if let override = credentialBundleOverride?.openAIAPIKey?.trimmingCharacters(in: .whitespacesAndNewlines), !override.isEmpty {
            return override
        }
        return try readCredential(service: openAIServiceName, account: openAIAccountName)
    }

    public func telegramBotToken() throws -> String {
        if let override = credentialBundleOverride?.telegramBotToken?.trimmingCharacters(in: .whitespacesAndNewlines), !override.isEmpty {
            return override
        }
        return try readCredential(service: telegramServiceName, account: telegramAccountName)
    }

    public func hasTelegramBotToken() -> Bool {
        (try? !telegramBotToken().isEmpty) ?? false
    }

    public func discordBotToken() throws -> String {
        if let override = credentialBundleOverride?.discordBotToken?.trimmingCharacters(in: .whitespacesAndNewlines), !override.isEmpty {
            return override
        }
        return try readCredential(service: discordServiceName, account: discordAccountName)
    }

    public func hasDiscordBotToken() -> Bool {
        (try? !discordBotToken().isEmpty) ?? false
    }

    public func hasDiscordClientID() -> Bool {
        !discordClientID.isEmpty
    }

    public func discordInstallURL() -> URL? {
        guard hasDiscordClientID() else { return nil }
        let permissions = 1024 + 2048 + 65536 + 274_877_906_944
        var components = URLComponents(string: "https://discord.com/oauth2/authorize")
        components?.queryItems = [
            URLQueryItem(name: "client_id", value: discordClientID),
            URLQueryItem(name: "scope", value: "bot"),
            URLQueryItem(name: "permissions", value: String(permissions))
        ]
        return components?.url
    }

    public func slackBotToken() throws -> String {
        if let override = credentialBundleOverride?.slackBotToken?.trimmingCharacters(in: .whitespacesAndNewlines), !override.isEmpty {
            return override
        }
        return try readCredential(service: slackBotServiceName, account: slackBotAccountName)
    }

    public func hasSlackBotToken() -> Bool {
        (try? !slackBotToken().isEmpty) ?? false
    }

    public func slackAppToken() throws -> String {
        if let override = credentialBundleOverride?.slackAppToken?.trimmingCharacters(in: .whitespacesAndNewlines), !override.isEmpty {
            return override
        }
        return try readCredential(service: slackAppServiceName, account: slackAppAccountName)
    }

    public func hasSlackAppToken() -> Bool {
        (try? !slackAppToken().isEmpty) ?? false
    }

    public func storeOpenAIAPIKey(_ secret: String) throws {
        try storeCredential(secret, service: openAIServiceName, account: openAIAccountName)
    }

    public func clearOpenAIAPIKey() throws {
        try deleteCredential(service: openAIServiceName, account: openAIAccountName)
    }

    public func storeTelegramBotToken(_ secret: String) throws {
        try storeCredential(secret, service: telegramServiceName, account: telegramAccountName)
    }

    public func clearTelegramBotToken() throws {
        try deleteCredential(service: telegramServiceName, account: telegramAccountName)
    }

    public func storeDiscordBotToken(_ secret: String) throws {
        try storeCredential(secret, service: discordServiceName, account: discordAccountName)
    }

    public func clearDiscordBotToken() throws {
        try deleteCredential(service: discordServiceName, account: discordAccountName)
    }

    public func storeSlackBotToken(_ secret: String) throws {
        try storeCredential(secret, service: slackBotServiceName, account: slackBotAccountName)
    }

    public func clearSlackBotToken() throws {
        try deleteCredential(service: slackBotServiceName, account: slackBotAccountName)
    }

    public func storeSlackAppToken(_ secret: String) throws {
        try storeCredential(secret, service: slackAppServiceName, account: slackAppAccountName)
    }

    public func clearSlackAppToken() throws {
        try deleteCredential(service: slackAppServiceName, account: slackAppAccountName)
    }

    public func openAIImageAPIKey() throws -> String {
        if let override = credentialBundleOverride?.openAIImageAPIKey?.trimmingCharacters(in: .whitespacesAndNewlines), !override.isEmpty {
            return override
        }
        return try readCredential(service: openAIImageServiceName, account: openAIImageAccountName)
    }

    public func googleImageAPIKey() throws -> String {
        if let override = credentialBundleOverride?.googleImageAPIKey?.trimmingCharacters(in: .whitespacesAndNewlines), !override.isEmpty {
            return override
        }
        return try readCredential(service: googleImageServiceName, account: googleImageAccountName)
    }

    public func hasOpenAIImageAPIKey() -> Bool {
        (try? !openAIImageAPIKey().isEmpty) ?? false
    }

    public func hasGoogleImageAPIKey() -> Bool {
        (try? !googleImageAPIKey().isEmpty) ?? false
    }

    public func storeOpenAIImageAPIKey(_ secret: String) throws {
        try storeCredential(secret, service: openAIImageServiceName, account: openAIImageAccountName)
    }

    public func clearOpenAIImageAPIKey() throws {
        try deleteCredential(service: openAIImageServiceName, account: openAIImageAccountName)
    }

    public func storeGoogleImageAPIKey(_ secret: String) throws {
        try storeCredential(secret, service: googleImageServiceName, account: googleImageAccountName)
    }

    public func clearGoogleImageAPIKey() throws {
        try deleteCredential(service: googleImageServiceName, account: googleImageAccountName)
    }

    public func braveSearchAPIKey() throws -> String {
        if let override = credentialBundleOverride?.braveSearchAPIKey?.trimmingCharacters(in: .whitespacesAndNewlines), !override.isEmpty {
            return override
        }
        return try readCredential(service: braveSearchServiceName, account: braveSearchAccountName)
    }

    public func hasBraveSearchAPIKey() -> Bool {
        (try? !braveSearchAPIKey().isEmpty) ?? false
    }

    public func storeBraveSearchAPIKey(_ secret: String) throws {
        try storeCredential(secret, service: braveSearchServiceName, account: braveSearchAccountName)
    }

    public func clearBraveSearchAPIKey() throws {
        try deleteCredential(service: braveSearchServiceName, account: braveSearchAccountName)
    }

    // MARK: - Finnhub API key

    public func finnhubAPIKey() throws -> String {
        if let override = credentialBundleOverride?.finnhubAPIKey?.trimmingCharacters(in: .whitespacesAndNewlines), !override.isEmpty {
            return override
        }
        return try readCredential(service: finnhubServiceName, account: finnhubAccountName)
    }

    public func hasFinnhubAPIKey() -> Bool {
        (try? !finnhubAPIKey().isEmpty) ?? false
    }

    public func setFinnhubAPIKey(_ key: String) throws {
        try storeCredential(key, service: finnhubServiceName, account: finnhubAccountName)
    }

    public func removeFinnhubAPIKey() throws {
        try deleteCredential(service: finnhubServiceName, account: finnhubAccountName)
    }

    // MARK: - Alpha Vantage API key

    public func alphaVantageAPIKey() throws -> String {
        if let override = credentialBundleOverride?.alphaVantageAPIKey?.trimmingCharacters(in: .whitespacesAndNewlines), !override.isEmpty {
            return override
        }
        return try readCredential(service: alphaVantageServiceName, account: alphaVantageAccountName)
    }

    public func hasAlphaVantageAPIKey() -> Bool {
        (try? !alphaVantageAPIKey().isEmpty) ?? false
    }

    public func setAlphaVantageAPIKey(_ key: String) throws {
        try storeCredential(key, service: alphaVantageServiceName, account: alphaVantageAccountName)
    }

    public func removeAlphaVantageAPIKey() throws {
        try deleteCredential(service: alphaVantageServiceName, account: alphaVantageAccountName)
    }

    // MARK: - Anthropic API key

    public func anthropicAPIKey() throws -> String {
        if let override = credentialBundleOverride?.anthropicAPIKey?.trimmingCharacters(in: .whitespacesAndNewlines), !override.isEmpty {
            return override
        }
        return try readCredential(service: "com.projectatlas.anthropic", account: "default")
    }

    public func hasAnthropicAPIKey() -> Bool {
        (try? !anthropicAPIKey().isEmpty) ?? false
    }

    public func storeAnthropicAPIKey(_ secret: String) throws {
        try storeCredential(secret, service: "com.projectatlas.anthropic", account: "default")
    }

    public func clearAnthropicAPIKey() throws {
        try deleteCredential(service: "com.projectatlas.anthropic", account: "default")
    }

    // MARK: - Gemini API key

    public func geminiAPIKey() throws -> String {
        if let override = credentialBundleOverride?.geminiAPIKey?.trimmingCharacters(in: .whitespacesAndNewlines), !override.isEmpty {
            return override
        }
        return try readCredential(service: "com.projectatlas.gemini", account: "default")
    }

    public func hasGeminiAPIKey() -> Bool {
        (try? !geminiAPIKey().isEmpty) ?? false
    }

    public func storeGeminiAPIKey(_ secret: String) throws {
        try storeCredential(secret, service: "com.projectatlas.gemini", account: "default")
    }

    public func clearGeminiAPIKey() throws {
        try deleteCredential(service: "com.projectatlas.gemini", account: "default")
    }

    // MARK: - LM Studio API key

    public func lmStudioAPIKey() throws -> String {
        if let override = credentialBundleOverride?.lmStudioAPIKey?.trimmingCharacters(in: .whitespacesAndNewlines), !override.isEmpty {
            return override
        }
        return try readCredential(service: "com.projectatlas.lmstudio", account: "default")
    }

    public func hasLMStudioAPIKey() -> Bool {
        (try? !lmStudioAPIKey().isEmpty) ?? false
    }

    public func storeLMStudioAPIKey(_ secret: String) throws {
        try storeCredential(secret, service: "com.projectatlas.lmstudio", account: "default")
    }

    public func clearLMStudioAPIKey() throws {
        try deleteCredential(service: "com.projectatlas.lmstudio", account: "default")
    }

    // MARK: - Remote access API key

    public func remoteAccessAPIKey() throws -> String {
        if let override = credentialBundleOverride?.remoteAccessAPIKey?.trimmingCharacters(in: .whitespacesAndNewlines), !override.isEmpty {
            return override
        }
        return try readCredential(service: "com.projectatlas.remote-access", account: "default")
    }

    public func hasRemoteAccessAPIKey() -> Bool {
        (try? !remoteAccessAPIKey().isEmpty) ?? false
    }

    /// Generates and stores a new UUID-based remote access key if none exists yet.
    /// **Must only be called from the macOS app process (AtlasApp)**, which is always
    /// signed with the correct Keychain access-group entitlement. The daemon must never
    /// call this — writes from the daemon can corrupt the shared Keychain bundle when
    /// the entitlement or signing is not perfectly aligned.
    @discardableResult
    public func generateAndStoreRemoteAccessKeyIfNeeded() throws -> String {
        if let existing = try? remoteAccessAPIKey(), !existing.isEmpty {
            return existing
        }
        let newKey = UUID().uuidString
        try storeRemoteAccessAPIKey(newKey)
        return newKey
    }

    public func storeRemoteAccessAPIKey(_ secret: String) throws {
        try storeCredential(secret, service: "com.projectatlas.remote-access", account: "default")
    }

    public func clearRemoteAccessAPIKey() throws {
        try deleteCredential(service: "com.projectatlas.remote-access", account: "default")
    }

    // MARK: - Integration registry

    public func integrations() -> [AtlasIntegration] {
        return [
            AtlasIntegration(
                id: "brave-search",
                name: "Brave Search",
                category: "Search",
                description: "Structured web search with higher result quality and freshness filters",
                isConfigured: hasBraveSearchAPIKey(),
                setupHint: "User can add a Brave Search API key in Settings → API Keys to upgrade web search"
            ),
            AtlasIntegration(
                id: "finnhub",
                name: "Finnhub",
                category: "Finance",
                description: "Live stock quotes, earnings calendar, company profiles, analyst ratings",
                isConfigured: hasFinnhubAPIKey(),
                setupHint: "User can add a Finnhub API key in Settings → API Keys to enable live stock data"
            ),
            AtlasIntegration(
                id: "alpha-vantage",
                name: "Alpha Vantage",
                category: "Finance",
                description: "Stock price history, technical indicators, and fundamentals",
                isConfigured: hasAlphaVantageAPIKey(),
                setupHint: "User can add an Alpha Vantage API key in Settings → API Keys for stock history"
            )
        ]
    }

    // MARK: - Custom API keys

    /// Converts a user-facing display name into the Keychain-convention key used
    /// for storage and skill manifest references.
    ///
    /// Example: "Trackingmore API" → "com.projectatlas.trackingmore-api"
    public static func keychainKey(forDisplayName displayName: String) -> String {
        let slug = displayName
            .lowercased()
            .trimmingCharacters(in: .whitespaces)
            .components(separatedBy: CharacterSet.alphanumerics.inverted)
            .filter { !$0.isEmpty }
            .joined(separator: "-")
        return "com.projectatlas.\(slug)"
    }

    // Registry: [keychainKey → displayName], stored as JSON in UserDefaults.
    private static let customKeyRegistryDefaultsKey = "com.projectatlas.customAPIKeyRegistry"

    private func customKeyRegistry() -> [String: String] {
        guard
            let data = UserDefaults.standard.data(forKey: Self.customKeyRegistryDefaultsKey),
            let dict = try? JSONDecoder().decode([String: String].self, from: data)
        else { return [:] }
        return dict
    }

    private func saveCustomKeyRegistry(_ registry: [String: String]) {
        if let data = try? JSONEncoder().encode(registry) {
            UserDefaults.standard.set(data, forKey: Self.customKeyRegistryDefaultsKey)
        }
    }

    /// Display names shown in the UI (e.g. "Trackingmore").
    /// Uses the Keychain bundle as the source of truth for which keys actually exist,
    /// then resolves display names from the UserDefaults registry. This keeps the UI
    /// in sync even if the registry and bundle drift apart (e.g. after a restore).
    public func customKeyDisplayNames() -> [String] {
        let storedKeys = Set((try? secretStore.listSecretNames()) ?? [])
        guard !storedKeys.isEmpty else { return [] }
        let registry = customKeyRegistry()
        // Prefer the registry display name; fall back to the raw keychain key slug.
        return storedKeys.map { registry[$0] ?? $0 }.sorted()
    }

    /// Keychain keys used in skill manifests and the agent system prompt
    /// (e.g. "com.projectatlas.trackingmore").
    public func customKeychainKeys() -> [String] {
        customKeyRegistry().keys.sorted()
    }

    /// Ordered pairs of (keychainKey, displayName) for system-prompt injection.
    public func customKeyEntries() -> [(keychainKey: String, displayName: String)] {
        customKeyRegistry()
            .map { (keychainKey: $0.key, displayName: $0.value) }
            .sorted { $0.keychainKey < $1.keychainKey }
    }

    /// Read a secret by its Keychain key (called by the agent's secretsReader).
    public func readCustomKey(name: String) throws -> String? {
        try secretStore.getSecret(name: name)
    }

    /// Store a new custom key. The display name is what the user typed;
    /// the Keychain key is derived automatically via `keychainKey(forDisplayName:)`.
    public func storeCustomKey(displayName: String, value: String) throws {
        let key = Self.keychainKey(forDisplayName: displayName)
        try secretStore.setSecret(name: key, value: value)
        var registry = customKeyRegistry()
        registry[key] = displayName
        saveCustomKeyRegistry(registry)
    }

    /// Delete a custom key by its user-facing display name.
    public func deleteCustomKey(displayName: String) throws {
        let key = Self.keychainKey(forDisplayName: displayName)
        try secretStore.deleteSecret(name: key)
        var registry = customKeyRegistry()
        registry.removeValue(forKey: key)
        saveCustomKeyRegistry(registry)
    }

    public func resolvedToolSandboxDirectory() throws -> URL {
        let url = URL(fileURLWithPath: toolSandboxDirectory, isDirectory: true).standardizedFileURL
        try FileManager.default.createDirectory(
            at: url,
            withIntermediateDirectories: true,
            attributes: nil
        )
        return url
    }

    private static func defaultToolSandboxDirectory() -> URL {
        let appSupportRoot = (try? FileManager.default.url(
            for: .applicationSupportDirectory,
            in: .userDomainMask,
            appropriateFor: nil,
            create: true
        )) ?? URL(fileURLWithPath: NSTemporaryDirectory(), isDirectory: true)

        return appSupportRoot
            .appendingPathComponent("ProjectAtlas", isDirectory: true)
            .appendingPathComponent("ToolSandbox", isDirectory: true)
    }

    public static func atlasSupportDirectory() -> URL {
        let appSupportRoot = (try? FileManager.default.url(
            for: .applicationSupportDirectory,
            in: .userDomainMask,
            appropriateFor: nil,
            create: true
        )) ?? URL(fileURLWithPath: NSTemporaryDirectory(), isDirectory: true)

        let dir = appSupportRoot.appendingPathComponent("ProjectAtlas", isDirectory: true)
        try? FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true, attributes: nil)
        return dir
    }

    public var gremlinsFilePath: String {
        AtlasConfig.atlasSupportDirectory().appendingPathComponent("GREMLINS.md").path
    }

    public var mindFilePath: String {
        AtlasConfig.atlasSupportDirectory().appendingPathComponent("MIND.md").path
    }

    public var skillsFilePath: String {
        AtlasConfig.atlasSupportDirectory().appendingPathComponent("SKILLS.md").path
    }

    public static let defaultSystemPrompt = """
    You are Atlas, a local macOS AI operator.
    Follow the active persona and relevant memory blocks supplied with each request.
    Use remembered information only when it appears in the provided memory context.
    Never claim that a tool ran unless you received its result.
    Never pretend to remember things you do not actually know or store.
    Only call registered Atlas tools when they are needed.
    When the App Automator skill is enabled, you can read and write macOS apps directly — Calendar, Reminders, Contacts, Mail, Notes, Music, and Safari — via registered tools. Never say you lack the ability to interact with these apps when the skill is available.
    Respect approval boundaries:
    - read tools may run automatically only within the allowed local scope
    - draft tools may require approval depending on policy
    - execute tools always require explicit approval
    If approval is needed, request the tool through a structured tool call instead of pretending the action completed.
    """

    public func isTelegramChatAllowed(_ chatID: Int64) -> Bool {
        telegramAllowedChatIDs.isEmpty || telegramAllowedChatIDs.contains(chatID)
    }

    public func isTelegramUserAllowed(_ userID: Int64?) -> Bool {
        guard let userID else {
            return telegramAllowedUserIDs.isEmpty
        }

        return telegramAllowedUserIDs.isEmpty || telegramAllowedUserIDs.contains(userID)
    }

    public func updatingTelegramEnabled(_ enabled: Bool) -> AtlasConfig {
        AtlasConfig(
            runtimePort: runtimePort,
            openAIServiceName: openAIServiceName,
            openAIAccountName: openAIAccountName,
            telegramServiceName: telegramServiceName,
            telegramAccountName: telegramAccountName,
            openAIImageServiceName: openAIImageServiceName,
            openAIImageAccountName: openAIImageAccountName,
            googleImageServiceName: googleImageServiceName,
            googleImageAccountName: googleImageAccountName,
            telegramEnabled: enabled,
            telegramPollingTimeoutSeconds: telegramPollingTimeoutSeconds,
            telegramPollingRetryBaseSeconds: telegramPollingRetryBaseSeconds,
            telegramCommandPrefix: telegramCommandPrefix,
            telegramAllowedUserIDs: telegramAllowedUserIDs,
            telegramAllowedChatIDs: telegramAllowedChatIDs,
            defaultOpenAIModel: defaultOpenAIModel,
            baseSystemPrompt: baseSystemPrompt,
            autoApproveDraftTools: autoApproveDraftTools,
            maxAgentIterations: maxAgentIterations,
            toolSandboxDirectory: toolSandboxDirectory,
            memoryDatabasePath: memoryDatabasePath,
            memoryEnabled: memoryEnabled,
            maxRetrievedMemoriesPerTurn: maxRetrievedMemoriesPerTurn,
            memoryAutoSaveThreshold: memoryAutoSaveThreshold,
            personaName: personaName,
            appearanceMode: appearanceMode,
            onboardingCompleted: onboardingCompleted,
            actionSafetyMode: actionSafetyMode,
            activeImageProvider: activeImageProvider
        )
    }

    public func updatingAppearanceMode(_ appearanceMode: AtlasAppearanceMode) -> AtlasConfig {
        AtlasConfig(
            runtimePort: runtimePort,
            openAIServiceName: openAIServiceName,
            openAIAccountName: openAIAccountName,
            telegramServiceName: telegramServiceName,
            telegramAccountName: telegramAccountName,
            openAIImageServiceName: openAIImageServiceName,
            openAIImageAccountName: openAIImageAccountName,
            googleImageServiceName: googleImageServiceName,
            googleImageAccountName: googleImageAccountName,
            telegramEnabled: telegramEnabled,
            telegramPollingTimeoutSeconds: telegramPollingTimeoutSeconds,
            telegramPollingRetryBaseSeconds: telegramPollingRetryBaseSeconds,
            telegramCommandPrefix: telegramCommandPrefix,
            telegramAllowedUserIDs: telegramAllowedUserIDs,
            telegramAllowedChatIDs: telegramAllowedChatIDs,
            defaultOpenAIModel: defaultOpenAIModel,
            baseSystemPrompt: baseSystemPrompt,
            autoApproveDraftTools: autoApproveDraftTools,
            maxAgentIterations: maxAgentIterations,
            toolSandboxDirectory: toolSandboxDirectory,
            memoryDatabasePath: memoryDatabasePath,
            memoryEnabled: memoryEnabled,
            maxRetrievedMemoriesPerTurn: maxRetrievedMemoriesPerTurn,
            memoryAutoSaveThreshold: memoryAutoSaveThreshold,
            personaName: personaName,
            appearanceMode: appearanceMode,
            onboardingCompleted: onboardingCompleted,
            actionSafetyMode: actionSafetyMode,
            activeImageProvider: activeImageProvider
        )
    }

    public func updatingOnboardingCompleted(_ onboardingCompleted: Bool) -> AtlasConfig {
        AtlasConfig(
            runtimePort: runtimePort,
            openAIServiceName: openAIServiceName,
            openAIAccountName: openAIAccountName,
            telegramServiceName: telegramServiceName,
            telegramAccountName: telegramAccountName,
            openAIImageServiceName: openAIImageServiceName,
            openAIImageAccountName: openAIImageAccountName,
            googleImageServiceName: googleImageServiceName,
            googleImageAccountName: googleImageAccountName,
            telegramEnabled: telegramEnabled,
            telegramPollingTimeoutSeconds: telegramPollingTimeoutSeconds,
            telegramPollingRetryBaseSeconds: telegramPollingRetryBaseSeconds,
            telegramCommandPrefix: telegramCommandPrefix,
            telegramAllowedUserIDs: telegramAllowedUserIDs,
            telegramAllowedChatIDs: telegramAllowedChatIDs,
            defaultOpenAIModel: defaultOpenAIModel,
            baseSystemPrompt: baseSystemPrompt,
            autoApproveDraftTools: autoApproveDraftTools,
            maxAgentIterations: maxAgentIterations,
            toolSandboxDirectory: toolSandboxDirectory,
            memoryDatabasePath: memoryDatabasePath,
            memoryEnabled: memoryEnabled,
            maxRetrievedMemoriesPerTurn: maxRetrievedMemoriesPerTurn,
            memoryAutoSaveThreshold: memoryAutoSaveThreshold,
            personaName: personaName,
            appearanceMode: appearanceMode,
            onboardingCompleted: onboardingCompleted,
            actionSafetyMode: actionSafetyMode,
            activeImageProvider: activeImageProvider
        )
    }

    public func updatingPersonaName(_ personaName: String) -> AtlasConfig {
        AtlasConfig(
            runtimePort: runtimePort,
            openAIServiceName: openAIServiceName,
            openAIAccountName: openAIAccountName,
            telegramServiceName: telegramServiceName,
            telegramAccountName: telegramAccountName,
            openAIImageServiceName: openAIImageServiceName,
            openAIImageAccountName: openAIImageAccountName,
            googleImageServiceName: googleImageServiceName,
            googleImageAccountName: googleImageAccountName,
            telegramEnabled: telegramEnabled,
            telegramPollingTimeoutSeconds: telegramPollingTimeoutSeconds,
            telegramPollingRetryBaseSeconds: telegramPollingRetryBaseSeconds,
            telegramCommandPrefix: telegramCommandPrefix,
            telegramAllowedUserIDs: telegramAllowedUserIDs,
            telegramAllowedChatIDs: telegramAllowedChatIDs,
            defaultOpenAIModel: defaultOpenAIModel,
            baseSystemPrompt: baseSystemPrompt,
            autoApproveDraftTools: autoApproveDraftTools,
            maxAgentIterations: maxAgentIterations,
            toolSandboxDirectory: toolSandboxDirectory,
            memoryDatabasePath: memoryDatabasePath,
            memoryEnabled: memoryEnabled,
            maxRetrievedMemoriesPerTurn: maxRetrievedMemoriesPerTurn,
            memoryAutoSaveThreshold: memoryAutoSaveThreshold,
            personaName: personaName,
            appearanceMode: appearanceMode,
            onboardingCompleted: onboardingCompleted,
            actionSafetyMode: actionSafetyMode,
            activeImageProvider: activeImageProvider
        )
    }

    public func updatingActionSafetyMode(_ actionSafetyMode: AtlasActionSafetyMode) -> AtlasConfig {
        AtlasConfig(
            runtimePort: runtimePort,
            openAIServiceName: openAIServiceName,
            openAIAccountName: openAIAccountName,
            telegramServiceName: telegramServiceName,
            telegramAccountName: telegramAccountName,
            openAIImageServiceName: openAIImageServiceName,
            openAIImageAccountName: openAIImageAccountName,
            googleImageServiceName: googleImageServiceName,
            googleImageAccountName: googleImageAccountName,
            telegramEnabled: telegramEnabled,
            telegramPollingTimeoutSeconds: telegramPollingTimeoutSeconds,
            telegramPollingRetryBaseSeconds: telegramPollingRetryBaseSeconds,
            telegramCommandPrefix: telegramCommandPrefix,
            telegramAllowedUserIDs: telegramAllowedUserIDs,
            telegramAllowedChatIDs: telegramAllowedChatIDs,
            defaultOpenAIModel: defaultOpenAIModel,
            baseSystemPrompt: baseSystemPrompt,
            autoApproveDraftTools: autoApproveDraftTools,
            maxAgentIterations: maxAgentIterations,
            toolSandboxDirectory: toolSandboxDirectory,
            memoryDatabasePath: memoryDatabasePath,
            memoryEnabled: memoryEnabled,
            maxRetrievedMemoriesPerTurn: maxRetrievedMemoriesPerTurn,
            memoryAutoSaveThreshold: memoryAutoSaveThreshold,
            personaName: personaName,
            appearanceMode: appearanceMode,
            onboardingCompleted: onboardingCompleted,
            actionSafetyMode: actionSafetyMode,
            activeImageProvider: activeImageProvider
        )
    }

    public func updatingActiveImageProvider(_ provider: ImageProviderType?) -> AtlasConfig {
        AtlasConfig(
            runtimePort: runtimePort,
            openAIServiceName: openAIServiceName,
            openAIAccountName: openAIAccountName,
            telegramServiceName: telegramServiceName,
            telegramAccountName: telegramAccountName,
            openAIImageServiceName: openAIImageServiceName,
            openAIImageAccountName: openAIImageAccountName,
            googleImageServiceName: googleImageServiceName,
            googleImageAccountName: googleImageAccountName,
            telegramEnabled: telegramEnabled,
            telegramPollingTimeoutSeconds: telegramPollingTimeoutSeconds,
            telegramPollingRetryBaseSeconds: telegramPollingRetryBaseSeconds,
            telegramCommandPrefix: telegramCommandPrefix,
            telegramAllowedUserIDs: telegramAllowedUserIDs,
            telegramAllowedChatIDs: telegramAllowedChatIDs,
            defaultOpenAIModel: defaultOpenAIModel,
            baseSystemPrompt: baseSystemPrompt,
            autoApproveDraftTools: autoApproveDraftTools,
            maxAgentIterations: maxAgentIterations,
            toolSandboxDirectory: toolSandboxDirectory,
            memoryDatabasePath: memoryDatabasePath,
            memoryEnabled: memoryEnabled,
            maxRetrievedMemoriesPerTurn: maxRetrievedMemoriesPerTurn,
            memoryAutoSaveThreshold: memoryAutoSaveThreshold,
            personaName: personaName,
            appearanceMode: appearanceMode,
            onboardingCompleted: onboardingCompleted,
            actionSafetyMode: actionSafetyMode,
            activeImageProvider: provider
        )
    }

    public func updatingActiveAIProvider(_ provider: AIProvider) -> AtlasConfig {
        AtlasConfig(
            runtimePort: runtimePort,
            openAIServiceName: openAIServiceName,
            openAIAccountName: openAIAccountName,
            telegramServiceName: telegramServiceName,
            telegramAccountName: telegramAccountName,
            openAIImageServiceName: openAIImageServiceName,
            openAIImageAccountName: openAIImageAccountName,
            googleImageServiceName: googleImageServiceName,
            googleImageAccountName: googleImageAccountName,
            telegramEnabled: telegramEnabled,
            telegramPollingTimeoutSeconds: telegramPollingTimeoutSeconds,
            telegramPollingRetryBaseSeconds: telegramPollingRetryBaseSeconds,
            telegramCommandPrefix: telegramCommandPrefix,
            telegramAllowedUserIDs: telegramAllowedUserIDs,
            telegramAllowedChatIDs: telegramAllowedChatIDs,
            defaultOpenAIModel: defaultOpenAIModel,
            baseSystemPrompt: baseSystemPrompt,
            autoApproveDraftTools: autoApproveDraftTools,
            maxAgentIterations: maxAgentIterations,
            toolSandboxDirectory: toolSandboxDirectory,
            memoryDatabasePath: memoryDatabasePath,
            memoryEnabled: memoryEnabled,
            maxRetrievedMemoriesPerTurn: maxRetrievedMemoriesPerTurn,
            memoryAutoSaveThreshold: memoryAutoSaveThreshold,
            personaName: personaName,
            appearanceMode: appearanceMode,
            onboardingCompleted: onboardingCompleted,
            actionSafetyMode: actionSafetyMode,
            activeImageProvider: activeImageProvider,
            activeAIProvider: provider,
            lmStudioBaseURL: lmStudioBaseURL
        )
    }

    public func updatingLMStudioBaseURL(_ baseURL: String) -> AtlasConfig {
        AtlasConfig(
            runtimePort: runtimePort,
            openAIServiceName: openAIServiceName,
            openAIAccountName: openAIAccountName,
            telegramServiceName: telegramServiceName,
            telegramAccountName: telegramAccountName,
            openAIImageServiceName: openAIImageServiceName,
            openAIImageAccountName: openAIImageAccountName,
            googleImageServiceName: googleImageServiceName,
            googleImageAccountName: googleImageAccountName,
            telegramEnabled: telegramEnabled,
            telegramPollingTimeoutSeconds: telegramPollingTimeoutSeconds,
            telegramPollingRetryBaseSeconds: telegramPollingRetryBaseSeconds,
            telegramCommandPrefix: telegramCommandPrefix,
            telegramAllowedUserIDs: telegramAllowedUserIDs,
            telegramAllowedChatIDs: telegramAllowedChatIDs,
            defaultOpenAIModel: defaultOpenAIModel,
            baseSystemPrompt: baseSystemPrompt,
            autoApproveDraftTools: autoApproveDraftTools,
            maxAgentIterations: maxAgentIterations,
            toolSandboxDirectory: toolSandboxDirectory,
            memoryDatabasePath: memoryDatabasePath,
            memoryEnabled: memoryEnabled,
            maxRetrievedMemoriesPerTurn: maxRetrievedMemoriesPerTurn,
            memoryAutoSaveThreshold: memoryAutoSaveThreshold,
            personaName: personaName,
            appearanceMode: appearanceMode,
            onboardingCompleted: onboardingCompleted,
            actionSafetyMode: actionSafetyMode,
            activeImageProvider: activeImageProvider,
            activeAIProvider: activeAIProvider,
            lmStudioBaseURL: baseURL
        )
    }
}
