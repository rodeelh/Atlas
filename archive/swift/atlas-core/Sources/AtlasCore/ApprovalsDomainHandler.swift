import Foundation
import NIOHTTP1
import AtlasShared

// MARK: - ApprovalsDomainHandler

/// Handles approval and action-policy routes.
///
/// Routes owned:
///   GET    /approvals
///   POST   /approvals/:toolCallID/approve
///   POST   /approvals/:toolCallID/deny
///   GET    /action-policies
///   PUT    /action-policies/:actionID
///   POST   /action-policies/:actionID
struct ApprovalsDomainHandler: RuntimeDomainHandler {
    let runtime: AgentRuntime

    func handle(
        method: HTTPMethod,
        path: String,
        queryItems: [String: String],
        body: String,
        headers: HTTPHeaders
    ) async throws -> EncodedResponse? {
        switch (method, path) {
        case (.GET, "/approvals"):
            let approvals = await runtime.approvals()
            return try EncodedResponse(status: .ok, payload: AtlasJSON.encoder.encode(approvals))

        case (.GET, "/action-policies"):
            let policies = await runtime.actionPolicies()
            return try EncodedResponse(status: .ok, payload: AtlasJSON.encoder.encode(policies))

        default:
            break
        }

        // POST /approvals/:toolCallID/approve|deny
        if let approvalResponse = try await routeApprovalAction(method: method, path: path) {
            return approvalResponse
        }

        // PUT|POST /action-policies/:actionID
        if let policyResponse = try await routeActionPolicyAction(method: method, path: path, body: body) {
            return policyResponse
        }

        return nil
    }

    // MARK: - Approval sub-routes

    private func routeApprovalAction(method: HTTPMethod, path: String) async throws -> EncodedResponse? {
        let components = path.split(separator: "/").map(String.init)
        guard
            method == .POST,
            components.count == 3,
            components[0] == "approvals",
            let id = UUID(uuidString: components[1])
        else {
            return nil
        }

        switch components[2] {
        case "approve":
            let envelope = try await runtime.approve(toolCallID: id)
            return try EncodedResponse(status: .ok, payload: AtlasJSON.encoder.encode(envelope))
        case "deny":
            let approval = try await runtime.deny(toolCallID: id)
            return try EncodedResponse(status: .ok, payload: AtlasJSON.encoder.encode(approval))
        default:
            return nil
        }
    }

    // MARK: - Action policy sub-routes

    private func routeActionPolicyAction(
        method: HTTPMethod,
        path: String,
        body: String
    ) async throws -> EncodedResponse? {
        let components = path.split(separator: "/").map(String.init)

        guard components.count == 2, components[0] == "action-policies" else {
            return nil
        }

        let actionID = (components[1].removingPercentEncoding ?? components[1])

        switch method {
        case .PUT, .POST:
            struct SetPolicyRequest: Codable { let policy: ActionApprovalPolicy }
            let request = try AtlasJSON.decoder.decode(SetPolicyRequest.self, from: Data(body.utf8))
            await runtime.setActionPolicy(request.policy, for: actionID)
            let policies = await runtime.actionPolicies()
            return try EncodedResponse(status: .ok, payload: AtlasJSON.encoder.encode(policies))
        default:
            return nil
        }
    }
}
