package projects

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	authv2 "github.com/taubyte/tau/pkg/proto/gen/tau/auth/v2"
)

const (
	pid    = "QmProjectId"
	cfg    = "github.com/acme/shop-config"
	code   = "github.com/acme/shop-code"
	alice  = "github.com/1001"
	mallet = "github.com/6666"
)

// tick is a clock that moves one second per reading, so claims order by when
// they were made.
func tick(start time.Time) func() time.Time {
	var mu sync.Mutex
	at := start
	return func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		at = at.Add(time.Second)
		return at
	}
}

func linked(t *testing.T, store Store, now func() time.Time) *Table {
	t.Helper()
	if now == nil {
		now = tick(time.Unix(1_700_000_000, 0))
	}
	return New(store, now)
}

func TestALinkIsReadBackAsOneProject(t *testing.T) {
	r := linked(t, newMemStore(), nil)

	rec, err := r.LinkProject(ctx, Link{Project: pid, Config: "GitHub.com/Acme/Shop-Config/", Code: []string{code, code, cfg, ""}, Claimant: alice})
	require.NoError(t, err)
	require.Equal(t, pid, rec.ID)
	require.Equal(t, "GitHub.com/Acme/Shop-Config", rec.Config, "a repository is kept as it was spelled")
	require.Equal(t, []string{code}, rec.Code, "duplicates and the config repository itself are not code")
	require.Equal(t, alice, rec.Claimant)
	require.Equal(t, []string{"GitHub.com/Acme/Shop-Config", code}, rec.Repositories())

	got, err := r.Project(ctx, pid)
	require.NoError(t, err)
	require.Equal(t, rec, got)

	repo, err := r.Repository(ctx, "github.com/ACME/shop-code")
	require.NoError(t, err)
	require.Equal(t, RepositoryRecord{Project: pid, Repository: code}, repo)

	repo, err = r.Repository(ctx, cfg)
	require.NoError(t, err)
	require.True(t, repo.Configures, "and is found however it is spelled")
	require.Equal(t, "GitHub.com/Acme/Shop-Config", repo.Repository)

	_, err = r.LinkProject(ctx, Link{Project: "QmOther", Config: cfg, Claimant: alice})
	require.ErrorIs(t, err, ErrRepositoryClaimed, "a second spelling is the same repository")

	mine, err := r.ProjectsOf(ctx, alice)
	require.NoError(t, err)
	require.Equal(t, []ProjectRecord{rec}, mine)
}

func TestAClaimedIdIsRefusedByName(t *testing.T) {
	r := linked(t, newMemStore(), nil)
	_, err := r.LinkProject(ctx, Link{Project: pid, Config: cfg, Claimant: alice})
	require.NoError(t, err)

	_, err = r.LinkProject(ctx, Link{Project: pid, Config: "github.com/mallet/evil", Claimant: mallet})
	require.ErrorIs(t, err, ErrProjectClaimed)
	require.Contains(t, err.Error(), "project "+pid+" is already claimed by "+cfg+"; generate a new id in project.yaml")

	mine, err := r.ProjectsOf(ctx, mallet)
	require.NoError(t, err)
	require.Empty(t, mine, "a refused claim wrote nothing")
}

func TestARepositoryBelongsToOneProject(t *testing.T) {
	r := linked(t, newMemStore(), nil)
	_, err := r.LinkProject(ctx, Link{Project: pid, Config: cfg, Code: []string{code}, Claimant: alice})
	require.NoError(t, err)

	_, err = r.LinkProject(ctx, Link{Project: "QmOther", Config: "github.com/acme/other", Code: []string{code}, Claimant: alice})
	require.ErrorIs(t, err, ErrRepositoryClaimed)

	_, err = r.LinkProject(ctx, Link{Project: "QmOther", Config: cfg, Claimant: alice})
	require.ErrorIs(t, err, ErrRepositoryClaimed, "a configuration repository configures one project")
}

