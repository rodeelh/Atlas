import Foundation
import AtlasLogging
import AtlasShared

/// Dynamically selects the best available OpenAI models at daemon startup,
/// caches them, and triggers a background refresh every 24 hours.
///
/// - `primaryModel` — newest flagship model (largest, non-mini). Used for all
///   user-facing conversation turns and tool calls.
/// - `fastModel`    — newest mini/small model. Used only for cheap internal
///   tasks like MIND.md Tier 1 reflection.
///
/// There are NO hardcoded model name defaults. If `resolvedPrimaryModel()` or
/// `resolvedFastModel()` is called before a successful refresh, a synchronous
/// refresh is attempted first. Callers receive `nil` only if the API is
/// unreachable and no prior refresh has succeeded.
public actor ModelSelector {

    // MARK: - Public state

    public private(set) var primaryModel: String?
    public private(set) var fastModel: String?
    public private(set) var lastRefreshedAt: Date?
    /// Full list of available chat models, newest first. Empty until first successful refresh.
    public private(set) var availableModels: [AIModelRecord] = []

    // MARK: - Private

    private let apiKey: @Sendable () throws -> String
    private let session: URLSession
    private let refreshInterval: TimeInterval = 86_400 // 24 hours
    private let logger = AtlasLogger.network

    /// ID substrings that identify non-chat models to exclude entirely.
    private static let excludedSubstrings: [String] = [
        "whisper", "tts", "dall-e", "davinci", "babbage",
        "ada", "curie", "embedding", "moderation", "realtime",
        "transcribe", "instruct", "search", "similarity", "insert", "edit",
        "computer-use"
    ]

    /// ID substrings that mark a model as a "fast" (mini/small/nano) variant.
    private static let fastSubstrings: [String] = ["mini", "nano", "small", "fast"]

    // MARK: - Init

    public init(
        apiKey: @escaping @Sendable () throws -> String,
        session: URLSession = .shared
    ) {
        self.apiKey = apiKey
        self.session = session
    }

    // MARK: - Refresh

    /// Fetch the live model list from OpenAI and update the cached selections.
    /// Safe to call from daemon startup — any failure is logged and silently
    /// swallowed so the daemon continues.
    public func refresh() async {
        do {
            let key = try apiKey()

            var request = URLRequest(
                url: URL(string: "https://api.openai.com/v1/models")!,
                timeoutInterval: 10
            )
            request.httpMethod = "GET"
            request.setValue("Bearer \(key)", forHTTPHeaderField: "Authorization")

            let (data, response) = try await session.data(for: request)

            guard let http = response as? HTTPURLResponse else {
                logger.warning("ModelSelector: /v1/models response was not HTTP")
                return
            }
            guard (200..<300).contains(http.statusCode) else {
                logger.warning("ModelSelector: /v1/models returned \(http.statusCode)")
                return
            }

            let list = try AtlasJSON.decoder.decode(OpenAIModelsListResponse.self, from: data)
            applyModelList(list.data)

        } catch {
            logger.warning("ModelSelector: refresh failed — \(error.localizedDescription)")
        }
    }

    // MARK: - Resolved accessors

    /// Returns the resolved primary model, refreshing synchronously if not yet
    /// populated. Returns `nil` only if the API is unreachable.
    public func resolvedPrimaryModel() async -> String? {
        if primaryModel == nil { await refresh() }
        triggerBackgroundRefreshIfStale()
        return primaryModel
    }

    /// Returns the resolved fast model, refreshing synchronously if not yet
    /// populated. Returns `nil` only if the API is unreachable.
    public func resolvedFastModel() async -> String? {
        if fastModel == nil { await refresh() }
        triggerBackgroundRefreshIfStale()
        return fastModel
    }

    // MARK: - Private helpers

    private func applyModelList(_ records: [OpenAIModelRecord]) {
        // Filter out non-chat models regardless of owner.
        // Ownership strings vary (openai, openai-internal, system, etc.) so we
        // intentionally do NOT filter by owned_by — the exclude list is enough.
        let chatCandidates = records.filter { record in
            !Self.excludedSubstrings.contains(where: { record.id.lowercased().contains($0) })
        }

        // Split by fast vs primary, newest first.
        let fast = chatCandidates
            .filter { model in Self.fastSubstrings.contains(where: { model.id.lowercased().contains($0) }) }
            .sorted { ($0.created ?? 0) > ($1.created ?? 0) }

        let primary = chatCandidates
            .filter { model in !Self.fastSubstrings.contains(where: { model.id.lowercased().contains($0) }) }
            .sorted { ($0.created ?? 0) > ($1.created ?? 0) }

        if let selected = primary.first { primaryModel = selected.id }
        if let selected = fast.first { fastModel    = selected.id }

        availableModels = chatCandidates
            .sorted { ($0.created ?? 0) > ($1.created ?? 0) }
            .map { record in
                let isFast = Self.fastSubstrings.contains(where: { record.id.lowercased().contains($0) })
                return AIModelRecord(id: record.id, displayName: record.id, isFast: isFast)
            }

        lastRefreshedAt = Date()
        logger.info("ModelSelector: primary=\(primaryModel ?? "none") fast=\(fastModel ?? "none") (\(availableModels.count) models)")
    }

    private func triggerBackgroundRefreshIfStale() {
        guard let last = lastRefreshedAt else { return }
        guard Date().timeIntervalSince(last) > refreshInterval else { return }
        Task { await self.refresh() }
    }
}
