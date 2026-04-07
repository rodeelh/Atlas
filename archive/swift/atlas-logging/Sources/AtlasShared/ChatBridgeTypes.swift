import Foundation

// MARK: - ChatSessionStoring

public protocol ChatSessionStoring: Actor {
    /// Resolves or creates a session for the given platform chat. Pass platformContext on first
    /// creation so it gets stored with the underlying conversation.
    func resolveSession(
        chatID: String,
        userID: String?,
        platformContext: String?
    ) async throws -> ChatSession

    /// Rotates the active conversation for a session (e.g. /reset). Preserves platform context.
    func rotateConversation(
        chatID: String,
        userID: String?,
        platformContext: String?
    ) async throws -> ChatSession

    func session(chatID: String) async throws -> ChatSession?
}

// MARK: - ChatPlatform

public enum ChatPlatform: String, Codable, CaseIterable, Hashable, Sendable {
    case telegram
    case discord
    case slack
    case whatsApp = "whatsapp"
    case companion
}

public enum CommunicationSetupState: String, Codable, CaseIterable, Hashable, Sendable {
    case notStarted = "not_started"
    case missingCredentials = "missing_credentials"
    case partialSetup = "partial_setup"
    case validationFailed = "validation_failed"
    case ready
}

public struct CommunicationDestination: Codable, Hashable, Sendable, Identifiable {
    public let platform: ChatPlatform
    public let channelID: String
    public let channelName: String?
    public let userID: String?
    public let threadID: String?

    public var id: String {
        [
            platform.rawValue,
            channelID,
            threadID ?? ""
        ].joined(separator: ":")
    }

    public init(
        platform: ChatPlatform,
        channelID: String,
        channelName: String? = nil,
        userID: String? = nil,
        threadID: String? = nil
    ) {
        self.platform = platform
        self.channelID = channelID
        self.channelName = channelName
        self.userID = userID
        self.threadID = threadID
    }
}

public struct ChatSession: Codable, Identifiable, Hashable, Sendable {
    public let id: UUID
    public let platform: ChatPlatform
    public let platformChatID: String
    public let platformUserID: String?
    public let platformThreadID: String?
    public let activeConversationID: UUID
    public let createdAt: Date
    public let updatedAt: Date

    public init(
        id: UUID = UUID(),
        platform: ChatPlatform,
        platformChatID: String,
        platformUserID: String?,
        platformThreadID: String? = nil,
        activeConversationID: UUID,
        createdAt: Date = .now,
        updatedAt: Date = .now
    ) {
        self.id = id
        self.platform = platform
        self.platformChatID = platformChatID
        self.platformUserID = platformUserID
        self.platformThreadID = platformThreadID
        self.activeConversationID = activeConversationID
        self.createdAt = createdAt
        self.updatedAt = updatedAt
    }
}

public struct CommunicationChannel: Codable, Identifiable, Hashable, Sendable {
    public let id: String
    public let platform: ChatPlatform
    public let channelID: String
    public let channelName: String?
    public let userID: String?
    public let threadID: String?
    public let activeConversationID: UUID
    public let createdAt: Date
    public let updatedAt: Date
    public let lastMessageID: String?
    public let canReceiveNotifications: Bool

    public init(
        id: String? = nil,
        platform: ChatPlatform,
        channelID: String,
        channelName: String? = nil,
        userID: String? = nil,
        threadID: String? = nil,
        activeConversationID: UUID,
        createdAt: Date = .now,
        updatedAt: Date = .now,
        lastMessageID: String? = nil,
        canReceiveNotifications: Bool = true
    ) {
        self.platform = platform
        self.channelID = channelID
        self.channelName = channelName
        self.userID = userID
        self.threadID = threadID
        self.activeConversationID = activeConversationID
        self.createdAt = createdAt
        self.updatedAt = updatedAt
        self.lastMessageID = lastMessageID
        self.canReceiveNotifications = canReceiveNotifications
        self.id = id ?? [
            platform.rawValue,
            channelID,
            threadID ?? ""
        ].joined(separator: ":")
    }

