import Foundation
import AtlasLogging
import AtlasShared

public enum TelegramClientError: LocalizedError {
    case missingBotToken
    case invalidResponse
    case unexpectedStatusCode(Int, String)
    case apiError(Int?, String)
    case localFileMissing(String)
    case missingFilePath(String)

    public var errorDescription: String? {
        switch self {
        case .missingBotToken:
            return "No Telegram bot token was found in the macOS Keychain."
        case .invalidResponse:
            return "The Telegram Bot API returned an invalid response."
        case .unexpectedStatusCode(let code, let message):
            return "The Telegram Bot API returned status code \(code): \(message)"
        case .apiError(let code, let message):
            if let code {
                return "Telegram Bot API error \(code): \(message)"
            }
            return "Telegram Bot API error: \(message)"
        case .localFileMissing(let path):
            return "The local file could not be read for Telegram upload: \(path)"
        case .missingFilePath(let fileID):
            return "Telegram did not return a downloadable path for file \(fileID)."
        }
    }
}

public protocol TelegramClienting: Sendable {
    func getMe() async throws -> TelegramGetMeResponse
    func getUpdates(offset: Int?, timeout: Int) async throws -> TelegramGetUpdatesResponse
    func getFile(fileID: String) async throws -> TelegramGetFileResponse
    func downloadFile(atPath path: String) async throws -> Data
    func sendMessage(chatID: Int64, text: String, replyToMessageID: Int?, replyMarkup: InlineKeyboardMarkup?, parseMode: String?) async throws -> TelegramSendMessageResponse
    func setMessageReaction(chatID: Int64, messageID: Int, emoji: String) async throws
    func answerCallbackQuery(callbackQueryID: String, text: String?) async throws -> TelegramBooleanResponse
    func sendPhoto(chatID: Int64, photoURL: URL, caption: String?, replyToMessageID: Int?) async throws -> TelegramSendPhotoResponse
    func sendDocument(chatID: Int64, documentURL: URL, caption: String?, replyToMessageID: Int?) async throws -> TelegramSendDocumentResponse
    func sendChatAction(chatID: Int64, action: String) async throws -> TelegramBooleanResponse
    func setMyCommands(commands: [TelegramBotCommand]) async throws -> TelegramBooleanResponse
    func deleteWebhook() async throws -> TelegramBooleanResponse
    func receiveWebhookUpdate(_ payload: Data) async throws -> TelegramUpdate
}

public final class TelegramClient: TelegramClienting, Sendable {
    private let session: URLSession
    private let config: AtlasConfig
    private let logger = AtlasLogger.network

    public init(
        session: URLSession = .shared,
        config: AtlasConfig = AtlasConfig()
    ) {
        self.session = session
        self.config = config
    }

    public func getMe() async throws -> TelegramGetMeResponse {
        try await perform(method: "POST", endpoint: "getMe", body: Optional<String>.none, decodeAs: TelegramGetMeResponse.self)
    }

    public func getUpdates(offset: Int?, timeout: Int) async throws -> TelegramGetUpdatesResponse {
        try await perform(
            method: "POST",
            endpoint: "getUpdates",
            body: TelegramGetUpdatesRequest(offset: offset, timeout: timeout),
            decodeAs: TelegramGetUpdatesResponse.self
        )
    }

    public func getFile(fileID: String) async throws -> TelegramGetFileResponse {
        try await perform(
            method: "POST",
            endpoint: "getFile",
            body: TelegramGetFileRequest(fileID: fileID),
            decodeAs: TelegramGetFileResponse.self
        )
    }

