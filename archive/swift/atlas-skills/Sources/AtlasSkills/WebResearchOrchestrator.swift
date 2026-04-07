import Foundation

public actor WebResearchOrchestrator: WebResearchOrchestrating {
    /// Ordered provider chain — first to return results wins. Brave (if configured) comes first,
    /// DuckDuckGo HTML is the fallback.
    private let providers: [any WebSearchProviding]
    private let fetchClient: any WebFetching
    private let extractor: WebContentExtractor
    private let domainPolicy: WebDomainPolicy
    private let maxFetchAttempts: Int
    private let cache = WebFetchCache()

    public init(
        providers: [any WebSearchProviding],
        fetchClient: any WebFetching,
        extractor: WebContentExtractor = WebContentExtractor(),
        domainPolicy: WebDomainPolicy = WebDomainPolicy(),
        maxFetchAttempts: Int = 6
    ) {
        self.providers = providers
        self.fetchClient = fetchClient
        self.extractor = extractor
        self.domainPolicy = domainPolicy
        self.maxFetchAttempts = max(1, maxFetchAttempts)
    }

    public func research(
        query: String,
        allowedDomains: [String]? = nil,
        maxSources: Int
    ) async throws -> WebResearchOutput {
        let normalizedQuery = try domainPolicy.normalizeQuery(query)
        let normalizedAllowedDomains = domainPolicy.normalizeAllowedDomains(allowedDomains)
        let requestedSourceCount = min(max(maxSources, 1), 8)
        let searchBuffer = min(requestedSourceCount * 2, 16)

        // Provider fallback chain — first provider to return non-empty results wins.
        var searchResults: [WebSearchResult] = []
        for provider in providers {
            do {
                let results = try await provider.search(
                    query: normalizedQuery,
                    allowedDomains: normalizedAllowedDomains,
                    maxResults: searchBuffer
                )
                if !results.isEmpty {
                    searchResults = results
                    break
                }
            } catch {
                // Try next provider
                continue
            }
        }

        guard !searchResults.isEmpty else {
            throw WebResearchError.noSearchResults
        }

        // Concurrent source fetching with per-source failure tracking
        var fetchedSources: [WebResearchSource] = []
        var failedSources: [WebResearchFailedSource] = []
        let candidateResults = Array(searchResults.prefix(min(maxFetchAttempts, searchBuffer)))
        let localExtractor = extractor
        let localFetchClient = fetchClient
        let localDomainPolicy = domainPolicy
        let localAllowedDomains = normalizedAllowedDomains
        let localCache = cache

        typealias FetchOutcome = (source: WebResearchSource?, failed: WebResearchFailedSource?)

        await withTaskGroup(of: FetchOutcome.self) { group in
            for result in candidateResults {
                let capturedResult = result
                group.addTask {
                    do {
                        // Check cache first
                        if let url = URL(string: capturedResult.url),
                           let cached = await localCache.get(url) {
                            let extraction = try localExtractor.extract(
                                contentType: cached.contentType,
                                body: cached.body,
                                pageURL: cached.url
                            )
                            let source = WebResearchSource(
                                title: extraction.title.isEmpty ? capturedResult.title : extraction.title,
                                url: cached.url.absoluteString,
                                domain: cached.url.host?.lowercased() ?? capturedResult.domain,
                                snippet: capturedResult.snippet,
                                excerpt: Self.excerpt(from: extraction.extractedText)
                            )
                            return (source: source, failed: nil)
                        }

                        let source = try await WebResearchOrchestrator.fetchSourceStatic(
                            from: capturedResult,
                            allowedDomains: localAllowedDomains,
                            fetchClient: localFetchClient,
                            extractor: localExtractor,
                            domainPolicy: localDomainPolicy,
                            cache: localCache
                        )
                        return (source: source, failed: nil)
                    } catch {
                        let domain = URL(string: capturedResult.url)?.host?.lowercased() ?? capturedResult.domain
                        return (
                            source: nil,
                            failed: WebResearchFailedSource(
                                url: capturedResult.url,
                                domain: domain,
                                error: error.localizedDescription
                            )
                        )
                    }
                }
            }
            for await outcome in group {
                if let source = outcome.source, fetchedSources.count < requestedSourceCount {
                    fetchedSources.append(source)
                } else if let failed = outcome.failed {
                    failedSources.append(failed)
                }
            }
        }

        guard !fetchedSources.isEmpty else {
            throw WebResearchError.noUsableSources
        }

        let keyPoints = buildKeyPoints(for: normalizedQuery, from: fetchedSources)
        let summary = keyPoints.first ?? fetchedSources.first?.snippet ?? ""
        var caveats: [String] = []
        caveats.append(contentsOf: sourceDiversityCaveats(for: fetchedSources, requestedSourceCount: requestedSourceCount))

        return WebResearchOutput(
            summary: summary,
            keyPoints: keyPoints,
            sources: fetchedSources,
            caveats: Array(Set(caveats)).sorted(),
            confidenceSummary: confidenceSummary(for: fetchedSources, caveats: caveats),
            failedSources: failedSources
        )
    }

    private static func fetchSourceStatic(
        from result: WebSearchResult,
        allowedDomains: [String]?,
        fetchClient: any WebFetching,
        extractor: WebContentExtractor,
        domainPolicy: WebDomainPolicy,
        cache: WebFetchCache
    ) async throws -> WebResearchSource {
        guard let url = URL(string: result.url) else {
            throw WebResearchError.invalidURL(result.url)
        }

        let resource = try await fetchClient.fetchResource(
            from: url,
            allowedDomains: allowedDomains,
            acceptedContentTypes: ["text/html", "text/plain"]
        )
        await cache.set(url, resource: resource)

        let extraction = try extractor.extract(
            contentType: resource.contentType,
            body: resource.body,
            pageURL: resource.url
        )

        guard !extraction.extractedText.isEmpty else {
            throw WebResearchError.emptyExtraction
        }

        return WebResearchSource(
            title: extraction.title.isEmpty ? result.title : extraction.title,
            url: resource.url.absoluteString,
            domain: resource.url.host?.lowercased() ?? result.domain,
            snippet: result.snippet,
            excerpt: excerpt(from: extraction.extractedText)
        )
    }

    static func excerpt(from text: String) -> String {
        let limit = min(text.count, 2000)
        let endIndex = text.index(text.startIndex, offsetBy: limit)
        let prefix = String(text[..<endIndex])

        if let boundary = prefix.lastIndex(where: { $0 == "." || $0 == "\n" }) {
            let candidate = prefix[..<boundary].trimmingCharacters(in: .whitespacesAndNewlines)
            if candidate.count > 120 {
                return String(candidate)
            }
        }

        return prefix.trimmingCharacters(in: .whitespacesAndNewlines)
    }

    // MARK: - Scored key point extraction

    private func buildKeyPoints(for query: String, from sources: [WebResearchSource]) -> [String] {
        let queryTerms = Set(
            query
                .lowercased()
                .split(whereSeparator: { $0.isWhitespace || $0.isPunctuation })
                .map(String.init)
                .filter { $0.count >= 3 }
        )

        struct ScoredCandidate {
            let text: String
            let score: Int
        }

        var seen = Set<String>()
        var scored: [ScoredCandidate] = []

        for source in sources {
            let sentences = source.excerpt
                .components(separatedBy: CharacterSet(charactersIn: ".\n"))
                .map { $0.trimmingCharacters(in: .whitespacesAndNewlines) }
                .filter { $0.count >= 40 }

            for sentence in sentences {
                let normalized = sentence.lowercased()
                guard seen.insert(normalized).inserted else { continue }

                // Score: query term hits, length penalty for very short/very long
                var score = 0
                for term in queryTerms where normalized.contains(term) {
                    score += 2
                }
                // Prefer medium-length sentences (50–200 chars)
                let len = sentence.count
                if len >= 50 && len <= 200 { score += 1 }
                // Prefer sentences that start with a capital letter (real sentences)
                if let first = sentence.first, first.isUppercase { score += 1 }
                // Penalize sentences with too many digits (likely boilerplate dates/numbers)
                let digitCount = sentence.filter(\.isNumber).count
                if digitCount > len / 4 { score -= 1 }

                if score >= 0 {
                    scored.append(ScoredCandidate(text: sentence, score: score))
                }
            }
        }

        let top = scored
            .sorted { $0.score > $1.score }
            .prefix(5)
            .map(\.text)

        if top.isEmpty {
            return sources.prefix(3).map(\.snippet).filter { !$0.isEmpty }
        }

        return Array(top)
    }

    private func sourceDiversityCaveats(for sources: [WebResearchSource], requestedSourceCount: Int) -> [String] {
        let distinctDomains = Set(sources.map(\.domain))
        var caveats: [String] = []

        if sources.count < requestedSourceCount {
            caveats.append("Atlas could only use \(sources.count) readable source\(sources.count == 1 ? "" : "s").")
        }

        if distinctDomains.count == 1 && sources.count > 1 {
            caveats.append("All usable sources came from the same domain.")
        }

        return caveats
    }

    private func confidenceSummary(for sources: [WebResearchSource], caveats: [String]) -> String {
        let distinctDomains = Set(sources.map(\.domain)).count

        if sources.count >= 3 && distinctDomains >= 2 && caveats.count <= 1 {
            return "Moderate confidence based on multiple readable public sources across more than one domain."
        }

        if sources.count >= 2 {
            return "Moderate confidence, but the source set is still limited."
        }

        return "Limited confidence because Atlas could only verify a small number of usable sources."
    }
}
