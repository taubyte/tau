//go:build dreaming

package dream_test

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/instance"
	nodeDream "github.com/taubyte/tau/instance/dream"
	pkgregister "github.com/taubyte/tau/pkg/register"
	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
	buildDeploy "github.com/taubyte/tau/services/build/deploy"
	"github.com/taubyte/tau/services/build/run"
)

const viewsID = "QmWBYU7xWH1ZwyEE3qRgrRmSc7ng1G12UMKPtUNYCattB5"

type configRepo struct{}

func (configRepo) Configures(context.Context, string, string) (bool, error) { return true, nil }

type corpusCheckout struct{ dir string }

func (c corpusCheckout) Checkout(context.Context, string, string, string) (string, func(), error) {
	return c.dir, func() {}, nil
}

type anyZone struct{}

func (anyZone) Prove(context.Context, string, string) error { return nil }

type deployLog struct{ strings.Builder }

func (*deployLog) Asset(context.Context, string, io.Reader) error { return nil }

// deployAs runs the config push a cloud serving regions makes, into one universe's register.
func deployAs(t *testing.T, n *instance.Instance, regions schema.Regions) string {
	t.Helper()
	d, err := buildDeploy.New(
		buildDeploy.WithProjects(configRepo{}),
		buildDeploy.WithCheckout(corpusCheckout{dir: projectDir("with/views")}),
		buildDeploy.WithLocalRegister(n.Register().Local()),
		buildDeploy.WithRegions(regions),
		buildDeploy.WithDomains(anyZone{}),
	)
	require.NoError(t, err)
	var log deployLog
	require.NoError(t, planThenRun(t.Context(), d, run.Work{
		JobID: "job", RepositoryKey: "github.com/acme/views-config", Ref: "refs/heads/main",
		CommitSHA: "0f1e2d3c4b5a69788796a5b4c3d2e1f00f1e2d3c", ProjectID: viewsID, Attempt: 1,
	}, &log))
	return log.String()
}

func TestADroppedRegionStopsResolving(t *testing.T) {
	for _, region := range []string{"region-1", "region-2"} {
		t.Run(region, func(t *testing.T) {
			u, err := nodeDream.Start(t.Context(), t.Name(), nodeDream.Config{
				Services:  []string{instance.Register},
				Configure: func(_ int, c *instance.Config) { c.Region = region },
			})
			require.NoError(t, err)
			t.Cleanup(u.Close)
			n := nodeOf(t, u.Pids()[0])
			reg := n.Register().Local()

			deployAs(t, n, schema.Preview("region-1", "region-2"))
			v, err := reg.ResolveView(t.Context(), viewsID, "prod", region)
			require.NoError(t, err)
			require.EqualValues(t, 1, v.Revision)

			require.Contains(t, deployAs(t, n, schema.Preview("region-1")), "published as revision 2")
			as, err := reg.Assigned(t.Context(), region)
			require.NoError(t, err)
			v, err = reg.ResolveView(t.Context(), viewsID, "prod", region)
			if region == "region-2" {
				require.ErrorIs(t, err, pkgregister.ErrNoView)
				require.Empty(t, as)
				return
			}
			require.NoError(t, err)
			require.EqualValues(t, 2, v.Revision)
			require.Len(t, as, 1)
		})
	}
}
