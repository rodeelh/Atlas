import Foundation

// MARK: - RuntimeSupervisor

/// The state of the runtime process as observed by the supervisor.
/// This type lives here so any shell implementation can share a common vocabulary
/// without importing platform-specific supervision code.
public enum RuntimeSupervisorState: Sendable, Equatable {
    case notInstalled
    case installedNotRunning
    case running
    case unreachable
}

/// Platform-agnostic contract for a supervisor that manages the Atlas runtime process.
/// Each platform (macOS launchd, Windows service, Linux systemd) provides its own
/// concrete implementation. The portable runtime never depends on a concrete supervisor.
public protocol RuntimeSupervisor: Sendable {
    /// Check the current state of the runtime process.
    func checkState() async -> RuntimeSupervisorState
    /// Install and start the runtime, returning the port it bound to.
    func installAndStart() async throws -> Int
    /// Start an already-installed runtime, returning the port it bound to.
    func start() async throws -> Int
    /// Stop the runtime.
    func stop() async throws
    /// Restart the runtime, returning the port it bound to.
    func restart() async throws -> Int
    /// The port the runtime most recently bound to.
    func currentPort() async -> Int
}

// MARK: - NotificationSink

/// Platform-agnostic contract for delivering a notification intent.
/// The runtime decides *that* a notification should be sent; the sink decides *how*
/// to deliver it on the current platform. This removes the need for process-name
/// heuristics inside runtime-owned notification logic.
///
/// Provided implementations:
/// - `RelayNotificationSink`  — relays via `NSDistributedNotificationCenter` (daemon context)
/// - `UNNotificationSink`     — delivers directly via `UNUserNotificationCenter` (app context)
public protocol NotificationSink: Sendable {
    /// Deliver a notification immediately.
    func deliver(title: String, body: String) async throws
    /// Deliver a notification after `delaySeconds` seconds.
    func deliverScheduled(title: String, body: String, delaySeconds: Int) async throws
}

// MARK: - FileAccessGrantAdapter

/// Shell-side contract for producing platform-specific file-access grant data from a URL.
///
/// Ownership split:
/// - The **runtime** (`FileAccessScopeStore`) owns the policy layer: storing approved roots,
///   enforcing access checks, and listing grants.
/// - The **shell** owns grant *acquisition*: showing native permission dialogs and producing
///   the opaque grant data the runtime needs to persist and re-resolve access.
///
/// On macOS, `MacOSBookmarkGrantAdapter` implements this using security-scoped bookmarks.
/// On future platforms, a different adapter can produce whatever token the OS requires.
public protocol FileAccessGrantAdapter: Sendable {
    /// Produce opaque grant data from a user-approved URL.
    /// The runtime stores this data and uses it to re-acquire access across restarts.
    func createGrant(for url: URL) throws -> Data
}

// MARK: - PathProvider

public protocol PathProvider: Sendable {
    func atlasSupportDirectory() -> URL
    func configFileURL() -> URL
}

public struct DefaultPathProvider: PathProvider, Sendable {
    public init() {}

    public func atlasSupportDirectory() -> URL {
        let fileManager = FileManager.default
        let baseURL = (try? fileManager.url(
            for: .applicationSupportDirectory,
            in: .userDomainMask,
            appropriateFor: nil,
            create: true
        )) ?? URL(fileURLWithPath: NSTemporaryDirectory(), isDirectory: true)

        return baseURL.appendingPathComponent("ProjectAtlas", isDirectory: true)
    }

    public func configFileURL() -> URL {
        atlasSupportDirectory().appendingPathComponent("config.json")
    }
}

public protocol ConfigStore: Sendable {
    func load() async -> RuntimeConfigSnapshot
    func save(_ snapshot: RuntimeConfigSnapshot) async throws
    func value<T: Codable>(for key: RuntimeConfigKey) async -> T?
    func setValue<T: Codable>(_ value: T, for key: RuntimeConfigKey) async throws
}

public protocol SecretStore: Sendable {
    func getSecret(name: String) throws -> String?
    func setSecret(name: String, value: String) throws
    func deleteSecret(name: String) throws
    func hasSecret(name: String) -> Bool
    func listSecretNames() throws -> [String]
}

public protocol SecretCacheInvalidating: Sendable {
    func invalidateSecretCache()
}

public enum UnsupportedSecretStoreError: LocalizedError {
    case unavailableBackend(description: String)

    public var errorDescription: String? {
        switch self {
        case .unavailableBackend(let description):
            return description
        }
    }
}
