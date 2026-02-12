package dream

import (
	"context"
	"fmt"
	"strings"
	"time"

	dream "github.com/taubyte/tau/clients/http/dream"
	dreamLib "github.com/taubyte/tau/dream"
	"github.com/taubyte/tau/dream/api"
	"github.com/taubyte/tau/tools/tau/session"
)

// DefaultURL returns the dream API URL (always http://127.0.0.1:DreamApiPort).
func DefaultURL() string {
	return fmt.Sprintf("http://127.0.0.1:%d", dreamLib.DreamApiPort)
}

func Client(ctx context.Context) (*dream.Client, error) {
	return dream.New(ctx, dream.URL(DefaultURL()), dream.Timeout(15*time.Second))
}

func Status(ctx context.Context) (echart api.Echart, err error) {
	var dreamClient *dream.Client
	dreamClient, err = Client(ctx)
	if err != nil {
		return
	}

	cloudValue, _ := session.GetCustomCloudUrl()
	universe := dreamClient.Universe(cloudValue)
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
