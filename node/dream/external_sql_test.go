//go:build dreaming && docker_integration

package dream_test

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/node"
	nodeDream "github.com/taubyte/tau/node/dream"
	"github.com/taubyte/tau/pkg/containers/backends/docker"
	"github.com/taubyte/tau/pkg/containers/core"
)

// external_sql_test runs projects/with/database/{postgres,mysql}/sql: a function
// and a `sql` database on somebody else's engine, compiled from yaml, published,
// and asked to create a table, write a row and read it back — against a real
// server this test starts in docker.
//
// THE PATH IS THE AUTHOR'S: host, database, credentials and tls come off the
// compiled resource, the credentials are a `secret` resolved through the node's
// vault as "user:password", and the connection is the driver boot links. Only
// the host's port is rewritten, in a copy, because the server's is the test's to
// pick.

// server is a database server one test started.
type server struct {
	image string
	port  int // inside the container
	env   []string
	// dsn is how the test itself waits for the server to accept a login.
	driver, dsn string
}

func postgresServer(port int) server {
	return server{
		image: "postgres:16-alpine", port: 5432,
		env:    []string{"POSTGRES_USER=tau", "POSTGRES_PASSWORD=s3cret", "POSTGRES_DB=orders"},
		driver: "pgx", dsn: fmt.Sprintf("postgres://tau:s3cret@127.0.0.1:%d/orders?sslmode=disable", port),
	}
}

func mysqlServer(port int) server {
	return server{
		image: "mysql:8.4", port: 3306,
		env: []string{"MYSQL_ROOT_PASSWORD=root", "MYSQL_DATABASE=orders",
			"MYSQL_USER=tau", "MYSQL_PASSWORD=s3cret"},
		driver: "mysql", dsn: fmt.Sprintf("tau:s3cret@tcp(127.0.0.1:%d)/orders", port),
	}
}

// freePort is a loopback port nothing holds right now.
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// start runs s published on hostPort and returns once it accepts a login.
func (s server) start(t *testing.T, hostPort int) {
	t.Helper()
	ctx := t.Context()

	backend, err := docker.New(core.DockerConfig{})
	require.NoError(t, err)
	require.NoError(t, backend.Image(s.image).Pull(ctx), "pulling %s", s.image)

	id, err := backend.Create(ctx, &core.ContainerConfig{
		Image: s.image,
		Env:   s.env,
		Network: &core.NetworkConfig{
			Mode:         "bridge",
			PortMappings: []core.PortMapping{{HostPort: hostPort, ContainerPort: s.port, Protocol: "tcp", HostIP: "127.0.0.1"}},
		},
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		// t.Context() is done by the time cleanups run.
		ctx := context.Background()
		backend.Stop(ctx, id)
		backend.Remove(ctx, id)
	})
	require.NoError(t, backend.Start(ctx, id))

	db, err := sql.Open(s.driver, s.dsn)
	require.NoError(t, err)
	defer db.Close()
	var last error
	require.Eventually(t, func() bool {
		last = db.PingContext(ctx)
		return last == nil
	}, 2*time.Minute, time.Second, "%s never accepted a login: %v", s.image, &last)
}

// pointedAt copies the named project with its database's host replaced.
func pointedAt(t *testing.T, name string, hostPort int) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "project")
	require.NoError(t, os.CopyFS(dir, os.DirFS(projectDir(name))))

	file := filepath.Join(dir, "database", "orders.yaml")
	yaml, err := os.ReadFile(file)
	require.NoError(t, err)
	host := []byte(fmt.Sprintf("host: 127.0.0.1:%d", hostPort))
	rewritten := strings.NewReplacer("host: 127.0.0.1:5432", string(host), "host: 127.0.0.1:3306", string(host)).Replace(string(yaml))
	require.NotEqual(t, string(yaml), rewritten, "the project names no host to point at the server")
	require.NoError(t, os.WriteFile(file, []byte(rewritten), 0o644))
	return dir
}

func TestAnExternalPostgresIsReachedAsDeclared(t *testing.T) {
	port := freePort(t)
	externalSQL(t, "with/database/postgres/sql", postgresServer(port), port)
}

func TestAnExternalMySQLIsReachedAsDeclared(t *testing.T) {
	port := freePort(t)
	externalSQL(t, "with/database/mysql/sql", mysqlServer(port), port)
}

func externalSQL(t *testing.T, name string, s server, port int) {
	defer nodeDream.Runtimes(nodeDream.Runtimes()...)
	s.start(t, port)

	r := deployServing(t, pointedAt(t, name, port), "orders.example.com", kitchenSinkWASM(t), nil,
		node.Register, node.Worker, node.Stats, node.Gateway, node.Database, node.Primitives, node.Secrets)

	// THE CREDENTIALS ARE A SECRET THE AUTHOR NAMED, set the way a developer
	// sets one. Without it the open is refused, which the first exec proves by
	// retrying until this has landed.
	ctx := t.Context()
	require.NoError(t, r.node.Secrets().Local().Set(ctx, "shop", "prod", r.idOf(t, "secret", "dbcreds"), []byte("tau:s3cret")))

	exec := func(statement string) {
		t.Helper()
		var last string
		require.Eventually(t, func() bool {
			res, err := r.onCarrying(ctx, keeper("shop"), http.MethodPost, "/exec/orders", statement)
			if err != nil {
				last = err.Error()
				return false
			}
			last = fmt.Sprintf("%d %s", res.Status, res.Body)
			return res.Status == 200
		}, 30*time.Second, time.Second, "%q was refused: %s", statement, &lastSaid{&last})
	}
	exec(`CREATE TABLE entries (id INTEGER PRIMARY KEY, payee VARCHAR(64), amount INTEGER)`)
	exec(`INSERT INTO entries(id, payee, amount) VALUES(1, 'alice', 42)`)

	res, err := r.onCarrying(ctx, keeper("shop"), http.MethodPost, "/query/orders", `SELECT payee, amount FROM entries`)
	require.NoError(t, err)
	require.Equal(t, 200, res.Status, "the read was refused: %s", res.Body)
	require.Contains(t, string(res.Body), "alice")
	require.Contains(t, string(res.Body), "42")

	// AND THE ROW IS IN THE SERVER, not in anything tau keeps: the test reads
	// it back over its own connection.
	db, err := sql.Open(s.driver, s.dsn)
	require.NoError(t, err)
	defer db.Close()
	var amount int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT amount FROM entries WHERE payee = 'alice'`).Scan(&amount))
	require.Equal(t, 42, amount)
}
