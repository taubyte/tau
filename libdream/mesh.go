package libdream

import (
	"context"
	"sync"

	peercore "github.com/libp2p/go-libp2p/core/peer"
	peer "github.com/taubyte/p2p/peer"
	"github.com/taubyte/tau/libdream/common"
)

func (u *Universe) Mesh(newNodes ...peer.Node) {
	ctx, ctxC := context.WithTimeout(u.ctx, common.MeshTimeout)
	defer ctxC()

	u.lock.RLock()
	var wg sync.WaitGroup
	for _, n0 := range newNodes {
		for _, n1 := range u.all {
			if n0 != n1 {
				wg.Add(1)
				go func(n0, n1 peer.Node) {
					n0.Peer().Connect(
						ctx,
						peercore.AddrInfo{
							ID:    n1.ID(),
							Addrs: n1.Peer().Addrs(),
						},
					)
					wg.Done()
				}(n0, n1)
			}
		}
	}
	wg.Wait()
	u.lock.RUnlock()

	u.lock.Lock()
	u.all = append(u.all, newNodes...)
	u.lock.Unlock()
}
