package builder

import (
	"encoding/json"
	"time"

	"github.com/taubyte/tau/core/builders"
	ci "github.com/taubyte/tau/pkg/containers"
)

// Build will build the given directory as configured and return a builder output
func (b *builder) Build(ops ...ci.ContainerOption) (builders.Output, error) {
	var (
		out *output
		err error
	)

	out = new(b.wd)

	environment := b.config.HandleDepreciatedEnvironment()
	clientImage, err := b.buildImage()
	if err != nil {
		return out, b.Errorf("initializing image failed with: %w", err)
	}

	if err = b.run(out, clientImage, environment, ops...); err != nil {
		json.NewEncoder(b.output).Encode(struct {
			Error     string `json:"error"`
			Timestamp int64  `json:"timestamp"`
		}{
			Timestamp: time.Now().UnixNano(),
			Error:     err.Error(),
		})
		return nil, err
	}

	json.NewEncoder(b.output).Encode(struct {
		Timestamp int64 `json:"timestamp"`
		Succeess  bool  `json:"success"`
	}{
		Timestamp: time.Now().UnixNano(),
		Succeess:  true,
	})

	return out, nil
}
