package peer

import (
	"context"
	"errors"
	"time"

	peer "github.com/libp2p/go-libp2p/core/peer"
	ping "github.com/libp2p/go-libp2p/p2p/protocol/ping"
)

var PingTimeout = time.Second * 4

func (p *node) Ping(pid string, count int) (healthy int, rtt time.Duration, err error) {
	if !p.closed {
		if count <= 0 {
			return 0, 0, errors.New("ping count must be positive")
		}

		var _pid peer.ID
		_pid, err = peer.Decode(pid)
		if err != nil {
			return
		}

		ps := ping.NewPingService(p.host)

		pctx, cancel := context.WithCancel(p.ctx)
		defer cancel()

		var sum time.Duration = 0
		for i := 0; i < count; i++ {
			ts := ps.Ping(pctx, _pid)
			select {
			case res := <-ts:
				if res.Error != nil {
					err = res.Error
				}
				sum += res.RTT
				healthy++
			case <-time.After(PingTimeout):
				err = errors.New("took too long to ping")
			}
		}

		// erase errors if at least one is healthy
		if healthy > 0 {
			err = nil
			rtt = sum / time.Duration(healthy)
		}

		return
	}

	err = errorClosed
	return
}
