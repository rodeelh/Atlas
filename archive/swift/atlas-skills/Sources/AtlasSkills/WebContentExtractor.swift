import Foundation

public struct WebContentExtractor: Sendable {
    public let maxCharacters: Int
    public let useJinaReader: Bool

    public init(maxCharacters: Int = 12_000, useJinaReader: Bool = false) {
        self.maxCharacters = max(1_000, maxCharacters)
        self.useJinaReader = useJinaReader
    }

    public func extract(contentType: String, body: String, pageURL: URL) throws -> WebContentExtraction {
        switch contentType {
        case "text/plain":
            let normalized = normalizePlainText(body)
            guard !normalized.isEmpty else {
                throw WebResearchError.emptyExtraction
            }

            let truncated = truncate(normalized)
            return WebContentExtraction(
                title: pageURL.host ?? pageURL.absoluteString,
                extractedText: truncated.text,
                truncated: truncated.truncated
            )
        case "text/html":
            return try extractHTML(body, pageURL: pageURL)
        default:
            throw WebResearchError.unsupportedContentType(contentType)
        }
    }

    /// Async variant that optionally tries Jina Reader first, then falls back to HTML extraction.
    public func extractAsync(contentType: String, body: String, pageURL: URL) async throws -> WebContentExtraction {
        let scheme = pageURL.scheme?.lowercased() ?? ""
        if useJinaReader, scheme == "http" || scheme == "https" {
            if let jinaText = await WebContentExtractor.extractViaJina(url: pageURL.absoluteString) {
                if jinaText.count > 200 {
                    let truncated = truncate(jinaText)
                    return WebContentExtraction(
                        title: pageURL.host ?? pageURL.absoluteString,
                        extractedText: truncated.text,
                        truncated: truncated.truncated
                    )
                }
            }
        }
        return try extract(contentType: contentType, body: body, pageURL: pageURL)
    }

    private static func extractViaJina(url: String, timeout: TimeInterval = 8) async -> String? {
        guard let jinaURL = URL(string: "https://r.jina.ai/\(url)") else { return nil }
        var req = URLRequest(url: jinaURL, cachePolicy: .reloadIgnoringLocalCacheData, timeoutInterval: timeout)
        req.setValue("Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.6 Safari/605.1.15", forHTTPHeaderField: "User-Agent")
        req.setValue("text/markdown", forHTTPHeaderField: "Accept")
        req.setValue("no-cache", forHTTPHeaderField: "X-No-Cache")
        guard let (data, response) = try? await URLSession.shared.data(for: req),
              (response as? HTTPURLResponse)?.statusCode == 200,
              let text = String(data: data, encoding: .utf8),
              text.count > 100 else { return nil }
        let lines = text.components(separatedBy: "\n")
        var startIdx = 0
        for (i, line) in lines.enumerated() {
            let trimmed = line.trimmingCharacters(in: .whitespaces)
            if trimmed.hasPrefix("Title:") || trimmed.hasPrefix("URL Source:") || trimmed.hasPrefix("Published Time:") || trimmed.hasPrefix("Markdown Content:") || trimmed.isEmpty {
                startIdx = i + 1
            } else {
                break
            }
        }
        let content = lines.dropFirst(startIdx).joined(separator: "\n").trimmingCharacters(in: .whitespacesAndNewlines)
        return content.isEmpty ? nil : content
    }

    // MARK: - Links extraction

    /// Extracts outbound anchor links from raw HTML, resolved against the page base URL.
    public func extractLinks(from html: String, baseURL: URL) -> [WebExtractedLink] {
        guard let anchorRegex = try? NSRegularExpression(
            pattern: #"<a[^>]+href=["']([^"'#][^"']*)["'][^>]*>(.*?)</a>"#,
            options: [.caseInsensitive, .dotMatchesLineSeparators]
        ) else { return [] }

        let nsRange = NSRange(html.startIndex..<html.endIndex, in: html)
        let matches = anchorRegex.matches(in: html, range: nsRange)
        var seen = Set<String>()
        var links: [WebExtractedLink] = []

        for match in matches {
            guard
                let hrefRange = Range(match.range(at: 1), in: html),
                let textRange = Range(match.range(at: 2), in: html)
            else { continue }

            let rawHref = String(html[hrefRange]).trimmingCharacters(in: .whitespacesAndNewlines)
            guard !rawHref.isEmpty, !rawHref.hasPrefix("javascript:"), !rawHref.hasPrefix("mailto:") else { continue }

            guard let resolved = URL(string: rawHref, relativeTo: baseURL)?.absoluteURL,
                  let scheme = resolved.scheme?.lowercased(),
                  scheme == "http" || scheme == "https",
                  let host = resolved.host?.lowercased(),
                  !host.isEmpty
            else { continue }

            let abs = resolved.absoluteString
            guard seen.insert(abs).inserted else { continue }

            let rawText = String(html[textRange])
            let text = cleanHTMLFragment(rawText)

            links.append(WebExtractedLink(url: abs, text: text.isEmpty ? abs : text, domain: host))
        }

        return links
    }

