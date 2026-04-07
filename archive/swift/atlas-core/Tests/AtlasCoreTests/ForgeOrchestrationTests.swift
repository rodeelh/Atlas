import XCTest
@testable import AtlasCore
import AtlasGuard
import AtlasLogging
import AtlasMemory
import AtlasShared
import AtlasSkills
import AtlasTools

final class ForgeOrchestrationTests: XCTestCase {

    // MARK: - Helpers

    private func makeStore() throws -> (ForgeProposalStore, URL) {
        let url = FileManager.default.temporaryDirectory
            .appendingPathComponent("ForgeOrcTests-\(UUID().uuidString).sqlite3")
        let memoryStore = try MemoryStore(databasePath: url.path)
        return (ForgeProposalStore(memoryStore: memoryStore), url)
    }

    private func makeHandlers(store: ForgeProposalStore) -> (ForgeOrchestrationHandlers, ForgeProposalService) {
        let service = ForgeProposalService(store: store)
        let registry = SkillRegistry(
            defaults: UserDefaults(suiteName: "ForgeOrcTests.\(UUID().uuidString)")!
        )
        let coreSkills = CoreSkillsRuntime(
            registry: registry,
            secretsReader: { _ in nil }
        )
        Task { await service.configure(coreSkills: coreSkills, skillRegistry: registry) }
        let handlers = ForgeOrchestrationHandlers(
            startResearching: { title, message in
                await service.startResearching(title: title, message: message).id
            },
            stopResearching: { id in
                await service.stopResearching(id: id)
            },
            createProposal: { spec, plans, summary, rationale, contractJSON in
                try await service.createProposal(spec: spec, plans: plans, summary: summary, rationale: rationale, contractJSON: contractJSON)
            }
        )
        return (handlers, service)
    }

    private func makeContext() -> SkillExecutionContext {
        SkillExecutionContext(
            conversationID: nil,
            logger: AtlasLogger(category: "test"),
            config: AtlasConfig(),
            permissionManager: PermissionManager(),
            runtimeStatusProvider: { nil },
            enabledSkillsProvider: { [] }
        )
    }

    /// Returns spec + plans + a high-confidence contract for a simple GET API skill.
    /// All existing tests that expect successful proposal creation use this fixture.
    private func makeValidSpec() -> (specJSON: String, plansJSON: String, contractJSON: String) {
        let spec = #"{"id":"test-tracker","name":"Test Tracker","description":"Tracks test packages.","category":"utility","riskLevel":"low","tags":["tracking"],"actions":[{"id":"test-tracker.get-status","name":"Get Status","description":"Get package status.","permissionLevel":"read","inputSchema":{"type":"object","properties":{"trackingNumber":{"type":"string","description":"The tracking number"}},"required":["trackingNumber"],"additionalProperties":false}}]}"#
        let plans = #"[{"actionID":"test-tracker.get-status","type":"http","httpRequest":{"method":"GET","url":"https://api.example-tracker.com/status/{trackingNumber}","headers":{},"query":{},"secretHeader":null}}]"#
        let contract = makeContractJSON(
            providerName: "Example Tracker API",
            docsURL: "https://docs.example-tracker.com",
            docsQuality: "high",
            baseURL: "https://api.example-tracker.com",
            endpoint: "/status/{trackingNumber}",
            method: "GET",
            authType: "none",
            requiredParams: ["trackingNumber"],
            paramLocations: ["trackingNumber": "path"],
            mappingConfidence: "high"
        )
        return (spec, plans, contract)
    }

    /// Builds a contract JSON string from discrete fields.
    private func makeContractJSON(
        providerName: String = "Test API",
        docsURL: String? = "https://docs.test.api",
        docsQuality: String = "high",
        baseURL: String? = "https://api.test.com",
        endpoint: String? = "/v1/resource",
        method: String? = "GET",
        authType: String? = "none",
        requiredParams: [String] = [],
        paramLocations: [String: String] = [:],
        exampleResponse: String? = "{\"id\": \"abc\", \"value\": 42}",
        mappingConfidence: String = "high",
        validationStatus: String = "unknown",
        notes: String? = nil
    ) -> String {
        var obj: [String: Any] = [
            "providerName": providerName,
            "docsQuality": docsQuality,
            "requiredParams": requiredParams,
            "optionalParams": [],
            "paramLocations": paramLocations,
            "mappingConfidence": mappingConfidence,
            "validationStatus": validationStatus
        ]
        if let docsURL { obj["docsURL"]         = docsURL         }
        if let baseURL { obj["baseURL"]         = baseURL         }
        if let endpoint { obj["endpoint"]        = endpoint        }
        if let method { obj["method"]          = method          }
        if let authType { obj["authType"]        = authType        }
        if let exampleResponse { obj["exampleResponse"] = exampleResponse }
        if let notes { obj["notes"]           = notes           }
        return String(data: try! JSONSerialization.data(withJSONObject: obj, options: [.sortedKeys]), encoding: .utf8)!
    }

    /// Build a properly-encoded `AtlasToolInput` whose argumentsJSON is a `[String: Any]` dict.
    private func makeToolInput(_ params: [String: Any]) -> AtlasToolInput {
        let data = try! JSONSerialization.data(withJSONObject: params, options: [])
        return AtlasToolInput(argumentsJSON: String(data: data, encoding: .utf8)!)
    }

    // MARK: - Existing Orchestration Tests (updated with contract_json)

    /// Explicit Forge request triggers researching state.
    func testForgeOrchestrationStartsResearchingState() async throws {
        let (store, _) = try makeStore()
        let (handlers, service) = makeHandlers(store: store)
        let skill = ForgeOrchestrationSkill(handlers: handlers)
        let (specJSON, plansJSON, contractJSON) = makeValidSpec()

        let result = try await skill.execute(
            actionID: "forge.orchestration.propose",
            input: makeToolInput([
                "spec_json": specJSON,
                "plans_json": plansJSON,
                "contract_json": contractJSON,
                "summary": "Package tracking skill"
            ]),
            context: makeContext()
        )

        XCTAssertTrue(result.success)
        // After success, researching state must be cleared.
        let researching = await service.listResearching()
        XCTAssertTrue(researching.isEmpty, "Researching items should be cleared after successful proposal creation. Found: \(researching)")
    }

    /// Proposal is created and persisted.
    func testForgeOrchestrationCreatesAndPersistsProposal() async throws {
        let (store, url) = try makeStore()
        let (handlers, _) = makeHandlers(store: store)
        let skill = ForgeOrchestrationSkill(handlers: handlers)
        let (specJSON, plansJSON, contractJSON) = makeValidSpec()

        let result = try await skill.execute(
            actionID: "forge.orchestration.propose",
            input: makeToolInput([
                "spec_json": specJSON,
                "plans_json": plansJSON,
                "contract_json": contractJSON,
                "summary": "Package tracking skill",
                "rationale": "User requested it"
            ]),
            context: makeContext()
        )

        XCTAssertTrue(result.success)
        XCTAssertNotNil(result.metadata["proposal_id"])
        XCTAssertEqual(result.metadata["skill_id"], "test-tracker")

        // Verify persistence: re-open the store and check the proposal is there.
        let reopenedStore = try MemoryStore(databasePath: url.path)
        let reopenedProposalStore = ForgeProposalStore(memoryStore: reopenedStore)
        let proposals = try await reopenedProposalStore.list()
        XCTAssertEqual(proposals.count, 1)
        XCTAssertEqual(proposals[0].skillID, "test-tracker")
        XCTAssertEqual(proposals[0].status, .pending)
    }

    /// Researching state clears after successful proposal creation.
    func testResearchingStateClearsAfterSuccess() async throws {
        let (store, _) = try makeStore()
        let (handlers, service) = makeHandlers(store: store)
        let skill = ForgeOrchestrationSkill(handlers: handlers)
        let (specJSON, plansJSON, contractJSON) = makeValidSpec()

        _ = try await skill.execute(
            actionID: "forge.orchestration.propose",
            input: makeToolInput([
                "spec_json": specJSON,
                "plans_json": plansJSON,
                "contract_json": contractJSON,
                "summary": "Test skill"
            ]),
            context: makeContext()
        )

        let remaining = await service.listResearching()
        XCTAssertTrue(remaining.isEmpty, "All researching items must be cleared after success.")
    }

