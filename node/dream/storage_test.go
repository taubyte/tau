//go:build dreaming

package dream_test

import (
	"bytes"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	nodeDream "github.com/taubyte/tau/node/dream"
)

// storage_test is an object written by a guest on one node and read by a guest
// on another — which is the whole claim of "content-addressed, served to the
// project's peers", and the one thing neither the ABI's own tests nor the
// node's can make on their own.
//
// The two halves have different plumbing and both have to work: the NAME is a
// row in a replicated kvdb and the BYTES are in the DAG, so this fails if
// either one does not cross.
//
// AND THE PROJECT IS AUTHORED RATHER THAN TRANSCRIBED, for project_test's
// reason. The object this test used to publish was written by hand, so it was a
// statement of what the compiler was believed to produce and nothing ever
// checked it. It had already drifted in two places the compiler fills in by
// itself: `execution/timeout`, which is Required(), so tcc would have refused
// the very project this test claimed to be running, and `implements`, which the
// vocabulary defaults to `handler`. The yaml under projects/with/storage/object
// is compiled here, so a bucket's declaration and a bucket's behaviour are one
// thing.

// TestAnObjectWrittenOnOneNodeIsReadableOnAnother.
//
// TWO NODES, because a swarm with one node in it is a swarm nothing has to
// cross. THE COMPONENT IS PUT INTO ONE ASSET STORE ONLY — deployFleet stages it
// on the node it publishes from — so the second node fetches the bytes it runs
// through the DAG as well as the bytes it serves.
//
// THE HOST IS INERT. deployFleet names one because a register keeps its
// pointers by fqdn, but every request below is put to a worker directly: the
// project declares no zone and no gateway, exactly as the object it replaces
// did.
func TestAnObjectCrossesTheSwarm(t *testing.T) {
	defer nodeDream.Runtimes(nodeDream.Runtimes()...)

	cloud := deployFleet(t, 2, "with/storage/object", "receipts.example.com", cacherWASM(t))
	first, second := cloud[0], cloud[1]

	// WRITTEN THROUGH THE FIRST NODE'S GUEST. A put answers the ETAG it stored
	// under, which is S3's contract and is this cloud's content id.
	//
	// NOT RETRIED, and that is a property of the kind rather than an omission.
	// A bucket's index is the node's own kvdb (node/storage.go:166), which is
	// crdt and has no group to form and nobody to elect — so unlike the queue's
	// first send there is no `unavailable` arm here for a caller to act on, and
	// an Eventually would be waiting for something that cannot happen.
	// deployFleet has already waited for every node to hold the project.
	res, err := first.onCarrying(t.Context(), target(), http.MethodPost, "/obj-put/receipts/2026/01", "a receipt")
	require.NoError(t, err)
	require.Equal(t, 200, res.Status, string(res.Body))
	etag := string(res.Body)
	require.NotEmpty(t, etag)

	// AND READ THROUGH THE SECOND'S, which means the compiled project reached
	// it, its own tier started the component, its own kvdb had the row and its
	// own DAG had the bytes.
	require.Eventually(t, func() bool {
		res, err := second.onCarrying(t.Context(), target(), http.MethodPost, "/obj-get/receipts/2026/01", "")
		return err == nil && res.Status == 200 && string(res.Body) == "a receipt"
	}, 60*time.Second, 500*time.Millisecond, "an object never crossed the swarm")

	// AND `head` ANSWERS THERE TOO, off the row rather than the bytes — the
	// record survives the ABI in a universe as well as in a unit test.
	//
	// THE ETAG IS THE ONE THE OTHER NODE ANSWERED WITH, which is the claim the
	// whole content-addressed story rests on: the second node computed nothing
	// and looked nothing up by name — it read the row that replicated, and the
	// row's content id IS what the first node wrote.
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
