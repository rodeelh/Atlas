import XCTest
import AtlasCore
import AtlasMemory
import AtlasShared

final class MemoryCenterRuntimeTests: XCTestCase {
    func testRuntimeCanListUpdateConfirmAndDeleteMemories() async throws {
        let memoryStore = try MemoryStore(databasePath: temporaryDatabasePath())
        let original = MemoryItem(
            category: .preference,
            title: "Preferred response style",
            content: "User prefers concise operational responses.",
            source: .conversationInference,
            confidence: 0.84,
            importance: 0.88,
            isUserConfirmed: false,
            tags: ["communication", "concise"]
        )

        _ = try await memoryStore.saveMemory(original)

        let context = try AgentContext(
            config: AtlasConfig(memoryDatabasePath: await memoryStore.databasePath),
            memoryStore: memoryStore
        )
        let runtime = try AgentRuntime(context: context)

        let listed = await runtime.memories()
        XCTAssertEqual(listed.count, 1)
        XCTAssertEqual(listed.first?.id, original.id)

        let updated = try await runtime.updateMemory(
            id: original.id,
            request: AtlasMemoryUpdateRequest(
                title: "Preferred response style",
                content: "User prefers concise status-first responses.",
                markAsConfirmed: true
            )
        )
        XCTAssertEqual(updated.content, "User prefers concise status-first responses.")
        XCTAssertTrue(updated.isUserConfirmed)

        let confirmed = try await runtime.confirmMemory(id: original.id)
        XCTAssertTrue(confirmed.isUserConfirmed)

        let deleted = try await runtime.deleteMemory(id: original.id)
        XCTAssertEqual(deleted.id, original.id)
        let remaining = await runtime.memories()
        XCTAssertTrue(remaining.isEmpty)
        let fetched = await runtime.memory(id: original.id)
        XCTAssertNil(fetched)
    }

    private func temporaryDatabasePath() -> String {
        FileManager.default.temporaryDirectory
            .appendingPathComponent("AtlasCoreMemoryTests-\(UUID().uuidString).sqlite3")
            .path
    }
}
