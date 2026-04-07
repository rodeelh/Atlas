import Foundation
import AtlasShared
import AtlasTools

public struct FileSystemSkill: AtlasSkill {
    public let manifest: SkillManifest
    public let actions: [SkillActionDefinition]

    private let scopeStore: FileAccessScopeStore
    private let policy: FileSystemPolicy
    private let reader: FileSystemReader
    private let searcher: FileSystemSearcher

    public init(scopeStore: FileAccessScopeStore = FileAccessScopeStore()) {
        let policy = FileSystemPolicy(scopeStore: scopeStore)
        self.scopeStore = scopeStore
        self.policy = policy
        self.reader = FileSystemReader(policy: policy)
        self.searcher = FileSystemSearcher(policy: policy)

        self.manifest = SkillManifest(
            id: "file-system",
            name: "File Explorer",
            version: "2.0.0",
            description: "Read, search, and inspect files inside approved folders. Supports line-range reads, content search with regex and context lines, extension filtering, and directory sort control.",
            category: .system,
            lifecycleState: .installed,
            capabilities: [
                .localRead,
                .fileListing,
                .fileReading,
                .fileSearch,
                .fileMetadata
            ],
            requiredPermissions: [
                .localRead
            ],
            riskLevel: .high,
            trustProfile: .localExact,
            freshnessType: .local,
            preferredQueryTypes: [.localFileRead, .directoryListing, .localFileSearch, .localMetadata],
            routingPriority: 70,
            canHandleLocalData: true,
            restrictionsSummary: [
                "Read-only only",
                "Approved folders only",
                "Hidden files are excluded by default",
                "No writes, moves, deletes, or renames"
            ],
            supportsReadOnlyMode: true,
            isUserVisible: true,
            isEnabledByDefault: true,
            author: "Project Atlas",
            source: "built_in",
            tags: ["filesystem", "read_only", "local"],
            intent: .localFileTask,
            triggers: [
                .init("list files in", queryType: .directoryListing),
                .init("list the files", queryType: .directoryListing),
                .init("show files in", queryType: .directoryListing),
                .init("directory listing", queryType: .directoryListing),
                .init("folder contents", queryType: .directoryListing),
                .init("search in the codebase", queryType: .localFileSearch),
                .init("search my codebase", queryType: .localFileSearch),
                .init("find file", queryType: .localFileSearch),
                .init("search files", queryType: .localFileSearch),
                .init("in the repo", queryType: .localFileRead),
                .init("in my repo", queryType: .localFileRead),
                .init("in the codebase", queryType: .localFileRead),
                .init("read the file", queryType: .localFileRead),
                .init("open the file", queryType: .localFileRead),
                .init("read the code", queryType: .localFileRead),
                .init("show the code", queryType: .localFileRead),
                .init("local file", queryType: .localFileRead),
                .init("local folder", queryType: .directoryListing),
                .init(".swift", queryType: .localFileRead),
                .init(".json", queryType: .localFileRead),
                .init(".md", queryType: .localFileRead)
            ]
        )

        self.actions = [
            SkillActionDefinition(
                id: "fs.list_directory",
                name: "List Directory",
                description: "List files and folders inside an approved directory, with optional bounded recursion and sort control.",
                inputSchemaSummary: "path is required; recursive, maxDepth, includeHidden, sortBy (name | size | modifiedAt), and sortOrder (asc | desc) are optional.",
                outputSchemaSummary: "Structured directory entries with name, path, type, size, and modifiedAt. Includes a truncated flag and totalCount (non-recursive only) when the entry cap was reached.",
                permissionLevel: .read,
                sideEffectLevel: .safeRead,

                preferredQueryTypes: [.directoryListing],
                routingPriority: 40,
                inputSchema: AtlasToolInputSchema(
                    properties: [
                        "path": AtlasToolInputProperty(type: "string", description: "Absolute path inside an approved folder, or a relative path when exactly one approved folder exists."),
                        "recursive": AtlasToolInputProperty(type: "boolean", description: "Whether Atlas should recurse into subfolders."),
                        "maxDepth": AtlasToolInputProperty(type: "integer", description: "Optional recursion depth cap up to 6."),
                        "includeHidden": AtlasToolInputProperty(type: "boolean", description: "Whether hidden files should be included."),
                        "sortBy": AtlasToolInputProperty(type: "string", description: "Sort field: name, size, or modifiedAt. Defaults to path order when omitted."),
                        "sortOrder": AtlasToolInputProperty(type: "string", description: "Sort direction: asc (default) or desc.")
                    ],
                    required: ["path"]
                )
            ),
            SkillActionDefinition(
                id: "fs.read_file",
                name: "Read File",
                description: "Read supported text-like files inside approved folders. Supports line-range slicing for large files.",
                inputSchemaSummary: "path is required; maxCharacters, startLine, and endLine are optional.",
                outputSchemaSummary: "Path, fileType, content, truncation flag, file size, and startLine/endLine when a range was requested.",
                permissionLevel: .read,
                sideEffectLevel: .safeRead,

                preferredQueryTypes: [.localFileRead],
                routingPriority: 45,
                inputSchema: AtlasToolInputSchema(
                    properties: [
                        "path": AtlasToolInputProperty(type: "string", description: "Absolute path inside an approved folder, or a relative path when exactly one approved folder exists."),
                        "maxCharacters": AtlasToolInputProperty(type: "integer", description: "Optional maximum characters to return, capped for safety."),
                        "startLine": AtlasToolInputProperty(type: "integer", description: "1-based line number to start reading from. Useful for reading specific sections of large files."),
                        "endLine": AtlasToolInputProperty(type: "integer", description: "1-based line number to stop reading at (inclusive). Can be used with or without startLine.")
                    ],
                    required: ["path"]
                )
            ),
            SkillActionDefinition(
                id: "fs.search",
                name: "Search Files",
                description: "Search filenames and paths inside an approved folder tree, with optional extension filtering.",
                inputSchemaSummary: "rootPath and query are required; filenameOnly, maxResults, includeHidden, and extensions are optional.",
                outputSchemaSummary: "Structured file search results with path, match type, and entry type, plus a truncated flag.",
                permissionLevel: .read,
                sideEffectLevel: .safeRead,

                preferredQueryTypes: [.localFileSearch],
                routingPriority: 35,
                inputSchema: AtlasToolInputSchema(
                    properties: [
                        "rootPath": AtlasToolInputProperty(type: "string", description: "Root folder inside an approved scope."),
                        "query": AtlasToolInputProperty(type: "string", description: "Case-insensitive filename or path query."),
                        "filenameOnly": AtlasToolInputProperty(type: "boolean", description: "Match only filenames instead of whole relative paths."),
                        "maxResults": AtlasToolInputProperty(type: "integer", description: "Optional maximum results up to 100."),
                        "includeHidden": AtlasToolInputProperty(type: "boolean", description: "Whether hidden files and folders should be included."),
                        "extensions": AtlasToolInputProperty(
                            type: "array",
                            description: "Optional list of file extensions to restrict results to, e.g. [\"swift\", \"md\"]. Applied to files only — folders are unfiltered.",
                            items: AtlasToolInputArrayItems(type: "string", description: "A file extension without a leading dot.")
                        )
                    ],
                    required: ["rootPath", "query"]
                )
            ),
            SkillActionDefinition(
                id: "fs.get_metadata",
                name: "Get Metadata",
                description: "Inspect file or folder metadata inside approved folders without reading file contents.",
                inputSchemaSummary: "path is required.",
                outputSchemaSummary: "Structured metadata including type, size, timestamps, and readability.",
                permissionLevel: .read,
                sideEffectLevel: .safeRead,

                preferredQueryTypes: [.localMetadata],
                routingPriority: 30,
                inputSchema: AtlasToolInputSchema(
                    properties: [
                        "path": AtlasToolInputProperty(type: "string", description: "Absolute path inside an approved folder, or a relative path when exactly one approved folder exists.")
                    ],
                    required: ["path"]
                )
            ),
            SkillActionDefinition(
                id: "fs.content_search",
                name: "Search File Contents",
                description: "Search inside text file contents across an approved folder tree. Supports regex patterns and surrounding context lines.",
                inputSchemaSummary: "rootPath and query are required; extensions, maxResults, includeHidden, contextLines (0–5), and useRegex are optional.",
                outputSchemaSummary: "Matches with file path, line number, matched line, and optional contextBefore/contextAfter lines, plus filesSearched count and truncated flag.",
                permissionLevel: .read,
                sideEffectLevel: .safeRead,

                preferredQueryTypes: [.localFileSearch],
                routingPriority: 38,
                inputSchema: AtlasToolInputSchema(
                    properties: [
                        "rootPath": AtlasToolInputProperty(type: "string", description: "Root folder inside an approved scope."),
                        "query": AtlasToolInputProperty(type: "string", description: "Case-insensitive text or regex pattern to find inside file contents."),
                        "extensions": AtlasToolInputProperty(
                            type: "array",
                            description: "Optional list of file extensions to restrict the search to, e.g. [\"swift\", \"md\"].",
                            items: AtlasToolInputArrayItems(type: "string", description: "A file extension without a leading dot.")
                        ),
                        "maxResults": AtlasToolInputProperty(type: "integer", description: "Optional maximum matches up to 200."),
                        "includeHidden": AtlasToolInputProperty(type: "boolean", description: "Whether hidden files should be included."),
                        "contextLines": AtlasToolInputProperty(type: "integer", description: "Number of surrounding lines to include with each match (0–5). Default is 0."),
                        "useRegex": AtlasToolInputProperty(type: "boolean", description: "Treat query as a case-insensitive regular expression. Default is false.")
                    ],
                    required: ["rootPath", "query"]
                )
            )
        ]
    }

