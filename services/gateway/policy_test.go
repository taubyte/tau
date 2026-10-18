package gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/ratelimit"
	"github.com/taubyte/tau/pkg/specs"
)

// TestTheRateLimitRefillsAtTheRateTheAuthorWrote, and is capped at the burst.
func TestTheRateLimitRefills(t *testing.T) {
	now := &fakeClock{at: time.Unix(1_700_000_000, 0)}
	l := ratelimit.NewBuckets(now.Now, 16)

	// 60 rpm, burst 3: three straight away, then one a second.
	require.True(t, l.Allow("k", 60, 3))
	require.True(t, l.Allow("k", 60, 3))
	require.True(t, l.Allow("k", 60, 3))
	require.False(t, l.Allow("k", 60, 3))

	now.advance(time.Second)
	require.True(t, l.Allow("k", 60, 3))
	require.False(t, l.Allow("k", 60, 3))

	// A long quiet spell refills to the burst and no further, so a bucket
	// cannot bank an hour of unused budget and spend it in one second.
	now.advance(time.Hour)
	require.True(t, l.Allow("k", 60, 3))
	require.True(t, l.Allow("k", 60, 3))
	require.True(t, l.Allow("k", 60, 3))
	require.False(t, l.Allow("k", 60, 3))
}

// TestNoRateLimitIsNoRateLimit. A gateway that wrote no rate_limit block means
// exactly that.
func TestNoRateLimitIsNoRateLimit(t *testing.T) {
	now := &fakeClock{at: time.Unix(1_700_000_000, 0)}
	l := ratelimit.NewBuckets(now.Now, 16)

	for range 1000 {
		require.True(t, l.Allow("k", 0, 0))
	}

	// A burst nobody wrote is the sustained rate, which is the reading that
	// makes `rpm: 60` on its own mean something.
	require.True(t, l.Allow("j", 2, 0))
	require.True(t, l.Allow("j", 2, 0))
	require.False(t, l.Allow("j", 2, 0))
}

// TestKeysAreSeparate, and the table is bounded — a gateway under a spray of
// one-request-per-key traffic must not grow a bucket per source forever.
func TestKeysAreSeparateAndBounded(t *testing.T) {
	now := &fakeClock{at: time.Unix(1_700_000_000, 0)}
	l := ratelimit.NewBuckets(now.Now, 2)

	require.True(t, l.Allow("a", 60, 1))
	require.False(t, l.Allow("a", 60, 1))
	require.True(t, l.Allow("b", 60, 1), "another key has its own budget")
	require.True(t, l.Allow("c", 60, 1))

	require.LessOrEqual(t, l.Len(), 2)
}

// TestTheLimitKeyIsNeverAForwardedHeader. X-Forwarded-For is client-controlled
// unless something in front is known to rewrite it, and trusting it would let
// one caller spend everyone's budget by picking a new value per request.
func TestTheLimitKeyIsNeverAForwardedHeader(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "203.0.113.7:51000"
	r.Header.Set("X-Forwarded-For", "198.51.100.1")

	require.Equal(t, "ip/203.0.113.7", limitKey("ip", r))
	require.Equal(t, "ip/203.0.113.7", limitKey("", r), "an unset key counts per ip")

	// An unknown key counts everything together rather than counting nothing:
	// a misspelled key that silently disabled the limit looks configured and
	// is not.
	require.Equal(t, "ip/203.0.113.7", limitKey("per-user", r))

	r.Header.Set("X-Api-Key", "abc")
	require.Equal(t, "h/x-api-key/abc", limitKey("header:X-Api-Key", r))
	require.Equal(t, "h/x-api-key/abc", limitKey("header: X-Api-Key", r))

	// An address with no port, and an IPv6 one.
	bare := httptest.NewRequest(http.MethodGet, "/", nil)
	bare.RemoteAddr = "203.0.113.7"
	require.Equal(t, "ip/203.0.113.7", limitKey("ip", bare))

	six := httptest.NewRequest(http.MethodGet, "/", nil)
	six.RemoteAddr = "[2001:db8::7]:51000"
	require.Equal(t, "ip/2001:db8::7", limitKey("ip", six))
}

func TestRetryAfterIsAWholeSecondAtLeast(t *testing.T) {
	require.Equal(t, "1", retryAfter(60))
	require.Equal(t, "1", retryAfter(600), "faster than one a second still says one")
	require.Equal(t, "2", retryAfter(30))
	require.Equal(t, "60", retryAfter(1))
	require.Equal(t, "7", retryAfter(9), "rounded up, never down")
	require.Equal(t, "1", retryAfter(0))
}

// TestAllowSelfIsHowAGeneratedNameSaysItsOwnOrigin. The cloud's half of the
// name is not knowable when the config is written, so an author cannot put it
// in the origins list.
func TestAllowSelf(t *testing.T) {
	gateway := specs.Resource{"cors": map[string]any{"allow_self": true}}

	for _, origin := range []string{"https://gen.tau.example.io", "http://gen.tau.example.io"} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.Header.Set("Origin", origin)

		cors(w, r, specs.ReadGateway(gateway).Cors, "gen.tau.example.io")
		require.Equal(t, origin, w.Header().Get("Access-Control-Allow-Origin"))
	}

	// And it says nothing about anyone else's.
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("Origin", "https://evil.example.com")
	cors(w, r, specs.ReadGateway(gateway).Cors, "gen.tau.example.io")
	require.Empty(t, w.Header().Get("Access-Control-Allow-Origin"))

	// A request with no Origin is not a CORS request at all.
	w = httptest.NewRecorder()
	cors(w, httptest.NewRequest(http.MethodGet, "/", nil), specs.ReadGateway(gateway).Cors, "gen.tau.example.io")
	require.Empty(t, w.Header())
}

func TestCorsMethodsAreEchoedWhenDeclared(t *testing.T) {
	gateway := specs.Resource{"cors": map[string]any{
		"origins": []any{"https://shop.example.com"},
		"methods": []any{"GET", "POST"},
	}}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodOptions, "/", nil)
	r.Header.Set("Origin", "https://shop.example.com")

	cors(w, r, specs.ReadGateway(gateway).Cors, "")
	require.Equal(t, "GET, POST", w.Header().Get("Access-Control-Allow-Methods"))
}
