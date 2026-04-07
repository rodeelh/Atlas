import Foundation
import Combine
import AppKit
import UserNotifications
import AtlasLogging
import AtlasShared
import AtlasNetwork
import AtlasSkills

@MainActor
public final class AtlasAppState: ObservableObject {
    public enum NativeShellState: Equatable {
        case needsAttention
        case daemonStopped
        case ready
    }

    public enum MenuBarPrimaryAction: Equatable {
        case repairAtlas
        case startAtlas
        case openAtlas

        var title: String {
            switch self {
            case .repairAtlas:
                return "Repair Atlas"
            case .startAtlas:
                return "Start Atlas"
            case .openAtlas:
                return "Open Atlas"
            }
        }

        var systemImage: String {
            switch self {
            case .repairAtlas:
                return "wrench.and.screwdriver"
            case .startAtlas:
                return "play.fill"
            case .openAtlas:
                return "arrow.up.forward.app"
            }
        }
    }

    public struct MenuBarServiceStatus: Identifiable, Equatable {
        public enum State: Equatable {
            case ready
            case warning
            case inactive
        }

        public let id: String
        public let title: String
        public let value: String
        public let state: State
    }

    public struct OnboardingCredentialSnapshot: Equatable {
        public let openAI: CredentialAvailability
        public let anthropic: CredentialAvailability
        public let gemini: CredentialAvailability
        public let lmStudio: CredentialAvailability
        public let braveSearch: CredentialAvailability
        public let telegram: CredentialAvailability
        public let discord: CredentialAvailability
    }

    @Published public var draftMessage = ""
    @Published public var telegramEnabled = AtlasConfig().telegramEnabled
    @Published public var appearanceMode = AtlasAppearanceMode.system
    @Published public private(set) var onboardingCompleted = AtlasConfig().onboardingCompleted
    @Published public private(set) var connectionSummary = "Starting Atlas runtime..."
    @Published public private(set) var runtimeStatus: AtlasRuntimeStatus?
    @Published public private(set) var logEntries: [AtlasLogEntry] = []
    @Published public private(set) var pendingApprovals: [ApprovalRequest] = []
    @Published public private(set) var skills: [AtlasSkillRecord] = []
    @Published public private(set) var actionPolicies: [String: ActionApprovalPolicy] = [:]
    @Published public private(set) var memoryItems: [MemoryItem] = []
    @Published public private(set) var approvedFileAccessRoots: [ApprovedFileAccessRoot] = []
    @Published public private(set) var runtimeConfig: RuntimeConfigSnapshot?
    @Published public private(set) var lastAssistantResponse = "Atlas responses will appear here."
    @Published public private(set) var lastError: String?
    @Published public private(set) var isSending = false
    @Published public private(set) var isLoadingMemories = false
    @Published public private(set) var activeApprovalActionID: UUID?
    @Published public private(set) var activeSkillOperationID: String?
    @Published public private(set) var activeMemoryOperationID: UUID?
    @Published public private(set) var isManagingFileAccess = false
    @Published public private(set) var isClearingApprovals = false
    @Published public private(set) var activeCredentialOperation: AtlasCredentialKind?
    @Published public private(set) var openAIValidationState: CredentialValidationState = .notConfigured
    @Published public private(set) var anthropicValidationState: CredentialValidationState = .notConfigured
    @Published public private(set) var geminiValidationState: CredentialValidationState = .notConfigured
    @Published public private(set) var telegramValidationState: CredentialValidationState = .notConfigured
    @Published public private(set) var discordValidationState: CredentialValidationState = .notConfigured
    @Published public private(set) var lmStudioValidationState: CredentialValidationState = .notConfigured
    @Published public private(set) var slackBotValidationState: CredentialValidationState = .notConfigured
    @Published public private(set) var slackAppValidationState: CredentialValidationState = .notConfigured
    @Published public private(set) var braveSearchValidationState: CredentialValidationState = .notConfigured
    @Published public private(set) var openAIImageValidationState: CredentialValidationState = .notConfigured
    @Published public private(set) var googleImageValidationState: CredentialValidationState = .notConfigured
    @Published public private(set) var daemonInstallRequired: Bool = false
    @Published public private(set) var modelSelectorInfo: ModelSelectorInfo?
    @Published public private(set) var isRefreshingModels = false
    @Published public private(set) var restartRequired = false

    private var config: AtlasConfig
    private var apiClient: AtlasAPIClient
    private var runtimeManager: AtlasRuntimeManager
    private let logger = AtlasLogger.security
    // Stored once at init — service/account names on AtlasConfig are constants and never change
    // at runtime, so a single instance is safe for the lifetime of AtlasAppState.
    private let credentialManager: AtlasCredentialManager

    private var refreshTask: Task<Void, Never>?
    private var hasBootstrapped = false
    private var hasOpenedInitialWebUI = false
    private var notificationRelayObserver: NSObjectProtocol?
    private var hasLoadedMemories = false
    private var hasAutoValidated = false
    private var wasRunning = false   // tracks previous poll state to detect running→stopped transition
    private var currentConversationID: UUID?
    private var credentialAvailability: [AtlasCredentialKind: CredentialAvailability] = [:]

    public init(config: AtlasConfig = AtlasConfig()) {
        self.config = config
        self.telegramEnabled = config.telegramEnabled
        self.appearanceMode = .system
        self.onboardingCompleted = config.onboardingCompleted
        self.apiClient = AtlasAPIClient(config: config)
        self.runtimeManager = AtlasRuntimeManager(config: config)
        self.credentialManager = AtlasCredentialManager(config: config)

        // Migrate any pre-bundle individual keychain items to the single JSON bundle entry.
        // Runs once on first launch after the update; subsequent calls are instant no-ops.
        Task.detached(priority: .utility) {
            KeychainSecretStore.migrateFromLegacyItemsIfNeeded()
        }

        setupNotificationRelay()
        Task {
            let sharedSnapshot = await AtlasConfigStore.shared.load()
            onboardingCompleted = sharedSnapshot.onboardingCompleted
            normalizeAppearanceModeIfNeeded()
            await refreshCredentialStates()
        }
    }

    /// Installs and starts the Atlas daemon. Call from the onboarding wizard.
    /// Does NOT clear `daemonInstallRequired` — the wizard stays open so the user
    /// can complete the remaining steps. `markOnboardingCompleted()` clears it on Done.
    public func installDaemon() async {
        do {
            let actualPort = try await runtimeManager.installAndStart()
            await syncRuntimePort(actualPort)
            connectionSummary = "Connected to localhost runtime."
            lastError = nil
            hasBootstrapped = false   // allow bootstrap to re-run now that daemon is up
            await bootstrap()
            autoOpenWebOnboardingIfNeeded()
        } catch {
            connectionSummary = "Daemon install failed."
            lastError = error.localizedDescription
        }
    }

    public func bootstrap() async {
        guard !hasBootstrapped else { return }
        hasBootstrapped = true

        let state = await runtimeManager.checkDaemonState()
        switch state {
        case .running:
            await syncRuntimePort(await runtimeManager.currentPort())
            daemonInstallRequired = false
            connectionSummary = "Connected to localhost runtime."
            await refresh()
            startRefreshLoop()
            autoOpenWebOnboardingIfNeeded()
        case .notInstalled:
            daemonInstallRequired = true
            connectionSummary = "Atlas daemon is not installed."
        case .installedNotRunning:
            runtimeStatus = nil
            lastError = nil
            if onboardingCompleted {
                daemonInstallRequired = false
                connectionSummary = "Atlas daemon is installed, but it is not running."
            } else {
                do {
                    let actualPort = try await runtimeManager.start()
                    await syncRuntimePort(actualPort)
                    daemonInstallRequired = false
                    connectionSummary = "Connected to localhost runtime."
                    await refresh()
                    startRefreshLoop()
                    autoOpenWebOnboardingIfNeeded()
                } catch {
                    connectionSummary = "Failed to start Atlas daemon."
                    lastError = error.localizedDescription
                }
            }
        case .unreachable:
            daemonInstallRequired = true
            runtimeStatus = nil
            if onboardingCompleted {
                connectionSummary = "Atlas daemon is not responding."
                lastError = "The Atlas daemon is installed but not responding on port \(config.runtimePort)."
            } else {
                connectionSummary = "Atlas daemon is not responding."
                lastError = "The Atlas daemon is installed but not responding on port \(config.runtimePort). Use the Install button to restart it."
            }
        }
    }

