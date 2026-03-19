# Changelog

All notable changes to this project will be documented in this file.
Format based on [Keep a Changelog](https://keepachangelog.com/).

## [Unreleased]

### Added
- **Research** (`/research`, formerly `/bg`): Read-only background analysis restricted to safe tools (Read, Glob, Grep, WebSearch, WebFetch)
  - `/research <message>` — analyze in background (read-only, no file modifications)
  - `/research <project> <message>` — in specific project
  - `/research inject <id>` — merge results into main session as context note
  - `/research status` / `cancel <id>`
  - Atomic task ID counter, typing indicators, 30-minute auto-cleanup
- **Ralph — Iterative Autonomous Loop** (`/ralph`): Claude repeats a task across multiple iterations in an isolated git worktree
  - `/ralph <message>` — start auto-loop in worktree (default 5 iterations)
  - `/ralph <message> --max <N>` — set max iterations (1-20)
  - `/ralph status` / `cancel <id>`
  - Auto-creates git worktree on first iteration via `--worktree` flag; branch persists after completion
  - Completion detection: no tool usage or `RALPH_DONE` signal
  - Safety: stall detection (3 iterations without progress), cost limit ($1.00 default), max iteration cap
  - Progress updates with iteration count, cost, and branch info
- **Plan Mode** (`/plan`): Claude analyzes and plans in the main session without executing. User reviews, modifies naturally via conversation, then says "execute" when ready
- **GitHub Actions CI** (`.github/workflows/ci.yml`): automated build, vet, gofmt check, test with race detector on push/PR to main
- **Makefile**: `make build`, `make test`, `make test-race`, `make vet`, `make fmt`, `make fmt-check`, `make ci` (full local pipeline), `make run`, `make clean`
- **Test Suite**: 65 test cases across 6 packages, all passing with `-race`
  - `store`: CRUD, stats, clear, outbox, message age
  - `claude`: stream JSON parsing, permission denials, UTF-8 truncation
  - `project`: add/remove/switch/rename, background executor, usage tracking, persistence across reloads
  - `worker`: tool summary, error classification, approval helpers, background pool (slots, cancel, cleanup, status, concurrency)
  - `bot`: safeTruncate UTF-8 safety
  - `config`: env helpers, PID file creation
- Typing indicator: "typing..." shown in Telegram while Claude processes
- `/usage` command: shows messages processed, session cost, total cost
- Queue notifications: "Queued (#N)" when worker is busy with another request
- `CLIResult` extended with `TotalCostUSD`, `DurationMs`, `NumTurns` fields
- `ProjectManager.NewBackgroundExecutor()`: creates independent executor not stored in manager's map
- `ProjectManager.TrackUsageForProject()`: records cost for specific (possibly non-active) project
- `ProjectManager.HasProject()`: existence check for `/bg` argument parsing
- `/status` now shows background task count when active
- `/project` now shows available subcommands (info, add, search, rename, remove) alongside the project keyboard

### Fixed
- **Long message auto-split**: Messages exceeding Telegram's 4096-char limit now automatically split into multiple messages. Prefers newline boundaries for natural breaks
- **Callback auth**: Chat ID validation added to callback handler — prevents unauthorized permission approvals
- **FormatToolName MCP parsing**: Correctly extracts service name from namespace (e.g., "claude\_ai\_Slack" → "Slack"). Slack/Notion icons now display properly
- **Message ID collisions**: Foreground message IDs now use atomic counter (same as background tasks)
- **UTF-8 Truncation**: `Truncate()`, `safeTruncate()`, and `truncate()` now operate on runes instead of bytes
- Trailing comma in `strings.Join()` call in `buildToolSummary`

### Changed
- **`/bg` renamed to `/research`**: Read-only tools only (Read, Glob, Grep, WebSearch, WebFetch). File modifications require `/ralph` instead
- **`/ralph` now uses git worktree**: All iterations run in an isolated branch via `--worktree` flag. Main codebase protected
- `interface{}` → `any` (Go 1.18+ canonical alias)
- `.gitignore` expanded: `.claude/`, `.DS_Store`, organized sections

- Renamed project from `claude-cowork-telegram` to `pocket-claude`
- Go module path: `github.com/GrapeInTheTree/pocket-claude`
- Binary: `pocket-claude`
- Default `CLAUDE_TIMEOUT_SECONDS`: 120 → 1200 (20 min)
- `Worker.Stop()` now cancels and waits for background tasks before stopping
- Approval callback routing checks `bg_` prefix for background task approvals

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
