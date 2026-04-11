package capabilities

import (
	"fmt"
	"strings"
)

type Policy struct {
	Decision    Decision `json:"decision"`
	NextAction  string   `json:"nextAction"`
	PromptBlock string   `json:"promptBlock"`
}

func BuildPolicy(analysis Analysis) Policy {
	policy := Policy{
		Decision: analysis.Decision,
	}

	switch analysis.Decision {
	case DecisionRunExisting:
		policy.NextAction = "use existing tools directly"
		policy.PromptBlock = strings.TrimSpace(`
Use Atlas's existing skills directly for this request.
- Prefer the smallest working tool path.
- Do not suggest Forge unless a real capability gap appears during execution.
- Complete the task with available tools when possible.`)
	case DecisionComposeExisting:
		policy.NextAction = "compose existing skills and control surfaces"
		policy.PromptBlock = strings.TrimSpace(`
This request is best handled by composing existing Atlas capabilities.
- Prefer workflow, automation, and communication control surfaces over inventing a new skill.
- Reuse existing file, workflow, automation, and communication tools before considering Forge.
- Keep the plan explicit and multi-step instead of improvising one giant action.`)
	case DecisionForgeNew:
		policy.NextAction = "forge a missing reusable capability"
		missing := "a missing capability"
		if len(analysis.MissingCapabilities) > 0 {
			missing = strings.Join(analysis.MissingCapabilities, ", ")
		}
		policy.PromptBlock = strings.TrimSpace(fmt.Sprintf(`
This request has a genuine capability gap: %s.
- Do not pretend the missing capability already exists.
- Prefer Forge when the gap is reusable and not solvable by composing current tools.
- If a partial result is still possible with current tools, state the tradeoff clearly.`, missing))
	case DecisionAskPrerequisite:
		policy.NextAction = "ask for the exact missing prerequisite"
		missing := "a required prerequisite"
		if len(analysis.MissingPrerequisites) > 0 {
			missing = strings.Join(analysis.MissingPrerequisites, ", ")
		}
		policy.PromptBlock = strings.TrimSpace(fmt.Sprintf(`
This request is blocked on prerequisites: %s.
- Ask for the exact blocker instead of forging or guessing.
- Do not claim success until the prerequisite is provided.
- If a safe partial path exists, offer it briefly alongside the blocker.`, missing))
	default:
		policy.NextAction = "use existing tools directly"
	}

	return policy
}
