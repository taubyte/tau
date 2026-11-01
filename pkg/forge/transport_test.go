package forge

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPinnedTransportRefusesTheRedirect(t *testing.T) {
	var elsewhereSaw http.Header
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		elsewhereSaw = r.Header.Clone()
	}))
	defer elsewhere.Close()

	var forgeSaw http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		forgeSaw = r.Header.Clone()
		http.Redirect(w, r, elsewhere.URL+"/stolen", http.StatusFound)
	}))
	defer srv.Close()

	client := &http.Client{Transport: PinnedTransport(hostOf(srv.URL), nil, func(h http.Header) {
		h.Set("Authorization", "Bearer the-credential")
	})}

	_, err := client.Get(srv.URL + "/api/user")
	require.Error(t, err)
	var leak *CredentialLeakError
	require.ErrorAs(t, err, &leak, "the redirect was followed with the credential attached")
	assert.Equal(t, hostOf(srv.URL), leak.Bound)
	assert.Contains(t, leak.Error(), "refusing to carry")

	assert.Equal(t, "Bearer the-credential", forgeSaw.Get("Authorization"), "the credential never reached the host it is for")
	assert.Nil(t, elsewhereSaw, "the second hop was made at all")
}

func TestPinnedTransportServesItsOwnHost(t *testing.T) {
	var saw *http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		saw = r.Clone(r.Context())
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := &http.Client{Transport: PinnedTransport(hostOf(srv.URL), nil, func(h http.Header) {
		h.Set("Authorization", "token abc")
	})}
	resp, err := client.Get(srv.URL + "/api/user")
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, "token abc", saw.Header.Get("Authorization"))

	// A caller's own request is not mutated: it may be retried, and a header
	// this transport wrote would then travel on a hop it did not check.
	req, err := http.NewRequest(http.MethodGet, srv.URL+"/api/user", nil)
	require.NoError(t, err)
	resp, err = client.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	assert.Empty(t, req.Header.Get("Authorization"))
}

func TestPinnedTransportRefusesCleartextOffLoopback(t *testing.T) {
	rt := PinnedTransport("git.example.invalid", roundTripperFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("the request reached the wire")
		return nil, nil
	}), nil)

	req, err := http.NewRequest(http.MethodGet, "http://git.example.invalid/api/user", nil)
	require.NoError(t, err)
	_, err = rt.RoundTrip(req)
	var leak *CredentialLeakError
	require.ErrorAs(t, err, &leak)
	assert.Contains(t, leak.To, "http://")
}

func TestPinnedTransportKnowsADefaultPortIsTheSameServer(t *testing.T) {
	reached := 0
	base := roundTripperFunc(func(*http.Request) (*http.Response, error) {
		reached++
		return &http.Response{StatusCode: 200, Body: http.NoBody}, nil
	})

	for _, tc := range []struct {
		host string
		url  string
		want bool
	}{
		{"git.example.invalid", "https://git.example.invalid/x", true},
		{"git.example.invalid", "https://git.example.invalid:443/x", true},
		{"git.example.invalid:8443", "https://git.example.invalid:8443/x", true},
		{"git.example.invalid", "https://git.example.invalid:8443/x", false},
		{"git.example.invalid:8443", "https://git.example.invalid/x", false},
		{"[::1]:8080", "http://[::1]:8080/x", true},
	} {
		req, err := http.NewRequest(http.MethodGet, tc.url, nil)
		require.NoError(t, err)
		_, err = PinnedTransport(tc.host, base, nil).RoundTrip(req)
		if tc.want {
			assert.NoError(t, err, "%s bound to %s", tc.url, tc.host)
		} else {
			assert.Error(t, err, "%s bound to %s", tc.url, tc.host)
		}
	}
	assert.Equal(t, 4, reached)
}

func TestLoopbackIsRecognisedByEverySpelling(t *testing.T) {
	for _, name := range []string{"localhost", "LOCALHOST", "127.0.0.1", "::1", "[::1]"} {
		assert.True(t, isLoopback(name), name)
	}
	for _, name := range []string{"git.example.invalid", "10.0.0.1", ""} {
		assert.False(t, isLoopback(name), name)
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func hostOf(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		panic(err)
	}
	return u.Host
}
