import AtlasLogging
import AtlasMemory
import AtlasNetwork
import AtlasShared
import Foundation

public struct SlackBridgeStatus: Sendable {
    public let connected: Bool
    public let botName: String?
    public let botUserID: String?
    public let workspaceName: String?
    public let lastError: String?
    public let lastEventAt: Date?

    public init(
        connected: Bool = false,
        botName: String? = nil,
        botUserID: String? = nil,
        workspaceName: String? = nil,
        lastError: String? = nil,
        lastEventAt: Date? = nil
    ) {
        self.connected = connected
        self.botName = botName
        self.botUserID = botUserID
        self.workspaceName = workspaceName
        self.lastError = lastError
        self.lastEventAt = lastEventAt
    }
}

public struct SlackMessageEvent: Codable, Sendable {
    public let type: String
    public let channel: String
    public let channelType: String?
    public let user: String?
    public let text: String?
    public let ts: String
    public let threadTS: String?
    public let subtype: String?
    public let botID: String?

    public init(
        type: String,
        channel: String,
        channelType: String? = nil,
        user: String? = nil,
        text: String? = nil,
        ts: String,
        threadTS: String? = nil,
        subtype: String? = nil,
        botID: String? = nil
    ) {
        self.type = type
        self.channel = channel
        self.channelType = channelType
        self.user = user
        self.text = text
        self.ts = ts
        self.threadTS = threadTS
        self.subtype = subtype
        self.botID = botID
    }

    enum CodingKeys: String, CodingKey {
        case type
        case channel
        case channelType = "channel_type"
        case user
        case text
        case ts
        case threadTS = "thread_ts"
        case subtype
        case botID = "bot_id"
    }
}

