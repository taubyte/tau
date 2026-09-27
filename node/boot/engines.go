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

// engines.go is what a machine can connect to.
//
// # Why a v2 cloud has to ship these
//
// The vocabulary names external engines per data model — `sql` is backed by
// postgres and mysql, `document` by mongo — and until something registered them
// a project writing `sql: {postgres: {host: db.example.com}}` compiled,
// deployed, and answered its first query with "no database engine is registered
// in this build". Bringing your own Postgres is the ordinary shape on every
// platform this competes with, so a cloud that cannot is not competing.
//
// # A registration is not a fork
//
// pkg/database's Registry decides what a node can do by what got registered,
// and pkg/database/engines/sql is one adapter over any database/sql driver. So
// adding a managed Postgres, a proxy, or a driver somebody prefers is a line
// here rather than a change to any of the layers below.
//
// # tau's own engines are not here
//
// `kv`, `document` and `vector` on the `distributed` engine are the node's to
// register, because they are backed by the swarm this node is already on and
// need nothing provisioned. See node/database's `engines`, which registers them
// on first use and lets a composer's own win.

// databaseEngines is the registry a machine serves external databases from.
//
// A REGISTRATION FAILURE IS A REFUSAL TO START. An engine that cannot open the
// probe spec is an engine whose surface is wrong, which is a build fault rather
// than an operator's — and a node that started anyway would answer every query
// for that engine with a message about a registry instead.
func databaseEngines(ctx context.Context) (*database.Registry, error) {
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
