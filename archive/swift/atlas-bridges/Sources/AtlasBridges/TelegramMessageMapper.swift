import Foundation
import AtlasNetwork
import AtlasShared

public enum TelegramInboundPayload: Sendable {
    case command(String)
    case message(AtlasMessage)
    case attachment(TelegramInboundAttachmentEnvelope)
    case unsupported(String)
}

public struct TelegramOutboundEvent: Codable, Hashable, Sendable {
    public let chatID: Int64
    public let text: String
    public let replyToMessageID: Int?
    public let replyMarkup: InlineKeyboardMarkup?
    public let parseMode: String?

    public init(chatID: Int64, text: String, replyToMessageID: Int? = nil, replyMarkup: InlineKeyboardMarkup? = nil, parseMode: String? = nil) {
        self.chatID = chatID
        self.text = text
        self.replyToMessageID = replyToMessageID
        self.replyMarkup = replyMarkup
        self.parseMode = parseMode
    }
}

public struct TelegramMessageMapper: Sendable {
    public let maxMessageLength: Int

    public init(maxMessageLength: Int = 3500) {
        self.maxMessageLength = max(256, maxMessageLength)
    }

    public func mapInbound(_ message: TelegramMessage, commandPrefix: String = "/") -> TelegramInboundPayload {
        if let text = normalizedText(message.text), isCommand(text: text, entities: message.entities, prefix: commandPrefix) {
            return .command(text)
        }

        if let location = message.location {
            let lat = String(format: "%.6f", location.latitude)
            let lon = String(format: "%.6f", location.longitude)
            let text = "📍 My current location: latitude \(lat), longitude \(lon)"
            return .message(AtlasMessage(role: .user, content: text))
        }

        if let attachmentEnvelope = attachmentEnvelope(from: message) {
            return .attachment(attachmentEnvelope)
        }

        if let text = normalizedText(message.text) {
            return .message(AtlasMessage(role: .user, content: text))
        }

        return .unsupported("I can't process this attachment type yet.")
    }