    public func openAtlasWebUI(route: String? = nil) {
        let port = effectiveRuntimePort
        let routeSuffix = route?.trimmingCharacters(in: .whitespacesAndNewlines)
        let sanitizedRoute: String
        if let routeSuffix, !routeSuffix.isEmpty {
            sanitizedRoute = routeSuffix.hasPrefix("#") ? routeSuffix : "#\(routeSuffix)"
        } else {
            sanitizedRoute = ""
        }
        Task {
            do {
                let token = try await apiClient.fetchLaunchToken()
                if let url = URL(string: "http://localhost:\(port)/auth/bootstrap?token=\(token)\(sanitizedRoute)") {
                    await MainActor.run {
                        NSWorkspace.shared.open(url)
                    }
                }
            } catch {
                if let fallback = URL(string: "http://localhost:\(port)/web/\(sanitizedRoute)") {
                    await MainActor.run {
                        NSWorkspace.shared.open(fallback)
                    }
                }
            }
        }
    }

    private func autoOpenWebOnboardingIfNeeded() {
        guard !onboardingCompleted, !hasOpenedInitialWebUI else { return }
        hasOpenedInitialWebUI = true
        openAtlasWebUI()
    }

    public func refresh() async {
        do {
            async let status = apiClient.fetchStatus()
            async let logs = apiClient.fetchLogs()
            async let approvals = apiClient.fetchPendingApprovals()
            async let skills = apiClient.fetchSkills()
            async let policies = apiClient.fetchActionPolicies()
            async let roots = apiClient.fetchFileAccessRoots()
            async let config = apiClient.fetchConfig()
            async let models = apiClient.fetchModels()

            runtimeStatus = try await status
            logEntries = try await logs
            pendingApprovals = try await approvals
            self.skills = try await skills
            self.actionPolicies = try await policies
            approvedFileAccessRoots = try await roots
            runtimeConfig = try? await config
            if let runtimeConfig {
                onboardingCompleted = runtimeConfig.onboardingCompleted
            }
            modelSelectorInfo = try? await models
            await syncRuntimePort(runtimeStatus?.runtimePort)

            let isRunning = runtimeStatus?.isRunning == true
            if isRunning {
                connectionSummary = "Connected to localhost runtime on port \(effectiveRuntimePort)."
            } else {
                connectionSummary = "Runtime is not responding."
                // Only reset the auto-validate flag when the daemon transitions from running →
                // stopped. Resetting on every failed poll would re-fire validation tasks on
                // every flap (e.g. brief unreachability during daemon restart).
                if wasRunning { hasAutoValidated = false }
            }
            wasRunning = isRunning

            lastError = runtimeStatus?.lastError
            await refreshCredentialStates()
            if isRunning {
                autoValidateIfNeeded()
            }
        } catch {
            lastError = error.localizedDescription
            connectionSummary = "Runtime API unavailable."
            await refreshCredentialStates()
        }
    }

    public func updateConfig(_ snapshot: RuntimeConfigSnapshot) async {
        do {
            let result = try await apiClient.updateConfig(snapshot)
            runtimeConfig = result.config
            restartRequired = result.restartRequired
        } catch {
            lastError = error.localizedDescription
        }
    }

    public func sendMessage() async {
        let message = draftMessage.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !message.isEmpty, !isSending else { return }

        isSending = true
        defer { isSending = false }

        do {
            let envelope = try await apiClient.sendMessage(
                conversationID: currentConversationID,
                message: message
            )

            currentConversationID = envelope.conversation.id
            lastAssistantResponse = envelope.response.assistantMessage
            pendingApprovals = envelope.response.pendingApprovals.isEmpty
                ? try await apiClient.fetchPendingApprovals()
                : envelope.response.pendingApprovals
            draftMessage = ""
            hasLoadedMemories = false
            await refresh()
        } catch {
            lastError = error.localizedDescription
        }
    }

    public func approve(_ request: ApprovalRequest) async {
        activeApprovalActionID = request.id
        isSending = true
        defer {
            activeApprovalActionID = nil
            isSending = false
        }

        do {
            let envelope = try await apiClient.approve(toolCallID: request.toolCallID)
            lastAssistantResponse = envelope.response.assistantMessage
            if !envelope.response.pendingApprovals.isEmpty {
                pendingApprovals = envelope.response.pendingApprovals
            }
        } catch {
            lastError = error.localizedDescription
        }
        await refresh()
    }

    public func deny(_ request: ApprovalRequest) async {
        activeApprovalActionID = request.id
        defer { activeApprovalActionID = nil }

        do {
            _ = try await apiClient.deny(toolCallID: request.toolCallID)
        } catch {
            lastError = error.localizedDescription
        }
        await refresh()
    }

    private enum ApprovalLifecycleStatusProxy {
        case pending
        case approved
        case running
        case completed
        case failed
        case denied
        case cancelled
    }

    private func approvalLifecycleStatus(for approval: ApprovalRequest) -> ApprovalLifecycleStatusProxy {
        switch approval.deferredExecutionStatus ?? (approval.status == .pending ? .pendingApproval : .approved) {
        case .pendingApproval:
            return .pending
        case .approved:
            return .approved
        case .running:
            return .running
        case .completed:
            return .completed
        case .failed:
            return .failed
        case .denied:
            return .denied
        case .cancelled:
            return .cancelled
        }
    }

    public func clearAllApprovals() async {
        guard !isClearingApprovals else { return }
        isClearingApprovals = true
        defer { isClearingApprovals = false }

        // Deny any approvals that are not already completed/cancelled/denied
        // This helps clear stuck items (e.g., failed/running/approved/pending)
        let approvalsToClear = pendingApprovals.filter { approval in
            switch approvalLifecycleStatus(for: approval) {
            case .completed, .cancelled, .denied:
                return false
            default:
                return true
            }
        }

        for approval in approvalsToClear {
            do {
                _ = try await apiClient.deny(toolCallID: approval.toolCallID)
            } catch {
                // Continue attempting to clear other approvals
                logger.warning("Failed to deny approval during bulk clear", metadata: [
                    "tool_call_id": approval.toolCallID.uuidString,
                    "error": error.localizedDescription
                ])
            }
        }

        await refresh()
    }

    public var pendingApprovalCount: Int {
        pendingApprovals.filter { $0.status == .pending }.count
    }

    public var shouldPresentOnboarding: Bool {
        false
    }

    public var nativeShellState: NativeShellState {
        if daemonInstallRequired {
            return .needsAttention
        }

        switch runtimeStatus?.state {
        case .ready, .starting:
            return .ready
        case .degraded:
            return .needsAttention
        case .stopped, nil:
            return .daemonStopped
        }
    }

    public var menuBarPrimaryAction: MenuBarPrimaryAction {
        switch nativeShellState {
        case .needsAttention:
            return .repairAtlas
        case .daemonStopped:
            return .startAtlas
        case .ready:
            return .openAtlas
        }
    }

    public var menuBarStatusTitle: String {
        switch nativeShellState {
        case .needsAttention:
            return "Needs Attention"
        case .daemonStopped:
            return "Stopped"
        case .ready:
            return assistantResponseStatusText
        }
    }

    public var menuBarStatusMessage: String {
        switch nativeShellState {
        case .needsAttention:
            return lastError ?? connectionSummary
        case .daemonStopped:
            return "Atlas is installed, but it is not running."
        case .ready:
            if pendingApprovalCount > 0 {
                return "\(pendingApprovalCount) approval\(pendingApprovalCount == 1 ? "" : "s") waiting for review."
            }
            return assistantResponseStatusText == "Ready" ? "Atlas is ready to open." : connectionSummary
        }
    }

    public var menuBarServiceStatuses: [MenuBarServiceStatus] {
        var rows = [
            MenuBarServiceStatus(
                id: "atlas-service",
                title: "Atlas Service",
                value: daemonStatusSummary,
                state: daemonStatusState
            ),
            MenuBarServiceStatus(
                id: "ai-provider",
                title: "AI Provider",
                value: aiProviderStatusSummary,
                state: connectionState(for: activeProviderValidationState, configured: activeProviderCredentialConfigured)
            ),
            MenuBarServiceStatus(
                id: "channels",
                title: "Channels",
                value: channelStatusSummary,
                state: channelStatusState
            )
        ]

        if pendingApprovalCount > 0 {
            rows.append(
                MenuBarServiceStatus(
                    id: "approvals",
                    title: "Approvals",
                    value: "\(pendingApprovalCount) pending",
                    state: .warning
                )
            )
        }

        return rows
    }

    public var telegramTokenConfigured: Bool {
        availability(for: .telegram).isConfigured
    }

    public var discordTokenConfigured: Bool {
        availability(for: .discord).isConfigured
    }

    public var slackBotTokenConfigured: Bool {
        availability(for: .slackBotToken).isConfigured
    }

    public var slackAppTokenConfigured: Bool {
        availability(for: .slackAppToken).isConfigured
    }

    public var openAICredentialConfigured: Bool {
        availability(for: .openAI).isConfigured
    }

