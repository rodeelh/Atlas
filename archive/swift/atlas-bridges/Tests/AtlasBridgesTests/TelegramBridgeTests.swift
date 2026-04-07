import XCTest
import AtlasBridges
import AtlasMemory
import AtlasNetwork
import AtlasShared
import Foundation

final class TelegramBridgeTests: XCTestCase {
    func testStatusCommandDoesNotInvokeRuntimeMessageFlow() async throws {
        let runtime = MockRuntime()
        let commandRouter = TelegramCommandRouter(config: AtlasConfig(telegramEnabled: true))
        let memoryStore = try MemoryStore(databasePath: temporaryDatabasePath())
        let sessionStore = TelegramSessionStore(memoryStore: memoryStore)
        let message = TelegramMessage(
            messageID: 1,
            from: TelegramUser(id: 10, isBot: false, firstName: "Alex"),
            chat: TelegramChat(id: 99, type: "private", title: nil, username: nil, firstName: "Alex", lastName: nil),
            date: nil,
            text: "/status",
            entities: [TelegramMessageEntity(type: "bot_command", offset: 0, length: 7)]
        )

        let result = await commandRouter.handle(.status, rawText: "/status", message: message, runtime: runtime, sessionStore: sessionStore)
        let invoked = await runtime.handledMessage()

        XCTAssertFalse(invoked)
        XCTAssertTrue(result.text.contains("Atlas is"))
        XCTAssertTrue(result.text.contains("Telegram:"))
    }

    func testBridgeRoutesNormalTextThroughRuntimeAndSendsReply() async throws {
        let runtime = MockRuntime()
        let telegramClient = MockTelegramClient()
        let memoryStore = try MemoryStore(databasePath: temporaryDatabasePath())
        let sessionStore = TelegramSessionStore(memoryStore: memoryStore)
        let bridge = TelegramBridge(
            config: AtlasConfig(telegramEnabled: true),
            telegramClient: telegramClient,
            runtime: runtime,
            sessionStore: sessionStore
        )

        let update = TelegramUpdate(
            updateID: 1,
            message: TelegramMessage(
                messageID: 42,
                from: TelegramUser(id: 20, isBot: false, firstName: "Taylor"),
                chat: TelegramChat(id: 555, type: "private", title: nil, username: nil, firstName: "Taylor", lastName: nil),
                date: nil,
                text: "Hello Atlas",
                entities: nil
            )
        )

        await bridge.handle(update: update)

        let routedMessage = await runtime.latestRequest()?.message
        let sentMessages = await telegramClient.sentMessageRequests()

        XCTAssertEqual(routedMessage, "Hello Atlas")
        XCTAssertEqual(sentMessages.last?.text, "Bridge test response")
    }

    func testResetCommandRotatesSessionConversation() async throws {
        let runtime = MockRuntime()
        let telegramClient = MockTelegramClient()
        let memoryStore = try MemoryStore(databasePath: temporaryDatabasePath())
        let sessionStore = TelegramSessionStore(memoryStore: memoryStore)
        let bridge = TelegramBridge(
            config: AtlasConfig(telegramEnabled: true),
            telegramClient: telegramClient,
            runtime: runtime,
            sessionStore: sessionStore
        )

        _ = try await sessionStore.resolveSession(chatID: 321, userID: 54, lastMessageID: 1)
        let before = try await sessionStore.session(forChatID: 321)

        let update = TelegramUpdate(
            updateID: 2,
            message: TelegramMessage(
                messageID: 2,
                from: TelegramUser(id: 54, isBot: false, firstName: "Jamie"),
                chat: TelegramChat(id: 321, type: "private", title: nil, username: nil, firstName: "Jamie", lastName: nil),
                date: nil,
                text: "/reset",
                entities: [TelegramMessageEntity(type: "bot_command", offset: 0, length: 6)]
            )
        )

        await bridge.handle(update: update)

        let after = try await sessionStore.session(forChatID: 321)
        let sentMessages = await telegramClient.sentMessageRequests()

        XCTAssertNotEqual(before?.activeConversationID, after?.activeConversationID)
        XCTAssertTrue(sentMessages.last?.text.contains("new conversation") == true)
    }

