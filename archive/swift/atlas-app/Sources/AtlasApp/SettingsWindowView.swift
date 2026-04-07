import SwiftUI
import AtlasShared

private struct SidebarPanelBackground: View {
    var body: some View {
        SettingsGlassSurface()
    }
}

// MARK: - Window styler (AppKit bridge)
// Configures the host NSWindow for full-bleed sidebar: transparent titlebar +
// fullSizeContentView so the sidebar material fills under the traffic lights.
// window.isOpaque = false + backgroundColor = .clear lets vibrancy render properly.
// Coordinator ensures configuration runs exactly once after the view is in a window.
private struct SettingsWindowStyler: NSViewRepresentable {
    class Coordinator { var configured = false }

    func makeCoordinator() -> Coordinator { Coordinator() }
    func makeNSView(context: Context) -> NSView { NSView() }

    func updateNSView(_ view: NSView, context: Context) {
        guard !context.coordinator.configured else { return }
        DispatchQueue.main.async {
            guard !context.coordinator.configured, let w = view.window else { return }
            context.coordinator.configured = true
            NSApp.activate(ignoringOtherApps: true)
            w.title                      = ""          // clear scene-set "Project Atlas Settings"
            w.titleVisibility            = .hidden
            w.titlebarAppearsTransparent = true        // toolbar area transparent → sidebar glass shows through
            w.toolbarStyle               = .unified    // sidebar toggle in correct strip next to traffic lights
            w.minSize                    = NSSize(width: 820, height: 560)
            w.level                      = .modalPanel
            w.orderFrontRegardless()
            w.makeKeyAndOrderFront(nil)
            DispatchQueue.main.async {
                w.level = .normal
            }
            // fullSizeContentView intentionally omitted: without it, SwiftUI content starts below
            // the toolbar (safe area is correct), so list rows never overlap the traffic lights.
            // The sidebar background ignoresSafeArea still bleeds into the transparent toolbar area.
        }
    }
}

struct SettingsWindowView: View {
    @ObservedObject var appState: AtlasAppState

    var body: some View {
        LocalAccessSettingsPane(appState: appState)
            .frame(minWidth: 620, maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
            .background(
                SidebarPanelBackground().ignoresSafeArea(.all, edges: .top)
            )
        .navigationTitle("")                        // Override scene-set window title
        .background(SettingsWindowStyler())
    }
}
