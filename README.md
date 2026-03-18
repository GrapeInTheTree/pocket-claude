<p align="center">
  <h1 align="center">Pocket Claude</h1>
  <p align="center">
    <strong>Your personal AI assistant, right in your pocket.</strong>
  </p>
  <p align="center">
    A Telegram bot that bridges Claude Code CLI with your phone — send messages, photos, or files and get instant AI-powered responses with full MCP tool access.
  </p>
</p>

---

## Why Pocket Claude?

Most Claude Telegram bots are simple API wrappers. Pocket Claude is different — it runs **Claude Code CLI** as a subprocess, giving you the full power of Claude Code from your phone:

- **MCP Tools** — Slack, Notion, Gmail, and any configured MCP server
- **File Operations** — Read, write, edit files on your machine
- **Shell Commands** — Execute terminal commands with your approval
- **Multimodal** — Send photos and documents for analysis
- **Session Memory** — Conversations persist across messages

Think of it as SSH-ing into Claude Code, but through Telegram.

## Features

- **Instant Processing** — Messages processed immediately via `claude -p` (no polling delay)
- **Session Management** — Conversations persist via explicit `--resume <session_id>` tracking
- **Session Resume** — Switch between previous conversations via inline keyboard (`/resume`)
- **Session Naming** — Label sessions for easy identification (`/name`)
- **Context Notes** — Add context without triggering full processing (`/btw`)
- **Interactive Permissions** — Approve/deny tool access via inline buttons with detailed tool info
- **Model Switching** — Change models on the fly (`/model sonnet`, `/model opus`)
- **Photo & File Support** — Send photos, screenshots, or documents for Claude to analyze
- **Extended Directory Access** — Access files outside the project via `CLAUDE_ADD_DIRS`
- **Message TTL** — Auto-expire stale messages (default 10 min), preventing retry loops
- **Smart Error Handling** — Restart kills retry silently; real errors notify and retry up to 3x
- **Single Instance Guard** — PID file prevents duplicate instances; auto-kills previous on start
- **Typing Indicator** — "typing..." shown in Telegram while Claude processes
- **Multi-Project Support** — Switch between repos at runtime via `/project`, search for git repos with `/project search`, each with isolated sessions and cost tracking
- **Background Tasks** — Run long tasks in parallel via `/bg` while continuing to chat. Up to 3 concurrent slots with independent sessions, permissions, and cost tracking
- **Usage Tracking** — Per-project messages and API-equivalent cost via `/usage` and `/project info`
- **Queue Notifications** — "Queued (#N)" when worker is busy with another request
- **Structured Logging** — Logs to both stdout and file with timestamps and levels

## Architecture

```
Telegram (phone) — text, photos, files
    |  HTTPS Long Polling
    v
Go Bot (local machine, single instance via PID file)
    |-- Save to inbox.json (pending)
    |-- Download attachments to /tmp (if photo/file)
    |-- Worker --> ProjectManager --> claude -p --resume <session_id> (subprocess)
    |       |          |-- projects["default"]  → Executor (workDir: "./")
    |       |          |-- projects["my-app"]   → Executor (workDir: "/path/to/my-app")
    |       |          '-- projects["api"]      → Executor (workDir: "/path/to/api")
    |       |-- Permission denied? --> Inline keyboard [Allow] [Deny]
    |       '-- Approved? --> Re-execute with --dangerously-skip-permissions
    |-- Background Pool (up to 3 concurrent)
    |       |-- /bg <task> --> ephemeral Executor --> independent session
    |       '-- Separate approval flow (bg_ prefix routing)
    '-- Send result to Telegram + record in outbox.json (audit)
```

## Quick Start

### Prerequisites

