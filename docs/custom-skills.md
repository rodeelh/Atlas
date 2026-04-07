# Custom Skills

**Last updated: 2026-04-03**

Custom skills let you give Atlas new capabilities without touching Go code or recompiling.
A custom skill is a directory containing a JSON manifest and an executable script (any language).
Atlas calls it via subprocess once per tool invocation — one JSON line in, one JSON line out.

---

## Directory layout

```
~/Library/Application Support/ProjectAtlas/skills/
  <skill-id>/
    skill.json    ← manifest (required)
    run           ← executable (required, chmod +x)
    .env          ← optional local credentials (not read by Atlas automatically)
    lib/          ← any supporting files your script imports
```

Atlas scans the `skills/` directory at startup. Each sub-directory that contains a valid
`skill.json` **and** a `run` executable is registered. Invalid entries are skipped with a
warning log — they never prevent Atlas from starting.

---

## Installing a custom skill

### Via the Skills UI
1. Open Atlas → **Skills**
2. Scroll to **Custom Extensions**
3. Click **Install from Folder** → select your skill directory
4. Restart the Atlas daemon: `make daemon-restart` (or `launchctl kickstart -k gui/$(id -u)/Atlas`)

### Manually
```bash
# Copy your skill directory into the skills folder
cp -r ~/my-skills/jira \
  ~/Library/"Application Support"/ProjectAtlas/skills/

# Restart the daemon
launchctl kickstart -k gui/$(id -u)/Atlas
```

### Via the API
```bash
curl -s -X POST http://localhost:1984/skills/install \
  -H 'Content-Type: application/json' \
  -d '{"path": "/Users/you/my-skills/jira"}'
```

---

## The `skill.json` manifest

```jsonc
{
  "id": "jira",               // unique ID — also the action prefix (jira.search)
  "name": "Jira",             // display name in the Skills UI
  "version": "1.0.0",
  "description": "Search, read, and create Jira issues",
  "author": "Your Name",
  "actions": [
    {
      "name": "search",                       // action suffix → jira.search
      "description": "Search Jira issues by JQL query",
      "permission_level": "read",             // "read" | "draft" | "execute"
      "action_class": "read",                 // see Action classes below
      "parameters": {                         // JSON Schema object (optional)
        "type": "object",
        "properties": {
          "query": {
            "type": "string",
            "description": "JQL query, e.g. 'project = APP AND status = Open'"
          },
          "max_results": {
            "type": "integer",
            "description": "Maximum issues to return (default 20)"
          }
        },
        "required": ["query"]
      }
    },
    {
      "name": "create",
      "description": "Create a new Jira issue",
      "permission_level": "execute",
      "action_class": "external_side_effect",
      "parameters": {
        "type": "object",
        "properties": {
          "project": { "type": "string", "description": "Project key (e.g. APP)" },
          "summary": { "type": "string", "description": "Issue title" },
          "description": { "type": "string", "description": "Issue body (optional)" },
          "issue_type": { "type": "string", "description": "Bug | Task | Story (default Task)" }
        },
        "required": ["project", "summary"]
      }
    }
  ]
}
```

### Field reference

| Field | Required | Description |
|-------|----------|-------------|
| `id` | No* | Unique skill ID. Defaults to the directory name. Action IDs become `<id>.<name>`. |
| `name` | No* | Display name. Defaults to `id`. |
| `version` | No | Semver string. Defaults to `"1.0"`. |
| `description` | Yes | Shown in the Skills UI and injected into the model's tool descriptions. |
| `author` | No | Attribution only. |
| `actions` | Yes | One or more action definitions. |
| `actions[].name` | Yes | Slug-style name. Becomes the action suffix (`id.name`). |
| `actions[].description` | Yes | Passed to the model as the tool description — be specific. |
| `actions[].permission_level` | Yes | `"read"` (auto-approve) · `"draft"` · `"execute"` (requires approval). |
| `actions[].action_class` | Yes | Controls the approval gate (see below). |
| `actions[].parameters` | No | JSON Schema `object`. Omit for zero-argument actions. |

### Action classes

