import SwiftUI
import AppKit
import AtlasShared
import LocalAuthentication

struct AtlasMenuBarExtraContentView: View {
    @ObservedObject var appState: AtlasAppState
    @Environment(\.openSettings) private var openSettings
    @Environment(\.openWindow) private var openWindow

    @State private var remoteEnabled: Bool = false

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            header
            sectionDivider
            primaryActionSection
            sectionDivider
            statusSummarySection
            sectionDivider
            lanAccessSection
            if shouldShowActionsSection {
                sectionDivider
                actionsSection
            }
            sectionDivider
            footerSection
        }
        .padding(14)
        .frame(width: 320)
        .onAppear { loadLANStatus() }
        .onChange(of: appState.remoteAccessEnabled) { _, _ in loadLANStatus() }
    }

    private var header: some View {
        VStack(alignment: .leading, spacing: 4) {
            HStack(alignment: .firstTextBaseline, spacing: 12) {
                Text("Atlas")
                    .font(.title2.weight(.semibold))
                Spacer()
                Text(appState.menuBarStatusTitle)
                    .font(.headline.weight(.semibold))
                    .foregroundStyle(.primary)
            }

            if shouldShowStatusMessage {
                Text(appState.menuBarStatusMessage)
                    .font(.body)
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
                    .padding(.top, 2)
            }
        }
        .padding(.bottom, 10)
    }

    private var primaryActionSection: some View {
        Button(action: performPrimaryAction) {
            HStack {
                Image(systemName: appState.menuBarPrimaryAction.systemImage)
                Text(appState.menuBarPrimaryAction.title)
                    .font(.body.weight(.semibold))
                Spacer()
            }
            .contentShape(Rectangle())
        }
        .menuBarPrimaryButtonStyle()
    }

    private var statusSummarySection: some View {
        VStack(spacing: 0) {
            ForEach(appState.menuBarServiceStatuses) { service in
                HStack(spacing: 10) {
                    Circle()
                        .fill(statusColor(service.state))
                        .frame(width: 8, height: 8)

                    Text(service.title)
                        .font(.subheadline)

                    Spacer(minLength: 12)

                    Text(service.value)
                        .font(.subheadline)
                        .foregroundStyle(.secondary)
                        .multilineTextAlignment(.trailing)
                        .lineLimit(2)
                        .fixedSize(horizontal: false, vertical: true)
                }
                .padding(.vertical, 7)
            }
        }
    }

    @ViewBuilder
    private var actionsSection: some View {
        VStack(alignment: .leading, spacing: 2) {
            switch appState.nativeShellState {
            case .needsAttention:
                actionRow(title: "Repair Atlas", role: nil, action: repairAtlas)
            case .daemonStopped:
                actionRow(title: "Start Atlas", role: nil, action: startDaemon)
            case .ready:
                actionRow(title: "Restart Atlas", role: nil, action: restartDaemon)
                actionRow(title: "Stop Atlas", role: .destructive, action: stopDaemon)
            }
        }
    }

    private var lanAccessSection: some View {
        VStack(spacing: 0) {
            HStack(spacing: 10) {
                Image(systemName: "antenna.radiowaves.left.and.right")
                    .font(.subheadline)
                    .foregroundStyle(remoteEnabled ? Color.green : Color.secondary)
                    .frame(width: 16)
                Text("LAN Access")
                    .font(.subheadline)
                Spacer(minLength: 12)
                Toggle("", isOn: $remoteEnabled)
                    .labelsHidden()
                    .controlSize(.small)
                    .onChange(of: remoteEnabled) { _, newValue in
                        Task { await appState.setRemoteAccess(newValue) }
                    }
            }
            .padding(.vertical, 7)

        }
    }

    private func loadLANStatus() {
        remoteEnabled = appState.remoteAccessEnabled
    }

    // Note: the access token is intentionally not shown in the menu bar popover.
    // It is available in the Atlas app under Settings → Network.

    private var footerSection: some View {
        VStack(alignment: .leading, spacing: 10) {
            actionRow(title: "Open Settings", role: nil, action: openAtlasSettings)
            Divider()
            actionRow(title: "Quit Atlas", role: .destructive, action: quitAtlas)
        }
    }

    private func actionRow(title: String, role: ButtonRole?, action: @escaping () -> Void) -> some View {
        Button(role: role, action: action) {
            HStack {
                Text(title)
                    .font(.body)
                Spacer()
            }
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .padding(.vertical, 5)
    }

    private var sectionDivider: some View {
        Divider()
            .padding(.vertical, 8)
    }

    private var shouldShowStatusMessage: Bool {
        !(appState.nativeShellState == .ready && appState.pendingApprovalCount == 0)
    }

    private var shouldShowActionsSection: Bool {
        true
    }

    private func performPrimaryAction() {
        switch appState.menuBarPrimaryAction {
        case .repairAtlas:
            repairAtlas()
        case .startAtlas:
            startDaemon()
        case .openAtlas:
            appState.openAtlasWebUI()
        }
    }

    private func openAtlasSettings() {
        NSApp.activate(ignoringOtherApps: true)
        openSettings()
    }

    private func repairAtlas() {
        Task { @MainActor in
            await appState.repairDaemon()
        }
    }

    private func startDaemon() {
        Task { @MainActor in
            await appState.startDaemon()
        }
    }

    private func restartDaemon() {
        Task { @MainActor in
            await appState.restartDaemon()
        }
    }

    private func stopDaemon() {
        Task { @MainActor in
            await appState.stopDaemon()
        }
    }

    private func quitAtlas() {
        NSApp.terminate(nil)
    }

    private func statusColor(_ state: AtlasAppState.MenuBarServiceStatus.State) -> Color {
        switch state {
        case .ready:
            return .green
        case .warning:
            return .orange
        case .inactive:
            return .secondary
        }
    }
}

