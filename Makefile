GOMEMLIMIT ?= 4GiB
FLAGS ?=

# EVERY go test HERE RUNS ITS BINARIES THROUGH LEAKCHECK, which gives each
# package's binary a TMPDIR of its own and fails the package if the binary
# leaves a peer's repository or a universe behind (scripts/leakcheck). A target
# whose binaries run as root puts it ahead of AS_ROOT, as one -exec.
LEAKCHECK := $(CURDIR)/scripts/leakcheck

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
DREAM_PKGS = $(shell grep -rl --include='*_test.go' '//go:build dreaming' . | xargs grep -L '//go:build dreaming && ee' | sed 's|^\./||' | grep -vE '^(ee/|tools/tau/|\.)' | xargs -n1 dirname | sort -u | sed 's|^|./|')

.PHONY: test test-cli test-acme test-acme-live test-dreaming test-raft test-docker test-containerd test-application test-build test-corpus test-external-databases forge-images test-forges test-containers cover-containers cover-containers-unit cover-containers-docker cover-containers-root cover-containers-rootless cover-containers-report test-taucorder test-race test-all proto-v2 proto-v2-check vault-helper

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
	go test -exec $(LEAKCHECK) -timeout 20m $(FLAGS) $(TAU_PKGS)

# test-race is test under the race detector, plus pkg/raft's integration tag:
# two raft races of 2026-10-24 failed nothing until a -race run read them.
test-race:
	go test -race -exec $(LEAKCHECK) -timeout 60m $(FLAGS) $(TAU_PKGS)
	GOMEMLIMIT=$(GOMEMLIMIT) go test -race -exec $(LEAKCHECK) -tags raft_integration -p 1 -timeout 60m $(FLAGS) ./pkg/raft/...

# test-cli tests the developer CLI, which is its own module (tools/tau, so the
# root module never imports it and `./...` here never sweeps it in): its unit
# tests, then the end-to-end run against a dream fleet (tag dreaming, which is
# why DREAM_PKGS leaves tools/tau/ out — the root module cannot build it).
test-cli:
	cd tools/tau && go vet ./... && go test -exec $(LEAKCHECK) -timeout 10m $(FLAGS) ./...
	cd tools/tau && GOMEMLIMIT=$(GOMEMLIMIT) go test -exec $(LEAKCHECK) -tags dreaming -timeout 20m $(FLAGS) ./e2e/
	$(MAKE) test-corpus

# test-corpus puts every project under node/dream/projects through `tau build`
# and `tau ci run` against a universe that builds in containers, so it needs a
# docker daemon and, for the cloud's restricted build egress, root. The first
# run on a machine builds the toolchain image the corpus names. RUN= replaces
# the -run filter, e.g. RUN='^TestEveryCorpusProjectBuildsBothWays$$/^starter$$' (dollars doubled for make).
test-corpus:
	cd tools/tau && GOMEMLIMIT=$(GOMEMLIMIT) go test -exec '$(LEAKCHECK) $(AS_ROOT)' -tags 'dreaming docker_integration' -timeout 150m -v $(FLAGS) -run '$(if $(RUN),$(RUN),^(TestEveryCorpusProjectBuildsBothWays|TestTheDomainCorpusBuildsBothWays)$$)' ./e2e/

test-dreaming:
	GOMEMLIMIT=$(GOMEMLIMIT) go test -exec $(LEAKCHECK) -tags dreaming -p $(DREAM_P) -timeout 30m $(FLAGS) $(DREAM_PKGS)

test-raft:
	GOMEMLIMIT=$(GOMEMLIMIT) go test -exec $(LEAKCHECK) -tags raft_integration -p 1 -timeout 20m $(FLAGS) ./pkg/raft/...

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
	go test -exec $(LEAKCHECK) -tags docker_integration -run '_Integration$$' -p 1 -timeout 15m $(FLAGS) ./pkg/containers/...

# Run as root when the user is root or has passwordless sudo, skipped otherwise:
# the system containerd socket is root's (scripts/as-root).
test-containerd:
	go test -exec '$(LEAKCHECK) $(AS_ROOT)' -tags containerd_integration -run '_Integration$$' -p 1 -timeout 20m $(FLAGS) ./pkg/containers/...