    public func downloadFile(atPath path: String) async throws -> Data {
        let token = try fetchBotToken()
        let normalizedPath = path.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !normalizedPath.isEmpty else {
            throw TelegramClientError.invalidResponse
        }

        let encodedPath = normalizedPath
            .split(separator: "/")
            .map { String($0).addingPercentEncoding(withAllowedCharacters: .urlPathAllowed) ?? String($0) }
            .joined(separator: "/")
        let url = URL(string: "https://api.telegram.org/file/bot\(token)/\(encodedPath)")!
        var request = URLRequest(url: url)
        request.httpMethod = "GET"

        let (data, response) = try await session.data(for: request)
        guard let httpResponse = response as? HTTPURLResponse else {
            throw TelegramClientError.invalidResponse
        }

        guard (200..<300).contains(httpResponse.statusCode) else {
            let message = String(decoding: data, as: UTF8.self)
            logger.error("Telegram file download failed", metadata: [
                "status_code": "\(httpResponse.statusCode)",
                "path": normalizedPath
            ])
            throw TelegramClientError.unexpectedStatusCode(httpResponse.statusCode, message)
        }

        return data
    }

    public func sendMessage(
        chatID: Int64,
        text: String,
        replyToMessageID: Int? = nil,
        replyMarkup: InlineKeyboardMarkup? = nil,
        parseMode: String? = nil
    ) async throws -> TelegramSendMessageResponse {
        logger.info("Sending Telegram message", metadata: ["chat_id": "\(chatID)"])
        return try await perform(
            method: "POST",
            endpoint: "sendMessage",
            body: TelegramSendMessageRequest(chatID: chatID, text: text, replyToMessageID: replyToMessageID, replyMarkup: replyMarkup, parseMode: parseMode),
            decodeAs: TelegramSendMessageResponse.self
        )
    }

    public func setMessageReaction(chatID: Int64, messageID: Int, emoji: String) async throws {
        _ = try await perform(
            method: "POST",
            endpoint: "setMessageReaction",
            body: TelegramSetMessageReactionRequest(chatID: chatID, messageID: messageID, reaction: [TelegramReactionEmoji(emoji: emoji)]),
            decodeAs: TelegramBooleanResponse.self
        )
    }

    public func answerCallbackQuery(callbackQueryID: String, text: String? = nil) async throws -> TelegramBooleanResponse {
        try await perform(
            method: "POST",
            endpoint: "answerCallbackQuery",
            body: TelegramAnswerCallbackQueryRequest(callbackQueryID: callbackQueryID, text: text),
            decodeAs: TelegramBooleanResponse.self
        )
    }

    public func sendPhoto(
        chatID: Int64,
        photoURL: URL,
        caption: String? = nil,
        replyToMessageID: Int? = nil
    ) async throws -> TelegramSendPhotoResponse {
        logger.info("Sending Telegram photo", metadata: [
            "chat_id": "\(chatID)",
            "file_name": photoURL.lastPathComponent
        ])

        let token = try fetchBotToken()
        let fileData: Data
        do {
            fileData = try Data(contentsOf: photoURL)
        } catch {
            throw TelegramClientError.localFileMissing(photoURL.path)
        }

        let boundary = "AtlasTelegramBoundary-\(UUID().uuidString)"
        let url = URL(string: "https://api.telegram.org/bot\(token)/sendPhoto")!
        var request = URLRequest(url: url)
        request.httpMethod = "POST"
        request.setValue("multipart/form-data; boundary=\(boundary)", forHTTPHeaderField: "Content-Type")
        request.httpBody = buildMultipartPhotoBody(
            boundary: boundary,
            chatID: chatID,
            photoData: fileData,
            fileName: photoURL.lastPathComponent.isEmpty ? "image" : photoURL.lastPathComponent,
            mimeType: mimeType(for: photoURL),
            caption: caption,
            replyToMessageID: replyToMessageID
        )

        let (data, response) = try await session.data(for: request)
        guard let httpResponse = response as? HTTPURLResponse else {
            throw TelegramClientError.invalidResponse
        }

        guard (200..<300).contains(httpResponse.statusCode) else {
            let message = decodeErrorMessage(from: data)
            logger.error("Telegram photo upload failed", metadata: [
                "status_code": "\(httpResponse.statusCode)",
                "message": message
            ])
            throw TelegramClientError.unexpectedStatusCode(httpResponse.statusCode, message)
        }

        do {
            return try AtlasJSON.decoder.decode(TelegramSendPhotoResponse.self, from: data)
        } catch {
            if let envelope = try? AtlasJSON.decoder.decode(TelegramAPIErrorEnvelope.self, from: data), envelope.ok == false {
                let message = envelope.description ?? "Unknown Telegram API error."
                throw TelegramClientError.apiError(envelope.errorCode, message)
            }

            logger.error("Telegram photo response decoding failed", metadata: [
                "error": error.localizedDescription
            ])
            throw error
        }
    }