    /// Researching state clears even when proposal creation throws.
    func testResearchingStateClearsAfterFailure() async throws {
        let (store, _) = try makeStore()
        let service = ForgeProposalService(store: store)

        let capturedID = ResearchingIDBox()

        let handlers = ForgeOrchestrationHandlers(
            startResearching: { title, message in
                let id = await service.startResearching(title: title, message: message).id
                await capturedID.set(id)
                return id
            },
            stopResearching: { id in
                await service.stopResearching(id: id)
            },
            createProposal: { _, _, _, _, _ in
                throw NSError(domain: "test", code: 1, userInfo: [NSLocalizedDescriptionKey: "Simulated DB failure"])
            }
        )

        let skill = ForgeOrchestrationSkill(handlers: handlers)
        let (specJSON, plansJSON, contractJSON) = makeValidSpec()

        do {
            _ = try await skill.execute(
                actionID: "forge.orchestration.propose",
                input: makeToolInput([
                    "spec_json": specJSON,
                    "plans_json": plansJSON,
                    "contract_json": contractJSON,
                    "summary": "Test skill"
                ]),
                context: makeContext()
            )
            XCTFail("Expected execution to throw on simulated DB failure.")
        } catch {
            // Expected path.
        }

        let captured = await capturedID.value
        XCTAssertNotNil(captured, "Researching state must have been started.")
        let remaining = await service.listResearching()
        XCTAssertTrue(remaining.isEmpty, "Researching state must be cleared even after failure.")
    }

    /// Invalid spec_json returns a structured SkillExecutionResult(success: false) rather than
    /// throwing. This prevents PATH B from surfacing the generic tool-call failure to the user.
    func testInvalidSpecJSONReturnsStructuredRefusal() async throws {
        let (store, _) = try makeStore()
        let (handlers, _) = makeHandlers(store: store)
        let skill = ForgeOrchestrationSkill(handlers: handlers)

        let result = try await skill.execute(
            actionID: "forge.orchestration.propose",
            input: makeToolInput([
                "spec_json": "not valid json at all",
                "plans_json": "[]",
                "contract_json": makeContractJSON(),
                "summary": "Bad spec test"
            ]),
            context: makeContext()
        )

        XCTAssertFalse(result.success, "Invalid spec_json must return success: false — not throw.")
        XCTAssertTrue(
            result.output.contains("spec_json") || result.output.contains("ForgeSkillSpec"),
            "Output must reference the bad field. Got: \(result.output)"
        )
        let proposals = try await store.list()
        XCTAssertTrue(proposals.isEmpty, "No proposal must be created when spec_json is unparseable.")
    }

    /// Spec with validation errors (e.g. reserved prefix) returns a structured refusal rather than
    /// throwing. A valid high-confidence contract is provided so the gate passes; failure is at
    /// spec validation. This prevents PATH B from surfacing the generic tool-call failure.
    func testSpecWithValidationErrorsReturnsStructuredRefusal() async throws {
        let (store, _) = try makeStore()
        let (handlers, _) = makeHandlers(store: store)
        let skill = ForgeOrchestrationSkill(handlers: handlers)

        // "atlas." prefix is reserved — spec validation must reject this.
        let badSpec = #"{"id":"atlas.forbidden","name":"Bad Skill","description":"Uses reserved prefix.","category":"utility","riskLevel":"low","tags":[],"actions":[]}"#

        let result = try await skill.execute(
            actionID: "forge.orchestration.propose",
            input: makeToolInput([
                "spec_json": badSpec,
                "plans_json": "[]",
                "contract_json": makeContractJSON(),
                "summary": "Bad prefix test"
            ]),
            context: makeContext()
        )

        XCTAssertFalse(result.success, "Spec with validation errors must return success: false — not throw.")
        XCTAssertTrue(
            result.output.contains("validation failed") || result.output.contains("reserved"),
            "Output must describe the validation failure. Got: \(result.output)"
        )
        let proposals = try await store.list()
        XCTAssertTrue(proposals.isEmpty, "No proposal must be created when spec validation fails.")
    }

    /// Resulting proposal appears in the existing Forge list path.
    func testProposalAppearsInForgeListPath() async throws {
        let (store, _) = try makeStore()
        let (handlers, _) = makeHandlers(store: store)
        let skill = ForgeOrchestrationSkill(handlers: handlers)
        let (specJSON, plansJSON, contractJSON) = makeValidSpec()

        _ = try await skill.execute(
            actionID: "forge.orchestration.propose",
            input: makeToolInput([
                "spec_json": specJSON,
                "plans_json": plansJSON,
                "contract_json": contractJSON,
                "summary": "Package tracking"
            ]),
            context: makeContext()
        )

        let proposals = try await store.list()
        XCTAssertEqual(proposals.count, 1)
        XCTAssertEqual(proposals[0].skillID, "test-tracker")
        XCTAssertEqual(proposals[0].status, .pending, "Proposal must remain pending — auto-install must not occur.")

        let pending = try await store.list(status: .pending)
        XCTAssertEqual(pending.count, 1)
    }

    /// User-facing response message directs to review and makes clear no install has occurred.
    func testResponseMessageDirectsToReview() async throws {
        let (store, _) = try makeStore()
        let (handlers, _) = makeHandlers(store: store)
        let skill = ForgeOrchestrationSkill(handlers: handlers)
        let (specJSON, plansJSON, contractJSON) = makeValidSpec()

        let result = try await skill.execute(
            actionID: "forge.orchestration.propose",
            input: makeToolInput([
                "spec_json": specJSON,
                "plans_json": plansJSON,
                "contract_json": contractJSON,
                "summary": "Package tracking"
            ]),
            context: makeContext()
        )

        XCTAssertTrue(result.success)
        let output = result.output.lowercased()
        XCTAssertTrue(
            output.contains("review") || output.contains("approve") || output.contains("pending"),
            "Output must direct the user to review the proposal. Got: \(result.output)"
        )
        XCTAssertFalse(
            output.contains("skill is installed") || output.contains("has been installed") || output.contains("skill is now active"),
            "Output must not claim the skill is already installed or active. Got: \(result.output)"
        )
        XCTAssertTrue(
            output.contains("not be active") || output.contains("approve") || output.contains("pending"),
            "Output must make clear the skill is not yet active. Got: \(result.output)"
        )
    }

    /// Handlers-nil skill returns graceful "not ready" message rather than crashing.
    func testNilHandlersReturnsNotReadyMessage() async throws {
        let skill = ForgeOrchestrationSkill(handlers: nil)

        let result = try await skill.execute(
            actionID: "forge.orchestration.propose",
            input: makeToolInput(["spec_json": "{}", "plans_json": "[]", "summary": "test"]),
            context: makeContext()
        )

        XCTAssertFalse(result.success)
        XCTAssertTrue(result.output.lowercased().contains("not yet ready") || result.output.lowercased().contains("initialising"),
                      "Should return a graceful not-ready message.")
    }

    // MARK: - Gate Tests: API skills

    /// API skill with low mapping confidence is refused — no proposal created.
    func testAPISkillWithLowMappingConfidenceIsRefused() async throws {
        let (store, _) = try makeStore()
        let (handlers, _) = makeHandlers(store: store)
        let skill = ForgeOrchestrationSkill(handlers: handlers)
        let (specJSON, plansJSON, _) = makeValidSpec()

        let weakContract = makeContractJSON(
            docsQuality: "medium",
            endpoint: "/status/{trackingNumber}",
            method: "GET",
            mappingConfidence: "low"   // ← fails gate 2
        )

        let result = try await skill.execute(
            actionID: "forge.orchestration.propose",
            input: makeToolInput([
                "spec_json": specJSON,
                "plans_json": plansJSON,
                "contract_json": weakContract,
                "summary": "Should be refused"
            ]),
            context: makeContext()
        )

        XCTAssertFalse(result.success, "Low mapping confidence must be refused.")
        let proposals = try await store.list()
        XCTAssertTrue(proposals.isEmpty, "No proposal must be created when gate fails.")
        XCTAssertTrue(
            result.output.lowercased().contains("confidence") || result.output.lowercased().contains("can't safely"),
            "Output must explain the confidence problem. Got: \(result.output)"
        )
    }

