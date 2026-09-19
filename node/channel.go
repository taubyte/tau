package node

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/taubyte/tau/pkg/invoke"
	wasmtier "github.com/taubyte/tau/pkg/invoke/wasm"
	"github.com/taubyte/tau/pkg/specs"
	"github.com/taubyte/tau/services/v2/placement"
	"github.com/taubyte/tau/services/worker"
)

// channel.go backs `taubyte:channel/dialer` on this node — the vocabulary's
// `channel` kind, which is what v1 called p2p.
//
// # A dial is an invocation, and that is the whole implementation
//
// A channel is a named request/response conduit to one other workload in the
// same project. v2 already moves exactly that between nodes: the worker's own
// `/tau/worker/v1`, connect-go over a libp2p stream, carrying
// `(address, request) -> response`. So a dial resolves the address the channel
// names and invokes it. There is no second protocol, no second discovery and no
// second timeout.
//
// # Why not a protocol per channel, which is what v1 did
//
// v1 registered a libp2p protocol id per resource — `hash(project+id)+name` —
// advertised it on the DHT, and fanned a dial out to whichever peers claimed to
// answer it, letting the receiver decide whether it actually did. That is a
// router built out of advertisements, and it was the only one available.
//
// v2 has a real one. A node announces the deployments it holds and every
// locator reads that same view, so the serving workload is ADDRESSED rather
// than advertised: one hop, to a node that certainly holds the project, chosen
// by the same placement the edge uses. It also makes the vocabulary's tenancy
// promise structural instead of lexical — "what goes on the wire has the
// project's identity prepended" was there so two tenants naming a channel
// `sync` never meet, and two deployments are different addresses whatever
// anybody named their channel.
//
// WHAT THE PROTOCOL NAME IS FOR, THEN. It travels as a header, so a workload
// serving several channels can tell which one it was reached on. It is the
// author's word for the conduit and it is no longer load-bearing for isolation,
// which is a promise moving to a stronger mechanism rather than one being
// dropped.
//
// # What this does not do
//
// It is request and response, which is what `taubyte:channel/dialer` declares
// and what almost every use of v1's p2p actually was. A genuine conduit — both
// directions, held open per caller — is a WIT `resource` the ABI deliberately
// does not have yet, and it is not blocked by anything here: `dial` reads the
// same when a `connect` lands beside it.

// headerChannel is the author's own name for the conduit, so a workload serving
// more than one can tell them apart. headerDialer is the channel's label as the
// DIALING workload wrote it, which is a different string and useful in a log.
const (
	headerChannel = "X-Taubyte-Channel"
	headerDialer  = "X-Taubyte-Channel-Label"
)

// What the composer wrote onto a channel grant, spelled as the vocabulary
// spells the attributes — except the scope, which travels RESOLVED: the
// outward walk an omitted namespace means happens where the compiled project
// is open, and what arrives here is the chain it produced.
const (
	settingProtocol   = "protocol"
	settingServeKind  = "serve/kind"
	settingServeName  = "serve/name"
	settingServeScope = "serve/namespace"
)

// channels dials the workload a channel grant names.
type channels struct{ node *Node }

// Dial invokes whatever serves the channel and answers with what came back.
//
// IT IS BOUNDED BY ctx AND NOTHING ELSE. The context carries the CALLING
// workload's own deadline: a channel declares none of its own, and the budget
// being spent belongs to the workload that is waiting.
func (c *channels) Dial(ctx context.Context, grant invoke.Grant, body []byte) ([]byte, error) {
	kind := grant.Settings[settingServeKind]
	name := grant.Settings[settingServeName]
	if kind == "" || name == "" {
		return nil, fmt.Errorf("%w: %s names nothing that serves it", wasmtier.ErrNoAnswer, grant.Label)
	}

	proj, environment, _ := splitHandle(grant.Handle)
	if proj == "" || environment == "" {
		return nil, fmt.Errorf("node: the handle %q does not name a deployment", grant.Handle)
	}

	res, err := c.invoke(ctx, worker.Target{
		Project:     proj,
		Environment: environment,
		Scope:       splitNamespace(grant.Settings[settingServeScope]),
		Kind:        kind,
		Name:        name,
	}, &invoke.Request{
		Method: http.MethodPost,
		Path:   "/",
		Headers: map[string][]string{
			headerChannel: {grant.Settings[settingProtocol]},
			headerDialer:  {grant.Label},
		},
		Body: body,
	})
	if err != nil {
		// NOT REACHED AND REFUSED ARE DIFFERENT ANSWERS, and the guest acts on
		// them differently: a caller RETRIES a channel whose far end is not up
		// yet, which on a cold deploy is a matter of seconds.
		if unanswered(err) {
			return nil, fmt.Errorf("%w: nothing in %s/%s serves %s/%s", wasmtier.ErrNoAnswer, proj, environment, kind, name)
		}
		return nil, fmt.Errorf("node: dialling %s failed: %w", grant.Label, err)
	}

	// THE FAR END'S OWN FAILURE IS ITS OWN ARM. A workload that answered with a
	// 500 was reached, and a caller fixes that rather than retrying it.
	if res.Status < 200 || res.Status > 299 {
		return nil, fmt.Errorf("%w: %s answered %d", wasmtier.ErrChannelFailed, grant.Label, res.Status)
	}
	return res.Body, nil
}

// invoke sends through placement when this node can route, and through the
// local worker when it cannot.
//
// PLACEMENT IS THE ONE THAT CAN LEAVE THIS MACHINE, and it already calls a
// local worker directly rather than opening a stream to itself — so where it
// exists it is strictly the better of the two. It does not exist on a node that
// runs no stats, and there the local worker is the only workload reachable at
// all: a dial that lands is better than one that cannot be attempted, and one
// that does not land says so.
func (c *channels) invoke(ctx context.Context, at specs.Address, req *invoke.Request) (*invoke.Response, error) {
	if c.node.workers != nil {
		return c.node.workers.Invoke(ctx, at, req)
	}
	if c.node.worker != nil {
		return c.node.worker.Invoke(ctx, at, req)
	}
	return nil, placement.ErrNoWorkers
}

// unanswered is "the far end is not there", as against "the far end said no".
func unanswered(err error) bool {
	return errors.Is(err, placement.ErrNoWorkers) || errors.Is(err, worker.ErrNoTarget)
}
