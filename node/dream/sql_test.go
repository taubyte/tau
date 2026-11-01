//go:build dreaming

package dream_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	nodeDream "github.com/taubyte/tau/node/dream"
)

// projects/with/database/distributed/sql/crdt on a fleet: tau's own engine, one file
// per database per node, copies meeting over the swarm. The schema is the guest's:
// only a workload's statement creates the table.

func TestASQLDatabaseAnswersFromANodeThatNeverTookTheWrite(t *testing.T) {
	defer nodeDream.Runtimes(nodeDream.Runtimes()...)

	cloud := deployFleet(t, 3, "with/database/distributed/sql/crdt", "ledger.example.com", kitchenSinkWASM(t))
	ctx := t.Context()

	exec := func(at int, statement string) {
		t.Helper()
		var last string
		require.Eventually(t, func() bool {
			res, err := cloud[at].onCarrying(ctx, keeper("shop"), http.MethodPost, "/exec/ledger", statement)
			if err != nil {
				last = err.Error()
				return false
			}
			last = fmt.Sprintf("%d %s", res.Status, res.Body)
			return res.Status == 200
		}, 30*time.Second, time.Second, "%q was refused on node %d: %s", statement, at, &lastSaid{&last})
	}

	exec(0, `CREATE TABLE entries (id INTEGER PRIMARY KEY, payee TEXT UNIQUE, amount INTEGER)`)
	exec(0, `INSERT INTO entries(payee, amount) VALUES('alice', 42)`)

	// cacher renders a result set as "columns|value|value…", with each value's
	// storage class visible.
	reader := cloud[2]
	var last string
	require.Eventually(t, func() bool {
		res, err := reader.onCarrying(ctx, keeper("shop"), http.MethodPost, "/query/ledger", `SELECT payee, amount FROM entries`)
		if err != nil {
			last = err.Error()
			return false
		}
		last = fmt.Sprintf("%d %s", res.Status, res.Body)
		return res.Status == 200 && strings.Contains(string(res.Body), "alice") && strings.Contains(string(res.Body), "42")
	}, 60*time.Second, time.Second, "a row written on one node never reached another: %s", &lastSaid{&last})

	// AND A WRITE ON THE READER COMES BACK TO THE WRITER: every copy writes.
	exec(2, `INSERT INTO entries(payee, amount) VALUES('bob', 7)`)
	require.Eventually(t, func() bool {
		res, err := cloud[0].onCarrying(ctx, keeper("shop"), http.MethodPost, "/query/ledger", `SELECT payee FROM entries WHERE payee = 'bob'`)
		if err != nil {
			last = err.Error()
			return false
		}
		last = fmt.Sprintf("%d %s", res.Status, res.Body)
		return res.Status == 200 && strings.Contains(string(res.Body), "bob")
	}, 60*time.Second, time.Second, "a row written on the second node never reached the first: %s", &lastSaid{&last})
}
