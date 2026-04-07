import Foundation
import AtlasShared

public struct ImageProviderValidation: Sendable {
    public let providerType: ImageProviderType
    public let status: SkillValidationStatus
    public let summary: String
    public let issues: [String]

    public init(
        providerType: ImageProviderType,
        status: SkillValidationStatus,
        summary: String,
        issues: [String] = []
    ) {
        self.providerType = providerType
        self.status = status
        self.summary = summary
        self.issues = issues
    }
}

public enum ImageGenerationError: LocalizedError {
    case promptRequired
    case providerNotConfigured
    case providerCredentialMissing(ImageProviderType)
    case invalidProviderSelection
    case invalidSize(String)
    case invalidQuality(String)
    case inputImageMissing(String)
    case inputImageUnreadable(String)
    case editNotSupported(ImageProviderType)
    case invalidResponse(String)
    case providerFailure(String)

    public var errorDescription: String? {
        switch self {
        case .promptRequired:
            return "Enter a prompt before generating an image."
        case .providerNotConfigured:
            return "No active image provider is configured in Settings."
        case .providerCredentialMissing(let provider):
            return "No API key is stored for \(provider.title)."
        case .invalidProviderSelection:
            return "The selected image provider is not available."
        case .invalidSize(let size):
            return "The image size '\(size)' is not supported."
        case .invalidQuality(let quality):
            return "The image quality '\(quality)' is not supported."
        case .inputImageMissing(let reference):
            return "Atlas could not find the input image at '\(reference)'."
        case .inputImageUnreadable(let reference):
            return "Atlas could not read the input image '\(reference)'."
        case .editNotSupported(let provider):
            return "\(provider.title) does not support image editing in Atlas v1."
        case .invalidResponse(let message):
            return message
        case .providerFailure(let message):
            return message
        }
    }
}
