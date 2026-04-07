import Foundation
import AtlasGuard
import AtlasLogging
import AtlasShared

public struct ToolExecutionContext: Sendable {
    public let logger: AtlasLogger
    public let permissionManager: PermissionManager
    public let fileAccessScope: URL
    public let conversationID: UUID
    public let toolCallID: UUID?

    public init(
        logger: AtlasLogger,
        permissionManager: PermissionManager,
        fileAccessScope: URL,
        conversationID: UUID,
        toolCallID: UUID? = nil
    ) {
        self.logger = logger
        self.permissionManager = permissionManager
        self.fileAccessScope = fileAccessScope.standardizedFileURL
        self.conversationID = conversationID
        self.toolCallID = toolCallID
    }

    public func validatePermission(_ level: PermissionLevel) async throws {
        try await permissionManager.validate(level: level)
    }

    public func resolveScopedURL(for rawPath: String?) async throws -> URL {
        let baseURL = fileAccessScope.standardizedFileURL
        let candidate: URL

        if let rawPath, !rawPath.isEmpty {
            if rawPath.hasPrefix("/") {
                candidate = URL(fileURLWithPath: rawPath, isDirectory: false).standardizedFileURL
            } else {
                candidate = baseURL.appendingPathComponent(rawPath, isDirectory: false).standardizedFileURL
            }
        } else {
            candidate = baseURL
        }

        try await permissionManager.validateScopedReadAccess(to: candidate, within: baseURL)
        return candidate
    }
}
