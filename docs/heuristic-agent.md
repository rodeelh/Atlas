# Agent Tool Selection — Flow Diagram

There are four independent tool selection modes. **Lazy** and **heuristic** are separate
modes that share one component — `SelectiveToolDefs` — but invoke it at different times
and for different reasons.

| | Heuristic | Lazy |
|---|---|---|
| When scoring runs | Before the loop | Inside the loop, only if model asks |
| Who decides tools are needed | service.go (always) | The model (on demand) |
| Entry token cost | ~800–1 800 | ~100 |
| Extra turn cost | None | +1 (free, doesn't count against maxIter) |

---

```mermaid
flowchart TD
    USER(["👤 User Message"]) --> SVC

    subgraph SVC["service.go — HandleMessage"]
        direction TB
        MODE{"ToolSelectionMode"}

        MODE -- '"heuristic"\nor empty' --> HEUR_SCORE
        MODE -- '"lazy"' --> LAZY_ONE
        MODE -- '"llm"' --> ROUTER
        MODE -- '"off"' --> FULL

        subgraph HEUR_SCORE["SelectiveToolDefs()  ·  runs before loop"]
            direction TB
            subgraph SCORING["scoreGroups()  — heuristic.go"]
                direction LR
                T1["Tier 1 · Phrase\n+2 pts  (substring)"]
                T2["Tier 2 · Pair\n+2 pts  (verb + object\nboth whole-word)"]
                T3["Tier 3 · Word\n+1 pt   (whole-word\nnegation-aware)"]
                T1 & T2 & T3 --> TSUM(["group score"])
            end
            THRESH{"score ≥ 1?"}
            ALWAYS["Always-on\ncore · management · custom"]
            COND["Conditional groups\nbrowser · filesystem\nsystem · scripting · services"]
            SCORING --> THRESH
            THRESH -- yes --> COND
            THRESH -- no --> XDROP(["group excluded"])
            ALWAYS & COND --> HEUR_OUT(["26–57 tools"])
        end

        LAZY_ONE["1 meta-tool only\nrequest_tools  ~100 tokens"]

        ROUTER["selectToolsWithLLM()\nfast model pre-selects\n─────────────────────\nany failure → SelectiveToolDefs()"]

        FULL["nil  →  loop uses\nfull ToolDefinitions()\n~130+ tools"]

        CAP["capToolsForProvider()\nhard cap 128  (OpenAI / LM Studio / Engine)\npriority: forge › core › mgmt › rest"]

        HEUR_OUT --> CAP
        LAZY_ONE --> CAP
        ROUTER  --> CAP
        FULL    --> CAP
    end

    CAP --> LOOP

    subgraph LOOP["agent/loop.go — Loop.Run()  (i = 0 … maxIter-1)"]
        direction TB
        STREAM["streamWithToolDetection()\nstreaming API call"]
        NO_TOOLS{"any tool\ncalls?"}
        DONE(["✅ complete"])
        NO_TOOLS -- no --> DONE

        LAZY_CHK{"request_tools called\nAND not yet upgraded?"}
        UPGRADE["SelectiveToolDefs(userMessage)\n+ capToolsForProvider()\ntoolsUpgraded = true\ni--  ← free iteration"]
        RESUME["append assistant + tool result msgs\ncontinue with scored tool set"]

        APPROVE{"NeedsApproval?"}
        DEFER(["⏸ pendingApproval\nSSE: approval_required"])
        EXEC_PAR["stateless tools\nparallel goroutines"]
        EXEC_SER["stateful tools  browser.*\nserial — shared go-rod"]
        RESULTS["append tool result messages\nSSE: tool_finished"]
        CONT["→ next iteration"]
        MAXITER(["⚠️ max iterations reached"])

        STREAM --> NO_TOOLS
        NO_TOOLS -- yes --> LAZY_CHK
        LAZY_CHK -- yes --> UPGRADE --> RESUME --> STREAM
        LAZY_CHK -- no  --> APPROVE
        APPROVE -- yes --> DEFER
        APPROVE -- no  --> EXEC_PAR & EXEC_SER --> RESULTS --> CONT --> STREAM
    end

    LOOP -- "i ≥ maxIter" --> MAXITER

    style HEUR_SCORE fill:#1e3a5f,stroke:#4a9eff,color:#e0f0ff
    style SCORING    fill:#0d2137,stroke:#4a9eff,color:#c8e6ff
    style LOOP       fill:#1a3a1a,stroke:#4aff6a,color:#e0ffe0
    style CAP        fill:#3a1a1a,stroke:#ff6a4a,color:#ffe0e0
    style SVC        fill:#1a1a3a,stroke:#9a6aff,color:#e8e0ff
    style LAZY_ONE   fill:#2a1a3a,stroke:#cc88ff,color:#eed8ff
    style ROUTER     fill:#2a2a1a,stroke:#cccc44,color:#ffffcc
```

---

## How lazy and heuristic relate

```
Heuristic mode                      Lazy mode
──────────────                      ─────────
service.go runs                     service.go injects
SelectiveToolDefs() ──────┐         1 tool only
before the loop       shared   ┌──────────────────────
                      component│   loop starts →
26–57 tools enter ←───────────┘   model replies
the loop pre-loaded                  │
                                     ├─ text only → done (scoring never ran)
                                     │
                                     └─ calls request_tools
                                          │
                                          └─ SelectiveToolDefs() ──┘
                                             runs NOW with same
                                             3-tier scoring
                                             i--  (free turn)
                                             loop continues
```
