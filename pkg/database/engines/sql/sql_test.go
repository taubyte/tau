package sql_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/database"
	sqlengine "github.com/taubyte/tau/pkg/database/engines/sql"
)

func spec() database.Spec {
	return database.Spec{
		Type: database.SQL, Engine: "postgres",
		Project: "shop", Environment: "prod", Name: "orders",
		Address: "db.internal:5432", Database: "app",
		Credentials: database.Credentials{Username: "app", Password: "s3cret", TLS: true},
	}
}

// TestTheRegistryProvesTheSurfaceOnce. Registering opens a probe connection per
// claimed type and type-asserts it, so an engine that claims sql and cannot
// query is refused where it is registered rather than where it is used.
func TestTheRegistryAcceptsIt(t *testing.T) {
	registry.reset()

	r := database.NewRegistry()
	require.NoError(t, r.Register(t.Context(), sqlengine.Postgres("recorder")))

	require.Equal(t, []string{"postgres"}, r.EnginesFor(database.SQL))
	require.Empty(t, r.EnginesFor(database.KV), "it claims one model and backs one")
}

// TestTLSIsSpelledOutInBothDirections. libpq's default is `prefer`, which falls
// back to plaintext without saying so — the exact shape of a security decision
// made by omission.
func TestTheConnectionStringIsBuilt(t *testing.T) {
	registry.reset()

	engine := sqlengine.Postgres("recorder")
	conn, err := engine.Open(t.Context(), spec())
	require.NoError(t, err)
	defer conn.Close()

	// database/sql opens lazily, so a query is what makes it connect — which
	// is also the property being asserted: Open does not ping.
	require.Empty(t, registry.opened(), "opening a pool connected to something")

	require.NoError(t, ping(t, conn))

	opened := registry.opened()
	require.Len(t, opened, 1)
	require.Contains(t, opened[0], "postgres://")
	require.Contains(t, opened[0], "app:s3cret@")
	require.Contains(t, opened[0], "db.internal:5432")
	require.Contains(t, opened[0], "/app")
	require.Contains(t, opened[0], "sslmode=require")

	// And plaintext is written down too, not left to a default.
	plain := spec()
	plain.Credentials.TLS = false
	dsn, err := sqlengine.PostgresDSN(plain)
	require.NoError(t, err)
	require.Contains(t, dsn, "sslmode=disable")
}

func TestTheMySQLForm(t *testing.T) {
	dsn, err := sqlengine.MySQLDSN(spec())
	require.NoError(t, err)
	require.Equal(t, "app:s3cret@tcp(db.internal:5432)/app?tls=true", dsn)

	plain := spec()
	plain.Credentials = database.Credentials{}
	dsn, err = sqlengine.MySQLDSN(plain)
	require.NoError(t, err)
	require.Equal(t, "tcp(db.internal:5432)/app?tls=false", dsn)
}

// TestAnExternalDatabaseNeedsAnAddress. Connecting to nothing is not a default.
func TestAnAddressIsRequired(t *testing.T) {
	bare := spec()
	bare.Address = ""

	_, err := sqlengine.PostgresDSN(bare)
	require.ErrorIs(t, err, sqlengine.ErrNoAddress)

	_, err = sqlengine.MySQLDSN(bare)
	require.ErrorIs(t, err, sqlengine.ErrNoAddress)

	_, err = sqlengine.Postgres("recorder").Open(context.Background(), bare)
	require.ErrorIs(t, err, sqlengine.ErrNoAddress)
}

// TestItBacksSqlAndNothingElse. A driver that also answered document queries
// would be claiming a data model whose verbs it does not have.
func TestItBacksSqlOnly(t *testing.T) {
	wrong := spec()
	wrong.Type = database.Document

	_, err := sqlengine.Postgres("recorder").Open(t.Context(), wrong)
	require.ErrorIs(t, err, sqlengine.ErrWrongType)
}

// TestQueryAndExecAreTheWholeSurface, and the rows adapter carries what the seam
// declares.
func TestQueryAndExec(t *testing.T) {
	registry.reset()
	registry.answer([]string{"id", "total"}, []any{int64(1), int64(100)}, []any{int64(2), int64(250)})

	conn, err := sqlengine.Postgres("recorder").Open(t.Context(), spec())
	require.NoError(t, err)
	defer conn.Close()

	sqlConn, ok := conn.(database.SQLConn)
	require.True(t, ok)

	rows, err := sqlConn.Query(t.Context(), "select id, total from orders")
	require.NoError(t, err)
	defer rows.Close()

	cols, err := rows.Columns()
	require.NoError(t, err)
	require.Equal(t, []string{"id", "total"}, cols)

	var got [][]int64
	for rows.Next() {
		var id, total int64
		require.NoError(t, rows.Scan(&id, &total))
		got = append(got, []int64{id, total})
	}
	require.NoError(t, rows.Err())
	require.Equal(t, [][]int64{{1, 100}, {2, 250}}, got)

	affected, err := sqlConn.Exec(t.Context(), "delete from orders where total < $1", 50)
	require.NoError(t, err)
	require.Equal(t, int64(3), affected)
}

// TestAnEngineNeedsANameAndADriver, and they are two names on purpose: a
// vocabulary engine is `postgres` and a driver registers itself as `pgx`, so
// conflating them would make swapping the driver a schema change.
func TestWhatItRefuses(t *testing.T) {
	_, err := sqlengine.New("", "pgx", sqlengine.PostgresDSN)
	require.ErrorIs(t, err, sqlengine.ErrNoName)

	_, err = sqlengine.New("postgres", "", sqlengine.PostgresDSN)
	require.ErrorIs(t, err, sqlengine.ErrNoDriver)

	_, err = sqlengine.New("postgres", "pgx", nil)
	require.Error(t, err)

	engine, err := sqlengine.New("postgres", "pgx", sqlengine.PostgresDSN)
	require.NoError(t, err)
	require.Equal(t, "postgres", engine.Name())
	require.Equal(t, []database.Type{database.SQL}, engine.Types())
}

// TestADriverNobodyRegisteredIsReportedWhereItIsAsked.
func TestAnUnregisteredDriver(t *testing.T) {
	_, err := sqlengine.Postgres("nobody-registered-this").Open(t.Context(), spec())
	require.Error(t, err)
	require.Contains(t, err.Error(), "postgres", "and it names the engine, not just the driver")
}

// ping makes the pool actually connect, since database/sql opens lazily.
func ping(t *testing.T, conn database.Conn) error {
	t.Helper()
	sqlConn := conn.(database.SQLConn)
	rows, err := sqlConn.Query(t.Context(), "select 1")
	if err != nil {
		return err
	}
	return rows.Close()
}