- [Go](https://go.dev/) 1.23+
- [Claude Code CLI](https://docs.anthropic.com/en/docs/claude-code) installed and authenticated
- A Telegram bot token from [@BotFather](https://t.me/BotFather)
- Your Telegram chat ID (use [@userinfobot](https://t.me/userinfobot) to find it)

### Install

```bash
git clone https://github.com/GrapeInTheTree/pocket-claude.git
cd pocket-claude
go mod download
```

### Configure

```bash
cp .env.example .env
# Edit .env with your Telegram token and chat ID
```

<details>
<summary>Environment Variables</summary>

| Variable | Default | Description |
|---|---|---|
| `TELEGRAM_TOKEN` | *(required)* | Bot token from BotFather |
| `TELEGRAM_CHAT_ID` | *(required)* | Your chat ID (all others ignored) |
| `INBOX_PATH` | `./inbox.json` | Incoming message store |
| `OUTBOX_PATH` | `./outbox.json` | Outgoing result store |
| `LOCK_TIMEOUT_MINUTES` | `5` | Stale lock detection threshold |
| `MAX_RETRY_COUNT` | `2` | Max retries for failed messages |
| `OUTBOX_POLL_INTERVAL_SECONDS` | `10` | Outbox polling interval |
| `LOG_FILE` | `./bot.log` | Log file path |
| `MESSAGE_TTL_MINUTES` | `10` | Auto-expire messages older than this |
| `CLAUDE_CLI_PATH` | `claude` | Claude CLI binary path |
| `CLAUDE_WORK_DIR` | `.` | Working directory for CLI |
| `CLAUDE_TIMEOUT_SECONDS` | `600` | CLI execution timeout (10 min) |
| `CLAUDE_SYSTEM_PROMPT` | *(none)* | Custom system prompt |
| `CLAUDE_MODEL` | *(none)* | Model override (e.g., `sonnet`, `opus`) |
| `CLAUDE_ADD_DIRS` | `~` | Extra directories Claude can access |
| `WORKER_QUEUE_SIZE` | `100` | Processing queue capacity |
| `PROJECTS_FILE` | `./projects.json` | Project persistence file |

</details>

### Build & Run

```bash
go build -o pocket-claude ./cmd/pocket-claude/
./pocket-claude
```

### Test

```bash
make test              # run all tests (57 cases)
make test-race         # with race detector
make ci                # full CI pipeline locally (fmt + vet + build + test)
```

Or directly:
```bash
go test ./...              # run tests
go test -race ./...        # with race detector
go vet ./...               # static analysis
gofmt -l .                 # check formatting
```

> The PID file (`bot.pid`) ensures only one instance runs at a time. Restarting automatically kills the previous instance.

### BotFather Commands (optional)

Send `/setcommands` to [@BotFather](https://t.me/BotFather):

```
help - Show available commands
new - Start a new conversation
name - Rename current session
resume - Resume a previous session
btw - Add context note
model - Switch AI model
project - Switch, search, or manage projects
bg - Run background tasks
cancel - Cancel current processing
usage - Token cost tracking
status - Message queue status
clear - Clean up completed messages
retry - Force retry error messages
```

## Commands

| Command | Description |
|---|---|
| `/help` | Show available commands |
| `/new` | Start a new conversation (reset session) |
| `/name <text>` | Rename current session (shown in `/resume`) |
| `/resume` | Select a previous session via inline buttons |
| `/btw <note>` | Add context note without full processing |
| `/model <name>` | Switch model (sonnet, opus, haiku) |
| `/project` | Switch project via inline buttons |
| `/project info` | Current project details + usage |
| `/project add <name> <path>` | Add a new project (validates path) |
| `/project search <keyword>` | Search git repos and add via buttons |
| `/project rename <old> <new>` | Rename a project |
| `/project remove <name>` | Remove a project |
| `/bg <message>` | Run task in background (current project) |
| `/bg <project> <message>` | Run background task in specific project |
| `/bg status` | Show running background tasks |
| `/bg cancel <id>` | Cancel a background task |
| `/cancel` | Cancel the currently processing foreground message |
| `/usage` | Show API-equivalent cost and message count (per project) |
| `/status` | Show message queue status (+ background task count) |
| `/clear` | Remove completed/failed/expired messages |
| `/retry` | Force retry error and failed messages |

## How It Works

### Message Flow

```
1. You send a message (text, photo, or file) on Telegram
2. If photo/file: bot downloads attachment to a temp file
3. Bot saves message to inbox.json with status "pending"
4. Worker checks TTL — skips if older than MESSAGE_TTL_MINUTES
5. Worker calls: claude -p "message" --resume <session_id> --output-format json
6. If permission denied:
   - Bot shows inline keyboard with tool details
   - [Allow] → re-execute with --dangerously-skip-permissions
   - [Deny] → return partial result
7. Result sent to Telegram + recorded in outbox.json
```

### Message Lifecycle

```
pending --> processing --> sent
    |            |
 expired       error --> (auto-retry up to 2x) --> pending
                 |
              failed --> (permanent, /retry to reset)
```

| Status | Description |
|---|---|
| `pending` | Waiting to be processed |
| `processing` | Claude CLI is running |
| `sent` | Result delivered to Telegram |
| `error` | Failed, will auto-retry |
| `failed` | Max retries exceeded (use `/retry` to reset) |
| `expired` | TTL exceeded, skipped |

### Session Management

Sessions are tracked using explicit `--resume <session_id>`, ensuring the bot never conflicts with other Claude Code instances in the same directory.

```
You: "Search for Daniel on Slack"
Bot: "Found Daniel (Product - Defi)..."
You: "Send him a DM saying hello"       <-- Claude remembers "him" = Daniel
Bot: "DM sent!"

/name slack-daniel                       <-- Label this session
/new                                     <-- Start fresh
/resume                                  <-- See all sessions, tap to switch
```

### Multi-Project Support

Work across multiple repositories without restarting the bot. Each project gets its own sessions, working directory, and cost tracking.

**Search and add repos:**
```
/project search my-app                    <-- Scans ~/... for git repos
  🔍 Found 2 repo(s) matching "my-app"
  Tap to add as project:
  [+ my-app  (~/projects/my-app)]
  [+ my-app-v2  (~/work/my-app-v2)]       <-- Tap to add instantly
```

**Or add manually:**
```
/project add api /Users/me/api-server     <-- Path is validated
```

**Switch between projects:**
```
/project                                  <-- Inline keyboard
  📂 Projects (2)
  Active: default
  [▶ default  (.)]
  [   my-app  (~/projects/my-app)]

/project my-app                           <-- Or direct switch by name
You: "Run the tests"                      <-- Executes in my-app's directory
/project default                          <-- Switch back, session preserved
/usage                                    <-- Shows cost for active project
```

**Other project commands:**
```
/project info                             <-- Current project details + usage
/project rename my-app frontend           <-- Rename without re-adding
/project remove api                       <-- Remove (can't remove active/default)
```

Projects persist to `projects.json` and survive bot restarts.

### Background Tasks

Run long-running tasks in the background while continuing to chat. Each background task gets its own ephemeral Executor with an independent session, so there's no interference with your foreground conversation.

```
/bg analyze the entire codebase and find security issues
  🔄 Background task started
  🆔 bg_1710756000123
  📂 Project: my-app
  💬 analyze the entire codebase and find security issues

  Use /bg status to check progress.
```

You can keep chatting normally while it runs:
```
You: "What's in the README?"               <-- processed immediately
Bot: "The README contains..."

/bg status
  🔄 Background Tasks
  🔄 bg_1710756000123 [running] analyze the entire codebas...
     📂 my-app | ⏱ 1m23s
  Slots: 1/3
```

Run tasks in other projects without switching:
```
/bg api-server run all integration tests   <-- runs in api-server project
```

When a background task finishes:
```
✅ Background Task Done
📂 Project: my-app
🆔 bg_1710756000123
📋 📖Read ×12  🔎Grep ×8  ⚡Bash ×3 | 95s | $0.0847
⏱ 1m35s

Found 3 potential security issues...
```

Background tasks have their own permission flow — if a background task needs approval, you'll see a separate inline keyboard tagged with the task ID, so it won't interfere with foreground approvals.

### Permission System

When Claude needs tools that require approval:

```
+------------------------------------------+
|  Permission Required                      |
|                                          |
|  * Terminal Command                       |
|      gcloud auth login --cred-file=...   |
|  * File Write                            |
|      write -> /Users/.../config.json     |
|                                          |
|  Claude: I need to set up auth...        |
|                                          |
|  Expires in 2 min                        |
|                                          |
|  [  Allow  ]  [  Deny  ]                |
+------------------------------------------+
```

### Error Handling

| Error Type | Behavior |
|---|---|
| `signal: killed` (restart) | Silent retry, no notification |
| `/cancel` | Mark as `failed`, no retry |
| Timeout / CLI error | Notify user, auto-retry up to 2x |
| Max retries exceeded | Mark as `failed`, notify user |
| Message too old (TTL) | Mark as `expired`, skip silently |

## Project Structure

```
pocket-claude/
+-- cmd/
|   +-- pocket-claude/
|       +-- main.go              # Entry point, wiring, graceful shutdown
+-- internal/
|   +-- config/
|   |   +-- config.go            # Config loading, logger, PID file
|   +-- store/
|   |   +-- models.go            # Data types, 7 status constants
|   |   +-- store.go             # JSON file I/O, mutex, lock file
|   +-- bot/
|   |   +-- bot.go               # Telegram listener, callbacks, outbox poller
|   |   +-- commands.go          # 12 commands with inline keyboards
|   |   +-- media.go             # Photo/document download
|   +-- claude/
|   |   +-- executor.go          # CLI execution, --resume session tracking
|   +-- project/
|   |   +-- types.go             # ProjectConfig, ProjectsFile, ProjectUsage types
|   |   +-- manager.go           # Multi-project executor routing, persistence
|   +-- worker/
|       +-- worker.go            # Message queue, TTL, error classification
|       +-- approval.go          # Permission flow, tool name formatting
|       +-- background.go        # Background task pool (3 concurrent slots)
+-- .github/
|   +-- workflows/
|       +-- ci.yml               # GitHub Actions: build, vet, fmt, test -race
+-- Makefile                     # make build/test/ci/fmt/run
+-- .env.example
+-- LICENSE                      # MIT
+-- CLAUDE.md                    # Project context for Claude Code
+-- CHANGELOG.md                 # Version history
```

## Auto-Start on macOS

Create `~/Library/LaunchAgents/com.pocket-claude.plist`:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"
  "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>com.pocket-claude</string>
  <key>ProgramArguments</key>
  <array>
    <string>/path/to/pocket-claude</string>
  </array>
  <key>WorkingDirectory</key>
  <string>/path/to/pocket-claude</string>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <true/>
  <key>StandardOutPath</key>
  <string>/tmp/pocket-claude.log</string>
  <key>StandardErrorPath</key>
  <string>/tmp/pocket-claude.err</string>
</dict>
</plist>
```

```bash
launchctl load ~/Library/LaunchAgents/com.pocket-claude.plist
```

## Security

- Only messages from your `TELEGRAM_CHAT_ID` are processed; all others are silently ignored
- Permission-sensitive tools require explicit approval via inline buttons
- Sessions use explicit `--resume <id>` — never conflicts with other Claude instances
- Goroutine spawning is bounded by semaphore (max 10 concurrent handlers)
- All Telegram messages are UTF-8 sanitized before sending
- `.env` contains secrets — never commit it
- Bot token can be revoked instantly via BotFather

## Limitations

- Requires the host machine to be running (macOS sleep will pause the bot)
- Response time depends on Claude's processing (typically 5-30 seconds)
- Session history is in-memory — session list resets on bot restart (sessions themselves persist in Claude)
- Background tasks are in-memory — lost on bot restart (max 3 concurrent)
- Each message consumes Claude Plan usage (Pro/Max recommended)
- Single user only (`TELEGRAM_CHAT_ID` supports one ID)

## Contributing

Contributions are welcome! Please open an issue or submit a pull request.

## License

[MIT](LICENSE)
