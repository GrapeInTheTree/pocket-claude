# CLAUDE.md

Project context for Claude Code when working in this repository.

## Overview

**Pocket Claude** — A Telegram bot (Go) that gives you remote access to Claude Code CLI from your phone. Messages from Telegram are processed instantly by calling `claude -p` as a subprocess, with full MCP tool access (Slack, Notion, Gmail, etc.).

## Architecture

```
Telegram --> Go Bot --> inbox.json --> Worker --> ProjectManager --> claude -p --resume <id> --> Telegram
                                          |            |
                                  Permission flow   Per-project executor routing
                                  via inline keyboard
                                          |
                                  BackgroundPool (max 3 concurrent)
                                    └── ephemeral Executor per task
                                        (independent session, approval, context)
```

**Key components:**

- **ProjectManager**: Owns per-project `Executor` instances. Routes all CLI calls to the active project. Persists config to `projects.json`. Switched via `/project` command.
- **Worker**: Single goroutine, sequential foreground processing from a buffered channel. Prevents concurrent `claude -p` calls on the main session.
- **BackgroundPool**: Up to 3 concurrent background tasks via `/bg`. Each task gets an ephemeral `Executor` (not stored in Manager). Independent approval flow routed by `bg_` prefix on callback IDs. Atomic counter for unique task IDs. Semaphore-based slot limiting. Shutdown-safe (`closed` flag rejects new submissions after `CancelAll`).
- **Session**: Explicit `--resume <session_id>` tracking per project. Never uses `--continue` (prevents conflicts with Claude Code terminal in same directory).
- **Permissions**: Two-phase execution. Phase 1: default permissions, check `permission_denials` in JSON output. Phase 2 (if approved): re-run with `--dangerously-skip-permissions`. Markdown fallback on parse errors.
- **Media**: Photos/documents downloaded from Telegram to `/tmp`, file path passed to Claude CLI for multimodal analysis.
- **TTL**: Messages older than `MESSAGE_TTL_MINUTES` auto-expire. Restart-caused errors (`signal: killed`) retry silently. User-initiated `/cancel` marks as `failed` permanently.
- **Single Instance**: PID file (`bot.pid`) ensures only one bot runs; auto-kills previous on start.
- **Concurrency**: Goroutine spawning bounded by semaphore (max 10). All Telegram messages UTF-8 sanitized.
- **Typing Indicator**: Sends Telegram "typing..." action every 4 seconds while processing. Stops on completion.
- **Usage Tracking**: Parses `total_cost_usd` from Claude CLI JSON output. Tracks per-project messages and API-equivalent cost. `/usage` and `/project info` show stats. Session cost resets on `/new` or project switch.

## Project Layout

```
cmd/pocket-claude/main.go           # Entry point, dependency wiring
internal/
  config/config.go                   # Config, env loading, logger, PID file
  store/models.go                    # Data types, 7 statuses (pending/processing/done/sent/error/failed/expired)
  store/store.go                     # JSON file I/O, sync.Mutex, lock file
  store/store_test.go                # Store CRUD, stats, clear, outbox tests
  bot/bot.go                         # Telegram listener, callback handler (project/resume/approval/bg_), outbox poller
  bot/bot_test.go                    # safeTruncate UTF-8 tests
  bot/commands.go                    # 13 commands: /help /new /name /btw /resume /model /cancel /usage /status /clear /retry /project /bg
  bot/media.go                       # Photo/document download with HTTP status validation
  config/config_test.go              # Env helpers, PID file tests
  claude/executor.go                 # Claude CLI execution, --resume session tracking, --name, model switching
  claude/executor_test.go            # Stream JSON parsing, UTF-8 truncation tests
  project/types.go                   # ProjectConfig, ProjectsFile, ProjectUsage types
  project/manager.go                 # Multi-project manager: per-project executor routing, persistence, background executor factory
  project/manager_test.go            # Project CRUD, switching, renaming, usage, persistence tests
  worker/worker.go                   # Message queue, TTL check, error classification, cancel detection, retry
  worker/approval.go                 # Permission approval flow, tool name formatting, UTF-8 safe truncation
  worker/background.go               # Background task pool: 3 concurrent slots, ephemeral executors, independent approval
  worker/approval_test.go            # Truncate UTF-8, EscapeMD, FormatToolName, permission message tests
  worker/worker_test.go              # Tool summary, error classification tests
  worker/background_test.go          # Pool: slots, cancel, cleanup, status, approval, concurrency tests
```

