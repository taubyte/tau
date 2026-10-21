package boot

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
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

// SeedFile is what every node holding the seed is given: the secrets, and the
// peer ids of every node given them.
type SeedFile struct {
	Holders []string `yaml:"holders"`
	Secrets []Secret `yaml:"secrets"`
}

// seedRetry is how often a holder looks at the vault again.
var seedRetry = time.Second

// ReadSeed reads a seed file. A file that is not there was seeded already,
// and holds nothing.
func ReadSeed(path string) (SeedFile, error) {
	var f SeedFile
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return f, nil
	}
	if err != nil {
		return f, fmt.Errorf("boot: seed: %w", err)
	}
	if err := yaml.Unmarshal(data, &f); err != nil {
		return f, fmt.Errorf("boot: seed %s: %w", path, err)
	}
	return f, nil
}

// Seed returns once v holds every secret of f, read back with its value.
//
// ONE WRITER AT A TIME: two concurrent writes of one secret can leave a record
// and a wrapping from different writers, which opens to nothing. The writer is
// the lowest holder that is self or in live(); every other holder only reads.
// A writer that dies drops out of live() and the next holder writes in its
// place, and what a race left unreadable is read as not held and written
// again, so the vault converges with no deploy and no operator.
func Seed(ctx context.Context, v node.Vault, self string, live func() []string, f SeedFile) error {
	for {
		missing := unheld(ctx, v, f.Secrets)
		if len(missing) == 0 {
			return nil
		}
		if writer(self, f.Holders, live()) {
			for _, s := range missing {
				if err := v.Set(ctx, s.Project, s.Environment, s.Name, []byte(s.Value)); err != nil {
					logger.Warnf("seeding %s/%s/%s: %v", s.Project, s.Environment, s.Name, err)
				}
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(seedRetry):
		}
	}
}

func unheld(ctx context.Context, v node.Vault, secrets []Secret) []Secret {
	var out []Secret
	for _, s := range secrets {
		if got, err := v.Secret(ctx, s.Project, s.Environment, s.Name); err != nil || got != s.Value {
			out = append(out, s)
		}
	}
	return out
}

func writer(self string, holders, live []string) bool {
	for _, h := range slices.Sorted(slices.Values(holders)) {
		if h == self {
			return true
		}
		if slices.Contains(live, h) {
			return false
		}
	}
	return false
}

// seed seeds the node's vault and removes the file once the vault holds it,
// so the secrets are on disk only until then.
func (r *Running) seed(ctx context.Context, path string, f SeedFile) {
	defer close(r.seeded)
	live := func() []string {
		var out []string
		for _, p := range r.Node.Peers(node.Discovery) {
			out = append(out, p.String())
		}
		return out
	}
	if err := Seed(ctx, r.Node.Vault(), r.peer.ID().String(), live, f); err != nil {
		return
	}
	if err := os.Remove(path); err != nil {
		logger.Errorf("removing the seed file: %v", err)
	}
}
