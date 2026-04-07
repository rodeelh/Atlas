import Foundation
import AtlasCore
import AtlasShared

// If run with --install flag, install the launchd agent and exit.
if CommandLine.arguments.contains("--install") {
    do {
        try DaemonInstaller.install()
        print("Atlas daemon installed.")
        exit(0)
    } catch {
        fputs("Atlas daemon install failed: \(error.localizedDescription)\n", stderr)
        exit(EXIT_FAILURE)
    }
}
// If run with --uninstall flag, uninstall and exit.
if CommandLine.arguments.contains("--uninstall") {
    do {
        try DaemonInstaller.uninstall()
        print("Atlas daemon uninstalled.")
        exit(0)
    } catch {
        fputs("Atlas daemon uninstall failed: \(error.localizedDescription)\n", stderr)
        exit(EXIT_FAILURE)
    }
}
// Normal daemon startup.
// Load persisted runtime config (atlas-config.json) before creating the runtime so
// settings saved by the app (telegramEnabled, personaName, etc.) are picked up
// immediately rather than falling back to per-process UserDefaults defaults.

do {
    let config = await AtlasConfig.loadFromStore()
    let context = try AgentContext(config: config)
    let runtime = try AgentRuntime(context: context)
    try await runtime.start()
    while !Task.isCancelled {
        try? await Task.sleep(for: .seconds(3600))
    }
} catch {
    fputs("Failed to start Atlas runtime: \(error.localizedDescription)\n", stderr)
    exit(EXIT_FAILURE)
}
