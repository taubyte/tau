# Test Coverage Improvement Plan

**Current Coverage:** 76.2%  
**Target Coverage:** 85%+

This document outlines areas where test coverage can be improved. Functions are organized by file and priority.

---

## Priority 1: Critical Path Functions (< 70% coverage)

### `cluster.go`

#### `requestVoterJoin()` - 0% coverage
**Location:** `cluster.go:334-366`  
**Why Important:** Background goroutine that handles late-joiner scenarios and retry logic for joining clusters.

**Test Scenarios:**
- Test that `requestVoterJoin` starts discovery and exchange in background
- Test leader preference logic (targets leader when known, falls back to `voterJoinTargets()`)
- Test timeout behavior (10 second context timeout)
- Test that it retries every 500ms
- Test early exit when context is cancelled
- Test behavior when `raftClient` or `tracker` is nil (early return)
- Test successful join path
- Test error handling paths

**Key Code Paths:**
```go
// Line 335-337: Early return if client/tracker nil
// Line 355-359: Leader preference logic
// Line 361: JoinVoter call with targets
```

#### `voterJoinTargets()` - 0% coverage
**Location:** `cluster.go:445-447`  
**Why Important:** Helper that determines which peers to target for join attempts.

**Test Scenarios:**
- Test that it calls `protocolPeers` with correct namespace protocol
- Test that it includes tracker peers (`includeTracker=true`)
- Test with empty peer list
- Test with peers that don't support protocol (filtered out)

#### `raftProtocolPeers()` - 0% coverage
**Location:** `cluster.go:449-451`  
**Why Important:** Helper that finds peers supporting the transport protocol.

**Test Scenarios:**
- Test that it calls `protocolPeers` with transport protocol
- Test that it excludes tracker peers (`includeTracker=false`)
- Test protocol filtering logic

#### `protocolPeers()` - 0% coverage
**Location:** `cluster.go:410-443`  
**Why Important:** Core function that filters peers by protocol support.

**Test Scenarios:**
- Test with `includeTracker=true` (includes tracker peers)
- Test with `includeTracker=false` (only network peers)
- Test protocol filtering (peers without protocol are excluded)
- Test error handling from `SupportsProtocols` (continues on error)
- Test deduplication (same peer from tracker and network)
- Test self-exclusion (self ID is filtered out)
- Test empty result when no peers support protocol

#### `handleBootstrap()` - 60% coverage
**Location:** `cluster.go:145-253`  
**Why Important:** Core bootstrap logic with multiple decision paths.

**Missing Test Scenarios:**
- Test early join path (line 176-179) - joining existing cluster during discovery
- Test late joiner path (line 188-190) - `isLateJoiner` returns true
- Test multiple founders path (line 198-219) - all edge cases:
  - Successful join when cluster exists
  - `requestVoterJoin` when leader exists but join fails
  - Bootstrap with peers when no leader
  - `ErrCantBootstrap` handling
- Test single founder with peers path (line 224-240):
  - `raftProtocolPeers()` returns peers
  - Join succeeds
  - Join fails but leader exists
  - No leader scenario
- Test truly alone path (line 245-251):
  - Successful bootstrap
  - `ErrCantBootstrap` handling

---

### `client.go`

#### `JoinVoter()` - 64.1% coverage
**Location:** `client.go:199-266`  
**Why Important:** Client method for joining cluster as voter.

**Missing Test Scenarios:**
- Test encryption path (lines 205-211) - encrypted body creation
- Test decryption path (lines 245-254) - encrypted response decryption
- Test decryption error handling (lines 246-252)
- Test `sawNoLeader` path (lines 233-234, 259-260)
- Test "no responses" error path (line 265)
- Test multiple error aggregation (firstErr handling)
- Test with empty peers list (no `To()` option)
- Test with single peer (threshold=1)
- Test with multiple peers (only one needs to respond)

#### `ExchangePeers()` - 54.8% coverage
**Location:** `client.go:322-383`  
**Why Important:** Peer discovery exchange mechanism.

**Missing Test Scenarios:**
- Test encryption path for request body
- Test decryption path for response
- Test decryption error handling
- Test error aggregation from multiple peers
- Test timeout handling
- Test with empty peer list
- Test successful exchange with multiple peers
- Test partial failures (some peers succeed, some fail)

#### `sendCommand()` - 61.9% coverage
**Location:** `client.go:384-418`  
**Why Important:** Internal helper for sending commands.

**Missing Test Scenarios:**
- Test encryption path
- Test decryption path
- Test error handling paths
- Test timeout scenarios
- Test with different command types

