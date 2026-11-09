package projects

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	kvdbmock "github.com/taubyte/tau/pkg/kvdb/mock"
)

func TestAnOwnerRelinkReplacesTheRepositories(t *testing.T) {
	r := New(newMemStore(), time.Now)
	ctx := t.Context()
	_, err := r.LinkProject(ctx, Link{Project: "QmPay", Config: "github.com/acme/cfg", Code: []string{"github.com/acme/pay"}, Claimant: "alice"})
	require.NoError(t, err)

	rec, err := r.LinkProject(ctx, Link{Project: "QmPay", Config: "github.com/acme-renamed/cfg", Code: []string{"github.com/acme/web"}, Claimant: "carol", Replace: true})
	require.NoError(t, err)
	require.Equal(t, "github.com/acme-renamed/cfg", rec.Config)
	require.Equal(t, []string{"github.com/acme/web"}, rec.Code)
	require.Equal(t, "alice", rec.Claimant, "a relink keeps the owner")
}

func TestARelinkKeepingTheConfigDropsOnlyWhatItLeavesOut(t *testing.T) {
	r := linked(t, newMemStore(), nil)
	_, err := r.LinkProject(ctx, Link{Project: pid, Config: cfg, Code: []string{code, "github.com/acme/web"}, Claimant: alice})
	require.NoError(t, err)

	rec, err := r.LinkProject(ctx, Link{Project: pid, Config: cfg, Code: []string{code}, Claimant: alice, Replace: true})
	require.NoError(t, err)
	require.Equal(t, []string{code}, rec.Code)
	_, err = r.Repository(ctx, "github.com/acme/web")
	require.ErrorIs(t, err, ErrNotLinked, "the dropped repository is released")
	held, err := r.Repository(ctx, code)
	require.NoError(t, err)
	require.Equal(t, pid, held.Project)

	// the old configuration, made a code repository, stays the project's
	rec, err = r.LinkProject(ctx, Link{Project: pid, Config: "github.com/acme/new-cfg", Code: []string{cfg}, Claimant: alice, Replace: true})
	require.NoError(t, err)
	require.Equal(t, []string{cfg}, rec.Code)
	held, err = r.Repository(ctx, cfg)
	require.NoError(t, err)
	require.False(t, held.Configures)

	mine, err := r.ProjectsOf(ctx, alice)
	require.NoError(t, err)
	require.Len(t, mine, 1)
	require.Equal(t, "github.com/acme/new-cfg", mine[0].Config)

	// a link without replace still cannot move the configuration
	_, err = r.LinkProject(ctx, Link{Project: pid, Config: "github.com/acme/third", Claimant: alice})
	require.ErrorIs(t, err, ErrProjectClaimed)
}

func TestARelinkOverAFailingStore(t *testing.T) {
	type testCase struct{ op, sub string }
	for _, c := range []testCase{
		{"list", codePrefix}, {"list", claimPrefix + pid}, {"put", claimPrefix}, {"put", codePrefix}, {"put", repoPrefix},
	} {
		store := &on{memStore: newMemStore()}
		r := linked(t, store, nil)
		_, err := r.LinkProject(ctx, Link{Project: pid, Config: cfg, Code: []string{code}, Claimant: alice})
		require.NoError(t, err)
		_, err = r.LinkProject(ctx, Link{Project: pid, Config: "github.com/acme/other", Claimant: alice})
		require.Error(t, err)
		store.op, store.sub = c.op, c.sub
		_, err = r.LinkProject(ctx, Link{Project: pid, Config: "github.com/acme/new", Claimant: alice, Replace: true})
		require.ErrorIs(t, err, errFailing, c.op+" "+c.sub)
	}
}

func TestARelinkOverADatastore(t *testing.T) {
	db, err := kvdbmock.New().New(nil, "auth", 0)
	require.NoError(t, err)
	defer db.Close()
	r := New(Over(db), time.Now)
	ctx := t.Context()
	_, err = r.LinkProject(ctx, Link{Project: "QmPay", Config: "github.com/acme/cfg", Claimant: "alice"})
	require.NoError(t, err)
	rec, err := r.LinkProject(ctx, Link{Project: "QmPay", Config: "github.com/acme/cfg", Code: []string{"github.com/acme/pay"}, Claimant: "alice", Replace: true})
	require.NoError(t, err)
	require.Equal(t, []string{"github.com/acme/pay"}, rec.Code)
	rec, err = r.LinkProject(ctx, Link{Project: "QmPay", Config: "github.com/acme/other", Claimant: "alice", Replace: true})
	require.NoError(t, err)
	require.Equal(t, "github.com/acme/other", rec.Config)
	repo, err := r.Repository(ctx, "github.com/acme/other")
	require.NoError(t, err)
	require.True(t, repo.Configures)
	_, err = r.Repository(ctx, "github.com/acme/cfg")
	require.ErrorIs(t, err, ErrNotLinked, "the configuration a relink left is released")
}
