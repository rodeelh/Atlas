import XCTest
import AtlasBridges
import AtlasMemory
import AtlasNetwork
import AtlasShared
import Foundation

final class DiscordBridgeTests: XCTestCase {
    func testBridgeRoutesDirectMessageThroughRuntimeAndReplies() async throws {
        let runtime = MockDiscordRuntime()
        let client = MockDiscordClient()
        let memoryStore = try MemoryStore(databasePath: temporaryDatabasePath())
        let sessionStore = CommunicationSessionStore(memoryStore: memoryStore)
        let bridge = DiscordBridge(
            config: AtlasConfig(discordEnabled: true),
            client: client,
            runtime: runtime,
            sessionStore: sessionStore
        )

        await bridge.handle(event: DiscordMessageCreateEvent(
            id: "m1",
            channelID: "dm-1",
            guildID: nil,
            content: "Hello Atlas",
            author: DiscordMessageAuthor(id: "u1", username: "alex", globalName: "Alex", isBot: false),
            mentions: []
        ))

        let sentMessages = await client.sentMessages()
        let latestRequest = await runtime.latestRequest()

        XCTAssertEqual(latestRequest?.message, "Hello Atlas")
        XCTAssertEqual(sentMessages.last?.content, "Discord bridge response")
    }

    func testBridgeRequiresMentionInGuildChannels() async throws {
        let runtime = MockDiscordRuntime()
        let client = MockDiscordClient()
        let memoryStore = try MemoryStore(databasePath: temporaryDatabasePath())
        let sessionStore = CommunicationSessionStore(memoryStore: memoryStore)
        let bridge = DiscordBridge(
            config: AtlasConfig(discordEnabled: true),
            client: client,
            runtime: runtime,
            sessionStore: sessionStore
        )

        await bridge.handle(event: DiscordMessageCreateEvent(
            id: "m2",
            channelID: "guild-chan",
            guildID: "guild-1",
            content: "<@bot-1> status",
            author: DiscordMessageAuthor(id: "u2", username: "jamie", globalName: nil, isBot: false),
            mentions: [DiscordMessageAuthor(id: "bot-1", username: "atlas_bot", globalName: "Atlas", isBot: true)]
        ))

        let sentMessages = await client.sentMessages()
        XCTAssertTrue(sentMessages.last?.content.contains("Atlas is") == true)
    }

    func testDiscordMessageAuthorDecodesWhenBotFieldIsMissing() throws {
        let data = Data("""
        {
          "id": "u42",
          "username": "jamie",
          "global_name": "Jamie"
        }
        """.utf8)

        let author = try AtlasJSON.decoder.decode(DiscordMessageAuthor.self, from: data)

        XCTAssertEqual(author.id, "u42")
        XCTAssertEqual(author.username, "jamie")
        XCTAssertEqual(author.globalName, "Jamie")
        XCTAssertFalse(author.isBot)
    }

    func testDiscordMessageCreateEventDecodesWhenAuthorBotFieldIsMissing() throws {
        let data = Data("""
        {
          "id": "m42",
          "channel_id": "chan-1",
          "guild_id": "guild-1",
          "content": "<@bot-1> hello",
          "author": {
            "id": "u42",
            "username": "jamie",
            "global_name": "Jamie"
          },
          "mentions": [
            {
              "id": "bot-1",
              "username": "atlas_bot",
              "global_name": "Atlas",
              "bot": true
            }
          ]
        }
        """.utf8)

        let event = try AtlasJSON.decoder.decode(DiscordMessageCreateEvent.self, from: data)

        XCTAssertEqual(event.author.id, "u42")
        XCTAssertFalse(event.author.isBot)
        XCTAssertEqual(event.mentions.first?.id, "bot-1")
    }

    func testBridgeFallsBackToPlainMessageWhenReplySendFails() async throws {
        let runtime = MockDiscordRuntime()
        let client = MockDiscordClient(failReplyMessages: true)
        let memoryStore = try MemoryStore(databasePath: temporaryDatabasePath())
        let sessionStore = CommunicationSessionStore(memoryStore: memoryStore)
        let bridge = DiscordBridge(
            config: AtlasConfig(discordEnabled: true),
            client: client,
            runtime: runtime,
            sessionStore: sessionStore
        )

        await bridge.handle(event: DiscordMessageCreateEvent(
            id: "m3",
            channelID: "dm-2",
            guildID: nil,
            content: "Hello again",
            author: DiscordMessageAuthor(id: "u3", username: "sam", globalName: "Sam", isBot: false),
            mentions: []
        ))

        let sentMessages = await client.sentMessages()
        XCTAssertEqual(sentMessages.count, 1)
        XCTAssertEqual(sentMessages.last?.replyToMessageID, nil)
        XCTAssertEqual(sentMessages.last?.content, "Discord bridge response")
    }

