import Foundation

/// Manages launchd agent installation, uninstallation, and lifecycle for AtlasRuntimeService.
public struct DaemonInstaller {
    public static let launchAgentLabel = "com.projectatlas.runtime"

    /// Path where the plist is installed: ~/Library/LaunchAgents/com.projectatlas.runtime.plist
    public static var plistDestination: URL {
        let launchAgents = FileManager.default
            .homeDirectoryForCurrentUser
            .appendingPathComponent("Library/LaunchAgents", isDirectory: true)
        return launchAgents.appendingPathComponent("\(launchAgentLabel).plist")
    }

    /// Logs directory for daemon stdout/stderr: ~/Library/Logs/ProjectAtlas/
    public static var logsDirectory: URL {
        FileManager.default
            .homeDirectoryForCurrentUser
            .appendingPathComponent("Library/Logs/ProjectAtlas", isDirectory: true)
    }

    /// Install the launchd agent using the current executable path.
    /// Idempotent — safe to call if already installed.
    public static func install() throws {
        // 1. Find the plist template next to the current executable.
        let executableURL = URL(fileURLWithPath: Bundle.main.executablePath
            ?? ProcessInfo.processInfo.arguments[0])
        let executableDir = executableURL.deletingLastPathComponent()
        let templateURL = executableDir.appendingPathComponent("com.projectatlas.runtime.plist")

        guard FileManager.default.fileExists(atPath: templateURL.path) else {
            throw DaemonInstallerError.plistTemplateNotFound(templateURL.path)
        }

        var plistContent = try String(contentsOf: templateURL, encoding: .utf8)

        // 2. Replace placeholders with actual paths.
        let executablePath = Bundle.main.executablePath ?? ProcessInfo.processInfo.arguments[0]
        plistContent = plistContent.replacingOccurrences(
            of: "EXECUTABLE_PATH_PLACEHOLDER",
            with: executablePath
        )
        plistContent = plistContent.replacingOccurrences(
            of: "LOGS_PATH_PLACEHOLDER",
            with: logsDirectory.path
        )
        plistContent = plistContent.replacingOccurrences(
            of: "HOME_PATH_PLACEHOLDER",
            with: NSHomeDirectory()
        )

        // 3. Create ~/Library/LaunchAgents/ if it does not exist.
        try FileManager.default.createDirectory(
            at: plistDestination.deletingLastPathComponent(),
            withIntermediateDirectories: true,
            attributes: nil
        )

        // 4. Create logsDirectory if it does not exist.
        try FileManager.default.createDirectory(
            at: logsDirectory,
            withIntermediateDirectories: true,
            attributes: nil
        )

        // 5. Write the resolved plist to plistDestination.
        try plistContent.write(to: plistDestination, atomically: true, encoding: .utf8)

        // 6. Run launchctl load -w <plistDestination>.
        try runLaunchctl(["load", "-w", plistDestination.path])
    }

    /// Uninstall the launchd agent.
    public static func uninstall() throws {
        guard isInstalled() else { return }
        try runLaunchctl(["unload", plistDestination.path])
        try FileManager.default.removeItem(at: plistDestination)
    }

    /// Returns true if the plist exists at the expected location.
    public static func isInstalled() -> Bool {
        FileManager.default.fileExists(atPath: plistDestination.path)
    }

    /// Returns true if the daemon process is currently running (checks via launchctl).
    public static func isRunning() -> Bool {
        let result = runLaunchctlOutput(["list", launchAgentLabel])
        return result.exitCode == 0
    }

    /// Start the daemon via launchctl (if installed but not running).
    public static func start() throws {
        try runLaunchctl(["start", launchAgentLabel])
    }

    /// Stop the daemon via launchctl.
    public static func stop() throws {
        try runLaunchctl(["stop", launchAgentLabel])
    }

    /// Restart the daemon via launchctl.
    public static func restart() throws {
        try stop()
        try start()
    }

    // MARK: - Private helpers

    @discardableResult
    private static func runLaunchctl(_ arguments: [String]) throws -> String {
        let result = runLaunchctlOutput(arguments)
        guard result.exitCode == 0 else {
            throw DaemonInstallerError.launchctlFailed(
                arguments: arguments,
                exitCode: result.exitCode,
                output: result.output
            )
        }
        return result.output
    }

    private static func runLaunchctlOutput(_ arguments: [String]) -> (exitCode: Int32, output: String) {
        let launchctlPath = "/bin/launchctl"
        guard FileManager.default.fileExists(atPath: launchctlPath) else {
            return (-1, "launchctl not found at \(launchctlPath)")
        }

        let process = Process()
        process.executableURL = URL(fileURLWithPath: launchctlPath)
        process.arguments = arguments

        let pipe = Pipe()
        process.standardOutput = pipe
        process.standardError = pipe

        do {
            try process.run()
            process.waitUntilExit()
        } catch {
            return (-1, "Failed to launch launchctl: \(error.localizedDescription)")
        }

        let data = pipe.fileHandleForReading.readDataToEndOfFile()
        let output = String(data: data, encoding: .utf8) ?? ""
        return (process.terminationStatus, output)
    }
}

// MARK: - DaemonInstallerError

public enum DaemonInstallerError: Error, LocalizedError {
    case plistTemplateNotFound(String)
    case launchctlFailed(arguments: [String], exitCode: Int32, output: String)

    public var errorDescription: String? {
        switch self {
        case .plistTemplateNotFound(let path):
            return "Daemon plist template not found at: \(path)"
        case .launchctlFailed(let arguments, let exitCode, let output):
            return "launchctl \(arguments.joined(separator: " ")) failed (exit \(exitCode)): \(output)"
        }
    }
}