## Build & Run

```bash
make build          # or: go build -o pocket-claude ./cmd/pocket-claude/
make test           # or: go test ./...           (57 cases)
make test-race      # or: go test -race ./...     (with race detector)
make vet            # or: go vet ./...
make fmt            # or: gofmt -w .
make fmt-check      # verify gofmt compliance
make ci             # run full CI pipeline locally (fmt-check + vet + build + test-race)
make run            # build & run
```

## Key Design Decisions

### Explicit --resume over --continue
`--continue` picks the most recent session in the directory, which conflicts with Claude Code terminal sessions in the same directory. `--resume <session_id>` always targets the bot's own session.

### Two-phase permission flow
Phase 1 detects `permission_denials` in JSON output. If denied, Telegram inline keyboard asks user. Phase 2 re-runs with `--dangerously-skip-permissions` if approved. 2-minute timeout auto-denies.

### Error classification
- `signal: killed` + `signal: terminated` → restart artifact, retry once silently
- `/cancel` → sets `cancelled` flag, marks as `failed` permanently (no retry)
- Real errors → notify user, retry up to `MAX_RETRY_COUNT`, then `failed`

### Message TTL
Messages older than `MESSAGE_TTL_MINUTES` → auto `expired`. Prevents stale retry loops from bot restarts.

### Bounded concurrency
Goroutine spawning for callbacks/messages bounded by semaphore (max 10). Falls back to synchronous execution when limit reached.

### Background tasks (/bg)
Each background task gets its own ephemeral `Executor` (not stored in Manager's map). This avoids session tracking race conditions between foreground and background. Approval callbacks are routed by ID prefix: `bg_` → BackgroundPool, `msg_` → Worker. Atomic counter (`sync/atomic.Int64`) for task IDs prevents millisecond collisions. `closed` flag prevents new submissions after `CancelAll` during shutdown. Typing indicators run independently per task.

### Multi-project support
Each project gets its own `Executor` with independent session, workDir, and addDirs. `ProjectManager` replaces the single executor in the Worker. Projects persist to `projects.json`. Default project auto-created from `CLAUDE_WORK_DIR` on first run. `/project` command for add/remove/switch via inline keyboard. `/project search <keyword>` scans home directory (depth 3) for git repos matching keyword, shows results as inline buttons for one-tap add. Path validation on add (must be existing directory).

## Important Notes

- Only ONE bot instance at a time (Telegram Long Polling + PID file)
- Never commit `.env`, `inbox.json`, `outbox.json`, `bot.log`, `*.lock`, `bot.pid`, `projects.json`
- `inbox.json` / `outbox.json` may contain personal data
- Session files stored in `~/.claude/projects/` (managed by Claude CLI, do not modify)

## Environment Variables

| Variable | Default | Description |
|---|---|---|
| `TELEGRAM_TOKEN` | *(required)* | Bot token |
| `TELEGRAM_CHAT_ID` | *(required)* | Allowed chat ID |
| `INBOX_PATH` | `./inbox.json` | Incoming messages |
| `OUTBOX_PATH` | `./outbox.json` | Outgoing results |
| `LOCK_TIMEOUT_MINUTES` | `5` | Stale lock threshold |
| `MAX_RETRY_COUNT` | `2` | Error retry limit |
| `OUTBOX_POLL_INTERVAL_SECONDS` | `10` | Outbox poll interval |
| `LOG_FILE` | `./bot.log` | Log file path |
| `MESSAGE_TTL_MINUTES` | `10` | Message expiry time |
| `CLAUDE_CLI_PATH` | `claude` | CLI binary path |
| `CLAUDE_WORK_DIR` | `.` | CLI working directory |
| `CLAUDE_TIMEOUT_SECONDS` | `600` | CLI execution timeout (10 min) |
| `CLAUDE_SYSTEM_PROMPT` | *(none)* | Custom system prompt |
| `CLAUDE_MODEL` | *(none)* | Model override |
| `CLAUDE_ADD_DIRS` | `~` | Extra directories for CLI access |
| `WORKER_QUEUE_SIZE` | `100` | Worker queue capacity |
| `PROJECTS_FILE` | `./projects.json` | Project persistence file |
