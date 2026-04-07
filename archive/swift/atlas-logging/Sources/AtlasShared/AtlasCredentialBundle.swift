import Foundation

/// All Atlas API credentials stored as a single JSON keychain entry.
/// Adding a new integration = add a field here + one case in KeychainSecretStore routing.
public struct AtlasCredentialBundle: Codable, Sendable {
    public var openAIAPIKey: String?
    public var telegramBotToken: String?
    public var discordBotToken: String?
    public var slackBotToken: String?
    public var slackAppToken: String?
    public var openAIImageAPIKey: String?
    public var googleImageAPIKey: String?
    public var braveSearchAPIKey: String?
    public var finnhubAPIKey: String?
    public var alphaVantageAPIKey: String?
    /// Anthropic (Claude) API key for the Claude provider.
    public var anthropicAPIKey: String?
    /// Google Gemini API key for the Gemini provider.
    public var geminiAPIKey: String?
    /// Optional Bearer token for LM Studio authentication (LM Studio v0.4.8+).
    public var lmStudioAPIKey: String?
    /// Auto-generated API key that remote devices must present to authenticate.
    /// Generated as a UUID on first enable; stored in Keychain; never shown in logs.
    public var remoteAccessAPIKey: String?
    /// Arbitrary user-defined API keys stored by name (e.g. "Trackingmore": "<key>").
    public var customSecrets: [String: String]?

    public init(
        openAIAPIKey: String? = nil,
        telegramBotToken: String? = nil,
        discordBotToken: String? = nil,
        slackBotToken: String? = nil,
        slackAppToken: String? = nil,
        openAIImageAPIKey: String? = nil,
        googleImageAPIKey: String? = nil,
        braveSearchAPIKey: String? = nil,
        finnhubAPIKey: String? = nil,
        alphaVantageAPIKey: String? = nil,
        anthropicAPIKey: String? = nil,
        geminiAPIKey: String? = nil,
        lmStudioAPIKey: String? = nil,
        remoteAccessAPIKey: String? = nil,
        customSecrets: [String: String]? = nil
    ) {
        self.openAIAPIKey = openAIAPIKey
        self.telegramBotToken = telegramBotToken
        self.discordBotToken = discordBotToken
        self.slackBotToken = slackBotToken
        self.slackAppToken = slackAppToken
        self.openAIImageAPIKey = openAIImageAPIKey
        self.googleImageAPIKey = googleImageAPIKey
        self.braveSearchAPIKey = braveSearchAPIKey
        self.finnhubAPIKey = finnhubAPIKey
        self.alphaVantageAPIKey = alphaVantageAPIKey
        self.anthropicAPIKey = anthropicAPIKey
        self.geminiAPIKey = geminiAPIKey
        self.lmStudioAPIKey = lmStudioAPIKey
        self.remoteAccessAPIKey = remoteAccessAPIKey
        self.customSecrets = customSecrets
    }
}
