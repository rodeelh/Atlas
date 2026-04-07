import Foundation
import AtlasShared
import AtlasTools

// MARK: - Provider Protocol

public protocol WebSearchAPIProvider: Sendable {
    var providerName: String { get }
    func search(query: String, count: Int, freshness: String?) async throws -> [WebSearchAPIResult]
}

// MARK: - Result Model

public struct WebSearchAPIResult: Sendable {
    public let title: String
    public let url: String
    public let description: String
    public let age: String?

    public init(title: String, url: String, description: String, age: String? = nil) {
        self.title = title
        self.url = url
        self.description = description
        self.age = age
    }
}

// MARK: - Brave Search Provider

public struct BraveSearchAPIProvider: WebSearchAPIProvider, WebSearchProviding {
    public nonisolated let providerName = "Brave Search"
    private let apiKey: String
    private let session: URLSession

    public init(apiKey: String, session: URLSession? = nil) {
        self.apiKey = apiKey
        if let session {
            self.session = session
        } else {
            let config = URLSessionConfiguration.ephemeral
            config.timeoutIntervalForRequest = 10
            self.session = URLSession(configuration: config)
        }
    }

    // MARK: WebSearchProviding

    public nonisolated func validateProvider() -> WebSearchProviderValidation {
        WebSearchProviderValidation(
            isAvailable: true,
            summary: "Brave Search API is configured with a subscription key."
        )
    }

    /// Conforms to `WebSearchProviding` — translates allowedDomains to `site:` operators.
    public func search(
        query: String,
        allowedDomains: [String]?,
        maxResults: Int
    ) async throws -> [WebSearchResult] {
        let siteQuery: String
        if let domains = allowedDomains, !domains.isEmpty {
            let siteFilter = domains.map { "site:\($0)" }.joined(separator: " OR ")
            siteQuery = "\(query) (\(siteFilter))"
        } else {
            siteQuery = query
        }
        let apiResults = try await search(query: siteQuery, count: min(max(maxResults, 1), 20), freshness: nil)
        return apiResults.map { r in
            WebSearchResult(
                title: r.title,
                url: r.url,
                snippet: r.description,
                domain: URL(string: r.url)?.host?.lowercased() ?? "",
                publishedAt: r.age
            )
        }
    }

    /// Freshness-aware news search — passes the Brave `freshness` parameter directly.
    public func searchNews(
        query: String,
        allowedDomains: [String]?,
        maxResults: Int,
        freshness: String?
    ) async throws -> [WebSearchResult] {
        let count = min(max(maxResults, 1), 20)
        let apiResults = try await search(query: query, count: count, freshness: freshness)
        return apiResults.map { r in
            WebSearchResult(
                title: r.title,
                url: r.url,
                snippet: r.description,
                domain: URL(string: r.url)?.host?.lowercased() ?? "",
                publishedAt: r.age
            )
        }
    }

    // MARK: WebSearchAPIProvider

    public func search(query: String, count: Int, freshness: String? = nil) async throws -> [WebSearchAPIResult] {
        var components = URLComponents(string: "https://api.search.brave.com/res/v1/web/search")!
        var queryItems: [URLQueryItem] = [
            URLQueryItem(name: "q", value: query),
            URLQueryItem(name: "count", value: "\(min(max(count, 1), 20))"),
            URLQueryItem(name: "result_filter", value: "web")
        ]
        if let freshness {
            queryItems.append(URLQueryItem(name: "freshness", value: freshness))
        }
        components.queryItems = queryItems

        guard let url = components.url else {
            throw WebSearchAPIError.invalidQuery
        }

        var request = URLRequest(url: url)
        request.setValue("application/json", forHTTPHeaderField: "Accept")
        request.setValue(apiKey, forHTTPHeaderField: "X-Subscription-Token")

        let (data, response) = try await session.data(for: request)

        guard let http = response as? HTTPURLResponse else {
            throw WebSearchAPIError.networkError("No HTTP response")
        }

        guard http.statusCode == 200 else {
            if http.statusCode == 401 || http.statusCode == 403 {
                throw WebSearchAPIError.invalidAPIKey
            }
            throw WebSearchAPIError.networkError("HTTP \(http.statusCode)")
        }

        return try parseBraveResponse(data)
    }

    private func parseBraveResponse(_ data: Data) throws -> [WebSearchAPIResult] {
        guard let json = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
              let web = json["web"] as? [String: Any],
              let results = web["results"] as? [[String: Any]] else {
            return []
        }

        return results.compactMap { item in
            guard let title = item["title"] as? String,
                  let url = item["url"] as? String else { return nil }
            let description = item["description"] as? String ?? ""
            let age = item["age"] as? String
            return WebSearchAPIResult(title: title, url: url, description: description, age: age)
        }
    }
}

// MARK: - Errors

enum WebSearchAPIError: LocalizedError {
    case notConfigured
    case invalidAPIKey
    case invalidQuery
    case networkError(String)

    var errorDescription: String? {
        switch self {
        case .notConfigured:
            return "Web Search API skill is not configured. Add a Brave Search API key in the Atlas app."
        case .invalidAPIKey:
            return "The Brave Search API key is invalid or expired. Update it in the Atlas app."
        case .invalidQuery:
            return "Could not build a valid search URL."
        case .networkError(let detail):
            return "Search request failed: \(detail)"
        }
    }
}

