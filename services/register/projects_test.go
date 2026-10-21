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

	link := pkgregister.Link{Project: "QmShop", Config: "github.com/acme/config", Code: []string{"github.com/acme/code"}, Claimant: "user-1"}
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

	mine, err := c.ProjectsOf(ctx, "user-1")
	require.NoError(t, err)
	require.Equal(t, []pkgregister.ProjectRecord{got}, mine)

	all, err := c.ListProjects(ctx)
	require.NoError(t, err)
	require.Equal(t, []pkgregister.ProjectRecord{got}, all)
}

func TestEveryProjectSentinelSurvivesTheWire(t *testing.T) {
	h := newHarness(t, pkgregister.NewMemStore())
	c := newClient(t, h)
	ctx := t.Context()

	_, err := c.LinkProject(ctx, pkgregister.Link{Project: "QmShop", Config: "github.com/acme/config", Code: []string{"github.com/acme/code"}, Claimant: "user-1"})
	require.NoError(t, err)

	_, err = c.LinkProject(ctx, pkgregister.Link{Project: "QmShop", Config: "github.com/evil/config", Claimant: "user-2"})
	require.ErrorIs(t, err, pkgregister.ErrProjectClaimed)
	require.Contains(t, err.Error(), "generate a new id in project.yaml")

	_, err = c.LinkProject(ctx, pkgregister.Link{Project: "QmOther", Config: "github.com/evil/config", Code: []string{"github.com/acme/code"}, Claimant: "user-2"})
	require.ErrorIs(t, err, pkgregister.ErrRepositoryClaimed)

	_, err = c.LinkProject(ctx, pkgregister.Link{Project: "QmOther", Claimant: "user-2"})
	require.ErrorIs(t, err, pkgregister.ErrNoRepository)
	_, err = c.LinkProject(ctx, pkgregister.Link{Project: "QmOther", Config: "github.com/x/y"})
	require.ErrorIs(t, err, pkgregister.ErrNoClaimant)

	_, err = c.Project(ctx, "QmNobody")
	require.ErrorIs(t, err, pkgregister.ErrNotLinked)
	_, err = c.Repository(ctx, "github.com/nobody/here")
	require.ErrorIs(t, err, pkgregister.ErrNotLinked)
	_, err = c.ProjectsOf(ctx, "")
	require.ErrorIs(t, err, pkgregister.ErrNoClaimant)

	store := pkgregister.NewMemStore()
	seeded, err := pkgregister.New(pkgregister.WithStore(store))
	require.NoError(t, err)
	_, err = seeded.LinkProject(ctx, pkgregister.Link{Project: "QmShop", Config: "github.com/acme/config", Claimant: "user-1"})
	require.NoError(t, err)
	broken := newClient(t, newHarness(t, brokenStore{store}))
	_, err = broken.Project(ctx, "QmShop")
	require.Error(t, err)
	_, err = broken.Repository(ctx, "github.com/acme/config")
	require.Error(t, err)
	_, err = broken.ProjectsOf(ctx, "user-1")
	require.Error(t, err)
	_, err = broken.ListProjects(ctx)
	require.Error(t, err)
	_, err = broken.LinkProject(ctx, pkgregister.Link{Project: "QmShop", Config: "github.com/acme/config", Claimant: "user-1"})
	require.Error(t, err)
}

func TestGrantsCrossTheWire(t *testing.T) {
	h := newHarness(t, pkgregister.NewMemStore())
	c := newClient(t, h)
	ctx := t.Context()

	_, err := c.LinkProject(ctx, pkgregister.Link{Project: "QmShop", Config: "github.com/acme/config", Claimant: "user-1"})
	require.NoError(t, err)

	// a relink by the owner crosses with its replace
	rec, err := c.LinkProject(ctx, pkgregister.Link{Project: "QmShop", Config: "github.com/acme-org/config", Claimant: "user-1", Replace: true})
	require.NoError(t, err)
	require.Equal(t, "github.com/acme-org/config", rec.Config)

	role, err := c.RoleOf(ctx, "QmShop", "user-1")
	require.NoError(t, err)
	require.Equal(t, pkgregister.RoleOwner, role)

	g, err := c.GrantRole(ctx, "QmShop", "user-2", pkgregister.RoleBuilder, "user-1")
	require.NoError(t, err)
	require.Equal(t, pkgregister.RoleBuilder, g.Role)
	grants, err := c.Grants(ctx, "QmShop")
	require.NoError(t, err)
	require.Equal(t, []pkgregister.Grant{g}, grants)
	role, err = c.RoleOf(ctx, "QmShop", "user-2")
	require.NoError(t, err)
	require.Equal(t, pkgregister.RoleBuilder, role)

	require.NoError(t, c.Revoke(ctx, "QmShop", g.ID, "user-1"))
	require.ErrorIs(t, c.Revoke(ctx, "QmShop", "NOPE", "user-1"), pkgregister.ErrNoGrant)
	grants, err = c.Grants(ctx, "QmShop")
	require.NoError(t, err)
	require.Empty(t, grants)

	_, err = c.GrantRole(ctx, "QmShop", "user-2", pkgregister.Role("admin"), "user-1")
	require.ErrorIs(t, err, pkgregister.ErrNoRole)
	_, err = c.GrantRole(ctx, "QmShop", "", pkgregister.RoleViewer, "user-1")
	require.ErrorIs(t, err, pkgregister.ErrNoGrantee)
	_, err = c.RoleOf(ctx, "QmNobody", "user-1")
	require.ErrorIs(t, err, pkgregister.ErrNotLinked)
	_, err = c.Grants(ctx, "")
	require.ErrorIs(t, err, pkgregister.ErrNoProject)
}
