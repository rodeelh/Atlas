import SwiftUI
import AppKit

@main
struct AtlasApp: App {
    @StateObject private var appState: AtlasAppState

    init() {
        let state = AtlasAppState()
        _appState = StateObject(wrappedValue: state)
    }

    var body: some Scene {
        MenuBarExtra {
            AtlasMenuBarExtraContentView(appState: appState)
                .onAppear { appState.applyAppearance() }
        } label: {
            AtlasMenuBarExtraLabelView(appState: appState)
        }
        .menuBarExtraStyle(.window)

        // Hidden anchor window — hosts the onboarding sheet and wires openSettings.
        Window("Atlas", id: "atlas-main") {
            AtlasAnchorView(appState: appState)
        }
        .windowResizability(.contentSize)
        .defaultSize(width: 0, height: 0)
        .windowStyle(.hiddenTitleBar)

        // Settings window — opened via openSettings environment action wired in AtlasAnchorView.
        Settings {
            SettingsWindowView(appState: appState)
                .onAppear { appState.applyAppearance() }
        }
        .defaultSize(width: 820, height: 560)
        .windowResizability(.contentMinSize)
    }
}

/// Zero-size anchor view that owns the onboarding sheet and wires the
/// onboarding sheet.
private struct AtlasAnchorView: View {
    @ObservedObject var appState: AtlasAppState

    var body: some View {
        Color.clear
            .frame(width: 0, height: 0)
            .onAppear {
                appState.applyAppearance()
                Task { await appState.bootstrap() }
            }
    }
}
