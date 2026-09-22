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
	primitivesv1 "github.com/taubyte/tau/pkg/proto/gen/tau/primitives/v1"
	"github.com/taubyte/tau/pkg/proto/gen/tau/primitives/v1/primitivesv1connect"
)

// status_test is the one check that replaces per-method vigilance.
//
// Every refusal this service names travels a REAL stream here, because the
// thing being tested is exactly what a wire throws away: connect.NewError wraps
// the sentinel in this process, so an encode-then-decode that never left would
// find the name still in the chain and pass for the wrong reason.

// refusingService answers every QueueSend with whatever refusal the case is
// about, encoded the way a handler encodes one.
type refusingService struct {
	primitivesv1connect.UnimplementedPrimitivesServiceHandler

	mu  sync.Mutex
	err error
}

func (r *refusingService) refuse(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.err = err
}

func (r *refusingService) QueueSend(context.Context, *connect.Request[primitivesv1.QueueSendRequest]) (*connect.Response[primitivesv1.Empty], error) {
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
	mux.Handle(primitivesv1connect.NewPrimitivesServiceHandler(svc))

	listener, err := rpc.Listen(t.Context(), server, Protocol, mux)
	require.NoError(t, err)
	t.Cleanup(func() { listener.Close() })

	client, err := NewClient(caller, server.ID())
	require.NoError(t, err)
	t.Cleanup(func() { client.Close() })

	return client, svc
}

// TestEVERYREFUSALREACHESAREMOTECALLERASITREACHESALOCALONE.
//
// This is the property the short-circuit rests on. p2p/rpc hands a caller this
// node's own service or a stream onto another node's and nothing above this
// package can tell which it got — so for every row of the table, what a caller
// can DECIDE on has to be the same either way: the tier's arm a guest's ABI
// has a variant for, the connect status a dispatcher filters on, and the
// service's own name where the status carries one.
//
// node/dispatch.go is the caller that made this necessary: it filtered ordinary
// refusals on ErrNotLeading, which only the local arm produced.
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

// TestANUNNAMEDFAILUREISUNAVAILABLEANDNOTINTERNAL, which is the default the
// table states and the one thing no row can assert.
func TestAnUnnamedFailureIsUnavailableAndNotInternal(t *testing.T) {
	client, svc := refusing(t)
	svc.refuse(errors.New("the store fell over"))

	err := client.Send(t.Context(), grantFor("QmWhatever"), "1", []byte("x"))
	require.Error(t, err)
	assert.Equal(t, connect.CodeUnavailable, connect.CodeOf(err))
	assert.Nil(t, armIn(err), "an unnamed failure was given a guest's variant")
}

// TestATASKINGKEEPSTHENAMEANDNOTTHEGUESTSARM. A window has no guest behind it,
// so the clock's decode is the table's backwards read without the arm — and the
// name it needs is exactly the one a status names alone.
func TestATaskingKeepsTheNameAndNotTheGuestsArm(t *testing.T) {
	refused := taskError(wire(NotLeading{Leader: "12D3KooWSomebodyElse"}))

	require.ErrorIs(t, refused, ErrNotLeading)
	assert.Equal(t, connect.CodeAborted, connect.CodeOf(refused))
	assert.Nil(t, armIn(refused), "a tasking was handed a variant meant for a guest")
}