    /// API skill with high-confidence contract proceeds to create a proposal.
    func testAPISkillWithHighConfidenceContractCreatesProposal() async throws {
        let (store, _) = try makeStore()
        let (handlers, _) = makeHandlers(store: store)
        let skill = ForgeOrchestrationSkill(handlers: handlers)
        let (specJSON, plansJSON, strongContract) = makeValidSpec()

        let result = try await skill.execute(
            actionID: "forge.orchestration.propose",
            input: makeToolInput([
                "spec_json": specJSON,
                "plans_json": plansJSON,
                "contract_json": strongContract,
                "summary": "High-confidence tracker skill"
            ]),
            context: makeContext()
        )

        XCTAssertTrue(result.success, "High-confidence contract must allow proposal creation.")
        let proposals = try await store.list()
        XCTAssertEqual(proposals.count, 1)
        XCTAssertEqual(proposals[0].skillID, "test-tracker")
    }

    /// API skill with low docs quality is refused.
    func testAPISkillWithLowDocsQualityIsRefused() async throws {
        let (store, _) = try makeStore()
        let (handlers, _) = makeHandlers(store: store)
        let skill = ForgeOrchestrationSkill(handlers: handlers)
        let (specJSON, plansJSON, _) = makeValidSpec()

        let poorDocsContract = makeContractJSON(
            docsQuality: "low",       // ← fails gate 1
            endpoint: "/status/{id}",
            method: "GET",
            mappingConfidence: "high"
        )

        let result = try await skill.execute(
            actionID: "forge.orchestration.propose",
            input: makeToolInput([
                "spec_json": specJSON,
                "plans_json": plansJSON,
                "contract_json": poorDocsContract,
                "summary": "Should be refused"
            ]),
            context: makeContext()
        )

        XCTAssertFalse(result.success, "Low docs quality must be refused.")
        let proposals = try await store.list()
        XCTAssertTrue(proposals.isEmpty, "No proposal must be created with low-quality docs.")
        XCTAssertTrue(
            result.output.lowercased().contains("documentation") || result.output.lowercased().contains("docs"),
            "Output must mention the documentation quality problem. Got: \(result.output)"
        )
    }

    /// API skill where a required param has no known location returns clarification, not a proposal.
    func testAPISkillWithMissingParamLocationReturnsClarification() async throws {
        let (store, _) = try makeStore()
        let (handlers, _) = makeHandlers(store: store)
        let skill = ForgeOrchestrationSkill(handlers: handlers)
        let (specJSON, plansJSON, _) = makeValidSpec()

        // trackingNumber is required but has NO entry in paramLocations → gate 5 fires.
        let unmappedContract = makeContractJSON(
            docsQuality: "high",
            endpoint: "/status/{trackingNumber}",
            method: "GET",
            requiredParams: ["trackingNumber"],
            paramLocations: [:],      // ← missing location for trackingNumber
            mappingConfidence: "high"
        )

        let result = try await skill.execute(
            actionID: "forge.orchestration.propose",
            input: makeToolInput([
                "spec_json": specJSON,
                "plans_json": plansJSON,
                "contract_json": unmappedContract,
                "summary": "Should ask for clarification"
            ]),
            context: makeContext()
        )

        XCTAssertFalse(result.success, "Missing param location must not produce a proposal.")
        let proposals = try await store.list()
        XCTAssertTrue(proposals.isEmpty, "No proposal must be created when param locations are unknown.")
        XCTAssertTrue(
            result.output.lowercased().contains("trackingNumber".lowercased()) ||
            result.output.lowercased().contains("location") ||
            result.output.lowercased().contains("clarif"),
            "Output must ask about the missing parameter location. Got: \(result.output)"
        )
    }

    /// A contract satisfying all five gate conditions results in a proposal — no refusal or clarification.
    /// This verifies end-to-end that every gate passes: docsQuality>=medium, mappingConfidence==high,
    /// endpoint defined, method defined, all required param locations known.
    func testContractSatisfyingAllGateConditionsCreatesProposal() async throws {
        let (store, _) = try makeStore()
        let (handlers, _) = makeHandlers(store: store)
        let skill = ForgeOrchestrationSkill(handlers: handlers)
        let (specJSON, plansJSON, _) = makeValidSpec()

        // Explicitly verify all 5 gate conditions are present in this contract.
        let allGatesPassContract = makeContractJSON(
            providerName: "Gate-Test API",
            docsURL: "https://docs.gate-test.example.com",
            docsQuality: "high",          // gate 1: >= medium ✓
            baseURL: "https://api.gate-test.example.com",
            endpoint: "/status/{trackingNumber}", // gate 3: defined ✓
            method: "GET",           // gate 4: known verb ✓
            authType: "none",
            requiredParams: ["trackingNumber"],
            paramLocations: ["trackingNumber": "path"], // gate 5: all mapped ✓
            mappingConfidence: "high"         // gate 2: == high ✓
        )

        let result = try await skill.execute(
            actionID: "forge.orchestration.propose",
            input: makeToolInput([
                "spec_json": specJSON,
                "plans_json": plansJSON,
                "contract_json": allGatesPassContract,
                "summary": "All five gates satisfied"
            ]),
            context: makeContext()
        )

        XCTAssertTrue(result.success, "A contract satisfying all gates must produce a proposal.")
        let proposals = try await store.list()
        XCTAssertEqual(proposals.count, 1, "Exactly one proposal must be created.")
        XCTAssertEqual(proposals[0].status, .pending)
    }

    /// Non-API skill (kind=composed) bypasses the contract gate entirely.
    /// No contract_json is provided — the proposal must still be created.
    func testComposedSkillBypassesAPIContractGate() async throws {
        let (store, _) = try makeStore()
        let (handlers, _) = makeHandlers(store: store)
        let skill = ForgeOrchestrationSkill(handlers: handlers)
        let (specJSON, plansJSON, _) = makeValidSpec()

        // No contract_json — would throw if kind were "api".
        let result = try await skill.execute(
            actionID: "forge.orchestration.propose",
            input: makeToolInput([
                "kind": "composed",   // ← bypasses API gate
                "spec_json": specJSON,
                "plans_json": plansJSON,
                "summary": "Composed skill — no API contract needed"
            ]),
            context: makeContext()
        )

        XCTAssertTrue(result.success, "Composed skill must succeed without contract_json.")
        let proposals = try await store.list()
        XCTAssertEqual(proposals.count, 1, "Proposal must be created for composed skill.")
    }

    /// Missing contract_json for an API skill returns a structured SkillExecutionResult(success: false)
    /// rather than throwing. This ensures the failure degrades as a Forge refusal (PATH A), not as the
    /// generic "Atlas could not complete the requested tool call." (PATH B).
    func testMissingContractForAPISkillReturnsStructuredRefusal() async throws {
        let (store, _) = try makeStore()
        let (handlers, _) = makeHandlers(store: store)
        let skill = ForgeOrchestrationSkill(handlers: handlers)
        let (specJSON, plansJSON, _) = makeValidSpec()

        let result = try await skill.execute(
            actionID: "forge.orchestration.propose",
            input: makeToolInput([
                "kind": "api",    // explicit api kind, no contract
                "spec_json": specJSON,
                "plans_json": plansJSON,
                "summary": "Missing contract"
            ]),
            context: makeContext()
        )

        XCTAssertFalse(result.success, "Missing contract_json must return success: false — not throw.")
        XCTAssertTrue(
            result.output.contains("contract_json"),
            "Output must mention contract_json. Got: \(result.output)"
        )
        let proposals = try await store.list()
        XCTAssertTrue(proposals.isEmpty, "No proposal must be created when contract_json is absent.")
    }
    // MARK: - PATH B Regression: validation/decode failures must never throw

