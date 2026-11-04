package clients

import (
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"connectrpc.com/connect"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/p2p/discovery"
	buildv2 "github.com/taubyte/tau/pkg/proto/gen/tau/build/v2"
	"github.com/taubyte/tau/services/build"
	"github.com/taubyte/tau/services/build/frontend"
)

var _ frontend.Backend = (*BuildClient)(nil)

func newBuilds(t *testing.T, backends ...*fakeBackend) *BuildClient {
	t.Helper()
	mn := newMocknet(t)
	caller := newHost(t, mn)

	peers := make([]peer.ID, 0, len(backends))
	for _, b := range backends {
		peers = append(peers, serveBuild(t, mn, b).ID())
	}

	p, err := NewPool(caller, discovery.Static(peers...), build.Protocol, 8)
	require.NoError(t, err)
	t.Cleanup(p.Close)

	c, err := Builds(p)
	require.NoError(t, err)
	return c
}

func TestBuildsRefusesAPoolForTheWrongProtocol(t *testing.T) {
	mn := newMocknet(t)

	c, err := Builds(nil)
	require.Error(t, err)
	require.Nil(t, c)

	p, err := NewPool(newHost(t, mn), discovery.Static(), AuthProtocol, 4)
	require.NoError(t, err)
	t.Cleanup(p.Close)

	c, err = Builds(p)
	require.ErrorContains(t, err, "/tau/build/v2")
	require.Nil(t, c)
}

func TestEnqueueCarriesThreeFieldsAndReturnsTheJobId(t *testing.T) {
	var got *buildv2.PushEvent
	c := newBuilds(t, &fakeBackend{enqueue: func(p *buildv2.PushEvent) (string, error) {
		got = p
		return "job-1", nil
	}})

	id, err := c.Enqueue(t.Context(), "github.com/acme/pay", "refs/heads/main", "abc")

	require.NoError(t, err)
	require.Equal(t, "job-1", id)
	require.Equal(t, "github.com/acme/pay", got.GetRepositoryKey())
	require.Equal(t, "refs/heads/main", got.GetRef())
	require.Equal(t, "abc", got.GetCommitSha())
}

func TestAnEnqueueWithNoIdIsAFault(t *testing.T) {
	c := newBuilds(t, &fakeBackend{enqueue: func(*buildv2.PushEvent) (string, error) { return "", nil }})

	_, err := c.Enqueue(t.Context(), "github.com/acme/pay", "refs/heads/main", "abc")
	require.Equal(t, connect.CodeInternal, connect.CodeOf(err))
}

func TestAFollowerIsWalkedPastForEveryRPC(t *testing.T) {
	notLeader := func() error {
		return connect.NewError(connect.CodeUnavailable, errors.New("not the leader of build/queue"))
	}
	follower := &fakeBackend{
		enqueue: func(*buildv2.PushEvent) (string, error) { return "", notLeader() },
		get:     func(string) (*buildv2.Job, error) { return nil, notLeader() },
		list:    func(string, func(*buildv2.JobRef) error) error { return notLeader() },
		cancel:  func(string) error { return notLeader() },
		retry:   func(string) error { return notLeader() },
	}
	leader := &fakeBackend{
		enqueue: func(*buildv2.PushEvent) (string, error) { return "job-1", nil },
		get:     func(id string) (*buildv2.Job, error) { return &buildv2.Job{Id: id}, nil },
		list:    func(_ string, send func(*buildv2.JobRef) error) error { return send(&buildv2.JobRef{Id: "job-1"}) },
		cancel:  func(string) error { return nil },
		retry:   func(string) error { return nil },
	}

	c := newBuilds(t, follower, leader)

	id, err := c.Enqueue(t.Context(), "github.com/acme/pay", "refs/heads/main", "abc")
	require.NoError(t, err)
	require.Equal(t, "job-1", id)

	job, err := c.Get(t.Context(), "job-1")
	require.NoError(t, err)
	require.Equal(t, "job-1", job.GetId())

	var listed []string
	require.NoError(t, c.List(t.Context(), "proj-1", func(id string) error {
		listed = append(listed, id)
		return nil
	}))
	require.Equal(t, []string{"job-1"}, listed)

	require.NoError(t, c.Cancel(t.Context(), "job-1"))
	require.NoError(t, c.Retry(t.Context(), "job-1"))
}

