package service

import (
	"context"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"connectrpc.com/connect"
	"gotest.tools/v3/assert"

	"github.com/taubyte/tau/tools/spore-drive/config"
	"github.com/taubyte/tau/tools/spore-drive/course"
	"github.com/taubyte/tau/tools/spore-drive/drive"
	pb "github.com/taubyte/tau/tools/spore-drive/proto/gen/drive/v2"
	pbconnect "github.com/taubyte/tau/tools/spore-drive/proto/gen/drive/v2/drivev2connect"
)

// acdz is a cloud config with the shape of a real one: one host named by a
// domain, a single shape running every v1 service, a main and a lite port, a
// location, and every key in its own file. The keys are dummies.
func acdz(t *testing.T) *pb.Document {
	t.Helper()
	doc := &pb.Document{Files: map[string][]byte{}}
	root := "testdata/acdz"
	err := filepath.WalkDir(root, func(name string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		content, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, name)
		doc.Files[filepath.ToSlash(rel)] = content
		return err
	})
	assert.NilError(t, err)
	return doc
}

func without(doc *pb.Document, names ...string) *pb.Document {
	out := &pb.Document{Files: map[string][]byte{}}
	for k, v := range doc.Files {
		if !slices.Contains(names, k) {
			out.Files[k] = v
		}
	}
	return out
}

func with(doc *pb.Document, name string, content string) *pb.Document {
	out := without(doc)
	out.Files[name] = []byte(content)
	return out
}

// fake runs a real drive for everything but the deploy itself.
type fake struct {
	drive.Spore
	displace func(ctx context.Context) <-chan drive.Progress
}

func (f *fake) Displace(ctx context.Context, _ course.Course) <-chan drive.Progress {
	return f.displace(ctx)
}

type step struct {
	hop, host, name string
	progress        int
	err             error
}

func (s step) Path() string   { return "/" + s.hop + "/" + s.host + "/" + s.name }
func (s step) Hop() string    { return s.hop }
func (s step) Host() string   { return s.host }
func (s step) Name() string   { return s.name }
func (s step) Progress() int  { return s.progress }
func (s step) Error() error   { return s.err }
func (s step) String() string { return s.Path() }

func serve(t *testing.T, displace func(ctx context.Context) <-chan drive.Progress) (*Service, pbconnect.DriveServiceClient) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	s, err := Serve(ctx)
	assert.NilError(t, err)
	if displace != nil {
		s.spore = func(p config.Parser, opts ...drive.Option) (drive.Spore, error) {
			sp, err := drive.New(p, opts...)
			if err != nil {
				return nil, err
			}
			return &fake{Spore: sp, displace: displace}, nil
		}
	}

	mux := http.NewServeMux()
	s.Attach(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return s, pbconnect.NewDriveServiceClient(srv.Client(), srv.URL)
}

func code(err error) connect.Code { return connect.CodeOf(err) }

// TestARealConfigShapeValidatesUnchangedAndPlans: the on-disk layout an
// operator already has is the document, read as it is.
func TestARealConfigShapeValidatesUnchangedAndPlans(t *testing.T) {
	_, c := serve(t, nil)
	doc := acdz(t)

	res, err := c.Validate(context.Background(), connect.NewRequest(&pb.ValidateRequest{Document: doc}))
	assert.NilError(t, err)
	assert.DeepEqual(t, res.Msg.Errors, []string(nil))
	assert.Assert(t, slices.Contains(res.Msg.Ignored, "hosts.node1.example.com.location (a v2 node reads no location)"), "%v", res.Msg.Ignored)
	assert.Assert(t, slices.Contains(res.Msg.Ignored, "shapes.all.ports.lite"), "%v", res.Msg.Ignored)

	// NOTHING WAS MISSING, SO NOTHING CHANGED: every file comes back byte for
	// byte.
	assert.Equal(t, len(res.Msg.Document.Files), len(doc.Files))
	for name, content := range doc.Files {
		assert.Equal(t, string(res.Msg.Document.Files[name]), string(content), name)
	}

	plan, err := c.Plan(context.Background(), connect.NewRequest(&pb.PlanRequest{Document: doc}))
	assert.NilError(t, err)
	assert.Assert(t, plan.Msg.Id != "")
	assert.Equal(t, len(plan.Msg.Hops), 1)
	assert.DeepEqual(t, plan.Msg.Hops[0].Shapes, []string{"all"})
	assert.DeepEqual(t, plan.Msg.Hops[0].Hosts, []string{"node1.example.com"})
	assert.Equal(t, len(plan.Msg.Hosts), 1)
	h := plan.Msg.Hosts[0]
	assert.Equal(t, h.Name, "node1.example.com")
	// patrick and monkey are both here, so build-frontend is split off.
	assert.Equal(t, len(h.Nodes), 2)
	assert.Equal(t, h.Nodes[0].Name, "node")
	assert.Equal(t, h.Nodes[0].Port, int32(4242))
	assert.Equal(t, h.Nodes[1].Name, "build-frontend")
	assert.Equal(t, h.Nodes[1].Port, int32(4243))
}