    /// Malformed contract_json (not valid JSON) must return a structured refusal, not throw.
    /// Before the fix, this threw AtlasToolError.invalidInput → ToolExecutionError.failed →
    /// AtlasToolResult(success: false) → AgentLoop generic fallback (PATH B).
    func testMalformedContractJSONReturnsStructuredRefusal() async throws {
        let (store, _) = try makeStore()
        let (handlers, _) = makeHandlers(store: store)
        let skill = ForgeOrchestrationSkill(handlers: handlers)
        let (specJSON, plansJSON, _) = makeValidSpec()

        let result = try await skill.execute(
            actionID: "forge.orchestration.propose",
            input: makeToolInput([
                "kind": "api",
                "spec_json": specJSON,
                "plans_json": plansJSON,
                "contract_json": "{ this is not json !!!",
                "summary": "Malformed contract test"
            ]),
            context: makeContext()
        )

        XCTAssertFalse(result.success, "Malformed contract_json must return success: false — not throw.")
        XCTAssertTrue(
            result.output.contains("contract_json") || result.output.contains("APIResearchContract"),
            "Output must reference the bad field. Got: \(result.output)"
        )
        let proposals = try await store.list()
        XCTAssertTrue(proposals.isEmpty, "No proposal must be created when contract_json is malformed.")
    }

    /// Malformed plans_json (not valid JSON) must return a structured refusal, not throw.
    func testMalformedPlansJSONReturnsStructuredRefusal() async throws {
        let (store, _) = try makeStore()
        let (handlers, _) = makeHandlers(store: store)
        let skill = ForgeOrchestrationSkill(handlers: handlers)
        let (specJSON, _, contractJSON) = makeValidSpec()

        let result = try await skill.execute(
            actionID: "forge.orchestration.propose",
            input: makeToolInput([
                "spec_json": specJSON,
                "plans_json": "not json at all ][",
                "contract_json": contractJSON,
                "summary": "Malformed plans test"
            ]),
            context: makeContext()
        )

        XCTAssertFalse(result.success, "Malformed plans_json must return success: false — not throw.")
        XCTAssertTrue(
            result.output.contains("plans_json") || result.output.contains("ForgeActionPlan"),
            "Output must reference the bad field. Got: \(result.output)"
        )
        let proposals = try await store.list()
        XCTAssertTrue(proposals.isEmpty, "No proposal must be created when plans_json is malformed.")
    }

    /// Missing spec_json must return a structured refusal, not throw.
    func testMissingSpecJSONReturnsStructuredRefusal() async throws {
        let (store, _) = try makeStore()
        let (handlers, _) = makeHandlers(store: store)
        let skill = ForgeOrchestrationSkill(handlers: handlers)
        let (_, plansJSON, contractJSON) = makeValidSpec()

        let result = try await skill.execute(
            actionID: "forge.orchestration.propose",
            input: makeToolInput([
                // spec_json intentionally omitted
                "plans_json": plansJSON,
                "contract_json": contractJSON,
                "summary": "Missing spec test"
            ]),
            context: makeContext()
        )

        XCTAssertFalse(result.success, "Missing spec_json must return success: false — not throw.")
        XCTAssertTrue(
            result.output.contains("spec_json"),
            "Output must mention spec_json. Got: \(result.output)"
        )
        let proposals = try await store.list()
        XCTAssertTrue(proposals.isEmpty, "No proposal must be created when spec_json is absent.")
    }

    /// Missing plans_json must return a structured refusal, not throw.
    func testMissingPlansJSONReturnsStructuredRefusal() async throws {
        let (store, _) = try makeStore()
        let (handlers, _) = makeHandlers(store: store)
        let skill = ForgeOrchestrationSkill(handlers: handlers)
        let (specJSON, _, contractJSON) = makeValidSpec()

        let result = try await skill.execute(
            actionID: "forge.orchestration.propose",
            input: makeToolInput([
                "spec_json": specJSON,
                // plans_json intentionally omitted
                "contract_json": contractJSON,
                "summary": "Missing plans test"
            ]),
            context: makeContext()
        )

        XCTAssertFalse(result.success, "Missing plans_json must return success: false — not throw.")
        XCTAssertTrue(
            result.output.contains("plans_json"),
            "Output must mention plans_json. Got: \(result.output)"
        )
        let proposals = try await store.list()
        XCTAssertTrue(proposals.isEmpty, "No proposal must be created when plans_json is absent.")
    }

    // MARK: - Gate Tests: boundary conditions

    /// docsQuality "medium" meets the minimum — gate 1 must pass.
    func testMediumDocsQualityPassesGate1() async throws {
        let (store, _) = try makeStore()
        let (handlers, _) = makeHandlers(store: store)
        let skill = ForgeOrchestrationSkill(handlers: handlers)
        let (specJSON, plansJSON, _) = makeValidSpec()

        let mediumDocsContract = makeContractJSON(
            docsQuality: "medium",  // gate 1: medium >= minimum ✓
            endpoint: "/status/{trackingNumber}",
            method: "GET",
            requiredParams: ["trackingNumber"],
            paramLocations: ["trackingNumber": "path"],
            mappingConfidence: "high"
        )

        let result = try await skill.execute(
            actionID: "forge.orchestration.propose",
            input: makeToolInput([
                "spec_json": specJSON,
                "plans_json": plansJSON,
                "contract_json": mediumDocsContract,
                "summary": "Medium docs should pass"
            ]),
            context: makeContext()
        )

        XCTAssertTrue(result.success, "docsQuality 'medium' must pass gate 1 and allow proposal creation.")
        let proposals = try await store.list()
        XCTAssertEqual(proposals.count, 1)
    }

    /// mappingConfidence "medium" fails gate 2 — must be refused.
    func testMediumMappingConfidenceIsRefused() async throws {
        let (store, _) = try makeStore()
        let (handlers, _) = makeHandlers(store: store)
        let skill = ForgeOrchestrationSkill(handlers: handlers)
        let (specJSON, plansJSON, _) = makeValidSpec()

        let mediumConfidenceContract = makeContractJSON(
            docsQuality: "high",
            endpoint: "/status/{trackingNumber}",
            method: "GET",
            requiredParams: ["trackingNumber"],
            paramLocations: ["trackingNumber": "path"],
            mappingConfidence: "medium"   // ← gate 2: not high → refuse
        )

        let result = try await skill.execute(
            actionID: "forge.orchestration.propose",
            input: makeToolInput([
                "spec_json": specJSON,
                "plans_json": plansJSON,
                "contract_json": mediumConfidenceContract,
                "summary": "Should be refused"
            ]),
            context: makeContext()
        )

        XCTAssertFalse(result.success, "mappingConfidence 'medium' must fail gate 2.")
        let proposals = try await store.list()
        XCTAssertTrue(proposals.isEmpty, "No proposal must be created with medium mapping confidence.")
    }

    /// Missing endpoint triggers a clarification request (gate 3).
    func testMissingEndpointReturnsClarification() async throws {
        let (store, _) = try makeStore()
        let (handlers, _) = makeHandlers(store: store)
        let skill = ForgeOrchestrationSkill(handlers: handlers)
        let (specJSON, plansJSON, _) = makeValidSpec()

        let noEndpointContract = makeContractJSON(
            docsQuality: "high",
            endpoint: nil,    // ← gate 3: missing → clarification
            method: "GET",
            mappingConfidence: "high"
        )

        let result = try await skill.execute(
            actionID: "forge.orchestration.propose",
            input: makeToolInput([
                "spec_json": specJSON,
                "plans_json": plansJSON,
                "contract_json": noEndpointContract,
                "summary": "Should ask for endpoint"
            ]),
            context: makeContext()
        )

        XCTAssertFalse(result.success, "Missing endpoint must not produce a proposal.")
        let proposals = try await store.list()
        XCTAssertTrue(proposals.isEmpty, "No proposal must be created without a defined endpoint.")
        XCTAssertTrue(
            result.output.lowercased().contains("endpoint") || result.output.lowercased().contains("path"),
            "Output must ask about the missing endpoint. Got: \(result.output)"
        )
    }

