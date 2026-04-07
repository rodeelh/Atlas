import Foundation
import AtlasLogging
import AtlasShared
import AtlasTools

public struct WebResearchSkill: AtlasSkill {
    public let manifest: SkillManifest
    public let actions: [SkillActionDefinition]

    private let providers: [any WebSearchProviding]
    private let fetchClient: any WebFetching
    private let extractor: WebContentExtractor
    private let domainPolicy: WebDomainPolicy
    private let orchestrator: any WebResearchOrchestrating

    public init(
        providers: [any WebSearchProviding]? = nil,
        searchProvider: (any WebSearchProviding)? = nil,
        fetchClient: (any WebFetching)? = nil,
        extractor: WebContentExtractor? = nil,
        domainPolicy: WebDomainPolicy = WebDomainPolicy(),
        useJinaReader: Bool = false,
        orchestrator: (any WebResearchOrchestrating)? = nil
    ) {
        let resolvedFetchClient = fetchClient ?? WebFetchClient(policy: domainPolicy)
        let resolvedExtractor = extractor ?? WebContentExtractor(useJinaReader: useJinaReader)

        // Provider chain: explicit list → single provider shim → default DDG
        let resolvedProviders: [any WebSearchProviding]
        if let providers, !providers.isEmpty {
            resolvedProviders = providers
        } else if let single = searchProvider {
            resolvedProviders = [single]
        } else {
            resolvedProviders = [
                DuckDuckGoHTMLSearchProvider(fetchClient: resolvedFetchClient, domainPolicy: domainPolicy)
            ]
        }

        self.providers = resolvedProviders
        self.fetchClient = resolvedFetchClient
        self.extractor = resolvedExtractor
        self.domainPolicy = domainPolicy
        self.orchestrator = orchestrator ?? WebResearchOrchestrator(
            providers: resolvedProviders,
            fetchClient: resolvedFetchClient,
            extractor: resolvedExtractor,
            domainPolicy: domainPolicy
        )

        self.manifest = SkillManifest(
            id: "web-research",
            name: "Web Research",
            version: "2.0.0",
            description: "Search public web content, fetch pages, synthesize research, check URLs, extract links, and run parallel multi-query searches.",
            category: .research,
            lifecycleState: .installed,
            capabilities: [
                .publicWebSearch,
                .publicWebFetch,
                .researchSynthesis
            ],
            requiredPermissions: [
                .publicWebRead
            ],
            riskLevel: .medium,
            trustProfile: .exploratory,
            freshnessType: .external,
            preferredQueryTypes: [.docsResearch, .comparisonResearch, .exploratoryResearch],
            routingPriority: 20,
            canHandleExploratoryQueries: true,
            restrictionsSummary: [
                "GET only",
                "No authentication or cookies",
                "Public domains only",
                "Local and private network targets are blocked"
            ],
            supportsReadOnlyMode: true,
            isUserVisible: true,
            isEnabledByDefault: true,
            author: "Project Atlas",
            source: "built_in",
            tags: ["web", "research", "read_only"],
            intent: .exploratoryResearch,
            triggers: [
                .init("find information about", queryType: .exploratoryResearch),
                .init("difference between", queryType: .comparisonResearch),
                .init("compare", queryType: .comparisonResearch),
                .init("comparison", queryType: .comparisonResearch),
                .init("versus", queryType: .comparisonResearch),
                .init(" vs ", queryType: .comparisonResearch),
                .init("api documentation", queryType: .docsResearch),
                .init("documentation for", queryType: .docsResearch),
                .init("docs for", queryType: .docsResearch),
                .init("research", queryType: .exploratoryResearch),
                .init("look up", queryType: .exploratoryResearch),
                .init("learn about", queryType: .exploratoryResearch),
                .init("summarize", queryType: .exploratoryResearch),
                .init("find sources", queryType: .exploratoryResearch),
                .init("latest news", queryType: .exploratoryResearch),
                .init("recent news", queryType: .exploratoryResearch),
                .init("who is ", queryType: .exploratoryResearch),
                .init("what is ", queryType: .exploratoryResearch),
                .init("how does", queryType: .exploratoryResearch),
                .init("when did", queryType: .exploratoryResearch),
                .init("where is ", queryType: .exploratoryResearch),
                .init("explain ", queryType: .exploratoryResearch)
            ]
        )

        self.actions = [
            SkillActionDefinition(
                id: "web.search",
                name: "Web Search",
                description: "Search public web content and return a normalized list of relevant sources.",
                inputSchemaSummary: "query is required; allowedDomains and maxResults are optional.",
                outputSchemaSummary: "Structured search results with title, url, snippet, domain, and optional publishedAt.",
                permissionLevel: .read,
                sideEffectLevel: .safeRead,
                isEnabled: true,
                preferredQueryTypes: [.docsResearch, .comparisonResearch, .exploratoryResearch],
                routingPriority: 20,
                inputSchema: AtlasToolInputSchema(
                    properties: [
                        "query": AtlasToolInputProperty(type: "string", description: "Public web search query."),
                        "allowedDomains": AtlasToolInputProperty(
                            type: "array",
                            description: "Optional domain allowlist.",
                            items: AtlasToolInputArrayItems(type: "string", description: "Allowed public domain.")
                        ),
                        "maxResults": AtlasToolInputProperty(type: "integer", description: "Optional result cap up to 10.")
                    ],
                    required: ["query"]
                )
            ),
            SkillActionDefinition(
                id: "web.fetch_page",
                name: "Fetch Page",
                description: "Fetch a single public web page and return cleaned readable text.",
                inputSchemaSummary: "url is required.",
                outputSchemaSummary: "Structured page content with title, domain, contentType, fetchedAt, and extractedText.",
                permissionLevel: .read,
                sideEffectLevel: .safeRead,
                isEnabled: true,
                preferredQueryTypes: [.docsResearch, .exploratoryResearch],
                routingPriority: 15,
                inputSchema: AtlasToolInputSchema(
                    properties: [
                        "url": AtlasToolInputProperty(type: "string", description: "Public http or https URL to fetch.")
                    ],
                    required: ["url"]
                )
            ),
            SkillActionDefinition(
                id: "web.research",
                name: "Research Topic",
                description: "Search public sources, fetch readable pages, and synthesize a concise research summary with references.",
                inputSchemaSummary: "query is required; allowedDomains and maxSources are optional.",
                outputSchemaSummary: "Structured research summary, key points, source references, caveats, confidence summary, and failed sources.",
                permissionLevel: .read,
                sideEffectLevel: .safeRead,
                isEnabled: true,
                preferredQueryTypes: [.comparisonResearch, .exploratoryResearch, .docsResearch],
                routingPriority: 30,
                inputSchema: AtlasToolInputSchema(
                    properties: [
                        "query": AtlasToolInputProperty(type: "string", description: "Research question or topic."),
                        "allowedDomains": AtlasToolInputProperty(
                            type: "array",
                            description: "Optional domain allowlist.",
                            items: AtlasToolInputArrayItems(type: "string", description: "Allowed public domain.")
                        ),
                        "maxSources": AtlasToolInputProperty(type: "integer", description: "Optional cap up to 8 readable sources.")
                    ],
                    required: ["query"]
                )
            ),
            SkillActionDefinition(
                id: "web.news",
                name: "News Search",
                description: "Search for recent news articles on a topic, with optional recency filtering. Returns structured news results with titles, URLs, and snippets.",
                inputSchemaSummary: "query is required; recency (day/week/month) and maxResults are optional.",
                outputSchemaSummary: "Structured search results with title, url, snippet, domain, and optional publishedAt.",
                permissionLevel: .read,
                sideEffectLevel: .safeRead,
                isEnabled: true,
                preferredQueryTypes: [.exploratoryResearch],
                routingPriority: 25,
                inputSchema: AtlasToolInputSchema(
                    properties: [
                        "query": AtlasToolInputProperty(type: "string", description: "News topic or search query."),
                        "recency": AtlasToolInputProperty(
                            type: "string",
                            description: "How recent: 'day' (past 24h), 'week' (past 7 days), or 'month' (past 30 days). Defaults to 'day'."
                        ),
                        "maxResults": AtlasToolInputProperty(type: "integer", description: "Optional result cap up to 10. Defaults to 10.")
                    ],
                    required: ["query"]
                )
            ),
            SkillActionDefinition(
                id: "web.check_url",
                name: "Check URL",
                description: "Check if a public URL is reachable and return its HTTP status code and content type without downloading the full body.",
                inputSchemaSummary: "url is required.",
                outputSchemaSummary: "statusCode, contentType, reachable flag, and a message.",
                permissionLevel: .read,
                sideEffectLevel: .safeRead,
                isEnabled: true,
                preferredQueryTypes: [.docsResearch, .exploratoryResearch],
                routingPriority: 10,
                inputSchema: AtlasToolInputSchema(
                    properties: [
                        "url": AtlasToolInputProperty(type: "string", description: "Public http or https URL to check.")
                    ],
                    required: ["url"]
                )
            ),
            SkillActionDefinition(
                id: "web.multi_search",
                name: "Multi Search",
                description: "Run 2–5 search queries in parallel and return a merged, deduplicated result list.",
                inputSchemaSummary: "queries (2–5 items) is required; allowedDomains and maxResultsPerQuery are optional.",
                outputSchemaSummary: "Merged deduplicated results with queryCount and totalBeforeDedup.",
                permissionLevel: .read,
                sideEffectLevel: .safeRead,
                isEnabled: true,
                preferredQueryTypes: [.comparisonResearch, .exploratoryResearch],
                routingPriority: 22,
                inputSchema: AtlasToolInputSchema(
                    properties: [
                        "queries": AtlasToolInputProperty(
                            type: "array",
                            description: "2–5 search queries to run in parallel.",
                            items: AtlasToolInputArrayItems(type: "string", description: "A search query string.")
                        ),
                        "allowedDomains": AtlasToolInputProperty(
                            type: "array",
                            description: "Optional domain allowlist applied to all queries.",
                            items: AtlasToolInputArrayItems(type: "string", description: "Allowed public domain.")
                        ),
                        "maxResultsPerQuery": AtlasToolInputProperty(type: "integer", description: "Max results per query (1–10). Defaults to 5.")
                    ],
                    required: ["queries"]
                )
            ),
            SkillActionDefinition(
                id: "web.extract_links",
                name: "Extract Links",
                description: "Fetch a public web page and return all outbound links with their anchor text.",
                inputSchemaSummary: "url is required; allowedDomains is optional.",
                outputSchemaSummary: "List of extracted links with url, text, and domain.",
                permissionLevel: .read,
                sideEffectLevel: .safeRead,
                isEnabled: true,
                preferredQueryTypes: [.docsResearch, .exploratoryResearch],
                routingPriority: 12,
                inputSchema: AtlasToolInputSchema(
                    properties: [
                        "url": AtlasToolInputProperty(type: "string", description: "Public http or https URL to extract links from."),
                        "allowedDomains": AtlasToolInputProperty(
                            type: "array",
                            description: "Optional domain filter for returned links.",
                            items: AtlasToolInputArrayItems(type: "string", description: "Allowed public domain.")
                        )
                    ],
                    required: ["url"]
                )
            ),
            SkillActionDefinition(
                id: "web.summarize_url",
                name: "Summarize URL",
                description: "Fetch a public web page and return a structured digest with a summary and key points extracted from the content.",
                inputSchemaSummary: "url is required.",
                outputSchemaSummary: "title, url, domain, summary, keyPoints, contentType, and truncated flag.",
                permissionLevel: .read,
                sideEffectLevel: .safeRead,
                isEnabled: true,
                preferredQueryTypes: [.docsResearch, .exploratoryResearch],
                routingPriority: 18,
                inputSchema: AtlasToolInputSchema(
                    properties: [
                        "url": AtlasToolInputProperty(type: "string", description: "Public http or https URL to summarize.")
                    ],
                    required: ["url"]
                )
            )
        ]
    }