    public func sendDocument(
        chatID: Int64,
        documentURL: URL,
        caption: String? = nil,
        replyToMessageID: Int? = nil
    ) async throws -> TelegramSendDocumentResponse {
        logger.info("Sending Telegram document", metadata: [
            "chat_id": "\(chatID)",
            "file_name": documentURL.lastPathComponent
        ])

        let token = try fetchBotToken()
        let fileData: Data
        do {
            fileData = try Data(contentsOf: documentURL)
        } catch {
            throw TelegramClientError.localFileMissing(documentURL.path)
        }

        let boundary = "AtlasTelegramBoundary-\(UUID().uuidString)"
        let url = URL(string: "https://api.telegram.org/bot\(token)/sendDocument")!
        var request = URLRequest(url: url)
        request.httpMethod = "POST"
        request.setValue("multipart/form-data; boundary=\(boundary)", forHTTPHeaderField: "Content-Type")
        request.httpBody = buildMultipartFileBody(
            boundary: boundary,
            chatID: chatID,
            fieldName: "document",
            fileData: fileData,
            fileName: documentURL.lastPathComponent.isEmpty ? "document" : documentURL.lastPathComponent,
            mimeType: mimeType(for: documentURL),
            caption: caption,
            replyToMessageID: replyToMessageID
        )

        let (data, response) = try await session.data(for: request)
        guard let httpResponse = response as? HTTPURLResponse else {
            throw TelegramClientError.invalidResponse
        }

        guard (200..<300).contains(httpResponse.statusCode) else {
            let message = decodeErrorMessage(from: data)
            logger.error("Telegram document upload failed", metadata: [
                "status_code": "\(httpResponse.statusCode)",
                "message": message
            ])
            throw TelegramClientError.unexpectedStatusCode(httpResponse.statusCode, message)
        }

        do {
            return try AtlasJSON.decoder.decode(TelegramSendDocumentResponse.self, from: data)
        } catch {
            if let envelope = try? AtlasJSON.decoder.decode(TelegramAPIErrorEnvelope.self, from: data), envelope.ok == false {
                let message = envelope.description ?? "Unknown Telegram API error."
                throw TelegramClientError.apiError(envelope.errorCode, message)
            }

            logger.error("Telegram document response decoding failed", metadata: [
                "error": error.localizedDescription
            ])
            throw error
        }
    }

    public func sendChatAction(chatID: Int64, action: String) async throws -> TelegramBooleanResponse {
        try await perform(
            method: "POST",
            endpoint: "sendChatAction",
            body: TelegramSendChatActionRequest(chatID: chatID, action: action),
            decodeAs: TelegramBooleanResponse.self
        )
    }

    public func setMyCommands(commands: [TelegramBotCommand]) async throws -> TelegramBooleanResponse {
        try await perform(
            method: "POST",
            endpoint: "setMyCommands",
            body: TelegramSetMyCommandsRequest(commands: commands),
            decodeAs: TelegramBooleanResponse.self
        )
    }

    public func deleteWebhook() async throws -> TelegramBooleanResponse {
        try await perform(method: "POST", endpoint: "deleteWebhook", body: Optional<String>.none, decodeAs: TelegramBooleanResponse.self)
    }

    public func receiveWebhookUpdate(_ payload: Data) async throws -> TelegramUpdate {
        try AtlasJSON.decoder.decode(TelegramUpdate.self, from: payload)
    }

