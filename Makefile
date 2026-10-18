.PHONY: help build all clean test test-unit test-e2e test-race bench fuzz console console-check console-dev site site-dev site-deploy server-setup

# The go on the PATH, or where the official installer puts it
GO ?= $(shell command -v go 2>/dev/null || echo /usr/local/go/bin/go)

help:
	@echo "Qoqi Flow"
	@echo ""
	@echo "  make build              Build qoqi"
	@echo "  make all                Build the console, then qoqi with it (needs node)"
	@echo "  make test               Run every test (unit and end to end)"
	@echo "  make test-unit          Run the Go unit tests"
	@echo "  make test-e2e           Run the end-to-end tests: qoqi on .qi files, the daemon, the SDKs"
	@echo "  make test-race          Run the tests that run programs under the race detector"
	@echo "  make bench              Measure the virtual machine, and whole programs"
	@echo "  make fuzz               Search for inputs that break the compiler or the machine (FUZZ_TIME=60s each)"
	@echo "  make console            Build the console of qoqi serve into qoqi (needs node)"
	@echo "  make console-check      Typecheck and test the console, without building it"
	@echo "  make console-dev        Serve the console from its source, against a daemon on :7070"
	@echo "  make site               Build qoqi.dev, the landing page and docs/, into website/dist"
	@echo "  make site-dev           Serve qoqi.dev at http://127.0.0.1:8080, built again at each page"
	@echo "  make site-deploy        Build qoqi.dev and send it to its server"
	@echo "  make server-setup       Set up the server of qoqi.dev and demo.qoqi.dev (see deploy/)"
	@echo "  make clean              Remove what the build and the tests leave"

build:
	@$(GO) build -o qoqi ./cmd/qoqi
	@echo "Built ./qoqi"

# The console of qoqi serve is a React app in console/. What it builds
# goes into internal/flowd/console, and is kept in the repository:
# building qoqi needs no node, only changing the console does.
NPM ?= npm

# Its dependencies, installed again only when the lock changes
console/node_modules/.package-lock.json: console/package-lock.json
	@cd console && $(NPM) ci --no-audit --no-fund
	@touch $@

console: console/node_modules/.package-lock.json
	@cd console && $(NPM) test && $(NPM) run build
	@echo "Built internal/flowd/console"

console-check: console/node_modules/.package-lock.json
	@cd console && $(NPM) run typecheck && $(NPM) test

console-dev: console/node_modules/.package-lock.json
	@cd console && $(NPM) run dev

all: console build

# Every run of the tests has a time limit, and the packages that run
# programs end themselves if they come to hold too much memory (see
# internal/memguard): a test that goes wrong fails, it does not take the
# machine with it.
TEST_TIMEOUT := 300s

test-unit:
	@$(GO) test -timeout $(TEST_TIMEOUT) ./internal/... ./pkg/...

test-e2e:
	@$(GO) test -timeout $(TEST_TIMEOUT) ./test/e2e ./test/fuzz ./test/bench

# The race detector also checks every conversion of a number into a
# pointer, which is how the VM keeps its objects in its values: that
# check is turned off, the rest is what is wanted.
test-race:
	@$(GO) test -race -gcflags=all=-d=checkptr=0 -timeout 900s ./internal/vm ./internal/stdlib ./internal/memguard ./internal/flowrt ./internal/flowtest ./internal/flowd ./pkg/... ./test/e2e

test: test-unit test-e2e

# The virtual machine on its own, then whole programs (test/bench)
bench:
	@$(GO) test ./internal/vm ./test/bench -run '^$$' -bench . -benchmem -timeout 600s

# Lets the machine look for inputs that break the compiler or the virtual
# machine, a minute each. What it finds is kept under test/fuzz/testdata
# and run with the tests from then on.
FUZZ_TIME := 60s

fuzz:
	@$(GO) test ./test/fuzz -run '^$$' -fuzz FuzzSource -fuzztime $(FUZZ_TIME) -parallel 4 -timeout 900s
	@$(GO) test ./test/fuzz -run '^$$' -fuzz FuzzBytecode -fuzztime $(FUZZ_TIME) -parallel 4 -timeout 900s

# qoqi.dev: the landing page, and the documentation made from docs/ by
# website/, a module of its own. Its links are checked as it is built.
SERVER ?= qoqi.dev

site:
	@cd website && $(GO) run .

site-dev:
	@cd website && $(GO) run . -serve 127.0.0.1:8080

site-deploy: site
	@rsync -az --delete website/dist/ deploy@$(SERVER):/var/www/qoqi.dev/
	@echo "Sent to https://qoqi.dev"

server-setup:
	@rsync -az --delete deploy/server/ install.sh root@$(SERVER):/opt/qoqi-ops/
	@ssh root@$(SERVER) sh /opt/qoqi-ops/setup.sh

clean:
	@rm -f qoqi
	@rm -f flows.db flows.db-wal flows.db-shm
	@find . -name "*.test" -delete
