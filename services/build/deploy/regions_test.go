package deploy_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
	"github.com/taubyte/tau/services/build/deploy"
)

// A REGION THIS CLOUD DOES NOT SERVE IS REFUSED BY DEFAULT. The basic fixture says
// where its scopes and resources run; a deploy given no Regions has none (NoRegion),
// so it refuses before publishing rather than running them somewhere they were not
// told to be.
func TestARegionThisCloudDoesNotServeIsRefusedByDefault(t *testing.T) {
	r := registry(t)
	d, err := deploy.New(
		deploy.WithProjects(projects{config: "github.com/acme/shop-config"}),
		deploy.WithCheckout(&checkout{dir: fixture(t, basicFixture)}),
		deploy.WithLocalRegister(r),
	)
	require.NoError(t, err)
	var log out
	err = d.Run(t.Context(), work(), &log)
	require.ErrorIs(t, err, deploy.ErrUnplaced)
	require.ErrorContains(t, err, `cache/sessions in shop: is placed in region "eu", which this build does not serve`)
	_, err = r.Resolve(t.Context(), "shop", deploy.DefaultEnvironment)
	require.Error(t, err, "nothing was published")
}

// LENIENT, THE SAME STATEMENT IS SAID AND NOT DROPPED: the deploy places it all in
// the one region, publishes, and says so in the log the developer reads.
func TestARegionThisCloudDoesNotServeIsSaidWhenLenient(t *testing.T) {
	r := registry(t)
	var log out
	require.NoError(t, runner(t, &checkout{dir: fixture(t, basicFixture)}, r).Run(t.Context(), work(), &log))

	require.Contains(t, log.String(),
		`check: cache/sessions in shop: is placed in region "eu", which this build does not serve; it runs in "default"`)
	_, err := r.Resolve(t.Context(), "shop", deploy.DefaultEnvironment)
	require.NoError(t, err)
}

// placesNothing places nothing anywhere, which is a cloud that cannot run the project.
type placesNothing struct{}

func (placesNothing) Place(schema.Placement) schema.Answer { return schema.Answer{} }
func (placesNothing) Allow([]string) []schema.Concern      { return nil }

// A PLACEMENT THE CLOUD CANNOT HONOUR IS REFUSED BEFORE ANYTHING PUBLISHES.
func TestAnUnplaceableDeploymentPublishesNothing(t *testing.T) {
	r := registry(t)
	var log out
	err := runner(t, &checkout{dir: fixture(t, basicFixture)}, r, deploy.WithRegions(placesNothing{})).
		Run(t.Context(), work(), &log)
	require.ErrorIs(t, err, deploy.ErrUnplaced)
	require.Contains(t, err.Error(), "is not placed anywhere")

	_, err = r.Resolve(t.Context(), "shop", deploy.DefaultEnvironment)
	require.Error(t, err, "nothing was published")
}

func TestWithRegionsRefusesNil(t *testing.T) {
	_, err := deploy.New(deploy.WithRegions(nil))
	require.ErrorContains(t, err, "regions must not be nil")
}
