GOMEMLIMIT ?= 4GiB
FLAGS ?=
DREAM_P ?= 4

# Tagged sweeps discover their packages instead of sweeping ./... so they only
# build and run the packages that actually carry tagged tests. Scoped to the
# community tree — the ee/ submodule's dreaming tests run under
# `make test-ee-dreaming`.
DREAM_PKGS = $(shell grep -rl --include='*_test.go' '//go:build dreaming' . | sed 's|^\./||' | grep -v '^ee/' | xargs -n1 dirname | sort -u | sed 's|^|./|')

.PHONY: test test-dreaming test-raft test-docker test-all bench-dreaming vm-fixtures test-cli test-cli-cover

test:
	go test $(FLAGS) ./...

# tau-cli coverage. The command tree is driven in-process through cli.Run, so
# coverage only shows up when credited across the whole module — -coverpkg does
# that (a plain per-package run reports each package against its own _test files
# and hides the e2e reach). Reports the module total and hard-enforces the DSL
# surface this refactor owns; the module total is gated by the I/O packages
# (git/OAuth/container/accounts) that need live-service mocks to unit-test.
# The gate is on tools/tau/tcc — the DSL reader the CLI is now built on, which is
# the substantive logic this refactor introduced. The tools/tau command tree is
# reported for visibility but not gated: roughly half its statements are I/O
# shells (git clone/push, OAuth, container builds, the accounts service,
# interactive prompts, OS process-discovery) that the e2e/dreaming suites drive
# against real services and that no unit test can reach without live-service
# mocks. Coverage is credited in-process via -coverpkg (a plain per-package run
# hides the e2e reach).
CLI_CORE ?= ./tools/tau/tcc/...
CLI_CORE_MIN ?= 80.0
test-cli:
	go test $(FLAGS) ./tools/tau/...

test-cli-cover:
	go test -coverpkg=./tools/tau/... -coverprofile=/tmp/tau-cli.cov $(FLAGS) ./tools/tau/...
	@echo "tau-cli module coverage (reported): $$(go tool cover -func=/tmp/tau-cli.cov | awk '/^total:/{print $$3}')"
	go test -coverpkg=$$(echo $(CLI_CORE) | tr ' ' ,) -coverprofile=/tmp/tau-core.cov $(FLAGS) $(CLI_CORE)
	@core=$$(go tool cover -func=/tmp/tau-core.cov | awk '/^total:/{sub(/%/,"",$$3); print $$3}'); \
	echo "tools/tau/tcc coverage: $$core% (min $(CLI_CORE_MIN)%)"; \
	awk "BEGIN{exit !($$core >= $(CLI_CORE_MIN))}" || { echo "tcc coverage below $(CLI_CORE_MIN)%"; exit 1; }

test-dreaming:
	GOMEMLIMIT=$(GOMEMLIMIT) go test -tags dreaming -p $(DREAM_P) -timeout 30m $(FLAGS) $(DREAM_PKGS)

test-raft:
	GOMEMLIMIT=$(GOMEMLIMIT) go test -tags raft_integration -p 1 -timeout 20m $(FLAGS) ./pkg/raft/...

test-docker:
	go test -tags docker_integration -run '_Integration$$' -p 1 $(FLAGS) ./pkg/containers/...

test-all: test test-dreaming test-raft

# Recompile the vm-low-orbit guest test fixtures (Go via tinygo/container,
# Rust via cargo/native) to size-optimized wasm importing "taubyte/sdk".
# Output is committed; only rerun when the guest sources change.
vm-fixtures:
	bash pkg/vm-low-orbit/tests/fixtures/build.sh

# Profiling benchmarks over a live dream universe (dream/benchmarks).
# Examples:
#   make bench-dreaming BENCH=HTTPFunction FLAGS="-cpuprofile=/tmp/cpu.prof -memprofile=/tmp/mem.prof"
#   make bench-dreaming BENCH=UniverseBoot FLAGS="-benchtime=5x -cpuprofile=/tmp/boot.prof"
BENCH ?= .
bench-dreaming:
	GOMEMLIMIT=$(GOMEMLIMIT) go test -tags dreaming -run '^$$' -bench '$(BENCH)' -benchmem -timeout 30m $(FLAGS) ./dream/benchmarks

# Enterprise-build targets live in the ee submodule and are pulled in when it's
# checked out (a no-op for an OSS-only tree). Run `make help-ee` for the list.
-include ee/Makefile
