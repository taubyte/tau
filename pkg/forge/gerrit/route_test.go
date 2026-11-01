package gerrit

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAProjectNameIsOneEscapedSegment(t *testing.T) {
	p, err := routeProject.at("acme/team/widget").path()
	require.NoError(t, err)
	assert.Equal(t, "projects/acme%2Fteam%2Fwidget", p)

	for _, bad := range []string{"", ".", "..", "acme/../pay", "acme/./pay", "acme//pay", "acme/"} {
		_, err := routeProject.at(bad).path()
		assert.Error(t, err, "%q addresses something other than itself", bad)
	}
}

func TestARouteTakesExactlyItsArguments(t *testing.T) {
	_, err := routeGroupMember.at("acme").path()
	assert.ErrorContains(t, err, "takes 2 arguments and was given 1")
	_, err = routeProject.at("a", "b").path()
	assert.ErrorContains(t, err, "takes 1 arguments and was given 2")
}

func TestAnUndeclaredQueryParameterIsRefused(t *testing.T) {
	_, err := routeProject.at("acme/pay").with(url.Values{"n": {"1"}}).rawQuery()
	assert.ErrorContains(t, err, `does not declare the query parameter "n"`)
	q, err := routeListProjects.at().with(url.Values{"n": {"1"}, "S": {"0"}}).rawQuery()
	require.NoError(t, err)
	assert.Equal(t, "S=0&n=1", q)
}

func TestAHostChosenStringIsBoundedInAnError(t *testing.T) {
	long := strings.Repeat("x", maxHostString+10)
	assert.Equal(t, maxHostString+len("…"), len(bounded(long)))
	assert.Equal(t, "short", bounded("short"))
}

func TestTheXSSIGuardIsRemovedAndItsAbsenceIsNotAnError(t *testing.T) {
	var v map[string]int
	require.NoError(t, decode(strings.NewReader(")]}'\n{\"a\":1}"), &v))
	assert.Equal(t, 1, v["a"])
	require.NoError(t, decode(strings.NewReader("{\"a\":2}"), &v))
	assert.Equal(t, 2, v["a"])
	assert.Error(t, decode(strings.NewReader(")]}'"), &v), "a guard with no line end has no body after it")
}

func TestARequestThatIsNotAURLIsRefusedBeforeTheWire(t *testing.T) {
	d := &driver{host: "gerrit.example", scheme: "https", rt: unreachable{}}
	c := d.bind(&basic{user: "u", password: "p"})
	// A host with a control character cannot be parsed into a URL.
	c.d = &driver{host: "bad\x7fhost", scheme: "https"}
	err := c.send(t.Context(), routeSelf.at(), nil, nil)
	assert.ErrorContains(t, err, "is not a URL")

	c = d.bind(nil)
	err = c.send(t.Context(), routeSelf.at(), func() {}, nil)
	assert.ErrorContains(t, err, "encoding the GET accounts/self request")

	err = c.send(t.Context(), routeSelf.at(), nil, nil)
	assert.Error(t, err)
	assert.Zero(t, statusOf(err), "a request that never reached a server carries no status")
	assert.Equal(t, http.StatusNotFound, statusOf(&statusError{Status: http.StatusNotFound}))
}

// unreachable is a transport that never reaches anything, so no test here
// touches the network.
type unreachable struct{}

func (unreachable) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("unreachable")
}
