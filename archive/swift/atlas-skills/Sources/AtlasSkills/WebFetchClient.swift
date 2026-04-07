import Foundation

private final class RedirectBlockingDelegate: NSObject, URLSessionTaskDelegate {
    func urlSession(
        _ session: URLSession,
        task: URLSessionTask,
        willPerformHTTPRedirection response: HTTPURLResponse,
        newRequest request: URLRequest,
        completionHandler: @escaping (URLRequest?) -> Void
    ) {
        completionHandler(nil)
    }
}

public actor WebFetchClient: WebFetching {
    private let policy: WebDomainPolicy
    private let session: URLSession
    private let maxResponseBytes: Int
    private let maxRedirects: Int

    public init(
        policy: WebDomainPolicy = WebDomainPolicy(),
        timeoutSeconds: TimeInterval = 15,
        maxResponseBytes: Int = 1_000_000,
        maxRedirects: Int = 3
    ) {
        self.policy = policy
        self.maxResponseBytes = max(50_000, maxResponseBytes)
        self.maxRedirects = max(0, maxRedirects)

        let configuration = URLSessionConfiguration.ephemeral
        configuration.timeoutIntervalForRequest = timeoutSeconds
        configuration.timeoutIntervalForResource = timeoutSeconds
        configuration.waitsForConnectivity = false
        configuration.requestCachePolicy = .reloadIgnoringLocalAndRemoteCacheData
        configuration.httpCookieStorage = nil
        configuration.httpShouldSetCookies = false
        configuration.urlCache = nil
        configuration.httpAdditionalHeaders = [
            "User-Agent": "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.6 Safari/605.1.15",
            "Accept": "text/html, text/plain;q=0.9, */*;q=0.1"
        ]

        self.session = URLSession(configuration: configuration)
    }

    public nonisolated func validateClient() -> [String] {
        []
    }

    public func fetchResource(
        from url: URL,
        allowedDomains: [String]? = nil,
        acceptedContentTypes: Set<String> = ["text/html", "text/plain"]
    ) async throws -> WebFetchedResource {
        let normalizedAllowedDomains = policy.normalizeAllowedDomains(allowedDomains)
        var currentURL = try policy.validateURL(url, allowedDomains: normalizedAllowedDomains)
        let delegate = RedirectBlockingDelegate()

        for _ in 0...maxRedirects {
            var request = URLRequest(url: currentURL)
            request.httpMethod = "GET"
            request.cachePolicy = .reloadIgnoringLocalAndRemoteCacheData

            let (bytes, response) = try await session.bytes(for: request, delegate: delegate)
            guard let httpResponse = response as? HTTPURLResponse else {
                throw WebResearchError.requestFailed("Atlas did not receive a valid HTTP response.")
            }

            if let contentLength = httpResponse.value(forHTTPHeaderField: "Content-Length"),
               let byteCount = Int(contentLength),
               byteCount > maxResponseBytes {
                throw WebResearchError.responseTooLarge(maxResponseBytes)
            }

            if (300...399).contains(httpResponse.statusCode) {
                guard let location = httpResponse.value(forHTTPHeaderField: "Location"),
                      let redirectedURL = URL(string: location, relativeTo: currentURL)?.absoluteURL else {
                    throw WebResearchError.requestFailed("Atlas could not follow a redirect from \(currentURL.absoluteString).")
                }

                currentURL = try policy.validateURL(
                    redirectedURL,
                    allowedDomains: normalizedAllowedDomains
                )
                continue
            }

            guard (200...299).contains(httpResponse.statusCode) else {
                throw WebResearchError.requestFailed(
                    "The page request returned status code \(httpResponse.statusCode)."
                )
            }

            let contentType = normalizedContentType(
                httpResponse.value(forHTTPHeaderField: "Content-Type")
            )

            guard acceptedContentTypes.contains(contentType) else {
                throw WebResearchError.unsupportedContentType(contentType)
            }

            var data = Data()
            for try await byte in bytes {
                data.append(byte)
                if data.count > maxResponseBytes {
                    throw WebResearchError.responseTooLarge(maxResponseBytes)
                }
            }

            let body = decode(data: data, contentTypeHeader: httpResponse.value(forHTTPHeaderField: "Content-Type"))

            return WebFetchedResource(
                url: httpResponse.url ?? currentURL,
                contentType: contentType,
                fetchedAt: .now,
                body: body,
                responseTruncated: false
            )
        }

        throw WebResearchError.requestFailed("Atlas stopped after too many redirects.")
    }

    private func normalizedContentType(_ rawValue: String?) -> String {
        rawValue?
            .split(separator: ";", maxSplits: 1)
            .first?
            .trimmingCharacters(in: .whitespacesAndNewlines)
            .lowercased() ?? "application/octet-stream"
    }

    private func decode(data: Data, contentTypeHeader: String?) -> String {
        let header = contentTypeHeader?.lowercased() ?? ""

        if header.contains("charset=iso-8859-1"),
           let decoded = String(data: data, encoding: .isoLatin1) {
            return decoded
        }

        if let decoded = String(data: data, encoding: .utf8) {
            return decoded
        }

        if let decoded = String(data: data, encoding: .isoLatin1) {
            return decoded
        }

        return String(decoding: data, as: UTF8.self)
    }
}