    public func validateConfiguration(context: SkillValidationContext) async -> SkillValidationResult {
        let providerValidation = providers.first?.validateProvider()
            ?? WebSearchProviderValidation(isAvailable: false, summary: "No search providers configured.")
        let clientIssues = fetchClient.validateClient()
        let issues = providerValidation.issues + clientIssues

        let status: SkillValidationStatus = issues.isEmpty ? .passed : .warning
        let summary = issues.isEmpty
            ? "Public web search and fetch services are ready in read-only mode."
            : "Web Research is available with cautions: \(issues.joined(separator: "; "))"

        return SkillValidationResult(
            skillID: manifest.id,
            status: providerValidation.isAvailable ? status : .failed,
            summary: providerValidation.isAvailable ? summary : providerValidation.summary,
            issues: providerValidation.isAvailable ? issues : providerValidation.issues,
            validatedAt: .now
        )
    }

    public func execute(
        actionID: String,
        input: AtlasToolInput,
        context: SkillExecutionContext
    ) async throws -> SkillExecutionResult {
        switch actionID {
        case "web.search":       return try await search(input: input, context: context)
        case "web.fetch_page":   return try await fetchPage(input: input, context: context)
        case "web.research":     return try await research(input: input, context: context)
        case "web.news":         return try await news(input: input, context: context)
        case "web.check_url":    return try await checkURL(input: input, context: context)
        case "web.multi_search": return try await multiSearch(input: input, context: context)
        case "web.extract_links": return try await extractLinks(input: input, context: context)
        case "web.summarize_url": return try await summarizeURL(input: input, context: context)
        default:
            throw AtlasToolError.invalidInput("The action '\(actionID)' is not supported by Web Research.")
        }
    }

