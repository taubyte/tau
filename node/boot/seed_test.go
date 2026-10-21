package boot

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/crypto"
	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/taubyte/tau/node"
)

// peerOf is the peer id of f's private key.
func peerOf(t *testing.T, f File) string {
	t.Helper()
	raw, err := identity(f.PrivateKey)
	require.NoError(t, err)
	key, err := crypto.UnmarshalPrivateKey(raw)
	require.NoError(t, err)
	id, err := libp2ppeer.IDFromPrivateKey(key)
	require.NoError(t, err)
	return id.String()
}

type seedVault struct {
	mu      sync.Mutex
	fails   int
	set     map[string]string
	writers map[string]int
}

func newSeedVault() *seedVault {
	return &seedVault{set: map[string]string{}, writers: map[string]int{}}
}

func (v *seedVault) Secret(_ context.Context, p, e, n string) (string, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	s, ok := v.set[p+"/"+e+"/"+n]
	if !ok {
		return "", errors.New("not set")
	}
	return s, nil
}

func (v *seedVault) Set(ctx context.Context, p, e, n string, value []byte) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.fails > 0 {
		v.fails--
		return errors.New("no key yet")
	}
	v.set[p+"/"+e+"/"+n] = string(value)
	if w, ok := ctx.Value(writerKey{}).(string); ok {
		v.writers[w]++
	}
	return nil
}

func (v *seedVault) Delete(context.Context, string, string, string) error { return nil }

func (v *seedVault) List(context.Context, string, string) ([]string, error) { return nil, nil }

type writerKey struct{}

func TestASeedFileIsRead(t *testing.T) {
	dir := t.TempDir()
	f, err := ReadSeed(filepath.Join(dir, "gone.yaml"))
	require.NoError(t, err, "a seed file already used holds nothing")
	require.Empty(t, f.Secrets)

	path := filepath.Join(dir, "seed.yaml")
	require.NoError(t, os.WriteFile(path, []byte("holders: [a]\nsecrets:\n  - {project: p, environment: e, name: n, value: v}\n"), 0o600))
	f, err = ReadSeed(path)
	require.NoError(t, err)
	require.Equal(t, SeedFile{Holders: []string{"a"}, Secrets: []Secret{{Project: "p", Environment: "e", Name: "n", Value: "v"}}}, f)

	require.NoError(t, os.WriteFile(path, []byte("{"), 0o600))
	_, err = ReadSeed(path)
	require.Error(t, err)
	_, err = ReadSeed(dir)
	require.Error(t, err)
}

var two = SeedFile{Holders: []string{"b", "a"}, Secrets: []Secret{{Project: "p", Environment: "e", Name: "x", Value: "1"}, {Project: "p", Environment: "e", Name: "y", Value: "2"}}}

func TestTheLowestLiveHolderWritesAndRetriesUntilTheVaultTakesIt(t *testing.T) {
	seedRetry = time.Millisecond
	v := newSeedVault()
	v.fails = 2
	none := func() []string { return nil }
	require.NoError(t, Seed(context.WithValue(t.Context(), writerKey{}, "a"), v, "a", none, two))
	require.Equal(t, map[string]string{"p/e/x": "1", "p/e/y": "2"}, v.set)

	// A value the vault holds differently is written again.
	v.set["p/e/x"] = "old"
	require.NoError(t, Seed(t.Context(), v, "a", none, two))
	require.Equal(t, "1", v.set["p/e/x"])

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	require.ErrorIs(t, Seed(ctx, newSeedVault(), "a", none, two), context.Canceled)
}

// TestADeadWriterIsReplaced: b waits while a, the lower holder, is alive, and
// writes once a is gone; both return once the vault holds the seed.
func TestADeadWriterIsReplaced(t *testing.T) {
	seedRetry = time.Millisecond
	v := newSeedVault()
	var mu sync.Mutex
	alive := []string{"a", "b"}
	live := func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), alive...)
	}

	done := make(chan error, 1)
	go func() { done <- Seed(context.WithValue(t.Context(), writerKey{}, "b"), v, "b", live, two) }()
	time.Sleep(50 * time.Millisecond)
	v.mu.Lock()
	require.Empty(t, v.set, "b does not write while a, the lower holder, is alive")
	v.mu.Unlock()

	mu.Lock()
	alive = []string{"b"}
	mu.Unlock()
	require.NoError(t, <-done)
	require.Equal(t, map[string]string{"p/e/x": "1", "p/e/y": "2"}, v.set)
	require.Equal(t, map[string]int{"b": 2}, v.writers, "the next holder wrote in a's place")

	// a, back, finds the seed held and writes nothing.
	require.NoError(t, Seed(context.WithValue(t.Context(), writerKey{}, "a"), v, "a", live, two))
	require.Equal(t, map[string]int{"b": 2}, v.writers)
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
	writeSeed(t, f.Seed, SeedFile{Holders: []string{peerOf(t, f)}, Secrets: []Secret{{Project: "p", Environment: "e", Name: "n", Value: "v"}}})
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
	writeSeed(t, f.Seed, SeedFile{Holders: []string{peerOf(t, f)}, Secrets: []Secret{{Project: "p", Environment: "e", Name: "n", Value: "v"}}})
	running, err := Start(t.Context(), f)
	require.NoError(t, err)
	require.NoError(t, running.Close(), "no vault answers, and close does not wait on one")
	_, err = os.Stat(f.Seed)
	require.NoError(t, err, "what was not seeded stays")
}

func writeSeed(t *testing.T, path string, f SeedFile) {
	t.Helper()
	data, err := yaml.Marshal(f)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, data, 0o600))
}
