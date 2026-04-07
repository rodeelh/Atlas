import Foundation
import AtlasShared

public struct ImageGenerateInput: Codable, Sendable {
    public let prompt: String
    public let size: String?
    public let quality: String?
    public let styleHint: String?
}

public struct ImageEditInput: Codable, Sendable {
    public let prompt: String
    public let inputImageReference: String
    public let size: String?
    public let quality: String?
}

public struct ImageArtifact: Codable, Hashable, Sendable {
    public let id: UUID
    public let filePath: String
    public let fileName: String
    public let mimeType: String
    public let byteCount: Int
}

public struct ImageGenerationOutput: Codable, Hashable, Sendable {
    public let providerUsed: String
    public let promptUsed: String
    public let imageCount: Int
    public let images: [ImageArtifact]
    public let metadataSummary: String
}

public struct ImageProviderGenerateRequest: Sendable {
    let prompt: String
    let size: String
    let quality: String?
    let styleHint: String?
}

public struct ImageProviderEditRequest: Sendable {
    let prompt: String
    let inputImageURL: URL
    let size: String
    let quality: String?
}

struct GeneratedImagePayload: Sendable {
    let data: Data
    let mimeType: String
}
