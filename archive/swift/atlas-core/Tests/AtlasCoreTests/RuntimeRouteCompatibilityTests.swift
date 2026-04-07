import XCTest
import AtlasLogging
import AtlasMemory
import AtlasNetwork
import AtlasShared
import AtlasSkills
@testable import AtlasCore

final class RuntimeRouteCompatibilityTests: XCTestCase {
    private var originalSharedStore: AtlasConfigStore?
    private var temporaryRoot: URL!

    override func setUp() {
        super.setUp()
        originalSharedStore = AtlasConfigStore.shared
        temporaryRoot = FileManager.default.temporaryDirectory
            .appendingPathComponent("AtlasRuntimeRouteCompatibility-\(UUID().uuidString)", isDirectory: true)
        try? FileManager.default.createDirectory(at: temporaryRoot, withIntermediateDirectories: true)
        AtlasConfigStore.shared = AtlasConfigStore(pathProvider: TestPathProvider(root: temporaryRoot))
        AtlasConfig.seedSnapshot(.defaults)
    }

    override func tearDown() {
        if let originalSharedStore {
            AtlasConfigStore.shared = originalSharedStore
        }
        AtlasConfig.seedSnapshot(.defaults)
        if let temporaryRoot {
            try? FileManager.default.removeItem(at: temporaryRoot)
        }
        super.tearDown()
    }

    func testOnboardingRouteMethodsRoundTripSharedState() async throws {
        let runtime = try makeRuntime()

        let initial = await runtime.onboardingStatus()
        XCTAssertFalse(initial.completed)

        let updated = try await runtime.setOnboardingCompleted(true)
        XCTAssertTrue(updated.completed)

        let persisted = await runtime.actionConfig()
        XCTAssertTrue(persisted.onboardingCompleted)
    }

    func testUpdateConfigMatchesRouteSemanticsForRestartDetection() async throws {
        let runtime = try makeRuntime(snapshot: RuntimeConfigSnapshot(runtimePort: 1984, remoteAccessEnabled: false))

        var noRestartSnapshot = await runtime.actionConfig()
        noRestartSnapshot.personaName = "Navigator"
        let noRestartResult = try await runtime.updateConfig(noRestartSnapshot)
        XCTAssertFalse(noRestartResult.restartRequired)
        XCTAssertEqual(noRestartResult.snapshot.personaName, "Navigator")

        var restartSnapshot = noRestartResult.snapshot
        restartSnapshot.runtimePort = 9091
        restartSnapshot.remoteAccessEnabled = true
        let restartResult = try await runtime.updateConfig(restartSnapshot)
        XCTAssertTrue(restartResult.restartRequired)
        XCTAssertEqual(restartResult.snapshot.runtimePort, 9091)
        XCTAssertTrue(restartResult.snapshot.remoteAccessEnabled)
    }

    func testRemoteAccessStatusReflectsConfigBackedRouteState() async throws {
        let runtime = try makeRuntime(snapshot: RuntimeConfigSnapshot(runtimePort: 9191, remoteAccessEnabled: true))

        let status = await runtime.remoteAccessStatus()
        XCTAssertTrue(status.remoteAccessEnabled)
        XCTAssertEqual(status.port, 9191)
    }

    func testAuthBootstrapMethodsCreateValidSession() async throws {
        let runtime = try makeRuntime()

        let token = await runtime.issueWebLaunchToken()
        XCTAssertFalse(token.isEmpty)

        let (sessionID, cookieHeader) = try await runtime.bootstrapWebSession(token: token)
        let isValid = await runtime.validateWebSession(id: sessionID)
        XCTAssertFalse(sessionID.isEmpty)
        XCTAssertTrue(cookieHeader.contains(sessionID))
        XCTAssertTrue(isValid)
    }

    func testRemoteAuthenticationMatchesConfiguredAPIKey() async throws {
        let runtime = try makeRuntime(
            snapshot: RuntimeConfigSnapshot(runtimePort: 1984, remoteAccessEnabled: true),
            credentialBundle: AtlasCredentialBundle(remoteAccessAPIKey: "atlas-secret")
        )

        let denied = await runtime.authenticateRemoteAPIKey("wrong-secret")
        XCTAssertNil(denied)

        let cookieHeader = await runtime.authenticateRemoteAPIKey("atlas-secret")
        XCTAssertNotNil(cookieHeader)

        let sessionID = WebAuthService.sessionID(fromCookieHeader: cookieHeader)
        let isValid = await runtime.validateWebSession(id: sessionID)
        XCTAssertTrue(isValid)
    }

    func testAPIKeyStatusReflectsCredentialBundleWithoutNativeSecretWrites() async throws {
        let runtime = try makeRuntime(
            credentialBundle: AtlasCredentialBundle(
                openAIAPIKey: "sk-openai",
                telegramBotToken: "tg-token",
                discordBotToken: "discord-token",
                braveSearchAPIKey: "brave-token",
                anthropicAPIKey: "anthropic-token"
            )
        )

        let status = await runtime.apiKeyStatus()
        XCTAssertTrue(status.openAIKeySet)
        XCTAssertTrue(status.telegramTokenSet)
        XCTAssertTrue(status.discordTokenSet)
        XCTAssertTrue(status.braveSearchKeySet)
        XCTAssertTrue(status.anthropicKeySet)
        XCTAssertTrue(status.customKeys.isEmpty)
        XCTAssertFalse(status.slackBotTokenSet)
    }

