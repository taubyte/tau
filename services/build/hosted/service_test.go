package hosted

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	gossh "golang.org/x/crypto/ssh"
	"gopkg.in/yaml.v3"

	"github.com/taubyte/tau/core/service"
	"github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/pkg/git/gittest"
)

// node is an instance as build sees it: a peer, nothing beside it, and the section
// an operator wrote.
type node struct {
	peer.Node
	section map[string]any
}

func (n *node) Peer() peer.Node          { return n.Node }
func (*node) Running() []service.Running { return nil }
func (*node) Root() string               { return "" }
func (*node) Cloud() string              { return "test" }
func (*node) Region() string             { return "" }
func (*node) Now() time.Time             { return time.Now() }

func (n *node) Config(into any) error {
	raw, err := yaml.Marshal(n.section)
	if err != nil {
		return err
	}
	return yaml.Unmarshal(raw, into)
}

func lone(t *testing.T, section map[string]any) *node {
	t.Helper()
	p := peer.Mock(t.Context())
	t.Cleanup(p.Close)
	return &node{Node: p, section: section}
}

func TestBuildStartsFromItsInstance(t *testing.T) {
	s := New()
	require.Nil(t, s.(Service).Addr(), "nothing bound before it starts")
	require.NoError(t, s.Start(t.Context(), lone(t, map[string]any{"listen": "127.0.0.1:0", "cloudfqdn": "tau.example.io"})))
	t.Cleanup(func() { s.Close() })

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

func TestACloudThatIssuesServesBuildOverTLS(t *testing.T) {
	s := New()
	require.NoError(t, s.Start(t.Context(), lone(t, map[string]any{
		"listen": "127.0.0.1:0", "cloudfqdn": "tau.example.io",
		"cm": map[string]any{"certificate": map[string]any{"acme": map[string]any{"enabled": true}}},
	})))
	t.Cleanup(func() { s.Close() })

	res, err := http.Get("http://" + s.(Service).Addr().String() + "/cloud")
	require.NoError(t, err)
	res.Body.Close()
	require.Equal(t, http.StatusBadRequest, res.StatusCode, "plain http to its TLS listener is refused")
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
