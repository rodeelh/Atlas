import XCTest
import AtlasShared
@testable import AtlasCore

final class RuntimeContractSmokeTests: XCTestCase {

    func testOnboardingStatusResponseEncodesCompletedFlag() throws {
        let payload = AgentRuntime.OnboardingStatusResponse(completed: true)
        let data = try AtlasJSON.encoder.encode(payload)
        let object = try XCTUnwrap(JSONSerialization.jsonObject(with: data) as? [String: Any])

        XCTAssertEqual(object["completed"] as? Bool, true)
    }

    func testAPIKeyStatusResponseEncodesExpectedWebFields() throws {
        let payload = APIKeyStatusResponse(
            openAIKeySet: true,
            telegramTokenSet: false,
            discordTokenSet: true,
            slackBotTokenSet: true,
            slackAppTokenSet: false,
            braveSearchKeySet: true,
            anthropicKeySet: true,
            geminiKeySet: false,
            lmStudioKeySet: true,
            customKeys: ["internal-search"]
        )
        let data = try AtlasJSON.encoder.encode(payload)
        let object = try XCTUnwrap(JSONSerialization.jsonObject(with: data) as? [String: Any])

        let expectedKeys = [
            "openAIKeySet",
            "telegramTokenSet",
            "discordTokenSet",
            "slackBotTokenSet",
            "slackAppTokenSet",
            "braveSearchKeySet",
            "anthropicKeySet",
            "geminiKeySet",
            "lmStudioKeySet",
            "customKeys"
        ]

        for key in expectedKeys {
            XCTAssertNotNil(object[key], "Expected API key payload to include '\(key)'.")
        }
    }

    func testRuntimeConfigSnapshotEncodesFieldsUsedByWebManagementFlows() throws {
        let payload = RuntimeConfigSnapshot.defaults
        let data = try AtlasJSON.encoder.encode(payload)
        let object = try XCTUnwrap(JSONSerialization.jsonObject(with: data) as? [String: Any])

        let expectedKeys = [
            "runtimePort",
            "onboardingCompleted",
            "personaName",
            "activeAIProvider",
            "defaultOpenAIModel",
            "webResearchUseJinaReader",
            "remoteAccessEnabled",
            "telegramEnabled",
            "discordEnabled",
            "slackEnabled"
        ]

        for key in expectedKeys {
            XCTAssertNotNil(object[key], "Expected config payload to include '\(key)'.")
        }
    }

    func testRuntimeStatusEncodesOperationalFieldsUsedByShellAndWeb() throws {
        let payload = AtlasRuntimeStatus(
            isRunning: true,
            activeConversationCount: 2,
            lastMessageAt: nil,
            lastError: nil,
            state: .ready,
            runtimePort: 1984,
            startedAt: nil,
            activeRequests: 1,
            pendingApprovalCount: 3,
            details: "Ready"
        )
        let data = try AtlasJSON.encoder.encode(payload)
        let object = try XCTUnwrap(JSONSerialization.jsonObject(with: data) as? [String: Any])

        let expectedKeys = [
            "isRunning",
            "activeConversationCount",
            "state",
            "runtimePort",
            "activeRequests",
            "pendingApprovalCount",
            "details",
            "telegram",
            "communications"
        ]

        for key in expectedKeys {
            XCTAssertNotNil(object[key], "Expected runtime status payload to include '\(key)'.")
        }
    }
}
