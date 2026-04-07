import XCTest
@testable import AtlasCore
import AtlasMemory
import AtlasSkills
import AtlasShared

// MARK: - ForgePersistenceTests
//
// Covers:
//  1. ForgeProposalStore CRUD — save, fetch, listActive, updateStatus
//  2. Restart simulation — status updates survive a new store instance
//  3. hydrateInstalledSkills — filters correctly by proposal status
//  4. hydrateInstalledSkills — restores .installed / .enabled skills to SkillRegistry
//  5. hydrateInstalledSkills — is idempotent (safe to call multiple times)
//  6. ForgeProposalService.createProposal — domain extraction from HTTP plan URLs
//  7. ForgeProposalService.createProposal — requiredSecrets extraction from secretHeader
//
// "Restart" is simulated by creating new MemoryStore + ForgeProposalStore + SkillRegistry
// instances pointing at the SAME SQLite file path but with a fresh UserDefaults suite
// (no persisted lifecycle state), then calling hydrateInstalledSkills() to re-register
// skills from the persisted SQLite blobs.

final class ForgePersistenceTests: XCTestCase {

    // MARK: - Private Helpers

    private func temporaryDatabasePath() -> String {
        FileManager.default.temporaryDirectory
            .appendingPathComponent("ForgePersistenceTests-\(UUID().uuidString).sqlite3")
            .path
    }

    /// Counts how many times a skill enable/disable triggers the resync callback.
    private actor ResyncCounter {
        private(set) var count = 0
        func increment() { count += 1 }
    }

    /// Boot a fresh ForgeProposalService session pointing at `dbPath`.
    ///
    /// Creates isolated MemoryStore, ForgeProposalStore, SkillRegistry (fresh UserDefaults
    /// suite), and CoreSkillsRuntime. Configures the service but does NOT call
    /// `hydrateInstalledSkills()` — callers do that explicitly when testing hydration.
    ///
    /// Pass the same `dbPath` to two successive calls to simulate a daemon restart.
    private func bootSession(
        dbPath: String,
        resyncCounter: ResyncCounter? = nil
    ) async throws -> (
        service: ForgeProposalService,
        registry: SkillRegistry,
        store: ForgeProposalStore
    ) {
        let memoryStore   = try MemoryStore(databasePath: dbPath)
        let proposalStore = ForgeProposalStore(memoryStore: memoryStore)
        // Fresh suite per boot so SkillRegistry has no persisted lifecycle state.
        let defaults      = UserDefaults(suiteName: "ForgePersistenceTests.\(UUID().uuidString)")!
        let registry      = SkillRegistry(defaults: defaults)
        let counter       = resyncCounter

        let coreSkills = CoreSkillsRuntime(
            registry: registry,
            secretsReader: { _ in nil },
            resyncCallback: {
                if let c = counter { await c.increment() }
            }
        )
        let service = ForgeProposalService(store: proposalStore)
        await service.configure(coreSkills: coreSkills, skillRegistry: registry)
        return (service, registry, proposalStore)
    }

    /// Minimal valid ForgeSkillSpec with two actions (GET + POST).
    private func makeQuoteSpec(id: String = "quote-service") -> ForgeSkillSpec {
        ForgeSkillSpec(
            id: id,
            name: "Quote Service",
            description: "Fetches and rates quotes from the Quotes API.",
            category: .utility,
            riskLevel: .low,
            tags: ["quotes", "api"],
            actions: [
                ForgeActionSpec(
                    id: "\(id).get",
                    name: "Get Quote",
                    description: "Fetch a random quote by category.",
                    permissionLevel: .read,
                    inputSchema: AtlasToolInputSchema(
                        properties: [
                            "category": AtlasToolInputProperty(
                                type: "string",
                                description: "Quote category"
                            )
                        ],
                        additionalProperties: false
                    )
                ),
                ForgeActionSpec(
                    id: "\(id).rate",
                    name: "Rate Quote",
                    description: "Submit a star rating for a quote.",
                    permissionLevel: .draft,
                    inputSchema: AtlasToolInputSchema(
                        properties: [
                            "quote_id": AtlasToolInputProperty(
                                type: "string",
                                description: "Quote identifier"
                            ),
                            "rating": AtlasToolInputProperty(
                                type: "integer",
                                description: "Star rating 1–5"
                            )
                        ],
                        additionalProperties: false
                    )
                )
            ]
        )
    }