    func testBridgeUploadsGeneratedImagesReturnedByRuntime() async throws {
        let fileURL = FileManager.default.temporaryDirectory
            .appendingPathComponent("atlas-telegram-image-\(UUID().uuidString)")
            .appendingPathExtension("png")
        try Data("fake-image-data".utf8).write(to: fileURL)
        defer { try? FileManager.default.removeItem(at: fileURL) }

        let runtime = MockRuntime(
            responseFactory: { request in
                let toolCallID = UUID()
                let outputJSON = """
                {
                  "providerUsed": "Google Nano Banana",
                  "promptUsed": "Create a blue icon",
                  "imageCount": 1,
                  "images": [
                    {
                      "id": "\(UUID().uuidString)",
                      "filePath": "\(fileURL.path)",
                      "fileName": "\(fileURL.lastPathComponent)",
                      "mimeType": "image/png",
                      "byteCount": 14
                    }
                  ],
                  "metadataSummary": "Generated 1 image."
                }
                """

                return AtlasMessageResponseEnvelope(
                    conversation: AtlasConversation(
                        id: request.conversationID ?? UUID(),
                        messages: [
                            AtlasMessage(role: .user, content: request.message),
                            AtlasMessage(role: .assistant, content: "Here’s the generated image.")
                        ]
                    ),
                    response: AtlasAgentResponse(
                        assistantMessage: "How would you like me to present it?",
                        toolCalls: [
                            AtlasToolCall(
                                toolName: "skill__image_generation__image_generate",
                                argumentsJSON: #"{"prompt":"Create a blue icon"}"#,
                                permissionLevel: .read,
                                requiresApproval: false,
                                status: .completed
                            )
                        ],
                        status: .completed,
                        toolResults: [
                            AtlasToolResult(
                                toolCallID: toolCallID,
                                output: outputJSON,
                                success: true
                            )
                        ]
                    )
                )
            },
            toolCallDecorator: { envelope in
                guard let first = envelope.response.toolCalls.first else { return envelope }
                let updatedToolCall = AtlasToolCall(
                    id: first.id,
                    toolName: first.toolName,
                    argumentsJSON: first.argumentsJSON,
                    permissionLevel: first.permissionLevel,
                    requiresApproval: first.requiresApproval,
                    status: first.status
                )
                let toolResults = envelope.response.toolResults.map {
                    AtlasToolResult(
                        toolCallID: updatedToolCall.id,
                        output: $0.output,
                        success: $0.success,
                        errorMessage: $0.errorMessage,
                        timestamp: $0.timestamp
                    )
                }
                return AtlasMessageResponseEnvelope(
                    conversation: envelope.conversation,
                    response: AtlasAgentResponse(
                        assistantMessage: envelope.response.assistantMessage,
                        toolCalls: [updatedToolCall],
                        status: envelope.response.status,
                        toolResults: toolResults,
                        pendingApprovals: envelope.response.pendingApprovals,
                        errorMessage: envelope.response.errorMessage
                    )
                )
            }
        )
        let telegramClient = MockTelegramClient()
        let memoryStore = try MemoryStore(databasePath: temporaryDatabasePath())
        let sessionStore = TelegramSessionStore(memoryStore: memoryStore)
        let bridge = TelegramBridge(
            config: AtlasConfig(telegramEnabled: true),
            telegramClient: telegramClient,
            runtime: runtime,
            sessionStore: sessionStore
        )

        let update = TelegramUpdate(
            updateID: 3,
            message: TelegramMessage(
                messageID: 77,
                from: TelegramUser(id: 20, isBot: false, firstName: "Taylor"),
                chat: TelegramChat(id: 555, type: "private", title: nil, username: nil, firstName: "Taylor", lastName: nil),
                date: nil,
                text: "Generate a blue icon",
                entities: nil
            )
        )

        await bridge.handle(update: update)

        let sentMessages = await telegramClient.sentMessageRequests()
        let sentPhotos = await telegramClient.sentPhotoRequests()

        XCTAssertEqual(
            sentMessages.first?.text,
            "I’m generating that image now. I’ll send it here as soon as it’s ready."
        )
        XCTAssertEqual(sentMessages.last?.text, "Here’s the generated image.")
        XCTAssertEqual(sentPhotos.count, 1)
        XCTAssertEqual(sentPhotos.first?.photoURL.path, fileURL.path)
    }

    func testBridgeDoesNotSendImageAcknowledgementForNormalText() async throws {
        let runtime = MockRuntime()
        let telegramClient = MockTelegramClient()
        let memoryStore = try MemoryStore(databasePath: temporaryDatabasePath())
        let sessionStore = TelegramSessionStore(memoryStore: memoryStore)
        let bridge = TelegramBridge(
            config: AtlasConfig(telegramEnabled: true),
            telegramClient: telegramClient,
            runtime: runtime,
            sessionStore: sessionStore
        )

        let update = TelegramUpdate(
            updateID: 4,
            message: TelegramMessage(
                messageID: 78,
                from: TelegramUser(id: 20, isBot: false, firstName: "Taylor"),
                chat: TelegramChat(id: 555, type: "private", title: nil, username: nil, firstName: "Taylor", lastName: nil),
                date: nil,
                text: "What’s the weather in Orlando?",
                entities: nil
            )
        )

        await bridge.handle(update: update)

        let sentMessages = await telegramClient.sentMessageRequests()

        XCTAssertEqual(sentMessages.count, 1)
        XCTAssertEqual(sentMessages.first?.text, "Bridge test response")
    }

    func testBridgeRoutesInboundPhotoToRuntime() async throws {
        let telegramClient = MockTelegramClient()
        await telegramClient.setFileResponse(
            TelegramGetFileResponse(
                ok: true,
                result: TelegramFile(
                    fileID: "photo-file-id",
                    fileUniqueID: "photo-unique-id",
                    fileSize: 16,
                    filePath: "photos/test-photo.jpg"
                )
            ),
            for: "photo-file-id"
        )
        let photoData = Data("fake-photo-bytes".utf8)
        await telegramClient.setDownloadedData(photoData, forPath: "photos/test-photo.jpg")

        let runtime = MockRuntime()
        let memoryStore = try MemoryStore(databasePath: temporaryDatabasePath())
        let sessionStore = TelegramSessionStore(memoryStore: memoryStore)
        let bridge = TelegramBridge(
            config: AtlasConfig(telegramEnabled: true),
            telegramClient: telegramClient,
            runtime: runtime,
            sessionStore: sessionStore
        )

        let update = TelegramUpdate(
            updateID: 5,
            message: TelegramMessage(
                messageID: 90,
                from: TelegramUser(id: 20, isBot: false, firstName: "Taylor"),
                chat: TelegramChat(id: 777, type: "private", title: nil, username: nil, firstName: "Taylor", lastName: nil),
                date: nil,
                caption: "What is in this photo?",
                entities: nil,
                photo: [
                    TelegramPhotoSize(fileID: "small-id", fileUniqueID: "small", width: 90, height: 90, fileSize: 8),
                    TelegramPhotoSize(fileID: "photo-file-id", fileUniqueID: "photo-unique-id", width: 1024, height: 1024, fileSize: 16)
                ]
            )
        )

        await bridge.handle(update: update)

        let sentMessages = await telegramClient.sentMessageRequests()
        let handledMessage = await runtime.handledMessage()
        let lastRequest = await runtime.latestRequest()

        XCTAssertTrue(handledMessage)
        XCTAssertEqual(sentMessages.last?.text, "Bridge test response")
        XCTAssertEqual(lastRequest?.message, "What is in this photo?")
        XCTAssertEqual(lastRequest?.attachments.count, 1)
        XCTAssertEqual(lastRequest?.attachments.first?.data, photoData.base64EncodedString())
        XCTAssertEqual(lastRequest?.attachments.first?.mimeType, "image/jpeg")
    }

