# api-proxy

**Self-hosted LLM API proxy gateway** — multi-channel pooled scheduling, unified five-protocol entry with protocol conversion, content security, and a web admin UI, all shipped as a single binary.

[中文](README.md) | Current version: v3.3.0 (see [VERSION](VERSION))

> Note: the detailed documentation under `docs/` is maintained in Chinese.

## Features

- **Unified five-protocol entry**: Claude Messages, OpenAI Responses, OpenAI Chat, Gemini, and Images protocols are interchangeable — clients connect with any protocol, upstream channels can be any combination
- **Multi-channel pooled scheduling**: failover, per-channel three-state circuit breaker, sliding-window rate limiting with 429 smoothing, multi-BaseURL health management and dynamic ordering, Trace/BaseURL session affinity (improves prompt cache hit rate)
- **Client disguise & one-click setup**: configuration pages for Claude Code / Codex / OpenCode / pi-agent / DSH / GPT that point each CLI tool at this proxy, with configurable disguise version strings
- **Content security**: sensitive-data detection with reversible redaction (AES-256-GCM), recursive private-parameter filtering, and an AI gate for dangerous commands
- **Channel evaluation workbench**: probes / suites / batch review / real-time SSE progress
- **Observability**: per-channel and per-model metrics, request & system logs (SQLite-backed with CLI queries), grouped interception records
- **All-in-one deployment**: the Vue 3 admin UI is embedded into the Go binary via `go:embed`; a single port (3000 by default) serves both the proxy API and the web UI

## Quick Start

### Docker (recommended)

```bash
# Edit PROXY_ACCESS_KEY in docker-compose.yml (mandatory), then:
docker compose up -d
```

Key environment variables:

| Variable | Description |
|---|---|
| `PROXY_ACCESS_KEY` | Access key — **must be changed** |
| `ENABLE_WEB_UI` | `true` (default) all-in-one mode / `false` API-only mode |
| `LOG_LEVEL` / `ENABLE_REQUEST_LOGS` / `ENABLE_RESPONSE_LOGS` | Logging controls |

Configuration persists under `./.config`; logs are written to `./logs`.

### Download a Release

Grab the artifact for your platform (linux / macOS / Windows × amd64 / arm64) from [Releases](../../releases) — a single executable. Open `http://localhost:3000` in a browser for the admin UI.

### Build from Source

Requirements: Go 1.25, Node 20+ (or bun), make (on Windows, `npm run check` replaces the make-based gate).

```bash
make build   # frontend build → embed → Go compile, output: dist/api-proxy-go
make dev     # backend hot-reload development (run `npm run dev` in frontend/ separately)
make check   # full local gate before committing
```

## Endpoints

| Endpoint | Purpose |
|---|---|
| `POST /v1/messages`, `/v1/messages/count_tokens` | Claude Messages protocol |
| `POST /v1/responses` (incl. `/compact`) | OpenAI Responses protocol |
| `POST /v1/chat/completions` | OpenAI Chat protocol |
| `POST /v1/images/{generations,edits,variations}` | Images protocol |
| `POST /v1beta/models/*` | Gemini protocol |
| `GET /v1/models` | Model catalog |
| `/api/*` | Admin API (requires the access key via the `x-api-key` header) |
| `/health` | Health check (public; used by the Docker HEALTHCHECK) |

## Documentation

| Document | Contents |
|---|---|
| [docs/开发说明.md](docs/开发说明.md) | Environment setup, commands, quality gate, release flow, Docker deployment |
| [docs/架构说明.md](docs/架构说明.md) | Architecture diagrams, request flow, scheduling, data storage, auth model |
| [docs/规范与不变量.md](docs/规范与不变量.md) | Code conventions and key invariants |
| [docs/技术栈.md](docs/技术栈.md) | Dependencies and toolchain overview |
| [docs/历史说明.md](docs/历史说明.md) | Version history and key milestones |

For the full version history see [CHANGELOG.md](CHANGELOG.md) (Chinese).

## Contributing

This repository uses [AGENTS.md](AGENTS.md) as the single entry document for both AI coding agents and human contributors (`CLAUDE.md` contains only a one-line reference to it). Make sure `npm run check` (or `make check`) passes before committing.

## License

[MIT](LICENSE) © 2025 wangyusong, 2026 insistanan
