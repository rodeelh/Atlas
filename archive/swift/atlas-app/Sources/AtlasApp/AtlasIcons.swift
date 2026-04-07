import SwiftUI

/// Centralized SF Symbol icons used throughout the Atlas application
struct AtlasIcons {
    // MARK: - App Identity

    /// Primary Atlas app icon symbol
    static let app = "wand.and.stars"

    /// Default/fallback icon
    static let `default` = "sparkles"

    // MARK: - Navigation & Tabs

    static let now = "clock.fill"
    static let approvals = "checkmark.shield.fill"
    static let activity = "clock.arrow.circlepath"
    static let settings = "gearshape.fill"

    // MARK: - Status & Indicators

    static let success = "checkmark.circle.fill"
    static let error = "exclamationmark.triangle.fill"
    static let warning = "exclamationmark.circle.fill"
    static let info = "info.circle.fill"

    // MARK: - Actions

    static let send = "arrow.up.circle.fill"
    static let copy = "doc.on.doc.fill"
    static let delete = "trash.fill"
    static let edit = "pencil"
    static let refresh = "arrow.clockwise"

    // MARK: - Content Types

    static let terminal = "terminal"
    static let document = "doc.text"
    static let folder = "folder.fill"
    static let file = "doc.fill"
    static let image = "photo.fill"

    // MARK: - Empty States

    static let emptyApprovals = "checkmark.shield.fill"
    static let emptyActivity = "clock.arrow.circlepath"
    static let emptyGeneric = "tray.fill"

    // MARK: - Permissions

    static let permissionRead = "doc.text.magnifyingglass"
    static let permissionDraft = "square.and.pencil"
    static let permissionExecute = "exclamationmark.shield"
}
