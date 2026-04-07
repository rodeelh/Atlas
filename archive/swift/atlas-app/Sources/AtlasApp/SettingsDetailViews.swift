import SwiftUI

struct LocalAccessSettingsPane: View {
    @ObservedObject var appState: AtlasAppState
    @StateObject private var permVM = OnboardingViewModel()

    private let appVersion: String = {
        Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String ?? "0.1.0"
    }()

    var body: some View {
        SettingsPaneLayout(
            icon: "folder.badge.gearshape",
            tint: .orange,
            title: "Local Access",
            subtitle: "Native-only folder permissions that still need a long-term home"
        ) {
            SettingsCard {
                VStack(alignment: .leading, spacing: 12) {
                    SettingsCardSectionHeader(title: "Needs Attention")
                    Text("This panel is intentionally temporary. Web onboarding now owns setup, and the menu bar app only keeps local folder permission controls until we decide where that capability should live next.")
                        .font(.subheadline)
                        .foregroundStyle(.secondary)
                        .fixedSize(horizontal: false, vertical: true)
                }
            }

            SettingsCard {
                VStack(alignment: .leading, spacing: 0) {
                    SettingsCardSectionHeader(title: "Folder Access")
                    folderRow(name: "Desktop", icon: "desktopcomputer", granted: isFolderGranted("Desktop"))
                    Divider().padding(.vertical, 6)
                    folderRow(name: "Documents", icon: "doc.fill", granted: isFolderGranted("Documents"))
                    Divider().padding(.vertical, 6)
                    folderRow(name: "Downloads", icon: "arrow.down.circle.fill", granted: isFolderGranted("Downloads"))
                }
            }

            SettingsCard {
                VStack(alignment: .leading, spacing: 0) {
                    SettingsCardSectionHeader(title: "Quick Links")
                    HStack {
                        VStack(alignment: .leading, spacing: 2) {
                            Text("Open Web UI")
                                .font(.body.weight(.medium))
                            Text("Use the web app for onboarding, credentials, communications, and runtime configuration.")
                                .font(.caption)
                                .foregroundStyle(.secondary)
                        }
                        Spacer(minLength: 16)
                        Button("Open Web UI") {
                            appState.openAtlasWebUI()
                        }
                        .buttonStyle(.bordered)
                    }
                }
            }

            Text("Project Atlas \(appVersion) · Native settings reduced to file access only")
                .font(.caption)
                .foregroundStyle(.tertiary)
                .frame(maxWidth: .infinity, alignment: .center)
                .padding(.top, 8)
        }
    }

    private func isFolderGranted(_ name: String) -> Bool {
        let home = FileManager.default.homeDirectoryForCurrentUser
        let path = home.appendingPathComponent(name).standardizedFileURL.path
        return appState.approvedFileAccessRoots.contains {
            $0.path == path || $0.displayName.caseInsensitiveCompare(name) == .orderedSame
        }
    }

    private func folderRow(name: String, icon: String, granted: Bool) -> some View {
        HStack(spacing: 12) {
            Image(systemName: icon)
                .font(.system(size: 13))
                .foregroundStyle(granted ? Color.green : Color.secondary)
                .frame(width: 20)
            Text(name)
                .font(.subheadline)
            Spacer()
            if granted {
                Label("Granted", systemImage: "checkmark.circle.fill")
                    .font(.subheadline)
                    .foregroundStyle(.green)
            } else {
                Button("Grant Access") {
                    Task { await permVM.grantFolder(name, using: appState) }
                }
                .buttonStyle(.bordered)
                .controlSize(.small)
            }
        }
        .padding(.vertical, 3)
    }
}
