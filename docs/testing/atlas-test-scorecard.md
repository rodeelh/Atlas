# Atlas Test Scorecard

_Last updated: 2026-04-11_

## Overall release confidence

**✅ Production-ready**

| Surface  | Status | Readiness |
| -------- | ------ | --------- |
| Backend  | PASS     | ✅ Ready |
| Web UI   | PASS     | ✅ Ready |
| E2E/Integration | PASS | ✅ Ready |

## Category coverage

| Category | Status | Notes |
| -------- | ------ | ----- |
| Unit tests (Go runtime) | PASS | go test ./... across 50+ packages |
| Integration tests (runtime) | PASS | internal/integration baseline + architecture guardrails |
| API/handler tests | PASS | covered inside internal/modules/* and internal/domain |
| Config validation | PASS | config/snapshot tested |
| Frontend / component tests | PASS | no component test runner installed; build + tsc are the gate |
| End-to-end critical flows | PASS | runtime_baseline_test boots HTTP server, exercises routes |
| Smoke / startup | PASS | runtime wiring_test |
| Build / package verification | PASS | go build ./... for runtime; vite build for web |
| Regression coverage | PASS | existing _test.go suites act as regression net |
| **Agents pipeline** | **PASS** | **13 new tests — see Agents section below** |
| Performance sanity | NOT YET COVERED | no benchmarks wired into release gate |
| Security checks | PASS | auth/middleware_security_test + cors_test in runtime |
| Race detector | SKIP | release tier only |

## Step results

| Step | Status | Note |
| ---- | ------ | ---- |
| runtime: go vet | PASS | — |
| runtime: go build | PASS | — |
| runtime: go test (full) | PASS | — |
| tui: go vet | PASS | — |
| tui: go build | PASS | — |
| tui: go test | PASS | — |
| web: tsc --noEmit | PASS | — |
| web: vite build | PASS | — |
| integration: runtime baseline + guardrails | PASS | — |

## Agents pipeline tests (2026-04-11)

13 tests in `internal/modules/teams/agent_pipeline_test.go` and `internal/chat/agents_context_test.go`. All green.

| # | Test | Coverage |
|---|------|----------|
| 1 | `TestAgentValidation_AllErrorPaths` | All 6 `validateDefinition` error paths + valid pass |
| 2 | `TestAgentHTTP_CRUD` | POST create → GET single → 404 for missing → PUT update → DELETE → confirm 404 |
| 3 | `TestAgentHTTP_RuntimeLifecycle` | disable → enabled=false → enable → pause → resume via HTTP |
| 4 | `TestAgentDelegation_SuccessPath` | Task + steps persisted; result in artifacts; agent returns to idle |
| 5 | `TestAgentDelegation_ErrorPath` | Error propagates; agent idle with `lastError` |
| 6 | `TestAgentDelegation_CancellationRoute` | Cancel running task → 200 cancelled; re-cancel → 409 |
| 7 | `TestAgentApproval_ApproveRejectLifecycle` | approve → completed; re-approve → 409; reject → cancelled |
| 8 | `TestAgentAllowedToolClasses_SkillFiltering` | `read` < total; `read+local_write` > read-only; nil/empty = no-op |
| 9 | `TestAgentCreate_SkillPatternValidation` | Valid pattern creates; invalid pattern → "match no registered skills" error |
| 10 | `TestAgentMultiAgent_IndependentDelegation` | Two agents delegated serially; tasks stored independently; both idle |
| 11 | `TestAgentRosterContext_PromptInjection` | Enabled agents in prompt; disabled excluded; missing file → "" |
| 12 | `TestAgentRosterContext_PromptInjection/all_disabled` | All-disabled AGENTS.md → empty roster block |
| 13 | `TestParseRosterMarkdown_EdgeCases` | Empty input; agent without ID excluded; optional activation field |

## Known risks and gaps

- **Web UI has no component-level test runner.** Vitest / Preact Testing Library is not installed. The current gate is `tsc --noEmit` + `vite build` — that catches type and build regressions but not behavior regressions.
- **No load/perf benchmarks in the release gate.** Add `go test -bench` for hot paths (agent loop, validate gate) before claiming SLA-grade readiness.
- **No headless browser E2E** against the web UI served by the runtime.
- **Agent delegation uses mock `delegateFn` in tests** — the real `delegateTask` path (which runs a live sub-agent loop against a provider) is not covered by unit tests; covered by manual smoke testing only.

## Commands used to generate this scorecard

```bash
./scripts/verify-release.sh standard
# or via Makefile:
make verify-release    # release tier (default)
make test-fast         # fast tier
make test-standard     # standard tier
```
