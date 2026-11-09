package hosted

import (
	"context"
	gosql "database/sql"
	"errors"
	"fmt"
	"slices"

	"github.com/taubyte/tau/pkg/database"
	sqlengine "github.com/taubyte/tau/pkg/database/engines/sql"
	"github.com/taubyte/tau/services/worker"
)

// ErrNoDriver is an external engine the worker's section names over a driver this
// binary does not link.
var ErrNoDriver = errors.New("worker: no such database driver is linked into this binary")

// external makes each engine the vocabulary names over the driver it connects with.
var external = map[string]func(driver string) database.Engine{
	"postgres": func(driver string) database.Engine { return sqlengine.Postgres(driver) },
	"mysql":    func(driver string) database.Engine { return sqlengine.MySQL(driver) },
}

// openEngines is the registry a workload's databases are opened from: the external
// engines the section names, each over its driver. An engine the section names over
// a driver this binary does not link is refused; a default one is left out, since
// registering an engine opens its driver.
func (n *Service) openEngines() error {
	n.engines = database.NewRegistry()
	wanted, named := n.cfg.Databases, true
	if len(wanted) == 0 {
		wanted, named = worker.DefaultDatabases(), false
	}
	linked := gosql.Drivers()
	for _, db := range wanted {
		make, ok := external[db.Engine]
		if !ok {
			return fmt.Errorf("worker: %q is not an external database engine; the engines are postgres and mysql", db.Engine)
		}
		if !slices.Contains(linked, db.Driver) {
			if named {
				return fmt.Errorf("%w: %s for %s", ErrNoDriver, db.Driver, db.Engine)
			}
			continue
		}
		if err := n.engines.Register(context.Background(), make(db.Driver)); err != nil {
			return fmt.Errorf("worker: registering the %s engine: %w", db.Engine, err)
		}
	}
	return nil
}
