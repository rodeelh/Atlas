import Foundation
import AtlasShared

// MARK: - APIResponseInspector

/// Deterministic (no LLM) response inspector for API validation.
///
/// Given a raw HTTP response, produces a scored `InspectionResult` that includes:
/// - Whether the response indicates success
/// - A confidence score (0.0–1.0)
/// - Extracted top-level field names from JSON responses
/// - A safe response preview (secrets stripped)
/// - A recommendation: `.usable`, `.needsRevision`, or `.reject`
///
/// Rules are applied in this order:
/// 1. 401/403 → reject (auth failure)
/// 2. Other 4xx → needsRevision
/// 3. 5xx → reject
/// 4. 2xx + empty body → needsRevision
/// 5. 2xx + body → field extraction + scoring
///
/// Non-actor, pure value type — easy to test.
public struct APIResponseInspector {

    // MARK: - Inspection Result

    public struct InspectionResult: Sendable {
        public let success: Bool
        public let confidence: Double
        public let extractedFields: [String]
        public let responsePreview: String?     // trimmed to 500 chars max
        public let failureCategory: APIValidationFailureCategory?
        public let failureReason: String?
        public let recommendation: APIValidationRecommendation

        /// Whether a different example input might produce a better result.
        /// The candidate loop uses this to decide whether to attempt a second call.
        public var isRevisable: Bool {
            recommendation == .needsRevision
        }
    }

    // MARK: - Inspect

    public static func inspect(
        data: Data,
        response: HTTPURLResponse,
        expectedFields: [String]
    ) -> InspectionResult {
        let status = response.statusCode

        // ── Rule 1: 401/403 — auth failure ───────────────────────────────────────
        if status == 401 || status == 403 {
            return InspectionResult(
                success: false,
                confidence: 0.0,
                extractedFields: [],
                responsePreview: nil,
                failureCategory: .httpError,
                failureReason: "Authentication failed (HTTP \(status))",
                recommendation: .reject
            )
        }

        // ── Rule 2: Other 4xx ─────────────────────────────────────────────────────
        if (402..<500).contains(status) {
            return InspectionResult(
                success: false,
                confidence: 0.1,
                extractedFields: [],
                responsePreview: safePreview(data: data, maxLength: 500),
                failureCategory: .httpError,
                failureReason: "Client error HTTP \(status)",
                recommendation: .needsRevision
            )
        }

        // ── Rule 3: 5xx ───────────────────────────────────────────────────────────
        if (500..<600).contains(status) {
            return InspectionResult(
                success: false,
                confidence: 0.0,
                extractedFields: [],
                responsePreview: nil,
                failureCategory: .httpError,
                failureReason: "Server error HTTP \(status)",
                recommendation: .reject
            )
        }

        // ── Rule 4: 2xx + empty body ─────────────────────────────────────────────
        if (200..<300).contains(status) && data.isEmpty {
            return InspectionResult(
                success: false,
                confidence: 0.1,
                extractedFields: [],
                responsePreview: nil,
                failureCategory: .emptyResponse,
                failureReason: "Response body is empty",
                recommendation: .needsRevision
            )
        }

        // ── Rule 4b: 2xx + structurally empty JSON body ──────────────────────────
        // An empty JSON array [] or empty JSON object {} carries no information.
        // Do not let these reach the scoring path with a misleadingly high base confidence.
        if (200..<300).contains(status) {
            if let parsed = try? JSONSerialization.jsonObject(with: data) {
                if let arr = parsed as? [Any], arr.isEmpty {
                    return InspectionResult(
                        success: false,
                        confidence: 0.1,
                        extractedFields: [],
                        responsePreview: "[]",
                        failureCategory: .emptyResponse,
                        failureReason: "Response is an empty JSON array — the API returned no records for the example input.",
                        recommendation: .needsRevision
                    )
                }
                if let obj = parsed as? [String: Any], obj.isEmpty {
                    return InspectionResult(
                        success: false,
                        confidence: 0.1,
                        extractedFields: [],
                        responsePreview: "{}",
                        failureCategory: .emptyResponse,
                        failureReason: "Response is an empty JSON object — the API returned no fields for the example input.",
                        recommendation: .needsRevision
                    )
                }
            }
        }

        // ── Rule 5: 2xx + body → field extraction + scoring ──────────────────────
        return inspectBody(data: data, expectedFields: expectedFields)
    }

    // MARK: - Body Inspection