    // MARK: - web.search

    private func search(
        input: AtlasToolInput,
        context: SkillExecutionContext
    ) async throws -> SkillExecutionResult {
        let payload = try input.decode(WebSearchInput.self)
        let query = try domainPolicy.normalizeQuery(payload.query)
        let allowedDomains = effectiveAllowedDomains(requested: payload.allowedDomains)
        let maxResults = min(max(payload.maxResults ?? 5, 1), 10)

        context.logger.info("Executing Web Research search", metadata: [
            "skill_id": manifest.id,
            "action_id": "web.search",
            "query": summarize(query),
            "allowed_domains": allowedDomains?.joined(separator: ",") ?? "all",
            "max_results": "\(maxResults)"
        ])

        var results: [WebSearchResult] = []
        for provider in providers {
            do {
                let r = try await provider.search(query: query, allowedDomains: allowedDomains, maxResults: maxResults)
                if !r.isEmpty { results = r; break }
            } catch { continue }
        }

        let output = WebSearchOutput(results: results)
        return SkillExecutionResult(
            skillID: manifest.id,
            actionID: "web.search",
            output: try encode(output),
            summary: "Found \(results.count) public search result\(results.count == 1 ? "" : "s") for \"\(query)\".",
            metadata: [
                "result_count": "\(results.count)",
                "allowed_domains": allowedDomains?.joined(separator: ",") ?? "all"
            ]
        )
    }

