import Foundation
import AtlasLogging
import AtlasMemory
import AtlasNetwork
import AtlasShared

public struct TelegramCommandResult: Sendable {
    public let text: String
    public let replyMarkup: InlineKeyboardMarkup?

    public init(text: String, replyMarkup: InlineKeyboardMarkup? = nil) {
        self.text = text
        self.replyMarkup = replyMarkup
    }
}

public enum TelegramCommand: String, CaseIterable, Sendable {
    case start = "start"
    case help = "help"
    case status = "status"
    case approvals = "approvals"
    case reset = "reset"
    case automations = "automations"
    case run = "run"
}

public struct TelegramCommandRouter: Sendable {
    private let config: AtlasConfig
    private let logger: AtlasLogger
    /// Returns a formatted list of all automations.
    let listAutomations: (@Sendable () async -> String)?
    /// Triggers an automation by ID or name and returns a status string.
    let triggerAutomation: (@Sendable (_ nameOrID: String) async -> String)?

    public init(
        config: AtlasConfig,
        logger: AtlasLogger = AtlasLogger(category: "telegram.command"),
        listAutomations: (@Sendable () async -> String)? = nil,
        triggerAutomation: (@Sendable (_ nameOrID: String) async -> String)? = nil
    ) {
        self.config = config
        self.logger = logger
        self.listAutomations = listAutomations
        self.triggerAutomation = triggerAutomation
    }

    public func parse(_ rawText: String) -> TelegramCommand? {
        let token = rawText
            .split(whereSeparator: \.isWhitespace)
            .first?
            .split(separator: "@")
            .first
            .map(String.init) ?? rawText

        let prefix = config.telegramCommandPrefix
        guard token.hasPrefix(prefix) else {
            return nil
        }

        let name = String(token.dropFirst(prefix.count)).lowercased()
        return TelegramCommand(rawValue: name)
    }

    public func handle(
        _ command: TelegramCommand,
        rawText: String = "",
        message: TelegramMessage,
        runtime: any AtlasRuntimeHandling,
        sessionStore: TelegramSessionStore
    ) async -> TelegramCommandResult {
        logger.info("Handling Telegram command", metadata: [
            "command": command.rawValue,
            "chat_id": "\(message.chat.id)"
        ])

        switch command {
        case .start:
            _ = try? await sessionStore.resolveSession(
                chatID: message.chat.id,
                userID: message.from?.id,
                lastMessageID: message.messageID
            )
            return TelegramCommandResult(text: """
            Hey! Atlas is connected and ready to go. 👋

            Just send me a message and I'll take care of it. Type /help anytime to see what I can do.
            """)

        case .help:
            return TelegramCommandResult(text: """
            Here's what I can do:

            /automations — list your scheduled automations
            /run <name> — trigger an automation right now
            /status — check how Atlas is doing
            /reset — start a fresh conversation
            /help — show this message

            Or just send me a message and I'll handle it.
            """)

        case .status:
            let runtimeStatus = await runtime.status()
            let openAIConfigured = (try? !config.openAIAPIKey().isEmpty) ?? false

            let stateEmoji: String
            switch runtimeStatus.state {
            case .ready: stateEmoji = "🟢"
            case .degraded: stateEmoji = "🟡"
            default: stateEmoji = "🔴"
            }
            let approvalNote = runtimeStatus.pendingApprovalCount > 0
                ? "\n⏳ \(runtimeStatus.pendingApprovalCount) action\(runtimeStatus.pendingApprovalCount == 1 ? "" : "s") waiting for approval — check the web UI"
                : ""
            return TelegramCommandResult(text: """
            \(stateEmoji) Atlas is \(runtimeStatus.state.rawValue)
            AI: \(openAIConfigured ? "✅ connected" : "❌ API key missing")
            Telegram: \(runtimeStatus.telegram.connected ? "✅ connected" : "❌ disconnected")\(approvalNote)
            """)

        case .approvals:
            let pending = await runtime.approvals().filter { $0.status == .pending }
            guard !pending.isEmpty else {
                return TelegramCommandResult(text: "No pending approvals — you're all clear. ✅")
            }

            let intro = pending.count == 1
                ? "1 action needs your approval:"
                : "\(pending.count) actions need your approval:"

            let keyboardRows = pending.prefix(5).map { req -> [InlineKeyboardButton] in
                let name = req.toolCall.toolName
                    .components(separatedBy: "__").last?
                    .components(separatedBy: "_").filter { !$0.isEmpty }
                    .map { $0.prefix(1).uppercased() + $0.dropFirst() }
                    .joined(separator: " ") ?? req.toolCall.toolName
                return [
                    InlineKeyboardButton(text: "✅ \(name)", callbackData: "approve:\(req.toolCallID.uuidString)"),
                    InlineKeyboardButton(text: "❌ Deny", callbackData: "deny:\(req.toolCallID.uuidString)")
                ]
            }

            return TelegramCommandResult(
                text: intro,
                replyMarkup: InlineKeyboardMarkup(inlineKeyboard: keyboardRows)
            )

        case .reset:
            guard (try? await sessionStore.rotateConversation(
                chatID: message.chat.id,
                userID: message.from?.id,
                lastMessageID: message.messageID
            )) != nil else {
                return TelegramCommandResult(text: "Hmm, something went wrong starting a new conversation. Try again in a moment.")
            }

            return TelegramCommandResult(text: "Fresh start! I've opened a new conversation. What's on your mind?")

        case .automations:
            if let lister = listAutomations {
                return TelegramCommandResult(text: await lister())
            }
            return TelegramCommandResult(text: "Automations aren't available right now. Try again in a moment.")

        case .run:
            guard let nameOrID = commandArgument(from: rawText), !nameOrID.isEmpty else {
                return TelegramCommandResult(text: "Which automation should I run? Try /run <name>.\nUse /automations to see the full list.")
            }
            if let trigger = triggerAutomation {
                return TelegramCommandResult(text: await trigger(nameOrID))
            }
            return TelegramCommandResult(text: "The automation runner isn't available right now. Try again in a moment.")
        }
    }

    // MARK: - Helpers

    private func commandArgument(from rawText: String) -> String? {
        let parts = rawText.split(whereSeparator: \.isWhitespace)
        guard parts.count >= 2 else { return nil }
        return String(parts[1])
    }
}
