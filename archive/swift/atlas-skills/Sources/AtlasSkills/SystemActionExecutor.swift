import AppKit
import Foundation
import AtlasTools

protocol SystemActionExecuting: Sendable {
    func providerSummary() async -> [String]
    func validateNotificationCapability() async -> NotificationCapabilityStatus
    // Existing actions
    func openApp(named appName: String) async throws -> SystemOpenAppOutput
    func openFile(at url: URL) async throws -> SystemOpenPathOutput
    func openFolder(at url: URL) async throws -> SystemOpenPathOutput
    func revealInFinder(_ url: URL) async throws -> SystemRevealInFinderOutput
    func copyToClipboard(_ text: String) async throws -> SystemCopyToClipboardOutput
    func sendNotification(title: String, body: String) async throws -> SystemSendNotificationOutput
    // v2 — Clipboard
    func readClipboard() async -> SystemReadClipboardOutput
    // v2 — URL
    func openURL(_ url: URL) async throws -> SystemOpenURLOutput
    // v2 — App state queries
    func runningApps() async -> SystemRunningAppsOutput
    func frontmostApp() async -> SystemFrontmostAppOutput
    func isAppRunning(named appName: String) async -> SystemIsAppRunningOutput
    // v2 — App + file
    func openFileWithApp(at url: URL, appName: String) async throws -> SystemOpenPathOutput
    // v2 — App lifecycle
    func activateApp(named appName: String) async throws -> SystemActivateAppOutput
    func quitApp(named appName: String, force: Bool) async throws -> SystemQuitAppOutput
    // v2 — Scheduled notification
    func scheduleNotification(title: String, body: String, delaySeconds: Int) async throws -> SystemSendNotificationOutput
}

struct SystemActionExecutor: SystemActionExecuting {
    private let appResolver: any SystemAppResolving
    private let clipboardService: any ClipboardServicing
    private let notificationService: any NotificationServicing

    init(
        appResolver: (any SystemAppResolving)? = nil,
        clipboardService: (any ClipboardServicing)? = nil,
        notificationService: (any NotificationServicing)? = nil
    ) {
        self.appResolver = appResolver ?? SystemAppResolver()
        self.clipboardService = clipboardService ?? ClipboardService()
        self.notificationService = notificationService ?? NotificationService()
    }

    func providerSummary() async -> [String] {
        let notificationStatus = await validateNotificationCapability()
        return [
            "Uses NSWorkspace for apps and Finder integration",
            "Uses NSPasteboard for clipboard read and write",
            notificationStatus.summary
        ]
    }

    func validateNotificationCapability() async -> NotificationCapabilityStatus {
        await notificationService.validateNotificationCapability()
    }

    // MARK: - Existing actions

    func openApp(named appName: String) async throws -> SystemOpenAppOutput {
        let resolved = try await appResolver.resolve(appName: appName)
        let opened = await MainActor.run { NSWorkspace.shared.open(resolved.appURL) }
        guard opened else {
            throw AtlasToolError.executionFailed("Atlas could not open '\(resolved.resolvedAppName)'.")
        }

        return SystemOpenAppOutput(
            requestedAppName: resolved.requestedAppName,
            resolvedAppName: resolved.resolvedAppName,
            bundleIdentifier: resolved.bundleIdentifier,
            launched: true,
            message: "Opened \(resolved.resolvedAppName)."
        )
    }

    func openFile(at url: URL) async throws -> SystemOpenPathOutput {
        let opened = await MainActor.run { NSWorkspace.shared.open(url) }
        guard opened else {
            throw AtlasToolError.executionFailed("Atlas could not open '\(url.path)' with its default application.")
        }

        return SystemOpenPathOutput(
            path: url.path,
            opened: true,
            message: "Opened \(url.lastPathComponent)."
        )
    }

    func openFolder(at url: URL) async throws -> SystemOpenPathOutput {
        let opened = await MainActor.run { NSWorkspace.shared.open(url) }
        guard opened else {
            throw AtlasToolError.executionFailed("Atlas could not open the folder '\(url.path)' in Finder.")
        }

        return SystemOpenPathOutput(
            path: url.path,
            opened: true,
            message: "Opened folder \(url.lastPathComponent) in Finder."
        )
    }

    func revealInFinder(_ url: URL) async throws -> SystemRevealInFinderOutput {
        await MainActor.run {
            NSWorkspace.shared.activateFileViewerSelecting([url])
        }

        return SystemRevealInFinderOutput(
            path: url.path,
            revealed: true,
            message: "Revealed \(url.lastPathComponent) in Finder."
        )
    }

    func copyToClipboard(_ text: String) async throws -> SystemCopyToClipboardOutput {
        try await clipboardService.copy(text: text)
    }

    func sendNotification(title: String, body: String) async throws -> SystemSendNotificationOutput {
        try await notificationService.send(title: title, body: body)
    }

