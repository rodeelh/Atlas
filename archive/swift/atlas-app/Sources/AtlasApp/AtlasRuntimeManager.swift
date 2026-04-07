import Foundation
import AtlasShared

public actor AtlasRuntimeManager {

    public enum DaemonState {
        case notInstalled
        case installedNotRunning
        case running
        case unreachable   // installed + running per launchctl, but HTTP not responding
    }

    private var port: Int

    public init(config: AtlasConfig = AtlasConfig()) {
        self.port = config.runtimePort
    }

    /// Check daemon installation and HTTP reachability.
    /// Timeout for HTTP check: 2 seconds.
    public func checkDaemonState() async -> DaemonState {
        let installed = isInstalled()
        let runningPerLaunchctl = isRunningViaLaunchctl()

        if !installed {
            return .notInstalled
        }

        if !runningPerLaunchctl {
            return .installedNotRunning
        }

        // Installed and running per launchctl — check HTTP reachability.
        let reachable = await detectReachablePort() != nil
        return reachable ? .running : .unreachable
    }

    /// Install (or reinstall) and start the daemon.
    /// Always rewrites the plist so the executable path stays current after Xcode rebuilds.
    /// Waits up to 10 seconds for the HTTP server to respond before returning.
    /// Throws `RuntimeManagerError.daemonDidNotStart` if HTTP never becomes reachable.
    public func installAndStart() async throws -> Int {
        // Always unload first (ignore errors — might not be loaded yet).
        _ = runLaunchctl(["unload", Self.plistDestination.path])
        // Brief wait to let the old process fully exit before rewriting the plist.
        try? await Task.sleep(for: .milliseconds(800))
        // Rewrite the plist unconditionally — picks up the current executable path.
        try runInstallCommand()
        return try await waitForHTTP()
    }

    /// Start the daemon via launchctl (if already installed).
    public func start() async throws -> Int {
        try startViaLaunchctl()
        return try await waitForHTTP()
    }

    /// Stop the daemon via launchctl.
    public func stop() async throws {
        try stopViaLaunchctl()
    }

    /// Restart the daemon: unload, wait for the process to exit, rewrite the plist
    /// (picks up a freshly built executable), reload, then wait for HTTP.
    public func restart() async throws -> Int {
        _ = runLaunchctl(["unload", Self.plistDestination.path])
        try? await Task.sleep(for: .milliseconds(1200))
        try runInstallCommand()
        return try await waitForHTTP()
    }

    /// Backwards-compatible lifecycle wrapper used by the app test suite.
    /// Starts the configured Atlas runtime if needed and becomes a no-op when it
    /// is already running.
    public func startEmbeddedRuntime() async throws {
        switch await checkDaemonState() {
        case .running:
            return
        case .installedNotRunning:
            _ = try await start()
        case .notInstalled, .unreachable:
            _ = try await installAndStart()
        }
    }

    /// Backwards-compatible lifecycle wrapper used by the app test suite.
    /// Stops the configured Atlas runtime when it is running and remains
    /// idempotent when the daemon is already stopped or not installed.
    public func stopEmbeddedRuntime() async throws {
        switch await checkDaemonState() {
        case .running, .unreachable:
            try await stop()
        case .installedNotRunning, .notInstalled:
            return
        }
    }

    // MARK: - Private helpers

    private static let launchAgentLabel = "com.projectatlas.runtime"

    private static var plistDestination: URL {
        FileManager.default
            .homeDirectoryForCurrentUser
            .appendingPathComponent("Library/LaunchAgents/\(launchAgentLabel).plist")
    }

    private func isInstalled() -> Bool {
        FileManager.default.fileExists(atPath: Self.plistDestination.path)
    }

    private func isRunningViaLaunchctl() -> Bool {
        let result = runLaunchctl(["list", Self.launchAgentLabel])
        return result.exitCode == 0
    }

    public func currentPort() -> Int {
        port
    }

    private func checkHTTPReachability(port: Int) async -> Bool {
        guard let url = URL(string: "http://127.0.0.1:\(port)/status") else { return false }

        var request = URLRequest(url: url)
        request.timeoutInterval = 2.0

        do {
            let (_, response) = try await URLSession.shared.data(for: request)
            if let httpResponse = response as? HTTPURLResponse {
                return httpResponse.statusCode < 500
            }
            return true
        } catch {
            return false
        }
    }

    private func runInstallCommand() throws {
        // Locate the Go runtime executable.
        guard let daemonURL = locateDaemonExecutable() else {
            throw RuntimeManagerError.executableNotFound
        }

        // Locate the bundled web assets.
        let webDir = locateWebDir(relativeTo: daemonURL) ?? daemonURL
            .deletingLastPathComponent()
            .appendingPathComponent("web")

        // Generate the launchd plist inline — no subprocess needed.
        let homeDir = FileManager.default.homeDirectoryForCurrentUser.path
        let logsDir = "\(homeDir)/Library/Logs/ProjectAtlas"
        try FileManager.default.createDirectory(
            atPath: logsDir,
            withIntermediateDirectories: true,
            attributes: nil
        )

        let plistContent = """
        <?xml version="1.0" encoding="UTF-8"?>
        <!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
        <plist version="1.0">
        <dict>
            <key>Label</key>
            <string>\(Self.launchAgentLabel)</string>
            <key>ProgramArguments</key>
            <array>
                <string>\(daemonURL.path)</string>
                <string>-web-dir</string>
                <string>\(webDir.path)</string>
            </array>
            <key>RunAtLoad</key>
            <true/>
            <key>KeepAlive</key>
            <false/>
            <key>StandardOutPath</key>
            <string>\(logsDir)/atlas-runtime.log</string>
            <key>StandardErrorPath</key>
            <string>\(logsDir)/atlas-runtime-error.log</string>
            <key>EnvironmentVariables</key>
            <dict>
                <key>HOME</key>
                <string>\(homeDir)</string>
            </dict>
        </dict>
        </plist>
        """

        // Write plist to ~/Library/LaunchAgents/.
        let destination = Self.plistDestination
        try FileManager.default.createDirectory(
            at: destination.deletingLastPathComponent(),
            withIntermediateDirectories: true,
            attributes: nil
        )
        guard let data = plistContent.data(using: .utf8) else {
            throw RuntimeManagerError.installFailed("Failed to encode plist content")
        }
        try data.write(to: destination, options: .atomic)

        // Register with launchctl.
        let result = runLaunchctl(["load", "-w", destination.path])
        guard result.exitCode == 0 else {
            throw RuntimeManagerError.installFailed(result.output)
        }
    }

    /// Locates the Go `Atlas` runtime binary.
    /// Checks two locations in priority order:
    /// 1. Production: bundled in Contents/Resources/Atlas
    /// 2. Xcode dev build: sibling at Build/Products/Debug/Atlas (built by a Run Script
    ///    build phase that runs `make` in atlas-runtime-go/).
    private func locateDaemonExecutable() -> URL? {
        // 1. Production: Go binary bundled as a Resource inside the app bundle.
        if let resourceURL = Bundle.main.url(forResource: "Atlas", withExtension: nil) {
            return resourceURL
        }

        // 2. Xcode dev build: Atlas binary is a sibling of the app package in DerivedData.
        if let executableURL = Bundle.main.executableURL {
            let buildProductsDir = executableURL
                .deletingLastPathComponent() // removes AtlasApp    → .../MacOS/
                .deletingLastPathComponent() // removes MacOS        → .../Contents/
                .deletingLastPathComponent() // removes Contents     → .../AtlasApp.app/
                .deletingLastPathComponent() // removes AtlasApp.app → .../Debug/
            let xcodeBuiltURL = buildProductsDir.appendingPathComponent("Atlas")
            if FileManager.default.fileExists(atPath: xcodeBuiltURL.path) {
                return xcodeBuiltURL
            }
        }

        return nil
    }

    /// Locates the bundled atlas-web/dist directory relative to the daemon binary.
    /// Checks:
    /// 1. `web/` sibling next to the binary (works for both bundle Resources/ and dev builds).
    /// 2. `Contents/Resources/web` inside the app bundle.
    private func locateWebDir(relativeTo binaryURL: URL) -> URL? {
        let siblingWeb = binaryURL.deletingLastPathComponent().appendingPathComponent("web")
        if FileManager.default.fileExists(atPath: siblingWeb.path) {
            return siblingWeb
        }
        if let bundleWeb = Bundle.main.url(forResource: "web", withExtension: nil) {
            return bundleWeb
        }
        return nil
    }

    private func waitForHTTP() async throws -> Int {
        var waited = 0.0
        while waited < 10.0 {
            if let reachablePort = await detectReachablePort() {
                port = reachablePort
                return reachablePort
            }
            try? await Task.sleep(for: .milliseconds(500))
            waited += 0.5
        }
        throw RuntimeManagerError.daemonDidNotStart
    }

    private func detectReachablePort() async -> Int? {
        for candidate in port...(port + 5) {
            if await checkHTTPReachability(port: candidate) {
                return candidate
            }
        }
        return nil
    }

    private func startViaLaunchctl() throws {
        let result = runLaunchctl(["start", Self.launchAgentLabel])
        guard result.exitCode == 0 else {
            throw RuntimeManagerError.launchctlFailed("start", result.output)
        }
    }

    private func stopViaLaunchctl() throws {
        let result = runLaunchctl(["stop", Self.launchAgentLabel])
        guard result.exitCode == 0 else {
            throw RuntimeManagerError.launchctlFailed("stop", result.output)
        }
    }

    private func runLaunchctl(_ arguments: [String]) -> (exitCode: Int32, output: String) {
        runProcess(executablePath: "/bin/launchctl", arguments: arguments)
    }

    private func runProcess(executablePath: String, arguments: [String]) -> (exitCode: Int32, output: String) {
        guard FileManager.default.fileExists(atPath: executablePath) else {
            return (-1, "Executable not found at \(executablePath)")
        }

        let process = Process()
        process.executableURL = URL(fileURLWithPath: executablePath)
        process.arguments = arguments

        let pipe = Pipe()
        process.standardOutput = pipe
        process.standardError = pipe

        do {
            try process.run()
            process.waitUntilExit()
        } catch {
            return (-1, "Process launch failed: \(error.localizedDescription)")
        }

        let data = pipe.fileHandleForReading.readDataToEndOfFile()
        let output = String(data: data, encoding: .utf8) ?? ""
        return (process.terminationStatus, output)
    }

}