// MARK: - WebSearchAPISkill

public struct WebSearchAPISkill: AtlasSkill {
    public let manifest: SkillManifest
    public let actions: [SkillActionDefinition]

    private let provider: (any WebSearchAPIProvider)?

    public init(provider: (any WebSearchAPIProvider)? = nil) {
        let config = AtlasConfig()
        let resolvedProvider: (any WebSearchAPIProvider)? = provider
            ?? (config.hasBraveSearchAPIKey()
                ? (try? config.braveSearchAPIKey()).map { BraveSearchAPIProvider(apiKey: $0) }
                : nil)
        self.provider = resolvedProvider

        self.manifest = SkillManifest(
            id: "websearch-api",
            name: "Web Search API",
            version: "1.0.0",
            description: "Search the web via the Brave Search API and return structured results with titles, URLs, and snippets.",
            category: .research,
            lifecycleState: .installed,
            capabilities: [.publicWebSearch],
            requiredPermissions: [.publicWebRead],
            riskLevel: .low,
            trustProfile: .exactStructured,
            freshnessType: .live,
            preferredQueryTypes: [.exploratoryResearch, .docsResearch],
            routingPriority: 85,
            canAnswerStructuredLiveData: true,
            restrictionsSummary: [
                "Provider: Brave Search API",
                "Requires Brave Search API key",
                "GET only — no writes",
                "Returns up to 10 results per query"
            ],
            supportsReadOnlyMode: true,
            isUserVisible: false,
            isEnabledByDefault: false,
            author: "Project Atlas",
            source: "built_in",
            tags: ["search", "web", "brave", "research"],
            intent: .liveStructuredData,
            triggers: [
                .init("search for", queryType: .exploratoryResearch),
                .init("look up", queryType: .exploratoryResearch),
                .init("find me", queryType: .exploratoryResearch),
                .init("google", queryType: .exploratoryResearch),
                .init("search the web", queryType: .exploratoryResearch),
                .init("what is", queryType: .exploratoryResearch),
                .init("who is", queryType: .exploratoryResearch),
                .init("latest news", queryType: .exploratoryResearch),
                .init("news about", queryType: .exploratoryResearch),
                .init("current price", queryType: .exploratoryResearch),
                .init("what's happening", queryType: .exploratoryResearch),
                .init("look into", queryType: .exploratoryResearch)
            ]
        )

        self.actions = [
            SkillActionDefinition(
                id: "websearch-api.query",
                name: "Web Search",
                description: "Search the web using Brave Search and return structured results including title, URL, and description for each hit.",
                inputSchemaSummary: "query (required): search terms. count (optional, 1–10, default 5): number of results.",
                outputSchemaSummary: "Numbered list of results with title, URL, snippet, and optional age.",
                permissionLevel: .read,
                sideEffectLevel: .safeRead,
                preferredQueryTypes: [.exploratoryResearch],
                routingPriority: 85,
                inputSchema: AtlasToolInputSchema(
                    properties: [
                        "query": .init(
                            type: "string",
                            description: "The search query."
                        ),
                        "count": .init(
                            type: "integer",
                            description: "Number of results to return (1–10). Defaults to 5."
                        )
                    ],
                    required: ["query"]
                )
            )
        ]
    }

    public func validateConfiguration(context: SkillValidationContext) async -> SkillValidationResult {
        guard provider != nil else {
            return SkillValidationResult(
                skillID: manifest.id,
                status: .failed,
                summary: "Brave Search API key not set. Add it in the Atlas app under Settings > API Keys.",
                issues: ["Missing Brave Search API key."]
            )
        }
        return SkillValidationResult(
            skillID: manifest.id,
            status: .passed,
            summary: "Brave Search API key is configured."
        )
    }

    public func execute(
        actionID: String,
        input: AtlasToolInput,
        context: SkillExecutionContext
    ) async throws -> SkillExecutionResult {
        guard actionID == "websearch-api.query" else {
            throw AtlasToolError.invalidInput("Unknown action '\(actionID)'.")
        }

        guard let provider else {
            throw WebSearchAPIError.notConfigured
        }

        struct SearchInput: Decodable {
            let query: String
            let count: Int?
        }
        let payload = try input.decode(SearchInput.self)
        let query = payload.query.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !query.isEmpty else {
            throw AtlasToolError.invalidInput("The 'query' parameter is required.")
        }

        let count = min(max(payload.count ?? 5, 1), 10)
        let results = try await provider.search(query: query, count: count, freshness: nil)

        if results.isEmpty {
            return SkillExecutionResult(
                skillID: manifest.id,
                actionID: actionID,
                output: "No results found for \"\(query)\".",
                summary: "No results found for \"\(query)\"."
            )
        }

        var lines: [String] = ["Search results for \"\(query)\":\n"]
        for (i, result) in results.enumerated() {
            var entry = "\(i + 1). **\(result.title)**\n   \(result.url)"
            if !result.description.isEmpty {
                entry += "\n   \(result.description)"
            }
            if let age = result.age {
                entry += "\n   *\(age)*"
            }
            lines.append(entry)
        }

        return SkillExecutionResult(
            skillID: manifest.id,
            actionID: actionID,
            output: lines.joined(separator: "\n\n"),
            summary: "Found \(results.count) result(s) for \"\(query)\".",
            metadata: ["provider": provider.providerName, "resultCount": "\(results.count)"]
        )
    }
}
