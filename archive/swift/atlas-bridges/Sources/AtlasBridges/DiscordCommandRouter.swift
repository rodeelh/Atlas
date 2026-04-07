import AtlasLogging
import AtlasMemory
import AtlasShared
import Foundation

public enum DiscordCommand: String, CaseIterable, Sendable {
    case help
    case status
    case approvals
    case reset
    case automations
    case run
    case approve
    case deny
}

public struct DiscordCommandRouter: Sendable {
    private let config: AtlasConfig
    private let logger: AtlasLogger
    let listAutomations: (@Sendable () async -> String)?
    let triggerAutomation: (@Sendable (_ nameOrID: String) async -> String)?

    public init(
        config: AtlasConfig,
        logger: AtlasLogger = AtlasLogger(category: "discord.command"),
        listAutomations: (@Sendable () async -> String)? = nil,
        triggerAutomation: (@Sendable (_ nameOrID: String) async -> String)? = nil
    ) {
        self.config = config
        self.logger = logger
        self.listAutomations = listAutomations
        self.triggerAutomation = triggerAutomation
    }

    public func parse(_ rawText: String) -> DiscordCommand? {
        let token = rawText
            .split(whereSeparator: \.isWhitespace)
            .first
            .map(String.init)?
            .lowercased() ?? rawText.lowercased()

        let normalized = token
            .trimmingCharacters(in: .whitespacesAndNewlines)
            .trimmingCharacters(in: CharacterSet(charactersIn: "!/"))

        return DiscordCommand(rawValue: normalized)
    }

    public func handle(
        _ command: DiscordCommand,
        rawText: String,
        event: DiscordMessageCreateEvent,
        runtime: any AtlasRuntimeHandling,
        session: ChatSession,
        sessionStore: CommunicationSessionStore,
        approvalHandler: ChatApprovalHandler
    ) async -> String {
        logger.info("Handling Discord command", metadata: [
            "command": command.rawValue,
            "channel_id": event.channelID
        ])

        switch command {
        case .help:
            return """
            Here’s what I can do here:

            !status — check how Atlas is doing
            !reset — start a fresh conversation
            !approvals — list pending approvals
            !approve <id> — approve a pending action
            !deny <id> — deny a pending action
            !automations — list your automations
            !run <name> — trigger an automation right now

            Or just message me directly and I’ll handle it.
            """

        case .status:
            let runtimeStatus = await runtime.status()
            let openAIConfigured = (try? !config.openAIAPIKey().isEmpty) ?? false
            let platformStatus = runtimeStatus.communications.platforms.first(where: { $0.platform == .discord })

            let stateEmoji: String
            switch runtimeStatus.state {
            case .ready: stateEmoji = "🟢"
            case .degraded: stateEmoji = "🟡"
            default: stateEmoji = "🔴"
            }

            let discordLine: String
            if let platformStatus {
                discordLine = "Discord: \(platformStatus.connected ? "✅ connected" : "❌ \(platformStatus.statusLabel.lowercased())")"
            } else {
                discordLine = "Discord: ❌ unavailable"
            }

            return """
            \(stateEmoji) Atlas is \(runtimeStatus.state.rawValue)
            AI: \(openAIConfigured ? "✅ connected" : "❌ token missing")
            \(discordLine)
            """

        case .approvals:
            let pending = await approvalHandler.listPending(
                conversationID: session.activeConversationID,
                runtime: runtime
            )
            if pending.isEmpty {
                return "No pending approvals in this conversation."
            }

            return pending.map { approval in
                let toolName = formatToolName(approval.toolCall.toolName)
                return "• \(toolName)\n  approve: `!approve \(approval.toolCallID.uuidString)`\n  deny: `!deny \(approval.toolCallID.uuidString)`"
            }.joined(separator: "\n\n")

        case .reset:
            do {
                _ = try await sessionStore.rotateConversation(
                    platform: .discord,
                    chatID: event.channelID,
                    userID: event.author.id,
                    channelName: event.guildID == nil ? (event.author.globalName ?? event.author.username) : event.author.username,
                    lastMessageID: event.id,
                    platformContext: ChatBridgePersona.discord.systemPromptAppend
                )
                return "Fresh start. I opened a new conversation."
            } catch {
                return "I couldn’t start a new conversation just now. Try again in a moment."
            }

        case .automations:
            if let listAutomations {
                return await listAutomations()
            }
            return "Automations aren’t available right now."

        case .run:
            guard let nameOrID = commandArgument(from: rawText), !nameOrID.isEmpty else {
                return "Which automation should I run? Try `!run <name>`."
            }
            if let triggerAutomation {
                return await triggerAutomation(nameOrID)
            }
            return "The automation runner isn’t available right now."

        case .approve, .deny:
            guard let argument = commandArgument(from: rawText), let toolCallID = UUID(uuidString: argument) else {
                return "Use `!\(command.rawValue) <approval-id>`."
            }

            let outcome = await approvalHandler.resolve(
                toolCallID: toolCallID,
                approve: command == .approve,
                runtime: runtime
            )

            switch outcome {
            case .approved(let assistantMessage, _):
                if assistantMessage.isEmpty {
                    return "Approved and completed."
                }
                return "Approved.\n\n\(assistantMessage)"
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
        }
    }

    private func commandArgument(from rawText: String) -> String? {
        let parts = rawText.split(whereSeparator: \.isWhitespace)
        guard parts.count >= 2 else { return nil }
        return String(parts[1])
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