    /// Invalid HTTP method triggers a clarification request (gate 4).
    func testInvalidMethodReturnsClarification() async throws {
        let (store, _) = try makeStore()
        let (handlers, _) = makeHandlers(store: store)
        let skill = ForgeOrchestrationSkill(handlers: handlers)
        let (specJSON, plansJSON, _) = makeValidSpec()

        let badMethodContract = makeContractJSON(
            docsQuality: "high",
            endpoint: "/status/{id}",
            method: "FETCH",   // ← gate 4: not a known HTTP verb
            mappingConfidence: "high"
        )

        let result = try await skill.execute(
            actionID: "forge.orchestration.propose",
            input: makeToolInput([
                "spec_json": specJSON,
                "plans_json": plansJSON,
                "contract_json": badMethodContract,
                "summary": "Should ask for method"
            ]),
            context: makeContext()
        )

        XCTAssertFalse(result.success, "Unrecognised HTTP method must not produce a proposal.")
        let proposals = try await store.list()
        XCTAssertTrue(proposals.isEmpty, "No proposal must be created with an invalid HTTP method.")
        XCTAssertTrue(
            result.output.lowercased().contains("method") || result.output.lowercased().contains("http"),
            "Output must ask about the HTTP method. Got: \(result.output)"
        )
    }

    /// An explicit validationStatus "fail" in the contract is a hard refusal (gate 0).
    func testValidationStatusFailRefusesProposal() async throws {
        let (store, _) = try makeStore()
        let (handlers, _) = makeHandlers(store: store)
        let skill = ForgeOrchestrationSkill(handlers: handlers)
        let (specJSON, plansJSON, _) = makeValidSpec()

        let failedValidationContract = makeContractJSON(
            docsQuality: "high",
            endpoint: "/status/{trackingNumber}",
            method: "GET",
            requiredParams: ["trackingNumber"],
            paramLocations: ["trackingNumber": "path"],
            mappingConfidence: "high",
            validationStatus: "fail"   // ← gate 0: explicit failure → hard refuse
        )

        let result = try await skill.execute(
            actionID: "forge.orchestration.propose",
            input: makeToolInput([
                "spec_json": specJSON,
                "plans_json": plansJSON,
                "contract_json": failedValidationContract,
                "summary": "Should be refused despite high confidence"
            ]),
            context: makeContext()
        )

        XCTAssertFalse(result.success, "validationStatus 'fail' must cause a hard refusal.")
        let proposals = try await store.list()
        XCTAssertTrue(proposals.isEmpty, "No proposal must be created when validation explicitly failed.")
        XCTAssertTrue(
            result.output.lowercased().contains("validation") || result.output.lowercased().contains("failed"),
            "Output must explain the validation failure. Got: \(result.output)"
        )
    }

    /// Transform kind bypasses the API gate just like composed does.
    func testTransformSkillBypassesAPIContractGate() async throws {
        let (store, _) = try makeStore()
        let (handlers, _) = makeHandlers(store: store)
        let skill = ForgeOrchestrationSkill(handlers: handlers)
        let (specJSON, plansJSON, _) = makeValidSpec()

        let result = try await skill.execute(
            actionID: "forge.orchestration.propose",
            input: makeToolInput([
                "kind": "transform",   // ← bypasses API gate
                "spec_json": specJSON,
                "plans_json": plansJSON,
                "summary": "Transform skill — no API contract needed"
            ]),
            context: makeContext()
        )

        XCTAssertTrue(result.success, "Transform skill must succeed without contract_json.")
        let proposals = try await store.list()
        XCTAssertEqual(proposals.count, 1, "Proposal must be created for transform skill.")
    }

    // MARK: - contractJSON persistence

    /// Approved API proposal persists contractJSON and it survives a round-trip through SQLite.
    func testAPIProposalPersistsContractJSON() async throws {
        let (store, _) = try makeStore()
        let (handlers, _) = makeHandlers(store: store)
        let skill = ForgeOrchestrationSkill(handlers: handlers)
        let (specJSON, plansJSON, contractJSON) = makeValidSpec()

        _ = try await skill.execute(
            actionID: "forge.orchestration.propose",
            input: makeToolInput([
                "spec_json": specJSON,
                "plans_json": plansJSON,
                "contract_json": contractJSON,
                "summary": "Contract persistence test"
            ]),
            context: makeContext()
        )

        let proposals = try await store.list()
        XCTAssertEqual(proposals.count, 1)
        XCTAssertNotNil(proposals[0].contractJSON, "contractJSON must be persisted for API proposals.")

        // Verify the persisted JSON round-trips to the same contract
        let savedJSON = try XCTUnwrap(proposals[0].contractJSON)
        let decoded = try JSONDecoder().decode(APIResearchContract.self, from: Data(savedJSON.utf8))
        XCTAssertEqual(decoded.providerName, "Example Tracker API")
        XCTAssertEqual(decoded.mappingConfidence, .high)
        XCTAssertEqual(decoded.docsQuality, .high)
    }

    /// Non-API (transform) proposal has nil contractJSON — not stored.
    func testNonAPIProposalHasNilContractJSON() async throws {
        let (store, _) = try makeStore()
        let (handlers, _) = makeHandlers(store: store)
        let skill = ForgeOrchestrationSkill(handlers: handlers)
        let (specJSON, plansJSON, _) = makeValidSpec()

        _ = try await skill.execute(
            actionID: "forge.orchestration.propose",
            input: makeToolInput([
                "kind": "transform",
                "spec_json": specJSON,
                "plans_json": plansJSON,
                "summary": "Transform — no contract"
            ]),
            context: makeContext()
        )

        let proposals = try await store.list()
        XCTAssertEqual(proposals.count, 1)
        XCTAssertNil(proposals[0].contractJSON, "contractJSON must be nil for non-API proposals.")
    }

    // MARK: - AuthCore Gate Tests (Gate 6)

    /// OAuth 2.0 Authorization Code is refused — proposal must not be created.
    func testOAuth2AuthorizationCodeIsRefused() async throws {
        let (store, _) = try makeStore()
        let (handlers, _) = makeHandlers(store: store)
        let skill = ForgeOrchestrationSkill(handlers: handlers)
        let (specJSON, plansJSON, _) = makeValidSpec()

        let oauthContract = makeContractJSON(
            docsQuality: "high",
            endpoint: "/v1/auth/token",
            method: "POST",
            authType: "oauth2AuthorizationCode",
            mappingConfidence: "high"
        )

        let result = try await skill.execute(
            actionID: "forge.orchestration.propose",
            input: makeToolInput([
                "spec_json": specJSON,
                "plans_json": plansJSON,
                "contract_json": oauthContract,
                "summary": "Should be refused"
            ]),
            context: makeContext()
        )

        XCTAssertFalse(result.success, "OAuth2 auth must cause a hard refusal.")
        let proposals = try await store.list()
        XCTAssertTrue(proposals.isEmpty, "No proposal must be created for OAuth2 auth.")
        let output = result.output.lowercased()
        XCTAssertTrue(
            output.contains("oauth") || output.contains("authorization code"),
            "Refusal must explain the OAuth limitation. Got: \(result.output)"
        )
    }

    /// OAuth 2.0 Client Credentials is now SUPPORTED (AuthCore v2) — proposal must be created.
    func testOAuth2ClientCredentialsIsNowSupported() async throws {
        let (store, _) = try makeStore()
        let (handlers, _) = makeHandlers(store: store)
        let skill = ForgeOrchestrationSkill(handlers: handlers)
        let (specJSON, plansJSON, _) = makeValidSpec()

        let oauthContract = makeContractJSON(
            docsQuality: "high",
            endpoint: "/v1/token",
            method: "POST",
            authType: "oauth2ClientCredentials",
            mappingConfidence: "high"
        )

        let result = try await skill.execute(
            actionID: "forge.orchestration.propose",
            input: makeToolInput([
                "spec_json": specJSON,
                "plans_json": plansJSON,
                "contract_json": oauthContract,
                "summary": "OAuth2 CC skill"
            ]),
            context: makeContext()
        )

        // Gate 6 now passes for oauth2ClientCredentials — proposal must be created.
        XCTAssertTrue(result.success,
            "OAuth2 Client Credentials is supported in AuthCore v2 — proposal must not be refused. Got: \(result.output)")
        let proposals = try await store.list()
        XCTAssertFalse(proposals.isEmpty,
            "A proposal must have been stored for an oauth2ClientCredentials skill.")
    }

