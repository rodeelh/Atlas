import Foundation
import AtlasShared

public struct ListDirectoryTool: AtlasTool {
    private struct Arguments: Codable {
        let path: String?
    }

    public let toolName = "list_directory"
    public let description = "List the contents of a directory inside the Atlas sandbox."
    public let permissionLevel: PermissionLevel = .read
    public let inputSchema = AtlasToolInputSchema(
        properties: [
            "path": AtlasToolInputProperty(
                type: "string",
                description: "Optional relative or absolute directory path inside the Atlas sandbox. Defaults to the sandbox root."
            )
        ],
        required: []
    )

    public init() {}

    public func execute(input: AtlasToolInput, context: ToolExecutionContext) async throws -> String {
        let arguments = (try? input.decode(Arguments.self)) ?? Arguments(path: nil)
        let url = try await context.resolveScopedURL(for: arguments.path)
        let entries = try FileManager.default.contentsOfDirectory(
            at: url,
            includingPropertiesForKeys: [.isDirectoryKey],
            options: [.skipsHiddenFiles]
        )

        let listing = try entries
            .sorted { $0.lastPathComponent < $1.lastPathComponent }
            .map { entry -> String in
                let values = try entry.resourceValues(forKeys: [.isDirectoryKey])
                return values.isDirectory == true
                    ? "\(entry.lastPathComponent)/"
                    : entry.lastPathComponent
            }
            .joined(separator: "\n")

        return listing.isEmpty ? "(empty directory)" : listing
    }
}
