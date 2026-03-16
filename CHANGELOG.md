# Changelog

All notable changes to this project will be documented in this file.

## [1.1.0] - 2026-03-16

### Added
- `/name <text>` command — rename current session (synced to Claude CLI via `--name` flag)
- Session names displayed in `/resume` inline keyboard (falls back to first message if unnamed)
- UTF-8 sanitization on all Telegram messages to prevent API rejection

### Fixed
- Permission approval messages failing with "strings must be encoded in UTF-8"

## [1.0.0] - 2026-03-16

### Changed
- **Project structure**: Migrated to `cmd/internal` Go project layout
  - `cmd/cowork-bot/main.go` — minimal entry point
  - `internal/config` — configuration, logger, PID file
  - `internal/store` — data models, file I/O with locking
  - `internal/bot` — Telegram handlers, commands (3 files), media download
  - `internal/claude` — CLI executor with session management
  - `internal/worker` — message queue, approval flow, retry (2 files)
- **Session management**: Replaced `--continue` with explicit `--resume <session_id>` to prevent conflicts with Claude Code terminal sessions in the same directory
- `/resume` now shows inline keyboard buttons for session selection (max 5)
- `/clear` and `/retry` now handle `failed` and `expired` statuses

### Added
- `MESSAGE_TTL_MINUTES` env var (default 10) — auto-expire old messages
- `expired` status — messages past TTL are skipped and never retried
- `failed` status — messages that exceeded max retries are permanently closed
- Smart error classification: `signal: killed` (restart) retries silently without counting
- Smart stale recovery on startup: respects TTL, marks old messages as expired

### Fixed
- Session conflicts between Telegram bot and Claude Code terminal in same directory
- Stale message retry loops caused by bot restarts killing Claude CLI processes
- Infinite retry cycles for permanently failed messages

## [0.8.0] - 2026-03-16

### Fixed
- Telegram Markdown parsing errors in permission messages (fallback to plain text)
- Duplicate bot instances causing "Conflict: terminated by other getUpdates request"

### Added
- PID file (`bot.pid`) for single instance enforcement; auto-kills previous on start

## [0.7.0] - 2026-03-16

### Added
- Photo and document attachment support via Telegram (multimodal)

## [0.6.0] - 2026-03-16

### Added
- `/btw <note>` command — add context to session without full processing
- `/resume` command — list and switch between previous sessions
- `/model <name>` command — switch Claude model on the fly
- `/cancel` command — cancel currently processing message
- `CLAUDE_ADD_DIRS` env var — access directories outside the project
- Detailed permission messages showing tool inputs (file paths, commands)
- Failure notifications sent to Telegram on timeout/error

## [0.5.0] - 2026-03-16

### Added
- Session continuity with `--continue` flag
- `/new` command to reset session
- `/help` command with formatted command list
- Pretty permission UI with emoji tool icons and Markdown

## [0.4.0] - 2026-03-16

### Added
- Claude Code CLI integration (`claude -p` subprocess)
- Worker pattern with message queue
- Two-phase permission flow with inline keyboard approval
- Direct Telegram send + outbox audit trail

### Changed
- Replaced Cowork 1-minute schedule with instant CLI invocation

## [0.3.0] - 2026-03-16

### Fixed
- "(empty result)" messages, outbox format parsing, multiple bot instance conflicts

## [0.2.0] - 2026-03-16

### Added
- Enterprise refactor: 4-file split, lock file, 5-stage status, retry, structured logging, graceful shutdown, /status /clear /retry commands

## [0.1.0] - 2026-03-16

### Added
- Initial Telegram bot: receive messages → inbox.json, poll outbox.json → send results
