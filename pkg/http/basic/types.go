package basic

import (
	"context"
	"net/http"

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
	err                error
	ctx                context.Context
	ctx_cancel         context.CancelFunc
}