    func testBridgeHoldsPhotoWithNoCaptionAndAcknowledges() async throws {
        let telegramClient = MockTelegramClient()
        await telegramClient.setFileResponse(
            TelegramGetFileResponse(
                ok: true,
                result: TelegramFile(
                    fileID: "photo-file-id",
                    fileUniqueID: "photo-unique-id",
                    fileSize: 16,
                    filePath: "photos/test-photo.jpg"
                )
            ),
            for: "photo-file-id"
        )
        await telegramClient.setDownloadedData(Data("fake-photo-bytes".utf8), forPath: "photos/test-photo.jpg")

        let runtime = MockRuntime()
        let memoryStore = try MemoryStore(databasePath: temporaryDatabasePath())
        let sessionStore = TelegramSessionStore(memoryStore: memoryStore)
        let bridge = TelegramBridge(
            config: AtlasConfig(telegramEnabled: true),
            telegramClient: telegramClient,
            runtime: runtime,
            sessionStore: sessionStore
        )

        let update = TelegramUpdate(
            updateID: 5,
            message: TelegramMessage(
                messageID: 90,
                from: TelegramUser(id: 20, isBot: false, firstName: "Taylor"),
                chat: TelegramChat(id: 777, type: "private", title: nil, username: nil, firstName: "Taylor", lastName: nil),
                date: nil,
                caption: nil,
                entities: nil,
                photo: [
                    TelegramPhotoSize(fileID: "photo-file-id", fileUniqueID: "photo-unique-id", width: 1024, height: 1024, fileSize: 16)
                ]
            )
        )

        await bridge.handle(update: update)

        let sentMessages = await telegramClient.sentMessageRequests()
        let handledMessage = await runtime.handledMessage()

        // No caption → should NOT call runtime, just acknowledge
        XCTAssertFalse(handledMessage)
        XCTAssertEqual(sentMessages.last?.text, "Got your image! What would you like me to do with it?")
    }

    func testBridgeAttachesPendingPhotoToFollowUpMessage() async throws {
        let telegramClient = MockTelegramClient()
        await telegramClient.setFileResponse(
            TelegramGetFileResponse(
                ok: true,
                result: TelegramFile(
                    fileID: "photo-file-id",
                    fileUniqueID: "photo-unique-id",
                    fileSize: 16,
                    filePath: "photos/test-photo.jpg"
                )
            ),
            for: "photo-file-id"
        )
        let photoData = Data("fake-photo-bytes".utf8)
        await telegramClient.setDownloadedData(photoData, forPath: "photos/test-photo.jpg")

        let runtime = MockRuntime()
        let memoryStore = try MemoryStore(databasePath: temporaryDatabasePath())
        let sessionStore = TelegramSessionStore(memoryStore: memoryStore)
        let bridge = TelegramBridge(
            config: AtlasConfig(telegramEnabled: true),
            telegramClient: telegramClient,
            runtime: runtime,
            sessionStore: sessionStore
        )

        // Step 1: send photo without caption
        await bridge.handle(update: TelegramUpdate(
            updateID: 5,
            message: TelegramMessage(
                messageID: 90,
                from: TelegramUser(id: 20, isBot: false, firstName: "Taylor"),
                chat: TelegramChat(id: 777, type: "private", title: nil, username: nil, firstName: "Taylor", lastName: nil),
                date: nil,
                caption: nil,
                entities: nil,
                photo: [TelegramPhotoSize(fileID: "photo-file-id", fileUniqueID: "photo-unique-id", width: 1024, height: 1024, fileSize: 16)]
            )
        ))

        let handledAfterPhoto = await runtime.handledMessage()
        XCTAssertFalse(handledAfterPhoto)

        // Step 2: follow-up text question
        await bridge.handle(update: TelegramUpdate(
            updateID: 6,
            message: TelegramMessage(
                messageID: 91,
                from: TelegramUser(id: 20, isBot: false, firstName: "Taylor"),
                chat: TelegramChat(id: 777, type: "private", title: nil, username: nil, firstName: "Taylor", lastName: nil),
                date: nil,
                text: "What colour is the car?",
                entities: nil
            )
        ))

        let lastRequest = await runtime.latestRequest()
        let handledAfterFollowUp = await runtime.handledMessage()

        XCTAssertTrue(handledAfterFollowUp)
        XCTAssertEqual(lastRequest?.message, "What colour is the car?")
        XCTAssertEqual(lastRequest?.attachments.count, 1)
        XCTAssertEqual(lastRequest?.attachments.first?.data, photoData.base64EncodedString())
    }