struct AtlasMenuBarExtraLabelView: View {
    @ObservedObject var appState: AtlasAppState

    var body: some View {
        Image(nsImage: makeMenuBarImage(status: appState.runtimeStatus))
    }
}

private extension View {
    @ViewBuilder
    func menuBarPrimaryButtonStyle() -> some View {
        if #available(macOS 26.0, *) {
            self.buttonStyle(.glassProminent)
        } else {
            self.buttonStyle(.borderedProminent)
        }
    }
}

private func makeMenuBarImage(status: AtlasRuntimeStatus?) -> NSImage {
    let statusDotColor: NSColor
    switch status?.state {
    case .ready:    statusDotColor = .systemGreen
    case .degraded: statusDotColor = .systemYellow
    case .starting: statusDotColor = .systemOrange
    case .stopped:  statusDotColor = .systemRed
    case nil:       statusDotColor = .systemRed
    }

    guard let logo = NSImage(named: "MenuBarIcon") else {
        let size = NSSize(width: 12, height: 12)
        let fallback = NSImage(size: size, flipped: false) { rect in
            statusDotColor.setFill()
            NSBezierPath(ovalIn: rect.insetBy(dx: 1, dy: 1)).fill()
            return true
        }
        fallback.isTemplate = false
        return fallback
    }

    let canvasSize = NSSize(width: 22, height: 18)
    let composite = NSImage(size: canvasSize, flipped: false) { rect in
        let logoSize: CGFloat = 14
        let logoRect = NSRect(
            x: 1,
            y: (rect.height - logoSize) / 2,
            width: logoSize,
            height: logoSize
        )
        logo.draw(in: logoRect, from: .zero, operation: .sourceOver, fraction: 1.0)

        let dotRadius: CGFloat = 2.5
        let dotRect = NSRect(
            x: rect.width - dotRadius * 2 - 0.5,
            y: 0.5,
            width: dotRadius * 2,
            height: dotRadius * 2
        )
        statusDotColor.setFill()
        NSBezierPath(ovalIn: dotRect).fill()

        return true
    }
    composite.isTemplate = false
    return composite
}
