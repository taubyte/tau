package secrets

import (
	"context"
	"errors"
	"net/http"

	"connectrpc.com/connect"

	"github.com/taubyte/tau/p2p/rpc"
	secretsv2 "github.com/taubyte/tau/pkg/proto/gen/tau/secrets/v2"
	"github.com/taubyte/tau/pkg/proto/gen/tau/secrets/v2/secretsv2connect"
	"github.com/taubyte/tau/pkg/secrets/tdh"
)

func (srv *Service) mount() http.Handler {
	mux := http.NewServeMux()
	mux.Handle(secretsv2connect.NewSecretsServiceHandler(&secretsHandler{srv}))
	mux.Handle(secretsv2connect.NewDealerServiceHandler(&dealerHandler{srv}))
	return mux
}

// secretsHandler is the storage surface. Its methods have exactly the
// signatures of the generated client, so a Client on this node calls it
// directly rather than over a stream to itself.
type secretsHandler struct{ srv *Service }

var (
	_ secretsv2connect.SecretsServiceHandler = (*secretsHandler)(nil)
	_ secretsv2connect.SecretsServiceClient  = (*secretsHandler)(nil)
)

func (h *secretsHandler) Key(ctx context.Context, req *connect.Request[secretsv2.KeyRequest]) (*connect.Response[secretsv2.GroupKey], error) {
	key, err := h.key(ctx, req.Msg.GetKeyId(), req.Msg.GetForm())
	if err != nil {
		return nil, connect.NewError(connect.CodeUnavailable, err)
	}
	return connect.NewResponse(&secretsv2.GroupKey{
		KeyId:     key.KeyID,
		Epoch:     key.Epoch,
		Public:    key.Public,
		Threshold: uint32(key.Threshold),
		Members:   key.Members,
	}), nil
}

// key is a key by id, or the active one. A write asking for the active key
// of a cluster that has none yet forms one now rather than failing until the
// boot timer does: the round it starts is the one every member would start.
func (h *secretsHandler) key(ctx context.Context, keyID string, form bool) (*KeyMeta, error) {
	if keyID != "" {
		return h.srv.keys.Get(ctx, keyID)
	}
	key, err := h.srv.ActiveKey(ctx)
	if err == nil || !form {
		return key, err
	}
	if err := h.srv.closing(); err != nil {
		return nil, err
	}
	if err := h.srv.ensureKey(ctx); err != nil {
		return nil, err
	}
	return h.srv.ActiveKey(ctx)
}

func (h *secretsHandler) Put(ctx context.Context, req *connect.Request[secretsv2.PutRequest]) (*connect.Response[secretsv2.Empty], error) {
	if err := h.srv.closing(); err != nil {
		return nil, connect.NewError(connect.CodeUnavailable, err)
	}
	ref, err := refOf(req.Msg.GetRef())
	if err != nil {
		return nil, wire(err)
	}
	w := req.Msg.GetWrapping()
	if w == nil || req.Msg.GetKeyId() == "" || len(req.Msg.GetCiphertext()) == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("secrets: a put needs a key id, a ciphertext and a wrapping"))
	}

	err = h.srv.Set(ctx, ref, req.Msg.GetCiphertext(), req.Msg.GetKeyId(), &tdh.Wrapping{R: w.GetR(), WrappedDEK: w.GetWrappedDek()})
	audit(ctx, "put", ref.String(), err)
	if err != nil {
		return nil, wire(err)
	}
	return connect.NewResponse(&secretsv2.Empty{}), nil
}

func (h *secretsHandler) Get(ctx context.Context, req *connect.Request[secretsv2.Ref]) (*connect.Response[secretsv2.Sealed], error) {
	ref, err := refOf(req.Msg)
	if err != nil {
		return nil, wire(err)
	}

	// A miss is reported as an answer, not an error. This node cannot tell an
	// absent secret from one that has not replicated here yet, so it says only
	// what it knows -- "not on me" -- and lets the caller, which can ask other
	// members and wait, decide what that means.
	stored, key, wrapping, writer, err := h.srv.Get(ctx, ref)
	audit(ctx, "get", ref.String(), err)
	if errors.Is(err, ErrNotSet) {
		return connect.NewResponse(&secretsv2.Sealed{}), nil
	}
	if err != nil {
		return nil, wire(err)
	}

	return connect.NewResponse(&secretsv2.Sealed{
		Found:      true,
		KeyId:      key.KeyID,
		Epoch:      key.Epoch,
		Writer:     writer,
		Wrapping:   &secretsv2.Wrapping{R: wrapping.R, WrappedDek: wrapping.WrappedDEK},
		Ciphertext: stored.Ciphertext,
		Cid:        stored.CID,
	}), nil
}

func (h *secretsHandler) Previous(ctx context.Context, req *connect.Request[secretsv2.Ref]) (*connect.Response[secretsv2.Sealed], error) {
	ref, err := refOf(req.Msg)
	if err != nil {
		return nil, wire(err)
	}

	stored, replacedAt, key, wrapping, writer, err := h.srv.Previous(ctx, ref)
	audit(ctx, "previous", ref.String(), err)
	if errors.Is(err, ErrNotSet) {
		return connect.NewResponse(&secretsv2.Sealed{}), nil
	}
	if err != nil {
		return nil, wire(err)
	}

	return connect.NewResponse(&secretsv2.Sealed{
		Found:      true,
		KeyId:      key.KeyID,
		Epoch:      key.Epoch,
		Writer:     writer,
		Wrapping:   &secretsv2.Wrapping{R: wrapping.R, WrappedDek: wrapping.WrappedDEK},
		Ciphertext: stored.Ciphertext,
		Cid:        stored.CID,
		ReplacedAt: replacedAt,
	}), nil
}

