package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"time"

	"connectrpc.com/connect"
	"github.com/ipfs/go-cid"
	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"

	"github.com/taubyte/tau/pkg/proto/codec"
	storagev2 "github.com/taubyte/tau/pkg/proto/gen/tau/storage/v2"
	"github.com/taubyte/tau/pkg/proto/gen/tau/storage/v2/storagev2connect"
)

func (s *server) mount() http.Handler {
	mux := http.NewServeMux()
	mux.Handle(storagev2connect.NewStorageServiceHandler(&handler{s: s}, codec.Option()))
	return mux
}

type handler struct{ s *server }

// storagev2Client is another storage node, as this one calls it.
type storagev2Client = storagev2connect.StorageServiceClient

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
	o := Object{
		ContentType: head.GetContentType(), Metadata: head.GetMetadata(), Versioned: head.GetVersioned(),
		Expire: time.Duration(head.GetExpireMs()) * time.Millisecond,
	}
	w, err := h.s.put(ctx, a, string(head.GetName()), o, &chunks{stream: stream})
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

func (h *handler) List(ctx context.Context, req *connect.Request[storagev2.ListRequest]) (*connect.Response[storagev2.ListResponse], error) {
	a, err := addressOf(req.Msg.GetAt())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	vs, err := h.s.list(ctx, a, string(req.Msg.GetPrefix()), string(req.Msg.GetAfter()), int(req.Msg.GetLimit()), req.Msg.GetHere())
	if err != nil {
		return nil, wire(err)
	}
	out := &storagev2.ListResponse{Versions: make([]*storagev2.Version, 0, len(vs))}
	for _, v := range vs {
		out.Versions = append(out.Versions, wireVersion(v))
	}
	return connect.NewResponse(out), nil
}

func (h *handler) Size(ctx context.Context, req *connect.Request[storagev2.SizeRequest]) (*connect.Response[storagev2.SizeResponse], error) {
	a, err := addressOf(req.Msg.GetAt())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	n, err := h.s.size(ctx, a, req.Msg.GetHere())
	if err != nil {
		return nil, wire(err)
	}
	return connect.NewResponse(&storagev2.SizeResponse{Bytes: n}), nil
}

func (h *handler) Read(ctx context.Context, req *connect.Request[storagev2.ReadRequest], stream *connect.ServerStream[storagev2.ReadResponse]) error {
	root, err := cid.Decode(req.Msg.GetRootCid())
	if err != nil {
		return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("storage: root: %w", err))
	}
	content, err := h.s.open(ctx, root, req.Msg.GetHere())
	if err != nil {
		return wire(err)
	}
	defer content.Close()
	buf := make([]byte, chunkSize)
	for {
		n, err := content.Read(buf)
		if n > 0 {
			if serr := stream.Send(&storagev2.ReadResponse{Chunk: buf[:n]}); serr != nil {
				return serr
			}
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func (h *handler) Probe(ctx context.Context, req *connect.Request[storagev2.ProbeRequest]) (*connect.Response[storagev2.ProbeResponse], error) {
	root, err := cid.Decode(req.Msg.GetRootCid())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("storage: root: %w", err))
	}
	proof, err := h.s.answerProbe(ctx, root, req.Msg.GetNonce())
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&storagev2.ProbeResponse{Proof: proof}), nil
}

func (h *handler) Covers(ctx context.Context, req *connect.Request[storagev2.CoversRequest]) (*connect.Response[storagev2.CoversResponse], error) {
	shard := req.Msg.GetShard()
	if shard >= Shards {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("storage: shard %d of %d", shard, Shards))
	}
	covered := h.s.answerCovers(ctx, uint16(shard), spansOf(req.Msg.GetSpans()))
	return connect.NewResponse(&storagev2.CoversResponse{Covered: covered}), nil
}

func (h *handler) Superseded(ctx context.Context, req *connect.Request[storagev2.SupersededRequest]) (*connect.Response[storagev2.SupersededResponse], error) {
	a, err := addressOf(req.Msg.GetAt())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	root, err := cid.Decode(req.Msg.GetRootCid())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("storage: root: %w", err))
	}
	st, err := h.s.answerSuperseded(ctx, a, string(req.Msg.GetName()), req.Msg.GetStamp(), root)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&storagev2.SupersededResponse{Held: st.held, Superseded: st.superseded, Kept: st.kept, Expired: st.expired}), nil
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
func (s *server) current(ctx context.Context, a Address, name string, here bool) (Version, error) {
	var out Version
	err := s.fromIndex(ctx, a, here, func(i index) error {
		v, err := i.Current(ctx, a, name)
		out = v
		return err
	}, func(svc storagev2Client) error {
		res, err := svc.Current(ctx, connect.NewRequest(&storagev2.CurrentRequest{At: wireAddress(a), Name: []byte(name), Here: true}))
		if err != nil {
			return err
		}
		out, err = versionFromWire(a, name, res.Msg.GetVersion())
		return err
	})
	if err == nil {
		s.observe(out)
	}
	return out, err
}

// observe advances this node's clock past versions it read: an overwrite or a delete
// of what was read, stamped here next, is stamped after it.
func (s *server) observe(vs ...Version) {
	for _, v := range vs {
		if err := s.clock.Observe(v.Stamp); err != nil {
			s.logf("storage: %v", err)
		}
	}
}