    public func outboundEvents(
        chatID: Int64,
        replyToMessageID: Int?,
        response: AtlasAgentResponse
    ) -> [TelegramOutboundEvent] {
        let baseText: String
        var replyMarkup: InlineKeyboardMarkup?

        switch response.status {
        case .waitingForApproval:
            let approvals = response.pendingApprovals
            let count = approvals.count
            let visibleApprovals = Array(approvals.prefix(3))

            if count > 0 {
                // Buttons will be shown — strip the "N tool approval(s) pending" suffix
                // since the keyboard makes it redundant. Show a clean action-focused intro.
                let cleaned = strippedApprovalSuffix(response.assistantMessage)
                let intro = cleaned.isEmpty ? "I need your approval to continue:" : cleaned
                baseText = intro

                let keyboardRows = visibleApprovals.map { req -> [InlineKeyboardButton] in
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
                replyMarkup = InlineKeyboardMarkup(inlineKeyboard: keyboardRows)
            } else {
                // No pending approvals in the response — direct to web UI
                let intro = response.assistantMessage.isEmpty
                    ? "I need your approval before I can continue."
                    : response.assistantMessage
                baseText = "\(intro)\n\nCheck the Atlas web UI to review and approve."
            }

        case .failed:
            let errorDetail = response.errorMessage.map { "\n\n\($0)" } ?? ""
            baseText = response.assistantMessage.isEmpty
                ? "Something went wrong on my end. Sorry about that.\(errorDetail)"
                : "\(response.assistantMessage)\(errorDetail)"
        case .completed:
            baseText = response.assistantMessage
        }

        let htmlText = Self.markdownToHTML(baseText)
        let chunks = splitOutboundText(htmlText)
        return chunks.enumerated().map { index, text in
            TelegramOutboundEvent(
                chatID: chatID,
                text: text,
                replyToMessageID: replyToMessageID,
                replyMarkup: index == 0 ? replyMarkup : nil,
                parseMode: "HTML"
            )
        }
    }

    /// Strips the agent loop's "N tool approval(s) is/are pending." suffix so the
    /// keyboard buttons can speak for themselves.
    private func strippedApprovalSuffix(_ message: String) -> String {
        let paragraphs = message.components(separatedBy: "\n\n")
        guard let last = paragraphs.last else { return message }
        let stripped = last.hasSuffix("tool approval is pending.")
            || last.hasSuffix("tool approvals are pending.")
            || last.hasPrefix("Atlas requires approval")
        guard stripped else { return message }
        let remaining = paragraphs.dropLast()
        return remaining.joined(separator: "\n\n").trimmingCharacters(in: .whitespacesAndNewlines)
    }

    private func splitOutboundText(_ text: String) -> [String] {
        let normalized = normalizedText(text) ?? ""
        guard normalized.count > maxMessageLength else {
            return [normalized.isEmpty ? "Atlas returned an empty response." : normalized]
        }

        var chunks: [String] = []
        var current = ""

        for line in normalized.split(separator: "\n", omittingEmptySubsequences: false) {
            let candidate = current.isEmpty ? String(line) : current + "\n" + line
            if candidate.count > maxMessageLength, !current.isEmpty {
                chunks.append(current)
                current = String(line)
            } else {
                current = candidate
            }
        }

        if !current.isEmpty {
            chunks.append(current)
        }

        return chunks.isEmpty ? [normalized] : chunks
    }

    /// Splits plain or pre-formatted text into sendable chunks without re-processing content.
    public func splitText(_ text: String) -> [String] {
        splitOutboundText(text)
    }

    /// Converts common Markdown patterns to Telegram HTML and escapes plain-text characters.
    /// Handles: fenced code blocks, inline code, bold (**text**), italic (*text*, _text_),
    /// strikethrough (~~text~~). HTML-escapes <, >, & elsewhere.
    public static func markdownToHTML(_ input: String) -> String {
        var result = ""
        var i = input.startIndex

        while i < input.endIndex {
            let remaining = input[i...]

            // Fenced code block: ```[lang]\ncode\n```
            if remaining.hasPrefix("```") {
                let fenceStart = input.index(i, offsetBy: 3, limitedBy: input.endIndex) ?? input.endIndex
                if let closeRange = input[fenceStart...].range(of: "```") {
                    var codeContent = String(input[fenceStart..<closeRange.lowerBound])
                    // Strip optional language tag (e.g. "swift\n") from first line
                    if let newline = codeContent.firstIndex(of: "\n") {
                        let langTag = String(codeContent[..<newline]).trimmingCharacters(in: .whitespaces)
                        if !langTag.isEmpty && langTag.count <= 20 && !langTag.contains(" ") {
                            codeContent = String(codeContent[codeContent.index(after: newline)...])
                        }
                    }
                    result += "<pre><code>\(htmlEscape(codeContent))</code></pre>"
                    i = closeRange.upperBound
                    continue
                }
            }

            // Inline code: `code`
            if input[i] == "`" {
                let afterTick = input.index(after: i)
                if afterTick < input.endIndex, let closeIdx = input[afterTick...].firstIndex(of: "`") {
                    let code = String(input[afterTick..<closeIdx])
                    result += "<code>\(htmlEscape(code))</code>"
                    i = input.index(after: closeIdx)
                    continue
                }
            }

            // Strikethrough: ~~text~~ (single-line only)
            if remaining.hasPrefix("~~") {
                let afterOpen = input.index(i, offsetBy: 2, limitedBy: input.endIndex) ?? input.endIndex
                if let closeRange = input[afterOpen...].range(of: "~~") {
                    let inner = String(input[afterOpen..<closeRange.lowerBound])
                    if !inner.isEmpty && !inner.contains("\n") {
                        result += "<s>\(htmlEscape(inner))</s>"
                        i = closeRange.upperBound
                        continue
                    }
                }
            }

            // Bold: **text** (single-line only, checked before single-* italic)
            if remaining.hasPrefix("**") {
                let afterOpen = input.index(i, offsetBy: 2, limitedBy: input.endIndex) ?? input.endIndex
                if let closeRange = input[afterOpen...].range(of: "**") {
                    let bold = String(input[afterOpen..<closeRange.lowerBound])
                    if !bold.isEmpty && !bold.contains("\n") {
                        result += "<b>\(htmlEscape(bold))</b>"
                        i = closeRange.upperBound
                        continue
                    }
                }
            }

            // Italic: *text* (single-line, no leading/trailing space — guards against bullet lists)
            if input[i] == "*" {
                let afterOpen = input.index(after: i)
                if afterOpen < input.endIndex,
                   input[afterOpen] != " ", input[afterOpen] != "\n", input[afterOpen] != "*",
                   let closeIdx = input[afterOpen...].firstIndex(of: "*") {
                    let inner = String(input[afterOpen..<closeIdx])
                    if !inner.isEmpty && !inner.contains("\n") && !inner.hasSuffix(" ") {
                        result += "<i>\(htmlEscape(inner))</i>"
                        i = input.index(after: closeIdx)
                        continue
                    }
                }
            }

            // Italic: _text_ (single-line, no leading/trailing space)
            if input[i] == "_" {
                let afterOpen = input.index(after: i)
                if afterOpen < input.endIndex,
                   input[afterOpen] != " ", input[afterOpen] != "\n", input[afterOpen] != "_",
                   let closeIdx = input[afterOpen...].firstIndex(of: "_") {
                    let inner = String(input[afterOpen..<closeIdx])
                    if !inner.isEmpty && !inner.contains("\n") && !inner.hasSuffix(" ") {
                        result += "<i>\(htmlEscape(inner))</i>"
                        i = input.index(after: closeIdx)
                        continue
                    }
                }
            }

            // HTML-escape special characters in plain text
            switch input[i] {
            case "<": result += "&lt;"
            case ">": result += "&gt;"
            case "&": result += "&amp;"
            default: result.append(input[i])
            }
            i = input.index(after: i)
        }

        return result
    }

    public static func htmlEscape(_ text: String) -> String {
        text
            .replacingOccurrences(of: "&", with: "&amp;")
            .replacingOccurrences(of: "<", with: "&lt;")
            .replacingOccurrences(of: ">", with: "&gt;")
    }

    private func isCommand(text: String, entities: [TelegramMessageEntity]?, prefix: String) -> Bool {
        if text.hasPrefix(prefix) {
            return true
        }

        guard let first = entities?.first else {
            return false
        }

        return first.type == "bot_command" && first.offset == 0
    }

    private func normalizedText(_ text: String?) -> String? {
        let trimmed = text?.trimmingCharacters(in: .whitespacesAndNewlines)
        guard let trimmed, !trimmed.isEmpty else {
            return nil
        }

        return trimmed
    }

    private func attachmentEnvelope(from message: TelegramMessage) -> TelegramInboundAttachmentEnvelope? {
        var attachments: [TelegramInboundAttachment] = []

        if let photo = largestPhoto(in: message.photo) {
            attachments.append(
                TelegramInboundAttachment(
                    kind: .image,
                    fileID: photo.fileID,
                    fileUniqueID: photo.fileUniqueID,
                    suggestedFileName: "telegram-photo-\(photo.fileUniqueID).jpg",
                    mimeType: "image/jpeg",
                    fileSize: photo.fileSize
                )
            )
        }

        if let document = message.document {
            let kind: TelegramAttachmentKind = (document.mimeType?.lowercased().hasPrefix("image/") == true) ? .image : .document
            attachments.append(
                TelegramInboundAttachment(
                    kind: kind,
                    fileID: document.fileID,
                    fileUniqueID: document.fileUniqueID,
                    suggestedFileName: document.fileName,
                    mimeType: document.mimeType,
                    fileSize: document.fileSize
                )
            )
        }

        guard !attachments.isEmpty else {
            return nil
        }

        return TelegramInboundAttachmentEnvelope(
            caption: normalizedText(message.caption),
            attachments: attachments
        )
    }

    private func largestPhoto(in photoSizes: [TelegramPhotoSize]?) -> TelegramPhotoSize? {
        photoSizes?.max { lhs, rhs in
            let lhsArea = lhs.width * lhs.height
            let rhsArea = rhs.width * rhs.height
            if lhsArea == rhsArea {
                return (lhs.fileSize ?? 0) < (rhs.fileSize ?? 0)
            }
            return lhsArea < rhsArea
        }
    }
}