    /// Standard action plans for the quote-service spec.
    private func makeQuotePlans(id: String = "quote-service") -> [ForgeActionPlan] {
        [
            ForgeActionPlan(
                actionID: "\(id).get",
                type: .http,
                httpRequest: HTTPRequestPlan(
                    method: "GET",
                    url: "https://api.quotes.mock.atlas.test/v1/quotes",
                    query: ["format": "json"]
                )
            ),
            ForgeActionPlan(
                actionID: "\(id).rate",
                type: .http,
                httpRequest: HTTPRequestPlan(
                    method: "POST",
                    url: "https://api.quotes.mock.atlas.test/v1/quotes"
                )
            )
        ]
    }

    /// Encode spec + plans to the JSON strings stored in ForgeProposalRecord.
    private func encodeProposalJSON(
        spec: ForgeSkillSpec,
        plans: [ForgeActionPlan]
    ) throws -> (specJSON: String, plansJSON: String) {
        let encoder = AtlasJSON.encoder
        let specJSON  = String(data: try encoder.encode(spec), encoding: .utf8)!
        let plansJSON = String(data: try encoder.encode(plans), encoding: .utf8)!
        return (specJSON, plansJSON)
    }

    /// Build a minimal pending ForgeProposalRecord for the given skill ID.
    private func makeRecord(
        id skillID: String = "quote-service",
        status: ForgeProposalStatus = .pending
    ) throws -> ForgeProposalRecord {
        let spec  = makeQuoteSpec(id: skillID)
        let plans = makeQuotePlans(id: skillID)
        let (specJSON, plansJSON) = try encodeProposalJSON(spec: spec, plans: plans)
        return ForgeProposalRecord(
            skillID: skillID,
            name: "\(skillID) skill",
            description: "Test proposal for \(skillID).",
            summary: "Fetches and rates quotes.",
            riskLevel: "low",
            status: status,
            specJSON: specJSON,
            plansJSON: plansJSON
        )
    }

    // ─────────────────────────────────────────────────────────────────
    // MARK: - 1. Store CRUD
    // ─────────────────────────────────────────────────────────────────

    func testForgeProposalStoreSaveAndFetch() async throws {
        let store  = ForgeProposalStore(
            memoryStore: try MemoryStore(databasePath: temporaryDatabasePath())
        )
        let record = try makeRecord()

        try await store.save(record)

        let fetched = try await store.fetch(id: record.id)
        XCTAssertNotNil(fetched)
        XCTAssertEqual(fetched?.id, record.id)
        XCTAssertEqual(fetched?.skillID, "quote-service")
        XCTAssertEqual(fetched?.status, .pending)
        XCTAssertEqual(fetched?.specJSON, record.specJSON)
        XCTAssertEqual(fetched?.plansJSON, record.plansJSON)
    }

    func testForgeProposalStoreFetchMissingIDReturnsNil() async throws {
        let store = ForgeProposalStore(
            memoryStore: try MemoryStore(databasePath: temporaryDatabasePath())
        )

        let result = try await store.fetch(id: UUID())
        XCTAssertNil(result, "Fetching an unknown proposal ID must return nil — not throw.")
    }

    func testForgeProposalStoreListActiveExcludesRejected() async throws {
        let store = ForgeProposalStore(
            memoryStore: try MemoryStore(databasePath: temporaryDatabasePath())
        )

        // Save one proposal per status.
        let statusMap: [(String, ForgeProposalStatus)] = [
            ("skill-pending", .pending),
            ("skill-installed", .installed),
            ("skill-enabled", .enabled),
            ("skill-rejected", .rejected)
        ]
        for (skillID, status) in statusMap {
            try await store.save(try makeRecord(id: skillID, status: status))
        }

        let active = try await store.listActive()

        XCTAssertEqual(active.count, 3, "listActive() must return pending + installed + enabled (3 of 4).")
        let ids = Set(active.map(\.skillID))
        XCTAssertTrue(ids.contains("skill-pending"))
        XCTAssertTrue(ids.contains("skill-installed"))
        XCTAssertTrue(ids.contains("skill-enabled"))
        XCTAssertFalse(ids.contains("skill-rejected"), "Rejected proposals must be excluded from listActive().")
    }

    // ─────────────────────────────────────────────────────────────────
    // MARK: - 2. Status persistence across instances (restart simulation)
    // ─────────────────────────────────────────────────────────────────

    func testForgeProposalStoreUpdateStatusPersistsAcrossNewStoreInstance() async throws {
        let dbPath = temporaryDatabasePath()

        // Session 1: save a pending proposal then approve it.
        let record = try makeRecord()
        let store1 = ForgeProposalStore(memoryStore: try MemoryStore(databasePath: dbPath))
        try await store1.save(record)
        try await store1.updateStatus(id: record.id, status: .installed)

        // Session 2: brand-new MemoryStore + ForgeProposalStore pointing at the same file.
        let store2  = ForgeProposalStore(memoryStore: try MemoryStore(databasePath: dbPath))
        let fetched = try await store2.fetch(id: record.id)

        XCTAssertEqual(
            fetched?.status, .installed,
            "updateStatus must write through to SQLite and be visible to a new store instance."
        )
    }

