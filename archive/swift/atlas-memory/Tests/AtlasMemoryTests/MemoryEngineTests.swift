import XCTest
import AtlasMemory
import AtlasShared

final class MemoryEngineTests: XCTestCase {
    func testRetrieverReturnsSmallRelevantSet() async throws {
        let store = try MemoryStore(databasePath: temporaryDatabasePath())
        let retriever = MemoryRetriever(
            memoryStore: store,
            config: AtlasConfig(maxRetrievedMemoriesPerTurn: 3)
        )

        let relevantProject = MemoryItem(
            category: .project,
            title: "Atlas project context",
            content: "Atlas is an active macOS-first AI operator project under ongoing development.",
            source: .conversationInference,
            confidence: 0.9,
            importance: 0.92,
            tags: ["atlas", "project", "memory"]
        )
        let relevantPreference = MemoryItem(
            category: .preference,
            title: "Approval visibility",
            content: "User wants approval-requiring actions surfaced clearly before Atlas continues.",
            source: .conversationInference,
            confidence: 0.88,
            importance: 0.9,
            tags: ["approvals", "safety"]
        )
        let unrelatedProfile = MemoryItem(
            category: .profile,
            title: "Casual interest",
            content: "User enjoys weekend hiking trips.",
            source: .conversationInference,
            confidence: 0.9,
            importance: 0.95,
            tags: ["hiking"]
        )

        _ = try await store.saveMemory(relevantProject)
        _ = try await store.saveMemory(relevantPreference)
        _ = try await store.saveMemory(unrelatedProfile)

        let results = try await retriever.retrieveRelevantMemories(
            for: "Refine the Atlas memory flow and keep approval handling clear.",
            conversationID: nil
        )

        XCTAssertLessThanOrEqual(results.count, 3)
        XCTAssertTrue(results.contains(where: { $0.id == relevantProject.id }))
        XCTAssertTrue(results.contains(where: { $0.id == relevantPreference.id }))
        XCTAssertFalse(results.contains(where: { $0.id == unrelatedProfile.id }))
    }

    func testExtractionIgnoresNoisyStatusQuestion() async throws {
        let store = try MemoryStore(databasePath: temporaryDatabasePath())
        let engine = MemoryExtractionEngine(
            memoryStore: store,
            config: AtlasConfig()
        )

        let candidates = engine.extractCandidates(
            from: MemoryTurnRecord(
                conversationID: UUID(),
                userMessage: AtlasMessage(role: .user, content: "Is Telegram connected right now?"),
                assistantMessage: AtlasMessage(role: .assistant, content: "Telegram is connected."),
                toolCalls: [],
                toolResults: [],
                responseStatus: .completed
            )
        )

        XCTAssertTrue(candidates.isEmpty)
    }

    func testExplicitPreferenceMemoryPersists() async throws {
        let store = try MemoryStore(databasePath: temporaryDatabasePath())
        let engine = MemoryExtractionEngine(
            memoryStore: store,
            config: AtlasConfig()
        )

        let stored = try await engine.extractAndPersist(
            from: MemoryTurnRecord(
                conversationID: UUID(),
                userMessage: AtlasMessage(role: .user, content: "Remember that I prefer concise operational responses."),
                assistantMessage: AtlasMessage(role: .assistant, content: "I’ll keep that in mind."),
                toolCalls: [],
                toolResults: [],
                responseStatus: .completed
            )
        )

        XCTAssertEqual(stored.count, 1)
        XCTAssertEqual(stored.first?.category, .preference)
        XCTAssertTrue(stored.first?.isUserConfirmed == true)
    }

    func testDurableMemoriesPersistAcrossStoreReopen() async throws {
        let path = temporaryDatabasePath()
        let firstStore = try MemoryStore(databasePath: path)
        let engine = MemoryExtractionEngine(
            memoryStore: firstStore,
            config: AtlasConfig()
        )

        _ = try await engine.extractAndPersist(
            from: MemoryTurnRecord(
                conversationID: UUID(),
                userMessage: AtlasMessage(role: .user, content: "Remember that I live in Boston."),
                assistantMessage: AtlasMessage(role: .assistant, content: "I’ll remember that."),
                toolCalls: [],
                toolResults: [],
                responseStatus: .completed
            )
        )

        _ = try await engine.extractAndPersist(
            from: MemoryTurnRecord(
                conversationID: UUID(),
                userMessage: AtlasMessage(role: .user, content: "Remember that I prefer fahrenheit for weather."),
                assistantMessage: AtlasMessage(role: .assistant, content: "I’ll use that preference."),
                toolCalls: [],
                toolResults: [],
                responseStatus: .completed
            )
        )

        let initialCount = try await firstStore.memoryCount()
        XCTAssertEqual(initialCount, 2)
        XCTAssertTrue(FileManager.default.fileExists(atPath: path))

        let reopenedStore = try MemoryStore(databasePath: path)
        let reopenedCount = try await reopenedStore.memoryCount()
        XCTAssertEqual(reopenedCount, 2)

        let persisted = try await reopenedStore.listMemories(limit: 10)
        XCTAssertTrue(persisted.contains(where: { $0.title == "Preferred location" && $0.category == .profile }))
        XCTAssertTrue(persisted.contains(where: { $0.title == "Preferred temperature unit" && $0.category == .preference }))
    }

