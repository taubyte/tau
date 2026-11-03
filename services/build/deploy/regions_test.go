package deploy_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
	"github.com/taubyte/tau/services/build/deploy"
)

func TestARegionThisCloudDoesNotServeIsRefusedByDefault(t *testing.T) {
	r := registry(t)
	d, err := deploy.New(
		deploy.WithProjects(projects{config: "github.com/acme/shop-config"}),
		deploy.WithCheckout(&checkout{dir: fixture(t, basicFixture)}),
		deploy.WithLocalRegister(r),
		deploy.WithDomains(provenAll()),
	)
	require.NoError(t, err)
	var log out
	err = pushed(t.Context(), d, work(), &log)
	require.ErrorIs(t, err, deploy.ErrBroken)
	require.ErrorContains(t, err, `cache/sessions in shop: is placed in region "region-1", which this build does not serve`)
	_, err = r.Resolve(t.Context(), shopID, "prod")
	require.Error(t, err, "nothing was published")
}

func TestTheFixtureDeploysAsEachRegion(t *testing.T) {
	for _, region := range []string{"region-1", "region-2"} {
		t.Run(region, func(t *testing.T) {
			r := registry(t)
			var log out
			require.NoError(t, pushed(t.Context(), runner(t, &checkout{dir: fixture(t, basicFixture)}, r, deploy.WithRegions(schema.WithRegion(region))),
				work(), &log))
			_, err := r.ResolveView(t.Context(), shopID, "prod", region)
			require.NoError(t, err)
		})
	}
}

// placesNothing places nothing anywhere, which is a cloud that cannot run the project.
type placesNothing struct{}

func (placesNothing) Place(schema.Placement) schema.Answer { return schema.Answer{} }
func (placesNothing) Allow([]string) []schema.Concern      { return nil }
func (placesNothing) Serves() []string                     { return nil }

func TestAnUnplaceableDeploymentPublishesNothing(t *testing.T) {
	r := registry(t)
	var log out
	err := pushed(t.Context(), runner(t, &checkout{dir: fixture(t, basicFixture)}, r, deploy.WithRegions(placesNothing{})),
		work(), &log)
	require.ErrorIs(t, err, deploy.ErrBroken)
	require.Contains(t, err.Error(), "is not placed anywhere")

	_, err = r.Resolve(t.Context(), shopID, "prod")
	require.Error(t, err, "nothing was published")
}

func TestWithRegionsRefusesNil(t *testing.T) {
	_, err := deploy.New(deploy.WithRegions(nil))
	require.ErrorContains(t, err, "regions must not be nil")
}