func TestARelinkKeepsItsPlaceAndAttachesMore(t *testing.T) {
	r := linked(t, newMemStore(), nil)
	first, err := r.LinkProject(ctx, Link{Project: pid, Config: cfg, Claimant: alice})
	require.NoError(t, err)

	again, err := r.LinkProject(ctx, Link{Project: pid, Config: cfg, Code: []string{code}, Claimant: "github.com/1002"})
	require.NoError(t, err)
	require.Equal(t, first.CreatedAt, again.CreatedAt, "a relink never moves a claim later")
	require.Equal(t, alice, again.Claimant, "the first claimant is the one recorded")
	require.Equal(t, []string{code}, again.Code)

	mine, err := r.ProjectsOf(ctx, "github.com/1002")
	require.NoError(t, err)
	require.Len(t, mine, 1, "a second maintainer relinking the owning repository sees it")
}

// merge is what CRDT replication does to two stores that each took a write the
// other had not seen: every key survives, because no key was written by both.
func merge(t *testing.T, a, b *memStore) {
	t.Helper()
	for _, k := range a.Keys() {
		if slices.Contains(b.Keys(), k) {
			t.Fatalf("both sides wrote %s: the layout has a contended key", k)
		}
	}
	for _, pair := range [][2]*memStore{{a, b}, {b, a}} {
		for _, k := range pair[0].Keys() {
			v, _, err := pair[0].Get(ctx, k)
			require.NoError(t, err)
			require.NoError(t, pair[1].Put(ctx, k, v))
		}
	}
	require.Equal(t, a.Keys(), b.Keys())
}

func TestTwoNodesRacingForOneIdAgreeOnTheOwner(t *testing.T) {
	type testCase struct {
		name         string
		atA, atB     time.Time
		wantOwnerCfg string
	}
	for _, tc := range []testCase{
		{"the earlier claim wins", time.Unix(200, 0), time.Unix(100, 0), "github.com/b/config"},
		{"a tie goes to the smaller repository", time.Unix(100, 0), time.Unix(100, 0), "github.com/a/config"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			storeA, storeB := newMemStore(), newMemStore()
			a := linked(t, storeA, func() time.Time { return tc.atA })
			b := linked(t, storeB, func() time.Time { return tc.atB })

			_, err := a.LinkProject(ctx, Link{Project: pid, Config: "github.com/a/config", Code: []string{"github.com/a/code"}, Claimant: alice})
			require.NoError(t, err)
			_, err = b.LinkProject(ctx, Link{Project: pid, Config: "github.com/b/config", Code: []string{"github.com/b/code"}, Claimant: mallet})
			require.NoError(t, err)

			merge(t, storeA, storeB)

			for _, r := range []*Table{a, b} {
				rec, err := r.Project(ctx, pid)
				require.NoError(t, err)
				require.Equal(t, tc.wantOwnerCfg, rec.Config)

				winner := strings.TrimSuffix(tc.wantOwnerCfg, "config") + "code"
				loser := "github.com/a/code"
				if winner == loser {
					loser = "github.com/b/code"
				}
				require.Equal(t, []string{winner}, rec.Code)

				_, err = r.Repository(ctx, loser)
				require.ErrorIs(t, err, ErrNotLinked, "a losing claim's repository is nobody's")
				_, err = r.Repository(ctx, strings.TrimSuffix(loser, "code")+"config")
				require.ErrorIs(t, err, ErrNotLinked)

				for _, who := range []string{alice, mallet} {
					mine, err := r.ProjectsOf(ctx, who)
					require.NoError(t, err)
					if rec.Claimant == who {
						require.Len(t, mine, 1)
					} else {
						require.Empty(t, mine, "the loser does not own what it lost")
					}
				}
			}

			// Once the earlier claim is visible, the later one is refused when
			// it is made again.
			loserCfg := "github.com/a/config"
			if tc.wantOwnerCfg == loserCfg {
				loserCfg = "github.com/b/config"
			}
			_, err = a.LinkProject(ctx, Link{Project: pid, Config: loserCfg, Claimant: alice})
			require.ErrorIs(t, err, ErrProjectClaimed)
		})
	}
}

func TestARaceSeenAfterTheWriteIsReported(t *testing.T) {
	store := &racingStore{memStore: newMemStore()}
	r := linked(t, store, func() time.Time { return time.Unix(500, 0) })
	store.onClaim = func() {
		// Another node's earlier claim on the same id replicates in between.
		replicate(t, store.memStore, Link{Project: pid, Config: "github.com/z/first", Claimant: mallet})
	}
	_, err := r.LinkProject(ctx, Link{Project: pid, Config: cfg, Claimant: alice})
	require.ErrorIs(t, err, ErrProjectClaimed)
	require.Contains(t, err.Error(), "github.com/z/first")
}

