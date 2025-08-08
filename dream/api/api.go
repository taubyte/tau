package api

import (
	"context"
	"time"

	goHttp "net/http"

	"github.com/pterm/pterm"
	"github.com/taubyte/tau/dream"
	httpIface "github.com/taubyte/tau/pkg/http"
	http "github.com/taubyte/tau/pkg/http/basic"
	"github.com/taubyte/tau/pkg/http/options"
)

type multiverseService struct {
	rest httpIface.Service
	*dream.Multiverse
}

func BigBang() error {
	srv := &multiverseService{
		Multiverse: dream.MultiVerse(),
	}

	var err error
	srv.rest, err = http.New(
		srv.Context(),
		options.Listen(dream.DreamApiListen),
		options.AllowedOrigins(true, []string{".*"}),
	)
	if err != nil {
		return err
	}

	srv.setUpHttpRoutes().Start()

	waitCtx, waitCtxC := context.WithTimeout(srv.Context(), 10*time.Second)
	defer waitCtxC()

	for {
		select {
		case <-waitCtx.Done():
			return waitCtx.Err()
		case <-time.After(100 * time.Millisecond):
			if srv.rest.Error() != nil {
				pterm.Error.Println("Dream failed to start")
				return srv.rest.Error()
			}
			_, err := goHttp.Get("http://" + dream.DreamApiListen)
			if err == nil {
				pterm.Info.Println("Dream ready")
				return nil
			}
		}
	}
}
