import Foundation
import AtlasLogging
import AtlasShared

public enum SlackClientError: LocalizedError {
    case missingBotToken
    case missingAppToken
    case invalidResponse
    case unexpectedStatusCode(Int, String)
    case apiError(String)
    case websocketClosed(Int?, String?)
    case encodingFailed

    public var errorDescription: String? {
        switch self {
        case .missingBotToken:
            return "No Slack bot token was found in the macOS Keychain."
        case .missingAppToken:
            return "No Slack app token was found in the macOS Keychain."
        case .invalidResponse:
            return "The Slack API returned an invalid response."
        case .unexpectedStatusCode(let code, let message):
            return "The Slack API returned status code \(code): \(message)"
        case .apiError(let message):
            return "Slack API error: \(message)"
        case .websocketClosed(let code, let reason):
            if let code {
                return "The Slack Socket Mode connection closed (code \(code)): \(reason ?? "no reason provided")."
            }
            return "The Slack Socket Mode connection closed unexpectedly."
        case .encodingFailed:
            return "Atlas could not encode the Slack request payload."
        }
    }
}

public protocol SlackClienting: Sendable {
    func authTestBot() async throws -> SlackAuthTestResponse
    func openSocketModeConnection() async throws -> SlackSocketConnection
    func postMessage(channelID: String, text: String, threadID: String?) async throws -> SlackPostedMessage
    func makeSocketTask(url: URL) -> URLSessionWebSocketTask
}

public struct SlackAuthTestResponse: Codable, Hashable, Sendable {
    public let ok: Bool
    public let userID: String
    public let user: String?
    public let team: String?
    public let teamID: String?

    public init(ok: Bool, userID: String, user: String?, team: String?, teamID: String?) {
        self.ok = ok
        self.userID = userID
        self.user = user
        self.team = team
        self.teamID = teamID
    }

    enum CodingKeys: String, CodingKey {
        case ok
        case userID = "user_id"
        case user
        case team
        case teamID = "team_id"
    }
}

public struct SlackSocketConnection: Codable, Hashable, Sendable {
    public let ok: Bool
    public let url: String

    public init(ok: Bool, url: String) {
        self.ok = ok
        self.url = url
    }

    public var socketURL: URL? { URL(string: url) }
}

public struct SlackPostedMessage: Codable, Hashable, Sendable {
    public let ok: Bool
    public let channel: String
    public let ts: String

    public init(ok: Bool, channel: String, ts: String) {
        self.ok = ok
        self.channel = channel
        self.ts = ts
    }
}

public final class SlackClient: SlackClienting, Sendable {
    private let session: URLSession
    private let config: AtlasConfig
    private let logger = AtlasLogger.network

    public init(session: URLSession = .shared, config: AtlasConfig = AtlasConfig()) {
        self.session = session
        self.config = config
    }

    public func authTestBot() async throws -> SlackAuthTestResponse {
        try await perform(
            method: "POST",
            path: "/auth.test",
            tokenType: .bot,
            body: Optional<String>.none,
            decodeAs: SlackAuthTestResponse.self
        )
    }

    public func openSocketModeConnection() async throws -> SlackSocketConnection {
        try await perform(
            method: "POST",
            path: "/apps.connections.open",
            tokenType: .app,
            body: Optional<String>.none,
            decodeAs: SlackSocketConnection.self
        )
    }

    public func postMessage(channelID: String, text: String, threadID: String?) async throws -> SlackPostedMessage {
        try await perform(
            method: "POST",
            path: "/chat.postMessage",
            tokenType: .bot,
            body: SlackPostMessageRequest(channel: channelID, text: text, threadTS: threadID),
            decodeAs: SlackPostedMessage.self
        )
    }

    public func makeSocketTask(url: URL) -> URLSessionWebSocketTask {
        session.webSocketTask(with: url)
    }

    private func fetchToken(_ type: SlackTokenType) throws -> String {
        let token: String
        switch type {
        case .bot:
            token = try config.slackBotToken()
        case .app:
            token = try config.slackAppToken()
        }

        let trimmed = token.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else {
            throw type == .bot ? SlackClientError.missingBotToken : SlackClientError.missingAppToken
        }
        return trimmed
    }

    private func perform<Body: Encodable, Response: Decodable>(
        method: String,
        path: String,
        tokenType: SlackTokenType,
        body: Body?,
        decodeAs type: Response.Type
    ) async throws -> Response {
        var request = URLRequest(url: URL(string: "https://slack.com/api\(path)")!)
        request.httpMethod = method
        request.setValue("Bearer \(try fetchToken(tokenType))", forHTTPHeaderField: "Authorization")
        request.setValue("application/json; charset=utf-8", forHTTPHeaderField: "Content-Type")

        if let body {
            do {
                request.httpBody = try AtlasJSON.encoder.encode(body)
            } catch {
                throw SlackClientError.encodingFailed
            }
        }

        let (data, response) = try await session.data(for: request)
        guard let httpResponse = response as? HTTPURLResponse else {
            throw SlackClientError.invalidResponse
        }

        guard (200..<300).contains(httpResponse.statusCode) else {
            let message = String(decoding: data, as: UTF8.self)
            logger.error("Slack request failed", metadata: [
                "path": path,
                "status_code": "\(httpResponse.statusCode)"
            ])
            throw SlackClientError.unexpectedStatusCode(httpResponse.statusCode, message)
        }

        if let envelope = try? AtlasJSON.decoder.decode(SlackErrorEnvelope.self, from: data), envelope.ok == false {
            throw SlackClientError.apiError(envelope.error ?? "Unknown Slack API error.")
        }

        do {
            return try AtlasJSON.decoder.decode(type, from: data)
        } catch {
            logger.error("Slack response decoding failed", metadata: [
                "path": path,
                "error": error.localizedDescription
            ])
            throw SlackClientError.invalidResponse
        }
    }
}

private enum SlackTokenType {
    case bot
    case app
}

private struct SlackPostMessageRequest: Encodable {
    let channel: String
    let text: String
    let threadTS: String?

    enum CodingKeys: String, CodingKey {
        case channel
        case text
        case threadTS = "thread_ts"
    }
}

private struct SlackErrorEnvelope: Decodable {
    let ok: Bool
    let error: String?
}
