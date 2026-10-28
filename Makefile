GOLANGCI_LINT ?= golangci-lint
TOOL          := go tool -modfile=tools/go.mod
COVER_DIR     := $(CURDIR)/.cover
FUZZTIME      ?= 30s
# CI keeps these. The pre-push hook passes TESTFLAGS= so Go's test cache applies.
TESTFLAGS     ?= -count=1 -shuffle=on
BENCH         ?= .
BENCHCOUNT    ?= 10
BENCHOUT      ?= bench.txt
FUZZ_TARGETS  := internal/adapters:FuzzSessionInfo internal/hook:FuzzReadInput \
                 internal/jsonx:FuzzParseObjectRoundTrip internal/raw:FuzzCompressRoundTrip \
                 internal/scrub:FuzzFile internal/scrub:FuzzText internal/capturepolicy:FuzzBaseName internal/shellwords:FuzzJoinSplit internal/gitinfo:FuzzStripUserinfo

.PHONY: check fmt fmt-check lint lint-version tidy-check fix-check deadcode vuln test cover crap fuzz mutation bench bench-smoke actionlint build hooks gopls clean

check: fmt-check lint fix-check tidy-check deadcode vuln crap bench-smoke actionlint

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
test:
	CGO_ENABLED=1 go test -race -shuffle=on ./...

# Unit coverage is a -coverprofile (cacheable). The end-to-end runs of the built binary write
# covdata to E2E_GOCOVERDIR as a side effect, which a cached test run does not redo. So that dir
# is kept between runs, named for a hash of the sources: an unchanged tree reuses its data, any
# change starts a fresh dir (and reruns the tests, since the dir is part of Go's cache key).
# Dirs are touched on use and pruned after 7 unused days (Go trims its cache after 5), so a
# cached result never points at a pruned dir; if the data is missing anyway, rerun uncached.
cover:
	@set -e; mkdir -p $(COVER_DIR); \
	key=$$(git ls-files -co --exclude-standard -z -- cmd internal go.mod go.sum | xargs -0 shasum 2>/dev/null | shasum | cut -c1-16); \
	e2e=$(COVER_DIR)/e2e-$$key; \
	find $(COVER_DIR) -mindepth 1 -maxdepth 1 -name 'e2e-*' -mtime +7 -exec rm -rf {} +; \
	case "$(TESTFLAGS)" in *-count=*) rm -rf "$$e2e";; esac; \
	mkdir -p "$$e2e"; touch "$$e2e"; \
	run() { set -x; CGO_ENABLED=1 E2E_GOCOVERDIR="$$e2e" go test $$1 -race -covermode=atomic \
		-coverpkg=./... -coverprofile=$(COVER_DIR)/unit.out ./...; { set +x; } 2>/dev/null; }; \
	run "$(TESTFLAGS)"; \
	[ -n "$$(ls -A "$$e2e")" ] || { echo "end-to-end coverage data missing for cached results; rerunning uncached"; run "-count=1 $(TESTFLAGS)"; }; \
	[ -n "$$(ls -A "$$e2e")" ] || { echo "no end-to-end coverage data in $$e2e"; exit 1; }; \
	go tool covdata textfmt -i="$$e2e" -o $(COVER_DIR)/e2e.out; \
	awk -f scripts/merge-cover.awk $(COVER_DIR)/unit.out $(COVER_DIR)/e2e.out > cover.out; \
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
	prek install --hook-type pre-commit --hook-type pre-push

clean:
	rm -rf $(COVER_DIR) cover.out bench.txt mutation.json bin dist
