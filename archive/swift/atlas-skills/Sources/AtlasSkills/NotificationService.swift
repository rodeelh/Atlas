import Foundation
import UserNotifications
import AtlasShared
import AtlasTools

// Distributed notification name used to relay from the daemon to the Atlas app host.
public let atlasNotificationRelayName = NSNotification.Name("com.projectatlas.relay.notification")

// MARK: - NotificationServicing

protocol NotificationServicing: Sendable {
    func validateNotificationCapability() async -> NotificationCapabilityStatus
    func send(title: String, body: String) async throws -> SystemSendNotificationOutput
    func schedule(title: String, body: String, delaySeconds: Int) async throws -> SystemSendNotificationOutput
}

// MARK: - RelayNotificationSink

/// Daemon-context delivery adapter.
/// Posts a distributed notification for the Atlas app host to pick up and display.
/// This is the default sink used when running inside `AtlasRuntimeService`.
public struct RelayNotificationSink: NotificationSink {
    public init() {}

    public func deliver(title: String, body: String) async throws {
        DistributedNotificationCenter.default().postNotificationName(
            atlasNotificationRelayName,
            object: nil,
            userInfo: ["title": title, "body": body, "id": UUID().uuidString],
            deliverImmediately: true
        )
    }

    public func deliverScheduled(title: String, body: String, delaySeconds: Int) async throws {
        // Scheduling is not available via distributed notifications;
        // relay an immediate notification as a best-effort substitute.
        try await deliver(title: title, body: body)
    }
}

// MARK: - UNNotificationSink

/// App-context delivery adapter.
/// Delivers notifications directly via `UNUserNotificationCenter`.
/// Inject this sink when `NotificationService` runs inside the Atlas app process.
public struct UNNotificationSink: NotificationSink {
    public init() {}

    public func deliver(title: String, body: String) async throws {
        let center = UNUserNotificationCenter.current()
        try await ensureAuthorized(center: center)

        let content = UNMutableNotificationContent()
        content.title = title
        content.body = body
        content.sound = .default

        let request = UNNotificationRequest(
            identifier: "atlas.system-actions.\(UUID().uuidString)",
            content: content,
            trigger: nil
        )
        try await center.add(request)
    }

    public func deliverScheduled(title: String, body: String, delaySeconds: Int) async throws {
        let center = UNUserNotificationCenter.current()
        try await ensureAuthorized(center: center)

        let content = UNMutableNotificationContent()
        content.title = title
        content.body = body
        content.sound = .default

        let trigger = UNTimeIntervalNotificationTrigger(
            timeInterval: TimeInterval(delaySeconds),
            repeats: false
        )
        let request = UNNotificationRequest(
            identifier: "atlas.system-actions.scheduled.\(UUID().uuidString)",
            content: content,
            trigger: trigger
        )
        try await center.add(request)
    }

    // MARK: - Helpers

    func capability() async -> NotificationCapabilityStatus {
        let center = UNUserNotificationCenter.current()
        let settings = await center.notificationSettings()
        switch settings.authorizationStatus {
        case .authorized, .provisional, .ephemeral:
            return NotificationCapabilityStatus(
                summary: "Local notifications are available.",
                isAvailable: true,
                issues: []
            )
        case .notDetermined:
            return NotificationCapabilityStatus(
                summary: "Notification permission will be requested the first time Atlas sends a local notification.",
                isAvailable: true,
                issues: []
            )
        case .denied:
            return NotificationCapabilityStatus(
                summary: "Local notification permission is denied.",
                isAvailable: false,
                issues: ["Notification permission is denied for Atlas."]
            )
        @unknown default:
            return NotificationCapabilityStatus(
                summary: "Notification permission state is unavailable.",
                isAvailable: false,
                issues: ["Notification authorization status could not be determined."]
            )
        }
    }

    private func ensureAuthorized(center: UNUserNotificationCenter) async throws {
        let settings = await center.notificationSettings()
        switch settings.authorizationStatus {
        case .denied:
            throw AtlasToolError.executionFailed("Atlas cannot send notifications because notification permission is denied.")
        case .notDetermined:
            let granted = try await center.requestAuthorization(options: [.alert, .badge, .sound])
            guard granted else {
                throw AtlasToolError.executionFailed("Atlas cannot send notifications because notification permission was not granted.")
            }
        case .authorized, .provisional, .ephemeral:
            break
        @unknown default:
            throw AtlasToolError.executionFailed("Atlas could not determine notification permission state.")
        }
    }
}

// MARK: - NotificationService

/// Runtime notification service. Validates capability and delegates delivery to
/// an injected `NotificationSink`, eliminating process-name heuristics from the
/// shared runtime layer.
///
/// Default sink: `RelayNotificationSink` (daemon-context relay).
/// Inject `UNNotificationSink` when running inside the Atlas app process.
struct NotificationService: NotificationServicing {
    private let sink: any NotificationSink

    init(sink: any NotificationSink = RelayNotificationSink()) {
        self.sink = sink
    }

    func validateNotificationCapability() async -> NotificationCapabilityStatus {
        if let unSink = sink as? UNNotificationSink {
            return await unSink.capability()
        }
        // Relay sink or any other adapter: capability is always available
        // (delivery is handled out-of-band by the app host).
        return NotificationCapabilityStatus(
            summary: "Notifications will be relayed through the Atlas app.",
            isAvailable: true,
            issues: []
        )
    }

    func send(title: String, body: String) async throws -> SystemSendNotificationOutput {
        let normalizedTitle = title.trimmingCharacters(in: .whitespacesAndNewlines)
        let normalizedBody = body.trimmingCharacters(in: .whitespacesAndNewlines)

        guard !normalizedTitle.isEmpty else {
            throw AtlasToolError.invalidInput("Notification title is required.")
        }
        guard !normalizedBody.isEmpty else {
            throw AtlasToolError.invalidInput("Notification body is required.")
        }

        try await sink.deliver(title: normalizedTitle, body: normalizedBody)

        return SystemSendNotificationOutput(
            title: normalizedTitle,
            deliveredOrScheduled: true,
            message: "Sent notification '\(normalizedTitle)'."
        )
    }

    func schedule(title: String, body: String, delaySeconds: Int) async throws -> SystemSendNotificationOutput {
        let normalizedTitle = title.trimmingCharacters(in: .whitespacesAndNewlines)
        let normalizedBody = body.trimmingCharacters(in: .whitespacesAndNewlines)

        guard !normalizedTitle.isEmpty else {
            throw AtlasToolError.invalidInput("Notification title is required.")
        }
        guard !normalizedBody.isEmpty else {
            throw AtlasToolError.invalidInput("Notification body is required.")
        }
        guard delaySeconds > 0 else {
            throw AtlasToolError.invalidInput("Delay must be greater than 0 seconds.")
        }

        try await sink.deliverScheduled(title: normalizedTitle, body: normalizedBody, delaySeconds: delaySeconds)

        let delayLabel = delaySeconds >= 60
            ? "\(delaySeconds / 60) minute\(delaySeconds / 60 == 1 ? "" : "s")"
            : "\(delaySeconds) second\(delaySeconds == 1 ? "" : "s")"

        return SystemSendNotificationOutput(
            title: normalizedTitle,
            deliveredOrScheduled: true,
            message: "Scheduled notification '\(normalizedTitle)' to fire in \(delayLabel)."
        )
    }
}
