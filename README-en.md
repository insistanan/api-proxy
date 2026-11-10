# api-proxy

api-proxy is a self-hosted LLM API gateway that manages upstream channels, converts protocols, handles failover, and checks content for sensitive data and dangerous commands. The web admin UI is embedded in the Go application, so deployment requires a single executable.

[中文](README.md) | Current version: see [VERSION](VERSION)

> Note: the detailed documentation under `docs/` is maintained in Chinese.

## Features

- **Protocol support**: accepts Claude Messages, OpenAI Responses, OpenAI Chat, Gemini, and Images requests. The first four support conversation protocol conversion. Images can use native Images API or Responses image-generation upstreams, with operations limited by the upstream protocol.
- **Channel scheduling**: tries channels by pool and configuration order, supports failover and circuit breakers, and lets healthy channels temporarily take priority within their pool. Requests sharing a URL, upstream protocol, and API key share concurrency slots and a FIFO queue. Retries respect 429 cooldowns and request budgets. Scheduling also manages multiple BaseURLs and session affinity.
- **Client setup**: configuration pages for Claude Code, Codex, OpenCode, pi-agent, DSH, and GPT help connect each client to the proxy and configure client disguise versions.
- **Content security**: detects sensitive data, stores reversible redaction mappings with AES-256-GCM, recursively filters private parameters, and uses an AI gate to review dangerous commands.
- **Logs and metrics**: tracks metrics by channel and model. Request and system logs are stored in SQLite and can also be queried through the CLI. Interception records are grouped by session.
- **Deployment**: the Vue 3 admin UI is embedded via `go:embed`. One port, 3000 by default, serves both the proxy API and the web UI.

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
| `PORT` / `ENV` | Listening port (default 3000) and runtime environment (default production) |
| `LOG_LEVEL` / `LOG_TO_CONSOLE` | Log level (default info) and console logging (default false) |

Configuration and databases default to `./.config` under the working directory. Runtime logs and request bodies are stored in its `logs.db` file. Request and response logging, timeouts, and CORS are managed in the settings UI.

When upgrading, legacy variables such as `ENABLE_REQUEST_LOGS` fill in settings that have not yet been saved; persisted settings take precedence afterward. Changing the log database path or metrics persistence requires a restart. Other request policies support live updates. Instructions for building a Docker image from source are in the [development guide](docs/开发说明.md) (Chinese).

### Download a Release

Download the executable for your platform from [Releases](../../releases). Linux, macOS, and Windows builds are available for amd64 and arm64. Set your access key, start the application, and open `http://localhost:3000` in a browser for the admin UI.

### Build from Source

Requirements: Go 1.25, Node 20.19+ or 22.12+, and make. Install dependencies in `frontend/` first with `npm ci` or `bun install`. The root Makefile calls npm; on Windows, `npm run check` runs the quality gate without make.

```bash
make build   # frontend build → embed → Go compile, output: dist/api-proxy-go
make dev     # start Vite frontend and Air backend hot reload
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

Images channels support either native **Images API** or **Responses API** upstreams. Responses image generation requires a `responsesModel` main model; the client model and mappings select the image-generation tool model. JSON and SSE results are converted to Images format with `b64_json`. Hosted image URLs and variations require a native Images API channel.

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
