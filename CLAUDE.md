# CLAUDE.md

Project context for Claude Code when working in this repository.

## Overview

Telegram bot (Go) that bridges Claude Code CLI with Telegram. Messages from Telegram are processed instantly by calling `claude -p` as a subprocess, with full MCP tool access.

## Architecture

```
Telegram → Go Bot → inbox.json → Worker → claude -p --resume <id> → Telegram
                                       ↕
                              Permission flow via inline keyboard
```

- **Worker**: Single goroutine processing messages from a buffered channel. Calls `claude -p` and sends results back to Telegram.
- **Session**: Uses explicit `--resume <session_id>` to track conversations. Never uses `--continue` (prevents conflicts with Claude Code terminal in same directory).
- **Permissions**: Two-phase execution. First run with default permissions → check `permission_denials` in JSON output → ask user via Telegram inline keyboard → re-run with `--dangerously-skip-permissions` if approved. Markdown fallback on parse errors.
- **Media**: Photos and documents downloaded from Telegram to `/tmp`, file path passed to Claude CLI for multimodal analysis.
- **Single Instance**: PID file (`bot.pid`) ensures only one bot runs; auto-kills previous on start.
- **TTL**: Messages older than `MESSAGE_TTL_MINUTES` auto-expire. Restart-caused errors (`signal: killed`) retry silently without counting.

## Project Structure

```
cmd/cowork-bot/main.go           # Entry point, wiring
internal/
  config/config.go               # Config, logger, PID file
  store/models.go                # Data types (7 statuses: pending/processing/done/sent/error/failed/expired)
  store/store.go                 # JSON file I/O, mutex, lock file
  bot/bot.go                     # Telegram listener, callbacks, outbox poller
  bot/commands.go                # 9 commands: /help /new /btw /resume /model /cancel /status /clear /retry
  bot/media.go                   # Photo/document download from Telegram API
  claude/executor.go             # Claude CLI execution, --resume session tracking, model switching
  worker/worker.go               # Message queue, TTL, error classification, retry processor
  worker/approval.go             # Permission approval flow, tool name formatting
```

## Build & Run

```bash
go build -o cowork-bot ./cmd/cowork-bot/   # build
go vet ./...                                # lint
./cowork-bot                                # run
```

## Key Design Decisions

### Explicit --resume over --continue
- `--continue` picks the most recent session in the directory → conflicts with Claude Code terminal
- `--resume <session_id>` always targets the bot's own session → no conflicts
- Session ID captured from JSON output on first call, reused for all subsequent calls

### Two-phase permission flow
- Phase 1: default permissions → detect `permission_denials` in JSON output
- If denied: Telegram inline keyboard `[Allow] [Deny]` with 2-min timeout
- Phase 2 (if approved): re-run with `--dangerously-skip-permissions`

### Message TTL and error classification
- Messages older than `MESSAGE_TTL_MINUTES` → auto `expired` (prevents stale retry loops)
- `signal: killed` / `signal: terminated` → restart artifact, retry once silently
- Real errors (timeout, CLI error) → notify user, retry up to `MAX_RETRY_COUNT`
- Max retries exceeded → `failed` status, permanent (use /retry to force reset)

### cmd/internal Go project layout
- `cmd/` for entry points, `internal/` for private packages
- Clean dependency graph: main → bot/worker/claude/store/config (no circular deps)
- Each package has a single responsibility

## Important Notes

- Only ONE bot instance at a time (Telegram Long Polling + PID file)
- Never commit `.env`, `inbox.json`, `outbox.json`, `bot.log`, `*.lock`, `bot.pid`
- `inbox.json` / `outbox.json` may contain personal data

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
