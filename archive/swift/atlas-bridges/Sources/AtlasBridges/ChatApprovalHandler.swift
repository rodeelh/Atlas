import Foundation
import AtlasShared

public enum ChatApprovalOutcome: Sendable {
    case approved(assistantMessage: String, toolName: String)
    case denied(toolName: String)
    case failed(toolName: String, error: String)
    case stillPending(toolName: String, pendingApprovals: [ApprovalRequest])
}

/// Shared approval resolution logic used by all chat bridges.
public struct ChatApprovalHandler: Sendable {

    public init() {}

    public func resolve(
        toolCallID: UUID,
        approve: Bool,
        runtime: any AtlasRuntimeHandling
    ) async -> ChatApprovalOutcome {
        if approve {
            do {
                let envelope = try await runtime.approve(toolCallID: toolCallID)
                let toolName = envelope.response.pendingApprovals.first?.toolCall.toolName
                    ?? envelope.response.toolCalls.first?.toolName
                    ?? "action"
                let name = formatToolName(toolName)
                switch envelope.response.status {
                case .completed:
                    let raw = envelope.response.assistantMessage.trimmingCharacters(in: .whitespacesAndNewlines)
                    let reply = strippedApprovalSuffix(raw).trimmingCharacters(in: .whitespacesAndNewlines)
                    return .approved(assistantMessage: reply, toolName: name)
                case .failed:
                    let reason = envelope.response.errorMessage ?? "unknown error"
                    return .failed(toolName: name, error: reason)
                case .waitingForApproval:
                    return .stillPending(toolName: name, pendingApprovals: envelope.response.pendingApprovals)
                }
            } catch {
                return .failed(toolName: "action", error: error.localizedDescription)
            }
        } else {
            do {
                let request = try await runtime.deny(toolCallID: toolCallID)
                return .denied(toolName: formatToolName(request.toolCall.toolName))
            } catch {
                return .failed(toolName: "action", error: error.localizedDescription)
            }
        }
    }

    public func listPending(
        conversationID: UUID,
        runtime: any AtlasRuntimeHandling
    ) async -> [ApprovalRequest] {
        await runtime.approvals()
            .filter { $0.conversationID == conversationID && ($0.status == .pending || $0.deferredExecutionStatus == .failed) }
    }

    /// Strips "N tool approval(s) is/are pending." suffixes that the agent loop appends.
    /// These are meaningless once the approval has been acted on.
    private func strippedApprovalSuffix(_ message: String) -> String {
        let paragraphs = message.components(separatedBy: "\n\n")
        guard let last = paragraphs.last else { return message }
        let isSuffix = last.hasSuffix("tool approval is pending.")
            || last.hasSuffix("tool approvals are pending.")
            || last.hasPrefix("Atlas requires approval")
        guard isSuffix else { return message }
        return paragraphs.dropLast().joined(separator: "\n\n").trimmingCharacters(in: .whitespacesAndNewlines)
    }

    private func formatToolName(_ toolName: String) -> String {
        let parts = toolName.components(separatedBy: "__")
        let raw = parts.last ?? toolName
        return raw
            .components(separatedBy: "_")
            .filter { !$0.isEmpty }
            .map { $0.prefix(1).uppercased() + $0.dropFirst() }
            .joined(separator: " ")
    }
}
