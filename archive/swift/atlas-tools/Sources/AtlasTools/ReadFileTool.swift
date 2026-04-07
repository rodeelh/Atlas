import Foundation
import AtlasShared

public struct ReadFileTool: AtlasTool {
    private struct Arguments: Codable {
        let path: String
    }

    public let toolName = "read_file"
    public let description = "Read a UTF-8 text file from the Atlas sandbox directory."
    public let permissionLevel: PermissionLevel = .read
    public let inputSchema = AtlasToolInputSchema(
        properties: [
            "path": AtlasToolInputProperty(
                type: "string",
                description: "Relative or absolute path to a UTF-8 text file within the Atlas sandbox."
            )
        ],
        required: ["path"]
    )

    public init() {}

    public func execute(input: AtlasToolInput, context: ToolExecutionContext) async throws -> String {
        let arguments = try input.decode(Arguments.self)
        let url = try await context.resolveScopedURL(for: arguments.path)
        let contents = try String(contentsOf: url, encoding: .utf8)
        return contents
    }
}
