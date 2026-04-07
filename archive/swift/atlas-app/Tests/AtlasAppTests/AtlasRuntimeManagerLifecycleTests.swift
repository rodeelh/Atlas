import Foundation
import XCTest
import AtlasShared
@testable import AtlasApp

final class AtlasRuntimeManagerLifecycleTests: XCTestCase {

    /// Integration tests that start the embedded daemon require launchd and a signed binary.
    /// Set ATLAS_INTEGRATION_TESTS=1 in the scheme environment to opt in.
    private func requiresDaemon() throws {
        try XCTSkipUnless(
            ProcessInfo.processInfo.environment["ATLAS_INTEGRATION_TESTS"] == "1",
            "Daemon integration test — set ATLAS_INTEGRATION_TESTS=1 in the scheme environment to run"
        )
    }

    func testEmbeddedRuntimeStartAndStopAreIdempotent() async throws {
        try requiresDaemon()
        let port = Int.random(in: 19000...19999)
        let sandboxURL = FileManager.default.temporaryDirectory
            .appendingPathComponent("AtlasRuntimeManagerTests-\(UUID().uuidString)", isDirectory: true)
        try FileManager.default.createDirectory(at: sandboxURL, withIntermediateDirectories: true)

        let config = AtlasConfig(
            runtimePort: port,
            openAIServiceName: "com.projectatlas.tests.openai.\(UUID().uuidString)",
            openAIAccountName: "missing",
            toolSandboxDirectory: sandboxURL.path
        )

        let runtimeManager = AtlasRuntimeManager(config: config)
        let client = AtlasAPIClient(config: config)

        try await runtimeManager.startEmbeddedRuntime()
        try await runtimeManager.startEmbeddedRuntime()

        let status = try await client.fetchStatus()
        XCTAssertTrue(status.isRunning)
        XCTAssertEqual(status.runtimePort, port)

        try await runtimeManager.stopEmbeddedRuntime()
        try await runtimeManager.stopEmbeddedRuntime()
    }
}
