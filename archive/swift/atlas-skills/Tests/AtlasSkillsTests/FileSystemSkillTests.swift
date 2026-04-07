import Foundation
import XCTest
import AtlasGuard
import AtlasLogging
import AtlasShared
@testable import AtlasSkills

final class FileSystemSkillTests: XCTestCase {
    func testFileSystemSkillListsReadsSearchesAndFetchesMetadataWithinApprovedRoot() async throws {
        let workspace = try makeWorkspace()
        let scopeStore = FileAccessScopeStore(
            defaults: UserDefaults(suiteName: "FileSystemSkillTests.\(UUID().uuidString)")!,
            storageKey: "AtlasFileAccessRoots.\(UUID().uuidString)"
        )
        let bookmark = try FileAccessScopeStore.makeBookmarkData(for: workspace.rootURL)
        _ = try await scopeStore.addRoot(bookmarkData: bookmark)

        let skill = FileSystemSkill(scopeStore: scopeStore)
        let context = makeContext()

        let listResult = try await skill.execute(
            actionID: "fs.list_directory",
            input: AtlasToolInput(argumentsJSON: #"{"path":"."}"#),
            context: context
        )
        let listOutput = try decode(listResult.output, as: FileSystemListDirectoryOutput.self)
        XCTAssertTrue(listOutput.entries.contains(where: { $0.name == "notes.md" }))
        XCTAssertFalse(listOutput.entries.contains(where: { $0.name.hasPrefix(".") }))
        // Small workspace — must not be truncated
        XCTAssertFalse(listOutput.truncated)

        let readResult = try await skill.execute(
            actionID: "fs.read_file",
            input: AtlasToolInput(argumentsJSON: #"{"path":"notes.md"}"#),
            context: context
        )
        let readOutput = try decode(readResult.output, as: FileSystemReadFileOutput.self)
        XCTAssertEqual(readOutput.fileType, "md")
        XCTAssertTrue(readOutput.content.contains("Atlas filesystem test"))

        let searchResult = try await skill.execute(
            actionID: "fs.search",
            input: AtlasToolInput(argumentsJSON: #"{"rootPath":".","query":"notes"}"#),
            context: context
        )
        let searchOutput = try decode(searchResult.output, as: FileSystemSearchOutput.self)
        XCTAssertEqual(searchOutput.results.first?.name, "notes.md")

        let metadataResult = try await skill.execute(
            actionID: "fs.get_metadata",
            input: AtlasToolInput(argumentsJSON: #"{"path":"notes.md"}"#),
            context: context
        )
        let metadataOutput = try decode(metadataResult.output, as: FileSystemMetadata.self)
        XCTAssertEqual(metadataOutput.fileExtension, "md")
        XCTAssertEqual(metadataOutput.type, .file)
        XCTAssertTrue(metadataOutput.readable)
    }

    // MARK: - Bug fix: list_directory truncation signal

    func testListDirectoryTruncatedFlagSetWhenEntriesExceedLimit() async throws {
        let rootURL = FileManager.default.temporaryDirectory
            .appendingPathComponent("FileSystemSkillTests-Truncated-\(UUID().uuidString)", isDirectory: true)
        try FileManager.default.createDirectory(at: rootURL, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: rootURL) }

        // Create more files than maxDirectoryEntries (500) to trigger truncation
        for i in 0..<510 {
            try Data("file \(i)".utf8).write(to: rootURL.appendingPathComponent("file-\(i).txt"))
        }

        let scopeStore = FileAccessScopeStore(
            defaults: UserDefaults(suiteName: "FileSystemSkillTests.\(UUID().uuidString)")!,
            storageKey: "AtlasFileAccessRoots.\(UUID().uuidString)"
        )
        let bookmark = try FileAccessScopeStore.makeBookmarkData(for: rootURL)
        _ = try await scopeStore.addRoot(bookmarkData: bookmark)

        let skill = FileSystemSkill(scopeStore: scopeStore)
        let result = try await skill.execute(
            actionID: "fs.list_directory",
            input: AtlasToolInput(argumentsJSON: #"{"path":"."}"#),
            context: makeContext()
        )
        let output = try decode(result.output, as: FileSystemListDirectoryOutput.self)
        XCTAssertEqual(output.entries.count, 500)
        XCTAssertTrue(output.truncated)
    }

    // MARK: - Bug fix: fs.search includeHidden

    func testSearchIncludesHiddenFilesWhenRequested() async throws {
        let workspace = try makeWorkspace()
        let scopeStore = FileAccessScopeStore(
            defaults: UserDefaults(suiteName: "FileSystemSkillTests.\(UUID().uuidString)")!,
            storageKey: "AtlasFileAccessRoots.\(UUID().uuidString)"
        )
        let bookmark = try FileAccessScopeStore.makeBookmarkData(for: workspace.rootURL)
        _ = try await scopeStore.addRoot(bookmarkData: bookmark)

        let skill = FileSystemSkill(scopeStore: scopeStore)
        let context = makeContext()

        // Without includeHidden: .hidden.txt should not appear
        let defaultResult = try await skill.execute(
            actionID: "fs.search",
            input: AtlasToolInput(argumentsJSON: #"{"rootPath":".","query":"hidden"}"#),
            context: context
        )
        let defaultOutput = try decode(defaultResult.output, as: FileSystemSearchOutput.self)
        XCTAssertTrue(defaultOutput.results.isEmpty, "Hidden file should not appear in default search.")

        // With includeHidden: true — .hidden.txt should now match
        let hiddenResult = try await skill.execute(
            actionID: "fs.search",
            input: AtlasToolInput(argumentsJSON: #"{"rootPath":".","query":"hidden","includeHidden":true}"#),
            context: context
        )
        let hiddenOutput = try decode(hiddenResult.output, as: FileSystemSearchOutput.self)
        XCTAssertTrue(hiddenOutput.results.contains(where: { $0.name == ".hidden.txt" }))
    }

    // MARK: - fs.content_search

    func testContentSearchFindsTextInsideFile() async throws {
        let workspace = try makeWorkspace()
        let scopeStore = FileAccessScopeStore(
            defaults: UserDefaults(suiteName: "FileSystemSkillTests.\(UUID().uuidString)")!,
            storageKey: "AtlasFileAccessRoots.\(UUID().uuidString)"
        )
        let bookmark = try FileAccessScopeStore.makeBookmarkData(for: workspace.rootURL)
        _ = try await scopeStore.addRoot(bookmarkData: bookmark)

        let skill = FileSystemSkill(scopeStore: scopeStore)
        let result = try await skill.execute(
            actionID: "fs.content_search",
            input: AtlasToolInput(argumentsJSON: #"{"rootPath":".","query":"filesystem test"}"#),
            context: makeContext()
        )
        let output = try decode(result.output, as: FileSystemContentSearchOutput.self)
        XCTAssertFalse(output.matches.isEmpty)
        XCTAssertEqual(output.matches.first?.name, "notes.md")
        XCTAssertTrue(output.matches.first?.lineContent.lowercased().contains("filesystem test") == true)
        XCTAssertFalse(output.truncated)
    }

    func testContentSearchReturnsEmptyForNoMatch() async throws {
        let workspace = try makeWorkspace()
        let scopeStore = FileAccessScopeStore(
            defaults: UserDefaults(suiteName: "FileSystemSkillTests.\(UUID().uuidString)")!,
            storageKey: "AtlasFileAccessRoots.\(UUID().uuidString)"
        )
        let bookmark = try FileAccessScopeStore.makeBookmarkData(for: workspace.rootURL)
        _ = try await scopeStore.addRoot(bookmarkData: bookmark)

        let skill = FileSystemSkill(scopeStore: scopeStore)
        let result = try await skill.execute(
            actionID: "fs.content_search",
            input: AtlasToolInput(argumentsJSON: #"{"rootPath":".","query":"xyzzy-no-match-ever"}"#),
            context: makeContext()
        )
        let output = try decode(result.output, as: FileSystemContentSearchOutput.self)
        XCTAssertTrue(output.matches.isEmpty)
        XCTAssertFalse(output.truncated)
    }

    func testContentSearchExtensionFilterRestrictsSearch() async throws {
        let workspace = try makeWorkspace()
        let scopeStore = FileAccessScopeStore(
            defaults: UserDefaults(suiteName: "FileSystemSkillTests.\(UUID().uuidString)")!,
            storageKey: "AtlasFileAccessRoots.\(UUID().uuidString)"
        )
        let bookmark = try FileAccessScopeStore.makeBookmarkData(for: workspace.rootURL)
        _ = try await scopeStore.addRoot(bookmarkData: bookmark)

        let skill = FileSystemSkill(scopeStore: scopeStore)
        let context = makeContext()

        // "struct Demo" is only in Demo.swift — searching with extensions:["swift"] should find it
        let swiftResult = try await skill.execute(
            actionID: "fs.content_search",
            input: AtlasToolInput(argumentsJSON: #"{"rootPath":".","query":"struct Demo","extensions":["swift"]}"#),
            context: context
        )
        let swiftOutput = try decode(swiftResult.output, as: FileSystemContentSearchOutput.self)
        XCTAssertFalse(swiftOutput.matches.isEmpty)
        XCTAssertTrue(swiftOutput.matches.allSatisfy { $0.name.hasSuffix(".swift") })

        // Limiting to .md only — "struct Demo" should not be found
        let mdResult = try await skill.execute(
            actionID: "fs.content_search",
            input: AtlasToolInput(argumentsJSON: #"{"rootPath":".","query":"struct Demo","extensions":["md"]}"#),
            context: context
        )
        let mdOutput = try decode(mdResult.output, as: FileSystemContentSearchOutput.self)
        XCTAssertTrue(mdOutput.matches.isEmpty)
    }

    func testContentSearchBlocksOutOfScopePath() async throws {
        let workspace = try makeWorkspace()
        let scopeStore = FileAccessScopeStore(
            defaults: UserDefaults(suiteName: "FileSystemSkillTests.\(UUID().uuidString)")!,
            storageKey: "AtlasFileAccessRoots.\(UUID().uuidString)"
        )
        let bookmark = try FileAccessScopeStore.makeBookmarkData(for: workspace.rootURL)
        _ = try await scopeStore.addRoot(bookmarkData: bookmark)

        let skill = FileSystemSkill(scopeStore: scopeStore)
        do {
            _ = try await skill.execute(
                actionID: "fs.content_search",
                input: AtlasToolInput(argumentsJSON: #"{"rootPath":"/etc","query":"password"}"#),
                context: makeContext()
            )
            XCTFail("Expected out-of-scope content search to fail.")
        } catch {
            XCTAssertFalse(error.localizedDescription.isEmpty)
        }
    }

    func testFileSystemSkillRejectsOutOfScopeAndBinaryReads() async throws {
        let workspace = try makeWorkspace()
        let scopeStore = FileAccessScopeStore(
            defaults: UserDefaults(suiteName: "FileSystemSkillTests.\(UUID().uuidString)")!,
            storageKey: "AtlasFileAccessRoots.\(UUID().uuidString)"
        )
        let bookmark = try FileAccessScopeStore.makeBookmarkData(for: workspace.rootURL)
        _ = try await scopeStore.addRoot(bookmarkData: bookmark)

        let skill = FileSystemSkill(scopeStore: scopeStore)
        let context = makeContext()

        do {
            _ = try await skill.execute(
                actionID: "fs.read_file",
                input: AtlasToolInput(argumentsJSON: #"{"path":"/etc/hosts"}"#),
                context: context
            )
            XCTFail("Expected out-of-scope read to fail.")
        } catch {
            XCTAssertTrue(error.localizedDescription.contains("outside"))
        }

        do {
            _ = try await skill.execute(
                actionID: "fs.read_file",
                input: AtlasToolInput(argumentsJSON: #"{"path":"blob.bin"}"#),
                context: context
            )
            XCTFail("Expected binary read to fail.")
        } catch {
            XCTAssertTrue(error.localizedDescription.contains("supports text-like files") || error.localizedDescription.contains("binary"))
        }
    }

    func testValidationIncludesAtlasManagedTelegramAttachmentsRoot() async throws {
        let scopeStore = FileAccessScopeStore(
            defaults: UserDefaults(suiteName: "FileSystemSkillTests.\(UUID().uuidString)")!,
            storageKey: "AtlasFileAccessRoots.\(UUID().uuidString)"
        )
        let skill = FileSystemSkill(scopeStore: scopeStore)
        let validation = await skill.validateConfiguration(
            context: SkillValidationContext(
                config: AtlasConfig(),
                logger: AtlasLogger(category: "test")
            )
        )

        XCTAssertEqual(validation.status, .passed)
        XCTAssertTrue(validation.summary.contains("approved folder"))
    }

    func testFileSystemSkillReadsTelegramAttachmentsInsideAtlasManagedRoot() async throws {
        let scopeStore = FileAccessScopeStore(
            defaults: UserDefaults(suiteName: "FileSystemSkillTests.\(UUID().uuidString)")!,
            storageKey: "AtlasFileAccessRoots.\(UUID().uuidString)"
        )
        let skill = FileSystemSkill(scopeStore: scopeStore)
        let context = makeContext()

        let telegramRoot = FileAccessScopeStore.telegramAttachmentsRootURL()
        let messageDirectory = telegramRoot
            .appendingPathComponent("chat-123", isDirectory: true)
            .appendingPathComponent("message-456", isDirectory: true)
        try FileManager.default.createDirectory(at: messageDirectory, withIntermediateDirectories: true)

        let fileURL = messageDirectory.appendingPathComponent("sample.txt")
        try Data("telegram attachment contents".utf8).write(to: fileURL)
        defer { try? FileManager.default.removeItem(at: telegramRoot) }

        let result = try await skill.execute(
            actionID: "fs.read_file",
            input: AtlasToolInput(argumentsJSON: #"{"path":"\#(fileURL.path)"}"#),
            context: context
        )
        let output = try decode(result.output, as: FileSystemReadFileOutput.self)
        XCTAssertTrue(output.content.contains("telegram attachment"))
    }

    func testFileSystemSkillListsImageArtifactsInsideAtlasManagedRoot() async throws {
        let scopeStore = FileAccessScopeStore(
            defaults: UserDefaults(suiteName: "FileSystemSkillTests.\(UUID().uuidString)")!,
            storageKey: "AtlasFileAccessRoots.\(UUID().uuidString)"
        )
        let skill = FileSystemSkill(scopeStore: scopeStore)
        let context = makeContext()

        let imageRoot = FileAccessScopeStore.imageArtifactsRootURL()
        let imageURL = imageRoot.appendingPathComponent("sample.png")
        try FileManager.default.createDirectory(at: imageRoot, withIntermediateDirectories: true)
        try Data([0x89, 0x50, 0x4E, 0x47]).write(to: imageURL)
        defer { try? FileManager.default.removeItem(at: imageRoot) }

        let result = try await skill.execute(
            actionID: "fs.list_directory",
            input: AtlasToolInput(argumentsJSON: #"{"path":"\#(imageRoot.path)"}"#),
            context: context
        )
        let output = try decode(result.output, as: FileSystemListDirectoryOutput.self)
        XCTAssertTrue(output.entries.contains(where: { $0.name == "sample.png" }))
    }

    func testFileSystemSkillAcceptsHomePathWithoutLeadingSlashForManagedRoot() async throws {
        let scopeStore = FileAccessScopeStore(
            defaults: UserDefaults(suiteName: "FileSystemSkillTests.\(UUID().uuidString)")!,
            storageKey: "AtlasFileAccessRoots.\(UUID().uuidString)"
        )
        let skill = FileSystemSkill(scopeStore: scopeStore)
        let context = makeContext()

        let imageRoot = FileAccessScopeStore.imageArtifactsRootURL()
        let imageURL = imageRoot.appendingPathComponent("sample-2.png")
        try FileManager.default.createDirectory(at: imageRoot, withIntermediateDirectories: true)
        try Data([0x89, 0x50, 0x4E, 0x47]).write(to: imageURL)
        defer { try? FileManager.default.removeItem(at: imageRoot) }

        let home = FileManager.default.homeDirectoryForCurrentUser.path
        let missingLeadingSlashPath = imageRoot.path.replacingOccurrences(of: home, with: String(home.dropFirst()))

        let result = try await skill.execute(
            actionID: "fs.list_directory",
            input: AtlasToolInput(argumentsJSON: #"{"path":"\#(missingLeadingSlashPath)"}"#),
            context: context
        )
        let output = try decode(result.output, as: FileSystemListDirectoryOutput.self)
        XCTAssertTrue(output.entries.contains(where: { $0.name == "sample-2.png" }))
    }

    private func makeContext() -> SkillExecutionContext {
        SkillExecutionContext(
            conversationID: nil,
            logger: AtlasLogger(category: "test"),
            config: AtlasConfig(),
            permissionManager: PermissionManager(grantedPermissions: [.read]),
            runtimeStatusProvider: { nil },
            enabledSkillsProvider: { [] }
        )
    }

    private func makeWorkspace() throws -> (rootURL: URL, fileURL: URL) {
        let rootURL = FileManager.default.temporaryDirectory.appendingPathComponent("FileSystemSkillTests-\(UUID().uuidString)", isDirectory: true)
        try FileManager.default.createDirectory(at: rootURL, withIntermediateDirectories: true)

        let fileURL = rootURL.appendingPathComponent("notes.md")
        try Data("# Atlas filesystem test\nThis file should be readable.".utf8).write(to: fileURL)
        try Data([0x00, 0x01, 0x02, 0x03]).write(to: rootURL.appendingPathComponent("blob.bin"))
        try Data("hidden".utf8).write(to: rootURL.appendingPathComponent(".hidden.txt"))

        let subdirectory = rootURL.appendingPathComponent("Sources", isDirectory: true)
        try FileManager.default.createDirectory(at: subdirectory, withIntermediateDirectories: true)
        try Data("struct Demo {}".utf8).write(to: subdirectory.appendingPathComponent("Demo.swift"))

        return (rootURL, fileURL)
    }

    private func decode<T: Decodable>(_ string: String, as type: T.Type) throws -> T {
        try AtlasJSON.decoder.decode(T.self, from: Data(string.utf8))
    }
}
