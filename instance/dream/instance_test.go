//go:build dreaming

package dream_test

import (
	"testing"

	storageService "github.com/taubyte/tau/services/storage"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/instance"
	nodeDream "github.com/taubyte/tau/instance/dream"
	"github.com/taubyte/tau/pkg/invoke"
)

// The `instances:` block end to end (projects/with/cache/instances): a guest naming
// one of many, a real validator function deciding, and two instances isolated from
// each other.

// instancesAt builds with/cache/instances in a universe of one node and is that node.
func instancesAt(t *testing.T) *instance.Instance {
	t.Helper()
	u, err := nodeDream.Start(t.Context(), t.Name(), nodeDream.Config{
		Services: []string{instance.Register, instance.Worker, instance.Discovery, storageService.Descriptor.Name, instance.Runner},
	})
	require.NoError(t, err)
	t.Cleanup(u.Close)
	pids := u.Pids()
	publish(t, u, pids[:1], deployed{project: "shop", name: "with/cache/instances"}, artifacts{"": cacherWASM(t), "may_open": admitterWASM(t)})
	return nodeOf(t, pids[0])
}

func TestAnInstanceIsAdmittedByARealFunction(t *testing.T) {
	defer nodeDream.Runtimes(nodeDream.Runtimes()...)
	n := instancesAt(t)

	// ONE INSTANCE, ADMITTED AND WRITTEN TO.
	res, err := n.Worker().Invoke(t.Context(), target(), &invoke.Request{
		Method: "POST", Path: "/put/rooms~general/greeting", Body: []byte("hello"),
	})
	require.NoError(t, err)
	require.Equal(t, 204, res.Status, string(res.Body))

	res, err = n.Worker().Invoke(t.Context(), target(), &invoke.Request{
		Method: "POST", Path: "/get/rooms~general/greeting",
	})
	require.NoError(t, err)
	require.Equal(t, 200, res.Status, string(res.Body))
	require.Equal(t, "hello", string(res.Body))

	// AND A SECOND INSTANCE IS A SECOND STORE. The handle carries the instance,
	// so the same key in another room is a miss rather than somebody else's
	// value.
	res, err = n.Worker().Invoke(t.Context(), target(), &invoke.Request{
		Method: "POST", Path: "/get/rooms~lobby/greeting",
	})
	require.NoError(t, err)
	require.Equal(t, 404, res.Status)
	require.Equal(t, "not-found", string(res.Body))

	// THE VALIDATOR'S NO IS THE ANSWER, and it now carries the validator's own
	// words rather than an HTTP status the node had to interpret. The fixture
	// refuses an instance called `denied`; the guest is told it was not admitted
	// rather than that the cache is down.
	res, err = n.Worker().Invoke(t.Context(), target(), &invoke.Request{
		Method: "POST", Path: "/get/rooms~denied/k",
	})
	require.NoError(t, err)
	require.Equal(t, 404, res.Status)
	require.Contains(t, string(res.Body), "denied:")
	require.Contains(t, string(res.Body), "not admitted")

	// AND ADDRESSING THE STORE ITSELF IS REFUSED, because serving it would be
	// one store where the config says many, admitted by nobody.
	res, err = n.Worker().Invoke(t.Context(), target(), &invoke.Request{
		Method: "POST", Path: "/get/rooms/k",
	})
	require.NoError(t, err)
	require.Contains(t, string(res.Body), "holds many instances")
}

func TestTheInstanceLimitHoldsEndToEnd(t *testing.T) {
	defer nodeDream.Runtimes(nodeDream.Runtimes()...)
	n := instancesAt(t)

	for _, room := range []string{"one", "two", "three"} {
		res, err := n.Worker().Invoke(t.Context(), target(), &invoke.Request{
			Method: "POST", Path: "/put/rooms~" + room + "/k", Body: []byte("x"),
		})
		require.NoError(t, err)
		require.Equal(t, 204, res.Status, "%s: %s", room, res.Body)
	}

	res, err := n.Worker().Invoke(t.Context(), target(), &invoke.Request{
		Method: "POST", Path: "/put/rooms~four/k", Body: []byte("x"),
	})
	require.NoError(t, err)
	require.Equal(t, 403, res.Status)
	require.Contains(t, string(res.Body), "limit is 3")

	// AND A ROOM ALREADY ADMITTED STILL SERVES. A full store is not a broken
	// one.
	res, err = n.Worker().Invoke(t.Context(), target(), &invoke.Request{
		Method: "POST", Path: "/get/rooms~one/k",
	})
	require.NoError(t, err)
	require.Equal(t, 200, res.Status, string(res.Body))
}
