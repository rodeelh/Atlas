import Foundation
import XCTest
import AtlasMemory
import AtlasShared
import AtlasSkills
@testable import AtlasApp

final class AtlasRuntimeIntegrationTests: XCTestCase {

    /// Integration tests that start the embedded daemon require launchd and a signed binary.
    /// Set ATLAS_INTEGRATION_TESTS=1 in the scheme environment to opt in.
    private func requiresDaemon() throws {
        try XCTSkipUnless(
            ProcessInfo.processInfo.environment["ATLAS_INTEGRATION_TESTS"] == "1",
            "Daemon integration test — set ATLAS_INTEGRATION_TESTS=1 in the scheme environment to run"
        )
    }

    @MainActor
    func testOnboardingSeedsPersistentMemoriesAndUpdatesConfig() async throws {
        try requiresDaemon()
        let port = Int.random(in: 19000...19999)
        let sandboxURL = FileManager.default.temporaryDirectory
            .appendingPathComponent("AtlasOnboardingTests-\(UUID().uuidString)", isDirectory: true)
        try FileManager.default.createDirectory(at: sandboxURL, withIntermediateDirectories: true)
        let memoryDatabaseURL = sandboxURL.appendingPathComponent("atlas-onboarding.sqlite3", isDirectory: false)

        let config = AtlasConfig(
            runtimePort: port,
            openAIServiceName: "com.projectatlas.tests.openai.\(UUID().uuidString)",
            openAIAccountName: "missing",
            telegramServiceName: "com.projectatlas.tests.telegram.\(UUID().uuidString)",
            telegramAccountName: "missing",
            toolSandboxDirectory: sandboxURL.path,
            memoryDatabasePath: memoryDatabaseURL.path,
            personaName: "Atlas",
            onboardingCompleted: false,
            actionSafetyMode: .askOnlyForRiskyActions
        )

        let appState = AtlasAppState(config: config)
        let runtimeManager = AtlasRuntimeManager(config: config)
        let client = AtlasAPIClient(config: config)

        await appState.bootstrap()

        do {
            try await appState.completeOnboarding(
                userName: "Rami",
                assistantName: "Atlas Prime",
                location: "Orlando, United States",
                inferredTemperatureUnit: "Fahrenheit",
                inferredTimeFormat: "12-hour time",
                inferredDateFormat: "Month/Day/Year",
                actionSafetyMode: AtlasActionSafetyMode.alwaysAskBeforeActions
            )

            XCTAssertTrue(appState.onboardingCompleted)
            XCTAssertEqual(appState.assistantName, "Atlas Prime")
            XCTAssertEqual(appState.preferredDisplayName, "Rami")
            XCTAssertEqual(appState.preferredLocation, "Orlando, United States")
            XCTAssertEqual(appState.actionSafetyMode, AtlasActionSafetyMode.alwaysAskBeforeActions)

            let memories = try await client.fetchMemories()
            XCTAssertTrue(memories.contains(where: { $0.title == "Preferred display name" && $0.content == "Rami" && $0.isUserConfirmed }))
            XCTAssertTrue(memories.contains(where: { $0.title == "Preferred Atlas name" && $0.content == "Atlas Prime" && $0.isUserConfirmed }))
            XCTAssertTrue(memories.contains(where: { $0.title == "Preferred location" && $0.content == "Orlando, United States" && $0.isUserConfirmed }))
            XCTAssertTrue(memories.contains(where: {
                $0.title == "Preferred temperature unit"
                    && !$0.isUserConfirmed
                    && $0.content.localizedCaseInsensitiveContains("Fahrenheit")
            }))
            XCTAssertTrue(memories.contains(where: { $0.title == "Action safety preference" && $0.content == "Always ask before actions" && $0.isUserConfirmed }))

            let persistedSnapshot = await AtlasConfigStore.shared.load()
            XCTAssertEqual(persistedSnapshot.personaName, "Atlas Prime")
            XCTAssertEqual(persistedSnapshot.actionSafetyMode, AtlasActionSafetyMode.alwaysAskBeforeActions.rawValue)
            XCTAssertEqual(persistedSnapshot.onboardingCompleted, true)

            try await appState.saveIdentityPreferences(
                userName: "Rami",
                assistantName: "Atlas Prime",
                location: "London, United Kingdom",
                inferredTemperatureUnit: "Celsius",
                inferredTimeFormat: "24-hour time",
                inferredDateFormat: "Day/Month/Year",
                markOnboardingCompleted: true
            )

            let updatedMemories = try await client.fetchMemories()
            XCTAssertEqual(updatedMemories.filter { $0.title == "Preferred location" }.count, 1)
            XCTAssertEqual(updatedMemories.first(where: { $0.title == "Preferred location" })?.content, "London, United Kingdom")
            XCTAssertEqual(updatedMemories.filter { $0.title == "Preferred temperature unit" }.count, 1)
            XCTAssertEqual(updatedMemories.first(where: { $0.title == "Preferred temperature unit" })?.content, "Celsius")

            let relaunchedState = AtlasAppState(config: AtlasConfig(
                runtimePort: port,
                openAIServiceName: config.openAIServiceName,
                openAIAccountName: config.openAIAccountName,
                telegramServiceName: config.telegramServiceName,
                telegramAccountName: config.telegramAccountName,
                toolSandboxDirectory: sandboxURL.path,
                memoryDatabasePath: memoryDatabaseURL.path
            ))
            XCTAssertTrue(relaunchedState.onboardingCompleted)
            XCTAssertEqual(relaunchedState.assistantName, "Atlas Prime")
            await relaunchedState.refreshMemories(force: true)
            XCTAssertEqual(relaunchedState.preferredLocation, "London, United Kingdom")
            XCTAssertEqual(relaunchedState.preferredTemperatureUnit, "Celsius")
        } catch {
            try? await runtimeManager.stopEmbeddedRuntime()
            throw error
        }

        try await runtimeManager.stopEmbeddedRuntime()
    }

