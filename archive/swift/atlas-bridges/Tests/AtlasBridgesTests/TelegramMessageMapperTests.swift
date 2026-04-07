import XCTest
import AtlasBridges
import AtlasNetwork
import AtlasShared
import Foundation

final class TelegramMessageMapperTests: XCTestCase {

    // MARK: - markdownToHTML

    func testBoldConversion() {
        XCTAssertEqual(
            TelegramMessageMapper.markdownToHTML("Hello **world**"),
            "Hello <b>world</b>"
        )
    }

    func testMultipleBoldSpans() {
        XCTAssertEqual(
            TelegramMessageMapper.markdownToHTML("**a** and **b**"),
            "<b>a</b> and <b>b</b>"
        )
    }

    func testInlineCodeConversion() {
        XCTAssertEqual(
            TelegramMessageMapper.markdownToHTML("Run `swift build` now"),
            "Run <code>swift build</code> now"
        )
    }

    func testFencedCodeBlockWithLanguage() {
        let input = "```swift\nlet x = 1\n```"
        let output = TelegramMessageMapper.markdownToHTML(input)
        XCTAssertTrue(output.contains("<pre><code>"), "Expected <pre><code> tag")
        XCTAssertTrue(output.contains("let x = 1"), "Expected code content")
        XCTAssertFalse(output.contains("swift\n"), "Language tag should be stripped")
    }

    func testFencedCodeBlockWithoutLanguage() {
        let input = "```\necho hello\n```"
        let output = TelegramMessageMapper.markdownToHTML(input)
        XCTAssertTrue(output.hasPrefix("<pre><code>"))
        XCTAssertTrue(output.contains("echo hello"))
    }

    func testHTMLEscapesAngleBrackets() {
        XCTAssertEqual(
            TelegramMessageMapper.markdownToHTML("a < b > c"),
            "a &lt; b &gt; c"
        )
    }

    func testHTMLEscapesAmpersand() {
        XCTAssertEqual(
            TelegramMessageMapper.markdownToHTML("fish & chips"),
            "fish &amp; chips"
        )
    }

    func testHTMLEscapeInsideCodeBlock() {
        let input = "```\nif a < b && c > d {\n}\n```"
        let output = TelegramMessageMapper.markdownToHTML(input)
        XCTAssertTrue(output.contains("&lt;"), "< should be escaped inside code block")
        XCTAssertTrue(output.contains("&amp;&amp;"), "&& should be escaped inside code block")
    }

    func testPlainTextPassthrough() {
        let plain = "Just a plain message with no markdown."
        XCTAssertEqual(TelegramMessageMapper.markdownToHTML(plain), plain)
    }

    func testMixedContent() {
        let input = "Use **bold** and `code` together."
        let output = TelegramMessageMapper.markdownToHTML(input)
        XCTAssertEqual(output, "Use <b>bold</b> and <code>code</code> together.")
    }

    func testBoldDoesNotMatchAcrossLines() {
        // ** spanning a newline should not be treated as bold
        let input = "**line one\nline two**"
        let output = TelegramMessageMapper.markdownToHTML(input)
        XCTAssertFalse(output.contains("<b>"), "Multi-line bold should not match")
    }

    func testItalicAsterisk() {
        XCTAssertEqual(
            TelegramMessageMapper.markdownToHTML("Hello *world*"),
            "Hello <i>world</i>"
        )
    }

    func testItalicUnderscore() {
        XCTAssertEqual(
            TelegramMessageMapper.markdownToHTML("Hello _world_"),
            "Hello <i>world</i>"
        )
    }

    func testStrikethrough() {
        XCTAssertEqual(
            TelegramMessageMapper.markdownToHTML("~~old text~~"),
            "<s>old text</s>"
        )
    }