    public func validateConfiguration(context: SkillValidationContext) async -> SkillValidationResult {
        let approvedRootCount = await scopeStore.approvedRootCount()
        let status: SkillValidationStatus = approvedRootCount == 0 ? .warning : .passed
        let summary = approvedRootCount == 0
            ? "File Explorer is ready, but Atlas needs at least one approved folder before it can read local files."
            : "File Explorer is ready with \(approvedRootCount) approved folder\(approvedRootCount == 1 ? "" : "s")."

        return SkillValidationResult(
            skillID: manifest.id,
            status: status,
            summary: summary,
            issues: approvedRootCount == 0 ? ["No approved folders are configured yet."] : [],
            validatedAt: .now
        )
    }

    public func execute(
        actionID: String,
        input: AtlasToolInput,
        context: SkillExecutionContext
    ) async throws -> SkillExecutionResult {
        switch actionID {
        case "fs.list_directory":
            return try await listDirectory(input: input, context: context)
        case "fs.read_file":
            return try await readFile(input: input, context: context)
        case "fs.search":
            return try await search(input: input, context: context)
        case "fs.get_metadata":
            return try await metadata(input: input, context: context)
        case "fs.content_search":
            return try await contentSearch(input: input, context: context)
        default:
            throw AtlasToolError.invalidInput("The action '\(actionID)' is not supported by File Explorer.")
        }
    }

