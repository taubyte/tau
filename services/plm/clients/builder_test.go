package clients

import (
	"context"
	"errors"
	"io"
	"net/http"
	"testing"

	"connectrpc.com/connect"
	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/core/service"
	"github.com/taubyte/tau/pkg/proto/codec"
	builderv2 "github.com/taubyte/tau/pkg/proto/gen/tau/builder/v2"
	"github.com/taubyte/tau/pkg/proto/gen/tau/builder/v2/builderv2connect"
	plmv2 "github.com/taubyte/tau/pkg/proto/gen/tau/plm/v2"
	"github.com/taubyte/tau/services/plm"
	"github.com/taubyte/tau/services/plm/run"
)

// aBuilder answers the builder's protocol with what it was told: events for a
// follow, then the end it was given.
type aBuilder struct {
	events []*plmv2.Job_Event
	end    error
	asked  []string
}

func (b *aBuilder) Plan(_ context.Context, req *connect.Request[builderv2.Work]) (*connect.Response[builderv2.Planned], error) {
	b.asked = append(b.asked, "plan "+req.Msg.GetJobId())
	return connect.NewResponse(&builderv2.Planned{Environments: []string{"prod"}, Said: req.Msg.GetPrelude()}), nil
}

func (b *aBuilder) Build(_ context.Context, req *connect.Request[builderv2.Work]) (*connect.Response[builderv2.Built], error) {
	b.asked = append(b.asked, "build "+req.Msg.GetEnvironment())
	return connect.NewResponse(&builderv2.Built{Stored: []*builderv2.Stored{{ResourceId: "QmR", Cid: "bafy"}}}), nil
}

func (b *aBuilder) Cancel(_ context.Context, req *connect.Request[builderv2.Run]) (*connect.Response[plmv2.Empty], error) {
	b.asked = append(b.asked, "cancel "+req.Msg.GetJobId())
	return connect.NewResponse(&plmv2.Empty{}), nil
}

func (b *aBuilder) Seal(_ context.Context, req *connect.Request[builderv2.Sealing]) (*connect.Response[builderv2.Sealed], error) {
	b.asked = append(b.asked, "seal "+req.Msg.GetStatus().String())
	return connect.NewResponse(&builderv2.Sealed{Log: "bafylog"}), nil
}

func (b *aBuilder) Follow(_ context.Context, _ *connect.Request[plmv2.FollowRequest], stream *connect.ServerStream[plmv2.Job_Event]) error {
	for _, ev := range b.events {
		if err := stream.Send(ev); err != nil {
			return err
		}
	}
	return b.end
}

func servedBuilder(t *testing.T, b *aBuilder) (*Builder, libp2ppeer.ID) {
	t.Helper()
	mn := newMocknet(t)
	at := newHost(t, mn)
	mux := http.NewServeMux()
	mux.Handle(builderv2connect.NewBuilderServiceHandler(b, codec.Option()))
	serve(t, at, plm.BuilderProtocol, mux)
	c, err := BuilderAt(newHost(t, mn), at.ID())
	require.NoError(t, err)
	t.Cleanup(func() { c.Close() })
	return c, at.ID()
}

func TestABuilderIsReachedOverItsProtocol(t *testing.T) {
	b := &aBuilder{events: []*plmv2.Job_Event{{Kind: &plmv2.Job_Event_Message{Message: "cloning"}}}}
	c, _ := servedBuilder(t, b)
	ctx := t.Context()

	planned, err := c.Plan(ctx, run.Work{JobID: "j", Prelude: "said"})
	require.NoError(t, err)
	require.Equal(t, []string{"prod"}, planned.GetEnvironments())
	require.Equal(t, "said", planned.GetSaid(), "the work crosses whole")
	built, err := c.Build(ctx, run.Work{JobID: "j", Environment: "prod"})
	require.NoError(t, err)
	require.Equal(t, "bafy", built.GetStored()[0].GetCid())
	require.NoError(t, c.Cancel(ctx, &builderv2.Run{JobId: "j", Attempt: 1}))
	sealed, err := c.Seal(ctx, &builderv2.Sealing{Run: &builderv2.Run{JobId: "j", Attempt: 1}, Status: plmv2.JobStatus_JobStatusSuccess})
	require.NoError(t, err)
	require.Equal(t, "bafylog", sealed.GetLog())
	require.Equal(t, []string{"plan j", "build prod", "cancel j", "seal JobStatusSuccess"}, b.asked)

	events, err := c.Follow(ctx, &plmv2.FollowRequest{JobId: "j", Attempt: 1})
	require.NoError(t, err)
	ev, err := events.Receive()
	require.NoError(t, err)
	require.Equal(t, "cloning", ev.GetMessage(), "the first event read ahead is the first handed over")
	_, err = events.Receive()
	require.ErrorIs(t, err, io.EOF)
	_, err = events.Receive()
	require.ErrorIs(t, err, io.EOF, "an ended follow stays ended")
	require.NoError(t, events.Close())
}