    // ─────────────────────────────────────────────────────────────────
    // MARK: - 3. hydrateInstalledSkills — status filtering
    // ─────────────────────────────────────────────────────────────────

    func testHydrateInstalledSkillsIgnoresPendingProposals() async throws {
        let dbPath = temporaryDatabasePath()

        // Session 1: save a pending proposal (no approval).
        let (session1, _, _) = try await bootSession(dbPath: dbPath)
        _ = try await session1.createProposal(
            spec: makeQuoteSpec(),
            plans: makeQuotePlans(),
            summary: "Pending — awaiting user decision."
        )

        // Session 2 (restart): hydrate and check nothing was registered.
        let (session2, registry2, _) = try await bootSession(dbPath: dbPath)
        await session2.hydrateInstalledSkills()

        let all = await registry2.listAll()
        XCTAssertTrue(all.isEmpty, "Pending proposals must NOT be hydrated into SkillRegistry.")
    }

    func testHydrateInstalledSkillsIgnoresRejectedProposals() async throws {
        let dbPath = temporaryDatabasePath()

        // Session 1: save a proposal and immediately reject it.
        let (session1, _, store1) = try await bootSession(dbPath: dbPath)
        let proposal = try await session1.createProposal(
            spec: makeQuoteSpec(),
            plans: makeQuotePlans(),
            summary: "Rejected by user."
        )
        try await store1.updateStatus(id: proposal.id, status: .rejected)

        // Session 2 (restart): rejected proposals must be skipped.
        let (session2, registry2, _) = try await bootSession(dbPath: dbPath)
        await session2.hydrateInstalledSkills()

        let all = await registry2.listAll()
        XCTAssertTrue(all.isEmpty, "Rejected proposals must NOT be hydrated into SkillRegistry.")
    }

    // ─────────────────────────────────────────────────────────────────
    // MARK: - 4. hydrateInstalledSkills — skill restoration
    // ─────────────────────────────────────────────────────────────────

    func testHydrateInstalledSkillsRestoresInstalledProposal() async throws {
        let dbPath = temporaryDatabasePath()

        // Session 1: create proposal and approve (install, no enable).
        let (session1, _, _) = try await bootSession(dbPath: dbPath)
        let proposal = try await session1.createProposal(
            spec: makeQuoteSpec(),
            plans: makeQuotePlans(),
            summary: "Installed but not yet enabled."
        )
        _ = try await session1.approveProposal(id: proposal.id, enable: false)

        // Session 2 (restart): hydrate with clean registry + clean UserDefaults.
        let resync = ResyncCounter()
        let (session2, registry2, _) = try await bootSession(dbPath: dbPath, resyncCounter: resync)
        await session2.hydrateInstalledSkills()

        // The skill should now be in the registry.
        let record = await registry2.skill(id: "quote-service")
        XCTAssertNotNil(record, "Hydration must re-register a .installed Forge skill.")

        // An installed (but not enabled) skill should remain in .installed state.
        XCTAssertEqual(
            record?.manifest.lifecycleState, .installed,
            "Hydrated .installed proposal must produce a skill in .installed state."
        )

        // install does NOT trigger a resync — only enable does.
        let resyncCount = await resync.count
        XCTAssertEqual(resyncCount, 0, "Installing without enabling must not trigger a catalog resync.")
    }

    func testHydrateInstalledSkillsRestoresEnabledProposalAndTriggersResync() async throws {
        let dbPath = temporaryDatabasePath()

        // Session 1: create proposal, approve, and enable.
        let (session1, _, _) = try await bootSession(dbPath: dbPath)
        let proposal = try await session1.createProposal(
            spec: makeQuoteSpec(),
            plans: makeQuotePlans(),
            summary: "Installed and enabled."
        )
        _ = try await session1.approveProposal(id: proposal.id, enable: true)

        // Session 2 (restart): hydrate — must re-register and re-enable.
        let resync = ResyncCounter()
        let (session2, registry2, _) = try await bootSession(dbPath: dbPath, resyncCounter: resync)
        await session2.hydrateInstalledSkills()

        // Skill must be in registry and in .enabled state.
        let record = await registry2.skill(id: "quote-service")
        XCTAssertNotNil(record, "Hydration must re-register a .enabled Forge skill.")
        XCTAssertEqual(
            record?.manifest.lifecycleState, .enabled,
            "Hydrated .enabled proposal must produce a skill in .enabled state."
        )

        // Enabling during hydration triggers the resync callback once.
        let resyncCount = await resync.count
        XCTAssertEqual(resyncCount, 1,
            "Re-enabling a Forge skill during hydration must trigger exactly one catalog resync.")
    }

