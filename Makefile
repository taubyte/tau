GOMEMLIMIT ?= 4GiB
FLAGS ?=

# ONE DREAMING PACKAGE IS A MULTI-NODE UNIVERSE, so this is a share of the
# MACHINE and not a fixed number. It was 4, which on a four-core box runs four
# universes against four cores: the raft and crdt tests there do not fail an
# assertion, they MISS A DEADLINE -- a group that never finishes forming, a
# rebroadcast that never lands inside its window -- and that reads as a bug in
# the code rather than as a box with no headroom.
#
# MEASURED on a four-core box: at -p 2 the ledger test hung past the 30m
# timeout against a ~100s norm and the crdt kv test missed its 60s window;
# both pass 3/3 isolated, and the whole suite is green at -p 1. A quarter of
# the cores reproduces that -- 1 on four cores, 2 on eight -- and never less
# than one.
DREAM_P ?= $(shell n=$$(nproc 2>/dev/null || echo 4); p=$$((n / 4)); [ $$p -lt 1 ] && p=1; echo $$p)

# Tagged sweeps discover their packages instead of sweeping ./... so they only
# build and run the packages that actually carry tagged tests. Scoped to the
# community tree — the ee/ submodule's dreaming tests run under
# `make test-ee-dreaming`.
#
# Dot-directories are excluded because tooling keeps git worktrees there —
# checkouts of this repo nested inside it. `go test ./...` skips them (own
# go.mod), but this glob does not, and including them both fails their packages
# outright and runs every dreaming test twice under -p, colliding on the fixed
# ports the fixtures bind.
#
# Files tagged `dreaming && ee` are dropped as well: a package whose dreaming
# tests are all tagged that way has nothing to build in this sweep, and listing
# it fails the package outright rather than skipping it.
DREAM_PKGS = $(shell grep -rl --include='*_test.go' '//go:build dreaming' . | xargs grep -L '//go:build dreaming && ee' | sed 's|^\./||' | grep -vE '^(ee/|\.)' | xargs -n1 dirname | sort -u | sed 's|^|./|')

.PHONY: test test-dreaming test-raft test-docker test-containerd test-application test-build test-external-databases test-containers test-all proto-v2 proto-v2-check

# THE TIMEOUT IS EXPLICIT BECAUSE THE DEFAULT IS A GUESS ABOUT THE MACHINE.
# go test allows a package 10 minutes unless told otherwise, and pkg/kvdb spends
# six of them on go-datastore's own conformance suite — SubtestCombinations
# enumerates query permutations, and it is third-party coverage worth having.
# Six minutes of a ten-minute budget is not headroom: on a slower box that suite
# runs past the default and the gate reports a timeout for a package that was
# only going to finish later. The tagged suites below already say a number; the
# untagged ones inherited the guess, so they say one now too.
# ee/ shares this module but is ported separately, so the community sweep
# lists its packages instead of taking ./... whole.
TAU_PKGS = $(shell go list -e ./... 2>/dev/null | grep -v '/ee/')

test:
	go test -timeout 20m $(FLAGS) $(TAU_PKGS)

test-dreaming:
	GOMEMLIMIT=$(GOMEMLIMIT) go test -tags dreaming -p $(DREAM_P) -timeout 30m $(FLAGS) $(DREAM_PKGS)

test-raft:
	GOMEMLIMIT=$(GOMEMLIMIT) go test -tags raft_integration -p 1 -timeout 20m $(FLAGS) ./pkg/raft/...

# Container backend integration suites. Both drive the same conformance suite
# (pkg/containers/backends/conformance), which is what keeps docker and
# containerd interchangeable. They are kept out of `test`/`test-all` because
# they need a live runtime; the untagged tests under ./pkg/containers/... run
# with no daemon at all.
#
# test-docker needs a running docker daemon.
# test-containerd needs either a reachable /run/containerd/containerd.sock or
# containerd + rootlesskit on PATH (plus subuid/subgid entries) for rootless.
test-docker:
	go test -tags docker_integration -run '_Integration$$' -p 1 -timeout 15m $(FLAGS) ./pkg/containers/...

