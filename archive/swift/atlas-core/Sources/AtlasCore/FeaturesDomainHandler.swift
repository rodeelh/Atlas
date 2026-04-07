import Foundation
import NIOHTTP1
import AtlasShared
import AtlasSkills

// MARK: - FeaturesDomainHandler

/// Handles skills, forge, automations, workflows, dashboards, and file-access routes.
///
/// Routes owned:
///   GET    /skills
///   GET    /skills/file-system/roots
///   POST   /skills/file-system/roots
///   POST   /skills/file-system/roots/:id/remove
///   POST   /skills/:id/enable
///   POST   /skills/:id/disable
///   POST   /skills/:id/validate
///   GET    /forge/researching
///   GET    /forge/proposals
///   POST   /forge/proposals
///   GET    /forge/installed
///   POST   /forge/installed/:skillID/uninstall
///   POST   /forge/proposals/:id/install
///   POST   /forge/proposals/:id/install-enable
///   POST   /forge/proposals/:id/reject
///   GET    /automations
///   POST   /automations
///   GET    /automations/file
///   PUT    /automations/file
///   GET    /automations/:id
///   PUT    /automations/:id
///   DELETE /automations/:id
///   GET    /automations/:id/runs
///   POST   /automations/:id/enable
///   POST   /automations/:id/disable
///   POST   /automations/:id/run
///   GET    /workflows
///   POST   /workflows
///   GET    /workflows/runs
///   GET    /workflows/:id
///   PUT    /workflows/:id
///   DELETE /workflows/:id
///   GET    /workflows/:id/runs
///   POST   /workflows/:id/run
///   POST   /workflows/runs/:runID/approve
///   POST   /workflows/runs/:runID/deny
///   GET    /dashboards/proposals
///   POST   /dashboards/proposals
///   POST   /dashboards/install
///   POST   /dashboards/reject
///   GET    /dashboards/installed
///   DELETE /dashboards/installed
///   POST   /dashboards/access
///   POST   /dashboards/pin
///   POST   /dashboards/widgets/execute
///   GET    /api-validation/history
struct FeaturesDomainHandler: RuntimeDomainHandler {
    let runtime: AgentRuntime

    func handle(
        method: HTTPMethod,
        path: String,
        queryItems: [String: String],
        body: String,
        headers: HTTPHeaders
    ) async throws -> EncodedResponse? {
        switch (method, path) {
        case (.GET, "/skills"):
            let skills = await runtime.skills()
            return try EncodedResponse(status: .ok, payload: AtlasJSON.encoder.encode(skills))

        case (.GET, "/skills/file-system/roots"):
            let roots = await runtime.fileAccessRoots()
            return try EncodedResponse(status: .ok, payload: AtlasJSON.encoder.encode(roots))

        case (.POST, "/skills/file-system/roots"):
            let request = try AtlasJSON.decoder.decode(FileAccessRootGrantRequest.self, from: Data(body.utf8))
            let root = try await runtime.addFileAccessRoot(bookmarkData: request.bookmarkData)
            return try EncodedResponse(status: .ok, payload: AtlasJSON.encoder.encode(root))

        default:
            break
        }

        // GET /api-validation/history
        if method == .GET, path == "/api-validation/history" {
            let limit = queryItems["limit"].flatMap { Int($0) } ?? 50
            let records = await runtime.listAPIValidationHistory(limit: limit)
            return try EncodedResponse(status: .ok, payload: AtlasJSON.encoder.encode(records))
        }

        if let skillResponse = try await routeSkillAction(method: method, path: path) {
            return skillResponse
        }

        if let forgeResponse = try await routeForgeAction(method: method, path: path, body: body) {
            return forgeResponse
        }

        if let automationResponse = try await routeAutomationAction(method: method, path: path, body: body) {
            return automationResponse
        }

        if let workflowResponse = try await routeWorkflowAction(method: method, path: path, body: body) {
            return workflowResponse
        }

        if let dashboardResponse = try await routeDashboardAction(method: method, path: path, body: body) {
            return dashboardResponse
        }

        return nil
    }

