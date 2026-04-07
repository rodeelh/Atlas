import Foundation

// MARK: - RuntimeConfigKey

public enum RuntimeConfigKey: String, Sendable {
    case runtimePort
    case telegramEnabled
    case discordEnabled
    case discordClientID
    case slackEnabled
    case telegramPollingTimeoutSeconds
    case telegramPollingRetryBaseSeconds
    case telegramCommandPrefix
    case telegramAllowedUserIDs
    case telegramAllowedChatIDs
    case defaultOpenAIModel
    case baseSystemPrompt
    case maxAgentIterations
    case memoryEnabled
    case maxRetrievedMemoriesPerTurn
    case memoryAutoSaveThreshold
    case personaName
    case actionSafetyMode
    case activeImageProvider
    case activeAIProvider
    case lmStudioBaseURL
    case conversationWindowLimit
    case selectedAnthropicModel
    case selectedGeminiModel
    case selectedOpenAIPrimaryModel
    case selectedOpenAIFastModel
    case selectedAnthropicFastModel
    case selectedGeminiFastModel
    case selectedLMStudioModel
    case lmStudioContextWindowLimit
    case lmStudioMaxAgentIterations
    case enableSmartToolSelection
    case webResearchUseJinaReader
    case enableMultiAgentOrchestration
    case maxParallelAgents
    case workerMaxIterations
    case remoteAccessEnabled
}

// MARK: - RuntimeConfigSnapshot

public struct RuntimeConfigSnapshot: Codable, Sendable {
    public var runtimePort: Int
    public var onboardingCompleted: Bool
    public var telegramEnabled: Bool
    public var discordEnabled: Bool
    public var discordClientID: String
    public var slackEnabled: Bool
    public var telegramPollingTimeoutSeconds: Int
    public var telegramPollingRetryBaseSeconds: Int
    public var telegramCommandPrefix: String
    public var telegramAllowedUserIDs: [Int64]
    public var telegramAllowedChatIDs: [Int64]
    public var defaultOpenAIModel: String
    public var baseSystemPrompt: String
    public var maxAgentIterations: Int
    /// Maximum number of messages from conversation history to send per API request.
    /// 0 = unlimited. Default 20 (10 exchanges). Keeps token usage predictable.
    public var conversationWindowLimit: Int
    public var memoryEnabled: Bool
    public var maxRetrievedMemoriesPerTurn: Int
    public var memoryAutoSaveThreshold: Double
    public var personaName: String
    public var actionSafetyMode: String
    public var activeImageProvider: String
    public var activeAIProvider: String
    public var lmStudioBaseURL: String
    public var selectedAnthropicModel: String
    public var selectedGeminiModel: String
    /// User-selected primary model override for OpenAI. Empty = auto-pick newest.
    public var selectedOpenAIPrimaryModel: String
    /// User-selected fast model override for OpenAI. Empty = auto-pick newest fast.
    public var selectedOpenAIFastModel: String
    /// User-selected fast model override for Anthropic. Empty = auto-pick.
    public var selectedAnthropicFastModel: String
    /// User-selected fast model override for Gemini. Empty = auto-pick.
    public var selectedGeminiFastModel: String
    /// User-selected model override for LM Studio. Empty = auto-pick (first available).
    public var selectedLMStudioModel: String
    /// Context window limit for LM Studio provider. Default 10.
    public var lmStudioContextWindowLimit: Int
    /// Max agent iterations for LM Studio provider. Default 2.
    public var lmStudioMaxAgentIterations: Int
    /// When true, only tools relevant to the detected intent are sent per turn. Default true.
    public var enableSmartToolSelection: Bool
    /// When true, web research uses Jina Reader as a first-pass extractor. Default false.
    public var webResearchUseJinaReader: Bool
    /// When true, compound requests are decomposed and run across parallel worker agents. Default false.
    public var enableMultiAgentOrchestration: Bool
    /// Maximum number of parallel worker agents. Range 2–5. Default 3.
    public var maxParallelAgents: Int
    /// Iteration cap for each worker agent loop. Default 4.
    public var workerMaxIterations: Int
    /// When true, the runtime binds on 0.0.0.0 so LAN devices can connect. Default false.
    public var remoteAccessEnabled: Bool

