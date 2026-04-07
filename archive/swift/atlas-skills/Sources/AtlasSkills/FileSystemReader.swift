import Foundation
import AtlasTools

struct FileSystemReader: Sendable {
    private let policy: FileSystemPolicy
    private let typePolicy: FileTypePolicy

    init(
        policy: FileSystemPolicy,
        typePolicy: FileTypePolicy = FileTypePolicy()
    ) {
        self.policy = policy
        self.typePolicy = typePolicy
    }

    func listDirectory(input: FileSystemListDirectoryInput) async throws -> FileSystemListDirectoryOutput {
        let access = try await policy.resolveAccess(for: input.path)
        let fileManager = FileManager.default
        let includeHidden = input.includeHidden ?? false
        let recursive = input.recursive ?? false
        let maxDepth = recursive ? policy.normalizedDepth(input.maxDepth) : 1
        let granted = access.rootURL.startAccessingSecurityScopedResource()
        defer {
            if granted {
                access.rootURL.stopAccessingSecurityScopedResource()
            }
        }

        var isDirectory: ObjCBool = false
        guard fileManager.fileExists(atPath: access.targetURL.path, isDirectory: &isDirectory), isDirectory.boolValue else {
            throw AtlasToolError.invalidInput("'\(access.targetURL.path)' is not a readable directory.")
        }

        let keys: Set<URLResourceKey> = [.isDirectoryKey, .isSymbolicLinkKey, .fileSizeKey, .contentModificationDateKey]

        if recursive {
            let rawEntries = try recursivelyEnumerateEntries(
                at: access.targetURL,
                includeHidden: includeHidden,
                maxDepth: maxDepth,
                resourceKeys: Array(keys),
                fileManager: fileManager
            )
            let isTruncated = rawEntries.count >= policy.maxDirectoryEntries
            return FileSystemListDirectoryOutput(
                entries: sortEntries(rawEntries, by: input.sortBy, order: input.sortOrder),
                truncated: isTruncated
                // totalCount omitted for recursive: enumerating everything would be too expensive
            )
        }

        let children = try fileManager.contentsOfDirectory(
            at: access.targetURL,
            includingPropertiesForKeys: Array(keys),
            options: [.skipsPackageDescendants]
        )

        let allVisible = children.filter { includeHidden || !isHidden($0) }
        let isTruncated = allVisible.count > policy.maxDirectoryEntries
        let rawEntries = try allVisible
            .prefix(policy.maxDirectoryEntries)
            .map(makeEntry(for:))

        return FileSystemListDirectoryOutput(
            entries: sortEntries(rawEntries, by: input.sortBy, order: input.sortOrder),
            truncated: isTruncated,
            totalCount: isTruncated ? allVisible.count : nil
        )
    }

    func readFile(input: FileSystemReadFileInput) async throws -> FileSystemReadFileOutput {
        let access = try await policy.resolveAccess(for: input.path)
        let fileManager = FileManager.default
        let granted = access.rootURL.startAccessingSecurityScopedResource()
        defer {
            if granted {
                access.rootURL.stopAccessingSecurityScopedResource()
            }
        }

        var isDirectory: ObjCBool = false
        guard fileManager.fileExists(atPath: access.targetURL.path, isDirectory: &isDirectory), !isDirectory.boolValue else {
            throw AtlasToolError.invalidInput("'\(access.targetURL.path)' is not a readable text file.")
        }

        try policy.validateReadableResource(at: access.targetURL, fileManager: fileManager)

        guard let fileType = typePolicy.fileType(for: access.targetURL) else {
            throw AtlasToolError.invalidInput("Atlas only supports text-like files such as .txt, .md, .json, .swift, and .html in File Explorer v1.")
        }

        let attributes = try fileManager.attributesOfItem(atPath: access.targetURL.path)
        let fileSize = (attributes[.size] as? NSNumber)?.int64Value ?? 0
        let characterLimit = policy.normalizedReadCharacterLimit(input.maxCharacters)

        guard let handle = try? FileHandle(forReadingFrom: access.targetURL) else {
            throw AtlasToolError.executionFailed("Atlas could not open '\(access.targetURL.path)' for reading.")
        }
        defer { try? handle.close() }

        let readLimit = min(policy.maxReadBytes, Int(max(fileSize, 1)))
        let data = try handle.read(upToCount: readLimit) ?? Data()

        if typePolicy.isLikelyBinary(data) {
            throw AtlasToolError.invalidInput("'\(access.targetURL.lastPathComponent)' appears to be binary or unsupported.")
        }

        guard var text = typePolicy.decodeText(from: data) else {
            throw AtlasToolError.invalidInput("Atlas could not decode '\(access.targetURL.lastPathComponent)' as readable text.")
        }

        var truncated = fileSize > Int64(data.count)

        // Line-range slicing (v2): slice to requested lines before applying the character limit.
        var reportedStartLine: Int?
        var reportedEndLine: Int?
        if input.startLine != nil || input.endLine != nil {
            let lines = text.components(separatedBy: "\n")
            let totalLineCount = lines.count
            let startIdx = max(0, (input.startLine ?? 1) - 1)            // 1-based → 0-based
            let endIdx   = min(totalLineCount - 1, (input.endLine ?? totalLineCount) - 1)

            if startIdx < totalLineCount && startIdx <= endIdx {
                text = lines[startIdx...endIdx].joined(separator: "\n")
                reportedStartLine = startIdx + 1                         // back to 1-based
                reportedEndLine   = endIdx + 1
            } else {
                text = ""
                reportedStartLine = input.startLine
                reportedEndLine   = input.startLine
            }
            truncated = false  // intentional slice, not overflow truncation
        }

        if text.count > characterLimit {
            text = String(text.prefix(characterLimit))
            truncated = true
        }

        return FileSystemReadFileOutput(
            path: access.targetURL.path,
            fileType: fileType,
            content: text,
            truncated: truncated,
            size: fileSize,
            startLine: reportedStartLine,
            endLine: reportedEndLine
        )
    }

