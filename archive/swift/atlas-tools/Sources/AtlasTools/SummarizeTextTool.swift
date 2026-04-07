import Foundation
import AtlasShared

public struct SummarizeTextTool: AtlasTool {
    private struct Arguments: Codable {
        let text: String
    }

    public let toolName = "summarize_text"
    public let description = "Produce a short local summary of the supplied text."
    public let permissionLevel: PermissionLevel = .draft
    public let inputSchema = AtlasToolInputSchema(
        properties: [
            "text": AtlasToolInputProperty(
                type: "string",
                description: "The text that Atlas should summarize locally."
            )
        ],
        required: ["text"]
    )

    public init() {}

    public func execute(input: AtlasToolInput, context: ToolExecutionContext) async throws -> String {
        let arguments = try input.decode(Arguments.self)
        let normalized = arguments.text
            .replacingOccurrences(of: "\n", with: " ")
            .split(whereSeparator: \.isWhitespace)
            .joined(separator: " ")

        let wordCount = normalized.split(separator: " ").count
        let summary = String(normalized.prefix(220))
        let suffix = normalized.count > 220 ? "..." : ""
        return "Summary (\(wordCount) words): \(summary)\(suffix)"
    }
}
