package jobs

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sync/atomic"
	"testing"
	"time"

	hoarderIface "github.com/taubyte/tau/core/services/hoarder"
	monkeyIface "github.com/taubyte/tau/core/services/monkey"
	"github.com/taubyte/tau/p2p/peer"
)

type flakyHoarder struct {
	hoarderIface.Client
	failures int32
	calls    atomic.Int32
	stored   atomic.Int32
}

func (h *flakyHoarder) Stash(_ string, r io.Reader, _ ...hoarderIface.StashOption) error {
	if h.calls.Add(1) <= h.failures {
		return errors.New("no hoarder reachable")
	}
	if _, err := io.Copy(io.Discard, r); err != nil {
		return err
	}
	h.stored.Add(1)
	return nil
}

type stashMonkey struct {
	monkeyIface.Service
	hoarder hoarderIface.Client
}

func (m stashMonkey) Hoarder() hoarderIface.Client { return m.hoarder }

type nopCloser struct{ *bytes.Reader }

func (nopCloser) Close() error { return nil }

// A build whose first pushes to Hoarder fail must still reach Hoarder after
// its job ends: the retry used to run on the job's context and died with it.
func TestStashRetryOutlivesTheJob(t *testing.T) {
	defer func(q int, qi, ri time.Duration) {
		StashQuickRetries, StashQuickInterval, StashRetryInterval = q, qi, ri
	}(StashQuickRetries, StashQuickInterval, StashRetryInterval)
	StashQuickRetries, StashQuickInterval, StashRetryInterval = 2, 10*time.Millisecond, 20*time.Millisecond

	node := peer.Mock(t.Context())
	defer node.Close()

	h := &flakyHoarder{failures: 4}
	jobCtx, endJob := context.WithCancel(t.Context())
	c := Context{Node: node, Monkey: stashMonkey{hoarder: h}, ctx: jobCtx}

	if _, err := c.StashBuildFile(nopCloser{bytes.NewReader(bytes.Repeat([]byte("module"), 1000))}); err != nil {
		t.Fatal(err)
	}
	endJob()

	deadline := time.Now().Add(3 * time.Second)
	for h.stored.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatalf("build never reached a hoarder after the job ended (%d attempts)", h.calls.Load())
		}
		time.Sleep(10 * time.Millisecond)
	}
}