func TestAConsideredRefusalIsNotRetried(t *testing.T) {
	asked := 0
	refuse := &fakeBackend{retry: func(string) error {
		asked++
		return connect.NewError(connect.CodeFailedPrecondition, errors.New("job is Claimed, not finished"))
	}}
	other := &fakeBackend{retry: func(string) error { asked++; return nil }}

	c := newBuilds(t, refuse, other)

	err := c.Retry(t.Context(), "job-1")

	require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))
	require.Equal(t, 1, asked)
}

func TestAListThatBrokeAfterItStartedIsNotRetriedAgainstAnotherPeer(t *testing.T) {
	breaks := &fakeBackend{list: func(_ string, send func(*buildv2.JobRef) error) error {
		if err := send(&buildv2.JobRef{Id: "a"}); err != nil {
			return err
		}
		return connect.NewError(connect.CodeUnavailable, errors.New("leadership lost mid-stream"))
	}}
	whole := &fakeBackend{list: func(_ string, send func(*buildv2.JobRef) error) error {
		for _, id := range []string{"a", "b", "c"} {
			if err := send(&buildv2.JobRef{Id: id}); err != nil {
				return err
			}
		}
		return nil
	}}

	c := newBuilds(t, breaks, whole)

	var got []string
	err := c.List(t.Context(), "proj-1", func(id string) error {
		got = append(got, id)
		return nil
	})

	require.Error(t, err)
	require.ErrorContains(t, err, "broke off after 1 ids")
	require.Equal(t, []string{"a"}, got, "no id was delivered twice")
}

func TestAListThatBrokeBeforeItStartedIsRetried(t *testing.T) {
	breaks := &fakeBackend{list: func(string, func(*buildv2.JobRef) error) error {
		return connect.NewError(connect.CodeUnavailable, errors.New("not the leader"))
	}}
	whole := &fakeBackend{list: func(_ string, send func(*buildv2.JobRef) error) error {
		return send(&buildv2.JobRef{Id: "a"})
	}}

	c := newBuilds(t, breaks, whole)

	var got []string
	require.NoError(t, c.List(t.Context(), "proj-1", func(id string) error {
		got = append(got, id)
		return nil
	}))
	require.Equal(t, []string{"a"}, got)
}

func TestTheCallersOwnFailureStopsTheWalk(t *testing.T) {
	emit := func(_ string, send func(*buildv2.JobRef) error) error {
		for _, id := range []string{"a", "b"} {
			if err := send(&buildv2.JobRef{Id: id}); err != nil {
				return err
			}
		}
		return nil
	}
	first := &fakeBackend{list: emit}
	second := &fakeBackend{list: emit}

	c := newBuilds(t, first, second)

	sentinel := errors.New("the caller hung up")
	var got []string
	err := c.List(t.Context(), "proj-1", func(id string) error {
		got = append(got, id)
		return sentinel
	})

	require.ErrorIs(t, err, sentinel)
	require.Equal(t, []string{"a"}, got)
}

func TestListNeedsSomewhereToPutTheIds(t *testing.T) {
	c := newBuilds(t, &fakeBackend{})
	err := c.List(t.Context(), "proj-1", nil)
	require.Equal(t, connect.CodeInternal, connect.CodeOf(err))
}

func TestEveryPeerUnavailableIsErrNoBackend(t *testing.T) {
	notLeader := func() error {
		return connect.NewError(connect.CodeUnavailable, errors.New("not the leader"))
	}
	follower := func() *fakeBackend {
		return &fakeBackend{
			get:    func(string) (*buildv2.Job, error) { return nil, notLeader() },
			cancel: func(string) error { return notLeader() },
		}
	}

	c := newBuilds(t, follower(), follower())

	_, err := c.Get(t.Context(), "job-1")
	require.ErrorIs(t, err, ErrNoBackend)
	// And the code underneath survives the wrapping, so a caller mapping it to
	// a status still gets 503 rather than 502.
	require.Equal(t, connect.CodeUnavailable, connect.CodeOf(err))

	require.ErrorIs(t, c.Cancel(t.Context(), "job-1"), ErrNoBackend)
}

// lines is a Follow the fake backend serves from a fixed string, a message event
// per line, each carrying the offset after it.
func lines(text string) func(string, int64, func(*buildv2.Job_Event) error) error {
	return func(_ string, offset int64, send func(*buildv2.Job_Event) error) error {
		if offset > int64(len(text)) {
			return connect.NewError(connect.CodeInvalidArgument, errors.New("offset past the end"))
		}
		for at := offset; at < int64(len(text)); {
			end := at + int64(strings.IndexByte(text[at:], '\n')) + 1
			if err := send(message(text[at:end-1], end)); err != nil {
				return err
			}
			at = end
		}
		return nil
	}
}

