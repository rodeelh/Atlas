import Foundation
import AtlasLogging
import AtlasShared

// MARK: - DashboardStoreError

public enum DashboardStoreError: LocalizedError, Sendable {
    case proposalNotFound(String)
    case dashboardNotFound(String)
    case invalidStatus(DashboardProposalStatus)

    public var errorDescription: String? {
        switch self {
        case .proposalNotFound(let id):
            return "Dashboard proposal '\(id)' not found."
        case .dashboardNotFound(let id):
            return "Installed dashboard '\(id)' not found."
        case .invalidStatus(let s):
            return "Cannot perform this action on a proposal in '\(s.rawValue)' status."
        }
    }
}

// MARK: - DashboardStore

/// Persists dashboard proposals and installed dashboards as JSON files in Application Support.
/// Mirrors the ActionPolicyStore / FileAccessScopeStore pattern of flat JSON file persistence.
public actor DashboardStore {
    private var proposals: [DashboardProposal]
    private var installed: [DashboardSpec]

    private let proposalsFileURL: URL
    private let installedFileURL: URL
    private let logger: AtlasLogger

    public init(
        directory: URL? = nil,
        logger: AtlasLogger = AtlasLogger(category: "dashboard.store")
    ) {
        let dir = directory ?? Self.defaultDirectory()
        self.proposalsFileURL = dir.appendingPathComponent("dashboard-proposals.json")
        self.installedFileURL = dir.appendingPathComponent("dashboard-installed.json")
        self.logger = logger
        self.proposals = Self.load([DashboardProposal].self, from: dir.appendingPathComponent("dashboard-proposals.json"))
        self.installed = Self.load([DashboardSpec].self, from: dir.appendingPathComponent("dashboard-installed.json"))
    }

    // MARK: - Proposals

    public func listProposals() -> [DashboardProposal] {
        proposals
    }

    public func addProposal(_ proposal: DashboardProposal) throws {
        proposals.append(proposal)
        persistProposals()
        logger.info("Dashboard proposal added", metadata: ["id": proposal.proposalID, "title": proposal.spec.title])
    }

    public func install(proposalID: String) throws {
        guard let idx = proposals.firstIndex(where: { $0.proposalID == proposalID }) else {
            throw DashboardStoreError.proposalNotFound(proposalID)
        }
        guard proposals[idx].status == .pending else {
            throw DashboardStoreError.invalidStatus(proposals[idx].status)
        }
        proposals[idx].status = .installed
        // Add the spec to the installed list (replace if already present by ID)
        let spec = proposals[idx].spec
        installed.removeAll { $0.id == spec.id }
        installed.append(spec)
        persistProposals()
        persistInstalled()
        logger.info("Dashboard installed", metadata: ["id": spec.id, "title": spec.title])
    }

    public func reject(proposalID: String) throws {
        guard let idx = proposals.firstIndex(where: { $0.proposalID == proposalID }) else {
            throw DashboardStoreError.proposalNotFound(proposalID)
        }
        guard proposals[idx].status == .pending else {
            throw DashboardStoreError.invalidStatus(proposals[idx].status)
        }
        proposals[idx].status = .rejected
        persistProposals()
        logger.info("Dashboard proposal rejected", metadata: ["id": proposalID])
    }

    // MARK: - Installed

    public func listInstalled() -> [DashboardSpec] {
        installed
    }

    public func remove(dashboardID: String) throws {
        guard installed.contains(where: { $0.id == dashboardID }) else {
            throw DashboardStoreError.dashboardNotFound(dashboardID)
        }
        installed.removeAll { $0.id == dashboardID }
        persistInstalled()
        logger.info("Dashboard removed", metadata: ["id": dashboardID])
    }

    /// Updates `lastAccessedAt` to now. Called each time a dashboard is opened.
    public func recordAccess(dashboardID: String) throws {
        guard let idx = installed.firstIndex(where: { $0.id == dashboardID }) else {
            throw DashboardStoreError.dashboardNotFound(dashboardID)
        }
        installed[idx].lastAccessedAt = Date()
        persistInstalled()
    }

    /// Toggles `isPinned` for the given dashboard.
    public func togglePin(dashboardID: String) throws {
        guard let idx = installed.firstIndex(where: { $0.id == dashboardID }) else {
            throw DashboardStoreError.dashboardNotFound(dashboardID)
        }
        installed[idx].isPinned.toggle()
        persistInstalled()
        logger.info("Dashboard pin toggled",
                    metadata: ["id": dashboardID, "isPinned": "\(installed[idx].isPinned)"])
    }

    // MARK: - Persistence

    private func persistProposals() {
        persist(proposals, to: proposalsFileURL)
    }

    private func persistInstalled() {
        persist(installed, to: installedFileURL)
    }

    private func persist<T: Encodable>(_ value: T, to url: URL) {
        do {
            let directory = url.deletingLastPathComponent()
            try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
            let data = try AtlasJSON.encoder.encode(value)
            try data.write(to: url, options: .atomic)
        } catch {
            logger.error("Failed to persist dashboard data", metadata: [
                "file": url.lastPathComponent,
                "error": error.localizedDescription
            ])
        }
    }

    private static func load<T: Decodable>(_ type: T.Type, from url: URL) -> T where T: ExpressibleByArrayLiteral {
        guard let data = try? Data(contentsOf: url) else { return [] }
        return (try? AtlasJSON.decoder.decode(T.self, from: data)) ?? []
    }

    private static func defaultDirectory() -> URL {
        let appSupport = (try? FileManager.default.url(
            for: .applicationSupportDirectory,
            in: .userDomainMask,
            appropriateFor: nil,
            create: true
        )) ?? FileManager.default.temporaryDirectory
        return appSupport.appendingPathComponent("ProjectAtlas", isDirectory: true)
    }
}