    // MARK: - web.fetch_page

    private func fetchPage(
        input: AtlasToolInput,
        context: SkillExecutionContext
    ) async throws -> SkillExecutionResult {
        let payload = try input.decode(WebFetchPageInput.self)
        let url = try domainPolicy.validateURLString(payload.url, allowedDomains: manifest.allowedDomains)

        context.logger.info("Executing Web Research page fetch", metadata: [
            "skill_id": manifest.id,
            "action_id": "web.fetch_page",
            "url": summarize(url.absoluteString)
        ])

        let resource = try await fetchClient.fetchResource(
            from: url,
            allowedDomains: manifest.allowedDomains,
            acceptedContentTypes: ["text/html", "text/plain"]
        )
        let extraction = try await extractor.extractAsync(
            contentType: resource.contentType,
            body: resource.body,
            pageURL: resource.url
        )

        let output = WebFetchPageOutput(
            title: extraction.title,
            url: resource.url.absoluteString,
            domain: resource.url.host?.lowercased() ?? "unknown",
            contentType: resource.contentType,
            fetchedAt: resource.fetchedAt,
            extractedText: extraction.extractedText,
            truncated: extraction.truncated || resource.responseTruncated
        )

        return SkillExecutionResult(
            skillID: manifest.id,
            actionID: "web.fetch_page",
            output: try encode(output),
            summary: "Fetched readable content from \(resource.url.host?.lowercased() ?? resource.url.absoluteString).",
            metadata: [
                "domain": resource.url.host?.lowercased() ?? "unknown",
                "content_type": resource.contentType,
                "truncated": (extraction.truncated || resource.responseTruncated) ? "true" : "false"
            ]
        )
    }

    // MARK: - web.research