    func testHydrateInstalledSkillsEnabledSkillAppearsInEnabledCatalog() async throws {
        let dbPath = temporaryDatabasePath()

        // Session 1: approve and enable.
        let (session1, _, _) = try await bootSession(dbPath: dbPath)
        let proposal = try await session1.createProposal(
            spec: makeQuoteSpec(),
            plans: makeQuotePlans(),
            summary: "Enabled skill for catalog test."
        )
        _ = try await session1.approveProposal(id: proposal.id, enable: true)

        // Session 2 (restart): hydrate and check enabled catalog.
        let (session2, registry2, _) = try await bootSession(dbPath: dbPath)
        await session2.hydrateInstalledSkills()

        let enabled = await registry2.listEnabled()
        let enabledIDs = enabled.map(\.manifest.id)
        XCTAssertTrue(
            enabledIDs.contains("quote-service"),
            "An .enabled Forge skill must appear in registry.listEnabled() after hydration."
        )
    }

    // ─────────────────────────────────────────────────────────────────
    // MARK: - 5. hydrateInstalledSkills — idempotency
    // ─────────────────────────────────────────────────────────────────

    func testHydrateInstalledSkillsIsIdempotent() async throws {
        let dbPath = temporaryDatabasePath()

        // Session 1: approve and install.
        let (session1, _, _) = try await bootSession(dbPath: dbPath)
        let proposal = try await session1.createProposal(
            spec: makeQuoteSpec(),
            plans: makeQuotePlans(),
            summary: "Idempotency test."
        )
        _ = try await session1.approveProposal(id: proposal.id, enable: false)

        // Session 2 (restart): call hydrateInstalledSkills twice — must not crash or duplicate.
        let (session2, registry2, _) = try await bootSession(dbPath: dbPath)
        await session2.hydrateInstalledSkills()
        await session2.hydrateInstalledSkills()  // second call must be a no-op

        let all = await registry2.listAll()
        let forgeSkills = all.filter { $0.manifest.source == "forge" }
        XCTAssertEqual(
            forgeSkills.count, 1,
            "Double hydration must not register the skill twice (CoreSkillService is idempotent)."
        )
    }

    func testHydrateInstalledSkillsWithNoRecordsIsNoOp() async throws {
        // Empty database: hydration must complete without error and register nothing.
        let (session, registry, _) = try await bootSession(dbPath: temporaryDatabasePath())
        await session.hydrateInstalledSkills()

        let all = await registry.listAll()
        XCTAssertTrue(all.isEmpty, "Hydration of an empty database must register no skills.")
    }

    // ─────────────────────────────────────────────────────────────────
    // MARK: - 6 & 7. createProposal — metadata extraction
    // ─────────────────────────────────────────────────────────────────

    func testCreateProposalExtractsDomainsFromPlanURLHosts() async throws {
        let (service, _, _) = try await bootSession(dbPath: temporaryDatabasePath())

        let spec = makeQuoteSpec()
        let plans: [ForgeActionPlan] = [
            ForgeActionPlan(
                actionID: "quote-service.get",
                type: .http,
                httpRequest: HTTPRequestPlan(
                    method: "GET",
                    url: "https://api.quotes.mock.atlas.test/v1/quotes"
                )
            ),
            ForgeActionPlan(
                actionID: "quote-service.rate",
                type: .http,
                httpRequest: HTTPRequestPlan(
                    method: "POST",
                    url: "https://api.quotes.mock.atlas.test/v1/reviews"  // same host, different path
                )
            )
        ]

        let proposal = try await service.createProposal(spec: spec, plans: plans, summary: "Domain test.")

        // Both plans use the same host — expect exactly one domain entry (deduped + sorted).
        XCTAssertEqual(
            proposal.domains,
            ["api.quotes.mock.atlas.test"],
            "createProposal must extract URL host(s) from HTTP plans, deduplicated and sorted."
        )
    }

