import AtlasLogging
import AtlasMemory
import AtlasNetwork
import AtlasShared
import Foundation

public actor TelegramBridge: ChatBridge {
    private static let maxInboundAttachmentBytes = 20 * 1024 * 1024

    public let platform: ChatPlatform = .telegram
    public let persona: ChatBridgePersona = .telegram

    private let config: AtlasConfig
    private let telegramClient: any TelegramClienting
    private let runtime: any AtlasRuntimeHandling
    private let sessionStore: TelegramSessionStore
    private let attachmentStore: TelegramAttachmentStore
    private let messageMapper: TelegramMessageMapper
    private let commandRouter: TelegramCommandRouter
    private let approvalHandler = ChatApprovalHandler()
    private let logger: AtlasLogger

    /// Attachments received without a caption, waiting to be attached to the next text message.
    private var pendingAttachments: [Int64: [AtlasMessageAttachment]] = [:]

    public var isConnected: Bool { true }

    public func start() async throws {}
    public func stop() async throws {}

    public init(
        config: AtlasConfig,
        telegramClient: any TelegramClienting,
        runtime: any AtlasRuntimeHandling,
        sessionStore: TelegramSessionStore,
        attachmentStore: TelegramAttachmentStore = try! TelegramAttachmentStore(),
        messageMapper: TelegramMessageMapper = TelegramMessageMapper(),
        commandRouter: TelegramCommandRouter? = nil,
        logger: AtlasLogger = AtlasLogger(category: "telegram.bridge")
    ) {
        self.config = config
        self.telegramClient = telegramClient
        self.runtime = runtime
        self.sessionStore = sessionStore
        self.attachmentStore = attachmentStore
        self.messageMapper = messageMapper
        self.commandRouter = commandRouter ?? TelegramCommandRouter(config: config)
        self.logger = logger
    }

    public func handle(update: TelegramUpdate) async {
        if let callbackQuery = update.callbackQuery {
            await handleCallbackQuery(callbackQuery)
            return
        }

        guard let message = update.message else {
            logger.debug("Ignoring unsupported Telegram update", metadata: [
                "update_id": "\(update.updateID)"
            ])
            return
        }

        logger.info("Received Telegram update", metadata: [
            "update_id": "\(update.updateID)",
            "chat_id": "\(message.chat.id)"
        ])

        guard isAuthorized(message) else {
            logger.warning("Rejected unauthorized Telegram chat", metadata: [
                "chat_id": "\(message.chat.id)",
                "user_id": "\(message.from?.id ?? 0)"
            ])
            try? await sendText(
                "Sorry, this Atlas instance isn't set up for this chat.",
                chatID: message.chat.id,
                replyToMessageID: message.messageID
            )
            return
        }

        switch messageMapper.mapInbound(message, commandPrefix: config.telegramCommandPrefix) {
        case .command(let rawCommand):
            await handleCommand(rawCommand, message: message)
        case .message(let atlasMessage):
            await handleUserMessage(atlasMessage, telegramMessage: message)
        case .attachment(let envelope):
            await handleAttachmentMessage(envelope, telegramMessage: message)
        case .unsupported(let fallback):
            try? await sendText(fallback, chatID: message.chat.id, replyToMessageID: message.messageID)
        }
    }

    private func handleCommand(_ rawCommand: String, message: TelegramMessage) async {
        guard let command = commandRouter.parse(rawCommand) else {
            try? await sendText(
                "I don't know that command. Type /help to see what I can do.",
                chatID: message.chat.id,
                replyToMessageID: message.messageID
            )
            return
        }

        let result = await commandRouter.handle(
            command,
            rawText: rawCommand,
            message: message,
            runtime: runtime,
            sessionStore: sessionStore
        )

        try? await sendText(result.text, chatID: message.chat.id, replyToMessageID: message.messageID, replyMarkup: result.replyMarkup)
        logger.info("Handled Telegram command", metadata: [
            "command": command.rawValue,
            "chat_id": "\(message.chat.id)"
        ])
    }

    private func handleCallbackQuery(_ query: TelegramCallbackQuery) async {
        _ = try? await telegramClient.answerCallbackQuery(callbackQueryID: query.id, text: nil)

        guard let data = query.data, !data.isEmpty, let message = query.message else { return }

        let chatID = message.chat.id

        guard config.isTelegramChatAllowed(chatID) && config.isTelegramUserAllowed(query.from.id) else {
            logger.warning("Rejected unauthorized Telegram callback query", metadata: [
                "chat_id": "\(chatID)",
                "user_id": "\(query.from.id)"
            ])
            return
        }

        let parts = data.split(separator: ":", maxSplits: 1).map(String.init)
        guard parts.count == 2, let toolCallID = UUID(uuidString: parts[1]) else {
            logger.warning("Unrecognized callback query data", metadata: ["data": data])
            return
        }

        let action = parts[0]
        logger.info("Handling Telegram inline button tap", metadata: [
            "action": action,
            "chat_id": "\(chatID)",
            "tool_call_id": parts[1]
        ])

        let shouldApprove = action == "approve"
        guard action == "approve" || action == "deny" else {
            logger.warning("Unknown inline button action", metadata: ["action": action])
            return
        }

        let outcome = await approvalHandler.resolve(
            toolCallID: toolCallID,
            approve: shouldApprove,
            runtime: runtime
        )

        switch outcome {
        case .approved(let assistantMessage, let toolName):
            let replyText = assistantMessage.isEmpty ? "✅ Done!" : "✅ Done!\n\n\(assistantMessage)"
            logger.info("Telegram inline approval succeeded", metadata: ["tool": toolName, "chat_id": "\(chatID)"])
            try? await sendText(replyText, chatID: chatID, replyToMessageID: nil)
        case .denied(let toolName):
            logger.info("Telegram inline denial succeeded", metadata: ["tool": toolName, "chat_id": "\(chatID)"])
            try? await sendText("Got it — that action has been cancelled.", chatID: chatID, replyToMessageID: nil)
        case .stillPending(_, let pendingApprovals):
            // The just-approved tool ran successfully, but another approval gate was hit.
            // Send an intro text then one keyboard row per pending approval — same UX as
            // the initial approval prompt so the user can tap ✅/❌ inline.
            try? await sendText("✅ Done — one more action needs your approval:", chatID: chatID, replyToMessageID: nil)
            for approval in pendingApprovals {
                let session = ChatSession(platform: .telegram, platformChatID: String(chatID), platformUserID: nil, activeConversationID: approval.conversationID ?? UUID())
                await notifyApprovalRequired(session: session, approval: approval)
            }
        case .failed(let toolName, let error):
            let replyText = shouldApprove
                ? "I approved it, but it ran into a problem: \(error)"
                : "Something went wrong cancelling that action. Try again in a moment."
            logger.error("Telegram inline button action failed", metadata: ["tool": toolName, "chat_id": "\(chatID)", "error": error])
            try? await sendText(replyText, chatID: chatID, replyToMessageID: nil)
        }
    }

    private func handleUserMessage(_ atlasMessage: AtlasMessage, telegramMessage: TelegramMessage) async {
        do {
            let session = try await sessionStore.resolveSession(
                chatID: telegramMessage.chat.id,
                userID: telegramMessage.from?.id,
                lastMessageID: telegramMessage.messageID,
                platformContext: persona.systemPromptAppend
            )

            // Drain any attachments the user sent before this text message.
            let held = pendingAttachments.removeValue(forKey: telegramMessage.chat.id) ?? []

            // Context-aware reactions — only one fires per message.
            if shouldReactWithLove(atlasMessage.content) {
                try? await telegramClient.setMessageReaction(chatID: telegramMessage.chat.id, messageID: telegramMessage.messageID, emoji: "❤")
            } else if shouldReactWithShock(atlasMessage.content) {
                try? await telegramClient.setMessageReaction(chatID: telegramMessage.chat.id, messageID: telegramMessage.messageID, emoji: "🤯")
            } else if shouldReactWithProcessing(atlasMessage.content) {
                try? await telegramClient.setMessageReaction(chatID: telegramMessage.chat.id, messageID: telegramMessage.messageID, emoji: "👀")
            }
            _ = try? await telegramClient.sendChatAction(chatID: telegramMessage.chat.id, action: "typing")
            if shouldAcknowledgeImageGeneration(atlasMessage.content) {
                try? await sendText(
                    "I’m generating that image now. I’ll send it here as soon as it’s ready.",
                    chatID: telegramMessage.chat.id,
                    replyToMessageID: telegramMessage.messageID
                )
                _ = try? await telegramClient.sendChatAction(chatID: telegramMessage.chat.id, action: "upload_photo")
            }
            logger.info("Routing Telegram message into Atlas runtime", metadata: [
                "chat_id": "\(telegramMessage.chat.id)",
                "conversation_id": session.activeConversationID.uuidString,
                "attachments": "\(held.count)"
            ])

            let envelope = await runtime.handleMessage(
                AtlasMessageRequest(
                    conversationID: session.activeConversationID,
                    message: atlasMessage.content,
                    attachments: held
                )
            )

            let imageURLs = imageArtifactURLs(from: envelope.response)
            let requestedDelivery = shouldAttemptTelegramFileDelivery(atlasMessage.content)
            let discoveredFiles = requestedDelivery ? fileDeliveryURLs(from: envelope.response) : []
            let telegramResponse = normalizedTelegramResponse(
                from: envelope.response,
                imageArtifactURLs: imageURLs,
                deliveredFileURLs: discoveredFiles
            )

            let events = messageMapper.outboundEvents(
                chatID: telegramMessage.chat.id,
                replyToMessageID: telegramMessage.messageID,
                response: telegramResponse
            )

            for event in events {
                try await sendText(event.text, chatID: event.chatID, replyToMessageID: event.replyToMessageID, replyMarkup: event.replyMarkup, parseMode: event.parseMode)
            }

            try await sendGeneratedImages(
                imageURLs: imageURLs,
                chatID: telegramMessage.chat.id,
                replyToMessageID: telegramMessage.messageID
            )

            try await sendDiscoveredFiles(
                fileURLs: discoveredFiles,
                chatID: telegramMessage.chat.id,
                replyToMessageID: telegramMessage.messageID
            )

            // React with ✅ only when tools actually ran — signals real work was done.
            if !envelope.response.toolCalls.isEmpty {
                try? await telegramClient.setMessageReaction(chatID: telegramMessage.chat.id, messageID: telegramMessage.messageID, emoji: "✅")
            }

            logger.info("Sent Telegram assistant reply", metadata: [
                "chat_id": "\(telegramMessage.chat.id)",
                "status": envelope.response.status.rawValue
            ])
        } catch {
            logger.error("Failed to route Telegram message", metadata: [
                "chat_id": "\(telegramMessage.chat.id)",
                "error": error.localizedDescription
            ])
            try? await sendText(
                "Something went wrong on my end. Sorry about that — try again in a moment.",
                chatID: telegramMessage.chat.id,
                replyToMessageID: telegramMessage.messageID
            )
        }
    }

    /// Routes a message + attachments to the runtime and sends the reply back to Telegram.
    private func routeToRuntime(
        message: String,
        attachments: [AtlasMessageAttachment],
        session: TelegramSession,
        telegramMessage: TelegramMessage
    ) async {
        do {
            // Attachments are always active work — react with 👀 while processing.
            try? await telegramClient.setMessageReaction(chatID: telegramMessage.chat.id, messageID: telegramMessage.messageID, emoji: "👀")
            _ = try? await telegramClient.sendChatAction(chatID: telegramMessage.chat.id, action: "typing")

            logger.info("Routing Telegram attachment into Atlas runtime", metadata: [
                "chat_id": "\(telegramMessage.chat.id)",
                "conversation_id": session.activeConversationID.uuidString,
                "attachments": "\(attachments.count)"
            ])

            let responseEnvelope = await runtime.handleMessage(
                AtlasMessageRequest(
                    conversationID: session.activeConversationID,
                    message: message,
                    attachments: attachments
                )
            )

            let imageURLs = imageArtifactURLs(from: responseEnvelope.response)
            let telegramResponse = normalizedTelegramResponse(
                from: responseEnvelope.response,
                imageArtifactURLs: imageURLs,
                deliveredFileURLs: []
            )

            let events = messageMapper.outboundEvents(
                chatID: telegramMessage.chat.id,
                replyToMessageID: telegramMessage.messageID,
                response: telegramResponse
            )

            for event in events {
                try await sendText(event.text, chatID: event.chatID, replyToMessageID: event.replyToMessageID, replyMarkup: event.replyMarkup, parseMode: event.parseMode)
            }

            try await sendGeneratedImages(
                imageURLs: imageURLs,
                chatID: telegramMessage.chat.id,
                replyToMessageID: telegramMessage.messageID
            )

            if !responseEnvelope.response.toolCalls.isEmpty {
                try? await telegramClient.setMessageReaction(chatID: telegramMessage.chat.id, messageID: telegramMessage.messageID, emoji: "✅")
            }

            logger.info("Sent Telegram assistant reply", metadata: [
                "chat_id": "\(telegramMessage.chat.id)",
                "status": responseEnvelope.response.status.rawValue
            ])
        } catch {
            logger.error("Failed to route Telegram attachment message", metadata: [
                "chat_id": "\(telegramMessage.chat.id)",
                "error": error.localizedDescription
            ])
            try? await sendText(
                "Something went wrong processing that attachment. Sorry — try again in a moment.",
                chatID: telegramMessage.chat.id,
                replyToMessageID: telegramMessage.messageID
            )
        }
    }

    private func handleAttachmentMessage(
        _ envelope: TelegramInboundAttachmentEnvelope,
        telegramMessage: TelegramMessage
    ) async {
        do {
            let session = try await sessionStore.resolveSession(
                chatID: telegramMessage.chat.id,
                userID: telegramMessage.from?.id,
                lastMessageID: telegramMessage.messageID,
                platformContext: persona.systemPromptAppend
            )

            var atlasAttachments: [AtlasMessageAttachment] = []
            var skippedCount = 0

            for attachment in envelope.attachments {
                if let fileSize = attachment.fileSize, fileSize > Self.maxInboundAttachmentBytes {
                    skippedCount += 1
                    logger.warning("Skipping oversized Telegram attachment", metadata: [
                        "chat_id": "\(telegramMessage.chat.id)",
                        "message_id": "\(telegramMessage.messageID)",
                        "bytes": "\(fileSize)",
                        "kind": attachment.kind.rawValue
                    ])
                    continue
                }

                let fileResponse = try await telegramClient.getFile(fileID: attachment.fileID)
                guard let filePath = fileResponse.result.filePath, !filePath.isEmpty else {
                    throw TelegramClientError.missingFilePath(attachment.fileID)
                }

                let data = try await telegramClient.downloadFile(atPath: filePath)
                atlasAttachments.append(AtlasMessageAttachment(
                    filename: attachment.suggestedFileName ?? "\(attachment.kind.rawValue)-\(attachment.fileUniqueID)",
                    mimeType: attachment.mimeType ?? "application/octet-stream",
                    data: data.base64EncodedString()
                ))
            }

            if atlasAttachments.isEmpty {
                let msg = skippedCount == 1
                    ? "That file is too large for me to process (max 20 MB). Try a smaller one."
                    : "Those \(skippedCount) files are too large to process (max 20 MB each). Try smaller ones."
                try? await sendText(msg, chatID: telegramMessage.chat.id, replyToMessageID: telegramMessage.messageID)
                return
            }

            // If the user included a caption, route immediately. Otherwise hold the
            // attachments and wait — we don't want to call the API speculatively.
            if let caption = envelope.caption {
                await routeToRuntime(
                    message: caption,
                    attachments: atlasAttachments,
                    session: session,
                    telegramMessage: telegramMessage
                )
            } else {
                pendingAttachments[telegramMessage.chat.id, default: []].append(contentsOf: atlasAttachments)
                let count = atlasAttachments.count
                let noun = count == 1 ? "image" : "images"
                try? await sendText(
                    "Got your \(noun)! What would you like me to do with \(count == 1 ? "it" : "them")?",
                    chatID: telegramMessage.chat.id,
                    replyToMessageID: telegramMessage.messageID
                )
                logger.info("Held Telegram attachment for next message", metadata: [
                    "chat_id": "\(telegramMessage.chat.id)",
                    "pending": "\(pendingAttachments[telegramMessage.chat.id]?.count ?? 0)"
                ])
            }
        } catch {
            logger.error("Failed to handle Telegram attachment", metadata: [
                "chat_id": "\(telegramMessage.chat.id)",
                "message_id": "\(telegramMessage.messageID)",
                "error": error.localizedDescription
            ])
            try? await sendText(
                "Something went wrong processing that attachment. Sorry — try again in a moment.",
                chatID: telegramMessage.chat.id,
                replyToMessageID: telegramMessage.messageID
            )
        }
    }

    // MARK: - ChatBridge: proactive approval notification

    public func notifyApprovalRequired(session: ChatSession, approval: ApprovalRequest) async {
        guard session.platform == .telegram, let chatID = Int64(session.platformChatID) else { return }

        let toolName = approval.toolCall.toolName
            .components(separatedBy: "__").last?
            .components(separatedBy: "_").filter { !$0.isEmpty }
            .map { $0.prefix(1).uppercased() + $0.dropFirst() }
            .joined(separator: " ") ?? approval.toolCall.toolName

        let keyboard = InlineKeyboardMarkup(inlineKeyboard: [[
            InlineKeyboardButton(text: "✅ \(toolName)", callbackData: "approve:\(approval.toolCallID.uuidString)"),
            InlineKeyboardButton(text: "❌ Deny", callbackData: "deny:\(approval.toolCallID.uuidString)")
        ]])

        try? await sendText(
            "Action requires your approval:",
            chatID: chatID,
            replyToMessageID: nil,
            replyMarkup: keyboard
        )

        logger.info("Sent proactive approval notification", metadata: [
            "chat_id": "\(chatID)",
            "tool_call_id": approval.toolCallID.uuidString,
            "tool": toolName
        ])
    }

    // MARK: - Automation delivery

    public func deliverAutomationResult(destination: CommunicationDestination, emoji: String, name: String, output: String) async {
        guard destination.platform == .telegram, let chatID = Int64(destination.channelID) else { return }
        await deliverAutomationResult(chatID: chatID, emoji: emoji, name: name, output: output)
    }

    /// Delivers the output of a completed Gremlin run to a Telegram chat.
    public func deliverAutomationResult(chatID: Int64, emoji: String, name: String, output: String) async {
        let header = "\(emoji) *\(name)* finished"
        let body = output.count > 3800 ? String(output.prefix(3800)) + "\n…" : output
        let text = "\(header)\n\n\(body)"
        try? await sendText(text, chatID: chatID, replyToMessageID: nil)
        logger.info("Delivered automation result to Telegram", metadata: [
            "chat_id": "\(chatID)",
            "gremlin": name
        ])
    }

    private func sendText(
        _ text: String,
        chatID: Int64,
        replyToMessageID: Int?,
        replyMarkup: InlineKeyboardMarkup? = nil,
        parseMode: String? = nil
    ) async throws {
        let chunks = messageMapper.splitText(text)

        for (index, chunk) in chunks.enumerated() {
            let markup = index == 0 ? replyMarkup : nil
            do {
                _ = try await telegramClient.sendMessage(
                    chatID: chatID,
                    text: chunk,
                    replyToMessageID: replyToMessageID,
                    replyMarkup: markup,
                    parseMode: parseMode
                )
            } catch let error as TelegramClientError where isTelegramMarkupRejection(error) && markup != nil {
                // Telegram rejected the reply_markup (400 Bad Request) — the message was NOT sent.
                // Retry text-only so the user still sees the content.
                logger.warning("Telegram rejected reply_markup, retrying text-only", metadata: [
                    "chat_id": "\(chatID)"
                ])
                try await sendChunk(
                    chunk,
                    chatID: chatID,
                    replyToMessageID: replyToMessageID,
                    parseMode: parseMode
                )
            } catch let error as TelegramClientError where isTelegramHTMLParseError(error) && parseMode != nil {
                // Telegram rejected the HTML (400 "can't parse entities") — the message was NOT sent.
                // Fall back to plain text so the user still receives the reply.
                logger.warning("Telegram rejected HTML parse mode, retrying as plain text", metadata: [
                    "chat_id": "\(chatID)"
                ])
                _ = try await telegramClient.sendMessage(
                    chatID: chatID,
                    text: TelegramMessageMapper.htmlEscape(chunk),
                    replyToMessageID: replyToMessageID,
                    replyMarkup: markup,
                    parseMode: nil
                )
            }
        }
    }

    /// Sends a single chunk, falling back to plain text if Telegram rejects HTML parse mode.
    private func sendChunk(
        _ chunk: String,
        chatID: Int64,
        replyToMessageID: Int?,
        parseMode: String?
    ) async throws {
        do {
            _ = try await telegramClient.sendMessage(
                chatID: chatID,
                text: chunk,
                replyToMessageID: replyToMessageID,
                replyMarkup: nil,
                parseMode: parseMode
            )
        } catch let error as TelegramClientError where isTelegramHTMLParseError(error) && parseMode != nil {
            logger.warning("Telegram rejected HTML on text-only retry, sending plain text", metadata: [
                "chat_id": "\(chatID)"
            ])
            _ = try await telegramClient.sendMessage(
                chatID: chatID,
                text: TelegramMessageMapper.htmlEscape(chunk),
                replyToMessageID: replyToMessageID,
                replyMarkup: nil,
                parseMode: nil
            )
        }
    }

    /// Returns true only for 400-level Telegram API errors — guarantees the message was NOT
    /// delivered, so a retry is safe. Network timeouts are excluded because the message may
    /// have already been sent when the connection dropped.
    private func isTelegramMarkupRejection(_ error: TelegramClientError) -> Bool {
        switch error {
        case .unexpectedStatusCode(400, _):
            return true
        case .apiError(let code, _) where code == 400:
            return true
        default:
            return false
        }
    }

    /// Returns true for Telegram's "can't parse entities" HTML rejection.
    private func isTelegramHTMLParseError(_ error: TelegramClientError) -> Bool {
        let msg: String
        switch error {
        case .unexpectedStatusCode(400, let m):
            msg = m
        case .apiError(_, let m):
            msg = m
        default:
            return false
        }
        return msg.lowercased().contains("can't parse entities")
    }

    private func sendGeneratedImages(
        imageURLs: [URL],
        chatID: Int64,
        replyToMessageID: Int?
    ) async throws {
        guard !imageURLs.isEmpty else {
            return
        }

        _ = try? await telegramClient.sendChatAction(chatID: chatID, action: "upload_photo")

        for (index, fileURL) in imageURLs.enumerated() {
            guard FileManager.default.fileExists(atPath: fileURL.path) else {
                logger.warning("Skipping missing Telegram image artifact", metadata: [
                    "chat_id": "\(chatID)",
                    "file_name": fileURL.lastPathComponent
                ])
                continue
            }

            _ = try await telegramClient.sendPhoto(
                chatID: chatID,
                photoURL: fileURL,
                caption: nil,
                replyToMessageID: index == 0 ? replyToMessageID : nil
            )
        }

        logger.info("Sent Telegram image attachments", metadata: [
            "chat_id": "\(chatID)",
            "artifact_count": "\(imageURLs.count)"
        ])
    }

    private func sendDiscoveredFiles(
        fileURLs: [URL],
        chatID: Int64,
        replyToMessageID: Int?
    ) async throws {
        guard !fileURLs.isEmpty else {
            return
        }

        for (index, fileURL) in fileURLs.enumerated() {
            guard FileManager.default.fileExists(atPath: fileURL.path) else {
                logger.warning("Skipping missing Telegram file delivery target", metadata: [
                    "chat_id": "\(chatID)",
                    "file_name": fileURL.lastPathComponent
                ])
                continue
            }

            if isImageFile(fileURL) {
                _ = try? await telegramClient.sendChatAction(chatID: chatID, action: "upload_photo")
                _ = try await telegramClient.sendPhoto(
                    chatID: chatID,
                    photoURL: fileURL,
                    caption: nil,
                    replyToMessageID: index == 0 ? replyToMessageID : nil
                )
            } else {
                _ = try? await telegramClient.sendChatAction(chatID: chatID, action: "upload_document")
                _ = try await telegramClient.sendDocument(
                    chatID: chatID,
                    documentURL: fileURL,
                    caption: nil,
                    replyToMessageID: index == 0 ? replyToMessageID : nil
                )
            }
        }

        logger.info("Sent Telegram discovered files", metadata: [
            "chat_id": "\(chatID)",
            "file_count": "\(fileURLs.count)"
        ])
    }

    private func imageArtifactURLs(from response: AtlasAgentResponse) -> [URL] {
        let imageToolCallIDs = Set(
            response.toolCalls
                .filter { toolCall in
                    toolCall.toolName.contains("skill__image_generation__image_generate")
                        || toolCall.toolName.contains("skill__image_generation__image_edit")
                }
                .map(\.id)
        )

        guard !imageToolCallIDs.isEmpty else {
            return []
        }

        return response.toolResults
            .filter { imageToolCallIDs.contains($0.toolCallID) && $0.success }
            .flatMap(extractImageArtifactURLs(from:))
    }

    private func extractImageArtifactURLs(from result: AtlasToolResult) -> [URL] {
        guard
            let data = result.output.data(using: .utf8),
            let object = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
            let images = object["images"] as? [[String: Any]]
        else {
            return []
        }

        return images.compactMap { image in
            guard let filePath = image["filePath"] as? String, !filePath.isEmpty else {
                return nil
            }
            return URL(fileURLWithPath: filePath)
        }
    }

    private func normalizedTelegramResponse(
        from response: AtlasAgentResponse,
        imageArtifactURLs: [URL],
        deliveredFileURLs: [URL]
    ) -> AtlasAgentResponse {
        let deliveredURLs = Array(Set(imageArtifactURLs + deliveredFileURLs))
        guard !deliveredURLs.isEmpty else {
            return response
        }

        let trimmed = response.assistantMessage.trimmingCharacters(in: .whitespacesAndNewlines)
        let normalizedMessage: String
        if trimmed.isEmpty || looksLikeImagePresentationQuestion(trimmed) {
            if !imageArtifactURLs.isEmpty && deliveredFileURLs.isEmpty {
                normalizedMessage = imageArtifactURLs.count == 1
                    ? "Here’s the generated image."
                    : "Here are the generated images."
            } else if deliveredURLs.count == 1 {
                normalizedMessage = isImageFile(deliveredURLs[0])
                    ? "Here’s the file."
                    : "Here’s the document."
            } else {
                normalizedMessage = "Here are the files."
            }
        } else {
            normalizedMessage = trimmed
        }

        return AtlasAgentResponse(
            assistantMessage: normalizedMessage,
            toolCalls: response.toolCalls,
            status: response.status,
            toolResults: response.toolResults,
            pendingApprovals: response.pendingApprovals,
            errorMessage: response.errorMessage
        )
    }

    private func looksLikeImagePresentationQuestion(_ text: String) -> Bool {
        let normalized = text.lowercased()
        guard normalized.contains("?") else {
            return false
        }

        let presentationPhrases = [
            "how would you like",
            "how do you want",
            "how should i",
            "would you like me to"
        ]
        let presentationTargets = [
            "present",
            "share",
            "send",
            "deliver",
            "show"
        ]

        return presentationPhrases.contains(where: normalized.contains)
            && presentationTargets.contains(where: normalized.contains)
    }

    private func shouldAttemptTelegramFileDelivery(_ text: String) -> Bool {
        let normalized = text.lowercased()
        let deliveryVerbs = ["send", "share", "attach", "upload", "return", "give me", "show me"]
        let fileTargets = ["file", "files", "image", "images", "photo", "photos", "document", "documents", "pdf", "artifact", "artifacts"]

        return deliveryVerbs.contains(where: normalized.contains)
            && fileTargets.contains(where: normalized.contains)
    }

    private func fileDeliveryURLs(from response: AtlasAgentResponse) -> [URL] {
        let toolCallMap = Dictionary(uniqueKeysWithValues: response.toolCalls.map { ($0.id, $0.toolName) })
        var seenPaths = Set<String>()
        var urls: [URL] = []

        for result in response.toolResults where result.success {
            guard let toolName = toolCallMap[result.toolCallID], toolName.contains("skill__file_system__") else {
                continue
            }

            for url in extractFileURLs(from: result.output) {
                if seenPaths.insert(url.path).inserted {
                    urls.append(url)
                }
            }
        }

        return urls
    }

    private func extractFileURLs(from output: String) -> [URL] {
        guard
            let data = output.data(using: .utf8),
            let object = try? JSONSerialization.jsonObject(with: data)
        else {
            return []
        }

        var paths: [String] = []

        if let dictionary = object as? [String: Any] {
            if let entries = dictionary["entries"] as? [[String: Any]] {
                for entry in entries where (entry["type"] as? String) == "file" {
                    if let path = entry["path"] as? String {
                        paths.append(path)
                    }
                }
            }

            if let results = dictionary["results"] as? [[String: Any]] {
                for result in results where (result["type"] as? String) == "file" {
                    if let path = result["path"] as? String {
                        paths.append(path)
                    }
                }
            }

            if (dictionary["type"] as? String) == "file", let path = dictionary["path"] as? String {
                paths.append(path)
            }
        }

        return paths
            .filter { !$0.isEmpty }
            .map(URL.init(fileURLWithPath:))
            .filter { isSendableTelegramFile($0) }
    }

    private func isSendableTelegramFile(_ url: URL) -> Bool {
        let allowedExtensions = [
            "jpg", "jpeg", "png", "webp", "gif",
            "pdf", "txt", "md", "json"
        ]
        return FileManager.default.fileExists(atPath: url.path)
            && allowedExtensions.contains(url.pathExtension.lowercased())
    }

    private func isImageFile(_ url: URL) -> Bool {
        let imageExtensions = ["jpg", "jpeg", "png", "webp", "gif"]
        return imageExtensions.contains(url.pathExtension.lowercased())
    }

    private func shouldReactWithLove(_ text: String) -> Bool {
        let normalized = text.lowercased()
        let praiseTerms = [
            "thank you", "thanks", "thx", "ty",
            "great job", "good job", "nice job", "well done",
            "awesome", "amazing", "fantastic", "brilliant", "excellent",
            "perfect", "love it", "love you", "you're the best", "you are the best",
            "incredible", "outstanding", "superb", "magnificent",
            "<3", "❤", "🙏"
        ]
        return praiseTerms.contains(where: normalized.contains)
    }

    private func shouldReactWithShock(_ text: String) -> Bool {
        let normalized = text.lowercased()
        let shockTerms = [
            "omg", "oh my god", "oh my gosh", "no way", "what the",
            "wtf", "wth", "holy", "shut up", "shut the",
            "i can't believe", "unbelievable", "impossible", "seriously?",
            "are you kidding", "you're joking", "no way!", "whoa", "woah",
            "mind blown", "jaw drop", "shocking", "insane", "crazy",
            "this is wild", "that's wild"
        ]
        return shockTerms.contains(where: normalized.contains)
    }

    /// Returns true when a message is clearly requesting active work — file ops, web research,
    /// calendar/reminders, automations, image generation — so 👀 is warranted.
    /// Conversational messages ("what's the capital of France?") return false.
    private func shouldReactWithProcessing(_ text: String) -> Bool {
        let normalized = text.lowercased()

        let actionVerbs = [
            "create", "add", "schedule", "book", "set", "send", "open", "run",
            "search", "find", "look up", "fetch", "get me", "download", "upload",
            "generate", "make", "build", "write", "edit", "delete", "remove",
            "move", "copy", "rename", "summarize", "translate", "convert",
            "remind", "automate", "trigger", "execute"
        ]
        let actionTargets = [
            "file", "folder", "document", "image", "photo", "email", "message",
            "calendar", "event", "meeting", "reminder", "note", "contact",
            "web", "website", "url", "link", "page", "result",
            "automation", "gremlin", "workflow", "script",
            "app", "application", "window"
        ]

        return actionVerbs.contains(where: normalized.contains)
            && actionTargets.contains(where: normalized.contains)
    }

    private func shouldAcknowledgeImageGeneration(_ text: String) -> Bool {
        let normalized = text.lowercased()
        let directPhrases = [
            "generate image",
            "create image",
            "make an image",
            "edit image",
            "generate a picture",
            "create a picture",
            "make a picture",
            "generate an illustration",
            "create an illustration",
            "generate artwork",
            "create artwork",
            "generate art",
            "create art",
            "create icon",
            "generate icon",
            "create logo",
            "generate logo"
        ]

        if directPhrases.contains(where: normalized.contains) {
            return true
        }

        let imageTerms = ["image", "picture", "illustration", "artwork", "art", "icon", "logo", "poster", "banner"]
        let creativeVerbs = ["generate", "create", "make", "design", "draw", "render", "edit", "redesign"]

        return imageTerms.contains(where: normalized.contains)
            && creativeVerbs.contains(where: normalized.contains)
    }

    private func isAuthorized(_ message: TelegramMessage) -> Bool {
        config.isTelegramChatAllowed(message.chat.id) && config.isTelegramUserAllowed(message.from?.id)
    }

}
