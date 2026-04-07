import XCTest
import AtlasMemory
import AtlasShared

final class GremlinFileParserTests: XCTestCase {
    func testParseLegacyTelegramNotificationMapsToCommunicationDestination() {
        let markdown = """
        ## Morning Briefing
        schedule: daily 08:00
        status: enabled
        created: 2026-03-27 via chat
        notify_telegram: 123456

        Send a quick summary.
        """

        let parser = GremlinFileParser()
        let items = parser.parse(markdown: markdown)

        XCTAssertEqual(items.count, 1)
        XCTAssertEqual(items.first?.telegramChatID, 123456)
        XCTAssertEqual(
            items.first?.communicationDestination,
            CommunicationDestination(platform: .telegram, channelID: "123456")
        )
    }

    func testParseAndSerialiseGenericCommunicationDestinationRoundTrips() {
        let markdown = """
        ## Release Watch
        schedule: weekly friday 09:00
        status: enabled
        created: 2026-03-27 via manual
        notify_destination: discord:channel-99

        Watch release chatter.
        """

        let parser = GremlinFileParser()
        let parsed = parser.parse(markdown: markdown)

        XCTAssertEqual(parsed.count, 1)
        XCTAssertEqual(
            parsed.first?.communicationDestination,
            CommunicationDestination(platform: .discord, channelID: "channel-99")
        )

        let serialised = parser.serialise(parsed)
        XCTAssertTrue(serialised.contains("notify_destination: discord:channel-99"))
        XCTAssertFalse(serialised.contains("notify_telegram:"))
    }
}
