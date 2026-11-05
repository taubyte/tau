package hosted

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"

	plmv2 "github.com/taubyte/tau/pkg/proto/gen/tau/plm/v2"
)

// answering is a job API that answers every call with err, having emitted what it
// says, and counts what it was asked.
type answering struct {
	err   error
	asked int
	at    int64
}

func (a *answering) Enqueue(context.Context, string, string, string) (string, error) {
	a.asked++
	return "job", a.err
}

func (a *answering) Get(context.Context, string) (*plmv2.Job, error) {
	a.asked++
	return &plmv2.Job{Id: "job"}, a.err
}

func (a *answering) List(_ context.Context, _ string, emit func(string) error) error {
	a.asked++
	if a.err != nil {
		return a.err
	}
	return emit("job")
}

func (a *answering) Cancel(context.Context, string) error { a.asked++; return a.err }
func (a *answering) Retry(context.Context, string) error  { a.asked++; return a.err }

func (a *answering) Follow(_ context.Context, _ string, _ int32, from int64, _ func(*plmv2.Job_Event) error) (int64, error) {
	a.asked++
	a.at = from
	return from + 10, a.err
}

func (a *answering) Snapshot(ctx context.Context, job string, attempt int32, from int64, emit func(*plmv2.Job_Event) error) (int64, error) {
	return a.Follow(ctx, job, attempt, from, emit)
}

func TestACallThisNodeCannotAnswerGoesToTheOthers(t *testing.T) {
	ctx := t.Context()
	notLeading := connect.NewError(connect.CodeUnavailable, errors.New("not the leader"))
	here, there := &answering{err: notLeading}, &answering{}
	r := routed{here: here, elsewhere: there}

	_, err := r.Enqueue(ctx, "repo", "ref", "commit")
	require.NoError(t, err)
	_, err = r.Get(ctx, "job")
	require.NoError(t, err)
	require.NoError(t, r.List(ctx, "project", func(string) error { return nil }))
	require.NoError(t, r.Cancel(ctx, "job"))
	require.NoError(t, r.Retry(ctx, "job"))
	at, err := r.Follow(ctx, "job", 1, 5, nil)
	require.NoError(t, err)
	require.Equal(t, int64(25), at)
	require.Equal(t, int64(15), there.at, "the others are asked from where this node left off")
	_, err = r.Snapshot(ctx, "job", 1, 0, nil)
	require.NoError(t, err)
	require.Equal(t, 7, here.asked)
	require.Equal(t, 7, there.asked, "every call this node could not answer went on")
}

func TestACallThisNodeAnswersStaysHere(t *testing.T) {
	ctx := t.Context()
	refused := connect.NewError(connect.CodeNotFound, errors.New("no such job"))
	for name, here := range map[string]*answering{"answered": {}, "refused": {err: refused}} {
		t.Run(name, func(t *testing.T) {
			there := &answering{}
			r := routed{here: here, elsewhere: there}
			_, err := r.Enqueue(ctx, "repo", "ref", "commit")
			require.ErrorIs(t, err, here.err)
			_, err = r.Get(ctx, "job")
			require.ErrorIs(t, err, here.err)
			require.ErrorIs(t, r.List(ctx, "project", func(string) error { return nil }), here.err)
			require.ErrorIs(t, r.Cancel(ctx, "job"), here.err)
			require.ErrorIs(t, r.Retry(ctx, "job"), here.err)
			_, err = r.Follow(ctx, "job", 1, 0, nil)
			require.ErrorIs(t, err, here.err)
			_, err = r.Snapshot(ctx, "job", 1, 0, nil)
			require.ErrorIs(t, err, here.err)
			require.Zero(t, there.asked, "an answer, a refusal included, is the leader's")
		})
	}
}
