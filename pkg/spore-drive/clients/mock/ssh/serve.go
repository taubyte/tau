package ssh

import (
	"context"
	"errors"
	"net/http"
	"sync"

	"connectrpc.com/connect"
	"github.com/moby/moby/pkg/namesgenerator"
	pb "github.com/taubyte/tau/pkg/spore-drive/proto/gen/mock/v1"
	pbconnect "github.com/taubyte/tau/pkg/spore-drive/proto/gen/mock/v1/mockv1connect"
)

type Service struct {
	pbconnect.UnimplementedMockSSHServiceHandler

	ctx context.Context

	lock  sync.Mutex
	hosts map[string]*hostInst

	path    string
	handler http.Handler
}

func (s *Service) Commands(ctx context.Context, in *connect.Request[pb.Host], stream *connect.ServerStream[pb.Command]) error {
	s.lock.Lock()
	defer s.lock.Unlock()

	hname := in.Msg.GetName()
	if hname == "" {
		return errors.New("must provide host name")
	}

	hi, exist := s.hosts[hname]
	if !exist {
		return errors.New("host does not exists")
	}

	hi.lock.Lock()
	defer hi.lock.Unlock()

streamCmds:
	for idx, cmd := range hi.commands {
		select {
		case <-ctx.Done():
			break streamCmds
		default:
			if err := stream.Send(&pb.Command{Index: int32(idx), Command: cmd}); err != nil {
				return err
			}
		}
	}

	return nil
}

func (s *Service) Filesystem(context.Context, *connect.Request[pb.Host], *connect.ServerStream[pb.BundleChunk]) error {
	return nil
}

func (s *Service) Lookup(_ context.Context, in *connect.Request[pb.Query]) (*connect.Response[pb.HostConfig], error) {
	s.lock.Lock()
	defer s.lock.Unlock()
	if hname := in.Msg.GetName(); hname != "" {
		hi, exists := s.hosts[hname]
		if !exists {
			return nil, errors.New("host does not exist")
		}

		return connect.NewResponse(hi.config), nil
	} else if port := in.Msg.GetPort(); port != 0 {
		for _, hi := range s.hosts {
			if hi.config.Port == port {
				return connect.NewResponse(hi.config), nil
			}
		}
		return nil, errors.New("no host on this port")
	}

	return nil, errors.New("empty query")
}

func (s *Service) Free(_ context.Context, in *connect.Request[pb.Host]) (*connect.Response[pb.Empty], error) {
	s.lock.Lock()
	defer s.lock.Unlock()

	hname := in.Msg.GetName()
	if hname == "" {
		return nil, errors.New("must provide host name")
	}

	hi, exist := s.hosts[hname]
	if !exist {
		return nil, errors.New("host does not exists")
	}

	hi.ctxC()

	return connect.NewResponse(&pb.Empty{}), nil
}

func (s *Service) New(_ context.Context, in *connect.Request[pb.HostConfig]) (*connect.Response[pb.HostConfig], error) {
	s.lock.Lock()
	defer s.lock.Unlock()

	if hc := in.Msg.GetHost(); hc == nil || hc.GetName() == "" {
		in.Msg.Host = &pb.Host{
			Name: namesgenerator.GetRandomName(0),
		}
	}

	if _, exist := s.hosts[in.Msg.Host.Name]; exist {
		return nil, errors.New("host exists")
	}

	hi, err := newSSHServer(s.ctx, in.Msg)
	if err != nil {
		return nil, err
	}

	s.hosts[hi.config.Host.Name] = hi

	return connect.NewResponse(hi.config), nil
}

func (s *Service) Attach(mux *http.ServeMux) {
	mux.Handle(s.path, s.handler)
}

func Serve(ctx context.Context) (*Service, error) {
	srv := &Service{
		ctx:   ctx,
		hosts: make(map[string]*hostInst),
	}

	srv.path, srv.handler = pbconnect.NewMockSSHServiceHandler(srv)

	return srv, nil
}
