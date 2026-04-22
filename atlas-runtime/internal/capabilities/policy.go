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
- Preserve the user's underlying goal across short follow-up turns.
- If the first route fails, try another viable route before declaring the task blocked.
- Do not suggest Forge unless a real capability gap appears during execution.
- Complete the task with available tools when possible.`)
	case DecisionComposeExisting:
		policy.NextAction = "compose existing skills and control surfaces"
		policy.PromptBlock = strings.TrimSpace(`
This request is best handled by composing existing Atlas capabilities.
- Use the right control surface: agent.create for agent/team-member requests, automation.create for recurring scheduled tasks, workflow for multi-step pipelines.
- Preserve the user's underlying goal across short follow-up turns and route changes.
- If one route fails, try another viable composition before treating the task as blocked.
- Reuse existing file, workflow, automation, team, and communication tools before considering Forge.
- Use exact outcome language in the user-facing answer: workflow means workflow, automation means automation, and agent/team member means an AGENTS.md team definition.
- Keep the plan explicit and multi-step instead of improvising one giant action.`)
	case DecisionForgeNew:
		policy.NextAction = "forge a missing reusable capability"
		missing := "a missing capability"
		if len(analysis.MissingCapabilities) > 0 {
			missing = strings.Join(analysis.MissingCapabilities, ", ")
		}
		policy.PromptBlock = strings.TrimSpace(fmt.Sprintf(`
This request has a genuine capability gap: %s.
- Treat this as engineering work to close the gap, not just something to explain.
- Do not pretend the missing capability already exists.
- Preserve the original user goal while you work around the gap or retry routes.
- First inspect the real environment with atlas.session_capabilities, atlas.diagnose_blocker, system.app_capabilities, terminal.check_command, and fs.workspace_roots when relevant.
- Expand the tool surface or inspect available relays first when a built-in path may already exist.
- In unleashed mode, research the gap online when needed and use what you learn to design the working path.
- Prefer forge.orchestration.propose_and_install when the gap is reusable and Atlas needs the new capability to become usable in the same run.
- After installing or composing the capability, retry the original user task instead of stopping at the install result.
- If the capability cannot be completed in this turn, produce the most concrete installable outcome you can: a live Forge install, Forge proposal, relay spec, operator change, exact next action, or partially working path.
- If a partial result is still possible with current tools, do that work and state the remaining tradeoff clearly.`, missing))
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
- Preserve the user's actual goal while describing the blocker.
- If a safe partial path exists, offer it briefly alongside the blocker.`, missing))
	default:
		policy.NextAction = "use existing tools directly"
	}

	return policy
}
