import AtlasBridges
import AtlasLogging
import AtlasNetwork
import AtlasShared
import Foundation

enum CommunicationManagerError: LocalizedError {
    case unknownPlatform(String)
    case unsupportedConfiguration(String)
    case unavailablePlatform(String)

    var errorDescription: String? {
        switch self {
        case .unknownPlatform(let platform):
            return "Unknown communication platform '\(platform)'."
        case .unsupportedConfiguration(let platform):
            return "Platform '\(platform)' is not configurable yet."
        case .unavailablePlatform(let platform):
            return "Platform '\(platform)' is unavailable."
        }
    }
}

protocol CommunicationPlatformService: Actor {
    var platform: ChatPlatform { get }
    func start() async
    func stop() async
    func status() async -> CommunicationPlatformStatus
    func bridge() async -> (any ChatBridge)?
}

actor TelegramCommunicationService: CommunicationPlatformService {
    let platform: ChatPlatform = .telegram

    private let config: AtlasConfig
    private let bridgeRef: TelegramBridge
    private let client: any TelegramClienting
    private let logger: AtlasLogger
    private var pollingService: TelegramPollingService?
    private var statusOverride: AtlasTelegramStatus

    init(
        config: AtlasConfig,
        bridge: TelegramBridge,
        client: any TelegramClienting,
        logger: AtlasLogger = AtlasLogger(category: "communications.telegram")
    ) {
        self.config = config
        self.bridgeRef = bridge
        self.client = client
        self.logger = logger
        self.statusOverride = AtlasTelegramStatus(enabled: config.telegramEnabled)
    }

    func start() async {
        if pollingService != nil {
            await stop()
        }

        guard config.telegramEnabled else {
            statusOverride = AtlasTelegramStatus(enabled: false)
            return
        }

        guard config.hasTelegramBotToken() else {
            statusOverride = AtlasTelegramStatus(
                enabled: true,
                connected: false,
                pollingActive: false,
                lastError: TelegramClientError.missingBotToken.localizedDescription
            )
            return
        }

        let service = TelegramPollingService(
            config: config,
            telegramClient: client,
            bridge: bridgeRef
        )

        do {
            try await service.start()
            pollingService = service
            statusOverride = await service.snapshot()
        } catch {
            pollingService = nil
            let sanitizedError = sanitizeTelegramError(error)
            statusOverride = AtlasTelegramStatus(
                enabled: true,
                connected: false,
                pollingActive: false,
                lastError: sanitizedError
            )
            logger.warning("Telegram communications service failed to start", metadata: [
                "error": sanitizedError
            ])
        }
    }

    func stop() async {
        await pollingService?.stop()
        pollingService = nil
    }

    func status() async -> CommunicationPlatformStatus {
        let telegram = await pollingService?.snapshot() ?? statusOverride
        let credentialConfigured = config.hasTelegramBotToken()
        let setupState: CommunicationSetupState = if telegram.connected {
            .ready
        } else if !credentialConfigured {
            .missingCredentials
        } else if telegram.enabled && telegram.lastError != nil {
            .validationFailed
        } else {
            .partialSetup
        }
        return CommunicationPlatformStatus(
            platform: .telegram,
            enabled: telegram.enabled,
            connected: telegram.connected,
            setupState: setupState,
            statusLabel: telegram.connected ? "Ready" : (credentialConfigured ? "Needs Setup" : "Missing Credentials"),
            connectedAccountName: telegram.botUsername,
            credentialConfigured: credentialConfigured,
            blockingReason: telegram.lastError ?? (!credentialConfigured ? "Add a Telegram bot token to finish setup." : nil),
            requiredCredentials: ["telegram_bot_token"],
            lastError: telegram.lastError,
            lastUpdatedAt: telegram.lastUpdateAt,
            metadata: [
                "pollingActive": telegram.pollingActive ? "true" : "false",
                "commandPrefix": config.telegramCommandPrefix
            ]
        )
    }

    func bridge() async -> (any ChatBridge)? {
        bridgeRef
    }

    func legacyStatus() async -> AtlasTelegramStatus {
        await pollingService?.snapshot() ?? statusOverride
    }
}

