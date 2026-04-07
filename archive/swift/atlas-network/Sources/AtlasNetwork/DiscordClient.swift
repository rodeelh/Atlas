import Foundation
import AtlasLogging
import AtlasShared

public enum DiscordClientError: LocalizedError {
    case missingBotToken
    case invalidResponse
    case unexpectedStatusCode(Int, String)
    case missingGatewayURL
    case encodingFailed
    case websocketClosed(Int?, String?)

    public var errorDescription: String? {
        switch self {
        case .missingBotToken:
            return "No Discord bot token was found in the macOS Keychain."
        case .invalidResponse:
            return "The Discord API returned an invalid response."
        case .unexpectedStatusCode(let code, let message):
            return "The Discord API returned status code \(code): \(message)"
        case .missingGatewayURL:
            return "The Discord API did not return a gateway URL."
        case .encodingFailed:
            return "Atlas could not encode the Discord request payload."
        case .websocketClosed(let code, let reason):
            if let code {
                return "The Discord gateway connection closed (code \(code)): \(reason ?? "no reason provided")."
            }
            return "The Discord gateway connection closed unexpectedly."
        }
    }
}

public protocol DiscordClienting: Sendable {
    func getCurrentUser() async throws -> DiscordCurrentUser
    func getGatewayBot() async throws -> DiscordGatewayBot
    func createMessage(channelID: String, content: String, replyToMessageID: String?) async throws -> DiscordCreatedMessage
    func makeGatewayTask(url: URL) -> URLSessionWebSocketTask
}

public struct DiscordCurrentUser: Codable, Hashable, Sendable {
    public let id: String
    public let username: String
    public let globalName: String?

    public init(id: String, username: String, globalName: String?) {
        self.id = id
        self.username = username
        self.globalName = globalName
    }

    enum CodingKeys: String, CodingKey {
        case id
        case username
        case globalName = "global_name"
    }
}

public struct DiscordGatewayBot: Codable, Hashable, Sendable {
    public let url: String
    public let shards: Int?

    public init(url: String, shards: Int?) {
        self.url = url
        self.shards = shards
    }

    public var gatewayURL: URL? {
        URL(string: url).map { url in
            if url.query?.isEmpty == false {
                return url
            }
            return URL(string: "\(url.absoluteString)?v=10&encoding=json")
        } ?? URL(string: "\(url)?v=10&encoding=json")
    }
}

public struct DiscordCreatedMessage: Codable, Hashable, Sendable {
    public let id: String
    public let channelID: String
    public let content: String?

    public init(id: String, channelID: String, content: String?) {
        self.id = id
        self.channelID = channelID
        self.content = content
    }

    enum CodingKeys: String, CodingKey {
        case id
        case channelID = "channel_id"
        case content
    }
}

public final class DiscordClient: DiscordClienting, Sendable {
    private let session: URLSession
    private let config: AtlasConfig
    private let logger = AtlasLogger.network

    public init(session: URLSession = .shared, config: AtlasConfig = AtlasConfig()) {
        self.session = session
        self.config = config
    }

    public func getCurrentUser() async throws -> DiscordCurrentUser {
        try await perform(method: "GET", path: "/users/@me", body: Optional<String>.none, decodeAs: DiscordCurrentUser.self)
    }

    public func getGatewayBot() async throws -> DiscordGatewayBot {
        try await perform(method: "GET", path: "/gateway/bot", body: Optional<String>.none, decodeAs: DiscordGatewayBot.self)
    }

    public func createMessage(channelID: String, content: String, replyToMessageID: String? = nil) async throws -> DiscordCreatedMessage {
        let payload = DiscordCreateMessageRequest(
            content: content,
            allowedMentions: DiscordAllowedMentions(parse: []),
            messageReference: replyToMessageID.map { DiscordMessageReference(messageID: $0) }
        )
        return try await perform(
            method: "POST",
            path: "/channels/\(channelID)/messages",
            body: payload,
            decodeAs: DiscordCreatedMessage.self
        )
    }

    public func makeGatewayTask(url: URL) -> URLSessionWebSocketTask {
        session.webSocketTask(with: url)
    }

    private func fetchBotToken() throws -> String {
        let token = try config.discordBotToken().trimmingCharacters(in: .whitespacesAndNewlines)
        guard !token.isEmpty else {
            throw DiscordClientError.missingBotToken
        }
        return token
    }

    private func perform<Body: Encodable, Response: Decodable>(
        method: String,
        path: String,
        body: Body?,
        decodeAs type: Response.Type
    ) async throws -> Response {
        var request = URLRequest(url: URL(string: "https://discord.com/api/v10\(path)")!)
        request.httpMethod = method
        request.setValue("Bot \(try fetchBotToken())", forHTTPHeaderField: "Authorization")
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        request.setValue("Atlas/0.2", forHTTPHeaderField: "User-Agent")

        if let body {
            do {
                request.httpBody = try AtlasJSON.encoder.encode(body)
            } catch {
                throw DiscordClientError.encodingFailed
            }
        }

        let (data, response) = try await session.data(for: request)
        guard let httpResponse = response as? HTTPURLResponse else {
            throw DiscordClientError.invalidResponse
        }

        guard (200..<300).contains(httpResponse.statusCode) else {
            let message = String(decoding: data, as: UTF8.self)
            logger.error("Discord request failed", metadata: [
                "path": path,
                "status_code": "\(httpResponse.statusCode)"
            ])
            throw DiscordClientError.unexpectedStatusCode(httpResponse.statusCode, message)
        }

        do {
            return try AtlasJSON.decoder.decode(type, from: data)
        } catch {
            logger.error("Discord response decoding failed", metadata: [
                "path": path,
                "error": error.localizedDescription
            ])
            throw DiscordClientError.invalidResponse
        }
    }
}

private struct DiscordCreateMessageRequest: Encodable {
    let content: String
    let allowedMentions: DiscordAllowedMentions
    let messageReference: DiscordMessageReference?

    enum CodingKeys: String, CodingKey {
        case content
        case allowedMentions = "allowed_mentions"
        case messageReference = "message_reference"
    }
}

private struct DiscordAllowedMentions: Encodable {
    let parse: [String]
}

private struct DiscordMessageReference: Encodable {
    let messageID: String

    enum CodingKeys: String, CodingKey {
        case messageID = "message_id"
    }
}
