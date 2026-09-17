package wasm_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/invoke/wasm"
)

// channel_test drives `taubyte:channel/dialer` — in-project addressing, which
// the vocabulary renamed from v1's `p2p` because that named the transport
// rather than the thing.

// dialed is a Channels that records what it was asked and answers with what it
// was told to.
type dialed struct {
	mu      sync.Mutex
	handles []string
	bodies  [][]byte

	answer []byte
	err    error
}

func (d *dialed) Dial(_ context.Context, grant invoke.Grant, body []byte) ([]byte, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.handles = append(d.handles, grant.Handle)
	d.bodies = append(d.bodies, body)
	if d.err != nil {
		return nil, d.err
	}
	return d.answer, nil
}

func (d *dialed) seen() ([]string, [][]byte) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.handles...), append([][]byte(nil), d.bodies...)
}

func channelGrant(label, handle string, ops ...string) []invoke.Grant {
	return []invoke.Grant{{Kind: "channel", Label: label, Handle: handle, Ops: ops}}
}

// TestAGuestDialsAndIsAnswered — request and response over a channel, with the
// bytes crossing in both directions.
func TestAComponentDialsAChannel(t *testing.T) {
	far := &dialed{answer: []byte("pong")}
	instance := start(t, tier(t, cacher(t), wasm.WithChannels(far)), invoke.Spec{
		Name:    "fn",
		Reaches: channelGrant("sync", "shop/prod/QmSync", "dial"),
	})

	res := call(t, instance, "/dial/sync/", []byte("ping"))
	require.Equal(t, 200, res.Status, string(res.Body))
	require.Equal(t, "pong", string(res.Body))

	// THE LABEL RESOLVED TO THE HANDLE, which is what keeps one component
	// portable between environments.
	handles, bodies := far.seen()
	require.Equal(t, []string{"shop/prod/QmSync"}, handles)
	require.Equal(t, [][]byte{[]byte("ping")}, bodies)
}

// TestNOTHINGISSERVINGItIsItsOwnArm, because a caller RETRIES that and fixes
// everything else — on a cold deploy the far end is seconds away.
func TestAChannelNobodyAnswersIsRetryable(t *testing.T) {
	far := &dialed{err: wasm.ErrNoAnswer}
	instance := start(t, tier(t, cacher(t), wasm.WithChannels(far)), invoke.Spec{
		Name:    "fn",
		Reaches: channelGrant("sync", "h", "dial"),
	})

	res := call(t, instance, "/dial/sync/", []byte("ping"))
	require.Equal(t, 403, res.Status)
	require.True(t, strings.HasPrefix(string(res.Body), "no-answer:"), "got %q", res.Body)
}

// TestTHEFARENDSAYINGNoIsNotAnOutage, which is the other half of the same
// distinction.
func TestAChannelThatRefusesIsNotUnavailable(t *testing.T) {
	far := &dialed{err: fmt.Errorf("the far end said: %w", wasm.ErrChannelFailed)}
	instance := start(t, tier(t, cacher(t), wasm.WithChannels(far)), invoke.Spec{
		Name:    "fn",
		Reaches: channelGrant("sync", "h", "dial"),
	})

	res := call(t, instance, "/dial/sync/", nil)
	require.True(t, strings.HasPrefix(string(res.Body), "failed:"), "got %q", res.Body)
}

// ── the grant is the authorization ───────────────────────────────────────────

func TestAnUndeclaredChannelIsRefusedByName(t *testing.T) {
	far := &dialed{}
	instance := start(t, tier(t, cacher(t), wasm.WithChannels(far)), invoke.Spec{
		Name:    "fn",
		Reaches: channelGrant("sync", "h", "dial"),
	})

	res := call(t, instance, "/dial/somebody-elses/", nil)
	require.Equal(t, 403, res.Status)
	require.Equal(t, "no-such-channel:somebody-elses", string(res.Body))

	handles, _ := far.seen()
	require.Empty(t, handles, "and nothing was dialled to find that out")
}

// TestDIALIsAWriteAndTheOpIsChecked. The vocabulary classifies it as one
// because what travels is opaque, so a grant without it may not dial.
func TestAChannelGrantWithoutDialCannot(t *testing.T) {
	far := &dialed{}
	instance := start(t, tier(t, cacher(t), wasm.WithChannels(far)), invoke.Spec{
		Name:    "fn",
		Reaches: channelGrant("sync", "h"),
	})

	res := call(t, instance, "/dial/sync/", nil)
	require.Equal(t, 403, res.Status)
	require.Equal(t, "denied:sync cannot dial", string(res.Body))

	handles, _ := far.seen()
	require.Empty(t, handles)
}

// TestANodeWithNoTransportRefusesTheWorkload, which is the shape every
// capability keeps: a grant with nothing behind it is a composition fault
// rather than a call that fails later.
func TestAChannelGrantNeedsATransport(t *testing.T) {
	_, err := tier(t, cacher(t)).Start(t.Context(), invoke.Spec{
		Name: "fn", Artifact: "bafy",
		Reaches: channelGrant("sync", "h", "dial"),
	})
	require.ErrorIs(t, err, wasm.ErrNoChannels)
}

// TestTHEIMPORTIsRegisteredEvenWithNoGrant. An unprovided component import is a
// TRAP that takes the instance with it.
func TestTheChannelImportExistsWithoutAGrant(t *testing.T) {
	instance := start(t, tier(t, cacher(t)), invoke.Spec{Name: "fn"})

	res := call(t, instance, "/dial/anything/", nil)
	require.Equal(t, 403, res.Status)
	require.Equal(t, "no-such-channel:anything", string(res.Body))
}