| Class | Approval | Use for |
|-------|----------|---------|
| `read` | Auto | Fetching data, no side effects |
| `local_write` | Auto | Writing local files |
| `destructive_local` | Required | Deleting local state |
| `external_side_effect` | Required | Calling external APIs with write/mutation operations |
| `send_publish_delete` | Required | Sending messages, publishing posts, deleting remote data |

---

## The subprocess protocol

Atlas spawns `run` fresh for each tool call and communicates over stdio:

```
stdin  → one JSON line: {"action": "<name>", "args": {…}}
stdout ← one JSON line: {"success": true,  "output": "…"}
           or          {"success": false, "error":  "…"}
```

- The process is killed after **30 seconds**
- stdout output is capped at **1 MB** before passing to the model
- Working directory is set to the skill's own folder — relative paths work
- Environment variables pass through from the daemon process
- Exit code is ignored — always emit the JSON response line

---

## Credentials and secrets

Atlas passes its full environment to the subprocess. Store credentials as
environment variables and read them in your script.

**Option 1 — System environment** (recommended for personal use)
Add to your shell profile (`~/.zshrc`):
```bash
export JIRA_API_TOKEN="your-token-here"
export JIRA_BASE_URL="https://yourco.atlassian.net"
```

**Option 2 — Atlas Keychain bundle** (`customSecrets`)
In Atlas Settings → API Keys, add a custom key such as `JIRA_API_TOKEN`.
Atlas stores it in the Keychain bundle and makes it available in the daemon
process environment automatically. No restart needed after adding.

**Option 3 — `.env` file in your skill directory**
Source it yourself at the top of your `run` script:
```python
from dotenv import load_dotenv
load_dotenv(os.path.join(os.path.dirname(__file__), '.env'))
```
*(Atlas does not source `.env` automatically — you own that call.)*

---

## Examples

### Example 1 — Linear issue search (Python, API key auth)

**`linear/skill.json`**
```json
{
  "id": "linear",
  "name": "Linear",
  "version": "1.0.0",
  "description": "Search and create Linear issues",
  "actions": [
    {
      "name": "search",
      "description": "Search Linear issues by keyword or team",
      "permission_level": "read",
      "action_class": "read",
      "parameters": {
        "type": "object",
        "properties": {
          "query": { "type": "string", "description": "Search keyword" },
          "team":  { "type": "string", "description": "Team key to filter by (optional)" }
        },
        "required": ["query"]
      }
    }
  ]
}
```

**`linear/run`** (chmod +x)
```python
#!/usr/bin/env python3
import json, os, sys, urllib.request, urllib.parse

LINEAR_API = "https://api.linear.app/graphql"
TOKEN = os.environ.get("LINEAR_API_KEY", "")

def search(query, team=None):
    filter_clause = f'filter: {{ title: {{ containsIgnoreCase: "{query}" }} }}'
    if team:
        filter_clause += f', team: {{ key: {{ eq: "{team}" }} }}'
    gql = f"""
    query {{
      issues({filter_clause}, first: 10) {{
        nodes {{ id title state {{ name }} url }}
      }}
    }}
    """
    data = json.dumps({"query": gql}).encode()
    req = urllib.request.Request(
        LINEAR_API, data=data,
        headers={"Authorization": TOKEN, "Content-Type": "application/json"}
    )
    with urllib.request.urlopen(req, timeout=20) as r:
        body = json.loads(r.read())
    issues = body["data"]["issues"]["nodes"]
    if not issues:
        return "No issues found."
    lines = [f"- [{i['state']['name']}] {i['title']}\n  {i['url']}" for i in issues]
    return "\n".join(lines)

def main():
    req = json.loads(sys.stdin.readline())
    action = req.get("action", "")
    args   = req.get("args") or {}
    try:
        if action == "search":
            out = search(args["query"], args.get("team"))
            print(json.dumps({"success": True, "output": out}))
        else:
            print(json.dumps({"success": False, "error": f"unknown action: {action}"}))
    except Exception as e:
        print(json.dumps({"success": False, "error": str(e)}))

if __name__ == "__main__":
    main()
```

