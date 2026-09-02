package github

// From inside the package: what the driver keeps a credential in. The rest of
// this package's tests are github_test, and this one is not, because the thing
// it asserts is a struct field nobody outside can see — which is exactly why it
// could sit there unnoticed.

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/v2/forge"
)

// TestTheDriverKeepsNoCopyOfTheCredential.
//
// forge.Credential exists to close EVERY way of rendering key material: Format
// catches every verb, and the marshallers fail rather than redact. bind unwraps
// it to a string at the door, and a client that stored that string beside its
// http.Client rendered it under %v like any other field — key material kept in
// the one shape the package went to trouble to make impossible. It was also
// never read: the Authorization header is written by a closure that captures
// the parameter.
func TestTheDriverKeepsNoCopyOfTheCredential(t *testing.T) {
	const secret = "gh-tok-INTERNAL-1a2b3c4d5e6f"
	d := &driver{host: "github.com", apiHost: "api.github.com", base: "https://api.github.com/"}
	c := d.bind(secret)

	for _, rendering := range []string{
		fmt.Sprintf("%v", *c), fmt.Sprintf("%+v", *c), fmt.Sprintf("%v", c),
	} {
		assert.NotContains(t, rendering, secret, "the client rendered its credential: %s", rendering)
	}
}

// recordingRoundTripper is the transport UNDER the pin — what the driver really
// put on the wire, and what header it carried when it did.
type recordingRoundTripper struct{ sent []*http.Request }

func (r *recordingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	r.sent = append(r.sent, req)
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader("{}")),
		Header:     make(http.Header),
	}, nil
}

// TestTheTransportBindInstallsIsPinnedToTheAPIHost is the assertion that
// forge.PinnedTransport is load-bearing HERE, in this driver, and not merely
// somewhere in the module.
//
// IT IS DRIVEN AT THE TRANSPORT AND NOT THROUGH A DRIVER CALL, and that is a
// deliberate retreat that should be read rather than tidied away. The pin's
// original route into this package was a redirect: an http.Client follows one,
// so a forge answering 302 handed the next hop the Authorization header unless
// the pin refused it. noRedirect closed that route — the client now stops at
// the 3xx and never asks the pin to make a second request — and it closed it
// for a better reason than the pin had, because a redirect to another
// REPOSITORY on the SAME host is one the pin permits by design. So every
// request this driver builds now goes to exactly one host, over https, and no
// sequence of answers from a fixture can make it build another: send() composes
// d.base with an escaped path, and there is no option that lets the two
// disagree. There is no public seam left through which the pin can be made to
// fire.
//
// What is left is still behaviour and not a type assertion: the transport that
// bind() actually installed is driven with the hops a redirect WOULD have
// produced, and it must refuse them. That fails if the wrap is dropped, and it
// fails if the wrap is kept but bound to the repository host instead of the API
// host — which for github.com are two different names, and which is the whole
// reason the pin is given the second.
func TestTheTransportBindInstallsIsPinnedToTheAPIHost(t *testing.T) {
	const secret = "gh-tok-PINNED-9f8e7d6c"

	base := &recordingRoundTripper{}
	d := &driver{host: "github.com", apiHost: "api.github.com", base: "https://api.github.com/", rt: base}
	rt := d.bind(secret).http.Transport
	require.NotNil(t, rt, "bind installed no transport, so nothing checks where a credential goes")

	// The hop this driver really makes. The credential is attached by the pin
	// and by nothing above it: the request send() built carries no
	// Authorization, so a retry of it is not a credentialed request.
	allowed, err := http.NewRequest(http.MethodGet, "https://api.github.com/user", nil)
	require.NoError(t, err)
	resp, err := rt.RoundTrip(allowed)
	require.NoError(t, err, "the pin refused the one host this driver talks to")
	resp.Body.Close()
	require.Len(t, base.sent, 1)
	assert.Equal(t, "Bearer "+secret, base.sent[0].Header.Get("Authorization"),
		"the credential is attached per hop, by the pin, or nothing decides which hops carry it")
	assert.Empty(t, allowed.Header.Get("Authorization"),
		"the transport wrote the credential into the caller's own request; a retry of that request would carry it on a hop nothing checked")

	for name, spelling := range map[string]string{
		"another host entirely":                          "https://elsewhere.example.invalid/api/v3/user",
		"the repository host, which is not the API host": "https://github.com/user",
		"the API host in cleartext":                      "http://api.github.com/user",
	} {
		t.Run(name, func(t *testing.T) {
			before := len(base.sent)
			req, err := http.NewRequest(http.MethodGet, spelling, nil)
			require.NoError(t, err)

			resp, err := rt.RoundTrip(req)
			if resp != nil {
				resp.Body.Close()
			}
			var leak *forge.CredentialLeakError
			require.ErrorAs(t, err, &leak, "the transport this driver installs carried the credential to %s", spelling)
			assert.Len(t, base.sent, before, "the request was sent before it was refused")
			assert.NotContains(t, err.Error(), secret, "the refusal quotes the credential it refused to spend")
		})
	}
}
