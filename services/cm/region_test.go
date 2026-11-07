package cm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sync"
	"testing"

	"connectrpc.com/connect"
	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/core/service"
	"github.com/taubyte/tau/p2p/discovery"
	"github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/pkg/invoke"
	regionv2 "github.com/taubyte/tau/pkg/proto/gen/tau/region/v2"
	workerv2 "github.com/taubyte/tau/pkg/proto/gen/tau/worker/v2"
	"github.com/taubyte/tau/pkg/register"
	"github.com/taubyte/tau/pkg/specs"
	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
)

// edge is a route answered in both regions, in front of a function in /eu that runs
// in region-1 alone, published as a cloud of the two cuts it: region-2 is told
// region-1 runs it.
func edge(t *testing.T) *register.Register {
	t.Helper()
	fs := afero.NewMemMapFs()
	for path, body := range map[string]string{
		"/project.yaml": "id: QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR\nname: shop\n" +
			"regions:\n    topology: active\n    in: [region-1, region-2]\nenvironments:\n    prod:\n        branches: [\"*\"]\n",
		"/namespace/eu/meta.yaml":         "id: QmTXGKe63GYuKA54yerPQmGyEtFjhUF5p1g2hahTpm6pkA\nregions:\n    in: [region-1]\n",
		"/namespace/eu/function/api.yaml": "id: QmPjgiv816ygGgxYKEFHhDoabVar1Cv6CAQvEa8aFubUtG\npackage:\n    name: api_code\n    namespace: /\nexecution:\n    call: handle\n    timeout: 15s\n    memory: 32MB\n",
		"/package/api_code.yaml":          "id: Qmf59hm7Wwxc4ZCNbqyF9N6zqKS1fVXB6tugiwHgEHpk2J\ntype: wasm\nsource: services/api\noutput: x.wasm\nbuild:\n    steps:\n        - name: build\n          image: rust:1.83\n          commands: [cargo build]\n",
		"/zone/example_com.yaml":          "id: QmXZy3vTBqdo6va5ouo2muAh8kHF4j8t8HWtpgyKoBVn7y\nfqdn: example.com\n",
		"/gateway/www.yaml": "id: QmWhQewj3uuPM9f3qodPJTPDUXE9BvG33zkyewVurWGHiU\nhost:\n    zone:\n        in:\n            name: example_com\n        name: www\n" +
			"route:\n    path: /\n    handler:\n        function:\n            namespace: /eu\n            name: api\n",
	} {
		require.NoError(t, afero.WriteFile(fs, path, []byte(body), 0o644))
	}
	c, err := schema.New(schema.WithVirtual(fs, "/"), schema.WithBranch("main"))
	require.NoError(t, err)
	obj, _, err := c.Compile(context.Background())
	require.NoError(t, err)
	flat := obj.Flat()
	views, findings, err := schema.Views(flat, schema.WithRegions(schema.Preview("region-1", "region-2")))
	require.NoError(t, err, "%v", findings)

	whole, err := json.Marshal(flat)
	require.NoError(t, err)
	pub := register.Publication{Project: "shop", Environment: "prod", Revision: 1, Compiled: whole}
	for _, region := range []string{"region-1", "region-2"} {
		b, err := json.Marshal(views[region].Object)
		require.NoError(t, err)
		v := register.View{Destination: region, Compiled: b}
		if region == "region-2" {
			v.Remote = []register.Remote{{Kind: specs.KindFunction, Scope: "eu", Name: "api", In: []string{"region-1"}}}
		}
		pub.Views = append(pub.Views, v)
	}
	r, err := register.New(register.WithStore(register.NewMemStore()))
	require.NoError(t, err)
	_, err = r.Publish(t.Context(), pub)
	require.NoError(t, err)
	return r
}

// origins is a region reading r, and workers that answer with what they were asked.
type origins struct {
	r      *register.Register
	region string
	err    error

	mu      sync.Mutex
	invoked []specs.Address
}

func (o *origins) Resolve(ctx context.Context, project, environment string) (register.Pointer, error) {
	return register.InRegion(o.r, o.region).Resolve(ctx, project, environment)
}

func (o *origins) Object(ctx context.Context, id register.ObjectID) ([]byte, error) {
	return o.r.Object(ctx, id)
}

func (o *origins) Routing(ctx context.Context, project, environment string) (register.Routing, error) {
	return o.r.Routing(ctx, project, environment, o.region)
}

