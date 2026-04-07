import Foundation
import AtlasShared

// MARK: - ChatBridge

public protocol ChatBridge: Actor {
    var platform: ChatPlatform { get }
    var persona: ChatBridgePersona { get }
    var isConnected: Bool { get async }
    func start() async throws
    func stop() async throws
    func deliverAutomationResult(destination: CommunicationDestination, emoji: String, name: String, output: String) async
    /// Proactively push an approval request to a specific chat session.
    /// Called when approvals are created outside an interactive message (e.g. Gremlin runs).
    func notifyApprovalRequired(session: ChatSession, approval: ApprovalRequest) async
}
