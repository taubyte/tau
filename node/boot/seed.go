package boot

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/taubyte/tau/node"
)

// Secret is one value an operator hands the vault through a seed file.
type Secret struct {
	Project     string `yaml:"project"`
	Environment string `yaml:"environment"`
	Name        string `yaml:"name"`
	Value       string `yaml:"value"`
}

// seedRetry is how long a write waits before trying a vault that refused it,
// typically one whose key is not dealt yet.
var seedRetry = time.Second

// ReadSeed reads a seed file: a yaml list of Secret. A file that is not there
// was seeded already, and holds nothing.
func ReadSeed(path string) ([]Secret, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("boot: seed: %w", err)
	}
	var secrets []Secret
	if err := yaml.Unmarshal(data, &secrets); err != nil {
		return nil, fmt.Errorf("boot: seed %s: %w", path, err)
	}
	return secrets, nil
}

// Seed writes each secret into v, retrying each until the vault takes it or
// ctx ends.
//
// ONE WRITER PER SECRET. Two concurrent writes of one secret can leave a
// record and a wrapping from different writers, which opens to nothing, so a
// cloud seeds from a single node.
func Seed(ctx context.Context, v node.Vault, secrets []Secret) error {
	for _, s := range secrets {
		for {
			err := v.Set(ctx, s.Project, s.Environment, s.Name, []byte(s.Value))
			if err == nil {
				break
			}
			logger.Warnf("seeding %s/%s/%s: %v", s.Project, s.Environment, s.Name, err)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(seedRetry):
			}
		}
	}
	return nil
}

// seed writes the file's secrets into the node's vault and removes the file,
// so they are on disk only until the vault holds them.
func (r *Running) seed(ctx context.Context, path string, secrets []Secret) {
	defer close(r.seeded)
	if err := Seed(ctx, r.Node.Vault(), secrets); err != nil {
		return
	}
	if err := os.Remove(path); err != nil {
		logger.Errorf("removing the seed file: %v", err)
	}
}