    private static func inspectBody(data: Data, expectedFields: [String]) -> InspectionResult {
        let preview = safePreview(data: data, maxLength: 500)

        // Try JSON object
        if let jsonObject = try? JSONSerialization.jsonObject(with: data),
           let dict = jsonObject as? [String: Any] {
            let fields = Array(dict.keys)

            // Detect error-body false positives: a JSON object with ≤ 3 fields
            // that contains common error-indicator key names (e.g. {"error": "...", "code": 400}).
            // These appear when an API returns HTTP 200 but treats bad example inputs as app-level errors.
            let errorIndicators: Set<String> = [
                "error", "errors", "fault", "message", "detail", "details", "code", "status"
            ]
            let indicatorHits = fields.filter { errorIndicators.contains($0.lowercased()) }.count
            if indicatorHits > 0 && fields.count <= 3 {
                return InspectionResult(
                    success: false,
                    confidence: 0.2,
                    extractedFields: fields,
                    responsePreview: preview,
                    failureCategory: .unusableResponse,
                    failureReason: "Response looks like an error body — fields: [\(fields.sorted().joined(separator: ", "))]. " +
                                   "The API may have rejected the example input. " +
                                   "Check required parameters and try a different example.",
                    recommendation: .needsRevision
                )
            }

            return scoreResult(
                extractedFields: fields,
                expectedFields: expectedFields,
                baseConfidence: 0.6,
                preview: preview
            )
        }

        // Try JSON array — inspect element 0
        if let jsonObject = try? JSONSerialization.jsonObject(with: data),
           let array = jsonObject as? [[String: Any]],
           let first = array.first {
            let fields = Array(first.keys)
            return scoreResult(
                extractedFields: fields,
                expectedFields: expectedFields,
                baseConfidence: 0.4,
                preview: preview
            )
        }

        // Plain text / non-JSON
        return scoreResult(
            extractedFields: [],
            expectedFields: expectedFields,
            baseConfidence: 0.3,
            preview: preview
        )
    }

    // MARK: - Scoring

    private static func scoreResult(
        extractedFields: [String],
        expectedFields: [String],
        baseConfidence: Double,
        preview: String?
    ) -> InspectionResult {
        var confidence = baseConfidence
        let recommendation: APIValidationRecommendation
        let failureCategory: APIValidationFailureCategory?
        let failureReason: String?

        if !expectedFields.isEmpty {
            // Check how many expected fields were found
            let foundExpected = expectedFields.filter { expected in
                extractedFields.contains(where: { $0.lowercased() == expected.lowercased() })
            }
            let matchRatio = Double(foundExpected.count) / Double(expectedFields.count)
            confidence += matchRatio * 0.4

            if matchRatio < 0.5 {
                recommendation = .needsRevision
                failureCategory = .missingExpectedFields
                failureReason = "Only \(foundExpected.count) of \(expectedFields.count) expected fields found in response"
            } else {
                recommendation = .usable
                failureCategory = nil
                failureReason = nil
            }
        } else {
            // No expected fields — just check we got something meaningful
            if !extractedFields.isEmpty {
                confidence += 0.3
                recommendation = .usable
                failureCategory = nil
                failureReason = nil
            } else {
                recommendation = .needsRevision
                failureCategory = .unusableResponse
                failureReason = "Response contained no parseable field names"
            }
        }

        return InspectionResult(
            success: recommendation == .usable,
            confidence: min(confidence, 1.0),
            extractedFields: extractedFields,
            responsePreview: preview,
            failureCategory: failureCategory,
            failureReason: failureReason,
            recommendation: recommendation
        )
    }

    // MARK: - Safe Preview

    /// Produces a trimmed preview of the response body with potential secrets stripped.
    ///
    /// Heuristic: strips any line containing keywords like "key", "token", "secret",
    /// "password", "auth", "bearer" alongside a long value (>= 16 chars on the same line).
    static func safePreview(data: Data, maxLength: Int) -> String? {
        guard let raw = String(data: data, encoding: .utf8) else { return nil }
        let lines = raw.components(separatedBy: .newlines)
        let secretKeywords = ["key", "token", "secret", "password", "auth", "bearer", "credential"]

        let filtered = lines.filter { line in
            let lower = line.lowercased()
            let hasSecretKeyword = secretKeywords.contains(where: { lower.contains($0) })
            // Strip if line contains a secret keyword AND has what looks like a long value
            let hasLongValue = line.count >= 32
            return !(hasSecretKeyword && hasLongValue)
        }

        let joined = filtered.joined(separator: "\n")
        if joined.isEmpty { return nil }
        let trimmed = String(joined.prefix(maxLength))
        return trimmed.isEmpty ? nil : trimmed
    }
}
