import Foundation
import AtlasShared
import AtlasSkills

public struct WorkflowPlanner: Sendable {
    public init() {}

    public static func derivedTrustScope(for steps: [AtlasWorkflowStep]) -> AtlasWorkflowTrustScope {
        AtlasWorkflowTrustScope(
            approvedRootPaths: Array(Set(steps.compactMap(\.targetPath))).sorted(),
            allowedApps: Array(Set(steps.compactMap(\.appName))).sorted(),
            allowsSensitiveRead: steps.contains(where: { $0.sideEffectLevel == "sensitive_read" }),
            allowsLiveWrite: steps.contains(where: { $0.sideEffectLevel == "live_write" })
        )
    }

    public func definitionFromToolHistory(
        name: String,
        description: String,
        promptTemplate: String,
        conversationID: UUID?,
        toolCalls: [AtlasToolCall],
        enabledSkills: [AtlasSkillRecord]
    ) -> AtlasWorkflowDefinition {
        let steps = toolCalls.enumerated().compactMap { index, call in
            stepFromToolCall(call, index: index, enabledSkills: enabledSkills)
        }
        let trustScope = trustScopeForSteps(steps)

        return AtlasWorkflowDefinition(
            id: slugify(name),
            name: name,
            description: description,
            promptTemplate: promptTemplate,
            tags: inferredTags(from: steps, prompt: promptTemplate),
            steps: steps,
            trustScope: trustScope,
            approvalMode: .workflowBoundary,
            sourceConversationID: conversationID
        )
    }

    private func stepFromToolCall(
        _ call: AtlasToolCall,
        index: Int,
        enabledSkills: [AtlasSkillRecord]
    ) -> AtlasWorkflowStep? {
        guard call.toolName.hasPrefix("skill__") else { return nil }
        let trimmed = String(call.toolName.dropFirst("skill__".count))
        let parts = trimmed.split(separator: "__", maxSplits: 1).map(String.init)
        guard parts.count == 2 else { return nil }

        let skillID = parts[0]
        let actionID = parts[1].replacingOccurrences(of: "__", with: ".")
        let action = enabledSkills.first(where: { $0.id == skillID })?.actions.first(where: { $0.id == actionID })
        let arguments = (try? call.input.dictionary()) ?? [:]

        return AtlasWorkflowStep(
            id: "step-\(index + 1)",
            title: action?.name ?? actionID,
            kind: .skillAction,
            skillID: skillID,
            actionID: actionID,
            inputJSON: call.argumentsJSON,
            appName: arguments["appName"],
            targetPath: arguments["path"] ?? arguments["rootPath"],
            sideEffectLevel: action?.sideEffectLevel.rawValue
        )
    }

    private func trustScopeForSteps(_ steps: [AtlasWorkflowStep]) -> AtlasWorkflowTrustScope {
        Self.derivedTrustScope(for: steps)
    }

    private func inferredTags(from steps: [AtlasWorkflowStep], prompt: String) -> [String] {
        var tags = Set<String>()
        if steps.contains(where: { $0.skillID == "file-system" }) { tags.insert("files") }
        if steps.contains(where: { $0.skillID == "system-actions" }) { tags.insert("system") }
        if steps.contains(where: { $0.skillID == "applescript-automation" }) { tags.insert("apps") }
        if prompt.localizedCaseInsensitiveContains("automation") { tags.insert("automation") }
        return tags.sorted()
    }

    private func slugify(_ value: String) -> String {
        value.lowercased()
            .replacingOccurrences(of: " ", with: "-")
            .filter { $0.isLetter || $0.isNumber || $0 == "-" }
    }
}