    public var anthropicCredentialConfigured: Bool {
        availability(for: .anthropic).isConfigured
    }

    public var geminiCredentialConfigured: Bool {
        availability(for: .gemini).isConfigured
    }

    public var onboardingCredentialSnapshot: OnboardingCredentialSnapshot {
        OnboardingCredentialSnapshot(
            openAI: availability(for: .openAI),
            anthropic: availability(for: .anthropic),
            gemini: availability(for: .gemini),
            lmStudio: availability(for: .lmStudio),
            braveSearch: availability(for: .braveSearch),
            telegram: availability(for: .telegram),
            discord: availability(for: .discord)
        )
    }

    public var activeAIProvider: AIProvider {
        runtimeConfig.flatMap { AIProvider(rawValue: $0.activeAIProvider) } ?? config.activeAIProvider
    }

    public var anthropicStatusSummary: String {
        anthropicValidationState.statusLabel
    }

    public var anthropicCredentialStateSummary: String {
        anthropicCredentialConfigured ? "Configured" : "Not Configured"
    }

    public var anthropicValidationDetail: String? {
        anthropicValidationState.detail
    }

    public var geminiStatusSummary: String {
        geminiValidationState.statusLabel
    }

    public var geminiCredentialStateSummary: String {
        geminiCredentialConfigured ? "Configured" : "Not Configured"
    }

    public var geminiValidationDetail: String? {
        geminiValidationState.detail
    }

    public var lmStudioStatusSummary: String {
        lmStudioValidationState.statusLabel
    }

    public var lmStudioValidationDetail: String? {
        lmStudioValidationState.detail
    }

    public func switchAIProvider(_ provider: AIProvider) async {
        await persistPreferredAIProvider(provider)
    }

    // MARK: - Remote access

    public var remoteAccessEnabled: Bool {
        runtimeConfig?.remoteAccessEnabled ?? false
    }

    public func setRemoteAccess(_ enabled: Bool) async {
        guard var snapshot = runtimeConfig else { return }
        do {
            if enabled {
                // Generate and persist the access key from the app side before enabling.
                // The daemon must never write to the Keychain bundle — see KeychainSecretStore remarks.
                _ = try config.generateAndStoreRemoteAccessKeyIfNeeded()
                try? await apiClient.invalidateCredentialCache()
            }
            snapshot.remoteAccessEnabled = enabled
            await updateConfig(snapshot)
        } catch {
            lastError = error.localizedDescription
        }
    }

    public func fetchRemoteAccessStatus() async throws -> RemoteAccessStatusResponse {
        try await apiClient.fetchRemoteAccessStatus()
    }

    public func revokeRemoteSessions() async throws {
        try await apiClient.revokeRemoteSessions()
    }

    public func fetchRemoteKey() async -> String? {
        try? await apiClient.fetchRemoteKey()
    }

    public var runtimePortDescription: String {
        String(effectiveRuntimePort)
    }

    public var assistantName: String {
        config.personaName
    }

    public var actionSafetyMode: AtlasActionSafetyMode {
        config.actionSafetyMode
    }

    public var preferredDisplayName: String? {
        displayValue(forTitle: "Preferred display name")
    }

    public var preferredLocation: String? {
        displayValue(forTitle: "Preferred location")
    }

    public var preferredTemperatureUnit: String? {
        displayValue(forTitle: "Preferred temperature unit")
    }

    public var preferredTimeFormat: String? {
        displayValue(forTitle: "Preferred time format")
    }

    public var preferredDateFormat: String? {
        displayValue(forTitle: "Preferred date format")
    }

    public var modelDescription: String {
        guard let info = modelSelectorInfo else { return "Not resolved" }
        let primary = info.primaryModel ?? "—"
        let fast = info.fastModel ?? "—"
        return "\(primary)  ·  fast: \(fast)"
    }

    @MainActor
    public func refreshModels() async {
        isRefreshingModels = true
        defer { isRefreshingModels = false }
        modelSelectorInfo = try? await apiClient.refreshModels()
    }

    public var activeImageProvider: ImageProviderType? {
        guard let provider = config.activeImageProvider, isImageProviderConfigured(provider) else {
            return nil
        }

        return provider
    }

    public var imageGenerationStatusSummary: String {
        guard let activeImageProvider else {
            return "Not Configured"
        }

        let label = imageValidationState(for: activeImageProvider).statusLabel
        return label == "Connected" ? "\(activeImageProvider.shortTitle) Connected" : "\(activeImageProvider.shortTitle) \(label)"
    }

    public func imageProviderStatusSummary(for provider: ImageProviderType) -> String {
        imageValidationState(for: provider).statusLabel
    }

    public func imageProviderCredentialStateSummary(for provider: ImageProviderType) -> String {
        isImageProviderConfigured(provider) ? "Configured" : "Not Configured"
    }

    public func imageProviderValidationDetail(for provider: ImageProviderType) -> String? {
        imageValidationState(for: provider).detail
    }

    public func isImageProviderConfigured(_ provider: ImageProviderType) -> Bool {
        availability(for: credentialKind(for: provider)).isConfigured
    }

    public var sandboxDirectoryDescription: String {
        config.toolSandboxDirectory
    }

    public var lastInteractionDescription: String {
        if let lastMessageAt = runtimeStatus?.lastMessageAt {
            return "Last interaction \(lastMessageAt.formatted(date: .abbreviated, time: .shortened))"
        }

        return "No interactions yet."
    }

    public var assistantResponseStatusText: String {
        switch runtimeStatus?.state {
        case .ready:
            return pendingApprovalCount > 0 ? "Waiting on approval" : "Ready"
        case .degraded:
            return "Needs attention"
        case .starting:
            return "Starting"
        case .stopped:
            return "Stopped"
        case nil:
            return "Connecting"
        }
    }

    public var hasInteractionContent: Bool {
        lastAssistantResponse != "Atlas responses will appear here." || !logEntries.isEmpty
    }

    public var latestActivitySummary: String? {
        if let latestLog = logEntries.last {
            return latestLog.message
        }

        if pendingApprovalCount > 0 {
            return "\(pendingApprovalCount) approval\(pendingApprovalCount == 1 ? "" : "s") waiting for review."
        }

        return nil
    }

    public func isApprovalActionInFlight(for request: ApprovalRequest) -> Bool {
        activeApprovalActionID == request.id
    }

    public func setTelegramEnabled(_ enabled: Bool) async {
        guard enabled != telegramEnabled else { return }

        telegramEnabled = enabled
        currentConversationID = nil

        let updatedConfig = config.updatingTelegramEnabled(enabled)
        updatedConfig.persistRuntimeSettings()

        do {
            let actualPort = try await runtimeManager.restart()
            await syncRuntimePort(actualPort)
            config = updatedConfig
            apiClient = AtlasAPIClient(config: updatedConfig)
            appearanceMode = updatedConfig.appearanceMode
            connectionSummary = "Connected to localhost runtime."
            lastError = nil
            await refresh()
        } catch {
            lastError = error.localizedDescription
            connectionSummary = "Daemon failed to restart."
        }
    }

    public var telegramStatus: AtlasTelegramStatus {
        runtimeStatus?.telegram ?? AtlasTelegramStatus(enabled: config.telegramEnabled)
    }

    public var installedSkillCount: Int {
        skills.filter {
            switch $0.manifest.lifecycleState {
            case .known, .proposed:
                return false
            default:
                return true
            }
        }.count
    }

    public var enabledSkillCount: Int {
        skills.filter(\.isEnabled).count
    }

    public var memoryEnabled: Bool {
        config.memoryEnabled
    }

    public var memoryConfirmedCount: Int {
        memoryItems.filter(\.isUserConfirmed).count
    }

    public var memoryInferredCount: Int {
        memoryItems.filter { !$0.isUserConfirmed }.count
    }

    public var failedSkillValidationCount: Int {
        skills.filter { $0.validation?.status == .failed || $0.manifest.lifecycleState == .failedValidation }.count
    }

    public var approvedFileAccessRootCount: Int {
        approvedFileAccessRoots.count
    }

    public func setAppearanceMode(_ mode: AtlasAppearanceMode) {
        guard appearanceMode != .system || config.appearanceMode != .system else {
            applyAppearance()
            return
        }

        normalizeAppearanceModeIfNeeded()
        applyAppearance()
    }

