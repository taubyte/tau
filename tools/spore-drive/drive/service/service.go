// Package service serves drive.v2: validate a cloud document, plan a course
// over it, apply the course, abort it.
//
// # A course is an id with a lifetime
//
// Plan registers the course it returns and nothing has to free it. It ends in
// one of three ways: Apply streams it to the end (or the client streaming it
// goes away, which aborts it), Abort ends it, or nobody applies it within
// Expiry and it is dropped. A service that stops ends every course it holds.
package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect"

	"github.com/taubyte/tau/pkg/proto/codec"
	"github.com/taubyte/tau/tools/spore-drive/config"
	"github.com/taubyte/tau/tools/spore-drive/course"
	"github.com/taubyte/tau/tools/spore-drive/drive"
	pb "github.com/taubyte/tau/tools/spore-drive/proto/gen/drive/v2"
	pbconnect "github.com/taubyte/tau/tools/spore-drive/proto/gen/drive/v2/drivev2connect"
	"github.com/taubyte/tau/utils/id"
)

// Expiry is how long a planned course waits to be applied.
const Expiry = 15 * time.Minute

type Service struct {
	pbconnect.UnimplementedDriveServiceHandler

	ctx    context.Context
	expiry time.Duration

	// spore builds the drive a course runs on: drive.New, or a fake under
	// test.
	spore func(config.Parser, ...drive.Option) (drive.Spore, error)

	lock    sync.Mutex
	courses map[string]*planned

	path    string
	handler http.Handler
}

// planned is one course between Plan and its end.
type planned struct {
	spore  drive.Spore
	course course.Course

	ctx    context.Context
	cancel context.CancelFunc
	expire *time.Timer

	applying bool
}

func Serve(ctx context.Context) (*Service, error) {
	s := &Service{
		ctx:     ctx,
		expiry:  Expiry,
		spore:   drive.New,
		courses: map[string]*planned{},
	}
	s.path, s.handler = pbconnect.NewDriveServiceHandler(s, codec.Option())
	return s, nil
}

func (s *Service) Attach(mux *http.ServeMux) {
	mux.Handle(s.path, s.handler)
}

func (s *Service) Validate(_ context.Context, in *connect.Request[pb.ValidateRequest]) (*connect.Response[pb.ValidateResponse], error) {
	out := &pb.ValidateResponse{}
	tree, p, err := load(in.Msg.GetDocument())
	if err != nil {
		out.Errors = []string{err.Error()}
		return connect.NewResponse(out), nil
	}

	if err := complete(p); err != nil {
		out.Errors = append(out.Errors, err.Error())
	}
	if out.Document, err = dump(tree); err != nil {
		return nil, err
	}

	sp, err := s.spore(p)
	if err != nil {
		out.Errors = append(out.Errors, err.Error())
		return connect.NewResponse(out), nil
	}
	_, ignored, errs := hostsOf(p, sp)
	out.Errors = append(out.Errors, errs...)
	out.Ignored = append(unread(in.Msg.GetDocument()), ignored...)
	return connect.NewResponse(out), nil
}

// hostsOf plans every host, by name.
func hostsOf(p config.Parser, sp drive.Spore) (hosts []*pb.Host, ignored, errs []string) {
	for _, name := range sorted(p.Hosts().List()) {
		nodes, err := sp.Nodes(name)
		if err != nil {
			errs = append(errs, fmt.Sprintf("host %s: %s", name, err))
			continue
		}
		h := &pb.Host{Name: name, Ignored: nodes.Ignored}
		for _, n := range nodes.Nodes {
			h.Nodes = append(h.Nodes, &pb.Node{Name: n.Name, Port: int32(n.Port), Services: n.Services})
		}
		hosts = append(hosts, h)
		ignored = append(ignored, nodes.Ignored...)
	}
	return
}

func tauOf(in *pb.PlanRequest) []drive.Option {
	switch v := in.GetTau().(type) {
	case *pb.PlanRequest_Latest:
		if v.Latest {
			return []drive.Option{drive.WithTauLatest()}
		}
	case *pb.PlanRequest_Version:
		return []drive.Option{drive.WithTauVersion(v.Version)}
	case *pb.PlanRequest_Url:
		return []drive.Option{drive.WithTauUrl(v.Url)}
	case *pb.PlanRequest_Path:
		return []drive.Option{drive.WithTauPath(v.Path)}
	}
	return nil
}

