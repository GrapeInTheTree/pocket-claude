# CLAUDE.md

Project context for Claude Code when working in this repository.

## Overview

Telegram bot (Go) that bridges Claude Code CLI with Telegram. Messages from Telegram are processed instantly by calling `claude -p` as a subprocess, with full MCP tool access.

## Architecture

```
Telegram → Go Bot → inbox.json → Worker → claude -p (subprocess) → Telegram
                                       ↕
                              Permission flow via inline keyboard
```

- **Worker**: Single goroutine processing messages from a buffered channel. Calls `claude -p` and sends results back to Telegram.
- **Session**: Uses `--continue` flag to maintain conversation context. `ResetSession()` clears it. `--resume <id>` to switch to a previous session. Last 10 sessions tracked.
- **Permissions**: Two-phase execution. First run with default permissions, check `permission_denials` in JSON output, ask user via Telegram inline keyboard, re-run with `--dangerously-skip-permissions` if approved. Markdown fallback on parse errors.
- **Media**: Photos and documents downloaded from Telegram to `/tmp`, file path passed to Claude CLI for multimodal analysis.
- **Single Instance**: PID file (`bot.pid`) ensures only one bot runs; auto-kills previous on start.

## File Responsibilities

| File | Role |
|---|---|
| `main.go` | Entry point, config loading, logger, graceful shutdown, wiring |
| `model.go` | Data types (`InboxMessage`, `OutboxMessage`, `CLIResult`, `PermissionDenial`) |
| `store.go` | JSON file I/O with `sync.Mutex` + lock file for cross-process safety |
| `bot.go` | Telegram update handler, commands (`/help`, `/new`, `/btw`, `/resume`, `/model`, `/cancel`, `/status`, `/clear`, `/retry`), callback queries, outbox poller |
| `claude.go` | Claude CLI executor with `--continue`/`--resume` session management, `--add-dir`, model switching, `--output-format json` parsing |
| `worker.go` | Message queue, two-phase permission flow with detailed tool info, pending poll, stale recovery, cancel support |

## Build & Run

```bash
go build ./...   # build
go vet ./...     # lint
go run .         # run (multi-file, not go run main.go)
```

## Key Design Decisions

### Claude CLI direct invocation
- Replaced Cowork scheduled polling (1 min) with instant `claude -p` subprocess calls
- Response time: seconds instead of up to 1 minute
- Usage: consumed only when messages arrive (no idle cost)

### Two-phase permission flow
- Phase 1: default permissions → detect `permission_denials` in JSON output
- If denied: Telegram inline keyboard `[Allow] [Deny]` with 2-min timeout
- Phase 2 (if approved): re-run with `--dangerously-skip-permissions`
- Secure: only authorized `TELEGRAM_CHAT_ID` can approve

### Session continuity
- `--continue` flag used after first successful execution
- `/new` command resets session via `ClaudeExecutor.ResetSession()`
- Enables natural conversation: "send him a DM" works because Claude remembers "him"

### Flexible outbox parsing
- Handles both `{"messages":[...]}` and `[...]` array formats
- Legacy Cowork compatibility maintained

## Important Notes

- Only ONE bot instance at a time (Telegram Long Polling limitation)
- Never commit `.env`, `inbox.json`, `outbox.json`, `bot.log`, `*.lock`
- `inbox.json` / `outbox.json` may contain personal data (conversations, emails)

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
| `CLAUDE_CLI_PATH` | `claude` | CLI binary path |
| `CLAUDE_WORK_DIR` | `.` | CLI working directory |
| `CLAUDE_TIMEOUT_SECONDS` | `120` | CLI execution timeout |
| `CLAUDE_SYSTEM_PROMPT` | *(none)* | Custom system prompt |
| `CLAUDE_MODEL` | *(none)* | Model override |
| `CLAUDE_ADD_DIRS` | `~` | Extra directories Claude can access (comma-separated) |
| `WORKER_QUEUE_SIZE` | `100` | Worker queue capacity |