    private func listDirectory(
        input: AtlasToolInput,
        context: SkillExecutionContext
    ) async throws -> SkillExecutionResult {
        let payload = try input.decode(FileSystemListDirectoryInput.self)
        context.logger.info("Executing File Explorer directory listing", metadata: [
            "skill_id": manifest.id,
            "action_id": "fs.list_directory",
            "path": summarizePath(payload.path),
            "recursive": (payload.recursive ?? false) ? "true" : "false"
        ])

        let output = try await reader.listDirectory(input: payload)
        return SkillExecutionResult(
            skillID: manifest.id,
            actionID: "fs.list_directory",
            output: try encode(output),
            summary: "Listed \(output.entries.count) file system entr\(output.entries.count == 1 ? "y" : "ies") in \(summarizePath(payload.path)).",
            metadata: [
                "entry_count": "\(output.entries.count)"
            ]
        )
    }

    private func readFile(
        input: AtlasToolInput,
        context: SkillExecutionContext
    ) async throws -> SkillExecutionResult {
        let payload = try input.decode(FileSystemReadFileInput.self)
        context.logger.info("Executing File Explorer file read", metadata: [
            "skill_id": manifest.id,
            "action_id": "fs.read_file",
            "path": summarizePath(payload.path)
        ])

        let output = try await reader.readFile(input: payload)
        return SkillExecutionResult(
            skillID: manifest.id,
            actionID: "fs.read_file",
            output: try encode(output),
            summary: "Read \(output.fileType.uppercased()) content from \(URL(fileURLWithPath: output.path).lastPathComponent).",
            metadata: [
                "file_type": output.fileType,
                "truncated": output.truncated ? "true" : "false",
                "size": "\(output.size)"
            ]
        )
    }

