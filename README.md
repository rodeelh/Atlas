# Project Atlas

Atlas is a local AI operator. A single Go binary runs on your machine, serves a web UI, and connects to OpenAI, Anthropic, Gemini, or a local LM Studio model.

It handles chat, memory, automations, skills, approvals, browser control, Telegram/Discord/WhatsApp integration, and a Forge pipeline for AI-generated skill extensions.

## Repository Layout

```text
Project Atlas/
├── atlas-runtime/          ← Go runtime (default port 1984)
├── atlas-web/              ← Preact + TypeScript web UI
├── docs/                   ← Architecture and API reference
├── archive/
│   └── swift/              ← Archived Swift packages (not built)
└── README.md               ← This file
```

## Quick Start

```bash
# 1) Build the runtime
cd atlas-runtime
go build -o atlas-runtime ./cmd/atlas-runtime

# 2) Build the web UI
cd ../atlas-web
npm ci
npm run build

# 3) Run Atlas
cd ../atlas-runtime
./atlas-runtime -port 1984 -web-dir ../atlas-web/dist
```

Open [http://localhost:1984/web](http://localhost:1984/web).

On first run Atlas creates its state under `~/Library/Application Support/ProjectAtlas/`. You do not need a repo-local `.env` file to start the app.

## Secrets And Local Config

- Atlas runtime credentials should be added in the web UI under `Settings -> Credentials`.
- Those credentials are stored in the macOS Keychain, not in repo files.
- `config.json` is for non-secret runtime settings only. Do not put API keys, bot tokens, webhook secrets, or session material in it.
- Repo-level `.env` files are only for optional developer scripts. Start from [.env.example](./.env.example) if you need them, and keep real values in an untracked local file such as `.env.local`.
- Atlas does not auto-load `.env` files for runtime configuration.

### Remote Access Security Notes

- LAN remote access now requires HTTPS end-to-end (or a trusted local reverse proxy that terminates TLS and forwards to `http://127.0.0.1:1984`).
- Tailscale access can connect directly over the Tailnet address shown in Settings.
- Remote state-changing API calls require a session-bound CSRF token (`X-CSRF-Token`), fetched from `GET /auth/csrf`.

## Development

```bash
# Go runtime
cd atlas-runtime
go build ./... && go vet ./...

# Web UI (hot reload)
cd ../atlas-web
npm run dev
```

## Key Docs

| Doc | Purpose |
|-----|---------|
| [`CLAUDE.md`](./CLAUDE.md) | Package map, conventions, where to add things |
| [`docs/architecture.md`](./docs/architecture.md) | System design — packages, agent loop, skills, browser, vault |
| [`docs/runtime-api-v1.md`](./docs/runtime-api-v1.md) | Full HTTP API reference |
| [`PLAN.md`](./PLAN.md) | V1.0 product plan |
| [`archive/MIGRATION.md`](archive/MIGRATION.md) | Archived migration history — Phases 0–9 |

## Runtime Configuration

All state lives in `~/Library/Application Support/ProjectAtlas/`:

| File | Purpose |
|------|---------|
| `config.json` | Runtime config (port, AI provider, models) |
| `atlas.sqlite3` | Conversations, messages, memories, sessions |
| `MIND.md` | Agent system prompt (edit freely) |
| `GREMLINS.md` | Automation definitions |
| `go-runtime-config.json` | Go-only settings (e.g. `browserShowWindow`) |

API keys are stored in the macOS Keychain under `com.projectatlas.credentials`.
Agent credentials (passwords, TOTP secrets) are stored in the vault under `com.projectatlas.vault`.

## What's Deferred to V1.0

- **Multi-agent supervisor** — single-agent loop only.

## Swift Archive

The original Swift runtime is archived at `archive/swift/`. See `archive/MIGRATION.md` for the migration history.
