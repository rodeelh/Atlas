import AtlasShared

public struct OpenAIReasoningConfig: Encodable, Sendable {
    public enum Effort: String, Encodable, Sendable {
        case low, medium, high
    }
    public let effort: Effort
}

public struct OpenAIResponsesCreateRequest: Encodable, Sendable {
    public let model: String
    public let instructions: String?
    public let input: [OpenAIInputItem]
    public let tools: [OpenAIToolDefinition]?
    public let store: Bool
    public let previousResponseID: String?
    /// When `true`, the API streams SSE events instead of returning a single response body.
    public let stream: Bool
    /// Reasoning effort for models that support it (e.g. gpt-oss). `nil` = not sent.
    public let reasoning: OpenAIReasoningConfig?

    enum CodingKeys: String, CodingKey {
        case model
        case instructions
        case input
        case tools
        case store
        case previousResponseID = "previous_response_id"
        case stream
        case reasoning
    }

    public init(
        model: String,
        instructions: String?,
        input: [OpenAIInputItem],
        tools: [OpenAIToolDefinition]? = nil,
        store: Bool = false,
        previousResponseID: String? = nil,
        stream: Bool = false,
        reasoning: OpenAIReasoningConfig? = nil
    ) {
        self.model = model
        self.instructions = instructions
        self.input = input
        self.tools = tools
        self.store = store
        self.previousResponseID = previousResponseID
        self.stream = stream
        self.reasoning = reasoning
    }
}

public enum OpenAIInputItem: Sendable {
    case message(OpenAIConversationInput)
    case functionCallOutput(OpenAIFunctionCallOutputInput)
}

extension OpenAIInputItem: Encodable {
    public func encode(to encoder: Encoder) throws {
        switch self {
        case .message(let message):
            try message.encode(to: encoder)
        case .functionCallOutput(let output):
            try output.encode(to: encoder)
        }
    }
}

public struct OpenAIConversationInput: Encodable, Sendable {
    public let type = "message"
    public let role: String
    public let content: [OpenAIMessageContent]

    /// Multi-content init: mix of text and image items.
    public init(role: String, content: [OpenAIMessageContent]) {
        self.role = role
        self.content = content
    }

    /// Convenience init for single-text messages (backward compatible).
    public init(role: String, text: String, contentType: OpenAIMessageContent.TextType = .inputText) {
        self.role = role
        self.content = [.text(text, contentType)]
    }
}

/// Polymorphic content item in an OpenAI message.
/// - `.text` maps to `input_text` / `output_text`
/// - `.image` maps to `input_image` (base64 data URI or HTTPS URL)
public enum OpenAIMessageContent: Encodable, Sendable {
    public enum TextType: String, Encodable, Sendable {
        case inputText  = "input_text"
        case outputText = "output_text"
    }

    case text(String, TextType)
    case image(String)  // data URI ("data:image/...;base64,...") or HTTPS URL

    public func encode(to encoder: Encoder) throws {
        switch self {
        case .text(let text, let textType):
            struct TextPayload: Encodable {
                let type: TextType
                let text: String
            }
            try TextPayload(type: textType, text: text).encode(to: encoder)
        case .image(let url):
            struct ImagePayload: Encodable {
                let type = "input_image"
                let imageURL: String
                enum CodingKeys: String, CodingKey {
                    case type, imageURL = "image_url"
                }
            }
            try ImagePayload(imageURL: url).encode(to: encoder)
        }
    }
}

public struct OpenAIFunctionCallOutputInput: Encodable, Sendable {
    public let type = "function_call_output"
    public let callID: String
    public let output: String

    enum CodingKeys: String, CodingKey {
        case type
        case callID = "call_id"
        case output
    }

    public init(callID: String, output: String) {
        self.callID = callID
        self.output = output
    }
}

public struct OpenAIToolDefinition: Encodable, Sendable {
    public let type = "function"
    public let name: String
    public let description: String
    public let parameters: AtlasToolInputSchema

    public init(definition: AtlasToolDefinition) {
        self.name = definition.name
        self.description = definition.description
        self.parameters = definition.inputSchema
    }
}

public struct OpenAIToolOutput: Sendable {
    public let callID: String
    public let output: String

    public init(callID: String, output: String) {
        self.callID = callID
        self.output = output
    }
}