    // MARK: - HTML

    private func extractHTML(_ html: String, pageURL: URL) throws -> WebContentExtraction {
        let title = extractTitle(from: html) ?? pageURL.host ?? pageURL.absoluteString
        let sanitizedHTML = sanitizeHTML(html)
        let readableText = strippedHTMLText(from: sanitizedHTML)
        let normalized = normalizePlainText(readableText)

        guard !normalized.isEmpty else {
            throw WebResearchError.emptyExtraction
        }

        let truncated = truncate(normalized)
        return WebContentExtraction(
            title: title,
            extractedText: truncated.text,
            truncated: truncated.truncated
        )
    }

    private func sanitizeHTML(_ html: String) -> String {
        var sanitized = html

        let removalPatterns = [
            #"(?is)<!--.*?-->"#,
            #"(?is)<script\b[^>]*>.*?</script>"#,
            #"(?is)<style\b[^>]*>.*?</style>"#,
            #"(?is)<noscript\b[^>]*>.*?</noscript>"#,
            #"(?is)<svg\b[^>]*>.*?</svg>"#,
            #"(?is)<canvas\b[^>]*>.*?</canvas>"#,
            #"(?is)<nav\b[^>]*>.*?</nav>"#,
            #"(?is)<header\b[^>]*>.*?</header>"#,
            #"(?is)<footer\b[^>]*>.*?</footer>"#,
            #"(?is)<aside\b[^>]*>.*?</aside>"#,
            #"(?is)<form\b[^>]*>.*?</form>"#
        ]

        for pattern in removalPatterns {
            sanitized = sanitized.replacingOccurrences(
                of: pattern,
                with: " ",
                options: .regularExpression
            )
        }

        return sanitized
    }