    func testCreateProposalDeduplicatesDomainsAcrossMultipleHosts() async throws {
        let spec = ForgeSkillSpec(
            id: "multi-host-skill",
            name: "Multi-Host Skill",
            description: "Contacts two different external APIs.",
            category: .research,
            riskLevel: .medium,
            actions: [
                ForgeActionSpec(
                    id: "multi-host-skill.alpha",
                    name: "Alpha",
                    description: "Calls alpha API.",
                    permissionLevel: .read
                ),
                ForgeActionSpec(
                    id: "multi-host-skill.beta",
                    name: "Beta",
                    description: "Calls beta API.",
                    permissionLevel: .read
                ),
                ForgeActionSpec(
                    id: "multi-host-skill.gamma",
                    name: "Gamma",
                    description: "Also calls alpha API.",
                    permissionLevel: .read
                )
            ]
        )
        let plans: [ForgeActionPlan] = [
            ForgeActionPlan(
                actionID: "multi-host-skill.alpha",
                type: .http,
                httpRequest: HTTPRequestPlan(method: "GET", url: "https://api.alpha.test/v1/data")
            ),
            ForgeActionPlan(
                actionID: "multi-host-skill.beta",
                type: .http,
                httpRequest: HTTPRequestPlan(method: "GET", url: "https://api.beta.test/v1/data")
            ),
            ForgeActionPlan(
                actionID: "multi-host-skill.gamma",
                type: .http,
                httpRequest: HTTPRequestPlan(method: "GET", url: "https://api.alpha.test/v2/data")
            )
        ]

        let (service, _, _) = try await bootSession(dbPath: temporaryDatabasePath())
        let proposal = try await service.createProposal(spec: spec, plans: plans, summary: "Multi-host test.")

        // Two distinct hosts: alpha + beta, sorted alphabetically.
        XCTAssertEqual(proposal.domains, ["api.alpha.test", "api.beta.test"])
    }

    func testCreateProposalExtractsRequiredSecretsFromPlanSecretHeader() async throws {
        let authSpec = ForgeSkillSpec(
            id: "auth-quotes-skill",
            name: "Authenticated Quote Skill",
            description: "Fetches premium quotes via an authenticated API.",
            category: .utility,
            riskLevel: .low,
            actions: [
                ForgeActionSpec(
                    id: "auth-quotes-skill.get",
                    name: "Get Premium Quote",
                    description: "Fetch a premium quote using API key.",
                    permissionLevel: .read
                )
            ]
        )
        let plans: [ForgeActionPlan] = [
            ForgeActionPlan(
                actionID: "auth-quotes-skill.get",
                type: .http,
                httpRequest: HTTPRequestPlan(
                    method: "GET",
                    url: "https://api.auth.mock.atlas.test/v1/premium-quotes",
                    secretHeader: "com.mock.atlas.test.quotes"
                )
            )
        ]

        let (service, _, _) = try await bootSession(dbPath: temporaryDatabasePath())
        let proposal = try await service.createProposal(spec: authSpec, plans: plans, summary: "Auth test.")

        XCTAssertEqual(
            proposal.requiredSecrets,
            ["com.mock.atlas.test.quotes"],
            "createProposal must extract the secretHeader service key into requiredSecrets."
        )
    }

    func testCreateProposalDeduplicatesRequiredSecrets() async throws {
        // Two actions referencing the same secretHeader — requiredSecrets must be deduped.
        let spec = ForgeSkillSpec(
            id: "dup-secret-skill",
            name: "Dup Secret Skill",
            description: "Two actions, same credential.",
            category: .utility,
            riskLevel: .low,
            actions: [
                ForgeActionSpec(id: "dup-secret-skill.a", name: "A", description: ".", permissionLevel: .read),
                ForgeActionSpec(id: "dup-secret-skill.b", name: "B", description: ".", permissionLevel: .read)
            ]
        )
        let sharedSecret = "com.shared.secret.key"
        let plans: [ForgeActionPlan] = [
            ForgeActionPlan(
                actionID: "dup-secret-skill.a",
                type: .http,
                httpRequest: HTTPRequestPlan(method: "GET", url: "https://api.test.com/a",
                                             secretHeader: sharedSecret)
            ),
            ForgeActionPlan(
                actionID: "dup-secret-skill.b",
                type: .http,
                httpRequest: HTTPRequestPlan(method: "GET", url: "https://api.test.com/b",
                                             secretHeader: sharedSecret)
            )
        ]

        let (service, _, _) = try await bootSession(dbPath: temporaryDatabasePath())
        let proposal = try await service.createProposal(spec: spec, plans: plans, summary: "Dedup secret.")

        XCTAssertEqual(
            proposal.requiredSecrets.count, 1,
            "createProposal must deduplicate secretHeader values in requiredSecrets."
        )
        XCTAssertEqual(proposal.requiredSecrets.first, sharedSecret)
    }

