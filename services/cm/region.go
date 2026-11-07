package cm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"

	"connectrpc.com/connect"
	"github.com/libp2p/go-libp2p/core/host"
	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"

	"github.com/taubyte/tau/core/service"
	"github.com/taubyte/tau/p2p/rpc"
	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/proto/codec"
	regionv2 "github.com/taubyte/tau/pkg/proto/gen/tau/region/v2"
	"github.com/taubyte/tau/pkg/proto/gen/tau/region/v2/regionv2connect"
	workerv2 "github.com/taubyte/tau/pkg/proto/gen/tau/worker/v2"
	"github.com/taubyte/tau/pkg/register"
	"github.com/taubyte/tau/pkg/specs"
	"github.com/taubyte/tau/utils/id"
)

// Each region runs its own cm, and that cm is the region's end of the link between
// the regions of one cloud (tau/region/v2): a service hands its cm a call that has
// to be answered in another region, cm hands it to the Link, and the cm at the far
// end checks it against its own release before anything runs there. The first call
// that crosses is a gateway route's, to a handler its own region does not run.

// RegionProtocol is what a region's cm answers the services of its region on.
var RegionProtocol protocol.ID = rpc.Protocol(regionv2connect.RegionServiceName)

var (
	// ErrNoLink is a cloud whose regions are not linked to each other.
	ErrNoLink = errors.New("cm: this cloud links none of its regions")

	// ErrUnreachable is a region the link could not reach. It is the only failure
	// that moves a call on to the next region that could answer it.
	ErrUnreachable = errors.New("cm: the region could not be reached")

	// ErrNoCall is a carried message holding nothing this region answers.
	ErrNoCall = errors.New("cm: the call carries nothing this region answers")

	// ErrOtherRelease is a call routed under a release this region does not serve.
	ErrOtherRelease = errors.New("cm: this region serves another release")

	// ErrNoSuchRoute is a call no route of the release reaches.
	ErrNoSuchRoute = errors.New("cm: no route of this release reaches the handler")

	// ErrNotRunHere is a handler this region does not run.
	ErrNotRunHere = errors.New("cm: this region does not run the handler")

	// ErrNoRegion is a handler the caller's routing names no region for.
	ErrNoRegion = errors.New("cm: the routing names no region running the handler")
)

// Link carries calls between the regions of one cloud, one end per region. The
// region an end speaks for is the one every call it carries comes from, so the far
// end knows who is asking from the link and never from the call. Dream links its
// region universes with a mock; production links are the deployment's.
type Link interface {
	// Region is the region this end speaks for.
	Region() string

	// Carry hands c to region's cm and returns its answer, or an error wrapping
	// ErrUnreachable when it could not reach it.
	Carry(ctx context.Context, region string, c *regionv2.Carried) (*regionv2.Answered, error)

	// Attach makes r what calls carried to this region reach, until it is closed.
	Attach(r Receiver) io.Closer
}

// Receiver is a region's cm as the link reaches it.
type Receiver interface {
	Receive(ctx context.Context, from string, c *regionv2.Carried) (*regionv2.Answered, error)
}

// Releases is the register as a region reads it: each deployment's view there, and
// the release it was cut from.
type Releases interface {
	register.Registry
	Routing(ctx context.Context, project, environment string) (register.Routing, error)
}

// Origins is what a region answers carried calls with: the register as the region
// reads it, and the region's workers to run a request on.
type Origins interface {
	Releases
	Invoke(ctx context.Context, at specs.Address, req *invoke.Request) (*invoke.Response, error)
}

// Carrier is a region's cm as the services of that region reach it.
type Carrier interface {
	Carry(ctx context.Context, req *regionv2.CarryRequest) (*regionv2.Answered, error)
}

// receiving is a cm answering the calls carried to its region.
type receiving struct {
	region  string
	origins Origins
	views   *register.Cache[*specs.Project]
	wholes  *register.Cache[*specs.Project]
}

func newReceiving(region string, origins Origins) (*receiving, error) {
	views, err := register.NewCache(origins, specs.Load)
	if err != nil {
		return nil, err
	}
	wholes, err := register.NewCache(wholeOf{origins}, specs.Load)
	if err != nil {
		return nil, err
	}
	return &receiving{region: region, origins: origins, views: views, wholes: wholes}, nil
}

// wholeOf reads a deployment as the whole object its region's view was cut from.
type wholeOf struct{ Releases }

func (w wholeOf) Resolve(ctx context.Context, project, environment string) (register.Pointer, error) {
	r, err := w.Routing(ctx, project, environment)
	return register.Pointer{Object: r.Release}, err
}

