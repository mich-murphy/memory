# One entry point for people, git hooks, agents and CI. `make check` runs every gate CI runs,
# except zizmor (CI only). Settings and reasons: docs/quality-gates.md.

GOLANGCI_LINT ?= golangci-lint
TOOL          := go tool -modfile=tools.mod
COVER_DIR     := $(CURDIR)/.cover
FUZZTIME      ?= 30s
FUZZ_TARGETS  := internal/adapters:FuzzSessionInfo internal/hook:FuzzReadInput \
                 internal/jsonx:FuzzParseObjectRoundTrip internal/raw:FuzzCompressRoundTrip \
                 internal/scrub:FuzzFile internal/shellwords:FuzzJoinSplit

.PHONY: check fmt fmt-check lint vet tidy-check fix-check deadcode vuln test cover fuzz actionlint build hooks clean

check: fmt-check lint fix-check tidy-check deadcode vuln cover actionlint

## Formatting (gofumpt and goimports through golangci-lint).
fmt:
	$(GOLANGCI_LINT) fmt ./...

fmt-check:
	$(GOLANGCI_LINT) fmt --diff ./...

## Static checks.
lint:
	$(GOLANGCI_LINT) run ./...

fix-check:
	go fix -diff ./...

tidy-check:
	go mod tidy -diff

deadcode:
	@out="$$($(TOOL) deadcode -test ./...)"; if [ -n "$$out" ]; then echo "$$out"; exit 1; fi

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

fuzz:
	@for t in $(FUZZ_TARGETS); do \
		pkg=$${t%%:*}; name=$${t##*:}; \
		echo "fuzzing $$name for $(FUZZTIME)"; \
		go test -run='^$$' -fuzz="^$$name$$" -fuzztime=$(FUZZTIME) ./$$pkg || exit 1; \
	done

build:
	CGO_ENABLED=0 go build -trimpath -o bin/agent-sessions ./cmd/agent-sessions

## Git hooks (lefthook.yml).
hooks:
	$(TOOL) lefthook install

clean:
	rm -rf $(COVER_DIR) cover.out bin dist