func (s *Service) Plan(_ context.Context, in *connect.Request[pb.PlanRequest]) (*connect.Response[pb.Course], error) {
	_, p, err := load(in.Msg.GetDocument())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	if gaps := missing(p); len(gaps) > 0 {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("the document is incomplete, missing %s; Validate generates them", strings.Join(gaps, ", ")))
	}

	sp, err := s.spore(p, append(tauOf(in.Msg), drive.WithRoot(in.Msg.GetRoot()))...)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	hosts, _, errs := hostsOf(p, sp)
	if len(errs) > 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New(strings.Join(errs, "; ")))
	}

	shapes := in.Msg.GetShapes()
	if len(shapes) == 0 {
		shapes = sorted(p.Shapes().List())
	}
	opts := []course.Option{course.Shapes(shapes...)}
	if c := in.Msg.GetConcurrency(); c > 0 {
		opts = append(opts, course.Concurrency(int(c)))
	}
	c, err := course.New(sp.Network(), opts...)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	out := &pb.Course{Id: id.Generate(), Hosts: hosts}
	for _, hypha := range c.Hyphae() {
		hop := &pb.Hop{Name: hypha.Name, Shapes: hypha.Shapes}
		for _, h := range hypha.Subnet.Hosts() {
			hop.Hosts = append(hop.Hosts, h.Name())
		}
		out.Hops = append(out.Hops, hop)
	}

	s.register(out.Id, sp, c)
	return connect.NewResponse(out), nil
}

func (s *Service) register(courseID string, sp drive.Spore, c course.Course) {
	ctx, cancel := context.WithCancel(s.ctx)
	pl := &planned{spore: sp, course: c, ctx: ctx, cancel: cancel}

	s.lock.Lock()
	defer s.lock.Unlock()
	s.courses[courseID] = pl
	pl.expire = time.AfterFunc(s.expiry, func() {
		s.lock.Lock()
		defer s.lock.Unlock()
		if !pl.applying {
			s.drop(courseID)
		}
	})
}

// drop ends a course. The caller holds the lock.
func (s *Service) drop(courseID string) {
	if pl, ok := s.courses[courseID]; ok {
		pl.expire.Stop()
		pl.cancel()
		delete(s.courses, courseID)
	}
}

func (s *Service) end(courseID string) {
	s.lock.Lock()
	defer s.lock.Unlock()
	s.drop(courseID)
}

var errNoCourse = errors.New("no such course: it ended, expired, or was never planned")

func (s *Service) Apply(ctx context.Context, in *connect.Request[pb.ApplyRequest], stream *connect.ServerStream[pb.Progress]) error {
	courseID := in.Msg.GetCourse()

	s.lock.Lock()
	pl, ok := s.courses[courseID]
	if ok && pl.applying {
		s.lock.Unlock()
		return connect.NewError(connect.CodeFailedPrecondition, errors.New("the course is already being applied"))
	}
	if ok {
		pl.applying = true
		pl.expire.Stop()
	}
	s.lock.Unlock()
	if !ok {
		return connect.NewError(connect.CodeNotFound, errNoCourse)
	}

	// THE COURSE ENDS WITH THE STREAM, however the stream ends.
	defer s.end(courseID)

	progress := pl.spore.Displace(pl.ctx, pl.course)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case p, more := <-progress:
			if !more {
				return nil
			}
			msg := &pb.Progress{Hop: p.Hop(), Host: p.Host(), Step: p.Name(), Progress: int32(p.Progress())}
			if err := p.Error(); err != nil {
				msg.Error = err.Error()
			}
			if err := stream.Send(msg); err != nil {
				return err
			}
		}
	}
}

func (s *Service) Abort(_ context.Context, in *connect.Request[pb.AbortRequest]) (*connect.Response[pb.AbortResponse], error) {
	s.lock.Lock()
	defer s.lock.Unlock()
	if _, ok := s.courses[in.Msg.GetCourse()]; !ok {
		return nil, connect.NewError(connect.CodeNotFound, errNoCourse)
	}
	s.drop(in.Msg.GetCourse())
	return connect.NewResponse(&pb.AbortResponse{}), nil
}
