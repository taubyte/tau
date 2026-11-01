package boot

import (
	"context"
	"fmt"

	// THE DRIVERS ARE LINKED HERE AND NOWHERE ELSE. database/sql is a registry
	// and a driver registers itself from its own init, so the import belongs at
	// the composition root — which this is. pkg/database and its engines stay
	// driver-free, which is what lets a test register a fake one and what would
	// let a different composer link pgbouncer's, a proxy's, or none at all.
	_ "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/taubyte/tau/pkg/database"
	sqlengine "github.com/taubyte/tau/pkg/database/engines/sql"
)

// What a machine can connect to: the external engines the vocabulary names
// (postgres, mysql, mongo), so bringing your own database works. Adding one is a
// registration here. tau's own engines are registered by node/database.

// DatabaseEngines is the registry a machine serves external databases from, exported
// for node/dream. A registration failure refuses the start: it is a build fault.
func DatabaseEngines(ctx context.Context) (*database.Registry, error) {
	registry := database.NewRegistry()

	for _, engine := range []database.Engine{
		// `pgx` is what jackc/pgx/v5/stdlib registers itself under, and
		// `postgres` is what the vocabulary calls the engine. They differ, and
		// conflating them would make swapping the driver a schema change.
		sqlengine.Postgres("pgx"),
		sqlengine.MySQL("mysql"),
	} {
		if err := registry.Register(ctx, engine); err != nil {
			return nil, fmt.Errorf("boot: registering the %s engine: %w", engine.Name(), err)
		}
	}
	return registry, nil
}