    func testCreateProposalWithNoSecretHeaderHasEmptyRequiredSecrets() async throws {
        let (service, _, _) = try await bootSession(dbPath: temporaryDatabasePath())
        let proposal = try await service.createProposal(
            spec: makeQuoteSpec(),
            plans: makeQuotePlans(),
            summary: "No auth required."
        )

        XCTAssertTrue(
            proposal.requiredSecrets.isEmpty,
            "A skill with no secretHeader in any plan must have an empty requiredSecrets array."
        )
    }

    // ─────────────────────────────────────────────────────────────────
    // MARK: - Proposal lifecycle error paths
    // ─────────────────────────────────────────────────────────────────

    func testApproveProposalAlreadyApprovedThrows() async throws {
        // approveProposal guards proposal.status == .pending.
        // Calling it twice must throw ForgeProposalError.invalidStatus on the second call.
        let (service, _, _) = try await bootSession(dbPath: temporaryDatabasePath())
        let proposal = try await service.createProposal(
            spec: makeQuoteSpec(),
            plans: makeQuotePlans(),
            summary: "Approve twice — second must throw."
        )

        // First approval succeeds and moves status to .installed
        _ = try await service.approveProposal(id: proposal.id, enable: false)

        // Second approval on .installed proposal must throw .invalidStatus
        do {
            _ = try await service.approveProposal(id: proposal.id, enable: false)
            XCTFail("Expected ForgeProposalError.invalidStatus on double-approve.")
        } catch let error as ForgeProposalError {
            guard case .invalidStatus = error else {
                XCTFail("Expected .invalidStatus, got: \(error)"); return
            }
            // expected — .installed is not .pending
        } catch {
            XCTFail("Expected ForgeProposalError, got unexpected error type: \(error)")
        }
    }

    func testRejectProposalDirectCallUpdatesStatusToRejected() async throws {
        // Verify the full service-level reject path, not just a pre-seeded rejected record.
        let (service, _, store) = try await bootSession(dbPath: temporaryDatabasePath())
        let proposal = try await service.createProposal(
            spec: makeQuoteSpec(),
            plans: makeQuotePlans(),
            summary: "Pending — about to be rejected."
        )

        XCTAssertEqual(proposal.status, .pending)

        let rejected = try await service.rejectProposal(id: proposal.id)
        XCTAssertEqual(rejected.status, .rejected)

        // Persisted to SQLite
        let persisted = try await store.fetch(id: proposal.id)
        XCTAssertEqual(persisted?.status, .rejected,
                       "rejectProposal must persist .rejected status to SQLite.")

        // Rejected proposals must not appear in listActive()
        let active = try await store.listActive()
        XCTAssertFalse(active.contains(where: { $0.id == proposal.id }),
                       "Rejected proposal must not appear in listActive().")
    }

    func testApproveProposalNotFoundThrows() async throws {
        let (service, _, _) = try await bootSession(dbPath: temporaryDatabasePath())
        let nonexistentID = UUID()

        do {
            _ = try await service.approveProposal(id: nonexistentID, enable: false)
            XCTFail("Expected ForgeProposalError.proposalNotFound")
        } catch let error as ForgeProposalError {
            guard case .proposalNotFound(let id) = error else {
                XCTFail("Expected .proposalNotFound, got: \(error)"); return
            }
            XCTAssertEqual(id, nonexistentID)
        } catch {
            XCTFail("Expected ForgeProposalError, got: \(error)")
        }
    }

    // ─────────────────────────────────────────────────────────────────
    // MARK: - Full restart round-trip smoke test
    // ─────────────────────────────────────────────────────────────────

