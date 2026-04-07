import Foundation

public enum FileSystemEntryType: String, Codable, CaseIterable, Hashable, Sendable {
    case file
    case folder
    case symlink
    case other
}

public enum FileSearchMatchType: String, Codable, CaseIterable, Hashable, Sendable {
    case name
    case path
}

public enum FileSortField: String, Codable, CaseIterable, Hashable, Sendable {
    case name
    case size
    case modifiedAt
}

public enum FileSortOrder: String, Codable, CaseIterable, Hashable, Sendable {
    case asc
    case desc
}

public struct FileSystemEntry: Codable, Hashable, Sendable {
    public let name: String
    public let path: String
    public let type: FileSystemEntryType
    public let size: Int64?
    public let modifiedAt: Date?

    public init(
        name: String,
        path: String,
        type: FileSystemEntryType,
        size: Int64? = nil,
        modifiedAt: Date? = nil
    ) {
        self.name = name
        self.path = path
        self.type = type
        self.size = size
        self.modifiedAt = modifiedAt
    }
}

public struct FileSearchResult: Codable, Hashable, Sendable {
    public let path: String
    public let name: String
    public let matchType: FileSearchMatchType
    public let type: FileSystemEntryType
    public let modifiedAt: Date?

    public init(
        path: String,
        name: String,
        matchType: FileSearchMatchType,
        type: FileSystemEntryType,
        modifiedAt: Date? = nil
    ) {
        self.path = path
        self.name = name
        self.matchType = matchType
        self.type = type
        self.modifiedAt = modifiedAt
    }
}

public struct FileSystemMetadata: Codable, Hashable, Sendable {
    public let path: String
    public let name: String
    public let type: FileSystemEntryType
    public let size: Int64?
    public let createdAt: Date?
    public let modifiedAt: Date?
    public let readable: Bool
    public let fileExtension: String?

    public init(
        path: String,
        name: String,
        type: FileSystemEntryType,
        size: Int64?,
        createdAt: Date?,
        modifiedAt: Date?,
        readable: Bool,
        fileExtension: String?
    ) {
        self.path = path
        self.name = name
        self.type = type
        self.size = size
        self.createdAt = createdAt
        self.modifiedAt = modifiedAt
        self.readable = readable
        self.fileExtension = fileExtension
    }
}

public struct ApprovedFileAccessRoot: Codable, Identifiable, Hashable, Sendable {
    public let id: UUID
    public let displayName: String
    public let path: String
    public let createdAt: Date
    public let updatedAt: Date

    public init(
        id: UUID = UUID(),
        displayName: String,
        path: String,
        createdAt: Date = .now,
        updatedAt: Date = .now
    ) {
        self.id = id
        self.displayName = displayName
        self.path = path
        self.createdAt = createdAt
        self.updatedAt = updatedAt
    }
}

public struct FileAccessRootGrantRequest: Codable, Hashable, Sendable {
    public let bookmarkData: Data

    public init(bookmarkData: Data) {
        self.bookmarkData = bookmarkData
    }
}

// MARK: - List Directory

public struct FileSystemListDirectoryInput: Codable, Hashable, Sendable {
    public let path: String
    public let recursive: Bool?
    public let maxDepth: Int?
    public let includeHidden: Bool?
    public let sortBy: FileSortField?
    public let sortOrder: FileSortOrder?

    public init(
        path: String,
        recursive: Bool? = nil,
        maxDepth: Int? = nil,
        includeHidden: Bool? = nil,
        sortBy: FileSortField? = nil,
        sortOrder: FileSortOrder? = nil
    ) {
        self.path = path
        self.recursive = recursive
        self.maxDepth = maxDepth
        self.includeHidden = includeHidden
        self.sortBy = sortBy
        self.sortOrder = sortOrder
    }
}

public struct FileSystemListDirectoryOutput: Codable, Hashable, Sendable {
    public let entries: [FileSystemEntry]
    public let truncated: Bool
    /// Only set when truncated is true and the total can be determined cheaply (non-recursive listing).
    public let totalCount: Int?

    public init(entries: [FileSystemEntry], truncated: Bool = false, totalCount: Int? = nil) {
        self.entries = entries
        self.truncated = truncated
        self.totalCount = totalCount
    }
}

// MARK: - Read File

public struct FileSystemReadFileInput: Codable, Hashable, Sendable {
    public let path: String
    public let maxCharacters: Int?
    /// 1-based start line. When provided, only lines from startLine onward are returned.
    public let startLine: Int?
    /// 1-based end line (inclusive). Requires startLine or can be used alone.
    public let endLine: Int?

