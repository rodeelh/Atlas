import Foundation

public struct TelegramUser: Codable, Hashable, Sendable {
    public let id: Int64
    public let isBot: Bool
    public let firstName: String
    public let lastName: String?
    public let username: String?

    enum CodingKeys: String, CodingKey {
        case id
        case isBot = "is_bot"
        case firstName = "first_name"
        case lastName = "last_name"
        case username
    }

    public init(id: Int64, isBot: Bool, firstName: String, lastName: String? = nil, username: String? = nil) {
        self.id = id
        self.isBot = isBot
        self.firstName = firstName
        self.lastName = lastName
        self.username = username
    }
}

public struct TelegramChat: Codable, Hashable, Sendable {
    public let id: Int64
    public let type: String
    public let title: String?
    public let username: String?
    public let firstName: String?
    public let lastName: String?

    enum CodingKeys: String, CodingKey {
        case id
        case type
        case title
        case username
        case firstName = "first_name"
        case lastName = "last_name"
    }

    public init(
        id: Int64,
        type: String,
        title: String? = nil,
        username: String? = nil,
        firstName: String? = nil,
        lastName: String? = nil
    ) {
        self.id = id
        self.type = type
        self.title = title
        self.username = username
        self.firstName = firstName
        self.lastName = lastName
    }
}

public struct TelegramMessageEntity: Codable, Hashable, Sendable {
    public let type: String
    public let offset: Int
    public let length: Int

    public init(type: String, offset: Int, length: Int) {
        self.type = type
        self.offset = offset
        self.length = length
    }
}

public struct TelegramLocation: Codable, Hashable, Sendable {
    public let latitude: Double
    public let longitude: Double

    public init(latitude: Double, longitude: Double) {
        self.latitude = latitude
        self.longitude = longitude
    }
}

public struct TelegramMessage: Codable, Hashable, Sendable {
    public let messageID: Int
    public let from: TelegramUser?
    public let chat: TelegramChat
    public let date: Int?
    public let text: String?
    public let caption: String?
    public let entities: [TelegramMessageEntity]?
    public let photo: [TelegramPhotoSize]?
    public let document: TelegramDocument?
    public let location: TelegramLocation?

    enum CodingKeys: String, CodingKey {
        case messageID = "message_id"
        case from
        case chat
        case date
        case text
        case caption
        case entities
        case photo
        case document
        case location
    }

    public init(
        messageID: Int,
        from: TelegramUser? = nil,
        chat: TelegramChat,
        date: Int? = nil,
        text: String? = nil,
        caption: String? = nil,
        entities: [TelegramMessageEntity]? = nil,
        photo: [TelegramPhotoSize]? = nil,
        document: TelegramDocument? = nil,
        location: TelegramLocation? = nil
    ) {
        self.messageID = messageID
        self.from = from
        self.chat = chat
        self.date = date
        self.text = text
        self.caption = caption
        self.entities = entities
        self.photo = photo
        self.document = document
        self.location = location
    }
}

public struct TelegramPhotoSize: Codable, Hashable, Sendable {
    public let fileID: String
    public let fileUniqueID: String
    public let width: Int
    public let height: Int
    public let fileSize: Int?

    enum CodingKeys: String, CodingKey {
        case fileID = "file_id"
        case fileUniqueID = "file_unique_id"
        case width
        case height
        case fileSize = "file_size"
    }

    public init(fileID: String, fileUniqueID: String, width: Int, height: Int, fileSize: Int? = nil) {
        self.fileID = fileID
        self.fileUniqueID = fileUniqueID
        self.width = width
        self.height = height
        self.fileSize = fileSize
    }
}

public struct TelegramDocument: Codable, Hashable, Sendable {
    public let fileID: String
    public let fileUniqueID: String
    public let fileName: String?
    public let mimeType: String?
    public let fileSize: Int?

