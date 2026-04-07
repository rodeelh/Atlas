public struct OpenAIResponsesCreateResponse: Decodable, Hashable, Sendable {
    public let id: String
    public let output: [OpenAIOutputItem]

    public init(id: String, output: [OpenAIOutputItem]) {
        self.id = id
        self.output = output
    }

    public func latestAssistantMessageText() -> String {
        output
            .reversed()
            .first { $0.type == "message" && ($0.role == nil || $0.role == "assistant") }?
            .content
            .compactMap(\.text)
            .joined(separator: "\n")
            .trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
    }
}

public struct OpenAIOutputItem: Decodable, Hashable, Sendable {
    public let id: String?
    public let type: String
    public let role: String?
    public let content: [OpenAIOutputContent]
    public let name: String?
    public let arguments: String?
    public let callID: String?
    public let status: String?

    enum CodingKeys: String, CodingKey {
        case id
        case type
        case role
        case content
        case name
        case arguments
        case callID = "call_id"
        case status
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        id = try container.decodeIfPresent(String.self, forKey: .id)
        type = try container.decode(String.self, forKey: .type)
        role = try container.decodeIfPresent(String.self, forKey: .role)
        content = try container.decodeIfPresent([OpenAIOutputContent].self, forKey: .content) ?? []
        name = try container.decodeIfPresent(String.self, forKey: .name)
        arguments = try container.decodeIfPresent(String.self, forKey: .arguments)
        callID = try container.decodeIfPresent(String.self, forKey: .callID)
        status = try container.decodeIfPresent(String.self, forKey: .status)
    }

    /// Factory for a synthetic message output item (used in streaming fallback).
    public static func makeMessage(text: String) -> OpenAIOutputItem {
        OpenAIOutputItem(
            id: nil, type: "message", role: "assistant",
            content: [OpenAIOutputContent(type: "output_text", text: text)],
            name: nil, arguments: nil, callID: nil, status: "completed"
        )
    }

    /// Factory for a synthetic function_call output item (used in streaming fallback).
    public static func makeFunctionCall(id: String, name: String, arguments: String) -> OpenAIOutputItem {
        OpenAIOutputItem(
            id: id, type: "function_call", role: nil,
            content: [],
            name: name, arguments: arguments, callID: id, status: "completed"
        )
    }

    /// Memberwise init for constructing synthetic items in tests and streaming fallbacks.
    public init(
        id: String?, type: String, role: String?,
        content: [OpenAIOutputContent],
        name: String?, arguments: String?, callID: String?, status: String?
    ) {
        self.id        = id
        self.type      = type
        self.role      = role
        self.content   = content
        self.name      = name
        self.arguments = arguments
        self.callID    = callID
        self.status    = status
    }
}

public struct OpenAIOutputContent: Decodable, Hashable, Sendable {
    public let type: String
    public let text: String?

    public init(type: String, text: String?) {
        self.type = type
        self.text = text
    }
}

public struct OpenAIErrorEnvelope: Decodable, Sendable {
    public let error: OpenAIErrorDetails
}

public struct OpenAIErrorDetails: Decodable, Sendable {
    public let message: String?
    public let type: String?
}

// MARK: - Streaming event

/// A single SSE event emitted by the OpenAI Responses API when `stream: true`.
/// Only the fields relevant to Atlas are decoded; unknown fields are silently ignored.
public struct OpenAIStreamEvent: Decodable, Sendable {
    /// Event type, e.g. "response.output_text.delta" or "response.completed".
    public let type: String
    /// Text delta — present when `type == "response.output_text.delta"`.
    public let delta: String?
    /// Full response — present when `type == "response.completed"`.
    public let response: OpenAIResponsesCreateResponse?
}