    public func updateCredential(_ secret: String, for kind: AtlasCredentialKind) async throws {
        activeCredentialOperation = kind
        defer { activeCredentialOperation = nil }

        let manager = credentialManager
        do {
            try manager.store(secret, for: kind)
            lastError = nil
            try? await apiClient.invalidateCredentialCache()
            await refreshCredentialStates()
            switch kind {
            case .openAI, .anthropic, .gemini, .telegram, .discord:
                try await validateCredential(kind)
            case .lmStudio, .slackBotToken, .slackAppToken,
                 .openAIImage, .googleNanoBananaImage, .braveSearch:
                setValidationState(.notValidated, for: kind)
            }

            if kind == .telegram, telegramEnabled {
                try await restartRuntimeForCurrentConfig()
            }
        } catch {
            let sanitized = sanitizedCredentialError(error, for: kind)
            logger.error("\(kind.title) credential update failed", metadata: [
                "reason": sanitized
            ])
            lastError = sanitized
            throw error
        }
    }

    public func validateCredential(_ kind: AtlasCredentialKind) async throws {
        activeCredentialOperation = kind
        setValidationState(.validating, for: kind)
        defer { activeCredentialOperation = nil }

        await refreshCredentialStates()

        switch availability(for: kind) {
        case .configured:
            break
        case .missing:
            setValidationState(.notConfigured, for: kind)
            return
        case .keychainError(let message):
            setValidationState(.keychainError(message), for: kind)
            lastError = message
            return
        }

        do {
            try await credentialManager.validate(kind)
            setValidationState(.connected, for: kind)
            lastError = nil
            if kind == .telegram {
                await refresh()
            }
        } catch {
            let nextState = validationState(for: error, kind: kind)
            setValidationState(nextState, for: kind)
            lastError = sanitizedCredentialError(error, for: kind)
            throw error
        }
    }

    public func clearCredential(_ kind: AtlasCredentialKind) async throws {
        activeCredentialOperation = kind
        defer { activeCredentialOperation = nil }

        do {
            try credentialManager.clear(kind)
            lastError = nil
            try? await apiClient.invalidateCredentialCache()
            setValidationState(.notConfigured, for: kind)

            if kind == .telegram, telegramEnabled {
                telegramEnabled = false
                let updatedConfig = config.updatingTelegramEnabled(false)
                updatedConfig.persistRuntimeSettings()
                config = updatedConfig
                apiClient = AtlasAPIClient(config: updatedConfig)
                try await restartRuntimeForCurrentConfig()
            } else if let provider = imageProvider(for: kind) {
                if config.activeImageProvider == provider {
                    let updatedConfig = config.updatingActiveImageProvider(nil)
                    try await applyUpdatedConfig(updatedConfig)
                } else {
                    await refreshCredentialStates()
                    await refresh()
                }
            } else {
                await refreshCredentialStates()
                await refresh()
            }
        } catch {
            let sanitized = sanitizedCredentialError(error, for: kind)
            logger.error("\(kind.title) credential clear failed", metadata: [
                "reason": sanitized
            ])
            lastError = sanitized
            throw error
        }
    }

    public func isCredentialOperationInFlight(for kind: AtlasCredentialKind) -> Bool {
        activeCredentialOperation == kind
    }

    public var openAIStatusSummary: String {
        openAIValidationState.statusLabel
    }

    public var openAICredentialStateSummary: String {
        openAICredentialConfigured ? "Configured" : "Not Configured"
    }

    public var openAIValidationDetail: String? {
        openAIValidationState.detail
    }

    public var telegramStatusSummary: String {
        if !telegramTokenConfigured {
            return CredentialValidationState.notConfigured.statusLabel
        }

        if telegramStatus.connected {
            return "Connected"
        }

        return telegramValidationState.statusLabel
    }

    public var telegramCredentialStateSummary: String {
        telegramTokenConfigured ? "Configured" : "Not Configured"
    }

    public var telegramValidationDetail: String? {
        if telegramValidationState.detail != nil {
            return telegramValidationState.detail
        }

        if !telegramStatus.connected, let lastError = telegramStatus.lastError, telegramTokenConfigured {
            return lastError
        }

        return nil
    }

    public func activateImageProvider(_ provider: ImageProviderType) async throws {
        activeCredentialOperation = credentialKind(for: provider)
        setValidationState(.validating, for: credentialKind(for: provider))
        defer { activeCredentialOperation = nil }

        let manager = ActiveImageProviderManager(config: config)
        let validation = await manager.validate(providerType: provider)
        let state = credentialValidationState(from: validation)
        setValidationState(state, for: credentialKind(for: provider))

        guard validation.status == .passed else {
            lastError = validation.summary
            throw ImageGenerationError.providerFailure(validation.summary)
        }

        let updatedConfig = config.updatingActiveImageProvider(provider)
        try await applyUpdatedConfig(updatedConfig)
        setValidationState(.connected, for: credentialKind(for: provider))
    }

    public func deactivateImageProvider() async throws {
        guard let activeProvider = config.activeImageProvider else {
            return
        }

        activeCredentialOperation = credentialKind(for: activeProvider)
        defer { activeCredentialOperation = nil }

        let updatedConfig = config.updatingActiveImageProvider(nil)
        try await applyUpdatedConfig(updatedConfig)
        await refreshCredentialStates()
    }

    public func applyAppearance() {
        guard let application = NSApp else {
            return
        }
        application.appearance = nil
    }

    private func normalizeAppearanceModeIfNeeded() {
        guard config.appearanceMode != .system || appearanceMode != .system else { return }
        let normalizedConfig = config.updatingAppearanceMode(.system)
        normalizedConfig.persistAppearanceMode()
        config = normalizedConfig
        apiClient = AtlasAPIClient(config: normalizedConfig)
        appearanceMode = .system
    }

    public func enableSkill(_ skill: AtlasSkillRecord) async {
        activeSkillOperationID = skill.id
        defer { activeSkillOperationID = nil }

        do {
            _ = try await apiClient.enableSkill(id: skill.id)
            await refresh()
        } catch {
            lastError = error.localizedDescription
        }
    }

    public func disableSkill(_ skill: AtlasSkillRecord) async {
        activeSkillOperationID = skill.id
        defer { activeSkillOperationID = nil }

        do {
            _ = try await apiClient.disableSkill(id: skill.id)
            await refresh()
        } catch {
            lastError = error.localizedDescription
        }
    }

    public func validateSkill(_ skill: AtlasSkillRecord) async {
        activeSkillOperationID = skill.id
        defer { activeSkillOperationID = nil }

        do {
            _ = try await apiClient.validateSkill(id: skill.id)
            await refresh()
        } catch {
            lastError = error.localizedDescription
        }
    }

    public func isSkillOperationInFlight(for skill: AtlasSkillRecord) -> Bool {
        activeSkillOperationID == skill.id
    }

    public func setActionPolicy(_ policy: ActionApprovalPolicy, for actionID: String) async {
        do {
            let updatedPolicies = try await apiClient.setActionPolicy(policy, for: actionID)
            self.actionPolicies = updatedPolicies
        } catch {
            lastError = error.localizedDescription
        }
    }

    public func refreshMemories(force: Bool = false) async {
        guard !isLoadingMemories else { return }
        guard force || !hasLoadedMemories else { return }

        isLoadingMemories = true
        defer { isLoadingMemories = false }

        do {
            memoryItems = try await apiClient.fetchMemories()
            hasLoadedMemories = true
            lastError = nil
        } catch {
            lastError = error.localizedDescription
        }
    }

    @available(*, deprecated, message: "Native SwiftUI onboarding has transitioned to the web onboarding flow.")
    public func saveIdentityPreferences(
        userName: String,
        assistantName: String,
        location: String,
        inferredTemperatureUnit: String,
        inferredTimeFormat: String,
        inferredDateFormat: String,
        markOnboardingCompleted: Bool
    ) async throws {
        let trimmedUserName = userName.trimmingCharacters(in: .whitespacesAndNewlines)
        let trimmedAssistantName = assistantName.trimmingCharacters(in: .whitespacesAndNewlines)
        let trimmedLocation = location.trimmingCharacters(in: .whitespacesAndNewlines)

        guard !trimmedAssistantName.isEmpty else {
            throw CredentialManagementError.emptySecret
        }

        let updatedConfig = config
            .updatingPersonaName(trimmedAssistantName)
            .updatingOnboardingCompleted(markOnboardingCompleted)
        try await applyUpdatedConfig(updatedConfig)
        try await persistIdentityMemories(
            userName: trimmedUserName,
            assistantName: trimmedAssistantName,
            location: trimmedLocation,
            inferredTemperatureUnit: inferredTemperatureUnit,
            inferredTimeFormat: inferredTimeFormat,
            inferredDateFormat: inferredDateFormat
        )

        await refreshMemories(force: true)
    }

    @available(*, deprecated, message: "Native SwiftUI onboarding has transitioned to the web onboarding flow.")
    public func saveInteractionPreferences(
        actionSafetyMode: AtlasActionSafetyMode,
        markOnboardingCompleted: Bool
    ) async throws {
        let updatedConfig = config
            .updatingActionSafetyMode(actionSafetyMode)
            .updatingOnboardingCompleted(markOnboardingCompleted)
        try await applyUpdatedConfig(updatedConfig)
        try await persistInteractionMemories(actionSafetyMode: actionSafetyMode)

        await refreshMemories(force: true)
    }

