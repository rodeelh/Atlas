import Foundation
import AtlasShared

struct OpenAIImageProvider: ImageProvider {
    let providerID: ImageProviderType = .openAI
    let displayName = "OpenAI"
    let supportsEdit = true

    private let apiKey: String
    private let artifactStore: ImageArtifactStore
    private let session: URLSession
    private let baseURL: URL

    init(
        apiKey: String,
        artifactStore: ImageArtifactStore,
        session: URLSession? = nil,
        baseURL: URL = URL(string: "https://api.openai.com/v1")!
    ) {
        self.apiKey = apiKey
        self.artifactStore = artifactStore
        self.baseURL = baseURL

        if let session {
            self.session = session
        } else {
            let configuration = URLSessionConfiguration.ephemeral
            configuration.timeoutIntervalForRequest = 60
            configuration.timeoutIntervalForResource = 60
            configuration.httpShouldSetCookies = false
            configuration.httpCookieStorage = nil
            configuration.requestCachePolicy = .reloadIgnoringLocalAndRemoteCacheData
            configuration.httpAdditionalHeaders = [
                "Authorization": "Bearer \(apiKey)"
            ]
            self.session = URLSession(configuration: configuration)
        }
    }

    func validateConfiguration() async -> ImageProviderValidation {
        var request = URLRequest(url: baseURL.appendingPathComponent("models"))
        request.httpMethod = "GET"
        request.setValue("Bearer \(apiKey)", forHTTPHeaderField: "Authorization")

        do {
            let (_, response) = try await session.data(for: request)
            guard let http = response as? HTTPURLResponse else {
                return ImageProviderValidation(
                    providerType: providerID,
                    status: .failed,
                    summary: "OpenAI image validation returned an invalid response.",
                    issues: ["The OpenAI validation response was not HTTP."]
                )
            }

            if (200..<300).contains(http.statusCode) {
                return ImageProviderValidation(
                    providerType: providerID,
                    status: .passed,
                    summary: "OpenAI is configured and ready for image generation."
                )
            }

            let status: SkillValidationStatus = (http.statusCode == 401 || http.statusCode == 403) ? .failed : .warning
            return ImageProviderValidation(
                providerType: providerID,
                status: status,
                summary: status == .failed ? "The stored OpenAI image key was rejected." : "OpenAI image validation could not be completed.",
                issues: ["OpenAI returned status code \(http.statusCode)."]
            )
        } catch {
            return ImageProviderValidation(
                providerType: providerID,
                status: .warning,
                summary: "OpenAI image validation could not be completed.",
                issues: [error.localizedDescription]
            )
        }
    }

    func generateImage(request: ImageProviderGenerateRequest) async throws -> ImageGenerationOutput {
        let payload = OpenAIImageGeneratePayload(
            model: "gpt-image-1",
            prompt: combinedPrompt(prompt: request.prompt, styleHint: request.styleHint),
            size: request.size,
            quality: normalizedQuality(request.quality)
        )

        var urlRequest = URLRequest(url: baseURL.appendingPathComponent("images/generations"))
        urlRequest.httpMethod = "POST"
        urlRequest.setValue("application/json", forHTTPHeaderField: "Content-Type")
        urlRequest.setValue("Bearer \(apiKey)", forHTTPHeaderField: "Authorization")
        urlRequest.httpBody = try AtlasJSON.encoder.encode(payload)

        let response: OpenAIImageResponse = try await decode(urlRequest)
        let generated = try response.data.map { item in
            guard let base64 = item.b64JSON, let data = Data(base64Encoded: base64) else {
                throw ImageGenerationError.invalidResponse("OpenAI did not return image data.")
            }
            return GeneratedImagePayload(data: data, mimeType: "image/png")
        }
        let artifacts = try artifactStore.saveImages(generated, provider: providerID)
        return ImageGenerationOutput(
            providerUsed: displayName,
            promptUsed: payload.prompt,
            imageCount: artifacts.count,
            images: artifacts,
            metadataSummary: "Generated \(artifacts.count) image\(artifacts.count == 1 ? "" : "s") with OpenAI."
        )
    }