    func testBridgeSendsDiscoveredFilesFromFileExplorerResults() async throws {
        let imageURL = FileManager.default.temporaryDirectory
            .appendingPathComponent("atlas-discovered-\(UUID().uuidString)")
            .appendingPathExtension("png")
        try Data([0x89, 0x50, 0x4E, 0x47]).write(to: imageURL)
        defer { try? FileManager.default.removeItem(at: imageURL) }

        let runtime = MockRuntime(
            responseFactory: { request in
                let toolCallID = UUID()
                let outputJSON = """
                {
                  "entries": [
                    {
                      "name": "\(imageURL.lastPathComponent)",
                      "path": "\(imageURL.path)",
                      "type": "file",
                      "size": 4
                    }
                  ]
                }
                """

                return AtlasMessageResponseEnvelope(
                    conversation: AtlasConversation(
                        id: request.conversationID ?? UUID(),
                        messages: [
                            AtlasMessage(role: .user, content: request.message),
                            AtlasMessage(role: .assistant, content: "I found the image.")
                        ]
                    ),
                    response: AtlasAgentResponse(
                        assistantMessage: "I found the image.",
                        toolCalls: [
                            AtlasToolCall(
                                id: toolCallID,
                                toolName: "skill__file_system__fs_list_directory",
                                argumentsJSON: #"{"path":"/Users/ralhassan/Library/Application Support/ProjectAtlas/ImageArtifacts"}"#,
                                permissionLevel: .read,
                                requiresApproval: false,
                                status: .completed
                            )
                        ],
                        status: .completed,
                        toolResults: [
                            AtlasToolResult(
                                toolCallID: toolCallID,
                                output: outputJSON,
                                success: true
                            )
                        ]
                    )
                )
            }
        )
        let telegramClient = MockTelegramClient()
        let memoryStore = try MemoryStore(databasePath: temporaryDatabasePath())
        let sessionStore = TelegramSessionStore(memoryStore: memoryStore)
        let bridge = TelegramBridge(
            config: AtlasConfig(telegramEnabled: true),
            telegramClient: telegramClient,
            runtime: runtime,
            sessionStore: sessionStore
        )

        let update = TelegramUpdate(
            updateID: 7,
            message: TelegramMessage(
                messageID: 92,
                from: TelegramUser(id: 21, isBot: false, firstName: "Jordan"),
                chat: TelegramChat(id: 778, type: "private", title: nil, username: nil, firstName: "Jordan", lastName: nil),
                date: nil,
                text: "Send me all the images",
                caption: nil,
                entities: nil,
                photo: nil,
                document: nil
            )
        )

        await bridge.handle(update: update)

        let sentPhotos = await telegramClient.sentPhotoRequests()
        XCTAssertEqual(sentPhotos.count, 1)
        XCTAssertEqual(sentPhotos.first?.photoURL.path, imageURL.path)
    }

    // MARK: - Reactions

    func testBridgeSendsEyesReactionOnlyForActionMessages() async throws {
        let runtime = MockRuntime()
        let telegramClient = MockTelegramClient()
        let memoryStore = try MemoryStore(databasePath: temporaryDatabasePath())
        let sessionStore = TelegramSessionStore(memoryStore: memoryStore)
        let bridge = TelegramBridge(
            config: AtlasConfig(telegramEnabled: true),
            telegramClient: telegramClient,
            runtime: runtime,
            sessionStore: sessionStore
        )

        // Conversational message — should NOT get 👀
        await bridge.handle(update: TelegramUpdate(
            updateID: 10,
            message: TelegramMessage(
                messageID: 55,
                from: TelegramUser(id: 20, isBot: false, firstName: "Rami"),
                chat: TelegramChat(id: 100, type: "private"),
                text: "Hello, how are you?"
            )
        ))

        let reactionsAfterChat = await telegramClient.sentReactionRequests()
        XCTAssertFalse(reactionsAfterChat.contains(where: { $0.emoji == "👀" }),
                       "Conversational message should not trigger 👀")
    }

    func testBridgeSendsEyesReactionForActionRequest() async throws {
        let runtime = MockRuntime()
        let telegramClient = MockTelegramClient()
        let memoryStore = try MemoryStore(databasePath: temporaryDatabasePath())
        let sessionStore = TelegramSessionStore(memoryStore: memoryStore)
        let bridge = TelegramBridge(
            config: AtlasConfig(telegramEnabled: true),
            telegramClient: telegramClient,
            runtime: runtime,
            sessionStore: sessionStore
        )

        // Action message — should get 👀
        await bridge.handle(update: TelegramUpdate(
            updateID: 11,
            message: TelegramMessage(
                messageID: 66,
                from: TelegramUser(id: 20, isBot: false, firstName: "Rami"),
                chat: TelegramChat(id: 100, type: "private"),
                text: "Search the web for Swift concurrency best practices"
            )
        ))

        let reactions = await telegramClient.sentReactionRequests()
        XCTAssertTrue(reactions.contains(where: { $0.emoji == "👀" && $0.messageID == 66 }),
                      "Action message should trigger 👀 reaction")
    }

    func testBridgeSendsCheckmarkOnlyWhenToolsRan() async throws {
        // MockRuntime returns a response with no tool calls → no ✅
        let runtime = MockRuntime()
        let telegramClient = MockTelegramClient()
        let memoryStore = try MemoryStore(databasePath: temporaryDatabasePath())
        let sessionStore = TelegramSessionStore(memoryStore: memoryStore)
        let bridge = TelegramBridge(
            config: AtlasConfig(telegramEnabled: true),
            telegramClient: telegramClient,
            runtime: runtime,
            sessionStore: sessionStore
        )

        await bridge.handle(update: TelegramUpdate(
            updateID: 12,
            message: TelegramMessage(
                messageID: 77,
                from: TelegramUser(id: 20, isBot: false, firstName: "Rami"),
                chat: TelegramChat(id: 100, type: "private"),
                text: "What is 2 + 2?"
            )
        ))

        let reactions = await telegramClient.sentReactionRequests()
        XCTAssertFalse(reactions.contains(where: { $0.emoji == "✅" }),
                       "No ✅ when runtime returned no tool calls")
    }

