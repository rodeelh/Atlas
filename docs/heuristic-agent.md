# Agent Tool Selection

Atlas has four tool-selection modes. The default **Smart** mode is now a compact hybrid:

1. Before the main turn, a fast background model routes the message against a small capability-group manifest instead of the full tool list.
2. Atlas injects only the tools for those selected groups, plus `request_tools`.
3. If the initial compact set is insufficient, the model can call `request_tools` again with `broad=true` or with `categories`.

This gets the right tools in front of the model sooner, without paying the token cost of sending every tool definition up front.

| Mode | Selection behavior |
| --- | --- |
| `lazy` / Smart | Uses a compact AI router to preselect capability groups, then keeps `request_tools` available as an escape hatch. |
| `heuristic` / Keywords | Injects `SelectiveToolDefs(userMessage)` before the main model turn. |
| `llm` / AI Router | Uses the compact AI router without `request_tools`. |
| `off` | Injects all tools; explicit opt-in only. |

```mermaid
flowchart TD
    USER(["User message"]) --> MODE{"ToolSelectionMode"}

    MODE -- "lazy / Smart" --> SMART["compact capability router"]
    MODE -- "heuristic / Keywords" --> SHORT["SelectiveToolDefs(userMessage)"]
    MODE -- "llm / AI Router" --> LLM["selectToolsWithLLM()"]
    MODE -- "off" --> FULL["Full ToolDefinitions()"]

    SMART --> CAP1["selected groups + request_tools"]
    SHORT --> CAP1
    CAP1 --> CLOUD["Cloud model turn"]
    CLOUD --> ENOUGH{"can complete?"}
    ENOUGH -- "yes" --> TOOLS["Use selected tools"]
    ENOUGH -- "no, calls request_tools again" --> BROAD{"broad or categories?"}
    BROAD -- "categories" --> CAT["ToolDefsForGroups(categories)"]
    BROAD -- "broad / omitted" --> FULL
    CAT --> CAP2["capToolsForProvider()"]
    FULL --> CAP2
    CAP2 --> CLOUD2["Cloud model with expanded tools"]
    LLM --> CAP1

    TOOLS --> APPROVAL{"Needs approval?"}
    CLOUD2 --> APPROVAL
    APPROVAL -- "yes" --> DEFER["approval_required"]
    APPROVAL -- "no" --> EXEC["execute tools"]
    EXEC --> DONE["Answer / finish turn"]
```

## Request Tools Contract

`request_tools` accepts optional arguments:

- `broad: true` asks Atlas to send the broad/full tool surface.
- `categories: [...]` asks Atlas to send all tools in specific capability groups.

Supported categories:

- `automation`
- `communication`
- `workflow`
- `weather`
- `web`
- `finance`
- `office`
- `media`
- `mac`
- `shell`
- `files`
- `vault`
- `browser`
- `voice`
- `creative`
- `forge`
- `meta`

## Safety

Tool selection only changes what the model can see. It does not bypass:

- provider tool caps
- action approval policy
- workflow trust scope
- filesystem root restrictions
- bridge destination validation
