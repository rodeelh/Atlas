import XCTest
@testable import AtlasShared

final class CommunicationModelsTests: XCTestCase {
    func testCommunicationDestinationIDIncludesSlackThreadIdentity() {
        let base = CommunicationDestination(
            platform: .slack,
            channelID: "C123",
            channelName: "ops"
        )
        let threaded = CommunicationDestination(
            platform: .slack,
            channelID: "C123",
            channelName: "ops",
            threadID: "171111.000100"
        )

        XCTAssertNotEqual(base.id, threaded.id)
    }

    func testCommunicationPlatformStatusRoundTripsSlackSetupFields() throws {
        let status = CommunicationPlatformStatus(
            platform: .slack,
            enabled: true,
            connected: false,
            available: true,
            setupState: .partialSetup,
            statusLabel: "Partial Setup",
            connectedAccountName: "Atlas Workspace",
            credentialConfigured: true,
            blockingReason: "Slack app token is missing.",
            requiredCredentials: ["slackBot", "slackApp"]
        )

        let encoded = try JSONEncoder().encode(status)
        let decoded = try JSONDecoder().decode(CommunicationPlatformStatus.self, from: encoded)

        XCTAssertEqual(decoded.platform, ChatPlatform.slack)
        XCTAssertEqual(decoded.setupState, CommunicationSetupState.partialSetup)
        XCTAssertEqual(decoded.blockingReason, "Slack app token is missing.")
        XCTAssertEqual(decoded.requiredCredentials, ["slackBot", "slackApp"])
    }
}
