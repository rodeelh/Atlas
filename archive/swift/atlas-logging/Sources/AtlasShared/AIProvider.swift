import Foundation

/// Selects the AI provider used for all agent conversations and internal tasks.
public enum AIProvider: String, Codable, CaseIterable, Hashable, Sendable, Identifiable {
    case openAI = "openai"
    case anthropic = "anthropic"
    case gemini = "gemini"
    case lmStudio = "lm_studio"

    public var id: String { rawValue }

    public var displayName: String {
        switch self {
        case .openAI:    return "OpenAI"
        case .anthropic: return "Claude (Anthropic)"
        case .gemini:    return "Gemini (Google)"
        case .lmStudio:  return "LM Studio (Local)"
        }
    }

    public var shortName: String {
        switch self {
        case .openAI:    return "OpenAI"
        case .anthropic: return "Claude"
        case .gemini:    return "Gemini"
        case .lmStudio:  return "LM Studio"
        }
    }

    /// Whether this provider requires an API key to be stored in Keychain.
    public var requiresAPIKey: Bool {
        switch self {
        case .openAI, .anthropic, .gemini: return true
        case .lmStudio: return false
        }
    }

    /// Default primary (flagship) model for the provider.
    public var defaultPrimaryModel: String {
        switch self {
        case .openAI:    return "gpt-4.1"
        case .anthropic: return "claude-sonnet-4-6"
        case .gemini:    return "gemini-2.0-flash"
        case .lmStudio:  return ""
        }
    }

    /// Default fast (lightweight) model used for internal tasks like MIND.md reflection.
    public var defaultFastModel: String {
        switch self {
        case .openAI:    return "gpt-4.1-mini"
        case .anthropic: return "claude-haiku-4-5-20251001"
        case .gemini:    return "gemini-2.0-flash-lite"
        case .lmStudio:  return ""
        }
    }

    /// Static model catalog for providers that don't support dynamic listing.
    /// Returns empty for OpenAI and LM Studio — those use dynamic discovery.
    public var staticModels: [AIModelRecord] {
        switch self {
        case .openAI, .lmStudio:
            return []
        case .anthropic:
            return [
                AIModelRecord(id: "claude-opus-4-6", displayName: "Claude Opus 4.6", isFast: false),
                AIModelRecord(id: "claude-sonnet-4-6", displayName: "Claude Sonnet 4.6", isFast: false),
                AIModelRecord(id: "claude-haiku-4-5-20251001", displayName: "Claude Haiku 4.5", isFast: true)
            ]
        case .gemini:
            return [
                AIModelRecord(id: "gemini-2.0-flash", displayName: "Gemini 2.0 Flash", isFast: false),
                AIModelRecord(id: "gemini-2.0-flash-lite", displayName: "Gemini 2.0 Flash Lite", isFast: true),
                AIModelRecord(id: "gemini-1.5-pro", displayName: "Gemini 1.5 Pro", isFast: false),
                AIModelRecord(id: "gemini-1.5-flash", displayName: "Gemini 1.5 Flash", isFast: true)
            ]
        }
    }
}

/// A model entry in the provider model catalog.
public struct AIModelRecord: Sendable, Identifiable, Hashable, Encodable {
    public let id: String
    public let displayName: String
    /// Whether this is a lightweight model suitable for internal reflection tasks.
    public let isFast: Bool

    public init(id: String, displayName: String, isFast: Bool) {
        self.id = id
        self.displayName = displayName
        self.isFast = isFast
    }
}
