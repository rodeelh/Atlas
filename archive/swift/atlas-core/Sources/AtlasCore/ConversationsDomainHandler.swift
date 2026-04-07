import Foundation
import NIOHTTP1
import AtlasShared
import AtlasMemory

// MARK: - ConversationsDomainHandler

/// Handles conversation, message, memory, and mind-doc routes.
///
/// Routes owned:
///   POST   /message
///   GET    /conversations
///   GET    /conversations/search
///   GET    /conversations/:id
///   GET    /memories
///   GET    /memories/search
///   POST   /memories
///   GET    /memories/:id
///   POST   /memories/:id/update
///   POST   /memories/:id/confirm
///   POST   /memories/:id/delete
///   GET    /mind
///   PUT    /mind
///   POST   /mind/regenerate
///   GET    /skills-memory
///   PUT    /skills-memory
struct ConversationsDomainHandler: RuntimeDomainHandler {
    let runtime: AgentRuntime

    func handle(
        method: HTTPMethod,
        path: String,
        queryItems: [String: String],
        body: String,
        headers: HTTPHeaders
    ) async throws -> EncodedResponse? {
        switch (method, path) {
        case (.POST, "/message"):
            let requestData = Data(body.utf8)
            let request = try AtlasJSON.decoder.decode(AtlasMessageRequest.self, from: requestData)
            let response = await runtime.handleMessage(request)
            return try EncodedResponse(status: .ok, payload: AtlasJSON.encoder.encode(response))

        case (.GET, "/conversations"):
            let limit  = queryItems["limit"].flatMap(Int.init) ?? 50
            let offset = queryItems["offset"].flatMap(Int.init) ?? 0
            let summaries = await runtime.conversationSummaries(limit: limit, offset: offset)
            return try EncodedResponse(status: .ok, payload: AtlasJSON.encoder.encode(summaries))

        case (.GET, "/conversations/search"):
            let query = queryItems["query"]?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
            guard !query.isEmpty else {
                throw RuntimeAPIError.invalidRequest("A search query is required.")
            }
            let limit = queryItems["limit"].flatMap(Int.init) ?? 50
            let summaries = await runtime.searchConversations(query: query, limit: limit)
            return try EncodedResponse(status: .ok, payload: AtlasJSON.encoder.encode(summaries))

        case (.GET, "/memories"):
            let limit = queryItems["limit"].flatMap(Int.init) ?? 500
            let category = queryItems["category"].flatMap(MemoryCategory.init(rawValue:))
            let memories = await runtime.memories(limit: limit, category: category)
            return try EncodedResponse(status: .ok, payload: AtlasJSON.encoder.encode(memories))

        case (.GET, "/memories/search"):
            let query = queryItems["query"]?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
            guard !query.isEmpty else {
                throw RuntimeAPIError.invalidRequest("A search query is required.")
            }
            let category = queryItems["category"].flatMap(MemoryCategory.init(rawValue:))
            let limit = queryItems["limit"].flatMap(Int.init) ?? 200
            let memories = await runtime.searchMemories(query: query, category: category, limit: limit)
            return try EncodedResponse(status: .ok, payload: AtlasJSON.encoder.encode(memories))

        case (.POST, "/memories"):
            let requestData = Data(body.utf8)
            let request = try AtlasJSON.decoder.decode(AtlasMemoryCreateRequest.self, from: requestData)
            let memory = try await runtime.createMemory(request: request)
            return try EncodedResponse(status: .ok, payload: AtlasJSON.encoder.encode(memory))

        case (.GET, "/mind"):
            let content = await runtime.mindContent()
            struct MindResponse: Encodable { let content: String }
            return try EncodedResponse(status: .ok, payload: AtlasJSON.encoder.encode(MindResponse(content: content)))

        case (.PUT, "/mind"):
            struct MindUpdateRequest: Decodable { let content: String }
            let req = try AtlasJSON.decoder.decode(MindUpdateRequest.self, from: Data(body.utf8))
            try await runtime.updateMindContent(req.content)
            return EncodedResponse(status: .ok, payload: Data("{}".utf8))

        case (.GET, "/skills-memory"):
            let content = await runtime.skillsMemoryContent()
            struct SkillsResponse: Encodable { let content: String }
            return try EncodedResponse(status: .ok, payload: AtlasJSON.encoder.encode(SkillsResponse(content: content)))

        case (.PUT, "/skills-memory"):
            struct SkillsUpdateRequest: Decodable { let content: String }
            let req = try AtlasJSON.decoder.decode(SkillsUpdateRequest.self, from: Data(body.utf8))
            try await runtime.updateSkillsMemory(req.content)
            return EncodedResponse(status: .ok, payload: Data("{}".utf8))

        default:
            break
        }

        // Parameterised path matches

        // GET /conversations/:id
        if method == .GET, path.hasPrefix("/conversations/") {
            let idString = String(path.dropFirst("/conversations/".count))
            guard let id = UUID(uuidString: idString) else {
                throw RuntimeAPIError.invalidRequest("Invalid conversation ID.")
            }
            guard let detail = await runtime.conversationDetail(id: id) else {
                throw RuntimeAPIError.notFound("Conversation not found.")
            }
            return try EncodedResponse(status: .ok, payload: AtlasJSON.encoder.encode(detail))
        }

        // /memories/:id routes
        if let memoryResponse = try await routeMemoryAction(method: method, path: path, body: body) {
            return memoryResponse
        }

        // POST /mind/regenerate
        if let mindResponse = try await routeMindAction(method: method, path: path) {
            return mindResponse
        }

        return nil
    }

    // MARK: - Memory sub-routes (/memories/:id/*)

    private func routeMemoryAction(method: HTTPMethod, path: String, body: String) async throws -> EncodedResponse? {
        let components = path.split(separator: "/").map(String.init)

        guard components.count >= 2, components[0] == "memories", let id = UUID(uuidString: components[1]) else {
            return nil
        }

        if method == .GET, components.count == 2 {
            guard let memory = await runtime.memory(id: id) else {
                throw RuntimeAPIError.notFound("Memory item not found.")
            }
            return try EncodedResponse(status: .ok, payload: AtlasJSON.encoder.encode(memory))
        }

        guard method == .POST, components.count == 3 else {
            return nil
        }

        switch components[2] {
        case "update":
            let request = try AtlasJSON.decoder.decode(AtlasMemoryUpdateRequest.self, from: Data(body.utf8))
            let memory = try await runtime.updateMemory(id: id, request: request)
            return try EncodedResponse(status: .ok, payload: AtlasJSON.encoder.encode(memory))
        case "confirm":
            let memory = try await runtime.confirmMemory(id: id)
            return try EncodedResponse(status: .ok, payload: AtlasJSON.encoder.encode(memory))
        case "delete":
            let deleted = try await runtime.deleteMemory(id: id)
            return try EncodedResponse(status: .ok, payload: AtlasJSON.encoder.encode(deleted))
        default:
            return nil
        }
    }

    // MARK: - Mind sub-routes (/mind/*)

    private func routeMindAction(method: HTTPMethod, path: String) async throws -> EncodedResponse? {
        let components = path.split(separator: "/").map(String.init)
        guard !components.isEmpty, components[0] == "mind" else { return nil }

        if method == .POST, components.count == 2, components[1] == "regenerate" {
            let content = try await runtime.regenerateMind()
            struct MindResponse: Encodable { let content: String }
            return try EncodedResponse(status: .ok, payload: AtlasJSON.encoder.encode(MindResponse(content: content)))
        }

        return nil
    }
}