func message(text string, offset int64) *buildv2.Job_Event {
	return &buildv2.Job_Event{Kind: &buildv2.Job_Event_Message{Message: text}, Offset: offset}
}

// said is an emit that keeps what it was told, a line per message.
func said(got *string) func(*buildv2.Job_Event) error {
	return func(ev *buildv2.Job_Event) error {
		*got += ev.GetMessage() + "\n"
		return nil
	}
}

func TestFollow(t *testing.T) {
	c := newBuilds(t, &fakeBackend{follow: lines("cloning\ncompiling\ndone\n")})

	var got string
	end, err := c.Follow(t.Context(), "job", 1, 0, said(&got))
	require.NoError(t, err)
	require.Equal(t, "cloning\ncompiling\ndone\n", got)
	require.Equal(t, int64(len("cloning\ncompiling\ndone\n")), end)
}

func TestFollowResumes(t *testing.T) {
	c := newBuilds(t, &fakeBackend{follow: lines("first\nsecond\n")})

	var got string
	_, err := c.Follow(t.Context(), "job", 1, int64(len("first\n")), said(&got))
	require.NoError(t, err)
	require.Equal(t, "second\n", got)
}

func TestFollowWalksToTheWriterAndResumes(t *testing.T) {
	mn := newMocknet(t)
	caller := newHost(t, mn)

	// The first peer serves a little and then goes Unavailable — the shape of a
	// backend whose claim lapsed mid-build.
	broke := &fakeBackend{follow: func(_ string, offset int64, send func(*buildv2.Job_Event) error) error {
		if offset == 0 {
			if err := send(message("cloning", 8)); err != nil {
				return err
			}
		}
		return connect.NewError(connect.CodeUnavailable, errors.New("not writing this log"))
	}}
	partial := serveBuild(t, mn, broke)
	// The second has the whole thing and is asked for the rest of it.
	writer := &fakeBackend{follow: lines("cloning\ncompiling\n")}
	whole := serveBuild(t, mn, writer)

	p, err := NewPool(caller, discovery.Static(partial.ID(), whole.ID()), build.Protocol, 4)
	require.NoError(t, err)
	t.Cleanup(p.Close)

	c, err := Builds(p)
	require.NoError(t, err)

	var got string
	end, err := c.Follow(t.Context(), "job", 3, 0, said(&got))
	require.NoError(t, err)
	require.Equal(t, "cloning\ncompiling\n", got, "the second peer resumed at 8 rather than repeating")
	require.Equal(t, int64(len("cloning\ncompiling\n")), end)
	// AND IT RESUMED THE SAME RUN: an offset carried to the next peer without
	// its attempt could be read from another run's log.
	require.Equal(t, []int32{3}, broke.asked())
	require.Equal(t, []int32{3}, writer.asked())
}

func TestFollowOfAnEndedJobStopsAtTheLeader(t *testing.T) {
	var asked atomic.Int32
	after := &fakeBackend{follow: func(id string, offset int64, send func(*buildv2.Job_Event) error) error {
		asked.Add(1)
		return lines("output\n")(id, offset, send)
	}}
	ended := func(string, int64, func(*buildv2.Job_Event) error) error {
		return connect.NewError(connect.CodeFailedPrecondition, errors.New("job finished as JobStatusSuccess"))
	}
	c := newBuilds(t, &fakeBackend{follow: ended, snapshot: ended}, after)

	_, err := c.Follow(t.Context(), "job", 1, 0, func(*buildv2.Job_Event) error { return nil })
	require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))
	_, err = c.Snapshot(t.Context(), "job", 1, 0, func(*buildv2.Job_Event) error { return nil })
	require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))
	require.Zero(t, asked.Load(), "nothing past the leader was asked")

	notHere := func(string, int64, func(*buildv2.Job_Event) error) error {
		return connect.NewError(connect.CodeUnavailable, errors.New("not writing this log"))
	}
	c = newBuilds(t, &fakeBackend{follow: notHere}, &fakeBackend{follow: notHere})
	_, err = c.Follow(t.Context(), "job", 1, 0, func(*buildv2.Job_Event) error { return nil })
	require.Equal(t, connect.CodeUnavailable, connect.CodeOf(err))
	require.ErrorIs(t, err, ErrNoBackend)
}

