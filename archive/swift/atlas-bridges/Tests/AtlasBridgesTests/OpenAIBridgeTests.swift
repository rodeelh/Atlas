import XCTest
import AtlasBridges
import AtlasNetwork
import AtlasShared

final class OpenAIBridgeTests: XCTestCase {
    func testAtlasResponseUsesLatestAssistantMessageOnly() {
        let payload = """
        {
          "id": "resp_test",
          "output": [
            {
              "id": "msg_1",
              "type": "message",
              "role": "assistant",
              "content": [
                {
                  "type": "output_text",
                  "text": "First answer"
                }
              ],
              "status": "completed"
            },
            {
              "id": "msg_2",
              "type": "message",
              "role": "assistant",
              "content": [
                {
                  "type": "output_text",
                  "text": "Final answer"
                }
              ],
              "status": "completed"
            }
          ]
        }
        """

        let response = try! JSONDecoder().decode(
            OpenAIResponsesCreateResponse.self,
            from: Data(payload.utf8)
        )

        let bridge = OpenAIBridge()
        let atlasResponse = bridge.atlasResponse(from: response)

        XCTAssertEqual(atlasResponse.assistantMessage, "Final answer")
    }
}