    private func research(
        input: AtlasToolInput,
        context: SkillExecutionContext
    ) async throws -> SkillExecutionResult {
        let payload = try input.decode(WebResearchInput.self)
        let query = try domainPolicy.normalizeQuery(payload.query)
        let allowedDomains = effectiveAllowedDomains(requested: payload.allowedDomains)
        let maxSources = min(max(payload.maxSources ?? 3, 1), 8)

        context.logger.info("Executing Web Research synthesis", metadata: [
            "skill_id": manifest.id,
            "action_id": "web.research",
            "query": summarize(query),
            "allowed_domains": allowedDomains?.joined(separator: ",") ?? "all",
            "max_sources": "\(maxSources)"
        ])

        let output = try await orchestrator.research(
            query: query,
            allowedDomains: allowedDomains,
            maxSources: maxSources
        )

        return SkillExecutionResult(
            skillID: manifest.id,
            actionID: "web.research",
            output: try encode(output),
            summary: "Reviewed \(output.sources.count) public source\(output.sources.count == 1 ? "" : "s") for \"\(query)\".",
            metadata: [
                "source_count": "\(output.sources.count)",
                "key_point_count": "\(output.keyPoints.count)",
                "allowed_domains": allowedDomains?.joined(separator: ",") ?? "all"
            ]
        )
    }

    // MARK: - web.news

    private func news(
        input: AtlasToolInput,
        context: SkillExecutionContext
    ) async throws -> SkillExecutionResult {
        struct NewsInput: Decodable {
            let query: String
            let recency: String?
            let maxResults: Int?
        }

        let payload = try input.decode(NewsInput.self)
        let query = try domainPolicy.normalizeQuery(payload.query)
        let recency = payload.recency ?? "day"
        let maxResults = min(max(payload.maxResults ?? 10, 1), 10)

        // Map recency to Brave freshness param: pd = past day, pw = past week, pm = past month
        let freshness: String
        switch recency {
        case "week":  freshness = "pw"
        case "month": freshness = "pm"
        default:      freshness = "pd"
        }

        context.logger.info("Executing Web Research news search", metadata: [
            "skill_id": manifest.id,
            "action_id": "web.news",
            "query": summarize(query),
            "recency": recency,
            "max_results": "\(maxResults)"
        ])

        var results: [WebSearchResult] = []
        let allowedDomains = effectiveAllowedDomains(requested: nil)
        for provider in providers {
            do {
                let r = try await provider.searchNews(
                    query: query,
                    allowedDomains: allowedDomains,
                    maxResults: maxResults,
                    freshness: freshness
                )
                if !r.isEmpty { results = r; break }
            } catch { continue }
        }

        let output = WebSearchOutput(results: results)
        return SkillExecutionResult(
            skillID: manifest.id,
            actionID: "web.news",
            output: try encode(output),
            summary: "Found \(results.count) news result\(results.count == 1 ? "" : "s") for \"\(query)\" (\(recency)).",
            metadata: ["result_count": "\(results.count)", "recency": recency]
        )
    }

    // MARK: - web.check_url

    private func checkURL(
        input: AtlasToolInput,
        context: SkillExecutionContext
    ) async throws -> SkillExecutionResult {
        let payload = try input.decode(WebCheckURLInput.self)
        let url = try domainPolicy.validateURLString(payload.url)

        context.logger.info("Executing Web Research URL check", metadata: [
            "skill_id": manifest.id,
            "action_id": "web.check_url",
            "url": summarize(url.absoluteString)
        ])

        let config = URLSessionConfiguration.ephemeral
        config.timeoutIntervalForRequest = 10
        config.httpAdditionalHeaders = [
            "User-Agent": "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.6 Safari/605.1.15"
        ]
        let session = URLSession(configuration: config)
        var request = URLRequest(url: url)
        request.httpMethod = "HEAD"

        let output: WebCheckURLOutput
        do {
            let (_, response) = try await session.data(for: request)
            if let http = response as? HTTPURLResponse {
                let ct = http.value(forHTTPHeaderField: "Content-Type")?
                    .split(separator: ";").first.map(String.init)?.trimmingCharacters(in: .whitespaces) ?? "unknown"
                // 405 means the server is up but doesn't support HEAD — URL is still reachable.
                let reachable = (200...399).contains(http.statusCode) || http.statusCode == 405
                let message: String
                switch http.statusCode {
                case 200...299:
                    message = "URL is reachable (HTTP \(http.statusCode))."
                case 300...399:
                    message = "URL redirects (HTTP \(http.statusCode))."
                case 405:
                    message = "URL is reachable but the server does not support HEAD requests (HTTP 405)."
                default:
                    message = "URL returned HTTP \(http.statusCode)."
                }
                output = WebCheckURLOutput(
                    url: url.absoluteString,
                    statusCode: http.statusCode,
                    contentType: ct,
                    reachable: reachable,
                    message: message
                )
            } else {
                output = WebCheckURLOutput(url: url.absoluteString, statusCode: 0, contentType: "unknown", reachable: false, message: "No HTTP response received.")
            }
        } catch {
            output = WebCheckURLOutput(
                url: url.absoluteString,
                statusCode: 0,
                contentType: "unknown",
                reachable: false,
                message: "Request failed: \(error.localizedDescription)"
            )
        }

        return SkillExecutionResult(
            skillID: manifest.id,
            actionID: "web.check_url",
            output: try encode(output),
            summary: output.message,
            metadata: ["status_code": "\(output.statusCode)", "reachable": output.reachable ? "true" : "false"]
        )
    }

