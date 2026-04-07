import Foundation
import AtlasTools

struct FileSystemSearcher: Sendable {
    private let policy: FileSystemPolicy

    init(policy: FileSystemPolicy) {
        self.policy = policy
    }

    func search(input: FileSystemSearchInput) async throws -> FileSystemSearchOutput {
        let access = try await policy.resolveAccess(for: input.rootPath)
        let fileManager = FileManager.default
        let query = input.query.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !query.isEmpty else {
            throw AtlasToolError.invalidInput("Search query cannot be empty.")
        }

        let granted = access.rootURL.startAccessingSecurityScopedResource()
        defer {
            if granted {
                access.rootURL.stopAccessingSecurityScopedResource()
            }
        }

        var isDirectory: ObjCBool = false
        guard fileManager.fileExists(atPath: access.targetURL.path, isDirectory: &isDirectory), isDirectory.boolValue else {
            throw AtlasToolError.invalidInput("'\(access.targetURL.path)' is not a searchable folder.")
        }

        let filenameOnly = input.filenameOnly ?? false
        let includeHidden = input.includeHidden ?? false
        let maxResults = policy.normalizedSearchResultLimit(input.maxResults)
        let allowedExtensions: Set<String>? = input.extensions.map { Set($0.map { $0.lowercased() }) }

        let (matches, truncated) = try enumerateMatches(
            rootURL: access.targetURL,
            query: query,
            filenameOnly: filenameOnly,
            includeHidden: includeHidden,
            maxResults: maxResults,
            allowedExtensions: allowedExtensions,
            fileManager: fileManager
        )

        return FileSystemSearchOutput(results: matches, truncated: truncated)
    }

    func contentSearch(input: FileSystemContentSearchInput) async throws -> FileSystemContentSearchOutput {
        let access = try await policy.resolveAccess(for: input.rootPath)
        let fileManager = FileManager.default
        let query = input.query.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !query.isEmpty else {
            throw AtlasToolError.invalidInput("Search query cannot be empty.")
        }

        let granted = access.rootURL.startAccessingSecurityScopedResource()
        defer {
            if granted { access.rootURL.stopAccessingSecurityScopedResource() }
        }

        var isDirectory: ObjCBool = false
        guard fileManager.fileExists(atPath: access.targetURL.path, isDirectory: &isDirectory), isDirectory.boolValue else {
            throw AtlasToolError.invalidInput("'\(access.targetURL.path)' is not a searchable folder.")
        }

        let includeHidden = input.includeHidden ?? false
        let maxMatches = policy.normalizedContentSearchMatchLimit(input.maxResults)
        let allowedExtensions: Set<String>? = input.extensions.map { Set($0.map { $0.lowercased() }) }
        let contextLines = policy.normalizedContextLines(input.contextLines)
        let useRegex = input.useRegex ?? false

        return try searchContents(
            rootURL: access.targetURL,
            query: query,
            includeHidden: includeHidden,
            maxMatches: maxMatches,
            allowedExtensions: allowedExtensions,
            contextLines: contextLines,
            useRegex: useRegex,
            fileManager: fileManager
        )
    }

