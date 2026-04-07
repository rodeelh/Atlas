import Foundation
import UniformTypeIdentifiers
import AtlasShared

public struct ImageArtifactStore: Sendable {
    public let rootDirectory: URL

    public init(rootDirectory: URL? = nil) throws {
        self.rootDirectory = try rootDirectory ?? Self.defaultRootDirectory()
        try FileManager.default.createDirectory(at: self.rootDirectory, withIntermediateDirectories: true)
    }

    func saveImages(
        _ images: [GeneratedImagePayload],
        provider: ImageProviderType
    ) throws -> [ImageArtifact] {
        try images.enumerated().map { index, payload in
            try saveImage(payload, provider: provider, index: index)
        }
    }

    private func saveImage(
        _ payload: GeneratedImagePayload,
        provider: ImageProviderType,
        index: Int
    ) throws -> ImageArtifact {
        let fileExtension = fileExtension(for: payload.mimeType)
        let fileName = "\(provider.rawValue)-\(timestamp)-\(index + 1)-\(UUID().uuidString.prefix(8)).\(fileExtension)"
        let fileURL = rootDirectory.appendingPathComponent(fileName, isDirectory: false)
        try payload.data.write(to: fileURL, options: .atomic)

        return ImageArtifact(
            id: UUID(),
            filePath: fileURL.path,
            fileName: fileName,
            mimeType: payload.mimeType,
            byteCount: payload.data.count
        )
    }

    private var timestamp: String {
        let formatter = DateFormatter()
        formatter.locale = Locale(identifier: "en_US_POSIX")
        formatter.dateFormat = "yyyyMMdd-HHmmss"
        return formatter.string(from: .now)
    }

    private func fileExtension(for mimeType: String) -> String {
        if let type = UTType(mimeType: mimeType), let preferred = type.preferredFilenameExtension {
            return preferred
        }
        return "png"
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
            .appendingPathComponent("ImageArtifacts", isDirectory: true)
    }
}