    func metadata(for path: String) async throws -> FileSystemMetadata {
        let access = try await policy.resolveAccess(for: path)
        let fileManager = FileManager.default
        let granted = access.rootURL.startAccessingSecurityScopedResource()
        defer {
            if granted {
                access.rootURL.stopAccessingSecurityScopedResource()
            }
        }

        guard fileManager.fileExists(atPath: access.targetURL.path) else {
            throw AtlasToolError.invalidInput("'\(access.targetURL.path)' does not exist inside the approved folders.")
        }

        let values = try access.targetURL.resourceValues(forKeys: [
            .isDirectoryKey,
            .isSymbolicLinkKey,
            .fileSizeKey,
            .creationDateKey,
            .contentModificationDateKey,
            .isReadableKey
        ])

        return FileSystemMetadata(
            path: access.targetURL.path,
            name: access.targetURL.lastPathComponent,
            type: entryType(for: values),
            size: values.fileSize.map(Int64.init),
            createdAt: values.creationDate,
            modifiedAt: values.contentModificationDate,
            readable: values.isReadable ?? fileManager.isReadableFile(atPath: access.targetURL.path),
            fileExtension: access.targetURL.pathExtension.isEmpty ? nil : access.targetURL.pathExtension.lowercased()
        )
    }

    private func makeEntry(for url: URL) throws -> FileSystemEntry {
        let values = try url.resourceValues(forKeys: [.isDirectoryKey, .isSymbolicLinkKey, .fileSizeKey, .contentModificationDateKey])
        return FileSystemEntry(
            name: url.lastPathComponent,
            path: url.path,
            type: entryType(for: values),
            size: values.fileSize.map(Int64.init),
            modifiedAt: values.contentModificationDate
        )
    }

    private func entryType(for values: URLResourceValues) -> FileSystemEntryType {
        if values.isSymbolicLink == true {
            return .symlink
        }
        if values.isDirectory == true {
            return .folder
        }
        return .file
    }

    private func isHidden(_ url: URL) -> Bool {
        if url.lastPathComponent.hasPrefix(".") { return true }
        return (try? url.resourceValues(forKeys: [.isHiddenKey]).isHidden) == true
    }

    private func sortEntries(
        _ entries: [FileSystemEntry],
        by field: FileSortField?,
        order: FileSortOrder?
    ) -> [FileSystemEntry] {
        let ascending = order != .desc
        guard let field else {
            return ascending
                ? entries.sorted { $0.path < $1.path }
                : entries.sorted { $0.path > $1.path }
        }
        return entries.sorted { a, b in
            let result: Bool
            switch field {
            case .name:
                result = a.name.localizedCaseInsensitiveCompare(b.name) == .orderedAscending
            case .size:
                result = (a.size ?? 0) < (b.size ?? 0)
            case .modifiedAt:
                result = (a.modifiedAt ?? .distantPast) < (b.modifiedAt ?? .distantPast)
            }
            return ascending ? result : !result
        }
    }

    private func isDirectoryURL(_ url: URL) -> Bool {
        ((try? url.resourceValues(forKeys: [.isDirectoryKey]).isDirectory) ?? false) == true
    }

    private func isSymlink(_ url: URL) -> Bool {
        ((try? url.resourceValues(forKeys: [.isSymbolicLinkKey]).isSymbolicLink) ?? false) == true
    }

    private func recursivelyEnumerateEntries(
        at rootURL: URL,
        includeHidden: Bool,
        maxDepth: Int,
        resourceKeys: [URLResourceKey],
        fileManager: FileManager
    ) throws -> [FileSystemEntry] {
        var entries: [FileSystemEntry] = []
        let baseDepth = rootURL.pathComponents.count
        guard let enumerator = fileManager.enumerator(
            at: rootURL,
            includingPropertiesForKeys: resourceKeys,
            options: [.skipsPackageDescendants]
        ) else {
            throw AtlasToolError.executionFailed("Atlas could not enumerate '\(rootURL.path)'.")
        }

        while let nextObject = enumerator.nextObject() {
            guard let itemURL = nextObject as? URL else {
                continue
            }

            let depth = itemURL.pathComponents.count - baseDepth
            if depth > maxDepth {
                enumerator.skipDescendants()
                continue
            }

            if includeHidden == false && isHidden(itemURL) {
                if isDirectoryURL(itemURL) {
                    enumerator.skipDescendants()
                }
                continue
            }

            entries.append(try makeEntry(for: itemURL))

            if isSymlink(itemURL) {
                enumerator.skipDescendants()
            }

            if entries.count >= policy.maxDirectoryEntries {
                break
            }
        }

        return entries
    }
}
