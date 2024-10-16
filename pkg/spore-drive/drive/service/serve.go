package service

import (
	"context"
	"errors"
	"net/http"

	"connectrpc.com/connect"
	"github.com/taubyte/tau/pkg/spore-drive/course"
	"github.com/taubyte/tau/pkg/spore-drive/drive"
	pb "github.com/taubyte/tau/pkg/spore-drive/proto/gen/drive/v1"
	pbconnect "github.com/taubyte/tau/pkg/spore-drive/proto/gen/drive/v1/drivev1connect"
)

func (s *Service) New(_ context.Context, in *connect.Request[pb.DriveRequest]) (*connect.Response[pb.Drive], error) {
	if in.Msg.GetConfig() == nil || in.Msg.GetConfig().GetId() == "" {
		return nil, errors.New("you need to provide config id")
	}

	opts := make([]drive.Option, 0)

	switch v := in.Msg.GetTau().(type) {
	case *pb.DriveRequest_Latest:
		opts = append(opts, drive.WithTauLatest())
	case *pb.DriveRequest_Version:
		opts = append(opts, drive.WithTauVersion(v.Version))
	case *pb.DriveRequest_Url:
		opts = append(opts, drive.WithTauUrl(v.Url))
	case *pb.DriveRequest_Path:
		opts = append(opts, drive.WithTauPath(v.Path))
	}

	sd, err := s.newDrive(in.Msg.GetConfig().GetId(), opts)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&pb.Drive{Id: sd.id}), nil
}

func (s *Service) Plot(ctx context.Context, in *connect.Request[pb.PlotRequest]) (*connect.Response[pb.Course], error) {
	if in.Msg.GetDrive() == nil || in.Msg.GetDrive().GetId() == "" {
		return nil, errors.New("you need to provide drive id")
	}

	d := s.getDrive(in.Msg.GetDrive().GetId())
	if d == nil {
		return nil, errors.New("drive not found")
	}

	var opts []course.Option

	if concur := in.Msg.GetConcurrency(); concur != 0 {
		opts = append(opts, course.Concurrency(int(concur)))
	}

	if sh := in.Msg.GetShapes(); sh != nil {
		opts = append(opts, course.Shapes(sh...))
	}

	ci, err := s.newCourse(d, opts)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&pb.Course{Id: ci.id}), nil
}

func (s *Service) Displace(ctx context.Context, in *connect.Request[pb.Course]) (*connect.Response[pb.Empty], error) {
	if in.Msg.GetId() == "" {
		return nil, errors.New("you need to provide course id")
	}

	ci := s.getCourse(in.Msg.GetId())
	if ci == nil {
		return nil, errors.New("course not found")
	}

	return noValReturn(ci.displace())
}

func (s *Service) Progress(ctx context.Context, in *connect.Request[pb.Course], stream *connect.ServerStream[pb.DisplacementProgress]) error {
	if in.Msg.GetId() == "" {
		return errors.New("you need to provide course id")
	}

	ci := s.getCourse(in.Msg.GetId())
	if ci == nil {
		return errors.New("course not found")
	}

	pch, err := ci.progress()
	if err != nil {
		return err
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case p := <-pch:
			if p == nil {
				//chan closed
				return nil
			}

			dp := &pb.DisplacementProgress{
				Name: p.Name(),
				Path: p.Path(),
			}
			if e := p.Error(); e != nil {
				dp.Error = e.Error()
			} else {
				dp.Progress = int32(p.Progress())
			}

			if err = stream.Send(dp); err != nil {
				return err
			}
		}
	}
}

func (s *Service) Abort(ctx context.Context, in *connect.Request[pb.Course]) (*connect.Response[pb.Empty], error) {
	if in.Msg.GetId() == "" {
		return nil, errors.New("you need to provide course id")
	}

	ci := s.getCourse(in.Msg.GetId())
	if ci == nil {
		return nil, errors.New("course not found")
	}

	ci.abort()

	return connect.NewResponse(&pb.Empty{}), nil
}

func (s *Service) Free(ctx context.Context, in *connect.Request[pb.Drive]) (*connect.Response[pb.Empty], error) {
	if in.Msg.GetId() == "" {
		return nil, errors.New("you need to provide drive id")
	}

	d := s.getDrive(in.Msg.GetId())
	if d == nil {
		return nil, errors.New("drive not found")
	}

	// free the drive, so no courses are added to it
	s.freeDrive(d.id)

	var courses []*courseInstance
	s.courseLock.Lock()
	for _, c := range s.courses {
		if c.drive == d {
			courses = append(courses, c)
		}
	}
	s.courseLock.Unlock()

	for _, c := range courses {
		c.abort()
	}

	return connect.NewResponse(&pb.Empty{}), nil
}

func (s *Service) Attach(mux *http.ServeMux) {
	mux.Handle(s.path, s.handler)
}

func Serve(ctx context.Context, resolver ConfigResolver) (*Service, error) {
	srv := &Service{
		ctx:      ctx,
		drives:   make(map[string]*driveInstance),
		courses:  make(map[string]*courseInstance),
		resolver: resolver,
	}

	srv.path, srv.handler = pbconnect.NewDriveServiceHandler(srv)

	return srv, nil
}
