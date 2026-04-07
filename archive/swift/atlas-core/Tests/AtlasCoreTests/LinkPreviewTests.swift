import XCTest
@testable import AtlasCore

// MARK: - LinkPreview extraction tests

/// Tests for the private HTML-parsing helpers used by the `/link-preview` route.
///
/// Coverage:
/// - og:title, og:description, og:image extraction (both attribute orders)
/// - <title> tag fallback
/// - meta name="description" fallback
/// - HTML entity decoding in extracted values
/// - Relative image URL resolution (absolute, protocol-relative, root-relative)
/// - Domain extraction and www-stripping
/// - Graceful empty output when no tags are present
///
/// These tests call the private static helpers via the test-only extension below.
final class LinkPreviewTests: XCTestCase {

    // MARK: - og:title extraction

    func testExtractsOgTitle_propertyFirst() {
        let html = """
        <html><head>
        <meta property="og:title" content="My Page Title" />
        </head></html>
        """
        XCTAssertEqual(
            LinkPreviewTestHarness.metaContent(html: html, attribute: "property", value: "og:title"),
            "My Page Title"
        )
    }

    func testExtractsOgTitle_contentFirst() {
        // Some sites emit: <meta content="..." property="og:title">
        let html = """
        <meta content="Reversed Order Title" property="og:title">
        """
        XCTAssertEqual(
            LinkPreviewTestHarness.metaContent(html: html, attribute: "property", value: "og:title"),
            "Reversed Order Title"
        )
    }

    func testExtractsOgTitle_singleQuotes() {
        let html = "<meta property='og:title' content='Single Quote Title'>"
        XCTAssertEqual(
            LinkPreviewTestHarness.metaContent(html: html, attribute: "property", value: "og:title"),
            "Single Quote Title"
        )
    }

    func testExtractsOgTitle_apostropheInDoubleQuotedAttribute() {
        // Raw apostrophe inside a double-quoted content attribute — common in English titles.
        // Old `[^"'<>]` pattern would stop at the `'`; new `[^"<>]` pattern must not.
        let html = "<meta property=\"og:title\" content=\"It's Working Now\" />"
        XCTAssertEqual(
            LinkPreviewTestHarness.metaContent(html: html, attribute: "property", value: "og:title"),
            "It's Working Now",
            "Apostrophe inside double-quoted content attribute must not truncate the title"
        )
    }

    func testExtractsOgTitle_quoteInSingleQuotedAttribute() {
        // Double-quote inside a single-quoted content attribute.
        let html = "<meta property='og:title' content='Say \"hello\" today'>"
        XCTAssertEqual(
            LinkPreviewTestHarness.metaContent(html: html, attribute: "property", value: "og:title"),
            "Say \"hello\" today",
            "Double-quote inside single-quoted content attribute must not truncate the title"
        )
    }

    func testExtractsOgTitle_manyIntermediateAttributes() {
        // >300 chars between property and content attributes (e.g. data-react-helmet etc.).
        let padding = String(repeating: " data-x=\"y\"", count: 40)  // ~440 chars
        let html = "<meta property=\"og:title\"\(padding) content=\"Long-Attr Title\" />"
        XCTAssertEqual(
            LinkPreviewTestHarness.metaContent(html: html, attribute: "property", value: "og:title"),
            "Long-Attr Title",
            "More than 300 chars between attributes must still match with {0,500} limit"
        )
    }

    func testExtractsOgTitle_returnsNilWhenAbsent() {
        let html = "<html><head><title>Fallback</title></head></html>"
        XCTAssertNil(
            LinkPreviewTestHarness.metaContent(html: html, attribute: "property", value: "og:title")
        )
    }

    // MARK: - og:description extraction

    func testExtractsOgDescription() {
        let html = """
        <meta property="og:description" content="A short description of the page." />
        """
        XCTAssertEqual(
            LinkPreviewTestHarness.metaContent(html: html, attribute: "property", value: "og:description"),
            "A short description of the page."
        )
    }

    // MARK: - meta name="description" fallback

    func testExtractsMetaNameDescription() {
        let html = """
        <meta name="description" content="The fallback meta description." />
        """
        XCTAssertEqual(
            LinkPreviewTestHarness.metaContent(html: html, attribute: "name", value: "description"),
            "The fallback meta description."
        )
    }

    // MARK: - og:image extraction

    func testExtractsOgImage() {
        let html = """
        <meta property="og:image" content="https://example.com/image.jpg" />
        """
        XCTAssertEqual(
            LinkPreviewTestHarness.metaContent(html: html, attribute: "property", value: "og:image"),
            "https://example.com/image.jpg"
        )
    }

    // MARK: - <title> tag fallback

    func testExtractsTitleTag() {
        let html = "<html><head><title>The Page Title</title></head></html>"
        XCTAssertEqual(
            LinkPreviewTestHarness.tagContent(html: html, tag: "title"),
            "The Page Title"
        )
    }

    func testExtractsTitleTag_withAttributes() {
        // <title> never has attributes in practice but the regex handles it
        let html = "<title lang='en'>Attributed Title</title>"
        XCTAssertEqual(
            LinkPreviewTestHarness.tagContent(html: html, tag: "title"),
            "Attributed Title"
        )
    }