    public var destination: CommunicationDestination {
        CommunicationDestination(
            platform: platform,
            channelID: channelID,
            channelName: channelName,
            userID: userID,
            threadID: threadID
        )
    }

    public var session: ChatSession {
        ChatSession(
            platform: platform,
            platformChatID: channelID,
            platformUserID: userID,
            platformThreadID: threadID,
            activeConversationID: activeConversationID,
            createdAt: createdAt,
            updatedAt: updatedAt
        )
    }
}

public struct CommunicationPlatformStatus: Codable, Hashable, Sendable, Identifiable {
    public let platform: ChatPlatform
    public let enabled: Bool
    public let connected: Bool
    public let available: Bool
    public let setupState: CommunicationSetupState
    public let statusLabel: String
    public let connectedAccountName: String?
    public let credentialConfigured: Bool
    public let blockingReason: String?
    public let requiredCredentials: [String]
    public let lastError: String?
    public let lastUpdatedAt: Date?
    public let metadata: [String: String]

    public var id: String { platform.rawValue }

    public init(
        platform: ChatPlatform,
        enabled: Bool,
        connected: Bool,
        available: Bool = true,
        setupState: CommunicationSetupState,
        statusLabel: String,
        connectedAccountName: String? = nil,
        credentialConfigured: Bool,
        blockingReason: String? = nil,
        requiredCredentials: [String] = [],
        lastError: String? = nil,
        lastUpdatedAt: Date? = nil,
        metadata: [String: String] = [:]
    ) {
        self.platform = platform
        self.enabled = enabled
        self.connected = connected
        self.available = available
        self.setupState = setupState
        self.statusLabel = statusLabel
        self.connectedAccountName = connectedAccountName
        self.credentialConfigured = credentialConfigured
        self.blockingReason = blockingReason
        self.requiredCredentials = requiredCredentials
        self.lastError = lastError
        self.lastUpdatedAt = lastUpdatedAt
        self.metadata = metadata
    }
}

public struct CommunicationsSnapshot: Codable, Hashable, Sendable {
    public let platforms: [CommunicationPlatformStatus]
    public let channels: [CommunicationChannel]

    public init(
        platforms: [CommunicationPlatformStatus] = [],
        channels: [CommunicationChannel] = []
    ) {
        self.platforms = platforms
        self.channels = channels
    }
}

public struct ChatBridgePersona: Sendable {
    public let platform: ChatPlatform
    /// Appended to the system prompt once at conversation creation. Keep concise.
    public let systemPromptAppend: String

    public init(platform: ChatPlatform, systemPromptAppend: String) {
        self.platform = platform
        self.systemPromptAppend = systemPromptAppend
    }

    public static let telegram = ChatBridgePersona(
        platform: .telegram,
        systemPromptAppend: "Responding via Telegram. Be warm, friendly, and conversational — like texting a knowledgeable friend. Keep replies concise but never cold. Light humor and personality are welcome. Use markdown formatting where it helps: **bold** for key terms, `backticks` for code or commands, and code blocks for multi-line code."
    )

    public static let discord = ChatBridgePersona(
        platform: .discord,
        systemPromptAppend: "Responding via Discord. Keep it casual and punchy. Humor is welcome."
    )

    public static let slack = ChatBridgePersona(
        platform: .slack,
        systemPromptAppend: "Responding via Slack. Be concise, clear, and collaborative. Keep replies thread-friendly."
    )

    public static let whatsApp = ChatBridgePersona(
        platform: .whatsApp,
        systemPromptAppend: "Responding via WhatsApp. Sound like a helpful contact, not a bot. Short messages, plain text only."
    )

    public static let companion = ChatBridgePersona(
        platform: .companion,
        systemPromptAppend: "Responding in the Atlas companion app. User is a power user. Technical depth is fine."
    )
}
