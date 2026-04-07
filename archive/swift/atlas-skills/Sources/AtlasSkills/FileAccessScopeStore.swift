import Foundation
import AtlasShared

public enum FileAccessScopeError: LocalizedError {
    case invalidRoot
    case invalidBookmark
    case noApprovedRoots
    case ambiguousRelativePath
    case pathOutsideApprovedRoots(String)
    case rootNotFound(UUID)

    public var errorDescription: String? {
        switch self {
        case .invalidRoot:
            return "The selected folder could not be approved."
        case .invalidBookmark:
            return "Atlas could not resolve the approved folder bookmark."
        case .noApprovedRoots:
            return "No approved folders are available for File Explorer yet."
        case .ambiguousRelativePath:
            return "Relative paths are ambiguous because multiple approved folders exist."
        case .pathOutsideApprovedRoots(let path):
            return "'\(path)' is outside the folders approved for File Explorer."
        case .rootNotFound:
            return "The approved folder could not be found."
        }
    }
}

struct ResolvedFileAccess: Sendable {
    let root: ApprovedFileAccessRoot
    let rootURL: URL
    let targetURL: URL
}

public actor FileAccessScopeStore {
    private static let telegramAttachmentsRootID = UUID(uuidString: "F0B12A21-8D64-4BDE-B6B1-2E1A2A7D5F11")!
    private static let imageArtifactsRootID = UUID(uuidString: "44F7E4D2-0B72-4E2A-B8E0-5D8C15D86232")!

    private struct PersistedRoot: Codable, Hashable, Sendable {
        let id: UUID
        let displayName: String
        let path: String
        let bookmarkData: Data
        let createdAt: Date
        let updatedAt: Date

        var publicRoot: ApprovedFileAccessRoot {
            ApprovedFileAccessRoot(
                id: id,
                displayName: displayName,
                path: path,
                createdAt: createdAt,
                updatedAt: updatedAt
            )
        }
    }

    private let defaults: UserDefaults
    private let storageKey: String

    public init(
        defaults: UserDefaults = .standard,
        storageKey: String = "AtlasFileAccessRoots"
    ) {
        self.defaults = defaults
        self.storageKey = storageKey
    }

    public static func makeBookmarkData(for url: URL) throws -> Data {
        let standardizedURL = url.standardizedFileURL
        var options: URL.BookmarkCreationOptions = [.withSecurityScope]
        if #available(macOS 11.0, *) {
            options.insert(.securityScopeAllowOnlyReadAccess)
        }

        do {
            return try standardizedURL.bookmarkData(
                options: options,
                includingResourceValuesForKeys: nil,
                relativeTo: nil
            )
        } catch {
            return try standardizedURL.bookmarkData()
        }
    }

    public func listRoots() -> [ApprovedFileAccessRoot] {
        let persisted = loadPersistedRoots().map(\.publicRoot)
        let combined = combinedRoots(persisted: persisted, managed: Self.atlasManagedRoots())

        return combined
            .sorted { $0.displayName.localizedCaseInsensitiveCompare($1.displayName) == .orderedAscending }
    }

    public func approvedRootCount() -> Int {
        combinedRoots(
            persisted: loadPersistedRoots().map(\.publicRoot),
            managed: Self.atlasManagedRoots()
        ).count
    }

    public func addRoot(bookmarkData: Data) throws -> ApprovedFileAccessRoot {
        let (resolvedURL, _) = try resolveBookmarkData(bookmarkData)
        guard resolvedURL.isFileURL else {
            throw FileAccessScopeError.invalidRoot
        }

        let standardizedURL = resolvedURL.standardizedFileURL
        var isDirectory: ObjCBool = false
        guard FileManager.default.fileExists(atPath: standardizedURL.path, isDirectory: &isDirectory), isDirectory.boolValue else {
            throw FileAccessScopeError.invalidRoot
        }

        var roots = loadPersistedRoots()
        if let existingIndex = roots.firstIndex(where: { $0.path == standardizedURL.path }) {
            let existing = roots[existingIndex]
            let updated = PersistedRoot(
                id: existing.id,
                displayName: standardizedURL.lastPathComponent,
                path: standardizedURL.path,
                bookmarkData: bookmarkData,
                createdAt: existing.createdAt,
                updatedAt: .now
            )
            roots[existingIndex] = updated
            savePersistedRoots(roots)
            return updated.publicRoot
        }

        let root = PersistedRoot(
            id: UUID(),
            displayName: standardizedURL.lastPathComponent,
            path: standardizedURL.path,
            bookmarkData: bookmarkData,
            createdAt: .now,
            updatedAt: .now
        )

        roots.append(root)
        savePersistedRoots(roots)
        return root.publicRoot
    }

    public func removeRoot(id: UUID) throws -> ApprovedFileAccessRoot {
        if let managed = Self.atlasManagedRoots().first(where: { $0.id == id }) {
            throw FileAccessScopeError.rootNotFound(managed.id)
        }

        var roots = loadPersistedRoots()
        guard let index = roots.firstIndex(where: { $0.id == id }) else {
            throw FileAccessScopeError.rootNotFound(id)
        }

        let removed = roots.remove(at: index)
        savePersistedRoots(roots)
        return removed.publicRoot
    }

    func resolveAccess(for requestedPath: String) throws -> ResolvedFileAccess {
        let trimmed = requestedPath.trimmingCharacters(in: .whitespacesAndNewlines)
        let normalizedPath = normalizeRequestedPath(trimmed)
        let roots = loadPersistedRoots()

        guard !normalizedPath.isEmpty else {
            throw FileAccessScopeError.pathOutsideApprovedRoots(requestedPath)
        }

        let managedRoots = Self.atlasManagedRoots()

        guard !roots.isEmpty || !managedRoots.isEmpty else {
            throw FileAccessScopeError.noApprovedRoots
        }

        let resolvedPersistedRoots = try roots.map { root -> (ApprovedFileAccessRoot, URL) in
            let (resolvedURL, isStale) = try resolveBookmarkData(root.bookmarkData)
            if isStale {
                silentlyRenewBookmark(for: root, resolvedURL: resolvedURL)
            }
            return (root.publicRoot, resolvedURL.standardizedFileURL)
        }
        let resolvedManagedRoots = managedRoots.map { root in
            (root, URL(fileURLWithPath: root.path).standardizedFileURL)
        }
        let resolvedRoots = deduplicatedResolvedRoots(resolvedPersistedRoots + resolvedManagedRoots)

        let targetCandidate: URL
        if normalizedPath.hasPrefix("/") {
            targetCandidate = URL(fileURLWithPath: normalizedPath).standardizedFileURL
        } else if resolvedPersistedRoots.count == 1, let onlyRoot = resolvedPersistedRoots.first?.1 {
            targetCandidate = onlyRoot.appendingPathComponent(normalizedPath).standardizedFileURL
        } else if resolvedRoots.count == 1, let onlyRoot = resolvedRoots.first?.1 {
            targetCandidate = onlyRoot.appendingPathComponent(normalizedPath).standardizedFileURL
        } else {
            throw FileAccessScopeError.ambiguousRelativePath
        }

        let resolvedTarget = resolvedURLIfPossible(for: targetCandidate)

        for (root, rootURL) in resolvedRoots {
            let normalizedRoot = resolvedURLIfPossible(for: rootURL)
            if contains(resolvedTarget, within: normalizedRoot) {
                return ResolvedFileAccess(
                    root: root,
                    rootURL: normalizedRoot,
                    targetURL: resolvedTarget
                )
            }
        }

        throw FileAccessScopeError.pathOutsideApprovedRoots(normalizedPath)
    }

    private func loadPersistedRoots() -> [PersistedRoot] {
        guard let data = defaults.data(forKey: storageKey) else {
            return []
        }

        return (try? AtlasJSON.decoder.decode([PersistedRoot].self, from: data)) ?? []
    }

    private func savePersistedRoots(_ roots: [PersistedRoot]) {
        if let data = try? AtlasJSON.encoder.encode(roots) {
            defaults.set(data, forKey: storageKey)
        }
    }

    private func resolveBookmarkData(_ bookmarkData: Data) throws -> (url: URL, isStale: Bool) {
        var isStale = false
        if let url = try? URL(
            resolvingBookmarkData: bookmarkData,
            options: [.withSecurityScope],
            relativeTo: nil,
            bookmarkDataIsStale: &isStale
        ) {
            return (url, isStale)
        }

        var isStale2 = false
        if let url = try? URL(
            resolvingBookmarkData: bookmarkData,
            options: [],
            relativeTo: nil,
            bookmarkDataIsStale: &isStale2
        ) {
            return (url, isStale2)
        }

        throw FileAccessScopeError.invalidBookmark
    }

    /// Silently attempts to refresh a stale security-scoped bookmark. Failures are ignored —
    /// the resolved URL remains valid for the current access even when the bookmark is stale.
    private func silentlyRenewBookmark(for root: PersistedRoot, resolvedURL: URL) {
        guard let freshData = try? FileAccessScopeStore.makeBookmarkData(for: resolvedURL) else { return }
        var roots = loadPersistedRoots()
        guard let idx = roots.firstIndex(where: { $0.id == root.id }) else { return }
        roots[idx] = PersistedRoot(
            id: root.id,
            displayName: root.displayName,
            path: resolvedURL.standardizedFileURL.path,
            bookmarkData: freshData,
            createdAt: root.createdAt,
            updatedAt: .now
        )
        savePersistedRoots(roots)
    }

    private func resolvedURLIfPossible(for url: URL) -> URL {
        // Resolve symlinks atomically in a single call — no separate fileExists
        // check avoids a TOCTOU window where a symlink could be swapped between
        // the existence test and resolution. resolvingSymlinksInPath() returns
        // the standardized path unchanged when the path does not exist on disk.
        return url.resolvingSymlinksInPath().standardizedFileURL
    }

    private func contains(_ candidate: URL, within root: URL) -> Bool {
        // Compare lowercased paths so that case differences on macOS's
        // case-insensitive (APFS/HFS+) filesystem don't create false negatives.
        // resolvingSymlinksInPath() already produces canonical paths for
        // existing entries; lowercasing covers edge cases for pending paths.
        let candidatePath = candidate.path.lowercased()
        let rootPath = root.path.lowercased()

        if candidatePath == rootPath {
            return true
        }

        let normalizedRootPath = rootPath.hasSuffix("/") ? rootPath : rootPath + "/"
        return candidatePath.hasPrefix(normalizedRootPath)
    }

    private func normalizeRequestedPath(_ path: String) -> String {
        guard !path.isEmpty else {
            return path
        }

        if path.hasPrefix("~/") {
            let suffix = String(path.dropFirst(2))
            return FileManager.default.homeDirectoryForCurrentUser
                .appendingPathComponent(suffix, isDirectory: false)
                .path
        }

        let homePath = FileManager.default.homeDirectoryForCurrentUser.path
        let homePathWithoutLeadingSlash = String(homePath.drop(while: { $0 == "/" }))
        if path == homePathWithoutLeadingSlash || path.hasPrefix(homePathWithoutLeadingSlash + "/") {
            return "/" + path
        }

        return path
    }

    private func combinedRoots(
        persisted: [ApprovedFileAccessRoot],
        managed: [ApprovedFileAccessRoot]
    ) -> [ApprovedFileAccessRoot] {
        var seenPaths = Set<String>()
        var roots: [ApprovedFileAccessRoot] = []

        for root in persisted + managed {
            if seenPaths.insert(root.path).inserted {
                roots.append(root)
            }
        }

        return roots
    }

    private func deduplicatedResolvedRoots(
        _ roots: [(ApprovedFileAccessRoot, URL)]
    ) -> [(ApprovedFileAccessRoot, URL)] {
        var seenPaths = Set<String>()
        var deduplicated: [(ApprovedFileAccessRoot, URL)] = []

        for (root, url) in roots {
            if seenPaths.insert(url.path).inserted {
                deduplicated.append((root, url))
            }
        }

        return deduplicated
    }
}