# cover-containers measures pkg/containers and its two backends the only way
# that means anything for daemon-backed code: every pass that reaches them, each
# with -coverpkg across all three, merged. A pass that cannot run here leaves no
# profile and is left out of the merge, so the passes can also be run one at a
# time on different machines into one COVER_DIR and merged with
# cover-containers-report:
#
#   unit                untagged, no daemon
#   docker              the docker suites as the user
#   root                everything as root (scripts/as-root): the firewall
#                       paths, the system containerd, and the unit tests'
#                       root-only branches
#   rootless            the containerd suite as the user, which starts a
#                       rootless daemon; needs containerd, rootlesskit and
#                       slirp4netns or pasta, and a system socket the user
#                       cannot reach
CONTAINERS_COVERPKG := ./pkg/containers,./pkg/containers/backends/containerd,./pkg/containers/backends/docker
# Not under TMPDIR: scripts/cap gives each run a TMPDIR of its own and removes
# it once the run is over, and the passes of one report may be separate runs.
COVER_DIR ?= /tmp/tau-containers-cover
CONTAINERS_COVER = go test -exec $(LEAKCHECK) -coverpkg=$(CONTAINERS_COVERPKG) -p 1 -count=1 -timeout 30m $(FLAGS)

cover-containers: cover-containers-unit cover-containers-docker cover-containers-root cover-containers-rootless cover-containers-report

cover-containers-unit:
	mkdir -p $(COVER_DIR) && $(CONTAINERS_COVER) -coverprofile=$(COVER_DIR)/unit.out ./pkg/containers/...

cover-containers-docker:
	mkdir -p $(COVER_DIR) && $(CONTAINERS_COVER) -tags docker_integration -run '_Integration$$' -coverprofile=$(COVER_DIR)/docker.out ./pkg/containers/...

cover-containers-root:
	mkdir -p $(COVER_DIR) && $(CONTAINERS_COVER) -exec '$(LEAKCHECK) $(AS_ROOT)' -tags 'docker_integration containerd_integration' -coverprofile=$(COVER_DIR)/root.out ./pkg/containers/...

cover-containers-rootless:
	mkdir -p $(COVER_DIR) && $(CONTAINERS_COVER) -tags containerd_integration -run '_Integration$$' -coverprofile=$(COVER_DIR)/rootless.out ./pkg/containers/backends/containerd/