    public init(
        runtimePort: Int = 1984,
        onboardingCompleted: Bool = false,
        telegramEnabled: Bool = false,
        discordEnabled: Bool = false,
        discordClientID: String = "",
        slackEnabled: Bool = false,
        telegramPollingTimeoutSeconds: Int = 30,
        telegramPollingRetryBaseSeconds: Int = 2,
        telegramCommandPrefix: String = "/",
        telegramAllowedUserIDs: [Int64] = [],
        telegramAllowedChatIDs: [Int64] = [],
        defaultOpenAIModel: String = "gpt-4.1-mini",
        baseSystemPrompt: String? = nil,
        maxAgentIterations: Int = 3,
        conversationWindowLimit: Int = 20,
        memoryEnabled: Bool = true,
        maxRetrievedMemoriesPerTurn: Int = 4,
        memoryAutoSaveThreshold: Double = 0.75,
        personaName: String = "Atlas",
        actionSafetyMode: String = "ask_only_for_risky_actions",
        activeImageProvider: String = "openai",
        activeAIProvider: String = "openai",
        lmStudioBaseURL: String = "http://localhost:1234",
        selectedAnthropicModel: String = "",
        selectedGeminiModel: String = "",
        selectedOpenAIPrimaryModel: String = "",
        selectedOpenAIFastModel: String = "",
        selectedAnthropicFastModel: String = "",
        selectedGeminiFastModel: String = "",
        selectedLMStudioModel: String = "",
        lmStudioContextWindowLimit: Int = 10,
        lmStudioMaxAgentIterations: Int = 2,
        enableSmartToolSelection: Bool = true,
        webResearchUseJinaReader: Bool = false,
        enableMultiAgentOrchestration: Bool = false,
        maxParallelAgents: Int = 3,
        workerMaxIterations: Int = 4,
        remoteAccessEnabled: Bool = false
    ) {
        self.runtimePort = runtimePort
        self.onboardingCompleted = onboardingCompleted
        self.telegramEnabled = telegramEnabled
        self.discordEnabled = discordEnabled
        self.discordClientID = discordClientID
        self.slackEnabled = slackEnabled
        self.telegramPollingTimeoutSeconds = telegramPollingTimeoutSeconds
        self.telegramPollingRetryBaseSeconds = telegramPollingRetryBaseSeconds
        self.telegramCommandPrefix = telegramCommandPrefix
        self.telegramAllowedUserIDs = telegramAllowedUserIDs
        self.telegramAllowedChatIDs = telegramAllowedChatIDs
        self.defaultOpenAIModel = defaultOpenAIModel
        self.baseSystemPrompt = baseSystemPrompt ?? Self.fallbackSystemPrompt
        self.maxAgentIterations = maxAgentIterations
        self.conversationWindowLimit = conversationWindowLimit
        self.memoryEnabled = memoryEnabled
        self.maxRetrievedMemoriesPerTurn = maxRetrievedMemoriesPerTurn
        self.memoryAutoSaveThreshold = memoryAutoSaveThreshold
        self.personaName = personaName
        self.actionSafetyMode = actionSafetyMode
        self.activeImageProvider = activeImageProvider
        self.activeAIProvider = activeAIProvider
        self.lmStudioBaseURL = lmStudioBaseURL
        self.selectedAnthropicModel = selectedAnthropicModel
        self.selectedGeminiModel = selectedGeminiModel
        self.selectedOpenAIPrimaryModel = selectedOpenAIPrimaryModel
        self.selectedOpenAIFastModel = selectedOpenAIFastModel
        self.selectedAnthropicFastModel = selectedAnthropicFastModel
        self.selectedGeminiFastModel = selectedGeminiFastModel
        self.selectedLMStudioModel = selectedLMStudioModel
        self.lmStudioContextWindowLimit = lmStudioContextWindowLimit
        self.lmStudioMaxAgentIterations = lmStudioMaxAgentIterations
        self.enableSmartToolSelection = enableSmartToolSelection
        self.webResearchUseJinaReader = webResearchUseJinaReader
        self.enableMultiAgentOrchestration = enableMultiAgentOrchestration
        self.maxParallelAgents = max(2, min(maxParallelAgents, 5))
        self.workerMaxIterations = max(1, min(workerMaxIterations, 10))
        self.remoteAccessEnabled = remoteAccessEnabled
    }

    public static var defaults: RuntimeConfigSnapshot {
        RuntimeConfigSnapshot()
    }

