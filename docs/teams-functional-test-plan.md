# Atlas Teams V1 — Functional Test Plan

**Type:** Production-style manual / semi-manual evaluation  
**Date:** 2026-04-12  
**Scope:** Atlas Teams V1 as shipped — DB-first, structured delegation, five specialist templates  
**Not in scope:** Code audit, unit test coverage, parallel delegation (not yet implemented)

---

## 1. Test Philosophy

### What "working as intended" means for Atlas Teams

Atlas Teams works when a user can assign real work to specialists, trust that Atlas is coordinating — not disappearing — and see accurate feedback in Team HQ without confusion about what is actually happening.

The system must satisfy four principles simultaneously. Failure on any one of them is a failure of the feature, not just a metric.

---

**Principle 1: Atlas remains primary**

Atlas is always the user's agent. Delegated work is Atlas extending itself, not Atlas stepping aside. A user should never feel like they are talking to a subagent. Atlas frames, delegates, and re-presents. The word "I" in a final response should mean Atlas.

Red line: if the user cannot tell that Atlas is in control, the system has failed.

---

**Principle 2: Agent owns delegation decisions**

Atlas decides whether to delegate, to whom, in what mode, and with what scope. Nothing else initiates delegation. The user can trigger Atlas into delegation by asking for it, but Atlas must reason about the delegation and construct the `DelegationPlan` — the user does not wire the plan manually.

Red line: if delegation happens without Atlas reasoning about it, or if Teams rewrites the plan, the boundary is broken.

---

**Principle 3: Teams owns delegated execution**

Once Atlas hands off, Teams persists, executes, tracks, and returns results. Atlas does not manage worker loop details. The user should see a structured, accurate task record in Team HQ that reflects what actually ran.

Red line: if Team HQ shows status that does not match what ran, or if orphaned tasks exist after completion, the system cannot be trusted.

---

**Principle 4: Specialists extend Atlas, not replace it**

Each specialist should feel like a distinct instrument. Scout finds things. Builder drafts things. Reviewer checks things. Operator executes things. Monitor watches things. If every specialist produces the same kind of generic text that Atlas would produce without delegation, there is no product value.

Red line: if specialist output is indistinguishable from generic Atlas output, the system is decorative, not functional.

---

## 2. Test Environment / Setup

### 2.1 Team Members to Create

Create exactly five team members before testing — one per template role. Use realistic names and missions so outputs can be evaluated qualitatively, not just structurally.

| Name | Template Role | Mission | Allowed Skills | Autonomy |
|---|---|---|---|---|
| Scout | scout | Research, gather facts, find references, compare options. Prefer evidence over speculation. | websearch, web, fs | assistive |
| Builder | builder | Draft first-pass implementations, documents, and plans. Move forward, don't critique. | fs, terminal | on_demand |
| Reviewer | reviewer | Inspect outputs for correctness, risk, gaps, and edge cases. Stay within review scope. | fs, websearch | on_demand |
| Operator | operator | Execute bounded system and file operations reliably. Stop cleanly when blocked. | terminal, fs, applescript | on_demand |
| Monitor | monitor | Watch for anomalies, failures, stale state, and surfaceable conditions. Escalate clearly. | fs, terminal, websearch | bounded_autonomous |

### 2.2 Skills Required

- `websearch` — enabled and working (API key configured)
- `web` — enabled and working
- `fs` — enabled, with at least one approved root (e.g., `~/Desktop`)
- `terminal` — enabled
- `applescript` — enabled
- `team.delegate`, `team.list`, `team.get` — always present when team members exist

### 2.3 Sample Data to Prepare

- A real code file of moderate complexity (e.g., a Go handler or TypeScript component from the project)
- A test directory with 10–20 sample files to list/search
- A realistic task list in a plain text file (simulating a backlog)
- At least one approval policy set to "require approval" for a `terminal.*` action
- An intentionally broken or incomplete file for Reviewer to inspect

### 2.4 Approvals Setup

- Set `terminal.*` actions to **require approval** before testing the blocking scenarios
- Set `fs.write_file` to auto-approve for Builder scenarios
- Confirm the Approvals UI surface is visible and shows pending items

### 2.5 UI Surfaces to Have Open

- Chat window (primary interaction surface)
- Team HQ (open in a second tab, do not refresh manually — verify it updates live or near-live)
- Approvals panel
- Atlas daemon logs (`make daemon-logs`) in a terminal — useful for diagnosing silent failures

---

## 3. Core Real-World Scenarios

### Scenario Matrix

Each scenario is labeled with a test ID, the user prompt to issue verbatim, and the full set of expected behaviors to check.

---

#### S-01 · Solo Atlas (baseline)

**Prompt:**  
> "What time zone should I use for a meeting between someone in Riyadh and someone in New York?"

**Expected Atlas behavior:**  
Atlas answers directly without delegating. No team skills called.

**Expected delegation mode/pattern:**  
None — solo.

**Expected Team HQ state:**  
Atlas station shows current mode. No new task rows. Activity rail unchanged.

**Expected user outcome:**  
A clear, correct answer. No mention of delegation or team members.

**What to check:**  
Atlas does not artificially delegate a trivial lookup to Scout. Delegation should be earned, not reflexive.

---

#### S-02 · Atlas + Scout (sync_assist)

**Prompt:**  
> "Can you research the current state of AI coding assistants — specifically what Cursor, Copilot, and Windsurf do differently? I want a structured comparison."

