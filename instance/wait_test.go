package instance

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	mocknet "github.com/libp2p/go-libp2p/p2p/net/mock"
	"github.com/libp2p/go-libp2p/p2p/protocol/identify"
	"github.com/stretchr/testify/require"
)

// A fleet's waits say why they gave up: a node with no gossip stream from the reporter
// never heard it (a missed identify push), while one with streams both ways in a
// stalled cgroup heard it too late. Each diagnosis is a Stringer, read when the wait
// gives up.

// gossipWith is which way a host's gossip runs with each peer it is connected
// to, and whether its peerstore has the peer gossiping, from identify or from a
// stream it opened.
type gossipWith struct{ h host.Host }

func (g gossipWith) String() string {
	var b strings.Builder
	for _, p := range g.h.Network().Peers() {
		to, from := false, false
		for _, c := range g.h.Network().ConnsToPeer(p) {
			for _, s := range c.GetStreams() {
				if !slices.Contains(pubsub.GossipSubDefaultProtocols, s.Protocol()) {
					continue
				}
				if s.Stat().Direction == network.DirOutbound {
					to = true
				} else {
					from = true
				}
			}
		}
		told, err := g.h.Peerstore().SupportsProtocols(p, pubsub.GossipSubDefaultProtocols...)
		gossips := strconv.FormatBool(len(told) > 0)
		if err != nil {
			gossips = err.Error()
		}
		fmt.Fprintf(&b, "\ngossip with %s: its peerstore says it gossips %s, a stream to it %v, a stream from it %v", p, gossips, to, from)
	}
	return b.String()
}

// viewOf is every node a node's discovery view holds, and how stale.
type viewOf struct{ n *Instance }

func (v viewOf) String() string {
	var b strings.Builder
	for _, row := range v.n.Discovery().All() {
		fmt.Fprintf(&b, "\nview: %s serving %v, %s old", row.Peer, row.Serving, row.Age.Round(time.Millisecond))
	}
	return b.String()
}

type stallCounter struct {
	parent    bool
	file, key string
}

// stallCounters are what the kernel counts against this test's cgroup and its
// parent, which under scripts/cap are the run's scope and the shared slice: how
// often memory.high was crossed, how long tasks waited on memory and on a CPU,
// and how long the slice's CPU quota held them back.
var stallCounters = []stallCounter{
	{false, "memory.events.local", "high"},
	{true, "memory.events.local", "high"},
	{false, "memory.pressure", "some"},
	{false, "memory.pressure", "full"},
	{false, "cpu.pressure", "some"},
	{true, "cpu.stat", "throttled_usec"},
}

// stall is how much of each counter a wait used, from when it began.
type stall struct {
	dir    string
	before []int64
	err    []error
}

// stallSince starts counting for the cgroup this process runs in.
func stallSince() *stall {
	b, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		return &stall{err: []error{err}}
	}
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if own, ok := strings.CutPrefix(line, "0::"); ok {
			return stallIn(filepath.Join("/sys/fs/cgroup", own))
		}
	}
	return &stall{err: []error{fmt.Errorf("/proc/self/cgroup has no cgroup v2 entry")}}
}

func stallIn(dir string) *stall {
	s := &stall{dir: dir}
	s.before, s.err = s.read()
	return s
}

func (s *stall) read() ([]int64, []error) {
	values := make([]int64, len(stallCounters))
	errs := make([]error, len(stallCounters))
	for i, c := range stallCounters {
		dir := s.dir
		if c.parent {
			dir = filepath.Dir(dir)
		}
		values[i], errs[i] = counter(filepath.Join(dir, c.file), c.key)
	}
	return values, errs
}

// counter is a pressure file's total for one line, or a flat-keyed file's
// value for one key.
func counter(path, key string) (int64, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) < 2 || f[0] != key {
			continue
		}
		v := f[1]
		if total, ok := strings.CutPrefix(f[len(f)-1], "total="); ok {
			v = total
		}
		return strconv.ParseInt(v, 10, 64)
	}
	return 0, fmt.Errorf("%s has no %s", path, key)
}

func (s *stall) String() string {
	if s.dir == "" {
		return fmt.Sprintf("\ncgroup unknown: %v", s.err[0])
	}
	now, errs := s.read()
	var out []string
	for i, c := range stallCounters {
		name := c.file + " " + c.key
		if c.parent {
			name = "../" + name
		}
		switch {
		case s.err[i] != nil:
			out = append(out, fmt.Sprintf("%s unreadable: %v", name, s.err[i]))
		case errs[i] != nil:
			out = append(out, fmt.Sprintf("%s unreadable: %v", name, errs[i]))
		case strings.HasSuffix(c.file, ".pressure") || strings.HasSuffix(c.key, "_usec"):
			out = append(out, fmt.Sprintf("%s +%s", name, (time.Duration(now[i]-s.before[i])*time.Microsecond).Round(time.Millisecond)))
		default:
			out = append(out, fmt.Sprintf("%s +%d", name, now[i]-s.before[i]))
		}
	}
	return fmt.Sprintf("\ncgroup %s during the wait: %s", s.dir, strings.Join(out, ", "))
}

