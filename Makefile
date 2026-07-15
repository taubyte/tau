GOMEMLIMIT ?= 4GiB
FLAGS ?=
DREAM_P ?= 4

# Tagged sweeps discover their packages instead of sweeping ./... so they only
# build and run the packages that actually carry tagged tests.
DREAM_PKGS = $(shell grep -rl --include='*_test.go' '//go:build dreaming' . | xargs -n1 dirname | sort -u | sed 's|^\([^./]\)|./\1|')
# web3 gates source files (not tests), so test every package tree that carries
# web3-gated code.
WEB3_PKGS = $(shell grep -rl --include='*.go' '//go:build web3' . | xargs -n1 dirname | sort -u | sed 's|^\([^./]\)|./\1|; s|$$|/...|')

.PHONY: test test-dreaming test-web3 test-raft test-docker test-all bench-dreaming vm-fixtures

test:
	go test $(FLAGS) ./...

test-dreaming:
	GOMEMLIMIT=$(GOMEMLIMIT) go test -tags dreaming -p $(DREAM_P) -timeout 30m $(FLAGS) $(DREAM_PKGS)

test-web3:
	GOMEMLIMIT=$(GOMEMLIMIT) go test -tags web3 -p 1 -timeout 15m $(FLAGS) $(WEB3_PKGS)

test-raft:
	GOMEMLIMIT=$(GOMEMLIMIT) go test -tags raft_integration -p 1 -timeout 20m $(FLAGS) ./pkg/raft/...

test-docker:
	go test -tags docker_integration -run '_Integration$$' -p 1 $(FLAGS) ./pkg/containers/...

test-all: test test-dreaming test-web3 test-raft

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
