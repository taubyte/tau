package proxy

import (
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Real sockets: a refused connection is closed with nothing written, which a counter
// assertion would not notice.

// limited is a proxy whose rate_limit block says what the test needs.
func limited(t *testing.T, block map[string]any) (*Service, Socket, *workers) {
	t.Helper()

	res := servable()
	res["rate_limit"] = block

	w := &workers{said: "pong:"}
	s, _ := served(t, Config{CloudFqdn: "tau.example.io"}, res, w)

	sock, err := s.Serve(t.Context(), pgwire())
	require.NoError(t, err)
	return s, sock, w
}

// dial opens one connection and reports whether it was carried — a refused one
// is closed with nothing written, which is all a raw socket can say.
func dial(t *testing.T, sock Socket) bool {
	t.Helper()

	c, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(int(sock.Port))), 2*time.Second)
	require.NoError(t, err)
	t.Cleanup(func() { c.Close() })

	c.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := c.Write([]byte("ping")); err != nil {
		return false
	}
	c.(*net.TCPConn).CloseWrite()

	buf := make([]byte, 16)
	n, _ := c.Read(buf)
	return n > 0
}

func TestConnectionsPerMinuteAreEnforced(t *testing.T) {
	_, sock, _ := limited(t, map[string]any{
		"connections_per_minute": 60, "burst": 2,
	})

	require.True(t, dial(t, sock), "the first is within the burst")
	require.True(t, dial(t, sock), "and so is the second")
	require.False(t, dial(t, sock), "the third is refused with nothing written")
}

func TestConcurrentConnectionsAreEnforcedWhereARateWouldNot(t *testing.T) {
	_, sock, _ := limited(t, map[string]any{"concurrent": 2})

	var held []net.Conn
	for range 2 {
		c, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(int(sock.Port))))
		require.NoError(t, err)
		t.Cleanup(func() { c.Close() })
		held = append(held, c)
	}

	// The two above are open and idle; nothing has arrived quickly enough for
	// any rate to matter.
	require.Eventually(t, func() bool { return !dial(t, sock) }, 2*time.Second, 20*time.Millisecond,
		"a third is refused while two are held")

	held[0].Close()
	require.Eventually(t, func() bool { return dial(t, sock) }, 2*time.Second, 20*time.Millisecond,
		"and a closed one gives its slot back")
}

func TestAProxyWithNoRateLimitBlockRefusesNothing(t *testing.T) {
	w := &workers{said: "pong:"}
	s, _ := served(t, Config{CloudFqdn: "tau.example.io"}, servable(), w)

	sock, err := s.Serve(t.Context(), pgwire())
	require.NoError(t, err)

	for range 20 {
		require.True(t, dial(t, sock))
	}
}

func TestASweepPicksUpAnEditedLimit(t *testing.T) {
	res := servable()
	res["rate_limit"] = map[string]any{"concurrent": 1}

	d := &deployments{}
	d.set(pgwire())

	deployed := &redeployable{}
	deployed.set(compiled(t, res))

	s, err := build(Config{CloudFqdn: "tau.example.io"},
		withProjects(deployed), withWorkers(&workers{said: "pong:"}), withDeployments(d))
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })

	require.Equal(t, 1, s.Sweep(t.Context()))
	sock := s.Serving()[0]

	held, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(int(sock.Port))))
	require.NoError(t, err)
	defer held.Close()

	require.Eventually(t, func() bool { return !dial(t, sock) }, 2*time.Second, 20*time.Millisecond,
		"one is held and one is all that is allowed")

	// The author raises it and deploys. Nothing re-binds; the next sweep
	// re-reads the resource it is already serving.
	res["rate_limit"] = map[string]any{"concurrent": 4}
	deployed.set(compiled(t, res))

	require.Equal(t, 0, s.Sweep(t.Context()), "it is already served")
	require.True(t, dial(t, sock), "and the new ceiling is in force")
}
