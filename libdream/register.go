package libdream

import (
	peer "github.com/taubyte/p2p/peer"
	"github.com/taubyte/tau/pkgs/kvdb"
)

func (u *Universe) Register(node peer.Node, name string, ports map[string]int) {
	u.lock.Lock()
	defer u.lock.Unlock()
	u.lookups[node.ID().Pretty()] = &NodeInfo{
		DbFactory: kvdb.New(node),
		Node:      node,
		Name:      name,
		Ports:     ports,
	}
}
