import Foundation
import AtlasShared

struct GoogleNanoBananaProvider: ImageProvider {
    let providerID: ImageProviderType = .googleNanoBanana
    let displayName = "Google Nano Banana"
    let supportsEdit = true

    private let apiKey: String
    private let artifactStore: ImageArtifactStore
    private let session: URLSession
    private let baseURL: URL
    private let modelName: String

    init(
        apiKey: String,
        artifactStore: ImageArtifactStore,
        session: URLSession? = nil,
        baseURL: URL = URL(string: "https://generativelanguage.googleapis.com/v1beta")!,
        modelName: String = "gemini-3.1-flash-image-preview"
    ) {
        self.apiKey = apiKey
        self.artifactStore = artifactStore
        self.baseURL = baseURL
        self.modelName = modelName

        if let session {
            self.session = session
        } else {
            let configuration = URLSessionConfiguration.ephemeral
            configuration.timeoutIntervalForRequest = 90
            configuration.timeoutIntervalForResource = 90
            configuration.httpShouldSetCookies = false
            configuration.httpCookieStorage = nil
            configuration.requestCachePolicy = .reloadIgnoringLocalAndRemoteCacheData
            self.session = URLSession(configuration: configuration)
        }
    }

    func validateConfiguration() async -> ImageProviderValidation {
        var components = URLComponents(url: baseURL.appendingPathComponent("models/\(modelName)"), resolvingAgainstBaseURL: false)
        components?.queryItems = [URLQueryItem(name: "key", value: apiKey)]

        guard let url = components?.url else {
            return ImageProviderValidation(
                providerType: providerID,
                status: .failed,
                summary: "Google Nano Banana configuration is invalid.",
                issues: ["The provider URL could not be built."]
            )
        }

        var request = URLRequest(url: url)
        request.httpMethod = "GET"

        do {
            let (_, response) = try await session.data(for: request)
            guard let http = response as? HTTPURLResponse else {
                return ImageProviderValidation(
                    providerType: providerID,
                    status: .failed,
                    summary: "Google Nano Banana validation returned an invalid response.",
                    issues: ["The validation response was not HTTP."]
                )
            }

            if (200..<300).contains(http.statusCode) {
                return ImageProviderValidation(
                    providerType: providerID,
                    status: .passed,
                    summary: "Google Nano Banana is configured and ready for image generation."
                )
            }

            let status: SkillValidationStatus = (http.statusCode == 401 || http.statusCode == 403) ? .failed : .warning
            return ImageProviderValidation(
                providerType: providerID,
                status: status,
                summary: status == .failed ? "The stored Google Nano Banana key was rejected." : "Google Nano Banana validation could not be completed.",
                issues: ["Google returned status code \(http.statusCode)."]
            )
        } catch {
            return ImageProviderValidation(
                providerType: providerID,
                status: .warning,
                summary: "Google Nano Banana validation could not be completed.",
                issues: [error.localizedDescription]
            )
        }
    }

    func generateImage(request: ImageProviderGenerateRequest) async throws -> ImageGenerationOutput {
        var urlRequest = URLRequest(url: try generateContentURL())
        urlRequest.httpMethod = "POST"
        urlRequest.setValue("application/json", forHTTPHeaderField: "Content-Type")
        urlRequest.httpBody = try AtlasJSON.encoder.encode(
            buildGeneratePayload(
                prompt: combinedPrompt(prompt: request.prompt, styleHint: request.styleHint),
                inlineImage: nil,
                size: request.size,
                quality: request.quality
            )
        )

        let response: GoogleImageResponse = try await decode(urlRequest)
        let artifacts = try artifactStore.saveImages(parseImages(from: response), provider: providerID)
        return ImageGenerationOutput(
            providerUsed: displayName,
            promptUsed: combinedPrompt(prompt: request.prompt, styleHint: request.styleHint),
            imageCount: artifacts.count,
            images: artifacts,
            metadataSummary: "Generated \(artifacts.count) image\(artifacts.count == 1 ? "" : "s") with Google Nano Banana."
        )
    }

