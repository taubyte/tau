package register

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"connectrpc.com/connect"
	"github.com/libp2p/go-libp2p/core/host"
	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"

	"github.com/taubyte/tau/p2p/rpc"
	pkgregister "github.com/taubyte/tau/pkg/register"

	"github.com/taubyte/tau/pkg/proto/codec"
	registerv2 "github.com/taubyte/tau/pkg/proto/gen/tau/register/v2"
	"github.com/taubyte/tau/pkg/proto/gen/tau/register/v2/registerv2connect"
)

// client.go is the other side of rpc.go, and it exists so that a consumer can
// hold ONE interface whether the register is on this node or another.
//
// THE ERRORS COME BACK AS THE SAME SENTINELS. A caller does
// `errors.Is(err, register.ErrNotPublished)` and it is true whether the answer
// came from a map in this process or from a status code across a stream.
// Without that, every consumer would carry two error paths for one question and
// would get the remote one wrong, because a status code is easy to ignore.
//
// It is the exact inverse of rpc.go's `wire`, and the pair is the reason a
// worker can be composed against a local *pkgregister.Register in a unit test
// and against a Client in production with no second code path.

// Client calls a register on another peer.
type Client struct {
	conn *rpc.Client
	svc  registerv2connect.RegisterServiceClient
}

// NewClient prepares calls to the register on p. Nothing is dialled until the
// first call, and that call's context governs the dial.
func NewClient(h host.Host, p libp2ppeer.ID) (*Client, error) {
	conn, err := rpc.NewClient(h, p, Protocol)
	if err != nil {
		return nil, fmt.Errorf("register: dialling %s: %w", p, err)
	}
	return &Client{conn: conn, svc: registerv2connect.NewRegisterServiceClient(conn.HTTP(), conn.URL(), codec.Option())}, nil
}

// Close releases the stream. Calls after it fail.
func (c *Client) Close() { c.conn.Close() }

func (c *Client) Publish(ctx context.Context, p pkgregister.Publication) (pkgregister.ObjectID, error) {
	res, err := c.svc.Publish(ctx, connect.NewRequest(&registerv2.PublishRequest{
		Project: p.Project, Environment: p.Environment, Revision: p.Revision,
		Compiled: p.Compiled, Hosts: p.Hosts,
		Views: viewsToWire(p.Views), Cut: p.Views != nil,
	}))
	if err != nil {
		return "", unwire(err)
	}
	return pkgregister.ObjectID(res.Msg.GetObject()), nil
}

func (c *Client) Resolve(ctx context.Context, project, environment string) (pkgregister.Pointer, error) {
	res, err := c.svc.Resolve(ctx, connect.NewRequest(&registerv2.ResolveRequest{
		Project: project, Environment: environment,
	}))
	if err != nil {
		return pkgregister.Pointer{}, unwire(err)
	}
	return pkgregister.Pointer{
		Object:   pkgregister.ObjectID(res.Msg.GetObject()),
		Revision: res.Msg.GetRevision(),
	}, nil
}

// bindAs is the session and entry a read binds its answer to; the zero bindAs binds
// nothing.
type bindAs struct {
	session string
	entry   uint64
}

func (b bindAs) on(h http.Header) {
	if b.session != "" {
		h.Set(sessionHeader, b.session)
		h.Set(entryHeader, strconv.FormatUint(b.entry, 10))
	}
}

// wasBound reads the register's mark that it bound an answer, on a response's
// headers or a refusal's metadata.
func wasBound(h http.Header, err error) bool {
	if err != nil {
		var ce *connect.Error
		return errors.As(err, &ce) && ce.Meta().Get(boundHeader) == "1"
	}
	return h.Get(boundHeader) == "1"
}

// hostAnswer is whose environment answers on a name.
type hostAnswer struct{ project, environment string }

func (c *Client) resolveHostAs(ctx context.Context, fqdn string, b bindAs) (hostAnswer, bool, error) {
	req := connect.NewRequest(&registerv2.HostRequest{Fqdn: fqdn})
	b.on(req.Header())
	res, err := c.svc.ResolveHost(ctx, req)
	if err != nil {
		return hostAnswer{}, wasBound(nil, err), unwire(err)
	}
	return hostAnswer{project: res.Msg.GetProject(), environment: res.Msg.GetEnvironment()}, wasBound(res.Header(), nil), nil
}

func (c *Client) resolveViewAs(ctx context.Context, project, environment, destination string, b bindAs) (pkgregister.ViewPointer, bool, error) {
	req := connect.NewRequest(&registerv2.ViewRequest{Project: project, Environment: environment, Destination: destination})
	b.on(req.Header())
	res, err := c.svc.ResolveView(ctx, req)
	if err != nil {
		return pkgregister.ViewPointer{}, wasBound(nil, err), unwire(err)
	}
	return viewFromWire(res.Msg), wasBound(res.Header(), nil), nil
}

// watch opens a session on this register.
func (c *Client) watch(ctx context.Context) (*connect.ServerStreamForClient[registerv2.WatchEvent], error) {
	return c.svc.Watch(ctx, connect.NewRequest(&registerv2.WatchRequest{}))
}

// forget releases entries this reader no longer keeps on session.
func (c *Client) forget(ctx context.Context, session string, entries []uint64) error {
	_, err := c.svc.Forget(ctx, connect.NewRequest(&registerv2.ForgetRequest{Session: session, Entries: entries}))
	return err
}

// ResolveHost says whose environment answers on a name.
// Environments is which deployments a project has, sorted.
func (c *Client) Environments(ctx context.Context, project string) ([]string, error) {
	res, err := c.svc.Environments(ctx, connect.NewRequest(&registerv2.ProjectRequest{Project: project}))
	if err != nil {
		return nil, unwire(err)
	}
	return res.Msg.GetEnvironments(), nil
}