func TestACodeRepositoryRaceSeenAfterTheWriteIsReported(t *testing.T) {
	store := &racingStore{memStore: newMemStore()}
	r := linked(t, store, func() time.Time { return time.Unix(500, 0) })
	store.onClaim = func() {
		replicate(t, store.memStore, Link{Project: "QmOther", Config: "github.com/z/other", Code: []string{code}, Claimant: mallet})
	}
	_, err := r.LinkProject(ctx, Link{Project: pid, Config: cfg, Code: []string{code}, Claimant: alice})
	require.ErrorIs(t, err, ErrRepositoryClaimed)
	require.Contains(t, err.Error(), "QmOther")
}

// replicate is another node's earlier link arriving: made on its own store,
// then copied in.
func replicate(t *testing.T, into *memStore, l Link) {
	t.Helper()
	elsewhere := newMemStore()
	_, err := linked(t, elsewhere, func() time.Time { return time.Unix(100, 0) }).LinkProject(ctx, l)
	require.NoError(t, err)
	for _, k := range elsewhere.Keys() {
		v, _, err := elsewhere.Get(ctx, k)
		require.NoError(t, err)
		require.NoError(t, into.Put(ctx, k, v))
	}
}

type racingStore struct {
	*memStore
	onClaim func()
	once    sync.Once
}

func (s *racingStore) Put(c context.Context, key string, v []byte) error {
	if err := s.memStore.Put(c, key, v); err != nil {
		return err
	}
	if strings.HasPrefix(key, claimPrefix) && s.onClaim != nil {
		s.once.Do(s.onClaim)
	}
	return nil
}

func TestALinkIsRefusedWhenItNamesNothing(t *testing.T) {
	r := linked(t, newMemStore(), nil)
	type testCase struct {
		link Link
		want error
	}
	for _, tc := range []testCase{
		{Link{Config: cfg, Claimant: alice}, ErrNoProject},
		{Link{Project: "a/b", Config: cfg, Claimant: alice}, ErrSeparator},
		{Link{Project: pid, Config: " / ", Claimant: alice}, ErrNoRepository},
		{Link{Project: pid, Config: cfg, Claimant: " "}, ErrNoClaimant},
	} {
		_, err := r.LinkProject(ctx, tc.link)
		require.ErrorIs(t, err, tc.want)
	}

	_, err := r.Project(ctx, "")
	require.ErrorIs(t, err, ErrNoProject)
	_, err = r.Project(ctx, pid)
	require.ErrorIs(t, err, ErrNotLinked)
	_, err = r.Repository(ctx, "")
	require.ErrorIs(t, err, ErrNoRepository)
	_, err = r.Repository(ctx, code)
	require.ErrorIs(t, err, ErrNotLinked)
	_, err = r.ProjectsOf(ctx, "")
	require.ErrorIs(t, err, ErrNoClaimant)
}

func TestAnUnreadableRowOwnsNothing(t *testing.T) {
	store := newMemStore()
	r := linked(t, store, nil)
	_, err := r.LinkProject(ctx, Link{Project: pid, Config: cfg, Code: []string{code}, Claimant: alice})
	require.NoError(t, err)

	for _, key := range []string{
		claimKey(pid, "github.com/0/early"),
		repoKey(code, "QmBroken"),
		codeKey(pid, cfg, "github.com/x/never") + "x!",
		mineKey(alice, "QmBroken"),
		claimPrefix + pid + sep + "!!",
		repoPrefix + segment(code) + sep,
		minePrefix + segment(alice) + sep + "QmGhost",
	} {
		require.NoError(t, store.Put(ctx, key, []byte("not a row")))
	}
	require.NoError(t, store.Put(ctx, codeKey(pid, cfg, "github.com/x/orphan"), row(t, &authv2.ProjectClaimRow{At: 1})))

	rec, err := r.Project(ctx, pid)
	require.NoError(t, err)
	require.Equal(t, cfg, rec.Config)
	require.Equal(t, []string{code}, rec.Code, "a code row with no repository claim behind it attaches nothing")

	repo, err := r.Repository(ctx, code)
	require.NoError(t, err)
	require.Equal(t, pid, repo.Project)

	mine, err := r.ProjectsOf(ctx, alice)
	require.NoError(t, err)
	require.Len(t, mine, 1)
}