    /// Custom/proprietary auth is refused — proposal must not be created.
    func testCustomUnsupportedAuthIsRefused() async throws {
        let (store, _) = try makeStore()
        let (handlers, _) = makeHandlers(store: store)
        let skill = ForgeOrchestrationSkill(handlers: handlers)
        let (specJSON, plansJSON, _) = makeValidSpec()

        let customContract = makeContractJSON(
            docsQuality: "high",
            endpoint: "/v1/data",
            method: "GET",
            authType: "customUnsupported",
            mappingConfidence: "high"
        )

        let result = try await skill.execute(
            actionID: "forge.orchestration.propose",
            input: makeToolInput([
                "spec_json": specJSON,
                "plans_json": plansJSON,
                "contract_json": customContract,
                "summary": "Should be refused"
            ]),
            context: makeContext()
        )

        XCTAssertFalse(result.success, "Custom unsupported auth must cause a hard refusal.")
        let customAuthProposals = try await store.list()
        XCTAssertTrue(customAuthProposals.isEmpty)
        let lower = result.output.lowercased()
        XCTAssertTrue(
            lower.contains("custom") || lower.contains("proprietary") || lower.contains("unsupported"),
            "Refusal must explain the custom auth limitation. Got: \(result.output)"
        )
    }

    /// Unknown auth type is refused — research must complete before forging.
    func testUnknownAuthTypeIsRefused() async throws {
        let (store, _) = try makeStore()
        let (handlers, _) = makeHandlers(store: store)
        let skill = ForgeOrchestrationSkill(handlers: handlers)
        let (specJSON, plansJSON, _) = makeValidSpec()

        let unknownContract = makeContractJSON(
            docsQuality: "high",
            endpoint: "/v1/data",
            method: "GET",
            authType: "unknown",
            mappingConfidence: "high"
        )

        let result = try await skill.execute(
            actionID: "forge.orchestration.propose",
            input: makeToolInput([
                "spec_json": specJSON,
                "plans_json": plansJSON,
                "contract_json": unknownContract,
                "summary": "Should be refused"
            ]),
            context: makeContext()
        )

        XCTAssertFalse(result.success, "Unknown auth must cause a hard refusal.")
        let unknownAuthProposals = try await store.list()
        XCTAssertTrue(unknownAuthProposals.isEmpty)
        let lower = result.output.lowercased()
        XCTAssertTrue(
            lower.contains("unknown") || lower.contains("identified") || lower.contains("research"),
            "Refusal must instruct the agent to identify the auth type. Got: \(result.output)"
        )
    }

    /// apiKeyHeader auth is supported — proposal must be created successfully.
    func testAPIKeyHeaderAuthAllowsProposal() async throws {
        let (store, _) = try makeStore()
        let (handlers, _) = makeHandlers(store: store)
        let skill = ForgeOrchestrationSkill(handlers: handlers)
        let (specJSON, plansJSON, _) = makeValidSpec()

        let contract = makeContractJSON(
            docsQuality: "high",
            endpoint: "/status/{trackingNumber}",
            method: "GET",
            authType: "apiKeyHeader",
            requiredParams: ["trackingNumber"],
            paramLocations: ["trackingNumber": "path"],
            mappingConfidence: "high"
        )

        let result = try await skill.execute(
            actionID: "forge.orchestration.propose",
            input: makeToolInput([
                "spec_json": specJSON,
                "plans_json": plansJSON,
                "contract_json": contract,
                "summary": "API key header auth skill"
            ]),
            context: makeContext()
        )

        XCTAssertTrue(result.success, "apiKeyHeader is a supported auth type — proposal must be created.")
        let apiKeyHeaderProposals = try await store.list()
        XCTAssertEqual(apiKeyHeaderProposals.count, 1)
    }

    /// apiKeyQuery auth is supported — proposal must be created successfully.
    func testAPIKeyQueryAuthAllowsProposal() async throws {
        let (store, _) = try makeStore()
        let (handlers, _) = makeHandlers(store: store)
        let skill = ForgeOrchestrationSkill(handlers: handlers)
        let (specJSON, plansJSON, _) = makeValidSpec()

        let contract = makeContractJSON(
            docsQuality: "high",
            endpoint: "/status/{trackingNumber}",
            method: "GET",
            authType: "apiKeyQuery",
            requiredParams: ["trackingNumber"],
            paramLocations: ["trackingNumber": "path"],
            mappingConfidence: "high"
        )

        let result = try await skill.execute(
            actionID: "forge.orchestration.propose",
            input: makeToolInput([
                "spec_json": specJSON,
                "plans_json": plansJSON,
                "contract_json": contract,
                "summary": "API key query auth skill"
            ]),
            context: makeContext()
        )

        XCTAssertTrue(result.success, "apiKeyQuery is a supported auth type — proposal must be created.")
        let apiKeyQueryProposals = try await store.list()
        XCTAssertEqual(apiKeyQueryProposals.count, 1)
    }

    /// bearerTokenStatic auth is supported — proposal must be created successfully.
    func testBearerTokenStaticAuthAllowsProposal() async throws {
        let (store, _) = try makeStore()
        let (handlers, _) = makeHandlers(store: store)
        let skill = ForgeOrchestrationSkill(handlers: handlers)
        let (specJSON, plansJSON, _) = makeValidSpec()

        let contract = makeContractJSON(
            docsQuality: "high",
            endpoint: "/status/{trackingNumber}",
            method: "GET",
            authType: "bearerTokenStatic",
            requiredParams: ["trackingNumber"],
            paramLocations: ["trackingNumber": "path"],
            mappingConfidence: "high"
        )

        let result = try await skill.execute(
            actionID: "forge.orchestration.propose",
            input: makeToolInput([
                "spec_json": specJSON,
                "plans_json": plansJSON,
                "contract_json": contract,
                "summary": "Bearer token static auth skill"
            ]),
            context: makeContext()
        )

        XCTAssertTrue(result.success, "bearerTokenStatic is a supported auth type — proposal must be created.")
        let bearerProposals = try await store.list()
        XCTAssertEqual(bearerProposals.count, 1)
    }

    /// basicAuth is supported — proposal must be created successfully.
    func testBasicAuthAllowsProposal() async throws {
        let (store, _) = try makeStore()
        let (handlers, _) = makeHandlers(store: store)
        let skill = ForgeOrchestrationSkill(handlers: handlers)
        let (specJSON, plansJSON, _) = makeValidSpec()

        let contract = makeContractJSON(
            docsQuality: "high",
            endpoint: "/status/{trackingNumber}",
            method: "GET",
            authType: "basicAuth",
            requiredParams: ["trackingNumber"],
            paramLocations: ["trackingNumber": "path"],
            mappingConfidence: "high"
        )

        let result = try await skill.execute(
            actionID: "forge.orchestration.propose",
            input: makeToolInput([
                "spec_json": specJSON,
                "plans_json": plansJSON,
                "contract_json": contract,
                "summary": "Basic auth skill"
            ]),
            context: makeContext()
        )

        XCTAssertTrue(result.success, "basicAuth is a supported auth type — proposal must be created.")
        let basicAuthProposals = try await store.list()
        XCTAssertEqual(basicAuthProposals.count, 1)
    }

    /// Public/no-auth APIs (authType == none) still work — existing behaviour preserved.
    func testNoAuthPublicAPIStillWorks() async throws {
        let (store, _) = try makeStore()
        let (handlers, _) = makeHandlers(store: store)
        let skill = ForgeOrchestrationSkill(handlers: handlers)
        let (specJSON, plansJSON, contractJSON) = makeValidSpec() // default authType: "none"

        let result = try await skill.execute(
            actionID: "forge.orchestration.propose",
            input: makeToolInput([
                "spec_json": specJSON,
                "plans_json": plansJSON,
                "contract_json": contractJSON,
                "summary": "Public API — no auth"
            ]),
            context: makeContext()
        )

        XCTAssertTrue(result.success, "A public API with no auth must still produce a proposal.")
        let noAuthProposals = try await store.list()
        XCTAssertEqual(noAuthProposals.count, 1)
    }