public actor SlackBridge: ChatBridge {
    private static let maxMessageLength = 3000

    public let platform: ChatPlatform = .slack
    public let persona: ChatBridgePersona = .slack

    private let config: AtlasConfig
    private let client: any SlackClienting
    private let runtime: any AtlasRuntimeHandling
    private let sessionStore: CommunicationSessionStore
    private let approvalHandler = ChatApprovalHandler()
    private let logger: AtlasLogger

    private var websocket: URLSessionWebSocketTask?
    private var connectionTask: Task<Void, Never>?
    private var status = SlackBridgeStatus()

    public var isConnected: Bool { status.connected }

    public init(
        config: AtlasConfig,
        client: any SlackClienting,
        runtime: any AtlasRuntimeHandling,
        sessionStore: CommunicationSessionStore,
        logger: AtlasLogger = AtlasLogger(category: "slack.bridge")
    ) {
        self.config = config
        self.client = client
        self.runtime = runtime
        self.sessionStore = sessionStore
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
        websocket?.cancel(with: .normalClosure, reason: nil)
        websocket = nil
        status = SlackBridgeStatus(
            connected: false,
            botName: status.botName,
            botUserID: status.botUserID,
            workspaceName: status.workspaceName,
            lastError: nil,
            lastEventAt: status.lastEventAt
        )
    }

    public func currentStatus() -> SlackBridgeStatus {
        status
    }

    public func handle(event: SlackMessageEvent) async {
        guard event.botID == nil, event.subtype == nil else { return }

        logger.info("Received Slack event", metadata: [
            "event_type": event.type,
            "channel_id": event.channel,
            "channel_type": event.channelType ?? "unknown",
            "user_id": event.user ?? "unknown",
            "thread_ts": event.threadTS ?? "none",
            "ts": event.ts
        ])

        if let commandResponse = await maybeHandleCommand(for: event) {
            try? await sendMessage(commandResponse, channelID: event.channel, threadID: replyThreadID(for: event))
            return
        }

        guard let content = normalizedInboundContent(from: event) else {
            return
        }

        do {
            let session = try await sessionStore.resolveSession(
                platform: .slack,
                chatID: event.channel,
                userID: event.user,
                threadID: sessionThreadID(for: event),
                channelName: event.channel,
                lastMessageID: event.ts,
                platformContext: persona.systemPromptAppend
            )

            let envelope = await runtime.handleMessage(
                AtlasMessageRequest(
                    conversationID: session.activeConversationID,
                    message: content
                )
            )

            let responseText = normalizedSlackResponse(from: envelope.response)
            try await sendMessage(responseText, channelID: event.channel, threadID: replyThreadID(for: event))
            logger.info("Sent Slack response", metadata: [
                "channel_id": event.channel,
                "thread_ts": replyThreadID(for: event) ?? "none"
            ])
        } catch {
            logger.error("Failed to route Slack message", metadata: [
                "channel_id": event.channel,
                "error": error.localizedDescription
            ])
            try? await sendMessage(
                "Something went wrong on my end. Try again in a moment.",
                channelID: event.channel,
                threadID: replyThreadID(for: event)
            )
        }
    }

    public func deliverAutomationResult(destination: CommunicationDestination, emoji: String, name: String, output: String) async {
        guard destination.platform == .slack else { return }

        let header = "\(emoji) *\(name)* finished"
        let body = output.trimmingCharacters(in: .whitespacesAndNewlines)
        let message = body.isEmpty ? header : "\(header)\n\n\(body)"
        try? await sendMessage(message, channelID: destination.channelID, threadID: destination.threadID)
    }

    public func notifyApprovalRequired(session: ChatSession, approval: ApprovalRequest) async {
        guard session.platform == .slack else { return }

        let toolName = formatToolName(approval.toolCall.toolName)
        let text = """
        Approval required for *\(toolName)*.

        Reply with:
        `!approve \(approval.toolCallID.uuidString)`
        or
        `!deny \(approval.toolCallID.uuidString)`
        """

        try? await sendMessage(text, channelID: session.platformChatID, threadID: session.platformThreadID)
    }

    private func runLoop() async {
        var retryDelay: UInt64 = 2

        while !Task.isCancelled {
            do {
                try await connectAndRun()
                retryDelay = 2
            } catch {
                if Task.isCancelled || error is CancellationError {
                    return
                }
                websocket?.cancel(with: .goingAway, reason: nil)
                websocket = nil
                status = SlackBridgeStatus(
                    connected: false,
                    botName: status.botName,
                    botUserID: status.botUserID,
                    workspaceName: status.workspaceName,
                    lastError: error.localizedDescription,
                    lastEventAt: status.lastEventAt
                )
                logger.error("Slack bridge connection failed", metadata: [
                    "error": error.localizedDescription
                ])

                try? await Task.sleep(nanoseconds: retryDelay * 1_000_000_000)
                retryDelay = min(retryDelay * 2, 30)
            }
        }
    }

    private func connectAndRun() async throws {
        let auth = try await client.authTestBot()
        let socket = try await client.openSocketModeConnection()
        guard let socketURL = socket.socketURL else {
            throw SlackClientError.invalidResponse
        }

        let task = client.makeSocketTask(url: socketURL)
        websocket = task
        task.resume()

        status = SlackBridgeStatus(
            connected: false,
            botName: auth.user,
            botUserID: auth.userID,
            workspaceName: auth.team,
            lastError: nil,
            lastEventAt: status.lastEventAt
        )

        while !Task.isCancelled {
            let envelope = try await receiveEnvelope()
            if let envelopeID = envelope.envelopeID {
                try await acknowledge(envelopeID: envelopeID)
            }

            switch envelope.type {
            case "hello":
                status = SlackBridgeStatus(
                    connected: true,
                    botName: auth.user,
                    botUserID: auth.userID,
                    workspaceName: auth.team,
                    lastError: nil,
                    lastEventAt: .now
                )
                logger.info("Slack Socket Mode ready", metadata: [
                    "workspace": auth.team ?? "unknown",
                    "bot_user_id": auth.userID
                ])
            case "disconnect":
                throw SlackClientError.apiError("Slack requested the Socket Mode connection to disconnect.")
            case "events_api":
                if let event = envelope.event {
                    status = SlackBridgeStatus(
                        connected: true,
                        botName: auth.user,
                        botUserID: auth.userID,
                        workspaceName: auth.team,
                        lastError: nil,
                        lastEventAt: .now
                    )
                    await handle(event: event)
                }
            default:
                continue
            }
        }
    }

    private func maybeHandleCommand(for event: SlackMessageEvent) async -> String? {
        guard let text = event.text?.trimmingCharacters(in: .whitespacesAndNewlines), !text.isEmpty else {
            return nil
        }

        let normalized = normalizeCommandText(text)
        guard normalized.hasPrefix("!") else {
            return nil
        }

        let parts = normalized.split(whereSeparator: \.isWhitespace).map(String.init)
        guard let command = parts.first else { return nil }

        switch command {
        case "!approve", "!deny":
            guard parts.count >= 2, let toolCallID = UUID(uuidString: parts[1]) else {
                return "Use `\(command) <approval-id>`."
            }
            let outcome = await approvalHandler.resolve(
                toolCallID: toolCallID,
                approve: command == "!approve",
                runtime: runtime
            )
            switch outcome {
            case .approved(let assistantMessage, _):
                return assistantMessage.isEmpty ? "Approved and completed." : "Approved.\n\n\(assistantMessage)"
            case .denied:
                return "Denied."
            case .stillPending(_, let pendingApprovals):
                let followUps = pendingApprovals.map {
                    "`!approve \($0.toolCallID.uuidString)` or `!deny \($0.toolCallID.uuidString)`"
                }.joined(separator: "\n")
                return "Approved, but another action needs approval.\n\n\(followUps)"
            case .failed(_, let error):
                return "That didn’t work: \(error)"
            }
        case "!reset":
            do {
                _ = try await sessionStore.rotateConversation(
                    platform: .slack,
                    chatID: event.channel,
                    userID: event.user,
                    threadID: sessionThreadID(for: event),
                    channelName: event.channel,
                    lastMessageID: event.ts,
                    platformContext: ChatBridgePersona.slack.systemPromptAppend
                )
                return "Fresh start. I opened a new conversation."
            } catch {
                return "I couldn’t start a new conversation just now. Try again in a moment."
            }
        case "!status":
            let runtimeStatus = await runtime.status()
            let slackPlatform = runtimeStatus.communications.platforms.first(where: { $0.platform == .slack })
            let slackLine = slackPlatform.map {
                $0.connected ? "Slack: ready" : "Slack: \($0.statusLabel.lowercased())"
            } ?? "Slack: unavailable"
            return "Atlas is \(runtimeStatus.state.rawValue)\n\(slackLine)"
        default:
            return nil
        }
    }

    private func normalizeCommandText(_ text: String) -> String {
        let botID = status.botUserID
        guard let botID else { return text }
        return text
            .replacingOccurrences(of: "<@\(botID)>", with: "")
            .trimmingCharacters(in: .whitespacesAndNewlines)
    }

    private func normalizedInboundContent(from event: SlackMessageEvent) -> String? {
        guard let rawText = event.text?.trimmingCharacters(in: .whitespacesAndNewlines), !rawText.isEmpty else {
            return nil
        }

        switch event.type {
        case "message":
            guard event.channelType == "im" else {
                return nil
            }
            return rawText
        case "app_mention":
            let cleaned = normalizeCommandText(rawText)
            return cleaned.isEmpty ? nil : cleaned
        default:
            return nil
        }
    }

    private func sessionThreadID(for event: SlackMessageEvent) -> String? {
        guard event.channelType != "im" else { return nil }
        return event.threadTS ?? event.ts
    }

    private func replyThreadID(for event: SlackMessageEvent) -> String? {
        sessionThreadID(for: event)
    }

    private func normalizedSlackResponse(from response: AtlasAgentResponse) -> String {
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

    private func sendMessage(_ text: String, channelID: String, threadID: String?) async throws {
        let trimmed = text.trimmingCharacters(in: .whitespacesAndNewlines)
        let chunks = splitMessage(trimmed.isEmpty ? "Done." : trimmed)
        for chunk in chunks {
            _ = try await client.postMessage(channelID: channelID, text: chunk, threadID: threadID)
        }
    }

    private func splitMessage(_ text: String) -> [String] {
        guard text.count > Self.maxMessageLength else {
            return [text]
        }

        var chunks: [String] = []
        var current = ""
        for line in text.components(separatedBy: "\n") {
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
        return chunks.isEmpty ? [text] : chunks
    }

    private func acknowledge(envelopeID: String) async throws {
        guard let websocket else {
            throw SlackClientError.invalidResponse
        }

        let ack = ["envelope_id": envelopeID]
        let data = try JSONSerialization.data(withJSONObject: ack)
        guard let json = String(data: data, encoding: .utf8) else {
            throw SlackClientError.encodingFailed
        }
        try await websocket.send(.string(json))
    }

    private func receiveEnvelope() async throws -> SlackSocketEnvelope {
        guard let websocket else {
            throw SlackClientError.invalidResponse
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
            if closeCode != nil || reason != nil {
                throw SlackClientError.websocketClosed(closeCode, reason)
            }
            throw error
        }
        let data: Data
        switch message {
        case .string(let string):
            guard let decoded = string.data(using: .utf8) else {
                throw SlackClientError.invalidResponse
            }
            data = decoded
        case .data(let payload):
            data = payload
        @unknown default:
            throw SlackClientError.invalidResponse
        }

        do {
            return try AtlasJSON.decoder.decode(SlackSocketEnvelope.self, from: data)
        } catch {
            throw SlackClientError.invalidResponse
        }
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
}

struct SlackSocketEnvelope: Decodable {
    let envelopeID: String?
    let type: String
    let payload: SlackEventPayload?

    var event: SlackMessageEvent? {
        payload?.event
    }

    enum CodingKeys: String, CodingKey {
        case envelopeID = "envelope_id"
        case type
        case payload
    }
}

struct SlackEventPayload: Decodable {
    let event: SlackMessageEvent?
}