    func testCommunicationSetupValuesReflectConfigAndCredentialState() async throws {
        let snapshot = RuntimeConfigSnapshot(
            discordClientID: "discord-client-id",
            telegramCommandPrefix: "!",
            telegramAllowedUserIDs: [42],
            telegramAllowedChatIDs: [84]
        )
        let runtime = try makeRuntime(
            snapshot: snapshot,
            credentialBundle: AtlasCredentialBundle(
                telegramBotToken: "telegram-secret",
                discordBotToken: "discord-secret",
                slackBotToken: "slack-bot-secret",
                slackAppToken: "slack-app-secret"
            )
        )

        let telegramValues = await runtime.communicationSetupValues(for: .telegram)
        XCTAssertEqual(telegramValues["telegram"], "telegram-secret")
        XCTAssertEqual(telegramValues.count, 1)

        let discordValues = await runtime.communicationSetupValues(for: .discord)
        XCTAssertEqual(discordValues["discord"], "discord-secret")
        XCTAssertEqual(discordValues["discordClientID"], "discord-client-id")

        let slackValues = await runtime.communicationSetupValues(for: .slack)
        XCTAssertEqual(slackValues["slackBot"], "slack-bot-secret")
        XCTAssertEqual(slackValues["slackApp"], "slack-app-secret")
    }

    func testValidateCommunicationPlatformOverridesDoNotPersistSharedConfig() async throws {
        let runtime = try await makeStartedRuntime(snapshot: RuntimeConfigSnapshot(
            runtimePort: availableTestPort(),
            discordEnabled: false,
            discordClientID: ""
        ))

        let status = try await runtime.validateCommunicationPlatform(
            .discord,
            credentialOverrides: [:],
            configOverrides: CommunicationValidationConfigOverrides(discordClientID: "override-client-id")
        )

        XCTAssertEqual(status.platform, .discord)
        XCTAssertTrue(status.enabled)
        XCTAssertFalse(status.connected)
        XCTAssertEqual(status.setupState, .missingCredentials)
        XCTAssertFalse(status.credentialConfigured)
        XCTAssertEqual(status.metadata["clientIDConfigured"], "true")

        let persisted = await AtlasConfigStore.shared.load()
        XCTAssertFalse(persisted.discordEnabled)
        XCTAssertEqual(persisted.discordClientID, "")

        try await runtime.stop()
    }

    func testUpdateCommunicationPlatformPersistsEnablementAndReturnsStatus() async throws {
        let runtime = try await makeStartedRuntime(snapshot: RuntimeConfigSnapshot(
            runtimePort: availableTestPort(),
            discordEnabled: false,
            discordClientID: ""
        ))

        let enabledStatus = try await runtime.updateCommunicationPlatform(platform: .discord, enabled: true)
        XCTAssertEqual(enabledStatus.platform, .discord)
        XCTAssertTrue(enabledStatus.enabled)
        XCTAssertFalse(enabledStatus.connected)
        XCTAssertEqual(enabledStatus.setupState, .missingCredentials)

        let enabledSnapshot = await AtlasConfigStore.shared.load()
        XCTAssertTrue(enabledSnapshot.discordEnabled)

        let disabledStatus = try await runtime.updateCommunicationPlatform(platform: .discord, enabled: false)
        XCTAssertEqual(disabledStatus.platform, .discord)
        XCTAssertFalse(disabledStatus.enabled)
        XCTAssertFalse(disabledStatus.connected)

        let disabledSnapshot = await AtlasConfigStore.shared.load()
        XCTAssertFalse(disabledSnapshot.discordEnabled)

        try await runtime.stop()
    }

    func testConversationHistoryMethodsExposeStoredConversationShape() async throws {
        let runtime = try makeRuntime()
        let context = runtimeContext(runtime)
        let conversationID = UUID()
        let createdAt = Date(timeIntervalSince1970: 1_700_000_000)
        _ = try await context.memoryStore.createConversation(
            id: conversationID,
            createdAt: createdAt,
            platformContext: "companion"
        )
        _ = try await context.conversationStore.appendMessage(
            AtlasMessage(
                role: .user,
                content: "Find my latest draft",
                timestamp: createdAt
            ),
            to: conversationID
        )
        _ = try await context.conversationStore.appendMessage(
            AtlasMessage(
                role: .assistant,
                content: "I found the latest draft in Notes.",
                timestamp: createdAt.addingTimeInterval(1)
            ),
            to: conversationID
        )

        let summaries = await runtime.conversationSummaries(limit: 10, offset: 0)
        XCTAssertEqual(summaries.count, 1)
        XCTAssertEqual(summaries.first?.id, conversationID)
        XCTAssertEqual(summaries.first?.platformContext, "companion")

        let detail = await runtime.conversationDetail(id: conversationID)
        XCTAssertEqual(detail?.id, conversationID)
        XCTAssertEqual(detail?.messages.count, 2)
        XCTAssertEqual(detail?.lastAssistantMessage, "I found the latest draft in Notes.")

        let searchResults = await runtime.searchConversations(query: "latest draft", limit: 10)
        XCTAssertEqual(searchResults.first?.id, conversationID)
    }

