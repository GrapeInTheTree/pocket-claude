.PHONY: build test test-race vet fmt fmt-check clean run

# Build
build:
	go build -o pocket-claude ./cmd/pocket-claude/

# Test
test:
	go test ./...

test-race:
	go test -race -count=1 ./...

test-verbose:
	go test -v ./...

# Code quality
vet:
	go vet ./...

fmt:
	gofmt -w .

fmt-check:
	@unformatted=$$(gofmt -l .); \
	if [ -n "$$unformatted" ]; then \
		echo "Unformatted files:"; \
		echo "$$unformatted"; \
		echo "Run 'make fmt' to fix."; \
		exit 1; \
	fi

# All checks (mirrors CI)
ci: fmt-check vet build test-race

# Run
run: build
	./pocket-claude

# Clean
clean:
	rm -f pocket-claude
