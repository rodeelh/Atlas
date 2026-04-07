import Foundation
import AtlasShared
import AtlasLogging

public enum PermissionManagerError: LocalizedError {
    case denied(level: PermissionLevel)
    case outOfScope(path: String, root: String)

    public var errorDescription: String? {
        switch self {
        case .denied(let level):
            return "The '\(level.rawValue)' permission is not currently granted."
        case .outOfScope(let path, let root):
            return "'\(path)' is outside the Atlas tool sandbox at '\(root)'."
        }
    }
}

public actor PermissionManager {
    private let logger = AtlasLogger.security
    private var grantedPermissions: Set<PermissionLevel>
    private let autoApproveDraftTools: Bool

    public init(
        grantedPermissions: Set<PermissionLevel> = [.read, .draft, .execute],
        autoApproveDraftTools: Bool = false
    ) {
        self.grantedPermissions = grantedPermissions
        self.autoApproveDraftTools = autoApproveDraftTools
    }

    public func grant(_ permissions: [PermissionLevel]) {
        grantedPermissions.formUnion(permissions)
        logger.info("Granted permissions", metadata: ["permissions": permissions.map(\.rawValue).joined(separator: ",")])
    }

    public func revoke(_ permissions: [PermissionLevel]) {
        grantedPermissions.subtract(permissions)
        logger.warning("Revoked permissions", metadata: ["permissions": permissions.map(\.rawValue).joined(separator: ",")])
    }

    public func validate(level: PermissionLevel) throws {
        guard grantedPermissions.contains(level) else {
            logger.warning("Permission validation failed", metadata: ["level": level.rawValue])
            throw PermissionManagerError.denied(level: level)
        }
    }

    public func requiresApproval(for level: PermissionLevel) -> Bool {
        switch level {
        case .read:
            return false
        case .draft:
            return !autoApproveDraftTools
        case .execute:
            return true
        }
    }

    public func validateScopedReadAccess(to url: URL, within root: URL) throws {
        guard isPathAllowed(url, within: root) else {
            logger.warning("Scoped read access denied", metadata: [
                "path": url.path,
                "root": root.path
            ])
            throw PermissionManagerError.outOfScope(path: url.path, root: root.path)
        }
    }

    public func isPathAllowed(_ url: URL, within root: URL) -> Bool {
        let standardizedRoot = root.standardizedFileURL.resolvingSymlinksInPath()
        let standardizedURL = url.standardizedFileURL.resolvingSymlinksInPath()

        return standardizedURL.path == standardizedRoot.path ||
            standardizedURL.path.hasPrefix(standardizedRoot.path + "/")
    }

    public func snapshot() -> [PermissionLevel] {
        grantedPermissions.sorted()
    }
}
