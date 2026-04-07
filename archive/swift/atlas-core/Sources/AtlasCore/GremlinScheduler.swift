import Foundation
import AtlasMemory
import AtlasShared
import AtlasLogging

/// Called after a successful gremlin run to deliver the output to a communication destination.
public typealias GremlinCommunicationNotifier = @Sendable (_ destination: CommunicationDestination, _ emoji: String, _ name: String, _ output: String) async -> Void

/// Called when a gremlin run requires user approval. Delivers the approval request to the
/// appropriate chat bridge identified by the session. Platform-agnostic — works for any bridge.
public typealias GremlinApprovalNotifier = @Sendable (_ session: ChatSession, _ approval: ApprovalRequest) async -> Void

private struct GremlinTimeoutError: LocalizedError {
    let gremlinID: String
    let timeoutSeconds: Int
    var errorDescription: String? { "Gremlin '\(gremlinID)' timed out after \(timeoutSeconds) seconds." }
}

/// Manages scheduling and execution of Gremlin automations.
public actor GremlinScheduler {
    private var tasks: [String: Task<Void, Never>] = [:]
    private var runLocks: Set<String> = []
    private let fileStore: GremlinsFileStore
    private let context: AgentContext
    private let logger: AtlasLogger
    private var communicationNotifier: GremlinCommunicationNotifier?
    private var approvalNotifier: GremlinApprovalNotifier?
    private let workflowRunner: WorkflowRunner?

    public init(
        fileStore: GremlinsFileStore,
        context: AgentContext,
        workflowRunner: WorkflowRunner? = nil,
        logger: AtlasLogger = .runtime
    ) {
        self.fileStore = fileStore
        self.context = context
        self.workflowRunner = workflowRunner
        self.logger = logger
    }

    public func setCommunicationNotifier(_ notifier: @escaping GremlinCommunicationNotifier) {
        self.communicationNotifier = notifier
    }

    public func setApprovalNotifier(_ notifier: @escaping GremlinApprovalNotifier) {
        self.approvalNotifier = notifier
    }

    // MARK: - Lifecycle

    public func start(gremlins: [GremlinItem]) async {
        logger.info("GremlinScheduler starting", metadata: ["count": "\(gremlins.count)"])
        for gremlin in gremlins where gremlin.isEnabled {
            await scheduleWithCatchup(gremlin)
        }
    }

    public func reload(gremlins: [GremlinItem]) async {
        cancelAll()
        await start(gremlins: gremlins)
    }

    public func cancel(id: String) {
        tasks[id]?.cancel()
        tasks.removeValue(forKey: id)
    }

    public func cancelAll() {
        for task in tasks.values { task.cancel() }
        tasks.removeAll()
    }

    // MARK: - Catchup-aware scheduling

    /// Schedules a gremlin, firing a catchup run first if a scheduled run was missed during downtime.
    private func scheduleWithCatchup(_ gremlin: GremlinItem) async {
        // Check if we missed a run while the daemon was down.
        // Query the last run from history and compare with what the schedule says.
        if let lastRun = try? await fileStore.runsForGremlin(gremlinID: gremlin.id, limit: 1).first {
            let referenceDate = lastRun.finishedAt ?? lastRun.startedAt
            let parser = GremlinFileParser()
            if let nextAfterLast = parser.nextRunDate(for: gremlin.scheduleRaw, after: referenceDate),
               nextAfterLast < Date.now {
                logger.info("Gremlin missed a run during downtime — firing catchup", metadata: [
                    "id": gremlin.id,
                    "missed_at": nextAfterLast.description
                ])
                Task { await self.runNow(gremlin) }
            }
        }
        await schedule(gremlin)
    }

    // MARK: - Scheduling

    public func schedule(_ gremlin: GremlinItem) async {
        guard gremlin.isEnabled else { return }

        let parser = GremlinFileParser()
        guard let nextRun = parser.nextRunDate(for: gremlin.scheduleRaw, after: .now) else {
            logger.info("Gremlin has unschedulable or unsupported schedule — skipping", metadata: [
                "id": gremlin.id,
                "schedule": gremlin.scheduleRaw
            ])
            return
        }

        let delay = nextRun.timeIntervalSinceNow
        logger.info("Scheduled gremlin", metadata: [
            "id": gremlin.id,
            "next_run": nextRun.description,
            "delay_seconds": "\(Int(delay))"
        ])

        let gremlinID = gremlin.id
        let task = Task {
            if delay > 0 {
                try? await Task.sleep(nanoseconds: UInt64(delay * 1_000_000_000))
            }
            guard !Task.isCancelled else { return }

            await self.runNow(gremlin)
            self.logger.info("Gremlin run completed", metadata: ["id": gremlinID])

            guard !Task.isCancelled else { return }
            if !gremlin.scheduleRaw.lowercased().hasPrefix("once ") {
                if let updated = (try? await self.fileStore.loadGremlins())?.first(where: { $0.id == gremlinID }),
                   updated.isEnabled {
                    await self.schedule(updated)
                }
            }
        }
        tasks[gremlin.id] = task
    }

    // MARK: - Run (public entry point with retry)

    @discardableResult
    public func runNow(_ gremlin: GremlinItem) async -> GremlinRun {
        guard !runLocks.contains(gremlin.id) else {
            logger.info("Gremlin already running, skipping", metadata: ["id": gremlin.id])
            let run = GremlinRun(
                gremlinID: gremlin.id, startedAt: .now, finishedAt: .now, status: .skipped,
                errorMessage: "Another run was already in progress."
            )
            try? await fileStore.saveRun(run)
            return run
        }

        return await runWithRetry(gremlin, attemptsRemaining: gremlin.maxRetries)
    }

    // MARK: - Retry loop

    private func runWithRetry(_ gremlin: GremlinItem, attemptsRemaining: Int) async -> GremlinRun {
        let run = await executeOnce(gremlin)
        if run.status == .failed, attemptsRemaining > 0 {
            logger.info("Gremlin run failed, retrying", metadata: [
                "id": gremlin.id,
                "attempts_remaining": "\(attemptsRemaining)"
            ])
            try? await Task.sleep(for: .seconds(30))
            guard !Task.isCancelled else { return run }
            return await runWithRetry(gremlin, attemptsRemaining: attemptsRemaining - 1)
        }
        return run
    }

    // MARK: - Single execution attempt

    private func executeOnce(_ gremlin: GremlinItem) async -> GremlinRun {
        runLocks.insert(gremlin.id)
        let startedAt = Date.now

        defer { runLocks.remove(gremlin.id) }

        logger.info("Running gremlin", metadata: ["id": gremlin.id, "prompt_length": "\(gremlin.prompt.count)"])

        let conversationID = UUID()

        do {
            let runStatus: GremlinRunStatus
            let output: String?
            let errorMessage: String?
            let actualConversationID: UUID?
            let workflowRunID: UUID?

            if let workflowID = gremlin.workflowID {
                guard let workflowRunner else {
                    throw NSError(
                        domain: "Atlas.GremlinScheduler", code: 1,
                        userInfo: [NSLocalizedDescriptionKey: "Workflow automations are unavailable because the workflow runner is not configured."]
                    )
                }
                guard let definition = await workflowRunner.workflowDefinition(id: workflowID) else {
                    throw NSError(
                        domain: "Atlas.GremlinScheduler", code: 2,
                        userInfo: [NSLocalizedDescriptionKey: "Workflow automation references missing workflow '\(workflowID)'."]
                    )
                }
                let workflowRun = await workflowRunner.run(
                    definition: definition,
                    inputValues: gremlin.workflowInputValues ?? [:]
                )
                workflowRunID = workflowRun.id
                actualConversationID = workflowRun.conversationID
                switch workflowRun.status {
                case .completed:
                    runStatus = .success
                    output = workflowRun.assistantSummary ?? workflowRun.stepRuns.compactMap(\.output).last
                    errorMessage = nil
                case .waitingForApproval:
                    runStatus = .failed
                    output = workflowRun.assistantSummary
                    errorMessage = workflowRun.errorMessage ?? "Workflow automation requires approval."
                case .failed, .denied:
                    runStatus = .failed
                    output = workflowRun.assistantSummary
                    errorMessage = workflowRun.errorMessage
                case .pending, .running:
                    runStatus = .running
                    output = workflowRun.assistantSummary
                    errorMessage = nil
                }
            } else {
                // Prompt-based run — wrap in timeout if configured
                let request = AtlasMessageRequest(conversationID: conversationID, message: gremlin.prompt)
                let response = try await executePrompt(request, gremlin: gremlin)
                actualConversationID = conversationID
                workflowRunID = nil

                switch response.response.status {
                case .completed:
                    runStatus = .success
                    output = response.response.assistantMessage
                    errorMessage = nil
                case .waitingForApproval:
                    runStatus = .failed
                    output = response.response.assistantMessage
                    errorMessage = "Gremlin run requires user approval — cannot complete automatically."
                    if let destination = gremlin.communicationDestination,
                       !response.response.pendingApprovals.isEmpty,
                       let notifier = approvalNotifier {
                        let session = ChatSession(
                            platform: destination.platform,
                            platformChatID: destination.channelID,
                            platformUserID: destination.userID,
                            activeConversationID: conversationID
                        )
                        for approval in response.response.pendingApprovals {
                            await notifier(session, approval)
                        }
                    }
                case .failed:
                    runStatus = .failed
                    output = response.response.assistantMessage
                    errorMessage = response.response.errorMessage
                }
            }

            // Deliver output to communication destination on success
            if let destination = gremlin.communicationDestination,
               let deliverableOutput = output, !deliverableOutput.isEmpty,
               let notifier = communicationNotifier,
               runStatus == .success {
                await notifier(destination, gremlin.emoji, gremlin.name, deliverableOutput)
            }

            // Disable one-shot gremlins after firing
            if gremlin.scheduleRaw.lowercased().hasPrefix("once ") {
                let disabledGremlin = GremlinItem(
                    id: gremlin.id, name: gremlin.name, emoji: gremlin.emoji,
                    prompt: gremlin.prompt, scheduleRaw: gremlin.scheduleRaw,
                    isEnabled: false, sourceType: gremlin.sourceType, createdAt: gremlin.createdAt,
                    workflowID: gremlin.workflowID, workflowInputValues: gremlin.workflowInputValues,
                    communicationDestination: gremlin.communicationDestination,
                    telegramChatID: gremlin.telegramChatID,
                    gremlinDescription: gremlin.gremlinDescription, tags: gremlin.tags,
                    maxRetries: gremlin.maxRetries, timeoutSeconds: gremlin.timeoutSeconds
                )
                try? await fileStore.updateGremlin(disabledGremlin)
            }

            let run = GremlinRun(
                gremlinID: gremlin.id, startedAt: startedAt, finishedAt: Date.now,
                status: runStatus, output: output, errorMessage: errorMessage,
                conversationID: actualConversationID, workflowRunID: workflowRunID
            )
            try? await fileStore.saveRun(run)
            return run

        } catch {
            let run = GremlinRun(
                gremlinID: gremlin.id, startedAt: startedAt, finishedAt: Date.now,
                status: .failed, errorMessage: error.localizedDescription,
                conversationID: conversationID
            )
            try? await fileStore.saveRun(run)
            logger.error("Gremlin run failed", metadata: ["id": gremlin.id, "error": error.localizedDescription])
            return run
        }
    }

    // MARK: - Timeout wrapper

    private func executePrompt(
        _ request: AtlasMessageRequest,
        gremlin: GremlinItem
    ) async throws -> AtlasMessageResponseEnvelope {
        guard let timeoutSeconds = gremlin.timeoutSeconds, timeoutSeconds > 0 else {
            return try await AgentLoop(context: context).process(request)
        }

        let capturedContext = context
        return try await withThrowingTaskGroup(of: AtlasMessageResponseEnvelope.self) { group in
            group.addTask {
                try await AgentLoop(context: capturedContext).process(request)
            }
            group.addTask {
                try await Task.sleep(for: .seconds(timeoutSeconds))
                throw GremlinTimeoutError(gremlinID: gremlin.id, timeoutSeconds: timeoutSeconds)
            }
            let result = try await group.next()!
            group.cancelAll()
            return result
        }
    }
}