    func testBridgeSendsHeartReactionOnPraise() async throws {
        let runtime = MockRuntime()
        let telegramClient = MockTelegramClient()
        let memoryStore = try MemoryStore(databasePath: temporaryDatabasePath())
        let sessionStore = TelegramSessionStore(memoryStore: memoryStore)
        let bridge = TelegramBridge(
            config: AtlasConfig(telegramEnabled: true),
            telegramClient: telegramClient,
            runtime: runtime,
            sessionStore: sessionStore
        )

        for praise in ["thank you!", "Thanks so much", "amazing job", "you're the best", "<3"] {
            let telegramClient2 = MockTelegramClient()
            let bridge2 = TelegramBridge(
                config: AtlasConfig(telegramEnabled: true),
                telegramClient: telegramClient2,
                runtime: runtime,
                sessionStore: sessionStore
            )
            await bridge2.handle(update: TelegramUpdate(
                updateID: Int.random(in: 100..<999),
                message: TelegramMessage(
                    messageID: Int.random(in: 1..<99),
                    from: TelegramUser(id: 20, isBot: false, firstName: "Rami"),
                    chat: TelegramChat(id: 100, type: "private"),
                    text: praise
                )
            ))
            let reactions = await telegramClient2.sentReactionRequests()
            XCTAssertTrue(reactions.contains(where: { $0.emoji == "❤" }),
                          "Expected ❤ reaction for '\(praise)'")
        }

        _ = bridge // suppress unused warning
    }

    func testBridgeDoesNotSendHeartForNonPraise() async throws {
        let runtime = MockRuntime()
        let telegramClient = MockTelegramClient()
        let memoryStore = try MemoryStore(databasePath: temporaryDatabasePath())
        let sessionStore = TelegramSessionStore(memoryStore: memoryStore)
        let bridge = TelegramBridge(
            config: AtlasConfig(telegramEnabled: true),
            telegramClient: telegramClient,
            runtime: runtime,
            sessionStore: sessionStore
        )
        await bridge.handle(update: TelegramUpdate(
            updateID: 50,
            message: TelegramMessage(
                messageID: 50,
                from: TelegramUser(id: 20, isBot: false, firstName: "Rami"),
                chat: TelegramChat(id: 100, type: "private"),
                text: "What's the weather today?"
            )
        ))
        let reactions = await telegramClient.sentReactionRequests()
        XCTAssertFalse(reactions.contains(where: { $0.emoji == "❤" }),
                       "Should not react with ❤ for non-praise messages")
    }

    func testBridgeSendsShockReactionOnShockingMessage() async throws {
        let runtime = MockRuntime()
        let memoryStore = try MemoryStore(databasePath: temporaryDatabasePath())
        let sessionStore = TelegramSessionStore(memoryStore: memoryStore)

        for phrase in ["omg no way", "whoa that's wild", "I can't believe this", "unbelievable!"] {
            let telegramClient = MockTelegramClient()
            let bridge = TelegramBridge(
                config: AtlasConfig(telegramEnabled: true),
                telegramClient: telegramClient,
                runtime: runtime,
                sessionStore: sessionStore
            )
            await bridge.handle(update: TelegramUpdate(
                updateID: Int.random(in: 1000..<9999),
                message: TelegramMessage(
                    messageID: Int.random(in: 1..<99),
                    from: TelegramUser(id: 20, isBot: false, firstName: "Rami"),
                    chat: TelegramChat(id: 100, type: "private"),
                    text: phrase
                )
            ))
            let reactions = await telegramClient.sentReactionRequests()
            XCTAssertTrue(reactions.contains(where: { $0.emoji == "🤯" }),
                          "Expected 🤯 reaction for '\(phrase)'")
        }
    }

    func testBridgeShockTakesPrecedenceOverProcessing() async throws {
        // A message that matches both shock and action terms should get 🤯, not 👀
        let runtime = MockRuntime()
        let telegramClient = MockTelegramClient()
        let memoryStore = try MemoryStore(databasePath: temporaryDatabasePath())
        let sessionStore = TelegramSessionStore(memoryStore: memoryStore)
        let bridge = TelegramBridge(
            config: AtlasConfig(telegramEnabled: true),
            telegramClient: telegramClient,
            runtime: runtime,
            sessionStore: sessionStore
        )
        await bridge.handle(update: TelegramUpdate(
            updateID: 200,
            message: TelegramMessage(
                messageID: 200,
                from: TelegramUser(id: 20, isBot: false, firstName: "Rami"),
                chat: TelegramChat(id: 100, type: "private"),
                text: "omg search for this file immediately"
            )
        ))
        let reactions = await telegramClient.sentReactionRequests()
        XCTAssertFalse(reactions.contains(where: { $0.emoji == "👀" }), "👀 should not fire when 🤯 matches")
    }

    func testBridgeHeartTakesPrecedenceOverShock() async throws {
        // Praise wins over everything
        let runtime = MockRuntime()
        let telegramClient = MockTelegramClient()
        let memoryStore = try MemoryStore(databasePath: temporaryDatabasePath())
        let sessionStore = TelegramSessionStore(memoryStore: memoryStore)
        let bridge = TelegramBridge(
            config: AtlasConfig(telegramEnabled: true),
            telegramClient: telegramClient,
            runtime: runtime,
            sessionStore: sessionStore
        )
        await bridge.handle(update: TelegramUpdate(
            updateID: 201,
            message: TelegramMessage(
                messageID: 201,
                from: TelegramUser(id: 20, isBot: false, firstName: "Rami"),
                chat: TelegramChat(id: 100, type: "private"),
                text: "omg thank you so much!"
            )
        ))
        let reactions = await telegramClient.sentReactionRequests()
        XCTAssertTrue(reactions.contains(where: { $0.emoji == "❤" }), "❤ should fire over 🤯")
        XCTAssertFalse(reactions.contains(where: { $0.emoji == "🤯" }), "🤯 should not fire when ❤ matches")
    }

