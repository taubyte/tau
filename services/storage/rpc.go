package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"

	"connectrpc.com/connect"
	"github.com/ipfs/go-cid"
	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"

	"github.com/taubyte/tau/pkg/proto/codec"
	storagev2 "github.com/taubyte/tau/pkg/proto/gen/tau/storage/v2"
	"github.com/taubyte/tau/pkg/proto/gen/tau/storage/v2/storagev2connect"
)

func (s *Service) mount() http.Handler {
	mux := http.NewServeMux()
	mux.Handle(storagev2connect.NewStorageServiceHandler(&handler{s: s}, codec.Option()))
	return mux
}

type handler struct{ s *Service }

var _ storagev2connect.StorageServiceHandler = (*handler)(nil)

func (h *handler) Put(ctx context.Context, stream *connect.ClientStream[storagev2.PutRequest]) (*connect.Response[storagev2.PutResponse], error) {
	if !stream.Receive() {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.Join(errors.New("storage: a put begins with its head"), stream.Err()))
	}
	head := stream.Msg().GetHead()
	if head == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("storage: a put begins with its head"))
	}
	a, err := addressOf(head.GetAt())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	w, err := h.s.put(ctx, a, string(head.GetName()), head.GetContentType(), &chunks{stream: stream})
	if err != nil {
		return nil, wire(err)
	}
	return connect.NewResponse(&storagev2.PutResponse{Version: wireVersion(w.version), Holders: uint32(w.holders)}), nil
}

func (h *handler) Current(ctx context.Context, req *connect.Request[storagev2.CurrentRequest]) (*connect.Response[storagev2.CurrentResponse], error) {
	a, err := addressOf(req.Msg.GetAt())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	v, err := h.s.current(ctx, a, string(req.Msg.GetName()), req.Msg.GetHere())
	if err != nil {
		return nil, wire(err)
	}
	return connect.NewResponse(&storagev2.CurrentResponse{Version: wireVersion(v)}), nil
}

func (h *handler) Versions(ctx context.Context, req *connect.Request[storagev2.VersionsRequest]) (*connect.Response[storagev2.VersionsResponse], error) {
	a, err := addressOf(req.Msg.GetAt())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	vs, err := h.s.versions(ctx, a, string(req.Msg.GetName()), int(req.Msg.GetLimit()), req.Msg.GetHere())
	if err != nil {
		return nil, wire(err)
	}
	out := &storagev2.VersionsResponse{Versions: make([]*storagev2.Version, 0, len(vs))}
	for _, v := range vs {
		out.Versions = append(out.Versions, wireVersion(v))
	}
	return connect.NewResponse(out), nil
}

func (h *handler) Delete(ctx context.Context, req *connect.Request[storagev2.DeleteRequest]) (*connect.Response[storagev2.DeleteResponse], error) {
	a, err := addressOf(req.Msg.GetAt())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	v, err := h.s.remove(ctx, a, string(req.Msg.GetName()))
	if err != nil {
		return nil, wire(err)
	}
	return connect.NewResponse(&storagev2.DeleteResponse{Version: wireVersion(v)}), nil
}

func (h *handler) Hold(ctx context.Context, req *connect.Request[storagev2.HoldRequest]) (*connect.Response[storagev2.HoldResponse], error) {
	a, err := addressOf(req.Msg.GetAt())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	root, err := cid.Decode(req.Msg.GetRootCid())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("storage: root: %w", err))
	}
	from, err := libp2ppeer.Decode(req.Msg.GetFrom())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("storage: from: %w", err))
	}
	if err := h.s.hold(ctx, root, from, a, string(req.Msg.GetName()), req.Msg.GetStamp()); err != nil {
		return nil, wire(err)
	}
	return connect.NewResponse(&storagev2.HoldResponse{}), nil
}

func (h *handler) Append(ctx context.Context, req *connect.Request[storagev2.AppendRequest]) (*connect.Response[storagev2.AppendResponse], error) {
	a, err := addressOf(req.Msg.GetAt())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	if req.Msg.GetRecord() == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("storage: an append carries its row"))
	}
	if err := h.s.appendHere(ctx, a, string(req.Msg.GetName()), req.Msg.GetStamp(), recordOf(req.Msg.GetRecord())); err != nil {
		return nil, wire(err)
	}
	return connect.NewResponse(&storagev2.AppendResponse{}), nil
}

// current is the newest version of name, from this node's copy of the index or a
// holder's.
func (s *Service) current(ctx context.Context, a Address, name string, here bool) (Version, error) {
	var out Version
	err := s.read(ctx, a, here, func(i index) error {
		v, err := i.Current(ctx, a, name)
		out = v
		return err
	}, func(svc storagev2connect.StorageServiceClient) error {
		res, err := svc.Current(ctx, connect.NewRequest(&storagev2.CurrentRequest{At: wireAddress(a), Name: []byte(name), Here: true}))
		if err != nil {
			return err
		}
		out, err = versionFromWire(a, name, res.Msg.GetVersion())
		return err
	})
	return out, err
}