func TestAFollowSaysHowItEnded(t *testing.T) {
	refused := connect.NewError(connect.CodeNotFound, errors.New("no such log"))
	c, _ := servedBuilder(t, &aBuilder{end: refused})
	_, err := c.Follow(t.Context(), &plmv2.FollowRequest{JobId: "j", Attempt: 1})
	require.Equal(t, connect.CodeNotFound, connect.CodeOf(err), "a refusal before any event is the follow's own error")

	c, _ = servedBuilder(t, &aBuilder{})
	empty, err := c.Follow(t.Context(), &plmv2.FollowRequest{JobId: "j", Attempt: 1})
	require.NoError(t, err)
	_, err = empty.Receive()
	require.ErrorIs(t, err, io.EOF, "a log with nothing past the offset ends at once")
	empty.Close()

	broke := connect.NewError(connect.CodeUnavailable, errors.New("the log went"))
	c, _ = servedBuilder(t, &aBuilder{events: []*plmv2.Job_Event{{}}, end: broke})
	cut, err := c.Follow(t.Context(), &plmv2.FollowRequest{JobId: "j", Attempt: 1})
	require.NoError(t, err)
	_, err = cut.Receive()
	require.NoError(t, err)
	_, err = cut.Receive()
	require.Equal(t, connect.CodeUnavailable, connect.CodeOf(err), "a follow cut short says so")
	cut.Close()
}

func TestABuilderThatDoesNotAnswerIsAnError(t *testing.T) {
	mn := newMocknet(t)
	c, err := BuilderAt(newHost(t, mn), newHost(t, mn).ID())
	require.NoError(t, err, "a client is made before anything is dialled")
	t.Cleanup(func() { c.Close() })
	ctx := t.Context()
	_, err = c.Plan(ctx, run.Work{})
	require.Error(t, err)
	_, err = c.Build(ctx, run.Work{})
	require.Error(t, err)
	require.Error(t, c.Cancel(ctx, &builderv2.Run{}))
	_, err = c.Seal(ctx, &builderv2.Sealing{})
	require.Error(t, err)
	_, err = c.Follow(ctx, &plmv2.FollowRequest{})
	require.Error(t, err)

	_, err = BuilderAt(newHost(t, mn), "")
	require.Error(t, err, "nobody is no builder")
}

// local is a builder running on the instance itself.
type local struct{ run.Builder }

func (local) Start(context.Context, service.Host) error { return nil }
func (local) Close() error                              { return nil }

func TestTheBuildersAreWhomTheFinderFindsAndThisNodesOwnIsInProcess(t *testing.T) {
	_, far := servedBuilder(t, &aBuilder{})
	h := newInstance(t)
	p := h.Node
	builders := BuildersOn(h)
	t.Cleanup(func() { builders.Close() })
	require.Empty(t, builders.Peers(), "nothing found and none here is no builder")

	here := local{}
	h.finding = named{far}
	h.Serve(plm.BuilderProtocol, here)
	require.Equal(t, []libp2ppeer.ID{p.ID(), far}, builders.Peers())

	got, err := builders.At(p.ID())
	require.NoError(t, err)
	require.Equal(t, here, got, "a node does not dial itself: the builder beside it is it")

	first, err := builders.At(far)
	require.NoError(t, err)
	again, err := builders.At(far)
	require.NoError(t, err)
	require.Same(t, first, again, "a builder is dialled once")
	require.NoError(t, builders.Close())

	none := newInstance(t, far)
	elsewhere := BuildersOn(none)
	t.Cleanup(func() { elsewhere.Close() })
	_, err = elsewhere.At(none.ID())
	require.NoError(t, err, "with none beside it, this node's id is dialled like any other")
	_, err = builders.At("")
	require.Error(t, err)
}
