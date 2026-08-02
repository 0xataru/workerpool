VERSION_FILE := VERSION
CURRENT := $(shell tr -d '[:space:]' < $(VERSION_FILE) 2>/dev/null)
STATICCHECK := honnef.co/go/tools/cmd/staticcheck@2025.1.1

.DEFAULT_GOAL := help

.PHONY: help fmt fmt-check vet lint test stress bench cover build check bump tag clean

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

tag: check ## Commit the bump and create the vX.Y.Z tag (does not push)
	@v=$$(tr -d '[:space:]' < $(VERSION_FILE)); \
	if git rev-parse -q --verify "refs/tags/v$$v" >/dev/null 2>&1; then \
		echo "tag v$$v already exists"; exit 1; \
	fi; \
	if [ -z "$$(git status --porcelain -- $(VERSION_FILE) CHANGELOG.md README.md)" ]; then \
		echo "nothing to release: run 'make bump VERSION=X.Y.Z' first"; exit 1; \
	fi; \
	git add $(VERSION_FILE) CHANGELOG.md README.md && \
	git commit -m "release v$$v" && \
	git tag -a "v$$v" -m "v$$v" && \
	echo && \
	echo "Tagged v$$v. Publish with:" && \
	echo "  git push --follow-tags"

clean: ## Remove build and coverage artefacts
	rm -f coverage.out
	go clean ./...
