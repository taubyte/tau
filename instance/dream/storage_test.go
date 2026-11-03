//go:build dreaming

package dream_test

import (
	"bytes"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	nodeDream "github.com/taubyte/tau/instance/dream"
)

// An object written by a guest on one node and read by a guest on another. The name
// is a row in a replicated kvdb and the bytes are in the DAG, so this fails if
// either does not cross.

func TestAnObjectCrossesTheSwarm(t *testing.T) {
	defer nodeDream.Runtimes(nodeDream.Runtimes()...)

	cloud := deployFleet(t, 2, "with/storage/object", "receipts.example.com", cacherWASM(t))
	first, second := cloud[0], cloud[1]

	// A put answers the ETag it stored under, which is the content id. Not retried: a
	// bucket's index is crdt, with no group to form.
	res, err := first.onCarrying(t.Context(), target(), http.MethodPost, "/obj-put/receipts/2026/01", "a receipt")
	require.NoError(t, err)
	require.Equal(t, 200, res.Status, string(res.Body))
	etag := string(res.Body)
	require.NotEmpty(t, etag)

	// Read through the second node's guest, tier, kvdb and DAG.
	require.Eventually(t, func() bool {
		res, err := second.onCarrying(t.Context(), target(), http.MethodPost, "/obj-get/receipts/2026/01", "")
		return err == nil && res.Status == 200 && string(res.Body) == "a receipt"
	}, 60*time.Second, 500*time.Millisecond, "an object never crossed the swarm")

	// head answers there too, off the row, with the ETag the first node answered: the
	// second node read the replicated row and computed nothing.
	var head string
	require.Eventually(t, func() bool {
		res, err := second.onCarrying(t.Context(), target(), http.MethodPost, "/obj-head/receipts/2026/01", "")
		if err != nil || res.Status != 200 {
			return false
		}
		head = string(res.Body)
		return bytes.HasPrefix(res.Body, []byte("2026/01:9:"))
	}, 60*time.Second, 500*time.Millisecond, "head never answered on the second node")

	require.Contains(t, head, ":"+etag+":", "the etag did not cross the swarm: %s", head)
}