actor DiscordCommunicationService: CommunicationPlatformService {
    let platform: ChatPlatform = .discord

    private let config: AtlasConfig
    private let client: any DiscordClienting
    private let bridgeRef: DiscordBridge
    private var lastStatus: CommunicationPlatformStatus

    init(
        config: AtlasConfig,
        client: any DiscordClienting,
        bridge: DiscordBridge
    ) {
        self.config = config
        self.client = client
        self.bridgeRef = bridge
        self.lastStatus = CommunicationPlatformStatus(
            platform: .discord,
            enabled: config.discordEnabled,
            connected: false,
            setupState: discordSetupState(config: config),
            statusLabel: discordStatusLabel(config: config, connected: false, hasError: false),
            credentialConfigured: config.hasDiscordBotToken() && config.hasDiscordClientID(),
            blockingReason: missingDiscordCredentialMessage(config: config),
            requiredCredentials: ["discord_bot_token", "discord_client_id"],
            metadata: discordMetadata(config: config)
        )
    }

    func start() async {
        try? await bridgeRef.stop()

        guard config.discordEnabled else {
            lastStatus = CommunicationPlatformStatus(
                platform: .discord,
                enabled: false,
                connected: false,
                setupState: discordSetupState(config: config),
                statusLabel: discordStatusLabel(config: config, connected: false, hasError: false),
                credentialConfigured: config.hasDiscordBotToken() && config.hasDiscordClientID(),
                blockingReason: missingDiscordCredentialMessage(config: config),
                requiredCredentials: ["discord_bot_token", "discord_client_id"],
                metadata: discordMetadata(config: config)
            )
            return
        }

        guard config.hasDiscordBotToken() else {
            lastStatus = CommunicationPlatformStatus(
                platform: .discord,
                enabled: true,
                connected: false,
                setupState: .missingCredentials,
                statusLabel: "Missing Credentials",
                credentialConfigured: false,
                blockingReason: missingDiscordCredentialMessage(config: config),
                requiredCredentials: ["discord_bot_token", "discord_client_id"],
                lastError: DiscordClientError.missingBotToken.localizedDescription,
                metadata: discordMetadata(config: config)
            )
            return
        }

        do {
            let currentUser = try await client.getCurrentUser()
            try await bridgeRef.start()
            let bridgeStatus = await awaitDiscordBridgeStatus()
            let bridgeUsable = discordBridgeIsUsable(bridgeStatus)
            let blockingReason = discordBlockingReason(
                bridgeStatus: bridgeStatus,
                currentUserID: currentUser.id
            ) ?? missingDiscordCredentialMessage(config: config)
            let hasBridgeError = blockingReason != nil && !bridgeUsable
            lastStatus = CommunicationPlatformStatus(
                platform: .discord,
                enabled: true,
                connected: bridgeUsable,
                setupState: bridgeUsable ? .ready : (hasBridgeError ? .validationFailed : .partialSetup),
                statusLabel: discordStatusLabel(config: config, connected: bridgeUsable, hasError: hasBridgeError),
                connectedAccountName: bridgeStatus.botName ?? currentUser.globalName ?? currentUser.username,
                credentialConfigured: config.hasDiscordBotToken() && config.hasDiscordClientID(),
                blockingReason: blockingReason,
                requiredCredentials: ["discord_bot_token", "discord_client_id"],
                lastError: bridgeStatus.lastError,
                lastUpdatedAt: bridgeStatus.lastEventAt,
                metadata: discordMetadata(
                    config: config,
                    extra: [
                        "botUserID": bridgeStatus.botUserID ?? currentUser.id,
                        "lastGatewayEventType": bridgeStatus.lastGatewayEventType ?? "unknown",
                        "hasSeenMessageCreate": bridgeStatus.hasSeenMessageCreate ? "true" : "false",
                        "lastGatewayEventAt": bridgeStatus.lastEventAt.map(iso8601String(from:)) ?? "",
                        "lastInboundMessageAt": bridgeStatus.lastInboundMessageAt.map(iso8601String(from:)) ?? ""
                    ]
                )
            )
        } catch {
            let sanitizedError = sanitizeDiscordError(error)
            lastStatus = CommunicationPlatformStatus(
                platform: .discord,
                enabled: true,
                connected: false,
                setupState: .validationFailed,
                statusLabel: "Validation Failed",
                credentialConfigured: config.hasDiscordBotToken() && config.hasDiscordClientID(),
                blockingReason: sanitizedError,
                requiredCredentials: ["discord_bot_token", "discord_client_id"],
                lastError: sanitizedError,
                metadata: discordMetadata(config: config)
            )
        }
    }

    private func awaitDiscordBridgeStatus(timeoutNanoseconds: UInt64 = 8_000_000_000) async -> DiscordBridgeStatus {
        let start = DispatchTime.now().uptimeNanoseconds
        while DispatchTime.now().uptimeNanoseconds - start < timeoutNanoseconds {
            let status = await bridgeRef.currentStatus()
            if status.connected || status.lastError != nil {
                return status
            }
            try? await Task.sleep(nanoseconds: 250_000_000)
        }
        return await bridgeRef.currentStatus()
    }

    func stop() async {
        try? await bridgeRef.stop()
    }

    func status() async -> CommunicationPlatformStatus {
        let bridgeStatus = await bridgeRef.currentStatus()
        let bridgeUsable = discordBridgeIsUsable(bridgeStatus) || (bridgeStatus.connected && (lastStatus.metadata["botUserID"]?.isEmpty == false))
        let blockingReason = discordBlockingReason(
            bridgeStatus: bridgeStatus,
            currentUserID: lastStatus.metadata["botUserID"]
        ) ?? lastStatus.blockingReason ?? missingDiscordCredentialMessage(config: config)
        let setupState: CommunicationSetupState = if bridgeUsable {
            .ready
        } else if !lastStatus.credentialConfigured {
            .missingCredentials
        } else if (bridgeStatus.lastError ?? lastStatus.lastError) != nil {
            .validationFailed
        } else {
            .partialSetup
        }
        return CommunicationPlatformStatus(
            platform: .discord,
            enabled: lastStatus.enabled,
            connected: bridgeUsable,
            setupState: setupState,
            statusLabel: discordStatusLabel(
                config: config,
                connected: bridgeUsable,
                hasError: !bridgeUsable && (bridgeStatus.lastError ?? lastStatus.lastError ?? blockingReason) != nil
            ),
            connectedAccountName: bridgeStatus.botName ?? lastStatus.connectedAccountName,
            credentialConfigured: config.hasDiscordBotToken() && config.hasDiscordClientID(),
            blockingReason: bridgeUsable ? nil : blockingReason,
            requiredCredentials: lastStatus.requiredCredentials,
            lastError: bridgeUsable ? nil : (bridgeStatus.lastError ?? lastStatus.lastError),
            lastUpdatedAt: bridgeStatus.lastEventAt ?? lastStatus.lastUpdatedAt,
            metadata: discordMetadata(
                config: config,
                extra: lastStatus.metadata.merging([
                    "lastGatewayEventType": bridgeStatus.lastGatewayEventType ?? (lastStatus.metadata["lastGatewayEventType"] ?? "unknown"),
                    "hasSeenMessageCreate": bridgeStatus.hasSeenMessageCreate ? "true" : (lastStatus.metadata["hasSeenMessageCreate"] ?? "false"),
                    "lastGatewayEventAt": bridgeStatus.lastEventAt.map(iso8601String(from:)) ?? (lastStatus.metadata["lastGatewayEventAt"] ?? ""),
                    "lastInboundMessageAt": bridgeStatus.lastInboundMessageAt.map(iso8601String(from:)) ?? (lastStatus.metadata["lastInboundMessageAt"] ?? "")
                ]) { _, new in new }
            )
        )
    }

    func bridge() async -> (any ChatBridge)? {
        bridgeRef
    }
}