// versions is name's versions, newest first.
func (s *server) versions(ctx context.Context, a Address, name string, limit int, here bool) ([]Version, error) {
	var out []Version
	err := s.fromIndex(ctx, a, here, func(i index) error {
		vs, err := i.Versions(ctx, a, name, limit)
		out = vs
		return err
	}, func(svc storagev2Client) error {
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
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	if err == nil {
		s.observe(out...)
	}
	return out, err
}

// fromIndex answers from this node's copy when it holds the shard, and otherwise from
// the first holder that answers. A request that says here never leaves this node.
//
// A COPY HOLDING NO ROW OF THE INSTANCE ANSWERS NOTHING: opened where placement just
// moved the shard, it holds nothing yet, and would call every object not found. The
// read goes to the next holder, and is not found only when every copy holds nothing.
func (s *server) fromIndex(ctx context.Context, a Address, here bool, local func(index) error, remote func(storagev2Client) error) error {
	i, release, ok, err := s.indexOf(ctx, a)
	if err != nil {
		return err
	}
	empty := 0
	if ok {
		held, err := i.holdsAny(ctx, a)
		if err == nil && held {
			defer release()
			return local(i)
		}
		release()
		if err != nil {
			return err
		}
		empty++
		if here {
			return fmt.Errorf("%w: %s", ErrEmptyCopy, a)
		}
	}
	if here {
		return fmt.Errorf("storage: this node does not hold %s's index", a)
	}

	shard, err := shardOf(a.Bucket())
	if err != nil {
		return err
	}
	replicas, _, err := s.promise(ctx)
	if err != nil {
		return err
	}
	holders := indexHolders(shard, s.placement(), replicas)
	var errs []error
	for _, holder := range holders {
		if holder == s.self {
			continue
		}
		svc, err := s.peer(holder)
		if err == nil {
			if err = unwire(remote(svc)); err == nil || errors.Is(err, ErrNotFound) {
				return err
			}
		}
		if errors.Is(err, ErrEmptyCopy) {
			empty++
		}
		errs = append(errs, fmt.Errorf("%s: %w", holder, err))
	}
	if empty > 0 && empty == len(holders) {
		return fmt.Errorf("%w: no copy of %s's index holds anything of it", ErrNotFound, a)
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
// node that could not be reached; emptyCopy the one FailedPrecondition that is a copy
// holding nothing of the instance.
const (
	underHeld = "storage-under-held"
	emptyCopy = "storage-empty-copy"
)

// wire is an error as a status the caller can tell apart.
func wire(err error) error {
	switch {
	case errors.Is(err, ErrNotFound):
		return connect.NewError(connect.CodeNotFound, err)
	case errors.Is(err, ErrUnderHeld):
		e := connect.NewError(connect.CodeUnavailable, err)
		e.Meta().Set(underHeld, "1")
		return e
	case errors.Is(err, ErrEmptyCopy):
		e := connect.NewError(connect.CodeFailedPrecondition, err)
		e.Meta().Set(emptyCopy, "1")
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
	case errors.As(err, &e) && e.Meta().Get(emptyCopy) != "":
		return fmt.Errorf("%w: %w", ErrEmptyCopy, err)
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
		Writer: r.Writer, Tombstone: r.Tombstone, Exclusive: r.Exclusive, Metadata: sortedMetadata(r.Metadata),
		Kept: r.Kept, ExpiresMs: r.ExpiresMS,
	}
}

func recordOf(m *storagev2.VersionRecord) VersionRecord {
	return VersionRecord{
		RootCID: m.GetRootCid(), Size: m.GetSize(), ContentType: m.GetContentType(), CreatedMS: m.GetCreatedMs(),
		Writer: m.GetWriter(), Tombstone: m.GetTombstone(), Exclusive: m.GetExclusive(), Metadata: metadataMap(m.GetMetadata()),
		Kept: m.GetKept(), ExpiresMS: m.GetExpiresMs(),
	}
}

// sortedMetadata is metadata in name order, so one record always encodes to one byte
// string.
func sortedMetadata(m map[string]string) []*storagev2.Metadata {
	if len(m) == 0 {
		return nil
	}
	out := make([]*storagev2.Metadata, 0, len(m))
	for name, value := range m {
		out = append(out, &storagev2.Metadata{Name: name, Value: value})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func metadataMap(pairs []*storagev2.Metadata) map[string]string {
	if len(pairs) == 0 {
		return nil
	}
	out := make(map[string]string, len(pairs))
	for _, p := range pairs {
		out[p.GetName()] = p.GetValue()
	}
	return out
}

func wireVersion(v Version) *storagev2.Version {
	return &storagev2.Version{
		RootCid: v.Record.RootCID, Stamp: v.Stamp, Writer: v.Writer, Size: v.Record.Size,
		ContentType: v.Record.ContentType, Tombstone: v.Record.Tombstone,
		CreatedMs: v.Record.CreatedMS, Metadata: v.Record.Metadata, Name: []byte(v.Name),
	}
}

func versionFromWire(a Address, name string, w *storagev2.Version) (Version, error) {
	return versionOf(a, name, w.GetStamp(), VersionRecord{
		RootCID: w.GetRootCid(), Size: w.GetSize(), ContentType: w.GetContentType(),
		Writer: w.GetWriter(), Tombstone: w.GetTombstone(), CreatedMS: w.GetCreatedMs(), Metadata: w.GetMetadata(),
	})
}
