package basic

import (
	"context"
	"errors"

	service "github.com/taubyte/tau/pkg/http"
	"github.com/taubyte/tau/pkg/http/options"

	"net"
	"net/http"
	"net/url"
)

func (s *Service) SetOption(optIface interface{}) error {
	if optIface == nil {
		return errors.New("`nil` option")
	}

	switch opt := optIface.(type) {
	case options.OptionListen:
		s.ListenAddress = opt.On
	case options.OptionAllowedMethods:
		s.AllowedMethods = opt.Methods
	case options.OptionAllowedOrigins:
		s.AllowedOriginsFunc = opt.Func
	case options.OptionDebug:
		s.Debug = true
	case options.OptionMaxBodyBytes:
		s.MaxBodyBytes = opt.Limit
	case options.OptionReadTimeout:
		s.ReadTimeout = opt.Duration
	case options.OptionWriteTimeout:
		s.WriteTimeout = opt.Duration
	case options.OptionIdleTimeout:
		s.IdleTimeout = opt.Duration
	case options.OptionReadHeaderTimeout:
		s.ReadHeaderTimeout = opt.Duration
	}

	// default: we ignore option we do not know so other modules can process them
	return nil
}

// Start binds and then serves.
//
// THE BIND HAPPENS HERE AND NOT INSIDE ListenAndServe, so that a caller who
// asked for port 0 can find out what it got: the kernel allocates at bind
// time, and the listen address is rewritten to the one actually taken before
// this returns, which is what makes GetListenAddress answer the truth. It also
// means a port that cannot be taken is an error the caller can see on Error()
// right away rather than one that surfaces from a goroutine later.
func (s *Service) Start() {
	addr := s.Server.Addr
	if addr == "" {
		addr = ":http" // what ListenAndServe defaults to
	}

	listener, err := net.Listen("tcp", addr)
	if err != nil {
		s.err = err
		s.Kill()
		return
	}
	s.ListenAddress = listener.Addr().String()

	go func() {
		s.err = s.Server.Serve(listener)
		if s.err != http.ErrServerClosed {
			s.Kill()
		}
	}()
	s.WatchContextDone()
}

func (s *Service) WatchContextDone() {
	go func() {
		<-s.ctx.Done()
		s.Server.Shutdown(s.ctx)
	}()
}

func (s *Service) Kill() {
	s.ctx_cancel()
}

func (s *Service) Stop() {
	ctx, ctxC := context.WithTimeout(s.ctx, service.ShutdownGracePeriod)
	defer ctxC()

	s.err = s.Server.Shutdown(ctx)
	s.Kill()
}

func (s *Service) Wait() error {
	<-s.ctx.Done()
	return s.err
}

func (s *Service) GetListenAddress() (*url.URL, error) {
	return url.Parse("http://" + s.ListenAddress)
}

func (s *Service) Error() error {
	return s.err
}

func (s *Service) Context() context.Context {
	return s.ctx
}