func TestAStoreThatCannotLookIsNotAMissingProject(t *testing.T) {
	for _, f := range []*failing{{get: true}, {put: true}, {list: true}} {
		f.memStore = newMemStore()
		_, err := linked(t, f.memStore, nil).LinkProject(ctx, Link{Project: pid, Config: cfg, Code: []string{code}, Claimant: alice})
		require.NoError(t, err)
		r := linked(t, f, nil)
		_, err = r.LinkProject(ctx, Link{Project: pid, Config: cfg, Code: []string{code, "github.com/acme/new"}, Claimant: alice})
		require.ErrorIs(t, err, errFailing)
		if f.put {
			continue
		}
		_, err = r.Project(ctx, pid)
		require.ErrorIs(t, err, errFailing)
		_, err = r.Repository(ctx, code)
		require.ErrorIs(t, err, errFailing)
		_, err = r.ProjectsOf(ctx, alice)
		require.ErrorIs(t, err, errFailing)
	}
}

func TestEveryStoreFailureDuringALinkSurfaces(t *testing.T) {
	for n := 1; ; n++ {
		store := &countingStore{memStore: newMemStore()}
		r := linked(t, store, nil)
		_, err := r.LinkProject(ctx, Link{Project: pid, Config: cfg, Code: []string{code}, Claimant: alice})
		require.NoError(t, err)
		rows := store.calls

		store.calls, store.failAt = 0, n
		_, err = r.LinkProject(ctx, Link{Project: pid, Config: cfg, Code: []string{code, "github.com/acme/more"}, Claimant: alice})
		if store.calls < n {
			require.NoError(t, err)
			require.Positive(t, rows)
			return
		}
		require.ErrorIs(t, err, errFailing, "call %d", n)

		store.calls, store.failAt = 0, n
		_, err = r.ProjectsOf(ctx, alice)
		if store.calls >= n {
			require.ErrorIs(t, err, errFailing, "call %d", n)
		}
	}
}

type countingStore struct {
	*memStore
	calls, failAt int
}

func (s *countingStore) hit() error {
	s.calls++
	if s.failAt > 0 && s.calls == s.failAt {
		return errFailing
	}
	return nil
}

func (s *countingStore) Get(c context.Context, k string) ([]byte, bool, error) {
	if err := s.hit(); err != nil {
		return nil, false, err
	}
	return s.memStore.Get(c, k)
}

func (s *countingStore) Put(c context.Context, k string, v []byte) error {
	if err := s.hit(); err != nil {
		return err
	}
	return s.memStore.Put(c, k, v)
}

func (s *countingStore) List(c context.Context, p string) ([]string, error) {
	if err := s.hit(); err != nil {
		return nil, err
	}
	return s.memStore.List(c, p)
}

func TestEveryLinkedProjectIsListedOnce(t *testing.T) {
	storeA, storeB := newMemStore(), newMemStore()
	a := linked(t, storeA, func() time.Time { return time.Unix(1, 0) })
	b := linked(t, storeB, func() time.Time { return time.Unix(2, 0) })
	_, err := a.LinkProject(ctx, Link{Project: pid, Config: cfg, Claimant: alice})
	require.NoError(t, err)
	_, err = b.LinkProject(ctx, Link{Project: pid, Config: "github.com/b/config", Claimant: mallet})
	require.NoError(t, err)
	_, err = a.LinkProject(ctx, Link{Project: "QmAnother", Config: "github.com/acme/another", Claimant: alice})
	require.NoError(t, err)
	merge(t, storeA, storeB)
	require.NoError(t, storeA.Put(ctx, claimPrefix+"QmBroken"+sep+segment("x"), []byte("not a row")))

	all, err := a.ListProjects(ctx)
	require.NoError(t, err)
	require.Len(t, all, 2, "two claims on one id are one project, and an unreadable claim is none")
	require.Equal(t, pid, all[1].ID)
	require.Equal(t, cfg, all[1].Config)

	_, err = linked(t, &failing{memStore: storeA, list: true}, nil).ListProjects(ctx)
	require.ErrorIs(t, err, errFailing)
	_, err = linked(t, &failing{memStore: storeA, get: true}, nil).ListProjects(ctx)
	require.ErrorIs(t, err, errFailing)
}
