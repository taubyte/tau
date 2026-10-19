package deploy_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
	"github.com/taubyte/tau/services/build/deploy"
)

// A REGION THIS CLOUD DOES NOT SERVE IS SAID, NOT DROPPED. The basic fixture says
// where its scopes and resources run; a deploy given no Regions serves one, places
// them all there, publishes, and says so in the log the developer reads.
func TestARegionThisCloudDoesNotServeIsSaid(t *testing.T) {
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