    func testAtlasAPIClientCanReachRuntimeStatusMessageLogsAndSkills() async throws {
        try requiresDaemon()
        let port = Int.random(in: 18080...18999)
        let sandboxURL = FileManager.default.temporaryDirectory
            .appendingPathComponent("AtlasAppTests-\(UUID().uuidString)", isDirectory: true)
        try FileManager.default.createDirectory(at: sandboxURL, withIntermediateDirectories: true)
        let memoryDatabaseURL = sandboxURL.appendingPathComponent("atlas-tests.sqlite3", isDirectory: false)
        let seededStore = try MemoryStore(databasePath: memoryDatabaseURL.path)
        let seededMemory = try await seededStore.saveMemory(
            MemoryItem(
                category: .profile,
                title: "Preferred location",
                content: "User is based in Boston.",
                source: .userExplicit,
                confidence: 0.99,
                importance: 0.95,
                isUserConfirmed: false,
                tags: ["location", "weather"]
            )
        )

        let config = AtlasConfig(
            runtimePort: port,
            openAIServiceName: "com.projectatlas.tests.openai.\(UUID().uuidString)",
            openAIAccountName: "missing",
            toolSandboxDirectory: sandboxURL.path,
            memoryDatabasePath: memoryDatabaseURL.path
        )

        let runtimeManager = AtlasRuntimeManager(config: config)
        let client = AtlasAPIClient(config: config)

        try await runtimeManager.startEmbeddedRuntime()

        do {
            let status = try await client.fetchStatus()
            XCTAssertTrue(status.isRunning)
            XCTAssertEqual(status.runtimePort, port)

            let initialSkills = try await client.fetchSkills()
            XCTAssertEqual(initialSkills.count, 13)
            // Core info + time
            XCTAssertTrue(initialSkills.contains(where: { $0.manifest.id == "atlas.info" }))
            XCTAssertTrue(initialSkills.contains(where: { $0.manifest.id == "info" }))
            // AI capabilities
            XCTAssertTrue(initialSkills.contains(where: { $0.manifest.id == "vision" }))
            XCTAssertTrue(initialSkills.contains(where: { $0.manifest.id == "image-generation" }))
            // Research
            XCTAssertTrue(initialSkills.contains(where: { $0.manifest.id == "weather" }))
            XCTAssertTrue(initialSkills.contains(where: { $0.manifest.id == "web-research" }))
            XCTAssertTrue(initialSkills.contains(where: { $0.manifest.id == "websearch-api" }))
            XCTAssertTrue(initialSkills.contains(where: { $0.manifest.id == "finance" }))
            // System access
            XCTAssertTrue(initialSkills.contains(where: { $0.manifest.id == "file-system" }))
            XCTAssertTrue(initialSkills.contains(where: { $0.manifest.id == "system-actions" }))
            XCTAssertTrue(initialSkills.contains(where: { $0.manifest.id == "applescript-automation" }))
            // Platform
            XCTAssertTrue(initialSkills.contains(where: { $0.manifest.id == "forge.orchestration" }))
            XCTAssertTrue(initialSkills.contains(where: { $0.manifest.id == "gremlin-management" }))

            let initialRoots = try await client.fetchFileAccessRoots()
            XCTAssertTrue(initialRoots.contains(where: { $0.displayName == "Telegram Attachments" }))
            XCTAssertTrue(initialRoots.contains(where: { $0.displayName == "Image Artifacts" }))

            let initialMemories = try await client.fetchMemories()
            XCTAssertEqual(initialMemories.count, 1)
            XCTAssertEqual(initialMemories.first?.id, seededMemory.id)

            let updatedMemory = try await client.updateMemory(
                id: seededMemory.id,
                request: AtlasMemoryUpdateRequest(
                    title: "Preferred location",
                    content: "User is based in Orlando.",
                    markAsConfirmed: true
                )
            )
            XCTAssertEqual(updatedMemory.content, "User is based in Orlando.")
            XCTAssertTrue(updatedMemory.isUserConfirmed)

            let fetchedMemory = try await client.fetchMemory(id: seededMemory.id)
            XCTAssertEqual(fetchedMemory.content, "User is based in Orlando.")

            let searchedMemories = try await client.searchMemories(query: "Orlando")
            XCTAssertEqual(searchedMemories.count, 1)
            XCTAssertEqual(searchedMemories.first?.id, seededMemory.id)

            let confirmedMemory = try await client.confirmMemory(id: seededMemory.id)
            XCTAssertTrue(confirmedMemory.isUserConfirmed)

            let bookmarkData = try MacOSBookmarkGrantAdapter().createGrant(for: sandboxURL)
            let addedRoot = try await client.addFileAccessRoot(bookmarkData: bookmarkData)
            XCTAssertEqual(addedRoot.path, sandboxURL.path)

            let refreshedRoots = try await client.fetchFileAccessRoots()
            XCTAssertEqual(refreshedRoots.count, 3)
            XCTAssertTrue(refreshedRoots.contains(where: { $0.path == sandboxURL.path }))

            let removedRoot = try await client.removeFileAccessRoot(id: addedRoot.id)
            XCTAssertEqual(removedRoot.id, addedRoot.id)
            let rootsAfterRemoval = try await client.fetchFileAccessRoots()
            XCTAssertEqual(rootsAfterRemoval.count, 2)
            XCTAssertTrue(rootsAfterRemoval.contains(where: { $0.displayName == "Telegram Attachments" }))
            XCTAssertTrue(rootsAfterRemoval.contains(where: { $0.displayName == "Image Artifacts" }))

            let disabledSkill = try await client.disableSkill(id: "atlas.info")
            XCTAssertEqual(disabledSkill.manifest.lifecycleState.rawValue, "disabled")

            let validatedSkill = try await client.validateSkill(id: "atlas.info")
            XCTAssertEqual(validatedSkill.manifest.lifecycleState.rawValue, "disabled")
            XCTAssertEqual(validatedSkill.validation?.status.rawValue, "passed")

            let enabledSkill = try await client.enableSkill(id: "atlas.info")
            XCTAssertEqual(enabledSkill.manifest.lifecycleState.rawValue, "enabled")

            let firstEnvelope = try await client.sendMessage(
                conversationID: nil,
                message: "List the sandbox root directory."
            )

            XCTAssertEqual(firstEnvelope.response.status, .failed)
            XCTAssertEqual(firstEnvelope.conversation.messages.count, 2)
            XCTAssertEqual(firstEnvelope.conversation.messages.first?.role, .user)
            XCTAssertEqual(firstEnvelope.conversation.messages.last?.role, .assistant)
            XCTAssertTrue(firstEnvelope.response.errorMessage?.contains("OpenAI API key") == true)

            let secondEnvelope = try await client.sendMessage(
                conversationID: firstEnvelope.conversation.id,
                message: "Try again."
            )

            XCTAssertEqual(secondEnvelope.conversation.id, firstEnvelope.conversation.id)
            XCTAssertEqual(secondEnvelope.conversation.messages.count, 4)

            let logs = try await client.fetchLogs()
            XCTAssertFalse(logs.isEmpty)
            XCTAssertTrue(logs.contains(where: { $0.message == "Enabled Atlas skill" }))
            XCTAssertTrue(logs.contains(where: { $0.message == "Disabled Atlas skill" }))
            XCTAssertTrue(logs.contains(where: { $0.message == "Validated Atlas skill" }))

            let approvals = try await client.fetchPendingApprovals()
            XCTAssertTrue(approvals.isEmpty)

            let deletedMemory = try await client.deleteMemory(id: seededMemory.id)
            XCTAssertEqual(deletedMemory.id, seededMemory.id)
            let finalMemories = try await client.fetchMemories()
            XCTAssertTrue(finalMemories.isEmpty)
        } catch {
            try? await runtimeManager.stopEmbeddedRuntime()
            throw error
        }

        try await runtimeManager.stopEmbeddedRuntime()
    }
}
