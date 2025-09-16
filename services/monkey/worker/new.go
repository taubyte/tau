package worker

import (
	"context"

	hoarderIface "github.com/taubyte/tau/core/services/hoarder"
	patrickIface "github.com/taubyte/tau/core/services/patrick"

	"github.com/taubyte/tau/clients/p2p/tns"
	"github.com/taubyte/tau/p2p/peer"
)

type Node interface {
	peer.Node
	TNS() tns.Client
	Patrick() patrickIface.Client
	Hoarder() hoarderIface.Client
}

func New(ctx context.Context, node Node, job *patrickIface.Job) (Worker, error) {
	return nil, nil
}