actor SlackCommunicationService: CommunicationPlatformService {
    let platform: ChatPlatform = .slack

    private let config: AtlasConfig
    private let client: any SlackClienting
    private let bridgeRef: SlackBridge
    private var lastStatus: CommunicationPlatformStatus

    init(
        config: AtlasConfig,
        client: any SlackClienting,
        bridge: SlackBridge
    ) {
        self.config = config
        self.client = client
        self.bridgeRef = bridge
        let credentialConfigured = config.hasSlackBotToken() && config.hasSlackAppToken()
        self.lastStatus = CommunicationPlatformStatus(
            platform: .slack,
            enabled: config.slackEnabled,
            connected: false,
            setupState: credentialConfigured ? .partialSetup : .missingCredentials,
            statusLabel: credentialConfigured ? "Needs Setup" : "Missing Credentials",
            credentialConfigured: credentialConfigured,
            blockingReason: credentialConfigured ? nil : "Add both the Slack bot token and Slack app token to finish setup.",
            requiredCredentials: ["slack_bot_token", "slack_app_token"],
            metadata: [
                "mode": "socket",
                "messageRouting": "dm+mention"
            ]
        )
    }

    func start() async {
        try? await bridgeRef.stop()

        let credentialConfigured = config.hasSlackBotToken() && config.hasSlackAppToken()
        guard config.slackEnabled else {
            lastStatus = CommunicationPlatformStatus(
                platform: .slack,
                enabled: false,
                connected: false,
                setupState: credentialConfigured ? .partialSetup : .missingCredentials,
                statusLabel: credentialConfigured ? "Needs Setup" : "Missing Credentials",
                credentialConfigured: credentialConfigured,
                blockingReason: credentialConfigured ? nil : "Add both the Slack bot token and Slack app token to finish setup.",
                requiredCredentials: ["slack_bot_token", "slack_app_token"],
                metadata: [
                    "mode": "socket",
                    "messageRouting": "dm+mention"
                ]
            )
            return
        }

        guard credentialConfigured else {
            lastStatus = CommunicationPlatformStatus(
                platform: .slack,
                enabled: true,
                connected: false,
                setupState: .missingCredentials,
                statusLabel: "Missing Credentials",
                credentialConfigured: false,
                blockingReason: "Add both the Slack bot token and Slack app token to finish setup.",
                requiredCredentials: ["slack_bot_token", "slack_app_token"],
                lastError: missingSlackCredentialMessage(config: config),
                metadata: [
                    "mode": "socket",
                    "messageRouting": "dm+mention"
                ]
            )
            return
        }

        do {
            let auth: SlackAuthTestResponse
            do {
                auth = try await client.authTestBot()
            } catch {
                let sanitizedError = sanitizeSlackError(error, phase: .botToken)
                lastStatus = CommunicationPlatformStatus(
                    platform: .slack,
                    enabled: true,
                    connected: false,
                    setupState: .validationFailed,
                    statusLabel: "Validation Failed",
                    credentialConfigured: true,
                    blockingReason: sanitizedError,
                    requiredCredentials: ["slack_bot_token", "slack_app_token"],
                    lastError: sanitizedError,
                    metadata: [
                        "mode": "socket",
                        "messageRouting": "dm+mention"
                    ]
                )
                return
            }

            do {
                _ = try await client.openSocketModeConnection()
            } catch {
                let sanitizedError = sanitizeSlackError(error, phase: .appToken)
                lastStatus = CommunicationPlatformStatus(
                    platform: .slack,
                    enabled: true,
                    connected: false,
                    setupState: .validationFailed,
                    statusLabel: "Validation Failed",
                    credentialConfigured: true,
                    blockingReason: sanitizedError,
                    requiredCredentials: ["slack_bot_token", "slack_app_token"],
                    lastError: sanitizedError,
                    metadata: [
                        "mode": "socket",
                        "messageRouting": "dm+mention"
                    ]
                )
                return
            }

            try await bridgeRef.start()
            let bridgeStatus = await awaitSlackBridgeStatus()
            let bridgeUsable = slackBridgeIsUsable(
                bridgeStatus,
                fallbackWorkspaceName: auth.team
            )
            let blockingReason = slackBlockingReason(
                bridgeStatus: bridgeStatus,
                fallbackWorkspaceName: auth.team
            )
            let hasBridgeError = blockingReason != nil && !bridgeUsable
            lastStatus = CommunicationPlatformStatus(
                platform: .slack,
                enabled: true,
                connected: bridgeUsable,
                setupState: bridgeUsable ? .ready : (hasBridgeError ? .validationFailed : .partialSetup),
                statusLabel: bridgeUsable ? "Ready" : (hasBridgeError ? "Validation Failed" : "Needs Setup"),
                connectedAccountName: auth.team ?? bridgeStatus.workspaceName,
                credentialConfigured: true,
                blockingReason: blockingReason,
                requiredCredentials: ["slack_bot_token", "slack_app_token"],
                lastError: bridgeStatus.lastError,
                lastUpdatedAt: bridgeStatus.lastEventAt,
                metadata: [
                    "mode": "socket",
                    "messageRouting": "dm+mention",
                    "workspace": auth.team ?? "",
                    "botUserID": bridgeStatus.botUserID ?? auth.userID
                ]
            )
        } catch {
            let sanitizedError = sanitizeSlackError(error, phase: .bridge)
            lastStatus = CommunicationPlatformStatus(
                platform: .slack,
                enabled: true,
                connected: false,
                setupState: .validationFailed,
                statusLabel: "Validation Failed",
                credentialConfigured: true,
                blockingReason: sanitizedError,
                requiredCredentials: ["slack_bot_token", "slack_app_token"],
                lastError: sanitizedError,
                metadata: [
                    "mode": "socket",
                    "messageRouting": "dm+mention"
                ]
            )
        }
    }

    private func awaitSlackBridgeStatus(timeoutNanoseconds: UInt64 = 8_000_000_000) async -> SlackBridgeStatus {
        let start = DispatchTime.now().uptimeNanoseconds
        while DispatchTime.now().uptimeNanoseconds - start < timeoutNanoseconds {
            let status = await bridgeRef.currentStatus()
            if status.connected || status.lastError != nil {
                return status
            }
            try? await Task.sleep(nanoseconds: 250_000_000)
        }
        return await bridgeRef.currentStatus()
    }

    func stop() async {
        try? await bridgeRef.stop()
    }

    func status() async -> CommunicationPlatformStatus {
        let bridgeStatus = await bridgeRef.currentStatus()
        let bridgeUsable = slackBridgeIsUsable(
            bridgeStatus,
            fallbackWorkspaceName: lastStatus.connectedAccountName
        ) || (bridgeStatus.connected && (lastStatus.metadata["botUserID"]?.isEmpty == false))
        let blockingReason = slackBlockingReason(
            bridgeStatus: bridgeStatus,
            fallbackWorkspaceName: lastStatus.connectedAccountName
        ) ?? lastStatus.blockingReason
        let setupState: CommunicationSetupState = if bridgeUsable {
            .ready
        } else if !lastStatus.credentialConfigured {
            .missingCredentials
        } else if (bridgeStatus.lastError ?? lastStatus.lastError) != nil {
            .validationFailed
        } else {
            .partialSetup
        }
        return CommunicationPlatformStatus(
            platform: .slack,
            enabled: lastStatus.enabled,
            connected: bridgeUsable,
            setupState: setupState,
            statusLabel: bridgeUsable ? "Ready" : ((bridgeStatus.lastError ?? lastStatus.lastError ?? blockingReason) != nil ? "Validation Failed" : (!lastStatus.credentialConfigured ? "Missing Credentials" : "Needs Setup")),
            connectedAccountName: bridgeStatus.workspaceName ?? lastStatus.connectedAccountName,
            credentialConfigured: lastStatus.credentialConfigured,
            blockingReason: bridgeUsable ? nil : blockingReason,
            requiredCredentials: lastStatus.requiredCredentials,
            lastError: bridgeUsable ? nil : (bridgeStatus.lastError ?? lastStatus.lastError),
            lastUpdatedAt: bridgeStatus.lastEventAt ?? lastStatus.lastUpdatedAt,
            metadata: lastStatus.metadata
        )
    }

    func bridge() async -> (any ChatBridge)? {
        bridgeRef
    }
}

