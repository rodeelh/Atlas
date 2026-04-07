import Foundation
import NIOHTTP1
import AtlasShared
import AtlasNetwork

// MARK: - ControlDomainHandler

/// Handles runtime control and configuration routes.
///
/// Routes owned:
///   GET    /status
///   GET    /logs
///   GET    /config
///   PUT    /config
///   GET    /onboarding
///   PUT    /onboarding
///   GET    /models
///   GET    /models/available
///   POST   /models/refresh
///   GET    /api-keys
///   POST   /api-keys
///   POST   /api-keys/invalidate-cache
///   DELETE /api-keys
///   GET    /link-preview
struct ControlDomainHandler: RuntimeDomainHandler {
    let runtime: AgentRuntime

    func handle(
        method: HTTPMethod,
        path: String,
        queryItems: [String: String],
        body: String,
        headers: HTTPHeaders
    ) async throws -> EncodedResponse? {
        switch (method, path) {
        case (.GET, "/status"):
            let status = await runtime.status()
            return try EncodedResponse(status: .ok, payload: AtlasJSON.encoder.encode(status))

        case (.GET, "/logs"):
            let limit = queryItems["limit"].flatMap { Int($0) } ?? 200
            let logs = await runtime.logs(limit: limit)
            return try EncodedResponse(status: .ok, payload: AtlasJSON.encoder.encode(logs))

        case (.GET, "/config"):
            let configSnapshot = await runtime.actionConfig()
            return try EncodedResponse(status: .ok, payload: AtlasJSON.encoder.encode(configSnapshot))

        case (.PUT, "/config"):
            let requestData = Data(body.utf8)
            let newSnapshot = try AtlasJSON.decoder.decode(RuntimeConfigSnapshot.self, from: requestData)
            let result = try await runtime.updateConfig(newSnapshot)
            struct ConfigUpdateResponse: Encodable {
                let config: RuntimeConfigSnapshot
                let restartRequired: Bool
            }
            let response = ConfigUpdateResponse(config: result.snapshot, restartRequired: result.restartRequired)
            return try EncodedResponse(status: .ok, payload: AtlasJSON.encoder.encode(response))

        case (.GET, "/onboarding"):
            let onboardingStatus = await runtime.onboardingStatus()
            return try EncodedResponse(status: .ok, payload: AtlasJSON.encoder.encode(onboardingStatus))

        case (.PUT, "/onboarding"):
            struct OnboardingUpdateRequest: Decodable { let completed: Bool }
            let request = try AtlasJSON.decoder.decode(OnboardingUpdateRequest.self, from: Data(body.utf8))
            let onboardingStatus = try await runtime.setOnboardingCompleted(request.completed)
            return try EncodedResponse(status: .ok, payload: AtlasJSON.encoder.encode(onboardingStatus))

        case (.GET, "/models"):
            let info = await runtime.modelSelectorInfo()
            return try EncodedResponse(status: .ok, payload: AtlasJSON.encoder.encode(info))

        case (.GET, "/models/available"):
            guard let providerStr = queryItems["provider"], let provider = AIProvider(rawValue: providerStr) else {
                throw RuntimeAPIError.invalidRequest("Missing or invalid 'provider' query parameter.")
            }
            let info = await runtime.availableModels(for: provider)
            return try EncodedResponse(status: .ok, payload: AtlasJSON.encoder.encode(info))

        case (.POST, "/models/refresh"):
            let info = await runtime.refreshModels()
            return try EncodedResponse(status: .ok, payload: AtlasJSON.encoder.encode(info))

        case (.GET, "/api-keys"):
            let keyStatus = await runtime.apiKeyStatus()
            return try EncodedResponse(status: .ok, payload: AtlasJSON.encoder.encode(keyStatus))

        case (.POST, "/api-keys"):
            struct SetKeyRequest: Decodable {
                let provider: String
                let key: String
                let name: String?
            }
            let req = try AtlasJSON.decoder.decode(SetKeyRequest.self, from: Data(body.utf8))
            try await runtime.setAPIKey(provider: req.provider, key: req.key, customName: req.name)
            let updatedStatus = await runtime.apiKeyStatus()
            return try EncodedResponse(status: .ok, payload: AtlasJSON.encoder.encode(updatedStatus))

        case (.POST, "/api-keys/invalidate-cache"):
            await runtime.invalidateSecretCache()
            return try EncodedResponse(status: .ok, payload: AtlasJSON.encoder.encode(["invalidated": true]))

        case (.DELETE, "/api-keys"):
            struct DeleteKeyRequest: Decodable { let name: String }
            let req = try AtlasJSON.decoder.decode(DeleteKeyRequest.self, from: Data(body.utf8))
            try await runtime.deleteAPIKey(name: req.name)
            let updatedStatus = await runtime.apiKeyStatus()
            return try EncodedResponse(status: .ok, payload: AtlasJSON.encoder.encode(updatedStatus))

        case (.GET, "/link-preview"):
            guard let rawURL = queryItems["url"],
                  !rawURL.isEmpty,
                  let targetURL = URL(string: rawURL),
                  targetURL.scheme == "http" || targetURL.scheme == "https" else {
                throw RuntimeAPIError.invalidRequest("A valid http or https URL is required.")
            }
            let preview = await Self.fetchLinkPreview(url: targetURL)
            return try EncodedResponse(status: .ok, payload: AtlasJSON.encoder.encode(preview))

        default:
            return nil
        }
    }