    func editImage(request: ImageProviderEditRequest) async throws -> ImageGenerationOutput {
        let imageData: Data
        do {
            imageData = try Data(contentsOf: request.inputImageURL)
        } catch {
            throw ImageGenerationError.inputImageUnreadable(request.inputImageURL.path)
        }
        let mimeType = mimeType(for: request.inputImageURL)

        var urlRequest = URLRequest(url: try generateContentURL())
        urlRequest.httpMethod = "POST"
        urlRequest.setValue("application/json", forHTTPHeaderField: "Content-Type")
        urlRequest.httpBody = try AtlasJSON.encoder.encode(
            buildGeneratePayload(
                prompt: request.prompt,
                inlineImage: GoogleInlineData(mimeType: mimeType, data: imageData.base64EncodedString()),
                size: request.size,
                quality: request.quality
            )
        )

        let response: GoogleImageResponse = try await decode(urlRequest)
        let artifacts = try artifactStore.saveImages(parseImages(from: response), provider: providerID)
        return ImageGenerationOutput(
            providerUsed: displayName,
            promptUsed: request.prompt,
            imageCount: artifacts.count,
            images: artifacts,
            metadataSummary: "Edited \(artifacts.count) image\(artifacts.count == 1 ? "" : "s") with Google Nano Banana."
        )
    }

    private func generateContentURL() throws -> URL {
        var components = URLComponents(
            url: baseURL.appendingPathComponent("models/\(modelName):generateContent"),
            resolvingAgainstBaseURL: false
        )
        components?.queryItems = [URLQueryItem(name: "key", value: apiKey)]
        guard let url = components?.url else {
            throw ImageGenerationError.invalidProviderSelection
        }
        return url
    }

    private func buildGeneratePayload(
        prompt: String,
        inlineImage: GoogleInlineData?,
        size: String,
        quality: String?
    ) -> GoogleGenerateContentRequest {
        let normalizedSize = normalizedGoogleSize(from: size, quality: quality)
        var parts: [GooglePart] = [.init(text: prompt, inlineData: nil)]
        if let inlineImage {
            parts.append(.init(text: nil, inlineData: inlineImage))
        }

        return GoogleGenerateContentRequest(
            contents: [
                GoogleContent(parts: parts)
            ],
            generationConfig: GoogleGenerationConfig(
                responseModalities: ["Image"],
                imageConfig: GoogleImageConfig(
                    aspectRatio: normalizedSize.aspectRatio,
                    imageSize: normalizedSize.imageSize
                )
            )
        )
    }

    private func normalizedGoogleSize(from requested: String, quality: String?) -> (aspectRatio: String, imageSize: String) {
        let aspectRatio: String
        switch requested.lowercased() {
        case "1536x1024":
            aspectRatio = "3:2"
        case "1024x1536":
            aspectRatio = "2:3"
        default:
            aspectRatio = "1:1"
        }

        let imageSize: String
        switch quality?.lowercased() {
        case "high":
            imageSize = "2K"
        default:
            imageSize = "1K"
        }

        return (aspectRatio, imageSize)
    }

    private func parseImages(from response: GoogleImageResponse) throws -> [GeneratedImagePayload] {
        let parts = response.candidates
            .flatMap { $0.content.parts }
            .compactMap(\.inlineData)

        let generated = parts.compactMap { data -> GeneratedImagePayload? in
            guard let bytes = Data(base64Encoded: data.data) else {
                return nil
            }
            return GeneratedImagePayload(data: bytes, mimeType: data.mimeType)
        }

        guard !generated.isEmpty else {
            throw ImageGenerationError.invalidResponse("Google Nano Banana did not return image data.")
        }

        return generated
    }

