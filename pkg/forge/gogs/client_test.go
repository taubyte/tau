package gogs

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/git/repokey"
)

func TestTheDriverKeepsNoCopyOfTheCredential(t *testing.T) {
	const secret = "gt-tok-INTERNAL-1a2b3c4d5e6f"
	d := &driver{host: "gogs.example", base: apiRoot("https://gogs.example/api/v1/")}
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

func TestTheTransportBindInstallsIsPinnedToTheAPIHost(t *testing.T) {
	const secret = "gt-tok-PINNED-9f8e7d6c"

	base := &recordingRoundTripper{}
	d := &driver{host: "gogs.example", base: apiRoot("https://gogs.example/api/v1/"), rt: base}
	rt := d.bind(secret).http.Transport
	require.NotNil(t, rt, "bind installed no transport, so nothing checks where a credential goes")

	// The hop this driver really makes. The credential is attached by the pin
	// and by nothing above it: the request send() built carries no
	// Authorization, so a retry of it is not a credentialed request.
	allowed, err := http.NewRequest(http.MethodGet, "https://gogs.example/api/v1/user", nil)
	require.NoError(t, err)
	resp, err := rt.RoundTrip(allowed)
	require.NoError(t, err, "the pin refused the one host this driver talks to")
	resp.Body.Close()
	require.Len(t, base.sent, 1)
	assert.Equal(t, "token "+secret, base.sent[0].Header.Get("Authorization"),
		"the credential is attached per hop, by the pin, or nothing decides which hops carry it")
	assert.Empty(t, allowed.Header.Get("Authorization"),
		"the transport wrote the credential into the caller's own request; a retry of that request would carry it on a hop nothing checked")

	for name, spelling := range map[string]string{
		"another host entirely":     "https://elsewhere.example.invalid/api/v1/user",
		"the API host in cleartext": "http://gogs.example/api/v1/user",
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

func apiRoot(raw string) *url.URL {
	u, err := url.Parse(raw)
	if err != nil {
		panic(err)
	}
	return u
}

// routeWithAMethodThatIsNotOne is declared here, at package level, so that the
// route table's own closure test can tell it from the routes this driver ships:
// newRoute records the file it was called from, and a route declared in a test
// is not a request this driver can issue.
var routeWithAMethodThatIsNotOne = newRoute("not a method", "user")

// routeNobodyDeclared is a route value built WITHOUT newRoute, which is the one
// way a path could reach the wire without the sweep having been told about it.
var routeNobodyDeclared = &route{method: http.MethodGet, template: "user", segments: []string{"user"}}

func TestRequestsThatCannotBeBuiltAreErrorsAndNotPanics(t *testing.T) {
	c := (&driver{host: "gogs.example", base: apiRoot("https://gogs.example/api/v1/")}).bind("tok")

	// A body nothing can encode.
	assert.Error(t, c.send(t.Context(), routeCreateOwnedByMe.at(), make(chan int), nil))

	// A method that is not one.
	assert.Error(t, c.send(t.Context(), routeWithAMethodThatIsNotOne.at(), nil, nil))

	// The sweep arms what is in the route table, so a path composed outside it is
	// refused.
	err := c.send(t.Context(), routeNobodyDeclared.at(), nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "newRoute")

	// A request with no route at all.
	require.Error(t, c.send(t.Context(), request{}, nil, nil))

	// A route given the wrong number of arguments is a path with a hole left in
	// it, and that is an error rather than a request against "{}".
	require.Error(t, c.send(t.Context(), routeRepository.at("acme"), nil, nil))
	require.Error(t, c.send(t.Context(), routeRepository.at("acme", "pay", "extra"), nil, nil))

	// A query parameter the route does not declare. Declaring them is what
	// makes the sweep able to say a parameter went unexercised, so one that
	// slipped past the declaration would slip past the sweep.
	err = c.send(t.Context(), routeListRepositories.at().with(url.Values{"legacy": {"1"}}), nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "legacy")
}

func TestARouteMatchesOnlyWhatWentThroughIt(t *testing.T) {
	base := apiRoot("https://gogs.example/api/v1/")

	assert.False(t, routeCurrentUser.matches(base, http.MethodGet, "/somewhere/else/entirely"),
		"a path outside the API root matched a route")
	assert.False(t, routeCurrentUser.matches(base, http.MethodPost, "/api/v1/user"),
		"a route matched another method")
	assert.True(t, routeCurrentUser.matches(base, http.MethodGet, "/api/v1/user"))
	assert.True(t, routeRepository.matches(base, http.MethodGet, "/api/v1/repos/acme/pay"))
	assert.False(t, routeRepository.matches(base, http.MethodGet, "/repos/acme/pay"))
}

func TestARouteIsEscapedByTheOneFunctionThatEscapesSegments(t *testing.T) {
	base := apiRoot("https://gogs.example/api/v1/")
	rq := routeRepository.at("acme", "pay#evil")
	path, err := rq.path()
	require.NoError(t, err)
	assert.Equal(t, "repos/acme/pay%23evil", path,
		"a name a forge can hand back with a # in it must not address the repository before the #")

	c := (&driver{host: "gogs.example", base: base, scheme: "https"}).bind("tok")
	addr, err := c.CloneAddress(repokey.Key{Host: "gogs.example", Owner: "acme", Name: "pay#evil"}, forge.HTTPS)
	require.NoError(t, err)
	assert.Equal(t, "https://gogs.example/acme/pay%23evil.git", addr.URL)
}

func TestEveryHoleOfEveryRouteRefusesASegmentThatNamesSomethingElse(t *testing.T) {
	for _, r := range declaredRoutesForTest() {
		holes := strings.Count(r.template, hole)
		if holes == 0 {
			continue
		}
		t.Run(r.method+" "+r.template, func(t *testing.T) {
			for at := 0; at < holes; at++ {
				for _, spelling := range []string{"", "..", ".", "../..", "a/.."} {
					args := make([]string, holes)
					for i := range args {
						args[i] = "benign"
					}
					args[at] = spelling

					_, err := composeForTest(r.at(args...))
					assert.Errorf(t, err,
						"hole %d of %s %s took %q and composed a path anyway", at, r.method, r.template, spelling)
				}
			}

			benign := make([]string, holes)
			for i := range benign {
				benign[i] = "benign"
			}
			_, err := composeForTest(r.at(benign...))
			assert.NoErrorf(t, err, "%s %s refuses a name, so the refusals above prove nothing", r.method, r.template)
		})
	}
}

func declaredRoutesForTest() []*route {
	routesMu.Lock()
	defer routesMu.Unlock()
	return append([]*route(nil), routeTable...)
}

func composeForTest(rq request) (string, error) { return rq.path() }

func TestCheckingTheEscapedSpellingWouldBeCheckingTheRawOneTwice(t *testing.T) {
	survives := func(s string) bool { return path.Clean("/"+s) == "/"+s }

	alphabet := []string{".", "/", "%", "2", "e", "F", "a", "?"}
	visited, held, converseFailed := 0, 0, 0

	var walk func(string, int)
	walk = func(s string, left int) {
		visited++
		if survives(s) {
			held++
			require.Truef(t, survives(url.PathEscape(s)),
				"%q survives dot-segment removal and %q, which is what this driver actually sends, does not: the escaped spelling is not implied by the raw one",
				s, url.PathEscape(s))
		} else if survives(url.PathEscape(s)) {
			converseFailed++
		}
		if left == 0 {
			return
		}
		for _, c := range alphabet {
			walk(s+c, left-1)
		}
	}
	walk("", 3)

	// 1 + 8 + 8*8 + 8*8*8: the empty string and every string of one, two and
	// three characters over the alphabet above.
	require.Equalf(t, 1+8+64+512, visited,
		"this row claims to be exhaustive over an eight-character alphabet up to length three, and it walked %d strings", visited)

	t.Logf("%d spellings survive raw and every one of them survives escaped", held)
	require.Positivef(t, converseFailed,
		"no spelling survives escaped and fails raw, so the two checks are the same check and the raw one is not the stronger: %d held", held)
	t.Logf("%d spellings survive escaped and do NOT survive raw, which is why the raw one is the one kept", converseFailed)
}
