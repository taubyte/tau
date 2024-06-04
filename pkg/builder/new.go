package builder

import (
	"context"
	"fmt"
	"os"

	ci "github.com/taubyte/go-simple-container"
	iface "github.com/taubyte/tau/core/builders"
	"github.com/taubyte/tau/pkg/specs/builders"
	specs "github.com/taubyte/tau/pkg/specs/builders/common"
	"gopkg.in/yaml.v3"
)

// New creates a new container Builder for the given working directory.
func New(ctx context.Context, workDir string) (iface.Builder, error) {
	// create new container client
	ciClient, err := ci.New()
	if err != nil {
		return nil, fmt.Errorf("new container client failed with: %w", err)
	}

	wd, err := specs.Wd(workDir)
	if err != nil {
		return nil, specs.DefaultWDError(err)
	}

	// set builder config
	b := &builder{
		config:          &builders.Config{},
		containerClient: ciClient,
		wd:              wd,
		context:         ctx,
	}

	// If context cancelled close.
	go func(_b *builder) {
		if context := _b.context; context != nil {
			<-context.Done()
			if _b != nil {
				_b.Close()
			}
		}
	}(b)

	// open taubyte config.yaml
	file, err := os.Open(b.wd.ConfigFile())
	if err != nil {
		return nil, err
	}
	defer file.Close()

	// read the taubyte config.yaml and set yaml config on the build config.
	if err = yaml.NewDecoder(file).Decode(b.config); err != nil {
		return nil, fmt.Errorf("decoding config failed with: %w", err)
	}

	// set tarball if any
	return b, b.setTarball()
}
