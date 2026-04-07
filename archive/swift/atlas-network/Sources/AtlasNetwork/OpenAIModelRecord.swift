import Foundation

// MARK: - Response envelope for GET /v1/models

struct OpenAIModelsListResponse: Decodable {
    let data: [OpenAIModelRecord]
}

// MARK: - Single model entry

struct OpenAIModelRecord: Decodable {
    let id: String
    let created: Int?
    let ownedBy: String?

    enum CodingKeys: String, CodingKey {
        case id
        case created
        case ownedBy = "owned_by"
    }
}