actor CommunicationManager {
    private let context: AgentContext
    private let logger: AtlasLogger
    private var services: [ChatPlatform: any CommunicationPlatformService] = [:]
    private var bridges: [ChatPlatform: any ChatBridge] = [:]
    private var telegramCommandRouter: TelegramCommandRouter?
    private var telegramRuntime: (any AtlasRuntimeHandling)?

    init(context: AgentContext, logger: AtlasLogger = .runtime) {
        self.context = context
        self.logger = logger
    }

    func start(
        telegramCommandRouter: TelegramCommandRouter,
        telegramRuntime: any AtlasRuntimeHandling
    ) async {
        self.telegramCommandRouter = telegramCommandRouter
        self.telegramRuntime = telegramRuntime
        await reloadServices(config: context.config)

        logger.info("Communication manager started", metadata: [
            "platforms": "\(services.keys.count)"
        ])
    }

    func stop() async {
        for service in services.values {
            await service.stop()
        }
    }

    func statusSnapshot() async -> CommunicationsSnapshot {
        let platforms = await services.keys.sorted(by: { $0.rawValue < $1.rawValue }).asyncMap { platform in
            await services[platform]?.status()
        }.compactMap { $0 }
        let channels = (try? await context.communicationSessionStore.allChannels()) ?? []
        return CommunicationsSnapshot(platforms: platforms, channels: channels)
    }

    func platformStatus(_ platform: ChatPlatform) async -> CommunicationPlatformStatus? {
        await services[platform]?.status()
    }

    func bridge(for platform: ChatPlatform) async -> (any ChatBridge)? {
        bridges[platform]
    }

    func validate(platform: ChatPlatform) async throws -> CommunicationPlatformStatus {
        guard let service = services[platform] else {
            throw CommunicationManagerError.unknownPlatform(platform.rawValue)
        }
        await service.start()
        guard let status = await services[platform]?.status() else {
            throw CommunicationManagerError.unavailablePlatform(platform.rawValue)
        }
        return status
    }

    func validate(
        platform: ChatPlatform,
        config overrideConfig: AtlasConfig
    ) async throws -> CommunicationPlatformStatus {
        guard let service = await makeService(for: platform, config: overrideConfig) else {
            throw CommunicationManagerError.unknownPlatform(platform.rawValue)
        }
        await service.start()
        let status = await service.status()
        await service.stop()
        return status
    }

    func updatePlatform(
        _ platform: ChatPlatform,
        enabled: Bool
    ) async throws -> CommunicationPlatformStatus {
        var snapshot = await AtlasConfigStore.shared.load()
        switch platform {
        case .telegram:
            snapshot.telegramEnabled = enabled
        case .discord:
            snapshot.discordEnabled = enabled
        case .slack:
            snapshot.slackEnabled = enabled
        case .whatsApp, .companion:
            throw CommunicationManagerError.unsupportedConfiguration(platform.rawValue)
        }
        try await AtlasConfigStore.shared.save(snapshot)
        AtlasConfig.seedSnapshot(snapshot)
        await reloadServices(config: context.config.derived(snapshot: snapshot))
        return try await validate(platform: platform)
    }

    func deliverAutomationResult(destination: CommunicationDestination, emoji: String, name: String, output: String) async {
        guard let bridge = bridges[destination.platform] else { return }
        await bridge.deliverAutomationResult(destination: destination, emoji: emoji, name: name, output: output)
    }

    func routeApprovalNotification(session: ChatSession, approval: ApprovalRequest) async {
        guard let bridge = bridges[session.platform] else { return }
        await bridge.notifyApprovalRequired(session: session, approval: approval)
    }

    func legacyTelegramStatus() async -> AtlasTelegramStatus {
        guard let service = services[.telegram] as? TelegramCommunicationService else {
            return AtlasTelegramStatus(enabled: context.config.telegramEnabled)
        }
        return await service.legacyStatus()
    }

    private func reloadServices(config: AtlasConfig) async {
        for service in services.values {
            await service.stop()
        }
        services.removeAll()
        bridges.removeAll()

        if telegramRuntime != nil, telegramCommandRouter != nil {
            if let telegramService = await makeService(for: .telegram, config: config) as? TelegramCommunicationService {
                services[.telegram] = telegramService
                if let telegramBridge = await telegramService.bridge() {
                    bridges[.telegram] = telegramBridge
                }
                await telegramService.start()
            }

            if let discordService = await makeService(for: .discord, config: config) as? DiscordCommunicationService {
                services[.discord] = discordService
                if let discordBridge = await discordService.bridge() {
                    bridges[.discord] = discordBridge
                }
                await discordService.start()
            }

            if let slackService = await makeService(for: .slack, config: config) as? SlackCommunicationService {
                services[.slack] = slackService
                if let slackBridge = await slackService.bridge() {
                    bridges[.slack] = slackBridge
                }
                await slackService.start()
            }
        }
    }

    private func makeService(
        for platform: ChatPlatform,
        config: AtlasConfig
    ) async -> (any CommunicationPlatformService)? {
        guard let telegramRuntime, let telegramCommandRouter else {
            return nil
        }

        switch platform {
        case .telegram:
            let telegramClient = TelegramClient(config: config)
            let telegramBridge = TelegramBridge(
                config: config,
                telegramClient: telegramClient,
                runtime: telegramRuntime,
                sessionStore: context.telegramSessionStore,
                commandRouter: telegramCommandRouter
            )
            return TelegramCommunicationService(
                config: config,
                bridge: telegramBridge,
                client: telegramClient
            )
        case .discord:
            let discordClient = DiscordClient(config: config)
            let discordBridge = DiscordBridge(
                config: config,
                client: discordClient,
                runtime: telegramRuntime,
                sessionStore: context.communicationSessionStore,
                commandRouter: DiscordCommandRouter(
                    config: config,
                    listAutomations: {
                        await self.context.gremlinManagingAdapter.listAutomationsForChat()
                    },
                    triggerAutomation: { nameOrID in
                        await self.context.gremlinManagingAdapter.triggerAutomationFromChat(nameOrID)
                    }
                )
            )
            return DiscordCommunicationService(
                config: config,
                client: discordClient,
                bridge: discordBridge
            )
        case .slack:
            let slackClient = SlackClient(config: config)
            let slackBridge = SlackBridge(
                config: config,
                client: slackClient,
                runtime: telegramRuntime,
                sessionStore: context.communicationSessionStore
            )
            return SlackCommunicationService(
                config: config,
                client: slackClient,
                bridge: slackBridge
            )
        case .whatsApp, .companion:
            return nil
        }
    }
}