    private func strippedHTMLText(from html: String) -> String {
        let withBlockSpacing = html
            .replacingOccurrences(of: #"(?i)</(p|div|section|article|li|tr|h[1-6]|br)\s*>"#, with: "\n", options: .regularExpression)
            .replacingOccurrences(of: #"(?is)<[^>]+>"#, with: " ", options: .regularExpression)

        return decodeHTMLEntities(withBlockSpacing)
    }

    func cleanHTMLFragment(_ value: String) -> String {
        let stripped = value.replacingOccurrences(
            of: #"(?is)<[^>]+>"#,
            with: " ",
            options: .regularExpression
        )
        return decodeHTMLEntities(stripped)
            .replacingOccurrences(of: #"\s+"#, with: " ", options: .regularExpression)
            .trimmingCharacters(in: .whitespacesAndNewlines)
    }

    private func extractTitle(from html: String) -> String? {
        let patterns = [
            #"(?is)<meta[^>]+property=["']og:title["'][^>]+content=["'](.*?)["'][^>]*>"#,
            #"(?is)<title[^>]*>(.*?)</title>"#
        ]

        for pattern in patterns {
            if let value = firstCapture(in: html, pattern: pattern) {
                let normalized = normalizePlainText(decodeHTMLEntities(value))
                if !normalized.isEmpty {
                    return normalized
                }
            }
        }

        return nil
    }

    private func firstCapture(in text: String, pattern: String) -> String? {
        guard let regex = try? NSRegularExpression(pattern: pattern) else {
            return nil
        }

        let nsRange = NSRange(text.startIndex..<text.endIndex, in: text)
        guard
            let match = regex.firstMatch(in: text, options: [], range: nsRange),
            match.numberOfRanges > 1,
            let range = Range(match.range(at: 1), in: text)
        else {
            return nil
        }

        return String(text[range])
    }

    private func normalizePlainText(_ text: String) -> String {
        text
            .replacingOccurrences(of: #"[ \t\r\f]+"#, with: " ", options: .regularExpression)
            .replacingOccurrences(of: #"\n\s*\n\s*\n+"#, with: "\n\n", options: .regularExpression)
            .replacingOccurrences(of: #"[ \t]*\n[ \t]*"#, with: "\n", options: .regularExpression)
            .trimmingCharacters(in: .whitespacesAndNewlines)
    }

    private func truncate(_ text: String) -> (text: String, truncated: Bool) {
        guard text.count > maxCharacters else {
            return (text, false)
        }

        let cutoffIndex = text.index(text.startIndex, offsetBy: maxCharacters)
        let prefix = String(text[..<cutoffIndex])

        if let boundary = prefix.lastIndex(where: { $0 == "\n" || $0 == "." || $0 == " " }) {
            let candidate = String(prefix[..<boundary]).trimmingCharacters(in: .whitespacesAndNewlines)
            if candidate.count > maxCharacters / 2 {
                return (candidate, true)
            }
        }

        return (prefix.trimmingCharacters(in: .whitespacesAndNewlines), true)
    }

    // MARK: - HTML entity decoding (30+ named + numeric)

    private func decodeHTMLEntities(_ text: String) -> String {
        let named: [String: String] = [
            "&amp;": "&",
            "&lt;": "<",
            "&gt;": ">",
            "&quot;": "\"",
            "&#39;": "'",
            "&apos;": "'",
            "&nbsp;": " ",
            "&ndash;": "\u{2013}",
            "&mdash;": "\u{2014}",
            "&hellip;": "\u{2026}",
            "&laquo;": "\u{00AB}",
            "&raquo;": "\u{00BB}",
            "&lsquo;": "\u{2018}",
            "&rsquo;": "\u{2019}",
            "&ldquo;": "\u{201C}",
            "&rdquo;": "\u{201D}",
            "&bull;": "\u{2022}",
            "&middot;": "\u{00B7}",
            "&copy;": "\u{00A9}",
            "&reg;": "\u{00AE}",
            "&trade;": "\u{2122}",
            "&euro;": "\u{20AC}",
            "&pound;": "\u{00A3}",
            "&yen;": "\u{00A5}",
            "&cent;": "\u{00A2}",
            "&deg;": "\u{00B0}",
            "&plusmn;": "\u{00B1}",
            "&times;": "\u{00D7}",
            "&divide;": "\u{00F7}",
            "&frac12;": "\u{00BD}",
            "&frac14;": "\u{00BC}",
            "&frac34;": "\u{00BE}",
            "&sup2;": "\u{00B2}",
            "&sup3;": "\u{00B3}",
            "&micro;": "\u{00B5}",
            "&para;": "\u{00B6}",
            "&sect;": "\u{00A7}",
            "&dagger;": "\u{2020}",
            "&Dagger;": "\u{2021}",
            "&permil;": "\u{2030}",
            "&prime;": "\u{2032}",
            "&Prime;": "\u{2033}",
            "&larr;": "\u{2190}",
            "&rarr;": "\u{2192}",
            "&uarr;": "\u{2191}",
            "&darr;": "\u{2193}",
            "&harr;": "\u{2194}"
        ]

        var result = text
        for (entity, replacement) in named {
            result = result.replacingOccurrences(of: entity, with: replacement)
        }

        // Decimal numeric entities &#NNN;
        // All match positions are captured against the unmodified string first, then applied
        // in reverse order so earlier replacements don't shift positions of later ones.
        if let decimalRegex = try? NSRegularExpression(pattern: #"&#(\d+);"#) {
            let snapshot = result
            let matches = decimalRegex.matches(in: snapshot, range: NSRange(snapshot.startIndex..., in: snapshot))
            var replacements: [(range: NSRange, replacement: String)] = []
            for match in matches {
                guard match.numberOfRanges > 1,
                      let numRange = Range(match.range(at: 1), in: snapshot),
                      let codePoint = UInt32(snapshot[numRange]),
                      let scalar = Unicode.Scalar(codePoint)
                else { continue }
                replacements.append((range: match.range, replacement: String(scalar)))
            }
            for item in replacements.reversed() {
                let mutable = NSMutableString(string: result)
                mutable.replaceCharacters(in: item.range, with: item.replacement)
                result = mutable as String
            }
        }

        // Hex numeric entities &#xNNNN;
        if let hexRegex = try? NSRegularExpression(pattern: #"&#x([0-9A-Fa-f]+);"#) {
            let snapshot = result
            let matches = hexRegex.matches(in: snapshot, range: NSRange(snapshot.startIndex..., in: snapshot))
            var replacements: [(range: NSRange, replacement: String)] = []
            for match in matches {
                guard match.numberOfRanges > 1,
                      let numRange = Range(match.range(at: 1), in: snapshot),
                      let codePoint = UInt32(snapshot[numRange], radix: 16),
                      let scalar = Unicode.Scalar(codePoint)
                else { continue }
                replacements.append((range: match.range, replacement: String(scalar)))
            }
            for item in replacements.reversed() {
                let mutable = NSMutableString(string: result)
                mutable.replaceCharacters(in: item.range, with: item.replacement)
                result = mutable as String
            }
        }

        return result
    }
}
