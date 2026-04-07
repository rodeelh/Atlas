import Foundation

public struct SystemOpenAppInput: Codable, Hashable, Sendable {
    public let appName: String
}

public struct SystemOpenFileInput: Codable, Hashable, Sendable {
    public let path: String
}

public struct SystemOpenFolderInput: Codable, Hashable, Sendable {
    public let path: String
}

public struct SystemRevealInFinderInput: Codable, Hashable, Sendable {
    public let path: String
}

public struct SystemCopyToClipboardInput: Codable, Hashable, Sendable {
    public let text: String
}

public struct SystemSendNotificationInput: Codable, Hashable, Sendable {
    public let title: String
    public let body: String
}

public struct SystemOpenAppOutput: Codable, Hashable, Sendable {
    public let requestedAppName: String
    public let resolvedAppName: String
    public let bundleIdentifier: String?
    public let launched: Bool
    public let message: String
}

public struct SystemOpenPathOutput: Codable, Hashable, Sendable {
    public let path: String
    public let opened: Bool
    public let message: String
}

public struct SystemRevealInFinderOutput: Codable, Hashable, Sendable {
    public let path: String
    public let revealed: Bool
    public let message: String
}

public struct SystemCopyToClipboardOutput: Codable, Hashable, Sendable {
    public let characterCount: Int
    public let copied: Bool
    public let message: String
}

public struct SystemSendNotificationOutput: Codable, Hashable, Sendable {
    public let title: String
    public let deliveredOrScheduled: Bool
    public let message: String
}

struct ResolvedApplicationTarget: Hashable, Sendable {
    let requestedAppName: String
    let resolvedAppName: String
    let bundleIdentifier: String?
    let appURL: URL
}

struct NotificationCapabilityStatus: Hashable, Sendable {
    let summary: String
    let isAvailable: Bool
    let issues: [String]
}

// MARK: - Read Clipboard

public struct SystemReadClipboardOutput: Codable, Hashable, Sendable {
    public let text: String?
    public let isEmpty: Bool
    public let message: String
}

// MARK: - Open URL

public struct SystemOpenURLInput: Codable, Hashable, Sendable {
    public let url: String
}

public struct SystemOpenURLOutput: Codable, Hashable, Sendable {
    public let url: String
    public let opened: Bool
    public let message: String
}

// MARK: - Running Apps

public struct SystemRunningAppsOutput: Codable, Hashable, Sendable {
    public let apps: [SystemRunningApp]
    public let count: Int
    public let message: String
}

public struct SystemRunningApp: Codable, Hashable, Sendable {
    public let name: String
    public let bundleIdentifier: String?
    public let isActive: Bool
}

// MARK: - Frontmost App

public struct SystemFrontmostAppOutput: Codable, Hashable, Sendable {
    public let name: String?
    public let bundleIdentifier: String?
    public let isAvailable: Bool
    public let message: String
}

// MARK: - Is App Running

public struct SystemIsAppRunningInput: Codable, Hashable, Sendable {
    public let appName: String
}

public struct SystemIsAppRunningOutput: Codable, Hashable, Sendable {
    public let appName: String
    public let isRunning: Bool
    public let runningInstances: Int
    public let message: String
}

// MARK: - Open File With App

public struct SystemOpenFileWithAppInput: Codable, Hashable, Sendable {
    public let path: String
    public let appName: String
}

// MARK: - Activate App

public struct SystemActivateAppInput: Codable, Hashable, Sendable {
    public let appName: String
}

public struct SystemActivateAppOutput: Codable, Hashable, Sendable {
    public let appName: String
    public let activated: Bool
    public let message: String
}

// MARK: - Quit App

public struct SystemQuitAppInput: Codable, Hashable, Sendable {
    public let appName: String
    public let force: Bool?
}

public struct SystemQuitAppOutput: Codable, Hashable, Sendable {
    public let appName: String
    public let terminated: Bool
    public let message: String
}

// MARK: - Schedule Notification

public struct SystemScheduleNotificationInput: Codable, Hashable, Sendable {
    public let title: String
    public let body: String
    public let delaySeconds: Int
}
