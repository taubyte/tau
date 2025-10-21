package api

import (
	"fmt"

	"github.com/taubyte/tau/dream"
	httpIface "github.com/taubyte/tau/pkg/http"
)

func (srv *Service) injectSimpleHttp() {
	srv.server.POST(&httpIface.RouteDefinition{
		Path: "/simple/{universe}/{name}",
		Vars: httpIface.Variables{
			Required: []string{"universe", "name", "config"},
		},
		Handler: srv.apiHandlerSimple,
	})
}

func (srv *Service) apiHandlerSimple(ctx httpIface.Context) (interface{}, error) {
	universe, err := srv.getUniverse(ctx)
	if err != nil {
		return nil, fmt.Errorf("killing simple failed with: %s", err.Error())
	}

	name, err := ctx.GetStringVariable("name")
	if err != nil {
		return nil, fmt.Errorf("failed getting name error %w", err)
	}

	config := struct {
		Config dream.SimpleConfig
	}{}

	err = ctx.ParseBody(&config)
	if err != nil {
		return nil, err
	}

	node, err := universe.CreateSimpleNode(name, &config.Config)
	if err != nil {
		return nil, fmt.Errorf("failed creating simple node `%s` with: %v", name, err)
	}

	// To prevent timeout from client
	go func() {
		universe.Mesh(node)
		universe.Register(node, name, map[string]int{"p2p": config.Config.Port})
	}()

	return nil, nil
}
