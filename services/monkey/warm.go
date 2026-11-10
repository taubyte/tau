package monkey

import (
	"context"

	"github.com/taubyte/tau/pkg/containers"
)

// buildImages are the images the project templates build with. Every node
// pulls them as it starts, so the first build after a start does not spend
// its first minute on the registry, and the image collector keeps them.
var buildImages = []string{"taubyte/go-wasi:latest"}

func warmBuildImages(ctx context.Context) {
	client, err := containers.New()
	if err != nil {
		logger.Warnf("warming build images: %s", err)
		return
	}
	for _, name := range buildImages {
		if _, err := client.Image(ctx, name); err != nil {
			logger.Warnf("warming build image %s: %s", name, err)
		}
	}
}