    @available(*, deprecated, message: "Native SwiftUI onboarding has transitioned to the web onboarding flow.")
    public func completeOnboarding(
        userName: String,
        assistantName: String,
        location: String,
        inferredTemperatureUnit: String,
        inferredTimeFormat: String,
        inferredDateFormat: String,
        aboutYou: String = "",
        actionSafetyMode: AtlasActionSafetyMode
    ) async throws {
        let trimmedUserName = userName.trimmingCharacters(in: .whitespacesAndNewlines)
        let trimmedAssistantName = assistantName.trimmingCharacters(in: .whitespacesAndNewlines)
        let trimmedLocation = location.trimmingCharacters(in: .whitespacesAndNewlines)

        guard !trimmedAssistantName.isEmpty else {
            throw CredentialManagementError.emptySecret
        }

        try await persistIdentityMemories(
            userName: trimmedUserName,
            assistantName: trimmedAssistantName,
            location: trimmedLocation,
            inferredTemperatureUnit: inferredTemperatureUnit,
            inferredTimeFormat: inferredTimeFormat,
            inferredDateFormat: inferredDateFormat
        )

        try await persistInteractionMemories(actionSafetyMode: actionSafetyMode)

        let trimmedAboutYou = aboutYou.trimmingCharacters(in: .whitespacesAndNewlines)
        if !trimmedAboutYou.isEmpty {
            try await upsertMemory(
                category: .profile,
                title: "About the user",
                content: trimmedAboutYou,
                source: .userExplicit,
                confidence: 0.99,
                importance: 0.95,
                isUserConfirmed: true,
                tags: ["identity", "context"]
            )
        }

        let updatedConfig = config
            .updatingPersonaName(trimmedAssistantName)
            .updatingActionSafetyMode(actionSafetyMode)
            .updatingOnboardingCompleted(true)
        try await applyUpdatedConfig(updatedConfig)

        onboardingCompleted = true
        daemonInstallRequired = false   // close the onboarding sheet
        await refreshMemories(force: true)
        await refresh()
    }

    public func updateMemory(
        _ memory: MemoryItem,
        title: String,
        content: String,
        markAsConfirmed: Bool = true
    ) async throws {
        activeMemoryOperationID = memory.id
        defer { activeMemoryOperationID = nil }

        let request = AtlasMemoryUpdateRequest(
            title: title,
            content: content,
            markAsConfirmed: markAsConfirmed
        )

        do {
            let updated = try await apiClient.updateMemory(id: memory.id, request: request)
            applyMemoryMutation(updated)
            lastError = nil
        } catch {
            lastError = error.localizedDescription
            throw error
        }
    }

    public func confirmMemory(_ memory: MemoryItem) async throws {
        activeMemoryOperationID = memory.id
        defer { activeMemoryOperationID = nil }

        do {
            let confirmed = try await apiClient.confirmMemory(id: memory.id)
            applyMemoryMutation(confirmed)
            lastError = nil
        } catch {
            lastError = error.localizedDescription
            throw error
        }
    }

    public func deleteMemory(_ memory: MemoryItem) async throws {
        activeMemoryOperationID = memory.id
        defer { activeMemoryOperationID = nil }

        do {
            _ = try await apiClient.deleteMemory(id: memory.id)
            memoryItems.removeAll { $0.id == memory.id }
            lastError = nil
        } catch {
            lastError = error.localizedDescription
            throw error
        }
    }

    public func isMemoryOperationInFlight(for memory: MemoryItem) -> Bool {
        activeMemoryOperationID == memory.id
    }

    public func addApprovedFolder() async {
        guard !isManagingFileAccess else { return }

        let panel = NSOpenPanel()
        panel.canChooseDirectories = true
        panel.canChooseFiles = false
        panel.allowsMultipleSelection = false
        panel.resolvesAliases = true
        panel.title = "Approve Folder for Atlas"
        panel.prompt = "Grant Access"
        panel.message = "Atlas will only read files inside the folders you approve here."

        guard panel.runModal() == .OK, let url = panel.url else {
            return
        }

        isManagingFileAccess = true
        defer { isManagingFileAccess = false }

        do {
            let bookmarkData = try MacOSBookmarkGrantAdapter().createGrant(for: url)
            _ = try await apiClient.addFileAccessRoot(bookmarkData: bookmarkData)
            await refresh()
        } catch {
            lastError = error.localizedDescription
        }
    }

    /// Registers a security-scoped bookmark for a URL that was already selected
    /// by the caller (e.g. via NSOpenPanel). Returns true on success.
    public func addFolderBookmark(for url: URL) async -> Bool {
        do {
            let bookmarkData = try MacOSBookmarkGrantAdapter().createGrant(for: url)
            if runtimeStatus?.isRunning == true {
                _ = try await apiClient.addFileAccessRoot(bookmarkData: bookmarkData)
                await refresh()
            } else {
                let root = try await FileAccessScopeStore().addRoot(bookmarkData: bookmarkData)
                approvedFileAccessRoots = mergeApprovedFileAccessRoots(approvedFileAccessRoots, with: root)
            }
            return true
        } catch {
            lastError = error.localizedDescription
            return false
        }
    }

    public func removeApprovedFolder(_ root: ApprovedFileAccessRoot) async {
        guard !isManagingFileAccess else { return }

        isManagingFileAccess = true
        defer { isManagingFileAccess = false }

        do {
            _ = try await apiClient.removeFileAccessRoot(id: root.id)
            await refresh()
        } catch {
            lastError = error.localizedDescription
        }
    }

    // MARK: - Onboarding (menu bar wizard)

    /// Mark onboarding complete and persist the flag. Called by OnboardingWizard.
    @available(*, deprecated, message: "Native SwiftUI onboarding has transitioned to the web onboarding flow.")
    public func markOnboardingCompleted() {
        let updatedConfig = config.updatingOnboardingCompleted(true)
        updatedConfig.persistOnboardingCompleted()
        config = updatedConfig
        onboardingCompleted = true
        daemonInstallRequired = false   // dismiss the wizard sheet
    }

    /// Re-open the setup wizard from Settings. Resets the completion flag so the sheet shows again.
    @available(*, deprecated, message: "Native SwiftUI onboarding has transitioned to the web onboarding flow.")
    public func reopenOnboarding() {
        let updatedConfig = config.updatingOnboardingCompleted(false)
        updatedConfig.persistOnboardingCompleted()
        config = updatedConfig
        onboardingCompleted = false
    }

    // MARK: - Daemon lifecycle (menu bar)

    public func startDaemon() async {
        hasAutoValidated = false
        do {
            let actualPort = try await runtimeManager.start()
            await syncRuntimePort(actualPort)
            connectionSummary = "Connected to localhost runtime."
            lastError = nil
            daemonInstallRequired = false
            restartRequired = false
            await refresh()
        } catch {
            lastError = error.localizedDescription
            connectionSummary = "Failed to start Atlas daemon."
        }
    }

    public func repairDaemon() async {
        hasAutoValidated = false
        do {
            if daemonInstallRequired {
                let actualPort = try await runtimeManager.installAndStart()
                await syncRuntimePort(actualPort)
            } else {
                let actualPort = try await runtimeManager.restart()
                await syncRuntimePort(actualPort)
            }
            daemonInstallRequired = false
            connectionSummary = "Connected to localhost runtime."
            lastError = nil
            restartRequired = false
            await refresh()
        } catch {
            lastError = error.localizedDescription
            connectionSummary = "Atlas needs attention."
        }
    }

    /// Restart the daemon and refresh state. Called by MenuBarController.
    public func restartDaemon() async {
        hasAutoValidated = false
        do {
            let actualPort = try await runtimeManager.restart()
            await syncRuntimePort(actualPort)
            connectionSummary = "Connected to localhost runtime."
            lastError = nil
            restartRequired = false
            await refresh()
        } catch {
            lastError = error.localizedDescription
            connectionSummary = "Daemon failed to restart."
        }
    }

    /// Stop the daemon and clear runtime status. Called by MenuBarController.
    public func stopDaemon() async {
        hasAutoValidated = false
        do {
            try await runtimeManager.stop()
            daemonInstallRequired = false
            runtimeStatus = nil
            connectionSummary = "Atlas daemon stopped."
            lastError = nil
        } catch {
            lastError = error.localizedDescription
        }
    }