func (o *origins) Invoke(_ context.Context, at specs.Address, req *invoke.Request) (*invoke.Response, error) {
	o.mu.Lock()
	o.invoked = append(o.invoked, at)
	o.mu.Unlock()
	if o.err != nil {
		return nil, o.err
	}
	return &invoke.Response{Status: http.StatusCreated, Headers: map[string][]string{"X-Echo": req.Headers["X-Probe"]}, Body: []byte(req.Method + " " + req.Path)}, nil
}

var (
	www = specs.Address{Project: "shop", Environment: "prod", Kind: specs.KindGateway, Name: "www"}
	api = specs.Address{Project: "shop", Environment: "prod", Scope: []string{"eu"}, Kind: specs.KindFunction, Name: "api"}
)

func originCall(release register.ObjectID, route, handler specs.Address) *regionv2.Carried {
	return &regionv2.Carried{Release: string(release), Id: "call-1", Call: &regionv2.Carried_Origin{Origin: &regionv2.Origin{
		Route: targetOf(route),
		Request: &workerv2.InvokeRequest{
			Target: targetOf(handler), Method: http.MethodGet, Path: "/a",
			Headers: toWire(map[string][]string{"X-Probe": {"one"}}),
		},
	}}}
}

func releaseOf(t *testing.T, r *register.Register) register.ObjectID {
	t.Helper()
	p, err := r.Resolve(t.Context(), "shop", "prod")
	require.NoError(t, err)
	return p.Object
}

func TestARegionAnswersWhatItsOwnReleaseRoutesToIt(t *testing.T) {
	r := edge(t)
	release := releaseOf(t, r)
	o := &origins{r: r, region: "region-1"}
	one, err := newReceiving("region-1", o)
	require.NoError(t, err)

	answered, err := one.Receive(t.Context(), "region-2", originCall(release, www, api))
	require.NoError(t, err)
	require.Equal(t, "region-1", answered.GetRegion())
	require.EqualValues(t, http.StatusCreated, answered.GetResponse().GetStatus())
	require.Equal(t, "GET /a", string(answered.GetResponse().GetBody()))
	require.Equal(t, []string{"one"}, fromWire(answered.GetResponse().GetHeaders())["X-Echo"])
	require.Equal(t, []specs.Address{api}, o.invoked)

	unknown := www
	unknown.Name = "nope"
	project := api
	project.Project = "blog"
	notGateway := www
	notGateway.Kind = specs.KindFunction
	elsewhere := api
	elsewhere.Scope = nil
	renamed := api
	renamed.Name = "api2"

	type refusal struct {
		name string
		from string
		call *regionv2.Carried
		want error
	}
	for _, tc := range []refusal{
		{"nothing carried", "region-2", &regionv2.Carried{Release: string(release)}, ErrNoCall},
		{"from itself", "region-1", originCall(release, www, api), ErrNoCall},
		{"a route that is no gateway", "region-2", originCall(release, notGateway, api), ErrNoCall},
		{"two deployments", "region-2", originCall(release, www, project), ErrNoCall},
		{"another release", "region-2", originCall("QmSomethingElse", www, api), ErrOtherRelease},
		{"a route that is not there", "region-2", originCall(release, unknown, api), ErrNoSuchRoute},
		{"a handler the route does not name", "region-2", originCall(release, www, renamed), ErrNoSuchRoute},
		{"a handler in another namespace", "region-2", originCall(release, www, elsewhere), ErrNoSuchRoute},
	} {
		_, err := one.Receive(t.Context(), tc.from, tc.call)
		require.ErrorIs(t, err, tc.want, tc.name)
	}
	require.Len(t, o.invoked, 1, "nothing refused ran")

	// REGION-2 IS ROUTED TO, AND DOES NOT RUN IT: a release that routes there is
	// refused there by name.
	two, err := newReceiving("region-2", &origins{r: r, region: "region-2"})
	require.NoError(t, err)
	_, err = two.Receive(t.Context(), "region-1", originCall(release, www, api))
	require.ErrorIs(t, err, ErrNotRunHere)

	// What the region could not read or run is said as it came.
	_, err = newReceivingOrFail(t, "region-3", r).Receive(t.Context(), "region-2", originCall(release, www, api))
	require.ErrorIs(t, err, register.ErrNoView)
	failing := &origins{r: r, region: "region-1", err: errors.New("worker gone")}
	_, err = newReceivingOrFail(t, "region-1", r, failing).Receive(t.Context(), "region-2", originCall(release, www, api))
	require.ErrorContains(t, err, "worker gone")
}

