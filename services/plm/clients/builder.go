package clients

import (
	"context"
	"io"
	"sync"

	"connectrpc.com/connect"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/taubyte/tau/core/service"
	"github.com/taubyte/tau/p2p/rpc"
	"github.com/taubyte/tau/pkg/proto/codec"
	builderv2 "github.com/taubyte/tau/pkg/proto/gen/tau/builder/v2"
	"github.com/taubyte/tau/pkg/proto/gen/tau/builder/v2/builderv2connect"
	plmv2 "github.com/taubyte/tau/pkg/proto/gen/tau/plm/v2"
	"github.com/taubyte/tau/services/plm"
	"github.com/taubyte/tau/services/plm/run"
)

// Builder is one builder, reached over its protocol: what plm hands a run to.
type Builder struct {
	conn *rpc.Client
	rpc  builderv2connect.BuilderServiceClient
}

// BuilderAt is the builder id is, dialled from h.
func BuilderAt(h host.Host, id peer.ID) (*Builder, error) {
	c, err := rpc.NewClient(h, id, plm.BuilderProtocol)
	if err != nil {
		return nil, err
	}
	return &Builder{conn: c, rpc: builderv2connect.NewBuilderServiceClient(c.HTTP(), c.URL(), codec.Option())}, nil
}

func (r *Builder) Plan(ctx context.Context, w run.Work) (*builderv2.Planned, error) {
	res, err := r.rpc.Plan(ctx, connect.NewRequest(w.Proto()))
	if err != nil {
		return nil, err
	}
	return res.Msg, nil
}

func (r *Builder) Build(ctx context.Context, w run.Work) (*builderv2.Built, error) {
	res, err := r.rpc.Build(ctx, connect.NewRequest(w.Proto()))
	if err != nil {
		return nil, err
	}
	return res.Msg, nil
}

func (r *Builder) Cancel(ctx context.Context, which *builderv2.Run) error {
	_, err := r.rpc.Cancel(ctx, connect.NewRequest(which))
	return err
}

func (r *Builder) Seal(ctx context.Context, s *builderv2.Sealing) (*builderv2.Sealed, error) {
	res, err := r.rpc.Seal(ctx, connect.NewRequest(s))
	if err != nil {
		return nil, err
	}
	return res.Msg, nil
}

// Follow is the run's log as the builder streams it. The stream is opened before it
// returns, so a builder that holds no such log is its error here.
func (r *Builder) Follow(ctx context.Context, req *plmv2.FollowRequest) (run.Events, error) {
	ctx, cancel := context.WithCancel(ctx)
	stream, err := r.rpc.Follow(ctx, connect.NewRequest(req))
	if err != nil {
		cancel()
		return nil, err
	}
	f := &followed{stream: stream, cancel: cancel}
	if !stream.Receive() {
		defer f.Close()
		if err := stream.Err(); err != nil {
			return nil, err
		}
		f.done = true
		return f, nil
	}
	f.first = stream.Msg()
	return f, nil
}

// Close lets go of the builder's connections.
func (r *Builder) Close() error {
	r.conn.Close()
	return nil
}

// followed is a builder's Follow stream, its first event read ahead.
type followed struct {
	stream *connect.ServerStreamForClient[plmv2.Job_Event]
	cancel context.CancelFunc
	first  *plmv2.Job_Event
	done   bool
}

func (f *followed) Receive() (*plmv2.Job_Event, error) {
	if ev := f.first; ev != nil {
		f.first = nil
		return ev, nil
	}
	if f.done || !f.stream.Receive() {
		f.done = true
		if err := f.stream.Err(); err != nil {
			return nil, err
		}
		return nil, io.EOF
	}
	return f.stream.Msg(), nil
}

func (f *followed) Close() error {
	f.cancel()
	return f.stream.Close()
}

// Builders is the builders discovery names beside a plm node, each dialled once and
// kept until Close.
type Builders struct {
	h       service.Host
	mu      sync.Mutex
	dialled map[peer.ID]*Builder
}

// BuildersOn is the builders the directory beside h names.
func BuildersOn(h service.Host) *Builders {
	return &Builders{h: h, dialled: map[peer.ID]*Builder{}}
}

// Peers is every builder the directory names now: none on an instance running no
// directory.
func (r *Builders) Peers() []peer.ID {
	return Discovered(r.h, plm.Builder.Name).Known()
}

// At is the builder id: the one running on this instance when id is this node's,
// which a node cannot dial, else id dialled.
func (r *Builders) At(id peer.ID) (run.Builder, error) {
	if id == r.h.Peer().ID() {
		if here, ok := service.Get[run.Builder](r.h); ok {
			return here, nil
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if c, ok := r.dialled[id]; ok {
		return c, nil
	}
	c, err := BuilderAt(r.h.Peer().Host(), id)
	if err != nil {
		return nil, err
	}
	r.dialled[id] = c
	return c, nil
}

// Close lets go of every builder dialled.
func (r *Builders) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, c := range r.dialled {
		c.Close()
		delete(r.dialled, id)
	}
	return nil
}
