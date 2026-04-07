import Foundation
import AtlasShared
import AtlasLogging

/// Wraps `ModelSelector` (for OpenAI dynamic discovery) and adds static model catalogs for
/// Anthropic and Gemini, plus dynamic discovery for LM Studio's local `/v1/models` endpoint.
///
/// Drop-in replacement for `ModelSelector` in `AgentContext`.  Exposes the same surface:
/// `primaryModel`, `fastModel`, `lastRefreshedAt`, `resolvedPrimaryModel()`, `resolvedFastModel()`,
/// `refresh()`, plus `availableModels()` for the settings UI.
public actor ProviderAwareModelSelector {

    // MARK: - Public state (mirrors ModelSelector)

    public private(set) var primaryModel: String?
    public private(set) var fastModel: String?
    public private(set) var lastRefreshedAt: Date?

    // MARK: - Private

    private let config: AtlasConfig
    private let openAISelector: ModelSelector
    private let session: URLSession
    private let logger = AtlasLogger.network

    /// Cached models per provider (populated on refresh).
    private var lmStudioModels: [AIModelRecord] = []
    private var geminiModels: [AIModelRecord] = []
    private var anthropicModels: [AIModelRecord] = []

    // MARK: - Init

    public init(
        config: AtlasConfig,
        openAISelector: ModelSelector? = nil,
        session: URLSession = .shared
    ) {
        self.config          = config
        self.session         = session
        self.openAISelector  = openAISelector ?? ModelSelector(apiKey: { try config.openAIAPIKey() })
    }

    // MARK: - Resolved accessors (same API as ModelSelector)

    /// Returns the best primary model for the active provider.
    /// Triggers a synchronous refresh if not yet populated.
    public func resolvedPrimaryModel() async -> String? {
        if primaryModel == nil { await refresh() }
        return primaryModel
    }

    /// Returns the fast model for the active provider.
    /// Triggers a synchronous refresh if not yet populated.
    public func resolvedFastModel() async -> String? {
        if fastModel == nil { await refresh() }
        return fastModel
    }

    // MARK: - Refresh

    /// Refresh model availability for the active provider.
    public func refresh() async {
        switch config.activeAIProvider {
        case .openAI:
            await openAISelector.refresh()
            let available        = await openAISelector.availableModels
            let autoPrimary      = await openAISelector.primaryModel
            let autoFast         = await openAISelector.fastModel
            primaryModel     = resolveUserModel(config.selectedOpenAIPrimaryModel, models: available, preferFast: false) ?? autoPrimary
            fastModel        = resolveUserModel(config.selectedOpenAIFastModel, models: available, preferFast: true) ?? autoFast
            lastRefreshedAt  = await openAISelector.lastRefreshedAt

        case .anthropic:
            await refreshAnthropic()

        case .gemini:
            await refreshGemini()

        case .lmStudio:
            await refreshLMStudio()
        }
    }

    // MARK: - Available models (for Settings UI)

    /// Returns the full available model list for the active provider.
    /// For OpenAI this queries the OpenAI API; for Anthropic/Gemini it returns the static catalog;
    /// for LM Studio it queries the local server.
    public func availableModels() async -> [AIModelRecord] {
        switch config.activeAIProvider {
        case .openAI:
            let models = await openAISelector.availableModels
            if models.isEmpty { await refresh() }
            return curate(await openAISelector.availableModels)

        case .anthropic:
            if anthropicModels.isEmpty { await refreshAnthropic() }
            return anthropicModels.isEmpty ? AIProvider.anthropic.staticModels : anthropicModels

        case .gemini:
            if geminiModels.isEmpty { await refreshGemini() }
            return geminiModels.isEmpty ? AIProvider.gemini.staticModels : geminiModels

        case .lmStudio:
            if lmStudioModels.isEmpty { await refreshLMStudio() }
            return lmStudioModels
        }
    }

    // MARK: - Anthropic discovery

    private struct AnthropicModelsResponse: Decodable {
        struct Model: Decodable {
            let id: String
            let display_name: String
        }
        let data: [Model]
    }

    private func refreshAnthropic() async {
        let apiKey: String
        do { apiKey = try config.anthropicAPIKey() } catch {
            logger.warning("ProviderAwareModelSelector: no Anthropic API key — using static catalog")
            applyAnthropicFallback(); return
        }

        guard let url = URL(string: "https://api.anthropic.com/v1/models") else { return }
        var req = URLRequest(url: url, timeoutInterval: 10)
        req.httpMethod = "GET"
        req.setValue(apiKey, forHTTPHeaderField: "x-api-key")
        req.setValue("2023-06-01", forHTTPHeaderField: "anthropic-version")

        do {
            let (data, response) = try await session.data(for: req)
            guard let http = response as? HTTPURLResponse, (200..<300).contains(http.statusCode) else {
                logger.warning("ProviderAwareModelSelector: Anthropic /v1/models non-2xx — using static catalog")
                applyAnthropicFallback(); return
            }
            let list = try JSONDecoder().decode(AnthropicModelsResponse.self, from: data)
            let allAnthropic = list.data.map { m in
                AIModelRecord(id: m.id, displayName: m.display_name, isFast: m.id.lowercased().contains("haiku"))
            }
            anthropicModels = curate(allAnthropic)
            primaryModel    = resolveUserModel(config.selectedAnthropicModel, models: anthropicModels, preferFast: false)
            fastModel       = resolveUserModel(config.selectedAnthropicFastModel, models: anthropicModels, preferFast: true)
            lastRefreshedAt = Date()
            logger.info("ProviderAwareModelSelector (Anthropic): fetched \(anthropicModels.count) models, primary=\(primaryModel ?? "none")")
        } catch {
            logger.warning("ProviderAwareModelSelector: Anthropic refresh error — \(error.localizedDescription) — using static catalog")
            applyAnthropicFallback()
        }
    }

    private func applyAnthropicFallback() {
        let models = AIProvider.anthropic.staticModels
        primaryModel    = resolveUserModel(config.selectedAnthropicModel, models: models, preferFast: false)
        fastModel       = resolveUserModel(config.selectedAnthropicFastModel, models: models, preferFast: true)
        lastRefreshedAt = Date()
    }

    // MARK: - Gemini discovery

    private struct GeminiModelsResponse: Decodable {
        struct Model: Decodable {
            let name: String
            let displayName: String
            let supportedGenerationMethods: [String]
        }
        let models: [Model]
    }

    private func refreshGemini() async {
        let apiKey: String
        do { apiKey = try config.geminiAPIKey() } catch {
            logger.warning("ProviderAwareModelSelector: no Gemini API key — using static catalog")
            applyGeminiFallback(); return
        }

        guard let url = URL(string: "https://generativelanguage.googleapis.com/v1beta/models?key=\(apiKey)") else { return }
        var req = URLRequest(url: url, timeoutInterval: 10)
        req.httpMethod = "GET"

        do {
            let (data, response) = try await session.data(for: req)
            guard let http = response as? HTTPURLResponse, (200..<300).contains(http.statusCode) else {
                logger.warning("ProviderAwareModelSelector: Gemini models non-2xx — using static catalog")
                applyGeminiFallback(); return
            }
            let list = try JSONDecoder().decode(GeminiModelsResponse.self, from: data)
            let allGemini: [AIModelRecord] = list.models
                .filter { $0.supportedGenerationMethods.contains("generateContent") }
                .compactMap { m -> AIModelRecord? in
                    let id = m.name.hasPrefix("models/") ? String(m.name.dropFirst(7)) : m.name
                    let lower = id.lowercased()
                    // Exclude non-conversational, experimental, and developer/tuning models
                    let excluded = ["embedding", "aqa", "retrieval", "tuning", "-exp", "preview"]
                    guard !excluded.contains(where: { lower.contains($0) }) else { return nil }
                    let fast = lower.contains("lite") || lower.contains("nano") || lower.contains("flash-8b")
                    return AIModelRecord(id: id, displayName: m.displayName, isFast: fast)
                }
            geminiModels = curate(allGemini)
            primaryModel    = resolveUserModel(config.selectedGeminiModel, models: geminiModels, preferFast: false)
            fastModel       = resolveUserModel(config.selectedGeminiFastModel, models: geminiModels, preferFast: true)
            lastRefreshedAt = Date()
            logger.info("ProviderAwareModelSelector (Gemini): fetched \(geminiModels.count) models, primary=\(primaryModel ?? "none")")
        } catch {
            logger.warning("ProviderAwareModelSelector: Gemini refresh error — \(error.localizedDescription) — using static catalog")
            applyGeminiFallback()
        }
    }

    private func applyGeminiFallback() {
        let models = AIProvider.gemini.staticModels
        primaryModel    = resolveUserModel(config.selectedGeminiModel, models: models, preferFast: false)
        fastModel       = resolveUserModel(config.selectedGeminiFastModel, models: models, preferFast: true)
        lastRefreshedAt = Date()
    }

    // MARK: - LM Studio discovery

    private func refreshLMStudio() async {
        let base = config.lmStudioBaseURL.trimmingCharacters(in: CharacterSet(charactersIn: "/"))
        guard let url = URL(string: "\(base)/v1/models") else {
            logger.warning("ProviderAwareModelSelector: invalid LM Studio base URL '\(config.lmStudioBaseURL)'")
            return
        }

        do {
            var req = URLRequest(url: url, timeoutInterval: 5)
            req.httpMethod = "GET"
            if let key = try? config.lmStudioAPIKey(), !key.isEmpty {
                req.setValue("Bearer \(key)", forHTTPHeaderField: "Authorization")
            }
            let (data, response) = try await session.data(for: req)
            guard let http = response as? HTTPURLResponse, (200..<300).contains(http.statusCode) else {
                logger.warning("ProviderAwareModelSelector: LM Studio /v1/models returned non-2xx")
                return
            }

            let list = try AtlasJSON.decoder.decode(OpenAIModelsListResponse.self, from: data)
            lmStudioModels = list.data.map { record in
                AIModelRecord(id: record.id, displayName: record.id, isFast: false)
            }
            let resolved = !config.selectedLMStudioModel.isEmpty && lmStudioModels.contains(where: { $0.id == config.selectedLMStudioModel })
                ? config.selectedLMStudioModel
                : lmStudioModels.first?.id
            primaryModel    = resolved
            fastModel       = resolved   // no fast distinction for local models
            lastRefreshedAt = Date()
            logger.info("ProviderAwareModelSelector (LM Studio): found \(lmStudioModels.count) models, primary=\(primaryModel ?? "none")")
        } catch {
            logger.warning("ProviderAwareModelSelector: LM Studio refresh failed — \(error.localizedDescription)")
        }
    }

    // MARK: - Private helpers

    /// Pick a model from the catalog. If the user override is non-empty and exists in the catalog,
    /// use it; otherwise fall back to the first primary (or fast) model in the catalog.
    private func resolveUserModel(_ userOverride: String, models: [AIModelRecord], preferFast: Bool) -> String? {
        let candidates = models.filter { $0.isFast == preferFast }
        if !userOverride.isEmpty, models.contains(where: { $0.id == userOverride }) {
            return userOverride
        }
        return candidates.first?.id ?? models.first?.id
    }

    /// Curates a model list for display in the Settings UI — top `maxPrimary` flagship models
    /// and top `maxFast` fast models. Keeps the list focused for non-developer users.
    private func curate(_ models: [AIModelRecord], maxPrimary: Int = 5, maxFast: Int = 5) -> [AIModelRecord] {
        let primary = models.filter { !$0.isFast }.prefix(maxPrimary)
        let fast    = models.filter {  $0.isFast }.prefix(maxFast)
        return Array(primary) + Array(fast)
    }
}
