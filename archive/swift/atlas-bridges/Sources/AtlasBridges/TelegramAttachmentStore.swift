import Foundation
import UniformTypeIdentifiers

public enum TelegramAttachmentKind: String, Codable, Hashable, Sendable {
    case image
    case document
}

public struct TelegramInboundAttachment: Hashable, Sendable {
    public let kind: TelegramAttachmentKind
    public let fileID: String
    public let fileUniqueID: String
    public let suggestedFileName: String?
    public let mimeType: String?
    public let fileSize: Int?

    public init(
        kind: TelegramAttachmentKind,
        fileID: String,
        fileUniqueID: String,
        suggestedFileName: String? = nil,
        mimeType: String? = nil,
        fileSize: Int? = nil
    ) {
        self.kind = kind
        self.fileID = fileID
        self.fileUniqueID = fileUniqueID
        self.suggestedFileName = suggestedFileName
        self.mimeType = mimeType
        self.fileSize = fileSize
    }
}

public struct TelegramInboundAttachmentEnvelope: Hashable, Sendable {
    public let caption: String?
    public let attachments: [TelegramInboundAttachment]

    public init(caption: String? = nil, attachments: [TelegramInboundAttachment]) {
        self.caption = caption
        self.attachments = attachments
    }
}

public struct StoredTelegramAttachment: Hashable, Sendable {
    public let id: UUID
    public let kind: TelegramAttachmentKind
    public let fileID: String
    public let originalFileName: String?
    public let mimeType: String?
    public let byteCount: Int
    public let localFileURL: URL
    public let chatID: Int64
    public let messageID: Int
    public let createdAt: Date
}

public struct TelegramAttachmentStore: Sendable {
    public let rootDirectory: URL

    public init(rootDirectory: URL? = nil) throws {
        self.rootDirectory = try rootDirectory ?? Self.defaultRootDirectory()
        try FileManager.default.createDirectory(at: self.rootDirectory, withIntermediateDirectories: true)
    }

    public func save(
        data: Data,
        attachment: TelegramInboundAttachment,
        chatID: Int64,
        messageID: Int
    ) throws -> StoredTelegramAttachment {
        let messageDirectory = rootDirectory
            .appendingPathComponent("chat-\(chatID)", isDirectory: true)
            .appendingPathComponent("message-\(messageID)", isDirectory: true)
        try FileManager.default.createDirectory(at: messageDirectory, withIntermediateDirectories: true)

        let fileName = safeFileName(for: attachment)
        let fileURL = uniqueURL(for: messageDirectory.appendingPathComponent(fileName))
        try data.write(to: fileURL, options: .atomic)

        return StoredTelegramAttachment(
            id: UUID(),
            kind: attachment.kind,
            fileID: attachment.fileID,
            originalFileName: attachment.suggestedFileName,
            mimeType: attachment.mimeType,
            byteCount: data.count,
            localFileURL: fileURL,
            chatID: chatID,
            messageID: messageID,
            createdAt: .now
        )
    }

    private func safeFileName(for attachment: TelegramInboundAttachment) -> String {
        let preferredBase = attachment.suggestedFileName?.trimmingCharacters(in: .whitespacesAndNewlines)
        let sanitizedBase = sanitizeFileName(preferredBase.flatMap { $0.isEmpty ? nil : $0 } ?? "\(attachment.kind.rawValue)-\(attachment.fileUniqueID)")
        let ext = preferredExtension(for: attachment)

        if sanitizedBase.lowercased().hasSuffix(".\(ext.lowercased())") {
            return sanitizedBase
        }

        return "\(sanitizedBase).\(ext)"
    }

    private func sanitizeFileName(_ value: String) -> String {
        let invalid = CharacterSet(charactersIn: "/:\\?%*|\"<>")
        let pieces = value.components(separatedBy: invalid)
        let joined = pieces.joined(separator: "-")
        let trimmed = joined.trimmingCharacters(in: .whitespacesAndNewlines)
        return trimmed.isEmpty ? "attachment" : trimmed
    }

    private func preferredExtension(for attachment: TelegramInboundAttachment) -> String {
        if
            let name = attachment.suggestedFileName,
            let ext = URL(fileURLWithPath: name).pathExtension.nilIfEmpty {
            return ext
        }

        if let mimeType = attachment.mimeType,
           let type = UTType(mimeType: mimeType),
           let ext = type.preferredFilenameExtension {
            return ext
        }

        switch attachment.kind {
        case .image:
            return "jpg"
        case .document:
            return "bin"
        }
    }

    private func uniqueURL(for candidate: URL) -> URL {
        guard FileManager.default.fileExists(atPath: candidate.path) else {
            return candidate
        }

        let stem = candidate.deletingPathExtension().lastPathComponent
        let ext = candidate.pathExtension
        let directory = candidate.deletingLastPathComponent()
        let uniqueName = "\(stem)-\(UUID().uuidString.prefix(8))"
        return directory.appendingPathComponent(ext.isEmpty ? uniqueName : "\(uniqueName).\(ext)")
    }

    private static func defaultRootDirectory() throws -> URL {
        let appSupport = try FileManager.default.url(
            for: .applicationSupportDirectory,
            in: .userDomainMask,
            appropriateFor: nil,
            create: true
        )

        return appSupport
            .appendingPathComponent("ProjectAtlas", isDirectory: true)
            .appendingPathComponent("TelegramAttachments", isDirectory: true)
    }
}

private extension String {
    var nilIfEmpty: String? {
        isEmpty ? nil : self
    }
}