**Expected Atlas behavior:**  
Atlas reasons that this is a research-heavy task, delegates to Scout with `sync_assist`, waits for the result, then synthesizes and presents it.

**Expected delegation:**  
`single`, `sync_assist`, target: Scout.

**Expected Team HQ state:**  
Scout station shows `working` during execution, then `done`. Task row shows `completed`. Result snippet visible. Activity rail: "Scout completed a research task."

**Expected user outcome:**  
A structured comparison synthesized by Atlas. Atlas frames the response — not raw Scout output pasted in. Scout is mentioned if useful but is not the voice of the response.

**Quality check:**  
Scout output type should be `findings_list` or `structured_brief`. If Scout produces vague prose with no citations or structure, that is a Weak Pass or worse.

---

#### S-03 · Atlas + Builder (sync_assist)

**Prompt:**  
> "Draft a Go function that accepts a list of file paths and returns a map of filename to SHA256 hash. Use the standard library only."

**Expected Atlas behavior:**  
Atlas delegates to Builder via `sync_assist`, waits for the artifact, then reviews and presents it with any context needed.

**Expected delegation:**  
`single`, `sync_assist`, target: Builder.

**Expected Team HQ state:**  
Builder station shows `working` → `done`. Task shows `completed`. Result snippet has code or reference to artifact.

**Expected user outcome:**  
Working Go code presented in the chat. Atlas introduces it, explains any notable choices, and may note what to review next — it does not just drop the raw builder output.

**Quality check:**  
Builder should produce `artifact_update` or `summary` output type. If Builder produces a planning document instead of code, that is Needs Work.

---

#### S-04 · Atlas + Reviewer (sync_assist)

**Prompt:**  
> "Review this Go handler for any issues — [paste the sample code file content]"

**Expected Atlas behavior:**  
Atlas delegates to Reviewer via `sync_assist`. Reviewer inspects and returns findings. Atlas synthesizes and prioritizes.

**Expected delegation:**  
`single`, `sync_assist`, target: Reviewer.

**Expected Team HQ state:**  
Reviewer station `working` → `done`. Task `completed`. Findings visible in result snippet.

**Expected user outcome:**  
A prioritized list of issues, not a rewrite. Atlas frames the findings ("Reviewer identified three issues worth addressing...").

**Quality check:**  
Reviewer should produce `findings_list`. If Reviewer rewrites the code instead of reviewing it, that is a role boundary failure — Fail.

---

#### S-05 · Atlas + Operator (sync_assist, with approval gate)

**Prompt:**  
> "Create a backup of my Desktop folder's text files into a new folder called ~/Desktop/backup-test"

**Expected Atlas behavior:**  
Atlas recognizes this as a bounded execution task for Operator. Delegates via `sync_assist`. Operator attempts the terminal/fs action, hits the approval gate.

**Expected delegation:**  
`single`, `sync_assist`, target: Operator.

**Expected Team HQ state:**  
Operator station shows `blocked`. Task row shows `blocked`. Blocked strip surfaces the item. Blocking kind: `approval`.

**Expected user outcome:**  
Atlas tells the user what Operator is trying to do and that approval is needed. The explanation is clear and actionable — not an error message.

**What to check:**  
Does the blocked strip appear? Is the task still `blocked` after the chat turn completes? Does Atlas accurately describe what is waiting?

---

#### S-06 · Async Assignment (fire and forget)

**Prompt:**  
> "Have Scout do a deep research pass on the current best practices for SQLite performance tuning in Go applications. Don't wait for it — just let me know when it's done."

**Expected Atlas behavior:**  
Atlas delegates to Scout via `async_assignment`. Task ID is pre-generated. Atlas immediately responds acknowledging the task was assigned and tells the user to check Team HQ.

**Expected delegation:**  
`single`, `async_assignment`, target: Scout.

**Expected Team HQ state:**  
Scout station shows `working`. Task row shows `in_progress`. Activity rail: "Atlas assigned Scout a background research task." After completion: task shows `completed`, result snippet available, Scout status `done`.

**Expected user outcome:**  
Atlas does not make the user wait. The response is immediate and informative: "Scout is on it. I'll update Team HQ when the research is complete." The task is visible and trackable in Team HQ without Atlas being involved again.

**What to check:**  
Does the async task actually run? Does Team HQ update without the user doing anything? Does Atlas NOT re-narrate the result unless the user asks?

---

#### S-07 · Sequence Delegation (Scout → Builder)

**Prompt:**  
> "First, have Scout research the top three Go HTTP router libraries and summarize their trade-offs. Then have Builder draft a comparison table I can paste into a doc."

