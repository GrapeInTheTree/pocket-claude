# Cowork Telegram Bot

A Telegram bot that bridges [Claude Code CLI](https://docs.anthropic.com/en/docs/claude-code) with Telegram, enabling you to interact with Claude from your phone. Send a message on Telegram, and Claude processes it instantly using the `claude -p` CLI — including full access to MCP tools like Slack, Notion, Gmail, and more.

## Features

- **Instant Processing** — Messages are processed immediately via Claude Code CLI (no polling delay)
- **Session Memory** — Conversations persist automatically using `--continue`; use `/new` to start fresh
- **Session Resume** — Switch back to any previous conversation with `/resume`
- **Context Notes** — Use `/btw` to add context without triggering full processing
- **Interactive Permissions** — When Claude needs elevated access (file writes, bash, etc.), the bot asks you with detailed tool info via inline buttons
- **Model Switching** — Change models on the fly with `/model sonnet` or `/model opus`
- **MCP Tool Access** — Slack, Notion, Gmail, and any MCP server configured in Claude Code
- **Extended Directory Access** — Access files outside the project via `CLAUDE_ADD_DIRS`
- **Retry & Recovery** — Automatic retry on failure (max 3), stale message recovery on restart, failure notifications
- **Audit Trail** — All messages and results logged in `inbox.json` / `outbox.json`
- **Structured Logging** — Logs to both stdout and file with timestamps and levels

## Architecture

```
Telegram (phone)
    ↕  HTTPS Long Polling
Go Bot (local machine)
    ├─ Save to inbox.json (pending)
    ├─ Worker → claude -p (subprocess)
    │   ├─ Permission denied? → Ask user via Telegram inline keyboard
    │   └─ Approved? → Re-execute with permissions
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
| `CLAUDE_CLI_PATH` | `claude` | Claude CLI binary path |
| `CLAUDE_WORK_DIR` | `.` | Working directory for CLI |
| `CLAUDE_TIMEOUT_SECONDS` | `120` | CLI execution timeout |
| `CLAUDE_SYSTEM_PROMPT` | *(none)* | Custom system prompt |
| `CLAUDE_MODEL` | *(none)* | Model selection (e.g., `sonnet`) |
| `CLAUDE_ADD_DIRS` | `~` | Extra directories Claude can access (comma-separated) |
| `WORKER_QUEUE_SIZE` | `100` | Processing queue capacity |

</details>

### Run

```bash
# Development
go run .

# Build and run
go build -o cowork-bot
./cowork-bot
```

### Set up BotFather commands (optional)

Send `/setcommands` to [@BotFather](https://t.me/BotFather) and paste:

```
help - Show available commands
new - Start a new conversation
btw - Add context note
resume - Resume a previous session
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
| `/btw <note>` | Add context note without full processing |
| `/resume` | List and resume a previous session |
| `/model <name>` | Switch model (sonnet, opus, haiku) |
| `/cancel` | Cancel the currently processing message |
| `/status` | Show pending/processing/error message counts |
| `/clear` | Remove completed (done/sent) messages from inbox |
| `/retry` | Force retry all error messages (reset retry count) |

## How It Works

### Message Flow

```
1. You send a message on Telegram
2. Bot saves it to inbox.json with status "pending"
3. Worker picks it up, sets status to "processing"
4. Worker calls: claude -p "your message" --output-format json --continue
5. If permission denied → sends inline keyboard [Allow] [Deny]
   - Allow → re-runs with --dangerously-skip-permissions
   - Deny  → returns partial result
6. Result sent to Telegram, status set to "sent"
7. Result also recorded in outbox.json for audit
```

### Message Status Lifecycle

```
pending → processing → sent
                ↓
              error → (auto-retry up to 3x) → pending
                ↓
           [MAX_RETRY] → notification sent
```

### Session Management

By default, conversations are **continued** across messages using Claude's `--continue` flag. This means Claude remembers previous context:

```
You: "Search for Daniel on Slack"
Bot: "Found Daniel (Product - Defi)..."
You: "Send him a DM saying hello"       ← Claude remembers "him" = Daniel
Bot: "DM sent!"
```

Session commands:

- `/new` — Start a fresh conversation when switching topics
- `/btw working on Kayen today` — Add context; Claude remembers it for subsequent messages
- `/resume` — List recent sessions and jump back to any of them
- `/resume 2` — Resume session #2 directly
- `/model opus` — Switch to a different model mid-conversation

### Permission System

When Claude needs tools that require approval (file writes, bash commands, etc.):

1. Bot detects `permission_denials` in Claude's JSON output
2. Sends you an inline keyboard with tool details:
   ```
   🔐 Permission Required

   Claude needs the following tools:
     💬 Slack → Send Message
     📄 File Write

   Allow execution?  (expires in 2 min)

   [✅ Allow]  [❌ Deny]
   ```
3. **Allow** → Claude re-executes with full permissions
4. **Deny** → Returns Claude's partial response
5. **Timeout** (2 min) → Auto-denied

### File Lock Mechanism

Prevents concurrent file access between the bot and external processes:

1. **In-process**: `sync.Mutex` serializes goroutine access
2. **Cross-process**: `inbox.lock` file with PID and timestamp
3. Locks older than 5 minutes are treated as stale and removed

## Project Structure

```
claude-cowork-telegram/
├── main.go       # Entry point, config, logger, graceful shutdown
├── model.go      # Data types and status constants
├── store.go      # JSON file I/O with mutex and lock file
├── bot.go        # Telegram handlers, commands, callbacks, outbox poller
├── claude.go     # Claude CLI executor with session management
├── worker.go     # Message queue, permission flow, retry, recovery
├── go.mod
├── go.sum
├── .env.example  # Environment variable template
├── CLAUDE.md     # Project context for Claude Code
└── CHANGELOG.md  # Version history
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
- Permission-sensitive tools require explicit approval via Telegram
- `.env` contains secrets — never commit it
- Bot token can be revoked via BotFather at any time

## Limitations

- Requires the host machine to be running (not a cloud service)
- Response time depends on Claude's processing (typically 5-30 seconds)
- Sleep/hibernate will pause the bot
- Each message consumes Claude Plan usage (Pro/Max recommended)

## License

MIT