    public var telegramConnectionSummary: String {
        if !telegramStatus.enabled {
            return "Telegram bridge is disabled."
        }

        if telegramStatus.pollingActive {
            return telegramStatus.connected
                ? "Telegram polling is active."
                : "Telegram polling is retrying after an error."
        }

        return telegramStatus.lastError ?? "Telegram bridge is enabled but inactive."
    }

    private func restartRuntimeForCurrentConfig() async throws {
        hasAutoValidated = false
        let actualPort = try await runtimeManager.restart()
        await syncRuntimePort(actualPort)
        connectionSummary = "Connected to localhost runtime."
        lastError = nil
        await refresh()
    }

    private func autoValidateIfNeeded() {
        guard !hasAutoValidated else { return }
        hasAutoValidated = true

        // Immediately mark configured APIs as validating so the UI responds at once
        if openAICredentialConfigured { setValidationState(.validating, for: .openAI) }
        if anthropicCredentialConfigured { setValidationState(.validating, for: .anthropic) }
        if geminiCredentialConfigured { setValidationState(.validating, for: .gemini) }
        if telegramTokenConfigured { setValidationState(.validating, for: .telegram) }

        Task {
            // Let validateCredential handle its own isConfigured guard — don't double-check here
            try? await validateCredential(.openAI)
            try? await validateCredential(.anthropic)
            try? await validateCredential(.gemini)
            try? await validateCredential(.telegram)
            await validateImageGenerationIfNeeded()
        }
    }

    private func validateImageGenerationIfNeeded() async {
        guard let activeProvider = config.activeImageProvider,
              isImageProviderConfigured(activeProvider) else { return }

        let kind = credentialKind(for: activeProvider)
        activeCredentialOperation = kind
        setValidationState(.validating, for: kind)
        defer { activeCredentialOperation = nil }

        let manager = ActiveImageProviderManager(config: config)
        let validation = await manager.validate(providerType: activeProvider)
        setValidationState(credentialValidationState(from: validation), for: kind)
    }

    private func refreshCredentialStates() async {
        let manager = credentialManager
        let availabilityByKind = await Task.detached(priority: .utility) {
            AtlasAppState.loadCredentialAvailability(using: manager)
        }.value

        credentialAvailability = availabilityByKind
        openAIValidationState = refreshedValidationState(current: openAIValidationState, availability: availability(for: .openAI))
        anthropicValidationState = refreshedValidationState(current: anthropicValidationState, availability: availability(for: .anthropic))
        geminiValidationState = refreshedValidationState(current: geminiValidationState, availability: availability(for: .gemini))
        telegramValidationState = refreshedValidationState(current: telegramValidationState, availability: availability(for: .telegram))
        discordValidationState = refreshedValidationState(current: discordValidationState, availability: availability(for: .discord))
        lmStudioValidationState = refreshedValidationState(current: lmStudioValidationState, availability: availability(for: .lmStudio))
        slackBotValidationState = refreshedValidationState(current: slackBotValidationState, availability: availability(for: .slackBotToken))
        slackAppValidationState = refreshedValidationState(current: slackAppValidationState, availability: availability(for: .slackAppToken))
        braveSearchValidationState = refreshedValidationState(current: braveSearchValidationState, availability: availability(for: .braveSearch))
        openAIImageValidationState = refreshedValidationState(current: openAIImageValidationState, availability: availability(for: .openAIImage))
        googleImageValidationState = refreshedValidationState(current: googleImageValidationState, availability: availability(for: .googleNanoBananaImage))

        if let active = config.activeImageProvider,
           availability(for: credentialKind(for: active)).isConfigured,
           imageValidationState(for: active) == .notValidated {
            setValidationState(.connected, for: credentialKind(for: active))
        }
    }

    private func refreshedValidationState(
        current: CredentialValidationState,
        availability: CredentialAvailability
    ) -> CredentialValidationState {
        switch availability {
        case .configured:
            return normalizedValidationState(current)
        case .missing:
            return .notConfigured
        case .keychainError(let message):
            return .keychainError(message)
        }
    }

    private func normalizedValidationState(_ state: CredentialValidationState) -> CredentialValidationState {
        switch state {
        case .notConfigured, .keychainError:
            return .notValidated
        default:
            return state
        }
    }

    private var daemonStatusSummary: String {
        if daemonInstallRequired {
            return onboardingCompleted ? "Needs repair" : "Not installed"
        }

        switch runtimeStatus?.state {
        case .ready:
            return "Running"
        case .degraded:
            return "Needs attention"
        case .starting:
            return "Starting"
        case .stopped:
            return "Stopped"
        case nil:
            return "Unavailable"
        }
    }

    private var daemonStatusState: MenuBarServiceStatus.State {
        if daemonInstallRequired {
            return .warning
        }

        switch runtimeStatus?.state {
        case .ready:
            return .ready
        case .degraded, .starting:
            return .warning
        case .stopped, nil:
            return .inactive
        }
    }

    /// Validation state for whichever AI provider is currently active.
    private var activeProviderValidationState: CredentialValidationState {
        switch activeAIProvider {
        case .openAI:    return openAIValidationState
        case .anthropic: return anthropicValidationState
        case .gemini:    return geminiValidationState
        case .lmStudio:  return lmStudioValidationState
        }
    }

    /// Whether the active AI provider has its credential configured.
    /// LM Studio doesn't require a key, so it's always considered configured.
    private var activeProviderCredentialConfigured: Bool {
        switch activeAIProvider {
        case .openAI:    return openAICredentialConfigured
        case .anthropic: return anthropicCredentialConfigured
        case .gemini:    return geminiCredentialConfigured
        case .lmStudio:  return true
        }
    }

    private var aiProviderStatusSummary: String {
        if case .keychainError = activeProviderValidationState {
            return activeProviderValidationState.statusLabel
        }
        guard activeProviderCredentialConfigured else { return "Needs key" }
        return activeProviderValidationState.statusLabel
    }

    private var channelStatusSummary: String {
        let liveChannels = Set((runtimeStatus?.communications.channels ?? []).map { channel in
            "\(channel.platform.rawValue):\(channel.channelID):\(channel.threadID ?? "")"
        }).count

        guard liveChannels > 0 else {
            return "No channels connected"
        }
        return liveChannels == 1 ? "1 connected" : "\(liveChannels) connected"
    }

    private var channelStatusState: MenuBarServiceStatus.State {
        let liveChannels = Set((runtimeStatus?.communications.channels ?? []).map { channel in
            "\(channel.platform.rawValue):\(channel.channelID):\(channel.threadID ?? "")"
        }).count
        return liveChannels > 0 ? .ready : .inactive
    }

    private var imageGenerationState: MenuBarServiceStatus.State {
        guard let activeProvider = config.activeImageProvider,
              isImageProviderConfigured(activeProvider) else {
            return .inactive
        }

        return connectionState(for: imageValidationState(for: activeProvider), configured: true)
    }

    private func connectionState(
        for validationState: CredentialValidationState,
        configured: Bool
    ) -> MenuBarServiceStatus.State {
        guard configured else {
            return .inactive
        }

        switch validationState {
        case .connected:
            return .ready
        case .validating, .notValidated:
            return .warning
        case .invalid, .validationFailed, .notConfigured, .keychainError:
            return .inactive
        }
    }

    public func credentialAvailability(for kind: AtlasCredentialKind) -> CredentialAvailability {
        availability(for: kind)
    }

    private func availability(for kind: AtlasCredentialKind) -> CredentialAvailability {
        credentialAvailability[kind] ?? .missing
    }

    nonisolated private static func loadCredentialAvailability(
        using manager: AtlasCredentialManager
    ) -> [AtlasCredentialKind: CredentialAvailability] {
        AtlasCredentialKind.allCases.reduce(into: [:]) { result, kind in
            result[kind] = manager.availability(kind)
        }
    }

    private func setValidationState(_ state: CredentialValidationState, for kind: AtlasCredentialKind) {
        switch kind {
        case .openAI:               openAIValidationState    = state
        case .anthropic:            anthropicValidationState  = state
        case .gemini:               geminiValidationState     = state
        case .telegram:             telegramValidationState   = state
        case .discord:              discordValidationState    = state
        case .lmStudio:             lmStudioValidationState   = state
        case .slackBotToken:        slackBotValidationState   = state
        case .slackAppToken:        slackAppValidationState   = state
        case .braveSearch:          braveSearchValidationState = state
        case .openAIImage:          openAIImageValidationState = state
        case .googleNanoBananaImage: googleImageValidationState = state
        }
    }

