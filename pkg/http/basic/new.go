package basic

import (
	"context"
	"net/http"

	"github.com/CAFxX/httpcompression"
	"github.com/gorilla/mux"
	"github.com/rs/cors"
	"github.com/taubyte/tau/pkg/http/options"
	"github.com/unrolled/secure"
)

func New(ctx context.Context, opts ...options.Option) (*Service, error) {
	var s Service
	s.ctx, s.ctx_cancel = context.WithCancel(ctx)

	err := options.Parse(&s, opts)
	if err != nil {
		return nil, err
	}

	s.Router = mux.NewRouter().StrictSlash(false)

	s.SecLayer = secure.New(secure.Options{
		STSSeconds:            31536000,
		STSIncludeSubdomains:  true,
		STSPreload:            true,
		FrameDeny:             true,
		ContentTypeNosniff:    true,
		BrowserXssFilter:      true,
		ContentSecurityPolicy: "script-src $NONCE",
	})

	corsOption := cors.Options{
		AllowCredentials:   true,
		AllowedHeaders:     []string{"*"},
		AllowedMethods:     DefaultAllowedMethods,
		OptionsPassthrough: false,
		AllowedOrigins:     []string{"*"},
		Debug:              s.Debug,
	}

	if s.AllowedMethods != nil {
		corsOption.AllowedMethods = s.AllowedMethods
	}

	if s.AllowedOriginsFunc != nil {
		corsOption.AllowOriginFunc = s.AllowedOriginsFunc
	}

	s.Cors = cors.New(corsOption)

	Compress, _ := httpcompression.DefaultAdapter()

	s.Server = &http.Server{
		Addr:    s.ListenAddress,
		Handler: s.Cors.Handler(Compress(s.Router)),
	}

	// make sure we end context if the server was shutdown
	s.Server.RegisterOnShutdown(func() {
		s.ctx_cancel()
	})

	return &s, nil
}