    func testBridgeSendsCheckmarkWhenToolsRan() async throws {
        let toolCallID = UUID()
        let runtime = MockRuntime(responseFactory: { request in
            AtlasMessageResponseEnvelope(
                conversation: AtlasConversation(
                    id: request.conversationID ?? UUID(),
                    messages: [AtlasMessage(role: .assistant, content: "Done")]
                ),
                response: AtlasAgentResponse(
                    assistantMessage: "Done",
                    toolCalls: [
                        AtlasToolCall(
                            id: toolCallID,
                            toolName: "skill__file_system__fs_read_file",
                            argumentsJSON: "{}",
                            permissionLevel: .read,
                            requiresApproval: false,
                            status: .completed
                        )
                    ],
                    status: .completed,
                    toolResults: [AtlasToolResult(toolCallID: toolCallID, output: "{}", success: true)]
                )
            )
        })
        let telegramClient = MockTelegramClient()
        let memoryStore = try MemoryStore(databasePath: temporaryDatabasePath())
        let sessionStore = TelegramSessionStore(memoryStore: memoryStore)
        let bridge = TelegramBridge(
            config: AtlasConfig(telegramEnabled: true),
            telegramClient: telegramClient,
            runtime: runtime,
            sessionStore: sessionStore
        )

        await bridge.handle(update: TelegramUpdate(
            updateID: 13,
            message: TelegramMessage(
                messageID: 88,
                from: TelegramUser(id: 20, isBot: false, firstName: "Rami"),
                chat: TelegramChat(id: 100, type: "private"),
                text: "Read my file"
            )
        ))

        let reactions = await telegramClient.sentReactionRequests()
        XCTAssertTrue(reactions.contains(where: { $0.emoji == "✅" && $0.messageID == 88 }),
                      "✅ should fire when tools actually ran")
    }

    // MARK: - HTML parse mode

    func testBridgeSendsHTMLParseModeOnReply() async throws {
        let runtime = MockRuntime()
        let telegramClient = MockTelegramClient()
        let memoryStore = try MemoryStore(databasePath: temporaryDatabasePath())
        let sessionStore = TelegramSessionStore(memoryStore: memoryStore)
        let bridge = TelegramBridge(
            config: AtlasConfig(telegramEnabled: true),
            telegramClient: telegramClient,
            runtime: runtime,
            sessionStore: sessionStore
        )

        await bridge.handle(update: TelegramUpdate(
            updateID: 13,
            message: TelegramMessage(
                messageID: 88,
                from: TelegramUser(id: 20, isBot: false, firstName: "Rami"),
                chat: TelegramChat(id: 100, type: "private"),
                text: "Test"
            )
        ))

        let messages = await telegramClient.sentMessageRequests()
        let replyMessage = messages.last(where: { $0.chatID == 100 })
        XCTAssertEqual(replyMessage?.parseMode, "HTML", "AI reply should use HTML parse mode")
    }

    // MARK: - Location routing

    func testBridgeRoutesLocationMessageToRuntime() async throws {
        let runtime = MockRuntime()
        let telegramClient = MockTelegramClient()
        let memoryStore = try MemoryStore(databasePath: temporaryDatabasePath())
        let sessionStore = TelegramSessionStore(memoryStore: memoryStore)
        let bridge = TelegramBridge(
            config: AtlasConfig(telegramEnabled: true),
            telegramClient: telegramClient,
            runtime: runtime,
            sessionStore: sessionStore
        )

        let update = TelegramUpdate(
            updateID: 20,
            message: TelegramMessage(
                messageID: 200,
                from: TelegramUser(id: 30, isBot: false, firstName: "Alex"),
                chat: TelegramChat(id: 300, type: "private"),
                location: TelegramLocation(latitude: 37.774929, longitude: -122.419416)
            )
        )

        await bridge.handle(update: update)

        let handledMessage = await runtime.handledMessage()
        let lastRequest = await runtime.latestRequest()

        XCTAssertTrue(handledMessage, "Location message should be routed to runtime")
        XCTAssertTrue(lastRequest?.message.contains("📍") == true, "Routed message should contain 📍")
        XCTAssertTrue(lastRequest?.message.contains("37.774929") == true, "Routed message should contain latitude")
    }

    // MARK: - /approvals command

    func testApprovalsCommandWithNoPendingApprovals() async throws {
        let runtime = MockRuntime(pendingApprovals: [])
        let commandRouter = TelegramCommandRouter(config: AtlasConfig(telegramEnabled: true))
        let memoryStore = try MemoryStore(databasePath: temporaryDatabasePath())
        let sessionStore = TelegramSessionStore(memoryStore: memoryStore)
        let message = TelegramMessage(
            messageID: 1,
            from: TelegramUser(id: 10, isBot: false, firstName: "Rami"),
            chat: TelegramChat(id: 99, type: "private")
        )

        let result = await commandRouter.handle(.approvals, rawText: "/approvals", message: message, runtime: runtime, sessionStore: sessionStore)

        XCTAssertTrue(result.text.contains("all clear") || result.text.contains("No pending"),
                      "Empty approvals should return a clear message")
        XCTAssertNil(result.replyMarkup, "No markup expected when no approvals pending")
    }