    enum CodingKeys: String, CodingKey {
        case runtimePort
        case onboardingCompleted
        case telegramEnabled
        case discordEnabled
        case discordClientID
        case slackEnabled
        case telegramPollingTimeoutSeconds
        case telegramPollingRetryBaseSeconds
        case telegramCommandPrefix
        case telegramAllowedUserIDs
        case telegramAllowedChatIDs
        case defaultOpenAIModel
        case baseSystemPrompt
        case maxAgentIterations
        case conversationWindowLimit
        case memoryEnabled
        case maxRetrievedMemoriesPerTurn
        case memoryAutoSaveThreshold
        case personaName
        case actionSafetyMode
        case activeImageProvider
        case activeAIProvider
        case lmStudioBaseURL
        case selectedAnthropicModel
        case selectedGeminiModel
        case selectedOpenAIPrimaryModel
        case selectedOpenAIFastModel
        case selectedAnthropicFastModel
        case selectedGeminiFastModel
        case selectedLMStudioModel
        case lmStudioContextWindowLimit
        case lmStudioMaxAgentIterations
        case enableSmartToolSelection
        case webResearchUseJinaReader
        case enableMultiAgentOrchestration
        case maxParallelAgents
        case workerMaxIterations
        case remoteAccessEnabled
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        self.init(
            runtimePort: try container.decodeIfPresent(Int.self, forKey: .runtimePort) ?? 1984,
            onboardingCompleted: try container.decodeIfPresent(Bool.self, forKey: .onboardingCompleted) ?? false,
            telegramEnabled: try container.decodeIfPresent(Bool.self, forKey: .telegramEnabled) ?? false,
            discordEnabled: try container.decodeIfPresent(Bool.self, forKey: .discordEnabled) ?? false,
            discordClientID: try container.decodeIfPresent(String.self, forKey: .discordClientID) ?? "",
            slackEnabled: try container.decodeIfPresent(Bool.self, forKey: .slackEnabled) ?? false,
            telegramPollingTimeoutSeconds: try container.decodeIfPresent(Int.self, forKey: .telegramPollingTimeoutSeconds) ?? 30,
            telegramPollingRetryBaseSeconds: try container.decodeIfPresent(Int.self, forKey: .telegramPollingRetryBaseSeconds) ?? 2,
            telegramCommandPrefix: try container.decodeIfPresent(String.self, forKey: .telegramCommandPrefix) ?? "/",
            telegramAllowedUserIDs: try container.decodeIfPresent([Int64].self, forKey: .telegramAllowedUserIDs) ?? [],
            telegramAllowedChatIDs: try container.decodeIfPresent([Int64].self, forKey: .telegramAllowedChatIDs) ?? [],
            defaultOpenAIModel: try container.decodeIfPresent(String.self, forKey: .defaultOpenAIModel) ?? "gpt-4.1-mini",
            baseSystemPrompt: try container.decodeIfPresent(String.self, forKey: .baseSystemPrompt),
            maxAgentIterations: try container.decodeIfPresent(Int.self, forKey: .maxAgentIterations) ?? 3,
            conversationWindowLimit: try container.decodeIfPresent(Int.self, forKey: .conversationWindowLimit) ?? 20,
            memoryEnabled: try container.decodeIfPresent(Bool.self, forKey: .memoryEnabled) ?? true,
            maxRetrievedMemoriesPerTurn: try container.decodeIfPresent(Int.self, forKey: .maxRetrievedMemoriesPerTurn) ?? 4,
            memoryAutoSaveThreshold: try container.decodeIfPresent(Double.self, forKey: .memoryAutoSaveThreshold) ?? 0.75,
            personaName: try container.decodeIfPresent(String.self, forKey: .personaName) ?? "Atlas",
            actionSafetyMode: try container.decodeIfPresent(String.self, forKey: .actionSafetyMode) ?? "ask_only_for_risky_actions",
            activeImageProvider: try container.decodeIfPresent(String.self, forKey: .activeImageProvider) ?? "openai",
            activeAIProvider: try container.decodeIfPresent(String.self, forKey: .activeAIProvider) ?? "openai",
            lmStudioBaseURL: try container.decodeIfPresent(String.self, forKey: .lmStudioBaseURL) ?? "http://localhost:1234",
            selectedAnthropicModel: try container.decodeIfPresent(String.self, forKey: .selectedAnthropicModel) ?? "",
            selectedGeminiModel: try container.decodeIfPresent(String.self, forKey: .selectedGeminiModel) ?? "",
            selectedOpenAIPrimaryModel: try container.decodeIfPresent(String.self, forKey: .selectedOpenAIPrimaryModel) ?? "",
            selectedOpenAIFastModel: try container.decodeIfPresent(String.self, forKey: .selectedOpenAIFastModel) ?? "",
            selectedAnthropicFastModel: try container.decodeIfPresent(String.self, forKey: .selectedAnthropicFastModel) ?? "",
            selectedGeminiFastModel: try container.decodeIfPresent(String.self, forKey: .selectedGeminiFastModel) ?? "",
            selectedLMStudioModel: try container.decodeIfPresent(String.self, forKey: .selectedLMStudioModel) ?? "",
            lmStudioContextWindowLimit: try container.decodeIfPresent(Int.self, forKey: .lmStudioContextWindowLimit) ?? 10,
            lmStudioMaxAgentIterations: try container.decodeIfPresent(Int.self, forKey: .lmStudioMaxAgentIterations) ?? 2,
            enableSmartToolSelection: try container.decodeIfPresent(Bool.self, forKey: .enableSmartToolSelection) ?? true,
            webResearchUseJinaReader: try container.decodeIfPresent(Bool.self, forKey: .webResearchUseJinaReader) ?? false,
            enableMultiAgentOrchestration: try container.decodeIfPresent(Bool.self, forKey: .enableMultiAgentOrchestration) ?? false,
            maxParallelAgents: try container.decodeIfPresent(Int.self, forKey: .maxParallelAgents) ?? 3,
            workerMaxIterations: try container.decodeIfPresent(Int.self, forKey: .workerMaxIterations) ?? 4,
            remoteAccessEnabled: try container.decodeIfPresent(Bool.self, forKey: .remoteAccessEnabled) ?? false
        )
    }