    // MARK: - web.multi_search

    private func multiSearch(
        input: AtlasToolInput,
        context: SkillExecutionContext
    ) async throws -> SkillExecutionResult {
        let payload = try input.decode(WebMultiSearchInput.self)
        let queries = payload.queries.prefix(5).map { $0 }
        guard !queries.isEmpty else {
            throw AtlasToolError.invalidInput("queries must contain at least one item.")
        }
        let allowedDomains = effectiveAllowedDomains(requested: payload.allowedDomains)
        let maxPerQuery = min(max(payload.maxResultsPerQuery ?? 5, 1), 10)

        context.logger.info("Executing Web Research multi-search", metadata: [
            "skill_id": manifest.id,
            "action_id": "web.multi_search",
            "query_count": "\(queries.count)"
        ])

        let localProviders = providers
        let localDomainPolicy = domainPolicy
        let localAllowedDomains = allowedDomains

        typealias QueryResults = [WebSearchResult]
        var allResults: [QueryResults] = []

        await withTaskGroup(of: QueryResults.self) { group in
            for rawQuery in queries {
                let capturedQuery = rawQuery
                group.addTask {
                    guard let normalized = try? localDomainPolicy.normalizeQuery(capturedQuery) else { return [] }
                    for provider in localProviders {
                        if let r = try? await provider.search(
                            query: normalized,
                            allowedDomains: localAllowedDomains,
                            maxResults: maxPerQuery
                        ), !r.isEmpty {
                            return r
                        }
                    }
                    return []
                }
            }
            for await results in group {
                allResults.append(results)
            }
        }

        let totalBeforeDedup = allResults.reduce(0) { $0 + $1.count }
        var seenURLs = Set<String>()
        var merged: [WebSearchResult] = []
        for batch in allResults {
            for result in batch where seenURLs.insert(result.url).inserted {
                merged.append(result)
            }
        }

        let output = WebMultiSearchOutput(
            results: merged,
            queryCount: queries.count,
            totalBeforeDedup: totalBeforeDedup
        )

        return SkillExecutionResult(
            skillID: manifest.id,
            actionID: "web.multi_search",
            output: try encode(output),
            summary: "Found \(merged.count) unique result\(merged.count == 1 ? "" : "s") across \(queries.count) parallel quer\(queries.count == 1 ? "y" : "ies").",
            metadata: [
                "result_count": "\(merged.count)",
                "query_count": "\(queries.count)",
                "dedup_removed": "\(totalBeforeDedup - merged.count)"
            ]
        )
    }

    // MARK: - web.extract_links

