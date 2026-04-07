import Foundation
import AtlasTools

struct FileSystemPolicy: Sendable {
    let scopeStore: FileAccessScopeStore
    let maxDirectoryDepth: Int
    let maxDirectoryEntries: Int
    let maxSearchResults: Int
    let maxReadBytes: Int
    let maxReadCharacters: Int
    let maxContentSearchFiles: Int
    let maxContentSearchMatches: Int
    let maxContextLines: Int

    init(
        scopeStore: FileAccessScopeStore,
        maxDirectoryDepth: Int = 6,
        maxDirectoryEntries: Int = 500,
        maxSearchResults: Int = 100,
        maxReadBytes: Int = 262_144,
        maxReadCharacters: Int = 20_000,
        maxContentSearchFiles: Int = 200,
        maxContentSearchMatches: Int = 200,
        maxContextLines: Int = 5
    ) {
        self.scopeStore = scopeStore
        self.maxDirectoryDepth = maxDirectoryDepth
        self.maxDirectoryEntries = maxDirectoryEntries
        self.maxSearchResults = maxSearchResults
        self.maxReadBytes = maxReadBytes
        self.maxReadCharacters = maxReadCharacters
        self.maxContentSearchFiles = maxContentSearchFiles
        self.maxContentSearchMatches = maxContentSearchMatches
        self.maxContextLines = maxContextLines
    }

    func resolveAccess(for path: String) async throws -> ResolvedFileAccess {
        try await scopeStore.resolveAccess(for: path)
    }

    func normalizedDepth(_ requestedDepth: Int?) -> Int {
        min(max(requestedDepth ?? 1, 0), maxDirectoryDepth)
    }

    func normalizedSearchResultLimit(_ requestedLimit: Int?) -> Int {
        min(max(requestedLimit ?? 25, 1), maxSearchResults)
    }

    func normalizedReadCharacterLimit(_ requestedLimit: Int?) -> Int {
        min(max(requestedLimit ?? 8_000, 256), maxReadCharacters)
    }

    func normalizedContentSearchMatchLimit(_ requestedLimit: Int?) -> Int {
        min(max(requestedLimit ?? 50, 1), maxContentSearchMatches)
    }

    func normalizedContextLines(_ requestedLines: Int?) -> Int {
        min(max(requestedLines ?? 0, 0), maxContextLines)
    }

    func validateReadableResource(at url: URL, fileManager: FileManager = .default) throws {
        let values = try url.resourceValues(forKeys: [.isReadableKey])
        guard values.isReadable ?? fileManager.isReadableFile(atPath: url.path) else {
            throw AtlasToolError.executionFailed("Atlas cannot read '\(url.path)'.")
        }
    }
}