private func sanitizeTelegramError(_ error: Error) -> String {
    switch error {
    case TelegramClientError.missingBotToken,
         TelegramClientError.invalidResponse,
         TelegramClientError.localFileMissing,
         TelegramClientError.missingFilePath:
        return error.localizedDescription
    case TelegramClientError.unexpectedStatusCode(let code, _):
        return "The Telegram Bot API returned status code \(code)."
    case TelegramClientError.apiError(let code, _):
        if let code {
            return "Telegram Bot API error \(code)."
        }
        return "Telegram Bot API returned an error."
    default:
        return error.localizedDescription
    }
}

private func sanitizeDiscordError(_ error: Error) -> String {
    switch error {
    case DiscordClientError.missingBotToken,
         DiscordClientError.invalidResponse,
         DiscordClientError.missingGatewayURL:
        return error.localizedDescription
    case DiscordClientError.unexpectedStatusCode(let code, _):
        if code == 401 {
            return "Discord rejected the bot token (401). Paste the Bot Token from the Discord Bot page, not the client secret or client ID."
        }
        return "The Discord API returned status code \(code)."
    case DiscordClientError.websocketClosed(let code, _):
        if code == 4014 {
            return "Discord rejected the gateway intents (4014). Enable the Message Content intent for the bot in the Discord Developer Portal."
        }
        if code == 4013 {
            return "Discord rejected the configured gateway intents (4013). Check the bot gateway intent settings in the Discord Developer Portal."
        }
        if let code {
            return "The Discord gateway closed the connection (code \(code))."
        }
        return "The Discord gateway connection closed."
    default:
        return error.localizedDescription
    }
}