func (h *secretsHandler) Partial(ctx context.Context, req *connect.Request[secretsv2.PartialRequest]) (*connect.Response[secretsv2.PartialDecryption], error) {
	ref, err := refOf(req.Msg.GetRef())
	if err != nil {
		return nil, wire(err)
	}

	v := version{Ref: ref, CID: req.Msg.GetCid()}
	partial, err := h.srv.Partial(ctx, v, req.Msg.GetKeyId(), req.Msg.GetWriter())
	audit(ctx, "partial", v.String(), err)
	if err != nil {
		return nil, wire(err)
	}
	return connect.NewResponse(&secretsv2.PartialDecryption{Id: partial.ID, D: partial.D}), nil
}

func (h *secretsHandler) Delete(ctx context.Context, req *connect.Request[secretsv2.Ref]) (*connect.Response[secretsv2.Empty], error) {
	if err := h.srv.closing(); err != nil {
		return nil, connect.NewError(connect.CodeUnavailable, err)
	}
	ref, err := refOf(req.Msg)
	if err != nil {
		return nil, wire(err)
	}

	err = h.srv.Delete(ctx, ref)
	audit(ctx, "delete", ref.String(), err)
	if err != nil {
		return nil, wire(err)
	}
	return connect.NewResponse(&secretsv2.Empty{}), nil
}

func (h *secretsHandler) List(ctx context.Context, req *connect.Request[secretsv2.Scope]) (*connect.Response[secretsv2.Names], error) {
	scope := scopeOf(req.Msg)
	refs, err := h.srv.List(ctx, scope)
	audit(ctx, "list", scope.String(), err)
	if err != nil {
		return nil, wire(err)
	}

	names := make([]string, 0, len(refs))
	for _, ref := range refs {
		names = append(names, ref.Name)
	}
	return connect.NewResponse(&secretsv2.Names{Names: names}), nil
}

// dealerHandler is the key-generation surface between members.
type dealerHandler struct{ srv *Service }

var _ secretsv2connect.DealerServiceHandler = (*dealerHandler)(nil)

func (h *dealerHandler) Contribute(ctx context.Context, req *connect.Request[secretsv2.Contribution]) (*connect.Response[secretsv2.Empty], error) {
	if err := h.srv.closing(); err != nil {
		return nil, connect.NewError(connect.CodeUnavailable, err)
	}

	err := h.srv.receive(ctx, req.Msg)
	audit(ctx, "dkg-contribute", req.Msg.GetRound(), err)
	if err != nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, err)
	}
	return connect.NewResponse(&secretsv2.Empty{}), nil
}

// Replay regenerates the contribution this node dealt to the caller in a
// finished round, so a member that lost its share can rebuild it.
//
// The recipient is the caller's own authenticated identity, never a name in the
// request. Serving somebody else's contribution would let one caller gather t of
// them and reconstruct the group secret, which is strictly cheaper than the
// partial-decryption path it would replace.
func (h *dealerHandler) Replay(ctx context.Context, req *connect.Request[secretsv2.ReplayRequest]) (*connect.Response[secretsv2.Contribution], error) {
	caller, err := rpc.RemotePeer(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("replay needs an identified caller"))
	}

	out, err := h.srv.replay(ctx, req.Msg.GetKeyId(), caller.String())
	audit(ctx, "dkg-replay", req.Msg.GetKeyId(), err)
	if err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}
	return connect.NewResponse(out), nil
}

func wireScope(s Scope) *secretsv2.Scope {
	if s.Service != "" {
		return &secretsv2.Scope{Scope: &secretsv2.Scope_Cloud{Cloud: &secretsv2.CloudScope{Service: s.Service}}}
	}
	return &secretsv2.Scope{Scope: &secretsv2.Scope_Project{Project: &secretsv2.ProjectScope{Project: s.Project, Environment: s.Environment}}}
}

func wireRef(ref Ref) *secretsv2.Ref {
	return &secretsv2.Ref{Scope: wireScope(ref.Scope), Name: ref.Name}
}

func scopeOf(s *secretsv2.Scope) Scope {
	if cloud := s.GetCloud(); cloud != nil {
		return Cloud(cloud.GetService())
	}
	return Project(s.GetProject().GetProject(), s.GetProject().GetEnvironment())
}

func refOf(r *secretsv2.Ref) (Ref, error) {
	ref := Ref{Scope: scopeOf(r.GetScope()), Name: r.GetName()}
	return ref, ref.Validate()
}

// wire carries this package's sentinels across the transport.
func wire(err error) error {
	switch {
	case errors.Is(err, ErrInvalidRef):
		return connect.NewError(connect.CodeInvalidArgument, err)
	case errors.Is(err, ErrNotSet):
		return connect.NewError(connect.CodeNotFound, err)
	default:
		return connect.NewError(connect.CodeUnavailable, err)
	}
}

// unwire restores them on the other side.
func unwire(err error) error {
	switch connect.CodeOf(err) {
	case connect.CodeInvalidArgument:
		return errors.Join(ErrInvalidRef, err)
	case connect.CodeNotFound:
		return errors.Join(ErrNotSet, err)
	default:
		return err
	}
}