    /// A contract without an authType field is allowed through the gate (backward compat).
    func testNilAuthTypeInContractIsPermitted() async throws {
        let (store, _) = try makeStore()
        let (handlers, _) = makeHandlers(store: store)
        let skill = ForgeOrchestrationSkill(handlers: handlers)
        let (specJSON, plansJSON, _) = makeValidSpec()

        // Build contract with no authType key at all
        let contractWithoutAuth = makeContractJSON(authType: nil)

        let result = try await skill.execute(
            actionID: "forge.orchestration.propose",
            input: makeToolInput([
                "spec_json": specJSON,
                "plans_json": plansJSON,
                "contract_json": contractWithoutAuth,
                "summary": "Legacy contract without authType"
            ]),
            context: makeContext()
        )

        XCTAssertTrue(result.success,
            "A contract without authType must be allowed through for backward compatibility.")
        let nilAuthProposals = try await store.list()
        XCTAssertEqual(nilAuthProposals.count, 1,
            "A contract without authType must still produce a proposal.")
    }

    /// OAuth refusal output must direct the user toward supported alternatives.
    func testOAuthRefusalMessageMentionsAlternatives() async throws {
        let (store, _) = try makeStore()
        let (handlers, _) = makeHandlers(store: store)
        let skill = ForgeOrchestrationSkill(handlers: handlers)
        let (specJSON, plansJSON, _) = makeValidSpec()

        let oauthContract = makeContractJSON(
            docsQuality: "high",
            endpoint: "/v1/resource",
            method: "GET",
            authType: "oauth2AuthorizationCode",
            mappingConfidence: "high"
        )

        let result = try await skill.execute(
            actionID: "forge.orchestration.propose",
            input: makeToolInput([
                "spec_json": specJSON,
                "plans_json": plansJSON,
                "contract_json": oauthContract,
                "summary": "OAuth API"
            ]),
            context: makeContext()
        )

        XCTAssertFalse(result.success)
        // Must mention a supported alternative (API key or Bearer token)
        let lower = result.output.lowercased()
        XCTAssertTrue(
            lower.contains("apikey") || lower.contains("api key") ||
            lower.contains("bearer") || lower.contains("token") || lower.contains("static"),
            "OAuth refusal must suggest a supported alternative auth approach. Got: \(result.output)"
        )
    }

    // MARK: - Gate 7: Auth Plan Field Completeness (Integration)

    /// Helper: build a skill with Gate 7/8 and dry-run injection, using a controlled
    /// secrets reader and HTTP executor.
    private func makeGatedSkill(
        store: ForgeProposalStore,
        secretsReader: @escaping CoreSecretsService.SecretsReader = { _ in nil },
        httpExecutor: ForgeDryRunValidator.HTTPExecutor? = nil
    ) -> (ForgeOrchestrationSkill, ForgeProposalService) {
        let service = ForgeProposalService(store: store)
        let registry = SkillRegistry(
            defaults: UserDefaults(suiteName: "GatedTests.\(UUID().uuidString)")!
        )
        let coreSkills = CoreSkillsRuntime(
            registry: registry,
            secretsReader: secretsReader
        )
        Task { await service.configure(coreSkills: coreSkills, skillRegistry: registry) }

        let handlers = ForgeOrchestrationHandlers(
            startResearching: { title, message in
                await service.startResearching(title: title, message: message).id
            },
            stopResearching: { id in await service.stopResearching(id: id) },
            createProposal: { spec, plans, summary, rationale, contractJSON in
                try await service.createProposal(
                    spec: spec, plans: plans,
                    summary: summary, rationale: rationale, contractJSON: contractJSON
                )
            }
        )

        let dryRunValidator: ForgeDryRunValidator? = httpExecutor.map { executor in
            ForgeDryRunValidator(secretsService: coreSkills.secrets, executor: executor)
        }

        let skill = ForgeOrchestrationSkill(
            handlers: handlers,
            secretsService: coreSkills.secrets,
            dryRunValidator: dryRunValidator
        )
        return (skill, service)
    }

