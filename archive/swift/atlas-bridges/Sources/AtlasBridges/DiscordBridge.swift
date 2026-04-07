import AtlasLogging
import AtlasMemory
import AtlasNetwork
import AtlasShared
import Foundation

public struct DiscordBridgeStatus: Sendable {
    public let connected: Bool
    public let botName: String?
    public let botUserID: String?
    public let lastError: String?
    public let lastEventAt: Date?
    public let lastGatewayEventType: String?
    public let lastInboundMessageAt: Date?
    public let hasSeenMessageCreate: Bool

    public init(
        connected: Bool = false,
        botName: String? = nil,
        botUserID: String? = nil,
        lastError: String? = nil,
        lastEventAt: Date? = nil,
        lastGatewayEventType: String? = nil,
        lastInboundMessageAt: Date? = nil,
        hasSeenMessageCreate: Bool = false
    ) {
        self.connected = connected
        self.botName = botName
        self.botUserID = botUserID
        self.lastError = lastError
        self.lastEventAt = lastEventAt
        self.lastGatewayEventType = lastGatewayEventType
        self.lastInboundMessageAt = lastInboundMessageAt
        self.hasSeenMessageCreate = hasSeenMessageCreate
    }
}

public actor DiscordBridge: ChatBridge {
    private static let maxMessageLength = 1900
    private static let messageIntents = 1 << 0 | 1 << 9 | 1 << 12 | 1 << 15

    public let platform: ChatPlatform = .discord
    public let persona: ChatBridgePersona = .discord

    private let config: AtlasConfig
    private let client: any DiscordClienting
    private let runtime: any AtlasRuntimeHandling
    private let sessionStore: CommunicationSessionStore
    private let commandRouter: DiscordCommandRouter
    private let approvalHandler = ChatApprovalHandler()
    private let logger: AtlasLogger

    private var websocket: URLSessionWebSocketTask?
    private var connectionTask: Task<Void, Never>?
    private var heartbeatTask: Task<Void, Never>?
    private var sequenceNumber: Int?
    private var status = DiscordBridgeStatus()

    public var isConnected: Bool { status.connected }

    public init(
        config: AtlasConfig,
        client: any DiscordClienting,
        runtime: any AtlasRuntimeHandling,
        sessionStore: CommunicationSessionStore,
        commandRouter: DiscordCommandRouter? = nil,
        logger: AtlasLogger = AtlasLogger(category: "discord.bridge")
    ) {
        self.config = config
        self.client = client
        self.runtime = runtime
        self.sessionStore = sessionStore
        self.commandRouter = commandRouter ?? DiscordCommandRouter(config: config)
        self.logger = logger
    }

    public func start() async throws {
        guard connectionTask == nil else { return }
        connectionTask = Task {
            await runLoop()
        }
    }

    public func stop() async throws {
        connectionTask?.cancel()
        connectionTask = nil
        heartbeatTask?.cancel()
        heartbeatTask = nil
        websocket?.cancel(with: .normalClosure, reason: nil)
        websocket = nil
        sequenceNumber = nil
        status = DiscordBridgeStatus(
            connected: false,
            botName: status.botName,
            botUserID: status.botUserID,
            lastGatewayEventType: status.lastGatewayEventType,
            lastInboundMessageAt: status.lastInboundMessageAt,
            hasSeenMessageCreate: status.hasSeenMessageCreate
        )
    }

    public func currentStatus() -> DiscordBridgeStatus {
        status
    }

    public func handle(event: DiscordMessageCreateEvent) async {
        guard !event.author.isBot else { return }

        status = status.updating(lastInboundMessageAt: .now)

        logger.info("Received Discord message", metadata: [
            "channel_id": event.channelID,
            "guild_id": event.guildID ?? "dm",
            "message_id": event.id,
            "has_mentions": event.mentions.isEmpty ? "false" : "true"
        ])

        guard let content = normalizedInboundContent(from: event) else {
            return
        }

        if let command = commandRouter.parse(content) {
            await handleCommand(command, rawText: content, event: event)
            return
        }

        await handleUserMessage(content, event: event)
    }

    public func deliverAutomationResult(destination: CommunicationDestination, emoji: String, name: String, output: String) async {
        guard destination.platform == .discord else { return }

        let header = "\(emoji) **\(name)** finished"
        let body = output.trimmingCharacters(in: .whitespacesAndNewlines)
        let message = body.isEmpty ? header : "\(header)\n\n\(body)"
        try? await sendMessage(message, channelID: destination.channelID, replyToMessageID: nil)
    }

    public func notifyApprovalRequired(session: ChatSession, approval: ApprovalRequest) async {
        guard session.platform == .discord else { return }

        let toolName = formatToolName(approval.toolCall.toolName)
        let text = """
        Approval required for **\(toolName)**.

        Reply with:
        `!approve \(approval.toolCallID.uuidString)`
        or
        `!deny \(approval.toolCallID.uuidString)`
        """

        try? await sendMessage(text, channelID: session.platformChatID, replyToMessageID: nil)
    }

    private func runLoop() async {
        var retryDelay: UInt64 = 2

        while !Task.isCancelled {
            do {
                try await connectAndRun()
                retryDelay = 2
            } catch {
                if Task.isCancelled {
                    return
                }
                heartbeatTask?.cancel()
                heartbeatTask = nil
                websocket?.cancel(with: .goingAway, reason: nil)
                websocket = nil
                status = DiscordBridgeStatus(
                    connected: false,
                    botName: status.botName,
                    botUserID: status.botUserID,
                    lastError: error.localizedDescription,
                    lastEventAt: status.lastEventAt,
                    lastGatewayEventType: status.lastGatewayEventType,
                    lastInboundMessageAt: status.lastInboundMessageAt,
                    hasSeenMessageCreate: status.hasSeenMessageCreate
                )
                logger.error("Discord bridge connection failed", metadata: [
                    "error": error.localizedDescription
                ])

                try? await Task.sleep(nanoseconds: retryDelay * 1_000_000_000)
                retryDelay = min(retryDelay * 2, 30)
            }
        }
    }

    private func connectAndRun() async throws {
        let gateway = try await client.getGatewayBot()
        guard let gatewayURL = gateway.gatewayURL else {
            throw DiscordClientError.missingGatewayURL
        }

        let task = client.makeGatewayTask(url: gatewayURL)
        websocket = task
        task.resume()

        let helloEnvelope = try await receiveEnvelope()
        guard helloEnvelope.op == 10,
              let helloPayload = decodePayload(DiscordHelloPayload.self, from: helloEnvelope.d)
        else {
            throw DiscordClientError.invalidResponse
        }

        status = DiscordBridgeStatus(
            connected: false,
            botName: status.botName,
            botUserID: status.botUserID,
            lastError: nil,
            lastEventAt: status.lastEventAt,
            lastGatewayEventType: status.lastGatewayEventType,
            lastInboundMessageAt: status.lastInboundMessageAt,
            hasSeenMessageCreate: status.hasSeenMessageCreate
        )

        startHeartbeat(intervalMS: helloPayload.heartbeatInterval)

        let identify = DiscordGatewayIdentifyPayload(
            token: try config.discordBotToken().trimmingCharacters(in: .whitespacesAndNewlines),
            intents: Self.messageIntents,
            properties: DiscordIdentifyProperties(
                os: "macOS",
                browser: "atlas",
                device: "atlas"
            )
        )
        try await sendGateway(op: 2, payload: identify)

        while !Task.isCancelled {
            let envelope = try await receiveEnvelope()
            if let s = envelope.s {
                sequenceNumber = s
            }

            switch envelope.op {
            case 0:
                try await handleDispatch(envelope)
            case 1:
                try await sendHeartbeat()
            case 7, 9:
                throw DiscordClientError.invalidResponse
            case 10:
                if let hello = decodePayload(DiscordHelloPayload.self, from: envelope.d) {
                    startHeartbeat(intervalMS: hello.heartbeatInterval)
                }
            case 11:
                continue
            default:
                continue
            }
        }
    }

    private func handleDispatch(_ envelope: DiscordGatewayEnvelope) async throws {
        if let eventType = envelope.t {
            status = status.updating(
                lastEventAt: .now,
                lastGatewayEventType: eventType
            )
        }

        if let eventType = envelope.t {
            logger.info("Discord dispatch received", metadata: [
                "event_type": eventType
            ])
        }

        switch envelope.t {
        case "READY":
            guard let ready = decodePayload(DiscordReadyPayload.self, from: envelope.d) else {
                throw DiscordClientError.invalidResponse
            }
            status = DiscordBridgeStatus(
                connected: true,
                botName: ready.user.globalName ?? ready.user.username,
                botUserID: ready.user.id,
                lastError: nil,
                lastEventAt: .now,
                lastGatewayEventType: "READY",
                lastInboundMessageAt: status.lastInboundMessageAt,
                hasSeenMessageCreate: status.hasSeenMessageCreate
            )
            logger.info("Discord gateway ready", metadata: [
                "bot_user_id": ready.user.id
            ])
        case "MESSAGE_CREATE":
            guard let event = decodePayload(DiscordMessageCreateEvent.self, from: envelope.d) else {
                logger.error("Failed to decode Discord MESSAGE_CREATE payload", metadata: [
                    "payload": stringifiedJSONValue(envelope.d) ?? "unavailable"
                ])
                return
            }
            status = DiscordBridgeStatus(
                connected: true,
                botName: status.botName,
                botUserID: status.botUserID,
                lastError: nil,
                lastEventAt: .now,
                lastGatewayEventType: "MESSAGE_CREATE",
                lastInboundMessageAt: status.lastInboundMessageAt,
                hasSeenMessageCreate: true
            )
            await handle(event: event)
        default:
            return
        }
    }

    private func handleCommand(_ command: DiscordCommand, rawText: String, event: DiscordMessageCreateEvent) async {
        let session: ChatSession?
        do {
            session = try await sessionStore.resolveSession(
                platform: .discord,
                chatID: event.channelID,
                userID: event.author.id,
                channelName: derivedChannelName(for: event),
                lastMessageID: event.id,
                platformContext: persona.systemPromptAppend
            )
        } catch {
            do {
                try await sendMessage("I couldn’t open this Discord session. Try again in a moment.", channelID: event.channelID, replyToMessageID: event.id)
            } catch {
                logger.error("Failed to send Discord session error", metadata: [
                    "channel_id": event.channelID,
                    "message_id": event.id,
                    "error": error.localizedDescription
                ])
            }
            return
        }

        guard let session else { return }
        let result = await commandRouter.handle(
            command,
            rawText: rawText,
            event: event,
            runtime: runtime,
            session: session,
            sessionStore: sessionStore,
            approvalHandler: approvalHandler
        )

        do {
            try await sendMessage(result, channelID: event.channelID, replyToMessageID: event.id)
        } catch {
            logger.error("Failed to send Discord command response", metadata: [
                "channel_id": event.channelID,
                "message_id": event.id,
                "error": error.localizedDescription
            ])
        }
    }

    private func handleUserMessage(_ content: String, event: DiscordMessageCreateEvent) async {
        do {
            let session = try await sessionStore.resolveSession(
                platform: .discord,
                chatID: event.channelID,
                userID: event.author.id,
                channelName: derivedChannelName(for: event),
                lastMessageID: event.id,
                platformContext: persona.systemPromptAppend
            )

            let envelope = await runtime.handleMessage(
                AtlasMessageRequest(
                    conversationID: session.activeConversationID,
                    message: content
                )
            )

            let responseText = normalizedDiscordResponse(from: envelope.response)
            try await sendMessage(responseText, channelID: event.channelID, replyToMessageID: event.id)
        } catch {
            logger.error("Failed to route Discord message", metadata: [
                "channel_id": event.channelID,
                "error": error.localizedDescription
            ])
            do {
                try await sendMessage(
                    "Something went wrong on my end. Try again in a moment.",
                    channelID: event.channelID,
                    replyToMessageID: event.id
                )
            } catch {
                logger.error("Failed to send Discord error response", metadata: [
                    "channel_id": event.channelID,
                    "message_id": event.id,
                    "error": error.localizedDescription
                ])
            }
        }
    }

    private func normalizedInboundContent(from event: DiscordMessageCreateEvent) -> String? {
        let trimmed = event.content.trimmingCharacters(in: .whitespacesAndNewlines)
        let isDirectMessage = event.guildID == nil

        if isDirectMessage {
            return trimmed.isEmpty ? nil : trimmed
        }

        let resolvedBotID = status.botUserID ?? event.mentions.first(where: \.isBot)?.id
        guard let selfUserID = resolvedBotID else {
            return nil
        }

        let mentionTokens = ["<@\(selfUserID)>", "<@!\(selfUserID)>"]
        guard mentionTokens.contains(where: trimmed.contains) else {
            return nil
        }

        var cleaned = trimmed
        for token in mentionTokens {
            cleaned = cleaned.replacingOccurrences(of: token, with: "")
        }
        cleaned = cleaned.trimmingCharacters(in: .whitespacesAndNewlines)
        return cleaned.isEmpty ? nil : cleaned
    }

    private func derivedChannelName(for event: DiscordMessageCreateEvent) -> String? {
        if event.guildID == nil {
            return event.author.globalName ?? event.author.username
        }
        return event.author.username
    }

    private func normalizedDiscordResponse(from response: AtlasAgentResponse) -> String {
        let trimmed = response.assistantMessage.trimmingCharacters(in: .whitespacesAndNewlines)
        if !trimmed.isEmpty {
            return trimmed
        }

        switch response.status {
        case .completed:
            return "Done."
        case .waitingForApproval:
            if let pending = response.pendingApprovals.first {
                return """
                I need approval before I can continue.

                Reply with:
                `!approve \(pending.toolCallID.uuidString)`
                or
                `!deny \(pending.toolCallID.uuidString)`
                """
            }
            return "I need approval before I can continue."
        case .failed:
            return response.errorMessage ?? "That ran into a problem."
        }
    }

    private func sendMessage(_ text: String, channelID: String, replyToMessageID: String?) async throws {
        let chunks = splitMessage(text)
        for (index, chunk) in chunks.enumerated() {
            let replyTarget = index == 0 ? replyToMessageID : nil
            do {
                _ = try await client.createMessage(
                    channelID: channelID,
                    content: chunk,
                    replyToMessageID: replyTarget
                )
            } catch {
                guard replyTarget != nil else {
                    throw error
                }
                logger.warning("Discord reply send failed, retrying without reply reference", metadata: [
                    "channel_id": channelID,
                    "error": error.localizedDescription
                ])
                _ = try await client.createMessage(
                    channelID: channelID,
                    content: chunk,
                    replyToMessageID: nil
                )
            }
        }
    }

    private func splitMessage(_ text: String) -> [String] {
        let trimmed = text.trimmingCharacters(in: .whitespacesAndNewlines)
        guard trimmed.count > Self.maxMessageLength else {
            return [trimmed.isEmpty ? "Done." : trimmed]
        }

        var chunks: [String] = []
        var current = ""

        for line in trimmed.components(separatedBy: "\n") {
            if current.count + line.count + 1 > Self.maxMessageLength, !current.isEmpty {
                chunks.append(current.trimmingCharacters(in: .whitespacesAndNewlines))
                current = line
            } else if current.isEmpty {
                current = line
            } else {
                current.append("\n")
                current.append(line)
            }
        }

        if !current.isEmpty {
            chunks.append(current.trimmingCharacters(in: .whitespacesAndNewlines))
        }

        return chunks.isEmpty ? ["Done."] : chunks
    }

    private func startHeartbeat(intervalMS: Int) {
        heartbeatTask?.cancel()
        heartbeatTask = Task {
            let intervalNS = UInt64(max(intervalMS, 1000)) * 1_000_000
            while !Task.isCancelled {
                try? await Task.sleep(nanoseconds: intervalNS)
                try? await sendHeartbeat()
            }
        }
    }

    private func sendHeartbeat() async throws {
        try await sendGateway(op: 1, payload: sequenceNumber)
    }

    private func sendGateway<T: Encodable>(op: Int, payload: T?) async throws {
        guard let websocket else {
            throw DiscordClientError.invalidResponse
        }

        let object = DiscordGatewayOutbound(op: op, d: payload)
        let data = try AtlasJSON.encoder.encode(object)
        guard let json = String(data: data, encoding: .utf8) else {
            throw DiscordClientError.encodingFailed
        }
        try await websocket.send(.string(json))
    }

    private func receiveEnvelope() async throws -> DiscordGatewayEnvelope {
        guard let websocket else {
            throw DiscordClientError.invalidResponse
        }

        let message: URLSessionWebSocketTask.Message
        do {
            message = try await websocket.receive()
        } catch {
            if Task.isCancelled {
                throw CancellationError()
            }
            let closeCode = websocket.closeCode == .invalid ? nil : Int(websocket.closeCode.rawValue)
            let reason = websocket.closeReason.flatMap { String(data: $0, encoding: .utf8) }
            if closeCode == URLSessionWebSocketTask.CloseCode.normalClosure.rawValue && Task.isCancelled {
                throw CancellationError()
            }
            if closeCode != nil || reason != nil {
                throw DiscordClientError.websocketClosed(closeCode, reason)
            }
            throw error
        }
        switch message {
        case .string(let string):
            guard let data = string.data(using: .utf8) else {
                throw DiscordClientError.invalidResponse
            }
            return try AtlasJSON.decoder.decode(DiscordGatewayEnvelope.self, from: data)
        case .data(let data):
            return try AtlasJSON.decoder.decode(DiscordGatewayEnvelope.self, from: data)
        @unknown default:
            throw DiscordClientError.invalidResponse
        }
    }

    private func decodePayload<T: Decodable>(_ type: T.Type, from value: JSONValue?) -> T? {
        guard let value else { return nil }
        guard let data = try? AtlasJSON.encoder.encode(value) else { return nil }
        return try? AtlasJSON.decoder.decode(type, from: data)
    }

    private func formatToolName(_ toolName: String) -> String {
        let parts = toolName.components(separatedBy: "__")
        let raw = parts.last ?? toolName
        return raw
            .components(separatedBy: "_")
            .filter { !$0.isEmpty }
            .map { $0.prefix(1).uppercased() + $0.dropFirst() }
            .joined(separator: " ")
    }

    private func stringifiedJSONValue(_ value: JSONValue?) -> String? {
        guard let value else { return nil }
        guard let data = try? AtlasJSON.encoder.encode(value) else { return nil }
        return String(data: data, encoding: .utf8)
    }
}

