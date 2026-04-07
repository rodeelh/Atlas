import Foundation
import AtlasLogging
import AtlasShared
import AtlasTools

public struct AtlasInfoSkill: AtlasSkill {
    public let manifest: SkillManifest
    public let actions: [SkillActionDefinition]

    public init() {
        self.manifest = SkillManifest(
            id: "atlas.info",
            name: "Atlas Info",
            version: "1.0.0",
            description: "Read-only operational summaries about the Atlas runtime and active skills.",
            category: .system,
            lifecycleState: .installed,
            capabilities: [
                .runtimeStatus,
                .skillCatalog,
                .capabilitySummary,
                .serviceConnectivity
            ],
            requiredPermissions: [
                .runtimeRead,
                .skillRegistryRead
            ],
            riskLevel: .low,
            trustProfile: .operational,
            freshnessType: .local,
            preferredQueryTypes: [.runtimeStatus, .skillsStatus, .approvalsStatus, .systemStatus],
            routingPriority: 60,
            canHandleLocalData: true,
            restrictionsSummary: [],
            supportsReadOnlyMode: true,
            isUserVisible: true,
            isEnabledByDefault: true,
            author: "Project Atlas",
            source: "built_in",
            tags: ["runtime", "status", "skills"],
            intent: .atlasSystemTask,
            triggers: [
                .init("how many conversations", queryType: .runtimeStatus),
                .init("is atlas running", queryType: .runtimeStatus),
                .init("atlas runtime", queryType: .runtimeStatus),
                .init("atlas status", queryType: .runtimeStatus),
                .init("runtime status", queryType: .runtimeStatus),
                .init("pending approvals", queryType: .approvalsStatus),
                .init("approvals status", queryType: .approvalsStatus),
                .init("skills enabled", queryType: .skillsStatus),
                .init("skills disabled", queryType: .skillsStatus),
                .init("skill status", queryType: .skillsStatus),
                .init("what skills", queryType: .skillsStatus),
                .init("which skills", queryType: .skillsStatus),
                .init("telegram status", queryType: .runtimeStatus),
                .init("openai status", queryType: .runtimeStatus),
                .init("bot status", queryType: .runtimeStatus),
                .init("atlas info", queryType: .runtimeStatus)
            ],
            alwaysInclude: true
        )

        self.actions = [
            SkillActionDefinition(
                id: "get_runtime_status",
                name: "Get Runtime Status",
                description: "Returns the current Atlas runtime health and connection summary.",
                inputSchemaSummary: "No input.",
                outputSchemaSummary: "Runtime state, port, activity, and pending approvals summary.",
                permissionLevel: .read,
                sideEffectLevel: .safeRead,

                isEnabled: true,
                preferredQueryTypes: [.runtimeStatus, .systemStatus],
                routingPriority: 40,
                inputSchema: AtlasToolInputSchema(properties: [:])
            ),
            SkillActionDefinition(
                id: "list_enabled_skills",
                name: "List Enabled Skills",
                description: "Lists the Atlas skills that are currently enabled.",
                inputSchemaSummary: "No input.",
                outputSchemaSummary: "A readable list of enabled skill names and lifecycle states.",
                permissionLevel: .read,
                sideEffectLevel: .safeRead,

                isEnabled: true,
                preferredQueryTypes: [.skillsStatus],
                routingPriority: 35,
                inputSchema: AtlasToolInputSchema(properties: [:])
            ),
            SkillActionDefinition(
                id: "summarize_capabilities",
                name: "Summarize Capabilities",
                description: "Summarizes the active Atlas capability surface in concise operational language.",
                inputSchemaSummary: "No input.",
                outputSchemaSummary: "A concise capability summary.",
                permissionLevel: .read,
                sideEffectLevel: .safeRead,

                isEnabled: true,
                preferredQueryTypes: [.skillsStatus, .systemStatus],
                routingPriority: 20,
                inputSchema: AtlasToolInputSchema(properties: [:])
            )
        ]
    }

    public func validateConfiguration(context: SkillValidationContext) async -> SkillValidationResult {
        SkillValidationResult(
            skillID: manifest.id,
            status: .passed,
            summary: "Built-in runtime introspection is available.",
            issues: [],
            validatedAt: .now
        )
    }

    public func execute(
        actionID: String,
        input: AtlasToolInput,
        context: SkillExecutionContext
    ) async throws -> SkillExecutionResult {
        switch actionID {
        case "get_runtime_status":
            return await runtimeStatusResult(context: context)
        case "list_enabled_skills":
            return await enabledSkillsResult(context: context)
        case "summarize_capabilities":
            return await capabilitySummaryResult(context: context)
        default:
            throw AtlasToolError.invalidInput("The action '\(actionID)' is not supported by Atlas Info.")
        }
    }

    private func runtimeStatusResult(context: SkillExecutionContext) async -> SkillExecutionResult {
        let status = await context.runtimeStatusProvider()
        let output: String

        if let status {
            output = """
            Runtime state: \(status.state.rawValue)
            Port: \(status.runtimePort)
            Active requests: \(status.activeRequests)
            Pending approvals: \(status.pendingApprovalCount)
            Telegram: \(status.telegram.pollingActive ? "active" : "inactive")
            """
        } else {
            output = "Runtime status is currently unavailable."
        }

        return SkillExecutionResult(
            skillID: manifest.id,
            actionID: "get_runtime_status",
            output: output,
            summary: "Returned the current runtime status."
        )
    }

    private func enabledSkillsResult(context: SkillExecutionContext) async -> SkillExecutionResult {
        let enabledSkills = await context.enabledSkillsProvider()
        let output: String

        if enabledSkills.isEmpty {
            output = "No Atlas skills are currently enabled."
        } else {
            let lines = enabledSkills.map { "\($0.manifest.name) (\($0.manifest.lifecycleState.rawValue))" }
            output = lines.joined(separator: "\n")
        }

        return SkillExecutionResult(
            skillID: manifest.id,
            actionID: "list_enabled_skills",
            output: output,
            summary: "Listed enabled Atlas skills.",
            metadata: ["count": "\(enabledSkills.count)"]
        )
    }

    private func capabilitySummaryResult(context: SkillExecutionContext) async -> SkillExecutionResult {
        let enabledSkills = await context.enabledSkillsProvider()
        let capabilities = enabledSkills
            .flatMap(\.manifest.capabilities)
            .map(\.rawValue)
            .sorted()

        let uniqueCapabilities = capabilities.reduce(into: [String]()) { partialResult, capability in
            if partialResult.contains(capability) == false {
                partialResult.append(capability)
            }
        }

        let output = uniqueCapabilities.isEmpty
            ? "Atlas is running with a minimal capability surface."
            : "Atlas can currently help with: \(uniqueCapabilities.joined(separator: ", "))."

        return SkillExecutionResult(
            skillID: manifest.id,
            actionID: "summarize_capabilities",
            output: output,
            summary: "Summarized the active Atlas capability surface.",
            metadata: ["capability_count": "\(uniqueCapabilities.count)"]
        )
    }
}