    /// Plans JSON with apiKeyHeader auth but missing authHeaderName (triggers Gate 7).
    private func makePlansWithMissingHeaderName() -> String {
        #"""
        [{"actionID":"test-tracker.get-status","type":"http","httpRequest":{"method":"GET","url":"https://api.example-tracker.com/status/{trackingNumber}","authType":"apiKeyHeader","authSecretKey":"com.projectatlas.tracker","authHeaderName":null}}]
        """#
    }

    /// Plans JSON with apiKeyQuery auth but missing authQueryParamName (triggers Gate 7).
    private func makePlansWithMissingQueryParamName() -> String {
        #"""
        [{"actionID":"test-tracker.get-status","type":"http","httpRequest":{"method":"GET","url":"https://api.example-tracker.com/status","authType":"apiKeyQuery","authSecretKey":"com.projectatlas.tracker","authQueryParamName":null}}]
        """#
    }

    /// Plans JSON with complete apiKeyHeader auth fields.
    private func makePlansWithCompleteAPIKeyHeader(secretKey: String = "com.projectatlas.tracker") -> String {
        """
        [{"actionID":"test-tracker.get-status","type":"http","httpRequest":{"method":"GET","url":"https://api.example-tracker.com/status/{trackingNumber}","authType":"apiKeyHeader","authSecretKey":"\(secretKey)","authHeaderName":"X-API-Key"}}]
        """
    }

    /// Contract JSON that passes all gates 1–6 and includes an authType that requires credential.
    private func makeContractWithAPIKeyHeader() -> String {
        makeContractJSON(
            docsQuality: "high",
            endpoint: "/status/{trackingNumber}",
            method: "GET",
            authType: "apiKeyHeader",
            requiredParams: ["trackingNumber"],
            paramLocations: ["trackingNumber": "path"],
            mappingConfidence: "high"
        )
    }

    /// Gate 7: missing authHeaderName in plan blocks proposal — clarification returned.
    func testMissingAuthHeaderNameBlocksProposal() async throws {
        let (store, _) = try makeStore()
        let (skill, _) = makeGatedSkill(store: store)
        let (specJSON, _, _) = makeValidSpec()

        let result = try await skill.execute(
            actionID: "forge.orchestration.propose",
            input: makeToolInput([
                "spec_json": specJSON,
                "plans_json": makePlansWithMissingHeaderName(),
                "contract_json": makeContractWithAPIKeyHeader(),
                "summary": "API key header skill — missing header name"
            ]),
            context: makeContext()
        )

        XCTAssertFalse(result.success, "Missing authHeaderName must block proposal creation.")
        let proposals = try await store.list()
        XCTAssertTrue(proposals.isEmpty, "No proposal must be created when Gate 7 fires.")
        XCTAssertTrue(
            result.output.lowercased().contains("authheadername") ||
            result.output.lowercased().contains("header name") ||
            result.output.lowercased().contains("incomplete"),
            "Output must explain the missing field. Got: \(result.output)"
        )
    }

    /// Gate 7: missing authQueryParamName in plan blocks proposal — clarification returned.
    func testMissingAuthQueryParamNameBlocksProposal() async throws {
        let (store, _) = try makeStore()
        let (skill, _) = makeGatedSkill(store: store)
        let (specJSON, _, _) = makeValidSpec()

        let contract = makeContractJSON(
            docsQuality: "high",
            endpoint: "/status",
            method: "GET",
            authType: "apiKeyQuery",
            mappingConfidence: "high"
        )

        let result = try await skill.execute(
            actionID: "forge.orchestration.propose",
            input: makeToolInput([
                "spec_json": specJSON,
                "plans_json": makePlansWithMissingQueryParamName(),
                "contract_json": contract,
                "summary": "API key query skill — missing param name"
            ]),
            context: makeContext()
        )

        XCTAssertFalse(result.success, "Missing authQueryParamName must block proposal creation.")
        let proposals = try await store.list()
        XCTAssertTrue(proposals.isEmpty, "No proposal must be created when Gate 7 fires.")
        XCTAssertTrue(
            result.output.lowercased().contains("authqueryparamname") ||
            result.output.lowercased().contains("query param") ||
            result.output.lowercased().contains("incomplete"),
            "Output must explain the missing field. Got: \(result.output)"
        )
    }

    // MARK: - Gate 8: Credential Readiness (Integration)

    /// Gate 8: missing Keychain credential blocks proposal — clarification returned.
    func testMissingCredentialBlocksProposal() async throws {
        let (store, _) = try makeStore()
        // secretsReader returns nil → credential not configured
        let (skill, _) = makeGatedSkill(store: store, secretsReader: { _ in nil })
        let (specJSON, _, _) = makeValidSpec()

        let result = try await skill.execute(
            actionID: "forge.orchestration.propose",
            input: makeToolInput([
                "spec_json": specJSON,
                "plans_json": makePlansWithCompleteAPIKeyHeader(),
                "contract_json": makeContractWithAPIKeyHeader(),
                "summary": "Complete plan but credential missing"
            ]),
            context: makeContext()
        )

        XCTAssertFalse(result.success, "Missing credential must block proposal creation.")
        let proposals = try await store.list()
        XCTAssertTrue(proposals.isEmpty, "No proposal must be created when Gate 8 fires.")
        let lower = result.output.lowercased()
        XCTAssertTrue(
            lower.contains("credential") || lower.contains("keychain") || lower.contains("api key"),
            "Output must explain the credential requirement. Got: \(result.output)"
        )
    }

    /// Gate 8: present credential allows proposal to proceed past Gate 8.
    /// (Dry-run skipped since no httpExecutor injected — proposal is created.)
    func testPresentCredentialPassesGate8() async throws {
        let (store, _) = try makeStore()
        // secretsReader returns a value → credential present
        let (skill, _) = makeGatedSkill(
            store: store,
            secretsReader: { _ in "my-secret-api-key" }
            // no httpExecutor → dry-run skipped
        )
        let (specJSON, _, _) = makeValidSpec()

        let result = try await skill.execute(
            actionID: "forge.orchestration.propose",
            input: makeToolInput([
                "spec_json": specJSON,
                "plans_json": makePlansWithCompleteAPIKeyHeader(),
                "contract_json": makeContractWithAPIKeyHeader(),
                "summary": "Complete plan with credential present"
            ]),
            context: makeContext()
        )

        XCTAssertTrue(result.success,
            "Complete plan with present credential must create a proposal. Got: \(result.output)")
        let proposals = try await store.list()
        XCTAssertEqual(proposals.count, 1, "Exactly one proposal must be created.")
    }

    // MARK: - Dry-Run Integration

    /// Dry-run failure (500) blocks proposal — no proposal persisted.
    func testDryRunFailureBlocksProposal() async throws {
        let (store, _) = try makeStore()
        let mockResponse = CoreHTTPResponse(
            statusCode: 500,
            headers: [:],
            body: Data(),
            url: URL(string: "https://api.example-tracker.com")!
        )
        let (skill, _) = makeGatedSkill(
            store: store,
            secretsReader: { _ in "my-secret-api-key" },
            httpExecutor: { _ in mockResponse }   // executor returns 500
        )
        let (specJSON, _, _) = makeValidSpec()

        let result = try await skill.execute(
            actionID: "forge.orchestration.propose",
            input: makeToolInput([
                "spec_json": specJSON,
                "plans_json": makePlansWithCompleteAPIKeyHeader(),
                "contract_json": makeContractWithAPIKeyHeader(),
                "summary": "API with dry-run failure"
            ]),
            context: makeContext()
        )

        XCTAssertFalse(result.success, "Dry-run failure must block proposal creation.")
        let proposals = try await store.list()
        XCTAssertTrue(proposals.isEmpty, "No proposal must be created when dry-run fails.")
        let lower = result.output.lowercased()
        XCTAssertTrue(
            lower.contains("invalid") || lower.contains("unreachable") || lower.contains("500"),
            "Output must explain the dry-run failure. Got: \(result.output)"
        )
    }

    /// Dry-run success (200) allows proposal to be created.
    func testDryRunSuccessAllowsProposal() async throws {
        let (store, _) = try makeStore()
        let mockResponse = CoreHTTPResponse(
            statusCode: 200,
            headers: [:],
            body: Data("{\"status\":\"ok\"}".utf8),
            url: URL(string: "https://api.example-tracker.com")!
        )
        let (skill, _) = makeGatedSkill(
            store: store,
            secretsReader: { _ in "my-secret-api-key" },
            httpExecutor: { _ in mockResponse }   // executor returns 200
        )
        let (specJSON, _, _) = makeValidSpec()

        let result = try await skill.execute(
            actionID: "forge.orchestration.propose",
            input: makeToolInput([
                "spec_json": specJSON,
                "plans_json": makePlansWithCompleteAPIKeyHeader(),
                "contract_json": makeContractWithAPIKeyHeader(),
                "summary": "API passing dry-run"
            ]),
            context: makeContext()
        )

        XCTAssertTrue(result.success,
            "Dry-run pass must allow proposal creation. Got: \(result.output)")
        let proposals = try await store.list()
        XCTAssertEqual(proposals.count, 1, "Exactly one proposal must be created after dry-run success.")
    }

    /// Researching state is NOT started when Gate 7 fires (no orphaned state).
    func testResearchingStateNotStartedOnGate7Failure() async throws {
        let (store, _) = try makeStore()
        let (skill, service) = makeGatedSkill(store: store)
        let (specJSON, _, _) = makeValidSpec()

        _ = try await skill.execute(
            actionID: "forge.orchestration.propose",
            input: makeToolInput([
                "spec_json": specJSON,
                "plans_json": makePlansWithMissingHeaderName(),
                "contract_json": makeContractWithAPIKeyHeader(),
                "summary": "Gate 7 should fire before researching starts"
            ]),
            context: makeContext()
        )

        let researching = await service.listResearching()
        XCTAssertTrue(researching.isEmpty,
            "No researching state must be created when Gate 7 blocks the proposal.")
    }

    /// Researching state is NOT started when Gate 8 fires (no orphaned state).
    func testResearchingStateNotStartedOnGate8Failure() async throws {
        let (store, _) = try makeStore()
        let (skill, service) = makeGatedSkill(store: store, secretsReader: { _ in nil })
        let (specJSON, _, _) = makeValidSpec()

        _ = try await skill.execute(
            actionID: "forge.orchestration.propose",
            input: makeToolInput([
                "spec_json": specJSON,
                "plans_json": makePlansWithCompleteAPIKeyHeader(),
                "contract_json": makeContractWithAPIKeyHeader(),
                "summary": "Gate 8 should fire before researching starts"
            ]),
            context: makeContext()
        )

        let researching = await service.listResearching()
        XCTAssertTrue(researching.isEmpty,
            "No researching state must be created when Gate 8 blocks the proposal.")
    }

    /// Researching state is NOT started when dry-run fails (no orphaned state).
    func testResearchingStateNotStartedOnDryRunFailure() async throws {
        let (store, _) = try makeStore()
        let mockFail = CoreHTTPResponse(
            statusCode: 500, headers: [:], body: Data(),
            url: URL(string: "https://api.example-tracker.com")!
        )
        let (skill, service) = makeGatedSkill(
            store: store,
            secretsReader: { _ in "key" },
            httpExecutor: { _ in mockFail }
        )
        let (specJSON, _, _) = makeValidSpec()

        _ = try await skill.execute(
            actionID: "forge.orchestration.propose",
            input: makeToolInput([
                "spec_json": specJSON,
                "plans_json": makePlansWithCompleteAPIKeyHeader(),
                "contract_json": makeContractWithAPIKeyHeader(),
                "summary": "Dry-run should fire before researching starts"
            ]),
            context: makeContext()
        )

        let researching = await service.listResearching()
        XCTAssertTrue(researching.isEmpty,
            "No researching state must be created when dry-run blocks the proposal.")
    }
}

// MARK: - Test Helpers

/// Concurrency-safe box for capturing a UUID produced inside a @Sendable closure.
private actor ResearchingIDBox {
    private(set) var value: UUID?
    func set(_ id: UUID) { value = id }
}
