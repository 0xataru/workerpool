VERSION_FILE := VERSION
CURRENT := $(shell tr -d '[:space:]' < $(VERSION_FILE) 2>/dev/null)
STATICCHECK := honnef.co/go/tools/cmd/staticcheck@2025.1.1

.DEFAULT_GOAL := help

.PHONY: help fmt fmt-check vet lint test stress fuzz bench cover build check bump tag clean

help: ## Show available commands
	@echo "workerpool — v$(CURRENT)"
	@echo ""
	@grep -E '^[a-zA-Z0-9_-]+:.*##' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*## "}; {printf "  \033[36m%-11s\033[0m %s\n", $$1, $$2}'

fmt: ## Format the tree
	gofmt -w .

fmt-check: ## Fail if anything is unformatted (what CI does)
	@unformatted=$$(gofmt -l .); \
	if [ -n "$$unformatted" ]; then echo "not gofmt'd:"; echo "$$unformatted"; exit 1; fi

vet: ## Run go vet
	go vet ./...

lint: ## Run staticcheck (fetched on demand, never a module dependency)
	go run $(STATICCHECK) ./...

test: ## Run the suite with the race detector
	go test -race -count=1 ./...

stress: ## Deep randomised run; a failure prints a replayable seed
	go test -race -count=20 -timeout 30m -run Stress ./...

# go test accepts only one -fuzz target per run, so they go one after another.
fuzz: ## Fuzz Map and Stream (FUZZTIME=1m each); failures land in testdata/fuzz
	go test -run '^$$' -fuzz '^FuzzMap$$' -fuzztime $(or $(FUZZTIME),1m) .
	go test -run '^$$' -fuzz '^FuzzStream$$' -fuzztime $(or $(FUZZTIME),1m) .

bench: ## Benchmarks — never with -race, it distorts channel timings
	go test -run '^$$' -bench . -benchmem ./...

# The library only, not ./...: the examples are package main with no tests, and
# counting them as 0% would report a number that says nothing about the package.
cover: ## Coverage of the library package
	@go test -race -count=1 -coverprofile=coverage.out . >/dev/null
	@go tool cover -func=coverage.out
	@echo "html: go tool cover -html=coverage.out"

build: ## Build the package and the examples
	go build ./...

check: fmt-check vet lint test ## Everything CI runs, in one command
	@echo "ok"

bump: ## Release bump (VERSION=X.Y.Z) — syncs VERSION, CHANGELOG, README badge
	@test -n "$(VERSION)" || (echo "Usage: make bump VERSION=0.2.0" && exit 1)
	@./scripts/bump.sh $(VERSION)

# Tags whatever VERSION currently says. The bump commit may or may not already be
# in history: if the release files are still dirty they get committed here, and if
# they were committed by hand the tag simply lands on HEAD. What is never allowed
# is tagging a tree that does not match the commit — a published Go tag is
# immutable, so it has to point at exactly what was tested.
tag: check ## Create the vX.Y.Z tag from VERSION (does not push)
	@v=$$(tr -d '[:space:]' < $(VERSION_FILE)); \
	git rev-parse HEAD >/dev/null 2>&1 || { echo "no commits yet: commit the source first"; exit 1; }; \
	if git rev-parse -q --verify "refs/tags/v$$v" >/dev/null 2>&1; then \
		echo "tag v$$v already exists — bump first, or delete the tag if it was never pushed"; exit 1; \
	fi; \
	other=$$(git status --porcelain -- . ':!$(VERSION_FILE)' ':!CHANGELOG.md' ':!README.md'); \
	if [ -n "$$other" ]; then \
		echo "uncommitted changes outside the release files:"; echo "$$other"; \
		echo "commit or stash them first — the tag must match what was tested"; exit 1; \
	fi; \
	if [ -n "$$(git status --porcelain -- $(VERSION_FILE) CHANGELOG.md README.md)" ]; then \
		git add $(VERSION_FILE) CHANGELOG.md README.md && git commit -m "release v$$v" || exit 1; \
	else \
		echo "release files already committed, tagging HEAD"; \
	fi; \
	git tag -a "v$$v" -m "v$$v" && \
	echo && \
	echo "Tagged v$$v at $$(git rev-parse --short HEAD). Publish with:" && \
	echo "  git push --follow-tags"

clean: ## Remove build and coverage artefacts
	rm -f coverage.out
	go clean ./...
