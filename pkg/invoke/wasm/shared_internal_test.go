package wasm

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/invoke"
)

// counted is a component store that counts its reads.
type counted struct {
	code  []byte
	reads atomic.Int32
	err   error
}

func (c *counted) Read(context.Context, string) (io.ReadCloser, error) {
	c.reads.Add(1)
	if c.err != nil {
		return nil, c.err
	}
	return io.NopCloser(bytes.NewReader(c.code)), nil
}

func sharedTier(t *testing.T, keep time.Duration) (*Runtime, *counted) {
	t.Helper()
	code, err := os.ReadFile("fixtures/handler.wasm")
	require.NoError(t, err)
	assets := &counted{code: code}
	r, err := New(assets, WithKeep(keep))
	require.NoError(t, err)
	t.Cleanup(func() { r.Close() })
	return r, assets
}

func compiledFor(r *Runtime) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.compiled)
}

var sharedSpec = invoke.Spec{Name: "shop/prod/function/checkout", Artifact: "bafycomponent"}

func TestInstancesOfOneArtifactShareOneCompiledRuntime(t *testing.T) {
	r, assets := sharedTier(t, time.Hour)

	var wg sync.WaitGroup
	instances := make([]invoke.Instance, 8)
	for i := range instances {
		wg.Add(1)
		go func() {
			defer wg.Done()
			instance, err := r.Start(t.Context(), sharedSpec)
			require.NoError(t, err)
			instances[i] = instance
		}()
	}
	wg.Wait()
	require.Equal(t, 1, compiledFor(r), "eight first starts, one runtime: the ones that lost the race closed theirs")

	for _, instance := range instances {
		res, err := instance.Serve(t.Context(), &invoke.Request{Method: "GET", Path: "/"})
		require.NoError(t, err)
		require.Equal(t, 200, res.Status)
	}
	for _, instance := range instances {
		require.NoError(t, instance.Close())
	}

	reads := assets.reads.Load()
	again, err := r.Start(t.Context(), sharedSpec)
	require.NoError(t, err)
	require.Equal(t, reads, assets.reads.Load(), "a start within Keep finds the component compiled and its bytes held")
	require.NoError(t, again.Close())

	bigger := sharedSpec
	bigger.Memory = 64 << 20
	other, err := r.Start(t.Context(), bigger)
	require.NoError(t, err)
	require.Equal(t, 2, compiledFor(r), "a memory ceiling is a runtime's, so another ceiling is another runtime")
	require.NoError(t, other.Close())
}

func TestARuntimeNoInstanceHoldsIsLetGoAfterKeep(t *testing.T) {
	r, _ := sharedTier(t, 10*time.Millisecond)
	instance, err := r.Start(t.Context(), sharedSpec)
	require.NoError(t, err)
	require.NoError(t, instance.Close())
	require.Eventually(t, func() bool { return compiledFor(r) == 0 }, 5*time.Second, 5*time.Millisecond)

	again, err := r.Start(t.Context(), sharedSpec)
	require.NoError(t, err, "and is compiled again for the next start")
	require.NoError(t, again.Close())
}

func TestAStartThatFailsLetsGoOfTheRuntime(t *testing.T) {
	r, _ := sharedTier(t, time.Hour)
	instance, err := r.Start(t.Context(), sharedSpec)
	require.NoError(t, err)
	require.NoError(t, instance.Close())

	r.mu.Lock()
	c := r.compiled[runtimeKey{artifact: sharedSpec.Artifact}]
	r.mu.Unlock()
	garbled := &compiled{key: c.key, runtime: c.runtime, cache: c.cache, code: []byte("not a component")}
	r.mu.Lock()
	r.compiled[c.key] = garbled
	r.mu.Unlock()

	_, err = r.Start(t.Context(), sharedSpec)
	require.ErrorContains(t, err, "instantiating")
	r.mu.Lock()
	refs := garbled.refs
	r.mu.Unlock()
	require.Zero(t, refs, "a failed instantiation holds nothing")

	r.mu.Lock()
	r.compiled[c.key] = c
	r.mu.Unlock()
}

func TestAComponentThatCannotBeFetchedIsSaidSo(t *testing.T) {
	r, assets := sharedTier(t, time.Hour)
	assets.err = errors.New("no such cid")
	_, err := r.Start(t.Context(), sharedSpec)
	require.ErrorContains(t, err, "fetching bafycomponent")
	require.Zero(t, compiledFor(r))
}

// broken reads part of a component and then fails.
type broken struct{}

func (broken) Read(context.Context, string) (io.ReadCloser, error) {
	return io.NopCloser(io.MultiReader(bytes.NewReader([]byte{0}), failing{})), nil
}

type failing struct{}

func (failing) Read([]byte) (int, error) { return 0, errors.New("the read broke") }

func TestAComponentThatCannotBeReadIsSaidSo(t *testing.T) {
	r, err := New(broken{})
	require.NoError(t, err)
	t.Cleanup(func() { r.Close() })
	_, err = r.Start(t.Context(), sharedSpec)
	require.ErrorContains(t, err, "reading bafycomponent")
}

func TestClosingTheTierClosesWhatItCompiled(t *testing.T) {
	r, _ := sharedTier(t, 10*time.Millisecond)
	instance, err := r.Start(t.Context(), sharedSpec)
	require.NoError(t, err)
	require.NoError(t, instance.Close())
	require.NoError(t, r.Close())
	require.Zero(t, compiledFor(r))
	time.Sleep(20 * time.Millisecond)
	require.Zero(t, compiledFor(r), "and a runtime let go before is not retired twice")
}
