GOLANGCI_LINT ?= golangci-lint
TOOL          := go tool -modfile=tools/go.mod
COVER_DIR     := $(CURDIR)/.cover
FUZZTIME      ?= 30s
BENCH         ?= .
BENCHCOUNT    ?= 10
BENCHOUT      ?= bench.txt
ANALYTICS_PYTHON ?= $(if $(wildcard .analytics-venv/bin/python),.analytics-venv/bin/python,python3)
FUZZ_TARGETS  := internal/adapters:FuzzSessionInfo internal/hook:FuzzReadInput \
                 internal/jsonx:FuzzParseObjectRoundTrip internal/raw:FuzzCompressRoundTrip \
                 internal/scrub:FuzzFile internal/scrub:FuzzText internal/capturepolicy:FuzzBaseName internal/shellwords:FuzzJoinSplit internal/gitinfo:FuzzStripUserinfo

.PHONY: check fmt fmt-check lint lint-version tidy-check fix-check deadcode vuln test cover crap fuzz mutation bench bench-smoke actionlint build hooks gopls clean capture-acceptance

check: fmt-check lint fix-check tidy-check deadcode vuln crap bench-smoke actionlint capture-acceptance

fmt:
	$(GOLANGCI_LINT) fmt ./...

fmt-check:
	$(GOLANGCI_LINT) fmt --diff ./...

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

# -test: internal/testenv is only used by tests.
deadcode:
	@out="$$($(TOOL) deadcode -test ./...)" || { echo "$$out"; exit 1; }; \
	if [ -n "$$out" ]; then echo "$$out"; exit 1; fi

vuln:
	$(TOOL) govulncheck ./...

actionlint:
	$(TOOL) actionlint

# The race detector needs cgo; release builds don't.
test: capture-acceptance
	CGO_ENABLED=1 go test -race -shuffle=on ./...

# No network, live ingestion, dependency installation, or legacy archive access.
# A fresh actual writer fixture is required; the consumer cannot skip this gate.
capture-acceptance:
	@tmp=$$(mktemp -d); trap 'rm -rf "$$tmp"' EXIT HUP INT TERM; \
		CGO_ENABLED=0 go build -o "$$tmp/agent-sessions" ./cmd/agent-sessions && \
		CAPTURE_V1_BINARY="$$tmp/agent-sessions" PYTHONDONTWRITEBYTECODE=1 \
		$(ANALYTICS_PYTHON) -m unittest discover -s tools/analytics -p 'test_*journey.py' -v && \
		PYTHONDONTWRITEBYTECODE=1 $(ANALYTICS_PYTHON) -m unittest discover -s tools/candidate-live-r2 -p 'test_*.py' -v && \
		PYTHONDONTWRITEBYTECODE=1 $(ANALYTICS_PYTHON) -m unittest discover -s tools/capture-delivery -p 'test_*.py' -v

cover:
	rm -rf $(COVER_DIR) && mkdir -p $(COVER_DIR)/unit $(COVER_DIR)/e2e
	CGO_ENABLED=1 E2E_GOCOVERDIR=$(COVER_DIR)/e2e go test -count=1 -race -shuffle=on \
		-covermode=atomic -coverpkg=./... ./... -args -test.gocoverdir=$(COVER_DIR)/unit
	go tool covdata textfmt -i=$(COVER_DIR)/unit,$(COVER_DIR)/e2e -o cover.out
	$(TOOL) go-test-coverage --config=.testcoverage.yml

# Compare two runs with: go tool -modfile=tools/go.mod benchstat old.txt new.txt
bench:
	go test -run='^$$' -bench='$(BENCH)' -benchmem -count=$(BENCHCOUNT) ./... | tee $(BENCHOUT)

# Runs each benchmark once, so a broken one fails CI.
bench-smoke:
	go test -run='^$$' -bench=. -benchtime=1x ./...

CRAP_FORMAT ?= table
crap: cover
	$(TOOL) go-crap scan ./... --coverage-profile=cover.out --exclude='internal/testenv/' \
		--missing=pessimistic --threshold=30 --fail-above --top=10 --no-progress --format=$(CRAP_FORMAT)

# MUTATE_DIFF=origin/main mutates only the lines changed since that ref.
MUTATE_DIFF ?=
mutation:
	$(TOOL) gremlins unleash --exclude-files='internal/testenv/' --timeout-coefficient=3 \
		--invert-assignments --invert-bitwise --invert-bwassign --invert-logical --invert-loopctrl \
		--remove-self-assignments --output=mutation.json $(if $(MUTATE_DIFF),--diff=$(MUTATE_DIFF))

fuzz:
	@for t in $(FUZZ_TARGETS); do \
		pkg=$${t%%:*}; name=$${t##*:}; \
		echo "fuzzing $$name for $(FUZZTIME)"; \
		go test -run='^$$' -fuzz="^$$name$$" -fuzztime=$(FUZZTIME) ./$$pkg || exit 1; \
	done

build:
	CGO_ENABLED=0 go build -trimpath -o bin/agent-sessions ./cmd/agent-sessions

gopls:
	go install -modfile=tools/gopls/go.mod golang.org/x/tools/gopls

hooks:
	$(TOOL) lefthook install

clean:
	rm -rf $(COVER_DIR) cover.out bench.txt mutation.json bin dist
