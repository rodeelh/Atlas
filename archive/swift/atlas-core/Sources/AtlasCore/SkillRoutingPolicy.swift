import Foundation
import AtlasLogging
import AtlasSkills
import AtlasShared

public protocol SkillRoutingPolicying: Sendable {
    func decision(for context: SkillRoutingContext) -> SkillRoutingDecision
    func rank(_ catalog: [SkillActionCatalogItem], with decision: SkillRoutingDecision) -> [SkillActionCatalogItem]
    /// Returns true when the message appears to contain multiple independent goals
    /// that would benefit from parallel multi-agent decomposition.
    func isCompoundRequest(message: String) -> Bool
}

public struct SkillRoutingPolicy: SkillRoutingPolicying, Sendable {
    private let classifier: any SkillRoutingClassifying
    private let logger: AtlasLogger

    public init(
        classifier: any SkillRoutingClassifying = SkillRoutingClassifier(),
        logger: AtlasLogger = AtlasLogger(category: "routing")
    ) {
        self.classifier = classifier
        self.logger = logger
    }

    // MARK: - Decision

    public func decision(for context: SkillRoutingContext) -> SkillRoutingDecision {
        let availableSkillIDs = Set(context.enabledSkills.map(\.id))
        let classification = classifier.classify(message: context.userMessage, skills: context.enabledSkills)

        let decision: SkillRoutingDecision

        if let skillID = classification.matchedSkillID, availableSkillIDs.contains(skillID) {
            let matchedSkill = context.enabledSkills.first { $0.id == skillID }
            let hint = matchedSkill.map {
                SkillRoutingHint(
                    text: "Prefer \($0.manifest.name) for this request — \($0.manifest.description)",
                    targetSkillID: $0.id
                )
            }
            decision = routedDecision(
                intent: classification.intent,
                queryType: classification.queryType,
                preferred: [skillID],
                deprioritized: [],
                availableSkillIDs: availableSkillIDs,
                confidence: classification.confidence,
                explanation: classification.explanation,
                hints: hint.map { [$0] } ?? []
            )
        } else {
            decision = SkillRoutingDecision(
                intent: classification.intent,
                queryType: classification.queryType,
                confidence: classification.confidence,
                explanation: classification.explanation
            )
        }

        logger.info("Computed skill routing decision", metadata: [
            "intent": decision.intent.rawValue,
            "query_type": decision.queryType?.rawValue ?? "none",
            "preferred_skills": decision.preferredSkills.joined(separator: ","),
            "confidence": String(format: "%.2f", decision.confidence),
            "explanation": decision.explanation
        ])

        return decision
    }

    // MARK: - Compound request detection

    public func isCompoundRequest(message: String) -> Bool {
        classifier.isCompoundRequest(message: message)
    }

    // MARK: - Ranking

    public func rank(_ catalog: [SkillActionCatalogItem], with decision: SkillRoutingDecision) -> [SkillActionCatalogItem] {
        catalog
            .filter { decision.suppressedSkills.contains($0.skillID) == false }
            .sorted { lhs, rhs in
                let lhsScore = score(lhs, decision: decision)
                let rhsScore = score(rhs, decision: decision)
                if lhsScore != rhsScore { return lhsScore > rhsScore }
                if lhs.routingPriority != rhs.routingPriority { return lhs.routingPriority > rhs.routingPriority }
                if lhs.skillName != rhs.skillName {
                    return lhs.skillName.localizedCaseInsensitiveCompare(rhs.skillName) == .orderedAscending
                }
                return lhs.action.name.localizedCaseInsensitiveCompare(rhs.action.name) == .orderedAscending
            }
    }

    // MARK: - Private helpers

    private func routedDecision(
        intent: SkillIntent,
        queryType: SkillQueryType?,
        preferred: [String],
        deprioritized: [String],
        availableSkillIDs: Set<String>,
        confidence: Double,
        explanation: String,
        hints: [SkillRoutingHint]
    ) -> SkillRoutingDecision {
        SkillRoutingDecision(
            intent: intent,
            queryType: queryType,
            preferredSkills: preferred.filter { availableSkillIDs.contains($0) },
            deprioritizedSkills: deprioritized.filter { availableSkillIDs.contains($0) },
            routingHints: hints.filter { hint in
                guard let targetSkillID = hint.targetSkillID else { return true }
                return availableSkillIDs.contains(targetSkillID)
            },
            confidence: confidence,
            explanation: explanation
        )
    }

    private func score(_ item: SkillActionCatalogItem, decision: SkillRoutingDecision) -> Int {
        var score = item.routingPriority

        if decision.preferredSkills.contains(item.skillID) {
            score += 1_000
        }
        if decision.deprioritizedSkills.contains(item.skillID) {
            score -= 500
        }
        if let queryType = decision.queryType, item.preferredQueryTypes.contains(queryType) {
            score += 250
        }

        switch item.trustProfile {
        case .exactStructured, .localExact:
            if decision.intent == .liveStructuredData || decision.intent == .localFileTask {
                score += 80
            }
        case .operational:
            if decision.intent == .atlasSystemTask || decision.intent == .appAutomation {
                score += 80
            }
        case .exploratory:
            if decision.intent == .exploratoryResearch {
                score += 80
            }
        case .general:
            break
        }

        return score
    }
}