---

## Priority 2: Discovery Functions (0% coverage)

### `discovery.go`

All peer tracker helper functions have 0% coverage. These are critical for bootstrap logic.

#### `newPeerTracker()` - 0% coverage
**Location:** `discovery.go:24-36`  
**Test Scenarios:**
- Test initialization with self ID
- Test that self ID is excluded from peer tracking

#### `addPeer()` - 0% coverage
**Location:** `discovery.go:38-51`  
**Test Scenarios:**
- Test adding new peer
- Test that self ID is ignored
- Test duplicate peer handling (doesn't overwrite existing)
- Test `lastChangeTime` update

#### `mergePeers()` - 0% coverage
**Location:** `discovery.go:53-88`  
**Test Scenarios:**
- Test merging new peers
- Test time normalization (their start time vs our start time)
- Test that earlier seen times are preserved
- Test `lastChangeTime` update when peers change
- Test invalid peer ID handling (skipped)
- Test self ID exclusion
- Test empty merge (no new peers)

#### `getPeersMap()` - 0% coverage
**Location:** `discovery.go:90-100`  
**Test Scenarios:**
- Test map creation with peer IDs and timestamps
- Test self ID exclusion
- Test empty peer list
- Test timestamp formatting (milliseconds since start)

#### `isLateJoiner()` - 0% coverage
**Location:** `discovery.go:120-137`  
**Test Scenarios:**
- Test returns false when ≤1 peer
- Test returns false when any peer seen before threshold
- Test returns true when all peers seen after threshold
- Test self ID exclusion in check

#### `allPeers()` - 0% coverage
**Location:** `discovery.go:140-151`  
**Test Scenarios:**
- Test returns all peers except self
- Test empty result when only self
- Test ordering (if any)

#### `peerCount()` - 0% coverage
**Location:** `discovery.go:154-158`  
**Test Scenarios:**
- Test count includes self
- Test count with multiple peers
- Test count with no peers (just self)

#### `isStable()` - 0% coverage
**Location:** `discovery.go:161-165`  
**Test Scenarios:**
- Test returns true when `lastChangeTime` is old enough
- Test returns false when recently changed
- Test with different stability durations

#### `dialPeer()` - 0% coverage
**Location:** `discovery.go:236-239`  
**Test Scenarios:**
- Test peer connection attempt
- Test error handling (connection failures are ignored)

#### `runDiscoveryAndExchange()` - Partial coverage
**Location:** `discovery.go:167-234`  
**Test Scenarios:**
- Test protocol filtering (`supportsRaftProtocol`)
- Test network peer discovery
- Test discovery service peer discovery
- Test peer exchange with known peers
- Test context cancellation
- Test ticker interval (100ms)
- Test that only protocol-supporting peers are added
- Test error handling in `ExchangePeers` (continues on error)

---

## Priority 3: Encryption Functions (0% coverage)

### `encryption.go`

All encryption/decryption functions have 0% coverage. These are critical for secure communication.

#### `Read()` - 0% coverage
**Location:** `encryption.go:41-69`  
**Test Scenarios:**
- Test reading encrypted data
- Test nonce extraction
- Test decryption
- Test error handling (invalid data, decryption failures)
- Test partial reads
- Test EOF handling

#### `Write()` - 0% coverage
**Location:** `encryption.go:71-95`  
**Test Scenarios:**
- Test writing plaintext data
- Test encryption
- Test nonce generation
- Test error handling
- Test partial writes

#### `encryptBody()` - 0% coverage
**Location:** `encryption.go:97-118`  
**Test Scenarios:**
- Test CBOR marshaling
- Test nonce generation
- Test encryption
- Test error handling (marshaling failures, encryption failures)
- Test empty body
- Test various body types

#### `decryptBody()` - 0% coverage
**Location:** `encryption.go:121-154`  
**Test Scenarios:**
- Test decryption with valid encrypted data
- Test nonce extraction
- Test CBOR unmarshaling
- Test error handling:
  - Missing "data" key
  - Invalid data type
  - Data too short (< nonceSize)
  - Decryption failures
  - Unmarshaling failures
- Test nil cipher handling

#### `encryptResponse()` - 0% coverage
**Location:** `encryption.go:157-178`  
**Test Scenarios:**
- Test CBOR marshaling
- Test nonce generation
- Test encryption
- Test error handling
- Test empty response

#### `decryptResponse()` - 0% coverage
**Location:** `encryption.go:181-203`  
**Test Scenarios:**
- Test decryption with valid encrypted data
- Test error handling (similar to `decryptBody`)
- Test nil cipher handling

---

## Priority 4: Transport Functions (0% coverage)

### `transport.go`

All transport logging and connection functions have 0% coverage. These are lower priority but still important for completeness.

#### Logger Functions (0% coverage)
**Locations:** `transport.go:100-126`  
- `With()`, `Named()`, `ResetNamed()`, `SetLevel()`, `GetLevel()`
- `StandardLogger()`, `StandardWriter()`, `ImpliedArgs()`

**Test Scenarios:**
- Test logger configuration
- Test level setting/getting
- Test name management
- Test standard logger/writer creation

#### Connection Functions (0% coverage)
**Locations:** `transport.go:153-201`  
- `Dial()`, `Accept()`, `Addr()`, `Close()`, `ServerAddr()`

**Test Scenarios:**
- Test dialing peers
- Test accepting connections
- Test address retrieval
- Test graceful shutdown
- Test error handling

#### Logging Functions (0% coverage)
**Locations:** `transport.go:29-96`  
- `formatArgs()`, `format()`, `Log()`, `Trace()`, `Debug()`, `Info()`, `Warn()`, `Error()`
- `IsTrace()`, `IsDebug()`, `IsInfo()`, `IsWarn()`, `IsError()`, `Name()`

**Test Scenarios:**
- Test log formatting
- Test different log levels
- Test level checking functions
- Test argument formatting

---

## Priority 5: Partially Covered Functions (70-80%)

### `cluster.go`

#### `Set()` - 68.8% coverage
**Missing:** Error paths, edge cases

#### `Delete()` - 62.5% coverage
**Missing:** Error paths, not-leader scenarios

#### `Apply()` - 76.5% coverage
**Missing:** Some error paths, timeout scenarios

#### `bootstrapSelf()` - 75% coverage
**Missing:** `ErrCantBootstrap` path

#### `bootstrapWithPeers()` - 90.9% coverage
**Missing:** Edge cases with empty peer list

### `client.go`

#### `Get()` - 82.9% coverage
**Missing:** Encryption paths, error scenarios

#### `Delete()` - 86.7% coverage
**Missing:** Some error paths

#### `Keys()` - 83.3% coverage
**Missing:** Encryption paths, error scenarios

#### `Set()` - 86.7% coverage
**Missing:** Some error paths

---

## Testing Strategy Recommendations

### 1. Unit Tests for Helpers
- Create focused unit tests for `discovery.go` functions using mocks
- Test `encryption.go` functions with known plaintext/ciphertext pairs
- Test `protocolPeers()` with mocked peerstore

### 2. Integration Tests for Bootstrap
- Test all `handleBootstrap()` paths with controlled scenarios
- Use test fixtures to simulate different peer discovery timings
- Test late joiner scenarios with delayed peer discovery

### 3. Error Injection Tests
- Test encryption/decryption error paths
- Test network failures in `requestVoterJoin()`
- Test protocol mismatches in `protocolPeers()`

### 4. Concurrent Tests
- Test `requestVoterJoin()` concurrent behavior
- Test `runDiscoveryAndExchange()` with concurrent peer additions
- Test race conditions in peer tracker

### 5. Edge Cases
- Empty peer lists
- Nil clients/trackers
- Invalid peer IDs
- Timeout scenarios
- Context cancellation

---

## Estimated Coverage Impact

| Priority | Functions | Estimated Coverage Gain |
|----------|-----------|----------------------|
| Priority 1 | 8 functions | +8-10% |
| Priority 2 | 9 functions | +5-7% |
| Priority 3 | 6 functions | +3-4% |
| Priority 4 | 20+ functions | +2-3% |
| Priority 5 | 8 functions | +1-2% |
| **Total** | **51+ functions** | **+19-26%** |

**Projected Final Coverage:** 95-100% (if all priorities completed)

---

## Quick Wins (Highest ROI)

1. **`protocolPeers()` and helpers** - Used by bootstrap logic, easy to test with mocks
2. **`isLateJoiner()` and `getFoundingMembers()`** - Simple logic, high impact
3. **Encryption error paths** - Critical for security, straightforward to test
4. **`requestVoterJoin()` basic paths** - Core functionality, testable with integration tests

---

## Notes

- Many 0% coverage functions in `transport.go` are logging wrappers - lower priority
- `discovery.go` functions are called indirectly through bootstrap - need integration tests
- Encryption functions need test vectors (known plaintext/ciphertext)
- Some functions are only called in error paths - need error injection tests