cover-containers-report:
	scripts/cover-merge $(COVER_DIR)/*.out

test-containers: test-docker test-containerd test-application test-build test-external-databases

# test-application runs a real application end to end: a corpus project
# compiled, published, routed by the gateway and served by a container the
# container tier started on docker. It needs a docker daemon AND CAP_NET_ADMIN,
# because the tier always restricts egress and the backend refuses to create a
# container it cannot firewall. So the binary is BUILT as the user and only the
# binary runs as root -- nothing root-owned lands in the Go cache. Without root
# or passwordless sudo it SKIPS and says so; with them and no daemon it fails.
#
# AS_ROOT runs a test binary with root or skips it (scripts/as-root).
AS_ROOT := $(CURDIR)/scripts/as-root

# test-acme issues real certificates from pebble, Let's Encrypt's test CA, run
# in process: the issuer against it by HTTP-01 and DNS-01, nodes ordering a
# service's name, a generated name and the zone's wildcard into the cm
# store and reading them back, and a node's https listeners presenting them.
# It needs nothing installed, so make test runs it too.
test-acme:
	go test -exec $(LEAKCHECK) -count=1 -v -timeout 10m ./pkg/certificate/acme/ ./services/cm/ && go test -exec $(LEAKCHECK) -count=1 -v -timeout 10m -run '^(TestAGeneratedNameIsIssuedByACMEAndServedOverTLS|TestTheZoneIsProvedByDNS01AndItsWildcardAnswersEveryService)$$' ./node/boot/

# test-acme-live issues from pebble in a container, a CA tau does not share a
# process with, whose resolver is bind9 finding the cloud's zone by real NS and
# glue in a bind9 parent for example.test: DNS-01 for a generated name and the
# wildcard, HTTP-01 through the gateway, then TLS against the gateway and the
# build frontend verified to pebble's root. A delegation names port 53, so the
# node's dns needs root; containers are host-networked, at pinned images.
test-acme-live:
	go test -exec '$(LEAKCHECK) $(AS_ROOT)' -tags docker_integration -count=1 -v -timeout 10m -run '^(TestARealCAIssuesThroughARealDelegation|TestTheLeftoverSweepTakesTheAnonymousVolumes)$$' ./node/boot/

test-application:
	go test -exec '$(LEAKCHECK) $(AS_ROOT)' -tags 'dreaming docker_integration' -count=1 -v -timeout 10m -run '^(TestAnApplicationAnswersThroughTheGateway|TestAContainerWorkerRunsToCompletionPerInvocation|TestAChannelServedByAContainerDeliversToItsNamedPort)$$' ./node/dream/

# test-build runs the build service's builds in real containers, for the same
# reason and under the same privilege as test-application: a build runs a
# stranger's commands, and the engine will not start one it cannot firewall.

test-build:
	go test -exec '$(LEAKCHECK) $(AS_ROOT)' -tags docker_integration -count=1 -v -timeout 15m -run '^TestARealBuild' ./services/build/artifact/

# test-external-databases deploys projects/with/database/{postgres,mysql}/sql
# against real servers the test starts in docker. No firewall is installed for a
# server the TEST runs, so it needs a docker daemon and no sudo.
test-external-databases:
	go test -exec $(LEAKCHECK) -tags 'dreaming docker_integration' -run '^TestAnExternal' -count=1 -timeout 15m $(FLAGS) ./node/dream/

# test-forges runs the forge drivers against real servers in docker: Gitea and
# Forgejo for the gitea driver, GitLab CE for the gitlab driver, at the images
# pinned in pkg/forge/live. Each walks one repository through identity,
# access, membership, create, list, a deploy key, a hook, a real push whose
# signed delivery pkg/forge verifies, and a clone with the deploy key. It needs
# a docker daemon and no sudo; GitLab alone takes minutes to boot.
test-forges: forge-images
	go test -exec $(LEAKCHECK) -tags docker_integration -run '^TestAReal' -count=1 -v -p 1 -timeout 30m $(FLAGS) ./pkg/forge/live/

# forge-images pulls every image test-forges starts, before its timed suite.
# A pull is bound by the network, not the machine: a cold GitLab is 1.7 GiB,
# half an hour on a host that downloads at 1 MB/s, and inside the suite's 30
# minutes it timed the suite out while no forge was slow. Pulled first, a cold
# cache costs the gate the download and never a forge's time; a pull that fails
# fails here, naming its image, before any forge starts. The two hours are the
# 2.7 GiB of images at under 400 KB/s.
forge-images:
	go test -exec $(LEAKCHECK) -tags docker_integration -run '^TestPullEveryForgeImage$$' -count=1 -v -timeout 2h ./pkg/forge/live/

# test-taucorder runs tools/taucorder, which is its own module (the root never
# imports it, so `make test` does not sweep it). It includes the end-to-end
# test against a node/dream fleet, and prints coverage over the whole module so
# the e2e run counts toward the code it exercises.
TAUCORDER_COVER ?= $${TMPDIR:-/tmp}/taucorder.cover
test-taucorder:
	cd tools/taucorder && GOMEMLIMIT=$(GOMEMLIMIT) go test -exec $(LEAKCHECK) -timeout 20m -coverpkg=./... -coverprofile=$(TAUCORDER_COVER) $(FLAGS) ./... && go tool cover -func=$(TAUCORDER_COVER) | tail -1

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
	cd pkg/kvdb/pb && buf generate

proto-v2-check:
	go test -exec $(LEAKCHECK) $(FLAGS) ./tools/protogen/... ./pkg/proto/...

# The secret helper a build or a container runs at /.taubyte/secret (pkg/vault). Its
# binaries are committed and embedded, so only an edit to secret.zig needs this. Zig is
# pinned because its standard library changes between releases.
ZIG ?= zig
VAULT_HELPER_OUT ?= pkg/vault/helper
vault-helper:
	@$(ZIG) version | grep -qx 0.14.1 || { echo "vault-helper: needs zig 0.14.1, $(ZIG) is $$($(ZIG) version)"; exit 1; }
	cache=$$(mktemp -d) && for a in x86_64 aarch64; do \
		$(ZIG) build-exe pkg/vault/helper/secret.zig -O ReleaseSmall -fstrip -fsingle-threaded \
			-target $$a-linux-musl --cache-dir $$cache -femit-bin=$(VAULT_HELPER_OUT)/secret-$$a || exit 1; \
		rm -f $(VAULT_HELPER_OUT)/secret-$$a.o; \
	done; rm -rf $$cache

# Enterprise-build targets live in the ee submodule and are pulled in when it's
# checked out (a no-op for an OSS-only tree). Run `make help-ee` for the list.
-include ee/Makefile