public extension FileAccessScopeStore {
    static func atlasManagedRoots() -> [ApprovedFileAccessRoot] {
        let telegramRoot = ensureTelegramAttachmentsRoot()
        let imageArtifactsRoot = ensureImageArtifactsRoot()
        return [
            ApprovedFileAccessRoot(
                id: telegramAttachmentsRootID,
                displayName: "Telegram Attachments",
                path: telegramRoot.path
            ),
            ApprovedFileAccessRoot(
                id: imageArtifactsRootID,
                displayName: "Image Artifacts",
                path: imageArtifactsRoot.path
            )
        ]
    }

    static func isAtlasManagedRoot(_ root: ApprovedFileAccessRoot) -> Bool {
        atlasManagedRoots().contains(where: { $0.id == root.id || $0.path == root.path })
    }

    static func telegramAttachmentsRootURL() -> URL {
        ensureTelegramAttachmentsRoot()
    }

    static func imageArtifactsRootURL() -> URL {
        ensureImageArtifactsRoot()
    }
}

private extension FileAccessScopeStore {
    static func ensureTelegramAttachmentsRoot() -> URL {
        let root = appSupportDirectory()
            .appendingPathComponent("ProjectAtlas", isDirectory: true)
            .appendingPathComponent("TelegramAttachments", isDirectory: true)

        try? FileManager.default.createDirectory(at: root, withIntermediateDirectories: true)
        return root
    }

    static func ensureImageArtifactsRoot() -> URL {
        let root = appSupportDirectory()
            .appendingPathComponent("ProjectAtlas", isDirectory: true)
            .appendingPathComponent("ImageArtifacts", isDirectory: true)

        try? FileManager.default.createDirectory(at: root, withIntermediateDirectories: true)
        return root
    }

    static func appSupportDirectory() -> URL {
        (try? FileManager.default.url(
            for: .applicationSupportDirectory,
            in: .userDomainMask,
            appropriateFor: nil,
            create: true
        )) ?? FileManager.default.homeDirectoryForCurrentUser
            .appendingPathComponent("Library", isDirectory: true)
            .appendingPathComponent("Application Support", isDirectory: true)
    }
}