private func missingDiscordCredentialMessage(config: AtlasConfig) -> String? {
    let missingBot = !config.hasDiscordBotToken()
    let missingClientID = !config.hasDiscordClientID()

    switch (missingBot, missingClientID) {
    case (true, true):
        return "Discord setup needs both a bot token and the app client ID."
    case (true, false):
        return "Discord setup needs the bot token."
    case (false, true):
        return "Discord setup needs the app client ID so Atlas can generate the install link."
    case (false, false):
        return nil
    }
}

private func discordSetupState(config: AtlasConfig) -> CommunicationSetupState {
    if config.hasDiscordBotToken() && config.hasDiscordClientID() {
        return .partialSetup
    }
    return .missingCredentials
}

private func discordStatusLabel(config: AtlasConfig, connected: Bool, hasError: Bool) -> String {
    if connected {
        return "Ready"
    }
    if hasError {
        return "Validation Failed"
    }
    if !config.hasDiscordBotToken() || !config.hasDiscordClientID() {
        return "Missing Credentials"
    }
    return "Needs Setup"
}

private func discordMetadata(config: AtlasConfig, extra: [String: String] = [:]) -> [String: String] {
    var metadata: [String: String] = [
        "mode": "gateway",
        "messageRouting": "dm+mention"
    ]
    if let installURL = config.discordInstallURL()?.absoluteString {
        metadata["installURL"] = installURL
    }
    metadata["clientIDConfigured"] = config.hasDiscordClientID() ? "true" : "false"
    for (key, value) in extra {
        metadata[key] = value
    }
    return metadata
}