    func testHandleMessageFailurePreservesConversationIDAndEmitsFallbackStreamLifecycle() async throws {
        let runtime = try makeRuntime()
        let conversationID = UUID()
        let stream = await runtime.streamBroadcaster.register(conversationID: conversationID)
        let collectorTask = Task { () -> [SSEEvent] in
            var events: [SSEEvent] = []
            for await event in stream {
                events.append(event)
            }
            return events
        }

        let response = await runtime.handleMessage(
            AtlasMessageRequest(conversationID: conversationID, message: "Hello from the web UI")
        )

        XCTAssertEqual(response.conversation.id, conversationID)
        XCTAssertEqual(response.response.status, .failed)
        XCTAssertFalse(response.response.assistantMessage.isEmpty)

        let events = await collectorTask.value
        XCTAssertEqual(events.map(\.type), [.assistantStarted, .assistantDelta, .assistantDone, .done])
        XCTAssertEqual(events.last?.status, "failed")
        XCTAssertEqual(events[1].content, response.response.assistantMessage)

        let detail = await runtime.conversationDetail(id: conversationID)
        XCTAssertEqual(detail?.id, conversationID)
        XCTAssertEqual(detail?.messages.last?.role, .assistant)
        XCTAssertEqual(detail?.messages.last?.content, response.response.assistantMessage)
    }

    func testHandleMessageWithoutConversationIDCreatesReusableConversation() async throws {
        let runtime = try makeRuntime()

        let response = await runtime.handleMessage(
            AtlasMessageRequest(message: "Start a new conversation for me")
        )

        XCTAssertEqual(response.response.status, .failed)
        XCTAssertFalse(response.conversation.id.uuidString.isEmpty)

        let detail = await runtime.conversationDetail(id: response.conversation.id)
        XCTAssertEqual(detail?.id, response.conversation.id)
        XCTAssertEqual(detail?.messages.count, 2)
        XCTAssertEqual(detail?.messages.first?.role, .user)
        XCTAssertEqual(detail?.messages.first?.content, "Start a new conversation for me")
        XCTAssertEqual(detail?.messages.last?.role, .assistant)
        XCTAssertEqual(detail?.messages.last?.content, response.response.assistantMessage)
    }

    func testMindContentRoundTripsThroughRuntimeBoundary() async throws {
        let runtime = try await makeDocumentRuntime()
        let initialContent = await runtime.mindContent()

        XCTAssertEqual(initialContent, validMindContent(read: "Seeded and ready."))

        let updatedContent = validMindContent(read: "Focused and steady.")
        try await runtime.updateMindContent(updatedContent)
        let persistedContent = await runtime.mindContent()

        XCTAssertEqual(persistedContent, updatedContent)
    }

    func testSkillsMemoryRoundTripsThroughRuntimeBoundary() async throws {
        let runtime = try await makeDocumentRuntime()
        let initialContent = await runtime.skillsMemoryContent()

        XCTAssertEqual(initialContent, "# Seeded Skills")

        let updatedContent = """
        # Skill Memory

        ## Learned Routines

        ### Daily Brief
        **Triggers:** morning brief
        **Steps:**
        1. WeatherSkill -> forecast
        """
        try await runtime.updateSkillsMemory(updatedContent)
        let persistedContent = await runtime.skillsMemoryContent()

        XCTAssertEqual(persistedContent, updatedContent)
    }

    func testCreateMemoryNormalizesAndPersistsRouteShape() async throws {
        let runtime = try makeRuntime()

        let saved = try await runtime.createMemory(
            request: AtlasMemoryCreateRequest(
                category: .preference,
                title: "  Favorite Editor  ",
                content: "  Prefers local-first workflows.  ",
                source: .userExplicit,
                confidence: 1.5,
                importance: -0.25,
                isUserConfirmed: true,
                isSensitive: false,
                tags: ["workflow", "editor"]
            )
        )

        XCTAssertEqual(saved.title, "Favorite Editor")
        XCTAssertEqual(saved.content, "Prefers local-first workflows.")
        XCTAssertEqual(saved.confidence, 1)
        XCTAssertEqual(saved.importance, 0)
        XCTAssertEqual(saved.category, .preference)

        let listed = await runtime.memories(limit: 10, category: .preference)
        XCTAssertEqual(listed.count, 1)
        XCTAssertEqual(listed.first?.id, saved.id)
        XCTAssertEqual(listed.first?.title, "Favorite Editor")
    }

