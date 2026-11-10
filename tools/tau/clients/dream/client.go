package dream

import (
	"context"
	"fmt"
	"strings"
	"time"

	dream "github.com/taubyte/tau/clients/http/dream"
	"github.com/taubyte/tau/clients/http/dream/inject"
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

// PushAll asks Dream to build the project's repos at branch, as `dream inject
// push-all` does: Dream gets no GitHub webhook.
func PushAll(ctx context.Context, url, universe, projectID, branch string) error {
	c, err := dream.New(ctx, dream.URL(url), dream.Timeout(2*time.Minute))
	if err != nil {
		return err
	}
	return c.Universe(universe).Inject(inject.Fixture("pushAll", []any{projectID, branch}))
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