    // MARK: - v2: Clipboard

    func readClipboard() async -> SystemReadClipboardOutput {
        await clipboardService.read()
    }

    // MARK: - v2: URL

    func openURL(_ url: URL) async throws -> SystemOpenURLOutput {
        let opened = await MainActor.run { NSWorkspace.shared.open(url) }
        guard opened else {
            throw AtlasToolError.executionFailed("Atlas could not open '\(url.absoluteString)'.")
        }
        return SystemOpenURLOutput(
            url: url.absoluteString,
            opened: true,
            message: "Opened \(url.absoluteString) in the default browser."
        )
    }

    // MARK: - v2: App state queries

    func runningApps() async -> SystemRunningAppsOutput {
        let apps = await appResolver.runningApps()
        let mapped = apps.compactMap { app -> SystemRunningApp? in
            guard let name = app.localizedName else { return nil }
            return SystemRunningApp(
                name: name,
                bundleIdentifier: app.bundleIdentifier,
                isActive: app.isActive
            )
        }.sorted { $0.name.localizedCaseInsensitiveCompare($1.name) == .orderedAscending }

        return SystemRunningAppsOutput(
            apps: mapped,
            count: mapped.count,
            message: "\(mapped.count) application\(mapped.count == 1 ? "" : "s") are currently running."
        )
    }

    func frontmostApp() async -> SystemFrontmostAppOutput {
        guard let app = await appResolver.frontmostApp() else {
            return SystemFrontmostAppOutput(
                name: nil,
                bundleIdentifier: nil,
                isAvailable: false,
                message: "No frontmost application could be determined."
            )
        }
        let name = app.localizedName ?? "Unknown"
        return SystemFrontmostAppOutput(
            name: name,
            bundleIdentifier: app.bundleIdentifier,
            isAvailable: true,
            message: "\(name) is the active application."
        )
    }

    func isAppRunning(named appName: String) async -> SystemIsAppRunningOutput {
        let instances = await appResolver.runningInstances(matching: appName)
        return SystemIsAppRunningOutput(
            appName: appName,
            isRunning: !instances.isEmpty,
            runningInstances: instances.count,
            message: instances.isEmpty
                ? "'\(appName)' is not currently running."
                : "'\(appName)' is running (\(instances.count) instance\(instances.count == 1 ? "" : "s"))."
        )
    }

    // MARK: - v2: Open file with specific app

    func openFileWithApp(at url: URL, appName: String) async throws -> SystemOpenPathOutput {
        let resolved = try await appResolver.resolve(appName: appName)
        let appURL = resolved.appURL
        let resolvedName = resolved.resolvedAppName
        let fileName = url.lastPathComponent

        try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<Void, Error>) in
            let config = NSWorkspace.OpenConfiguration()
            Task { @MainActor in
                NSWorkspace.shared.open([url], withApplicationAt: appURL, configuration: config) { _, error in
                    if let error {
                        continuation.resume(throwing: AtlasToolError.executionFailed("Atlas could not open '\(fileName)' with \(resolvedName): \(error.localizedDescription)"))
                    } else {
                        continuation.resume()
                    }
                }
            }
        }

        return SystemOpenPathOutput(
            path: url.path,
            opened: true,
            message: "Opened \(fileName) with \(resolvedName)."
        )
    }

    // MARK: - v2: App lifecycle

    func activateApp(named appName: String) async throws -> SystemActivateAppOutput {
        let instances = await appResolver.runningInstances(matching: appName)
        guard let app = instances.first else {
            throw AtlasToolError.invalidInput("'\(appName)' is not currently running. Open it first with system.open_app.")
        }

        let activated = await MainActor.run { app.activate(options: .activateIgnoringOtherApps) }
        let resolvedName = app.localizedName ?? appName
        return SystemActivateAppOutput(
            appName: resolvedName,
            activated: activated,
            message: activated ? "Brought \(resolvedName) to the front." : "Atlas could not activate \(resolvedName)."
        )
    }

    func quitApp(named appName: String, force: Bool) async throws -> SystemQuitAppOutput {
        let instances = await appResolver.runningInstances(matching: appName)
        guard let app = instances.first else {
            throw AtlasToolError.invalidInput("'\(appName)' is not currently running.")
        }

        let resolvedName = app.localizedName ?? appName
        let terminated = await MainActor.run {
            force ? app.forceTerminate() : app.terminate()
        }

        return SystemQuitAppOutput(
            appName: resolvedName,
            terminated: terminated,
            message: terminated
                ? "\(force ? "Force-quit" : "Quit") \(resolvedName)."
                : "Atlas could not quit \(resolvedName)."
        )
    }

    // MARK: - v2: Scheduled notification

    func scheduleNotification(title: String, body: String, delaySeconds: Int) async throws -> SystemSendNotificationOutput {
        try await notificationService.schedule(title: title, body: body, delaySeconds: delaySeconds)
    }
}
