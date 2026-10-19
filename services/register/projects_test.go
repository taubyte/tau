package register

import (
	"testing"

	"github.com/stretchr/testify/require"

	pkgregister "github.com/taubyte/tau/pkg/register"
)

func TestTheProjectTableCrossesTheWire(t *testing.T) {
	h := newHarness(t, pkgregister.NewMemStore())
	c := newClient(t, h)
	ctx := t.Context()

	link := pkgregister.Link{Project: "QmShop", Config: "github.com/acme/config", Code: []string{"github.com/acme/code"}, Claimant: "github.com/1"}
	rec, err := c.LinkProject(ctx, link)
	require.NoError(t, err)
	require.Equal(t, "github.com/acme/config", rec.Config)
	require.Equal(t, []string{"github.com/acme/code"}, rec.Code)
	require.False(t, rec.CreatedAt.IsZero())

	local, err := h.Service.Local().Project(ctx, "QmShop")
	require.NoError(t, err)
	got, err := c.Project(ctx, "QmShop")
	require.NoError(t, err)
	require.Equal(t, local, got, "the client is the same table")

	repo, err := c.Repository(ctx, "github.com/acme/config")
	require.NoError(t, err)
	require.Equal(t, pkgregister.RepositoryRecord{Project: "QmShop", Repository: "github.com/acme/config", Configures: true}, repo)

	mine, err := c.ProjectsOf(ctx, "github.com/1")
	require.NoError(t, err)
	require.Equal(t, []pkgregister.ProjectRecord{got}, mine)
}

func TestEveryProjectSentinelSurvivesTheWire(t *testing.T) {
	h := newHarness(t, pkgregister.NewMemStore())
	c := newClient(t, h)
	ctx := t.Context()

	_, err := c.LinkProject(ctx, pkgregister.Link{Project: "QmShop", Config: "github.com/acme/config", Code: []string{"github.com/acme/code"}, Claimant: "github.com/1"})
	require.NoError(t, err)

	_, err = c.LinkProject(ctx, pkgregister.Link{Project: "QmShop", Config: "github.com/evil/config", Claimant: "github.com/2"})
	require.ErrorIs(t, err, pkgregister.ErrProjectClaimed)
	require.Contains(t, err.Error(), "generate a new id in project.yaml")

	_, err = c.LinkProject(ctx, pkgregister.Link{Project: "QmOther", Config: "github.com/evil/config", Code: []string{"github.com/acme/code"}, Claimant: "github.com/2"})
	require.ErrorIs(t, err, pkgregister.ErrRepositoryClaimed)

	_, err = c.LinkProject(ctx, pkgregister.Link{Project: "QmOther", Claimant: "github.com/2"})
	require.ErrorIs(t, err, pkgregister.ErrNoRepository)
	_, err = c.LinkProject(ctx, pkgregister.Link{Project: "QmOther", Config: "github.com/x/y"})
	require.ErrorIs(t, err, pkgregister.ErrNoClaimant)

	_, err = c.Project(ctx, "QmNobody")
	require.ErrorIs(t, err, pkgregister.ErrNotLinked)
	_, err = c.Repository(ctx, "github.com/nobody/here")
	require.ErrorIs(t, err, pkgregister.ErrNotLinked)
	_, err = c.ProjectsOf(ctx, "")
	require.ErrorIs(t, err, pkgregister.ErrNoClaimant)

	broken := newClient(t, newHarness(t, brokenStore{pkgregister.NewMemStore()}))
	_, err = broken.Project(ctx, "QmShop")
	require.Error(t, err)
}