Set `LINEAR_API_KEY` in Atlas Settings → API Keys and install the folder.

---

### Example 2 — GitHub PR summary (Python, Bearer auth, multiple actions)

**`github/skill.json`**
```json
{
  "id": "github",
  "name": "GitHub",
  "version": "1.0.0",
  "description": "Read pull requests and repository information from GitHub",
  "actions": [
    {
      "name": "list_prs",
      "description": "List open pull requests for a repository",
      "permission_level": "read",
      "action_class": "read",
      "parameters": {
        "type": "object",
        "properties": {
          "repo":  { "type": "string", "description": "owner/repo, e.g. acme/backend" },
          "state": { "type": "string", "description": "open | closed | all (default open)" }
        },
        "required": ["repo"]
      }
    },
    {
      "name": "get_pr",
      "description": "Get full details of a single pull request including diff stats",
      "permission_level": "read",
      "action_class": "read",
      "parameters": {
        "type": "object",
        "properties": {
          "repo":   { "type": "string", "description": "owner/repo" },
          "number": { "type": "integer", "description": "PR number" }
        },
        "required": ["repo", "number"]
      }
    }
  ]
}
```

**`github/run`** (chmod +x)
```python
#!/usr/bin/env python3
import json, os, sys, urllib.request

BASE = "https://api.github.com"
TOKEN = os.environ.get("GITHUB_TOKEN", "")

def gh(path):
    req = urllib.request.Request(
        BASE + path,
        headers={
            "Authorization": f"Bearer {TOKEN}",
            "Accept": "application/vnd.github+json",
            "X-GitHub-Api-Version": "2022-11-28",
        }
    )
    with urllib.request.urlopen(req, timeout=20) as r:
        return json.loads(r.read())

def list_prs(repo, state="open"):
    prs = gh(f"/repos/{repo}/pulls?state={state}&per_page=15")
    if not prs:
        return f"No {state} PRs found."
    lines = [f"#{p['number']} {p['title']} (@{p['user']['login']})" for p in prs]
    return "\n".join(lines)

def get_pr(repo, number):
    p = gh(f"/repos/{repo}/pulls/{number}")
    return (
        f"#{p['number']} {p['title']}\n"
        f"Author: @{p['user']['login']}  |  State: {p['state']}\n"
        f"Branch: {p['head']['ref']} → {p['base']['ref']}\n"
        f"+{p['additions']} / -{p['deletions']} in {p['changed_files']} files\n"
        f"{p['body'] or '(no description)'}\n"
        f"{p['html_url']}"
    )

def main():
    req = json.loads(sys.stdin.readline())
    action = req.get("action", "")
    args   = req.get("args") or {}
    try:
        if action == "list_prs":
            out = list_prs(args["repo"], args.get("state", "open"))
        elif action == "get_pr":
            out = get_pr(args["repo"], int(args["number"]))
        else:
            print(json.dumps({"success": False, "error": f"unknown action: {action}"}))
            return
        print(json.dumps({"success": True, "output": out}))
    except Exception as e:
        print(json.dumps({"success": False, "error": str(e)}))

if __name__ == "__main__":
    main()
```

Set `GITHUB_TOKEN` in Atlas Settings → API Keys.

---

### Example 3 — System uptime check (shell script, no auth)

Skills can be any executable — shell, Ruby, Node.js, compiled binaries. This one uses bash:

**`sysinfo/skill.json`**
```json
{
  "id": "sysinfo",
  "name": "System Info",
  "version": "1.0.0",
  "description": "Quick local system diagnostics — uptime, disk, memory",
  "actions": [
    {
      "name": "summary",
      "description": "Get a one-line system status: uptime, disk usage, and memory pressure",
      "permission_level": "read",
      "action_class": "read"
    }
  ]
}
```