func newReceivingOrFail(t *testing.T, region string, r *register.Register, with ...*origins) *receiving {
	t.Helper()
	o := &origins{r: r, region: region}
	if len(with) > 0 {
		o = with[0]
	}
	rc, err := newReceiving(region, o)
	require.NoError(t, err)
	return rc
}

// links is the regions of one cloud, as a test links them, and link one end.
type links struct {
	mu  sync.Mutex
	at  map[string]Receiver
	cut map[string]bool
}

type link struct {
	*links
	region string
}

func newLink() *links { return &links{at: map[string]Receiver{}, cut: map[string]bool{}} }

func (l *links) end(region string) link { return link{links: l, region: region} }

func (l link) Region() string { return l.region }

func (l link) Carry(ctx context.Context, region string, c *regionv2.Carried) (*regionv2.Answered, error) {
	l.mu.Lock()
	r, ok := l.at[region]
	down := l.cut[region]
	l.mu.Unlock()
	if !ok || down {
		return nil, ErrUnreachable
	}
	return r.Receive(ctx, l.region, c)
}

func (l link) Attach(r Receiver) io.Closer {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.at[l.region] = r
	return detached{l}
}

type detached struct{ l link }

func (d detached) Close() error {
	d.l.mu.Lock()
	defer d.l.mu.Unlock()
	delete(d.l.at, d.l.region)
	return nil
}

// refusing is a region that is reached and refuses.
type refusing struct{}

func (refusing) Receive(context.Context, string, *regionv2.Carried) (*regionv2.Answered, error) {
	return nil, ErrOtherRelease
}

// answers is a region that is reached and answers as the region it names.
type answers string

func (a answers) Receive(context.Context, string, *regionv2.Carried) (*regionv2.Answered, error) {
	return &regionv2.Answered{Region: string(a)}, nil
}

func TestACallGoesToTheFirstRegionReachedAndStopsAtARefusal(t *testing.T) {
	ctx := t.Context()
	l := newLink()
	l.at["region-1"], l.at["region-3"] = answers("region-1"), answers("region-3")
	from := l.end("region-2")

	_, err := carry(ctx, nil, []string{"region-1"}, &regionv2.Carried{})
	require.ErrorIs(t, err, ErrNoLink)
	_, err = carry(ctx, from, nil, &regionv2.Carried{})
	require.ErrorIs(t, err, ErrNoRegion)

	got, err := carry(ctx, from, []string{"region-1", "region-3"}, &regionv2.Carried{})
	require.NoError(t, err)
	require.Equal(t, "region-1", got.GetRegion())

	l.cut["region-1"] = true
	got, err = carry(ctx, from, []string{"region-1", "region-3"}, &regionv2.Carried{})
	require.NoError(t, err)
	require.Equal(t, "region-3", got.GetRegion(), "an unreachable region moves the call on")

	l.at["region-1"], l.cut["region-1"] = refusing{}, false
	_, err = carry(ctx, from, []string{"region-1", "region-3"}, &regionv2.Carried{})
	require.ErrorIs(t, err, ErrOtherRelease, "a region reached that refuses ends it")

	_, err = carry(ctx, from, []string{"region-9", "region-8"}, &regionv2.Carried{})
	require.ErrorIs(t, err, ErrUnreachable)
}

func TestARefusalCrossesTheWireAsItsCode(t *testing.T) {
	type wired struct {
		err  error
		code connect.Code
	}
	for _, tc := range []wired{
		{ErrNoCall, connect.CodeInvalidArgument},
		{ErrNoRegion, connect.CodeInvalidArgument},
		{ErrNoSuchRoute, connect.CodePermissionDenied},
		{ErrNotRunHere, connect.CodeNotFound},
		{register.ErrNotPublished, connect.CodeNotFound},
		{register.ErrNoView, connect.CodeNotFound},
		{ErrOtherRelease, connect.CodeFailedPrecondition},
		{ErrNoLink, connect.CodeFailedPrecondition},
		{ErrUnreachable, connect.CodeUnavailable},
		{errors.New("anything else"), connect.CodeUnavailable},
	} {
		require.Equal(t, tc.code, connect.CodeOf(regionWire(tc.err)), "%v", tc.err)
	}
}

// region is an instance in a region, with what runs beside cm.
type region struct {
	hosting
	name string
}

func (r region) Region() string { return r.name }

// finding finds the nodes it names.
type finding []libp2ppeer.ID

