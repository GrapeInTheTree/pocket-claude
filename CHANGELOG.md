# Changelog

All notable changes to this project will be documented in this file.
Format based on [Keep a Changelog](https://keepachangelog.com/).

## [Unreleased]

### Added
- **Background Tasks** (`/bg`): Run up to 3 concurrent tasks in the background while continuing to chat
  - `/bg <message>` — run in current project
  - `/bg <project> <message>` — run in specific project without switching
  - `/bg status` — show running tasks with elapsed time and slot usage
  - `/bg cancel <id>` — cancel a specific task
  - Each task gets an ephemeral Executor with independent session and approval flow
  - Background approval callbacks routed by `bg_` ID prefix (no collision with foreground `msg_` prefix)
  - Typing indicators per background task
  - Atomic task ID counter prevents millisecond collisions
  - Shutdown-safe: `closed` flag rejects submissions after `CancelAll`, `Wait` ensures goroutine cleanup
  - Completed tasks auto-cleaned after 30 minutes
- **Test Suite**: 52 test cases across 4 packages, all passing with `-race`
  - `store`: CRUD, stats, clear, outbox, message age
  - `claude`: stream JSON parsing, permission denials, UTF-8 truncation
  - `project`: add/remove/switch/rename, background executor, usage tracking, persistence across reloads
  - `worker`: tool summary, error classification, approval helpers, background pool (slots, cancel, cleanup, status, concurrency)
- Typing indicator: "typing..." shown in Telegram while Claude processes
- `/usage` command: shows messages processed, session cost, total cost
- Queue notifications: "Queued (#N)" when worker is busy with another request
- `CLIResult` extended with `TotalCostUSD`, `DurationMs`, `NumTurns` fields
- `ProjectManager.NewBackgroundExecutor()`: creates independent executor not stored in manager's map
- `ProjectManager.TrackUsageForProject()`: records cost for specific (possibly non-active) project
- `ProjectManager.HasProject()`: existence check for `/bg` argument parsing
- `/status` now shows background task count when active

### Fixed
- **UTF-8 Truncation**: `Truncate()`, `safeTruncate()`, and `truncate()` now operate on runes instead of bytes — Korean, emoji, and CJK text no longer produces invalid UTF-8 when truncated
- Trailing comma in `strings.Join()` call in `buildToolSummary`

### Changed
- Renamed project from `claude-cowork-telegram` to `pocket-claude`
- Go module path: `github.com/GrapeInTheTree/pocket-claude`
- Binary: `pocket-claude`
- Default `MAX_RETRY_COUNT`: 3 → 2
- Default `CLAUDE_TIMEOUT_SECONDS`: 120 → 600 (10 min)
- `Worker.Stop()` now cancels and waits for background tasks before stopping
- Approval callback routing in `bot.go` now checks `bg_` prefix for background task approvals

## [1.1.0] - 2026-03-16

### Added
- `/name <text>` command — rename current session (synced to Claude CLI via `--name` flag)
- Session names displayed in `/resume` inline keyboard
- UTF-8 sanitization on all Telegram messages

### Fixed
- Permission approval messages failing with "strings must be encoded in UTF-8"
- `/cancel` causing zombie retry loops (now marks as `failed` permanently)

### Security
- Bounded goroutine spawning with semaphore (max 10 concurrent handlers)
- HTTP status code validation on file downloads
- Safe string slice bounds checking (prevent panics)
- All Telegram API errors now logged (5 previously silent instances)
- Type assertion safety on approval channels
- Temp file cleanup on download failure

## [1.0.0] - 2026-03-16

### Changed
- **Project structure**: Migrated to `cmd/internal` Go project layout
- **Session management**: Replaced `--continue` with explicit `--resume <session_id>` (prevents conflicts with Claude Code terminal)
- `/resume` uses inline keyboard buttons for session selection (max 5)
- `/clear` and `/retry` handle `failed` and `expired` statuses

### Added
- `MESSAGE_TTL_MINUTES` env var (default 10) — auto-expire old messages
- `expired` and `failed` statuses for permanent message termination
- Smart error classification: `signal: killed` retries silently
- Smart stale recovery on startup: respects TTL

### Fixed
- Session conflicts between Telegram bot and Claude Code terminal in same directory
- Stale message retry loops from bot restarts
- Infinite retry cycles for permanently failed messages

## [0.8.0] - 2026-03-16

### Fixed
- Telegram Markdown parsing errors (fallback to plain text)
- Duplicate bot instances ("Conflict: terminated by other getUpdates request")

### Added
- PID file (`bot.pid`) for single instance enforcement

## [0.7.0] - 2026-03-16

### Added
- Photo and document attachment support (multimodal analysis)

## [0.6.0] - 2026-03-16

### Added
- `/btw`, `/resume`, `/model`, `/cancel` commands
- `CLAUDE_ADD_DIRS` for extended directory access
- Detailed permission messages with tool inputs
- Failure notifications on timeout/error

## [0.5.0] - 2026-03-16

### Added
- Session continuity (`--continue`), `/new`, `/help` commands
- Pretty permission UI with emoji tool icons

## [0.4.0] - 2026-03-16

### Added
- Claude Code CLI integration (`claude -p` subprocess)
- Worker pattern with message queue
- Two-phase permission flow

### Changed
- Replaced Cowork 1-minute schedule with instant CLI invocation

## [0.3.0] - 2026-03-16

### Fixed
- "(empty result)" messages, outbox format parsing, multiple instance conflicts

## [0.2.0] - 2026-03-16

### Added
- Enterprise refactor: lock file, 5-stage status, retry, structured logging, graceful shutdown

## [0.1.0] - 2026-03-16

### Added
- Initial Telegram bot: receive messages to inbox.json, poll outbox.json for results
