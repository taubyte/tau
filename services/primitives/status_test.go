package primitives

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"

	"connectrpc.com/connect"
	mocknet "github.com/libp2p/go-libp2p/p2p/net/mock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/p2p/rpc"
	wasmtier "github.com/taubyte/tau/pkg/invoke/wasm"
	primitivesv2 "github.com/taubyte/tau/pkg/proto/gen/tau/primitives/v2"
	"github.com/taubyte/tau/pkg/proto/gen/tau/primitives/v2/primitivesv2connect"
)

// Every refusal travels a real stream, since an in-process encode-decode would find
// the sentinel still in the chain.

// refusingService answers every QueueSend with whatever refusal the case is
// about, encoded the way a handler encodes one.
type refusingService struct {
	primitivesv2connect.UnimplementedPrimitivesServiceHandler

	mu  sync.Mutex
	err error
}

func (r *refusingService) refuse(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.err = err
}

func (r *refusingService) QueueSend(context.Context, *connect.Request[primitivesv2.QueueSendRequest]) (*connect.Response[primitivesv2.Empty], error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return nil, wire(r.err)
}

// refusing is a node that serves nothing but refusals, and a real client onto
// it. No store, no consensus and no declarations: what is under test is the
// table, and a service that had to resolve a resource first could only ever be
// made to produce a handful of the rows.
func refusing(t *testing.T) (*Client, *refusingService) {
	t.Helper()

	mn := mocknet.New()
	t.Cleanup(func() { mn.Close() })

	server, err := mn.GenPeer()
	require.NoError(t, err)
	caller, err := mn.GenPeer()
	require.NoError(t, err)
	require.NoError(t, mn.LinkAll())
	require.NoError(t, mn.ConnectAllButSelf())

	svc := &refusingService{}
	mux := http.NewServeMux()
	mux.Handle(primitivesv2connect.NewPrimitivesServiceHandler(svc))

	listener, err := rpc.Listen(t.Context(), server, Protocol, mux)
	require.NoError(t, err)
	t.Cleanup(func() { listener.Close() })

	client, err := NewClient(caller, server.ID())
	require.NoError(t, err)
	t.Cleanup(func() { client.Close() })

	return client, svc
}

func TestEveryRefusalReachesARemoteCallerAsItReachesALocalOne(t *testing.T) {
	client, svc := refusing(t)
	grant := grantFor("QmSomeQueueTheHandlerNeverLooksAt")

	for _, row := range refusals {
		t.Run(row.err.Error(), func(t *testing.T) {
			svc.refuse(row.err)

			local := tierError(row.err)
			remote := client.Send(t.Context(), grant, "1", []byte("x"))
			require.Error(t, remote, "the handler answered a refusal with success")

			// THE ARM IS THE SAME ONE, which is the half a guest sees.
			assert.Equal(t, armIn(local), armIn(remote),
				"the two arms answer a guest differently")

			// AND THE STATUS SURVIVES, which is the half a caller between the
			// guest and the service decides on.
			assert.Equal(t, row.code, connect.CodeOf(remote))

			// AND THE NAME SURVIVES WHENEVER THE STATUS CARRIES ONE. A status
			// two rows share names neither, and the table is what says which is
			// which — so this assertion reads the table rather than repeating
			// its contents.
			if named, lone := restores[row.code]; lone {
				assert.ErrorIs(t, remote, named)
				assert.Same(t, row.err, named,
					"a status only this row claims came back as another row's name")
			} else {
				assert.NotErrorIs(t, remote, row.err,
					"a name the wire cannot carry was restored anyway")
			}
		})
	}
}

// armIn is the tier arm in a chain, or nil. It is written out rather than read
// off the table so that the assertion above is not checking the table against
// itself.
func armIn(err error) error {
	for _, arm := range []error{
		wasmtier.ErrNotWritable,
		wasmtier.ErrRecordTooLarge,
		wasmtier.ErrBadCursor,
		wasmtier.ErrNotFound,
	} {
		if errors.Is(err, arm) {
			return arm
		}
	}
	return nil
}

func TestAnUnnamedFailureIsUnavailableAndNotInternal(t *testing.T) {
	client, svc := refusing(t)
	svc.refuse(errors.New("the store fell over"))

	err := client.Send(t.Context(), grantFor("QmWhatever"), "1", []byte("x"))
	require.Error(t, err)
	assert.Equal(t, connect.CodeUnavailable, connect.CodeOf(err))
	assert.Nil(t, armIn(err), "an unnamed failure was given a guest's variant")
}

func TestATaskingKeepsTheNameAndNotTheGuestsArm(t *testing.T) {
	refused := taskError(wire(NotLeading{Leader: "12D3KooWSomebodyElse"}))

	require.ErrorIs(t, refused, ErrNotLeading)
	assert.Equal(t, connect.CodeAborted, connect.CodeOf(refused))
	assert.Nil(t, armIn(refused), "a tasking was handed a variant meant for a guest")
}
