package onedev_test

import (
	"net/url"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/forge/forgetest"
	"github.com/taubyte/tau/pkg/forge/onedev"
)

func TestTheSuiteReachesEveryRouteThisDriverShips(t *testing.T) {
	f := newFixture(t)
	reg := f.registry(t, forge.Policy{})
	forgetest.Run(t, f.subject(t, reg))

	drv, err := reg.Driver(f.Host())
	require.NoError(t, err)
	routes := onedev.DeclaredRoutes(drv)
	sent := f.rt.everything()
	require.NotEmpty(t, sent)

	for _, req := range sent {
		u, err := url.Parse(req.URL)
		require.NoError(t, err)
		if u.Host != f.Host() {
			continue
		}
		matched := slices.ContainsFunc(routes, func(r onedev.RouteFact) bool {
			return r.Ships && r.Matches(req.Method, u.EscapedPath())
		})
		require.True(t, matched, "%s %s matches no route this driver ships", req.Method, u.EscapedPath())
	}

	for _, r := range routes {
		if !r.Ships {
			continue
		}
		var hits []*url.URL
		for _, req := range sent {
			u, err := url.Parse(req.URL)
			require.NoError(t, err)
			if r.Matches(req.Method, u.EscapedPath()) {
				hits = append(hits, u)
			}
		}
		if len(hits) == 0 {
			t.Errorf("%s %s, declared at %s, was never reached by the suite", r.Method, r.Template, r.Declared)
			continue
		}
		for _, p := range r.Params {
			if !slices.ContainsFunc(hits, func(u *url.URL) bool { return u.Query().Has(p) }) {
				t.Errorf("%s %s declares the query parameter %q and no request carried it", r.Method, r.Template, p)
			}
		}
	}
}
