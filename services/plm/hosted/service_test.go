package hosted

import (
	"crypto/tls"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"
	gossh "golang.org/x/crypto/ssh"
	"gopkg.in/yaml.v3"

	"github.com/taubyte/tau/core/service"
	"github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/pkg/git/gittest"
	"github.com/taubyte/tau/pkg/raft"
	"github.com/taubyte/tau/services/plm/ingress"
	"github.com/taubyte/tau/services/plm/jobs"
)

// node is an instance as plm sees it: a peer, a directory, the groups it opens,
// and the section an operator wrote.
type node struct {
	peer.Node
	root    string
	running []service.Running
	section map[string]any
}

func (n *node) Peer() peer.Node            { return n.Node }
func (n *node) Running() []service.Running { return n.running }
func (n *node) Root() string               { return n.root }
func (*node) Cloud() string                { return "test" }
func (*node) Region() string               { return "" }
func (*node) Now() time.Time               { return time.Now() }

func (n *node) Config(into any) error {
	raw, err := yaml.Marshal(n.section)
	if err != nil {
		return err
	}
	return yaml.Unmarshal(raw, into)
}

// lone is a node running nothing beside build, its instance opening the group its
// queue is, as a lone build process is.
func lone(t *testing.T, section map[string]any) *node {
	t.Helper()
	p := peer.Mock(t.Context())
	t.Cleanup(p.Close)
	n := &node{Node: p, root: t.TempDir(), section: section}
	n.running = []service.Running{{Service: groupsOn{node: p, root: n.root}}}
	return n
}

func TestBuildStartsFromItsInstance(t *testing.T) {
	s := New()
	require.Nil(t, s.(Service).Addr(), "nothing bound before it starts")
	_, err := s.(Service).Built("shop", "prod", "QmNothing")
	require.ErrorIs(t, err, jobs.ErrNotStarted)
	require.ErrorIs(t, s.(Service).WaitForLeader(t.Context()), jobs.ErrNotStarted)
	require.NoError(t, s.Start(t.Context(), lone(t, map[string]any{"listen": "127.0.0.1:0", "cloudfqdn": "tau.example.io"})))
	t.Cleanup(func() { s.Close() })

	require.NoError(t, s.(Service).WaitForLeader(t.Context()), "it leads its own queue")
	_, err = s.(Service).Built("shop", "prod", "QmNothing")
	require.Equal(t, connect.CodeNotFound, connect.CodeOf(err), "and nothing is built")
	_, err = s.(Service).Local().Enqueue(t.Context(), "github.com/acme/app", "refs/heads/main", strings.Repeat("c", 40))
	require.Equal(t, connect.CodeUnavailable, connect.CodeOf(err), "asked in-process, with no register to say whose the repository is")

	addr := s.(Service).Addr()
	require.NotNil(t, addr)
	res, err := http.Get("http://" + addr.String() + "/cloud")
	require.NoError(t, err)
	res.Body.Close()
	require.Equal(t, http.StatusUnauthorized, res.StatusCode, "it serves its routes, and asks who is calling")

	require.ErrorContains(t, s.Start(t.Context(), lone(t, nil)), "already started")
	require.NoError(t, s.Close())
}

func TestBuildRefusesWhatItCannotServeWith(t *testing.T) {
	gerrit := map[string]any{"host": "review.example.com", "driver": "gerrit", "events": map[string]any{
		"address": "review.example.com:29418", "user": "tau", "key": "not a key"}}
	for name, section := range map[string]map[string]any{
		"no address to listen on":       {},
		"a section of the wrong shape":  {"listen": []any{"many"}},
		"a namespace that is not one":   {"listen": "127.0.0.1:0", "tenancy": []any{"not a namespace"}},
		"a forge with no driver":        {"listen": "127.0.0.1:0", "forges": []any{map[string]any{"host": "code.example.com", "driver": "fossil"}}},
		"a stream with no pinned key":   {"listen": "127.0.0.1:0", "forges": []any{gerrit}},
		"certificates and no zone":      {"listen": "127.0.0.1:0", "cm": map[string]any{"certificate": map[string]any{"acme": map[string]any{"enabled": true}}}},
		"uploads it cannot keep":        {"listen": "127.0.0.1:0", "uploads": "/dev/null/uploads"},
		"certificates with a bad block": {"listen": "127.0.0.1:0", "cloudfqdn": "tau.example.io", "cm": map[string]any{"certificate": map[string]any{"acme": map[string]any{"enabled": true}, "challenge": "carrier-pigeon"}}},
	} {
		t.Run(name, func(t *testing.T) {
			s := New()
			require.Error(t, s.Start(t.Context(), lone(t, section)))
			require.NoError(t, s.Close(), "what it opened before refusing closes")
		})
	}
}