    private func perform<Body: Encodable, Response: Decodable>(
        method: String,
        endpoint: String,
        body: Body?,
        decodeAs type: Response.Type
    ) async throws -> Response {
        let token = try fetchBotToken()
        let url = URL(string: "https://api.telegram.org/bot\(token)/\(endpoint)")!
        var request = URLRequest(url: url)
        request.httpMethod = method
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")

        if let body {
            request.httpBody = try AtlasJSON.encoder.encode(body)
        }

        let (data, response) = try await session.data(for: request)
        guard let httpResponse = response as? HTTPURLResponse else {
            throw TelegramClientError.invalidResponse
        }

        guard (200..<300).contains(httpResponse.statusCode) else {
            let message = decodeErrorMessage(from: data)
            logger.error("Telegram request failed", metadata: [
                "endpoint": endpoint,
                "status_code": "\(httpResponse.statusCode)",
                "message": message
            ])
            throw TelegramClientError.unexpectedStatusCode(httpResponse.statusCode, message)
        }

        do {
            let decoded = try AtlasJSON.decoder.decode(Response.self, from: data)
            return decoded
        } catch {
            if let envelope = try? AtlasJSON.decoder.decode(TelegramAPIErrorEnvelope.self, from: data), envelope.ok == false {
                let message = envelope.description ?? "Unknown Telegram API error."
                throw TelegramClientError.apiError(envelope.errorCode, message)
            }

            logger.error("Telegram response decoding failed", metadata: [
                "endpoint": endpoint,
                "error": error.localizedDescription
            ])
            throw error
        }
    }

    private func decodeErrorMessage(from data: Data) -> String {
        if let envelope = try? AtlasJSON.decoder.decode(TelegramAPIErrorEnvelope.self, from: data) {
            return envelope.description ?? "Unknown Telegram API error."
        }

        return String(decoding: data, as: UTF8.self)
    }

    private func fetchBotToken() throws -> String {
        do {
            return try config.telegramBotToken()
        } catch {
            throw TelegramClientError.missingBotToken
        }
    }

    private func buildMultipartPhotoBody(
        boundary: String,
        chatID: Int64,
        photoData: Data,
        fileName: String,
        mimeType: String,
        caption: String?,
        replyToMessageID: Int?
    ) -> Data {
        buildMultipartFileBody(
            boundary: boundary,
            chatID: chatID,
            fieldName: "photo",
            fileData: photoData,
            fileName: fileName,
            mimeType: mimeType,
            caption: caption,
            replyToMessageID: replyToMessageID
        )
    }

    private func buildMultipartFileBody(
        boundary: String,
        chatID: Int64,
        fieldName: String,
        fileData: Data,
        fileName: String,
        mimeType: String,
        caption: String?,
        replyToMessageID: Int?
    ) -> Data {
        var body = Data()

        func appendField(name: String, value: String) {
            body.append(Data("--\(boundary)\r\n".utf8))
            body.append(Data("Content-Disposition: form-data; name=\"\(name)\"\r\n\r\n".utf8))
            body.append(Data(value.utf8))
            body.append(Data("\r\n".utf8))
        }

        appendField(name: "chat_id", value: String(chatID))
        if let caption, !caption.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
            appendField(name: "caption", value: String(caption.prefix(1024)))
        }
        if let replyToMessageID {
            appendField(name: "reply_to_message_id", value: String(replyToMessageID))
        }

        body.append(Data("--\(boundary)\r\n".utf8))
        body.append(Data("Content-Disposition: form-data; name=\"\(fieldName)\"; filename=\"\(fileName)\"\r\n".utf8))
        body.append(Data("Content-Type: \(mimeType)\r\n\r\n".utf8))
        body.append(fileData)
        body.append(Data("\r\n".utf8))
        body.append(Data("--\(boundary)--\r\n".utf8))
        return body
    }

    private func mimeType(for url: URL) -> String {
        switch url.pathExtension.lowercased() {
        case "jpg", "jpeg":
            return "image/jpeg"
        case "png":
            return "image/png"
        case "webp":
            return "image/webp"
        case "gif":
            return "image/gif"
        default:
            return "application/octet-stream"
        }
    }
}
