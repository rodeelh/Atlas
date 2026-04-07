import Foundation
import AtlasLogging
import AtlasNetwork
import AtlasShared

public actor TelegramPollingService {
    private let config: AtlasConfig
    private let telegramClient: any TelegramClienting
    private let bridge: TelegramBridge
    private let logger: AtlasLogger

    private var pollingTask: Task<Void, Never>?
    private var nextUpdateOffset: Int?
    private var status: AtlasTelegramStatus

    public init(
        config: AtlasConfig,
        telegramClient: any TelegramClienting,
        bridge: TelegramBridge,
        logger: AtlasLogger = AtlasLogger(category: "telegram.polling")
    ) {
        self.config = config
        self.telegramClient = telegramClient
        self.bridge = bridge
        self.logger = logger
        self.status = AtlasTelegramStatus(enabled: config.telegramEnabled)
    }

    public func start() async throws {
        guard pollingTask == nil else { return }
        guard config.telegramEnabled else {
            status = AtlasTelegramStatus(enabled: false)
            return
        }

        logger.info("Starting Telegram polling service")
        _ = try await telegramClient.deleteWebhook()
        let me = try await telegramClient.getMe()

        status = AtlasTelegramStatus(
            enabled: true,
            connected: true,
            pollingActive: true,
            botUsername: me.result.username,
            lastError: nil,
            lastUpdateAt: nil
        )

        do {
            _ = try await telegramClient.setMyCommands(commands: Self.defaultCommands)
        } catch {
            logger.warning("Telegram command registration failed", metadata: [
                "error": error.localizedDescription
            ])
        }

        pollingTask = Task { [weak self] in
            await self?.runLoop()
        }
    }

    public func stop() async {
        pollingTask?.cancel()
        pollingTask = nil
        status = AtlasTelegramStatus(
            enabled: status.enabled,
            connected: status.connected,
            pollingActive: false,
            botUsername: status.botUsername,
            lastError: status.lastError,
            lastUpdateAt: status.lastUpdateAt
        )
        logger.info("Stopped Telegram polling service")
    }

    public func snapshot() -> AtlasTelegramStatus {
        status
    }

    private func runLoop() async {
        var retryDelay = config.telegramPollingRetryBaseSeconds

        while !Task.isCancelled {
            do {
                let response = try await telegramClient.getUpdates(
                    offset: nextUpdateOffset,
                    timeout: config.telegramPollingTimeoutSeconds
                )

                retryDelay = config.telegramPollingRetryBaseSeconds
                status = AtlasTelegramStatus(
                    enabled: true,
                    connected: true,
                    pollingActive: true,
                    botUsername: status.botUsername,
                    lastError: nil,
                    lastUpdateAt: status.lastUpdateAt
                )

                for update in response.result.sorted(by: { $0.updateID < $1.updateID }) {
                    nextUpdateOffset = update.updateID + 1
                    logger.info("Processing Telegram update", metadata: [
                        "update_id": "\(update.updateID)",
                        "next_offset": "\(nextUpdateOffset ?? 0)"
                    ])
                    await bridge.handle(update: update)
                    status = AtlasTelegramStatus(
                        enabled: true,
                        connected: true,
                        pollingActive: true,
                        botUsername: status.botUsername,
                        lastError: nil,
                        lastUpdateAt: .now
                    )
                }
            } catch {
                status = AtlasTelegramStatus(
                    enabled: true,
                    connected: false,
                    pollingActive: pollingTask != nil,
                    botUsername: status.botUsername,
                    lastError: error.localizedDescription,
                    lastUpdateAt: status.lastUpdateAt
                )

                logger.error("Telegram polling failed", metadata: [
                    "error": error.localizedDescription,
                    "retry_seconds": "\(retryDelay)"
                ])

                try? await Task.sleep(for: .seconds(retryDelay))
                retryDelay = min(retryDelay * 2, 30)
            }
        }
    }

    private static let defaultCommands: [TelegramBotCommand] = [
        TelegramBotCommand(command: "start", description: "Connect this chat to Atlas"),
        TelegramBotCommand(command: "help", description: "Show Atlas bot commands"),
        TelegramBotCommand(command: "status", description: "Show runtime and bot status"),
        TelegramBotCommand(command: "approvals", description: "List pending approvals"),
        TelegramBotCommand(command: "reset", description: "Start a new Atlas conversation")
    ]
}
