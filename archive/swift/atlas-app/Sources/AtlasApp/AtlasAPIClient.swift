import Foundation
import AtlasLogging
import AtlasShared
import AtlasSkills

enum AtlasAPIClientError: LocalizedError {
    case invalidResponse
    case unexpectedStatusCode(Int, String)

    var errorDescription: String? {
        switch self {
        case .invalidResponse:
            return "The Atlas runtime returned an invalid response."
        case .unexpectedStatusCode(let statusCode, let message):
            return "The Atlas runtime returned status code \(statusCode): \(message)"
        }
    }
}

public struct RemoteAccessStatusResponse: Decodable {
    public let remoteAccessEnabled: Bool
    public let port: Int
    public let lanIP: String?
    public let accessURL: String?
}

public struct ModelSelectorInfo: Decodable {
    let primaryModel: String?
    let fastModel: String?
    let lastRefreshedAt: Date?
}

struct RuntimeConfigUpdateResult: Decodable {
    let config: RuntimeConfigSnapshot
    let restartRequired: Bool
}

struct AtlasAPIClient {
    private let session: URLSession
    private let config: AtlasConfig
    private let logger = AtlasLogger.app

    init(
        session: URLSession = .shared,
        config: AtlasConfig = AtlasConfig()
    ) {
        self.session = session
        self.config = config
    }

    func sendMessage(conversationID: UUID?, message: String) async throws -> AtlasMessageResponseEnvelope {
        let payload = AtlasMessageRequest(conversationID: conversationID, message: message)
        let request = try makeRequest(path: "/message", method: "POST", body: payload)
        return try await perform(request, decodeAs: AtlasMessageResponseEnvelope.self)
    }

    func fetchStatus() async throws -> AtlasRuntimeStatus {
        let request = try makeRequest(path: "/status", method: "GET")
        return try await perform(request, decodeAs: AtlasRuntimeStatus.self)
    }

    func fetchLogs() async throws -> [AtlasLogEntry] {
        let request = try makeRequest(path: "/logs", method: "GET")
        return try await perform(request, decodeAs: [AtlasLogEntry].self)
    }

    func fetchPendingApprovals() async throws -> [ApprovalRequest] {
        let request = try makeRequest(path: "/approvals", method: "GET")
        return try await perform(request, decodeAs: [ApprovalRequest].self)
    }

    func fetchSkills() async throws -> [AtlasSkillRecord] {
        let request = try makeRequest(path: "/skills", method: "GET")
        return try await perform(request, decodeAs: [AtlasSkillRecord].self)
    }

    func fetchMemories() async throws -> [MemoryItem] {
        let request = try makeRequest(path: "/memories", method: "GET")
        return try await perform(request, decodeAs: [MemoryItem].self)
    }

    func searchMemories(query: String, category: MemoryCategory? = nil) async throws -> [MemoryItem] {
        var components = URLComponents()
        components.path = "/memories/search"
        components.queryItems = [
            URLQueryItem(name: "query", value: query)
        ]

        if let category {
            components.queryItems?.append(URLQueryItem(name: "category", value: category.rawValue))
        }

        let request = try makeRequest(path: components.string ?? "/memories/search", method: "GET")
        return try await perform(request, decodeAs: [MemoryItem].self)
    }

    func fetchMemory(id: UUID) async throws -> MemoryItem {
        let request = try makeRequest(path: "/memories/\(id.uuidString)", method: "GET")
        return try await perform(request, decodeAs: MemoryItem.self)
    }

    func createMemory(_ payload: AtlasMemoryCreateRequest) async throws -> MemoryItem {
        let request = try makeRequest(path: "/memories", method: "POST", body: payload)
        return try await perform(request, decodeAs: MemoryItem.self)
    }

    func updateMemory(id: UUID, request payload: AtlasMemoryUpdateRequest) async throws -> MemoryItem {
        let request = try makeRequest(
            path: "/memories/\(id.uuidString)/update",
            method: "POST",
            body: payload
        )
        return try await perform(request, decodeAs: MemoryItem.self)
    }