func TestAStallIsReportedAsWhatTheWaitUsed(t *testing.T) {
	scope := filepath.Join(t.TempDir(), "tau-cap.slice", "tau-cap-1.scope")
	require.NoError(t, os.MkdirAll(scope, 0o755))
	write := func(dir, file, content string) {
		require.NoError(t, os.WriteFile(filepath.Join(dir, file), []byte(content), 0o644))
	}
	pressure := func(some, full int) string {
		return fmt.Sprintf("some avg10=0.00 avg60=0.00 avg300=0.00 total=%d\nfull avg10=0.00 avg60=0.00 avg300=0.00 total=%d\n", some, full)
	}
	write(scope, "memory.events.local", "low 0\nhigh 552\nmax 0\n")
	write(filepath.Dir(scope), "memory.events.local", "low 0\nhigh 7\nmax 0\n")
	write(scope, "memory.pressure", pressure(4864, 4864))
	write(scope, "cpu.pressure", pressure(202, 196))

	s := stallIn(scope)
	write(scope, "memory.events.local", "low 0\nhigh 76527\nmax 0\n")
	write(scope, "memory.pressure", pressure(10670156, 10667085))
	write(scope, "cpu.pressure", pressure(114057, 113780))

	require.Equal(t, "\ncgroup "+scope+" during the wait: "+
		"memory.events.local high +75975, ../memory.events.local high +0, "+
		"memory.pressure some +10.665s, memory.pressure full +10.662s, cpu.pressure some +114ms, "+
		"../cpu.stat throttled_usec unreadable: open "+filepath.Join(filepath.Dir(scope), "cpu.stat")+": no such file or directory",
		s.String())
}

func TestAGossipReportSaysWhichWayEachStreamRuns(t *testing.T) {
	mn := mocknet.New()
	t.Cleanup(func() { mn.Close() })

	late, deaf := newHost(t, mn), newHost(t, mn)

	deaf.RemoveStreamHandler(identify.IDPush)
	_, err := mn.ConnectPeers(late.ID(), deaf.ID())
	require.NoError(t, err)

	// deaf has identified late before late gossips, so only the push it
	// refuses could tell it.
	require.Eventually(t, func() bool {
		protos, err := deaf.Peerstore().GetProtocols(late.ID())
		return err == nil && slices.Contains(protos, identify.ID)
	}, 10*time.Second, 10*time.Millisecond)

	// deaf's router starts after the connection, so its first look at its peers
	// finds late and tries a stream, which late refuses. Started before, it may look
	// first and never try; and a try not yet refused when late gossips succeeds.
	// It also says when an RPC from late reaches it, which is late's stream up at
	// both ends. late lists deaf among its peers sooner, while that stream is still
	// being opened.
	reached := make(chan struct{})
	var once sync.Once
	refused := &refusals{Host: deaf, of: late.ID(), refused: make(chan struct{})}
	_, err = pubsub.NewGossipSub(t.Context(), refused, pubsub.WithAppSpecificRpcInspector(func(from peer.ID, _ *pubsub.RPC) error {
		if from == late.ID() {
			once.Do(func() { close(reached) })
		}
		return nil
	}))
	require.NoError(t, err)
	select {
	case <-refused.refused:
	case <-time.After(10 * time.Second):
		t.Fatal("deaf never tried a stream to late")
	}

	topic, err := newPubSub(t, late).Join("probe")
	require.NoError(t, err)
	sub, err := topic.Subscribe()
	require.NoError(t, err)
	t.Cleanup(sub.Cancel)
	select {
	case <-reached:
	case <-time.After(10 * time.Second):
		t.Fatal("nothing late sent ever reached deaf")
	}

	require.Equal(t, fmt.Sprintf("\ngossip with %s: its peerstore says it gossips false, a stream to it false, a stream from it true", late.ID()), gossipWith{deaf}.String())
	require.Equal(t, fmt.Sprintf("\ngossip with %s: its peerstore says it gossips true, a stream to it true, a stream from it false", deaf.ID()), gossipWith{late}.String())
}

// refusals is a host that says when a stream it opens to one peer is refused.
type refusals struct {
	host.Host
	of      peer.ID
	once    sync.Once
	refused chan struct{}
}

func (r *refusals) NewStream(ctx context.Context, p peer.ID, protos ...protocol.ID) (network.Stream, error) {
	s, err := r.Host.NewStream(ctx, p, protos...)
	if err != nil && p == r.of {
		r.once.Do(func() { close(r.refused) })
	}
	return s, err
}