func TestFollowStopsOnTheCallersFailure(t *testing.T) {
	c := newBuilds(t, &fakeBackend{follow: lines("output\n")})

	_, err := c.Follow(t.Context(), "job", 1, 0, func(*buildv2.Job_Event) error {
		return errors.New("client went away")
	})
	require.ErrorContains(t, err, "client went away")
	require.NotEqual(t, connect.CodeUnavailable, connect.CodeOf(err))
}

func TestFollowNeedsSomewhereToPutIt(t *testing.T) {
	c := newBuilds(t, &fakeBackend{})
	_, err := c.Follow(t.Context(), "job", 1, 0, nil)
	require.Equal(t, connect.CodeInternal, connect.CodeOf(err))
}

func TestArtifact(t *testing.T) {
	c := newBuilds(t, &fakeBackend{built: func(project, environment, resource string) (string, error) {
		require.Equal(t, "shop", project)
		require.Equal(t, "prod", environment)
		require.Equal(t, "QmXnnyufdzAWL5CqZ2RnSNgPbvCc1ALT73s6epPrRnZ1Zc", resource)
		return "bafybuilt", nil
	}})

	cid, err := c.Artifact(t.Context(), "shop", "prod", "QmXnnyufdzAWL5CqZ2RnSNgPbvCc1ALT73s6epPrRnZ1Zc")
	require.NoError(t, err)
	require.Equal(t, "bafybuilt", cid)
}

func TestArtifactThatIsNotThere(t *testing.T) {
	c := newBuilds(t, &fakeBackend{built: func(string, string, string) (string, error) {
		return "", connect.NewError(connect.CodeNotFound, errors.New("nothing has been built for that"))
	}})

	_, err := c.Artifact(t.Context(), "shop", "prod", "resource")
	require.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
}

func TestArtifactWalksPastAFollower(t *testing.T) {
	c := newBuilds(t,
		&fakeBackend{built: func(string, string, string) (string, error) {
			return "", connect.NewError(connect.CodeUnavailable, errors.New("not the leader"))
		}},
		&fakeBackend{built: func(string, string, string) (string, error) { return "bafybuilt", nil }},
	)

	cid, err := c.Artifact(t.Context(), "shop", "prod", "resource")
	require.NoError(t, err)
	require.Equal(t, "bafybuilt", cid)
}

func TestSnapshotAsksForWhatTheLogHoldsNow(t *testing.T) {
	c := newBuilds(t, &fakeBackend{
		follow: func(string, int64, func(*buildv2.Job_Event) error) error {
			return connect.NewError(connect.CodeFailedPrecondition, errors.New("asked to follow"))
		},
		snapshot: lines("cloning\n"),
	})

	var got string
	end, err := c.Snapshot(t.Context(), "job", 1, 0, said(&got))
	require.NoError(t, err)
	require.Equal(t, "cloning\n", got)
	require.Equal(t, int64(len("cloning\n")), end)
}

func TestSnapshotWalksToTheWriterAndResumes(t *testing.T) {
	mn := newMocknet(t)
	caller := newHost(t, mn)

	broke := &fakeBackend{snapshot: func(_ string, offset int64, send func(*buildv2.Job_Event) error) error {
		if offset == 0 {
			if err := send(message("cloning", 8)); err != nil {
				return err
			}
		}
		return connect.NewError(connect.CodeUnavailable, errors.New("not writing this log"))
	}}
	partial := serveBuild(t, mn, broke)
	writer := &fakeBackend{snapshot: lines("cloning\ncompiling\n")}
	whole := serveBuild(t, mn, writer)

	p, err := NewPool(caller, discovery.Static(partial.ID(), whole.ID()), build.Protocol, 4)
	require.NoError(t, err)
	t.Cleanup(p.Close)

	c, err := Builds(p)
	require.NoError(t, err)

	var got string
	end, err := c.Snapshot(t.Context(), "job", 2, 0, said(&got))
	require.NoError(t, err)
	require.Equal(t, "cloning\ncompiling\n", got)
	require.Equal(t, int64(len("cloning\ncompiling\n")), end)
	require.Equal(t, []int32{2}, broke.asked())
	require.Equal(t, []int32{2}, writer.asked())
}
