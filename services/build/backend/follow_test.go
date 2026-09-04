package backend

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"

	buildv1 "github.com/taubyte/tau/pkg/proto/gen/tau/build/v1"
	"github.com/taubyte/tau/services/build/livelog"
)

// follow_test is the wire in front of Service.Follow: the seam existed and
// nothing could reach it, so a build could be watched only by whoever was
// logged into the machine running it.

// collector is a ServerStream's Send, which is all the handler uses.
type collector struct {
	mu     sync.Mutex
	chunks []*buildv1.LogChunk
	fail   error
}

func (c *collector) send(chunk *buildv1.LogChunk) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.fail != nil {
		return c.fail
	}
	c.chunks = append(c.chunks, &buildv1.LogChunk{
		Data:       append([]byte(nil), chunk.GetData()...),
		NextOffset: chunk.GetNextOffset(),
	})
	return nil
}

func (c *collector) text() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []byte
	for _, chunk := range c.chunks {
		out = append(out, chunk.GetData()...)
	}
	return string(out)
}

func (c *collector) last() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.chunks) == 0 {
		return 0
	}
	return c.chunks[len(c.chunks)-1].GetNextOffset()
}

// running puts a log on this service under jobID, as a run would.
func running(t *testing.T, s *Service, jobID string) *livelog.Log {
	t.Helper()

	log, err := livelog.Create(t.TempDir() + "/log")
	require.NoError(t, err)

	s.logsMu.Lock()
	s.logs[jobID] = log
	s.logsMu.Unlock()

	t.Cleanup(func() {
		s.logsMu.Lock()
		delete(s.logs, jobID)
		s.logsMu.Unlock()
		log.Close()
	})
	return log
}

// A LOG IS FOLLOWED WHILE IT IS WRITTEN, which is the whole point: v1 can hand
// one over only once the build has ended and the whole of it has reached IPFS.
func TestFollowStreamsARunningBuild(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)
	log := running(t, h.Service, id)

	_, err := log.Write([]byte("cloning\n"))
	require.NoError(t, err)

	var out collector
	done := make(chan error, 1)
	go func() {
		done <- h.follow(t.Context(), id, 0, out.send)
	}()

	// What was already there arrives without waiting for more.
	require.Eventually(t, func() bool { return out.text() == "cloning\n" }, 2*time.Second, 5*time.Millisecond)

	// And so does what comes next, while the follower is still attached.
	_, err = log.Write([]byte("compiling\n"))
	require.NoError(t, err)
	require.Eventually(t, func() bool { return out.text() == "cloning\ncompiling\n" }, 2*time.Second, 5*time.Millisecond)

	// THE BUILD ENDING IS A COMPLETE ANSWER AND NOT AN ERROR. livelog does not
	// return EOF early — a reader that catches up blocks — so reaching it means
	// the writer closed and the follower has the whole log.
	require.NoError(t, log.Close())
	require.NoError(t, <-done)

	require.Equal(t, int64(len("cloning\ncompiling\n")), out.last(),
		"the offset a resuming client sends back is carried per chunk")
}

// THE OFFSET IS WHY A DROPPED CONNECTION COSTS NOTHING. A viewer resumes where
// it was and sees neither a gap nor a repeat.
func TestFollowResumesAtAnOffset(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)
	log := running(t, h.Service, id)

	_, err := log.Write([]byte("first\nsecond\n"))
	require.NoError(t, err)
	require.NoError(t, log.Close())

	var out collector
	require.NoError(t, h.follow(t.Context(), id, int64(len("first\n")), out.send))
	require.Equal(t, "second\n", out.text())
}

// A NODE THAT IS NOT WRITING THIS LOG SAYS Unavailable, NOT NotFound — which is
// what makes a pooled caller walk to the next backend instead of stopping at
// whichever one its resolver named first.
func TestFollowOnANodeThatIsNotTheWriter(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)

	err := h.follow(t.Context(), id, 0, (&collector{}).send)
	require.Equal(t, connect.CodeUnavailable, connect.CodeOf(err))

	// The narrowing is real: the other refusals keep their own codes, so a
	// caller does not walk the whole fleet over a bad request.
	err = h.follow(t.Context(), "", 0, (&collector{}).send)
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))

	err = h.follow(t.Context(), id, -1, (&collector{}).send)
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))

	// An offset past the end of a log that IS here is the caller's arithmetic,
	// not a routing problem.
	running(t, h.Service, id)
	err = h.follow(t.Context(), id, 4096, (&collector{}).send)
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}

// A VIEWER THAT HUNG UP IS NOTICED ON THE NEXT CHUNK. livelog's Reader blocks
// until the writer appends, so a follow of a quiet build must check the context
// before it reads or it would hold the goroutine until the build ended.
func TestFollowEndsWhenTheViewerGoesAway(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)
	log := running(t, h.Service, id)

	_, err := log.Write([]byte("started\n"))
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())
	var out collector
	done := make(chan error, 1)
	go func() { done <- h.follow(ctx, id, 0, out.send) }()

	require.Eventually(t, func() bool { return out.text() == "started\n" }, 2*time.Second, 5*time.Millisecond)

	cancel()
	// The build never ends; the follow does.
	_, err = log.Write([]byte("still going\n"))
	require.NoError(t, err)

	select {
	case err := <-done:
		require.NoError(t, err, "a viewer hanging up is not a failure of the build")
	case <-time.After(5 * time.Second):
		t.Fatal("the follow outlived the viewer")
	}
}

// A send that fails is the caller's connection, and stops the follow with its
// own error rather than being retried anywhere.
func TestFollowStopsWhenItCannotSend(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)
	log := running(t, h.Service, id)

	_, err := log.Write([]byte("output\n"))
	require.NoError(t, err)

	broken := &collector{fail: errors.New("client went away")}
	require.ErrorContains(t, h.follow(t.Context(), id, 0, broken.send), "client went away")

}
