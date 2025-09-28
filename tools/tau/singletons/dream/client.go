package dream

import (
	"context"
	"fmt"
	"strings"
	"time"

	dream "github.com/taubyte/tau/clients/http/dream"
	dreamLib "github.com/taubyte/tau/dream"
	"github.com/taubyte/tau/dream/api"
	"github.com/taubyte/tau/tools/tau/env"
)

var dream_client *dream.Client

func Client(ctx context.Context) (*dream.Client, error) {
	if dream_client == nil {
		var err error
		dream_client, err = dream.New(ctx, dream.URL(fmt.Sprintf("http://127.0.0.1:%d", dreamLib.DreamApiPort)), dream.Timeout(15*time.Second))
		if err != nil {
			return nil, err
		}
	}

	return dream_client, nil
}

func Status(ctx context.Context) (echart api.Echart, err error) {
	var dreamClient *dream.Client
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