    // MARK: - Link Preview

    private struct LinkPreviewResult: Encodable {
        let url: String
        let title: String?
        let description: String?
        let imageURL: String?
        let domain: String?
    }

    /// Fetches a URL and returns a compact preview object.
    ///
    /// Strategy:
    /// 1. If the URL belongs to a known oEmbed provider (YouTube, Vimeo, SoundCloud…),
    ///    hit their structured JSON API — fast, reliable, no HTML parsing needed.
    /// 2. Otherwise fetch the page, read the first 64 KB, extract og:/meta tags.
    ///
    /// Both paths time out after 5 s and fail gracefully.
    private static func fetchLinkPreview(url: URL) async -> LinkPreviewResult {
        let domain = url.host.map { $0.hasPrefix("www.") ? String($0.dropFirst(4)) : $0 }

        if let result = await fetchOEmbed(url: url, domain: domain) {
            return result
        }

        var request = URLRequest(url: url, cachePolicy: .reloadIgnoringLocalCacheData, timeoutInterval: 5)
        request.setValue("Mozilla/5.0 (compatible; Atlas/1.0)", forHTTPHeaderField: "User-Agent")
        request.setValue("text/html,application/xhtml+xml", forHTTPHeaderField: "Accept")

        guard let (data, response) = try? await URLSession.shared.data(for: request),
              (response as? HTTPURLResponse)?.statusCode == 200 else {
            return LinkPreviewResult(url: url.absoluteString, title: nil, description: nil, imageURL: nil, domain: domain)
        }

        let truncated = Data(data.prefix(65_536))
        let html = String(data: truncated, encoding: .utf8)
                ?? String(data: truncated, encoding: .isoLatin1)
                ?? ""

        let rawTitle       = metaContent(html: html, attribute: "property", value: "og:title")
                           ?? tagContent(html: html, tag: "title")
        let rawDescription = metaContent(html: html, attribute: "property", value: "og:description")
                           ?? metaContent(html: html, attribute: "name", value: "description")
        var rawImageURL    = metaContent(html: html, attribute: "property", value: "og:image")

        if let img = rawImageURL {
            if img.hasPrefix("//") {
                rawImageURL = "\(url.scheme ?? "https"):\(img)"
            } else if img.hasPrefix("/"), let host = url.host {
                rawImageURL = "\(url.scheme ?? "https")://\(host)\(img)"
            } else if !img.hasPrefix("http") {
                rawImageURL = nil
            }
        }

        func clean(_ s: String?) -> String? {
            guard let s else { return nil }
            let v = decodeHTMLEntities(s).trimmingCharacters(in: .whitespacesAndNewlines)
            return v.isEmpty ? nil : v
        }

        return LinkPreviewResult(
            url: url.absoluteString,
            title: clean(rawTitle),
            description: clean(rawDescription),
            imageURL: rawImageURL,
            domain: domain
        )
    }