func (f finding) Find(protocol.ID) discovery.Resolver { return discovery.Static(f...) }

func inRegion(t *testing.T, name string, running ...service.Running) region {
	t.Helper()
	return region{hosting: hosting{instance: newInstance(t, t.TempDir(), quick), running: running}, name: name}
}

// A GATEWAY'S CALL CROSSES as the contract says: to its region's cm, wherever that
// runs, then over the link to the region that runs the handler, which checks it.
func TestARouteReachesAHandlerAnotherRegionRuns(t *testing.T) {
	ctx := t.Context()
	r := edge(t)
	l := newLink()

	// region-1 runs the handler; its cm answers what the link carries there.
	runs := &origins{r: r, region: "region-1"}
	one := New()
	t.Cleanup(func() { one.Close() })
	require.NoError(t, one.Start(ctx, inRegion(t, "region-1", service.Running{Service: l.end("region-1")}, service.Running{Service: runs})))

	// region-2's cm carries for its region, from another node of it.
	cmNode := inRegion(t, "region-2", service.Running{Service: l.end("region-2")})
	two := New()
	t.Cleanup(func() { two.Close() })
	require.NoError(t, two.Start(ctx, cmNode))

	gateway := inRegion(t, "region-2")
	require.NoError(t, peer.LinkAllPeers())
	require.NoError(t, gateway.Host().Connect(ctx, libp2ppeer.AddrInfo{ID: cmNode.ID(), Addrs: cmNode.Host().Addrs()}))
	gateway.running = []service.Running{{Service: finding{cmNode.ID()}}}

	regions := RegionsOn(gateway, &origins{r: r, region: "region-2"})
	t.Cleanup(func() { regions.Close() })
	res, from, err := regions.Origin(ctx, www, api, &invoke.Request{Method: http.MethodGet, Path: "/a", Headers: map[string][]string{"X-Probe": {"one"}}})
	require.NoError(t, err)
	require.Equal(t, "region-1", from)
	require.Equal(t, http.StatusCreated, res.Status)
	require.Equal(t, "GET /a", string(res.Body))
	require.Equal(t, []string{"one"}, res.Headers["X-Echo"])
	require.Equal(t, []specs.Address{api}, runs.invoked)

	// The cm beside a gateway is reached without a stream.
	beside := cmNode
	beside.running = append(beside.running, service.Running{Name: Name, Service: two})
	local := RegionsOn(beside, &origins{r: r, region: "region-2"})
	_, from, err = local.Origin(ctx, www, api, &invoke.Request{})
	require.NoError(t, err)
	require.Equal(t, "region-1", from)

	// A handler the routing names no region for is not carried at all.
	_, _, err = regions.Origin(ctx, www, specs.Address{Project: "shop", Environment: "prod", Kind: specs.KindFunction, Name: "nope"}, &invoke.Request{})
	require.ErrorIs(t, err, ErrNoRegion)
	_, _, err = regions.Origin(ctx, www, specs.Address{Project: "blog", Environment: "prod"}, &invoke.Request{})
	require.ErrorIs(t, err, register.ErrNotPublished)

	// A REFUSAL CROSSES BACK BY CODE: region-1 has moved on to another release.
	l.mu.Lock()
	l.at["region-1"] = refusing{}
	l.mu.Unlock()
	_, _, err = regions.Origin(ctx, www, api, &invoke.Request{})
	require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))

	// A cloud whose regions are not linked carries nothing.
	alone := New()
	t.Cleanup(func() { alone.Close() })
	lone := inRegion(t, "region-2")
	require.NoError(t, alone.Start(ctx, lone))
	lone.running = []service.Running{{Name: Name, Service: alone}}
	_, _, err = RegionsOn(lone, &origins{r: r, region: "region-2"}).Origin(ctx, www, api, &invoke.Request{})
	require.ErrorIs(t, err, ErrNoLink)

	// No cm in the region at all.
	_, _, err = RegionsOn(inRegion(t, "region-2"), &origins{r: r, region: "region-2"}).Origin(ctx, www, api, &invoke.Request{})
	require.ErrorIs(t, err, service.ErrNoneRuns)
}

func TestAnEndOfTheLinkSpeaksForItsNodesRegion(t *testing.T) {
	err := New().Start(t.Context(), inRegion(t, "region-1", service.Running{Service: newLink().end("region-2")}))
	require.ErrorContains(t, err, `serves region "region-1", and its end of the link speaks for "region-2"`)
}