    func testWeatherQueryRetrievesPersistedLocationAndUnitPreferencesAfterReopen() async throws {
        let path = temporaryDatabasePath()
        let firstStore = try MemoryStore(databasePath: path)

        _ = try await firstStore.saveMemory(
            MemoryItem(
                category: .profile,
                title: "Preferred location",
                content: "User is based in Boston.",
                source: .userExplicit,
                confidence: 0.99,
                importance: 0.95,
                isUserConfirmed: true,
                tags: ["location", "weather"]
            )
        )
        _ = try await firstStore.saveMemory(
            MemoryItem(
                category: .preference,
                title: "Preferred temperature unit",
                content: "User prefers fahrenheit for weather-related temperatures.",
                source: .userExplicit,
                confidence: 0.99,
                importance: 0.94,
                isUserConfirmed: true,
                tags: ["weather", "temperature", "unit", "fahrenheit"]
            )
        )

        let reopenedStore = try MemoryStore(databasePath: path)
        let retriever = MemoryRetriever(
            memoryStore: reopenedStore,
            config: AtlasConfig(maxRetrievedMemoriesPerTurn: 4)
        )

        let results = try await retriever.retrieveRelevantMemories(
            for: "What's the weather tomorrow?",
            conversationID: nil
        )

        XCTAssertTrue(results.contains(where: { $0.title == "Preferred location" }))
        XCTAssertTrue(results.contains(where: { $0.title == "Preferred temperature unit" }))
    }

    func testStableFallbackReturnsMultipleDurablePreferencesForAmbiguousPrompt() async throws {
        let store = try MemoryStore(databasePath: temporaryDatabasePath())
        let retriever = MemoryRetriever(
            memoryStore: store,
            config: AtlasConfig(maxRetrievedMemoriesPerTurn: 4)
        )

        _ = try await store.saveMemory(
            MemoryItem(
                category: .profile,
                title: "Preferred display name",
                content: "User prefers to be called Rami.",
                source: .userExplicit,
                confidence: 0.99,
                importance: 0.95,
                isUserConfirmed: true,
                tags: ["identity", "name"]
            )
        )
        _ = try await store.saveMemory(
            MemoryItem(
                category: .preference,
                title: "Response style",
                content: "User prefers concise operational responses.",
                source: .userExplicit,
                confidence: 0.98,
                importance: 0.93,
                isUserConfirmed: true,
                tags: ["communication", "concise", "responses"]
            )
        )

        let results = try await retriever.retrieveRelevantMemories(
            for: "What should we do next?",
            conversationID: nil
        )

        XCTAssertGreaterThanOrEqual(results.count, 2)
        XCTAssertTrue(results.contains(where: { $0.title == "Preferred display name" }))
        XCTAssertTrue(results.contains(where: { $0.title == "Response style" }))
    }

    func testNaturalLanguagePreferencePhrasesPersistDurableMemory() async throws {
        let store = try MemoryStore(databasePath: temporaryDatabasePath())
        let engine = MemoryExtractionEngine(
            memoryStore: store,
            config: AtlasConfig()
        )

        _ = try await engine.extractAndPersist(
            from: MemoryTurnRecord(
                conversationID: UUID(),
                userMessage: AtlasMessage(
                    role: .user,
                    content: "i want you to go by Joe from now on. I also live in orlando!"
                ),
                assistantMessage: AtlasMessage(
                    role: .assistant,
                    content: "Got it. I’ll go by Joe from now on."
                ),
                toolCalls: [],
                toolResults: [],
                responseStatus: .completed
            )
        )

        _ = try await engine.extractAndPersist(
            from: MemoryTurnRecord(
                conversationID: UUID(),
                userMessage: AtlasMessage(role: .user, content: "i only want weather in F moving forward"),
                assistantMessage: AtlasMessage(role: .assistant, content: "I’ll use Fahrenheit from now on."),
                toolCalls: [],
                toolResults: [],
                responseStatus: .completed
            )
        )

        let stored = try await store.listMemories(limit: 10)
        XCTAssertTrue(stored.contains(where: { $0.title == "Preferred Atlas name" && $0.content.contains("Joe") }))
        XCTAssertTrue(stored.contains(where: { $0.title == "Preferred location" && $0.content.contains("Orlando") }))
        XCTAssertTrue(stored.contains(where: { $0.title == "Preferred temperature unit" && $0.content.contains("fahrenheit") }))
    }