    private func validationState(for error: Error, kind: AtlasCredentialKind) -> CredentialValidationState {
        let detail = sanitizedCredentialError(error, for: kind)

        if case OpenAIClientError.unexpectedStatusCode(let code, _) = error, kind == .openAI, code == 401 || code == 403 {
            return .invalid(detail)
        }

        if case AnthropicClientError.unexpectedStatusCode(let code, _) = error, kind == .anthropic, code == 401 || code == 403 {
            return .invalid(detail)
        }

        if case GeminiClientError.unexpectedStatusCode(let code, _) = error, kind == .gemini, code == 401 || code == 403 {
            return .invalid(detail)
        }

        if case TelegramClientError.unexpectedStatusCode(let code, _) = error, kind == .telegram, code == 401 || code == 403 {
            return .invalid(detail)
        }

        if case TelegramClientError.apiError(let code, _) = error, kind == .telegram, code == 401 || code == 403 {
            return .invalid(detail)
        }

        if case DiscordClientError.unexpectedStatusCode(let code, _) = error, kind == .discord, code == 401 || code == 403 {
            return .invalid(detail)
        }

        return .validationFailed(detail)
    }

    private func sanitizedCredentialError(_ error: Error, for kind: AtlasCredentialKind) -> String {
        switch error {
        case AnthropicClientError.missingAPIKey:
            return "No Anthropic API key is stored in Keychain."
        case AnthropicClientError.unexpectedStatusCode(let code, let message) where kind == .anthropic:
            return code == 401 || code == 403
                ? "The stored Anthropic API key was rejected."
                : message
        case GeminiClientError.missingAPIKey:
            return "No Gemini API key is stored in Keychain."
        case GeminiClientError.unexpectedStatusCode(let code, let message) where kind == .gemini:
            return code == 401 || code == 403
                ? "The stored Gemini API key was rejected."
                : message
        case OpenAIClientError.missingAPIKey:
            return "No OpenAI API key is stored in Keychain."
        case TelegramClientError.missingBotToken:
            return "No Telegram bot token is stored in Keychain."
        case ImageGenerationError.providerCredentialMissing(let provider):
            return "No \(provider.title) API key is stored in Keychain."
        case OpenAIClientError.unexpectedStatusCode(let code, let message):
            return code == 401 || code == 403
                ? "The stored OpenAI API key was rejected."
                : message
        case TelegramClientError.unexpectedStatusCode(let code, _):
            return code == 401 || code == 403
                ? "The stored Telegram bot token was rejected."
                : "Telegram validation could not be completed."
        case TelegramClientError.apiError(let code, _):
            return code == 401 || code == 403
                ? "The stored Telegram bot token was rejected."
                : "Telegram validation could not be completed."
        case DiscordClientError.missingBotToken:
            return "No Discord bot token is stored in Keychain."
        case DiscordClientError.unexpectedStatusCode(let code, _):
            return code == 401 || code == 403
                ? "The stored Discord bot token was rejected."
                : "Discord validation could not be completed."
        case KeychainSecretStoreError.secretNotFound:
            return "No \(kind.title) credential is stored in Keychain."
        case KeychainSecretStoreError.invalidSecretEncoding:
            return "The stored \(kind.title) credential could not be decoded from Keychain."
        case KeychainSecretStoreError.osStatus(let status):
            return SecCopyErrorMessageString(status, nil) as String? ?? "\(kind.title) Keychain access failed."
        case CredentialManagementError.emptySecret:
            return "Enter a value before saving."
        case CredentialManagementError.unsupportedValidation:
            return "Validate image providers from Image Generation settings."
        default:
            return "\(kind.title) validation could not be completed."
        }
    }

    private func memoryValue(forTitle title: String) -> String? {
        memoryItems.first(where: { $0.title == title })?.content
    }

    private func displayValue(forTitle title: String) -> String? {
        guard let content = memoryValue(forTitle: title)?.trimmingCharacters(in: .whitespacesAndNewlines), !content.isEmpty else {
            return nil
        }

        switch title {
        case "Preferred location":
            if let matched = content.firstMatch(for: #"(?i)\b(?:based in|live in|located in)\s+(.+?)(?:\.)?$"#) {
                return matched
            }
        case "Preferred temperature unit":
            if let matched = content.firstMatch(for: #"(?i)\b(Fahrenheit|Celsius)\b"#) {
                return matched.capitalized
            }
        case "Preferred display name":
            if let matched = content.firstMatch(for: #"(?i)\b(?:call me|i go by|my name is)\s+(.+?)(?:\.)?$"#) {
                return matched
            }
        default:
            break
        }

        return content
    }

    private func applyUpdatedConfig(_ updatedConfig: AtlasConfig) async throws {
        let actualPort = try await runtimeManager.restart()
        updatedConfig.persistRuntimeSettings()
        updatedConfig.updatingAppearanceMode(.system).persistAppearanceMode()
        updatedConfig.persistOnboardingCompleted()

        config = updatedConfig
        apiClient = AtlasAPIClient(config: updatedConfig)
        await syncRuntimePort(actualPort)
        telegramEnabled = updatedConfig.telegramEnabled
        appearanceMode = .system
        onboardingCompleted = updatedConfig.onboardingCompleted
        applyAppearance()
        connectionSummary = "Connected to localhost runtime."
        lastError = nil
        hasLoadedMemories = false
        await refresh()
    }

    private func imageProvider(for kind: AtlasCredentialKind) -> ImageProviderType? {
        switch kind {
        case .openAIImage:
            return .openAI
        case .googleNanoBananaImage:
            return .googleNanoBanana
        default:
            return nil
        }
    }

    private func credentialKind(for provider: ImageProviderType) -> AtlasCredentialKind {
        switch provider {
        case .openAI:
            return .openAIImage
        case .googleNanoBanana:
            return .googleNanoBananaImage
        }
    }

    public var lmStudioBaseURL: String {
        runtimeConfig?.lmStudioBaseURL ?? config.lmStudioBaseURL
    }

    public func persistPreferredAIProvider(_ provider: AIProvider) async {
        if var snapshot = runtimeConfig {
            snapshot.activeAIProvider = provider.rawValue
            await updateConfig(snapshot)
            return
        }

        let updatedConfig = config.updatingActiveAIProvider(provider)
        updatedConfig.persistRuntimeSettings()
        config = updatedConfig
        apiClient = AtlasAPIClient(config: updatedConfig)
    }

    public func persistLMStudioBaseURL(_ baseURL: String) async {
        let trimmed = baseURL.trimmingCharacters(in: .whitespacesAndNewlines)

        if var snapshot = runtimeConfig {
            snapshot.lmStudioBaseURL = trimmed
            await updateConfig(snapshot)
            return
        }

        let updatedConfig = config.updatingLMStudioBaseURL(trimmed)
        updatedConfig.persistRuntimeSettings()
        config = updatedConfig
        apiClient = AtlasAPIClient(config: updatedConfig)
    }

    private var effectiveRuntimePort: Int {
        runtimeStatus?.runtimePort ?? runtimeConfig?.runtimePort ?? config.runtimePort
    }

    private func syncRuntimePort(_ port: Int?) async {
        guard let port, port != config.runtimePort else { return }

        let updatedConfig = AtlasConfig(
            runtimePort: port,
            openAIServiceName: config.openAIServiceName,
            openAIAccountName: config.openAIAccountName,
            telegramServiceName: config.telegramServiceName,
            telegramAccountName: config.telegramAccountName,
            discordServiceName: config.discordServiceName,
            discordAccountName: config.discordAccountName,
            slackBotServiceName: config.slackBotServiceName,
            slackBotAccountName: config.slackBotAccountName,
            slackAppServiceName: config.slackAppServiceName,
            slackAppAccountName: config.slackAppAccountName,
            openAIImageServiceName: config.openAIImageServiceName,
            openAIImageAccountName: config.openAIImageAccountName,
            googleImageServiceName: config.googleImageServiceName,
            googleImageAccountName: config.googleImageAccountName,
            braveSearchServiceName: config.braveSearchServiceName,
            braveSearchAccountName: config.braveSearchAccountName,
            finnhubServiceName: config.finnhubServiceName,
            finnhubAccountName: config.finnhubAccountName,
            alphaVantageServiceName: config.alphaVantageServiceName,
            alphaVantageAccountName: config.alphaVantageAccountName,
            telegramEnabled: config.telegramEnabled,
            discordEnabled: config.discordEnabled,
            discordClientID: config.discordClientID,
            slackEnabled: config.slackEnabled,
            telegramPollingTimeoutSeconds: config.telegramPollingTimeoutSeconds,
            telegramPollingRetryBaseSeconds: config.telegramPollingRetryBaseSeconds,
            telegramCommandPrefix: config.telegramCommandPrefix,
            telegramAllowedUserIDs: config.telegramAllowedUserIDs,
            telegramAllowedChatIDs: config.telegramAllowedChatIDs,
            defaultOpenAIModel: config.defaultOpenAIModel,
            baseSystemPrompt: config.baseSystemPrompt,
            autoApproveDraftTools: config.autoApproveDraftTools,
            maxAgentIterations: config.maxAgentIterations,
            conversationWindowLimit: config.conversationWindowLimit,
            lmStudioContextWindowLimit: config.lmStudioContextWindowLimit,
            lmStudioMaxAgentIterations: config.lmStudioMaxAgentIterations,
            toolSandboxDirectory: config.toolSandboxDirectory,
            memoryDatabasePath: config.memoryDatabasePath,
            memoryEnabled: config.memoryEnabled,
            maxRetrievedMemoriesPerTurn: config.maxRetrievedMemoriesPerTurn,
            memoryAutoSaveThreshold: config.memoryAutoSaveThreshold,
            personaName: config.personaName,
            appearanceMode: config.appearanceMode,
            onboardingCompleted: config.onboardingCompleted,
            actionSafetyMode: config.actionSafetyMode,
            activeImageProvider: config.activeImageProvider,
            activeAIProvider: config.activeAIProvider,
            lmStudioBaseURL: config.lmStudioBaseURL,
            selectedAnthropicModel: config.selectedAnthropicModel,
            selectedGeminiModel: config.selectedGeminiModel,
            selectedOpenAIPrimaryModel: config.selectedOpenAIPrimaryModel,
            selectedOpenAIFastModel: config.selectedOpenAIFastModel,
            selectedAnthropicFastModel: config.selectedAnthropicFastModel,
            selectedGeminiFastModel: config.selectedGeminiFastModel,
            selectedLMStudioModel: config.selectedLMStudioModel,
            enableSmartToolSelection: config.enableSmartToolSelection,
            enableMultiAgentOrchestration: config.enableMultiAgentOrchestration,
            maxParallelAgents: config.maxParallelAgents,
            workerMaxIterations: config.workerMaxIterations,
            remoteAccessEnabled: config.remoteAccessEnabled
        )
        updatedConfig.persistRuntimeSettings()
        config = updatedConfig
        apiClient = AtlasAPIClient(config: updatedConfig)
        runtimeManager = AtlasRuntimeManager(config: updatedConfig)
    }

