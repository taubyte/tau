package monkey

import (
	"context"

	"github.com/taubyte/tau/pkg/containers"
)

// devBuildImages are the images the project templates build with. A
// development node pulls them as it starts, so the first push after Dream
// starts does not spend its first minute on the registry.
var devBuildImages = []string{"taubyte/go-wasi:latest"}

func warmBuildImages(ctx context.Context) {
	client, err := containers.New()
	if err != nil {
		logger.Warnf("warming build images: %s", err)
		return
	}
	for _, name := range devBuildImages {
		if _, err := client.Image(ctx, name); err != nil {
			logger.Warnf("warming build image %s: %s", name, err)
		}
	}
}
