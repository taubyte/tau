package container

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/invoke"
)

// A volume is the one unserved capability that loses data rather than failing, so
// it is refused when there is nothing to mount, and mounted at the author's path
// when a Volumes backing exists.

// mountTier is a runtime over a backend that starts and listens, so a test can
// tell "it refused" from "it could not have started anyway".
func mountTier(t *testing.T, adjust func() []Option) *Runtime {
	t.Helper()
	rt := newFakeBackend()
	rt.imagePresent = true
	return newRuntime(t, rt, Image{Ref: "acme/shop:1"}, adjust)
}

func mountGrant(label, path string) invoke.Grant {
	return invoke.Grant{
		Kind: invoke.MountKind, Label: label,
		Ops:      []string{"mount-rw"},
		Settings: map[string]string{"path": path},
	}
}

func TestAWorkloadThatDeclaresAMountIsRefused(t *testing.T) {
	_, err := mountTier(t, nil).Start(context.Background(), invoke.Spec{
		Name:     "shop/prod/application/db",
		Artifact: "postgres:16",
		Reaches:  []invoke.Grant{mountGrant("pgdata", "/var/lib/postgresql/data")},
	})

	require.ErrorIs(t, err, ErrNoMounts)
	require.ErrorContains(t, err, "pgdata at /var/lib/postgresql/data")
	require.ErrorContains(t, err, "discarded with the container")
}

func TestEveryMountIsNamedAtOnce(t *testing.T) {
	_, err := mountTier(t, nil).Start(context.Background(), invoke.Spec{
		Name:     "app",
		Artifact: "x",
		Reaches: []invoke.Grant{
			mountGrant("uploads", "/srv/uploads"),
			mountGrant("pgdata", "/var/lib/postgresql/data"),
		},
	})

	require.ErrorIs(t, err, ErrNoMounts)
	require.ErrorContains(t, err, "pgdata at /var/lib/postgresql/data, uploads at /srv/uploads")
}

func TestAMountWithNoPathIsRefused(t *testing.T) {
	_, err := mountTier(t, nil).Start(context.Background(), invoke.Spec{
		Name: "app", Artifact: "x",
		Reaches: []invoke.Grant{{Kind: invoke.MountKind, Label: "data", Ops: []string{"mount-ro"}}},
	})

	require.ErrorIs(t, err, ErrNoMounts)
	require.ErrorContains(t, err, "data at an unnamed path")
}

func TestOtherUnservedAccessWarnsRatherThanRefusing(t *testing.T) {
	var warnings []string
	runtime := mountTier(t, func() []Option {
		return []Option{WithWarn(func(format string, args ...any) {
			warnings = append(warnings, fmt.Sprintf(format, args...))
		})}
	})

	_, err := runtime.Start(context.Background(), invoke.Spec{
		Name: "shop/prod/application/api", Artifact: "x",
		Reaches: []invoke.Grant{
			{Kind: "database", Label: "ledger", Ops: []string{"query"}},
			{Kind: "cache", Label: "sessions", Ops: []string{"get"}},
		},
	})
	require.NoError(t, err, "it started")

	joined := strings.Join(warnings, "\n")
	require.Contains(t, joined, "does not serve")
	require.Contains(t, joined, "cache, database", "sorted, and both named")
}

func TestNoAccessMeansNoWarning(t *testing.T) {
	var warnings []string
	runtime := mountTier(t, func() []Option {
		return []Option{WithWarn(func(format string, args ...any) {
			warnings = append(warnings, fmt.Sprintf(format, args...))
		})}
	})

	_, err := runtime.Start(context.Background(), invoke.Spec{Name: "app", Artifact: "x"})
	require.NoError(t, err)

	for _, w := range warnings {
		require.NotContains(t, w, "does not serve", w)
	}
}

// fakeVolumes hands back a path per label and records what it was asked for.
type fakeVolumes struct {
	err   error
	asked []invoke.Grant
}

func (f *fakeVolumes) Mount(_ context.Context, grant invoke.Grant) (string, error) {
	f.asked = append(f.asked, grant)
	if f.err != nil {
		return "", f.err
	}
	return "/srv/tau/volumes/" + grant.Label, nil
}