    func confirmMemory(id: UUID) async throws -> MemoryItem {
        let request = try makeRequest(path: "/memories/\(id.uuidString)/confirm", method: "POST")
        return try await perform(request, decodeAs: MemoryItem.self)
    }

    func deleteMemory(id: UUID) async throws -> MemoryItem {
        let request = try makeRequest(path: "/memories/\(id.uuidString)/delete", method: "POST")
        return try await perform(request, decodeAs: MemoryItem.self)
    }

    func fetchConfig() async throws -> RuntimeConfigSnapshot {
        let request = try makeRequest(path: "/config", method: "GET")
        return try await perform(request, decodeAs: RuntimeConfigSnapshot.self)
    }

    func updateConfig(_ snapshot: RuntimeConfigSnapshot) async throws -> RuntimeConfigUpdateResult {
        let request = try makeRequest(path: "/config", method: "PUT", body: snapshot)
        return try await perform(request, decodeAs: RuntimeConfigUpdateResult.self)
    }

    func fetchActionPolicies() async throws -> [String: ActionApprovalPolicy] {
        let request = try makeRequest(path: "/action-policies", method: "GET")
        return try await perform(request, decodeAs: [String: ActionApprovalPolicy].self)
    }

    func setActionPolicy(_ policy: ActionApprovalPolicy, for actionID: String) async throws -> [String: ActionApprovalPolicy] {
        struct SetPolicyPayload: Encodable {
            let policy: ActionApprovalPolicy
        }

        let request = try makeRequest(
            path: "/action-policies/\(encodedPathComponent(actionID))",
            method: "PUT",
            body: SetPolicyPayload(policy: policy)
        )
        return try await perform(request, decodeAs: [String: ActionApprovalPolicy].self)
    }

    func fetchFileAccessRoots() async throws -> [ApprovedFileAccessRoot] {
        let request = try makeRequest(path: "/skills/file-system/roots", method: "GET")
        return try await perform(request, decodeAs: [ApprovedFileAccessRoot].self)
    }

    func approve(toolCallID: UUID) async throws -> AtlasMessageResponseEnvelope {
        let request = try makeRequest(path: "/approvals/\(toolCallID.uuidString)/approve", method: "POST")
        return try await perform(request, decodeAs: AtlasMessageResponseEnvelope.self)
    }

    func deny(toolCallID: UUID) async throws -> ApprovalRequest {
        let request = try makeRequest(path: "/approvals/\(toolCallID.uuidString)/deny", method: "POST")
        return try await perform(request, decodeAs: ApprovalRequest.self)
    }

    func enableSkill(id: String) async throws -> AtlasSkillRecord {
        let request = try makeRequest(path: "/skills/\(encodedPathComponent(id))/enable", method: "POST")
        return try await perform(request, decodeAs: AtlasSkillRecord.self)
    }

    func disableSkill(id: String) async throws -> AtlasSkillRecord {
        let request = try makeRequest(path: "/skills/\(encodedPathComponent(id))/disable", method: "POST")
        return try await perform(request, decodeAs: AtlasSkillRecord.self)
    }

    func validateSkill(id: String) async throws -> AtlasSkillRecord {
        let request = try makeRequest(path: "/skills/\(encodedPathComponent(id))/validate", method: "POST")
        return try await perform(request, decodeAs: AtlasSkillRecord.self)
    }

    func addFileAccessRoot(bookmarkData: Data) async throws -> ApprovedFileAccessRoot {
        let request = try makeRequest(
            path: "/skills/file-system/roots",
            method: "POST",
            body: FileAccessRootGrantRequest(bookmarkData: bookmarkData)
        )
        return try await perform(request, decodeAs: ApprovedFileAccessRoot.self)
    }

    func removeFileAccessRoot(id: UUID) async throws -> ApprovedFileAccessRoot {
        let request = try makeRequest(
            path: "/skills/file-system/roots/\(id.uuidString)/remove",
            method: "POST"
        )
        return try await perform(request, decodeAs: ApprovedFileAccessRoot.self)
    }

    func fetchModels() async throws -> ModelSelectorInfo {
        let request = try makeRequest(path: "/models", method: "GET")
        return try await perform(request, decodeAs: ModelSelectorInfo.self)
    }