    /// Simulates a complete operator lifecycle spanning two daemon sessions:
    ///
    /// Session 1:
    ///   1. Agent creates a Forge proposal.
    ///   2. User approves and enables it.
    ///   3. Skill appears as enabled in registry.
    ///   4. Daemon "shuts down" (session1 variables discarded).
    ///
    /// Session 2 (restart):
    ///   5. New daemon starts — registry is empty.
    ///   6. hydrateInstalledSkills() runs.
    ///   7. Skill re-appears in registry in .enabled state.
    ///   8. Resync triggered once.
    ///   9. Proposal record in SQLite still shows .enabled status.
    func testFullRestartRoundTrip() async throws {
        let dbPath = temporaryDatabasePath()

        // ── Session 1 ──────────────────────────────────────────────
        let (session1, registry1, store1) = try await bootSession(dbPath: dbPath)

        let proposal = try await session1.createProposal(
            spec: makeQuoteSpec(),
            plans: makeQuotePlans(),
            summary: "Quote skill — created in session 1.",
            rationale: "User asked for a quote fetcher."
        )

        XCTAssertEqual(proposal.status, .pending)

        let approved = try await session1.approveProposal(id: proposal.id, enable: true)
        XCTAssertEqual(approved.status, .enabled)

        // Skill is enabled in session 1 registry.
        let s1Record = await registry1.skill(id: "quote-service")
        XCTAssertEqual(s1Record?.manifest.lifecycleState, .enabled)

        // Drop session 1 references (simulates shutdown).
        _ = store1  // suppress unused warning

        // ── Session 2 (restart) ────────────────────────────────────
        let resync = ResyncCounter()
        let (session2, registry2, store2) = try await bootSession(dbPath: dbPath, resyncCounter: resync)

        // Before hydration: registry is empty.
        let beforeHydration = await registry2.skill(id: "quote-service")
        XCTAssertNil(beforeHydration, "Registry must be empty before hydrateInstalledSkills() is called.")

        await session2.hydrateInstalledSkills()

        // After hydration: skill is back.
        let s2Record = await registry2.skill(id: "quote-service")
        XCTAssertNotNil(s2Record, "Skill must be present after hydration.")
        XCTAssertEqual(
            s2Record?.manifest.lifecycleState, .enabled,
            "Hydrated .enabled proposal must restore the skill in .enabled state."
        )

        // Resync was triggered once (from re-enabling during hydration).
        let resyncCount = await resync.count
        XCTAssertEqual(resyncCount, 1)

        // SQLite proposal record still shows .enabled.
        let persistedProposal = try await store2.fetch(id: proposal.id)
        XCTAssertEqual(
            persistedProposal?.status, .enabled,
            "Proposal status in SQLite must still be .enabled after restart and hydration."
        )

        // Forged skill appears in listInstalledForgedSkills.
        let forged = await session2.listInstalledForgedSkills()
        XCTAssertTrue(
            forged.contains(where: { $0.manifest.id == "quote-service" }),
            "listInstalledForgedSkills() must include the hydrated skill."
        )
    }

    // MARK: - Uninstall Tests

    // ── 14. Uninstall removes skill from registry ──────────────────────────────────

    func testUninstallInstalledForgeSkillRemovesFromRegistry() async throws {
        let dbPath = temporaryDatabasePath()
        let session = try await bootSession(dbPath: dbPath)

        // Install a Forge skill.
        let spec  = makeQuoteSpec()
        let plans = makeQuotePlans()
        let proposal = try await session.service.createProposal(
            spec: spec, plans: plans, summary: "Test skill", rationale: nil
        )
        _ = try await session.service.approveProposal(id: proposal.id, enable: false)

        // Verify it is registered.
        let before = await session.registry.skill(id: spec.id)
        XCTAssertNotNil(before, "Skill must be in registry before uninstall.")

        // Uninstall.
        try await session.service.uninstallForgeSkill(skillID: spec.id)

        // Must no longer be in registry.
        let after = await session.registry.skill(id: spec.id)
        XCTAssertNil(after, "Skill must be absent from registry after uninstall.")

        // Must no longer appear in listInstalledForgedSkills.
        let forged = await session.service.listInstalledForgedSkills()
        XCTAssertFalse(
            forged.contains(where: { $0.manifest.id == spec.id }),
            "listInstalledForgedSkills() must not include an uninstalled skill."
        )
    }

    // ── 15. Uninstall an enabled skill removes it from the action catalog ──────────

    func testUninstallEnabledForgeSkillRemovesFromActionCatalog() async throws {
        let dbPath   = temporaryDatabasePath()
        let counter  = ResyncCounter()
        let session  = try await bootSession(dbPath: dbPath, resyncCounter: counter)

        let spec  = makeQuoteSpec()
        let plans = makeQuotePlans()
        let proposal = try await session.service.createProposal(
            spec: spec, plans: plans, summary: "Test skill", rationale: nil
        )
        // Install and immediately enable.
        _ = try await session.service.approveProposal(id: proposal.id, enable: true)

        // Confirm skill is in the enabled action catalog.
        let catalogBefore = await session.registry.enabledActionCatalog()
        XCTAssertTrue(
            catalogBefore.contains(where: { $0.skillID == spec.id }),
            "Enabled skill must appear in the action catalog before uninstall."
        )

        // Uninstall.
        try await session.service.uninstallForgeSkill(skillID: spec.id)

        // Skill must be absent from the action catalog immediately.
        let catalogAfter = await session.registry.enabledActionCatalog()
        XCTAssertFalse(
            catalogAfter.contains(where: { $0.skillID == spec.id }),
            "Uninstalled skill must not appear in the action catalog."
        )
    }