// MARK: - RuntimeSupervisor conformance

extension AtlasRuntimeManager: RuntimeSupervisor {
    /// Adapts the macOS-specific `DaemonState` to the portable `RuntimeSupervisorState`.
    public func checkState() async -> RuntimeSupervisorState {
        switch await checkDaemonState() {
        case .notInstalled:       return .notInstalled
        case .installedNotRunning: return .installedNotRunning
        case .running:            return .running
        case .unreachable:        return .unreachable
        }
    }
}

// MARK: - RuntimeManagerError

public enum RuntimeManagerError: Error, LocalizedError {
    case executableNotFound
    case installFailed(String)
    case launchctlFailed(String, String)
    case daemonDidNotStart

    public var errorDescription: String? {
        switch self {
        case .executableNotFound:
            return "Atlas runtime executable not found. Run 'make' in atlas-runtime-go/ to build it, or open the Xcode project and build the AtlasApp scheme."
        case .installFailed(let output):
            return "Daemon install failed: \(output)"
        case .launchctlFailed(let command, let output):
            return "launchctl \(command) failed: \(output)"
        case .daemonDidNotStart:
            return "Daemon installed but did not respond on its expected localhost port range. Check ~/Library/Logs/ProjectAtlas/atlas-runtime-error.log for details."
        }
    }
}
