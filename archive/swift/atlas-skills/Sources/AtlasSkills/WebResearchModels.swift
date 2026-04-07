import Foundation

public struct WebSearchInput: Codable, Hashable, Sendable {
    public let query: String
    public let allowedDomains: [String]?
    public let maxResults: Int?

    public init(
        query: String,
        allowedDomains: [String]? = nil,
        maxResults: Int? = nil
    ) {
        self.query = query
        self.allowedDomains = allowedDomains
        self.maxResults = maxResults
    }
}

public struct WebSearchResult: Codable, Hashable, Sendable {
    public let title: String
    public let url: String
    public let snippet: String
    public let domain: String
    public let publishedAt: String?

    public init(
        title: String,
        url: String,
        snippet: String,
        domain: String,
        publishedAt: String? = nil
    ) {
        self.title = title
        self.url = url
        self.snippet = snippet
        self.domain = domain
        self.publishedAt = publishedAt
    }
}

public struct WebSearchOutput: Codable, Hashable, Sendable {
    public let results: [WebSearchResult]

    public init(results: [WebSearchResult]) {
        self.results = results
    }
}

public struct WebFetchPageInput: Codable, Hashable, Sendable {
    public let url: String

    public init(url: String) {
        self.url = url
    }
}

public struct WebFetchPageOutput: Codable, Hashable, Sendable {
    public let title: String
    public let url: String
    public let domain: String
    public let contentType: String
    public let fetchedAt: Date
    public let extractedText: String
    public let truncated: Bool

    public init(
        title: String,
        url: String,
        domain: String,
        contentType: String,
        fetchedAt: Date,
        extractedText: String,
        truncated: Bool
    ) {
        self.title = title
        self.url = url
        self.domain = domain
        self.contentType = contentType
        self.fetchedAt = fetchedAt
        self.extractedText = extractedText
        self.truncated = truncated
    }
}

public struct WebCheckURLInput: Codable, Hashable, Sendable {
    public let url: String

    public init(url: String) {
        self.url = url
    }
}

public struct WebCheckURLOutput: Codable, Hashable, Sendable {
    public let url: String
    public let statusCode: Int
    public let contentType: String
    public let reachable: Bool
    public let message: String

    public init(url: String, statusCode: Int, contentType: String, reachable: Bool, message: String) {
        self.url = url
        self.statusCode = statusCode
        self.contentType = contentType
        self.reachable = reachable
        self.message = message
    }
}

public struct WebMultiSearchInput: Codable, Hashable, Sendable {
    public let queries: [String]
    public let allowedDomains: [String]?
    public let maxResultsPerQuery: Int?

    public init(queries: [String], allowedDomains: [String]? = nil, maxResultsPerQuery: Int? = nil) {
        self.queries = queries
        self.allowedDomains = allowedDomains
        self.maxResultsPerQuery = maxResultsPerQuery
    }
}

public struct WebMultiSearchOutput: Codable, Hashable, Sendable {
    public let results: [WebSearchResult]
    public let queryCount: Int
    public let totalBeforeDedup: Int

    public init(results: [WebSearchResult], queryCount: Int, totalBeforeDedup: Int) {
        self.results = results
        self.queryCount = queryCount
        self.totalBeforeDedup = totalBeforeDedup
    }
}

public struct WebExtractLinksInput: Codable, Hashable, Sendable {
    public let url: String
    public let allowedDomains: [String]?

    public init(url: String, allowedDomains: [String]? = nil) {
        self.url = url
        self.allowedDomains = allowedDomains
    }
}

public struct WebExtractedLink: Codable, Hashable, Sendable {
    public let url: String
    public let text: String
    public let domain: String

    public init(url: String, text: String, domain: String) {
        self.url = url
        self.text = text
        self.domain = domain
    }
}

public struct WebExtractLinksOutput: Codable, Hashable, Sendable {
    public let sourceURL: String
    public let links: [WebExtractedLink]
    public let totalFound: Int

    public init(sourceURL: String, links: [WebExtractedLink], totalFound: Int) {
        self.sourceURL = sourceURL
        self.links = links
        self.totalFound = totalFound
    }
}

public struct WebSummarizeURLInput: Codable, Hashable, Sendable {
    public let url: String

    public init(url: String) {
        self.url = url
    }
}

public struct WebSummarizeURLOutput: Codable, Hashable, Sendable {
    public let title: String
    public let url: String
    public let domain: String
    public let summary: String
    public let keyPoints: [String]
    public let contentType: String
    public let truncated: Bool

    public init(
        title: String,
        url: String,
        domain: String,
        summary: String,
        keyPoints: [String],
        contentType: String,
        truncated: Bool
    ) {
        self.title = title
        self.url = url
        self.domain = domain
        self.summary = summary
        self.keyPoints = keyPoints
        self.contentType = contentType
        self.truncated = truncated
    }
}

public struct WebResearchInput: Codable, Hashable, Sendable {
    public let query: String
    public let allowedDomains: [String]?
    public let maxSources: Int?

    public init(
        query: String,
        allowedDomains: [String]? = nil,
        maxSources: Int? = nil
    ) {
        self.query = query
        self.allowedDomains = allowedDomains
        self.maxSources = maxSources
    }
}

public struct WebResearchSource: Codable, Hashable, Sendable {
    public let title: String
    public let url: String
    public let domain: String
    public let snippet: String
    public let excerpt: String

