import AtlasNetwork
import AtlasShared

public struct OpenAIBridge: Sendable {
    public init() {}

    public func atlasResponse(
        from payload: OpenAIResponsesCreateResponse,
        permissionLevels: [String: PermissionLevel] = [:],
        approvalRequirements: [String: Bool] = [:]
    ) -> AtlasAgentResponse {
        let assistantMessage = payload.latestAssistantMessageText()

        return AtlasAgentResponse(
            assistantMessage: assistantMessage.isEmpty
                ? "The OpenAI adapter returned an empty response."
                : assistantMessage,
            toolCalls: atlasToolCalls(
                from: payload,
                permissionLevels: permissionLevels,
                approvalRequirements: approvalRequirements
            ),
            status: .completed
        )
    }

    public func atlasToolCalls(
        from payload: OpenAIResponsesCreateResponse,
        permissionLevels: [String: PermissionLevel] = [:],
        approvalRequirements: [String: Bool] = [:]
    ) -> [AtlasToolCall] {
        payload.output.compactMap { item in
            guard item.type == "function_call", let name = item.name else {
                return nil
            }

            return AtlasToolCall(
                toolName: name,
                argumentsJSON: item.arguments ?? "{}",
                permissionLevel: permissionLevels[name] ?? .read,
                requiresApproval: approvalRequirements[name] ?? false,
                status: .pending,
                openAICallID: item.callID
            )
        }
    }
}