test-containerd:
	go test -tags containerd_integration -run '_Integration$$' -p 1 -timeout 20m $(FLAGS) ./pkg/containers/...

test-containers: test-docker test-containerd test-application test-build test-external-databases

# test-application runs a real application end to end: a corpus project
# compiled, published, routed by the gateway and served by a container the
# container tier started on docker. It needs a docker daemon AND CAP_NET_ADMIN,
# because the tier always restricts egress and the backend refuses to create a
# container it cannot firewall. So the binary is BUILT as the user and only the
# binary runs under sudo -- nothing root-owned lands in the Go cache -- from
# node/dream, where the corpus paths are relative. It fails rather than skips
# without a daemon or the privilege.
APPLICATION_TEST := $(CURDIR)/node/dream/application.test

test-application:
	cd node/dream && go test -c -tags 'dreaming docker_integration' -o $(APPLICATION_TEST) .
	cd node/dream && sudo -n $(APPLICATION_TEST) -test.run '^(TestAnApplicationAnswersThroughTheGateway|TestAContainerWorkerRunsToCompletionPerInvocation|TestAChannelServedByAContainerDeliversToItsNamedPort)$$' -test.timeout 10m -test.v

# test-build runs the build service's builds in real containers, for the same
# reason and under the same privilege as test-application: a build runs a
# stranger's commands, and the engine will not start one it cannot firewall.
BUILD_TEST := $(CURDIR)/services/build/artifact/artifact.test

test-build:
	cd services/build/artifact && go test -c -tags docker_integration -o $(BUILD_TEST) .
	cd services/build/artifact && sudo -n $(BUILD_TEST) -test.run '^TestARealBuild' -test.timeout 15m -test.v

# test-external-databases deploys projects/with/database/{postgres,mysql}/sql
# against real servers the test starts in docker. No firewall is installed for a
# server the TEST runs, so it needs a docker daemon and no sudo.
test-external-databases:
	go test -tags 'dreaming docker_integration' -run '^TestAnExternal' -count=1 -timeout 15m $(FLAGS) ./node/dream/

test-all: test test-dreaming test-raft

# Regenerate v2's inter-service RPC stubs (protoc-gen-go + protoc-gen-connect-go,
# both pinned in pkg/proto/buf.gen.yaml) from pkg/proto. Requires buf,
# protoc-gen-go and protoc-gen-connect-go on PATH at those pinned versions.
# `go test ./pkg/proto/...` (already part of `test` above) fails if the
# committed gen/ output drifts from this; run it directly with proto-v2-check.
#
# `buf generate` hard-errors on a module with zero .proto files ("had no
# .proto files") — and a fresh clone hits exactly that until a service's
# first .proto lands, since this tree ships tooling only (see the commit
# that added it). tools/protogen.Check already treats "no sources" as its
# own case rather than shelling out to buf (pkg/proto/drift_test.go
# passes today, at zero files, for the same reason); this target mirrors
# that same scan (any *.proto below pkg/proto, excluding gen/ wherever
# it appears, not just at the top) so `make proto-v2` degrades the same way
# instead of failing the build for having nothing to do yet.
proto-v2:
	@if find pkg/proto \( -name gen -type d \) -prune -o -name '*.proto' -print | grep -q .; then \
		cd pkg/proto && buf generate; \
	else \
		echo "pkg/proto: no .proto sources yet — nothing to generate"; \
	fi

proto-v2-check:
	go test $(FLAGS) ./tools/protogen/... ./pkg/proto/...

# Enterprise-build targets live in the ee submodule and are pulled in when it's
# checked out (a no-op for an OSS-only tree). Run `make help-ee` for the list.
-include ee/Makefile
