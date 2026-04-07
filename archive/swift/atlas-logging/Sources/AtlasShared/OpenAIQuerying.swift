import Foundation

/// Minimal protocol for sending a single-turn prompt to an LLM.
/// Defined in AtlasShared to avoid circular dependencies:
/// atlas-memory needs to call OpenAI without depending on atlas-network.
/// OpenAIClient (atlas-network) conforms; the conforming object is injected at startup.
public protocol OpenAIQuerying: Sendable {
    /// Send a single-turn request: system prompt + user content → assistant text.
    /// Pass a specific `model` to override the default; nil uses the client's fallback.
    func complete(systemPrompt: String, userContent: String, model: String?) async throws -> String
}

public extension OpenAIQuerying {
    /// Convenience overload without an explicit model — uses the client's fallback.
    func complete(systemPrompt: String, userContent: String) async throws -> String {
        try await complete(systemPrompt: systemPrompt, userContent: userContent, model: nil)
    }
}