    private func extractLinks(
        input: AtlasToolInput,
        context: SkillExecutionContext
    ) async throws -> SkillExecutionResult {
        let payload = try input.decode(WebExtractLinksInput.self)
        let url = try domainPolicy.validateURLString(payload.url)
        let allowedDomains = effectiveAllowedDomains(requested: payload.allowedDomains)

        context.logger.info("Executing Web Research extract links", metadata: [
            "skill_id": manifest.id,
            "action_id": "web.extract_links",
            "url": summarize(url.absoluteString)
        ])

        let resource = try await fetchClient.fetchResource(
            from: url,
            allowedDomains: manifest.allowedDomains,
            acceptedContentTypes: ["text/html"]
        )

        let allLinks = extractor.extractLinks(from: resource.body, baseURL: resource.url)
        let filtered: [WebExtractedLink]
        if let allowed = allowedDomains {
            filtered = allLinks.filter { link in
                allowed.contains(where: { link.domain == $0 || link.domain.hasSuffix(".\($0)") })
            }
        } else {
            filtered = allLinks
        }

        let output = WebExtractLinksOutput(
            sourceURL: resource.url.absoluteString,
            links: Array(filtered.prefix(100)),
            totalFound: filtered.count
        )

        return SkillExecutionResult(
            skillID: manifest.id,
            actionID: "web.extract_links",
            output: try encode(output),
            summary: "Extracted \(output.totalFound) link\(output.totalFound == 1 ? "" : "s") from \(resource.url.host?.lowercased() ?? url.absoluteString).",
            metadata: ["link_count": "\(output.totalFound)", "domain": resource.url.host?.lowercased() ?? "unknown"]
        )
    }

    // MARK: - web.summarize_url

    private func summarizeURL(
        input: AtlasToolInput,
        context: SkillExecutionContext
    ) async throws -> SkillExecutionResult {
        let payload = try input.decode(WebSummarizeURLInput.self)
        let url = try domainPolicy.validateURLString(payload.url)

        context.logger.info("Executing Web Research URL summary", metadata: [
            "skill_id": manifest.id,
            "action_id": "web.summarize_url",
            "url": summarize(url.absoluteString)
        ])

        let resource = try await fetchClient.fetchResource(
            from: url,
            allowedDomains: manifest.allowedDomains,
            acceptedContentTypes: ["text/html", "text/plain"]
        )
        let extraction = try await extractor.extractAsync(
            contentType: resource.contentType,
            body: resource.body,
            pageURL: resource.url
        )

        // Build key points from the extracted text using scored sentence extraction
        let text = extraction.extractedText
        let sentences = text
            .components(separatedBy: CharacterSet(charactersIn: ".\n"))
            .map { $0.trimmingCharacters(in: .whitespacesAndNewlines) }
            .filter { $0.count >= 40 && $0.count <= 300 }

        var seen = Set<String>()
        var keyPoints: [String] = []
        for sentence in sentences {
            let norm = sentence.lowercased()
            guard seen.insert(norm).inserted else { continue }
            keyPoints.append(sentence)
            if keyPoints.count >= 5 { break }
        }

        let summary = keyPoints.first ?? String(text.prefix(300)).trimmingCharacters(in: .whitespacesAndNewlines)

        let output = WebSummarizeURLOutput(
            title: extraction.title,
            url: resource.url.absoluteString,
            domain: resource.url.host?.lowercased() ?? "unknown",
            summary: summary,
            keyPoints: keyPoints,
            contentType: resource.contentType,
            truncated: extraction.truncated || resource.responseTruncated
        )

        return SkillExecutionResult(
            skillID: manifest.id,
            actionID: "web.summarize_url",
            output: try encode(output),
            summary: "Summarized content from \(resource.url.host?.lowercased() ?? url.absoluteString).",
            metadata: [
                "domain": resource.url.host?.lowercased() ?? "unknown",
                "key_point_count": "\(keyPoints.count)"
            ]
        )
    }

    // MARK: - Helpers

    private func effectiveAllowedDomains(requested: [String]?) -> [String]? {
        let requestedDomains = domainPolicy.normalizeAllowedDomains(requested)

        guard let manifestDomains = domainPolicy.normalizeAllowedDomains(manifest.allowedDomains) else {
            return requestedDomains
        }

        guard let requestedDomains else {
            return manifestDomains
        }

        let intersection = manifestDomains.filter { requestedDomains.contains($0) }
        return intersection.isEmpty ? manifestDomains : intersection
    }

    private func encode<T: Encodable>(_ value: T) throws -> String {
        let data = try AtlasJSON.encoder.encode(value)
        return String(decoding: data, as: UTF8.self)
    }

    private func summarize(_ value: String, limit: Int = 120) -> String {
        let normalized = value.replacingOccurrences(of: #"\s+"#, with: " ", options: .regularExpression)
        guard normalized.count > limit else { return normalized }
        let endIndex = normalized.index(normalized.startIndex, offsetBy: limit)
        return String(normalized[..<endIndex]) + "..."
    }
}