// Receive answers a call another region carried here, once this region's own
// release says it may: the same release, a gateway route in it reaching the
// handler, and the handler run here.
func (a *receiving) Receive(ctx context.Context, from string, c *regionv2.Carried) (*regionv2.Answered, error) {
	o := c.GetOrigin()
	if o == nil || o.GetRequest() == nil || from == a.region {
		return nil, ErrNoCall
	}
	route, handler := addressOf(o.GetRoute()), addressOf(o.GetRequest().GetTarget())
	if route.Project != handler.Project || route.Environment != handler.Environment || route.Kind != specs.KindGateway {
		return nil, ErrNoCall
	}
	at := register.Deployment{Project: handler.Project, Environment: handler.Environment}

	routing, err := a.origins.Routing(ctx, at.Project, at.Environment)
	if err != nil {
		return nil, err
	}
	if string(routing.Release) != c.GetRelease() {
		return nil, fmt.Errorf("%w: %s serves %s of %s, and %s routed this under %s", ErrOtherRelease, a.region, routing.Release, at, from, c.GetRelease())
	}

	whole, _, err := a.wholes.At(ctx, at)
	if err != nil {
		return nil, err
	}
	if !reaches(whole, route, handler) {
		return nil, fmt.Errorf("%w: gateway/%s does not reach %s/%s", ErrNoSuchRoute, route.Name, handler.Kind, handler.Name)
	}

	view, _, err := a.views.At(ctx, at)
	if err != nil {
		return nil, err
	}
	if _, _, err := view.Resource(handler.Scope, handler.Kind, handler.Name); err != nil {
		return nil, fmt.Errorf("%w: %s does not run %s/%s", ErrNotRunHere, a.region, handler.Kind, handler.Name)
	}

	req := o.GetRequest()
	res, err := a.origins.Invoke(ctx, handler, &invoke.Request{
		Method:  req.GetMethod(),
		Path:    req.GetPath(),
		Headers: fromWire(req.GetHeaders()),
		Body:    req.GetBody(),
	})
	if err != nil {
		return nil, err
	}
	return &regionv2.Answered{Region: a.region, Response: &workerv2.InvokeResponse{
		Status:  int32(res.Status),
		Headers: toWire(res.Headers),
		Body:    res.Body,
	}}, nil
}

// reaches is whether the gateway at route has a route whose own handler is handler,
// named as the gateway names it: in the route's namespace when it gives one, else in
// the gateway's.
func reaches(whole *specs.Project, route, handler specs.Address) bool {
	r, _, err := whole.Resource(route.Scope, specs.KindGateway, route.Name)
	if err != nil {
		return false
	}
	declared := specs.ReadGateway(r).Route
	if declared.Target == specs.GatewayRouteTargetRouter || declared.HandlerPort != "" {
		return false
	}
	scope := route.Scope
	if ns := strings.Trim(declared.HandlerNamespace, "/"); ns != "" {
		scope = strings.Split(ns, "/")
	}
	return declared.HandlerKind == handler.Kind && declared.HandlerName == handler.Name && slices.Equal(scope, handler.Scope)
}

// carry hands c to the first region in to that the link reaches. A region that is
// reached and refuses ends it.
func carry(ctx context.Context, link Link, to []string, c *regionv2.Carried) (*regionv2.Answered, error) {
	if link == nil {
		return nil, ErrNoLink
	}
	if len(to) == 0 {
		return nil, ErrNoRegion
	}
	var errs []error
	for _, region := range to {
		answered, err := link.Carry(ctx, region, c)
		if err == nil || !errors.Is(err, ErrUnreachable) {
			return answered, err
		}
		errs = append(errs, err)
	}
	return nil, errors.Join(errs...)
}

// mountRegions is the RegionService a cm answers its region's services on.
func mountRegions(c Carrier) http.Handler {
	mux := http.NewServeMux()
	mux.Handle(regionv2connect.NewRegionServiceHandler(regionHandler{c}, codec.Option()))
	return mux
}

type regionHandler struct{ c Carrier }

func (h regionHandler) Carry(ctx context.Context, req *connect.Request[regionv2.CarryRequest]) (*connect.Response[regionv2.Answered], error) {
	answered, err := h.c.Carry(ctx, req.Msg)
	if err != nil {
		return nil, regionWire(err)
	}
	return connect.NewResponse(answered), nil
}