    func testApprovalNotificationIncludesApproveAndDenyCommands() async throws {
        let runtime = MockDiscordRuntime()
        let client = MockDiscordClient()
        let memoryStore = try MemoryStore(databasePath: temporaryDatabasePath())
        let sessionStore = CommunicationSessionStore(memoryStore: memoryStore)
        let bridge = DiscordBridge(
            config: AtlasConfig(discordEnabled: true),
            client: client,
            runtime: runtime,
            sessionStore: sessionStore
        )

        let toolCall = AtlasToolCall(
            toolName: "skill__system_actions__open_app",
            argumentsJSON: "{}",
            permissionLevel: .execute,
            requiresApproval: true,
            status: .pending
        )
        let approval = ApprovalRequest(
            id: toolCall.id,
            toolCall: toolCall,
            conversationID: UUID(),
            status: .pending
        )

        await bridge.notifyApprovalRequired(
            session: ChatSession(platform: .discord, platformChatID: "chan-1", platformUserID: "u1", activeConversationID: UUID()),
            approval: approval
        )

        let sentMessages = await client.sentMessages()
        XCTAssertTrue(sentMessages.last?.content.contains("!approve \(toolCall.id.uuidString)") == true)
        XCTAssertTrue(sentMessages.last?.content.contains("!deny \(toolCall.id.uuidString)") == true)
    }

    func testAutomationDeliveryTargetsDiscordChannel() async throws {
        let runtime = MockDiscordRuntime()
        let client = MockDiscordClient()
        let memoryStore = try MemoryStore(databasePath: temporaryDatabasePath())
        let sessionStore = CommunicationSessionStore(memoryStore: memoryStore)
        let bridge = DiscordBridge(
            config: AtlasConfig(discordEnabled: true),
            client: client,
            runtime: runtime,
            sessionStore: sessionStore
        )

        await bridge.deliverAutomationResult(
            destination: CommunicationDestination(platform: .discord, channelID: "chan-77", channelName: "alerts"),
            emoji: "📈",
            name: "Market Check",
            output: "AAPL is up 1.2%."
        )

        let sentMessages = await client.sentMessages()
        XCTAssertEqual(sentMessages.last?.channelID, "chan-77")
        XCTAssertTrue(sentMessages.last?.content.contains("Market Check") == true)
    }

    func testHandlingInboundMessageUpdatesDiscordDiagnosticsState() async throws {
        let runtime = MockDiscordRuntime()
        let client = MockDiscordClient()
        let memoryStore = try MemoryStore(databasePath: temporaryDatabasePath())
        let sessionStore = CommunicationSessionStore(memoryStore: memoryStore)
        let bridge = DiscordBridge(
            config: AtlasConfig(discordEnabled: true),
            client: client,
            runtime: runtime,
            sessionStore: sessionStore
        )

        await bridge.handle(event: DiscordMessageCreateEvent(
            id: "m4",
            channelID: "dm-3",
            guildID: nil,
            content: "Ping",
            author: DiscordMessageAuthor(id: "u4", username: "lee", globalName: "Lee", isBot: false),
            mentions: []
        ))

        let status = await bridge.currentStatus()

        XCTAssertNotNil(status.lastInboundMessageAt)
    }

    private func temporaryDatabasePath() -> String {
        FileManager.default.temporaryDirectory
            .appendingPathComponent(UUID().uuidString)
            .appendingPathExtension("sqlite3")
            .path
    }
}

private actor MockDiscordRuntime: AtlasRuntimeHandling {
    private(set) var lastRequest: AtlasMessageRequest?

    func handleMessage(_ request: AtlasMessageRequest) async -> AtlasMessageResponseEnvelope {
        lastRequest = request
        return AtlasMessageResponseEnvelope(
            conversation: AtlasConversation(
                id: request.conversationID ?? UUID(),
                messages: [
                    AtlasMessage(role: .user, content: request.message),
                    AtlasMessage(role: .assistant, content: "Discord bridge response")
                ]
            ),
            response: AtlasAgentResponse(
                assistantMessage: "Discord bridge response",
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
        throw MockDiscordRuntimeError.unsupported
    }

    func deny(toolCallID: UUID) async throws -> ApprovalRequest {
        throw MockDiscordRuntimeError.unsupported
    }

    func latestRequest() -> AtlasMessageRequest? {
        lastRequest
    }
}

private enum MockDiscordRuntimeError: Error {
    case unsupported
}

private actor MockDiscordClient: DiscordClienting {
    struct SentMessage: Sendable {
        let channelID: String
        let content: String
        let replyToMessageID: String?
    }

    private var currentUser = DiscordCurrentUser(id: "bot-1", username: "atlas_bot", globalName: "Atlas")
    private var messages: [SentMessage] = []
    private let failReplyMessages: Bool

    init(failReplyMessages: Bool = false) {
        self.failReplyMessages = failReplyMessages
    }

    func getCurrentUser() async throws -> DiscordCurrentUser {
        return currentUser
    }

    func getGatewayBot() async throws -> DiscordGatewayBot {
        DiscordGatewayBot(url: "wss://gateway.discord.gg", shards: 1)
    }

    func createMessage(channelID: String, content: String, replyToMessageID: String?) async throws -> DiscordCreatedMessage {
        if failReplyMessages, replyToMessageID != nil {
            throw DiscordClientError.unexpectedStatusCode(400, "{\"message\":\"Unknown message\"}")
        }
        messages.append(SentMessage(channelID: channelID, content: content, replyToMessageID: replyToMessageID))
        return DiscordCreatedMessage(id: UUID().uuidString, channelID: channelID, content: content)
    }

    nonisolated func makeGatewayTask(url: URL) -> URLSessionWebSocketTask {
        URLSession.shared.webSocketTask(with: url)
    }

    func sentMessages() async -> [SentMessage] {
        return messages
    }
}
