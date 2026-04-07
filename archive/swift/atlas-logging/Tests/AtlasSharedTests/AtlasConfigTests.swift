import XCTest
@testable import AtlasShared

final class AtlasConfigTests: XCTestCase {
    func testCredentialBundleOverrideAllowsValidationWithoutKeychainWrites() throws {
        let config = AtlasConfig(
            discordClientID: "123456",
            credentialBundleOverride: AtlasCredentialBundle(
                discordBotToken: "discord-bot-token",
                slackBotToken: "xoxb-test",
                slackAppToken: "xapp-test"
            )
        )

        XCTAssertEqual(try config.discordBotToken(), "discord-bot-token")
        XCTAssertTrue(config.hasDiscordBotToken())
        XCTAssertEqual(try config.slackBotToken(), "xoxb-test")
        XCTAssertEqual(try config.slackAppToken(), "xapp-test")
    }
}