    func testWorkflowRoutesPreserveDefinitionLifecycleAndRunHistory() async throws {
        let runtime = try await makeWorkflowRuntime()
        let workflowID = "workflow-boundary-\(UUID().uuidString)"

        let created = try await runtime.createWorkflow(
            AtlasWorkflowDefinition(
                id: workflowID,
                name: "Route Boundary Workflow",
                description: "Exercises workflow route compatibility.",
                promptTemplate: "Keep Atlas steady.",
                tags: ["boundary"],
                steps: [
                    AtlasWorkflowStep(
                        id: "step-1",
                        title: "Inspect runtime state",
                        kind: .skillAction,
                        skillID: RouteBoundarySkill.skillID,
                        actionID: RouteBoundarySkill.actionID,
                        inputJSON: #"{"query":"{{topic}}"}"#
                    )
                ],
                approvalMode: .workflowBoundary
            )
        )

        XCTAssertEqual(created.id, workflowID)
        XCTAssertEqual(created.steps.first?.sideEffectLevel, "safe_read")
        XCTAssertTrue(created.trustScope.approvedRootPaths.isEmpty)
        XCTAssertFalse(created.trustScope.allowsLiveWrite)

        let listed = await runtime.workflows()
        XCTAssertEqual(listed.count, 1)
        XCTAssertEqual(listed.first?.id, workflowID)

        let updated = try await runtime.updateWorkflow(
            AtlasWorkflowDefinition(
                id: workflowID,
                name: "Route Boundary Workflow Updated",
                description: "Exercises workflow route compatibility.",
                promptTemplate: "Keep Atlas steady.",
                tags: ["boundary", "updated"],
                steps: created.steps,
                approvalMode: created.approvalMode,
                createdAt: created.createdAt,
                updatedAt: created.updatedAt,
                sourceConversationID: created.sourceConversationID,
                isEnabled: created.isEnabled
            )
        )

        XCTAssertEqual(updated.name, "Route Boundary Workflow Updated")
        XCTAssertEqual(updated.tags, ["boundary", "updated"])

        let fetched = await runtime.workflow(id: workflowID)
        XCTAssertEqual(fetched?.name, "Route Boundary Workflow Updated")

        let run = try await runtime.runWorkflow(id: workflowID, inputValues: ["topic": "ops"])
        XCTAssertEqual(run.workflowID, workflowID)
        XCTAssertEqual(run.status, .completed)
        XCTAssertEqual(run.outcome, .success)
        XCTAssertEqual(run.inputValues["topic"], "ops")
        XCTAssertEqual(run.stepRuns.map(\.status), [.completed])
        XCTAssertEqual(run.stepRuns.first?.output, "Read-only route boundary response.")

        let runs = await runtime.workflowRuns(workflowID: workflowID)
        XCTAssertEqual(runs.count, 1)
        XCTAssertEqual(runs.first?.id, run.id)

        let deleted = try await runtime.deleteWorkflow(id: workflowID)
        XCTAssertEqual(deleted.id, workflowID)
        let missing = await runtime.workflow(id: workflowID)
        XCTAssertNil(missing)
    }

    func testDashboardRoutesPreserveProposalInstallAndCompanionState() async throws {
        let runtime = try await makeDashboardRuntime()

        let proposal = try await runtime.createDashboardProposal(
            intent: "Give me a simple runtime overview dashboard.",
            skillIDs: [RouteBoundarySkill.skillID]
        )

        XCTAssertEqual(proposal.status, .pending)
        XCTAssertEqual(proposal.spec.sourceSkillIDs, [RouteBoundarySkill.skillID])
        XCTAssertEqual(proposal.spec.widgets.first?.action, RouteBoundarySkill.actionID)

        let proposals = await runtime.dashboardProposals()
        XCTAssertTrue(proposals.contains(where: { $0.proposalID == proposal.proposalID }))

        let installedProposal = try await runtime.installDashboard(proposalID: proposal.proposalID)
        XCTAssertEqual(installedProposal.status, .installed)

        let installed = await runtime.installedDashboards()
        let installedDashboard = try XCTUnwrap(installed.first(where: { $0.id == proposal.spec.id }))
        XCTAssertEqual(installedDashboard.isPinned, false)

        let pinned = try await runtime.toggleDashboardPin(dashboardID: proposal.spec.id)
        XCTAssertTrue(pinned.isPinned)

        try await runtime.recordDashboardAccess(dashboardID: proposal.spec.id)
        let accessed = await runtime.installedDashboards()
        let accessedDashboard = try XCTUnwrap(accessed.first(where: { $0.id == proposal.spec.id }))
        XCTAssertNotNil(accessedDashboard.lastAccessedAt)

        try await runtime.removeDashboard(dashboardID: proposal.spec.id)
        let removed = await runtime.installedDashboards()
        XCTAssertTrue(removed.isEmpty)

        try await runtime.stop()
    }

    func testForgeRoutesPreserveResearchingProposalAndInstallLifecycle() async throws {
        let runtime = try await makeForgeRuntime()

        let researchingID = await runtime.forgeStartResearching(
            title: "Quote Service",
            message: "Researching a simple quote API."
        )
        let researching = await runtime.forgeResearching()
        XCTAssertTrue(researching.contains(where: { $0.id == researchingID }))

        await runtime.forgeStopResearching(id: researchingID)
        let afterStopResearching = await runtime.forgeResearching()
        XCTAssertFalse(afterStopResearching.contains(where: { $0.id == researchingID }))

        let rejectedCandidate = try await runtime.forgeCreateProposal(
            spec: makeForgeQuoteSpec(id: "quote-service-boundary"),
            plans: makeForgeQuotePlans(id: "quote-service-boundary"),
            summary: "Fetches and rates quotes.",
            rationale: "Covers the forge compatibility boundary.",
            contractJSON: #"{"kind":"api"}"#
        )

        let activeProposals = try await runtime.forgeProposals()
        XCTAssertTrue(activeProposals.contains(where: { $0.id == rejectedCandidate.id && $0.status == .pending }))

        let rejected = try await runtime.forgeRejectProposal(id: rejectedCandidate.id)
        XCTAssertEqual(rejected.status, .rejected)
        let proposalsAfterReject = try await runtime.forgeProposals()
        XCTAssertFalse(proposalsAfterReject.contains(where: { $0.id == rejectedCandidate.id }))

        let installableProposal = try await runtime.forgeCreateProposal(
            spec: makeForgeQuoteSpec(id: "quote-service-install-boundary"),
            plans: makeForgeQuotePlans(id: "quote-service-install-boundary"),
            summary: "Installs a quote service skill.",
            rationale: nil,
            contractJSON: #"{"kind":"api"}"#
        )

        let enabled = try await runtime.forgeApproveProposal(id: installableProposal.id, enable: true)
        XCTAssertEqual(enabled.status, .enabled)

        let installedSkills = await runtime.forgeInstalledSkills()
        XCTAssertTrue(installedSkills.contains(where: { $0.manifest.id == installableProposal.skillID }))

        try await runtime.forgeUninstallSkill(skillID: installableProposal.skillID)
        let afterUninstall = await runtime.forgeInstalledSkills()
        XCTAssertFalse(afterUninstall.contains(where: { $0.manifest.id == installableProposal.skillID }))

        try await runtime.stop()
    }

