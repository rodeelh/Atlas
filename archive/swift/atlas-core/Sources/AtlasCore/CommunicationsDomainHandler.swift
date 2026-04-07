import Foundation
import NIOHTTP1
import AtlasShared

// MARK: - CommunicationsDomainHandler

/// Handles communications platform and channel routes.
///
/// Routes owned:
///   GET    /communications
///   GET    /communications/channels
///   GET    /communications/platforms/:platform/setup
///   PUT    /communications/platforms/:platform
///   POST   /communications/platforms/:platform/validate
///   GET    /telegram/chats
struct CommunicationsDomainHandler: RuntimeDomainHandler {
    let runtime: AgentRuntime

    func handle(
        method: HTTPMethod,
        path: String,
        queryItems: [String: String],
        body: String,
        headers: HTTPHeaders
    ) async throws -> EncodedResponse? {
        switch (method, path) {
        case (.GET, "/communications"):
            let snapshot = await runtime.communicationsSnapshot()
            return try EncodedResponse(status: .ok, payload: AtlasJSON.encoder.encode(snapshot))

        case (.GET, "/communications/channels"):
            let channels = await runtime.communicationChannels()
            return try EncodedResponse(status: .ok, payload: AtlasJSON.encoder.encode(channels))

        case (.GET, "/telegram/chats"):
            let chats = await runtime.knownTelegramChats()
            return try EncodedResponse(status: .ok, payload: AtlasJSON.encoder.encode(chats))

        default:
            break
        }

        // GET /communications/platforms/:platform/setup
        if method == .GET,
           path.hasPrefix("/communications/platforms/"),
           path.hasSuffix("/setup") {
            struct SetupValuesResponse: Encodable { let values: [String: String] }
            let platformRaw = String(path
                .dropFirst("/communications/platforms/".count)
                .dropLast("/setup".count))
            guard let platform = ChatPlatform(rawValue: platformRaw) else {
                throw RuntimeAPIError.invalidRequest("Unknown communication platform '\(platformRaw)'.")
            }
            let values = await runtime.communicationSetupValues(for: platform)
            return try EncodedResponse(status: .ok, payload: AtlasJSON.encoder.encode(SetupValuesResponse(values: values)))
        }

        // PUT /communications/platforms/:platform
        if method == .PUT, path.hasPrefix("/communications/platforms/") {
            struct UpdatePlatformRequest: Decodable { let enabled: Bool }
            let platformRaw = String(path.dropFirst("/communications/platforms/".count))
            guard let platform = ChatPlatform(rawValue: platformRaw) else {
                throw RuntimeAPIError.invalidRequest("Unknown communication platform '\(platformRaw)'.")
            }
            let req = try AtlasJSON.decoder.decode(UpdatePlatformRequest.self, from: Data(body.utf8))
            let status = try await runtime.updateCommunicationPlatform(platform: platform, enabled: req.enabled)
            return try EncodedResponse(status: .ok, payload: AtlasJSON.encoder.encode(status))
        }

        // POST /communications/platforms/:platform/validate
        if method == .POST,
           path.hasPrefix("/communications/platforms/"),
           path.hasSuffix("/validate") {
            struct ValidatePlatformRequest: Decodable {
                let credentials: [String: String]?
                let config: ValidatePlatformConfig?
            }
            struct ValidatePlatformConfig: Decodable {
                let discordClientID: String?
            }
            let platformRaw = String(path
                .dropFirst("/communications/platforms/".count)
                .dropLast("/validate".count))
            guard let platform = ChatPlatform(rawValue: platformRaw) else {
                throw RuntimeAPIError.invalidRequest("Unknown communication platform '\(platformRaw)'.")
            }
            let requestData = Data(body.utf8)
            let request = if requestData.isEmpty {
                ValidatePlatformRequest(credentials: nil, config: nil)
            } else {
                try AtlasJSON.decoder.decode(ValidatePlatformRequest.self, from: requestData)
            }
            let status = try await runtime.validateCommunicationPlatform(
                platform,
                credentialOverrides: request.credentials ?? [:],
                configOverrides: CommunicationValidationConfigOverrides(
                    discordClientID: request.config?.discordClientID
                )
            )
            return try EncodedResponse(status: .ok, payload: AtlasJSON.encoder.encode(status))
        }

        return nil
    }
}
