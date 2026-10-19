package secrets

import (
	"context"

	logging "github.com/ipfs/go-log/v2"

	"github.com/taubyte/tau/p2p/rpc"
)

// auditLog records who asked this node for what, and how it answered.
//
// It is a separate logger so the trail can be routed and retained on its own
// terms rather than being mixed into service diagnostics.
var auditLog = logging.Logger("tau.secrets.audit")

// audit writes one line per operation, naming the caller the transport
// authenticated. A call made in this process names no peer and is "local".
//
// The trail is not tamper-evident -- a node that is compromised can rewrite its
// own log. Making it so means chaining entries, which is a later step, not a
// different design.
func audit(ctx context.Context, verb, ref string, err error) {
	caller := "local"
	if id, perr := rpc.RemotePeer(ctx); perr == nil {
		caller = id.String()
	}

	if err != nil {
		auditLog.Infof("caller=%s verb=%s ref=%q outcome=denied error=%q", caller, verb, ref, err)
		return
	}

	auditLog.Infof("caller=%s verb=%s ref=%q outcome=ok", caller, verb, ref)
}