private extension DiscordBridgeStatus {
    func updating(
        connected: Bool? = nil,
        botName: String? = nil,
        botUserID: String? = nil,
        lastError: String? = nil,
        lastEventAt: Date? = nil,
        lastGatewayEventType: String? = nil,
        lastInboundMessageAt: Date? = nil,
        hasSeenMessageCreate: Bool? = nil
    ) -> DiscordBridgeStatus {
        DiscordBridgeStatus(
            connected: connected ?? self.connected,
            botName: botName ?? self.botName,
            botUserID: botUserID ?? self.botUserID,
            lastError: lastError ?? self.lastError,
            lastEventAt: lastEventAt ?? self.lastEventAt,
            lastGatewayEventType: lastGatewayEventType ?? self.lastGatewayEventType,
            lastInboundMessageAt: lastInboundMessageAt ?? self.lastInboundMessageAt,
            hasSeenMessageCreate: hasSeenMessageCreate ?? self.hasSeenMessageCreate
        )
    }
}

private struct DiscordGatewayOutbound<T: Encodable>: Encodable {
    let op: Int
    let d: T?
}

private struct DiscordHelloPayload: Decodable {
    let heartbeatInterval: Int

    enum CodingKeys: String, CodingKey {
        case heartbeatInterval = "heartbeat_interval"
    }
}

