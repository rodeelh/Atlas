import XCTest
@testable import AtlasBridges
import AtlasMemory
import AtlasNetwork
import AtlasShared
import Foundation

final class SlackBridgeTests: XCTestCase {
    func testSocketEnvelopeDecodesHelloWithoutEnvelopeID() throws {
        let data = Data("""
        {
          "type": "hello"
        }
        """.utf8)

        let envelope = try AtlasJSON.decoder.decode(SlackSocketEnvelope.self, from: data)

        XCTAssertNil(envelope.envelopeID)
        XCTAssertEqual(envelope.type, "hello")
        XCTAssertNil(envelope.event)
    }

    func testSocketEnvelopeDecodesEventsAPIEnvelopeWithEnvelopeID() throws {
        let data = Data("""
        {
          "envelope_id": "env-123",
          "type": "events_api",
          "payload": {
            "event": {
              "type": "app_mention",
              "channel": "C123",
              "channel_type": "channel",
              "user": "U123",
              "text": "<@B123> ping",
              "ts": "171111.000100"
            }
          }
        }
        """.utf8)

        let envelope = try AtlasJSON.decoder.decode(SlackSocketEnvelope.self, from: data)

        XCTAssertEqual(envelope.envelopeID, "env-123")
        XCTAssertEqual(envelope.type, "events_api")
        XCTAssertEqual(envelope.event?.channel, "C123")
    }

    func testBridgeRoutesDirectMessageThroughRuntimeAndRepliesWithoutThread() async throws {
        let runtime = MockSlackRuntime()
        let client = MockSlackClient()
        let memoryStore = try MemoryStore(databasePath: temporaryDatabasePath())
        let sessionStore = CommunicationSessionStore(memoryStore: memoryStore)
        let bridge = SlackBridge(
            config: AtlasConfig(slackEnabled: true),
            client: client,
            runtime: runtime,
            sessionStore: sessionStore
        )

        await bridge.handle(event: SlackMessageEvent(
            type: "message",
            channel: "D123",
            channelType: "im",
            user: "U1",
            text: "Hello from Slack",
            ts: "171111.000100"
        ))

        let sentMessages = await client.sentMessages()
        let latestRequest = await runtime.latestRequest()

        XCTAssertEqual(latestRequest?.message, "Hello from Slack")
        XCTAssertEqual(sentMessages.last?.channelID, "D123")
        XCTAssertNil(sentMessages.last?.threadID)
    }

    func testBridgeRoutesMentionThreadAndRepliesInsideThread() async throws {
        let runtime = MockSlackRuntime()
        let client = MockSlackClient()
        let memoryStore = try MemoryStore(databasePath: temporaryDatabasePath())
        let sessionStore = CommunicationSessionStore(memoryStore: memoryStore)
        let bridge = SlackBridge(
            config: AtlasConfig(slackEnabled: true),
            client: client,
            runtime: runtime,
            sessionStore: sessionStore
        )

        await bridge.handle(event: SlackMessageEvent(
            type: "app_mention",
            channel: "C123",
            channelType: "channel",
            user: "U2",
            text: "<@B123> status check",
            ts: "171111.000200",
            threadTS: "171111.000100"
        ))

        let sentMessages = await client.sentMessages()
        let channels = try await memoryStore.fetchCommunicationChannels(platform: .slack)

        XCTAssertEqual(sentMessages.last?.threadID, "171111.000100")
        XCTAssertEqual(channels.first?.threadID, "171111.000100")
    }

    func testBridgeIgnoresNonMentionChannelMessages() async throws {
        let runtime = MockSlackRuntime()
        let client = MockSlackClient()
        let memoryStore = try MemoryStore(databasePath: temporaryDatabasePath())
        let sessionStore = CommunicationSessionStore(memoryStore: memoryStore)
        let bridge = SlackBridge(
            config: AtlasConfig(slackEnabled: true),
            client: client,
            runtime: runtime,
            sessionStore: sessionStore
        )

        await bridge.handle(event: SlackMessageEvent(
            type: "message",
            channel: "C999",
            channelType: "channel",
            user: "U3",
            text: "no mention here",
            ts: "171111.000300"
        ))

        let latestRequest = await runtime.latestRequest()
        let sentMessages = await client.sentMessages()

        XCTAssertNil(latestRequest)
        XCTAssertTrue(sentMessages.isEmpty)
    }

