# One entry point for people, git hooks, agents and CI. `make check` runs every gate CI runs,
# except zizmor (CI only). Settings and reasons: docs/quality-gates.md.

GOLANGCI_LINT ?= golangci-lint
TOOL          := go tool -modfile=tools/go.mod
COVER_DIR     := $(CURDIR)/.cover
FUZZTIME      ?= 30s
FUZZ_TARGETS  := internal/adapters:FuzzSessionInfo internal/hook:FuzzReadInput \
                 internal/jsonx:FuzzParseObjectRoundTrip internal/raw:FuzzCompressRoundTrip \
                 internal/shellwords:FuzzJoinSplit internal/gitinfo:FuzzStripUserinfo

.PHONY: check fmt fmt-check lint lint-version tidy-check fix-check deadcode vuln test cover crap fuzz actionlint build hooks gopls clean

check: fmt-check lint fix-check tidy-check deadcode vuln crap actionlint

## Formatting (gofumpt and goimports through golangci-lint).
fmt:
	$(GOLANGCI_LINT) fmt ./...

fmt-check:
	$(GOLANGCI_LINT) fmt --diff ./...

## Static checks. CI installs the golangci-lint version in .golangci-lint-version; a different
## local one fails here rather than passing code CI would reject.
lint: lint-version
	$(GOLANGCI_LINT) run ./...

lint-version:
	@want="$$(cat .golangci-lint-version)"; have="v$$($(GOLANGCI_LINT) version --short)"; \
	if [ "$$have" != "$$want" ]; then echo "golangci-lint is $$have; CI uses $$want"; exit 1; fi

fix-check:
	go fix -diff ./...

tidy-check:
	go mod tidy -diff
	go -C tools mod tidy -diff
	go -C tools/gopls mod tidy -diff

# -test counts what tests call as live: raw.Join, the manifest reader, is how tests check that
# manifests rebuild their files. A failed load fails the target as well as any finding.
deadcode:
	@out="$$($(TOOL) deadcode -test ./...)" || { echo "$$out"; exit 1; }; \
	if [ -n "$$out" ]; then echo "$$out"; exit 1; fi

vuln:
	$(TOOL) govulncheck ./...

actionlint:
	$(TOOL) actionlint

## Tests. The race detector needs cgo; release builds don't.
test:
	CGO_ENABLED=1 go test -race -shuffle=on ./...

# Unit tests plus the end-to-end runs of the built binary, merged into one profile, then the
# coverage gate (.testcoverage.yml).
cover:
	rm -rf $(COVER_DIR) && mkdir -p $(COVER_DIR)/unit $(COVER_DIR)/e2e
	CGO_ENABLED=1 E2E_GOCOVERDIR=$(COVER_DIR)/e2e go test -count=1 -race -shuffle=on \
		-covermode=atomic -coverpkg=./... ./... -args -test.gocoverdir=$(COVER_DIR)/unit
	go tool covdata textfmt -i=$(COVER_DIR)/unit,$(COVER_DIR)/e2e -o cover.out
	$(TOOL) go-test-coverage --config=.testcoverage.yml

# CRAP gate (go-crap) on the profile `cover` just wrote: per function, cyclomatic complexity
# against coverage. Fails when any function scores over 30, so complex code must be tested and
# untested code must stay simple. Prints the ten worst either way. CI sets CRAP_FORMAT=github.
CRAP_FORMAT ?= table
crap: cover
	$(TOOL) go-crap scan ./... --coverage-profile=cover.out --exclude='internal/testenv/' \
		--missing=pessimistic --threshold=30 --fail-above --top=10 --no-progress --format=$(CRAP_FORMAT)

fuzz:
	@for t in $(FUZZ_TARGETS); do \
		pkg=$${t%%:*}; name=$${t##*:}; \
		echo "fuzzing $$name for $(FUZZTIME)"; \
		go test -run='^$$' -fuzz="^$$name$$" -fuzztime=$(FUZZTIME) ./$$pkg || exit 1; \
	done

build:
	CGO_ENABLED=0 go build -trimpath -o bin/agent-sessions ./cmd/agent-sessions

## gopls for coding agents, pinned in tools/gopls/go.mod. `.mcp.json` runs it as an MCP server
## with `go tool`; the gopls-lsp Claude Code plugin needs this binary on PATH.
gopls:
	go install -modfile=tools/gopls/go.mod golang.org/x/tools/gopls

## Git hooks (lefthook.yml).
hooks:
	$(TOOL) lefthook install

clean:
	rm -rf $(COVER_DIR) cover.out bin dist
