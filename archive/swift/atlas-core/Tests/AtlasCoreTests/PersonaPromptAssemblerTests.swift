import XCTest
import AtlasCore
import AtlasSkills
import AtlasShared

final class PersonaPromptAssemblerTests: XCTestCase {
    func testPromptAssemblyIncludesMindContentAndRouting() {
        let assembler = PersonaPromptAssembler()
        let mindContent = """
        # Mind of Atlas

        ## Who I Am

        I am Atlas.

        ## Today's Read

        Fresh session.
        """

        let prompt = assembler.assemblePrompt(
            mindContent: mindContent,
            sessionContext: PersonaSessionContext(
                conversationID: UUID(),
                latestUserInput: "Refine Atlas memory retrieval.",
                messageCount: 7
            ),
            routingDecision: SkillRoutingDecision(
                intent: .localFileTask,
                queryType: .localFileRead,
                preferredSkills: ["file-system"],
                deprioritizedSkills: ["web-research"],
                routingHints: [
                    SkillRoutingHint(
                        text: "For local file operations, prefer File Explorer over web research.",
                        targetSkillID: "file-system"
                    )
                ],
                confidence: 0.9,
                explanation: "The request targets local files."
            ),
            enabledSkills: [],
            skillsBlock: nil
        )

        // MIND.md content should be present
        XCTAssertTrue(prompt.contains("Mind of Atlas"))
        XCTAssertTrue(prompt.contains("Who I Am"))
        // Routing block should be present
        XCTAssertTrue(prompt.contains("Routing guidance:"))
        XCTAssertTrue(prompt.contains("prefer File Explorer over web research"))
        // Session block
        XCTAssertTrue(prompt.contains("Current session context:"))
        // Old mechanical memory block should NOT be present
        XCTAssertFalse(prompt.contains("Relevant memory:"))
        XCTAssertFalse(prompt.contains("Persona:"))
    }

    func testPromptWithSkillsBlock() {
        let assembler = PersonaPromptAssembler()
        let skillsBlock = "Learned routine for this request:\n### Morning Brief\nStep 1: get weather"

        let prompt = assembler.assemblePrompt(
            mindContent: "# Mind of Atlas\n\n## Who I Am\nI am Atlas.",
            sessionContext: PersonaSessionContext(
                conversationID: UUID(),
                latestUserInput: "good morning",
                messageCount: 1
            ),
            routingDecision: nil,
            enabledSkills: [],
            skillsBlock: skillsBlock
        )

        XCTAssertTrue(prompt.contains("Morning Brief"))
        XCTAssertTrue(prompt.contains("get weather"))
    }
}