    enum CodingKeys: String, CodingKey {
        case fileID = "file_id"
        case fileUniqueID = "file_unique_id"
        case fileName = "file_name"
        case mimeType = "mime_type"
        case fileSize = "file_size"
    }

    public init(
        fileID: String,
        fileUniqueID: String,
        fileName: String? = nil,
        mimeType: String? = nil,
        fileSize: Int? = nil
    ) {
        self.fileID = fileID
        self.fileUniqueID = fileUniqueID
        self.fileName = fileName
        self.mimeType = mimeType
        self.fileSize = fileSize
    }
}

public struct InlineKeyboardButton: Codable, Hashable, Sendable {
    public let text: String
    public let callbackData: String?

    enum CodingKeys: String, CodingKey {
        case text
        case callbackData = "callback_data"
    }

    public init(text: String, callbackData: String?) {
        self.text = text
        self.callbackData = callbackData
    }
}

public struct InlineKeyboardMarkup: Codable, Hashable, Sendable {
    public let inlineKeyboard: [[InlineKeyboardButton]]

    enum CodingKeys: String, CodingKey {
        case inlineKeyboard = "inline_keyboard"
    }

    public init(inlineKeyboard: [[InlineKeyboardButton]]) {
        self.inlineKeyboard = inlineKeyboard
    }
}

public struct TelegramCallbackQuery: Codable, Hashable, Sendable {
    public let id: String
    public let from: TelegramUser
    public let message: TelegramMessage?
    public let data: String?

    public init(id: String, from: TelegramUser, message: TelegramMessage? = nil, data: String? = nil) {
        self.id = id
        self.from = from
        self.message = message
        self.data = data
    }
}

public struct TelegramAnswerCallbackQueryRequest: Codable, Hashable, Sendable {
    public let callbackQueryID: String
    public let text: String?

    enum CodingKeys: String, CodingKey {
        case callbackQueryID = "callback_query_id"
        case text
    }

    public init(callbackQueryID: String, text: String? = nil) {
        self.callbackQueryID = callbackQueryID
        self.text = text
    }
}

public struct TelegramUpdate: Codable, Hashable, Sendable {
    public let updateID: Int
    public let message: TelegramMessage?
    public let callbackQuery: TelegramCallbackQuery?

    enum CodingKeys: String, CodingKey {
        case updateID = "update_id"
        case message
        case callbackQuery = "callback_query"
    }

    public init(updateID: Int, message: TelegramMessage?, callbackQuery: TelegramCallbackQuery? = nil) {
        self.updateID = updateID
        self.message = message
        self.callbackQuery = callbackQuery
    }
}

public struct TelegramBotCommand: Codable, Hashable, Sendable {
    public let command: String
    public let description: String

    public init(command: String, description: String) {
        self.command = command
        self.description = description
    }
}

public struct TelegramGetMeResponse: Codable, Hashable, Sendable {
    public let ok: Bool
    public let result: TelegramUser

    public init(ok: Bool, result: TelegramUser) {
        self.ok = ok
        self.result = result
    }
}

public struct TelegramGetUpdatesResponse: Codable, Hashable, Sendable {
    public let ok: Bool
    public let result: [TelegramUpdate]

    public init(ok: Bool, result: [TelegramUpdate]) {
        self.ok = ok
        self.result = result
    }
}

public struct TelegramGetFileRequest: Codable, Hashable, Sendable {
    public let fileID: String

    enum CodingKeys: String, CodingKey {
        case fileID = "file_id"
    }

    public init(fileID: String) {
        self.fileID = fileID
    }
}

public struct TelegramFile: Codable, Hashable, Sendable {
    public let fileID: String
    public let fileUniqueID: String
    public let fileSize: Int?
    public let filePath: String?

    enum CodingKeys: String, CodingKey {
        case fileID = "file_id"
        case fileUniqueID = "file_unique_id"
        case fileSize = "file_size"
        case filePath = "file_path"
    }

