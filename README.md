# Cowork Telegram Bot

A Telegram bot that bridges [Claude Code CLI](https://docs.anthropic.com/en/docs/claude-code) with Telegram, enabling you to interact with Claude from your phone. Send a message on Telegram, and Claude processes it instantly using the `claude -p` CLI — including full access to MCP tools like Slack, Notion, Gmail, and more.

## Features

- **Instant Processing** — Messages are processed immediately via Claude Code CLI (no polling delay)
- **Session Memory** — Conversations persist automatically using explicit `--resume <session_id>` tracking
- **Session Resume** — Switch back to any previous conversation via inline keyboard with `/resume`
- **Context Notes** — Use `/btw` to add context without triggering full processing
- **Interactive Permissions** — When Claude needs elevated access, the bot shows detailed tool info and asks via inline buttons
- **Model Switching** — Change models on the fly with `/model sonnet` or `/model opus`
- **Photo & File Support** — Send photos, screenshots, or documents via Telegram for Claude to analyze (multimodal)
- **MCP Tool Access** — Slack, Notion, Gmail, and any MCP server configured in Claude Code
- **Extended Directory Access** — Access files outside the project via `CLAUDE_ADD_DIRS`
- **Message TTL** — Messages older than 10 minutes auto-expire (configurable), preventing stale retry loops
- **Smart Error Handling** — Restart kills (`signal: killed`) retry silently; real errors notify and retry up to 3x
- **Single Instance Guard** — PID file prevents duplicate bot instances; auto-kills previous on start
- **Audit Trail** — All messages and results logged in `inbox.json` / `outbox.json`
- **Structured Logging** — Logs to both stdout and file with timestamps and levels

## Architecture

```
Telegram (phone) — text, photos, files
    ↕  HTTPS Long Polling
Go Bot (local machine, single instance via PID file)
    ├─ Save to inbox.json (pending)
    ├─ Download attachments to /tmp (if photo/file)
    ├─ Worker → claude -p --resume <session_id> (subprocess)
    │   ├─ Permission denied? → Ask user via inline keyboard
    │   └─ Approved? → Re-execute with --dangerously-skip-permissions
    └─ Send result to Telegram + record in outbox.json
```

## Quick Start

### Prerequisites

- [Go](https://go.dev/) 1.21+
- [Claude Code CLI](https://docs.anthropic.com/en/docs/claude-code) installed and authenticated
- A Telegram bot token from [@BotFather](https://t.me/BotFather)
- Your Telegram chat ID (use [@userinfobot](https://t.me/userinfobot) to find it)

### Installation

```bash
git clone https://github.com/GrapeInTheTree/claude-cowork-telegram.git
cd claude-cowork-telegram
go mod download
```

### Configuration

```bash
cp .env.example .env
```

Edit `.env` with your values:

```env
# Required
TELEGRAM_TOKEN=your-bot-token
TELEGRAM_CHAT_ID=your-chat-id

# Optional — Claude CLI
CLAUDE_CLI_PATH=claude          # Path to claude binary
CLAUDE_TIMEOUT_SECONDS=120      # Max execution time per message
CLAUDE_SYSTEM_PROMPT=           # Custom system prompt (optional)
CLAUDE_MODEL=                   # Model override: sonnet, opus, etc.
```

<details>
<summary>All environment variables</summary>

| Variable | Default | Description |
|---|---|---|
| `TELEGRAM_TOKEN` | *(required)* | Bot token from BotFather |
| `TELEGRAM_CHAT_ID` | *(required)* | Allowed chat ID (all others ignored) |
| `INBOX_PATH` | `./inbox.json` | Incoming message store |
| `OUTBOX_PATH` | `./outbox.json` | Outgoing result store |
| `LOCK_TIMEOUT_MINUTES` | `5` | Stale lock detection threshold |
| `MAX_RETRY_COUNT` | `3` | Max retries for failed messages |
| `OUTBOX_POLL_INTERVAL_SECONDS` | `10` | Outbox polling interval |
| `LOG_FILE` | `./bot.log` | Log file path |
| `MESSAGE_TTL_MINUTES` | `10` | Auto-expire messages older than this |
| `CLAUDE_CLI_PATH` | `claude` | Claude CLI binary path |
| `CLAUDE_WORK_DIR` | `.` | Working directory for CLI |
| `CLAUDE_TIMEOUT_SECONDS` | `120` | CLI execution timeout |
| `CLAUDE_SYSTEM_PROMPT` | *(none)* | Custom system prompt |
| `CLAUDE_MODEL` | *(none)* | Model selection (e.g., `sonnet`) |
| `CLAUDE_ADD_DIRS` | `~` | Extra directories Claude can access (comma-separated) |
| `WORKER_QUEUE_SIZE` | `100` | Processing queue capacity |

</details>

### Build & Run

```bash
# Build and run (recommended)
go build -o cowork-bot ./cmd/cowork-bot/
./cowork-bot

# Or run directly (development only)
go run ./cmd/cowork-bot/
```

> **Note:** Using the built binary is recommended over `go run` for reliable process management. The PID file (`bot.pid`) ensures only one instance runs — restarting automatically kills the previous instance.

### Set up BotFather commands (optional)

Send `/setcommands` to [@BotFather](https://t.me/BotFather) and paste:

```
help - Show available commands
new - Start a new conversation
name - Rename current session
resume - Resume a previous session
btw - Add context note
model - Switch AI model
cancel - Cancel current processing
status - Message queue status
clear - Clean up completed messages
retry - Force retry error messages
```

## Telegram Commands

| Command | Description |
|---|---|
| `/help` | Show available commands |
| `/new` | Start a new conversation (reset session) |
| `/name <text>` | Rename current session (shown in `/resume` list) |
| `/resume` | Select a previous session via inline buttons |
| `/btw <note>` | Add context note without full processing |
| `/model <name>` | Switch model (sonnet, opus, haiku) |
| `/cancel` | Cancel the currently processing message |
| `/status` | Show pending/processing/error message counts |
| `/clear` | Remove completed/failed/expired messages from inbox |
| `/retry` | Force retry error and failed messages (reset retry count) |

## How It Works

### Message Flow

```
1. You send a message (text, photo, or file) on Telegram
2. If photo/file: bot downloads it to a temp file
3. Bot saves message to inbox.json with status "pending"
4. Worker checks TTL — skips if message is older than MESSAGE_TTL_MINUTES
5. Worker calls: claude -p "message" --resume <session_id> --output-format json
6. If permission denied → inline keyboard with tool details [Allow] [Deny]
   - Allow → re-runs with --dangerously-skip-permissions
   - Deny  → returns partial result
7. Result sent to Telegram, status set to "sent"
8. Result also recorded in outbox.json for audit
```

### Message Status Lifecycle

```
pending → processing → sent
    ↓           ↓
 expired      error → (auto-retry up to 3x) → pending
                ↓
             failed → (permanent, /retry to force)
```

| Status | Description |
|---|---|
| `pending` | Waiting to be processed |
| `processing` | Currently being handled by Claude CLI |
| `sent` | Result delivered to Telegram |
| `error` | Processing failed (will auto-retry) |
| `failed` | Max retries exceeded (permanent, use /retry to reset) |
| `expired` | Message TTL exceeded (auto-cleaned) |

### Session Management

Conversations are tracked using explicit `--resume <session_id>`. Each session has its own ID, so the bot never conflicts with other Claude Code instances running in the same directory.

```
You: "Search for Daniel on Slack"
Bot: "Found Daniel (Product - Defi)..."
You: "Send him a DM saying hello"       ← Claude remembers "him" = Daniel
Bot: "DM sent!"
```

Session commands:

- `/new` — Start a fresh conversation when switching topics
- `/name autoresearch 조사` — Label the current session for easy identification
- `/resume` — Show recent sessions as inline buttons (displays `/name` if set), tap to switch
- `/btw working on the API project today` — Add context; Claude remembers it for subsequent messages
- `/model opus` — Switch to a different model mid-conversation

### Permission System

When Claude needs tools that require approval (file writes, bash commands, etc.):

1. Bot detects `permission_denials` in Claude's JSON output
2. Sends you an inline keyboard with detailed tool info:
   ```
   🔐 Permission Required

   • ⚡ Terminal Command
       `gcloud auth login --cred-file=...`
   • 📄 File Write
       `write → /Users/.../config.json`

   💬 Claude: I need to set up authentication...

   Expires in 2 min

   [✅ Allow]  [❌ Deny]
   ```
3. **Allow** → Claude re-executes with full permissions
4. **Deny** → Returns Claude's partial response
5. **Timeout** (2 min) → Auto-denied

### Error Handling

| Error Type | Behavior |
|---|---|
| `signal: killed` (restart) | Silent retry once, no notification |
| Timeout / real error | Notify user, auto-retry up to 3x |
| Max retries exceeded | Mark as `failed`, notify user |
| Message too old (TTL) | Mark as `expired`, skip silently |

## Project Structure

```
claude-cowork-telegram/
├── cmd/
│   └── cowork-bot/
│       └── main.go              # Entry point, wiring, graceful shutdown
├── internal/
│   ├── config/
│   │   └── config.go            # Config loading, logger, PID file
│   ├── store/
│   │   ├── models.go            # Data types, status constants
│   │   └── store.go             # JSON file I/O with mutex and lock file
│   ├── bot/
│   │   ├── bot.go               # Telegram listener, callbacks, outbox poller
│   │   ├── commands.go          # /help /new /btw /resume /model /cancel /status /clear /retry
│   │   └── media.go             # Photo and document download
│   ├── claude/
│   │   └── executor.go          # Claude CLI execution, --resume session tracking
│   └── worker/
│       ├── worker.go            # Message queue, TTL, error classification, retry
│       └── approval.go          # Permission flow, tool formatting, Markdown escape
├── go.mod
├── go.sum
├── .env.example
├── LICENSE
├── CLAUDE.md
└── CHANGELOG.md
```

## Auto-Start on macOS (optional)

Create `~/Library/LaunchAgents/com.cowork.telegram.plist`:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"
  "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>com.cowork.telegram</string>
  <key>ProgramArguments</key>
  <array>
    <string>/path/to/cowork-bot</string>
  </array>
  <key>WorkingDirectory</key>
  <string>/path/to/claude-cowork-telegram</string>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <true/>
  <key>StandardOutPath</key>
  <string>/tmp/cowork-telegram.log</string>
  <key>StandardErrorPath</key>
  <string>/tmp/cowork-telegram.err</string>
</dict>
</plist>
```

```bash
launchctl load ~/Library/LaunchAgents/com.cowork.telegram.plist
```

## Security

- Only messages from `TELEGRAM_CHAT_ID` are processed; all others are silently ignored
- Permission-sensitive tools require explicit approval via Telegram inline buttons
- Sessions use explicit `--resume <id>` — never conflicts with other Claude instances
- `.env` contains secrets — never commit it
- Bot token can be revoked via BotFather at any time

## Limitations

- Requires the host machine to be running (not a cloud service)
- Response time depends on Claude's processing (typically 5-30 seconds)
- Sleep/hibernate will pause the bot
- Each message consumes Claude Plan usage (Pro/Max recommended)
- Session history is in-memory only — lost on bot restart (sessions themselves persist in Claude)

## License

MIT
