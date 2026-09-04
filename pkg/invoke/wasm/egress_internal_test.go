package wasm

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

// AN ABSENT LIST IS THE FLOOR ALONE AND NOT "NOWHERE". It is the vocabulary's
// own sentence — "layered on the node-wide deny floor, which it can never
// widen; absent means the floor alone" — and the opposite of what this file
// said first: a resource that names nothing asked for no NARROWING, not for
// isolation, and reading it the other way would make every function that calls
// an API fail with a message about a list its author never knew to write.
func TestAnAbsentListIsTheFloorAlone(t *testing.T) {
	var reached string
	client := egressFor(nil, roundTrip(func(r *http.Request) { reached = r.URL.Host }))

	res, err := client.Get("https://api.stripe.com/v1/charges")
	require.NoError(t, err)
	res.Body.Close()
	require.Equal(t, "api.stripe.com", reached)

	// And the floor is still under it, which is what "can never widen" means:
	// the transport a real one gets refuses a private range at the dial.
	require.NotNil(t, floor())
}

func TestAnAllowedHostIsReached(t *testing.T) {
	var reached string
	client := egressFor([]string{"api.stripe.com"}, roundTrip(func(r *http.Request) {
		reached = r.URL.Host
	}))

	res, err := client.Get("https://api.stripe.com/v1/charges")
	require.NoError(t, err)
	res.Body.Close()
	require.Equal(t, "api.stripe.com", reached)

	// A HOST NAME IS CASE-INSENSITIVE and the author wrote a name rather than
	// an endpoint, so the port it is reached on is not part of the match.
	res, err = client.Get("https://API.Stripe.com:8443/v1/charges")
	require.NoError(t, err)
	res.Body.Close()

	// And anything else is still refused.
	_, err = client.Get("https://evil.example/steal")
	require.ErrorContains(t, err, "evil.example")
}

// A WILDCARD IS ONE LABEL AND NEVER THE WHOLE NAME — the same rule every
// certificate follows, so an author's intuition about it is already right.
func TestAWildcardIsOneLabel(t *testing.T) {
	client := egressFor([]string{"*.stripe.com"}, roundTrip(nil))

	res, err := client.Get("https://api.stripe.com/v1")
	require.NoError(t, err)
	res.Body.Close()

	for _, host := range []string{
		"https://stripe.com/v1",       // the apex is not one label under it
		"https://a.b.stripe.com/v1",   // nor is a deeper name
		"https://notstripe.com/v1",    // nor a suffix match on the string
		"https://api.stripe.com.evil", // nor a name that merely starts the same
	} {
		_, err := client.Get(host)
		require.Error(t, err, host)
	}
}

func TestWildcardOfAName(t *testing.T) {
	require.Equal(t, "*.stripe.com", wildcard("api.stripe.com"))
	require.Equal(t, "*.b.stripe.com", wildcard("a.b.stripe.com"))

	// A name with no parent to wildcard has none: "stripe.com" must not
	// become "*.com".
	require.Equal(t, "", wildcard("stripe.com"))
	require.Equal(t, "", wildcard("localhost"))
	require.Equal(t, "", wildcard(""))
	require.Equal(t, "", wildcard(".com"))
}

// Whitespace around a name in the configuration is the author's typing, not a
// different host.
func TestAnEgressEntryIsTrimmed(t *testing.T) {
	client := egressFor([]string{"  api.stripe.com  "}, roundTrip(nil))

	res, err := client.Get("https://api.stripe.com/v1")
	require.NoError(t, err)
	res.Body.Close()
}

// refuse fails every request, so a test of the allowlist cannot pass by
// reaching the network.
type refuse struct{}

func (refuse) RoundTrip(*http.Request) (*http.Response, error) {
	panic("wasm: an allowlist let a request through to the network")
}

// roundTrip answers every request it is given, after observing it.
func roundTrip(saw func(*http.Request)) http.RoundTripper {
	return roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if saw != nil {
			saw(r)
		}
		recorder := httptest.NewRecorder()
		recorder.WriteHeader(http.StatusOK)
		return recorder.Result(), nil
	})
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
