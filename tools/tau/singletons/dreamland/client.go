package dreamland

import (
	"context"
	"fmt"
	"strings"
	"time"

	dreamland "github.com/taubyte/tau/clients/http/dream"
	"github.com/taubyte/tau/dream/api"
	"github.com/taubyte/tau/tools/tau/env"
)

var dream_client *dreamland.Client

func Client(ctx context.Context) (*dreamland.Client, error) {
	if dream_client == nil {
		var err error
		dream_client, err = dreamland.New(ctx, dreamland.URL("http://127.0.0.1:1421"), dreamland.Timeout(15*time.Second))
		if err != nil {
			return nil, err
		}
	}

	return dream_client, nil
}

func Status(ctx context.Context) (echart api.Echart, err error) {
	var dreamClient *dreamland.Client
	dreamClient, err = Client(ctx)
	if err != nil {
		return
	}

	selectedUniverse, _ := env.GetCustomNetworkUrl()
	universe := dreamClient.Universe(selectedUniverse)
	echart, err = universe.Chart()
	return
}

func HTTPPort(ctx context.Context, name string) (int, error) {
	echart, err := Status(ctx)
	if err != nil {
		return 0, err
	}

	for _, node := range echart.Nodes {
		if strings.Contains(node.Name, name) {
			httpPort, ok := node.Value["http"]
			if !ok {
				return 0, fmt.Errorf("http port for `%s` not set", name)
			}

			return httpPort, nil
		}
	}

	return 0, fmt.Errorf("node `%s` not found", name)
}
