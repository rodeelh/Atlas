import Foundation

/// Serves the bundled Preact web app from the AtlasRuntimeService resource bundle.
struct WebStaticHandler: Sendable {
    /// Returns (mimeType: String, data: Data) for the given path under /web/*.
    /// Path "/" or "/index.html" → serves index.html.
    /// Unknown paths → serves index.html (SPA client-side routing fallback).
    static func response(for path: String) -> (mimeType: String, data: Data)? {
        guard let webDir = locateWebDirectory() else {
            return nil
        }

        let normalizedPath = path.isEmpty || path == "/" ? "index.html" : String(path.hasPrefix("/") ? path.dropFirst() : path[path.startIndex...])
        let fileURL = webDir.appendingPathComponent(normalizedPath)

        // Try to serve the exact file
        if let data = try? Data(contentsOf: fileURL) {
            let mimeType = Self.mimeType(for: fileURL.pathExtension)
            return (mimeType, data)
        }

        // SPA fallback: serve index.html for unknown paths (client-side routing)
        let indexURL = webDir.appendingPathComponent("index.html")
        if let data = try? Data(contentsOf: indexURL) {
            return ("text/html; charset=utf-8", data)
        }

        return nil
    }

    // MARK: - Private

    /// Locates the `web` directory from the main bundle resources, or walks up from the executable.
    private static func locateWebDirectory() -> URL? {
        let executableURL = Bundle.main.executableURL ?? URL(fileURLWithPath: CommandLine.arguments[0])
        let executableDir = executableURL.deletingLastPathComponent()

        // 1. SPM command-line build (.build/debug or .build/release): resources next to executable.
        let spmCandidate = executableDir.appendingPathComponent("web")
        if FileManager.default.fileExists(atPath: spmCandidate.path) {
            return spmCandidate
        }

        // 2. SPM resource bundle (both Xcode Run Script copy and direct `swift build`):
        //    - `swift build` creates a flat bundle: bundle/web/
        //    - Xcode creates a macOS-style bundle: bundle/Contents/Resources/web/
        //    Load the bundle via Bundle(url:) so resourceURL handles both layouts.
        let spmBundleNames = [
            "AtlasCorePackage_AtlasRuntimeService.bundle",
            "AtlasRuntimeService_AtlasRuntimeService.bundle"
        ]
        for bundleName in spmBundleNames {
            let bundleURL = executableDir.appendingPathComponent(bundleName)
            // Try loading as a proper Bundle first (handles Contents/Resources layout)
            if let bundle = Bundle(url: bundleURL),
               let resourceURL = bundle.resourceURL {
                let candidate = resourceURL.appendingPathComponent("web")
                if FileManager.default.fileExists(atPath: candidate.path) {
                    return candidate
                }
            }
            // Flat layout fallback (swift build output)
            let flatCandidate = bundleURL.appendingPathComponent("web")
            if FileManager.default.fileExists(atPath: flatCandidate.path) {
                return flatCandidate
            }
        }

        // 3. Main bundle resourceURL (launchd daemon or app bundle fallback).
        if let bundleResourceURL = Bundle.main.resourceURL {
            let candidate = bundleResourceURL.appendingPathComponent("web")
            if FileManager.default.fileExists(atPath: candidate.path) {
                return candidate
            }
        }

        // 4. Walk up from executable to find Sources/AtlasRuntimeService/web (development source tree).
        var url = executableDir
        for _ in 0..<10 {
            let candidate = url.appendingPathComponent("Sources/AtlasRuntimeService/web")
            if FileManager.default.fileExists(atPath: candidate.path) {
                return candidate
            }
            url = url.deletingLastPathComponent()
        }

        return nil
    }

    private static func mimeType(for ext: String) -> String {
        switch ext.lowercased() {
        case "html", "htm": return "text/html; charset=utf-8"
        case "js", "mjs":   return "application/javascript; charset=utf-8"
        case "css":          return "text/css; charset=utf-8"
        case "json":         return "application/json; charset=utf-8"
        case "png":          return "image/png"
        case "ico":          return "image/x-icon"
        case "svg":          return "image/svg+xml"
        case "woff":         return "font/woff"
        case "woff2":        return "font/woff2"
        case "ttf":          return "font/ttf"
        case "webmanifest":  return "application/manifest+json"
        default:             return "application/octet-stream"
        }
    }
}
