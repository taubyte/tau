package function

import (
	"testing"

	matcherSpec "github.com/taubyte/tau/pkg/specs/matcher"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	"github.com/taubyte/tau/services/substrate/components/http/common"
)

// The DSL accepts a method in any casing — IsHttpMethod upper-cases before
// checking its closed set, and advertises the uppercase set as canonical — but
// this matcher compared the configured method verbatim against net/http's
// r.Method, which is always uppercase. A function authored `method: get`
// therefore validated, compiled, deployed, and silently never routed.
//
// It stayed invisible because every YAML fixture in the tree authors lowercase
// yet is only ever compiled, while every test that actually routes builds the
// function programmatically with "GET"/"POST". Nothing took the path that
// exposes it, so this test takes it.
func TestMatchCanonicalizesTheConfiguredMethod(t *testing.T) {
	const path = "/ping"

	match := func(configured, requested string) matcherSpec.Index {
		f := &Function{config: structureSpec.Function{Method: configured, Paths: []string{path}}}
		return f.Match(common.New("example.com", path, requested))
	}

	t.Run("a lowercase configured method still routes", func(t *testing.T) {
		if got := match("get", "GET"); got != matcherSpec.HighMatch {
			t.Fatalf(`config "get" must serve a GET request, got %v`, got)
		}
		if got := match("post", "POST"); got != matcherSpec.HighMatch {
			t.Fatalf(`config "post" must serve a POST request, got %v`, got)
		}
	})

	t.Run("canonical config keeps working", func(t *testing.T) {
		if got := match("GET", "GET"); got != matcherSpec.HighMatch {
			t.Fatalf(`config "GET" must serve a GET request, got %v`, got)
		}
	})

	t.Run("a different method still does not match", func(t *testing.T) {
		if got := match("get", "POST"); got != matcherSpec.NoMatch {
			t.Fatalf(`config "get" must not serve a POST request, got %v`, got)
		}
	})

	// The REQUEST side stays exact: RFC 9110 §9.1 makes the method token
	// case-sensitive, so a client sending "get" has not sent GET. Loosening
	// both sides would also split the cache from the match — MatchDefinition's
	// key is Host+Path+Method verbatim.
	t.Run("the request's method is not loosened", func(t *testing.T) {
		if got := match("GET", "get"); got != matcherSpec.NoMatch {
			t.Fatalf(`a request for "get" must not be served by a GET handler, got %v`, got)
		}
	})

	t.Run("a non-matching path still does not match", func(t *testing.T) {
		f := &Function{config: structureSpec.Function{Method: "get", Paths: []string{path}}}
		if got := f.Match(common.New("example.com", "/other", "GET")); got != matcherSpec.NoMatch {
			t.Fatalf("path must still have to match, got %v", got)
		}
	})
}
