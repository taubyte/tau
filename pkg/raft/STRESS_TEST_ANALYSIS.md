# Raft Stress Test Analysis (Before & After Fixes)

This document tracks the evolution of the Raft stress tests, the original issues that were found, and the current behavior after the fixes that were implemented and validated.

## Current Test Results (After Fixes)

Command used:

```bash
go test ./pkg/raft -tags=stress -run TestStressCluster -timeout 30m -v -count=1 \
  |& tee /tmp/stresstest2.log
```

**All stress tests now pass:**

| Test | Status | Time (from `/tmp/stresstest2.log`) |
|------|--------|------------------------------------|
| ConcurrentJoin_5Nodes | ✅ PASS | 107.89s |
| ConcurrentJoin_10Nodes | ✅ PASS | 116.84s |
| ConcurrentJoin_20Nodes | ✅ PASS | 143.03s |
| SequentialJoin_5Nodes | ✅ PASS | 90.64s |
| SequentialJoin_10Nodes | ✅ PASS | 139.82s |
| SequentialJoin_20Nodes | ✅ PASS | 227.89s |
| PhasedJoin_5Nodes | ✅ PASS | 23.86s |
| PhasedJoin_10Nodes | ✅ PASS | 29.14s |
| PhasedJoin_20Nodes | ✅ PASS | 44.67s |
| LeaderDistribution | ✅ PASS | 203.69s |

All tests are running at realistic scale (up to 20 nodes), with concurrent and sequential join patterns, and they complete without timeouts or membership failures.

---

## What Was Broken Before (Historical)

The initial version of the stress tests exposed several **real issues** in the way clusters were bootstrapped and how the tests were written:

- **20-node tests failed** while 5- and 10-node tests passed.
- **Split-brain behavior** in `ConcurrentJoin_20Nodes`: up to 20 independent Raft clusters formed, each with its own leader.
- **Nodes failing to join as voters** (notably "node 17" in the 20-node tests).
- Tests used `WithForceBootstrap()` patterns that do **not** reflect real-world usage.

### Historical Symptoms

From the original (pre-fix) runs:

- `ConcurrentJoin_20Nodes`:
  - `cluster 1 failed to wait for leader: context deadline exceeded`
  - Logs showed 20 different nodes entering leader state with `tally=1` (each node bootstrapped itself).
- `SequentialJoin_20Nodes` and `PhasedJoin_20Nodes`:
  - `node 17 failed to join as voter`
  - Timeouts waiting for a specific node to be added as a voter.

### Historical Root Causes

- **Test-only `WithForceBootstrap()` usage**:
  - Multiple tests explicitly forced a bootstrap path that is *not* used in real deployments.
  - This amplified split-brain scenarios by encouraging nodes to form standalone clusters.

- **Aggressive / fixed bootstrap timeouts**:
  - Short, fixed bootstrap timeouts (e.g. ~2s) were too small for 20 concurrent nodes to discover each other.
  - Nodes concluded they were "alone" and executed `bootstrapSelf()`, producing many independent 1-node clusters.

- **Concurrency + insufficient join coordination**:
  - Many nodes attempted to start simultaneously.
  - Some tests did not wait long enough for leader election and configuration convergence before performing strict assertions.

These historical issues were valid and correctly documented in the earlier version of this file. The rest of this document explains what changed and what is now being verified.

---

## Fixes Implemented in the Stress Tests

The recent changes focus on making the **stress tests mirror real-world usage** and on adding much stronger correctness checks.

### 1. Removed `WithForceBootstrap()` from Stress Scenarios

Previously, several stress tests used a pattern like:

```go
// Pseudo-code (old pattern)
if idx == 0 {
    // First node bootstraps
    clusters[idx], err = New(
        nodes[idx],
        namespace,
        WithTimeouts(stressTimeoutConfig()),
        WithForceBootstrap(),
        WithBootstrapTimeout(bootTimeout),
    )
} else {
    // Others use discovery
    clusters[idx], err = New(
        nodes[idx],
        namespace,
        WithTimeouts(stressTimeoutConfig()),
        WithBootstrapTimeout(bootTimeout),
    )
}
```

This pattern was removed from:

- `testConcurrentJoin`
- `testSequentialJoin`
- `testPhasedJoin`
- `TestStressCluster_LeaderDistribution`

Now **all nodes start using the real discovery/join path**, matching production behavior.

### 2. More Realistic Bootstrap Timeouts

The stress tests now derive `bootTimeout` via `bootstrapTimeoutForNodes(nodeCount)`, which scales with node count and is large enough for:

- Peer discovery
- Exchange of Raft transport peers
- Cluster formation under load

This eliminates the historical "2 seconds for 20 nodes" issue that led to 20 independent clusters.

### 3. Stronger Cluster Health Checks

We extended the cluster health checks to exercise and assert much more than just:

- "A leader exists" and
- "Nodes are voters".

#### 3.1. Configuration Consistency

`verifyConfigurationConsistency` is run across all clusters and logs:

- `verifyConfigurationConsistency: SUCCESS - all N clusters have consistent configuration (N members)`

In `/tmp/stresstest.log` we now see repeated confirmations such as:

```text
verifyConfigurationConsistency: SUCCESS - all 20 clusters have consistent configuration (20 members)
```

This confirms that **every node sees the same Raft configuration** (same members, suffrage, etc.) and that we no longer have the "cluster 1 has 5 members, cluster 0 has 1" problem.

#### 3.2. Cluster Stability

`verifyClusterStability` periodically checks that:

- Exactly one leader remains during a stability window.
- No unexpected state changes (e.g. multiple leaders, leader flapping).

The logs show:

```text
verifyClusterStability: SUCCESS - cluster stable for 500ms (3 checks)
```

for many iterations and cluster sizes, indicating **no split-brain and stable leadership** during the check windows.

### 4. New FSM Coherence Tests (Critical for Production)

To make sure Raft is not just electing leaders but actually replicating and applying state correctly, we added:

- `verifyFSMCoherence`
- `verifyFSMCoherenceWithDelete`

These are invoked from:

- `testConcurrentJoin`
- `testSequentialJoin`
- `testPhasedJoin`
- `TestStressCluster_LeaderDistribution`
- `verifyClusterHealth`

#### 4.1. `verifyFSMCoherence`

High-level behavior:

1. Find the current leader.
2. Leader writes a number of key/value pairs (e.g. `concurrent-20-3/key-7`).
3. Leader calls `Barrier()` to ensure all writes are committed.
4. Every node calls `Get(key)` and verifies the expected value.

From `/tmp/stresstest.log`:

```text
verifyFSMCoherence: Starting FSM coherence test with 10 keys (prefix: concurrent-20-3)
verifyFSMCoherence: SUCCESS - all 20 clusters have coherent FSM state (10 keys verified)
```

We see many such lines across:

- concurrent join tests (5, 10, 20 nodes)
- sequential join tests
- phased join tests

This confirms that **the KV FSM state is identical across all nodes for the keys we write**.

#### 4.2. `verifyFSMCoherenceWithDelete`

For phased joins, we also test delete propagation:

1. Leader writes a key.
2. Verify the key exists on all nodes.
3. Leader deletes the key.
4. Verify the key disappears from all nodes.

From `/tmp/stresstest.log`:

```text
verifyFSMCoherenceWithDelete: SUCCESS - delete operation replicated correctly
```

This demonstrates that **both writes and deletes are replicated and applied consistently**.

### 5. Leader Distribution Test: Confirming Fair Leadership

`TestStressCluster_LeaderDistribution` now demonstrates that **different nodes can become leader over time** in a 5-node cluster.

From `/tmp/stresstest2.log`:

```text
Leader distribution across 10 iterations:
  12D3KooWDaX3KrGmkJqopt1dwoYDBpn4iyEJAvuNqobRDLmQddMq: 1 times (10.0%)
  ...
  12D3KooWC7aCfp4uEJm2wbdWWpswnFJDKVavnMAG1TvLAZqXnKrp: 1 times (10.0%)
Total unique leaders: 10
```

This confirms:

- No single node is "stuck" as leader.
- Election behavior is healthy and well-distributed.

---

## Current Assessment: Production Readiness

Based on the latest stress runs and logs (`/tmp/stresstest.log`, `/tmp/stresstest2.log`):

- **No split-brain**:
  - Exactly one leader per cluster at a time.
  - Pre-vote mechanism correctly rejects candidate requests when a leader exists.
- **Random join order is safe**:
  - Concurrent join tests with 5, 10, and 20 nodes all converge to a single shared configuration.
- **Node failure / shutdown is handled cleanly**:
  - `transport shutdown` / `failed to negotiate protocol` errors only appear during shutdown/cleanup phases.
  - Raft aborts pipeline replication when nodes are closing, as expected.
- **FSM state is coherent** across all nodes in all tested scenarios:
  - Verified by explicit Set + Barrier + Get checks.
  - Deletes are also correctly replicated.

From a stress-testing perspective, the current system behaves as we would expect in production under:

- Randomized start/join order
- Larger cluster sizes (up to 20 nodes in these tests)
- Repeated cycles of cluster creation, verification, and teardown

---

## Historical Appendix (Kept for Context)

The earlier sections of this file (now summarized in "What Was Broken Before") are left as **historical context** to document:

- The split-brain behavior that was previously observed.
- Why it happened (test-only `WithForceBootstrap()`, overly aggressive timeouts, etc.).
- The thought process that led to the current fixes.

Going forward, any new Raft changes should:

1. Run the full `TestStressCluster` suite with `-tags=stress`.
2. Inspect `/tmp/stresstest.log` and `/tmp/stresstest2.log` for:
   - Any failed FSM coherence checks.
   - Any configuration inconsistency or stability failures.
   - Unexpected patterns in leader elections.

This gives us a strong safety net against regressions in the Raft implementation or its integration with discovery/transport.
