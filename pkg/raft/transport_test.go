package raft

import (
	"bufio"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/raft"
	logging "github.com/ipfs/go-log/v2"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/require"
	taupeer "github.com/taubyte/tau/p2p/peer"
)

// transportLog is what the raft-transport logger printed while a test ran.
type transportLog struct {
	mu    sync.Mutex
	lines []map[string]any
}

// captureTransportLog collects the raft-transport logger's lines at every
// level until the test ends, then puts its level back.
func captureTransportLog(t *testing.T) *transportLog {
	t.Helper()
	previous, err := logging.SubsystemLevelName("raft-transport")
	require.NoError(t, err)
	require.NoError(t, logging.SetLogLevel("raft-transport", "debug"))

	pipe := logging.NewPipeReader()
	captured := &transportLog{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		scanner := bufio.NewScanner(pipe)
		scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
		for scanner.Scan() {
			var line map[string]any
			if json.Unmarshal(scanner.Bytes(), &line) != nil || line["logger"] != "raft-transport" {
				continue
			}
			captured.mu.Lock()
			captured.lines = append(captured.lines, line)
			captured.mu.Unlock()
		}
	}()
	t.Cleanup(func() {
		pipe.Close()
		<-done
		logging.SetLogLevel("raft-transport", previous)
	})
	return captured
}

// line is the level and message of the line logged with marker in it.
func (l *transportLog) line(t *testing.T, marker string) (level, msg string) {
	t.Helper()
	require.Eventually(t, func() bool {
		l.mu.Lock()
		defer l.mu.Unlock()
		for _, line := range l.lines {
			if text := fmt.Sprint(line["msg"]); strings.Contains(text, marker) {
				level, msg = fmt.Sprint(line["level"]), text
				return true
			}
		}
		return false
	}, 5*time.Second, 5*time.Millisecond, "nothing was logged with %q", marker)
	return level, msg
}

// TestTransportLogger_LogsEachLevelAtItsOwn: raft's transport logs through
// hclog, and each of its levels lands on go-log's nearest; a level the adapter
// does not know is a warning rather than a line dropped.
func TestTransportLogger_LogsEachLevelAtItsOwn(t *testing.T) {
	captured := captureTransportLog(t)
	var lg hclog.Logger = &hcLogToLogger{}

	for _, tc := range []struct {
		level hclog.Level
		want  string
	}{
		{hclog.Trace, "debug"},
		{hclog.Debug, "debug"},
		{hclog.NoLevel, "info"},
		{hclog.Info, "info"},
		{hclog.Warn, "warn"},
		{hclog.Error, "error"},
		{hclog.Level(42), "warn"},
	} {
		marker := fmt.Sprintf("%s-log-%d", t.Name(), tc.level)
		lg.Log(tc.level, marker)
		level, _ := captured.line(t, marker)
		require.Equal(t, tc.want, level, "hclog level %d", tc.level)
	}

	for _, tc := range []struct {
		name string
		log  func(string, ...interface{})
		want string
	}{
		{"trace", lg.Trace, "debug"},
		{"debug", lg.Debug, "debug"},
		{"info", lg.Info, "info"},
		{"warn", lg.Warn, "warn"},
		{"error", lg.Error, "error"},
	} {
		marker := fmt.Sprintf("%s-%s", t.Name(), tc.name)
		tc.log(marker)
		level, _ := captured.line(t, marker)
		require.Equal(t, tc.want, level, tc.name)
	}
}

// TestTransportLogger_CarriesItsNameAndArgs: a logger made by Named or With
// prints its name and its pairs on every line, as hclog's own do, and passes
// both to the loggers made from it.
func TestTransportLogger_CarriesItsNameAndArgs(t *testing.T) {
	captured := captureTransportLog(t)
	var root hclog.Logger = &hcLogToLogger{}
	require.Empty(t, root.Name())
	require.Empty(t, root.ImpliedArgs())

	conn := root.Named("net").With("peer", "p1").Named("conn").With("stream", 7)
	require.Equal(t, "net: conn", conn.Name())
	require.Equal(t, []interface{}{"peer", "p1", "stream", 7}, conn.ImpliedArgs())

	marker := t.Name() + "-named"
	conn.Warn(marker, "error", "refused")
	_, msg := captured.line(t, marker)
	require.Equal(t, "net: conn: "+marker+". Args: peer=p1. stream=7. error=refused. ", msg)

	other := conn.ResetNamed("other")
	require.Equal(t, "other", other.Name())
	require.Equal(t, conn.ImpliedArgs(), other.ImpliedArgs())

	marker = t.Name() + "-odd"
	root.Error(marker, 42, "skipped", "kept", "yes", "dangling")
	_, msg = captured.line(t, marker)
	require.Equal(t, marker+". Args: kept=yes. EXTRA_VALUE_AT_END=dangling. ", msg,
		"a pair whose key is not a string is skipped, and a value left without a key keeps hclog's")

	require.Equal(t, []interface{}{"k", "v", hclog.MissingKey, "dangling"}, root.With("k", "v", "dangling").ImpliedArgs())
}