    private func decode<Response: Decodable>(_ request: URLRequest) async throws -> Response {
        let (data, response) = try await session.data(for: request)
        guard let http = response as? HTTPURLResponse else {
            throw ImageGenerationError.invalidResponse("Google Nano Banana returned an invalid response.")
        }

        guard (200..<300).contains(http.statusCode) else {
            let message = String(decoding: data, as: UTF8.self)
            throw ImageGenerationError.providerFailure(sanitizedProviderMessage(from: message, statusCode: http.statusCode))
        }

        do {
            return try AtlasJSON.decoder.decode(Response.self, from: data)
        } catch {
            throw ImageGenerationError.invalidResponse("Google Nano Banana returned malformed image data.")
        }
    }

    private func combinedPrompt(prompt: String, styleHint: String?) -> String {
        guard let styleHint, !styleHint.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else {
            return prompt
        }
        return "\(prompt)\n\nStyle hint: \(styleHint.trimmingCharacters(in: .whitespacesAndNewlines))"
    }

    private func sanitizedProviderMessage(from _: String, statusCode: Int) -> String {
        if statusCode == 401 || statusCode == 403 {
            return "The stored Google Nano Banana key was rejected."
        }
        if statusCode == 429 {
            return "Google Nano Banana is temporarily unavailable because the quota or rate limit was reached."
        }
        return "Google Nano Banana failed with status code \(statusCode)."
    }

    private func mimeType(for url: URL) -> String {
        switch url.pathExtension.lowercased() {
        case "jpg", "jpeg":
            return "image/jpeg"
        case "webp":
            return "image/webp"
        case "gif":
            return "image/gif"
        default:
            return "image/png"
        }
    }
}

private struct GoogleGenerateContentRequest: Encodable {
    let contents: [GoogleContent]
    let generationConfig: GoogleGenerationConfig

    enum CodingKeys: String, CodingKey {
        case contents
        case generationConfig = "generationConfig"
    }
}

private struct GoogleContent: Encodable {
    let parts: [GooglePart]
}

private struct GooglePart: Encodable {
    let text: String?
    let inlineData: GoogleInlineData?

    enum CodingKeys: String, CodingKey {
        case text
        case inlineData = "inline_data"
    }
}

private struct GoogleInlineData: Codable {
    let mimeType: String
    let data: String

    private enum CodingKeys: String, CodingKey {
        case mimeType
        case mimeTypeSnake = "mime_type"
        case data
    }

    init(mimeType: String, data: String) {
        self.mimeType = mimeType
        self.data = data
    }

    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        self.mimeType = try container.decodeIfPresent(String.self, forKey: .mimeType)
            ?? container.decode(String.self, forKey: .mimeTypeSnake)
        self.data = try container.decode(String.self, forKey: .data)
    }

    func encode(to encoder: Encoder) throws {
        var container = encoder.container(keyedBy: CodingKeys.self)
        try container.encode(mimeType, forKey: .mimeTypeSnake)
        try container.encode(data, forKey: .data)
    }
}

private struct GoogleGenerationConfig: Encodable {
    let responseModalities: [String]
    let imageConfig: GoogleImageConfig

    enum CodingKeys: String, CodingKey {
        case responseModalities = "responseModalities"
        case imageConfig = "imageConfig"
    }
}

private struct GoogleImageConfig: Encodable {
    let aspectRatio: String
    let imageSize: String

    enum CodingKeys: String, CodingKey {
        case aspectRatio = "aspectRatio"
        case imageSize = "imageSize"
    }
}

private struct GoogleImageResponse: Decodable {
    struct Candidate: Decodable {
        struct Content: Decodable {
            let parts: [Part]
        }

        let content: Content
    }

    struct Part: Decodable {
        let text: String?
        let inlineData: GoogleInlineData?

        private enum CodingKeys: String, CodingKey {
            case text
            case inlineData
            case inlineDataSnake = "inline_data"
        }

        init(from decoder: Decoder) throws {
            let container = try decoder.container(keyedBy: CodingKeys.self)
            self.text = try container.decodeIfPresent(String.self, forKey: .text)
            self.inlineData = try container.decodeIfPresent(GoogleInlineData.self, forKey: .inlineData)
                ?? container.decodeIfPresent(GoogleInlineData.self, forKey: .inlineDataSnake)
        }
    }

    let candidates: [Candidate]
}