    private func searchContents(
        rootURL: URL,
        query: String,
        includeHidden: Bool,
        maxMatches: Int,
        allowedExtensions: Set<String>?,
        contextLines: Int,
        useRegex: Bool,
        fileManager: FileManager
    ) throws -> FileSystemContentSearchOutput {
        let typePolicy = FileTypePolicy()

        // Compile regex once if requested; reject invalid patterns early.
        let regex: NSRegularExpression?
        if useRegex {
            do {
                regex = try NSRegularExpression(pattern: query, options: .caseInsensitive)
            } catch {
                throw AtlasToolError.invalidInput("Invalid regex pattern '\(query)': \(error.localizedDescription)")
            }
        } else {
            regex = nil
        }
        let normalizedQuery = query.lowercased()  // used only for literal search

        var enumeratorOptions: FileManager.DirectoryEnumerationOptions = [.skipsPackageDescendants]
        if !includeHidden {
            enumeratorOptions.insert(.skipsHiddenFiles)
        }

        let keys: [URLResourceKey] = [.isDirectoryKey, .isSymbolicLinkKey, .fileSizeKey]
        guard let enumerator = fileManager.enumerator(
            at: rootURL,
            includingPropertiesForKeys: keys,
            options: enumeratorOptions
        ) else {
            throw AtlasToolError.executionFailed("Atlas could not enumerate '\(rootURL.path)'.")
        }

        var matches: [ContentSearchMatch] = []
        var filesSearched = 0

        while let nextObject = enumerator.nextObject() {
            guard let itemURL = nextObject as? URL else { continue }

            let values = (try? itemURL.resourceValues(forKeys: Set(keys))) ?? URLResourceValues()
            if values.isSymbolicLink == true { enumerator.skipDescendants(); continue }
            if values.isDirectory == true { continue }

            // Extension filter — use allowed list if provided, else fall back to FileTypePolicy
            let ext = itemURL.pathExtension.lowercased()
            if let allowedExtensions {
                guard allowedExtensions.contains(ext) else { continue }
            } else {
                guard typePolicy.fileType(for: itemURL) != nil else { continue }
            }

            // Skip files larger than the read cap
            let fileSize = Int64(values.fileSize ?? 0)
            guard fileSize <= Int64(policy.maxReadBytes) else { continue }

            filesSearched += 1
            if filesSearched > policy.maxContentSearchFiles {
                return FileSystemContentSearchOutput(matches: matches, filesSearched: filesSearched, truncated: true)
            }

            guard let handle = try? FileHandle(forReadingFrom: itemURL) else { continue }
            defer { try? handle.close() }

            guard
                let data = try? handle.read(upToCount: policy.maxReadBytes),
                !typePolicy.isLikelyBinary(data),
                let text = typePolicy.decodeText(from: data)
            else { continue }

            let lines = text.components(separatedBy: "\n")

            for (index, line) in lines.enumerated() {
                let lineMatches: Bool
                if let regex {
                    let nsRange = NSRange(line.startIndex..., in: line)
                    lineMatches = regex.firstMatch(in: line, options: [], range: nsRange) != nil
                } else {
                    lineMatches = line.lowercased().contains(normalizedQuery)
                }
                guard lineMatches else { continue }

                let beforeStart = max(0, index - contextLines)
                let afterEnd    = min(lines.count - 1, index + contextLines)

                let ctxBefore: [String] = contextLines > 0 && beforeStart < index
                    ? lines[beforeStart..<index].map { String($0.prefix(300)) }
                    : []
                let ctxAfter: [String] = contextLines > 0 && index + 1 <= afterEnd
                    ? lines[(index + 1)...afterEnd].map { String($0.prefix(300)) }
                    : []

                matches.append(ContentSearchMatch(
                    path: itemURL.path,
                    name: itemURL.lastPathComponent,
                    lineNumber: index + 1,
                    lineContent: String(line.trimmingCharacters(in: .whitespaces).prefix(300)),
                    contextBefore: ctxBefore,
                    contextAfter: ctxAfter
                ))

                if matches.count >= maxMatches {
                    return FileSystemContentSearchOutput(matches: matches, filesSearched: filesSearched, truncated: true)
                }
            }
        }

        return FileSystemContentSearchOutput(matches: matches, filesSearched: filesSearched, truncated: false)
    }

    private func enumerateMatches(
        rootURL: URL,
        query: String,
        filenameOnly: Bool,
        includeHidden: Bool,
        maxResults: Int,
        allowedExtensions: Set<String>?,
        fileManager: FileManager
    ) throws -> ([FileSearchResult], Bool) {
        let keys: Set<URLResourceKey> = [.isDirectoryKey, .isSymbolicLinkKey, .contentModificationDateKey]
        var enumeratorOptions: FileManager.DirectoryEnumerationOptions = [.skipsPackageDescendants]
        if !includeHidden {
            enumeratorOptions.insert(.skipsHiddenFiles)
        }
        guard let enumerator = fileManager.enumerator(
            at: rootURL,
            includingPropertiesForKeys: Array(keys),
            options: enumeratorOptions
        ) else {
            throw AtlasToolError.executionFailed("Atlas could not search '\(rootURL.path)'.")
        }

        let normalizedQuery = query.lowercased()
        var matches: [FileSearchResult] = []

        while let nextObject = enumerator.nextObject() {
            guard let itemURL = nextObject as? URL else {
                continue
            }

            let rootPath = rootURL.path
            let fullPath = itemURL.path
            let relativePath = fullPath.hasPrefix(rootPath)
                ? String(fullPath.dropFirst(rootPath.count)).trimmingCharacters(in: CharacterSet(charactersIn: "/"))
                : fullPath
            let candidateName = itemURL.lastPathComponent.lowercased()
            let candidatePath = relativePath.lowercased()

            let matchType: FileSearchMatchType?
            if candidateName.contains(normalizedQuery) {
                matchType = .name
            } else if filenameOnly == false && candidatePath.contains(normalizedQuery) {
                matchType = .path
            } else {
                matchType = nil
            }

            guard let matchType else {
                continue
            }

            let values = try itemURL.resourceValues(forKeys: keys)
            let type: FileSystemEntryType
            if values.isSymbolicLink == true {
                type = .symlink
                enumerator.skipDescendants()
            } else if values.isDirectory == true {
                type = .folder
            } else {
                // Extension filter applies to files only
                if let allowedExtensions {
                    let ext = itemURL.pathExtension.lowercased()
                    guard allowedExtensions.contains(ext) else { continue }
                }
                type = .file
            }

            matches.append(
                FileSearchResult(
                    path: itemURL.path,
                    name: itemURL.lastPathComponent,
                    matchType: matchType,
                    type: type,
                    modifiedAt: values.contentModificationDate
                )
            )

            if matches.count >= maxResults {
                return (matches, true)
            }
        }

        return (matches, false)
    }
}