// servedTLS is the instance's certificate client as a service asks for it.
type servedTLS struct {
	config *tls.Config
	err    error
}

func (s servedTLS) ServiceTLS() (*tls.Config, error) { return s.config, s.err }

var issuing = map[string]any{
	"listen": "127.0.0.1:0", "cloudfqdn": "tau.example.io",
	"cm": map[string]any{"certificate": map[string]any{"acme": map[string]any{"enabled": true}}},
}

func TestACloudThatIssuesServesBuildOverTLS(t *testing.T) {
	n := lone(t, issuing)
	n.running = append(n.running, service.Running{Service: servedTLS{config: &tls.Config{}}})
	s := New()
	require.NoError(t, s.Start(t.Context(), n))
	t.Cleanup(func() { s.Close() })

	res, err := http.Get("http://" + s.(Service).Addr().String() + "/cloud")
	require.NoError(t, err)
	res.Body.Close()
	require.Equal(t, http.StatusBadRequest, res.StatusCode, "plain http to its TLS listener is refused")
}

// ITS CERTIFICATE IS THE INSTANCE'S, from the client every listener on the node
// takes one from: plm builds none of its own.
func TestACloudThatIssuesNeedsTheInstancesCertificates(t *testing.T) {
	// A REFUSED START IS STILL CLOSED, as the instance closes it: its queue's group
	// opened before the refusal.
	refused := New()
	t.Cleanup(func() { refused.Close() })
	require.ErrorIs(t, refused.Start(t.Context(), lone(t, issuing)), ErrNoListenerTLS)

	n := lone(t, issuing)
	n.running = append(n.running, service.Running{Service: servedTLS{err: errors.New("no zone")}})
	unproven := New()
	t.Cleanup(func() { unproven.Close() })
	require.ErrorContains(t, unproven.Start(t.Context(), n), "no zone")
}

func TestAForgeWithAnEventStreamIsFollowed(t *testing.T) {
	priv, pub := gittest.KeyPair(t)
	s := New()
	require.NoError(t, s.Start(t.Context(), lone(t, map[string]any{
		"listen": "127.0.0.1:0",
		"forges": []any{map[string]any{
			"host": "review.example.com", "driver": "gerrit",
			"hostkeys": []any{strings.TrimSpace(string(gossh.MarshalAuthorizedKey(pub)))},
			"events":   map[string]any{"address": "127.0.0.1:1", "user": "tau", "key": priv},
		}},
	})))
	require.NoError(t, s.Close(), "and the stream it opened stops with it")
}

func TestASectionGivenInGoKeepsTheDefaultsItLeavesOut(t *testing.T) {
	raw, err := yaml.Marshal(ingress.Config{Listen: "127.0.0.1:0"})
	require.NoError(t, err)
	var cfg config
	require.NoError(t, yaml.Unmarshal(raw, &cfg))
	require.Nil(t, cfg.EligibleRefs, "no refs named is the default refs, not none")
}

// groupsOn opens real groups on a test's peer, timed so a lone peer elects itself in
// milliseconds: the instance's groups, as a service beside it finds them.
type groupsOn struct {
	node peer.Node
	root string
}

func (g groupsOn) Group(namespace string) (raft.Cluster, error) {
	return raft.New(g.node, namespace, quickRaft.Options(g.root)...)
}

// quickRaft is raft timed for one process.
var quickRaft = raft.Timing{
	Timeouts: raft.TimeoutConfig{
		HeartbeatTimeout:   50 * time.Millisecond,
		ElectionTimeout:    50 * time.Millisecond,
		CommitTimeout:      25 * time.Millisecond,
		LeaderLeaseTimeout: 25 * time.Millisecond,
		SnapshotInterval:   time.Minute,
		SnapshotThreshold:  1000,
	},
	BootstrapTimeout: 200 * time.Millisecond,
}