    public init(title: String, url: String, domain: String, snippet: String, excerpt: String) {
        self.title = title
        self.url = url
        self.domain = domain
        self.snippet = snippet
        self.excerpt = excerpt
    }
}

public struct WebResearchFailedSource: Codable, Hashable, Sendable {
    public let url: String
    public let domain: String
    public let error: String

    public init(url: String, domain: String, error: String) {
        self.url = url
        self.domain = domain
        self.error = error
    }
}

public struct WebResearchOutput: Codable, Hashable, Sendable {
    public let summary: String
    public let keyPoints: [String]
    public let sources: [WebResearchSource]
    public let caveats: [String]
    public let confidenceSummary: String
    public let failedSources: [WebResearchFailedSource]

    public init(
        summary: String,
        keyPoints: [String],
        sources: [WebResearchSource],
        caveats: [String],
        confidenceSummary: String,
        failedSources: [WebResearchFailedSource] = []
    ) {
        self.summary = summary
        self.keyPoints = keyPoints
        self.sources = sources
        self.caveats = caveats
        self.confidenceSummary = confidenceSummary
        self.failedSources = failedSources
    }
}

public struct WebFetchedResource: Hashable, Sendable {
    public let url: URL
    public let contentType: String
    public let fetchedAt: Date
    public let body: String
    public let responseTruncated: Bool

    public init(
        url: URL,
        contentType: String,
        fetchedAt: Date,
        body: String,
        responseTruncated: Bool
    ) {
        self.url = url
        self.contentType = contentType
        self.fetchedAt = fetchedAt
        self.body = body
        self.responseTruncated = responseTruncated
    }
}

public struct WebContentExtraction: Hashable, Sendable {
    public let title: String
    public let extractedText: String
    public let truncated: Bool

    public init(title: String, extractedText: String, truncated: Bool) {
        self.title = title
        self.extractedText = extractedText
        self.truncated = truncated
    }
}

public struct WebSearchProviderValidation: Hashable, Sendable {
    public let isAvailable: Bool
    public let summary: String
    public let issues: [String]

    public init(isAvailable: Bool, summary: String, issues: [String] = []) {
        self.isAvailable = isAvailable
        self.summary = summary
        self.issues = issues
    }
}

public enum WebResearchError: LocalizedError, Hashable, Sendable {
    case invalidQuery
    case invalidURL(String)
    case unsupportedScheme(String)
    case blockedHost(String)
    case blockedIPAddress(String)
    case disallowedDomain(String)
    case requestFailed(String)
    case unsupportedContentType(String)
    case responseTooLarge(Int)
    case emptyContent
    case emptyExtraction
    case noSearchResults
    case noUsableSources
    case searchFailed(String)

    public var errorDescription: String? {
        switch self {
        case .invalidQuery:
            return "Please provide a non-empty public web query."
        case .invalidURL(let value):
            return "The URL '\(value)' is invalid."
        case .unsupportedScheme(let scheme):
            return "The scheme '\(scheme)' is not allowed. Only http and https are supported."
        case .blockedHost(let host):
            return "The host '\(host)' is blocked by Atlas web safety policy."
        case .blockedIPAddress(let address):
            return "The address '\(address)' is blocked because it points to a local or private target."
        case .disallowedDomain(let domain):
            return "The domain '\(domain)' is outside the allowed domain policy."
        case .requestFailed(let details):
            return details
        case .unsupportedContentType(let contentType):
            return "Atlas only supports text/html and text/plain for web research right now, but received '\(contentType)'."
        case .responseTooLarge(let limit):
            return "The remote response exceeded the safe size limit of \(limit) bytes."
        case .emptyContent:
            return "The fetched page did not return any readable content."
        case .emptyExtraction:
            return "Atlas could not extract readable text from that page."
        case .noSearchResults:
            return "Atlas did not find any public search results for that query."
        case .noUsableSources:
            return "Atlas found sources, but none of them yielded usable readable content."
        case .searchFailed(let details):
            return details
        }
    }
}

public protocol WebSearchProviding: Sendable {
    var providerName: String { get }
    func validateProvider() -> WebSearchProviderValidation
    func search(query: String, allowedDomains: [String]?, maxResults: Int) async throws -> [WebSearchResult]
    func searchNews(query: String, allowedDomains: [String]?, maxResults: Int, freshness: String?) async throws -> [WebSearchResult]
}

extension WebSearchProviding {
    /// Default implementation — appends a time hint to the query and falls back to regular search.
    public func searchNews(
        query: String,
        allowedDomains: [String]?,
        maxResults: Int,
        freshness: String?
    ) async throws -> [WebSearchResult] {
        let timeHint: String
        switch freshness {
        case "pw": timeHint = "this week"
        case "pm": timeHint = "this month"
        default: timeHint = "today"
        }
        return try await search(
            query: "\(query) \(timeHint)",
            allowedDomains: allowedDomains,
            maxResults: maxResults
        )
    }
}

public protocol WebFetching: Sendable {
    func validateClient() -> [String]
    func fetchResource(
        from url: URL,
        allowedDomains: [String]?,
        acceptedContentTypes: Set<String>
    ) async throws -> WebFetchedResource
}

public protocol WebResearchOrchestrating: Sendable {
    func research(
        query: String,
        allowedDomains: [String]?,
        maxSources: Int
    ) async throws -> WebResearchOutput
}
