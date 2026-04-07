import Foundation

// MARK: - Request

struct AnthropicMessagesRequest: Encodable {
    let model: String
    let maxTokens: Int
    let system: String?
    let messages: [AnthropicMessage]
    let tools: [AnthropicTool]?
    let stream: Bool?

    enum CodingKeys: String, CodingKey {
        case model
        case maxTokens = "max_tokens"
        case system, messages, tools, stream
    }
}

struct AnthropicMessage: Encodable {
    let role: String   // "user" or "assistant"
    let content: AnthropicMessageContent
}

/// Content is either a plain string (convenience) or an array of typed content blocks.
enum AnthropicMessageContent: Encodable {
    case text(String)
    case blocks([AnthropicContentBlock])

    func encode(to encoder: Encoder) throws {
        var container = encoder.singleValueContainer()
        switch self {
        case .text(let s):   try container.encode(s)
        case .blocks(let b): try container.encode(b)
        }
    }
}

struct AnthropicContentBlock: Encodable {
    let type: String

    // text block
    var text: String?

    // tool_use block (assistant requesting a tool)
    var id: String?
    var name: String?
    var input: [String: AnthropicJSONValue]?

    // tool_result block (user providing a tool result)
    var toolUseID: String?
    var content: String?
    var isError: Bool?

    enum CodingKeys: String, CodingKey {
        case type, text, id, name, input
        case toolUseID = "tool_use_id"
        case content
        case isError = "is_error"
    }

    static func textBlock(_ text: String) -> AnthropicContentBlock {
        AnthropicContentBlock(type: "text", text: text)
    }

    static func toolUseBlock(id: String, name: String, input: [String: AnthropicJSONValue]) -> AnthropicContentBlock {
        AnthropicContentBlock(type: "tool_use", id: id, name: name, input: input)
    }

    static func toolResultBlock(toolUseID: String, content: String, isError: Bool = false) -> AnthropicContentBlock {
        AnthropicContentBlock(
            type: "tool_result",
            toolUseID: toolUseID,
            content: content,
            isError: isError ? true : nil
        )
    }
}

/// Recursive JSON value for Anthropic tool inputs.
indirect enum AnthropicJSONValue: Codable {
    case string(String)
    case number(Double)
    case bool(Bool)
    case null
    case array([AnthropicJSONValue])
    case object([String: AnthropicJSONValue])

    init(_ any: Any) {
        switch any {
        case let s as String:  self = .string(s)
        case let b as Bool:    self = .bool(b)
        case let n as NSNumber:
            if CFGetTypeID(n) == CFBooleanGetTypeID() {
                self = .bool(n.boolValue)
            } else {
                self = .number(n.doubleValue)
            }
        case let a as [Any]:             self = .array(a.map { AnthropicJSONValue($0) })
        case let d as [String: Any]:     self = .object(d.mapValues { AnthropicJSONValue($0) })
        default: self = .null
        }
    }

    func encode(to encoder: Encoder) throws {
        var c = encoder.singleValueContainer()
        switch self {
        case .string(let s):  try c.encode(s)
        case .number(let n):  try c.encode(n)
        case .bool(let b):    try c.encode(b)
        case .null:           try c.encodeNil()
        case .array(let a):   try c.encode(a)
        case .object(let o):  try c.encode(o)
        }
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.singleValueContainer()
        if let b = try? c.decode(Bool.self) { self = .bool(b);   return }
        if let n = try? c.decode(Double.self) { self = .number(n); return }
        if let s = try? c.decode(String.self) { self = .string(s); return }
        if let a = try? c.decode([AnthropicJSONValue].self) { self = .array(a);  return }
        if let o = try? c.decode([String: AnthropicJSONValue].self) { self = .object(o); return }
        if c.decodeNil() { self = .null; return }
        throw DecodingError.dataCorruptedError(in: c, debugDescription: "Cannot decode AnthropicJSONValue")
    }

    /// Build the input dict from a JSON string (arguments from the model).
    static func dict(fromJSONString jsonString: String) -> [String: AnthropicJSONValue] {
        guard
            let data = jsonString.data(using: .utf8),
            let raw  = try? JSONSerialization.jsonObject(with: data) as? [String: Any]
        else { return [:] }
        return raw.mapValues { AnthropicJSONValue($0) }
    }
}

struct AnthropicTool: Encodable {
    let name: String
    let description: String
    let inputSchema: InputSchema

    struct InputSchema: Encodable {
        let type = "object"
        let properties: [String: PropertySchema]
        let required: [String]
    }

    struct PropertySchema: Encodable {
        let type: String
        let description: String
    }

    enum CodingKeys: String, CodingKey {
        case name, description
        case inputSchema = "input_schema"
    }
}

// MARK: - Response

struct AnthropicMessagesResponse: Decodable {
    let id: String
    let content: [AnthropicResponseBlock]
    let stopReason: String?

    enum CodingKeys: String, CodingKey {
        case id, content
        case stopReason = "stop_reason"
    }
}

struct AnthropicResponseBlock: Decodable {
    let type: String
    let text: String?
    let id: String?
    let name: String?
    let input: [String: AnthropicJSONValue]?
}

// MARK: - Streaming events

struct AnthropicStreamEvent: Decodable {
    let type: String
    let index: Int?
    let delta: AnthropicStreamDelta?
    let contentBlock: AnthropicStreamContentBlock?

    enum CodingKeys: String, CodingKey {
        case type, index, delta
        case contentBlock = "content_block"
    }
}

struct AnthropicStreamDelta: Decodable {
    let type: String?
    let text: String?
    let partialJson: String?

    enum CodingKeys: String, CodingKey {
        case type, text
        case partialJson = "partial_json"
    }
}

struct AnthropicStreamContentBlock: Decodable {
    let type: String
    let id: String?
    let name: String?
}

struct AnthropicErrorEnvelope: Decodable {
    let error: AnthropicError
    struct AnthropicError: Decodable {
        let message: String?
    }
}
