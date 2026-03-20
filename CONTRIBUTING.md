# Contributing to Pocket Claude

Thanks for your interest! Here's how to get started.

## Development Setup

```bash
git clone https://github.com/GrapeInTheTree/pocket-claude.git
cd pocket-claude
cp .env.example .env    # fill in your Telegram token and chat ID
go mod download
```

## Workflow

1. Fork the repo
2. Create a branch (`git checkout -b feature/my-feature`)
3. Make changes
4. Run checks: `make ci` (fmt + vet + build + test with race detector)
5. Commit with a clear message
6. Open a pull request

## Code Style

- Follow standard Go conventions (`gofmt`, `go vet`)
- Keep functions short and focused
- Add tests for new functionality
- Use `any` instead of `interface{}`

## Project Structure

```
cmd/pocket-claude/     Entry point
internal/
  bot/                 Telegram listener, commands
  claude/              CLI execution, session tracking
  config/              Environment config, logger
  project/             Multi-project management
  store/               JSON persistence
  worker/              Message queue, background pool, ralph loop
```

## Running Tests

```bash
make test        # all tests
make test-race   # with race detector
make ci          # full pipeline (fmt + vet + build + test)
```

## Reporting Issues

- Bug reports: include logs (`bot.log`) with sensitive data redacted
- Feature requests: describe the use case, not just the solution

## License

By contributing, you agree that your contributions will be licensed under the MIT License.