    func testAutomationDeliveryTargetsSlackThread() async throws {
        let runtime = MockSlackRuntime()
        let client = MockSlackClient()
        let memoryStore = try MemoryStore(databasePath: temporaryDatabasePath())
        let sessionStore = CommunicationSessionStore(memoryStore: memoryStore)
        let bridge = SlackBridge(
            config: AtlasConfig(slackEnabled: true),
            client: client,
            runtime: runtime,
            sessionStore: sessionStore
        )

        await bridge.deliverAutomationResult(
            destination: CommunicationDestination(
                platform: .slack,
                channelID: "C-alerts",
                channelName: "alerts",
                threadID: "171111.000900"
            ),
            emoji: "📈",
            name: "Market Check",
            output: "AAPL is up 1.2%."
        )

        let sentMessages = await client.sentMessages()
        XCTAssertEqual(sentMessages.last?.channelID, "C-alerts")
        XCTAssertEqual(sentMessages.last?.threadID, "171111.000900")
        XCTAssertTrue(sentMessages.last?.content.contains("Market Check") == true)
    }

    private func temporaryDatabasePath() -> String {
        FileManager.default.temporaryDirectory
            .appendingPathComponent(UUID().uuidString)
            .appendingPathExtension("sqlite3")
            .path
    }
}

private actor MockSlackRuntime: AtlasRuntimeHandling {
    private(set) var lastRequest: AtlasMessageRequest?

    func handleMessage(_ request: AtlasMessageRequest) async -> AtlasMessageResponseEnvelope {
        lastRequest = request
        return AtlasMessageResponseEnvelope(
            conversation: AtlasConversation(
                id: request.conversationID ?? UUID(),
                messages: [
                    AtlasMessage(role: .user, content: request.message),
                    AtlasMessage(role: .assistant, content: "Slack bridge response")
                ]
            ),
            response: AtlasAgentResponse(
                assistantMessage: "Slack bridge response",
                status: .completed
            )
        )
    }

    func status() async -> AtlasRuntimeStatus {
        AtlasRuntimeStatus(
            isRunning: true,
            activeConversationCount: 1,
            lastMessageAt: nil,
            lastError: nil,
            state: .ready,
            runtimePort: 1984,
            startedAt: nil,
            activeRequests: 0,
            pendingApprovalCount: 0,
            details: "Ready"
        )
    }

    func approvals() async -> [ApprovalRequest] { [] }

    func approve(toolCallID: UUID) async throws -> AtlasMessageResponseEnvelope {
        throw MockSlackRuntimeError.unsupported
    }

    func deny(toolCallID: UUID) async throws -> ApprovalRequest {
        throw MockSlackRuntimeError.unsupported
    }

    func latestRequest() -> AtlasMessageRequest? {
        lastRequest
    }
}

private enum MockSlackRuntimeError: Error {
    case unsupported
}

private actor MockSlackClient: SlackClienting {
    struct SentMessage: Sendable {
        let channelID: String
        let content: String
        let threadID: String?
    }

    private var messages: [SentMessage] = []

    func authTestBot() async throws -> SlackAuthTestResponse {
        SlackAuthTestResponse(ok: true, userID: "B123", user: "atlas", team: "Atlas HQ", teamID: "T123")
    }

    func openSocketModeConnection() async throws -> SlackSocketConnection {
        SlackSocketConnection(ok: true, url: "wss://wss-primary.slack.com/link")
    }

    func postMessage(channelID: String, text: String, threadID: String?) async throws -> SlackPostedMessage {
        messages.append(SentMessage(channelID: channelID, content: text, threadID: threadID))
        return SlackPostedMessage(ok: true, channel: channelID, ts: UUID().uuidString)
    }

    nonisolated func makeSocketTask(url: URL) -> URLSessionWebSocketTask {
        URLSession.shared.webSocketTask(with: url)
    }

    func sentMessages() async -> [SentMessage] {
        messages
    }
}