    // MARK: - Skills sub-routes

    private func routeSkillAction(method: HTTPMethod, path: String) async throws -> EncodedResponse? {
        let components = path.split(separator: "/").map(String.init)

        // POST /skills/file-system/roots/:id/remove
        if method == .POST,
           components.count == 5,
           components[0] == "skills",
           components[1] == "file-system",
           components[2] == "roots",
           let id = UUID(uuidString: components[3]),
           components[4] == "remove" {
            let removed = try await runtime.removeFileAccessRoot(id: id)
            return try EncodedResponse(status: .ok, payload: AtlasJSON.encoder.encode(removed))
        }

        guard
            method == .POST,
            components.count == 3,
            components[0] == "skills"
        else {
            return nil
        }

        let skillID = components[1].removingPercentEncoding ?? components[1]

        switch components[2] {
        case "enable":
            let record = try await runtime.enableSkill(id: skillID)
            return try EncodedResponse(status: .ok, payload: AtlasJSON.encoder.encode(record))
        case "disable":
            let record = try await runtime.disableSkill(id: skillID)
            return try EncodedResponse(status: .ok, payload: AtlasJSON.encoder.encode(record))
        case "validate":
            let record = try await runtime.validateSkill(id: skillID)
            return try EncodedResponse(status: .ok, payload: AtlasJSON.encoder.encode(record))
        default:
            return nil
        }
    }

    // MARK: - Forge routes

    private func routeForgeAction(
        method: HTTPMethod,
        path: String,
        body: String
    ) async throws -> EncodedResponse? {
        let components = path.split(separator: "/").map(String.init)
        guard !components.isEmpty, components[0] == "forge" else { return nil }

        if method == .GET, components.count == 2, components[1] == "researching" {
            let items = await runtime.forgeResearching()
            return try EncodedResponse(status: .ok, payload: AtlasJSON.encoder.encode(items))
        }

        if method == .GET, components.count == 2, components[1] == "proposals" {
            let proposals = try await runtime.forgeProposals()
            return try EncodedResponse(status: .ok, payload: AtlasJSON.encoder.encode(proposals))
        }

        if method == .POST, components.count == 2, components[1] == "proposals" {
            struct CreateRequest: Decodable {
                let spec: ForgeSkillSpec
                let plans: [ForgeActionPlan]
                let summary: String
                let rationale: String?
                let contractJSON: String?
            }
            let req = try AtlasJSON.decoder.decode(CreateRequest.self, from: Data(body.utf8))
            let proposal = try await runtime.forgeCreateProposal(
                spec: req.spec,
                plans: req.plans,
                summary: req.summary,
                rationale: req.rationale,
                contractJSON: req.contractJSON
            )
            return try EncodedResponse(status: .ok, payload: AtlasJSON.encoder.encode(proposal))
        }

        if method == .GET, components.count == 2, components[1] == "installed" {
            let skills = await runtime.forgeInstalledSkills()
            return try EncodedResponse(status: .ok, payload: AtlasJSON.encoder.encode(skills))
        }

        if method == .POST,
           components.count == 4,
           components[1] == "installed",
           components[3] == "uninstall" {
            let skillID = components[2]
            try await runtime.forgeUninstallSkill(skillID: skillID)
            struct UninstallResult: Encodable { let skillID: String; let uninstalled: Bool }
            return try EncodedResponse(status: .ok, payload: AtlasJSON.encoder.encode(
                UninstallResult(skillID: skillID, uninstalled: true)
            ))
        }

        if method == .POST,
           components.count == 4,
           components[1] == "proposals",
           let id = UUID(uuidString: components[2]) {
            switch components[3] {
            case "install":
                let proposal = try await runtime.forgeApproveProposal(id: id, enable: false)
                return try EncodedResponse(status: .ok, payload: AtlasJSON.encoder.encode(proposal))
            case "install-enable":
                let proposal = try await runtime.forgeApproveProposal(id: id, enable: true)
                return try EncodedResponse(status: .ok, payload: AtlasJSON.encoder.encode(proposal))
            case "reject":
                let proposal = try await runtime.forgeRejectProposal(id: id)
                return try EncodedResponse(status: .ok, payload: AtlasJSON.encoder.encode(proposal))
            default:
                return nil
            }
        }

        return nil
    }

