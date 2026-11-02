package gitlab

// The credential pin, in the driver that installs it. BaseURL accepts http://
// off loopback, so the pin's cleartext rule is tested through a driver call.
// With no redirect followed, no driver call reaches the host rule, so that is
// driven at the transport httpClient installed.

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/git/repokey"
)

const (
	// Long and odd, so that finding one where it must not be is unambiguous.
	pinCallerToken     = "glpat-PINNED-CALLER-8b21f7c4"
	pinDeploymentToken = "glpat-PINNED-DEPLOY-1e6a90dd"

	// An instance an operator names on their own network. It is not loopback,
	// which is the whole point: loopback is the exception the pin makes so that
	// httptest works at all.
	pinHost = "gitlab.acme.internal"
)

// pinRecorder is the transport UNDER the pin — what this driver really put on
// the wire, and what header it carried when it did. It answers in-process, so a
// request that reaches it is a request that was NOT refused.
type pinRecorder struct{ sent []*http.Request }

func (p *pinRecorder) RoundTrip(req *http.Request) (*http.Response, error) {
	p.sent = append(p.sent, req.Clone(req.Context()))
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       http.NoBody,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
	}, nil
}

func TestACleartextInstanceIsNeverSentTheCredential(t *testing.T) {
	rec := &pinRecorder{}
	drv, err := New(pinHost, BaseURL("http://"+pinHost), HTTPTransport(rec), Deployment(forge.Token(pinDeploymentToken)), Signing())
	require.NoError(t, err, "BaseURL refused cleartext; if it now does, this test's premise is gone and so is the operator it protects")

	reg, err := forge.New(forge.Policy{}, forge.Binding{Host: pinHost, Driver: drv})
	require.NoError(t, err)
	key, err := repokey.Parse("https://" + pinHost + "/acme/pay.git")
	require.NoError(t, err)
	sess, err := reg.For(key, forge.Token(pinCallerToken))
	require.NoError(t, err)

	for _, op := range sweepOps {
		t.Run(op.name, func(t *testing.T) {
			got, err := op.call(context.Background(), reg, sess, key, benignArgs)
			t.Logf("%s -> %v, err = %v", op.name, got, err)

			if op.offline {
				// It answers from the key alone, so there is no request for the
				// pin to refuse — and that IS the assertion for this row: an
				// operation that suddenly needed one would be spending a
				// credential on a wire this test is about.
				require.NoError(t, err, "%s answers from the key alone and it failed", op.name)
				assert.Empty(t, rec.sent, "%s made a request: %v", op.name, rec.sent)
				return
			}
			require.Error(t, err, "%s spoke to %s in the clear", op.name, pinHost)
			var leak *forge.CredentialLeakError
			require.ErrorAs(t, err, &leak,
				"%s failed for some other reason, so this says nothing about the credential", op.name)
			assert.Contains(t, leak.To, "http://", "the refusal is about the scheme, and it must name it")
			assert.Empty(t, rec.sent, "the credential went on a cleartext wire: %v", rec.sent)
		})
	}
}

func TestACleartextInstanceIsNeverSentTheDeploymentCredential(t *testing.T) {
	rec := &pinRecorder{}
	drv, err := New(pinHost, BaseURL("http://"+pinHost), HTTPTransport(rec), Deployment(forge.Token(pinDeploymentToken)), Signing())
	require.NoError(t, err)
	reg, err := forge.New(forge.Policy{}, forge.Binding{Host: pinHost, Driver: drv})
	require.NoError(t, err)

	member, err := reg.IsActiveMember(context.Background(), pinHost, "acme", "alice")
	t.Logf("IsActiveMember -> %v, err = %v", member, err)

	assert.False(t, member, "an unanswerable question answered a membership")
	require.Error(t, err)
	var leak *forge.CredentialLeakError
	require.ErrorAs(t, err, &leak, "the membership walk failed for some other reason")
	assert.Empty(t, rec.sent, "the deployment's credential went on a cleartext wire: %v", rec.sent)
}

type pinClient struct {
	opts   []Option
	client func(*testing.T, forge.Driver) *http.Client
	header string
	value  string
}

// pinClients names each http.Client this driver builds, because there are two
// and they are built in two places: one per Bind, one per driver holding a
// deployment credential.
var pinClients = map[string]pinClient{
	"the client a caller's token is bound to": {
		client: pinCallerClient,
		header: "PRIVATE-TOKEN",
		value:  pinCallerToken,
	},
	"the client the deployment's own credential spends": {
		opts:   []Option{Deployment(forge.Token(pinDeploymentToken))},
		client: pinDeploymentClient,
		header: "PRIVATE-TOKEN",
		value:  pinDeploymentToken,
	},
}

func pinCallerClient(t *testing.T, d forge.Driver) *http.Client {
	t.Helper()
	bound, err := d.Bind(forge.Token(pinCallerToken))
	require.NoError(t, err)
	return bound.(*client).api
}

func pinDeploymentClient(t *testing.T, d forge.Driver) *http.Client {
	t.Helper()
	m, ok := d.(*memberDriver)
	require.True(t, ok, "a driver given a deployment credential is a different Go type, and it is not this one")
	return m.api
}

func TestEveryClientThisDriverBuildsIsPinnedToTheInstance(t *testing.T) {
	for name, tc := range pinClients {
		t.Run(name, func(t *testing.T) {
			rec := &pinRecorder{}
			drv, err := New(pinHost, append(tc.opts, HTTPTransport(rec))...)
			require.NoError(t, err)
			rt := tc.client(t, drv).Transport
			require.NotNil(t, rt, "this client installs no transport, so nothing checks where its credential goes")

			// The hop this driver really makes, and the guard that keeps the
			// refusals below from being vacuous: a pin bound to nothing at all
			// refuses every one of them too.
			allowed, err := http.NewRequest(http.MethodGet, "https://"+pinHost+"/api/v4/user", nil)
			require.NoError(t, err)
			resp, err := rt.RoundTrip(allowed)
			require.NoError(t, err, "the pin refused the one host this driver talks to")
			resp.Body.Close()

			require.Len(t, rec.sent, 1)
			// Two halves of one claim: the credential is attached per hop, inside the pin.
			assert.Equal(t, tc.value, rec.sent[0].Header.Get(tc.header),
				"nothing attached the credential to the hop this driver makes")
			assert.Empty(t, allowed.Header.Get(tc.header),
				"the credential was written into the request the driver composed rather than into the hop; a retry of that request would carry it on a hop nothing checked")

			for spelling, to := range map[string]string{
				"another host entirely":                                          "https://elsewhere.example.invalid/api/v4/user",
				"a host that merely ends with this one's name":                   "https://evil-" + pinHost + "/api/v4/user",
				"this very instance in cleartext, which its own BaseURL accepts": "http://" + pinHost + "/api/v4/user",
			} {
				t.Run(spelling, func(t *testing.T) {
					before := len(rec.sent)
					req, err := http.NewRequest(http.MethodGet, to, nil)
					require.NoError(t, err)

					resp, err := rt.RoundTrip(req)
					if resp != nil {
						resp.Body.Close()
					}
					var leak *forge.CredentialLeakError
					require.ErrorAs(t, err, &leak, "the transport this driver installs carried the credential to %s", to)
					assert.Len(t, rec.sent, before, "the request was sent before it was refused")
				})
			}
		})
	}
}