// TestValidateGeneratesWhatADocumentLeavesOut, and Plan refuses until it has.
func TestValidateGeneratesWhatADocumentLeavesOut(t *testing.T) {
	_, c := serve(t, nil)
	doc := acdz(t)
	hosts := `node1.example.com:
    addr:
        - 203.0.113.10/32
    ssh:
        addr: 203.0.113.10
        port: 22
        auth:
            - main
    shapes:
        all: {}
`
	bare := with(without(doc, "keys/swarm.key", "keys/dv_private.key", "keys/dv_public.key"), "hosts.yaml", hosts)

	_, err := c.Plan(context.Background(), connect.NewRequest(&pb.PlanRequest{Document: bare}))
	assert.Equal(t, code(err), connect.CodeFailedPrecondition)
	assert.ErrorContains(t, err, "the swarm key")
	assert.ErrorContains(t, err, "the domain validation keys")
	assert.ErrorContains(t, err, "the key of host node1.example.com for shape all")

	res, err := c.Validate(context.Background(), connect.NewRequest(&pb.ValidateRequest{Document: bare}))
	assert.NilError(t, err)
	assert.DeepEqual(t, res.Msg.Errors, []string(nil))
	for _, name := range []string{"keys/swarm.key", "keys/dv_private.key", "keys/dv_public.key"} {
		assert.Assert(t, len(res.Msg.Document.Files[name]) > 0, name)
	}
	assert.Assert(t, string(res.Msg.Document.Files["hosts.yaml"]) != hosts, "no node key was written")

	_, err = c.Plan(context.Background(), connect.NewRequest(&pb.PlanRequest{Document: res.Msg.Document}))
	assert.NilError(t, err)
}

func TestWhatADocumentCannotBe(t *testing.T) {
	_, c := serve(t, nil)
	validate := func(doc *pb.Document) []string {
		res, err := c.Validate(context.Background(), connect.NewRequest(&pb.ValidateRequest{Document: doc}))
		assert.NilError(t, err)
		return res.Msg.Errors
	}

	assert.DeepEqual(t, validate(nil), []string{"an empty document"})
	for _, name := range []string{"../escape.yaml", "/etc/passwd", ".", ""} {
		errs := validate(&pb.Document{Files: map[string][]byte{name: []byte("x")}})
		assert.Equal(t, len(errs), 1, name)
		assert.ErrorContains(t, errors.New(errs[0]), "is not a path inside the config")
	}

	broken := with(acdz(t), "hosts.yaml", "node1.example.com:\n    addr:\n        - nonsense\n    shapes:\n        all: {}\n")
	errs := validate(broken)
	assert.Assert(t, len(errs) > 0)

	_, err := c.Plan(context.Background(), connect.NewRequest(&pb.PlanRequest{}))
	assert.Equal(t, code(err), connect.CodeInvalidArgument)

	_, err = c.Plan(context.Background(), connect.NewRequest(&pb.PlanRequest{
		Document: acdz(t),
		Tau:      &pb.PlanRequest_Path{Path: "/nonexistent/tau"},
	}))
	assert.Equal(t, code(err), connect.CodeInvalidArgument)

	shapes := with(acdz(t), "shapes.yaml", "all:\n    services:\n        - gatewya\n    ports:\n        main: 4242\n")
	_, err = c.Plan(context.Background(), connect.NewRequest(&pb.PlanRequest{Document: shapes}))
	assert.Equal(t, code(err), connect.CodeInvalidArgument)
	assert.ErrorContains(t, err, "gatewya")
}

// TestDocumentsNoV2NodeReadsAreReportedNotRefused.
func TestDocumentsNoV2NodeReadsAreReportedNotRefused(t *testing.T) {
	_, c := serve(t, nil)
	doc := with(acdz(t), "accounts.yaml", "session-ttl: 168h\n")
	res, err := c.Validate(context.Background(), connect.NewRequest(&pb.ValidateRequest{Document: doc}))
	assert.NilError(t, err)
	assert.DeepEqual(t, res.Msg.Errors, []string(nil))
	assert.Assert(t, slices.Contains(res.Msg.Ignored, "accounts.yaml (no v2 node reads it)"))
}

func TestPlanTakesItsShapesAndConcurrency(t *testing.T) {
	_, c := serve(t, nil)
	plan, err := c.Plan(context.Background(), connect.NewRequest(&pb.PlanRequest{
		Document: acdz(t), Shapes: []string{"all", "all"}, Concurrency: 2,
	}))
	assert.NilError(t, err)
	assert.Equal(t, len(plan.Msg.Hops), 2)
}

func TestTauSources(t *testing.T) {
	assert.Equal(t, len(tauOf(&pb.PlanRequest{})), 0)
	assert.Equal(t, len(tauOf(&pb.PlanRequest{Tau: &pb.PlanRequest_Latest{Latest: false}})), 0)
	for _, in := range []*pb.PlanRequest{
		{Tau: &pb.PlanRequest_Latest{Latest: true}},
		{Tau: &pb.PlanRequest_Version{Version: "1.0.0"}},
		{Tau: &pb.PlanRequest_Url{Url: "http://localhost/tau"}},
		{Tau: &pb.PlanRequest_Path{Path: "/tau"}},
	} {
		assert.Equal(t, len(tauOf(in)), 1)
	}
}