func (c *Client) ResolveHost(ctx context.Context, fqdn string) (project, environment string, err error) {
	a, _, err := c.resolveHostAs(ctx, fqdn, bindAs{})
	return a.project, a.environment, err
}

func (c *Client) Object(ctx context.Context, id pkgregister.ObjectID) ([]byte, error) {
	res, err := c.svc.Object(ctx, connect.NewRequest(&registerv2.ObjectRequest{Object: string(id)}))
	if err != nil {
		return nil, unwire(err)
	}
	return res.Msg.GetCompiled(), nil
}

// unwire turns a status back into the sentinel the local register would have
// returned.
//
// IT MATCHES ON THE MESSAGE AS WELL AS THE CODE, because two sentinels share a
// code: NotFound is both "this environment has nothing published" and "there is
// no such object", and they are different answers — the first says deploy, the
// second says the store lost an object a pointer still names.
func viewsToWire(in []pkgregister.View) []*registerv2.View {
	out := make([]*registerv2.View, 0, len(in))
	for _, v := range in {
		out = append(out, &registerv2.View{Destination: v.Destination, Compiled: v.Compiled})
	}
	return out
}

func viewFromWire(p *registerv2.ViewPointer) pkgregister.ViewPointer {
	return pkgregister.ViewPointer{
		Object:   pkgregister.ObjectID(p.GetObject()),
		Revision: p.GetRevision(),
		Manifest: pkgregister.ObjectID(p.GetManifest()),
	}
}

// ResolveView is which object a destination holds of a deployment.
func (c *Client) ResolveView(ctx context.Context, project, environment, destination string) (pkgregister.ViewPointer, error) {
	p, _, err := c.resolveViewAs(ctx, project, environment, destination, bindAs{})
	return p, err
}

// Assigned is every deployment a destination holds a view of now.
func (c *Client) Assigned(ctx context.Context, destination string) ([]pkgregister.Assignment, error) {
	res, err := c.svc.Assigned(ctx, connect.NewRequest(&registerv2.DestinationRequest{Destination: destination}))
	if err != nil {
		return nil, unwire(err)
	}
	var out []pkgregister.Assignment
	for _, a := range res.Msg.GetAssignments() {
		out = append(out, pkgregister.Assignment{Project: a.GetProject(), Environment: a.GetEnvironment(), View: viewFromWire(a.GetView())})
	}
	return out, nil
}

func unwire(err error) error {
	var cerr *connect.Error
	if !errors.As(err, &cerr) {
		return err
	}

	switch cerr.Code() {
	case connect.CodeNotFound:
		if sentinel := matching(cerr.Message(),
			pkgregister.ErrNotPublished, pkgregister.ErrNoObject, pkgregister.ErrNoHost, pkgregister.ErrNoView,
			pkgregister.ErrNotLinked, pkgregister.ErrNoGrant); sentinel != nil {
			return fmt.Errorf("%w: %s", sentinel, cerr.Message())
		}
	case connect.CodeAlreadyExists:
		if sentinel := matching(cerr.Message(),
			pkgregister.ErrProjectClaimed, pkgregister.ErrRepositoryClaimed); sentinel != nil {
			return fmt.Errorf("%w: %s", sentinel, cerr.Message())
		}
		return fmt.Errorf("%w: %s", pkgregister.ErrHostClaimed, cerr.Message())
	case connect.CodeFailedPrecondition:
		return fmt.Errorf("%w: %s", pkgregister.ErrStaleRevision, cerr.Message())
	case connect.CodeDataLoss:
		return fmt.Errorf("%w: %s", pkgregister.ErrMalformedPointer, cerr.Message())
	case connect.CodeInvalidArgument:
		if sentinel := matching(cerr.Message(),
			pkgregister.ErrNoProject, pkgregister.ErrNoEnvironment,
			pkgregister.ErrEmptyObject, pkgregister.ErrSeparator, pkgregister.ErrNoDestination,
			pkgregister.ErrNoRepository, pkgregister.ErrNoClaimant,
			pkgregister.ErrNoRole, pkgregister.ErrNoGrantee); sentinel != nil {
			return fmt.Errorf("%w: %s", sentinel, cerr.Message())
		}
	}
	return err
}

// matching finds which sentinel a status message was built from. The wire
// carries the message the server produced, and every sentinel's text is in it
// because rpc.go wraps rather than replaces.
func matching(message string, candidates ...error) error {
	for _, c := range candidates {
		if strings.HasPrefix(message, c.Error()) {
			return c
		}
	}
	return nil
}

// Manifest is a deployment's current revision and every destination it lists.
func (c *Client) Manifest(ctx context.Context, project, environment string) (pkgregister.Manifest, pkgregister.ObjectID, error) {
	res, err := c.svc.Manifest(ctx, connect.NewRequest(&registerv2.ResolveRequest{
		Project: project, Environment: environment,
	}))
	if err != nil {
		return pkgregister.Manifest{}, "", unwire(err)
	}
	m := pkgregister.Manifest{
		Project: res.Msg.GetProject(), Environment: res.Msg.GetEnvironment(), Revision: res.Msg.GetRevision(),
		Object: pkgregister.ObjectID(res.Msg.GetObject()), Whole: res.Msg.GetWhole(),
		Views: map[string]pkgregister.ObjectID{},
	}
	for d, v := range res.Msg.GetViews() {
		m.Views[d] = pkgregister.ObjectID(v)
	}
	return m, pkgregister.ObjectID(res.Msg.GetId()), nil
}
