# Taubyte Raft (`pkg/raft`)

This package wraps HashiCorp Raft and makes it fit Taubyte's world: **libp2p transport**, **Taubyte discovery**, and **datastore-backed persistence**.

The part I care about is the operational story: you can bring nodes up and down, start them in any order, and still end up with a working cluster. No static "here are the 3 seed nodes" config. No separate bootstrap step. Just start the service with a namespace.

Common things this unlocks for Taubyte:

- Cron-style leader scheduling (one node decides, everyone agrees)
- Queue metadata and coordination
- Locks / barriers / leases for distributed synchronization
- Replicated service state for stateful containers/services

---

## Namespaces = independent clusters

A cluster is identified by a namespace string that must start with `/raft/` (for example: `/raft/cron`, `/raft/queues/global`, `/raft/service/my-api`).

That same namespace threads through everything:

- **Discovery** uses the namespace as the rendezvous key.
- **Command API** runs on `/raft/v1/<namespace>`.
- **Raft RPC transport** runs on `/raft/v1/<namespace>/transport`.
- **Storage** is prefixed under `namespace + "/"` in the node datastore (and snapshots go to disk).

If you want two independent Raft groups on the same mesh, you don't spin up two subsystems. You change the namespace.

---

## What runs on the wire

There are two pieces:

- **Raft transport (RPC)**: `raft.NetworkTransport` over libp2p streams (via gostream). Raft server IDs and addresses are peer IDs, so membership is "these peers are voters".
- **Stream command service (API)**: a Taubyte command service that exposes `set/get/delete/keys` plus a couple of cluster-management helpers (`joinVoter`, `exchangePeers`).

Writes and membership changes are leader-only in Raft. The stream service handles that reality by **forwarding to the leader** when it has to. In practice that means clients can usually talk to any reachable node and still get work done.

---

## Bootstrapping without a coordinator

When a node starts, it runs a short "who else is here?" phase before it decides what to do. Two details matter:

- **`bootstrapTimeout`**: how long to spend discovering + exchanging peer lists (default: 10s).
- **`bootstrapThreshold`**: a time cutoff inside that window (default: 0.8). Peers seen before the cutoff are treated as "founders"; peers seen after are treated as late joiners.

The flow is roughly:

1. Discover peers (Taubyte discovery + currently connected libp2p peers).
2. Exchange peer lists (`exchangePeers`) so "A saw C" can quickly become "B also knows C".
3. If we're a late joiner, we try to get added as a voter (`joinVoter`) and stop pretending we should bootstrap anything.
4. If multiple founders exist, nodes try to join an existing leader first. If nobody can find a leader, founders bootstrap together.
5. If we saw nobody, we bootstrap as a single-node cluster (and if we were wrong, we fall back to join).

This sounds a bit fussy on paper, but it solves the annoying real-world cases: simultaneous startup, staggered startup, partial connectivity, and nodes coming online just after the initial "formation" moment.

---

## "Self-healing" in this package (concretely)

This isn't magic membership management. It's the basics, done reliably:

- **Rejoin**: a node that was removed can come back and request to be added again.
- **Reboot**: a follower can disappear, the cluster keeps running (if quorum holds), and when it returns it catches up.
- **Leader reboot**: leadership can move; the old leader can rejoin as a follower later.

These are covered in the integration tests (look for rejoin/reboot/leader-rejoin scenarios).

---

## Storage model

- **Raft logs**: datastore-backed log store under `namespace + "/log/"`
- **Stable store**: datastore-backed stable store under `namespace + "/stable/"`
- **FSM data (built-in KV)**: datastore under `namespace + "/data/"`
- **Snapshots**: filesystem-backed snapshots (default retain: 3) under `/tmp/tau-raft-snapshots/...`

Snapshots being on disk is a deliberate tradeoff (simple + fast). If you're running in an environment where `/tmp` isn't what you want, that's the first knob to revisit.

---

## API surface

### `Cluster` (local)

- **KV**: `Set`, `Get`, `Delete`, `Keys`
- **Raw replication**: `Apply([]byte, timeout)` (leader-only)
- **Consistency**: `Barrier(timeout)` (use before reads when you can't tolerate stale follower data)
- **State**: `IsLeader`, `Leader`, `State`, `Members`, `WaitForLeader`
- **Membership**: `AddVoter`, `RemoveServer`, `TransferLeadership`

### `Client` (remote)

The `Client` uses the stream command service and supports:

- `Set`, `Get` (optionally with a barrier), `Delete`, `Keys`
- `JoinVoter`
- `ExchangePeers`

---

## Configuration knobs

- **Timeout presets**: `local`, `regional` (default), `global`
- **Bootstrap**:
  - `WithBootstrapTimeout(d)`
  - `WithForceBootstrap()` (only use when you really mean "this should start a new cluster right now")
- **Encryption**:
  - `WithEncryptionKey(key)` enables AES-256-GCM for both:
    - Raft transport (RPC)
    - Stream commands (including join + peer exchange)
  - All members must share the same key.

---

## Quickstart (sketch)

### Start (or join) a cluster

```go
cl, err := raft.New(node, "/raft/cron",
  raft.WithTimeoutPreset(raft.PresetRegional),
  raft.WithBootstrapTimeout(3*time.Second),
  // raft.WithEncryptionKey(key32bytesOrMore),
)
if err != nil { /* handle */ }
defer cl.Close()

ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
defer cancel()
_ = cl.WaitForLeader(ctx)
```

### Write on the leader (KV)

```go
if cl.IsLeader() {
  _ = cl.Set("next-run", []byte("2026-01-27T10:00:00Z"), 5*time.Second)
}
```

### Read with a barrier

```go
_ = cl.Barrier(2 * time.Second)
v, ok := cl.Get("next-run")
_ = v
_ = ok
```

### Use the p2p `Client`

```go
cli, _ := raft.NewClient(node, "/raft/cron", nil)
defer cli.Close()

_ = cli.Set("job:123", []byte("scheduled"), 5*time.Second, somePeerID)
val, found, _ := cli.Get("job:123", int64((2*time.Second).Nanoseconds()), somePeerID)
_ = val
_ = found
```

---

## Notes

- **Leader-only writes**: `Cluster.Set/Delete/Apply/AddVoter/RemoveServer` require leadership. If you don't know the leader, use the `Client` path and let the service forward.
- **Follower reads can be stale** unless you use `Barrier()` (or the client-side barrier option for `Get`).

---

## Where to look in the code

- `cluster.go`: lifecycle, bootstrap logic, membership operations
- `discovery.go`: peer tracking + discovery/exchange loop
- `transport.go`: libp2p stream-based Raft transport
- `stream.go` + `client.go`: command service + client, leader forwarding, join flows
- `fsm.go`: built-in KV FSM and command encoding

