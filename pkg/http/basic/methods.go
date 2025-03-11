package basic

import (
	"context"
	"errors"

	service "github.com/taubyte/tau/pkg/http"
	"github.com/taubyte/tau/pkg/http/options"

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
	}

	// default: we ignore option we do not know so other modules can process them
	return nil
}

func (s *Service) Start() {
	go func() {
		s.err = s.Server.ListenAndServe()
		if s.err != http.ErrServerClosed {
			s.Kill()
		}
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
