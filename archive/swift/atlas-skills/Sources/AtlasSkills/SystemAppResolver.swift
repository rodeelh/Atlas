import AppKit
import Foundation
import AtlasTools

protocol SystemAppResolving: Sendable {
    func resolve(appName: String) async throws -> ResolvedApplicationTarget
    func runningApps() async -> [NSRunningApplication]
    func frontmostApp() async -> NSRunningApplication?
    func runningInstances(matching appName: String) async -> [NSRunningApplication]
}

struct SystemAppResolver: SystemAppResolving {

    func resolve(appName: String) async throws -> ResolvedApplicationTarget {
        let trimmed = appName.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else {
            throw AtlasToolError.invalidInput("App name is required.")
        }

        if looksLikeBundleIdentifier(trimmed),
           let url = await MainActor.run(body: { NSWorkspace.shared.urlForApplication(withBundleIdentifier: trimmed) }) {
            return ResolvedApplicationTarget(
                requestedAppName: trimmed,
                resolvedAppName: appDisplayName(for: url),
                bundleIdentifier: trimmed,
                appURL: url
            )
        }

        let candidates = try findCandidates(matching: trimmed)
        if candidates.isEmpty {
            throw AtlasToolError.invalidInput("Atlas could not find an application named '\(trimmed)'.")
        }

        if candidates.count > 1 {
            let names = candidates.map(\.resolvedAppName).sorted().joined(separator: ", ")
            throw AtlasToolError.invalidInput("Atlas found multiple apps matching '\(trimmed)': \(names). Please be more specific.")
        }

        return candidates[0]
    }

    private func findCandidates(matching query: String) throws -> [ResolvedApplicationTarget] {
        let normalizedQuery = normalizeAppName(query)
        let appURLs = applicationSearchRoots()
            .flatMap { root in
                (try? FileManager.default.contentsOfDirectory(
                    at: root,
                    includingPropertiesForKeys: [.isApplicationKey, .isDirectoryKey],
                    options: [.skipsHiddenFiles]
                )) ?? []
            }
            .filter { $0.pathExtension.caseInsensitiveCompare("app") == .orderedSame }

        var exactMatches: [ResolvedApplicationTarget] = []
        var prefixMatches: [ResolvedApplicationTarget] = []

        for url in appURLs {
            let displayName = appDisplayName(for: url)
            let normalizedDisplayName = normalizeAppName(displayName)

            let candidate = ResolvedApplicationTarget(
                requestedAppName: query,
                resolvedAppName: displayName,
                bundleIdentifier: bundleIdentifier(for: url),
                appURL: url
            )

            if normalizedDisplayName == normalizedQuery {
                exactMatches.append(candidate)
            } else if normalizedDisplayName.contains(normalizedQuery) || normalizedQuery.contains(normalizedDisplayName) {
                prefixMatches.append(candidate)
            }
        }

        let resolved = exactMatches.isEmpty ? prefixMatches : exactMatches
        return deduplicated(resolved)
    }

    private func applicationSearchRoots() -> [URL] {
        let staticRoots: [URL] = [
            URL(fileURLWithPath: "/Applications", isDirectory: true),
            URL(fileURLWithPath: "/System/Applications", isDirectory: true),
            URL(fileURLWithPath: "/System/Applications/Utilities", isDirectory: true),
            FileManager.default.homeDirectoryForCurrentUser.appendingPathComponent("Applications", isDirectory: true)
        ]
        .filter { FileManager.default.fileExists(atPath: $0.path) }

        // Scan one level of subdirectories under each root so apps installed
        // in vendor sub-folders (e.g. Setapp inside /Applications/Setapp/) are found.
        let subdirectories = staticRoots.flatMap { root -> [URL] in
            let children = (try? FileManager.default.contentsOfDirectory(
                at: root,
                includingPropertiesForKeys: [.isDirectoryKey],
                options: [.skipsHiddenFiles]
            )) ?? []
            return children.filter {
                $0.pathExtension.caseInsensitiveCompare("app") != .orderedSame &&
                (try? $0.resourceValues(forKeys: [.isDirectoryKey]).isDirectory) == true
            }
        }

        return staticRoots + subdirectories
    }

    private func appDisplayName(for url: URL) -> String {
        if let bundle = Bundle(url: url) {
            let displayName = bundle.object(forInfoDictionaryKey: "CFBundleDisplayName") as? String
            let name = bundle.object(forInfoDictionaryKey: "CFBundleName") as? String
            if let displayName, !displayName.isEmpty {
                return displayName
            }
            if let name, !name.isEmpty {
                return name
            }
        }

        return url.deletingPathExtension().lastPathComponent
    }

    private func bundleIdentifier(for url: URL) -> String? {
        Bundle(url: url)?.bundleIdentifier
    }

    private func looksLikeBundleIdentifier(_ value: String) -> Bool {
        guard !value.contains(" ") else { return false }
        let components = value.split(separator: ".", omittingEmptySubsequences: true)
        return components.count >= 3
    }

    private func normalizeAppName(_ value: String) -> String {
        value
            .lowercased()
            .replacingOccurrences(of: ".app", with: "")
            .replacingOccurrences(of: "-", with: " ")
            .components(separatedBy: CharacterSet.alphanumerics.inverted)
            .filter { !$0.isEmpty }
            .joined(separator: " ")
    }

    func runningApps() async -> [NSRunningApplication] {
        await MainActor.run {
            NSWorkspace.shared.runningApplications.filter {
                $0.activationPolicy == .regular || $0.activationPolicy == .accessory
            }
        }
    }

    func frontmostApp() async -> NSRunningApplication? {
        await MainActor.run { NSWorkspace.shared.frontmostApplication }
    }

    func runningInstances(matching appName: String) async -> [NSRunningApplication] {
        let normalizedQuery = normalizeAppName(appName)
        let running = await runningApps()
        return running.filter { app in
            let name = app.localizedName ?? ""
            return normalizeAppName(name) == normalizedQuery
                || normalizeAppName(name).contains(normalizedQuery)
        }
    }

    private func deduplicated(_ candidates: [ResolvedApplicationTarget]) -> [ResolvedApplicationTarget] {
        var seen = Set<URL>()
        return candidates.filter { candidate in
            seen.insert(candidate.appURL).inserted
        }
    }
}