    // MARK: - Automation routes

    private func routeAutomationAction(
        method: HTTPMethod,
        path: String,
        body: String
    ) async throws -> EncodedResponse? {
        let components = path.split(separator: "/").map(String.init)
        guard !components.isEmpty, components[0] == "automations" else { return nil }

        if method == .GET, components.count == 1 {
            let items = await runtime.automations()
            return try EncodedResponse(status: .ok, payload: AtlasJSON.encoder.encode(items))
        }

        if method == .POST, components.count == 1 {
            let item = try AtlasJSON.decoder.decode(GremlinItem.self, from: Data(body.utf8))
            let created = try await runtime.createAutomation(item)
            return try EncodedResponse(status: .ok, payload: AtlasJSON.encoder.encode(created))
        }

        if method == .GET, components.count == 2, components[1] == "file" {
            let raw = await runtime.automationsRawMarkdown()
            struct FileResponse: Encodable { let content: String }
            return try EncodedResponse(status: .ok, payload: AtlasJSON.encoder.encode(FileResponse(content: raw)))
        }

        if method == .PUT, components.count == 2, components[1] == "file" {
            struct FileRequest: Decodable { let content: String }
            let req = try AtlasJSON.decoder.decode(FileRequest.self, from: Data(body.utf8))
            try await runtime.writeAutomationsMarkdown(req.content)
            return EncodedResponse(status: .ok, payload: Data("{}".utf8))
        }

        guard components.count >= 2 else { return nil }
        let gremlinID = components[1]

        if method == .GET, components.count == 2 {
            guard let item = await runtime.automation(id: gremlinID) else {
                throw RuntimeAPIError.notFound("Automation not found.")
            }
            return try EncodedResponse(status: .ok, payload: AtlasJSON.encoder.encode(item))
        }

        if method == .PUT, components.count == 2 {
            let item = try AtlasJSON.decoder.decode(GremlinItem.self, from: Data(body.utf8))
            let updated = try await runtime.updateAutomation(item)
            return try EncodedResponse(status: .ok, payload: AtlasJSON.encoder.encode(updated))
        }

        if method == .DELETE, components.count == 2 {
            try await runtime.deleteAutomation(id: gremlinID)
            return EncodedResponse(status: .ok, payload: Data("{}".utf8))
        }

        guard components.count == 3 else { return nil }
        let action = components[2]

        if method == .GET, action == "runs" {
            let runs = await runtime.automationRuns(gremlinID: gremlinID)
            return try EncodedResponse(status: .ok, payload: AtlasJSON.encoder.encode(runs))
        }

        if method == .POST, action == "enable" {
            guard let existing = await runtime.automation(id: gremlinID) else {
                throw RuntimeAPIError.notFound("Automation not found.")
            }
            let updated = GremlinItem(
                id: existing.id, name: existing.name, emoji: existing.emoji,
                prompt: existing.prompt, scheduleRaw: existing.scheduleRaw,
                isEnabled: true, sourceType: existing.sourceType, createdAt: existing.createdAt,
                workflowID: existing.workflowID, workflowInputValues: existing.workflowInputValues,
                communicationDestination: existing.communicationDestination,
                telegramChatID: existing.telegramChatID
            )
            let result = try await runtime.updateAutomation(updated)
            return try EncodedResponse(status: .ok, payload: AtlasJSON.encoder.encode(result))
        }

        if method == .POST, action == "disable" {
            guard let existing = await runtime.automation(id: gremlinID) else {
                throw RuntimeAPIError.notFound("Automation not found.")
            }
            let updated = GremlinItem(
                id: existing.id, name: existing.name, emoji: existing.emoji,
                prompt: existing.prompt, scheduleRaw: existing.scheduleRaw,
                isEnabled: false, sourceType: existing.sourceType, createdAt: existing.createdAt,
                workflowID: existing.workflowID, workflowInputValues: existing.workflowInputValues,
                communicationDestination: existing.communicationDestination,
                telegramChatID: existing.telegramChatID
            )
            let result = try await runtime.updateAutomation(updated)
            return try EncodedResponse(status: .ok, payload: AtlasJSON.encoder.encode(result))
        }

        if method == .POST, action == "run" {
            let run = try await runtime.runAutomationNow(id: gremlinID)
            return try EncodedResponse(status: .ok, payload: AtlasJSON.encoder.encode(run))
        }

        return nil
    }

