package container

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/invoke"
)

// mount_test.go is about the one unserved capability that LOSES DATA.
//
// Everything else an access block declares fails where it is used: a database
// nothing connected to is a connection error the workload itself reports, at
// the moment it tries. A VOLUME does not. A container started without the
// filesystem it declared writes into its own layer, which is discarded with the
// container — and nothing anywhere says so until somebody goes looking for the
// data.
//
// So it refuses, and the message names what was wanted and where.

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

// TestTheRefusalNamesEveryMount, sorted, so a workload with three of them gets
// one message naming all three rather than one at a time across three deploys.
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

// TestAMountWithNoPathIsStillRefused, because the path is the entry's and an
// entry that omitted it is still a filesystem nobody gets.
func TestAMountWithNoPathIsRefused(t *testing.T) {
	_, err := mountTier(t, nil).Start(context.Background(), invoke.Spec{
		Name: "app", Artifact: "x",
		Reaches: []invoke.Grant{{Kind: invoke.MountKind, Label: "data", Ops: []string{"mount-ro"}}},
	})

	require.ErrorIs(t, err, ErrNoMounts)
	require.ErrorContains(t, err, "data at an unnamed path")
}

// TestEverythingElseWarnsAndStarts. A database, a cache or a stream this tier
// does not inject fails where the workload uses it, loudly and by itself — so
// refusing would take down a workload over something it may never touch.
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

// TestAWorkloadThatReachesNothingIsQuiet, so the warning means something when
// it appears.
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
