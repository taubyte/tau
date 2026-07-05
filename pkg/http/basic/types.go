package basic

import (
	"context"
	"net/http"
	"time"

	"github.com/gorilla/mux"
	"github.com/rs/cors"
	"github.com/unrolled/secure"
)

type Service struct {
	ListenAddress      string
	AllowedMethods     []string
	AllowedOriginsFunc func(origin string) bool
	Router             *mux.Router
	Server             *http.Server
	SecLayer           *secure.Secure
	Cors               *cors.Cors
	Debug              bool

	// Zero on any of these = stdlib default (no cap / no timeout).
	MaxBodyBytes      int64
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
	ReadHeaderTimeout time.Duration

	err        error
	ctx        context.Context
	ctx_cancel context.CancelFunc
}