    public init(fileID: String, fileUniqueID: String, fileSize: Int? = nil, filePath: String? = nil) {
        self.fileID = fileID
        self.fileUniqueID = fileUniqueID
        self.fileSize = fileSize
        self.filePath = filePath
    }
}

public struct TelegramGetFileResponse: Codable, Hashable, Sendable {
    public let ok: Bool
    public let result: TelegramFile

    public init(ok: Bool, result: TelegramFile) {
        self.ok = ok
        self.result = result
    }
}

public struct TelegramSendMessageRequest: Codable, Hashable, Sendable {
    public let chatID: Int64
    public let text: String
    public let replyToMessageID: Int?
    public let replyMarkup: InlineKeyboardMarkup?
    public let parseMode: String?

    enum CodingKeys: String, CodingKey {
        case chatID = "chat_id"
        case text
        case replyToMessageID = "reply_to_message_id"
        case replyMarkup = "reply_markup"
        case parseMode = "parse_mode"
    }

    public init(chatID: Int64, text: String, replyToMessageID: Int? = nil, replyMarkup: InlineKeyboardMarkup? = nil, parseMode: String? = nil) {
        self.chatID = chatID
        self.text = text
        self.replyToMessageID = replyToMessageID
        self.replyMarkup = replyMarkup
        self.parseMode = parseMode
    }
}

public struct TelegramReactionEmoji: Codable, Hashable, Sendable {
    public let type: String
    public let emoji: String

    public init(emoji: String) {
        self.type = "emoji"
        self.emoji = emoji
    }
}

public struct TelegramSetMessageReactionRequest: Codable, Hashable, Sendable {
    public let chatID: Int64
    public let messageID: Int
    public let reaction: [TelegramReactionEmoji]
    public let isBig: Bool

    enum CodingKeys: String, CodingKey {
        case chatID = "chat_id"
        case messageID = "message_id"
        case reaction
        case isBig = "is_big"
    }

    public init(chatID: Int64, messageID: Int, reaction: [TelegramReactionEmoji], isBig: Bool = false) {
        self.chatID = chatID
        self.messageID = messageID
        self.reaction = reaction
        self.isBig = isBig
    }
}

public struct TelegramSendMessageResponse: Codable, Hashable, Sendable {
    public let ok: Bool
    public let result: TelegramMessage

    public init(ok: Bool, result: TelegramMessage) {
        self.ok = ok
        self.result = result
    }
}

public struct TelegramSendPhotoResponse: Codable, Hashable, Sendable {
    public let ok: Bool
    public let result: TelegramMessage

    public init(ok: Bool, result: TelegramMessage) {
        self.ok = ok
        self.result = result
    }
}

public struct TelegramSendDocumentResponse: Codable, Hashable, Sendable {
    public let ok: Bool
    public let result: TelegramMessage

    public init(ok: Bool, result: TelegramMessage) {
        self.ok = ok
        self.result = result
    }
}

public struct TelegramSendChatActionRequest: Codable, Hashable, Sendable {
    public let chatID: Int64
    public let action: String

    enum CodingKeys: String, CodingKey {
        case chatID = "chat_id"
        case action
    }
}

public struct TelegramSetMyCommandsRequest: Codable, Hashable, Sendable {
    public let commands: [TelegramBotCommand]
}

public struct TelegramBooleanResponse: Codable, Hashable, Sendable {
    public let ok: Bool
    public let result: Bool

    public init(ok: Bool, result: Bool) {
        self.ok = ok
        self.result = result
    }
}

public struct TelegramGetUpdatesRequest: Codable, Hashable, Sendable {
    public let offset: Int?
    public let timeout: Int

    public init(offset: Int?, timeout: Int) {
        self.offset = offset
        self.timeout = timeout
    }
}

public struct TelegramAPIErrorEnvelope: Decodable, Sendable {
    public let ok: Bool
    public let errorCode: Int?
    public let description: String?

    enum CodingKeys: String, CodingKey {
        case ok
        case errorCode = "error_code"
        case description
    }
}