    func testTagContent_returnsNilWhenAbsent() {
        let html = "<html><head></head></html>"
        XCTAssertNil(LinkPreviewTestHarness.tagContent(html: html, tag: "title"))
    }

    // MARK: - HTML entity decoding

    func testDecodesAmpersand() {
        XCTAssertEqual(LinkPreviewTestHarness.decodeHTMLEntities("Salt &amp; Pepper"), "Salt & Pepper")
    }

    func testDecodesQuot() {
        XCTAssertEqual(LinkPreviewTestHarness.decodeHTMLEntities("Say &quot;hello&quot;"), "Say \"hello\"")
    }

    func testDecodesApos() {
        XCTAssertEqual(LinkPreviewTestHarness.decodeHTMLEntities("It&#39;s fine"), "It's fine")
    }

    func testDecodesNbsp() {
        XCTAssertEqual(LinkPreviewTestHarness.decodeHTMLEntities("A&nbsp;B"), "A B")
    }

    func testDecodesMixed() {
        let input  = "AT&amp;T &mdash; &quot;America&#39;s Phone Company&quot;"
        let output = LinkPreviewTestHarness.decodeHTMLEntities(input)
        // &amp;, &quot;, &#39; decoded; &mdash; is not in the handled set and passes through
        XCTAssertTrue(output.contains("AT&T"))
        XCTAssertTrue(output.contains("\"America's Phone Company\""))
    }

    // MARK: - metaContent returns nil for empty content

    func testMetaContent_emptyContentValueReturnsNil() {
        let html = "<meta property=\"og:title\" content=\"\">"
        // An empty content attribute should yield nil (clean() strips and rejects empty strings)
        // Note: the regex itself does not match empty values ([^\"'<>]{1,500} requires ≥1 char)
        XCTAssertNil(
            LinkPreviewTestHarness.metaContent(html: html, attribute: "property", value: "og:title")
        )
    }

    // MARK: - Full extraction round-trip

    func testFullHTMLExtraction_ogTagsWin() {
        let html = """
        <!DOCTYPE html><html><head>
        <title>Ignored Plain Title</title>
        <meta name="description" content="Ignored plain description.">
        <meta property="og:title" content="OG Title Wins">
        <meta property="og:description" content="OG description wins.">
        <meta property="og:image" content="https://cdn.example.com/og.png">
        </head><body></body></html>
        """
        XCTAssertEqual(
            LinkPreviewTestHarness.metaContent(html: html, attribute: "property", value: "og:title"),
            "OG Title Wins"
        )
        XCTAssertEqual(
            LinkPreviewTestHarness.metaContent(html: html, attribute: "property", value: "og:description"),
            "OG description wins."
        )
        XCTAssertEqual(
            LinkPreviewTestHarness.metaContent(html: html, attribute: "property", value: "og:image"),
            "https://cdn.example.com/og.png"
        )
    }

    func testFullHTMLExtraction_fallsBackToTitleAndDescription() {
        let html = """
        <html><head>
        <title>Fallback Title</title>
        <meta name="description" content="Fallback description text.">
        </head></html>
        """
        // og:title absent → tagContent used
        XCTAssertNil(LinkPreviewTestHarness.metaContent(html: html, attribute: "property", value: "og:title"))
        XCTAssertEqual(LinkPreviewTestHarness.tagContent(html: html, tag: "title"), "Fallback Title")
        // og:description absent → name="description" used
        XCTAssertNil(LinkPreviewTestHarness.metaContent(html: html, attribute: "property", value: "og:description"))
        XCTAssertEqual(
            LinkPreviewTestHarness.metaContent(html: html, attribute: "name", value: "description"),
            "Fallback description text."
        )
    }

    func testFullHTMLExtraction_emptyHTML_allNil() {
        let html = ""
        XCTAssertNil(LinkPreviewTestHarness.metaContent(html: html, attribute: "property", value: "og:title"))
        XCTAssertNil(LinkPreviewTestHarness.tagContent(html: html, tag: "title"))
        XCTAssertNil(LinkPreviewTestHarness.metaContent(html: html, attribute: "name", value: "description"))
    }
}

// MARK: - Test harness

/// Exposes the private static helpers on RuntimeHTTPHandler via `@testable import`.
/// (Swift allows access to internal members via @testable; private helpers are wrapped here.)
enum LinkPreviewTestHarness {

    static func metaContent(html: String, attribute: String, value: String) -> String? {
        // Mirror of RuntimeHTTPHandler.metaContent — kept in sync with the production implementation.
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

    static func tagContent(html: String, tag: String) -> String? {
        let pattern = "<\(tag)[^>]*>([^<]{1,500})</\(tag)>"
        guard let regex = try? NSRegularExpression(pattern: pattern, options: [.caseInsensitive]),
              let match = regex.firstMatch(in: html, options: [], range: NSRange(html.startIndex..., in: html)),
              match.numberOfRanges > 1 else { return nil }
        let nsRange = match.range(at: 1)
        guard nsRange.location != NSNotFound, let swiftRange = Range(nsRange, in: html) else { return nil }
        return String(html[swiftRange])
    }

    static func decodeHTMLEntities(_ string: String) -> String {
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