func regionWire(err error) error {
	switch {
	case errors.Is(err, ErrNoCall), errors.Is(err, ErrNoRegion):
		return connect.NewError(connect.CodeInvalidArgument, err)
	case errors.Is(err, ErrNoSuchRoute):
		return connect.NewError(connect.CodePermissionDenied, err)
	case errors.Is(err, ErrNotRunHere), errors.Is(err, register.ErrNotPublished), errors.Is(err, register.ErrNoView):
		return connect.NewError(connect.CodeNotFound, err)
	case errors.Is(err, ErrOtherRelease), errors.Is(err, ErrNoLink):
		return connect.NewError(connect.CodeFailedPrecondition, err)
	}
	return connect.NewError(connect.CodeUnavailable, err)
}

// regionClient is a region's cm on another node of that region.
type regionClient struct {
	conn *rpc.Client
	svc  regionv2connect.RegionServiceClient
}

func dialRegions(h host.Host, p libp2ppeer.ID) (Carrier, error) {
	conn, err := rpc.NewClient(h, p, RegionProtocol)
	if err != nil {
		return nil, fmt.Errorf("cm: dialling %s: %w", p, err)
	}
	return &regionClient{conn: conn, svc: regionv2connect.NewRegionServiceClient(conn.HTTP(), conn.URL(), codec.Option())}, nil
}

func (c *regionClient) Carry(ctx context.Context, req *regionv2.CarryRequest) (*regionv2.Answered, error) {
	res, err := c.svc.Carry(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, err
	}
	return res.Msg, nil
}

func (c *regionClient) Close() { c.conn.Close() }

// Routes is what a region's routes reach elsewhere, as the register says it.
type Routes interface {
	Routing(ctx context.Context, project, environment string) (register.Routing, error)
}

// Regions is how a gateway route reaches a handler only other regions run: by the
// routing its own region's view was published with, through its region's cm.
type Regions struct {
	routes Routes
	reach  *rpc.Reacher[Carrier]
}

// RegionsOn is Regions for the services of the instance h, reading routes.
func RegionsOn(h service.Host, routes Routes) *Regions {
	return &Regions{routes: routes, reach: service.Reach(h, RegionProtocol, dialRegions)}
}

// Origin answers req with handler, run in the first region the routing names for it
// that answers, as the route at route asked for it. The answer says which region
// that was.
func (r *Regions) Origin(ctx context.Context, route, handler specs.Address, req *invoke.Request) (*invoke.Response, string, error) {
	routing, err := r.routes.Routing(ctx, handler.Project, handler.Environment)
	if err != nil {
		return nil, "", err
	}
	to := routing.In(handler.Scope, handler.Kind, handler.Name)
	if len(to) == 0 {
		return nil, "", fmt.Errorf("%w: %s/%s", ErrNoRegion, handler.Kind, handler.Name)
	}
	cm, err := r.reach.Now(ctx)
	if err != nil {
		return nil, "", err
	}
	answered, err := cm.Carry(ctx, &regionv2.CarryRequest{To: to, Carried: &regionv2.Carried{
		Release: string(routing.Release),
		Id:      id.Generate(),
		Call: &regionv2.Carried_Origin{Origin: &regionv2.Origin{
			Route: targetOf(route),
			Request: &workerv2.InvokeRequest{
				Target:  targetOf(handler),
				Method:  req.Method,
				Path:    req.Path,
				Headers: toWire(req.Headers),
				Body:    req.Body,
			},
		}},
	}})
	if err != nil {
		return nil, "", err
	}
	res := answered.GetResponse()
	return &invoke.Response{
		Status:  int(res.GetStatus()),
		Headers: fromWire(res.GetHeaders()),
		Body:    res.GetBody(),
	}, answered.GetRegion(), nil
}

// Close releases the stream to a cm on another node.
func (r *Regions) Close() error { return r.reach.Close() }

func targetOf(a specs.Address) *workerv2.Target {
	return &workerv2.Target{Project: a.Project, Environment: a.Environment, Scope: a.Scope, Kind: a.Kind, Name: a.Name}
}

func addressOf(t *workerv2.Target) specs.Address {
	return specs.Address{Project: t.GetProject(), Environment: t.GetEnvironment(), Scope: t.GetScope(), Kind: t.GetKind(), Name: t.GetName()}
}

func fromWire(in map[string]*workerv2.Headers) map[string][]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string][]string, len(in))
	for name, v := range in {
		out[name] = v.GetValues()
	}
	return out
}

func toWire(in map[string][]string) map[string]*workerv2.Headers {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]*workerv2.Headers, len(in))
	held := make([]workerv2.Headers, len(in))
	i := 0
	for name, values := range in {
		held[i].Values = values
		out[name] = &held[i]
		i++
	}
	return out
}
