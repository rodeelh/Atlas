import Foundation
import AtlasShared

public protocol ImageProvider: Sendable {
    var providerID: ImageProviderType { get }
    var displayName: String { get }
    var supportsEdit: Bool { get }

    func validateConfiguration() async -> ImageProviderValidation
    func generateImage(request: ImageProviderGenerateRequest) async throws -> ImageGenerationOutput
    func editImage(request: ImageProviderEditRequest) async throws -> ImageGenerationOutput
}