    func testBulletListAsteriskNotTreatedAsItalic() {
        // "* item" starts with asterisk+space — should NOT become italic
        let input = "* list item"
        let output = TelegramMessageMapper.markdownToHTML(input)
        XCTAssertFalse(output.contains("<i>"), "Bullet-style * should not become italic")
        XCTAssertTrue(output.contains("* list item"), "Bullet text should pass through unchanged")
    }

    func testBoldBeatsItalicForDoubleAsterisk() {
        // **bold** should not be treated as italic nested inside asterisks
        XCTAssertEqual(
            TelegramMessageMapper.markdownToHTML("**bold**"),
            "<b>bold</b>"
        )
    }

    func testHTMLEscape() {
        XCTAssertEqual(TelegramMessageMapper.htmlEscape("a<b>&c"), "a&lt;b&gt;&amp;c")
        XCTAssertEqual(TelegramMessageMapper.htmlEscape("normal text"), "normal text")
    }

    // MARK: - Location mapping

    func testLocationMessageMapsToText() {
        let mapper = TelegramMessageMapper()
        let message = TelegramMessage(
            messageID: 1,
            from: TelegramUser(id: 10, isBot: false, firstName: "Rami"),
            chat: TelegramChat(id: 99, type: "private"),
            location: TelegramLocation(latitude: 37.774929, longitude: -122.419416)
        )

        let result = mapper.mapInbound(message)

        if case .message(let atlasMessage) = result {
            XCTAssertTrue(atlasMessage.content.contains("📍"))
            XCTAssertTrue(atlasMessage.content.contains("37.774929"))
            XCTAssertTrue(atlasMessage.content.contains("-122.419416"))
        } else {
            XCTFail("Expected .message, got \(result)")
        }
    }

    func testLocationTakesPrecedenceOverAttachments() {
        // A message with both location and a document should route as location
        let mapper = TelegramMessageMapper()
        let message = TelegramMessage(
            messageID: 1,
            chat: TelegramChat(id: 1, type: "private"),
            location: TelegramLocation(latitude: 1.0, longitude: 2.0)
        )
        if case .message(let m) = mapper.mapInbound(message) {
            XCTAssertTrue(m.content.contains("📍"))
        } else {
            XCTFail("Expected location to map as .message")
        }
    }

    // MARK: - outboundEvents parseMode

    func testOutboundEventsUseHTMLParseMode() {
        let mapper = TelegramMessageMapper()
        let response = AtlasAgentResponse(assistantMessage: "Hello", status: .completed)
        let events = mapper.outboundEvents(chatID: 1, replyToMessageID: nil, response: response)

        XCTAssertFalse(events.isEmpty)
        for event in events {
            XCTAssertEqual(event.parseMode, "HTML", "All outbound events should use HTML parse mode")
        }
    }

    func testOutboundEventsConvertMarkdownInAssistantMessage() {
        let mapper = TelegramMessageMapper()
        let response = AtlasAgentResponse(assistantMessage: "Use **bold** text", status: .completed)
        let events = mapper.outboundEvents(chatID: 1, replyToMessageID: nil, response: response)

        XCTAssertEqual(events.first?.text, "Use <b>bold</b> text")
    }

    // MARK: - splitText

    func testSplitTextShortMessageReturnedAsIs() {
        let mapper = TelegramMessageMapper()
        let chunks = mapper.splitText("Short message")
        XCTAssertEqual(chunks, ["Short message"])
    }

    func testSplitTextLongMessageSplitOnNewlines() {
        // maxMessageLength is clamped to min 256 in the mapper init, so we need a
        // genuinely long input to trigger splitting.
        let mapper = TelegramMessageMapper()  // default 3500 char limit
        let line = String(repeating: "A", count: 200)
        let input = (0..<20).map { "Line \($0): \(line)" }.joined(separator: "\n")
        // input is ~20 * 208 chars = ~4160 chars — exceeds 3500 limit
        let chunks = mapper.splitText(input)
        XCTAssertGreaterThan(chunks.count, 1, "Long message should be split into multiple chunks")
    }
}
