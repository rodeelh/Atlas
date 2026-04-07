import Foundation

public actor DuckDuckGoHTMLSearchProvider: WebSearchProviding {
    public nonisolated let providerName = "DuckDuckGo HTML"

    private let fetchClient: any WebFetching
    private let domainPolicy: WebDomainPolicy
    private let searchEndpoint = URL(string: "https://html.duckduckgo.com/html/")!
    private let providerDomains = ["duckduckgo.com"]

    public init(
        fetchClient: any WebFetching,
        domainPolicy: WebDomainPolicy = WebDomainPolicy()
    ) {
        self.fetchClient = fetchClient
        self.domainPolicy = domainPolicy
    }

    public nonisolated func validateProvider() -> WebSearchProviderValidation {
        WebSearchProviderValidation(
            isAvailable: true,
            summary: "DuckDuckGo HTML search is configured for public unauthenticated lookup."
        )
    }

    public func search(
        query: String,
        allowedDomains: [String]? = nil,
        maxResults: Int
    ) async throws -> [WebSearchResult] {
        let normalizedQuery = try domainPolicy.normalizeQuery(query)
        let normalizedAllowedDomains = domainPolicy.normalizeAllowedDomains(allowedDomains)
        let clampedResults = min(max(maxResults, 1), 10)

        var components = URLComponents(url: searchEndpoint, resolvingAgainstBaseURL: false)
        components?.queryItems = [
            URLQueryItem(name: "q", value: normalizedQuery)
        ]

        guard let url = components?.url else {
            throw WebResearchError.searchFailed("Atlas could not build the search request.")
        }

        let resource = try await fetchClient.fetchResource(
            from: url,
            allowedDomains: providerDomains,
            acceptedContentTypes: ["text/html", "text/plain"]
        )

        let parsedResults = parseResults(from: resource.body)
        let filteredResults = parsedResults.filter { result in
            guard let resultURL = URL(string: result.url) else {
                return false
            }

            do {
                try domainPolicy.validateURL(
                    resultURL,
                    allowedDomains: normalizedAllowedDomains,
                    resolveDNS: false
                )
                return true
            } catch {
                return false
            }
        }

        return Array(filteredResults.prefix(clampedResults))
    }

    private func parseResults(from html: String) -> [WebSearchResult] {
        guard let anchorRegex = try? NSRegularExpression(
            pattern: #"<a[^>]*class=["'][^"']*(?:result__a|result-link)[^"']*["'][^>]*href=["']([^"']+)["'][^>]*>(.*?)</a>"#,
            options: [.caseInsensitive, .dotMatchesLineSeparators]
        ) else {
            return []
        }

        let textRange = NSRange(html.startIndex..<html.endIndex, in: html)
        let matches = anchorRegex.matches(in: html, range: textRange)
        var seenURLs = Set<String>()
        var results: [WebSearchResult] = []

        for match in matches {
            guard
                let hrefRange = Range(match.range(at: 1), in: html),
                let titleRange = Range(match.range(at: 2), in: html)
            else {
                continue
            }

            let rawHref = String(html[hrefRange])
            guard let normalizedURL = normalizeResultURL(rawHref) else {
                continue
            }

            guard let url = URL(string: normalizedURL), let domain = url.host?.lowercased() else {
                continue
            }

            guard seenURLs.insert(normalizedURL).inserted else {
                continue
            }

            let rawTitle = String(html[titleRange])
            let title = cleanHTMLFragment(rawTitle)

            let snippetWindow = snippetWindowText(in: html, following: match.range)
            let snippet = extractSnippet(from: snippetWindow)

            results.append(
                WebSearchResult(
                    title: title.isEmpty ? normalizedURL : title,
                    url: normalizedURL,
                    snippet: snippet,
                    domain: domain
                )
            )
        }

        return results
    }

    private func normalizeResultURL(_ rawHref: String) -> String? {
        let href = rawHref.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !href.isEmpty else {
            return nil
        }

        let resolvedHref: String
        if href.hasPrefix("//") {
            resolvedHref = "https:\(href)"
        } else if href.hasPrefix("/") {
            resolvedHref = "https://duckduckgo.com\(href)"
        } else {
            resolvedHref = href
        }

        guard let url = URL(string: resolvedHref) else {
            return nil
        }

        if let host = url.host?.lowercased(),
           host.contains("duckduckgo.com"),
           let components = URLComponents(url: url, resolvingAgainstBaseURL: false),
           let queryItems = components.queryItems,
           let redirectTarget = queryItems.first(where: { $0.name == "uddg" || $0.name == "u" })?.value {
            let decodedTarget = redirectTarget.removingPercentEncoding ?? redirectTarget
            if URL(string: decodedTarget) != nil {
            return decodedTarget
            }
        }

        return url.absoluteString
    }

    private func snippetWindowText(in html: String, following range: NSRange) -> String {
        guard let upperBound = Range(range, in: html)?.upperBound else {
            return ""
        }

        let endIndex = html.index(
            upperBound,
            offsetBy: min(1_500, html.distance(from: upperBound, to: html.endIndex)),
            limitedBy: html.endIndex
        ) ?? html.endIndex

        return String(html[upperBound..<endIndex])
    }

    private func extractSnippet(from window: String) -> String {
        let patterns = [
            #"<[^>]*class=["'][^"']*(?:result__snippet|result-snippet)[^"']*["'][^>]*>(.*?)</[^>]+>"#,
            #"<td[^>]*class=["'][^"']*result-snippet[^"']*["'][^>]*>(.*?)</td>"#
        ]

        for pattern in patterns {
            if let value = firstCapture(in: window, pattern: pattern) {
                let cleaned = cleanHTMLFragment(value)
                if !cleaned.isEmpty {
                    return cleaned
                }
            }
        }

        return ""
    }

    private func cleanHTMLFragment(_ value: String) -> String {
        let stripped = value.replacingOccurrences(
            of: #"(?is)<[^>]+>"#,
            with: " ",
            options: .regularExpression
        )

        return stripped
            .replacingOccurrences(of: "&amp;", with: "&")
            .replacingOccurrences(of: "&lt;", with: "<")
            .replacingOccurrences(of: "&gt;", with: ">")
            .replacingOccurrences(of: "&#39;", with: "'")
            .replacingOccurrences(of: "&quot;", with: "\"")
            .replacingOccurrences(of: #"\s+"#, with: " ", options: .regularExpression)
            .trimmingCharacters(in: .whitespacesAndNewlines)
    }

    private func firstCapture(in text: String, pattern: String) -> String? {
        guard let regex = try? NSRegularExpression(
            pattern: pattern,
            options: [.caseInsensitive, .dotMatchesLineSeparators]
        ) else {
            return nil
        }

        let nsRange = NSRange(text.startIndex..<text.endIndex, in: text)
        guard
            let match = regex.firstMatch(in: text, range: nsRange),
            match.numberOfRanges > 1,
            let range = Range(match.range(at: 1), in: text)
        else {
            return nil
        }

        return String(text[range])
    }
}