    private static func oEmbedEndpoint(for url: URL) -> URL? {
        let host = url.host ?? ""
        guard let encoded = url.absoluteString
                .addingPercentEncoding(withAllowedCharacters: .urlQueryAllowed) else { return nil }
        if host.contains("youtube.com") || host.contains("youtu.be") {
            return URL(string: "https://www.youtube.com/oembed?url=\(encoded)&format=json")
        }
        if host.contains("vimeo.com") {
            return URL(string: "https://vimeo.com/api/oembed.json?url=\(encoded)")
        }
        if host.contains("soundcloud.com") {
            return URL(string: "https://soundcloud.com/oembed?url=\(encoded)&format=json")
        }
        if host.contains("twitter.com") || host.contains("x.com") {
            return URL(string: "https://publish.twitter.com/oembed?url=\(encoded)")
        }
        return nil
    }

    private static func fetchOEmbed(url: URL, domain: String?) async -> LinkPreviewResult? {
        guard let endpoint = oEmbedEndpoint(for: url) else { return nil }
        var req = URLRequest(url: endpoint, cachePolicy: .reloadIgnoringLocalCacheData, timeoutInterval: 5)
        req.setValue("application/json", forHTTPHeaderField: "Accept")
        guard let (data, response) = try? await URLSession.shared.data(for: req),
              (response as? HTTPURLResponse)?.statusCode == 200,
              let json = try? JSONSerialization.jsonObject(with: data) as? [String: Any] else {
            return nil
        }
        let title        = json["title"]         as? String
        let thumbnailURL = json["thumbnail_url"] as? String
        let provider     = (json["provider_name"] as? String)?.lowercased()
                        ?? domain
        guard let title, !title.isEmpty else { return nil }
        return LinkPreviewResult(
            url: url.absoluteString,
            title: title,
            description: nil,
            imageURL: thumbnailURL,
            domain: provider
        )
    }

    private static func metaContent(html: String, attribute: String, value: String) -> String? {
        let escaped = NSRegularExpression.escapedPattern(for: value)
        let patterns = [
            "\(attribute)=\"\(escaped)\"[^>]{0,500}content=\"([^\"<>]{1,2000})\"",
            "content=\"([^\"<>]{1,2000})\"[^>]{0,500}\(attribute)=\"\(escaped)\"",
            "\(attribute)='\(escaped)'[^>]{0,500}content='([^'<>]{1,2000})'",
            "content='([^'<>]{1,2000})'[^>]{0,500}\(attribute)='\(escaped)'"
        ]
        for pattern in patterns {
            guard let regex = try? NSRegularExpression(pattern: pattern, options: [.caseInsensitive]),
                  let match = regex.firstMatch(in: html, options: [], range: NSRange(html.startIndex..., in: html)),
                  match.numberOfRanges > 1 else { continue }
            let nsRange = match.range(at: 1)
            guard nsRange.location != NSNotFound, let swiftRange = Range(nsRange, in: html) else { continue }
            let v = String(html[swiftRange])
            if !v.isEmpty { return v }
        }
        return nil
    }

    private static func tagContent(html: String, tag: String) -> String? {
        let pattern = "<\(tag)[^>]*>([^<]{1,500})</\(tag)>"
        guard let regex = try? NSRegularExpression(pattern: pattern, options: [.caseInsensitive]),
              let match = regex.firstMatch(in: html, options: [], range: NSRange(html.startIndex..., in: html)),
              match.numberOfRanges > 1 else { return nil }
        let nsRange = match.range(at: 1)
        guard nsRange.location != NSNotFound, let swiftRange = Range(nsRange, in: html) else { return nil }
        return String(html[swiftRange])
    }

    private static func decodeHTMLEntities(_ string: String) -> String {
        string
            .replacingOccurrences(of: "&amp;", with: "&")
            .replacingOccurrences(of: "&lt;", with: "<")
            .replacingOccurrences(of: "&gt;", with: ">")
            .replacingOccurrences(of: "&quot;", with: "\"")
            .replacingOccurrences(of: "&#39;", with: "'")
            .replacingOccurrences(of: "&apos;", with: "'")
            .replacingOccurrences(of: "&nbsp;", with: " ")
    }
}