    static let fallbackSystemPrompt = """
    You are Atlas, a local macOS AI operator.
    Follow the active persona and relevant memory blocks supplied with each request.
    Use remembered information only when it appears in the provided memory context.
    Never claim that a tool ran unless you received its result.
    Never pretend to remember things you do not actually know or store.
    Only call registered Atlas tools when they are needed.
    Respect approval boundaries:
    - read tools may run automatically only within the allowed local scope
    - draft tools may require approval depending on policy
    - execute tools always require explicit approval
    If approval is needed, request the tool through a structured tool call instead of pretending the action completed.
    """
}

// MARK: - AtlasConfigStore

public actor AtlasConfigStore: ConfigStore {
    public static var shared = AtlasConfigStore()

    private let pathProvider: PathProvider

    private var cachedSnapshot: RuntimeConfigSnapshot?

    public init(pathProvider: PathProvider = DefaultPathProvider()) {
        self.pathProvider = pathProvider
    }

    private var configFileURL: URL {
        pathProvider.configFileURL()
    }

    private var legacyConfigFileURL: URL {
        pathProvider.atlasSupportDirectory().appendingPathComponent("atlas-config.json")
    }

    private func setSecurePermissionsIfPossible(for url: URL) {
        #if os(macOS) || os(Linux)
        try? FileManager.default.setAttributes([.posixPermissions: 0o600], ofItemAtPath: url.path)
        #endif
    }

    private func readSnapshot(from url: URL) throws -> RuntimeConfigSnapshot {
        let data = try Data(contentsOf: url)
        return try AtlasJSON.decoder.decode(RuntimeConfigSnapshot.self, from: data)
    }

    private func migrateLegacyConfigIfNeeded() throws -> RuntimeConfigSnapshot? {
        guard !FileManager.default.fileExists(atPath: configFileURL.path),
              FileManager.default.fileExists(atPath: legacyConfigFileURL.path) else {
            return nil
        }

        let snapshot = try readSnapshot(from: legacyConfigFileURL)
        try persist(snapshot, to: configFileURL)
        return snapshot
    }

    private func persist(_ snapshot: RuntimeConfigSnapshot, to url: URL) throws {
        let directory = url.deletingLastPathComponent()

        if !FileManager.default.fileExists(atPath: directory.path) {
            try FileManager.default.createDirectory(
                at: directory,
                withIntermediateDirectories: true,
                attributes: [.posixPermissions: 0o700]
            )
        }

        let data = try AtlasJSON.encoder.encode(snapshot)

        let tempURL = directory.appendingPathComponent("config.tmp.\(UUID().uuidString)")
        try data.write(to: tempURL, options: .atomic)
        setSecurePermissionsIfPossible(for: tempURL)

        do {
            _ = try FileManager.default.replaceItemAt(url, withItemAt: tempURL)
        } catch {
            if FileManager.default.fileExists(atPath: url.path) {
                try FileManager.default.removeItem(at: url)
            }
            try FileManager.default.moveItem(at: tempURL, to: url)
        }

        setSecurePermissionsIfPossible(for: url)
    }

    /// Load the config snapshot from disk. Returns defaults if the file does not exist.
    public func load() async -> RuntimeConfigSnapshot {
        if let cached = cachedSnapshot {
            return cached
        }

        if let migrated = try? migrateLegacyConfigIfNeeded() {
            cachedSnapshot = migrated
            return migrated
        }

        let url = configFileURL

        guard FileManager.default.fileExists(atPath: url.path) else {
            let defaults = RuntimeConfigSnapshot.defaults
            cachedSnapshot = defaults
            return defaults
        }

        do {
            let snapshot = try readSnapshot(from: url)
            cachedSnapshot = snapshot
            return snapshot
        } catch {
            // If the file is corrupt or unreadable, fall back to defaults
            let defaults = RuntimeConfigSnapshot.defaults
            cachedSnapshot = defaults
            return defaults
        }
    }

    /// Persist the snapshot to disk atomically.
    public func save(_ snapshot: RuntimeConfigSnapshot) async throws {
        try persist(snapshot, to: configFileURL)
        cachedSnapshot = snapshot
    }

    /// Read a single typed value by key.
    public func value<T: Codable>(for key: RuntimeConfigKey) async -> T? {
        let snapshot = await load()
        switch key {
        case .runtimePort: return snapshot.runtimePort as? T
        case .telegramEnabled: return snapshot.telegramEnabled as? T
        case .discordEnabled: return snapshot.discordEnabled as? T
        case .discordClientID: return snapshot.discordClientID as? T
        case .slackEnabled: return snapshot.slackEnabled as? T
        case .telegramPollingTimeoutSeconds: return snapshot.telegramPollingTimeoutSeconds as? T
        case .telegramPollingRetryBaseSeconds: return snapshot.telegramPollingRetryBaseSeconds as? T
        case .telegramCommandPrefix: return snapshot.telegramCommandPrefix as? T
        case .telegramAllowedUserIDs: return snapshot.telegramAllowedUserIDs as? T
        case .telegramAllowedChatIDs: return snapshot.telegramAllowedChatIDs as? T
        case .defaultOpenAIModel: return snapshot.defaultOpenAIModel as? T
        case .baseSystemPrompt: return snapshot.baseSystemPrompt as? T
        case .maxAgentIterations: return snapshot.maxAgentIterations as? T
        case .conversationWindowLimit: return snapshot.conversationWindowLimit as? T
        case .memoryEnabled: return snapshot.memoryEnabled as? T
        case .maxRetrievedMemoriesPerTurn: return snapshot.maxRetrievedMemoriesPerTurn as? T
        case .memoryAutoSaveThreshold: return snapshot.memoryAutoSaveThreshold as? T
        case .personaName: return snapshot.personaName as? T
        case .actionSafetyMode: return snapshot.actionSafetyMode as? T
        case .activeImageProvider: return snapshot.activeImageProvider as? T
        case .activeAIProvider: return snapshot.activeAIProvider as? T
        case .lmStudioBaseURL: return snapshot.lmStudioBaseURL as? T
        case .selectedAnthropicModel: return snapshot.selectedAnthropicModel as? T
        case .selectedGeminiModel: return snapshot.selectedGeminiModel as? T
        case .selectedOpenAIPrimaryModel: return snapshot.selectedOpenAIPrimaryModel as? T
        case .selectedOpenAIFastModel: return snapshot.selectedOpenAIFastModel as? T
        case .selectedAnthropicFastModel: return snapshot.selectedAnthropicFastModel as? T
        case .selectedGeminiFastModel: return snapshot.selectedGeminiFastModel as? T
        case .selectedLMStudioModel: return snapshot.selectedLMStudioModel as? T
        case .lmStudioContextWindowLimit: return snapshot.lmStudioContextWindowLimit as? T
        case .lmStudioMaxAgentIterations: return snapshot.lmStudioMaxAgentIterations as? T
        case .enableSmartToolSelection: return snapshot.enableSmartToolSelection as? T
        case .webResearchUseJinaReader: return snapshot.webResearchUseJinaReader as? T
        case .enableMultiAgentOrchestration: return snapshot.enableMultiAgentOrchestration as? T
        case .maxParallelAgents: return snapshot.maxParallelAgents as? T
        case .workerMaxIterations: return snapshot.workerMaxIterations as? T
        case .remoteAccessEnabled: return snapshot.remoteAccessEnabled as? T
        }
    }

    /// Write a single typed value by key.
    public func setValue<T: Codable>(_ value: T, for key: RuntimeConfigKey) async throws {
        var snapshot = await load()
        switch key {
        case .runtimePort:
            if let v = value as? Int { snapshot.runtimePort = v }
        case .telegramEnabled:
            if let v = value as? Bool { snapshot.telegramEnabled = v }
        case .discordEnabled:
            if let v = value as? Bool { snapshot.discordEnabled = v }
        case .discordClientID:
            if let v = value as? String { snapshot.discordClientID = v }
        case .slackEnabled:
            if let v = value as? Bool { snapshot.slackEnabled = v }
        case .telegramPollingTimeoutSeconds:
            if let v = value as? Int { snapshot.telegramPollingTimeoutSeconds = v }
        case .telegramPollingRetryBaseSeconds:
            if let v = value as? Int { snapshot.telegramPollingRetryBaseSeconds = v }
        case .telegramCommandPrefix:
            if let v = value as? String { snapshot.telegramCommandPrefix = v }
        case .telegramAllowedUserIDs:
            if let v = value as? [Int64] { snapshot.telegramAllowedUserIDs = v }
        case .telegramAllowedChatIDs:
            if let v = value as? [Int64] { snapshot.telegramAllowedChatIDs = v }
        case .defaultOpenAIModel:
            if let v = value as? String { snapshot.defaultOpenAIModel = v }
        case .baseSystemPrompt:
            if let v = value as? String { snapshot.baseSystemPrompt = v }
        case .maxAgentIterations:
            if let v = value as? Int { snapshot.maxAgentIterations = v }
        case .conversationWindowLimit:
            if let v = value as? Int { snapshot.conversationWindowLimit = v }
        case .memoryEnabled:
            if let v = value as? Bool { snapshot.memoryEnabled = v }
        case .maxRetrievedMemoriesPerTurn:
            if let v = value as? Int { snapshot.maxRetrievedMemoriesPerTurn = v }
        case .memoryAutoSaveThreshold:
            if let v = value as? Double { snapshot.memoryAutoSaveThreshold = v }
        case .personaName:
            if let v = value as? String { snapshot.personaName = v }
        case .actionSafetyMode:
            if let v = value as? String { snapshot.actionSafetyMode = v }
        case .activeImageProvider:
            if let v = value as? String { snapshot.activeImageProvider = v }
        case .activeAIProvider:
            if let v = value as? String { snapshot.activeAIProvider = v }
        case .lmStudioBaseURL:
            if let v = value as? String { snapshot.lmStudioBaseURL = v }
        case .selectedAnthropicModel:
            if let v = value as? String { snapshot.selectedAnthropicModel = v }
        case .selectedGeminiModel:
            if let v = value as? String { snapshot.selectedGeminiModel = v }
        case .selectedOpenAIPrimaryModel:
            if let v = value as? String { snapshot.selectedOpenAIPrimaryModel = v }
        case .selectedOpenAIFastModel:
            if let v = value as? String { snapshot.selectedOpenAIFastModel = v }
        case .selectedAnthropicFastModel:
            if let v = value as? String { snapshot.selectedAnthropicFastModel = v }
        case .selectedGeminiFastModel:
            if let v = value as? String { snapshot.selectedGeminiFastModel = v }
        case .selectedLMStudioModel:
            if let v = value as? String { snapshot.selectedLMStudioModel = v }
        case .lmStudioContextWindowLimit:
            if let v = value as? Int { snapshot.lmStudioContextWindowLimit = v }
        case .lmStudioMaxAgentIterations:
            if let v = value as? Int { snapshot.lmStudioMaxAgentIterations = v }
        case .enableSmartToolSelection:
            if let v = value as? Bool { snapshot.enableSmartToolSelection = v }
        case .webResearchUseJinaReader:
            if let v = value as? Bool { snapshot.webResearchUseJinaReader = v }
        case .enableMultiAgentOrchestration:
            if let v = value as? Bool { snapshot.enableMultiAgentOrchestration = v }
        case .maxParallelAgents:
            if let v = value as? Int { snapshot.maxParallelAgents = max(2, min(v, 5)) }
        case .workerMaxIterations:
            if let v = value as? Int { snapshot.workerMaxIterations = max(1, min(v, 10)) }
        case .remoteAccessEnabled:
            if let v = value as? Bool { snapshot.remoteAccessEnabled = v }
        }
        try await save(snapshot)
    }
}
