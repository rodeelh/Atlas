import Foundation

// MARK: - Request

struct GeminiGenerateContentRequest: Encodable {
    let systemInstruction: GeminiContent?
    let contents: [GeminiContent]
    let tools: [GeminiTools]?

    enum CodingKeys: String, CodingKey {
        case systemInstruction = "system_instruction"
        case contents, tools
    }
}

struct GeminiContent: Codable {
    let role: String?
    let parts: [GeminiPart]
}

struct GeminiPart: Codable {
    // text part
    var text: String?
    // functionCall part (model output)
    var functionCall: GeminiFunctionCall?
    // functionResponse part (user input)
    var functionResponse: GeminiFunctionResponse?

    enum CodingKeys: String, CodingKey {
        case text
        case functionCall     = "functionCall"
        case functionResponse = "functionResponse"
    }

    static func textPart(_ text: String) -> GeminiPart {
        GeminiPart(text: text)
    }

    static func functionCallPart(name: String, args: [String: GeminiJSONValue]) -> GeminiPart {
        GeminiPart(functionCall: GeminiFunctionCall(name: name, args: args))
    }

    static func functionResponsePart(name: String, response: String) -> GeminiPart {
        GeminiPart(functionResponse: GeminiFunctionResponse(
            name: name,
            response: ["content": .string(response)]
        ))
    }
}

struct GeminiFunctionCall: Codable {
    let name: String
    let args: [String: GeminiJSONValue]
}

struct GeminiFunctionResponse: Codable {
    let name: String
    let response: [String: GeminiJSONValue]
}

struct GeminiTools: Encodable {
    let functionDeclarations: [GeminiFunctionDeclaration]

    enum CodingKeys: String, CodingKey {
        case functionDeclarations = "function_declarations"
    }
}

struct GeminiFunctionDeclaration: Encodable {
    let name: String
    let description: String
    let parameters: GeminiParametersSchema
}

struct GeminiParametersSchema: Encodable {
    let type = "OBJECT"
    let properties: [String: GeminiPropertySchema]
    let required: [String]
}

struct GeminiPropertySchema: Encodable {
    let type: String
    let description: String
    let items: GeminiItemsSchema?

    init(type: String, description: String, items: GeminiItemsSchema? = nil) {
        self.type = type
        self.description = description
        self.items = items
    }

    // Gemini uses uppercase type strings
    var geminiType: String {
        switch type.lowercased() {
        case "string":  return "STRING"
        case "number":  return "NUMBER"
        case "integer": return "INTEGER"
        case "boolean": return "BOOLEAN"
        case "array":   return "ARRAY"
        case "object":  return "OBJECT"
        default:        return "STRING"
        }
    }

    func encode(to encoder: Encoder) throws {
        var container = encoder.container(keyedBy: CodingKeys.self)
        try container.encode(geminiType, forKey: .type)
        try container.encode(description, forKey: .description)
        if let items {
            try container.encode(items, forKey: .items)
        }
    }

    enum CodingKeys: String, CodingKey { case type, description, items }
}

struct GeminiItemsSchema: Encodable {
    let type: String

    var geminiType: String {
        switch type.lowercased() {
        case "string":  return "STRING"
        case "number":  return "NUMBER"
        case "integer": return "INTEGER"
        case "boolean": return "BOOLEAN"
        case "array":   return "ARRAY"
        case "object":  return "OBJECT"
        default:        return "STRING"
        }
    }

    func encode(to encoder: Encoder) throws {
        var container = encoder.container(keyedBy: CodingKeys.self)
        try container.encode(geminiType, forKey: .type)
    }

    enum CodingKeys: String, CodingKey { case type }
}

/// Recursive JSON value for Gemini function args/response payloads.
indirect enum GeminiJSONValue: Codable {
    case string(String)
    case number(Double)
    case bool(Bool)
    case null
    case array([GeminiJSONValue])
    case object([String: GeminiJSONValue])

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
        case let a as [Any]:         self = .array(a.map { GeminiJSONValue($0) })
        case let d as [String: Any]: self = .object(d.mapValues { GeminiJSONValue($0) })
        default: self = .null
        }
    }

    func encode(to encoder: Encoder) throws {
        var c = encoder.singleValueContainer()
        switch self {
        case .string(let s): try c.encode(s)
        case .number(let n): try c.encode(n)
        case .bool(let b):   try c.encode(b)
        case .null:          try c.encodeNil()
        case .array(let a):  try c.encode(a)
        case .object(let o): try c.encode(o)
        }
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.singleValueContainer()
        if let b = try? c.decode(Bool.self) { self = .bool(b);   return }
        if let n = try? c.decode(Double.self) { self = .number(n); return }
        if let s = try? c.decode(String.self) { self = .string(s); return }
        if let a = try? c.decode([GeminiJSONValue].self) { self = .array(a);  return }
        if let o = try? c.decode([String: GeminiJSONValue].self) { self = .object(o); return }
        if c.decodeNil() { self = .null; return }
        throw DecodingError.dataCorruptedError(in: c, debugDescription: "Cannot decode GeminiJSONValue")
    }

    static func dict(fromJSONString jsonString: String) -> [String: GeminiJSONValue] {
        guard
            let data = jsonString.data(using: .utf8),
            let raw  = try? JSONSerialization.jsonObject(with: data) as? [String: Any]
        else { return [:] }
        return raw.mapValues { GeminiJSONValue($0) }
    }
}

// MARK: - Response

struct GeminiGenerateContentResponse: Decodable {
    let candidates: [GeminiCandidate]?
}

struct GeminiCandidate: Decodable {
    let content: GeminiContent?
    let finishReason: String?

    enum CodingKeys: String, CodingKey {
        case content
        case finishReason = "finishReason"
    }
}

// MARK: - Streaming

struct GeminiStreamChunk: Decodable {
    let candidates: [GeminiCandidate]?
}

struct GeminiErrorEnvelope: Decodable {
    let error: GeminiError
    struct GeminiError: Decodable {
        let message: String?
    }
}
