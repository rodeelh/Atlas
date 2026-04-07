import XCTest
import AtlasMemory
import AtlasShared

final class TelegramSessionStoreTests: XCTestCase {
    func testResolveSessionCreatesAndPersistsConversationMapping() async throws {
        let memoryStore = try MemoryStore(databasePath: temporaryDatabasePath())
        let store = TelegramSessionStore(memoryStore: memoryStore)

        let session = try await store.resolveSession(chatID: 12345, userID: 777, lastMessageID: 10)
        let fetched = try await store.session(forChatID: 12345)

        XCTAssertEqual(session.chatID, 12345)
        XCTAssertEqual(session.userID, 777)
        XCTAssertEqual(session.lastTelegramMessageID, 10)
        XCTAssertEqual(fetched?.activeConversationID, session.activeConversationID)
    }

    func testRotateConversationPreservesChatAndReplacesConversation() async throws {
        let memoryStore = try MemoryStore(databasePath: temporaryDatabasePath())
        let store = TelegramSessionStore(memoryStore: memoryStore)

        let first = try await store.resolveSession(chatID: 9, userID: 1, lastMessageID: 2)
        let rotated = try await store.rotateConversation(chatID: 9, userID: 1, lastMessageID: 3)

        XCTAssertEqual(rotated.chatID, first.chatID)
        XCTAssertEqual(rotated.userID, first.userID)
        XCTAssertNotEqual(rotated.activeConversationID, first.activeConversationID)
        XCTAssertEqual(rotated.lastTelegramMessageID, 3)
    }

    func testTelegramSessionStorePersistsIntoCommunicationChannels() async throws {
        let memoryStore = try MemoryStore(databasePath: temporaryDatabasePath())
        let store = TelegramSessionStore(memoryStore: memoryStore)

        _ = try await store.resolveSession(chatID: 222, userID: 333, lastMessageID: 444)
        let channels = try await memoryStore.fetchCommunicationChannels(platform: .telegram)

        XCTAssertEqual(channels.count, 1)
        XCTAssertEqual(channels.first?.platform, .telegram)
        XCTAssertEqual(channels.first?.channelID, "222")
        XCTAssertEqual(channels.first?.userID, "333")
        XCTAssertEqual(channels.first?.lastMessageID, "444")
    }

    func testCommunicationSessionStoreSupportsNonTelegramPlatforms() async throws {
        let memoryStore = try MemoryStore(databasePath: temporaryDatabasePath())
        let store = CommunicationSessionStore(memoryStore: memoryStore)

        let session = try await store.resolveSession(
            platform: .discord,
            chatID: "channel-42",
            userID: "user-7",
            channelName: "ops-room",
            lastMessageID: "m-9",
            platformContext: "Discord persona"
        )
        let fetched = try await store.channel(platform: .discord, channelID: "channel-42")

        XCTAssertEqual(session.platform, .discord)
        XCTAssertEqual(session.platformChatID, "channel-42")
        XCTAssertEqual(session.platformUserID, "user-7")
        XCTAssertEqual(fetched?.channelName, "ops-room")
        XCTAssertEqual(fetched?.lastMessageID, "m-9")
        XCTAssertEqual(fetched?.activeConversationID, session.activeConversationID)
    }

    func testCommunicationSessionStoreRotatePreservesChannelIdentity() async throws {
        let memoryStore = try MemoryStore(databasePath: temporaryDatabasePath())
        let store = CommunicationSessionStore(memoryStore: memoryStore)

        let first = try await store.resolveSession(
            platform: .discord,
            chatID: "channel-11",
            userID: "user-2",
            channelName: "general",
            lastMessageID: "m-1"
        )
        let rotated = try await store.rotateConversation(
            platform: .discord,
            chatID: "channel-11",
            userID: "user-2",
            channelName: "general",
            lastMessageID: "m-2"
        )

        XCTAssertEqual(rotated.platform, .discord)
        XCTAssertEqual(rotated.platformChatID, first.platformChatID)
        XCTAssertNotEqual(rotated.activeConversationID, first.activeConversationID)

        let fetched = try await store.channel(platform: .discord, channelID: "channel-11")
        XCTAssertEqual(fetched?.lastMessageID, "m-2")
        XCTAssertEqual(fetched?.activeConversationID, rotated.activeConversationID)
    }

    func testCommunicationSessionStoreSeparatesSlackThreadsWithinOneChannel() async throws {
        let memoryStore = try MemoryStore(databasePath: temporaryDatabasePath())
        let store = CommunicationSessionStore(memoryStore: memoryStore)

        let first = try await store.resolveSession(
            platform: .slack,
            chatID: "C123",
            userID: "U1",
            threadID: "171111.000100",
            channelName: "product",
            lastMessageID: "171111.000100"
        )
        let second = try await store.resolveSession(
            platform: .slack,
            chatID: "C123",
            userID: "U2",
            threadID: "171111.000200",
            channelName: "product",
            lastMessageID: "171111.000200"
        )

        XCTAssertNotEqual(first.activeConversationID, second.activeConversationID)

        let firstChannel = try await store.channel(platform: .slack, channelID: "C123", threadID: "171111.000100")
        let secondChannel = try await store.channel(platform: .slack, channelID: "C123", threadID: "171111.000200")

        XCTAssertEqual(firstChannel?.threadID, "171111.000100")
        XCTAssertEqual(secondChannel?.threadID, "171111.000200")
    }

    private func temporaryDatabasePath() -> String {
        FileManager.default.temporaryDirectory
            .appendingPathComponent(UUID().uuidString)
            .appendingPathExtension("sqlite3")
            .path
    }
}