private func iso8601String(from date: Date) -> String {
    ISO8601DateFormatter().string(from: date)
}

private func discordBridgeIsUsable(_ status: DiscordBridgeStatus) -> Bool {
    status.connected && status.lastError == nil && status.botUserID?.isEmpty == false
}

private func discordBlockingReason(
    bridgeStatus: DiscordBridgeStatus,
    currentUserID: String?
) -> String? {
    if let lastError = bridgeStatus.lastError {
        return lastError
    }
    if bridgeStatus.connected && (bridgeStatus.botUserID ?? currentUserID)?.isEmpty != false {
        return "Discord connected to the gateway, but Atlas could not confirm the bot identity yet."
    }
    return nil
}

private func slackBridgeIsUsable(
    _ status: SlackBridgeStatus,
    fallbackWorkspaceName: String?
) -> Bool {
    status.connected &&
    status.lastError == nil &&
    status.botUserID?.isEmpty == false &&
    ((status.workspaceName ?? fallbackWorkspaceName)?.isEmpty == false)
}

private func slackBlockingReason(
    bridgeStatus: SlackBridgeStatus,
    fallbackWorkspaceName: String?
) -> String? {
    if let lastError = bridgeStatus.lastError {
        return lastError
    }
    if bridgeStatus.connected && bridgeStatus.botUserID?.isEmpty != false {
        return "Slack connected, but Atlas could not confirm the bot user ID yet."
    }
    if bridgeStatus.connected && (bridgeStatus.workspaceName ?? fallbackWorkspaceName)?.isEmpty != false {
        return "Slack connected, but Atlas could not confirm the workspace identity yet."
    }
    return nil
}