    private func search(
        input: AtlasToolInput,
        context: SkillExecutionContext
    ) async throws -> SkillExecutionResult {
        let payload = try input.decode(FileSystemSearchInput.self)
        context.logger.info("Executing File Explorer search", metadata: [
            "skill_id": manifest.id,
            "action_id": "fs.search",
            "root_path": summarizePath(payload.rootPath),
            "query": payload.query
        ])

        let output = try await searcher.search(input: payload)
        return SkillExecutionResult(
            skillID: manifest.id,
            actionID: "fs.search",
            output: try encode(output),
            summary: "Found \(output.results.count) file match\(output.results.count == 1 ? "" : "es") for \"\(payload.query)\".",
            metadata: [
                "result_count": "\(output.results.count)"
            ]
        )
    }

    private func metadata(
        input: AtlasToolInput,
        context: SkillExecutionContext
    ) async throws -> SkillExecutionResult {
        let payload = try input.decode(FileSystemGetMetadataInput.self)
        context.logger.info("Executing File Explorer metadata lookup", metadata: [
            "skill_id": manifest.id,
            "action_id": "fs.get_metadata",
            "path": summarizePath(payload.path)
        ])

        let output = try await reader.metadata(for: payload.path)
        return SkillExecutionResult(
            skillID: manifest.id,
            actionID: "fs.get_metadata",
            output: try encode(output),
            summary: "Collected metadata for \(output.name).",
            metadata: [
                "type": output.type.rawValue,
                "readable": output.readable ? "true" : "false"
            ]
        )
    }

    private func contentSearch(
        input: AtlasToolInput,
        context: SkillExecutionContext
    ) async throws -> SkillExecutionResult {
        let payload = try input.decode(FileSystemContentSearchInput.self)
        context.logger.info("Executing File Explorer content search", metadata: [
            "skill_id": manifest.id,
            "action_id": "fs.content_search",
            "root_path": summarizePath(payload.rootPath),
            "query": payload.query
        ])

        let output = try await searcher.contentSearch(input: payload)
        return SkillExecutionResult(
            skillID: manifest.id,
            actionID: "fs.content_search",
            output: try encode(output),
            summary: "Found \(output.matches.count) match\(output.matches.count == 1 ? "" : "es") in \(output.filesSearched) file\(output.filesSearched == 1 ? "" : "s") for \"\(payload.query)\".",
            metadata: [
                "match_count": "\(output.matches.count)",
                "files_searched": "\(output.filesSearched)",
                "truncated": output.truncated ? "true" : "false"
            ]
        )
    }

    private func encode<T: Encodable>(_ value: T) throws -> String {
        let data = try AtlasJSON.encoder.encode(value)
        guard let string = String(data: data, encoding: .utf8) else {
            throw AtlasToolError.executionFailed("Atlas could not encode File Explorer output.")
        }
        return string
    }

    private func summarizePath(_ path: String) -> String {
        let normalized = path.trimmingCharacters(in: .whitespacesAndNewlines)
        if normalized.count <= 120 {
            return normalized
        }
        return String(normalized.prefix(117)) + "..."
    }
}
