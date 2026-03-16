# Changelog

All notable changes to this project will be documented in this file.

## [0.8.0] - 2026-03-16

### Fixed
- Telegram Markdown parsing errors in permission messages (fallback to plain text)
- Duplicate bot instances causing "Conflict: terminated by other getUpdates request"

### Added
- PID file (`bot.pid`) for single instance enforcement; auto-kills previous on start
- `cowork-bot` binary build target for reliable process management

## [0.7.0] - 2026-03-16

### Added
- Photo and document attachment support — send images or files via Telegram for Claude to analyze
- Telegram photos downloaded to temp files and passed to Claude CLI for multimodal processing
- Caption support: use photo caption as prompt, defaults to "Analyze this image"

## [0.6.0] - 2026-03-16

### Added
- `/btw <note>` command — add context to the session without triggering full processing
- `/resume` command — list recent sessions and switch back to any previous conversation
- `/model <name>` command — switch Claude model on the fly (sonnet, opus, haiku)
- `/cancel` command — cancel the currently processing message
- `CLAUDE_ADD_DIRS` env var — allow Claude CLI to access directories outside the project (uses `--add-dir`)
- Detailed permission request messages showing tool inputs (file paths, bash commands, etc.)
- Claude's explanation included in permission request messages
- Failure notifications sent to Telegram on timeout/error with retry guidance
- Session tracking: last 10 sessions stored with ID, first message, and timestamp

### Changed
- Permission UI now shows specific details per tool (e.g., `write → /path/to/file`, `ls -la`)
- Error handling sends structured Markdown notifications to Telegram

## [0.5.0] - 2026-03-16

### Added
- Session continuity with `--continue` flag (conversations persist across messages)
- `/new` command to reset session and start a fresh conversation
- `/help` command with formatted command list and usage guide
- Unknown command handler with `/help` suggestion
- Pretty permission request messages with emoji tool icons and Markdown formatting
- Tool name formatter: maps internal names to readable labels (e.g., `mcp__claude_ai_Slack__slack_send_message` → `💬 Slack → Send Message`)

### Changed
- All Telegram UI messages use Markdown formatting
- Permission buttons styled with emoji: `✅ Allow` / `❌ Deny`
- Approval/denial callback messages updated with bold status text

## [0.4.0] - 2026-03-16

### Added
- Claude Code CLI integration (`claude -p` subprocess invocation)
- Worker pattern: message queue + single goroutine sequential processing
- `claude.go`: CLI executor with timeout, model selection, system prompt support
- `worker.go`: queue management, in-flight dedup (`sync.Map`), pending poll, stale recovery
- Two-phase permission flow: detect `permission_denials` → Telegram inline keyboard → re-execute with approval
- Direct Telegram send + outbox audit trail
- Stale message recovery on startup ("processing" → "pending")
- Environment variables: `CLAUDE_CLI_PATH`, `CLAUDE_WORK_DIR`, `CLAUDE_TIMEOUT_SECONDS`, `CLAUDE_SYSTEM_PROMPT`, `CLAUDE_MODEL`, `WORKER_QUEUE_SIZE`

### Changed
- Replaced Cowork 1-minute schedule with instant CLI invocation (response in seconds)
- Result delivery: direct Telegram send with outbox fallback on failure

## [0.3.0] - 2026-03-16

### Fixed
- "(empty result)" messages sent to Telegram
- `outbox.json` array format parsing failure (Cowork compatibility)
- Multiple bot instance conflict

### Removed
- `PollInboxDone` — eliminated duplicate sends from inbox/outbox concurrent polling

### Changed
- Outbox messages with empty result are skipped instead of sending "(empty result)"

## [0.2.0] - 2026-03-16

### Added
- Refactored into 4 files: `main.go`, `model.go`, `store.go`, `bot.go`
- Lock file mechanism (`inbox.lock` with PID/timestamp, stale detection)
- 5-stage message status: pending → processing → done → sent → error
- Auto-retry logic (max 3 attempts, Telegram notification on exhaustion)
- Dual concurrency protection: `sync.Mutex` + lock file
- Structured logging with `log/slog` (stdout + `bot.log`)
- Graceful shutdown on SIGINT/SIGTERM
- Telegram commands: `/status`, `/clear`, `/retry`
- New environment variables: `LOCK_TIMEOUT_MINUTES`, `MAX_RETRY_COUNT`, `OUTBOX_POLL_INTERVAL_SECONDS`, `LOG_FILE`

### Changed
- Go module path set to `github.com/GrapeInTheTree/claude-cowork-telegram`
- Message IDs use `UnixMilli` for collision prevention
- `inbox.json` schema extended with `retry_count`, `last_error`, `telegram_message_id`

## [0.1.0] - 2026-03-16

### Added
- Initial Telegram bot implementation
- Receive messages → save to `inbox.json` (pending)
- Poll `outbox.json` every 10 seconds → send done items to Telegram → mark sent
- `TELEGRAM_CHAT_ID` filtering (ignore unauthorized chats)
- `.env` file loading via godotenv
- `.env.example` template