// TestTransportLogger_LeavesLevelsToGoLog: what is printed is decided per
// subsystem by go-log, so the adapter claims every level and ignores hclog's
// setting, and it offers no standard library logger in its place.
func TestTransportLogger_LeavesLevelsToGoLog(t *testing.T) {
	var lg hclog.Logger = &hcLogToLogger{}
	require.True(t, lg.IsTrace())
	require.True(t, lg.IsDebug())
	require.True(t, lg.IsInfo())
	require.True(t, lg.IsWarn())
	require.True(t, lg.IsError())

	lg.SetLevel(hclog.Error)
	require.Equal(t, hclog.Debug, lg.GetLevel())

	require.Nil(t, lg.StandardLogger(nil))
	require.Nil(t, lg.StandardWriter(nil))
}

// TestTransport_DialsOnlyAPeer: raft dials server addresses, and the
// transport's are peer ids; anything else is refused before a stream is
// opened, as is a dial on a layer that was never built.
func TestTransport_DialsOnlyAPeer(t *testing.T) {
	_, err := (&namespaceStreamLayer{}).Dial(raft.ServerAddress(newMockNode(t).ID().String()), time.Second)
	require.ErrorContains(t, err, "not initialized")

	layer, err := newNamespaceStreamLayer(newMockNode(t).Peer(), "transport-dial", nil)
	require.NoError(t, err)
	defer layer.Close()

	conn, err := layer.Dial("not-a-peer-id", time.Second)
	require.Error(t, err)
	require.Nil(t, conn)
}

// TestCluster_AnEncryptedPairReplicates: with a key, every raft message is
// sealed on the way out and opened on the way in, and a pair keyed alike
// elects, joins and replicates as an unkeyed one does.
func TestCluster_AnEncryptedPairReplicates(t *testing.T) {
	const namespace = "encrypted-pair"
	key := make([]byte, AESKeySize)
	_, err := rand.Read(key)
	require.NoError(t, err)

	nodeA := newMockNode(t)
	nodeB := newMockNode(t)
	require.NoError(t, taupeer.LinkAllPeers())

	a, err := New(nodeA, namespace, WithEncryptionKey(key), WithTimeouts(testTimeoutConfig()),
		WithForceBootstrap(), WithSnapshotRoot(t.TempDir()))
	require.NoError(t, err)
	defer a.Close()
	waitLeader(t, a, 5*time.Second)
	require.NoError(t, nodeB.Peer().Connect(t.Context(), peer.AddrInfo{ID: nodeA.ID(), Addrs: nodeA.Peer().Addrs()}))

	b, err := New(nodeB, namespace, WithEncryptionKey(key), WithTimeouts(testTimeoutConfig()),
		WithBootstrapTimeout(2*time.Second), WithSnapshotRoot(t.TempDir()))
	require.NoError(t, err)
	defer b.Close()

	require.Eventually(t, func() bool {
		members, err := a.Members()
		return err == nil && len(members) == 2
	}, 10*time.Second, 20*time.Millisecond, "b never joined a")

	value := make([]byte, 8*1024)
	_, err = rand.Read(value)
	require.NoError(t, err)
	require.NoError(t, onLeader(t, []Cluster{a, b}, func(l Cluster) error {
		return l.Set("sealed", value, 5*time.Second)
	}))

	for _, member := range []Cluster{a, b} {
		require.Eventually(t, func() bool {
			got, found := member.Get("sealed")
			return found && string(got) == string(value)
		}, leaderWait, 20*time.Millisecond, "%s never applied the write", idOf(member).ShortString())
	}
}
