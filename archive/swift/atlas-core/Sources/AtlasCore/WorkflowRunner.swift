import Foundation
import AtlasLogging
import AtlasShared
import AtlasSkills
import AtlasTools

public actor WorkflowRunner {
    private let context: AgentContext
    private let store: WorkflowStore
    private let logger: AtlasLogger

    public init(
        context: AgentContext,
        store: WorkflowStore,
        logger: AtlasLogger = AtlasLogger(category: "workflow.runner")
    ) {
        self.context = context
        self.store = store
        self.logger = logger
    }

    public func workflowDefinition(id: String) async -> AtlasWorkflowDefinition? {
        await store.definition(id: id)
    }

    public func run(
        definition: AtlasWorkflowDefinition,
        inputValues: [String: String] = [:]
    ) async -> AtlasWorkflowRun {
        let startedAt = Date.now

        if definition.approvalMode == .workflowBoundary,
           requiresWorkflowApproval(definition: definition) {
            let approval = AtlasWorkflowApproval(
                workflowID: definition.id,
                workflowRunID: UUID(),
                status: .pending,
                reason: approvalReason(for: definition),
                trustScope: definition.trustScope
            )

            let run = AtlasWorkflowRun(
                id: approval.workflowRunID,
                workflowID: definition.id,
                workflowName: definition.name,
                status: .waitingForApproval,
                outcome: .waitingForApproval,
                inputValues: inputValues,
                stepRuns: definition.steps.map {
                    AtlasWorkflowStepRun(stepID: $0.id, title: $0.title, status: .pending)
                },
                approval: approval,
                assistantSummary: nil,
                errorMessage: approval.reason,
                startedAt: startedAt
            )
            await store.upsertRun(run)
            return run
        }

        return await executeApprovedWorkflow(
            definition: definition,
            inputValues: inputValues,
            existingRunID: nil,
            approvedAt: nil
        )
    }

    public func approve(runID: UUID) async throws -> AtlasWorkflowRun {
        guard let run = await store.run(id: runID),
              let definition = await store.definition(id: run.workflowID) else {
            throw WorkflowStoreError.runNotFound(runID)
        }

        guard let approval = run.approval, approval.status == .pending else {
            return run
        }

        return await executeApprovedWorkflow(
            definition: definition,
            inputValues: run.inputValues,
            existingRunID: runID,
            approvedAt: .now
        )
    }

    public func deny(runID: UUID) async throws -> AtlasWorkflowRun {
        guard let run = await store.run(id: runID) else {
            throw WorkflowStoreError.runNotFound(runID)
        }

        let deniedApproval = run.approval.map {
            AtlasWorkflowApproval(
                id: $0.id,
                workflowID: $0.workflowID,
                workflowRunID: $0.workflowRunID,
                status: .denied,
                reason: $0.reason,
                requestedAt: $0.requestedAt,
                resolvedAt: .now,
                trustScope: $0.trustScope
            )
        }

        let deniedRun = AtlasWorkflowRun(
            id: run.id,
            workflowID: run.workflowID,
            workflowName: run.workflowName,
            status: .denied,
            outcome: .denied,
            inputValues: run.inputValues,
            stepRuns: run.stepRuns,
            approval: deniedApproval,
            assistantSummary: run.assistantSummary,
            errorMessage: "Workflow approval was denied.",
            startedAt: run.startedAt,
            finishedAt: .now,
            conversationID: run.conversationID
        )
        await store.upsertRun(deniedRun)
        return deniedRun
    }

    private func executeApprovedWorkflow(
        definition: AtlasWorkflowDefinition,
        inputValues: [String: String],
        existingRunID: UUID?,
        approvedAt: Date?
    ) async -> AtlasWorkflowRun {
        let runID = existingRunID ?? UUID()
        let existingRun: AtlasWorkflowRun? = if let existingRunID {
            await store.run(id: existingRunID)
        } else {
            nil
        }
        let startedAt = existingRun?.startedAt ?? Date.now
        var stepRuns: [AtlasWorkflowStepRun] = existingRun?.stepRuns ?? []
        var conversationID: UUID? = existingRun?.conversationID
        var assistantSummary: String? = existingRun?.assistantSummary
        var consumedStepApproval = false

        for step in definition.steps {
            if let existingStepRun = stepRuns.first(where: { $0.stepID == step.id }),
               existingStepRun.status == .completed {
                continue
            }

            do {
                let resolvedStep = try await resolveStep(step, inputValues: inputValues)

                if shouldPauseBeforeStep(
                    resolvedStep,
                    definition: definition,
                    approvedAt: approvedAt,
                    consumedStepApproval: consumedStepApproval
                ) {
                    let pausedRun = waitingForApprovalRun(
                        definition: definition,
                        runID: runID,
                        startedAt: startedAt,
                        stepRuns: markStep(
                            stepRuns,
                            stepID: step.id,
                            title: step.title,
                            status: .waitingForApproval,
                            startedAt: nil,
                            finishedAt: nil,
                            output: nil,
                            errorMessage: approvalReason(for: resolvedStep, workflowName: definition.name)
                        ),
                        inputValues: inputValues,
                        conversationID: conversationID,
                        assistantSummary: assistantSummary,
                        approvalReason: approvalReason(for: resolvedStep, workflowName: definition.name),
                        trustScope: approvalTrustScope(for: resolvedStep, fallback: definition.trustScope)
                    )
                    await store.upsertRun(pausedRun)
                    return pausedRun
                }

                let startedStep = AtlasWorkflowStepRun(
                    stepID: step.id,
                    title: step.title,
                    status: .running,
                    startedAt: .now
                )
                stepRuns = markStep(
                    stepRuns,
                    stepID: step.id,
                    title: step.title,
                    status: .running,
                    startedAt: startedStep.startedAt
                )

                switch resolvedStep.step.kind {
                case .skillAction:
                    let result = try await executeSkillStep(
                        resolvedStep,
                        definition: definition,
                        runID: runID,
                        approvedAt: shouldGrantWorkflowTrust(
                            to: resolvedStep,
                            definition: definition,
                            approvedAt: approvedAt,
                            consumedStepApproval: consumedStepApproval
                        ) ? approvedAt : nil,
                        inputValues: inputValues
                    )
                    stepRuns = markStep(
                        stepRuns,
                        stepID: step.id,
                        title: step.title,
                        status: .completed,
                        startedAt: startedStep.startedAt,
                        finishedAt: .now,
                        output: result.summary
                    )
                case .prompt:
                    let result = try await executePromptStep(step, inputValues: inputValues)
                    conversationID = result.conversationID
                    assistantSummary = result.message
                    stepRuns = markStep(
                        stepRuns,
                        stepID: step.id,
                        title: step.title,
                        status: .completed,
                        startedAt: startedStep.startedAt,
                        finishedAt: .now,
                        output: result.message
                    )
                }
                if definition.approvalMode == .stepByStep, resolvedStep.requiresExplicitApproval {
                    consumedStepApproval = true
                }
            } catch let error as ToolExecutionError {
                let failedRun = AtlasWorkflowRun(
                    id: runID,
                    workflowID: definition.id,
                    workflowName: definition.name,
                    status: .failed,
                    outcome: .failed,
                    inputValues: inputValues,
                    stepRuns: markStep(
                        stepRuns,
                        stepID: step.id,
                        title: step.title,
                        status: .failed,
                        startedAt: nil,
                        finishedAt: .now,
                        output: nil,
                        errorMessage: error.localizedDescription
                    ),
                    approval: nil,
                    assistantSummary: assistantSummary,
                    errorMessage: error.localizedDescription,
                    startedAt: startedAt,
                    finishedAt: .now,
                    conversationID: conversationID
                )
                await store.upsertRun(failedRun)
                return failedRun
            } catch {
                let failedRun = AtlasWorkflowRun(
                    id: runID,
                    workflowID: definition.id,
                    workflowName: definition.name,
                    status: .failed,
                    outcome: .failed,
                    inputValues: inputValues,
                    stepRuns: markStep(
                        stepRuns,
                        stepID: step.id,
                        title: step.title,
                        status: .failed,
                        startedAt: nil,
                        finishedAt: .now,
                        output: nil,
                        errorMessage: error.localizedDescription
                    ),
                    approval: nil,
                    assistantSummary: assistantSummary,
                    errorMessage: error.localizedDescription,
                    startedAt: startedAt,
                    finishedAt: .now,
                    conversationID: conversationID
                )
                await store.upsertRun(failedRun)
                return failedRun
            }
        }

        let run = AtlasWorkflowRun(
            id: runID,
            workflowID: definition.id,
            workflowName: definition.name,
            status: .completed,
            outcome: .success,
            inputValues: inputValues,
            stepRuns: stepRuns,
            approval: approvedAt.map {
                AtlasWorkflowApproval(
                    workflowID: definition.id,
                    workflowRunID: runID,
                    status: .approved,
                    reason: approvalReason(for: definition),
                    requestedAt: startedAt,
                    resolvedAt: $0,
                    trustScope: definition.trustScope
                )
            },
            assistantSummary: assistantSummary,
            errorMessage: nil,
            startedAt: startedAt,
            finishedAt: .now,
            conversationID: conversationID
        )
        await store.upsertRun(run)
        return run
    }

    private struct ResolvedStepExecution: Sendable {
        let step: AtlasWorkflowStep
        let interpolatedInputJSON: String
        let actualTrustScope: AtlasWorkflowTrustScope
        let requiresExplicitApproval: Bool
    }

    private func executeSkillStep(
        _ resolvedStep: ResolvedStepExecution,
        definition: AtlasWorkflowDefinition,
        runID: UUID,
        approvedAt: Date?,
        inputValues: [String: String]
    ) async throws -> SkillExecutionResult {
        let step = resolvedStep.step
        guard let skillID = step.skillID, let actionID = step.actionID else {
            throw AtlasToolError.invalidInput("Workflow step '\(step.title)' is missing skill metadata.")
        }

        let request = SkillExecutionRequest(
            skillID: skillID,
            actionID: actionID,
            input: AtlasToolInput(argumentsJSON: resolvedStep.interpolatedInputJSON),
            conversationID: nil,
            toolCallID: UUID()
        )
        if approvedAt != nil {
            try validateWorkflowTrustCoverage(
                for: resolvedStep,
                workflowTrustScope: definition.trustScope
            )
        }
        let executionContext = await makeSkillExecutionContext(
            workflowDefinition: definition,
            runID: runID,
            approvedAt: approvedAt,
            stepTrustScope: approvedAt != nil ? resolvedStep.actualTrustScope : nil
        )
        return try await context.skillExecutionGateway.execute(request, context: executionContext)
    }

    private struct PromptStepOutput: Sendable {
        let conversationID: UUID
        let message: String
    }

    private func executePromptStep(
        _ step: AtlasWorkflowStep,
        inputValues: [String: String]
    ) async throws -> PromptStepOutput {
        let conversationID = UUID()
        let prompt = interpolate(step.prompt ?? "", values: inputValues)
        let response = try await AgentLoop(context: context).process(
            AtlasMessageRequest(conversationID: conversationID, message: prompt)
        )
        return PromptStepOutput(
            conversationID: conversationID,
            message: response.response.assistantMessage
        )
    }

    private func makeSkillExecutionContext(
        workflowDefinition: AtlasWorkflowDefinition,
        runID: UUID,
        approvedAt: Date?,
        stepTrustScope: AtlasWorkflowTrustScope?
    ) async -> SkillExecutionContext {
        SkillExecutionContext(
            conversationID: nil,
            logger: context.logger,
            config: context.config,
            permissionManager: context.permissionManager,
            workflowExecution: stepTrustScope.map {
                AtlasWorkflowExecutionContext(
                    workflowID: workflowDefinition.id,
                    workflowRunID: runID,
                    trustScope: $0,
                    approvalMode: workflowDefinition.approvalMode,
                    approvalGrantedAt: approvedAt
                )
            },
            runtimeStatusProvider: { nil },
            enabledSkillsProvider: {
                await self.context.skillRegistry.listEnabled()
            },
            memoryItemsProvider: {
                (try? await self.context.memoryStore.listMemories(limit: 100)) ?? []
            }
        )
    }

    private func requiresWorkflowApproval(definition: AtlasWorkflowDefinition) -> Bool {
        definition.steps.contains(where: { $0.kind == .prompt }) ||
        definition.trustScope.allowsSensitiveRead ||
        definition.trustScope.allowsLiveWrite ||
        !definition.trustScope.approvedRootPaths.isEmpty ||
        !definition.trustScope.allowedApps.isEmpty
    }

    private func approvalReason(for definition: AtlasWorkflowDefinition) -> String {
        let rootCount = definition.trustScope.approvedRootPaths.count
        let appCount = definition.trustScope.allowedApps.count
        return "Approve workflow '\(definition.name)' to run across \(rootCount) trusted folder\(rootCount == 1 ? "" : "s") and \(appCount) app target\(appCount == 1 ? "" : "s")."
    }

    private func interpolate(_ value: String, values: [String: String]) -> String {
        values.reduce(value) { partial, pair in
            partial.replacingOccurrences(of: "{{\(pair.key)}}", with: pair.value)
        }
    }

    private func resolveStep(
        _ step: AtlasWorkflowStep,
        inputValues: [String: String]
    ) async throws -> ResolvedStepExecution {
        switch step.kind {
        case .prompt:
            return ResolvedStepExecution(
                step: step,
                interpolatedInputJSON: "{}",
                actualTrustScope: AtlasWorkflowTrustScope(),
                requiresExplicitApproval: true
            )
        case .skillAction:
            guard let skillID = step.skillID, let actionID = step.actionID else {
                throw AtlasToolError.invalidInput("Workflow step '\(step.title)' is missing skill metadata.")
            }
            guard let record = await context.skillRegistry.skill(id: skillID),
                  let action = record.actions.first(where: { $0.id == actionID }) else {
                throw AtlasToolError.invalidInput("Workflow step '\(step.title)' references an unknown skill action.")
            }
            guard action.sideEffectLevel != .destructive else {
                throw ToolExecutionError.denied("Destructive workflow steps are prohibited.")
            }

            let interpolatedInputJSON = interpolate(step.inputJSON ?? "{}", values: inputValues)
            let interpolatedTargets = stepTargets(from: interpolatedInputJSON)
            let actualAppName = normalizeScopeValue(interpolatedTargets.appName ?? step.appName)
            let actualTargetPath = normalizePathValue(interpolatedTargets.targetPath ?? step.targetPath)
            let effectiveStep = AtlasWorkflowStep(
                id: step.id,
                title: step.title,
                kind: step.kind,
                skillID: step.skillID,
                actionID: step.actionID,
                inputJSON: step.inputJSON,
                prompt: step.prompt,
                appName: actualAppName,
                targetPath: actualTargetPath,
                sideEffectLevel: action.sideEffectLevel.rawValue
            )
            return ResolvedStepExecution(
                step: effectiveStep,
                interpolatedInputJSON: interpolatedInputJSON,
                actualTrustScope: AtlasWorkflowTrustScope(
                    approvedRootPaths: actualTargetPath.map { [$0] } ?? [],
                    allowedApps: actualAppName.map { [$0] } ?? [],
                    allowsSensitiveRead: action.sideEffectLevel == .sensitiveRead,
                    allowsLiveWrite: action.sideEffectLevel == .liveWrite
                ),
                requiresExplicitApproval: action.sideEffectLevel == .sensitiveRead || action.sideEffectLevel == .liveWrite
            )
        }
    }

    private func shouldPauseBeforeStep(
        _ resolvedStep: ResolvedStepExecution,
        definition: AtlasWorkflowDefinition,
        approvedAt: Date?,
        consumedStepApproval: Bool
    ) -> Bool {
        guard definition.approvalMode == .stepByStep,
              resolvedStep.requiresExplicitApproval else {
            return false
        }
        return approvedAt == nil || consumedStepApproval
    }

    private func shouldGrantWorkflowTrust(
        to resolvedStep: ResolvedStepExecution,
        definition: AtlasWorkflowDefinition,
        approvedAt: Date?,
        consumedStepApproval: Bool
    ) -> Bool {
        guard approvedAt != nil else { return false }
        guard resolvedStep.requiresExplicitApproval else { return false }
        if definition.approvalMode == .stepByStep {
            return !consumedStepApproval
        }
        return true
    }

    private func waitingForApprovalRun(
        definition: AtlasWorkflowDefinition,
        runID: UUID,
        startedAt: Date,
        stepRuns: [AtlasWorkflowStepRun],
        inputValues: [String: String],
        conversationID: UUID?,
        assistantSummary: String?,
        approvalReason: String,
        trustScope: AtlasWorkflowTrustScope
    ) -> AtlasWorkflowRun {
        AtlasWorkflowRun(
            id: runID,
            workflowID: definition.id,
            workflowName: definition.name,
            status: .waitingForApproval,
            outcome: .waitingForApproval,
            inputValues: inputValues,
            stepRuns: stepRuns,
            approval: AtlasWorkflowApproval(
                workflowID: definition.id,
                workflowRunID: runID,
                status: .pending,
                reason: approvalReason,
                trustScope: trustScope
            ),
            assistantSummary: assistantSummary,
            errorMessage: approvalReason,
            startedAt: startedAt,
            conversationID: conversationID
        )
    }

    private func approvalTrustScope(
        for resolvedStep: ResolvedStepExecution,
        fallback: AtlasWorkflowTrustScope
    ) -> AtlasWorkflowTrustScope {
        let scope = resolvedStep.actualTrustScope
        if !scope.approvedRootPaths.isEmpty || !scope.allowedApps.isEmpty || scope.allowsSensitiveRead || scope.allowsLiveWrite {
            return scope
        }
        return fallback
    }

    private func approvalReason(
        for resolvedStep: ResolvedStepExecution,
        workflowName: String
    ) -> String {
        switch resolvedStep.step.kind {
        case .prompt:
            return "Approve prompt-driven step '\(resolvedStep.step.title)' in workflow '\(workflowName)'."
        case .skillAction:
            let appSummary = resolvedStep.step.appName.map { " app '\($0)'" } ?? ""
            let pathSummary = resolvedStep.step.targetPath.map { " path '\($0)'" } ?? ""
            return "Approve step '\(resolvedStep.step.title)' in workflow '\(workflowName)' for\(appSummary)\(pathSummary)."
        }
    }

    private func validateWorkflowTrustCoverage(
        for resolvedStep: ResolvedStepExecution,
        workflowTrustScope: AtlasWorkflowTrustScope
    ) throws {
        guard resolvedStep.requiresExplicitApproval else { return }

        if let appName = resolvedStep.step.appName,
           !workflowTrustScope.allowedApps.contains(where: { $0.caseInsensitiveCompare(appName) == .orderedSame }) {
            throw ToolExecutionError.denied("Workflow trust scope does not cover app '\(appName)' for step '\(resolvedStep.step.title)'.")
        }

        if let targetPath = resolvedStep.step.targetPath,
           !workflowTrustScope.approvedRootPaths.contains(where: { pathWithinApprovedRoot(targetPath, approvedRoot: $0) }) {
            throw ToolExecutionError.denied("Workflow trust scope does not cover path '\(targetPath)' for step '\(resolvedStep.step.title)'.")
        }
    }

    private func pathWithinApprovedRoot(_ path: String, approvedRoot: String) -> Bool {
        let normalizedPath = normalizePathValue(path) ?? path
        let normalizedRoot = normalizePathValue(approvedRoot) ?? approvedRoot

        if normalizedPath == normalizedRoot {
            return true
        }

        if normalizedPath.hasPrefix("/"), normalizedRoot.hasPrefix("/") {
            let rootURL = URL(fileURLWithPath: normalizedRoot).standardizedFileURL
            let pathURL = URL(fileURLWithPath: normalizedPath).standardizedFileURL
            let rootComponents = rootURL.pathComponents
            let pathComponents = pathURL.pathComponents
            guard pathComponents.count >= rootComponents.count else { return false }
            return Array(pathComponents.prefix(rootComponents.count)) == rootComponents
        }

        return normalizedPath.hasPrefix(normalizedRoot + "/")
    }

    private func stepTargets(from inputJSON: String) -> (appName: String?, targetPath: String?) {
        guard let args = try? AtlasToolInput(argumentsJSON: inputJSON).dictionary() else {
            return (nil, nil)
        }
        return (
            args["appName"],
            args["path"] ??
                args["rootPath"] ??
                args["targetPath"] ??
                args["filePath"] ??
                args["directoryPath"] ??
                args["folderPath"]
        )
    }

    private func normalizeScopeValue(_ value: String?) -> String? {
        guard let trimmed = value?.trimmingCharacters(in: .whitespacesAndNewlines),
              !trimmed.isEmpty else {
            return nil
        }
        return trimmed
    }

    private func normalizePathValue(_ value: String?) -> String? {
        guard let normalized = normalizeScopeValue(value) else {
            return nil
        }
        guard normalized.hasPrefix("/") else {
            return normalized
        }
        return URL(fileURLWithPath: normalized).standardizedFileURL.path
    }

    private func markStep(
        _ existingStepRuns: [AtlasWorkflowStepRun],
        stepID: String,
        title: String,
        status: AtlasWorkflowStepStatus,
        startedAt: Date?,
        finishedAt: Date? = nil,
        output: String? = nil,
        errorMessage: String? = nil
    ) -> [AtlasWorkflowStepRun] {
        var updated = existingStepRuns
        if let index = updated.firstIndex(where: { $0.stepID == stepID }) {
            let original = updated[index]
            updated[index] = AtlasWorkflowStepRun(
                id: original.id,
                stepID: stepID,
                title: title,
                status: status,
                output: output,
                errorMessage: errorMessage,
                startedAt: startedAt ?? original.startedAt,
                finishedAt: finishedAt
            )
        } else {
            updated.append(
                AtlasWorkflowStepRun(
                    stepID: stepID,
                    title: title,
                    status: status,
                    output: output,
                    errorMessage: errorMessage,
                    startedAt: startedAt,
                    finishedAt: finishedAt
                )
            )
        }
        return updated
    }
}