func TestADeclaredMountBecomesAContainerMount(t *testing.T) {
	disks := &fakeVolumes{}
	rt := newFakeBackend()
	rt.imagePresent = true
	tier := newRuntime(t, rt, Image{Ref: "postgres:16"}, func() []Option {
		return []Option{WithVolumes(disks)}
	})

	_, err := tier.Start(context.Background(), invoke.Spec{
		Name:     "shop/prod/application/db",
		Artifact: "postgres:16",
		Reaches:  []invoke.Grant{mountGrant("pgdata", "/var/lib/postgresql/data")},
	})
	require.NoError(t, err)

	require.Len(t, rt.created, 1)
	mounts := rt.created[0].Volumes
	require.Len(t, mounts, 1)
	require.Equal(t, "/srv/tau/volumes/pgdata", mounts[0].Source)
	require.Equal(t, "/var/lib/postgresql/data", mounts[0].Destination)
	require.False(t, mounts[0].ReadOnly, "the grant asked to write")

	require.Len(t, disks.asked, 1)
	require.Equal(t, "pgdata", disks.asked[0].Label)
}

func TestAReadOnlyGrantIsMountedReadOnly(t *testing.T) {
	rt := newFakeBackend()
	rt.imagePresent = true
	tier := newRuntime(t, rt, Image{Ref: "acme/shop:1"}, func() []Option {
		return []Option{WithVolumes(&fakeVolumes{})}
	})

	grant := mountGrant("assets", "/srv/assets")
	grant.Ops = []string{"mount-ro"}

	_, err := tier.Start(context.Background(), invoke.Spec{
		Name: "shop/prod/application/web", Artifact: "acme/shop:1",
		Reaches: []invoke.Grant{grant},
	})
	require.NoError(t, err)
	require.True(t, rt.created[0].Volumes[0].ReadOnly)
}

func TestMountsAreOrderedByDestination(t *testing.T) {
	rt := newFakeBackend()
	rt.imagePresent = true
	tier := newRuntime(t, rt, Image{Ref: "acme/shop:1"}, func() []Option {
		return []Option{WithVolumes(&fakeVolumes{})}
	})

	_, err := tier.Start(context.Background(), invoke.Spec{
		Name: "shop/prod/application/db", Artifact: "acme/shop:1",
		Reaches: []invoke.Grant{
			mountGrant("wal", "/var/lib/wal"),
			mountGrant("pgdata", "/var/lib/postgresql/data"),
		},
	})
	require.NoError(t, err)

	mounts := rt.created[0].Volumes
	require.Len(t, mounts, 2)
	require.Equal(t, "/var/lib/postgresql/data", mounts[0].Destination)
	require.Equal(t, "/var/lib/wal", mounts[1].Destination)
}

func TestARefusedMountGrantStillRefusesTheWorkload(t *testing.T) {
	tier := mountTier(t, func() []Option { return []Option{WithVolumes(&fakeVolumes{})} })

	grant := mountGrant("pgdata", "/var/lib/postgresql/data")
	grant.Refused = "this cloud has no disk for it"

	_, err := tier.Start(context.Background(), invoke.Spec{
		Name: "shop/prod/application/db", Artifact: "postgres:16",
		Reaches: []invoke.Grant{grant},
	})
	require.ErrorIs(t, err, ErrNoMounts)
	require.ErrorContains(t, err, "this cloud has no disk for it")
}

func TestAVolumeBackingThatFailsRefusesTheWorkload(t *testing.T) {
	tier := mountTier(t, func() []Option {
		return []Option{WithVolumes(&fakeVolumes{err: fmt.Errorf("no room")})}
	})

	_, err := tier.Start(context.Background(), invoke.Spec{
		Name: "shop/prod/application/db", Artifact: "postgres:16",
		Reaches: []invoke.Grant{mountGrant("pgdata", "/var/lib/postgresql/data")},
	})
	require.ErrorIs(t, err, ErrNoMounts)
	require.ErrorContains(t, err, "no room")
}
