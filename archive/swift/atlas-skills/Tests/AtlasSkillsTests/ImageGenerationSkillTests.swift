import XCTest
@testable import AtlasSkills
import AtlasGuard
import AtlasLogging
import AtlasShared
import AtlasTools

final class ImageGenerationSkillTests: XCTestCase {
    func testImageActionsDefaultToAutoApprove() throws {
        let skill = ImageGenerationSkill()

        let generate = try XCTUnwrap(skill.actions.first(where: { $0.id == "image.generate" }))
        XCTAssertEqual(generate.permissionLevel, .read)
        XCTAssertEqual(generate.approvalPolicy, .autoApprove)

        let edit = try XCTUnwrap(skill.actions.first(where: { $0.id == "image.edit" }))
        XCTAssertEqual(edit.permissionLevel, .read)
        XCTAssertEqual(edit.approvalPolicy, .autoApprove)
    }

    func testImageGenerationSkillReturnsStructuredGeneratedArtifacts() async throws {
        let skill = ImageGenerationSkill(
            providerManagerFactory: { _ in MockActiveImageProviderManager(
                configuredProviderType: .openAI,
                validation: ImageProviderValidation(
                    providerType: .openAI,
                    status: .passed,
                    summary: "Ready."
                ),
                provider: MockImageProvider(providerID: .openAI, displayName: "OpenAI")
            ) }
        )

        let result = try await skill.execute(
            actionID: "image.generate",
            input: AtlasToolInput(argumentsJSON: #"{"prompt":"Create a blue atlas logo","size":"1024x1024","quality":"high"}"#),
            context: SkillExecutionContext(
                conversationID: nil,
                logger: AtlasLogger(category: "test"),
                config: AtlasConfig(activeImageProvider: .openAI),
                permissionManager: PermissionManager(grantedPermissions: [.read, .draft]),
                runtimeStatusProvider: { nil },
                enabledSkillsProvider: { [] }
            )
        )

        let output = try AtlasJSON.decoder.decode(ImageGenerationOutput.self, from: Data(result.output.utf8))
        XCTAssertEqual(output.providerUsed, "OpenAI")
        XCTAssertEqual(output.imageCount, 1)
        XCTAssertEqual(output.images.first?.fileName, "mock-output.png")
    }

    func testImageEditRejectsMissingInputImage() async throws {
        let skill = ImageGenerationSkill(
            providerManagerFactory: { _ in MockActiveImageProviderManager(
                configuredProviderType: .googleNanoBanana,
                validation: ImageProviderValidation(
                    providerType: .googleNanoBanana,
                    status: .passed,
                    summary: "Ready."
                ),
                provider: MockImageProvider(providerID: .googleNanoBanana, displayName: "Google Nano Banana")
            ) }
        )

        do {
            _ = try await skill.execute(
                actionID: "image.edit",
                input: AtlasToolInput(argumentsJSON: #"{"prompt":"Clean up the icon","inputImageReference":"/tmp/does-not-exist.png"}"#),
                context: SkillExecutionContext(
                    conversationID: nil,
                    logger: AtlasLogger(category: "test"),
                    config: AtlasConfig(activeImageProvider: .googleNanoBanana),
                    permissionManager: PermissionManager(grantedPermissions: [.read, .draft]),
                    runtimeStatusProvider: { nil },
                    enabledSkillsProvider: { [] }
                )
            )
            XCTFail("Expected missing file to fail.")
        } catch let error as ImageGenerationError {
            guard case .inputImageMissing = error else {
                XCTFail("Unexpected image generation error: \(error.localizedDescription)")
                return
            }
        }
    }

    func testGoogleProviderParsesCamelCaseImageResponse() async throws {
        let temporaryRoot = FileManager.default.temporaryDirectory
            .appendingPathComponent("GoogleNanoBananaProviderTests-\(UUID().uuidString)", isDirectory: true)
        let artifactStore = try ImageArtifactStore(rootDirectory: temporaryRoot)
        let session = URLSession(configuration: {
            let configuration = URLSessionConfiguration.ephemeral
            configuration.protocolClasses = [MockURLProtocol.self]
            return configuration
        }())

        MockURLProtocol.requestHandler = { request in
            let body = try XCTUnwrap(Self.requestBody(from: request))
            let bodyObject = try XCTUnwrap(try JSONSerialization.jsonObject(with: body) as? [String: Any])
            let generationConfig = try XCTUnwrap(bodyObject["generationConfig"] as? [String: Any])
            let responseModalities = try XCTUnwrap(generationConfig["responseModalities"] as? [String])
            XCTAssertEqual(responseModalities, ["Image"])

            let responseObject: [String: Any] = [
                "candidates": [
                    [
                        "content": [
                            "parts": [
                                [
                                    "inlineData": [
                                        "mimeType": "image/png",
                                        "data": Data("fake-image".utf8).base64EncodedString()
                                    ]
                                ]
                            ]
                        ]
                    ]
                ]
            ]
            let data = try JSONSerialization.data(withJSONObject: responseObject)
            let response = HTTPURLResponse(url: request.url!, statusCode: 200, httpVersion: nil, headerFields: nil)!
            return (response, data)
        }

        let provider = GoogleNanoBananaProvider(
            apiKey: "test-key",
            artifactStore: artifactStore,
            session: session
        )

        let output = try await provider.generateImage(
            request: ImageProviderGenerateRequest(
                prompt: "Create an icon",
                size: "1024x1024",
                quality: nil,
                styleHint: nil
            )
        )

        XCTAssertEqual(output.providerUsed, "Google Nano Banana")
        XCTAssertEqual(output.imageCount, 1)
        XCTAssertEqual(output.images.first?.mimeType, "image/png")
        XCTAssertTrue(FileManager.default.fileExists(atPath: try XCTUnwrap(output.images.first?.filePath)))
    }

    func testOpenAIProviderUsesCurrentRequestShapeAndParsesImageResponse() async throws {
        let temporaryRoot = FileManager.default.temporaryDirectory
            .appendingPathComponent("OpenAIImageProviderTests-\(UUID().uuidString)", isDirectory: true)
        let artifactStore = try ImageArtifactStore(rootDirectory: temporaryRoot)
        let session = URLSession(configuration: {
            let configuration = URLSessionConfiguration.ephemeral
            configuration.protocolClasses = [MockURLProtocol.self]
            return configuration
        }())

        MockURLProtocol.requestHandler = { request in
            XCTAssertEqual(request.url?.path, "/v1/images/generations")
            let body = try XCTUnwrap(Self.requestBody(from: request))
            let bodyObject = try XCTUnwrap(try JSONSerialization.jsonObject(with: body) as? [String: Any])
            XCTAssertEqual(bodyObject["model"] as? String, "gpt-image-1")
            XCTAssertEqual(bodyObject["quality"] as? String, "medium")
            XCTAssertNil(bodyObject["response_format"])

            let responseObject: [String: Any] = [
                "created": 1_763_611_200,
                "data": [
                    [
                        "b64_json": Data("fake-openai-image".utf8).base64EncodedString()
                    ]
                ]
            ]
            let data = try JSONSerialization.data(withJSONObject: responseObject)
            let response = HTTPURLResponse(url: request.url!, statusCode: 200, httpVersion: nil, headerFields: nil)!
            return (response, data)
        }

        let provider = OpenAIImageProvider(
            apiKey: "test-key",
            artifactStore: artifactStore,
            session: session
        )

        let output = try await provider.generateImage(
            request: ImageProviderGenerateRequest(
                prompt: "Create an icon",
                size: "1024x1024",
                quality: "standard",
                styleHint: nil
            )
        )

        XCTAssertEqual(output.providerUsed, "OpenAI")
        XCTAssertEqual(output.imageCount, 1)
        XCTAssertEqual(output.images.first?.mimeType, "image/png")
        XCTAssertTrue(FileManager.default.fileExists(atPath: try XCTUnwrap(output.images.first?.filePath)))
    }

    private static func requestBody(from request: URLRequest) -> Data? {
        if let body = request.httpBody {
            return body
        }

        guard let stream = request.httpBodyStream else {
            return nil
        }

        stream.open()
        defer { stream.close() }

        let bufferSize = 4_096
        let buffer = UnsafeMutablePointer<UInt8>.allocate(capacity: bufferSize)
        defer { buffer.deallocate() }

        var data = Data()
        while stream.hasBytesAvailable {
            let read = stream.read(buffer, maxLength: bufferSize)
            guard read > 0 else {
                break
            }
            data.append(buffer, count: read)
        }

        return data.isEmpty ? nil : data
    }
}

private struct MockActiveImageProviderManager: ActiveImageProviderManaging {
    let configuredProviderType: ImageProviderType?
    let validation: ImageProviderValidation
    let provider: any ImageProvider

    func activeProviderType() -> ImageProviderType? {
        configuredProviderType
    }

    func provider(for providerType: ImageProviderType) throws -> any ImageProvider {
        provider
    }

    func activeProvider() throws -> any ImageProvider {
        provider
    }

    func validate(providerType: ImageProviderType) async -> ImageProviderValidation {
        validation
    }

    func validateActiveProvider() async -> ImageProviderValidation {
        validation
    }
}

private struct MockImageProvider: ImageProvider {
    let providerID: ImageProviderType
    let displayName: String
    let supportsEdit = true

    func validateConfiguration() async -> ImageProviderValidation {
        ImageProviderValidation(providerType: providerID, status: .passed, summary: "Ready.")
    }

    func generateImage(request: ImageProviderGenerateRequest) async throws -> ImageGenerationOutput {
        ImageGenerationOutput(
            providerUsed: displayName,
            promptUsed: request.prompt,
            imageCount: 1,
            images: [
                ImageArtifact(
                    id: UUID(),
                    filePath: "/tmp/mock-output.png",
                    fileName: "mock-output.png",
                    mimeType: "image/png",
                    byteCount: 128
                )
            ],
            metadataSummary: "Generated 1 image."
        )
    }

    func editImage(request: ImageProviderEditRequest) async throws -> ImageGenerationOutput {
        ImageGenerationOutput(
            providerUsed: displayName,
            promptUsed: request.prompt,
            imageCount: 1,
            images: [
                ImageArtifact(
                    id: UUID(),
                    filePath: "/tmp/mock-edit.png",
                    fileName: "mock-edit.png",
                    mimeType: "image/png",
                    byteCount: 128
                )
            ],
            metadataSummary: "Edited 1 image."
        )
    }
}

private final class MockURLProtocol: URLProtocol {
    static var requestHandler: ((URLRequest) throws -> (HTTPURLResponse, Data))?

    override class func canInit(with request: URLRequest) -> Bool {
        true
    }

    override class func canonicalRequest(for request: URLRequest) -> URLRequest {
        request
    }

    override func startLoading() {
        guard let handler = Self.requestHandler else {
            client?.urlProtocol(self, didFailWithError: URLError(.badServerResponse))
            return
        }

        do {
            let (response, data) = try handler(request)
            client?.urlProtocol(self, didReceive: response, cacheStoragePolicy: .notAllowed)
            client?.urlProtocol(self, didLoad: data)
            client?.urlProtocolDidFinishLoading(self)
        } catch {
            client?.urlProtocol(self, didFailWithError: error)
        }
    }

    override func stopLoading() {}
}
