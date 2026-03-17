# CLAUDE.md

Project context for Claude Code when working in this repository.

## Overview

**Pocket Claude** — A Telegram bot (Go) that gives you remote access to Claude Code CLI from your phone. Messages from Telegram are processed instantly by calling `claude -p` as a subprocess, with full MCP tool access (Slack, Notion, Gmail, etc.).

## Architecture

```
Telegram --> Go Bot --> inbox.json --> Worker --> claude -p --resume <id> --> Telegram
                                          |
                                  Permission flow via inline keyboard
```

**Key components:**

- **Worker**: Single goroutine, sequential processing from a buffered channel. Prevents concurrent `claude -p` calls.
- **Session**: Explicit `--resume <session_id>` tracking. Never uses `--continue` (prevents conflicts with Claude Code terminal in same directory).
- **Permissions**: Two-phase execution. Phase 1: default permissions, check `permission_denials` in JSON output. Phase 2 (if approved): re-run with `--dangerously-skip-permissions`. Markdown fallback on parse errors.
- **Media**: Photos/documents downloaded from Telegram to `/tmp`, file path passed to Claude CLI for multimodal analysis.
- **TTL**: Messages older than `MESSAGE_TTL_MINUTES` auto-expire. Restart-caused errors (`signal: killed`) retry silently. User-initiated `/cancel` marks as `failed` permanently.
- **Single Instance**: PID file (`bot.pid`) ensures only one bot runs; auto-kills previous on start.
- **Concurrency**: Goroutine spawning bounded by semaphore (max 10). All Telegram messages UTF-8 sanitized.

## Project Layout

```
cmd/pocket-claude/main.go           # Entry point, dependency wiring
internal/
  config/config.go                   # Config, env loading, logger, PID file
  store/models.go                    # Data types, 7 statuses (pending/processing/done/sent/error/failed/expired)
  store/store.go                     # JSON file I/O, sync.Mutex, lock file
  bot/bot.go                         # Telegram listener, callback handler, outbox poller
  bot/commands.go                    # 10 commands: /help /new /name /btw /resume /model /cancel /status /clear /retry
  bot/media.go                       # Photo/document download with HTTP status validation
  claude/executor.go                 # Claude CLI execution, --resume session tracking, --name, model switching
  worker/worker.go                   # Message queue, TTL check, error classification, cancel detection, retry
  worker/approval.go                 # Permission approval flow, tool name formatting, UTF-8 escape
```

## Build & Run

```bash
go build -o pocket-claude ./cmd/pocket-claude/    # build
go vet ./...                                       # lint
./pocket-claude                                    # run
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

## Important Notes

- Only ONE bot instance at a time (Telegram Long Polling + PID file)
- Never commit `.env`, `inbox.json`, `outbox.json`, `bot.log`, `*.lock`, `bot.pid`
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
| `MAX_RETRY_COUNT` | `3` | Error retry limit |
| `OUTBOX_POLL_INTERVAL_SECONDS` | `10` | Outbox poll interval |
| `LOG_FILE` | `./bot.log` | Log file path |
| `MESSAGE_TTL_MINUTES` | `10` | Message expiry time |
| `CLAUDE_CLI_PATH` | `claude` | CLI binary path |
| `CLAUDE_WORK_DIR` | `.` | CLI working directory |
| `CLAUDE_TIMEOUT_SECONDS` | `120` | CLI execution timeout |
| `CLAUDE_SYSTEM_PROMPT` | *(none)* | Custom system prompt |
| `CLAUDE_MODEL` | *(none)* | Model override |
| `CLAUDE_ADD_DIRS` | `~` | Extra directories for CLI access |
| `WORKER_QUEUE_SIZE` | `100` | Worker queue capacity |