    func testApprovalsCommandWithPendingApprovalsShowsInlineKeyboard() async throws {
        let toolCallID = UUID()
        let approval = ApprovalRequest(
            id: UUID(),
            toolCall: AtlasToolCall(
                id: toolCallID,
                toolName: "skill__file_system__fs_read_file",
                argumentsJSON: "{}",
                permissionLevel: .read,
                requiresApproval: true,
                status: .pending
            ),
            status: .pending
        )

        let runtime = MockRuntime(pendingApprovals: [approval])
        let commandRouter = TelegramCommandRouter(config: AtlasConfig(telegramEnabled: true))
        let memoryStore = try MemoryStore(databasePath: temporaryDatabasePath())
        let sessionStore = TelegramSessionStore(memoryStore: memoryStore)
        let message = TelegramMessage(
            messageID: 1,
            from: TelegramUser(id: 10, isBot: false, firstName: "Rami"),
            chat: TelegramChat(id: 99, type: "private")
        )

        let result = await commandRouter.handle(.approvals, rawText: "/approvals", message: message, runtime: runtime, sessionStore: sessionStore)

        XCTAssertNotNil(result.replyMarkup, "Expected inline keyboard for pending approvals")
        let keyboard = result.replyMarkup?.inlineKeyboard ?? []
        XCTAssertEqual(keyboard.count, 1, "One row per approval")
        let row = keyboard[0]
        XCTAssertEqual(row.count, 2, "Each row has approve + deny buttons")
        XCTAssertTrue(row[0].text.contains("✅"), "First button should be approve")
        XCTAssertTrue(row[1].text.contains("❌"), "Second button should be deny")
        XCTAssertTrue(row[0].callbackData?.contains(toolCallID.uuidString) == true,
                      "Callback data should contain the tool call ID")
    }

    func testApprovalsCommandWithMultiplePendingCapsAtFive() async throws {
        let approvals = (0..<7).map { i in
            ApprovalRequest(
                id: UUID(),
                toolCall: AtlasToolCall(
                    toolName: "skill__test__action_\(i)",
                    argumentsJSON: "{}",
                    permissionLevel: .read,
                    requiresApproval: true,
                    status: .pending
                ),
                status: .pending
            )
        }

        let runtime = MockRuntime(pendingApprovals: approvals)
        let commandRouter = TelegramCommandRouter(config: AtlasConfig(telegramEnabled: true))
        let memoryStore = try MemoryStore(databasePath: temporaryDatabasePath())
        let sessionStore = TelegramSessionStore(memoryStore: memoryStore)
        let message = TelegramMessage(
            messageID: 1,
            chat: TelegramChat(id: 99, type: "private")
        )

        let result = await commandRouter.handle(.approvals, rawText: "/approvals", message: message, runtime: runtime, sessionStore: sessionStore)

        let keyboard = result.replyMarkup?.inlineKeyboard ?? []
        XCTAssertLessThanOrEqual(keyboard.count, 5, "Should show at most 5 approval rows")
    }

    private func temporaryDatabasePath() -> String {
        FileManager.default.temporaryDirectory
            .appendingPathComponent(UUID().uuidString)
            .appendingPathExtension("sqlite3")
            .path
    }
}

private actor MockRuntime: AtlasRuntimeHandling {
    private(set) var didHandleMessage = false
    private(set) var lastRequest: AtlasMessageRequest?
    private let responseFactory: (AtlasMessageRequest) -> AtlasMessageResponseEnvelope
    private let toolCallDecorator: (AtlasMessageResponseEnvelope) -> AtlasMessageResponseEnvelope
    private let pendingApprovals: [ApprovalRequest]

    init(
        responseFactory: @escaping (AtlasMessageRequest) -> AtlasMessageResponseEnvelope = { request in
            AtlasMessageResponseEnvelope(
                conversation: AtlasConversation(
                    id: request.conversationID ?? UUID(),
                    messages: [
                        AtlasMessage(role: .user, content: request.message),
                        AtlasMessage(role: .assistant, content: "Bridge test response")
                    ]
                ),
                response: AtlasAgentResponse(
                    assistantMessage: "Bridge test response",
                    status: .completed
                )
            )
        },
        toolCallDecorator: @escaping (AtlasMessageResponseEnvelope) -> AtlasMessageResponseEnvelope = { $0 },
        pendingApprovals: [ApprovalRequest] = []
    ) {
        self.responseFactory = responseFactory
        self.toolCallDecorator = toolCallDecorator
        self.pendingApprovals = pendingApprovals
    }

    func handleMessage(_ request: AtlasMessageRequest) async -> AtlasMessageResponseEnvelope {
        didHandleMessage = true
        lastRequest = request
        return toolCallDecorator(responseFactory(request))
    }

    func status() async -> AtlasRuntimeStatus {
        AtlasRuntimeStatus(
            isRunning: true,
            activeConversationCount: 1,
            lastMessageAt: nil,
            lastError: nil,
            state: .ready,
            runtimePort: 1984,
            startedAt: nil,
            activeRequests: 0,
            pendingApprovalCount: pendingApprovals.count,
            details: "Ready",
            telegram: AtlasTelegramStatus(
                enabled: true,
                connected: true,
                pollingActive: true,
                botUsername: "atlas_test_bot"
            )
        )
    }

    func approvals() async -> [ApprovalRequest] {
        pendingApprovals
    }

    func approve(toolCallID: UUID) async throws -> AtlasMessageResponseEnvelope {
        throw MockRuntimeError.notSupported
    }

    func deny(toolCallID: UUID) async throws -> ApprovalRequest {
        throw MockRuntimeError.notSupported
    }

    func handledMessage() -> Bool {
        didHandleMessage
    }

    func latestRequest() -> AtlasMessageRequest? {
        lastRequest
    }
}