    func refreshModels() async throws -> ModelSelectorInfo {
        let request = try makeRequest(path: "/models/refresh", method: "POST")
        return try await perform(request, decodeAs: ModelSelectorInfo.self)
    }

    // MARK: - Remote access

    func fetchRemoteAccessStatus() async throws -> RemoteAccessStatusResponse {
        let request = try makeRequest(path: "/auth/remote-status", method: "GET")
        return try await perform(request, decodeAs: RemoteAccessStatusResponse.self)
    }

    func fetchRemoteKey() async throws -> String {
        struct KeyResponse: Decodable { let key: String }
        let request = try makeRequest(path: "/auth/remote-key", method: "GET")
        let response = try await perform(request, decodeAs: KeyResponse.self)
        return response.key
    }

    func revokeRemoteSessions() async throws {
        let request = try makeRequest(path: "/auth/remote-sessions", method: "DELETE")
        let (_, response) = try await session.data(for: request)
        guard let httpResponse = response as? HTTPURLResponse,
              (200..<300).contains(httpResponse.statusCode) else {
            throw AtlasAPIClientError.invalidResponse
        }
    }

    func invalidateCredentialCache() async throws {
        let request = try makeRequest(path: "/api-keys/invalidate-cache", method: "POST")
        let (_, response) = try await session.data(for: request)
        guard let httpResponse = response as? HTTPURLResponse,
              (200..<300).contains(httpResponse.statusCode) else {
            throw AtlasAPIClientError.invalidResponse
        }
    }

    // MARK: - Launch token

    /// Fetch a short-lived launch token from the daemon for the menu bar → browser handoff.
    ///
    /// The token is used to open `/auth/bootstrap?token=<token>` in the browser, which
    /// exchanges it for a session cookie and redirects to `/web`. Tokens expire in 60 s.
    func fetchLaunchToken() async throws -> String {
        struct TokenResponse: Decodable { let token: String }
        let request = try makeRequest(path: "/auth/token", method: "GET")
        let response = try await perform(request, decodeAs: TokenResponse.self)
        return response.token
    }

    /// Returns the URL to open for SSE streaming of a conversation turn.
    /// Used by the menu bar "Open Web UI" action to deep-link to the right conversation.
    func messageStreamURL(conversationID: String) -> URL {
        URL(string: "http://127.0.0.1:\(config.runtimePort)/message/stream?conversationID=\(conversationID)")!
    }

    private func makeRequest(path: String, method: String) throws -> URLRequest {
        try makeRequest(path: path, method: method, bodyData: nil)
    }

    private func makeRequest<T: Encodable>(path: String, method: String, body: T? = nil) throws -> URLRequest {
        let bodyData = try body.map { try AtlasJSON.encoder.encode($0) }
        return try makeRequest(path: path, method: method, bodyData: bodyData)
    }

    private func makeRequest(path: String, method: String, bodyData: Data?) throws -> URLRequest {
        let url = URL(string: "http://127.0.0.1:\(config.runtimePort)\(path)")!
        var request = URLRequest(url: url)
        request.httpMethod = method
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")

        request.httpBody = bodyData

        return request
    }

    private func perform<Response: Decodable>(_ request: URLRequest, decodeAs type: Response.Type) async throws -> Response {
        logger.debug("Calling Atlas runtime API", metadata: [
            "method": request.httpMethod ?? "GET",
            "path": request.url?.path ?? "/"
        ])

        let (data, response) = try await session.data(for: request)
        guard let httpResponse = response as? HTTPURLResponse else {
            throw AtlasAPIClientError.invalidResponse
        }

        guard (200..<300).contains(httpResponse.statusCode) else {
            let message = String(decoding: data, as: UTF8.self)
            throw AtlasAPIClientError.unexpectedStatusCode(httpResponse.statusCode, message)
        }

        return try AtlasJSON.decoder.decode(Response.self, from: data)
    }

    private func encodedPathComponent(_ value: String) -> String {
        value.addingPercentEncoding(withAllowedCharacters: .urlPathAllowed) ?? value
    }
}