    private func mergeApprovedFileAccessRoots(
        _ existingRoots: [ApprovedFileAccessRoot],
        with newRoot: ApprovedFileAccessRoot
    ) -> [ApprovedFileAccessRoot] {
        var rootsByPath: [String: ApprovedFileAccessRoot] = [:]
        for root in existingRoots {
            rootsByPath[root.path] = root
        }
        rootsByPath[newRoot.path] = newRoot
        return rootsByPath.values.sorted {
            $0.displayName.localizedCaseInsensitiveCompare($1.displayName) == .orderedAscending
        }
    }

    private func imageValidationState(for provider: ImageProviderType) -> CredentialValidationState {
        switch provider {
        case .openAI:
            return openAIImageValidationState
        case .googleNanoBanana:
            return googleImageValidationState
        }
    }

    private func credentialValidationState(from validation: ImageProviderValidation) -> CredentialValidationState {
        switch validation.status {
        case .passed:
            return .connected
        case .failed:
            return .invalid(validation.summary)
        case .warning:
            return .validationFailed(validation.summary)
        case .notValidated:
            return .notValidated
        }
    }

    private func upsertMemory(
        category: MemoryCategory,
        title: String,
        content: String,
        source: MemorySource,
        confidence: Double,
        importance: Double,
        isUserConfirmed: Bool,
        tags: [String]
    ) async throws {
        let trimmedContent = content.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmedContent.isEmpty else {
            return
        }

        if !hasLoadedMemories {
            memoryItems = try await apiClient.fetchMemories()
            hasLoadedMemories = true
        }

        if let existing = memoryItems.first(where: { $0.category == category && $0.title == title }) {
            let updated = try await apiClient.updateMemory(
                id: existing.id,
                request: AtlasMemoryUpdateRequest(
                    title: title,
                    content: trimmedContent,
                    markAsConfirmed: isUserConfirmed
                )
            )
            applyMemoryMutation(updated.updating(isUserConfirmed: isUserConfirmed, tags: tags))
            return
        }

        let created = try await apiClient.createMemory(
            AtlasMemoryCreateRequest(
                category: category,
                title: title,
                content: trimmedContent,
                source: source,
                confidence: confidence,
                importance: importance,
                isUserConfirmed: isUserConfirmed,
                tags: tags
            )
        )
        applyMemoryMutation(created)
    }

    private func applyMemoryMutation(_ memory: MemoryItem) {
        if let index = memoryItems.firstIndex(where: { $0.id == memory.id }) {
            memoryItems[index] = memory
        } else {
            memoryItems.append(memory)
        }

        memoryItems.sort { lhs, rhs in
            lhs.updatedAt > rhs.updatedAt
        }
        hasLoadedMemories = true
    }

    private func persistIdentityMemories(
        userName: String,
        assistantName: String,
        location: String,
        inferredTemperatureUnit: String,
        inferredTimeFormat: String,
        inferredDateFormat: String
    ) async throws {
        if !userName.isEmpty {
            try await upsertMemory(
                category: .profile,
                title: "Preferred display name",
                content: userName,
                source: .userExplicit,
                confidence: 0.99,
                importance: 0.95,
                isUserConfirmed: true,
                tags: ["identity", "name"]
            )
        }

        try await upsertMemory(
            category: .profile,
            title: "Preferred Atlas name",
            content: assistantName,
            source: .userExplicit,
            confidence: 1.0,
            importance: 0.98,
            isUserConfirmed: true,
            tags: ["identity", "assistant-name"]
        )

        if !location.isEmpty {
            try await upsertMemory(
                category: .profile,
                title: "Preferred location",
                content: location,
                source: .userExplicit,
                confidence: 0.98,
                importance: 0.93,
                isUserConfirmed: true,
                tags: ["location", "weather"]
            )
        }

        try await upsertMemory(
            category: .preference,
            title: "Preferred temperature unit",
            content: inferredTemperatureUnit,
            source: .systemDerived,
            confidence: 0.88,
            importance: 0.82,
            isUserConfirmed: false,
            tags: ["weather", "temperature", "unit"]
        )

        try await upsertMemory(
            category: .preference,
            title: "Preferred time format",
            content: inferredTimeFormat,
            source: .systemDerived,
            confidence: 0.86,
            importance: 0.74,
            isUserConfirmed: false,
            tags: ["time", "format"]
        )

        try await upsertMemory(
            category: .preference,
            title: "Preferred date format",
            content: inferredDateFormat,
            source: .systemDerived,
            confidence: 0.84,
            importance: 0.7,
            isUserConfirmed: false,
            tags: ["date", "format"]
        )
    }

    private func persistInteractionMemories(
        actionSafetyMode: AtlasActionSafetyMode
    ) async throws {
        try await upsertMemory(
            category: .preference,
            title: "Action safety preference",
            content: actionSafetyMode.title,
            source: .userExplicit,
            confidence: 0.99,
            importance: 0.9,
            isUserConfirmed: true,
            tags: ["safety", "approvals"]
        )
    }

    deinit {
        refreshTask?.cancel()
    }

    private func startRefreshLoop() {
        refreshTask?.cancel()
        refreshTask = Task { [weak self] in
            while !Task.isCancelled {
                guard let self else { return }
                await self.refresh()
                try? await Task.sleep(for: .seconds(3))
            }
        }
    }

    // Listens for notifications relayed from the Atlas daemon (which cannot use
    // UNUserNotificationCenter directly). The daemon posts via
    // NSDistributedNotificationCenter; this method receives and delivers them.
    private func setupNotificationRelay() {
        notificationRelayObserver = DistributedNotificationCenter.default().addObserver(
            forName: atlasNotificationRelayName,
            object: nil,
            queue: .main
        ) { notification in
            guard let userInfo = notification.userInfo,
                  let title = userInfo["title"] as? String,
                  let body = userInfo["body"] as? String else { return }

            let content = UNMutableNotificationContent()
            content.title = title
            content.body = body
            content.sound = .default

            let notifID = userInfo["id"] as? String ?? UUID().uuidString
            let request = UNNotificationRequest(
                identifier: "atlas.relay.\(notifID)",
                content: content,
                trigger: nil
            )

            Task {
                try? await UNUserNotificationCenter.current().add(request)
            }
        }
    }
}

private extension String {
    func firstMatch(for pattern: String) -> String? {
        guard let expression = try? NSRegularExpression(pattern: pattern) else {
            return nil
        }

        let range = NSRange(startIndex..<endIndex, in: self)
        guard
            let match = expression.firstMatch(in: self, range: range),
            match.numberOfRanges > 1,
            let captureRange = Range(match.range(at: 1), in: self)
        else {
            return nil
        }

        return String(self[captureRange]).trimmingCharacters(in: .whitespacesAndNewlines)
    }
}
