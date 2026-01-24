package peer

import (
	"context"
	"fmt"
	"time"

	peer "github.com/libp2p/go-libp2p/core/peer"
	ping "github.com/libp2p/go-libp2p/p2p/protocol/ping"
)

var PingTimeout = time.Second * 4

func (p *node) Ping(ctx context.Context, pid string, count int) (healthy int, rtt time.Duration, err error) {
	if p.closed.Load() {
		return 0, 0, errorClosed
	}

	if count <= 0 {
		return 0, 0, fmt.Errorf("ping count must be positive, got %d", count)
	}

	var _pid peer.ID
	_pid, err = peer.Decode(pid)
	if err != nil {
		return 0, 0, fmt.Errorf("decoding peer ID %q failed: %w", pid, err)
	}

	ps := ping.NewPingService(p.host)

	pctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var sum time.Duration = 0
	for i := 0; i < count; i++ {
		ts := ps.Ping(pctx, _pid)
		select {
		case res := <-ts:
			if res.Error != nil {
				err = fmt.Errorf("ping %d/%d to %s failed: %w", i+1, count, pid, res.Error)
			}
			sum += res.RTT
			healthy++
		case <-time.After(PingTimeout):
			err = fmt.Errorf("ping %d/%d to %s timed out after %v", i+1, count, pid, PingTimeout)
		case <-ctx.Done():
			err = fmt.Errorf("ping to %s canceled: %w", pid, ctx.Err())
			return
		}
	}

	// erase errors if at least one is healthy
	if healthy > 0 {
		err = nil
		rtt = sum / time.Duration(healthy)
	}

	return
}