    public init(
        path: String,
        maxCharacters: Int? = nil,
        startLine: Int? = nil,
        endLine: Int? = nil
    ) {
        self.path = path
        self.maxCharacters = maxCharacters
        self.startLine = startLine
        self.endLine = endLine
    }
}

public struct FileSystemReadFileOutput: Codable, Hashable, Sendable {
    public let path: String
    public let fileType: String
    public let content: String
    public let truncated: Bool
    public let size: Int64
    /// 1-based line number where the returned content starts. Nil when the full file was read from the top.
    public let startLine: Int?
    /// 1-based line number where the returned content ends. Nil when the full file was read from the top.
    public let endLine: Int?

    public init(
        path: String,
        fileType: String,
        content: String,
        truncated: Bool,
        size: Int64,
        startLine: Int? = nil,
        endLine: Int? = nil
    ) {
        self.path = path
        self.fileType = fileType
        self.content = content
        self.truncated = truncated
        self.size = size
        self.startLine = startLine
        self.endLine = endLine
    }
}

// MARK: - Search

public struct FileSystemSearchInput: Codable, Hashable, Sendable {
    public let rootPath: String
    public let query: String
    public let filenameOnly: Bool?
    public let maxResults: Int?
    public let includeHidden: Bool?
    /// Optional list of file extensions to restrict filename search to, e.g. ["swift", "md"].
    public let extensions: [String]?

    public init(
        rootPath: String,
        query: String,
        filenameOnly: Bool? = nil,
        maxResults: Int? = nil,
        includeHidden: Bool? = nil,
        extensions: [String]? = nil
    ) {
        self.rootPath = rootPath
        self.query = query
        self.filenameOnly = filenameOnly
        self.maxResults = maxResults
        self.includeHidden = includeHidden
        self.extensions = extensions
    }
}

public struct FileSystemSearchOutput: Codable, Hashable, Sendable {
    public let results: [FileSearchResult]
    public let truncated: Bool

    public init(results: [FileSearchResult], truncated: Bool = false) {
        self.results = results
        self.truncated = truncated
    }
}

// MARK: - Get Metadata

public struct FileSystemGetMetadataInput: Codable, Hashable, Sendable {
    public let path: String

    public init(path: String) {
        self.path = path
    }
}

// MARK: - Content Search

public struct FileSystemContentSearchInput: Codable, Hashable, Sendable {
    public let rootPath: String
    public let query: String
    public let extensions: [String]?
    public let maxResults: Int?
    public let includeHidden: Bool?
    /// Number of surrounding lines to include with each match (0–5). Default is 0.
    public let contextLines: Int?
    /// Treat query as a case-insensitive regular expression. Default is false.
    public let useRegex: Bool?

    public init(
        rootPath: String,
        query: String,
        extensions: [String]? = nil,
        maxResults: Int? = nil,
        includeHidden: Bool? = nil,
        contextLines: Int? = nil,
        useRegex: Bool? = nil
    ) {
        self.rootPath = rootPath
        self.query = query
        self.extensions = extensions
        self.maxResults = maxResults
        self.includeHidden = includeHidden
        self.contextLines = contextLines
        self.useRegex = useRegex
    }
}

public struct ContentSearchMatch: Codable, Hashable, Sendable {
    public let path: String
    public let name: String
    public let lineNumber: Int
    public let lineContent: String
    /// Lines immediately before the match (up to contextLines). Empty when contextLines is 0.
    public let contextBefore: [String]
    /// Lines immediately after the match (up to contextLines). Empty when contextLines is 0.
    public let contextAfter: [String]

    public init(
        path: String,
        name: String,
        lineNumber: Int,
        lineContent: String,
        contextBefore: [String] = [],
        contextAfter: [String] = []
    ) {
        self.path = path
        self.name = name
        self.lineNumber = lineNumber
        self.lineContent = lineContent
        self.contextBefore = contextBefore
        self.contextAfter = contextAfter
    }
}

public struct FileSystemContentSearchOutput: Codable, Hashable, Sendable {
    public let matches: [ContentSearchMatch]
    public let filesSearched: Int
    public let truncated: Bool

    public init(matches: [ContentSearchMatch], filesSearched: Int, truncated: Bool) {
        self.matches = matches
        self.filesSearched = filesSearched
        self.truncated = truncated
    }
}