private enum SlackValidationPhase {
    case botToken
    case appToken
    case bridge
}

private func sanitizeSlackError(_ error: Error, phase: SlackValidationPhase = .bridge) -> String {
    switch error {
    case SlackClientError.apiError(let message):
        return sanitizeSlackAPIError(message, phase: phase)
    case SlackClientError.missingBotToken,
         SlackClientError.missingAppToken,
         SlackClientError.invalidResponse,
         SlackClientError.encodingFailed:
        return error.localizedDescription
    case SlackClientError.unexpectedStatusCode(let code, _):
        return "The Slack API returned status code \(code)."
    default:
        return error.localizedDescription
    }
}

private func sanitizeSlackAPIError(_ message: String, phase: SlackValidationPhase) -> String {
    switch (phase, message) {
    case (.botToken, "token_revoked"):
        return "The Slack bot token has been revoked. Generate a new Bot User OAuth Token (xoxb-...) and update it in Atlas."
    case (.botToken, "not_allowed_token_type"):
        return "The Slack bot token must be a Bot User OAuth Token (xoxb-...), not an app-level token."
    case (.botToken, "invalid_auth"):
        return "The Slack bot token is invalid. Paste the Bot User OAuth Token (xoxb-...) from your Slack app."
    case (.appToken, "token_revoked"):
        return "The Slack app token has been revoked. Generate a new App-Level Token (xapp-...) and update it in Atlas."
    case (.appToken, "not_allowed_token_type"):
        return "The Slack app token must be an App-Level Token (xapp-...), not a bot token."
    case (.appToken, "invalid_auth"):
        return "The Slack app token is invalid. Paste the App-Level Token (xapp-...) from your Slack app."
    case (.appToken, "missing_scope"):
        return "The Slack app token is missing the connections:write scope required for Socket Mode."
    default:
        return "Slack API error: \(message)"
    }
}

private func missingSlackCredentialMessage(config: AtlasConfig) -> String {
    let missingBot = !config.hasSlackBotToken()
    let missingApp = !config.hasSlackAppToken()

    switch (missingBot, missingApp) {
    case (true, true):
        return "Slack requires both a bot token and an app token."
    case (true, false):
        return "Slack requires a bot token."
    case (false, true):
        return "Slack requires an app token."
    case (false, false):
        return "Slack credentials are configured."
    }
}

private extension Array {
    func asyncMap<T>(_ transform: (Element) async -> T?) async -> [T?] {
        var results: [T?] = []
        results.reserveCapacity(count)
        for element in self {
            results.append(await transform(element))
        }
        return results
    }
}
