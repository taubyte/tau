# Raft Consensus Protocol for Tau

This package provides a high-level abstraction over the [libp2p-raft](https://github.com/libp2p/go-libp2p-raft) library, which wraps [HashiCorp's Raft](https://github.com/hashicorp/raft) implementation with libp2p transport capabilities.

## Overview

The `pkg/raft` package enables strongly consistent, replicated state machines across distributed Tau nodes. By leveraging libp2p's networking stack, clusters can be formed dynamically using peer discovery without static configuration.

### Why Raft?

- **Strong Consistency**: Guarantees linearizable reads and writes
- **Leader Election**: Automatic failover when the leader becomes unavailable
- **Log Replication**: Ordered, durable log entries replicated across all nodes
- **Dynamic Membership**: Add/remove nodes without cluster downtime

## Architecture

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                               pkg/raft                                      │
│  ┌─────────────┐  ┌──────────────┐  ┌─────────────┐  ┌────────────────────┐ │
│  │   Cluster   │  │   Options    │  │  Built-in   │  │   Membership       │ │
│  │   Manager   │  │   Builder    │  │  KV FSM     │  │   Manager          │ │
│  └─────────────┘  └──────────────┘  └─────────────┘  └────────────────────┘ │
│  ┌───────────────────────────────┐  ┌───────────────────────────────────┐   │
│  │   P2P Stream Service          │  │   P2P Client (with forwarding)    │   │
│  │   /raft/v1/<namespace>        │  │   auto-forwards to leader         │   │
│  └───────────────────────────────┘  └───────────────────────────────────┘   │
├─────────────────────────────────────────────────────────────────────────────┤
│                           hashicorp/raft                                    │
│  ┌───────────────────────────────────────────────────────────────────────┐  │
│  │                    Log, Stable, Snapshot Stores                       │  │
│  └───────────────────────────────────────────────────────────────────────┘  │
├─────────────────────────────────────────────────────────────────────────────┤
│                    p2p/peer & p2p/streams                                   │
│  ┌───────────────────────────────────────────────────────────────────────┐  │
│  │   libp2p Host   │   Discovery   │   Streams   │   node.Store()        │  │
│  └───────────────────────────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────────────────────┘
                                      │
                                      ▼
                            All storage uses node.Store()
                            with namespace prefixes
```

## Core Concepts

### Built-in State Management

The package includes a built-in key-value FSM that uses the **peer's existing datastore** (`node.Store()`). No separate storage is created:

```go
cluster, err := raft.New(node, "/raft/my-service")

// Just use Set/Get/Delete - FSM is handled internally
// Data is stored in peer's datastore with namespace prefix
cluster.Set("key", []byte("value"), timeout)
val, ok := cluster.Get("key")
cluster.Delete("key", timeout)
```

**How it works:**
- FSM writes to `node.Store()` (existing pebble/badger backend)
- Keys are prefixed with namespace: `/raft/my-service/key`
- No duplicate storage - leverages existing infrastructure

### Write Operations

Writes must go through the leader. The package provides two approaches:

#### Direct Cluster API (No Forwarding)

When calling `Set`/`Delete`/`Apply` directly on the `Cluster` interface:

- If this node is leader → commits directly
- If this node is follower → returns `ErrNotLeader`
- If no leader / quorum lost → returns error

```go
err := cluster.Set("key", []byte("value"), timeout)
if errors.Is(err, raft.ErrNotLeader) {
    // Need to forward to leader manually or retry
    leader, _ := cluster.Leader()
    log.Printf("not leader, leader is %s", leader)
}
```

#### P2P Client API (Automatic Forwarding)

The package also provides a P2P stream-based client that automatically forwards writes to the leader:

```go
// Create a client for the same namespace
client, err := raft.NewClient(node, "/raft/my-service")
if err != nil { /* handle error */ }

// Set/Get/Delete automatically forward to leader if needed
err = client.Set("key", []byte("value"), timeout)  // Works on any node!
val, found, err := client.Get("key")
err = client.Delete("key", timeout)
keys, err := client.Keys("prefix/")
```

The stream service runs on protocol `/raft/v1/<namespace>` (e.g., `/raft/v1/raft/my-service`) and handles:
- `set` - Forwards to leader, commits, returns success
- `get` - Reads from local FSM (eventually consistent)
- `delete` - Forwards to leader, commits, returns success
- `keys` - Lists keys from local FSM

> **Note**: Forwarding is implemented via P2P streams. The leader receives the request and applies it to Raft. This is transparent to the client.

### Read Consistency

`Get()` reads from the local FSM (committed state), but may be stale:

| Read on | Consistency | Notes |
|---------|-------------|-------|
| Leader | Strong (usually) | Latest committed state |
| Follower | Eventual | May lag behind leader |

For **guaranteed fresh reads**:

```go
// Option 1: Use Barrier to ensure caught up
err := cluster.Barrier(5 * time.Second)
if err != nil {
    // Cluster may be unhealthy
}
val, _ := cluster.Get("key")  // Now guaranteed current

// Option 2: Only read from leader
if !cluster.IsLeader() {
    // Redirect to leader or accept stale read
}
```

> **Note**: For many use cases, stale reads are acceptable. Only use Barrier when strong consistency is required.

### Custom FSM (Advanced)

For advanced use cases, you can provide a custom FSM via `WithFSM()`:

```go
// FSM defines the interface for custom state machines
type FSM interface {
    // Apply is invoked when a log entry is committed
    // Returns typed FSMResponse instead of interface{}
    Apply(log *raft.Log) FSMResponse
    
    // Snapshot creates a point-in-time snapshot
    Snapshot() (FSMSnapshot, error)
    
    // Restore rebuilds from a snapshot
    Restore(snapshot io.ReadCloser) error
}

// FSMResponse provides typed response from Apply
type FSMResponse struct {
    Error error
    Data  []byte
}

// Use custom FSM
cluster, err := raft.New(node, "/raft/my-service",
    raft.WithFSM(myCustomFSM),
)
```

### Cluster Roles

| Role | Description |
|------|-------------|
| **Leader** | Handles all client requests and replicates to followers |
| **Follower** | Receives replicated logs from leader, can become candidate |
| **Candidate** | Temporarily during leader election |

### Quorum

A quorum requires a strict majority of nodes: `⌊n/2⌋ + 1`

| Cluster Size | Quorum | Fault Tolerance | Notes |
|--------------|--------|-----------------|-------|
| 1 | 1 | 0 nodes | Valid for dev/testing, single point of failure |
| 2 | 2 | 0 nodes | ⚠️ **Avoid** - worse than 1 node (both must be up) |
| 3 | 2 | 1 node | Minimum for production |
| 5 | 3 | 2 nodes | Recommended for production |
| 7 | 4 | 3 nodes | High availability |

> **⚠️ Warning about 2-node clusters**: A 2-node cluster requires *both* nodes to be available for any writes. This provides no fault tolerance while adding a failure dependency. If you need minimal resources, use 1 node (accepting no HA) or 3 nodes (for actual fault tolerance). Never use 2 nodes in production.

## Regional Scaling with High Timeouts

For clusters spanning multiple geographic regions, network latency between nodes can be significant (50-300ms+). The package provides timeout presets optimized for different deployment scenarios.

### Timeout Configuration

```go
// TimeoutPreset defines timeout configurations for different deployment scenarios
type TimeoutPreset string

const (
    // PresetLocal for same-datacenter deployments (low latency)
    PresetLocal TimeoutPreset = "local"
    
    // PresetRegional for multi-region within same continent
    PresetRegional TimeoutPreset = "regional"
    
    // PresetGlobal for worldwide distributed clusters
    PresetGlobal TimeoutPreset = "global"
    
    // PresetCustom for user-defined timeouts
    PresetCustom TimeoutPreset = "custom"
)
```

### Preset Values

| Parameter | Local | Regional | Global |
|-----------|-------|----------|--------|
| **HeartbeatTimeout** | 1s | 5s | 15s |
| **ElectionTimeout** | 1s | 10s | 30s |
| **CommitTimeout** | 500ms | 5s | 15s |
| **LeaderLeaseTimeout** | 500ms | 5s | 15s |
| **SnapshotInterval** | 2min | 5min | 10min |
| **SnapshotThreshold** | 8192 | 16384 | 32768 |

### Custom Timeout Configuration

```go
// TimeoutConfig allows fine-grained control over Raft timing
type TimeoutConfig struct {
    // HeartbeatTimeout specifies the time in follower state without
    // a leader before we attempt an election
    HeartbeatTimeout time.Duration
    
    // ElectionTimeout specifies the time in candidate state without
    // leader contact before we attempt another election
    ElectionTimeout time.Duration
    
    // CommitTimeout controls the time without an Apply operation
    // before heartbeat is sent to ensure timely commit
    CommitTimeout time.Duration
    
    // LeaderLeaseTimeout is used to control how long the "lease"
    // lasts for leader without being able to contact quorum
    LeaderLeaseTimeout time.Duration
    
    // SnapshotInterval controls how often we check if we should
    // perform a snapshot
    SnapshotInterval time.Duration
    
    // SnapshotThreshold controls how many outstanding logs there
    // must be before we perform a snapshot
    SnapshotThreshold uint64
}
```

## P2P Protocol

The package uses libp2p streams for inter-node communication. Each cluster has a unique protocol path.

### Protocol Naming

```go
const ProtocolRaftPrefix = "/raft/v1"

// Full protocol: /raft/v1/<namespace>
// Example: /raft/v1/raft/my-service
func Protocol(namespace string) string {
    return ProtocolRaftPrefix + namespace
}
```

For a cluster with namespace `/raft/my-service`, the stream protocol is:
- `/raft/v1/raft/my-service`

This ensures that clusters with different namespaces use different protocols and never interfere.

### Stream Commands

The stream service handles the following commands:

| Command | Description | Forwarded? |
|---------|-------------|------------|
| `set` | Store a key-value pair | Yes (to leader) |
| `get` | Retrieve a value by key | No (local read) |
| `delete` | Remove a key | Yes (to leader) |
| `keys` | List keys with prefix | No (local read) |

### Using the P2P Client

```go
// Create client for a specific namespace
client, err := raft.NewClient(node, "/raft/my-service")
if err != nil { /* handle */ }
defer client.Close()

// Operations automatically find peers and forward as needed
err = client.Set("config/db-host", []byte("localhost:5432"), 5*time.Second)

val, found, err := client.Get("config/db-host")
if found {
    fmt.Printf("Value: %s\n", val)
}

keys, err := client.Keys("config/")
```

## Dynamic Cluster Creation

The package supports clusters created and modified at runtime through libp2p peer discovery. **No manual bootstrap configuration is needed** - clusters form autonomously.

### Autonomous Bootstrap

When you create a Raft cluster with a namespace, the node automatically:

1. **Searches for existing peers** advertising the same namespace
2. **Waits for the bootstrap timeout** (default: 10 seconds) to find peers
3. **Bootstraps** based on what it finds:
   - If peers found → bootstraps with ALL discovered peers, Raft elects leader
   - If no peers found → bootstraps as single-node cluster (becomes leader)

**Scenario 1: Sequential Start**
```
Node A starts with namespace "/raft/myapp"
  → Waits 10s for peers → None found → Bootstraps as single-node leader

Node B starts later with namespace "/raft/myapp"  
  → Discovers Node A → Leader A adds B as voter (via AddVoter)

Node C starts later
  → Discovers A, B → Leader adds C as voter
  → Now 3-node cluster with fault tolerance
```

**Scenario 2: Simultaneous Start**
```
Nodes A, B, C all start at the same time with namespace "/raft/myapp"
  → All discover each other within timeout
  → All bootstrap with configuration [A, B, C]
  → Raft election happens → One becomes leader
  → 3-node cluster formed automatically!
```

This is fully automatic - **no manual bootstrap configuration needed**.

### Bootstrap Options

```go
// Default behavior: discover first, auto-bootstrap if no peers found
cluster, err := raft.New(node, "/raft/my-service")

// Customize bootstrap timeout (how long to wait for peers)
cluster, err := raft.New(node, "/raft/my-service",
    raft.WithBootstrapTimeout(30 * time.Second),  // Wait longer for peers
)

// Force immediate bootstrap (skip discovery - use sparingly!)
cluster, err := raft.New(node, "/raft/my-service",
    raft.WithForceBootstrap(),  // Bootstrap immediately as single-node
)
```

| Option | When to Use |
|--------|-------------|
| Default | Most cases - autonomous cluster formation |
| `WithBootstrapTimeout(d)` | Slow networks where discovery takes longer |
| `WithForceBootstrap()` | Testing, or when you KNOW this is the first node |

### Peer Discovery Integration

Clusters are identified by a **namespace** (name). Nodes with the same namespace automatically discover each other using libp2p's existing discovery mechanisms (DHT, mDNS, etc.):

```go
// Create a Raft cluster - nodes discover each other by namespace
cluster, err := raft.New(node, "/raft/my-service", fsm)
```

That's it. Nodes advertising the same namespace will:
1. Find each other via libp2p discovery
2. Automatically form or join the Raft cluster
3. Elect a leader and begin consensus

```go
// Discovery and bootstrap can be tuned via options:

raft.WithBootstrapTimeout(30 * time.Second) // Wait for peers before auto-bootstrap
raft.WithDiscoveryInterval(10 * time.Second) // How often to search for new peers  
raft.WithMinPeers(2)                         // Wait for N peers before starting
raft.WithDiscoveryTimeout(5 * time.Minute)   // Max time to wait for MinPeers
```

### Membership Overview

Membership is handled automatically via namespace discovery:

- **Join**: Start node with same namespace → auto-discovered and added
- **Leave**: Call `cluster.Close()` → gracefully removed  
- **Remove**: Leader calls `cluster.RemoveServer(peerID, timeout)` → forcibly removed

## Package API

### Cluster Interface

```go
// Cluster represents a Raft consensus cluster
type Cluster interface {
    // Close gracefully shuts down the Raft node
    Close() error
    
    // Namespace returns the cluster namespace
    Namespace() string
    
    // --- Built-in Key-Value Operations ---
    
    // Set stores a key-value pair (replicated via Raft)
    // Returns ErrNotLeader if not leader
    Set(key string, value []byte, timeout time.Duration) error
    
    // Get retrieves a value by key from local committed state
    // Note: May return stale data on followers (replication lag)
    // For strong consistency, call Barrier() first
    Get(key string) ([]byte, bool)
    
    // Delete removes a key (replicated via Raft)
    // Returns ErrNotLeader if not leader
    Delete(key string, timeout time.Duration) error
    
    // Keys returns all keys matching a prefix
    Keys(prefix string) []string
    
    // --- Low-level Raft Operations ---
    
    // Apply submits raw bytes to be replicated (for custom FSM)
    // Returns ErrNotLeader if not leader
    Apply(cmd []byte, timeout time.Duration) (FSMResponse, error)
    
    // Barrier ensures all preceding operations are committed
    Barrier(timeout time.Duration) error
    
    // --- Cluster State ---
    
    // IsLeader returns true if this node is the current leader
    IsLeader() bool
    
    // Leader returns the peer ID of the current leader
    Leader() (peer.ID, error)
    
    // State returns the current Raft state (Follower, Candidate, Leader)
    State() raft.RaftState
    
    // WaitForLeader blocks until a leader is elected
    WaitForLeader(ctx context.Context) error
    
    // --- Membership ---
    
    // Members returns all cluster members
    Members() ([]Member, error)
    
    // RemoveServer removes a node from the cluster (leader only)
    RemoveServer(id peer.ID, timeout time.Duration) error
    
    // TransferLeadership transfers leadership to another node
    TransferLeadership() error
    
    // LeaderCh returns a channel that signals leadership changes
    LeaderCh() <-chan bool
}

// Member represents a cluster member
type Member struct {
    ID       peer.ID
    Address  string
    Suffrage raft.ServerSuffrage  // Voter, Nonvoter, or Staging
}
```

### Constructor

The primary constructor is simple - just the node and namespace:

```go
import "github.com/taubyte/tau/p2p/peer"

// New creates a Raft cluster with the given namespace
// Nodes with the same namespace discover each other automatically
// Uses the node's existing Store() for all state management
func New(node peer.Node, namespace string, opts ...Option) (Cluster, error)
```

The `peer.Node` interface provides everything we need:
- `node.Peer()` → libp2p host for transport
- `node.Store()` → datastore for FSM, logs, snapshots
- `node.Discovery()` → peer discovery for cluster formation
- `node.ID()` → unique node identifier

**Example:**
```go
cluster, err := raft.New(node, "/raft/patrick/jobs")
```

That's it. The package manages the FSM internally.

### P2P Client

For clients that need to interact with a Raft cluster from any node (with automatic forwarding):

```go
// Client is a p2p client for communicating with raft cluster nodes
type Client struct {
    // Embeds the stream client for discovery and communication
    *streamClient.Client
}

// NewClient creates a new raft p2p client for the given namespace
func NewClient(node Node, namespace string) (*Client, error)

// Set sends a set command (forwards to leader automatically)
func (c *Client) Set(key string, value []byte, timeout time.Duration, peers ...peer.ID) error

// Get sends a get command to retrieve a value
func (c *Client) Get(key string, peers ...peer.ID) ([]byte, bool, error)

// Delete sends a delete command (forwards to leader automatically)
func (c *Client) Delete(key string, timeout time.Duration, peers ...peer.ID) error

// Keys sends a keys command to list keys with a prefix
func (c *Client) Keys(prefix string, peers ...peer.ID) ([]string, error)

// Send sends a custom command to the specified peers
func (c *Client) Send(cmd string, body command.Body, peers ...peer.ID) (cr.Response, error)
```

**Example:**
```go
client, err := raft.NewClient(node, "/raft/my-service")
if err != nil { /* handle */ }
defer client.Close()

// Works from any node - automatically forwards writes to leader
err = client.Set("key", []byte("value"), 5*time.Second)
val, found, err := client.Get("key")
```

### Options

```go
// Option configures optional cluster behavior
type Option func(*config)

// WithDataDir sets the directory for Raft data (logs, snapshots)
// Default: derived from node's repo path + namespace
func WithDataDir(dir string) Option

// WithTimeoutPreset sets a predefined timeout configuration
// Default: PresetRegional
func WithTimeoutPreset(preset TimeoutPreset) Option

// WithTimeouts sets custom timeout configuration
func WithTimeouts(cfg TimeoutConfig) Option

// WithMinPeers waits for N peers before starting consensus
// Default: 0 (start immediately)
func WithMinPeers(n int) Option

// WithFSM provides a custom FSM implementation (advanced)
// Default: built-in key-value FSM
func WithFSM(fsm raft.FSM) Option

// WithLogger sets a custom logger
func WithLogger(logger *log.Logger) Option
```

**Example with options:**
```go
cluster, err := raft.New(node, "/raft/patrick/jobs",
    raft.WithTimeoutPreset(raft.PresetGlobal),
    raft.WithMinPeers(2),  // Wait for at least 2 other nodes
)
```

## Usage Examples

### Creating a Cluster

```go
package main

import (
    "context"
    "log"
    "time"
    
    "github.com/taubyte/tau/p2p/peer"  // peer.Node interface
    "github.com/taubyte/tau/pkg/raft"
)

func main() {
    ctx := context.Background()
    
    // Create libp2p node (using existing tau infrastructure)
    node, err := peer.NewFull(ctx, "/data/node", privateKey, swarmKey, 
        []string{"/ip4/0.0.0.0/tcp/4001"},
        nil, true, peer.Bootstrap(bootstrapPeers...))
    if err != nil {
        log.Fatal(err)
    }
    
    // Create Raft cluster - nodes discover each other by namespace
    cluster, err := raft.New(node, "/raft/my-service")
    if err != nil {
        log.Fatal(err)
    }
    defer cluster.Close()
    
    // Wait for leader election
    ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
    defer cancel()
    
    if err := cluster.WaitForLeader(ctx); err != nil {
        log.Fatal(err)
    }
    
    // Set a value (replicated across cluster)
    err = cluster.Set("config/timeout", []byte("30s"), 10*time.Second)
    if err != nil {
        log.Printf("Set failed: %v", err)
    }
    
    // Get a value (reads local state)
    val, ok := cluster.Get("config/timeout")
    if ok {
        log.Printf("Value: %s", val)
    }
}
```

### Multi-Region Cluster

For clusters spanning regions, just add timeout options:

```go
// Same namespace = same cluster, nodes find each other
cluster, err := raft.New(node, "/raft/global-service",
    raft.WithTimeoutPreset(raft.PresetGlobal),
)
```

All nodes using namespace `"/raft/global-service"` will discover each other and form a single Raft cluster with timeouts tuned for cross-region latency.

### Custom FSM Example (Advanced)

For complex state machines beyond key-value, implement the FSM interface:

```go
package myfsm

import (
    "encoding/json"
    "io"
    "sync"
    
    "github.com/hashicorp/raft"
)

// Command types for the FSM
type CommandType uint8

const (
    CommandSet CommandType = iota
    CommandDelete
)

// Command is the structure replicated via Raft
type Command struct {
    Type  CommandType `json:"type"`
    Key   string      `json:"key"`
    Value []byte      `json:"value,omitempty"`
}

// KeyValueFSM is a simple key-value store FSM
type KeyValueFSM struct {
    mu   sync.RWMutex
    data map[string][]byte
}

func NewKeyValueFSM() *KeyValueFSM {
    return &KeyValueFSM{
        data: make(map[string][]byte),
    }
}

// Apply implements raft.FSM
func (f *KeyValueFSM) Apply(log *raft.Log) raft.FSMResponse {
    var cmd Command
    if err := json.Unmarshal(log.Data, &cmd); err != nil {
        return raft.FSMResponse{Error: err}
    }
    
    f.mu.Lock()
    defer f.mu.Unlock()
    
    switch cmd.Type {
    case CommandSet:
        f.data[cmd.Key] = cmd.Value
        return raft.FSMResponse{}
    case CommandDelete:
        delete(f.data, cmd.Key)
        return raft.FSMResponse{}
    default:
        return raft.FSMResponse{Error: fmt.Errorf("unknown command type: %d", cmd.Type)}
    }
}

// Snapshot implements raft.FSM
func (f *KeyValueFSM) Snapshot() (raft.FSMSnapshot, error) {
    f.mu.RLock()
    defer f.mu.RUnlock()
    
    // Deep copy the data
    data := make(map[string][]byte, len(f.data))
    for k, v := range f.data {
        data[k] = append([]byte(nil), v...)
    }
    
    return &kvSnapshot{data: data}, nil
}

// Restore implements raft.FSM
func (f *KeyValueFSM) Restore(snapshot io.ReadCloser) error {
    defer snapshot.Close()
    
    var data map[string][]byte
    if err := json.NewDecoder(snapshot).Decode(&data); err != nil {
        return err
    }
    
    f.mu.Lock()
    defer f.mu.Unlock()
    f.data = data
    
    return nil
}

// Get reads a value (can be called on any node)
func (f *KeyValueFSM) Get(key string) ([]byte, bool) {
    f.mu.RLock()
    defer f.mu.RUnlock()
    val, ok := f.data[key]
    return val, ok
}

// kvSnapshot implements raft.FSMSnapshot
type kvSnapshot struct {
    data map[string][]byte
}

func (s *kvSnapshot) Persist(sink raft.SnapshotSink) error {
    if err := json.NewEncoder(sink).Encode(s.data); err != nil {
        sink.Cancel()
        return err
    }
    return sink.Close()
}

func (s *kvSnapshot) Release() {}
```

### Dynamic Cluster Membership

**Adding nodes**: Just start a new node with the same namespace - it discovers and joins automatically.

```go
// On new node - just use the same namespace
cluster, err := raft.New(node, "/raft/myapp")
// Automatically discovers existing cluster and joins
```

**Removing nodes**: For graceful removal, use the membership API:

```go
// Remove a node from the cluster (leader only)
err := cluster.RemoveServer(peerID, 30*time.Second)

// Or trigger leadership transfer before shutdown
err := cluster.TransferLeadership()
```

**Listing members**:

```go
members, err := cluster.Members()
for _, m := range members {
    fmt.Printf("ID: %s, Addr: %s, Voter: %v\n", m.ID, m.Address, m.Suffrage == raft.Voter)
}
```

## Storage

All Raft data uses the **peer's existing datastore** (`node.Store()`), namespaced by prefix:

```
/raft/<namespace>/
├── log/          # Raft log entries
├── stable/       # Raft metadata (term, voted for)
├── snapshots/    # FSM snapshots
└── data/         # FSM key-value data
```

**Benefits:**
- Single storage backend (pebble) for everything
- No additional storage configuration
- Existing backup/restore applies to Raft data
- Namespace isolation between different Raft clusters

## Transport Layer

The libp2p transport wraps HashiCorp Raft with libp2p streams:

### Protocol ID

```go
const (
    // ProtocolRaft is the libp2p protocol for Raft RPC
    ProtocolRaft = "/tau/raft/1.0.0"
)
```

### Stream Multiplexing

Raft operations use dedicated libp2p streams:
- **AppendEntries**: Log replication from leader to followers
- **RequestVote**: Candidate requesting votes during election
- **InstallSnapshot**: Leader sending snapshot to follower
- **TimeoutNow**: Leadership transfer request

## Observability

### Events

```go
// ClusterEvent represents significant cluster state changes
type ClusterEvent int

const (
    EventLeaderElected ClusterEvent = iota
    EventLeaderLost
    EventNodeJoined
    EventNodeLeft
    EventSnapshotCreated
    EventSnapshotRestored
)

// EventHandler is called when cluster events occur
type EventHandler func(event ClusterEvent, data interface{})

// WithEventHandler registers an event handler
func WithEventHandler(handler EventHandler) Option
```

### Metrics

The package exposes Prometheus-compatible metrics:

| Metric | Type | Description |
|--------|------|-------------|
| `tau_raft_state` | Gauge | Current Raft state (0=Follower, 1=Candidate, 2=Leader) |
| `tau_raft_term` | Gauge | Current Raft term |
| `tau_raft_commit_index` | Gauge | Index of last committed log entry |
| `tau_raft_applied_index` | Gauge | Index of last applied log entry |
| `tau_raft_fsm_apply_total` | Counter | Total number of FSM applies |
| `tau_raft_snapshot_total` | Counter | Total number of snapshots taken |
| `tau_raft_peers` | Gauge | Number of peers in the cluster |

## Best Practices

### Cluster Sizing

| Scenario | Recommended Size | Notes |
|----------|-----------------|-------|
| Development/Testing | 1 node | No fault tolerance, but fully functional |
| ~~Minimal~~ | ~~2 nodes~~ | ❌ **Never use** - no benefit over 1 node |
| Production (minimal) | 3 nodes | Tolerates 1 failure |
| Production (standard) | 5 nodes | Tolerates 2 failures |
| Production (high-availability) | 7 nodes | Tolerates 3 failures |

> **Note**: Larger clusters increase consensus latency. For most use cases, 5 nodes provides an optimal balance.

> **Why not 2 nodes?** With 2 nodes, quorum requires both nodes. If *either* fails, the cluster halts. You get the operational complexity of a distributed system with zero fault tolerance benefits. Use 1 node (simpler, same fault tolerance) or 3 nodes (actual HA).

### Scaling from 1 Node

A common pattern is starting with 1 node and scaling up:

```
1 node → 3 nodes    ✅ Recommended (skip 2)
1 node → 2 nodes    ⚠️ Avoid (makes things worse)
```

When scaling from 1 to 3 nodes:
1. Start with single-node cluster (leader)
2. Add second node as voter → cluster now needs 2/2 for quorum (brief vulnerability)
3. Add third node as voter → cluster now needs 2/3 for quorum (fault tolerant)

The transition through 2 nodes is brief and acceptable during planned scaling.

### Failure Scenarios

> **Note**: These behaviors are implemented by the Raft protocol itself (via `hashicorp/raft`). We don't implement consensus logic - we just configure and use it.

#### Single Node Cluster

| Operation | Behavior |
|-----------|----------|
| `Set/Delete` | ✅ Succeeds immediately (quorum = 1) |
| `Get` | ✅ Works (reads local state) |
| `IsLeader` | ✅ Always true |
| Node crashes | ❌ Cluster unavailable until restart |

Single node is fully functional - the node is always the leader and can commit instantly.

#### Leader Loses Quorum (Network Partition / Node Failures)

When the leader can no longer reach a majority of nodes:

| Operation | Behavior |
|-----------|----------|
| `Set/Delete` | ❌ Fails after timeout (can't commit without quorum) |
| `Get` | ✅ Works (reads stale local state) |
| `IsLeader` | Returns `false` after LeaderLeaseTimeout expires |

```go
// Writes will fail when quorum is lost
err := cluster.Set("key", value, 10*time.Second)
if err != nil {
    // err = "leadership lost" or "timeout"
    // Retry or wait for cluster recovery
}

// Reads still work (but may be stale)
val, ok := cluster.Get("key")  // Still works
```

#### Follower Loses Connection

If a follower can't reach the leader:

- Follower continues serving `Get()` requests (stale reads)
- After ElectionTimeout, follower becomes candidate
- If it can reach quorum, it may trigger new election
- If isolated (can't reach quorum), it remains candidate indefinitely

#### Split Brain Protection

Raft prevents split-brain by requiring quorum for writes:

```
3-node cluster splits into [A] and [B, C]

Partition [A] (1 node):
  - Can't reach quorum (needs 2)
  - Writes fail, reads work (stale)
  - A steps down as leader

Partition [B, C] (2 nodes):
  - Has quorum (2 of 3)
  - Elects new leader
  - Writes succeed
  - Cluster continues operating
```

### Cross-Region Deployment

1. **Use appropriate timeout presets**: Start with `PresetRegional` for multi-region and adjust based on observed latencies
2. **Monitor election frequency**: Frequent elections indicate timeouts are too aggressive
3. **Consider read replicas**: Use non-voting members in remote regions for read scaling
4. **Snapshot tuning**: Increase snapshot intervals for high-latency networks

### Handling Leader Changes

```go
// Watch for leadership changes
go func() {
    for {
        select {
        case isLeader := <-cluster.LeaderCh():
            if isLeader {
                log.Println("Became leader")
                // Initialize leader-specific tasks
            } else {
                log.Println("Lost leadership")
                // Clean up leader-specific tasks
            }
        case <-ctx.Done():
            return
        }
    }
}()
```

## Implementation Plan

### Package Structure

```
pkg/raft/
├── cluster.go          # Cluster implementation
├── cluster_test.go     # Cluster unit tests
├── config.go           # Configuration types
├── config_test.go      # Config unit tests
├── errors.go           # Error definitions
├── fsm.go              # Built-in KV FSM
├── fsm_test.go         # FSM unit tests
├── interfaces.go       # All interface definitions
├── membership.go       # Membership management
├── membership_test.go  # Membership unit tests
├── options.go          # Functional options
├── options_test.go     # Options unit tests
├── storage.go          # Storage adapters for node.Store()
├── storage_test.go     # Storage unit tests
├── transport.go        # Libp2p transport wrapper
├── transport_test.go   # Transport unit tests
└── RAFT.md             # This documentation
```

### Testing Requirements

| Requirement | Target |
|-------------|--------|
| **Unit test coverage** | > 80% |
| **All public APIs** | Must have tests |
| **All error paths** | Must be tested |
| **Mocks for dependencies** | Required |

### Interface-Driven Design

All external dependencies must be behind interfaces for testability:

```go
// interfaces.go

// Node abstracts the peer.Node for testing
type Node interface {
    ID() peer.ID
    Peer() host.Host
    Store() datastore.Batching
    Discovery() discovery.Discovery
    Context() context.Context
}

// Transport abstracts the Raft transport layer
type Transport interface {
    LocalAddr() raft.ServerAddress
    AppendEntriesPipeline(id raft.ServerID, target raft.ServerAddress) (raft.AppendPipeline, error)
    AppendEntries(id raft.ServerID, target raft.ServerAddress, args *raft.AppendEntriesRequest, resp *raft.AppendEntriesResponse) error
    RequestVote(id raft.ServerID, target raft.ServerAddress, args *raft.RequestVoteRequest, resp *raft.RequestVoteResponse) error
    InstallSnapshot(id raft.ServerID, target raft.ServerAddress, args *raft.InstallSnapshotRequest, resp *raft.InstallSnapshotResponse, data io.Reader) error
    EncodePeer(id raft.ServerID, addr raft.ServerAddress) []byte
    DecodePeer([]byte) raft.ServerAddress
    SetHeartbeatHandler(cb func(rpc raft.RPC))
    TimeoutNow(id raft.ServerID, target raft.ServerAddress, args *raft.TimeoutNowRequest, resp *raft.TimeoutNowResponse) error
    Close() error
}

// LogStore abstracts Raft log storage
type LogStore interface {
    FirstIndex() (uint64, error)
    LastIndex() (uint64, error)
    GetLog(index uint64, log *raft.Log) error
    StoreLog(log *raft.Log) error
    StoreLogs(logs []*raft.Log) error
    DeleteRange(min, max uint64) error
}

// StableStore abstracts Raft stable storage
type StableStore interface {
    Set(key []byte, val []byte) error
    Get(key []byte) ([]byte, error)
    SetUint64(key []byte, val uint64) error
    GetUint64(key []byte) (uint64, error)
}

// SnapshotStore abstracts snapshot storage
type SnapshotStore interface {
    Create(version raft.SnapshotVersion, index, term uint64, configuration raft.Configuration, configurationIndex uint64, trans raft.Transport) (raft.SnapshotSink, error)
    List() ([]*raft.SnapshotMeta, error)
    Open(id string) (*raft.SnapshotMeta, io.ReadCloser, error)
}

// FSM is the finite state machine interface
type FSM interface {
    Apply(log *raft.Log) FSMResponse
    Snapshot() (FSMSnapshot, error)
    Restore(snapshot io.ReadCloser) error
}

// FSMResponse is the typed response from FSM.Apply
type FSMResponse struct {
    Error error
    Data  []byte
}

// FSMSnapshot for creating snapshots
type FSMSnapshot interface {
    Persist(sink raft.SnapshotSink) error
    Release()
}
```

### Type Safety - Avoid `any`/`interface{}`

**❌ Avoid:**
```go
// Bad - untyped
func (c *cluster) Apply(cmd []byte, timeout time.Duration) (interface{}, error)

// Bad - loses type info
type Command struct {
    Data interface{} `json:"data"`
}
```

**✅ Prefer:**
```go
// Good - typed response
func (c *cluster) Apply(cmd []byte, timeout time.Duration) (FSMResponse, error)

// Good - specific command types
type SetCommand struct {
    Key   string `cbor:"1,keyasint"`
    Value []byte `cbor:"2,keyasint"`
}

type DeleteCommand struct {
    Key string `cbor:"1,keyasint"`
}

// Command uses tagged union pattern
type Command struct {
    Type   CommandType   `cbor:"1,keyasint"`
    Set    *SetCommand   `cbor:"2,keyasint,omitempty"`
    Delete *DeleteCommand `cbor:"3,keyasint,omitempty"`
}
```

### Mock Implementations for Testing

```go
// mock_test.go

type mockNode struct {
    id        peer.ID
    host      host.Host
    store     datastore.Batching
    discovery discovery.Discovery
    ctx       context.Context
}

func (m *mockNode) ID() peer.ID                    { return m.id }
func (m *mockNode) Peer() host.Host                { return m.host }
func (m *mockNode) Store() datastore.Batching     { return m.store }
func (m *mockNode) Discovery() discovery.Discovery { return m.discovery }
func (m *mockNode) Context() context.Context       { return m.ctx }

type mockTransport struct {
    localAddr raft.ServerAddress
    // ... mock fields for tracking calls
}

type mockLogStore struct {
    logs map[uint64]*raft.Log
    mu   sync.RWMutex
}

type mockStableStore struct {
    data map[string][]byte
    mu   sync.RWMutex
}
```

### Unit Test Examples

```go
// cluster_test.go

func TestCluster_New(t *testing.T) {
    node := newMockNode(t)
    
    cluster, err := New(node, "/raft/test")
    require.NoError(t, err)
    require.NotNil(t, cluster)
    
    assert.Equal(t, "/raft/test", cluster.Namespace())
    
    t.Cleanup(func() {
        require.NoError(t, cluster.Close())
    })
}

func TestCluster_Set_NotLeader(t *testing.T) {
    cluster := newFollowerCluster(t)
    
    err := cluster.Set("key", []byte("value"), time.Second)
    
    assert.ErrorIs(t, err, ErrNotLeader)
}

func TestCluster_Set_Success(t *testing.T) {
    cluster := newLeaderCluster(t)
    
    err := cluster.Set("key", []byte("value"), time.Second)
    require.NoError(t, err)
    
    val, ok := cluster.Get("key")
    assert.True(t, ok)
    assert.Equal(t, []byte("value"), val)
}

func TestCluster_Get_StaleRead(t *testing.T) {
    leader := newLeaderCluster(t)
    follower := newFollowerCluster(t)
    
    // Set on leader
    err := leader.Set("key", []byte("value"), time.Second)
    require.NoError(t, err)
    
    // Follower may not have it yet (stale)
    // This tests the eventual consistency behavior
    _, ok := follower.Get("key")
    // ok may be false initially - this is expected
    _ = ok
}

// fsm_test.go

func TestKVFSM_Apply_Set(t *testing.T) {
    store := newMockStore(t)
    fsm := NewKVFSM(store, "/raft/test/")
    
    cmd := Command{
        Type: CommandSet,
        Set:  &SetCommand{Key: "mykey", Value: []byte("myvalue")},
    }
    data, _ := cbor.Marshal(cmd)
    
    log := &raft.Log{Data: data, Index: 1}
    resp := fsm.Apply(log)
    
    assert.NoError(t, resp.Error)
    
    // Verify stored
    val, err := store.Get(context.Background(), datastore.NewKey("/raft/test/data/mykey"))
    require.NoError(t, err)
    assert.Equal(t, []byte("myvalue"), val)
}

func TestKVFSM_Apply_Delete(t *testing.T) {
    store := newMockStore(t)
    fsm := NewKVFSM(store, "/raft/test/")
    
    // First set
    _ = store.Put(context.Background(), datastore.NewKey("/raft/test/data/mykey"), []byte("value"))
    
    // Then delete
    cmd := Command{
        Type:   CommandDelete,
        Delete: &DeleteCommand{Key: "mykey"},
    }
    data, _ := cbor.Marshal(cmd)
    
    log := &raft.Log{Data: data, Index: 2}
    resp := fsm.Apply(log)
    
    assert.NoError(t, resp.Error)
    
    // Verify deleted
    _, err := store.Get(context.Background(), datastore.NewKey("/raft/test/data/mykey"))
    assert.ErrorIs(t, err, datastore.ErrNotFound)
}

func TestKVFSM_Snapshot_Restore(t *testing.T) {
    store1 := newMockStore(t)
    fsm1 := NewKVFSM(store1, "/raft/test/")
    
    // Add some data
    _ = store1.Put(context.Background(), datastore.NewKey("/raft/test/data/key1"), []byte("val1"))
    _ = store1.Put(context.Background(), datastore.NewKey("/raft/test/data/key2"), []byte("val2"))
    
    // Create snapshot
    snap, err := fsm1.Snapshot()
    require.NoError(t, err)
    
    // Write to buffer
    var buf bytes.Buffer
    sink := &mockSnapshotSink{Writer: &buf}
    err = snap.Persist(sink)
    require.NoError(t, err)
    
    // Restore to new FSM
    store2 := newMockStore(t)
    fsm2 := NewKVFSM(store2, "/raft/test/")
    
    err = fsm2.Restore(io.NopCloser(&buf))
    require.NoError(t, err)
    
    // Verify data restored
    val, err := store2.Get(context.Background(), datastore.NewKey("/raft/test/data/key1"))
    require.NoError(t, err)
    assert.Equal(t, []byte("val1"), val)
}

// storage_test.go

func TestLogStore_StoreAndRetrieve(t *testing.T) {
    store := newMockDatastore(t)
    logStore := NewLogStore(store, "/raft/test/log/")
    
    log := &raft.Log{
        Index: 1,
        Term:  1,
        Type:  raft.LogCommand,
        Data:  []byte("test data"),
    }
    
    err := logStore.StoreLog(log)
    require.NoError(t, err)
    
    var retrieved raft.Log
    err = logStore.GetLog(1, &retrieved)
    require.NoError(t, err)
    
    assert.Equal(t, log.Index, retrieved.Index)
    assert.Equal(t, log.Term, retrieved.Term)
    assert.Equal(t, log.Data, retrieved.Data)
}

func TestLogStore_FirstLastIndex(t *testing.T) {
    store := newMockDatastore(t)
    logStore := NewLogStore(store, "/raft/test/log/")
    
    // Empty initially
    first, err := logStore.FirstIndex()
    require.NoError(t, err)
    assert.Equal(t, uint64(0), first)
    
    // Add logs
    for i := uint64(5); i <= 10; i++ {
        _ = logStore.StoreLog(&raft.Log{Index: i, Term: 1})
    }
    
    first, _ = logStore.FirstIndex()
    last, _ := logStore.LastIndex()
    
    assert.Equal(t, uint64(5), first)
    assert.Equal(t, uint64(10), last)
}
```

### Running Tests

```bash
# Run all tests with coverage
go test -v -race -coverprofile=coverage.out ./pkg/raft/...

# Check coverage meets threshold
go tool cover -func=coverage.out | grep total | awk '{print $3}' | sed 's/%//' | \
  awk '{if ($1 < 80) exit 1}'

# Generate HTML coverage report
go tool cover -html=coverage.out -o coverage.html
```

### Error Types

Define specific error types instead of generic errors:

```go
// errors.go

var (
    // ErrNotLeader is returned when a write is attempted on a non-leader
    ErrNotLeader = errors.New("not leader")
    
    // ErrNoLeader is returned when no leader is known
    ErrNoLeader = errors.New("no leader")
    
    // ErrTimeout is returned when an operation times out
    ErrTimeout = errors.New("operation timeout")
    
    // ErrShutdown is returned when the cluster is shutting down
    ErrShutdown = errors.New("cluster shutdown")
    
    // ErrKeyNotFound is returned when a key doesn't exist
    ErrKeyNotFound = errors.New("key not found")
    
    // ErrInvalidCommand is returned for malformed commands
    ErrInvalidCommand = errors.New("invalid command")
)
```

## Dependencies

```go
require (
    github.com/hashicorp/raft v1.7.3
    github.com/libp2p/go-libp2p-raft v0.4.0
    // Uses existing: github.com/libp2p/go-libp2p (already in tau)
    // Uses existing: node.Store() for all storage (no raft-boltdb needed)
)
```

## Related Packages

- `p2p/peer`: Core libp2p node infrastructure
- `p2p/streams`: Stream-based RPC for p2p communication  
- `pkg/kvdb`: CRDT-based eventually consistent key-value store (for contrast)

## References

- [Raft Paper](https://raft.github.io/raft.pdf) - Original Raft consensus paper
- [HashiCorp Raft](https://github.com/hashicorp/raft) - Go implementation
- [go-libp2p-raft](https://github.com/libp2p/go-libp2p-raft) - libp2p transport wrapper
- [Raft Visualization](https://raft.github.io/) - Interactive Raft visualization
