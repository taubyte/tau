package clients

import (
	"errors"
	"testing"

	"connectrpc.com/connect"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/p2p/discovery"
	buildv1 "github.com/taubyte/tau/pkg/proto/gen/tau/build/v1"
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
	require.ErrorContains(t, err, "/tau/build/v1")
	require.Nil(t, c)
}

func TestEnqueueCarriesThreeFieldsAndReturnsTheJobId(t *testing.T) {
	var got *buildv1.PushEvent
	c := newBuilds(t, &fakeBackend{enqueue: func(p *buildv1.PushEvent) (string, error) {
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
	c := newBuilds(t, &fakeBackend{enqueue: func(*buildv1.PushEvent) (string, error) { return "", nil }})

	_, err := c.Enqueue(t.Context(), "github.com/acme/pay", "refs/heads/main", "abc")
	require.Equal(t, connect.CodeInternal, connect.CodeOf(err))
}

// The backend answers Unavailable on a follower, for reads and writes alike, so
// a cloud whose leader is the second peer costs one extra round trip and no
// special handling anywhere.
func TestAFollowerIsWalkedPastForEveryRPC(t *testing.T) {
	notLeader := func() error {
		return connect.NewError(connect.CodeUnavailable, errors.New("not the leader of build/queue"))
	}
	follower := &fakeBackend{
		enqueue: func(*buildv1.PushEvent) (string, error) { return "", notLeader() },
		get:     func(string) (*buildv1.Job, error) { return nil, notLeader() },
		list:    func(string, func(*buildv1.JobRef) error) error { return notLeader() },
		cancel:  func(string) error { return notLeader() },
		retry:   func(string) error { return notLeader() },
	}
	leader := &fakeBackend{
		enqueue: func(*buildv1.PushEvent) (string, error) { return "job-1", nil },
		get:     func(id string) (*buildv1.Job, error) { return &buildv1.Job{Id: id}, nil },
		list:    func(_ string, send func(*buildv1.JobRef) error) error { return send(&buildv1.JobRef{Id: "job-1"}) },
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

// Every other code is the ANSWER, and retrying it against the next peer would
// turn one considered refusal into N.
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

// A CONSUMED STREAM CANNOT BE RETRIED. Pool.Do's rule is right for a unary
// call and wrong the moment an id has been handed onwards: the next peer would
// start its listing from the beginning and the caller would see the first ids
// twice.
func TestAListThatBrokeAfterItStartedIsNotRetriedAgainstAnotherPeer(t *testing.T) {
	breaks := &fakeBackend{list: func(_ string, send func(*buildv1.JobRef) error) error {
		if err := send(&buildv1.JobRef{Id: "a"}); err != nil {
			return err
		}
		return connect.NewError(connect.CodeUnavailable, errors.New("leadership lost mid-stream"))
	}}
	whole := &fakeBackend{list: func(_ string, send func(*buildv1.JobRef) error) error {
		for _, id := range []string{"a", "b", "c"} {
			if err := send(&buildv1.JobRef{Id: id}); err != nil {
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

// A stream that broke BEFORE its first id is still safe to retry, and is.
func TestAListThatBrokeBeforeItStartedIsRetried(t *testing.T) {
	breaks := &fakeBackend{list: func(string, func(*buildv1.JobRef) error) error {
		return connect.NewError(connect.CodeUnavailable, errors.New("not the leader"))
	}}
	whole := &fakeBackend{list: func(_ string, send func(*buildv1.JobRef) error) error {
		return send(&buildv1.JobRef{Id: "a"})
	}}

	c := newBuilds(t, breaks, whole)

	var got []string
	require.NoError(t, c.List(t.Context(), "proj-1", func(id string) error {
		got = append(got, id)
		return nil
	}))
	require.Equal(t, []string{"a"}, got)
}

// The caller's own failure — a broken HTTP connection, most likely — is never a
// routing problem, so it must not be retried either.
func TestTheCallersOwnFailureStopsTheWalk(t *testing.T) {
	emit := func(_ string, send func(*buildv1.JobRef) error) error {
		for _, id := range []string{"a", "b"} {
			if err := send(&buildv1.JobRef{Id: id}); err != nil {
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
			get:    func(string) (*buildv1.Job, error) { return nil, notLeader() },
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

// ── following a running build ────────────────────────────────────────────

// chunks is a Follow the fake backend serves from a fixed string.
func chunks(text string, size int) func(string, int64, func(*buildv1.LogChunk) error) error {
	return func(_ string, offset int64, send func(*buildv1.LogChunk) error) error {
		if offset > int64(len(text)) {
			return connect.NewError(connect.CodeInvalidArgument, errors.New("offset past the end"))
		}
		for at := offset; at < int64(len(text)); {
			end := min(at+int64(size), int64(len(text)))
			if err := send(&buildv1.LogChunk{Data: []byte(text[at:end]), NextOffset: end}); err != nil {
				return err
			}
			at = end
		}
		return nil
	}
}

func TestFollow(t *testing.T) {
	c := newBuilds(t, &fakeBackend{follow: chunks("cloning\ncompiling\ndone\n", 5)})

	var got []byte
	end, err := c.Follow(t.Context(), "job", 0, func(data []byte, _ int64) error {
		got = append(got, data...)
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, "cloning\ncompiling\ndone\n", string(got))
	require.Equal(t, int64(len("cloning\ncompiling\ndone\n")), end)
}

// A CALLER RESUMES WHERE IT WAS and sees neither a gap nor a repeat.
func TestFollowResumes(t *testing.T) {
	c := newBuilds(t, &fakeBackend{follow: chunks("first\nsecond\n", 64)})

	var got []byte
	_, err := c.Follow(t.Context(), "job", int64(len("first\n")), func(data []byte, _ int64) error {
		got = append(got, data...)
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, "second\n", string(got))
}

// THE OFFSET IS WHAT MAKES THE WALK SAFE, and it is the whole difference from
// List: a peer that cannot serve says Unavailable, the pool moves on, and the
// request carries where the caller got to — so the next peer RESUMES rather
// than starting the log again.
func TestFollowWalksToTheWriterAndResumes(t *testing.T) {
	mn := newMocknet(t)
	caller := newHost(t, mn)

	// The first peer serves a little and then goes Unavailable — the shape of a
	// backend whose claim lapsed mid-build.
	partial := serveBuild(t, mn, &fakeBackend{follow: func(_ string, offset int64, send func(*buildv1.LogChunk) error) error {
		if offset == 0 {
			if err := send(&buildv1.LogChunk{Data: []byte("cloning\n"), NextOffset: 8}); err != nil {
				return err
			}
		}
		return connect.NewError(connect.CodeUnavailable, errors.New("not writing this log"))
	}})
	// The second has the whole thing and is asked for the rest of it.
	whole := serveBuild(t, mn, &fakeBackend{follow: chunks("cloning\ncompiling\n", 64)})

	p, err := NewPool(caller, discovery.Static(partial.ID(), whole.ID()), build.Protocol, 4)
	require.NoError(t, err)
	t.Cleanup(p.Close)

	c, err := Builds(p)
	require.NoError(t, err)

	var got []byte
	end, err := c.Follow(t.Context(), "job", 0, func(data []byte, _ int64) error {
		got = append(got, data...)
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, "cloning\ncompiling\n", string(got), "the second peer resumed at 8 rather than repeating")
	require.Equal(t, int64(len("cloning\ncompiling\n")), end)
}

// An emit that fails is the CALLER's failure — a hung-up HTTP client — and is
// never a routing problem, so it stops rather than being retried elsewhere.
func TestFollowStopsOnTheCallersFailure(t *testing.T) {
	c := newBuilds(t, &fakeBackend{follow: chunks("output\n", 2)})

	_, err := c.Follow(t.Context(), "job", 0, func([]byte, int64) error {
		return errors.New("client went away")
	})
	require.ErrorContains(t, err, "client went away")
	require.NotEqual(t, connect.CodeUnavailable, connect.CodeOf(err))
}

func TestFollowNeedsSomewhereToPutIt(t *testing.T) {
	c := newBuilds(t, &fakeBackend{})
	_, err := c.Follow(t.Context(), "job", 0, nil)
	require.Equal(t, connect.CodeInternal, connect.CodeOf(err))
}