private struct DiscordReadyPayload: Decodable {
    let sessionID: String
    let user: DiscordCurrentUser

    enum CodingKeys: String, CodingKey {
        case sessionID = "session_id"
        case user
    }
}

public struct DiscordMessageCreateEvent: Codable, Sendable {
    public let id: String
    public let channelID: String
    public let guildID: String?
    public let content: String
    public let author: DiscordMessageAuthor
    public let mentions: [DiscordMessageAuthor]

    public init(
        id: String,
        channelID: String,
        guildID: String?,
        content: String,
        author: DiscordMessageAuthor,
        mentions: [DiscordMessageAuthor]
    ) {
        self.id = id
        self.channelID = channelID
        self.guildID = guildID
        self.content = content
        self.author = author
        self.mentions = mentions
    }

    enum CodingKeys: String, CodingKey {
        case id
        case channelID = "channel_id"
        case guildID = "guild_id"
        case content
        case author
        case mentions
    }
}

public struct DiscordMessageAuthor: Codable, Sendable, Hashable {
    public let id: String
    public let username: String
    public let globalName: String?
    public let isBot: Bool

    public init(id: String, username: String, globalName: String?, isBot: Bool) {
        self.id = id
        self.username = username
        self.globalName = globalName
        self.isBot = isBot
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        id = try container.decode(String.self, forKey: .id)
        username = try container.decode(String.self, forKey: .username)
        globalName = try container.decodeIfPresent(String.self, forKey: .globalName)
        isBot = try container.decodeIfPresent(Bool.self, forKey: .isBot) ?? false
    }

