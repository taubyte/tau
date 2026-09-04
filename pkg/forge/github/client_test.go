package github

// From inside the package: what the driver keeps a credential in. The rest of
// this package's tests are github_test, and this one is not, because the thing
// it asserts is a struct field nobody outside can see — which is exactly why it
// could sit there unnoticed.

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
	d := &driver{host: "github.com", apiHost: "api.github.com", base: apiRoot("https://api.github.com/")}
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
	d := &driver{host: "github.com", apiHost: "api.github.com", base: apiRoot("https://api.github.com/"), rt: base}
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

// apiRoot is the parsed API base a driver built by New holds, for the two tests
// above that build one by hand.
func apiRoot(raw string) *url.URL {
	u, err := url.Parse(raw)
	if err != nil {
		panic(err)
	}
	return u
}

// ─────────────────────────── the routing table ───────────────────────────

// routeWithAMethodThatIsNotOne is declared here, at package level, so that the
// route table's own closure test can tell it from the routes this driver ships:
// newRoute records the file it was called from, and a route declared in a test
// is not a request this driver can issue.
var routeWithAMethodThatIsNotOne = newRoute("not a method", "user")

// routeNobodyDeclared is a route value built WITHOUT newRoute, which is the one
// way a path could reach the wire without the sweep having been told about it.
var routeNobodyDeclared = &route{method: http.MethodGet, template: "user", segments: []string{"user"}}

// TestRequestsThatCannotBeBuiltAreErrorsAndNotPanics is the gitlab driver's row
// of the same name, which this package did not have. Every one of these is a
// way the routing table can be asked for something it cannot compose, and the
// answer has to be an error a caller sees rather than a panic in a request path
// or — worse — a request that goes out anyway.
func TestRequestsThatCannotBeBuiltAreErrorsAndNotPanics(t *testing.T) {
	c := (&driver{host: "github.com", apiHost: "api.github.com", base: apiRoot("https://api.github.com/")}).bind("tok")

	// A body nothing can encode.
	assert.Error(t, c.send(t.Context(), routeCreateOwnedByMe.at(), make(chan int), nil))

	// A method that is not one.
	assert.Error(t, c.send(t.Context(), routeWithAMethodThatIsNotOne.at(), nil, nil))

	// A route nobody declared. Every request this driver makes is armed by the
	// sweep because the sweep runs over the ROUTE TABLE, so a path composed
	// outside it is a request nothing arms — and it is refused here rather than
	// sent.
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

// TestARouteMatchesOnlyWhatWentThroughIt. The route table's closures rest on
// matching what went out against what was declared, so the two ways that match
// can be wrong are worth a row.
func TestARouteMatchesOnlyWhatWentThroughIt(t *testing.T) {
	base := apiRoot("https://api.github.com/")

	assert.False(t, routeCurrentUser.matches(base, http.MethodGet, "/somewhere/else/entirely"),
		"a path outside the API root matched a route")
	assert.False(t, routeCurrentUser.matches(base, http.MethodPost, "/user"),
		"a route matched another method")
	assert.True(t, routeCurrentUser.matches(base, http.MethodGet, "/user"))

	// An enterprise instance serves the same table under /api/v3.
	enterprise := apiRoot("https://github.acme.example/api/v3/")
	assert.True(t, routeRepository.matches(enterprise, http.MethodGet, "/api/v3/repos/acme/pay"))
	assert.False(t, routeRepository.matches(enterprise, http.MethodGet, "/repos/acme/pay"))
}

// TestARouteIsEscapedByTheOneFunctionThatEscapesSegments. The clone address is
// not a request, so no row about requests reaches it, and both halves of a key
// used to be escaped there by their own line — one of the five that could be
// deleted with the whole tree at exit 0.
func TestARouteIsEscapedByTheOneFunctionThatEscapesSegments(t *testing.T) {
	base := apiRoot("https://api.github.com/")
	rq := routeRepository.at("acme", "pay#evil")
	path, err := rq.path()
	require.NoError(t, err)
	assert.Equal(t, "repos/acme/pay%23evil", path,
		"a name a forge can hand back with a # in it must not address the repository before the #")

	c := (&driver{host: "github.com", apiHost: "api.github.com", base: base}).bind("tok")
	addr, err := c.CloneAddress(repokey.Key{Host: "github.com", Owner: "acme", Name: "pay#evil"}, forge.HTTPS)
	require.NoError(t, err)
	assert.Equal(t, "https://github.com/acme/pay%23evil.git", addr.URL)
}

// TestEveryHoleOfEveryRouteRefusesASegmentThatNamesSomethingElse is the
// BACKSTOP, and it is a different statement from the refusals at the doors.
//
// A door refuses so that an operation's LATER requests never happen — measured,
// the membership walk's refusal at the route came after a token had been minted.
// This one refuses so that a HOLE ADDED TOMORROW is covered by nothing having
// been remembered: it runs over the ROUTE TABLE ITSELF, every route this driver
// declared and every hole in it, so a route registered next round is in this
// population the moment it is constructed.
//
// The benign fill is what proves the row is read: the same route with a real
// name composes a path, so a failure here is the refusal and not a route that
// could never be built.
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

// declaredRoutesForTest is the table as this package holds it, under the lock
// the table is written behind.
func declaredRoutesForTest() []*route {
	routesMu.Lock()
	defer routesMu.Unlock()
	return append([]*route(nil), routeTable...)
}

// composeForTest is the path one request names, which is where a segment
// becomes part of a URL in this driver.
func composeForTest(rq request) (string, error) { return rq.path() }

// TestCheckingTheEscapedSpellingWouldBeCheckingTheRawOneTwice is the row
// addressesItself's ONE line rests on.
//
// It was written with two, one per spelling, on the argument that the two
// resolvers in a request's chain read different ones. Measured: deleting the
// escaped half left the whole forge tree at GO_TEST_EXIT=0 — a line deletable at
// exit 0, which is the defect this branch keeps finding, in the fix for this
// branch's own defect.
//
// The reason it is dead is stated here rather than argued: url.PathEscape emits
// no "/", so its output is always ONE component, and it leaves an unreserved dot
// alone, so that component is "." or ".." exactly when its input is. Therefore a
// segment that survives dot-segment removal RAW survives it ESCAPED, and
// checking the escaped one adds nothing — while the converse is false, which is
// why the raw one is the one kept: "../.." does not survive raw and does survive
// escaped, as "..%2F..".
//
// THE POPULATION IS EXHAUSTIVE AND NOT A SAMPLE, over the characters that decide
// this: every string of up to three of them.
//
// AND THE SIZE IS PINNED AS A LITERAL, because a walk whose extent is computed
// from its own depth is a walk that can be shortened without saying so.
// Measured: with the count unpinned, changing walk("", 3) to walk("", 1) was
// GO_TEST_EXIT=0 — nine strings claiming to be five hundred and eighty-five.
// Widening the alphabet costs the author this arithmetic, which is the right
// price for an exhaustiveness claim.
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