func plan(t *testing.T, c pbconnect.DriveServiceClient) string {
	t.Helper()
	res, err := c.Plan(context.Background(), connect.NewRequest(&pb.PlanRequest{Document: acdz(t)}))
	assert.NilError(t, err)
	return res.Msg.Id
}

// TestApplyStreamsTheCourseToItsEndAndTheCourseEndsWithIt.
func TestApplyStreamsTheCourseToItsEndAndTheCourseEndsWithIt(t *testing.T) {
	_, c := serve(t, func(context.Context) <-chan drive.Progress {
		ch := make(chan drive.Progress, 2)
		ch <- step{hop: "h", host: "node1.example.com", name: "upload", progress: 50}
		ch <- step{hop: "h", host: "node1.example.com", name: "upload", progress: 100, err: errors.New("disk full")}
		close(ch)
		return ch
	})
	id := plan(t, c)

	stream, err := c.Apply(context.Background(), connect.NewRequest(&pb.ApplyRequest{Course: id}))
	assert.NilError(t, err)
	var got []*pb.Progress
	for stream.Receive() {
		got = append(got, stream.Msg())
	}
	assert.NilError(t, stream.Err())
	assert.Equal(t, len(got), 2)
	assert.Equal(t, got[0].Host, "node1.example.com")
	assert.Equal(t, got[0].Step, "upload")
	assert.Equal(t, got[0].Progress, int32(50))
	assert.Equal(t, got[1].Error, "disk full")

	// THE COURSE ENDED WITH ITS STREAM.
	_, err = c.Abort(context.Background(), connect.NewRequest(&pb.AbortRequest{Course: id}))
	assert.Equal(t, code(err), connect.CodeNotFound)
	stream, err = c.Apply(context.Background(), connect.NewRequest(&pb.ApplyRequest{Course: id}))
	assert.NilError(t, err)
	assert.Assert(t, !stream.Receive())
	assert.Equal(t, code(stream.Err()), connect.CodeNotFound)
}

// TestACourseIsAppliedOnceAndAbortStopsIt.
func TestACourseIsAppliedOnceAndAbortStopsIt(t *testing.T) {
	started := make(chan struct{})
	_, c := serve(t, func(ctx context.Context) <-chan drive.Progress {
		ch := make(chan drive.Progress)
		go func() {
			defer close(ch)
			close(started)
			<-ctx.Done()
		}()
		return ch
	})
	id := plan(t, c)

	// THE STREAM OPENS WITH ITS FIRST MESSAGE, so it is opened beside the test.
	done := make(chan error, 1)
	go func() {
		stream, err := c.Apply(context.Background(), connect.NewRequest(&pb.ApplyRequest{Course: id}))
		if err != nil {
			done <- err
			return
		}
		for stream.Receive() {
		}
		done <- stream.Err()
	}()
	<-started

	again, err := c.Apply(context.Background(), connect.NewRequest(&pb.ApplyRequest{Course: id}))
	assert.NilError(t, err)
	assert.Assert(t, !again.Receive())
	assert.Equal(t, code(again.Err()), connect.CodeFailedPrecondition)

	_, err = c.Abort(context.Background(), connect.NewRequest(&pb.AbortRequest{Course: id}))
	assert.NilError(t, err)
	assert.NilError(t, <-done, "an aborted course ends its stream")
}

// TestAClientThatGoesAwayAbortsItsCourse.
func TestAClientThatGoesAwayAbortsItsCourse(t *testing.T) {
	started, stopped := make(chan struct{}), make(chan struct{})
	_, c := serve(t, func(ctx context.Context) <-chan drive.Progress {
		ch := make(chan drive.Progress)
		go func() {
			close(started)
			<-ctx.Done()
			close(stopped)
		}()
		return ch
	})
	id := plan(t, c)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		stream, err := c.Apply(ctx, connect.NewRequest(&pb.ApplyRequest{Course: id}))
		if err != nil {
			return
		}
		for stream.Receive() {
		}
	}()
	<-started
	cancel()

	select {
	case <-stopped:
	case <-time.After(10 * time.Second):
		t.Fatal("the deploy went on after its client left")
	}
}

// TestAnUnappliedCourseExpires.
func TestAnUnappliedCourseExpires(t *testing.T) {
	s, c := serve(t, nil)
	s.expiry = 10 * time.Millisecond
	id := plan(t, c)

	time.Sleep(200 * time.Millisecond)
	_, err := c.Abort(context.Background(), connect.NewRequest(&pb.AbortRequest{Course: id}))
	assert.Equal(t, code(err), connect.CodeNotFound)
}

func TestAbortingNothingSaysSo(t *testing.T) {
	_, c := serve(t, nil)
	_, err := c.Abort(context.Background(), connect.NewRequest(&pb.AbortRequest{Course: "nope"}))
	assert.Equal(t, code(err), connect.CodeNotFound)
}