    func testRecoveryBackfillsDurableMemoriesFromConversationHistory() async throws {
        let store = try MemoryStore(databasePath: temporaryDatabasePath())
        let conversationID = UUID()
        let created = try await store.createConversation(id: conversationID, createdAt: .now)
        let engine = MemoryExtractionEngine(
            memoryStore: store,
            config: AtlasConfig()
        )

        _ = try await store.appendMessage(
            AtlasMessage(role: .user, content: "My name is Rami btw"),
            to: created.id
        )
        _ = try await store.appendMessage(
            AtlasMessage(role: .assistant, content: "Nice to meet you, Rami."),
            to: created.id
        )
        _ = try await store.appendMessage(
            AtlasMessage(role: .user, content: "oh! im in orlando"),
            to: created.id
        )
        _ = try await store.appendMessage(
            AtlasMessage(role: .assistant, content: "Got it, I’ll use Orlando."),
            to: created.id
        )
        let conversation = try await store.appendMessage(
            AtlasMessage(role: .user, content: "always use F"),
            to: created.id
        )
        let finalConversation = try await store.appendMessage(
            AtlasMessage(role: .assistant, content: "I’ll use Fahrenheit from now on."),
            to: conversation.id
        )

        let recovered = try await engine.recoverDurableMemories(from: [finalConversation], limit: 20)

        XCTAssertTrue(recovered.contains(where: { $0.title == "Preferred display name" && $0.content.contains("Rami") }))
        XCTAssertTrue(recovered.contains(where: { $0.title == "Preferred location" && $0.content.contains("Orlando") }))
        XCTAssertTrue(recovered.contains(where: { $0.title == "Preferred temperature unit" && $0.content.contains("fahrenheit") }))
    }

    func testNormalizeStoredDurableMemoriesRepairsLegacyFormatting() async throws {
        let store = try MemoryStore(databasePath: temporaryDatabasePath())
        let engine = MemoryExtractionEngine(
            memoryStore: store,
            config: AtlasConfig()
        )

        let legacyDisplayName = try await store.saveMemory(
            MemoryItem(
                category: .profile,
                title: "Preferred display name",
                content: "User prefers to be called Rami btw.",
                source: .userExplicit,
                confidence: 0.95,
                importance: 0.9,
                isUserConfirmed: true,
                tags: ["identity", "name"]
            )
        )

        let legacyLocation = try await store.saveMemory(
            MemoryItem(
                category: .profile,
                title: "Preferred location",
                content: "User is based in orlando.",
                source: .userExplicit,
                confidence: 0.95,
                importance: 0.9,
                isUserConfirmed: true,
                tags: ["location", "weather"]
            )
        )

        let normalized = try await engine.normalizeStoredDurableMemories(limit: 10)
        XCTAssertEqual(normalized.count, 2)

        let fetchedDisplayName = try await store.fetchMemory(id: legacyDisplayName.id)
        let displayName = try XCTUnwrap(fetchedDisplayName)
        XCTAssertEqual(displayName.content, "User prefers to be called Rami.")

        let fetchedLocation = try await store.fetchMemory(id: legacyLocation.id)
        let location = try XCTUnwrap(fetchedLocation)
        XCTAssertEqual(location.content, "User is based in Orlando.")
    }

    func testDeletedMemoryNoLongerAppearsInRetrieval() async throws {
        let store = try MemoryStore(databasePath: temporaryDatabasePath())
        let retriever = MemoryRetriever(
            memoryStore: store,
            config: AtlasConfig(maxRetrievedMemoriesPerTurn: 4)
        )

        let memory = MemoryItem(
            category: .profile,
            title: "Preferred location",
            content: "User is based in Boston.",
            source: .userExplicit,
            confidence: 0.99,
            importance: 0.95,
            isUserConfirmed: true,
            tags: ["location", "weather"]
        )

        _ = try await store.saveMemory(memory)
        try await store.deleteMemory(id: memory.id)

        let results = try await retriever.retrieveRelevantMemories(
            for: "What's the weather tomorrow?",
            conversationID: nil
        )

        XCTAssertFalse(results.contains(where: { $0.id == memory.id }))
    }

    private func temporaryDatabasePath() -> String {
        FileManager.default.temporaryDirectory
            .appendingPathComponent(UUID().uuidString)
            .appendingPathExtension("sqlite3")
            .path
    }
}