    func editImage(request: ImageProviderEditRequest) async throws -> ImageGenerationOutput {
        let imageData: Data
        do {
            imageData = try Data(contentsOf: request.inputImageURL)
        } catch {
            throw ImageGenerationError.inputImageUnreadable(request.inputImageURL.path)
        }
        let boundary = "AtlasOpenAIImageEdit-\(UUID().uuidString)"
        var urlRequest = URLRequest(url: baseURL.appendingPathComponent("images/edits"))
        urlRequest.httpMethod = "POST"
        urlRequest.setValue("multipart/form-data; boundary=\(boundary)", forHTTPHeaderField: "Content-Type")
        urlRequest.setValue("Bearer \(apiKey)", forHTTPHeaderField: "Authorization")
        urlRequest.httpBody = MultipartFormEncoder(boundary: boundary)
            .addTextField(named: "model", value: "gpt-image-1")
            .addTextField(named: "prompt", value: request.prompt)
            .addTextField(named: "size", value: request.size)
            .addOptionalTextField(named: "quality", value: normalizedQuality(request.quality))
            .addFileField(
                named: "image",
                fileName: request.inputImageURL.lastPathComponent,
                mimeType: mimeType(for: request.inputImageURL),
                data: imageData
            )
            .build()

        let response: OpenAIImageResponse = try await decode(urlRequest)
        let generated = try response.data.map { item in
            guard let base64 = item.b64JSON, let data = Data(base64Encoded: base64) else {
                throw ImageGenerationError.invalidResponse("OpenAI did not return edited image data.")
            }
            return GeneratedImagePayload(data: data, mimeType: "image/png")
        }
        let artifacts = try artifactStore.saveImages(generated, provider: providerID)
        return ImageGenerationOutput(
            providerUsed: displayName,
            promptUsed: request.prompt,
            imageCount: artifacts.count,
            images: artifacts,
            metadataSummary: "Edited \(artifacts.count) image\(artifacts.count == 1 ? "" : "s") with OpenAI."
        )
    }

    private func decode<Response: Decodable>(_ request: URLRequest) async throws -> Response {
        let (data, response) = try await session.data(for: request)
        guard let http = response as? HTTPURLResponse else {
            throw ImageGenerationError.invalidResponse("OpenAI returned an invalid response.")
        }
        guard (200..<300).contains(http.statusCode) else {
            let message = String(decoding: data, as: UTF8.self)
            throw ImageGenerationError.providerFailure(sanitizedProviderMessage(from: message, statusCode: http.statusCode))
        }
        do {
            return try AtlasJSON.decoder.decode(Response.self, from: data)
        } catch {
            throw ImageGenerationError.invalidResponse("OpenAI returned malformed image data.")
        }
    }

    private func combinedPrompt(prompt: String, styleHint: String?) -> String {
        guard let styleHint, !styleHint.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else {
            return prompt
        }
        return "\(prompt)\n\nStyle hint: \(styleHint.trimmingCharacters(in: .whitespacesAndNewlines))"
    }

    private func normalizedQuality(_ quality: String?) -> String? {
        guard let quality = quality?.trimmingCharacters(in: .whitespacesAndNewlines).lowercased(),
              !quality.isEmpty else {
            return nil
        }

        switch quality {
        case "standard":
            return "medium"
        case "low", "medium", "high", "auto":
            return quality
        default:
            return nil
        }
    }

    private func sanitizedProviderMessage(from raw: String, statusCode: Int) -> String {
        if statusCode == 401 || statusCode == 403 {
            return "The stored OpenAI image key was rejected."
        }
        if statusCode == 429 {
            return "OpenAI image generation is temporarily unavailable because the quota or rate limit was reached."
        }
        return "OpenAI image generation failed with status code \(statusCode)."
    }

    private func mimeType(for url: URL) -> String {
        switch url.pathExtension.lowercased() {
        case "jpg", "jpeg":
            return "image/jpeg"
        case "webp":
            return "image/webp"
        default:
            return "image/png"
        }
    }
}

private struct OpenAIImageGeneratePayload: Encodable {
    let model: String
    let prompt: String
    let size: String
    let quality: String?

    enum CodingKeys: String, CodingKey {
        case model
        case prompt
        case size
        case quality
    }
}

private struct OpenAIImageResponse: Decodable {
    struct ImageData: Decodable {
        let b64JSON: String?

        enum CodingKeys: String, CodingKey {
            case b64JSON = "b64_json"
        }
    }

    let data: [ImageData]
}

private struct MultipartFormEncoder {
    let boundary: String
    private var body = Data()

    init(boundary: String) {
        self.boundary = boundary
    }

    func addTextField(named name: String, value: String) -> MultipartFormEncoder {
        var copy = self
        copy.body.append("--\(boundary)\r\n".data(using: .utf8)!)
        copy.body.append("Content-Disposition: form-data; name=\"\(name)\"\r\n\r\n".data(using: .utf8)!)
        copy.body.append("\(value)\r\n".data(using: .utf8)!)
        return copy
    }

    func addOptionalTextField(named name: String, value: String?) -> MultipartFormEncoder {
        guard let value, !value.isEmpty else { return self }
        return addTextField(named: name, value: value)
    }

    func addFileField(named name: String, fileName: String, mimeType: String, data: Data) -> MultipartFormEncoder {
        var copy = self
        copy.body.append("--\(boundary)\r\n".data(using: .utf8)!)
        copy.body.append("Content-Disposition: form-data; name=\"\(name)\"; filename=\"\(fileName)\"\r\n".data(using: .utf8)!)
        copy.body.append("Content-Type: \(mimeType)\r\n\r\n".data(using: .utf8)!)
        copy.body.append(data)
        copy.body.append("\r\n".data(using: .utf8)!)
        return copy
    }

    func build() -> Data {
        var data = body
        data.append("--\(boundary)--\r\n".data(using: .utf8)!)
        return data
    }
}