**Expected Atlas behavior:**  
Atlas constructs a `sequence` delegation plan: step 1 → Scout (research), step 2 → Builder (draft table using Scout's output as prior result). Atlas waits for both steps or assigns async.

**Expected delegation:**  
`sequence`, target: Scout (step 1) → Builder (step 2). Prior result from Scout injected into Builder's `input_context.prior_results`.

**Expected Team HQ state:**  
Activity rail: "Scout completed research." "Builder received Scout's output and is drafting." Task rows for both steps. Both `completed` at the end.

**Expected user outcome:**  
A properly formatted comparison table grounded in Scout's research. Atlas presents the final output and mentions that it was a two-step workflow.

**Quality check:**  
Builder's output should reference the specific libraries Scout found, not invent new ones. If Builder ignores prior_results and drafts from scratch, that is a sequence wiring failure — Fail.

---

#### S-08 · Atlas as Team Lead (multi-specialist, async)

**Prompt:**  
> "I want you to act as team lead on this: research the most popular open-source license for Go CLI tools, draft a brief LICENSE file using the result, and then have someone review it for accuracy. Assign it all in the background."

**Expected Atlas behavior:**  
Atlas constructs a `sequence` with three steps: Scout (research), Builder (draft), Reviewer (review). Assigns async. Returns task IDs or a summary immediately.

**Expected delegation:**  
`sequence`, `async_assignment`, three tasks.

**Expected Team HQ state:**  
Three task rows. Activity rail shows all three phases. Each specialist station shows status changes as the sequence progresses. Final result on Reviewer step: a review of the drafted LICENSE file.

**Expected user outcome:**  
Atlas explains the plan concisely and points the user to Team HQ. Does not dump all intermediate results into chat unless asked.

**What to check:**  
Does Atlas maintain the coordinator role or drift into worker mode? Does the sequence actually chain outputs? Does Team HQ reflect all three steps accurately?

---

#### S-09 · Blocked Approval + Resume

**Prompt:**  
> "Have Operator list all running processes and write the output to ~/Desktop/process-snapshot.txt"

**Setup:**  
Ensure `terminal.*` requires approval.

**Expected Atlas behavior:**  
Atlas delegates to Operator. Operator hits the terminal approval gate. Task enters `blocked` state. Atlas explains what happened.

**Resume flow:**  
User approves in the Approvals panel (or via chat). Operator resumes, completes the task, writes the file.

**Expected Team HQ state:**  
Operator `blocked` → (after approval) `working` → `done`. Task `blocked` → `completed`. Blocked strip clears after approval. Activity rail: "Operator resumed after approval."

**Expected user outcome:**  
Atlas narrates the resume: "Operator completed the task after your approval. The snapshot was written to ~/Desktop/process-snapshot.txt."

**What to check:**  
No orphaned task state remains. The task is `completed`, not stuck in `blocked`. The blocked strip is empty after resolution. Atlas explains the outcome — it does not just say "done."

---

#### S-10 · Monitor (bounded autonomous)

**Prompt:**  
> "Have Monitor check whether there are any recent error lines in the Atlas runtime logs and tell me if anything needs attention."

**Expected Atlas behavior:**  
Atlas delegates to Monitor via `sync_assist`. Monitor reads logs, checks for errors, and returns a structured brief.

**Expected delegation:**  
`single`, `sync_assist`, target: Monitor.

**Expected Team HQ state:**  
Monitor station `working` → `done`. Task `completed`. Result snippet: either "No anomalies detected" or a concise list of issues.

**Expected user outcome:**  
A quiet, factual report. If nothing is wrong, Monitor should say so clearly. If issues are found, Atlas escalates with recommendations — not panic.

**Quality check:**  
Monitor should report only meaningful signals. If Monitor produces a wall of raw log text without analysis, that is Needs Work.

---

## 4. Specialist Quality Evaluation

For each specialist, there is a distinct identity described in the template contract. The evaluation goal is to determine whether that identity actually survives into the worker's output, or whether every specialist feels like the same generic LLM response with a different name attached.

---

### 4.1 Scout

**Good result:**
- Concrete findings with source references or evidence
- Uncertainty flagged explicitly ("I could not find current data on X")
- `findings_list` or `structured_brief` output type
- Does not attempt implementation or code
- Short on speculation, long on evidence

**Weak/incorrect behavior:**
- Speculates without flagging it as speculation
- Produces implementation plans instead of findings
- Returns generic, unstructured paragraphs without clear research framing
- Does not distinguish between verified facts and inferred claims

**How to tell if Scout is just generic Atlas:**
Ask Scout to research something with a known, verifiable answer (e.g., "What HTTP router does the Chi library use internally?"). Generic Atlas would answer from training data without web searching. Scout should web search, cite, and explicitly note any gaps. If Scout's response looks like Atlas answering from memory with no tool calls, it is not behaving as Scout.

---

### 4.2 Builder

**Good result:**
- A working or near-working first-pass artifact
- Reasonable assumptions made and noted briefly
- `artifact_update` or `summary` output type
- Does not critique its own output at length — moves forward
- Does not rewrite scope or add unsolicited features

**Weak/incorrect behavior:**
- Produces a planning document instead of the artifact
- Over-explains rather than building
- Produces code with obvious errors and no note that it is a first pass
- Rewrites the scope (e.g., asked for a function, returns an entire package)

**How to tell if Builder is just generic Atlas:**
Ask Builder to draft a specific Go function with defined inputs and outputs. Generic Atlas would write the code and then also add a full explanation, tests, usage examples, and caveats. Builder should produce just the function with brief inline comments, make a reasonable assumption where needed, and stop. If Builder produces a textbook chapter, it has lost its identity.

---

### 4.3 Reviewer

**Good result:**
- A prioritized list of issues — not a rewrite
- Issues are specific, not generic ("line 42 has an unchecked error" not "error handling could be improved")
- `findings_list` output type
- Stays strictly within review scope
- Notes what is good as well as what is wrong

**Weak/incorrect behavior:**
- Rewrites the code being reviewed
- Issues are so general they apply to any code ("you should add tests")
- Output is a long essay rather than a structured list
- Reviewer expands scope ("while I'm here, you should also change...")

**How to tell if Reviewer is just generic Atlas:**
Paste a Go handler with two specific bugs and one non-issue. A proper Reviewer should identify the two bugs specifically and not flag the non-issue. Generic Atlas would also suggest refactoring, adding error handling everywhere, writing tests, and restructuring the package. If the output reads like a code review PR comment from a senior engineer, Reviewer is working. If it reads like a tutorial, it is not.

---

### 4.4 Operator

**Good result:**
- Task is executed deterministically — commands run, files are created/written
- Blockers are reported cleanly with `blocking_kind` set
- `summary` or `artifact_update` output type
- Does not reason extensively about what to do — it executes
- Stops cleanly if a prerequisite is missing rather than improvising

**Weak/incorrect behavior:**
- Reasons extensively about whether to take an action without taking it
- Takes an action outside defined scope
- Does not report what it actually did (no audit trail)
- Ignores tool failures rather than surfacing them as blockers

**How to tell if Operator is just generic Atlas:**
Ask Operator to create a file with specific content. Generic Atlas would explain what it would do, offer alternatives, suggest you confirm first, and maybe do it. Operator should execute the action, report what was done, and stop. If the response contains more words of explanation than actions taken, the Operator is not functioning as an executor.

---

### 4.5 Monitor

**Good result:**
- Silent when nothing is wrong (does not produce a report when there is nothing to report)
- Concise escalation when something is wrong
- `structured_brief` or `findings_list` output type
- Recommendations are specific and actionable
- Does not analyze more than it was asked to observe

**Weak/incorrect behavior:**
- Always produces a lengthy report even when nothing is wrong
- Flags non-issues as issues
- Cannot recommend a specific action — just describes what it observed
- Produces raw log dumps instead of synthesized observations

**How to tell if Monitor is just generic Atlas:**
Point Monitor at a clean log file with no errors. Generic Atlas might still produce a detailed summary of what it read. Monitor should say: "No anomalies found. Last N lines are clean." If Monitor produces paragraphs describing log lines that are not errors, it has lost its identity.

---

## 5. Atlas Primacy Checks

These checks verify that Atlas remains the user's primary agent throughout delegated work. Run them during or after the scenario matrix.

---

**P-01 · Atlas frames the delegation before it happens**

During any `sync_assist` scenario, check that Atlas says something like: "I'm going to have Scout look into this..." before calling `team.delegate`. Atlas should not silently delegate without informing the user.

Pass: User knows who is working on it and why before it runs.  
Fail: Delegation happens silently with no framing from Atlas.

---

**P-02 · Atlas reintegrates, not pastes**

After `sync_assist` completes, Atlas's response should synthesize the specialist output — not dump it raw into the chat. Atlas should interpret, prioritize, and present.

Pass: Specialist findings are woven into Atlas's voice. "Based on Scout's research, the key difference between Cursor and Copilot is..."  
Fail: Atlas response is just `[Scout's output copied verbatim]`.

---

**P-03 · Subagent does not speak as Atlas**

Check the worker prompt (via step logs in Team HQ or daemon logs) to confirm that the execution contract says workers must not present themselves as Atlas.

Pass: Step logs show the worker identifying itself as "Scout" or "Builder" within its own reasoning, not as "I, Atlas."  
Fail: Worker's internal reasoning uses Atlas's first-person persona.

---

**P-04 · Atlas remains coherent across the full turn**

For `sync_assist`, the user's single conversational turn should feel complete. Atlas delegates mid-turn and returns a final answer as if it was always in control.

Pass: The user experience feels like one response from Atlas, not two outputs from two agents.  
Fail: The user can clearly see the seam — raw worker output followed by "I hope that helps!"

---

**P-05 · Atlas handles cases that should not be delegated**

**Prompt:**  
> "What's 847 divided by 7?"

Atlas should not delegate this to Scout or any specialist. Trivial computation belongs to Atlas directly.

Pass: Atlas answers immediately without any team.delegate call.  
Fail: Atlas routes a simple arithmetic question to a specialist.

---

**P-06 · Atlas falls back cleanly when no specialist is appropriate**

**Prompt:**  
> "I need help writing a wedding speech."

No specialist in the default team is well-suited for this. Atlas should either handle it directly or explain why it would not delegate it.

Pass: Atlas handles it as Solo Atlas. No delegation.  
Fail: Atlas forces the task onto Builder or Scout because they are the closest match, resulting in an inappropriate framing.

---

## 6. Team HQ Functional Checks

These checks verify that the Team HQ UI surface is an accurate projection of actual runtime state — not a stale or inferred view.

---

**H-01 · Member runtime status reflects reality**

During S-02 (Scout, sync_assist), open Team HQ before the task completes.

Check:
- Scout station shows `working` while the task is in progress
- Scout station shows `done` (or `idle`) after completion
- Status does not lag more than a few seconds behind reality

Pass: Status transitions match what is happening in the agent loop.  
Fail: Scout shows `idle` the entire time, or shows `working` after the task is complete.

---

**H-02 · Task row appears immediately**

For `async_assignment` (S-06), check Team HQ immediately after Atlas responds.

Check:
- A task row for Scout is visible with status `in_progress` or `assigned`
- Task title and objective are present and match what was delegated
- The row was not created by polling — it should appear within the response cycle

Pass: Task row visible before the user has to refresh or wait.  
Fail: Task row appears only after the async task completes, or only after manual refresh.

---

**H-03 · Blocked strip surfaces blocked tasks**

During S-05 or S-09 (Operator, approval gate):

Check:
- The blocked strip is visible at the top of Team HQ
- The blocked item names the specialist and the blocking reason
- Clicking the blocked item shows relevant detail (blocking_kind, blocking_detail)

Pass: Blocked strip appears immediately when the task enters `blocked` state.  
Fail: Blocked strip does not appear, or appears with no detail.

---

**H-04 · Blocked strip clears after resolution**

After approving the task in S-09:

Check:
- Blocked strip item disappears
- Task status updates to `completed`
- No stale blocked item remains

Pass: Blocked strip is empty. Task is `completed`.  
Fail: Blocked strip still shows the item after the task finished. Task stuck in `blocked`.

---

**H-05 · Activity rail reflects real events**

During S-07 (sequence, Scout → Builder):

Check:
- Activity rail shows: "Atlas delegated to Scout"
- After Scout completes: "Scout completed a research task"
- After Builder receives output: "Builder is drafting based on Scout's output"
- After Builder completes: "Builder completed an artifact"

Pass: Activity rail events are timestamped and accurate.  
Fail: Activity rail shows no events, or shows events out of order.

---

**H-06 · Result snippet is meaningful**

After S-03 (Builder, sync_assist), check the Builder station.

Check:
- Result snippet shows a brief excerpt or summary of what Builder produced
- Snippet is not truncated to uselessness
- Snippet is not the full output — just enough to identify what was done

Pass: Snippet gives a useful preview without being the full worker output.  
Fail: Snippet is empty, shows raw JSON, or is identical to the full output.

---

**H-07 · Sync vs async task presentation is distinguishable**

Compare S-02 (sync) and S-06 (async) in Team HQ.

Check:
- Sync tasks appear and complete within the same conversation turn's timeframe
- Async tasks show an explicit indicator that they are running in the background
- Mode (`sync_assist` / `async_assignment`) is surfaced in the task inspector

Pass: A user can look at two task rows and immediately tell which was sync and which was async.  
Fail: Both tasks look identical in Team HQ regardless of execution mode.

---

**H-08 · Task inspector shows step log**

Click into any completed task in Team HQ.

Check:
- Step log shows the worker's execution steps (system/user/assistant/tool messages)
- Tool calls are visible with their parameters and results
- Steps are in chronological order

Pass: Step log provides a complete, readable trace of what the worker did.  
Fail: Step log is empty, shows only the final result, or shows garbled output.

---

## 7. Approval / Blocking / Resume Checks

These checks test the complete blocking lifecycle as a user would experience it.

---

### Full Flow Test (run as a single contiguous sequence)

**Setup:**  
- `terminal.*` requires approval
- Operator is enabled with `terminal` in its allowed skills

**Step 1 — Trigger the block**

**Prompt:**  
> "Have Operator run `ps aux | head -20` and summarize the top processes."

Expected: Atlas delegates to Operator. Operator attempts `terminal.*`. Task enters `blocked`. Atlas tells the user: "Operator needs your approval to run a terminal command. The command is: `ps aux | head -20`. You can approve it in the Approvals panel."

Check:
- Atlas's explanation is accurate (names the command)
- Task status is `blocked`
- Approvals panel shows the pending item
- Team HQ blocked strip shows the item
- Operator station shows `blocked`

**Step 2 — Verify blocked state persists correctly**

Wait 30 seconds. Check:
- Task is still `blocked` — not timed out or auto-canceled
- Blocked strip still shows the item
- No duplicate task was created

**Step 3 — Approve**

Approve the terminal action in the Approvals panel.

Expected:
- Operator resumes
- Task moves from `blocked` → `in_progress` → `completed`
- Operator station moves from `blocked` → `working` → `done`
- Blocked strip item disappears

**Step 4 — Atlas narrates the outcome**

Expected: Atlas produces a turn result explaining what Operator found — not just "Operator resumed." The output should include the summarized process list.

Check:
- Atlas's narration is complete and accurate
- The result is synthesized, not raw terminal output
- No mention of "blocked" in the final response (it was resolved)

**Step 5 — No orphaned state**

After completion, check:
- Task row is `completed` — not `blocked`
- Blocked strip is empty
- Operator station is `idle` or `done`
- No duplicate task rows for the same delegation

---

### Rejection Flow Test

Repeat Step 1, but this time **reject** the approval.

Expected:
- Task moves from `blocked` → `failed` or `canceled`
- Atlas explains that the approval was rejected and what that means for the result
- Blocked strip clears
- No orphaned task in `blocked`

---

## 8. Failure / Edge-Case Scenarios

---

**E-01 · Disabled specialist**

Disable Builder in Team HQ. Then:

**Prompt:**  
> "Have Builder draft a readme for this project."

Expected: Atlas recognizes Builder is disabled. Atlas either handles the task itself or explains that Builder is unavailable and offers to proceed without delegation.

Pass: Atlas handles it gracefully without a stack trace or silent failure.  
Fail: Atlas attempts delegation, gets a validation error, and returns nothing useful to the user.

---

**E-02 · Invalid delegation target**

**Prompt:**  
> "Have Architect draft a system design for this." (no Architect exists)

Expected: Atlas recognizes that no team member named "Architect" exists. Atlas either handles it directly or offers to use the closest match.

Pass: Atlas explains the missing specialist and proceeds intelligently.  
Fail: `team.delegate` is called with a nonexistent agent ID, producing an opaque error.

---

**E-03 · Specialist lacks required skill**

Remove `websearch` from Scout's allowed skills. Then:

**Prompt:**  
> "Have Scout search the web for recent AI benchmarks."

Expected: Teams validation rejects the plan (capability validation). Atlas explains that Scout cannot perform web searches with its current configuration.

Pass: Clear, actionable message. Atlas offers alternatives.  
Fail: Scout is delegated the task, attempts to call `websearch`, gets a tool-not-found error, and returns garbage.

---

**E-04 · Async task failure**

Set up a scenario where an async task will fail (e.g., delegate a file operation to Operator for a path that does not exist, without approval configured to auto-fail).

Expected: Task status updates to `failed` in Team HQ. Monitor station (if active) should note the failure. Atlas does not narrate the failure mid-turn since it was async, but Team HQ reflects it.

Pass: Task shows `failed` with a reason. Blocked strip or activity rail surfaces the failure. User can inspect the step log.  
Fail: Task shows `in_progress` forever. No indication of failure anywhere.

---

**E-05 · Sequence step failure**

Run S-07 (Scout → Builder sequence). Manually break Scout (e.g., by revoking websearch mid-task).

Expected: Scout's step fails. The sequence should stop — Builder should not receive Scout's output and attempt to draft from nothing (or worse, hallucinate research).

Pass: Sequence stops at the failed step. Task row for Builder shows `canceled` or is not created. Atlas is informed and explains what happened.  
Fail: Builder receives an empty or corrupt `prior_results` and drafts something nonsensical. The sequence appears to "complete" with bad data.

---

**E-06 · Stale/abandoned task**

Start an async task. Kill the daemon and restart it (`make daemon-restart`).

Expected: The task that was `in_progress` should move to `failed` or remain in a terminal state that is visible in Team HQ. The task should not reset to `created` or `in_progress` as if nothing happened.

Pass: Task shows `failed` with a note about unexpected termination. Team HQ does not show ghost `working` status after restart.  
Fail: Task shows `in_progress` forever after daemon restart. No recovery behavior.

---

**E-07 · Duplicate delegation temptation**

**Prompt (in one message):**  
> "Have Scout and Builder both work on my README at the same time."

Expected: Atlas should not create two simultaneous overlapping tasks on the same artifact. Atlas should either sequence them (Scout researches first, then Builder drafts) or explain why parallel is not appropriate, since parallel is not yet implemented.

Pass: Atlas constructs a sensible sequence or explains the limitation clearly.  
Fail: Atlas attempts `pattern: "parallel"`, gets a validation error, returns nothing useful.

---

**E-08 · Task that should not be delegated**

**Prompt:**  
> "What is the capital of France?"

Expected: Atlas answers directly. No delegation.

**Prompt:**  
> "Am I a good person?"

Expected: Atlas handles this personally and reflectively. No delegation to Reviewer.

Pass: Atlas does not mechanically delegate trivially answerable or personal questions.  
Fail: Atlas delegates to Scout because it "involves research" or to Reviewer because it "involves assessment."

---

## 9. Evaluation Rubric

Each scenario is scored on six dimensions. Score each independently, then derive an overall scenario score.

### Scoring Scale

| Score | Meaning |
|---|---|
| **Pass** | Behavior matches intent. No corrective action needed. |
| **Weak Pass** | Behavior is mostly correct but has a meaningful gap (e.g., right outcome, wrong narration). Flag for iteration. |
| **Needs Work** | Behavior is partially wrong in a way that affects user trust or output quality. Requires a fix before release. |
| **Fail** | Behavior is incorrect, missing, or actively misleading. Blocks release of the scenario. |

### Dimensions

**D1 — Delegation Correctness**  
Did Atlas delegate (or not delegate) appropriately? Was the delegation plan correct — right specialist, right mode, right pattern?

**D2 — Specialist Quality**  
Did the specialist output match its template role? Was the output distinct from generic Atlas output? Was the output type correct?

**D3 — Atlas Primacy**  
Did Atlas frame the delegation? Did Atlas reintegrate the result? Is Atlas clearly the primary agent in the user's experience?

**D4 — Clarity of User-Facing Behavior**  
Is the user-visible behavior clear, informative, and consistent? Would a new user understand what is happening?

**D5 — Team HQ Accuracy**  
Does Team HQ reflect actual runtime state? Are status transitions correct? Are blocked items surfaced and cleared correctly?

**D6 — Recovery Behavior**  
When blocked, failed, or rejected: does the system recover cleanly? Is there orphaned state? Is the explanation correct?

### Scenario Rubric Table

| Scenario | D1 | D2 | D3 | D4 | D5 | D6 | Overall |
|---|---|---|---|---|---|---|---|
| S-01 Solo Atlas | — | — | Pass/Fail | Pass/Fail | — | — | |
| S-02 Scout sync | | | | | | | |
| S-03 Builder sync | | | | | | | |
| S-04 Reviewer sync | | | | | | | |
| S-05 Operator + approval | | | | | | | |
| S-06 Async assignment | | | | | | | |
| S-07 Sequence Scout→Builder | | | | | | | |
| S-08 Team lead (3-step) | | | | | | | |
| S-09 Block + resume | | | | | | | |
| S-10 Monitor | | | | | | | |
| E-01 Disabled specialist | | | | | | | |
| E-02 Invalid target | | | | | | | |
| E-03 Missing skill | | | | | | | |
| E-04 Async failure | | | | | | | |
| E-05 Sequence failure | | | | | | | |

**Overall score per scenario:**
- All Pass → Ship
- Any Fail → Block
- Multiple Weak Pass / Needs Work → Prioritize for iteration

---

## 10. Recommended Execution Order

Run in this order to maximize learning signal and minimize confusion from cascading state.

**Phase 1 — Baseline and environment validation (Day 1, ~1 hour)**

1. S-01 (Solo Atlas) — confirm Atlas works at all and does not over-delegate
2. H-01 through H-03 (Team HQ basics) — confirm UI surface loads and status is readable
3. S-02 (Scout sync) — simplest delegation case; confirm the end-to-end path works

If S-02 fails at the delegation level, stop and diagnose before continuing. Everything else depends on this path.

**Phase 2 — Specialist quality sweep (Day 1 or 2, ~2–3 hours)**

4. S-03 (Builder)
5. S-04 (Reviewer)
6. S-10 (Monitor)

Each of these is a `single, sync_assist` case. Run them in this order because Builder and Reviewer can share the same code sample.

**Phase 3 — Async and sequencing (Day 2, ~2 hours)**

7. S-06 (Async Scout) — first async test; verify Task HQ updates in background
8. S-07 (Sequence Scout→Builder) — first sequence test; verify prior_results pass-through
9. S-08 (3-step team lead) — complex sequence; do this only after S-07 passes

**Phase 4 — Approval and blocking (Day 2, ~1.5 hours)**

10. S-05 (Operator + approval gate) — sync block
11. S-09 (Full block + resume flow) — complete lifecycle
12. Rejection flow (from Section 7)

**Phase 5 — Failure and edge cases (Day 3, ~2 hours)**

13. E-01 (Disabled specialist)
14. E-02 (Invalid target)
15. E-03 (Missing skill)
16. E-08 (Should not delegate)
17. E-04 (Async failure)
18. E-05 (Sequence failure)
19. E-06 (Stale task after restart) — do this last; it requires a daemon restart

**Phase 6 — Atlas primacy sweep (Day 3, ~1 hour)**

20. P-01 through P-06 — run these as overlay checks on already-completed scenarios where possible, but do P-05 and P-06 as standalone prompts

---

## 11. Highest-Signal Findings to Watch For

These are the behaviors that — if seen — indicate a fundamental problem with the system, not a cosmetic defect. Treat any of these as a blocking issue.

---

**Red flag 1 — Atlas over-delegates**

Symptom: Atlas calls `team.delegate` for trivial tasks, personal questions, or anything that does not benefit from specialization.

Why it matters: If Atlas delegates indiscriminately, it stops being the primary agent and becomes a router. This breaks the core product promise. It also creates noise in Team HQ and erodes user trust in Atlas's judgment.

Signal to watch: Any delegation to Scout for questions Atlas could answer directly from memory or simple reasoning.

---

**Red flag 2 — Specialists feel generic**

Symptom: Scout produces unstructured prose. Builder produces planning documents. Reviewer rewrites code. Monitor dumps logs. Every specialist sounds like the same generic AI assistant.

Why it matters: If specialists are not distinct, there is no reason to have them. The product value of Teams is zero if the five roles are cosmetically different names on the same behavior.

Signal to watch: Run the same prompt through two different specialists and compare outputs. If they are structurally similar, specialist identity is not working.

---

**Red flag 3 — Atlas stops narrating**

Symptom: After `sync_assist` completes, Atlas responds with raw worker output or a minimal acknowledgment like "Here is what Scout found:" followed by an unedited dump.

Why it matters: Atlas must synthesize and reintegrate. If Atlas is just a pass-through for specialist output, it has lost its role as the final narrator and the user has no single coherent intelligence to trust.

Signal to watch: Check whether Atlas's final response reads like Atlas wrote it, or like Atlas forwarded a file.

---

**Red flag 4 — Team HQ lies about status**

Symptom: Task shows `in_progress` after it completed. Specialist shows `working` after the task ended. Blocked strip shows old items. Task shows `completed` but no result is available.

Why it matters: Team HQ is the user's primary visibility surface for delegated work. If it is inaccurate, users cannot trust it to tell them what is happening. This is especially critical for async tasks where Team HQ is the only feedback channel.

Signal to watch: Compare Team HQ state with daemon logs after every scenario. If they disagree, HQ is lying.

---

**Red flag 5 — Approval flow breaks the mental model**

Symptom: After approval, the task does not resume. After rejection, the task stays `blocked`. The blocked strip persists after resolution. Atlas's narration after resolution is wrong or missing.

Why it matters: The approval flow is the user's trust boundary for risky actions. If approving an action does not result in visible, correct resumption, users cannot confidently use specialists for operational work.

Signal to watch: After approving in the Approvals panel, check task status, Atlas's narration, and blocked strip — all three must be correct simultaneously.

---

**Red flag 6 — Async tasks become invisible**

Symptom: User issues an `async_assignment`. Atlas responds. Team HQ never updates. User cannot find the task. Atlas does not offer any way to check status.

Why it matters: Async tasks are only viable if Team HQ is a reliable status surface. If async tasks disappear into a black box, they are worse than useless — they create uncertainty.

Signal to watch: After any async delegation, verify that a task row appears in Team HQ within the same response cycle, before the task completes.

---

**Red flag 7 — Sequence ignores prior results**

Symptom: In a Scout → Builder sequence, Builder drafts output that does not reference anything Scout found. Builder invents its own research rather than using Scout's output.

Why it matters: If `prior_results` is not correctly injected into the next step's context, sequence delegation is broken. The entire value of sequential workflows is that each step builds on the last.

Signal to watch: In the Builder step log, check whether Scout's findings appear in the `input_context` section of the worker prompt.

---

**Red flag 8 — Orphaned task state**

Symptom: After a task completes, is approved, or is canceled, the task row still shows `blocked`, `in_progress`, or `working`. A specialist station shows `blocked` with no pending task.

Why it matters: Orphaned state accumulates over time. After a few sessions, Team HQ becomes untrustworthy because it shows stale data. Users stop checking it.

Signal to watch: After every scenario, verify that all task rows are in terminal states and all specialist stations reflect their actual current state.

---

## 12. Final Deliverable Format

### 12.1 Pre-Test Setup Checklist

```
[ ] Five team members created (Scout, Builder, Reviewer, Operator, Monitor)
[ ] All team members enabled
[ ] websearch working and tested
[ ] fs approved root configured (~/Desktop or equivalent)
[ ] terminal skill enabled
[ ] terminal.* approval policy set to "require approval"
[ ] Sample code file prepared
[ ] Sample data directory prepared
[ ] Team HQ open in browser tab
[ ] Approvals panel accessible
[ ] Daemon logs running (make daemon-logs)
[ ] Atlas chat open and functional
```

### 12.2 Scenario Execution Table

| # | Scenario | Prompt (abbreviated) | Mode | Pattern | Specialist(s) | Status |
|---|---|---|---|---|---|---|
| S-01 | Solo Atlas | "What time zone for Riyadh / New York?" | solo | — | — | |
| S-02 | Scout sync | "Research AI coding assistants comparison" | sync_assist | single | Scout | |
| S-03 | Builder sync | "Draft a Go SHA256 hash function" | sync_assist | single | Builder | |
| S-04 | Reviewer sync | "Review this Go handler" | sync_assist | single | Reviewer | |
| S-05 | Operator + block | "Back up Desktop text files" | sync_assist | single | Operator | |
| S-06 | Async assignment | "Scout: deep SQLite research, background" | async_assignment | single | Scout | |
| S-07 | Sequence 2-step | "Scout research → Builder table" | async/sync | sequence | Scout→Builder | |
| S-08 | Team lead 3-step | "Research + draft + review LICENSE file" | async_assignment | sequence | Scout→Builder→Reviewer | |
| S-09 | Block + resume | "Operator: ps aux to file" | sync_assist | single | Operator | |
| S-10 | Monitor | "Check Atlas logs for errors" | sync_assist | single | Monitor | |
| E-01 | Disabled specialist | "Have Builder draft..." (Builder disabled) | — | — | Builder | |
| E-02 | Invalid target | "Have Architect design..." | — | — | — | |
| E-03 | Missing skill | "Have Scout search web..." (websearch removed) | — | — | Scout | |
| E-04 | Async failure | Async task with broken path | async_assignment | single | Operator | |
| E-05 | Sequence failure | Sequence with mid-run revocation | — | sequence | Scout→Builder | |
| E-06 | Stale task | Daemon restart mid-task | — | — | Any | |
| E-07 | Parallel attempt | "Scout and Builder simultaneously" | — | — | — | |
| E-08 | No delegation | "What is the capital of France?" | solo | — | — | |

### 12.3 Scoring Rubric — Per Scenario

| Scenario | D1 Delegation | D2 Specialist | D3 Primacy | D4 Clarity | D5 HQ | D6 Recovery | Overall |
|---|---|---|---|---|---|---|---|
| S-01 | | | | | | | |
| S-02 | | | | | | | |
| S-03 | | | | | | | |
| S-04 | | | | | | | |
| S-05 | | | | | | | |
| S-06 | | | | | | | |
| S-07 | | | | | | | |
| S-08 | | | | | | | |
| S-09 | | | | | | | |
| S-10 | | | | | | | |
| E-01 | | | | | | | |
| E-02 | | | | | | | |
| E-03 | | | | | | | |
| E-04 | | | | | | | |
| E-05 | | | | | | | |
| E-06 | | | | | | | |
| E-07 | | | | | | | |
| E-08 | | | | | | | |

**Score key:** P = Pass · WP = Weak Pass · NW = Needs Work · F = Fail · — = Not applicable

### 12.4 Summary Scorecard

Fill this in after all scenarios are run.

```
Atlas Teams V1 — Functional Evaluation Scorecard
================================================

Run date: ____________
Tester:   ____________
Build:    ____________ (git sha or version)

Core Scenarios Passed (S-01 to S-10):     __ / 10
Edge Case Scenarios Passed (E-01 to E-08): __ / 8
Red Flags Observed:                        __ / 8

--- Dimension Summary ---
D1 Delegation Correctness:   __ / __ Pass
D2 Specialist Quality:       __ / __ Pass
D3 Atlas Primacy:            __ / __ Pass
D4 Clarity:                  __ / __ Pass
D5 Team HQ Accuracy:         __ / __ Pass
D6 Recovery Behavior:        __ / __ Pass

--- Blocking Issues ---
[ ] Any Fail in D1–D6 (list scenarios):
[ ] Any red flag from Section 11 observed (list):

--- Ship Recommendation ---
[ ] Ship — all core scenarios pass, no red flags
[ ] Ship with known issues — document and track
[ ] Do not ship — blocking failures present

Notes:
_______________________________________________________
_______________________________________________________
```

---

*This plan should be revisited after the first real evaluation run. Scenarios that reveal new behavioral patterns not covered here should be added. The goal is not to check boxes — it is to find the gaps between the spec and the real experience.*