    enum CodingKeys: String, CodingKey {
        case id
        case username
        case globalName = "global_name"
        case isBot = "bot"
    }
}

private struct DiscordIdentifyProperties: Codable {
    let os: String
    let browser: String
    let device: String

    enum CodingKeys: String, CodingKey {
        case os = "$os"
        case browser = "$browser"
        case device = "$device"
    }
}

private struct DiscordGatewayIdentifyPayload: Codable {
    let token: String
    let intents: Int
    let properties: DiscordIdentifyProperties
}

private struct DiscordGatewayEnvelope: Decodable {
    let op: Int
    let d: JSONValue?
    let s: Int?
    let t: String?
}

private indirect enum JSONValue: Codable {
    case string(String)
    case number(Double)
    case bool(Bool)
    case object([String: JSONValue])
    case array([JSONValue])
    case null

    init(from decoder: Decoder) throws {
        let container = try decoder.singleValueContainer()
        if container.decodeNil() {
            self = .null
        } else if let value = try? container.decode(Bool.self) {
            self = .bool(value)
        } else if let value = try? container.decode(Double.self) {
            self = .number(value)
        } else if let value = try? container.decode(String.self) {
            self = .string(value)
        } else if let value = try? container.decode([String: JSONValue].self) {
            self = .object(value)
        } else if let value = try? container.decode([JSONValue].self) {
            self = .array(value)
        } else {
            throw DecodingError.dataCorruptedError(in: container, debugDescription: "Unsupported JSON value")
        }
    }

    func encode(to encoder: Encoder) throws {
        var container = encoder.singleValueContainer()
        switch self {
        case .string(let value):
            try container.encode(value)
        case .number(let value):
            try container.encode(value)
        case .bool(let value):
            try container.encode(value)
        case .object(let value):
            try container.encode(value)
        case .array(let value):
            try container.encode(value)
        case .null:
            try container.encodeNil()
        }
    }
}