    // MARK: - Workflow routes

    private func routeWorkflowAction(
        method: HTTPMethod,
        path: String,
        body: String
    ) async throws -> EncodedResponse? {
        let components = path.split(separator: "/").map(String.init)
        guard !components.isEmpty, components[0] == "workflows" else { return nil }

        if method == .GET, components.count == 1 {
            let definitions = await runtime.workflows()
            return try EncodedResponse(status: .ok, payload: AtlasJSON.encoder.encode(definitions))
        }

        if method == .POST, components.count == 1 {
            let definition = try AtlasJSON.decoder.decode(AtlasWorkflowDefinition.self, from: Data(body.utf8))
            let created = try await runtime.createWorkflow(definition)
            return try EncodedResponse(status: .ok, payload: AtlasJSON.encoder.encode(created))
        }

        if method == .GET, components.count == 2, components[1] == "runs" {
            let runs = await runtime.workflowRuns()
            return try EncodedResponse(status: .ok, payload: AtlasJSON.encoder.encode(runs))
        }

        if method == .POST, components.count == 4, components[1] == "runs", components[3] == "approve" {
            guard let runID = UUID(uuidString: components[2]) else {
                throw RuntimeAPIError.invalidRequest("Invalid workflow run ID.")
            }
            let run = try await runtime.approveWorkflowRun(id: runID)
            return try EncodedResponse(status: .ok, payload: AtlasJSON.encoder.encode(run))
        }

        if method == .POST, components.count == 4, components[1] == "runs", components[3] == "deny" {
            guard let runID = UUID(uuidString: components[2]) else {
                throw RuntimeAPIError.invalidRequest("Invalid workflow run ID.")
            }
            let run = try await runtime.denyWorkflowRun(id: runID)
            return try EncodedResponse(status: .ok, payload: AtlasJSON.encoder.encode(run))
        }

        guard components.count >= 2 else { return nil }
        let workflowID = components[1]

        if method == .GET, components.count == 2 {
            guard let definition = await runtime.workflow(id: workflowID) else {
                throw RuntimeAPIError.notFound("Workflow not found.")
            }
            return try EncodedResponse(status: .ok, payload: AtlasJSON.encoder.encode(definition))
        }

        if method == .PUT, components.count == 2 {
            let definition = try AtlasJSON.decoder.decode(AtlasWorkflowDefinition.self, from: Data(body.utf8))
            let updated = try await runtime.updateWorkflow(definition)
            return try EncodedResponse(status: .ok, payload: AtlasJSON.encoder.encode(updated))
        }

        if method == .DELETE, components.count == 2 {
            let deleted = try await runtime.deleteWorkflow(id: workflowID)
            return try EncodedResponse(status: .ok, payload: AtlasJSON.encoder.encode(deleted))
        }

        if method == .GET, components.count == 3, components[2] == "runs" {
            let runs = await runtime.workflowRuns(workflowID: workflowID)
            return try EncodedResponse(status: .ok, payload: AtlasJSON.encoder.encode(runs))
        }

        if method == .POST, components.count == 3, components[2] == "run" {
            struct WorkflowRunRequest: Decodable { let inputValues: [String: String]? }
            let req = (try? AtlasJSON.decoder.decode(WorkflowRunRequest.self, from: Data(body.utf8))) ?? WorkflowRunRequest(inputValues: nil)
            let run = try await runtime.runWorkflow(id: workflowID, inputValues: req.inputValues ?? [:])
            return try EncodedResponse(status: .ok, payload: AtlasJSON.encoder.encode(run))
        }

        return nil
    }

    // MARK: - Dashboard routes

