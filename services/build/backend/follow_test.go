package backend

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"

	buildv2 "github.com/taubyte/tau/pkg/proto/gen/tau/build/v2"
	"github.com/taubyte/tau/services/build/livelog"
)

// follow_test is the wire in front of Service.Follow: the seam existed and
// nothing could reach it, so a build could be watched only by whoever was
// logged into the machine running it.

// collector is a ServerStream's Send, which is all the handler uses.
type collector struct {
	mu     sync.Mutex
	chunks []*buildv2.LogChunk
	fail   error
}

func (c *collector) send(chunk *buildv2.LogChunk) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.fail != nil {
		return c.fail
	}
	c.chunks = append(c.chunks, &buildv2.LogChunk{
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
		done <- h.follow(t.Context(), &buildv2.FollowRequest{JobId: id}, out.send)
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
	require.NoError(t, h.follow(t.Context(), &buildv2.FollowRequest{JobId: id, Offset: int64(len("first\n"))}, out.send))
	require.Equal(t, "second\n", out.text())
}

// A NODE THAT IS NOT WRITING THIS LOG SAYS Unavailable, NOT NotFound — which is
// what makes a pooled caller walk to the next backend instead of stopping at
// whichever one its resolver named first.
func TestFollowOnANodeThatIsNotTheWriter(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)

	err := h.follow(t.Context(), &buildv2.FollowRequest{JobId: id}, (&collector{}).send)
	require.Equal(t, connect.CodeUnavailable, connect.CodeOf(err))

	// The narrowing is real: the other refusals keep their own codes, so a
	// caller does not walk the whole fleet over a bad request.
	err = h.follow(t.Context(), &buildv2.FollowRequest{}, (&collector{}).send)
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))

	err = h.follow(t.Context(), &buildv2.FollowRequest{JobId: id, Offset: -1}, (&collector{}).send)
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))

	// An offset past the end of a log that IS here is the caller's arithmetic,
	// not a routing problem.
	running(t, h.Service, id)
	err = h.follow(t.Context(), &buildv2.FollowRequest{JobId: id, Offset: 4096}, (&collector{}).send)
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
	go func() { done <- h.follow(ctx, &buildv2.FollowRequest{JobId: id}, out.send) }()

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
	require.ErrorContains(t, h.follow(t.Context(), &buildv2.FollowRequest{JobId: id}, broken.send), "client went away")

}

// heldBuild is a build in the middle of its work: it has printed its first
// line and prints each line it is handed until it is finished.
type heldBuild struct {
	lines chan string
	once  sync.Once
	ended chan error
}

func (b *heldBuild) say(line string) { b.lines <- line }

func (b *heldBuild) finish() { b.once.Do(func() { close(b.lines) }) }

// hold starts the pushed job's build and returns once it has printed first.
func hold(t *testing.T, h *harness, first string) *heldBuild {
	t.Helper()
	b := &heldBuild{lines: make(chan string), ended: make(chan error, 1)}
	started := make(chan struct{})
	h.runner.run = func(_ context.Context, _ Work, out Output) error {
		fmt.Fprint(out, first)
		close(started)
		for line := range b.lines {
			fmt.Fprint(out, line)
		}
		return nil
	}
	go func() {
		_, err := h.step(context.WithoutCancel(t.Context()))
		b.ended <- err
	}()
	t.Cleanup(b.finish)
	<-started
	return b
}

// snapshotOf is what id's log holds from `from`. It must come back while the
// build runs: a read that waited for the build would not.
func snapshotOf(t *testing.T, h *harness, id string, from int64) *collector {
	t.Helper()
	var out collector
	done := make(chan error, 1)
	go func() {
		done <- h.follow(t.Context(), &buildv2.FollowRequest{JobId: id, Offset: from, Snapshot: true}, out.send)
	}()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("a snapshot of a running build waited for the build to end")
	}
	return &out
}

// A SNAPSHOT IS WHAT A RUNNING BUILD HAS WRITTEN SO FAR, and it ends while the
// build goes on: `tau logs` without --follow. A follow of the same build gets
// each line as it is written and ends with the build.
func TestASnapshotEndsAtWhatTheBuildHasWrittenAndAFollowEndsWithTheBuild(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)
	build := hold(t, h, "cloning\n")

	soFar := snapshotOf(t, h, id, 0)
	require.Equal(t, "cloning\n", soFar.text())
	require.Equal(t, int64(len("cloning\n")), soFar.last())

	var followed collector
	following := make(chan error, 1)
	go func() {
		following <- h.follow(t.Context(), &buildv2.FollowRequest{JobId: id}, followed.send)
	}()
	require.Eventually(t, func() bool { return followed.text() == "cloning\n" }, 2*time.Second, 5*time.Millisecond)

	build.say("compiling\n")
	require.Eventually(t, func() bool { return followed.text() == "cloning\ncompiling\n" }, 2*time.Second, 5*time.Millisecond)
	select {
	case err := <-following:
		t.Fatalf("the follow ended (%v) while the build was still running", err)
	default:
	}

	require.Equal(t, "cloning\ncompiling\n", snapshotOf(t, h, id, 0).text())
	require.Equal(t, "compiling\n", snapshotOf(t, h, id, int64(len("cloning\n"))).text())
	require.Empty(t, snapshotOf(t, h, id, int64(len("cloning\ncompiling\n"))).text(),
		"nothing has been written past that offset yet")

	build.finish()
	require.NoError(t, <-build.ended)
	select {
	case err := <-following:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("the follow outlived the build")
	}

	// ENDED, THE LOG IS THE STORED ONE for either read: it is not on this node
	// any more, and the record names the whole of it.
	stored := h.assets.content(t, h.mustRecord(t, id).Logs[stamp(1)])
	require.Equal(t, "cloning\ncompiling\n", stored)
	require.Equal(t, stored, followed.text(), "the follower saw the whole log")
	for _, snapshot := range []bool{false, true} {
		err := h.follow(t.Context(), &buildv2.FollowRequest{JobId: id, Snapshot: snapshot}, (&collector{}).send)
		require.Equal(t, connect.CodeUnavailable, connect.CodeOf(err), "snapshot=%v", snapshot)
	}
}

// A LOG WHOSE WRITER HAS CLOSED IS WHOLE TO BOTH READS: the moment between a
// build ending and its log leaving this node.
func TestAnEndedLogIsWholeToAFollowAndASnapshot(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)
	log := running(t, h.Service, id)

	_, err := log.Write([]byte("cloning\ncompiling\n"))
	require.NoError(t, err)
	require.NoError(t, log.Close())

	for _, snapshot := range []bool{false, true} {
		var out collector
		require.NoError(t, h.follow(t.Context(), &buildv2.FollowRequest{JobId: id, Snapshot: snapshot}, out.send))
		require.Equal(t, "cloning\ncompiling\n", out.text(), "snapshot=%v", snapshot)
		require.Equal(t, int64(len("cloning\ncompiling\n")), out.last(), "snapshot=%v", snapshot)
	}
}

// A snapshot is refused exactly where a follow is, with the same codes.
func TestASnapshotIsRefusedWhereAFollowIs(t *testing.T) {
	h := newHarness(t)
	id := h.push(t, testRef, testCommit)
	ask := func(jobID string, offset int64) error {
		return h.follow(t.Context(), &buildv2.FollowRequest{JobId: jobID, Offset: offset, Snapshot: true}, (&collector{}).send)
	}

	require.Equal(t, connect.CodeUnavailable, connect.CodeOf(ask(id, 0)))
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(ask("", 0)))
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(ask(id, -1)))

	running(t, h.Service, id)
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(ask(id, 4096)))
}