private enum MockRuntimeError: Error {
    case notSupported
}

private actor MockTelegramClient: TelegramClienting {
    private(set) var sentMessages: [TelegramSendMessageRequest] = []
    private(set) var sentPhotos: [(chatID: Int64, photoURL: URL, caption: String?, replyToMessageID: Int?)] = []
    private(set) var sentDocuments: [(chatID: Int64, documentURL: URL, caption: String?, replyToMessageID: Int?)] = []
    private(set) var sentReactions: [(chatID: Int64, messageID: Int, emoji: String)] = []
    private var fileResponses: [String: TelegramGetFileResponse] = [:]
    private var downloadedFiles: [String: Data] = [:]

    func getMe() async throws -> TelegramGetMeResponse {
        TelegramGetMeResponse(ok: true, result: TelegramUser(id: 1, isBot: true, firstName: "Atlas", username: "atlas_test_bot"))
    }

    func getUpdates(offset: Int?, timeout: Int) async throws -> TelegramGetUpdatesResponse {
        TelegramGetUpdatesResponse(ok: true, result: [])
    }

    func getFile(fileID: String) async throws -> TelegramGetFileResponse {
        fileResponses[fileID] ?? TelegramGetFileResponse(
            ok: true,
            result: TelegramFile(fileID: fileID, fileUniqueID: fileID, fileSize: nil, filePath: nil)
        )
    }

    func downloadFile(atPath path: String) async throws -> Data {
        downloadedFiles[path] ?? Data()
    }

    func sendMessage(chatID: Int64, text: String, replyToMessageID: Int?, replyMarkup: InlineKeyboardMarkup?, parseMode: String?) async throws -> TelegramSendMessageResponse {
        let request = TelegramSendMessageRequest(chatID: chatID, text: text, replyToMessageID: replyToMessageID, replyMarkup: replyMarkup, parseMode: parseMode)
        sentMessages.append(request)
        return TelegramSendMessageResponse(
            ok: true,
            result: TelegramMessage(
                messageID: replyToMessageID ?? 1,
                from: TelegramUser(id: 1, isBot: true, firstName: "Atlas", username: "atlas_test_bot"),
                chat: TelegramChat(id: chatID, type: "private", title: nil, username: nil, firstName: nil, lastName: nil),
                date: nil,
                text: text,
                entities: nil
            )
        )
    }

    func setMessageReaction(chatID: Int64, messageID: Int, emoji: String) async throws {
        sentReactions.append((chatID: chatID, messageID: messageID, emoji: emoji))
    }

    func answerCallbackQuery(callbackQueryID: String, text: String?) async throws -> TelegramBooleanResponse {
        TelegramBooleanResponse(ok: true, result: true)
    }

    func sendPhoto(chatID: Int64, photoURL: URL, caption: String?, replyToMessageID: Int?) async throws -> TelegramSendPhotoResponse {
        sentPhotos.append((chatID: chatID, photoURL: photoURL, caption: caption, replyToMessageID: replyToMessageID))
        return TelegramSendPhotoResponse(
            ok: true,
            result: TelegramMessage(
                messageID: replyToMessageID ?? 1,
                from: TelegramUser(id: 1, isBot: true, firstName: "Atlas", username: "atlas_test_bot"),
                chat: TelegramChat(id: chatID, type: "private", title: nil, username: nil, firstName: nil, lastName: nil),
                date: nil,
                text: caption,
                entities: nil
            )
        )
    }

    func sendDocument(chatID: Int64, documentURL: URL, caption: String?, replyToMessageID: Int?) async throws -> TelegramSendDocumentResponse {
        sentDocuments.append((chatID: chatID, documentURL: documentURL, caption: caption, replyToMessageID: replyToMessageID))
        return TelegramSendDocumentResponse(
            ok: true,
            result: TelegramMessage(
                messageID: replyToMessageID ?? 1,
                from: TelegramUser(id: 1, isBot: true, firstName: "Atlas", username: "atlas_test_bot"),
                chat: TelegramChat(id: chatID, type: "private", title: nil, username: nil, firstName: nil, lastName: nil),
                date: nil,
                text: caption,
                entities: nil
            )
        )
    }

    func sendChatAction(chatID: Int64, action: String) async throws -> TelegramBooleanResponse {
        TelegramBooleanResponse(ok: true, result: true)
    }

    func setMyCommands(commands: [TelegramBotCommand]) async throws -> TelegramBooleanResponse {
        TelegramBooleanResponse(ok: true, result: true)
    }

    func deleteWebhook() async throws -> TelegramBooleanResponse {
        TelegramBooleanResponse(ok: true, result: true)
    }

    func receiveWebhookUpdate(_ payload: Data) async throws -> TelegramUpdate {
        try AtlasJSON.decoder.decode(TelegramUpdate.self, from: payload)
    }

    func sentMessageRequests() -> [TelegramSendMessageRequest] {
        sentMessages
    }

    func sentPhotoRequests() -> [(chatID: Int64, photoURL: URL, caption: String?, replyToMessageID: Int?)] {
        sentPhotos
    }

    func sentDocumentRequests() -> [(chatID: Int64, documentURL: URL, caption: String?, replyToMessageID: Int?)] {
        sentDocuments
    }

    func sentReactionRequests() -> [(chatID: Int64, messageID: Int, emoji: String)] {
        sentReactions
    }

    func setFileResponse(_ response: TelegramGetFileResponse, for fileID: String) {
        fileResponses[fileID] = response
    }

    func setDownloadedData(_ data: Data, forPath path: String) {
        downloadedFiles[path] = data
    }
}