    // ── 16. Uninstall triggers resync ─────────────────────────────────────────────

    func testUninstallTriggersResync() async throws {
        let dbPath  = temporaryDatabasePath()
        let counter = ResyncCounter()
        let session = try await bootSession(dbPath: dbPath, resyncCounter: counter)

        let spec  = makeQuoteSpec()
        let plans = makeQuotePlans()
        let proposal = try await session.service.createProposal(
            spec: spec, plans: plans, summary: "Test skill", rationale: nil
        )
        _ = try await session.service.approveProposal(id: proposal.id, enable: true)

        let countBeforeUninstall = await counter.count

        try await session.service.uninstallForgeSkill(skillID: spec.id)

        let countAfterUninstall = await counter.count
        XCTAssertGreaterThan(
            countAfterUninstall, countBeforeUninstall,
            "Uninstall must trigger at least one resync callback."
        )
    }

    // ── 17. Uninstalled skill does not return after restart/hydration ─────────────

    func testUninstalledSkillDoesNotReturnAfterHydration() async throws {
        let dbPath   = temporaryDatabasePath()
        let session1 = try await bootSession(dbPath: dbPath)

        let spec  = makeQuoteSpec()
        let plans = makeQuotePlans()
        let proposal = try await session1.service.createProposal(
            spec: spec, plans: plans, summary: "Test skill", rationale: nil
        )
        _ = try await session1.service.approveProposal(id: proposal.id, enable: true)

        // Uninstall in session 1.
        try await session1.service.uninstallForgeSkill(skillID: spec.id)

        // Verify proposal status is .uninstalled in SQLite.
        let stored = try await session1.store.findBySkillID(spec.id)
        XCTAssertEqual(stored?.status, .uninstalled,
            "Proposal status must be .uninstalled in SQLite after uninstall.")

        // Simulate daemon restart with a fresh session.
        let session2 = try await bootSession(dbPath: dbPath)
        await session2.service.hydrateInstalledSkills()

        // Skill must NOT be re-registered.
        let rehydrated = await session2.registry.skill(id: spec.id)
        XCTAssertNil(rehydrated,
            "Uninstalled skill must not reappear in the registry after restart hydration.")
    }

    // ── 18. Uninstalling a non-Forge skill is rejected ────────────────────────────

    func testUninstallNonForgeSkillIsRejected() async throws {
        let dbPath  = temporaryDatabasePath()
        let session = try await bootSession(dbPath: dbPath)

        // Register a plain (non-Forge) skill directly.
        let builtInSkill = AtlasInfoSkill()
        try await session.registry.register(builtInSkill)

        do {
            try await session.service.uninstallForgeSkill(skillID: builtInSkill.manifest.id)
            XCTFail("Expected CoreSkillServiceError.notAForgeSkill for a built-in skill.")
        } catch let error as CoreSkillServiceError {
            guard case .notAForgeSkill(let id) = error else {
                XCTFail("Expected .notAForgeSkill, got: \(error)"); return
            }
            XCTAssertEqual(id, builtInSkill.manifest.id)
        }

        // Built-in skill must still be registered.
        let record = await session.registry.skill(id: builtInSkill.manifest.id)
        XCTAssertNotNil(record, "Built-in skill must remain in registry after rejected uninstall.")
    }

    // ── 19. Uninstall updates proposal status and excludes from listActive ─────────

    func testUninstallUpdatesProposalStatusAndExcludesFromListActive() async throws {
        let dbPath  = temporaryDatabasePath()
        let session = try await bootSession(dbPath: dbPath)

        let spec  = makeQuoteSpec()
        let plans = makeQuotePlans()
        let proposal = try await session.service.createProposal(
            spec: spec, plans: plans, summary: "Test skill", rationale: nil
        )
        _ = try await session.service.approveProposal(id: proposal.id, enable: false)

        // Confirm it appears in listActive before uninstall.
        let activeBefore = try await session.store.listActive()
        XCTAssertTrue(activeBefore.contains(where: { $0.skillID == spec.id }),
            "Installed proposal must appear in listActive before uninstall.")

        try await session.service.uninstallForgeSkill(skillID: spec.id)

        // Must no longer appear in listActive.
        let activeAfter = try await session.store.listActive()
        XCTAssertFalse(activeAfter.contains(where: { $0.skillID == spec.id }),
            "Uninstalled proposal must not appear in listActive.")

        // Status in store must be .uninstalled.
        let stored = try await session.store.findBySkillID(spec.id)
        XCTAssertEqual(stored?.status, .uninstalled,
            "Proposal must have .uninstalled status in the store.")
    }
}