    private func routeDashboardAction(
        method: HTTPMethod,
        path: String,
        body: String
    ) async throws -> EncodedResponse? {
        let components = path.split(separator: "/").map(String.init)
        guard !components.isEmpty, components[0] == "dashboards" else { return nil }

        if method == .GET, components.count == 2, components[1] == "proposals" {
            let proposals = await runtime.dashboardProposals()
            return try EncodedResponse(status: .ok, payload: AtlasJSON.encoder.encode(proposals))
        }

        if method == .POST, components.count == 2, components[1] == "proposals" {
            struct CreateRequest: Decodable { let intent: String; let skillIDs: [String] }
            let req = try AtlasJSON.decoder.decode(CreateRequest.self, from: Data(body.utf8))
            let proposal = try await runtime.createDashboardProposal(intent: req.intent, skillIDs: req.skillIDs)
            return try EncodedResponse(status: .ok, payload: AtlasJSON.encoder.encode(proposal))
        }

        if method == .POST, components.count == 2, components[1] == "install" {
            struct InstallRequest: Decodable { let proposalID: String }
            let req = try AtlasJSON.decoder.decode(InstallRequest.self, from: Data(body.utf8))
            let proposal = try await runtime.installDashboard(proposalID: req.proposalID)
            return try EncodedResponse(status: .ok, payload: AtlasJSON.encoder.encode(proposal))
        }

        if method == .POST, components.count == 2, components[1] == "reject" {
            struct RejectRequest: Decodable { let proposalID: String }
            let req = try AtlasJSON.decoder.decode(RejectRequest.self, from: Data(body.utf8))
            let proposal = try await runtime.rejectDashboard(proposalID: req.proposalID)
            return try EncodedResponse(status: .ok, payload: AtlasJSON.encoder.encode(proposal))
        }

        if method == .GET, components.count == 2, components[1] == "installed" {
            let dashboards = await runtime.installedDashboards()
            return try EncodedResponse(status: .ok, payload: AtlasJSON.encoder.encode(dashboards))
        }

        if method == .DELETE, components.count == 2, components[1] == "installed" {
            struct RemoveRequest: Decodable { let dashboardID: String }
            let req = try AtlasJSON.decoder.decode(RemoveRequest.self, from: Data(body.utf8))
            try await runtime.removeDashboard(dashboardID: req.dashboardID)
            struct OkResponse: Encodable { let ok: Bool }
            return try EncodedResponse(status: .ok, payload: AtlasJSON.encoder.encode(OkResponse(ok: true)))
        }

        if method == .POST, components.count == 2, components[1] == "access" {
            struct AccessRequest: Decodable { let dashboardID: String }
            let req = try AtlasJSON.decoder.decode(AccessRequest.self, from: Data(body.utf8))
            try await runtime.recordDashboardAccess(dashboardID: req.dashboardID)
            struct OkResponse: Encodable { let ok: Bool }
            return try EncodedResponse(status: .ok, payload: AtlasJSON.encoder.encode(OkResponse(ok: true)))
        }

        if method == .POST, components.count == 2, components[1] == "pin" {
            struct PinRequest: Decodable { let dashboardID: String }
            let req = try AtlasJSON.decoder.decode(PinRequest.self, from: Data(body.utf8))
            let updated = try await runtime.toggleDashboardPin(dashboardID: req.dashboardID)
            return try EncodedResponse(status: .ok, payload: AtlasJSON.encoder.encode(updated))
        }

        if method == .POST, components == ["dashboards", "widgets", "execute"] {
            struct ExecuteRequest: Decodable {
                let dashboardID: String
                let widgetID: String
                let inputs: [String: String]?
            }
            let req = try AtlasJSON.decoder.decode(ExecuteRequest.self, from: Data(body.utf8))
            let result = try await runtime.executeWidgetAction(
                dashboardID: req.dashboardID,
                widgetID: req.widgetID,
                inputs: req.inputs ?? [:]
            )
            return try EncodedResponse(status: .ok, payload: AtlasJSON.encoder.encode(result))
        }

        return nil
    }
}
