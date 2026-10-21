package boot

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/node"
)

type seedVault struct {
	mu    sync.Mutex
	fails int
	set   map[string]string
}

func (v *seedVault) Secret(context.Context, string, string, string) (string, error) {
	return "", errors.New("unused")
}

func (v *seedVault) Set(_ context.Context, p, e, n string, value []byte) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.fails > 0 {
		v.fails--
		return errors.New("no key yet")
	}
	v.set[p+"/"+e+"/"+n] = string(value)
	return nil
}

func (v *seedVault) Delete(context.Context, string, string, string) error { return nil }

func (v *seedVault) List(context.Context, string, string) ([]string, error) { return nil, nil }

func TestASeedFileIsRead(t *testing.T) {
	dir := t.TempDir()
	secrets, err := ReadSeed(filepath.Join(dir, "gone.yaml"))
	require.NoError(t, err, "a seed file already used holds nothing")
	require.Empty(t, secrets)

	path := filepath.Join(dir, "seed.yaml")
	require.NoError(t, os.WriteFile(path, []byte("- project: p\n  environment: e\n  name: n\n  value: v\n"), 0o600))
	secrets, err = ReadSeed(path)
	require.NoError(t, err)
	require.Equal(t, []Secret{{Project: "p", Environment: "e", Name: "n", Value: "v"}}, secrets)

	require.NoError(t, os.WriteFile(path, []byte("{"), 0o600))
	_, err = ReadSeed(path)
	require.Error(t, err)
	_, err = ReadSeed(dir)
	require.Error(t, err)
}

func TestSeedingRetriesUntilTheVaultTakesIt(t *testing.T) {
	seedRetry = time.Millisecond
	v := &seedVault{fails: 2, set: map[string]string{}}
	require.NoError(t, Seed(t.Context(), v, []Secret{{Project: "p", Environment: "e", Name: "a", Value: "1"}, {Project: "p", Environment: "e", Name: "b", Value: "2"}}))
	require.Equal(t, map[string]string{"p/e/a": "1", "p/e/b": "2"}, v.set)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	require.ErrorIs(t, Seed(ctx, &seedVault{fails: 1, set: map[string]string{}}, []Secret{{Name: "a"}}), context.Canceled)
}

func TestANodeSeedsItsVaultFromTheFile(t *testing.T) {
	f := admit(t, bare(t))
	f.Seed = t.TempDir()
	_, err := Start(t.Context(), f)
	require.Error(t, err, "a seed that is not a file")

	f.Seed = filepath.Join(t.TempDir(), "seed.yaml")
	running, err := Start(t.Context(), f)
	require.NoError(t, err, "a seed already used")
	require.NoError(t, running.Close())

	f.Node.Services = append(f.Node.Services, node.Secrets)
	require.NoError(t, os.WriteFile(f.Seed, []byte("- {project: p, environment: e, name: n, value: v}\n"), 0o600))
	running, err = Start(t.Context(), f)
	require.NoError(t, err)
	t.Cleanup(func() { running.Close() })
	require.Eventually(t, func() bool {
		_, err := os.Stat(f.Seed)
		return errors.Is(err, os.ErrNotExist)
	}, time.Minute, 50*time.Millisecond, "the file is removed once the vault holds it")
	got, err := running.Node.Vault().Secret(t.Context(), "p", "e", "n")
	require.NoError(t, err)
	require.Equal(t, "v", got)
}

func TestClosingStopsAnUnfinishedSeed(t *testing.T) {
	f := admit(t, bare(t))
	f.Seed = filepath.Join(t.TempDir(), "seed.yaml")
	require.NoError(t, os.WriteFile(f.Seed, []byte("- {project: p, environment: e, name: n, value: v}\n"), 0o600))
	running, err := Start(t.Context(), f)
	require.NoError(t, err)
	require.NoError(t, running.Close(), "no vault answers, and close does not wait on one")
	_, err = os.Stat(f.Seed)
	require.NoError(t, err, "what was not seeded stays")
}