**`sysinfo/run`** (chmod +x)
```bash
#!/usr/bin/env bash
# Read the action from stdin (we only have one so we ignore it)
read -r input

uptime_str=$(uptime | sed 's/.*up /up /' | sed 's/, [0-9]* user.*//')
disk=$(df -h / | awk 'NR==2 {print $3 " used / " $2 " total (" $5 " full)"}')
mem=$(memory_pressure | grep 'System-wide memory free percentage' | awk '{print $NF}' || echo "n/a")

output="Uptime: $uptime_str | Disk /: $disk | Memory free: $mem"
printf '{"success":true,"output":"%s"}\n' "$output"
```

---

### Example 4 — Slack message sender (Python, write action)

**`slack-notify/skill.json`**
```json
{
  "id": "slack-notify",
  "name": "Slack Notify",
  "version": "1.0.0",
  "description": "Send messages to a Slack channel via Incoming Webhook",
  "actions": [
    {
      "name": "send",
      "description": "Send a message to the configured Slack channel",
      "permission_level": "execute",
      "action_class": "send_publish_delete",
      "parameters": {
        "type": "object",
        "properties": {
          "text": { "type": "string", "description": "Message text to send" }
        },
        "required": ["text"]
      }
    }
  ]
}
```

**`slack-notify/run`** (chmod +x)
```python
#!/usr/bin/env python3
import json, os, sys, urllib.request

WEBHOOK = os.environ.get("SLACK_WEBHOOK_URL", "")

def main():
    req = json.loads(sys.stdin.readline())
    args = req.get("args") or {}
    text = args.get("text", "")
    if not text:
        print(json.dumps({"success": False, "error": "text is required"}))
        return
    if not WEBHOOK:
        print(json.dumps({"success": False, "error": "SLACK_WEBHOOK_URL not configured"}))
        return
    payload = json.dumps({"text": text}).encode()
    urllib.request.urlopen(
        urllib.request.Request(WEBHOOK, data=payload,
            headers={"Content-Type": "application/json"}),
        timeout=10
    )
    print(json.dumps({"success": True, "output": f"Message sent to Slack: {text[:80]}"}))

if __name__ == "__main__":
    main()
```

Because `action_class` is `send_publish_delete`, Atlas will ask for approval before
sending — the user sees the message text in the approval prompt.

---

## Testing a skill locally

Before installing, test your `run` script directly:

```bash
# Single action test
echo '{"action": "search", "args": {"query": "bug"}}' | ./run

# Expected output
{"success": true, "output": "- [In Progress] Fix login crash\n  https://..."}
```

**Common mistakes:**
- Missing shebang (`#!/usr/bin/env python3`) — script runs as shell and fails silently
- `run` not executable — `chmod +x run`
- Printing extra lines before the JSON — Atlas takes the **last** line of stdout
- Forgetting to handle unknown `action` names — always emit a JSON error response
- Writing to stderr — stderr goes to the Atlas daemon log (`make daemon-logs`), not to the model

---

## Forge-generated skills

When you install a Forge skill (Settings → Forge → Install), Atlas automatically:
1. Generates a `skill.json` with actions derived from the AI research plan
2. Generates a Python `run` script with the HTTP plan embedded
3. Writes both to `skills/<skillID>/` so the agent can call them

Forge skills appear in the **Custom Extensions** group with a **Generated** badge.
They are managed by the Forge screen — use Forge Uninstall to remove them.
Their `run` script supports bearer, API-key-header, query-param, and basic auth
and is regenerated if you re-install the proposal.

---

## Removing a custom skill

**Via the Skills UI:** Skills screen → Custom Extensions → click **Remove** next to the skill.

**Via the API:**
```bash
curl -s -X DELETE http://localhost:1984/skills/jira
```

**Manually:**
```bash
rm -rf ~/Library/"Application Support"/ProjectAtlas/skills/jira
launchctl kickstart -k gui/$(id -u)/Atlas   # restart to deregister
```

---

## File layout reference

```
skills/
  linear/
    skill.json        ← manifest
    run               ← python3 script, chmod +x
  github/
    skill.json
    run
  sysinfo/
    skill.json
    run               ← bash script, chmod +x
  slack-notify/
    skill.json
    run
    .env              ← SLACK_WEBHOOK_URL=https://hooks.slack.com/...
```