    func testApprovalsAndDenyReflectDeferredExecutionLifecycle() async throws {
        let runtime = try makeApprovalRuntime()
        let context = runtimeContext(runtime)
        let skill = ApprovalRouteTestSkill()
        try await context.skillRegistry.register([skill])
        _ = try await context.skillRegistry.enable(skillID: skill.manifest.id)

        let conversationID = UUID()
        let toolCallID = UUID()
        let request = SkillExecutionRequest(
            skillID: skill.manifest.id,
            actionID: skill.actions[0].id,
            input: AtlasToolInput(argumentsJSON: #"{"message":"needs approval"}"#),
            conversationID: conversationID,
            toolCallID: toolCallID
        )

        do {
            _ = try await context.skillExecutionGateway.execute(
                request,
                context: await makeSkillExecutionContext(runtime: runtime, context: context)
            )
            XCTFail("Expected approval-required execution path.")
        } catch {
            let approvals = await runtime.approvals()
            XCTAssertEqual(approvals.count, 1)
            XCTAssertEqual(approvals.first?.toolCall.id, toolCallID)
            XCTAssertEqual(approvals.first?.status, .pending)

            let denied = try await runtime.deny(toolCallID: toolCallID)
            XCTAssertEqual(denied.toolCall.id, toolCallID)
            XCTAssertEqual(denied.status, .denied)
            XCTAssertEqual(denied.conversationID, conversationID)
        }
    }

    func testDenyEmitsStreamErrorAndDoneForWaitingApprovalConversation() async throws {
        let runtime = try makeApprovalRuntime()
        let context = runtimeContext(runtime)
        let skill = ApprovalRouteTestSkill()
        try await context.skillRegistry.register([skill])
        _ = try await context.skillRegistry.enable(skillID: skill.manifest.id)

        let conversationID = UUID()
        let toolCallID = UUID()
        let stream = await runtime.streamBroadcaster.register(conversationID: conversationID)
        let collectorTask = Task { () -> [SSEEvent] in
            var events: [SSEEvent] = []
            for await event in stream {
                events.append(event)
            }
            return events
        }

        let request = SkillExecutionRequest(
            skillID: skill.manifest.id,
            actionID: skill.actions[0].id,
            input: AtlasToolInput(argumentsJSON: #"{"message":"deny via stream"}"#),
            conversationID: conversationID,
            toolCallID: toolCallID
        )

        do {
            _ = try await context.skillExecutionGateway.execute(
                request,
                context: await makeSkillExecutionContext(runtime: runtime, context: context)
            )
            XCTFail("Expected approval-required execution path.")
        } catch {
            _ = try await runtime.deny(toolCallID: toolCallID)
            let events = await collectorTask.value

            XCTAssertEqual(events.map(\.type), [.error, .done])
            XCTAssertEqual(events.first?.errorMessage, "The action was denied.")
            XCTAssertEqual(events.last?.status, "denied")
        }
    }

    func testApproveEmitsResumedStreamFailureLifecycleWithoutModelCredentials() async throws {
        let runtime = try makeApprovalRuntime()
        let context = runtimeContext(runtime)
        let skill = ApprovalRouteTestSkill()
        try await context.skillRegistry.register([skill])
        _ = try await context.skillRegistry.enable(skillID: skill.manifest.id)

        let conversationID = UUID()
        let toolCallID = UUID()
        let stream = await runtime.streamBroadcaster.register(conversationID: conversationID)
        let collectorTask = Task { () -> [SSEEvent] in
            var events: [SSEEvent] = []
            for await event in stream {
                events.append(event)
            }
            return events
        }

        let request = SkillExecutionRequest(
            skillID: skill.manifest.id,
            actionID: skill.actions[0].id,
            input: AtlasToolInput(argumentsJSON: #"{"message":"approve via stream"}"#),
            conversationID: conversationID,
            toolCallID: toolCallID
        )

        do {
            _ = try await context.skillExecutionGateway.execute(
                request,
                context: await makeSkillExecutionContext(runtime: runtime, context: context)
            )
            XCTFail("Expected approval-required execution path.")
        } catch {
            let response = try await runtime.approve(toolCallID: toolCallID)
            XCTAssertEqual(response.response.status, .completed)

            let events = await collectorTask.value
            XCTAssertEqual(events.first?.type, .assistantStarted)
            XCTAssertEqual(events.last?.type, .done)
            XCTAssertEqual(events.last?.status, "completed")
            XCTAssertTrue(events.contains(where: { $0.type == .assistantDone }))
            XCTAssertTrue(events.contains(where: { $0.type == .assistantDelta && !($0.content ?? "").isEmpty }))
        }
    }

    func testActionPolicyMutationReturnsUpdatedPolicyMapShape() async throws {
        let runtime = try makeRuntime()
        let actionID = "system.open_app"

        let initialPolicies = await runtime.actionPolicies()
        XCTAssertEqual(initialPolicies[actionID], .autoApprove)

        await runtime.setActionPolicy(.askOnce, for: actionID)
        let updatedPolicies = await runtime.actionPolicies()
        XCTAssertEqual(updatedPolicies[actionID], .askOnce)

        await runtime.setActionPolicy(.autoApprove, for: actionID)
        let overwrittenPolicies = await runtime.actionPolicies()
        XCTAssertEqual(overwrittenPolicies[actionID], .autoApprove)
    }

    private func makeRuntime(
        snapshot: RuntimeConfigSnapshot = .defaults,
        credentialBundle: AtlasCredentialBundle? = nil,
        aiClient: (any AtlasAIClient)? = nil,
        skillRegistry: SkillRegistry? = nil
    ) throws -> AgentRuntime {
        AtlasConfig.seedSnapshot(snapshot)
        let databasePath = temporaryRoot.appendingPathComponent("\(UUID().uuidString).sqlite3").path
        let memoryStore = try MemoryStore(databasePath: databasePath)
        let config = AtlasConfig(
            snapshot: snapshot,
            credentialStore: TestCredentialStore(),
            secretStore: TestSecretStore(),
            credentialBundleOverride: credentialBundle
        )
        let context = try AgentContext(
            config: config,
            aiClient: aiClient,
            memoryStore: memoryStore,
            skillRegistry: skillRegistry
        )
        return try AgentRuntime(context: context)
    }

    private func makeStartedRuntime(
        snapshot: RuntimeConfigSnapshot = .defaults,
        credentialBundle: AtlasCredentialBundle? = nil
    ) async throws -> AgentRuntime {
        let runtime = try makeRuntime(snapshot: snapshot, credentialBundle: credentialBundle)
        try await runtime.start()
        return runtime
    }

    private func makeWorkflowRuntime() async throws -> AgentRuntime {
        let registry = SkillRegistry(defaults: UserDefaults(suiteName: "AtlasWorkflowRouteCompatibility.\(UUID().uuidString)")!)
        let skill = RouteBoundarySkill()
        try await registry.register([skill])
        _ = try await registry.enable(skillID: skill.manifest.id)
        return try makeRuntime(aiClient: TestAtlasAIClient(), skillRegistry: registry)
    }

    private func makeDashboardRuntime() async throws -> AgentRuntime {
        let registry = SkillRegistry(defaults: UserDefaults(suiteName: "AtlasDashboardRouteCompatibility.\(UUID().uuidString)")!)
        let skill = RouteBoundarySkill()
        try await registry.register([skill])
        _ = try await registry.enable(skillID: skill.manifest.id)
        let aiClient = TestAtlasAIClient(
            completionText: #"""
            {
              "id":"runtime-overview",
              "title":"Runtime Overview",
              "icon":"bolt.horizontal.circle",
              "description":"A compact look at Atlas runtime state.",
              "sourceSkillIDs":["route-boundary-skill"],
              "widgets":[
                {
                  "id":"runtime-state",
                  "type":"stat_card",
                  "title":"Runtime State",
                  "skillID":"route-boundary-skill",
                  "action":"route-boundary.read",
                  "defaultInputs":{"query":"runtime state"},
                  "binding":{"valuePath":"message"},
                  "dataKey":"message",
                  "emptyMessage":"No runtime state."
                }
              ],
              "emptyState":"No dashboard data available."
            }
            """#
        )
        return try makeRuntime(aiClient: aiClient, skillRegistry: registry)
    }

    private func makeForgeRuntime() async throws -> AgentRuntime {
        let runtime = try makeRuntime(snapshot: RuntimeConfigSnapshot(runtimePort: availableTestPort()))
        try await runtime.start()
        return runtime
    }

    private func makeForgeQuoteSpec(id: String) -> ForgeSkillSpec {
        ForgeSkillSpec(
            id: id,
            name: "Quote Service",
            description: "Fetches and rates quotes from a mock API.",
            category: .utility,
            riskLevel: .low,
            tags: ["quotes", "api"],
            actions: [
                ForgeActionSpec(
                    id: "\(id).get",
                    name: "Get Quote",
                    description: "Fetch a quote by category.",
                    permissionLevel: .read,
                    inputSchema: AtlasToolInputSchema(
                        properties: [
                            "category": AtlasToolInputProperty(type: "string", description: "Quote category")
                        ],
                        additionalProperties: false
                    )
                ),
                ForgeActionSpec(
                    id: "\(id).rate",
                    name: "Rate Quote",
                    description: "Submit a rating for a quote.",
                    permissionLevel: .draft,
                    inputSchema: AtlasToolInputSchema(
                        properties: [
                            "quote_id": AtlasToolInputProperty(type: "string", description: "Quote identifier"),
                            "rating": AtlasToolInputProperty(type: "integer", description: "Rating value")
                        ],
                        additionalProperties: false
                    )
                )
            ]
        )
    }

    private func makeForgeQuotePlans(id: String) -> [ForgeActionPlan] {
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

    private func makeApprovalRuntime() throws -> AgentRuntime {
        let databasePath = temporaryRoot.appendingPathComponent("\(UUID().uuidString).sqlite3").path
        let memoryStore = try MemoryStore(databasePath: databasePath)
        let registry = SkillRegistry(defaults: UserDefaults(suiteName: "AtlasRuntimeRouteCompatibility.\(UUID().uuidString)")!)
        let config = AtlasConfig(
            autoApproveDraftTools: false,
            memoryDatabasePath: databasePath
        )
        let context = try AgentContext(
            config: config,
            logger: AtlasLogger(category: "runtime-route-compatibility"),
            memoryStore: memoryStore,
            skillRegistry: registry
        )
        return try AgentRuntime(context: context)
    }

    private func makeDocumentRuntime() async throws -> AgentRuntime {
        let mindPath = temporaryRoot.appendingPathComponent("MIND-\(UUID().uuidString).md").path
        let skillsPath = temporaryRoot.appendingPathComponent("SKILLS-\(UUID().uuidString).md").path
        let databasePath = temporaryRoot.appendingPathComponent("\(UUID().uuidString).sqlite3").path
        let memoryStore = try MemoryStore(databasePath: databasePath)
        let config = AtlasConfig(
            memoryDatabasePath: databasePath
        )
        let mindEngine = MindEngine(
            fileStore: MindFileStore(filePath: mindPath),
            reflectionService: MindReflectionService(
                openAI: TestOpenAIQuery(),
                fastModel: { "test-fast-model" }
            )
        )
        let skillsEngine = SkillsEngine(
            skillsFilePath: skillsPath,
            openAI: TestOpenAIQuery()
        )
        let context = try AgentContext(
            config: config,
            memoryStore: memoryStore,
            mindEngine: mindEngine,
            skillsEngine: skillsEngine
        )

        try await mindEngine.updateContent(validMindContent(read: "Seeded and ready."))
        try await skillsEngine.updateContent("# Seeded Skills")
        return try AgentRuntime(context: context)
    }

    private func makeSkillExecutionContext(
        runtime: AgentRuntime,
        context: AgentContext
    ) async -> SkillExecutionContext {
        SkillExecutionContext(
            conversationID: nil,
            logger: context.logger,
            config: context.config,
            permissionManager: context.permissionManager,
            runtimeStatusProvider: {
                await runtime.status()
            },
            enabledSkillsProvider: {
                await context.skillRegistry.listEnabled()
            },
            memoryItemsProvider: {
                do {
                    return try await context.memoryStore.listMemories(limit: 50)
                } catch {
                    return []
                }
            }
        )
    }

    private func runtimeContext(_ runtime: AgentRuntime) -> AgentContext {
        Mirror(reflecting: runtime)
            .children
            .first(where: { $0.label == "context" })?
            .value as! AgentContext
    }

    private func availableTestPort() -> Int {
        18_000 + Int.random(in: 0..<1_000)
    }

    private func validMindContent(read: String) -> String {
        """
        # Mind of Atlas

        ## Who I Am

        I am Atlas.

        ## My Understanding of You

        Still learning.

        ## Patterns I've Noticed

        Nothing significant yet.

        ## Active Theories

        None yet.

        ## Our Story

        We are getting started.

        ## What I'm Curious About

        What matters most right now?

        ## Today's Read

        \(read)
        """
    }
}

private struct TestPathProvider: PathProvider {
    let root: URL

    func atlasSupportDirectory() -> URL {
        root
    }

    func configFileURL() -> URL {
        root.appendingPathComponent("config.json")
    }
}

private struct TestCredentialStore: CredentialStore {
    func readSecret(service: String, account: String) throws -> String {
        throw UnsupportedSecretStoreError.unavailableBackend(description: "Test credential store is empty.")
    }

    func storeSecret(_ secret: String, service: String, account: String) throws {}

    func deleteSecret(service: String, account: String) throws {}

    func containsSecret(service: String, account: String) -> Bool {
        false
    }
}

private struct TestSecretStore: SecretStore, SecretCacheInvalidating {
    func getSecret(name: String) throws -> String? {
        nil
    }

    func setSecret(name: String, value: String) throws {}

    func deleteSecret(name: String) throws {}

    func hasSecret(name: String) -> Bool {
        false
    }

    func listSecretNames() throws -> [String] {
        []
    }

    func invalidateSecretCache() {}
}

private struct TestOpenAIQuery: OpenAIQuerying {
    func complete(systemPrompt: String, userContent: String, model: String?) async throws -> String {
        ""
    }
}

private actor TestAtlasAIClient: AtlasAIClient {
    let completionText: String

    init(completionText: String = "Route boundary response.") {
        self.completionText = completionText
    }

    func sendTurn(
        conversation: AtlasConversation,
        tools: [AtlasToolDefinition],
        instructions: String?,
        model: String,
        attachments: [AtlasMessageAttachment]
    ) async throws -> AITurnResponse {
        AITurnResponse(turnID: UUID().uuidString, assistantText: completionText, rawToolCalls: [])
    }

    func sendTurnStreaming(
        conversation: AtlasConversation,
        tools: [AtlasToolDefinition],
        instructions: String?,
        model: String,
        attachments: [AtlasMessageAttachment],
        onDelta: @escaping @Sendable (String) async -> Void
    ) async throws -> AITurnResponse {
        await onDelta(completionText)
        return AITurnResponse(turnID: UUID().uuidString, assistantText: completionText, rawToolCalls: [])
    }

    func continueTurn(
        previousTurnID: String,
        conversation: AtlasConversation,
        toolCalls: [AtlasToolCall],
        toolOutputs: [AIToolOutput],
        tools: [AtlasToolDefinition],
        instructions: String?,
        model: String
    ) async throws -> AITurnResponse {
        AITurnResponse(turnID: UUID().uuidString, assistantText: completionText, rawToolCalls: [])
    }

    func continueTurnStreaming(
        previousTurnID: String,
        conversation: AtlasConversation,
        toolCalls: [AtlasToolCall],
        toolOutputs: [AIToolOutput],
        tools: [AtlasToolDefinition],
        instructions: String?,
        model: String,
        onDelta: @escaping @Sendable (String) async -> Void
    ) async throws -> AITurnResponse {
        await onDelta(completionText)
        return AITurnResponse(turnID: UUID().uuidString, assistantText: completionText, rawToolCalls: [])
    }

    func validateCredential() async throws {}

    func complete(systemPrompt: String, userContent: String, model: String?) async throws -> String {
        completionText
    }
}

private struct ApprovalRouteTestSkill: AtlasSkill {
    var manifest: SkillManifest {
        SkillManifest(
            id: "approval-route-test",
            name: "Approval Route Test",
            version: "1.0.0",
            description: "A test skill that requires approval.",
            category: .productivity,
            lifecycleState: .enabled,
            capabilities: [.capabilitySummary],
            requiredPermissions: [.draftWrite],
            riskLevel: .medium,
            trustProfile: .general,
            freshnessType: .staticKnowledge,
            preferredQueryTypes: [],
            routingPriority: 0,
            canAnswerStructuredLiveData: false,
            canHandleLocalData: false,
            canHandleExploratoryQueries: false,
            restrictionsSummary: ["Used only in compatibility tests."],
            supportsReadOnlyMode: false,
            isUserVisible: false,
            isEnabledByDefault: true
        )
    }

    var actions: [SkillActionDefinition] {
        [
            SkillActionDefinition(
                id: "approval-route-test.run",
                name: "Approval Route Test",
                description: "Requires approval before execution.",
                inputSchemaSummary: "message: String",
                outputSchemaSummary: "A short success string.",
                permissionLevel: .draft,
                sideEffectLevel: .draftWrite,
                isEnabled: true,
                routingPriority: 0
            )
        ]
    }

    func validateConfiguration(context: SkillValidationContext) async -> SkillValidationResult {
        SkillValidationResult(
            skillID: manifest.id,
            status: .passed,
            summary: "Ready."
        )
    }

    func execute(actionID: String, input: AtlasToolInput, context: SkillExecutionContext) async throws -> SkillExecutionResult {
        SkillExecutionResult(
            skillID: manifest.id,
            actionID: actionID,
            output: "Approved route execution completed.",
            summary: "Approved route execution completed."
        )
    }
}

private struct RouteBoundarySkill: AtlasSkill {
    static let skillID = "route-boundary-skill"
    static let actionID = "route-boundary.read"

    var manifest: SkillManifest {
        SkillManifest(
            id: Self.skillID,
            name: "Route Boundary Skill",
            version: "1.0.0",
            description: "A read-only test skill for compatibility routes.",
            category: .utility,
            lifecycleState: .enabled,
            capabilities: [.capabilitySummary],
            requiredPermissions: [],
            riskLevel: .low,
            trustProfile: .exactStructured,
            freshnessType: .staticKnowledge,
            preferredQueryTypes: [],
            routingPriority: 0,
            canAnswerStructuredLiveData: true,
            canHandleLocalData: false,
            canHandleExploratoryQueries: false,
            restrictionsSummary: ["Used only in compatibility tests."],
            supportsReadOnlyMode: true,
            isUserVisible: false,
            isEnabledByDefault: true
        )
    }

    var actions: [SkillActionDefinition] {
        [
            SkillActionDefinition(
                id: Self.actionID,
                name: "Read Boundary State",
                description: "Returns a fixed read-only status payload.",
                inputSchemaSummary: "query: String",
                outputSchemaSummary: "message",
                permissionLevel: .read,
                sideEffectLevel: .safeRead,
                isEnabled: true,
                routingPriority: 0,
                inputSchema: AtlasToolInputSchema(
                    properties: [
                        "query": AtlasToolInputProperty(type: "string", description: "Boundary query")
                    ],
                    required: ["query"]
                )
            )
        ]
    }

    func validateConfiguration(context: SkillValidationContext) async -> SkillValidationResult {
        SkillValidationResult(
            skillID: manifest.id,
            status: .passed,
            summary: "Ready."
        )
    }

    func execute(actionID: String, input: AtlasToolInput, context: SkillExecutionContext) async throws -> SkillExecutionResult {
        SkillExecutionResult(
            skillID: manifest.id,
            actionID: actionID,
            output: #"{"message":"Read-only route boundary response."}"#,
            summary: "Read-only route boundary response."
        )
    }
}