// versions is name's versions, newest first.
func (s *Service) versions(ctx context.Context, a Address, name string, limit int, here bool) ([]Version, error) {
	var out []Version
	err := s.read(ctx, a, here, func(i index) error {
		vs, err := i.Versions(ctx, a, name, limit)
		out = vs
		return err
	}, func(svc storagev2connect.StorageServiceClient) error {
		res, err := svc.Versions(ctx, connect.NewRequest(&storagev2.VersionsRequest{At: wireAddress(a), Name: []byte(name), Limit: uint32(limit), Here: true}))
		if err != nil {
			return err
		}
		out = out[:0]
		for _, w := range res.Msg.GetVersions() {
			v, err := versionFromWire(a, name, w)
			if err != nil {
				return err
			}
			out = append(out, v)
		}
		return nil
	})
	return out, err
}

// read answers from this node's copy when it holds the shard, and otherwise from the
// first holder that answers. A request that says here never leaves this node.
func (s *Service) read(ctx context.Context, a Address, here bool, local func(index) error, remote func(storagev2connect.StorageServiceClient) error) error {
	i, ok, err := s.indexOf(ctx, a)
	if err != nil {
		return err
	}
	if ok || here {
		if !ok {
			return fmt.Errorf("storage: this node does not hold %s's index", a)
		}
		return local(i)
	}

	shard, err := shardOf(a.Bucket())
	if err != nil {
		return err
	}
	replicas, _, _ := s.fleet.read(ctx)
	var errs []error
	for _, holder := range indexHolders(shard, s.placement(), replicas) {
		svc, err := s.peer(holder)
		if err == nil {
			if err = remote(svc); err == nil || errors.Is(err, ErrNotFound) {
				return err
			}
		}
		errs = append(errs, fmt.Errorf("%s: %w", holder, err))
	}
	return fmt.Errorf("storage: no holder of %s's index answered: %w", a, errors.Join(errs...))
}

// chunks reads a put's bytes out of the stream after its head.
type chunks struct {
	stream *connect.ClientStream[storagev2.PutRequest]
	buf    []byte
}

func (c *chunks) Read(p []byte) (int, error) {
	for len(c.buf) == 0 {
		if !c.stream.Receive() {
			if err := c.stream.Err(); err != nil {
				return 0, err
			}
			return 0, io.EOF
		}
		c.buf = c.stream.Msg().GetChunk()
	}
	n := copy(p, c.buf)
	c.buf = c.buf[n:]
	return n, nil
}

// underHeld marks the one Unavailable that is this service's refusal rather than a
// node that could not be reached.
const underHeld = "storage-under-held"

// wire is an error as a status the caller can tell apart.
func wire(err error) error {
	switch {
	case errors.Is(err, ErrNotFound):
		return connect.NewError(connect.CodeNotFound, err)
	case errors.Is(err, ErrUnderHeld):
		e := connect.NewError(connect.CodeUnavailable, err)
		e.Meta().Set(underHeld, "1")
		return e
	case errors.Is(err, ErrNoAddress), errors.Is(err, ErrEmptyIdentity):
		return connect.NewError(connect.CodeInvalidArgument, err)
	}
	return err
}

// unwire is a status as the error it was sent as.
func unwire(err error) error {
	var e *connect.Error
	switch {
	case connect.CodeOf(err) == connect.CodeNotFound:
		return fmt.Errorf("%w: %w", ErrNotFound, err)
	case errors.As(err, &e) && e.Meta().Get(underHeld) != "":
		return fmt.Errorf("%w: %w", ErrUnderHeld, err)
	}
	return err
}

func addressOf(w *storagev2.Address) (Address, error) {
	return NewAddress(w.GetProject(), w.GetEnvironment(), w.GetBucket(), w.GetInstance())
}

func wireAddress(a Address) *storagev2.Address {
	b := a.Bucket()
	return &storagev2.Address{Project: b.Project(), Environment: b.Environment(), Bucket: b.Name(), Instance: a.Instance()}
}

func wireRecord(r VersionRecord) *storagev2.VersionRecord {
	return &storagev2.VersionRecord{
		RootCid: r.RootCID, Size: r.Size, ContentType: r.ContentType, CreatedMs: r.CreatedMS,
		Writer: r.Writer, Tombstone: r.Tombstone, Exclusive: r.Exclusive,
	}
}

func recordOf(m *storagev2.VersionRecord) VersionRecord {
	return VersionRecord{
		RootCID: m.GetRootCid(), Size: m.GetSize(), ContentType: m.GetContentType(), CreatedMS: m.GetCreatedMs(),
		Writer: m.GetWriter(), Tombstone: m.GetTombstone(), Exclusive: m.GetExclusive(),
	}
}

func wireVersion(v Version) *storagev2.Version {
	return &storagev2.Version{
		RootCid: v.Record.RootCID, Stamp: v.Stamp, Writer: v.Writer, Size: v.Record.Size,
		ContentType: v.Record.ContentType, Tombstone: v.Record.Tombstone,
	}
}

func versionFromWire(a Address, name string, w *storagev2.Version) (Version, error) {
	return versionOf(a, name, w.GetStamp(), VersionRecord{
		RootCID: w.GetRootCid(), Size: w.GetSize(), ContentType: w.GetContentType(),
		Writer: w.GetWriter(), Tombstone: w.GetTombstone(),
	})
}
